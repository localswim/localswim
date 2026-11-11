package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/localswim/localswim/internal/client"
)

type t03Edit struct {
	validatorPath string
	idField       string
	id            string
	tagField      string
	textInput     bool
	file          string
	uploadID      string
	filename      string
}

func parseCLIRevision(value string) (*uint64, error) {
	revision, err := strconv.ParseUint(value, 10, 64)
	if err != nil || revision == 0 || revision > maxCLIRevision {
		return nil, errors.New("--revision requires an integer from 1 to 9007199254740991")
	}
	return &revision, nil
}

func parseT03(name string, args []string) (cliCommand, error) {
	var cmd cliCommand
	var validator, field string
	count := 1
	switch name {
	case "card patch", "card description", "card move", "card assign", "card title", "card cover", "card set-priority":
		cmd.method, validator, count = http.MethodPatch, "/cards/", 2
	case "card archive", "card unarchive":
		cmd.method, validator = http.MethodPatch, "/cards/"
	case "lane rename", "lane set-approval-button":
		cmd.method, validator, count = http.MethodPatch, "/lanes/", 2
	case "lane retire":
		cmd.method, validator = http.MethodPatch, "/lanes/"
	case "lane add":
		cmd.method, cmd.resource, validator, field = http.MethodPost, "/lanes", "/boards/", "board_id"
		count = 2
		cmd.edit.tagField = "board_etag"
	case "comment post", "attachment upload":
		cmd.method, cmd.resource, validator, field = http.MethodPost, "/comments", "/cards/", "card_id"
		count = 2
		cmd.edit.tagField = "expected_card_etag"
		if name == "attachment upload" {
			cmd.resource = "/attachments"
		}
	case "comment list", "attachment list":
		return parseCLIList(name, args)
	case "attachment get":
		cmd.method, cmd.resource = http.MethodGet, "/attachments/"
	case "attachment detach":
		cmd.method, cmd.resource = http.MethodDelete, "/attachments/"
		cmd.detachAttachment = true
	case "lane get":
		cmd.method, cmd.resource = http.MethodGet, "/lanes/"
	default:
		return cmd, errors.New("unknown command")
	}
	flags := flag.NewFlagSet("localswim", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var revision, uploadID, filename string
	if cmd.method != http.MethodGet {
		flags.StringVar(&revision, "revision", "", "expected resource revision")
	}
	if name == "attachment upload" {
		flags.StringVar(&uploadID, "id", "", "known attachment UUIDv4")
		flags.StringVar(&filename, "filename", "", "display filename")
	}
	if err := flags.Parse(args); err != nil {
		return cmd, err
	}
	requiresInput := name == "card patch" || name == "card description" || name == "comment post" || name == "lane add" || name == "attachment upload"
	if requiresInput && flags.NArg() < count {
		return cmd, errors.New("expected a UUIDv4 resource ID followed by FILE or -")
	}
	if flags.NArg() != count || !boardIDPattern.MatchString(flags.Arg(0)) {
		return cmd, errors.New("expected a UUIDv4 resource ID and the documented arguments")
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
		case "filename":
			optionErr = validateUploadFilename(filename)
		case "id":
			if !boardIDPattern.MatchString(uploadID) {
				optionErr = errors.New("--id requires a UUIDv4 attachment ID")
			}
		}
	})
	if optionErr != nil {
		return cmd, optionErr
	}
	if cmd.method == http.MethodGet || cmd.detachAttachment {
		cmd.resource += strings.ToLower(flags.Arg(0))
		return cmd, nil
	}
	cmd.edit.idField, cmd.edit.id = field, flags.Arg(0)
	cmd.edit.validatorPath = validator + cmd.edit.id
	if cmd.method == http.MethodPatch {
		cmd.resource = cmd.edit.validatorPath
	}
	value := flags.Arg(1)
	switch name {
	case "card archive", "card unarchive":
		cmd.body, _ = json.Marshal(map[string]bool{"archived": name == "card archive"})
	case "lane retire":
		cmd.body, _ = json.Marshal(map[string]bool{"retired": true})
	case "lane set-approval-button":
		if value != "enabled" && value != "disabled" {
			return cmd, errors.New("expected enabled or disabled")
		}
		cmd.body, _ = json.Marshal(map[string]bool{"approval_enabled": value == "enabled"})
	case "card cover":
		switch {
		case value == "none":
			cmd.body = json.RawMessage("{\"cover_attachment_id\":null}")
		case boardIDPattern.MatchString(value):
			cmd.body, _ = json.Marshal(map[string]string{"cover_attachment_id": value})
		default:
			return cmd, errors.New("expected a UUIDv4 attachment ID or none")
		}
	case "attachment upload":
		cmd.edit.file = value
		cmd.edit.uploadID = strings.ToLower(uploadID)
		cmd.edit.filename = filename
		if value == "" {
			return cmd, errors.New("attachment upload requires a file path or -")
		}
	case "card patch", "card description", "comment post", "lane add":
		if value == "" {
			return cmd, errors.New("input requires a file path or -")
		}
		cmd.inputPath = value
		cmd.edit.textInput = name == "card description"
	case "card move":
		if !boardIDPattern.MatchString(value) {
			return cmd, errors.New("expected a UUIDv4 lane ID")
		}
		cmd.body, _ = json.Marshal(map[string]string{"lane_id": value})
	case "card assign":
		if !boardIDPattern.MatchString(value) {
			return cmd, errors.New("expected a UUIDv4 owner ID")
		}
		cmd.body, _ = json.Marshal(map[string]string{"owner_id": value})
	case "card set-priority":
		if !boardIDPattern.MatchString(value) {
			return cmd, errors.New("expected a UUIDv4 priority ID")
		}
		cmd.body, _ = json.Marshal(map[string]string{"priority_id": value})
	case "card title", "lane rename":
		if value == "" || !utf8.ValidString(value) {
			return cmd, errors.New("title or label must be nonempty valid UTF-8")
		}
		key := "title"
		if name == "lane rename" {
			key = "label"
		}
		cmd.body, _ = json.Marshal(map[string]string{key: value})
	}
	return cmd, nil
}

