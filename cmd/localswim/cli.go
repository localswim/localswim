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
	"strconv"

	"github.com/localswim/localswim/internal/client"
	"github.com/localswim/localswim/internal/v2"
)

const cliHelp = `Usage:
  localswim workspace get
  localswim workspace pins [--if-match ETAG] < pins.json
  localswim board list [--limit N] [--cursor CURSOR]
  localswim board create < board.json
  localswim board get BOARD_ID
  localswim board patch [--if-match ETAG] BOARD_ID < patch.json
  localswim board verify [--data DIRECTORY] BOARD_ID
  localswim board export [--type json] BOARD_ID
  localswim service status
  localswim service builds [--limit N] [--cursor CURSOR]

Live commands use LOCALSWIM_URL (http://127.0.0.1:<port>) and LOCALSWIM_TOKEN.
Set --data on board verify to read the private board data offline.
Board export writes JSON to standard output. The default --type is json.
Workspace pins accepts a JSON object with pinned_board_ids in the desired order.
Omit --if-match to read the current resource ETag once before updating.
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

var boardIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type cliCommand struct {
	method, resource, boardID, ifMatch, dataDir string
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
		fmt.Fprintln(stderr, err)
		fmt.Fprint(stderr, cliHelp)
		return 2
	}
	if err := executeCLI(command, stdin, stdout, getenv); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func parseCLI(args []string) (cliCommand, error) {
	var command cliCommand
	if len(args) < 2 {
		return command, errors.New("a command group and action are required")
	}

	flags := flag.NewFlagSet("localswim", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	needBoardID := false
	paginated := false
	format := exportJSON
	switch args[0] + " " + args[1] {
	case "workspace get":
		command.method, command.resource = http.MethodGet, "/workspace"
	case "workspace pins":
		command.method, command.resource = http.MethodPatch, "/workspace"
	case "board list":
		command.method, command.resource = http.MethodGet, "/boards"
		paginated = true
	case "board create":
		command.method, command.resource = http.MethodPost, "/boards"
	case "board get":
		command.method, command.resource, needBoardID = http.MethodGet, "/boards", true
	case "board patch":
		command.method, command.resource, needBoardID = http.MethodPatch, "/boards", true
	case "board verify":
		command.method, command.resource, needBoardID = http.MethodGet, "/verifications", true
		flags.StringVar(&command.dataDir, "data", "", "private data directory")
	case "board export":
		command.method, command.resource, needBoardID = http.MethodGet, "/snapshots", true
		flags.Var(&format, "type", "export type (allowed value: json)")
	case "service status":
		command.method, command.resource = http.MethodGet, "/service"
	case "service builds":
		command.method, command.resource = http.MethodGet, "/builds"
		paginated = true
	default:
		return command, errors.New("unknown command")
	}

	if command.method == http.MethodPatch {
		flags.StringVar(&command.ifMatch, "if-match", "", "strong resource ETag")
	}
	var limit int
	var cursor string
	if paginated {
		flags.IntVar(&limit, "limit", 50, "page size")
		flags.StringVar(&cursor, "cursor", "", "cursor from the preceding page")
	}
	if err := flags.Parse(args[2:]); err != nil {
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
	flags.Visit(func(f *flag.Flag) {
		if f.Value.String() == "" {
			optionErr = errors.New("command options require nonempty values")
		}
	})
	if optionErr != nil {
		return command, optionErr
	}
	if needBoardID {
		if flags.NArg() != 1 || !boardIDPattern.MatchString(flags.Arg(0)) {
			return command, errors.New("expected one UUIDv4 board ID")
		}
		command.boardID = flags.Arg(0)
		command.resource += "/" + command.boardID
	} else if flags.NArg() != 0 {
		return command, errors.New("unexpected command arguments")
	}
	if command.ifMatch != "" && !client.IsEntityTag(command.ifMatch) {
		return command, errors.New("--if-match requires one quoted strong ETag")
	}
	return command, nil
}

func executeCLI(command cliCommand, stdin io.Reader, stdout io.Writer, getenv func(string) string) error {
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
	var body json.RawMessage
	if command.method == http.MethodPost || command.method == http.MethodPatch {
		body, err = io.ReadAll(io.LimitReader(stdin, maxCLIInput+1))
		if err != nil || len(body) > maxCLIInput || !client.IsJSONObject(body) {
			return errors.New("standard input must contain one valid JSON object within 96 MiB")
		}
	}
	if command.method == http.MethodPatch && command.ifMatch == "" {
		current, err := api.Do(http.MethodGet, command.resource, nil, "")
		if err != nil {
			return err
		}
		if current.ETag == "" {
			return errors.New("service did not return a strong resource ETag")
		}
		command.ifMatch = current.ETag
	}
	reply, err := api.Do(command.method, command.resource, body, command.ifMatch)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, string(reply.JSON)); err != nil {
		if body != nil {
			return errors.New("service accepted the mutation, but its response could not be written")
		}
		return errors.New("could not write the API response")
	}
	return nil
}
