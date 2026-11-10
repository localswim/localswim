package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/localswim/localswim/internal/client"
	"github.com/localswim/localswim/internal/v2"
)

const cliHelp = `Usage:
  localswim workspace info
  localswim workspace pin set [--revision N] BOARD_ID [BOARD_ID ...]
  localswim workspace pin add [--revision N] BOARD_ID [BOARD_ID ...]
  localswim workspace pin remove [--revision N] BOARD_ID [BOARD_ID ...]
  localswim board list [--limit N] [--cursor CURSOR]
  localswim board create --name NAME
  localswim board create --json < definition.json
  localswim board get BOARD_ID
  localswim board rename [--revision N] BOARD_ID NEW_NAME
  localswim board patch [--revision N] BOARD_ID < patch.json
  localswim board verify [--data DIRECTORY] BOARD_ID
  localswim board export [--type json] BOARD_ID
  localswim card create [--revision N] BOARD_ID < definition.json
  localswim card get CARD_ID
  localswim card get BOARD_ID TICKET
  localswim policy get BOARD_ID
  localswim policy grant [--revision N] BOARD_ID < grants.json
  localswim service status
  localswim service builds [--limit N] [--cursor CURSOR]

Live commands use LOCALSWIM_URL (http://127.0.0.1:<port>) and LOCALSWIM_TOKEN.
Board creation requires exactly one of --name or --json.
--json reads a new board definition from standard input.
Set --data on board verify to read the private board data offline.
Board export writes JSON to standard output. The default --type is json.
All pin commands require at least one board ID. Pin set replaces the order.
Pin add appends new boards in the supplied order.
Pin remove skips failed targets with WARNING messages and preserves the remaining order.
Pin set validates the entire list and reports ERROR if any target fails.
Omit --revision to read the current revision once before updating.
Supply --revision to require the version you previously inspected.
For card create and policy grant, --revision refers to the board revision.
Card create reads the card fields. The CLI supplies the board ID and its validator.
Include an explicit card ID to allow focused readback after a lost create reply.
Policy grant reads create_grants and/or move_grants and preserves existing permissions.
`

type exportFormat string

const exportJSON exportFormat = "json"

func (format *exportFormat) String() string { return string(*format) }

func (format *exportFormat) Set(value string) error {
	if value != string(exportJSON) {
		return fmt.Errorf("unsupported export type %q (allowed value is json)", value)
	}
	*format = exportJSON
	return nil
}

const maxCLIInput = 96 << 20
const maxCLIRevision = 1<<53 - 1

var boardIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type cliCommand struct {
	method, resource, boardID, dataDir string
	revision                           *uint64
	body                               json.RawMessage
	pinAction                          string
	boardAction                        string
	pinIDs                             []string
	warnings                           []string
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	return runCLIWithInput(args, os.Stdin, stdout, stderr, os.Getenv)
}

func runCLIWithInput(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h")) {
		_, err := io.WriteString(stdout, cliHelp)
		if err != nil {
			return 1
		}
		return 0
	}

	command, err := parseCLI(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if _, err := io.WriteString(stdout, cliHelp); err != nil {
				return 1
			}
			return 0
		}
		fmt.Fprintln(stderr, "ERROR", err)
		fmt.Fprint(stderr, cliHelp)
		return 2
	}
	if err := executeCLI(command, stdin, stdout, stderr, getenv); err != nil {
		fmt.Fprintln(stderr, "ERROR", err)
		return 1
	}
	return 0
}

