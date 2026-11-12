# localswim

A local swimlane board for organizing work. V2 serves the workspace and its boards
through one loopback service and port.

**[Roadmap and scope](ROADMAP.md)**

## Getting started

Use Go 1.27 to build the service and CLI.

For a console-free Windows installation, see [Run localswim V2 independently on Windows](docs/windows-operations.md).

```powershell
go build -o localswim.exe ./cmd/localswim
```

Set `LOCALSWIM_HUMAN_TOKEN` and `LOCALSWIM_BOT_TOKEN` in the server's environment.
Use distinct random base64url values of at least 32 bytes for each server start.
Start the service with a fresh data directory.

```powershell
.\localswim.exe serve --data C:\absolute\path\to\fresh-v2-data --port 0
```

Use `--port NUMBER` to select a port, or `--port 0` (the default) to choose an
available port automatically. The service prints its local URL. In your CLI
terminal, set `LOCALSWIM_URL` to that URL and `LOCALSWIM_TOKEN` to the chosen
Human or Bot token. `service status` reports the actual listening port.

```powershell
.\localswim.exe board create --name "Personal"
.\localswim.exe board list
.\localswim.exe workspace info
.\localswim.exe --help
```

The name-only command creates a board with the generic five-lane workflow and P2
Normal. Add `--preset terry` to create a board with Terry's workflow.

```powershell
$terry = .\localswim.exe board create --name "Terry workflow" --preset terry | ConvertFrom-Json
```

The preset adds Blocked and Needs Terry, uses P0 through P5 with P3 Normal,
and assigns new cards to Bot by default. It enables dependency reorder in Backlog
and Human bulk approval in Ready For Review. Terry and Bot can create cards in
Backlog, Ready For Work, Blocked, and Needs Terry. Terry alone can move a card
from Ready For Review to Completed.

Use `board create --json definition.json` with a `workflow` object to define
custom lanes, priorities, and grants. Lane ordering and triage use each
priority's numeric `rank`. For the Terry preset the rank equals the P-number.
Commands that read JSON require a file path or `-` for standard input.

Use `policy grant` to add one creation or movement grant. Read the revision and
user IDs with `board get` and the lane IDs with `board get --view lanes`.
For example, to allow Bot to move cards directly from Backlog to In Progress on
a Terry board, use the default owner's ID and those two lane UUIDs.

```powershell
$board = .\localswim.exe board get $terry.id | ConvertFrom-Json
$lanes = (.\localswim.exe board get --view lanes $terry.id | ConvertFrom-Json).lanes
$backlog = ($lanes | Where-Object key -eq backlog).id
$inProgress = ($lanes | Where-Object key -eq in_progress).id
.\localswim.exe policy grant --revision $board.revision --move $board.id $board.default_owner_id $backlog $inProgress
```

For a creation grant, use `--create BOARD_ID USER_ID LANE_ID` with `--revision`.
A stale board revision refuses the edit. For multiple grants, use
`policy grant BOARD_ID grants.json` with `create_grants` and/or `move_grants`
arrays. The service checks both the board and policy validators on every grant
and preserves the other grants and default priority.

Export a board with `board export BOARD_ID FILE|-`. Use a new file path, or `-`
for standard output. The default format is JSON.

Use `board verify BOARD_ID` to verify the durable board through the service or
`board verify --data DIRECTORY BOARD_ID` to read the local snapshot offline.
Both forms write the full JSON report to standard output. A valid report exits
with status 0. An invalid report exits with status 1 and includes the issues.
For a board quarantined at startup, the live report is available to the Human
workspace administrator. Its `recovery` field gives `same_id` with a trusted
`revision_floor` or `fresh_only` if same-ID continuity cannot be established.
Use `board recover --mode same_id BOARD_ID FILE|-` with a verified native snapshot
or ZIP to preserve a trusted identity. Explicit `--mode fresh` creates a new board
identity and keeps the original failure available. Both modes leave publishing
disabled until explicitly enabled. Inspect `board list` and verification after an uncertain reply before another attempt.

Add `--data DIRECTORY` before the board ID on `card triage-order`, `card search`,
`card inspect`, or `comment list` to use the same report against the saved
snapshot while the writer is stopped. Add `--prose` to include descriptions
and comments in focused ticket inspection.

Use `activity list --data DIRECTORY --from UTC --to UTC BOARD_ID` to inspect
saved activity while the writer is stopped. Quote both inclusive UTC bounds in
`YYYY-MM-DD HH:MM:SS+00:00` format. The report includes sanitized metadata,
event-time tickets, the captured board revision, and the latest retained event
time. Saved reports always mark live coverage as incomplete.

### Verify a board-change notice

After 15 seconds of quiet, an enabled owner notice lists the writer, UTC interval,
and up to 128 card UUID and event-time ticket pairs. Its **Operations** line
groups the retained tickets by committed Human edits. An approval and its receipt
comment appear together under **Reviewed & approved**. Ordinary moves, comments,
and archive or restore actions have their own groups.

Use the notice's board UUID, writer UUID, and Start and End values to read
sanitized activity, including each event's `operation_group`.

```powershell
.\localswim.exe activity list --writer-id $writer --from $start --to $end $board
```

Continue with `--cursor` and the same query until pagination is complete. Check
`coverage.complete` before relying on the interval. For a focused follow-up,
match the card UUID and its ticket at the event, which may differ from its
current ticket. Treat operation groups as triage hints and follow the board's
work and review gates.

[Development and conventions](CONTRIBUTING.md) · [API](docs/openapi.yaml) · [Milestones](milestone-plan.md)
