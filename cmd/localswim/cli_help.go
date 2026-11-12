package main

import (
	"strings"
)

// commandUsage is the shared source for root, group, action, and error usage.
const commandUsage = `Usage:
  localswim workspace info
  localswim workspace pin set [--revision N] BOARD_ID [BOARD_ID ...]
  localswim workspace pin add [--revision N] BOARD_ID [BOARD_ID ...]
  localswim workspace pin remove [--revision N] BOARD_ID [BOARD_ID ...]
  localswim workspace publishing enable [--revision N]
  localswim workspace publishing disable [--revision N]
  localswim workspace publishing status
  localswim board list [--limit N] [--cursor CURSOR]
  localswim board create --name NAME [--preset terry]
  localswim board create --json <JSON_FILE|->
  localswim board get [--view lanes] [--data DIRECTORY] BOARD_ID
  localswim board rename [--revision N] BOARD_ID NEW_NAME
  localswim board close [--revision N] BOARD_ID
  localswim board reopen [--revision N] BOARD_ID
  localswim board patch [--revision N] BOARD_ID <JSON_FILE|->
  localswim board publishing enable [--revision N] BOARD_ID
  localswim board publishing disable [--revision N] BOARD_ID
  localswim board publishing status BOARD_ID
  localswim board notifications target [--revision N] BOARD_ID THREAD_UUID
  localswim board notifications enable [--revision N] BOARD_ID
  localswim board notifications disable [--revision N] BOARD_ID
  localswim board notifications status BOARD_ID
  localswim board recover --mode <same_id|fresh> BOARD_ID <FILE|->
  localswim board verify [--data DIRECTORY] BOARD_ID
  localswim board export [--type json] [--data DIRECTORY] BOARD_ID <FILE|->
  localswim card create [--revision N] <JSON_FILE|->
  localswim card create [--revision N] --title TITLE [--description TEXT] BOARD_ID LANE_ID
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
  localswim card set-priorities [--revision N] BOARD_ID PRIORITY_ID CARD_ID [CARD_ID ...]
  localswim card set-priorities [--revision N] --tickets BOARD_ID PRIORITY_ID TICKET [TICKET ...]
  localswim card description [--revision N] CARD_ID FILE|-
  localswim card archive [--revision N] CARD_ID
  localswim card archive [--revision N] BOARD_ID TICKET
  localswim card unarchive [--revision N] CARD_ID
  localswim card unarchive [--revision N] BOARD_ID TICKET
  localswim card cover [--revision N] CARD_ID ATTACHMENT_ID|none
  localswim card set-parent [--revision N] BOARD_ID TICKET PARENT_TICKET
  localswim card clear-parent [--revision N] BOARD_ID TICKET
  localswim relationship list [--limit N] [--cursor CURSOR] BOARD_ID [CARD_ID]
  localswim relationship get RELATIONSHIP_ID
  localswim relationship create [--revision N] [--id UUID] SOURCE_CARD_ID KIND TARGET_CARD_ID
  localswim relationship create [--revision N] [--id UUID] BOARD_ID SOURCE_TICKET KIND TARGET_TICKET
  localswim relationship patch [--revision N] RELATIONSHIP_ID FILE|-
  localswim relationship remove [--revision N] RELATIONSHIP_ID
  localswim relationship remove [--revision N] BOARD_ID SOURCE_TICKET KIND TARGET_TICKET
  localswim comment list [--data DIRECTORY] [--archived exclude|include|only]
                         [--limit N] [--cursor CURSOR] BOARD_ID [CARD_ID]
  localswim comment post [--revision N] CARD_ID <JSON_FILE|->
  localswim comment post [--revision N] --text TEXT CARD_ID
  localswim activity list [--data DIRECTORY] --from UTC --to UTC [--view full|monitor]
                        [--ticket N] [--card-id CARD_ID]
                        [--writer-id WRITER_ID] [--actor-id UUID] [--operation OP] [--limit N]
                        [--cursor CURSOR] BOARD_ID
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
  localswim attachment upload [--revision N] [--id UUID] [--filename NAME] BOARD_ID TICKET <FILE|->
  localswim attachment detach [--revision N] ATTACHMENT_ID
  localswim attachment detach [--revision N] BOARD_ID TICKET ATTACHMENT_ID
  localswim policy get BOARD_ID
  localswim policy grant --revision N --create BOARD_ID USER_ID LANE_ID
  localswim policy grant --revision N --move BOARD_ID USER_ID FROM_LANE_ID TO_LANE_ID
  localswim policy grant [--revision N] BOARD_ID <JSON_FILE|->
  localswim ticket-order preview LANE_ID [CARD_ID ...]
  localswim backup create [--revision N] [--id UUID] BOARD_ID
  localswim backup info BACKUP_ID
  localswim backup download BACKUP_ID FILE|-
  localswim backup verify <FILE|->
  localswim card search [--data DIRECTORY] [--search TEXT] [--search-content] [--lane ID]...
                       [--archived exclude|include|only] [--limit N] [--cursor CURSOR] BOARD_ID
  localswim card triage-order [--data DIRECTORY] [--search TEXT] [--search-content] [--lane ID]...
                             [--archived exclude|include|only] [--limit N] [--cursor CURSOR] BOARD_ID
  localswim card lookup [--search TEXT] [--search-content] [--lane ID] [--limit N] [--cursor CURSOR] BOARD_ID
  localswim ticket-order apply LANE_ID FILE|-
  localswim board restore [--revision N] BOARD_ID FILE|-
  localswim restoration create [--revision N] [--id UUID] BOARD_ID BACKUP_ID
  localswim restoration get RESTORATION_ID
  localswim publication create [--id UUID] [--remote NAME] [--branch BRANCH] BOARD_ID BACKUP_ID
  localswim publication get PUBLICATION_ID
  localswim service status
  localswim service builds [--limit N] [--cursor CURSOR]
  localswim service activate [--id UUID] BUILD_ID
  localswim service activation ACTIVATION_ID
  localswim service stop
`

