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
	count := 1
	switch name {
	case "relationship create":
		cmd.method, count = http.MethodPost, 3
	case "relationship patch":
		cmd.method, count = http.MethodPatch, 2
	case "relationship remove":
		cmd.method = http.MethodDelete
	case "attachment download":
		cmd.resource, count = "/attachments", 2
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
	if name == "relationship create" {
		if flags.NArg() != 3 || !boardIDPattern.MatchString(flags.Arg(0)) || !boardIDPattern.MatchString(flags.Arg(2)) {
			return cmd, errors.New("relationship create requires SOURCE_CARD_ID KIND TARGET_CARD_ID")
		}
		kind := strings.ToLower(flags.Arg(1))
		if !cliRelationshipKind(kind) {
			return cmd, errors.New("kind must be blocks, duplicates, references, or relates_to")
		}
		source, target := strings.ToLower(flags.Arg(0)), strings.ToLower(flags.Arg(2))
		if source == target {
			return cmd, errors.New("relationship endpoints must be different cards")
		}
		if relationshipID == "" {
			relationshipID = newUploadID()
		}
		cmd.relationshipID = strings.ToLower(relationshipID)
		cmd.body, _ = json.Marshal(map[string]string{"id": cmd.relationshipID, "source_card_id": source, "target_card_id": target, "kind": kind})
	} else if count == 2 && flags.NArg() < count {
		return cmd, errors.New("expected a UUIDv4 resource ID followed by FILE or -")
	}
	if name != "relationship create" && (flags.NArg() != count || !boardIDPattern.MatchString(flags.Arg(0)) ||
		(count == 2 && flags.Arg(1) == "")) {
		return cmd, errors.New("expected a UUIDv4 resource ID and the documented arguments")
	}
	id := strings.ToLower(flags.Arg(0))
	if name == "relationship create" {
		cmd.resource = "/relationships"
	} else {
		cmd.resource += "/" + id
	}
	switch name {
	case "relationship patch":
		cmd.inputPath = flags.Arg(1)
	case "attachment download":
		cmd.downloadPath = flags.Arg(1)
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
	var board client.Response
	var ifMatch string
	var err error
	if cmd.method == http.MethodPost {
		var sourceID, targetID string
		_ = json.Unmarshal(fields["source_card_id"], &sourceID)
		_ = json.Unmarshal(fields["target_card_id"], &targetID)
		sourceCard, source, readErr := relationshipEndpoint(api, sourceID)
		if readErr != nil {
			return nil, "", "", readErr
		}
		_, target, readErr := relationshipEndpoint(api, targetID)
		if readErr != nil {
			return nil, "", "", readErr
		}
		if source != target {
			return nil, "", "", errors.New("relationship endpoints must belong to the same board")
		}
		cmd.boardID = source
		board, err = parentBoard(api, sourceCard.JSON)
	} else {
		current, readErr := api.Do(http.MethodGet, cmd.resource, nil, "")
		if readErr != nil {
			return nil, "", "", readErr
		}
		if current.ETag == "" {
			return nil, "", "", errors.New("service did not return a strong relationship ETag")
		}
		if err := checkCLIRevision(cmd.revision, current.JSON); err != nil {
			return nil, "", "", err
		}
		ifMatch = current.ETag
		board, err = parentBoard(api, current.JSON)
	}
	if err != nil {
		return nil, "", "", err
	}
	if board.ETag == "" {
		return nil, "", "", errors.New("service did not return a strong board ETag")
	}
	if cmd.method == http.MethodPost {
		if err := checkCLIRevision(cmd.revision, board.JSON); err != nil {
			return nil, "", "", err
		}
	}
	if cmd.method == http.MethodDelete {
		return nil, ifMatch, board.ETag, nil
	}
	fields["expected_board_etag"], _ = json.Marshal(board.ETag)
	if cmd.method == http.MethodPost {
		fields["board_id"], _ = json.Marshal(cmd.boardID)
	}
	encoded, err := json.Marshal(fields)
	if len(encoded) > maxCLIInput {
		return nil, "", "", errors.New("request exceeds 96 MiB after adding resource validators")
	}
	return encoded, ifMatch, "", err
}

func downloadAttachment(api *client.Client, resource, output string, stdout io.Writer) error {
	reply, err := api.Do(http.MethodGet, resource, nil, "")
	if err != nil {
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

func relationshipEndpoint(api *client.Client, id string) (client.Response, string, error) {
	reply, err := api.Do(http.MethodGet, "/cards/"+id, nil, "")
	if err != nil {
		return client.Response{}, "", err
	}
	var card struct {
		ID      string `json:"id"`
		BoardID string `json:"board_id"`
	}
	if json.Unmarshal(reply.JSON, &card) != nil || !boardIDPattern.MatchString(card.ID) || strings.ToLower(card.ID) != id || !boardIDPattern.MatchString(card.BoardID) {
		return client.Response{}, "", errors.New("service did not return the requested card and a valid parent board ID")
	}
	return reply, strings.ToLower(card.BoardID), nil
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