func parseCLI(args []string) (cliCommand, error) {
	var command cliCommand
	if len(args) < 2 {
		return command, errors.New("a command group and action are required")
	}

	name := args[0] + " " + args[1]
	optionArgs := args[2:]
	if name == "workspace pin" {
		if len(optionArgs) == 0 {
			return command, errors.New("workspace pin requires set, add, or remove")
		}
		if optionArgs[0] == "--help" || optionArgs[0] == "-h" {
			return command, flag.ErrHelp
		}
		name += " " + optionArgs[0]
		optionArgs = optionArgs[1:]
	}

	flags := flag.NewFlagSet("localswim", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	needBoardID := false
	rename := false
	creating := false
	var createName string
	var jsonInput bool
	paginated := false
	format := exportJSON
	switch name {
	case "workspace info":
		command.method, command.resource = http.MethodGet, "/workspace"
	case "workspace pin set", "workspace pin add", "workspace pin remove":
		command.method, command.resource = http.MethodPatch, "/workspace"
		command.pinAction = args[2]
	case "board list":
		command.method, command.resource = http.MethodGet, "/boards"
		paginated = true
	case "board create":
		command.method, command.resource = http.MethodPost, "/boards"
		creating = true
		flags.StringVar(&createName, "name", "", "new board name with the default workflow")
		flags.BoolVar(&jsonInput, "json", false, "read a new board definition from standard input")
	case "board get":
		command.method, command.resource, needBoardID = http.MethodGet, "/boards", true
	case "board rename":
		command.method, command.resource, needBoardID = http.MethodPatch, "/boards", true
		rename = true
	case "board patch":
		command.method, command.resource, needBoardID = http.MethodPatch, "/boards", true
	case "board verify":
		command.method, command.resource, needBoardID = http.MethodGet, "/verifications", true
		flags.StringVar(&command.dataDir, "data", "", "private data directory")
	case "board export":
		command.method, command.resource, needBoardID = http.MethodGet, "/snapshots", true
		flags.Var(&format, "type", "export type (allowed value: json)")
	case "card create":
		command.method, command.resource, needBoardID = http.MethodPost, "/cards", true
		command.boardAction = name
	case "card get":
		command.method, command.resource = http.MethodGet, "/cards"
	case "policy get", "policy grant":
		command.method, command.resource, needBoardID = http.MethodGet, "/policies", true
		command.boardAction = name
		if name == "policy grant" {
			command.method = http.MethodPut
		}
	case "service status":
		command.method, command.resource = http.MethodGet, "/service"
	case "service builds":
		command.method, command.resource = http.MethodGet, "/builds"
		paginated = true
	default:
		return command, errors.New("unknown command")
	}

	var revisionValue string
	if command.method == http.MethodPatch || command.method == http.MethodPut || name == "card create" {
		flags.StringVar(&revisionValue, "revision", "", "expected resource revision")
	}
	var limit int
	var cursor string
	if paginated {
		flags.IntVar(&limit, "limit", 50, "page size")
		flags.StringVar(&cursor, "cursor", "", "cursor from the preceding page")
	}
	if err := flags.Parse(optionArgs); err != nil {
		return command, fmt.Errorf("invalid command options (%w)", err)
	}
	if paginated {
		if limit < 1 || limit > 200 || len(cursor) > 2048 {
			return command, errors.New("page limit must be 1 to 200 and cursor must fit within 2048 bytes")
		}
		query := url.Values{"limit": {strconv.Itoa(limit)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		command.resource += "?" + query.Encode()
	}
	var optionErr error
	var hasName, hasJSON, hasRevision bool
	flags.Visit(func(f *flag.Flag) {
		hasName = hasName || f.Name == "name"
		hasJSON = hasJSON || f.Name == "json"
		hasRevision = hasRevision || f.Name == "revision"
		if f.Name != "revision" && f.Value.String() == "" {
			optionErr = errors.New("command options require nonempty values")
		}
	})
	if optionErr != nil {
		return command, optionErr
	}
	if creating {
		if hasName == hasJSON || (hasJSON && !jsonInput) {
			return command, errors.New("board create requires exactly one of --name NAME or --json")
		}
		if hasName {
			var err error
			command.body, err = boardNameBody(createName)
			if err != nil {
				return command, err
			}
		}
	}
	if command.pinAction != "" {
		if flags.NArg() == 0 {
			return command, errors.New("pin commands require at least one board ID")
		}
		if flags.NArg() > 1000 {
			return command, errors.New("pin commands accept at most 1000 board IDs")
		}
		command.pinIDs = make([]string, 0, flags.NArg())
		for index, id := range flags.Args() {
			if !boardIDPattern.MatchString(id) {
				if command.pinAction == "remove" {
					command.warnings = append(command.warnings, fmt.Sprintf("Skipped remove argument %d because it is not a UUIDv4 board ID.", index+1))
					continue
				}
				return command, errors.New("expected UUIDv4 board IDs")
			}
			id = strings.ToLower(id)
			if slices.Contains(command.pinIDs, id) {
				if command.pinAction == "remove" {
					command.warnings = append(command.warnings, fmt.Sprintf("Skipped repeated board ID %s.", id))
					continue
				}
				return command, errors.New("duplicate board ID")
			}
			command.pinIDs = append(command.pinIDs, id)
		}
	} else if name == "card get" {
		switch {
		case flags.NArg() == 1 && boardIDPattern.MatchString(flags.Arg(0)):
			command.resource += "/" + flags.Arg(0)
		case flags.NArg() == 2 && boardIDPattern.MatchString(flags.Arg(0)):
			ticket, err := strconv.ParseUint(flags.Arg(1), 10, 53)
			if err != nil || ticket == 0 {
				return command, errors.New("ticket requires an integer from 1 to 9007199254740991")
			}
			query := url.Values{"board_id": {flags.Arg(0)}, "ticket": {strconv.FormatUint(ticket, 10)}, "archived": {"include"}}
			command.resource += "?" + query.Encode()
		default:
			return command, errors.New("expected a UUIDv4 card ID or a UUIDv4 board ID followed by a ticket")
		}
	} else if needBoardID {
		count, expected := 1, "expected one UUIDv4 board ID"
		if rename {
			count, expected = 2, "expected a UUIDv4 board ID followed by one new name (quote names containing spaces)"
		}
		if flags.NArg() != count || !boardIDPattern.MatchString(flags.Arg(0)) {
			return command, errors.New(expected)
		}
		command.boardID = flags.Arg(0)
		if command.boardAction == "" {
			command.resource += "/" + command.boardID
		}
		if rename {
			var err error
			command.body, err = boardNameBody(flags.Arg(1))
			if err != nil {
				return command, err
			}
		}
	} else if flags.NArg() != 0 {
		return command, errors.New("unexpected command arguments")
	}
	if hasRevision {
		revision, err := strconv.ParseUint(revisionValue, 10, 64)
		if err != nil || revision == 0 || revision > maxCLIRevision {
			return command, errors.New("--revision requires an integer from 1 to 9007199254740991")
		}
		command.revision = &revision
	}
	return command, nil
}

func boardNameBody(name string) (json.RawMessage, error) {
	if name == "" {
		return nil, errors.New("board name must not be empty")
	}
	if !utf8.ValidString(name) {
		return nil, errors.New("board name must be valid UTF-8")
	}
	return json.Marshal(map[string]string{"name": name})
}

func executeCLI(command cliCommand, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	if command.dataDir != "" {
		report, err := v2.VerifyOffline(command.dataDir, command.boardID)
		if err != nil {
			return errors.New("offline verification could not read or verify the requested board")
		}
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			return errors.New("could not write the verification report")
		}
		return nil
	}

	api, err := client.New(getenv("LOCALSWIM_URL"), getenv("LOCALSWIM_TOKEN"))
	if err != nil {
		return err
	}
	body := command.body
	if body == nil && command.pinAction == "" && (command.method == http.MethodPost || command.method == http.MethodPatch || command.method == http.MethodPut) {
		body, err = io.ReadAll(io.LimitReader(stdin, maxCLIInput+1))
		if err != nil || len(body) > maxCLIInput || !client.IsJSONObject(body) {
			return errors.New("standard input must contain one valid JSON object within 96 MiB")
		}
	}
	var ifMatch string
	if command.boardAction != "" {
		command.resource, body, ifMatch, err = prepareBoardAction(api, command, body)
		if err != nil {
			return err
		}
	}
	if command.method == http.MethodPatch {
		current, err := api.Do(http.MethodGet, command.resource, nil, "")
		if err != nil {
			return err
		}
		if current.ETag == "" {
			return errors.New("service did not return a strong resource ETag")
		}
		if err := checkCLIRevision(command.revision, current.JSON); err != nil {
			return err
		}
		if command.pinAction != "" {
			var warnings []string
			body, warnings, err = pinRequestBody(command.pinAction, command.pinIDs, current.JSON)
			command.warnings = append(command.warnings, warnings...)
			if err != nil {
				return err
			}
		}
		ifMatch = current.ETag
	}
	reply, err := api.Do(command.method, command.resource, body, ifMatch)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, string(reply.JSON)); err != nil {
		if body != nil {
			return errors.New("service accepted the mutation, but its response could not be written")
		}
		return errors.New("could not write the API response")
	}
	for _, warning := range command.warnings {
		if _, err := fmt.Fprintln(stderr, "WARNING", warning); err != nil {
			return errors.New("service accepted the mutation, but warnings could not be written")
		}
	}
	return nil
}

func pinRequestBody(action string, requested []string, current json.RawMessage) (json.RawMessage, []string, error) {
	pins := requested
	var warnings []string
	if action != "set" {
		var workspace struct {
			PinnedBoardIDs []string `json:"pinned_board_ids"`
		}
		if err := json.Unmarshal(current, &workspace); err != nil || workspace.PinnedBoardIDs == nil || len(workspace.PinnedBoardIDs) > 1000 {
			return nil, nil, errors.New("service did not return a valid pinned board list")
		}
		pins = workspace.PinnedBoardIDs
		if action == "add" {
			for _, id := range requested {
				if !slices.Contains(pins, id) {
					pins = append(pins, id)
				}
			}
		} else {
			for _, id := range requested {
				if !slices.Contains(pins, id) {
					warnings = append(warnings, fmt.Sprintf("Board %s was not pinned.", id))
				}
			}
			pins = slices.DeleteFunc(pins, func(id string) bool { return slices.Contains(requested, id) })
		}
	}
	body, err := json.Marshal(map[string][]string{"pinned_board_ids": pins})
	return body, warnings, err
}

func checkCLIRevision(expected *uint64, document json.RawMessage) error {
	if expected == nil {
		return nil
	}
	var value struct {
		Revision *uint64 `json:"revision"`
	}
	if json.Unmarshal(document, &value) != nil || value.Revision == nil || *value.Revision == 0 || *value.Revision > maxCLIRevision {
		return errors.New("service did not return a valid resource revision")
	}
	if *value.Revision != *expected {
		return fmt.Errorf("revision mismatch (expected %d, current %d)", *expected, *value.Revision)
	}
	return nil
}

func prepareBoardAction(api *client.Client, command cliCommand, input json.RawMessage) (string, json.RawMessage, string, error) {
	var fields map[string]json.RawMessage
	if input != nil {
		var err error
		fields, err = cliInputFields(input)
		if err != nil {
			return "", nil, "", err
		}
	}
	board, err := api.Do(http.MethodGet, "/boards/"+command.boardID, nil, "")
	if err != nil {
		return "", nil, "", err
	}
	if command.method != http.MethodGet && board.ETag == "" {
		return "", nil, "", errors.New("service did not return a strong board ETag")
	}
	if err := checkCLIRevision(command.revision, board.JSON); err != nil {
		return "", nil, "", err
	}
	if command.boardAction == "card create" {
		if fields["board_id"] != nil || fields["expected_board_etag"] != nil {
			return "", nil, "", errors.New("card create supplies board_id and expected_board_etag")
		}
		fields["board_id"], _ = json.Marshal(command.boardID)
		fields["expected_board_etag"], _ = json.Marshal(board.ETag)
		body, err := json.Marshal(fields)
		return command.resource, body, "", err
	}

	var target struct {
		PolicyID string `json:"policy_id"`
	}
	if json.Unmarshal(board.JSON, &target) != nil || !boardIDPattern.MatchString(target.PolicyID) {
		return "", nil, "", errors.New("service did not return a valid policy ID")
	}
	resource := command.resource + "/" + target.PolicyID
	if command.method == http.MethodGet {
		return resource, nil, "", nil
	}
	policy, err := api.Do(http.MethodGet, resource, nil, "")
	if err != nil {
		return "", nil, "", err
	}
	if policy.ETag == "" {
		return "", nil, "", errors.New("service did not return a strong policy ETag")
	}
	body, err := policyGrantBody(fields, policy.JSON, board.ETag)
	return resource, body, policy.ETag, err
}

// Retain nested grant objects for the server's strict schema checks.
func cliInputFields(input json.RawMessage) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	if _, err := decoder.Token(); err != nil {
		return nil, errors.New("invalid JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, errors.New("invalid JSON object")
		}
		name, ok := key.(string)
		if !ok || fields[name] != nil {
			return nil, errors.New("standard input must not contain repeated fields")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("invalid JSON field")
		}
		fields[name] = value
	}
	return fields, nil
}