var cliHelp = writeCommandHelp("")
var cardCreateHelp = writeCommandHelp("card create")
var commentPostHelp = writeCommandHelp("comment post")
var priorityBatchHelp = writeCommandHelp("card set-priorities")
var boardCreateHelp = writeCommandHelp("board create")
var policyGrantHelp = writeCommandHelp("policy grant")

// Help is resolved before parsing only for command paths, never for an option
// value or positional input that happens to contain a help token.
func requestedCLIHelp(args []string) (string, bool) {
	if len(args) == 0 {
		return cliHelp, true
	}
	path := args
	explicit := false
	if args[0] == "help" {
		path, explicit = args[1:], true
	} else if last := args[len(args)-1]; last == "--help" || last == "-h" {
		path, explicit = args[:len(args)-1], true
	}
	name, ok := cliHelpPath(path)
	if !ok {
		return "", false
	}
	help := writeCommandHelp(name)
	if help == "" {
		return "", false
	}
	if explicit || (len(args) == 1 && !hasExactCommand(name)) {
		return help, true
	}
	return "", false
}

func contextualCLIHelp(args []string) string {
	for length := len(args); length > 0; length-- {
		name, ok := cliHelpPath(args[:length])
		if !ok {
			continue
		}
		if help := writeCommandHelp(name); help != "" {
			return help
		}
	}
	return cliHelp
}

func cliHelpPath(tokens []string) (string, bool) {
	for _, token := range tokens {
		if token == "" || strings.ContainsRune(token, ' ') {
			return "", false
		}
	}
	return strings.Join(tokens, " "), true
}

func commandUsageLines() []string {
	var lines []string
	for _, line := range strings.Split(commandUsage, "\n") {
		if strings.HasPrefix(line, "  localswim ") {
			lines = append(lines, strings.TrimSpace(line))
		} else if strings.TrimSpace(line) != "" && line != "Usage:" && len(lines) > 0 {
			lines[len(lines)-1] += " " + strings.TrimSpace(line)
		}
	}
	return lines
}

func usageCommand(line string) string {
	fields := strings.Fields(strings.TrimPrefix(line, "localswim "))
	count := 2
	if len(fields) > 2 && (fields[1] == "pin" || fields[1] == "publishing" || fields[1] == "notifications") {
		count = 3
	}
	return strings.Join(fields[:count], " ")
}