func parseCLIList(name string, args []string) (cliCommand, error) {
	resources := map[string]string{
		"card list": "/cards", "relationship list": "/relationships",
		"comment list": "/comments", "attachment list": "/attachments",
	}
	cmd := cliCommand{method: http.MethodGet, resource: resources[name]}
	flags := flag.NewFlagSet("localswim", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	limit := flags.Int("limit", 50, "page size")
	cursor := flags.String("cursor", "", "cursor from the preceding page")
	var archived string
	if name == "card list" {
		flags.StringVar(&archived, "archived", "exclude", "exclude, include, or only archived cards")
	}
	if err := flags.Parse(args); err != nil {
		return cmd, err
	}
	if *limit < 1 || *limit > 200 || len(*cursor) > 2048 {
		return cmd, errors.New("page limit must be 1 to 200 and cursor must fit within 2048 bytes")
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}}
	if *cursor != "" {
		query.Set("cursor", *cursor)
	}
	if name == "card list" {
		archived = strings.ToLower(archived)
		switch archived {
		case "exclude", "include", "only":
			query.Set("archived", archived)
		default:
			return cmd, errors.New("--archived requires exclude, include, or only")
		}
	}
	optionalCard := name == "comment list" || name == "relationship list"
	switch {
	case name == "attachment list":
		if flags.NArg() != 1 || !boardIDPattern.MatchString(flags.Arg(0)) {
			return cmd, errors.New("expected one UUIDv4 card ID")
		}
		query.Set("card_id", flags.Arg(0))
	case flags.NArg() < 1 || flags.NArg() > 2 || (flags.NArg() == 2 && !optionalCard) ||
		!boardIDPattern.MatchString(flags.Arg(0)):
		if optionalCard {
			return cmd, errors.New("expected a UUIDv4 board ID and optional UUIDv4 card ID")
		}
		return cmd, errors.New("expected one UUIDv4 board ID")
	default:
		query.Set("board_id", flags.Arg(0))
		if flags.NArg() == 2 {
			if !boardIDPattern.MatchString(flags.Arg(1)) {
				return cmd, errors.New("expected a UUIDv4 card ID")
			}
			query.Set("card_id", flags.Arg(1))
		}
	}
	cmd.resource += "?" + query.Encode()
	return cmd, nil
}