func policyGrantBody(delta map[string]json.RawMessage, current json.RawMessage, boardTag string) (json.RawMessage, error) {
	var policy struct {
		BoardETag       string            `json:"board_etag"`
		DefaultPriority string            `json:"default_priority_id"`
		CreateGrants    []json.RawMessage `json:"create_grants"`
		MoveGrants      []json.RawMessage `json:"move_grants"`
		ResourceGrants  []json.RawMessage `json:"resource_grants"`
	}
	if json.Unmarshal(current, &policy) != nil || !boardIDPattern.MatchString(policy.DefaultPriority) ||
		policy.CreateGrants == nil || policy.MoveGrants == nil || policy.ResourceGrants == nil {
		return nil, errors.New("service did not return a complete policy")
	}
	policy.BoardETag = boardTag
	added := 0
	for name, raw := range delta {
		var grants []json.RawMessage
		if json.Unmarshal(raw, &grants) != nil || grants == nil {
			return nil, errors.New("policy grant requires arrays of creation or movement grants")
		}
		switch name {
		case "create_grants":
			policy.CreateGrants = append(policy.CreateGrants, grants...)
		case "move_grants":
			policy.MoveGrants = append(policy.MoveGrants, grants...)
		default:
			return nil, errors.New("policy grant accepts only create_grants and move_grants")
		}
		added += len(grants)
	}
	if added == 0 {
		return nil, errors.New("policy grant requires at least one grant")
	}
	return json.Marshal(policy)
}