func hasExactCommand(name string) bool {
	for _, line := range commandUsageLines() {
		if usageCommand(line) == name {
			return true
		}
	}
	return false
}

func writeCommandHelp(name string) string {
	var selected []string
	for _, line := range commandUsageLines() {
		command := usageCommand(line)
		if name == "" || command == name || strings.HasPrefix(command, name+" ") {
			selected = append(selected, line)
		}
	}
	if len(selected) == 0 {
		return ""
	}
	var output strings.Builder
	output.WriteString("Usage:\n")
	for _, line := range selected {
		output.WriteString("  " + line + "\n")
	}
	output.WriteString("\nAppend --help to an object or action for scoped help. Use help OBJECT ACTION as an alternative.\n")
	output.WriteString("Options precede positional arguments. Uppercase arguments are required; brackets mark optional inputs.\n")
	if name == "" {
		output.WriteString("Live commands use LOCALSWIM_URL (http://127.0.0.1:<port>) and LOCALSWIM_TOKEN.\n")
		output.WriteString("Help and usage errors do not contact the service. Help exits 0, usage errors 2, execution failures 1.\n")
		return output.String()
	}
	if notes := scopedHelpNotes(name); notes != "" {
		output.WriteString("\n" + notes + "\n")
	}
	joined := strings.Join(selected, "\n")
	if strings.Contains(joined, "FILE") {
		output.WriteString("\nInput FILE or JSON_FILE requires an explicit path or - for standard input. Output FILE requires a new path or - for standard output.\n")
	}
	if strings.Contains(joined, "--data") {
		output.WriteString("--data DIRECTORY reads a validated saved snapshot offline. Omit it to use the live service.\n")
	}
	if strings.Contains(joined, "--revision") {
		output.WriteString("--revision N requires the previously inspected positive integer revision. Omit it to use the current validator.\n")
		if revision := commandRevision(name); revision != "" {
			output.WriteString("This command checks the " + revision + " revision.\n")
		}
		output.WriteString("After an unknown write outcome, inspect the original resource before retrying.\n")
	}
	if hasExactCommand(name) {
		example := commandExamples[name]
		if example == "" {
			example = "  " + usageExample(selected[0])
		}
		output.WriteString("\nExamples:\n" + example + "\n")
	}
	return output.String()
}

func scopedHelpNotes(name string) string {
	for name != "" {
		if notes := commandHelpNotes[name]; notes != "" {
			return notes
		}
		index := strings.LastIndexByte(name, ' ')
		if index < 0 {
			break
		}
		name = name[:index]
	}
	return ""
}

// Examples use shell variables for object identifiers and concrete file paths.
func usageExample(usage string) string {
	var words []string
	depth := 0
	for _, word := range strings.Fields(usage) {
		previous := depth
		depth += strings.Count(word, "[") - strings.Count(word, "]")
		if previous > 0 || strings.Contains(word, "[") {
			continue
		}
		word = strings.Trim(word, "<>")
		switch {
		case strings.Contains(word, "FILE"):
			word = "input.json"
		case word == "enabled|disabled":
			word = "enabled"
		case strings.ToUpper(word) == word:
			word, _, _ = strings.Cut(word, "|")
			word = "$" + word
		}
		words = append(words, word)
	}
	return strings.Join(words, " ")
}

func commandRevision(name string) string {
	switch name {
	case "board notifications target", "board notifications enable", "board notifications disable":
		return "notification routing"
	case "workspace pin set", "workspace pin add", "workspace pin remove", "workspace publishing enable", "workspace publishing disable":
		return "workspace"
	case "lane rename", "lane set-key", "lane retire", "lane set-approval-button":
		return "lane"
	case "relationship patch", "relationship remove":
		return "relationship"
	case "attachment detach":
		return "attachment"
	case "card set-parent", "card clear-parent":
		return "child card"
	case "card create", "card set-priorities", "relationship create", "lane add", "policy grant", "backup create", "restoration create", "board restore", "board rename", "board patch", "board close", "board reopen", "board publishing enable", "board publishing disable":
		return "board"
	}
	if strings.HasPrefix(name, "card ") || name == "comment post" || name == "attachment upload" {
		return "card"
	}
	return ""
}

