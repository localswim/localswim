# localswim

## V2 release direction

- **Same product.** V1 is the reference for the UI, UX, and functionality.
  Build V2 fresh and match the browser, CLI, and integration behavior people
  rely on, including permissions, audit history, and reliable saves and recovery.
- **One port.** One loopback service serves the UI, workspace, and every board.
  The browser and CLI share a date-versioned REST API with flat resource URLs,
  for example `/api/2026-11-10/cards/{card_id}`. Each
  board has one authoritative writer. No per-board ports or forwarding gateway.
- **Fresh starts.** Run V1 and V2 side by side on separate ports with separate
  data directories. Existing projects stay in V1. New projects start in V2 when
  it is ready. No data migration or cutover.
- **Ship quickly.** Aim for a working board on day one and the three-day delivery target
  in the execution plan. Create a board, add and move cards, and restart with
  the changes intact. Check parity as we build. Focus testing on failures
  that can lose edits, break approvals, or prevent recovery.

- **Reboot aggressively.** Terry and Bot are the entire user base. Treat breaking
  changes as having zero external compatibility impact. Replace superseded
  contracts directly and keep the implementation ruthlessly lean. Owner-directed
  pivots can replace earlier decisions completely.

This direction supersedes the previous milestone plan, its broad baseline
campaign, and the import and cutover proposal.

[Execution plan](execution-plan.md) · [API contract](docs/openapi.yaml) · [Decision record](docs/adr/0001-api-contract.md)

## API naming

JSON member names, path parameter identifiers, and query parameter names use
`snake_case` throughout V2. Standard HTTP headers keep their defined spelling.
Enum values and user text retain their defined or supplied spelling. Go and
JavaScript identifiers follow their language conventions. String inputs mean UTF-8,
and user-supplied text is preserved without silent replacement or normalization.

Keep one current API, CLI, and storage format during the reboot. Native
snapshots use `format_version: 2`.

## Run the Turn 1 service

Build the Windows service and CLI with Go 1.27.

```powershell
go build -o localswim.exe ./cmd/localswim
.\localswim.exe serve --data C:\absolute\path\to\fresh-v2-data --port 0
```

Before starting the service, set `LOCALSWIM_HUMAN_TOKEN` and
`LOCALSWIM_BOT_TOKEN` in its private launch environment. Supply a fresh,
independent random base64url value of at least 32 bytes for each process start.
The service prints its bound loopback origin on startup. Set `LOCALSWIM_URL`
to that origin and `LOCALSWIM_TOKEN` to the chosen capability in the CLI's
environment. Keep these values out of arguments, Git, and shared logs.

```powershell
.\localswim.exe board create --name "Local demo"
.\localswim.exe board list
.\localswim.exe workspace info
```

For a custom workflow, pipe its board definition into `localswim board create --json`.
Choose exactly one of `--name` or `--json`.

Rename a board with `localswim board rename [--revision N] BOARD_ID "New name"`.
`board rename`, `board patch`, and `workspace pin` commands accept optional `--revision N`.
Supply a previously read revision to reject changes since that read, or omit it
to read the latest revision before updating. Put options before positional arguments.

At least one active lane must permit card creation through both a lane grant
and a member’s `create_card` capability. Every active lane must reach Completed
through directed move grants, with actor handoffs allowed. Creation and native
snapshot admission check these rules after shorthand expansion.

Use `workspace pin set ID [ID ...]` to replace the pin order, `workspace pin add ID`
to append one board, and `workspace pin remove ID [ID ...]` to unpin boards.
An empty `workspace pin set` clears all pins. Repeated adds leave the order alone.
Removal skips invalid, repeated, or unpinned IDs with `WARNING` messages on stderr
and applies valid removals. Warnings retain a successful exit status. Set validates
the complete ordered list and reports `ERROR` without changing any pins if a target fails.

A V2 data directory has one writer. Native board snapshots live in `boards/`.
The verification endpoint and `board verify --data <directory> <board-id>`
read the same durable format. The workspace pin order has its own revision, starting at 1.

Export a board's native snapshot as JSON with
`localswim board export [--type json] <board-id>`. JSON is the only supported
type and the default. Output goes to standard output.

For local build staging, place `candidate.json` and the native
`candidate.exe` (Windows) or `candidate` (Unix) in the data directory.
The manifest contains `version` and lowercase SHA-256 `sha256`. The
`/service` response identifies the running generation. The `/builds`
responses report staged validation using the published build schema.
Validation inspects bounded bytes and native Go identity. Activation is a
later milestone.

The browser adapter is in [web](web/README.md). Live browser capability
bootstrap and card resources belong to their scheduled milestones.

## Quality checks

Install the pinned development compiler once with `npm --prefix web ci --ignore-scripts`.
Run `.\check.ps1` before handoff. It requires clean Go formatting, `go vet`, Go tests,
strict TypeScript checking of the browser source, browser-client tests, and clean Git whitespace.
