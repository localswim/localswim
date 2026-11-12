package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/localswim/localswim/internal/client"
)

func parseT04(name string, args []string) (cliCommand, error) {
	cmd := cliCommand{method: http.MethodGet, resource: "/relationships"}
	flags := flag.NewFlagSet("localswim", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	count := 2
	switch name {
	case "relationship create":
		cmd.method, count = http.MethodPost, 4
	case "relationship patch":
		cmd.method, count = http.MethodPatch, 3
	case "relationship remove":
		cmd.method = http.MethodDelete
	case "attachment download":
		cmd.resource, count = "/attachments", 3
	}
	cmd.relationshipWrite = cmd.method != http.MethodGet
	var revision, relationshipID string
	if name == "relationship create" {
		flags.StringVar(&relationshipID, "id", "", "relationship UUIDv4")
	}
	if cmd.relationshipWrite {
		label := "expected relationship revision"
		if cmd.method == http.MethodPost {
			label = "expected board revision"
		}
		flags.StringVar(&revision, "revision", "", label)
	}
	if err := flags.Parse(args); err != nil {
		return cmd, err
	}
	var hasID bool
	flags.Visit(func(f *flag.Flag) { hasID = hasID || f.Name == "id" })
	if hasID && !boardIDPattern.MatchString(relationshipID) {
		return cmd, errors.New("--id requires a UUIDv4")
	}
	var optionErr error
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "revision" {
			cmd.revision, optionErr = parseCLIRevision(revision)
		}
	})
	if optionErr != nil {
		return cmd, optionErr
	}
	if (name == "relationship create" || name == "relationship remove") && flags.NArg() == 4 {
		cmd.ticketRelationship, optionErr = parseTicketLinkCommand(name, flags.Args(), relationshipID)
		return cmd, optionErr
	}
	if name == "relationship remove" {
		if flags.NArg() != 2 || !boardIDPattern.MatchString(flags.Arg(0)) || !boardIDPattern.MatchString(flags.Arg(1)) {
			return cmd, errors.New("relationship remove requires BOARD_ID RELATIONSHIP_ID or BOARD_ID SOURCE_TICKET KIND TARGET_TICKET")
		}
		cmd.boardID = strings.ToLower(flags.Arg(0))
		cmd.resource += "/" + strings.ToLower(flags.Arg(1))
		return cmd, nil
	}
	if flags.NArg() != count || !boardIDPattern.MatchString(flags.Arg(0)) ||
		!boardIDPattern.MatchString(flags.Arg(1)) || (count == 3 && flags.Arg(2) == "") {
		return cmd, errors.New("expected BOARD_ID RESOURCE_ID and the documented arguments")
	}
	cmd.boardID = strings.ToLower(flags.Arg(0))
	cmd.resource += "/" + strings.ToLower(flags.Arg(1))
	switch name {
	case "relationship patch":
		cmd.inputPath = flags.Arg(2)
	case "attachment download":
		cmd.downloadPath = flags.Arg(2)
	}
	return cmd, nil
}

func prepareRelationshipWrite(api *client.Client, cmd cliCommand, body json.RawMessage) (json.RawMessage, string, string, error) {
	var fields map[string]json.RawMessage
	if cmd.method != http.MethodDelete {
		var err error
		fields, err = cliInputFields(body)
		if err != nil {
			return nil, "", "", err
		}
		if cmd.method == http.MethodPatch {
			var kind string
			if json.Unmarshal(fields["kind"], &kind) != nil || !cliRelationshipKind(strings.ToLower(kind)) {
				return nil, "", "", errors.New("kind must be blocks, duplicates, references, or relates_to")
			}
			fields["kind"], _ = json.Marshal(strings.ToLower(kind))
		}
		if fields["board_id"] != nil || fields["expected_board_etag"] != nil {
			return nil, "", "", errors.New("CLI supplies the board ID and validator as required")
		}
	}
	current, err := api.Do(http.MethodGet, cmd.resource, nil, "")
	if err != nil {
		return nil, "", "", err
	}
	if err := validateBoardResource(current.JSON, cmd.boardID, strings.TrimPrefix(cmd.resource, "/relationships/")); err != nil {
		return nil, "", "", err
	}
	if current.ETag == "" {
		return nil, "", "", errors.New("service did not return a strong relationship ETag")
	}
	if err := checkCLIRevision(cmd.revision, current.JSON); err != nil {
		return nil, "", "", err
	}
	board, err := parentBoard(api, current.JSON)
	if err != nil {
		return nil, "", "", err
	}
	if cmd.method == http.MethodDelete {
		return nil, current.ETag, board.ETag, nil
	}
	fields["expected_board_etag"], _ = json.Marshal(board.ETag)
	encoded, err := json.Marshal(fields)
	if len(encoded) > maxCLIInput {
		return nil, "", "", errors.New("request exceeds 96 MiB after adding resource validators")
	}
	return encoded, current.ETag, "", err
}

func downloadAttachment(api *client.Client, boardID, resource, output string, stdout io.Writer) error {
	reply, err := api.Do(http.MethodGet, resource, nil, "")
	if err != nil {
		return err
	}
	if err := validateBoardResource(reply.JSON, boardID, strings.TrimPrefix(resource, "/attachments/")); err != nil {
		return err
	}
	var metadata struct {
		ID     string  `json:"id"`
		Size   *uint64 `json:"size"`
		Digest string  `json:"sha2_256"`
		Active bool    `json:"active"`
	}
	if err := json.Unmarshal(reply.JSON, &metadata); err != nil ||
		metadata.ID != strings.TrimPrefix(resource, "/attachments/") ||
		!metadata.Active || metadata.Size == nil || *metadata.Size > client.MaxAttachmentBytes {
		return errors.New("service did not return valid active attachment metadata")
	}
	if digest, err := hex.DecodeString(metadata.Digest); err != nil || len(digest) != sha256.Size {
		return errors.New("service did not return a valid attachment digest")
	}
	data, err := api.AttachmentContent(resource + "/content?disposition=attachment")
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if uint64(len(data)) != *metadata.Size || hex.EncodeToString(digest[:]) != metadata.Digest {
		return errors.New("attachment content does not match its size and SHA-2-256 metadata")
	}
	if output == "-" {
		if _, err := io.Copy(stdout, bytes.NewReader(data)); err != nil {
			return errors.New("could not write attachment bytes to standard output")
		}
		return nil
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("could not create a new attachment output file")
	}
	_, writeErr := io.Copy(file, bytes.NewReader(data))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("could not finish writing the attachment output file. Inspect the destination before trying again")
	}
	return nil
}

func cliRelationshipKind(kind string) bool {
	return kind == "blocks" || kind == "duplicates" || kind == "references" || kind == "relates_to"
}

func confirmedRelationshipCreation(body, response json.RawMessage) bool {
	type link struct {
		ID           string `json:"id"`
		BoardID      string `json:"board_id"`
		SourceCardID string `json:"source_card_id"`
		TargetCardID string `json:"target_card_id"`
		Kind         string `json:"kind"`
	}
	var requested link
	var saved struct {
		link
		Revision *uint64 `json:"revision"`
	}
	return json.Unmarshal(body, &requested) == nil && json.Unmarshal(response, &saved) == nil &&
		saved.link == requested && saved.Revision != nil && *saved.Revision > 0 && *saved.Revision <= maxCLIRevision
}
