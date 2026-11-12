package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/localswim/localswim/internal/client"
)

func parseT06(name string, args []string) (cliCommand, error) {
	cmd := cliCommand{method: http.MethodGet, t06Action: name}
	flags := flag.NewFlagSet("localswim", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var revision, backupID string
	switch name {
	case "card set-priorities":
		flags.StringVar(&revision, "revision", "", "expected board revision")
		flags.BoolVar(&cmd.force, "force", false, "confirm all unfinished cards without prompting")
		flags.BoolVar(&cmd.force, "f", false, "confirm all unfinished cards without prompting")
	case "backup create":
		flags.StringVar(&revision, "revision", "", "expected board revision")
	case "ticket-order preview", "backup info", "backup download":
	default:
		return cmd, errors.New("unknown command")
	}
	if name == "backup create" {
		flags.StringVar(&backupID, "id", "", "known backup UUIDv7")
	}
	if err := flags.Parse(args); err != nil {
		return cmd, err
	}
	count := 1
	if name == "backup info" || name == "ticket-order preview" {
		count = 2
	}
	if name == "backup download" {
		count = 3
	}
	if name == "card set-priorities" {
		count = 2
		if flags.NArg() < count {
			return cmd, errors.New("card set-priorities requires BOARD_ID PRIORITY_ID")
		}
	}
	variadic := name == "ticket-order preview" || name == "card set-priorities"
	if flags.NArg() < count || (!variadic && flags.NArg() != count) || !boardIDPattern.MatchString(flags.Arg(0)) {
		return cmd, errors.New("expected BOARD_ID and the documented resource arguments")
	}
	if (name == "backup info" || name == "backup download") && !operationIDPattern.MatchString(flags.Arg(1)) {
		return cmd, errors.New("expected a UUIDv7 backup ID after BOARD_ID")
	}
	if name == "backup download" && flags.Arg(2) == "" {
		return cmd, errors.New("backup download requires FILE or -")
	}
	var optionErr error
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "revision":
			var err error
			cmd.revision, err = parseCLIRevision(revision)
			if err != nil {
				optionErr = err
			}
		case "id":
			if !operationIDPattern.MatchString(backupID) {
				optionErr = errors.New("--id requires a UUIDv7 backup ID")
			}
		}
	})
	if optionErr != nil {
		return cmd, optionErr
	}
	id := strings.ToLower(flags.Arg(0))
	switch name {
	case "card set-priorities":
		cmd.method, cmd.resource, cmd.boardID = http.MethodPatch, "/cards", id
		var err error
		cmd.cardSelectors, cmd.body, err = parsePriorityBatch(cmd.boardID, flags.Arg(1), flags.Args()[2:])
		if err != nil {
			return cmd, err
		}
	case "backup create":
		cmd.method, cmd.resource, cmd.boardID = http.MethodPost, "/backups", id
		if backupID == "" {
			backupID = newOperationID()
		}
		cmd.body, _ = json.Marshal(map[string]string{"id": strings.ToLower(backupID)})
	case "backup info", "backup download":
		cmd.boardID = id
		cmd.resource = "/backups/" + strings.ToLower(flags.Arg(1))
		if name == "backup download" {
			cmd.downloadPath = flags.Arg(2)
		}
	case "ticket-order preview":
		if flags.NArg() > 1002 || !boardIDPattern.MatchString(flags.Arg(1)) {
			return cmd, errors.New("preview requires BOARD_ID LANE_ID and at most 1000 card selectors")
		}
		cmd.boardID = id
		cmd.resource = "/ticket-orders/" + strings.ToLower(flags.Arg(1)) + "?preview=true"
		for _, reference := range flags.Args()[2:] {
			if _, err := parseCardTarget(id, reference); err != nil {
				return cmd, err
			}
			cmd.cardSelectors = append(cmd.cardSelectors, reference)
		}
	}
	return cmd, nil
}

