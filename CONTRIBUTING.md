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

Full attachment JSON and native attachment snapshots carry a service-managed positive
safe-integer `revision` for the board commit that uploaded the attachment or last
changed its metadata. Unrelated board edits and no-op operations preserve the value,
and successive attachment revisions may skip values. Attachment upload still checks
the parent card's revision with CLI `--revision N`. HTTP validators remain in headers.

Hash names in API fields and documentation identify both the hash family and digest
size. Attachment `sha2_256` contains the lowercase hexadecimal SHA-2-256 digest of
the file bytes.

Comments are immutable after creation. Their JSON and native snapshot records contain
identity, parent references, attribution, text, and timestamps. Comment HTTP validators
are carried in response headers. Posting a comment still requires the parent card
validator and advances that card's revision.

Use `comment list --limit N BOARD_ID` to read the newest comments on active cards
with their current numeric `ticket`, attribution, and full text. The limit is 1 to
200 and defaults to 50. Equal timestamps retain newest commit order. Add
`--archived include` to read active and archived work together or `--archived only`
for archived work. The limit applies after filtering. Pass a `CARD_ID` after
`BOARD_ID` to focus the report on one card, which remains readable when archived.
Use `--cursor CURSOR` with the same selection to continue a report.

Approval receipts are immutable. Their `board_revision` is the positive safe-integer
board commit revision shared by every receipt change. Approval requests retain
`expected_board_etag` and each selected card's `etag`. The response's
`X-Board-ETag` carries the committed board validator. A lost approval response is
resolved by reading the original receipt UUID before considering another write.

For card edits, comment posting, and attachment upload, CLI `--revision N` checks
the addressed card revision before writing with its current HTTP validator. Card
creation, relationship creation, policy changes, and `lane add` check the board
revision. `lane rename`, `lane retire`, and `lane set-approval-button` check the
addressed lane revision. Relationship patch and remove check the addressed
relationship revision. `attachment detach` checks the attachment revision and
sends the parent board validator with the attachment's HTTP validator.

Use a card UUID or a board UUID followed by the ticket with `card move`,
`card assign`, `card archive`, and `card unarchive`. A ticket edit reads one
focused view and submits its card and board validators together. A stale view
refuses the edit even when the card itself has not changed. The optional
`--revision N` checks the resolved card revision. After a stale response,
inspect the current ticket mapping before issuing another command. After an
uncertain response, inspect the original card by UUID.

Use `activity list` with inclusive canonical UTC `--from` and `--to`. By default,
it returns event metadata and ticket numbers without card prose. Select
`--view full` to read attributed audit values. Both views support `--actor-id`,
`--operation`, and `--ticket` filters. Operation identifiers are matched exactly.
Pages default to 50 records and allow up to 200. A cursor stays tied to the
original interval, filters, and view.

`lane set-approval-button` controls whether the optional Human lane action is enabled.
Manage lanes in the browser or PATCH the active Ready for Review lane to set
`bulk_approval_comment`. The setting accepts up to 1,024 characters, and an empty
string restores the default. It can be saved while lane approval is disabled.

The approval request accepts up to 5,000 cards and 2 MiB of JSON. The browser
submits every rendered active member with its original card version and the
original board version. The service commits all moves and comments together or
refuses the whole request. Empty lane requests leave the revision unchanged.
The first bulk move records the applied comment in the receipt and audit.
Individual approvals always use `Reviewed & approved, moving to Completed.`.

`lane retire` preserves archived cards and historical lane identity. It refuses
active occupancy, the Completed role, and any retirement that would break a
required workflow path. Retired lanes stay inspectable. New arrivals and
restoring an archived card in a retired lane are refused.

`card set-priority` applies the effective blocking floor. Creating or changing a
blocking link and raising a priority can promote upstream cards in one audited
revision. Archived unfinished cards participate, and Completed cards stop paths.
Removing a link does not lower priorities.

Batch priority changes send one `priority_id` and the selected `card_ids` to
`PATCH /cards` under `expected_board_etag`. Evaluate every selection together
before applying the blocking floor. The result includes effective selected cards,
all actual audit changes, and `board_revision`. `X-Board-ETag` carries the committed
validator. Duplicate IDs and Completed selections refuse the entire operation.
An unchanged effective result preserves the revision and audit.

