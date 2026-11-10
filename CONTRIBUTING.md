# Contributing

## Product remit

V1 is the reference for V2's UI, UX, and functionality. Preserve the browser,
CLI, permission, audit, save, and recovery behavior people rely on.

Use one loopback service and port for the UI, workspace, and every board. Keep
V1 and V2 independent, with fresh V2 data. Build quickly with a lean implementation.

## Conventions

Use snake_case for JSON members, path parameter identifiers, and query parameters.
Standard HTTP headers retain their defined spelling. Go and JavaScript identifiers
follow their language conventions.

Application timestamps use `YYYY-MM-DD HH:MM:SS+00:00`, normalized to UTC and whole
seconds. Revisions distinguish edits within the same second. Native snapshots use
`format_version: 2`.

CLI commands that read JSON require a positional file path or `-` for standard input.
Omitting the input path is a command error. Card creation takes `board_id`
in its JSON document. The CLI obtains the board revision validator.

Full card JSON and native card snapshots carry a positive safe-integer `revision`
for the board commit that last changed that card. Unrelated board edits leave it
unchanged, and successive card revisions may skip values. The service assigns this
field; card creation and patch inputs cannot supply it. HTTP `ETag` headers remain
the conditional-write validators.

Full lane JSON and native lane snapshots carry a positive safe-integer `revision`
for the board commit that created or last changed that lane, including its position.
Initial workflow lanes use revision 1. Unrelated board edits leave the value unchanged,
and successive lane revisions may skip values. Lane creation and patch inputs reject
this service-managed field. Lane writes retain their lane and board HTTP validators.

Full relationship JSON and native relationship snapshots carry a positive safe-integer
`revision` for the board commit that created or last changed that relationship.
Relationships seeded during card creation use that creation commit. Unrelated board
edits and no-op patches leave the value unchanged, and successive revisions may skip
values. Relationship creation, seed, and patch inputs reject this service-managed
field. HTTP validators remain in response headers.

Comments are immutable after creation. Their JSON and native snapshot records contain
identity, parent references, attribution, text, and timestamps. Comment HTTP validators
are carried in response headers. Posting a comment still requires the parent card
validator and advances that card's revision.

For card edits, comment posting, and attachment upload, CLI `--revision N` checks
the addressed card revision before writing with its current HTTP validator. Card
creation, relationship creation, policy changes, and `lane add` check the board
revision. `lane rename` checks the addressed lane revision. Relationship patch
and remove check the addressed relationship revision.

String inputs mean UTF-8. Preserve user text exactly, including whitespace and
Unicode. Entity and build IDs are immutable UUIDv4 values. The service_instance_id
uses UUIDv7, with the instance creation time encoded as Unix milliseconds.
Publish UUIDs in lowercase. Accepted UUID input may use either hex case.

The reboot has only Terry and Bot as users. Keep one current API, CLI, and storage
contract. Follow the current owner-approved direction and the rules in [AGENTS.md](AGENTS.md).

Every card has a non-null `owner_id` referencing a board member. Card creation
may omit it to use `board.default_owner_id`; assignment patches require a member
UUID and cannot clear the owner. Published boards and native snapshots require
`default_owner_id`. Board creation may omit the default when the global runtime
Bot ID is a member. Custom boards without Bot must supply a member explicitly.
Changing the default requires `manage_board` and applies to subsequent card
creation. Owner assignment audit values record the previous and new user UUIDs.

## Checks

Install the pinned development compiler.

```powershell
npm --prefix web ci --ignore-scripts
```

Run the complete quality gate before handoff.

```powershell
.\check.ps1
```

It requires clean Go formatting, go vet, Go tests, strict TypeScript checking of
the browser source, browser-client tests, and clean Git whitespace. Fix findings
at their source and justify any narrow suppression.

## Data and build staging

Native board snapshots live in the data directory's `boards/` folder. Verification
and export use the same durable format. Workspace pin order has its own revision,
starting at 1.

For local build staging, place `candidate.json` and the native `candidate.exe`
(Windows) or `candidate` (Unix) in the data directory. The manifest contains
`version` and lowercase SHA-256 `sha256`. The `/service` response identifies the
running service instance, and `/builds` reports staged validation. Activation is a
separate milestone.

Attachment bytes use `attachments/<board UUID>/<card UUID>/<attachment UUID>`
inside the data directory. The original filename, size, and SHA-256 digest are
stored in the board snapshot. Focused attachment metadata readback can resolve
an uncertain upload by its original attachment UUID.

## References

- [API contract](docs/openapi.yaml)
- [API decisions](docs/adr/0001-api-contract.md)
- [Milestones](milestone-plan.md)
- [Execution plan](execution-plan.md)
- [Browser client](web/README.md)