var commandHelpNotes = map[string]string{
	"board create":             "Choose exactly one of --name or --json. Use --name alone for a generic workflow or --preset terry for Terry's seven-lane workflow. --json reads a full custom workflow document.",
	"board get":                "The default returns board metadata. --view lanes returns active lanes and visible cards in lane order and requires the live service.",
	"board verify":             "Print the full verification report. An invalid board exits 1. Verification does not change saved state.",
	"board recover":            "Recover a board that failed startup from a verified native JSON snapshot or ZIP backup. Use same_id when trusted continuity is available or fresh to create a new board identity. Recovery leaves publishing disabled. Inspect board list and the recovery diagnostic after an unknown result.",
	"board export":             "--type accepts json and defaults to json. Export writes the validated native snapshot.",
	"board restore":            "Read a native JSON snapshot or ZIP backup. ZIP input streams from private staging and can exceed 96 MiB. JSON retains its 96 MiB limit. Inspect the accepted board after an uncertain result.",
	"board close":              "Drain admitted board operations and flush enabled publishing before closing. Inspect board get after an unknown outcome.",
	"board reopen":             "Reopen the saved board at its existing address. Inspect board get after an unknown outcome.",
	"board notifications":      "Set a verified owning-chat target before enabling. Target changes preserve the enabled state. Disable retains the target. Inspect status after an uncertain settings write.",
	"board publishing":         "Enable or disable board publishing, or inspect its pending work, latest success and failure code. Inspect status after an uncertain settings write.",
	"workspace publishing":     "Enable or disable workspace publishing, or inspect its status. Inspect status after an uncertain settings write.",
	"workspace pin":            "Supply at least one board UUID. Set replaces the order; add appends new boards in order. Remove preserves the remaining order and reports failed targets as warnings.",
	"card create":              "The concise form uses the board's default owner and priority. The JSON document must include board_id and may include an explicit card ID for readback after a lost reply.",
	"card inspect":             "Supply a board UUID and positive ticket number. Includes archived cards. --prose includes the selected card's description and attributed comments.",
	"card get":                 "Choose a card UUID or a board UUID followed by its positive ticket number. Includes archived cards.",
	"card move":                "Choose a card UUID or a board UUID and ticket, followed by the destination lane UUID. Ticket form also guards the board version from the focused read.",
	"card assign":              "Choose a card UUID or a board UUID and ticket, followed by a board-member owner UUID. Ticket form also guards the board version.",
	"card archive":             "Choose a card UUID or a board UUID and ticket. Ticket form also guards the board version. Archived cards retain their contents and history.",
	"card unarchive":           "Choose a card UUID or a board UUID and ticket. Ticket form also guards the board version. Retired lanes refuse unarchive.",
	"card description":         "Read UTF-8 description text verbatim, including trailing newlines.",
	"card cover":               "Use an active attachment UUID, or none to clear the cover.",
	"card set-priorities":      "Supply 1 to 1000 distinct card UUIDs or, with --tickets, distinct positive ticket numbers. All selected cards must belong to the board. The batch applies effective blocking floors to all selections together.",
	"card search":              "Match literal titles and tickets. --search-content includes readable descriptions and comments. Repeat --lane for multiple lanes. Results use whole-board triage ordering.",
	"card triage-order":        "Return the highest-ranked unfinished card by default. --limit requests more. Repeat --lane for multiple lanes. --search-content includes readable descriptions and comments. This command makes no changes.",
	"card lookup":              "Return matching cards in numeric ticket order. --search-content includes readable descriptions and comments.",
	"comment post":             "Quote literal --text. The JSON form reads an object with text and an optional id.",
	"comment list":             "Return newest comments with attribution and full text. --limit is 1 to 200, default 50. --archived accepts exclude, include, or only. Board-wide reports default to active cards; focused card reports include archived cards by default.",
	"relationship create":      "Choose source and target card UUIDs, or a board UUID and source and target tickets. KIND is blocks, duplicates, references, or relates_to. --id accepts a UUIDv4.",
	"relationship remove":      "Choose a relationship UUID, or a board UUID and source ticket, kind and target ticket. KIND is blocks, duplicates, references, or relates_to. Success produces no output.",
	"relationship patch":       "Read a JSON object with kind. KIND is blocks, duplicates, references, or relates_to.",
	"attachment upload":        "Read raw bytes, up to 10 MiB. Choose a card UUID or a board UUID and ticket. --id accepts a UUIDv4. --filename supplies a UTF-8 display name. By default a file uses its basename and stdin uses the attachment UUID. The card revision is checked before bytes are read. Ticket form also guards the board version. Inspect attachment get with the original ID after an unknown outcome.",
	"attachment download":      "Verify the active file before writing raw bytes. Inspect the destination after a local write failure.",
	"attachment detach":        "Choose an attachment UUID, or a board UUID, ticket and attachment UUID. Ticket form checks card ownership and guards the board version. Success produces no output. Inspect attachment get after an unknown outcome.",
	"policy grant":             "Add one creation or movement grant using board-specific UUIDv4 IDs. Concise --create and --move forms require --revision. JSON accepts create_grants and/or move_grants. All forms preserve other permissions. Use policy get to inspect the result.",
	"ticket-order preview":     "Select all active cards in the lane, or an explicit card UUID subset. Preview returns the original board validator and proposed ticket numbers without changing the board.",
	"ticket-order apply":       "Read the saved preview, including its original validator and exact proposed order. Stale previews refuse the entire operation.",
	"backup create":            "Capture the board and audited attachment bytes. --id accepts a UUIDv7; omission generates one. Inspect backup info with the original ID after an unknown outcome.",
	"backup verify":            "Verify ZIP backup bytes locally without a service. Stdin is copied to a private temporary file for verification and removed afterward.",
	"backup download":          "Verify backup size and SHA-2-256 before writing bytes.",
	"restoration create":       "Restore a stored backup. --id accepts a UUIDv7; omission generates one. Inspect restoration get with the original ID after an unknown outcome.",
	"publication create":       "Publish a stored backup. --id accepts a UUIDv7; omission generates one. Optional --remote and --branch select the Git destination. Inspect publication get with the original ID after an unknown outcome.",
	"service activate":         "Activate a staged build with the current service validator. --id accepts a UUIDv7; omission generates one. After an unknown outcome refresh the token as needed and inspect service activation with the original ID.",
	"service stop":             "Gracefully drain the service and flush enabled publishing. After an unknown outcome, inspect service status.",
	"activity list":            "--from and --to are required inclusive UTC bounds in YYYY-MM-DD HH:MM:SS+00:00. Quote timestamps. --view accepts monitor (default) or full. Saved data supports monitor only. --limit is 1 to 200, default 50. Continue with the same filters and --cursor. Start owner-notice inspection with its board, writer and interval.",
	"lane set-approval-button": "Choose enabled or disabled. Only the Ready for Review lane can enable approval.",
	"lane retire":              "Retain lane identity and archived history. Active occupancy and the Completed lane refuse retirement.",
}

var commandExamples = map[string]string{
	"board create":        "  localswim board create --name \"Pool projects\" --preset terry\n  localswim board create --json workflow.json",
	"board recover":       "  localswim board recover --mode same_id $board backup.zip\n  localswim board recover --mode fresh $board snapshot.json",
	"card create":         "  localswim card create --title \"Check pool hours\" $board $lane\n  localswim card create --revision 12 card.json",
	"card inspect":        "  localswim card inspect --prose $board 12",
	"attachment upload":   "  localswim attachment upload --revision 12 $board 17 photo.png\n  localswim attachment upload --filename photo.png $card -",
	"comment post":        "  localswim comment post --text \"Confirmed weekend hours.\" $card\n  localswim comment post --revision 13 $card comment.json",
	"card set-priorities": "  localswim card set-priorities --tickets $board $priority 12 17\n  localswim card set-priorities --revision 14 $board $priority $card1 $card2",
	"policy grant":        "  localswim policy grant --revision 4 --create $board $user $lane\n  localswim policy grant --revision 5 --move $board $user $from $to",
	"activity list":       "  localswim activity list --from \"2026-11-11 00:00:00+00:00\" --to \"2026-11-11 01:00:00+00:00\" $board",
}
