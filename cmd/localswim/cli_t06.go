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
	case "card set-priorities", "backup create":
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
	if name == "card set-priorities" || name == "backup download" {
		count = 2
	}
	resourcePattern := boardIDPattern
	if name == "backup info" || name == "backup download" {
		resourcePattern = operationIDPattern
	}
	if flags.NArg() < count || (name != "ticket-order preview" && flags.NArg() != count) ||
		!resourcePattern.MatchString(flags.Arg(0)) || (count == 2 && flags.Arg(1) == "") {
		return cmd, errors.New("expected a valid resource ID and the documented arguments")
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
		cmd.method, cmd.resource, cmd.boardID, cmd.inputPath = http.MethodPatch, "/cards", id, flags.Arg(1)
	case "backup create":
		cmd.method, cmd.resource, cmd.boardID = http.MethodPost, "/backups", id
		if backupID == "" {
			backupID = newOperationID()
		}
		cmd.body, _ = json.Marshal(map[string]string{"id": strings.ToLower(backupID)})
	case "backup info", "backup download":
		cmd.resource = "/backups/" + id
		if name == "backup download" {
			cmd.downloadPath = flags.Arg(1)
		}
	case "ticket-order preview":
		if flags.NArg() > 1001 {
			return cmd, errors.New("preview accepts at most 1000 card IDs")
		}
		query := url.Values{"preview": {"true"}}
		seen := make(map[string]bool)
		for _, cardID := range flags.Args()[1:] {
			cardID = strings.ToLower(cardID)
			if !boardIDPattern.MatchString(cardID) || seen[cardID] {
				return cmd, errors.New("preview requires distinct UUIDv4 card IDs")
			}
			seen[cardID] = true
			query.Add("card_id", cardID)
		}
		cmd.resource = "/ticket-orders/" + id + "?" + query.Encode()
	}
	return cmd, nil
}

func executeT06(api *client.Client, cmd cliCommand, stdin io.Reader, stdout io.Writer) error {
	if cmd.t06Action == "backup download" {
		return downloadBackup(api, cmd.resource, cmd.downloadPath, stdout)
	}
	body := cmd.body
	if cmd.inputPath != "" {
		if cmd.inputPath != "-" {
			file, err := os.Open(cmd.inputPath)
			if err != nil {
				return errors.New("could not open input file")
			}
			defer file.Close()
			stdin = file
		}
		var err error
		body, err = io.ReadAll(io.LimitReader(stdin, maxCLIInput+1))
		if err != nil || len(body) > maxCLIInput || !client.IsJSONObject(body) {
			return errors.New("JSON input must contain one valid object within 96 MiB")
		}
	}
	if cmd.boardID != "" {
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
		board, err := api.Do(http.MethodGet, "/boards/"+cmd.boardID, nil, "")
		if err != nil {
			return err
		}
		if board.ETag == "" {
			return errors.New("service did not return a strong board ETag")
		}
		if err := checkCLIRevision(cmd.revision, board.JSON); err != nil {
			return err
		}
		fields["board_id"], _ = json.Marshal(cmd.boardID)
		fields["expected_board_etag"], _ = json.Marshal(board.ETag)
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
			return fmt.Errorf("%w. Run backup info %s before retrying", err, identity.ID)
		}
		return err
	}
	if cmd.t06Action == "backup create" {
		var requested, returned struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &requested)
		_ = json.Unmarshal(reply.JSON, &returned)
		if requested.ID != returned.ID {
			return fmt.Errorf("%w. Service returned a different backup ID. Run backup info %s before retrying", client.ErrMutationOutcomeUnknown, requested.ID)
		}
	}
	if cmd.t06Action == "ticket-order preview" {
		fields, parseErr := cliInputFields(reply.JSON)
		if parseErr != nil || reply.BoardETag == "" {
			return errors.New("service did not return a valid preview board validator")
		}
		fields["expected_board_etag"], _ = json.Marshal(reply.BoardETag)
		reply.JSON, _ = json.Marshal(fields)
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

func downloadBackup(api *client.Client, resource, output string, stdout io.Writer) error {
	reply, err := api.Do(http.MethodGet, resource, nil, "")
	if err != nil {
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
