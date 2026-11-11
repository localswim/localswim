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
)

const cliHelp = `Usage:
  localswim workspace info
  localswim workspace pin set [--revision N] BOARD_ID [BOARD_ID ...]
  localswim workspace pin add [--revision N] BOARD_ID [BOARD_ID ...]
  localswim workspace pin remove [--revision N] BOARD_ID [BOARD_ID ...]
  localswim workspace publishing enable [--revision N]
  localswim workspace publishing disable [--revision N]
  localswim workspace publishing status
  localswim board list [--limit N] [--cursor CURSOR]
  localswim board create --name NAME
  localswim board create --json <JSON_FILE|->
  localswim board get [--view lanes] [--data DIRECTORY] BOARD_ID
  localswim board rename [--revision N] BOARD_ID NEW_NAME
  localswim board close [--revision N] BOARD_ID
  localswim board reopen [--revision N] BOARD_ID
  localswim board patch [--revision N] BOARD_ID <JSON_FILE|->
  localswim board publishing enable [--revision N] BOARD_ID
  localswim board publishing disable [--revision N] BOARD_ID
  localswim board publishing status BOARD_ID
  localswim board verify [--data DIRECTORY] BOARD_ID
  localswim board export [--type json] [--data DIRECTORY] BOARD_ID <FILE|->
  localswim card create [--revision N] <JSON_FILE|->
  localswim card list [--archived exclude|include|only] [--limit N] [--cursor CURSOR] BOARD_ID
  localswim card get CARD_ID
  localswim card get BOARD_ID TICKET
  localswim card inspect [--prose] [--data DIRECTORY] BOARD_ID TICKET
  localswim card patch [--revision N] CARD_ID FILE|-
  localswim card move [--revision N] CARD_ID LANE_ID
  localswim card move [--revision N] BOARD_ID TICKET LANE_ID
  localswim card assign [--revision N] CARD_ID OWNER_ID
  localswim card assign [--revision N] BOARD_ID TICKET OWNER_ID
  localswim card title [--revision N] CARD_ID TITLE
  localswim card set-priority [--revision N] CARD_ID PRIORITY_ID
  localswim card set-priorities [--revision N] BOARD_ID FILE|-
  localswim card description [--revision N] CARD_ID FILE|-
  localswim card archive [--revision N] CARD_ID
  localswim card archive [--revision N] BOARD_ID TICKET
  localswim card unarchive [--revision N] CARD_ID
  localswim card unarchive [--revision N] BOARD_ID TICKET
  localswim card cover [--revision N] CARD_ID ATTACHMENT_ID|none
  localswim relationship list [--limit N] [--cursor CURSOR] BOARD_ID [CARD_ID]
  localswim relationship get RELATIONSHIP_ID
  localswim relationship create [--revision N] [--id UUID] SOURCE_CARD_ID KIND TARGET_CARD_ID
  localswim relationship patch [--revision N] RELATIONSHIP_ID FILE|-
  localswim relationship remove [--revision N] RELATIONSHIP_ID
  localswim comment list [--archived exclude|include|only] [--limit N] [--cursor CURSOR] BOARD_ID [CARD_ID]
  localswim comment post [--revision N] CARD_ID FILE|-
  localswim activity list --from UTC --to UTC [--view full|monitor] [--ticket N]
                        [--actor-id UUID] [--operation OP] [--limit N] [--cursor CURSOR] BOARD_ID
  localswim lane add [--revision N] BOARD_ID FILE|-
  localswim lane get LANE_ID
  localswim lane rename [--revision N] LANE_ID LABEL
  localswim lane set-key [--revision N] LANE_ID KEY
  localswim lane retire [--revision N] LANE_ID
  localswim lane set-approval-button [--revision N] LANE_ID enabled|disabled
  localswim attachment list [--limit N] [--cursor CURSOR] CARD_ID
  localswim attachment get ATTACHMENT_ID
  localswim attachment download ATTACHMENT_ID FILE|-
  localswim attachment upload [--revision N] [--id UUID] [--filename NAME] CARD_ID <FILE|->
  localswim attachment detach [--revision N] ATTACHMENT_ID
  localswim policy get BOARD_ID
  localswim policy grant [--revision N] BOARD_ID <JSON_FILE|->
  localswim ticket-order preview LANE_ID [CARD_ID ...]
  localswim backup create [--revision N] [--id UUID] BOARD_ID
  localswim backup info BACKUP_ID
  localswim backup download BACKUP_ID FILE|-
  localswim backup verify FILE
  localswim card search [--search TEXT] [--search-content] [--lane ID]...
                       [--archived exclude|include|only] [--limit N] [--cursor CURSOR] BOARD_ID
  localswim card triage-order [--search TEXT] [--search-content] [--lane ID]...
                     [--archived exclude|include|only] [--limit N] [--cursor CURSOR] BOARD_ID
  localswim card lookup [--search TEXT] [--search-content] [--lane ID] [--limit N] [--cursor CURSOR] BOARD_ID
  localswim ticket-order apply LANE_ID FILE|-
  localswim board restore [--revision N] BOARD_ID FILE|-
  localswim restoration create [--revision N] [--id UUID] BOARD_ID BACKUP_ID
  localswim restoration get RESTORATION_ID
  localswim publication create [--id UUID] BOARD_ID BACKUP_ID
  localswim publication get PUBLICATION_ID
  localswim service status
  localswim service builds [--limit N] [--cursor CURSOR]
  localswim service activate [--id UUID] BUILD_ID
  localswim service activation ACTIVATION_ID
  localswim service stop

Live commands use LOCALSWIM_URL (http://127.0.0.1:<port>) and LOCALSWIM_TOKEN.
Board creation requires exactly one of --name or --json.
--json reads a new board definition from JSON_FILE or standard input.
JSON_FILE is required. Use a file path or - to read standard input.
Set --data on board get, board export, card inspect, or board verify to read saved board data offline.
Offline board get returns board metadata. The lanes view requires a live service.
Verification prints the full JSON report and exits 1 if the board is invalid.
Board export writes JSON to a new FILE, or standard output when given -. The default --type is json.
All pin commands require at least one board ID. Pin set replaces the order.
Pin add appends new boards in the supplied order.
Pin remove skips failed targets with WARNING messages and preserves the remaining order.
Pin set validates the entire list and reports ERROR if any target fails.
Publishing commands return the full board or workspace JSON, including publishing status.
Board publishing uses the board revision. Workspace publishing uses the workspace revision.
Board close and reopen use the board revision and return the full board JSON.
Use board get BOARD_ID to inspect availability after an unknown close or reopen outcome.
Omit --revision to use the current resource validator.
Supply --revision to require the version you previously inspected.
For card create and policy grant, --revision refers to the board revision.
Card create reads JSON_FILE, or standard input when given as -.
Its JSON must include board_id. The CLI supplies the board revision validator.
Include an explicit card ID to allow focused readback after a lost create reply.
Policy grant reads create_grants and/or move_grants and preserves existing permissions.
Comment list excludes archived cards for a board and includes them when scoped to one card by default.
Comment post reads an object containing text and an optional ID.
Lane add reads the lane definition. The CLI supplies its board ID and validator.
Card edits, comment post, and attachment upload check the card revision.
Ticket-based move, assign, archive, and unarchive also check the board version from the focused read.
Lane add checks the board revision. Attachment detach checks the attachment revision.
Lane rename, set-key, retire, and set-approval-button check the lane revision.
Card set-priorities reads card_ids and priority_id and checks the board revision.
The batch evaluates all selected cards together and returns effective priorities and changes.
Board get --view lanes returns visible cards in their swimlane order.
Ticket-order preview defaults to all active lane cards. Optional card IDs select the ticket pool.
Preview is read-only and returns the board revision and proposed ticket numbers.
Ticket-order apply reads that saved preview and uses its original validator. Stale previews refuse.
Card search and triage-order use whole-board triage ordering. Lookup uses numeric ticket order.
Card triage-order returns the highest-ranked unfinished card by default and makes no changes.
Use --limit for more cards. Repeat --lane to select multiple lanes, including Completed.
--search matches literal titles and ticket numbers. --search-content includes readable descriptions and comments.
Board restore requires a native JSON snapshot or ZIP backup file, or - for raw stdin.
Direct restore reads the current durable snapshot validator before writing.
Use restoration create with a stored backup if the durable snapshot cannot be read.
Publication create publishes a stored backup.
Operation creates generate an ID when --id is omitted. Inspect the original operation ID after an unknown outcome.
Restoration get and publication get inspect that original operation without replaying it.
Service activate and stop use the current service validator.
Service activate generates an ID when --id is omitted.
After an unknown activation outcome, refresh LOCALSWIM_TOKEN as needed and use service activation with that ID.
After an unknown stop outcome, use service status to inspect availability.
Backup create captures the current board and audited attachment bytes and checks the board revision.
Backup create generates an ID when --id is omitted and reports it on an unknown outcome.
Backup info reads metadata for the supplied backup ID.
Use backup info with the original ID after an unknown capture outcome.
Backup download verifies its size and SHA-2-256 digest before writing a new file or stdout.
Backup verify checks a local ZIP and writes its verification report without contacting a service.
Activity intervals are inclusive. Quote both UTC bounds in YYYY-MM-DD HH:MM:SS+00:00.
Activity lists sanitized edit metadata by default. Use --view full for the attributed audit.
The CLI manages HTTP validators.
For input FILE|-, pass a file path or use - to read standard input.
For output FILE|-, pass a new file path or use - to write standard output.
Card description reads UTF-8 text verbatim, including trailing newlines.
Card list excludes archived cards by default. Focused card get includes them.
Card inspect returns ticket, lane, owner, priority, and relationship metadata. Use --prose for its description and comments.
Use card cover CARD_ID none to clear the selected cover.
Relationship create reads source_card_id, target_card_id, kind, and optional id.
Relationship patch reads kind. The CLI supplies the required validators.
For relationship create, --revision refers to the board revision.
For relationship patch and remove, --revision refers to the relationship revision.
Relationship remove exits silently on success.
Attachment download verifies the active file before writing. Use a new file path
or - for raw bytes on standard output. Inspect the destination after a local write failure.
If a mutation outcome is unknown, inspect the resource before retrying.
Attachment upload reads raw bytes from FILE or standard input with -.
--filename overrides the display filename for either input source.
Without --filename, FILE uses its basename and - uses the attachment UUID.
Attachment upload generates an ID when --id is omitted and reports the ID
on an unknown outcome. Use attachment get to inspect that exact ID.
Attachment detach exits silently on success. Use attachment get to check active
after an unknown detach outcome.
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
	inputPath                          string
	revision                           *uint64
	body                               json.RawMessage
	pinAction                          string
	boardAction                        string
	pinIDs                             []string
	warnings                           []string
	edit                               t03Edit
	relationshipWrite                  bool
	relationshipID                     string
	exportPath                         string
	downloadPath                       string
	verifyBoard                        bool
	detachAttachment                   bool
	t06Action                          string
	nativeRead                         string
	ticketInspection                   *cliTicketInspection
	ticketPatch                        *ticketCardPatchTarget
	t07Action                          string
	publishingAction                   string
	boardAvailability                  string
	serviceAction                      string
	serviceResourceID                  string
	serviceActivationID                string
	backupVerifyPath                   string
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
	if name == "backup verify" {
		flags := flag.NewFlagSet(name, flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		if err := flags.Parse(optionArgs); err != nil {
			return command, err
		}
		if flags.NArg() != 1 || flags.Arg(0) == "" || flags.Arg(0) == "-" {
			return command, errors.New("backup verify requires one local ZIP file path")
		}
		command.backupVerifyPath = flags.Arg(0)
		return command, nil
	}
	if name == "service activate" || name == "service activation" || name == "service stop" {
		return parseServiceAction(name, optionArgs)
	}
	if name == "board publishing" || name == "workspace publishing" {
		if len(optionArgs) == 0 {
			return command, errors.New("publishing requires enable, disable, or status")
		}
		switch optionArgs[0] {
		case "--help", "-h":
			return command, flag.ErrHelp
		case "enable", "disable", "status":
			command.publishingAction = optionArgs[0]
			optionArgs = optionArgs[1:]
		default:
			return command, errors.New("publishing requires enable, disable, or status")
		}
	}
	if name == "card search" || name == "card triage-order" || name == "card lookup" || name == "ticket-order apply" || name == "board restore" || strings.HasPrefix(name, "restoration ") || strings.HasPrefix(name, "publication ") {
		return parseT07(name, optionArgs)
	}
	if name == "card set-priorities" || name == "ticket-order preview" || strings.HasPrefix(name, "backup ") {
		return parseT06(name, optionArgs)
	}
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
	var boardView string
	paginated := false
	format := exportJSON
	switch name {
	case "board publishing", "workspace publishing":
		command.method, command.resource = http.MethodGet, "/workspace"
		if name == "board publishing" {
			command.resource, needBoardID = "/boards", true
		}
		switch command.publishingAction {
		case "enable":
			command.method, command.body = http.MethodPatch, json.RawMessage(`{"publishing":{"enabled":true}}`)
		case "disable":
			command.method, command.body = http.MethodPatch, json.RawMessage(`{"publishing":{"enabled":false}}`)
		}
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
		command.nativeRead = "metadata"
		flags.StringVar(&boardView, "view", "", "board view (lanes)")
		flags.StringVar(&command.dataDir, "data", "", "private data directory")
	case "board rename":
		command.method, command.resource, needBoardID = http.MethodPatch, "/boards", true
		rename = true
	case "board close", "board reopen":
		command.method, command.resource, needBoardID = http.MethodPatch, "/boards", true
		command.boardAvailability = "open"
		if name == "board close" {
			command.boardAvailability = "closed"
		}
		command.body = json.RawMessage(`{"availability":"` + command.boardAvailability + `"}`)
	case "board patch":
		command.method, command.resource, needBoardID = http.MethodPatch, "/boards", true
	case "board verify":
		command.verifyBoard = true
		command.method, command.resource, needBoardID = http.MethodGet, "/verifications", true
		flags.StringVar(&command.dataDir, "data", "", "private data directory")
	case "board export":
		command.method, command.resource, needBoardID = http.MethodGet, "/snapshots", true
		command.nativeRead = "export"
		flags.Var(&format, "type", "export type (allowed value: json)")
		flags.StringVar(&command.dataDir, "data", "", "private data directory")
	case "card create":
		command.method, command.resource = http.MethodPost, "/cards"
		command.boardAction = name
	case "card get":
		command.method, command.resource = http.MethodGet, "/cards"
	case "card inspect":
		var err error
		command.ticketInspection, err = parseTicketInspection(optionArgs)
		return command, err
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
	case "activity list":
		return parseActivityList(optionArgs)
	case "card list", "relationship list":
		return parseCLIList(name, optionArgs)
	case "relationship get", "relationship create", "relationship patch", "relationship remove", "attachment download":
		return parseT04(name, optionArgs)
	default:
		return parseT03(name, optionArgs)
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
	} else if creating && hasJSON {
		if flags.NArg() != 1 || flags.Arg(0) == "" {
			return command, errors.New("board create --json accepts one JSON file or - for standard input")
		}
		command.inputPath = flags.Arg(0)
	} else if name == "card create" {
		if flags.NArg() != 1 || flags.Arg(0) == "" {
			return command, errors.New("card create accepts one JSON file or - for standard input")
		}
		command.inputPath = flags.Arg(0)
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
		if name == "board export" {
			count, expected = 2, "board export requires a UUIDv4 board ID followed by FILE or - for standard output"
			command.exportPath = flags.Arg(1)
			if command.exportPath == "" {
				return command, errors.New(expected)
			}
		}
		if rename {
			count, expected = 2, "expected a UUIDv4 board ID followed by one new name (quote names containing spaces)"
		}
		if name == "board patch" || name == "policy grant" {
			command.inputPath = flags.Arg(1)
			if flags.NArg() != 2 || command.inputPath == "" {
				return command, errors.New("input file path must not be empty")
			}
			count = 2
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
		var err error
		command.revision, err = parseCLIRevision(revisionValue)
		if err != nil {
			return command, err
		}
	}
	if boardView != "" {
		if boardView != "lanes" {
			return command, errors.New("--view requires lanes")
		}
		if command.dataDir != "" {
			return command, errors.New("--view lanes requires a live board")
		}
		command.resource += "?view=lanes"
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
	if command.backupVerifyPath != "" {
		return verifyBackupFile(command.backupVerifyPath, stdout)
	}
	if command.exportPath != "" {
		return executeBoardExport(command, stdout, getenv)
	}
	if inspect := command.ticketInspection; inspect != nil && inspect.dataDir != "" {
		return executeNativeRead(inspect.dataDir, inspect.boardID, "ticket", inspect.ticket, inspect.prose, stdout)
	}
	if command.dataDir != "" && command.nativeRead != "" {
		return executeNativeRead(command.dataDir, command.boardID, command.nativeRead, 0, false, stdout)
	}
	if command.dataDir != "" {
		return executeOfflineVerification(command, stdout)
	}

	api, err := client.New(getenv("LOCALSWIM_URL"), getenv("LOCALSWIM_TOKEN"))
	if err != nil {
		return err
	}
	if command.ticketPatch != nil {
		return executeTicketCardPatch(api, command, stdout)
	}
	if command.ticketInspection != nil {
		return executeTicketInspection(api, command.ticketInspection, stdout)
	}
	if command.serviceAction != "" {
		return executeServiceAction(api, command, stdout)
	}
	if command.t07Action != "" {
		return executeT07(api, command, stdin, stdout)
	}
	if command.t06Action != "" {
		return executeT06(api, command, stdin, stdout)
	}
	if command.detachAttachment {
		return detachAttachment(api, command)
	}
	if command.downloadPath != "" {
		return downloadAttachment(api, command.resource, command.downloadPath, stdout)
	}
	body := command.body
	if command.inputPath != "" && command.inputPath != "-" {
		file, err := os.Open(command.inputPath)
		if err != nil {
			return fmt.Errorf("could not open input file: %w", err)
		}
		defer file.Close()
		stdin = file
	}
	var ifMatch, contentType, boardTag string
	if command.edit.validatorPath != "" {
		if command.edit.file != "" && command.edit.uploadID == "" {
			command.edit.uploadID = newUploadID()
		}
		body, ifMatch, contentType, err = prepareT03(api, command, stdin)
		if err != nil {
			return err
		}
	} else if body == nil && command.pinAction == "" && (command.method == http.MethodPost || command.method == http.MethodPatch || command.method == http.MethodPut) {
		body, err = io.ReadAll(io.LimitReader(stdin, maxCLIInput+1))
		if err != nil || len(body) > maxCLIInput || !client.IsJSONObject(body) {
			return errors.New("JSON input must contain one valid object within 96 MiB")
		}
	}
	if command.relationshipWrite {
		body, ifMatch, boardTag, err = prepareRelationshipWrite(api, command, body)
		if err != nil {
			return err
		}
	}
	if command.boardAction != "" {
		command.resource, body, ifMatch, err = prepareBoardAction(api, command, body)
		if err != nil {
			return err
		}
	}
	if command.method == http.MethodPatch && command.edit.validatorPath == "" && !command.relationshipWrite {
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
		if command.publishingAction != "" {
			if _, err := current.PublishingEnabled(command.boardID); err != nil {
				return err
			}
		}
		if command.boardAvailability != "" {
			if _, err := current.BoardAvailability(command.boardID); err != nil {
				return err
			}
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
	if command.method == http.MethodDelete {
		return api.RemoveRelationship(command.resource, ifMatch, boardTag)
	}
	var reply client.Response
	if command.boardAvailability != "" {
		reply, err = api.PatchBoardAvailability(command.resource, body, ifMatch)
	} else {
		reply, err = api.DoWithContentType(command.method, command.resource, body, ifMatch, contentType)
	}
	if err != nil {
		if command.relationshipID != "" && errors.Is(err, client.ErrMutationOutcomeUnknown) {
			return fmt.Errorf("%w. Read back with localswim relationship get %s before retrying", err, command.relationshipID)
		}
		if command.edit.uploadID != "" && errors.Is(err, client.ErrMutationOutcomeUnknown) {
			return fmt.Errorf("%w (attachment ID %s)", err, command.edit.uploadID)
		}
		if command.boardAvailability != "" && errors.Is(err, client.ErrMutationOutcomeUnknown) {
			return fmt.Errorf("%w. Read back with localswim board get %s", err, command.boardID)
		}
		return err
	}
	if command.relationshipID != "" && !confirmedRelationshipCreation(body, reply.JSON) {
		return fmt.Errorf("service did not confirm the requested relationship. %w. Read back with localswim relationship get %s before retrying", client.ErrMutationOutcomeUnknown, command.relationshipID)
	}
	if command.boardAvailability != "" {
		availability, err := reply.BoardAvailability(command.boardID)
		if err != nil || availability != command.boardAvailability {
			return fmt.Errorf("service did not confirm the requested board availability. %w. Read back with localswim board get %s before retrying",
				client.ErrMutationOutcomeUnknown, command.boardID)
		}
	}
	if command.publishingAction != "" {
		enabled, err := reply.PublishingEnabled(command.boardID)
		if command.method == http.MethodPatch && (err != nil || enabled != (command.publishingAction == "enable")) {
			return fmt.Errorf("service did not confirm the requested publishing state. %w. Inspect publishing status before retrying", client.ErrMutationOutcomeUnknown)
		}
		if err != nil {
			return err
		}
	}
	if command.verifyBoard {
		return writeLiveVerificationReport(stdout, reply, command.boardID)
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		if command.relationshipID != "" {
			return fmt.Errorf("service accepted relationship %s, but its response could not be written. Read back with localswim relationship get %s", command.relationshipID, command.relationshipID)
		}
		if command.edit.uploadID != "" {
			return fmt.Errorf("service accepted attachment %s, but its response could not be written", command.edit.uploadID)
		}
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
	if command.boardAction == "card create" {
		if json.Unmarshal(fields["board_id"], &command.boardID) != nil || !boardIDPattern.MatchString(command.boardID) {
			return "", nil, "", errors.New("card create JSON requires a UUIDv4 board_id")
		}
		command.boardID = strings.ToLower(command.boardID)
		if fields["expected_board_etag"] != nil {
			return "", nil, "", errors.New("card create supplies expected_board_etag")
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
			return nil, errors.New("JSON input must not contain repeated fields")
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