func prepareT03(api *client.Client, cmd cliCommand, stdin io.Reader) (json.RawMessage, string, string, error) {
	edit := cmd.edit
	body := cmd.body
	if cmd.inputPath != "" {
		var err error
		body, err = readT03Input(stdin, edit.textInput)
		if err != nil {
			return nil, "", "", err
		}
	}
	current, err := api.Do(http.MethodGet, edit.validatorPath, nil, "")
	if err != nil {
		return nil, "", "", err
	}
	if current.ETag == "" {
		return nil, "", "", errors.New("service did not return a strong resource ETag")
	}
	if err := checkCLIRevision(cmd.revision, current.JSON); err != nil {
		return nil, "", "", err
	}
	if cmd.resource == "/lanes/"+edit.id {
		board, err := parentBoard(api, current.JSON)
		if err != nil {
			return nil, "", "", err
		}
		fields, err := cliInputFields(body)
		if err != nil {
			return nil, "", "", err
		}
		fields["board_etag"], _ = json.Marshal(board.ETag)
		body, err = json.Marshal(fields)
		return body, current.ETag, "", err
	}
	if cmd.method == http.MethodPatch {
		return body, current.ETag, "", nil
	}
	if edit.file != "" {
		body, contentType, err := uploadBody(edit.file, edit.filename, edit.uploadID, edit.id, current.ETag, stdin)
		return body, "", contentType, err
	}
	fields, err := cliInputFields(body)
	if err != nil {
		return nil, "", "", err
	}
	if fields[edit.idField] != nil || fields[edit.tagField] != nil {
		return nil, "", "", fmt.Errorf("CLI supplies %s and %s", edit.idField, edit.tagField)
	}
	fields[edit.idField], _ = json.Marshal(edit.id)
	fields[edit.tagField], _ = json.Marshal(current.ETag)
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(fields); err != nil {
		return nil, "", "", err
	}
	body = bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'})
	if len(body) > maxCLIInput {
		return nil, "", "", errors.New("request exceeds 96 MiB after adding resource validators")
	}
	return body, "", "", nil
}

func parentBoard(api *client.Client, resource json.RawMessage) (client.Response, error) {
	var parent struct {
		BoardID string `json:"board_id"`
	}
	if json.Unmarshal(resource, &parent) != nil || !boardIDPattern.MatchString(parent.BoardID) {
		return client.Response{}, errors.New("service did not return a valid parent board ID")
	}
	board, err := api.Do(http.MethodGet, "/boards/"+parent.BoardID, nil, "")
	if err != nil {
		return client.Response{}, err
	}
	if board.ETag == "" {
		return client.Response{}, errors.New("service did not return a strong board ETag")
	}
	return board, nil
}

func readT03Input(input io.Reader, textInput bool) (json.RawMessage, error) {
	if textInput {
		const maxCodePoints = 4_194_304
		const maxUTF8Bytes = maxCodePoints * utf8.UTFMax
		data, err := io.ReadAll(io.LimitReader(input, maxUTF8Bytes+1))
		if err != nil || len(data) > maxUTF8Bytes || !utf8.Valid(data) || utf8.RuneCount(data) > maxCodePoints {
			return nil, errors.New("input must contain valid UTF-8 text within 4,194,304 Unicode code points")
		}
		return json.Marshal(map[string]string{"description": string(data)})
	}
	data, err := io.ReadAll(io.LimitReader(input, maxCLIInput+1))
	if err != nil || len(data) > maxCLIInput || !client.IsJSONObject(data) {
		return nil, errors.New("input must contain one valid JSON object within 96 MiB")
	}
	return data, nil
}

func newUploadID() string {
	return uuid.NewV4().String()
}

func validateUploadFilename(filename string) error {
	if filename == "" || !utf8.ValidString(filename) || utf8.RuneCountInString(filename) > 1024 || strings.ContainsAny(filename, "\r\n\x00") {
		return errors.New("attachment filename must contain 1 to 1024 valid UTF-8 characters without line breaks or NUL")
	}
	return nil
}

func uploadBody(path, filename, attachmentID, cardID, etag string, input io.Reader) ([]byte, string, error) {
	const maxFile = 10 << 20
	const maxUpload = maxFile + (256 << 10)
	if filename == "" {
		filename = filepath.Base(path)
		if path == "-" {
			filename = attachmentID
		}
	}
	if err := validateUploadFilename(filename); err != nil {
		return nil, "", err
	}
	if path != "-" {
		candidate, err := os.Stat(path)
		if err != nil {
			return nil, "", errors.New("could not inspect attachment file")
		}
		if !candidate.Mode().IsRegular() {
			return nil, "", errors.New("attachment upload requires a regular file")
		}
		if candidate.Size() > maxFile {
			return nil, "", errors.New("attachment file exceeds 10 MiB")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, "", errors.New("could not open attachment file")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, "", errors.New("attachment upload requires a regular file")
		}
		if !os.SameFile(candidate, info) {
			return nil, "", errors.New("attachment file changed before opening")
		}
		if info.Size() > maxFile {
			return nil, "", errors.New("attachment file exceeds 10 MiB")
		}
		input = file
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("id", attachmentID); err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("card_id", cardID); err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("expected_card_etag", etag); err != nil {
		return nil, "", err
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", err
	}
	size, err := io.Copy(part, io.LimitReader(input, maxFile+1))
	if err != nil {
		return nil, "", errors.New("could not read attachment file")
	}
	if size > maxFile {
		return nil, "", errors.New("attachment file exceeds 10 MiB")
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	if body.Len() > maxUpload {
		return nil, "", errors.New("encoded attachment exceeds the upload limit")
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}