`GET /boards/{board_id}?view=lanes` returns `board`, active `lanes`, and visible
`cards` in lane order. Within each lane, priority precedes same-rank blocking
relationships. Cycles stay together in ticket order. Archived cards do not
participate in this visible graph.

Use `card inspect [--prose] BOARD_ID TICKET` to read one ticket, including
archived tickets, and see its current lane, owner, priority, direct relationships,
parent, and children. The JSON includes the selected card and board revisions.
Relationships identify whether the selected card is the source, target, or a
peer in a symmetric relation. Use `--prose` to include only the selected card's
description and attributed comments. The default contains titles and workflow
metadata. An authenticated board member with read permission can inspect a
ticket while the board is closed.

Lane key patches preserve the lane UUID and its card, grant, and historical
references. The Completed role and `completed` key are protected. Read-only
ticket-order previews follow full blocking paths through intermediate cards,
reject selected cycles, and permute only the selected ticket pool. They preserve
the board revision and return it as `board_revision`.

Set `notes` on an initial workflow lane, in `lane add` JSON, or in a
revision-checked lane PATCH. Notes display as plain text above the lane's cards,
preserve line breaks, and accept up to 4,096 characters. An empty string clears
the note. Choose the position of a new lane with `before_id` or `after_id`.

Set `actor_presentation` in board creation JSON or use `board patch` with the
`manage_board` capability. The optional `human` and `bot` entries each accept a
`label` and a six-digit hexadecimal `accent` such as `#356AAB`. Board patches
replace all overrides. Send an empty object to restore the users' display
metadata. Attribution still uses the saved user UUID and permissions still use
the authenticated principal.

Use `PATCH /lanes/{lane_id}` with Boolean `reorder_enabled` and the current
`board_etag`. Send the current lane validator in `If-Match`. The caller needs
`manage_workflow`. The setting defaults to false. An enabled lane offers the
browser preview and Apply workflow once it has at least two active cards.

Backup capture holds the board read lock while capturing one committed snapshot.
Attachment verification and archive construction run after releasing that lock.
The ZIP includes the native snapshot and exact audited immutable attachment
paths, including detached files. Backup metadata uses `sha2_256` for the archive
digest and `board_revision` for the captured commit. Later edits remain available
to subsequent captures. Use `backup info BACKUP_ID` to read metadata for the
original backup after an uncertain POST before attempting another capture.
Use `board export [--type json] [--data DIRECTORY] BOARD_ID FILE|-` to save the
complete native JSON snapshot. Supply a new file path or `-` for stdout. Omitting
the destination is an error. The same destination rule applies online and offline.

Use `backup download BACKUP_ID FILE|-` to verify and save the ZIP.

Run `localswim backup verify FILE` to check a downloaded Localswim ZIP offline.
The command returns the board UUID, captured revision, and attachment count
after checking the native snapshot, complete audit, exact archive inventory, and
each active and detached attachment. It reads the ZIP in place, supports archives
over 96 MiB, and leaves both the archive and service data untouched.

`attachment detach` removes active content access and clears a matching cover in
one transaction. Inspect the inactive record with `attachment get`. The original
metadata and immutable bytes remain available for backup.

Repeat `--lane UUID` on `card search` and `card next` to select the union of those lanes.
Both commands accept `--archived exclude|include|only` and default to excluding archived cards.
Without a lane filter, `card next` selects unfinished cards. To inspect Completed work with
`card next`, pass the Completed lane UUID. `card search` includes Completed cards by default.

Search and next-work queries compute distinct unfinished downstream counts over the whole board
before lane, archive, completion, search, cursor and limit filters. Completed cards stop paths. Archived unfinished
cards participate. The triage score is (priority rank + 1) * 100 - downstream count, followed by
priority rank and ticket. Numeric relationship lookup retains ticket order. Search uses full Unicode
case folding without normalization. Checked-in exceptions reproduce Python 3.15.2 casefold
against Go 1.27 Unicode lowercase mappings.

Ticket-order apply consumes the original preview validator and its exact deterministic proposed
order. It recomputes the selected pool and full dependency paths in one transaction. A stale preview
refuses. A real permutation changes only selected ticket numbers, card revisions and renumber audit.
UUID attachment paths and existing history remain intact. An unchanged order preserves the revision.

