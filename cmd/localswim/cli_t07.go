package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/localswim/localswim/internal/client"
)

func parseT07(name string, args []string) (cliCommand, error) {
	cmd := cliCommand{method: http.MethodGet, t07Action: name}
	flags := flag.NewFlagSet("localswim", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var revision, id, search, lane, cursor, archived string
	var lanes []string
	var content bool
	limit := 50
	count := 1
	switch name {
	case "card search", "card triage-order", "card lookup":
		flags.StringVar(&search, "search", "", "literal title or ticket search")
		if name == "card lookup" {
			flags.StringVar(&lane, "lane", "", "lane UUIDv4")
		} else {
			flags.Func("lane", "lane UUIDv4 (repeatable)", func(value string) error {
				if !boardIDPattern.MatchString(value) {
					return errors.New("--lane requires a UUIDv4")
				}
				lanes = append(lanes, strings.ToLower(value))
				return nil
			})
			flags.StringVar(&archived, "archived", "", "exclude, include, or only archived cards")
		}
		flags.BoolVar(&content, "search-content", false, "search readable descriptions and comments")
		if name == "card triage-order" {
			limit = 1
		}
		flags.IntVar(&limit, "limit", limit, "page size")
		flags.StringVar(&cursor, "cursor", "", "pagination cursor")
	case "board restore", "restoration create":
		flags.StringVar(&revision, "revision", "", "expected board revision")
		count = 2
		if name == "restoration create" {
			flags.StringVar(&id, "id", "", "restoration UUIDv7")
		}
	case "publication create":
		flags.StringVar(&id, "id", "", "publication UUIDv7")
		count = 2
	case "ticket-order apply":
		count = 2
	case "restoration get", "publication get":
	default:
		return cmd, errors.New("unknown command")
	}
	if err := flags.Parse(args); err != nil {
		return cmd, err
	}
	resourcePattern := boardIDPattern
	if name == "restoration get" || name == "publication get" {
		resourcePattern = operationIDPattern
	}
	if flags.NArg() != count || !resourcePattern.MatchString(flags.Arg(0)) || (count == 2 && flags.Arg(1) == "") {
		return cmd, errors.New("expected a valid resource ID and the documented arguments, including FILE or - where required")
	}
	var optionErr error
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "revision":
			var revisionErr error
			cmd.revision, revisionErr = parseCLIRevision(revision)
			if revisionErr != nil {
				optionErr = revisionErr
			}
		case "id":
			if !operationIDPattern.MatchString(id) {
				optionErr = errors.New("--id requires a UUIDv7")
			}
		case "lane":
			if name == "card lookup" && !boardIDPattern.MatchString(lane) {
				optionErr = errors.New("--lane requires a UUIDv4")
			}
		case "archived":
			archived = strings.ToLower(archived)
			if archived != "exclude" && archived != "include" && archived != "only" {
				optionErr = errors.New("--archived requires exclude, include, or only")
			}
		case "search", "cursor":
			if f.Value.String() == "" {
				optionErr = errors.New("options require nonempty values")
			}
		}
	})
	if optionErr != nil {
		return cmd, optionErr
	}
	resourceID := strings.ToLower(flags.Arg(0))
	switch name {
	case "card search", "card triage-order", "card lookup":
		if limit < 1 || limit > 200 || len(cursor) > 2048 {
			return cmd, errors.New("page limit must be 1 to 200 and cursor must fit within 2048 bytes")
		}
		query := url.Values{"board_id": {resourceID}, "limit": {strconv.Itoa(limit)}, "sort": {"triage"}}
		if name == "card lookup" {
			query.Set("sort", "ticket")
		}
		if search != "" {
			query.Set("q", search)
		}
		if lane != "" {
			query.Set("lane_id", strings.ToLower(lane))
		}
		if len(lanes) != 0 {
			query["lane_id"] = lanes
		} else if name == "card triage-order" {
			query.Set("unfinished_only", "true")
		}
		if archived != "" {
			query.Set("archived", archived)
		}
		if content {
			query.Set("search_content", "true")
		}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		cmd.resource = "/cards?" + query.Encode()
	case "ticket-order apply":
		cmd.method, cmd.resource, cmd.inputPath = http.MethodPut, "/ticket-orders/"+resourceID, flags.Arg(1)
	case "board restore":
		cmd.method, cmd.resource, cmd.boardID, cmd.inputPath = http.MethodPut, "/snapshots/"+resourceID, resourceID, flags.Arg(1)
	case "restoration create", "publication create":
		if !operationIDPattern.MatchString(flags.Arg(1)) {
			return cmd, errors.New("expected a UUIDv7 backup ID")
		}
		if id == "" {
			id = newOperationID()
		}
		cmd.method, cmd.boardID = http.MethodPost, resourceID
		cmd.resource = "/restorations"
		if name == "publication create" {
			cmd.resource = "/publications"
		}
		cmd.body, _ = json.Marshal(map[string]string{"id": strings.ToLower(id), "board_id": resourceID, "backup_id": strings.ToLower(flags.Arg(1))})
	case "restoration get":
		cmd.resource = "/restorations/" + resourceID
	case "publication get":
		cmd.resource = "/publications/" + resourceID
	}
	return cmd, nil
}

