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
		cmd.method, count = http.MethodPost, 2
	case "relationship patch":
		cmd.method, count = http.MethodPatch, 2
	case "relationship remove":
		cmd.method = http.MethodDelete
	case "attachment download":
		cmd.resource, count = "/attachments", 2
	}
	cmd.relationshipWrite = cmd.method != http.MethodGet
	var revision string
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
	if count == 2 && flags.NArg() < count {
		return cmd, errors.New("expected a UUIDv4 resource ID followed by FILE or -")
	}
	if flags.NArg() != count || !boardIDPattern.MatchString(flags.Arg(0)) ||
		(count == 2 && flags.Arg(1) == "") {
		return cmd, errors.New("expected a UUIDv4 resource ID and the documented arguments")
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
	id := strings.ToLower(flags.Arg(0))
	if name == "relationship create" {
		cmd.boardID = id
	} else {
		cmd.resource += "/" + id
	}
	switch name {
	case "relationship create", "relationship patch":
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
		if fields["board_id"] != nil || fields["expected_board_etag"] != nil {
			return nil, "", "", errors.New("CLI supplies the board ID and validator as required")
		}
	}
	var board client.Response
	var ifMatch string
	var err error
	if cmd.method == http.MethodPost {
		board, err = api.Do(http.MethodGet, "/boards/"+cmd.boardID, nil, "")
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