Ordinary writes to existing boards compare the saved snapshot with the service's
accepted snapshot before preparing a change and again immediately before replacing
the file. After validating the saved contents, the service rechecks the named
file's identity, size, and modification time. A damaged or missing snapshot returns
`503 unavailable` and appears as `durable_state_unavailable` in the board catalog.
A valid replacement that differs from the accepted state returns `503 unavailable`
and appears as `durable_state_changed`, even when the two revisions match.
Both refusals leave the saved and accepted state intact. Other boards remain
writable. Native snapshot verification reports the file's validity, while the
catalog reports whether the file agrees with the running service. Use explicit
snapshot admission or backup restoration to recover the affected board.

Native recovery accepts JSON snapshots or application/zip backups at the existing board address.
Both request entities have an inclusive 96 MiB limit. PUT uses the current durable snapshot
representation validator when the snapshot is readable. When durable state is unavailable, a previously
obtained tag for the last accepted snapshot can authorize admission. Stored backup restoration uses the
live board validator and returns an inspectable receipt.
Validate policy, complete audit and every active or detached attachment before publishing. Recovery
preserves the board UUID and shared global user configuration and allocates a revision above the live,
readable durable and source revisions. Recovered mutable resources use the recovery commit version.
Inspect accepted state after an uncertain result. A later restore can supersede a prior receipt by
admitting an earlier audit history.

Manual publication discovers the Git worktree enclosing the service data directory. It requires
an initial commit, local branch, configured identity and an origin remote. The publication commit's
changes consist of the captured native snapshot and exact audited attachment paths. The push follows
normal Git ancestry. Publication uses a private index under the persistent
Git common-directory localswim-autopush.lock. The caller's index and unrelated staged work remain
intact. Before writing Git objects, publication checks every captured path against Git's ignore
rules, including paths already tracked or staged. Any ignored path refuses the entire capture with
`publication_path_ignored`. An ignore check that fails or warns reports `git_ignore_unavailable`.
Unsupported hooks, external content filters and filesystem monitors also refuse before object publication.
Inspect the original publication UUID after an uncertain result.

Enable automatic publication independently with
`board publishing enable [--revision N] BOARD_ID` or
`workspace publishing enable [--revision N]`. Both default to disabled and use
the data directory's enclosing Git worktree, local branch, and origin remote.
An enabled board publishes a coherent committed snapshot and its exact audited
attachment paths after five seconds of quiet. Enabled workspace publishing
captures only `localswim-workspace.json`, including saved pin order. Both use
the manual publisher's Git safety checks and common-directory lock.
Automatic attempts use the resolved origin push destination. Explicit origin
transport overrides leave the attempt pending with `unsupported_remote_transport`.

Use the matching `status` command to inspect pending work, the last successful
push time, and a fixed failure code. Failed automatic attempts remain pending and
retry with bounded backoff without another edit. Status changes leave board,
card, and workspace revisions intact. Use `disable` with the same revision
options to stop new attempts. A disabled publisher keeps durable pending intent.
After a disabled restart, status history is loaded when the publisher is
re-enabled. Inspect status after an uncertain settings write.
A board restore requires matching publishing settings. Reconcile any difference
through the explicit settings command before restoring.
A Git transport helper can outlive Git's timeout. The worker retains its lock
while the admitted command joins.

Use `board close [--revision N] BOARD_ID` and
`board reopen [--revision N] BOARD_ID` to change availability at the existing
board address. The revision option checks the board revision and the write uses
its current HTTP validator. Both commands require the board's `close_board`
capability and return the resulting Board document.

Close drains admitted operations on that board. If its accepted publishing
settings are enabled, the publisher flushes the final accepted open revision
before the closed revision is saved. A failed flush leaves the board open with
publishing status available for inspection. While closed, board metadata and
the workspace remain available, and the other boards continue on the same
listener. Reopen restores access to the saved board and restarts enabled
publishing. If the service restarts with the board closed, the reopened worker
loads the durable publication checkpoint. After an uncertain write, inspect
`board get BOARD_ID` before
choosing whether to try again.

Use an explicit `--data DIR` to read a saved board through the local filesystem.