func executeT07(api *client.Client, cmd cliCommand, stdin io.Reader, stdout io.Writer) error {
	body := []byte(cmd.body)
	var err error
	if cmd.inputPath != "" {
		if cmd.inputPath != "-" {
			file, openErr := os.Open(cmd.inputPath)
			if openErr != nil {
				return errors.New("could not open input file")
			}
			defer file.Close()
			stdin = file
		}
		body, err = io.ReadAll(io.LimitReader(stdin, maxCLIInput+1))
		if err != nil || len(body) > maxCLIInput {
			return errors.New("input must fit within 96 MiB")
		}
	}
	var ifMatch, contentType, operationID string
	switch cmd.t07Action {
	case "ticket-order apply":
		body, err = ticketOrderApplyBody(body, strings.TrimPrefix(cmd.resource, "/ticket-orders/"))
		if err != nil {
			return err
		}
	case "board restore", "restoration create":
		preflightResource := "/boards/" + cmd.boardID
		if cmd.t07Action == "board restore" {
			preflightResource = cmd.resource
		}
		board, readErr := api.Do(http.MethodGet, preflightResource, nil, "")
		if readErr != nil {
			return readErr
		}
		if board.ETag == "" {
			return errors.New("service did not return a strong board ETag")
		}
		revisionJSON := board.JSON
		if cmd.t07Action == "board restore" {
			var snapshot struct {
				Board json.RawMessage `json:"board"`
			}
			if json.Unmarshal(board.JSON, &snapshot) != nil || !client.IsJSONObject(snapshot.Board) {
				return errors.New("service did not return a native snapshot board")
			}
			revisionJSON = snapshot.Board
		}
		if err := checkCLIRevision(cmd.revision, revisionJSON); err != nil {
			return err
		}
		if cmd.t07Action == "board restore" {
			ifMatch = board.ETag
			switch {
			case client.IsJSONObject(body):
				contentType = "application/json"
			case bytes.HasPrefix(body, []byte{'P', 'K', 3, 4}):
				contentType = "application/zip"
			default:
				return errors.New("restore input must be a native JSON snapshot or ZIP backup")
			}
		} else {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(body, &fields)
			fields["expected_board_etag"], _ = json.Marshal(board.ETag)
			body, _ = json.Marshal(fields)
		}
	}
	if cmd.method == http.MethodPost {
		var identity struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &identity)
		operationID = identity.ID
	}
	reply, err := api.DoWithContentType(cmd.method, cmd.resource, body, ifMatch, contentType)
	if err != nil {
		if operationID != "" && errors.Is(err, client.ErrMutationOutcomeUnknown) {
			return fmt.Errorf("%w (operation ID %s)", err, operationID)
		}
		return err
	}
	if operationID != "" {
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(reply.JSON, &identity) != nil || identity.ID != operationID {
			return fmt.Errorf("%w. Inspect original operation ID %s before retrying", client.ErrMutationOutcomeUnknown, operationID)
		}
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		if operationID != "" {
			return fmt.Errorf("service accepted operation %s, but its response could not be written", operationID)
		}
		if cmd.method != http.MethodGet {
			return errors.New("service accepted the mutation, but its response could not be written")
		}
		return errors.New("could not write the API response")
	}
	return nil
}

func ticketOrderApplyBody(input []byte, laneID string) ([]byte, error) {
	if !client.IsJSONObject(input) {
		return nil, errors.New("expected a saved ticket-order preview JSON object")
	}
	if _, err := cliInputFields(input); err != nil {
		return nil, err
	}
	var preview struct {
		BoardID       string `json:"board_id"`
		LaneID        string `json:"lane_id"`
		BoardRevision uint64 `json:"board_revision"`
		Proposed      []struct {
			CardID string `json:"card_id"`
		} `json:"proposed"`
	}
	if json.Unmarshal(input, &preview) != nil || preview.LaneID != laneID || !boardIDPattern.MatchString(preview.BoardID) || (preview.BoardRevision == 0 || preview.BoardRevision > maxCLIRevision) || preview.Proposed == nil || len(preview.Proposed) > 1000 {
		return nil, errors.New("preview must contain its matching lane, board, proposed order, and original board_revision")
	}
	ids := make([]string, 0, len(preview.Proposed))
	seen := map[string]bool{}
	for _, item := range preview.Proposed {
		id := strings.ToLower(item.CardID)
		if !boardIDPattern.MatchString(id) || seen[id] {
			return nil, errors.New("preview requires distinct UUIDv4 card IDs")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return json.Marshal(map[string]any{"board_id": preview.BoardID, "expected_board_etag": fmt.Sprintf("\"%s-%d\"", strings.ToLower(preview.BoardID), preview.BoardRevision), "card_ids": ids})
}

func writeCLIJSON(output io.Writer, document json.RawMessage) error {
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, document, "", "  "); err != nil {
		return err
	}
	_, err := fmt.Fprintln(output, formatted.String())
	return err
}