func executeT06(api *client.Client, cmd cliCommand, stdout io.Writer) error {
	var previewBoardTag string
	if cmd.t06Action == "backup download" {
		return downloadBackup(api, cmd.boardID, cmd.resource, cmd.downloadPath, stdout)
	}
	if cmd.t06Action == "ticket-order preview" {
		laneID := strings.TrimSuffix(strings.TrimPrefix(cmd.resource, "/ticket-orders/"), "?preview=true")
		lane, err := api.Do(http.MethodGet, "/lanes/"+laneID, nil, "")
		if err != nil {
			return err
		}
		if err := validateBoardResource(lane.JSON, cmd.boardID, laneID); err != nil {
			return err
		}
		query := url.Values{"preview": {"true"}}
		seen := make(map[string]bool, len(cmd.cardSelectors))
		for _, reference := range cmd.cardSelectors {
			selector, err := parseCardTarget(cmd.boardID, reference)
			if err != nil {
				return err
			}
			target, err := resolveCardTarget(api, selector)
			if err != nil {
				return err
			}
			if seen[target.CardID] || (previewBoardTag != "" && previewBoardTag != target.BoardETag) {
				return errors.New("preview requires distinct cards from the same confirmed board revision")
			}
			seen[target.CardID], previewBoardTag = true, target.BoardETag
			query.Add("card_id", target.CardID)
		}
		cmd.resource = "/ticket-orders/" + laneID + "?" + query.Encode()
	}
	body := cmd.body
	if cmd.boardID != "" && cmd.method != http.MethodGet {
		fields := make(map[string]json.RawMessage)
		if body != nil {
			var err error
			fields, err = cliInputFields(body)
			if err != nil {
				return err
			}
		}
		if fields["board_id"] != nil || fields["expected_board_etag"] != nil {
			return errors.New("CLI supplies board_id and expected_board_etag")
		}
		boardTag, err := t06BoardValidator(api, cmd, fields)
		if err != nil {
			return err
		}
		fields["board_id"], _ = json.Marshal(cmd.boardID)
		fields["expected_board_etag"], _ = json.Marshal(boardTag)
		body, err = json.Marshal(fields)
		if err != nil || len(body) > maxCLIInput {
			return errors.New("request exceeds 96 MiB after adding resource validators")
		}
	}
	reply, err := api.Do(cmd.method, cmd.resource, body, "")
	if err != nil {
		if cmd.t06Action == "backup create" && errors.Is(err, client.ErrMutationOutcomeUnknown) {
			var identity struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(body, &identity)
			return fmt.Errorf("%w. Run backup info %s %s before retrying", err, cmd.boardID, identity.ID)
		}
		return err
	}
	if cmd.t06Action == "backup info" {
		if err := validateBoardResource(reply.JSON, cmd.boardID, strings.TrimPrefix(cmd.resource, "/backups/")); err != nil {
			return err
		}
	}
	if cmd.t06Action == "backup create" {
		var requested, returned struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &requested)
		_ = json.Unmarshal(reply.JSON, &returned)
		if requested.ID != returned.ID || validateBoardResource(reply.JSON, cmd.boardID, requested.ID) != nil {
			return fmt.Errorf("%w. Service returned a different backup identity. Run backup info %s %s before retrying", client.ErrMutationOutcomeUnknown, cmd.boardID, requested.ID)
		}
	}
	if cmd.t06Action == "ticket-order preview" {
		if previewBoardTag != "" && reply.BoardETag != previewBoardTag {
			return errors.New("board changed after resolving the preview card selectors")
		}
		var preview struct {
			BoardID       string `json:"board_id"`
			BoardRevision uint64 `json:"board_revision"`
		}
		if json.Unmarshal(reply.JSON, &preview) != nil || !boardIDPattern.MatchString(preview.BoardID) || preview.BoardID != cmd.boardID ||
			preview.BoardRevision == 0 || preview.BoardRevision > maxCLIRevision ||
			reply.BoardETag != fmt.Sprintf("\"%s-%d\"", preview.BoardID, preview.BoardRevision) {
			return errors.New("service did not return a valid preview board revision")
		}
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		if cmd.t06Action == "backup create" {
			var identity struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(body, &identity)
			return fmt.Errorf("service accepted backup %s, but its response could not be written", identity.ID)
		}
		if cmd.method != http.MethodGet {
			return errors.New("service accepted the mutation, but its response could not be written")
		}
		return errors.New("could not write the API response")
	}
	return nil
}

func t06BoardValidator(api *client.Client, cmd cliCommand, fields map[string]json.RawMessage) (string, error) {
	if cmd.t06Action == "card set-priorities" && len(cmd.cardSelectors) != 0 {
		ids, boardTag, err := resolvePriorityCards(api, cmd)
		if err != nil {
			return "", err
		}
		fields["card_ids"], _ = json.Marshal(ids)
		return boardTag, nil
	}
	board, err := api.Do(http.MethodGet, "/boards/"+cmd.boardID, nil, "")
	if err != nil {
		return "", err
	}
	if board.ETag == "" {
		return "", errors.New("service did not return a strong board ETag")
	}
	if err := checkCLIRevision(cmd.revision, board.JSON); err != nil {
		return "", err
	}
	return board.ETag, nil
}

func downloadBackup(api *client.Client, boardID, resource, output string, stdout io.Writer) error {
	reply, err := api.Do(http.MethodGet, resource, nil, "")
	if err != nil {
		return err
	}
	if err := validateBoardResource(reply.JSON, boardID, strings.TrimPrefix(resource, "/backups/")); err != nil {
		return err
	}
	var metadata struct {
		ID     string  `json:"id"`
		Size   *uint64 `json:"size"`
		Digest string  `json:"sha2_256"`
	}
	if json.Unmarshal(reply.JSON, &metadata) != nil || metadata.ID != strings.TrimPrefix(resource, "/backups/") ||
		metadata.Size == nil || *metadata.Size > maxCLIRevision {
		return errors.New("service did not return valid backup metadata")
	}
	if digest, err := hex.DecodeString(metadata.Digest); err != nil || len(digest) != sha256.Size {
		return errors.New("service did not return a valid backup digest")
	}
	file, err := os.CreateTemp("", "localswim-backup-*")
	if err != nil {
		return errors.New("could not create private backup staging file")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	digest := sha256.New()
	if err := api.BackupContent(resource+"/content", *metadata.Size, io.MultiWriter(file, digest)); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != metadata.Digest {
		return errors.New("backup content does not match its SHA-2-256 metadata")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return errors.New("could not read verified backup staging file")
	}
	if output == "-" {
		if _, err := io.Copy(stdout, file); err != nil {
			return errors.New("could not write backup bytes to standard output")
		}
		return nil
	}
	destination, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("could not create a new backup output file")
	}
	_, writeErr := io.Copy(destination, file)
	closeErr := destination.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("could not finish writing the backup output file. Inspect the destination before trying again")
	}
	return nil
}