- `board get --data DIR BOARD_ID` returns saved board metadata.
- `board export --data DIR BOARD_ID` emits the validated native snapshot.
- `card inspect --data DIR [--prose] BOARD_ID TICKET` inspects a focused ticket.

Each invocation validates one committed snapshot. The directory must be directly
readable. These commands work with the writer stopped and with open or closed
boards. The same inspections without `--data` use the authenticated live API.
Closed boards still refuse card writes.

String inputs mean UTF-8. Preserve user text exactly, including whitespace and
Unicode. Ordinary entity, comment, attachment, build, and request correlation IDs
use UUIDv4. Backup, publication, restoration, build activation, audit event,
approval receipt, and service instance IDs use UUIDv7, which encodes ID generation
time as Unix milliseconds. IDs are immutable. Publish UUIDs in lowercase and
accept either hex case for the declared version.

When displaying backup or publication creation time, derive it from the ID and
format it as `YYYY-MM-DD HH:MM:SS+00:00`. Their stored and API records omit
`created_at`.
Publication `finished_at` and audit, approval, and restoration `at` retain actual
execution times. A caller-supplied operation ID can precede execution.

The reboot has only Terry and Bot as users. Keep one current API, CLI, and storage
contract. Follow the current owner-approved direction and the rules in [AGENTS.md](AGENTS.md).

Every card has a non-null `owner_id` referencing a board member. Card creation
may omit it to use `board.default_owner_id`; assignment patches require a member
UUID and cannot clear the owner. Published boards and native snapshots require
`default_owner_id`. Board creation may omit the default when the global runtime
Bot ID is a member. Custom boards without Bot must supply a member explicitly.
Changing the default requires `manage_board` and applies to subsequent card
creation. Owner assignment audit values record the previous and new user UUIDs.

Set `user_css` in `board create --json` or `board patch` JSON to apply a stylesheet
while viewing that board. Patches require `manage_board`. An empty string clears
the stylesheet. The browser replaces the active stylesheet when switching boards
and uses the same content security policy throughout.

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
and export use the same durable format. Workspace preferences expose their own numeric
`revision`, starting at 1, in HTTP JSON and native storage. Workspace conditional reads
and writes use the HTTP `ETag` and `If-Match` headers. The browser adapter retains the
response header in its result's `etag` property separately from the JSON `data`.

For local build staging, place `candidate.json` and the native `candidate.exe`
(Windows) or `candidate` (Unix) in the data directory. The manifest contains
`version` and lowercase SHA-2-256 `sha256`. The `/service` response identifies the
running service instance, including the bound port. Use `service builds` to
inspect the staged build. A workspace administrator can activate a validated
build with `service activate BUILD_ID` and inspect the receipt with
`service activation ACTIVATION_ID`. Activation returns an accepted receipt
before the replacement takes over. The replacement uses the same data directory,
actual port, and board URLs. The browser obtains a new session capability.

Use `service stop` for an authorized graceful shutdown. Activation and shutdown
drain admitted writes and event streams and flush enabled board and workspace
publishing. A failed final publication refuses the transition. The service
stays available, with a fixed failure code in `service status` and details in
the affected publisher status. After an uncertain response, inspect service
health and the original activation ID before deciding whether to submit again.

Attachment bytes use `attachments/<board UUID>/<card UUID>/<attachment UUID>`
inside the data directory. The original filename, size, and SHA-2-256 digest are
stored in the board snapshot. Focused attachment metadata readback can resolve
an uncertain upload by its original attachment UUID.

Use `attachment upload [--revision N] [--id UUID] [--filename NAME] CARD_ID <FILE|->`.
The required `-` input reads raw bytes from standard input without text conversion.
Its default display filename is the same attachment UUID supplied by `--id` or
generated for that upload. A file path defaults to its basename. `--filename`
overrides either default with a UTF-8 name of 1 to 1024 characters, without line
breaks or NUL. Both input sources have a 10 MiB limit. The parent card revision
is checked before reading attachment bytes.

## References

- [API contract](docs/openapi.yaml)
- [API decisions](docs/adr/0001-api-contract.md)
- [Milestones](milestone-plan.md)
- [Execution plan](execution-plan.md)
- [Browser client](web/README.md)
