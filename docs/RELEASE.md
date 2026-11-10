# V2 release path

Ship a Windows localhost board that the same two users can use in place of V1.
Keep approved V1 behavior and UX as the floor. Use V1 as the behavior reference
and keep the fresh V2 implementation. Reuse its single Go writer, SQLite
transactions, browser and CLI adapters, and native archive. Keep board data and
recovery evidence private.

## Where we are

V2 can start a [local host](../cmd/localswim/local_entry.go), open a registered
workspace, and use browser and CLI card reads and mutations. The
[browser board](../web/src/board/view.ts) has details, editing, comments and
individual approval. Search, hierarchy, relationships, attachments, and
[native restore](../cmd/localswim/local_restore.go) also have working entry
points. V1's daily browser and Bot CLI workflows still need a joined pass on
an imported copy. V2's native archive restores V2 data. Importing current V1
JSON, policy, audit history, and attachment bytes is a separate gap.

## Build order

1. **Make one copied V1 board usable in V2.** Import the current V1 snapshot
   format in use into a separate V2 root. Preserve IDs, ticket numbers, lanes,
   audit history, relationships, and attachment hashes. Open it through the
   existing workspace and inspect it in the browser and CLI. Perform one normal
   browser edit and one Bot edit, reopen it, export it, and restore the export.
   Keep V1 as the only writer of the live board throughout this exercise.
2. **Close daily parity gaps on that copy.** Bring over the familiar lane/card
   UI, keyboard and draft behavior, review actions, and Attention. Match V1
   blocker priority inheritance, audited priority changes, triage order,
   dependency ticket ordering, and workflow maintenance. Use the same Go
   operations for browser and CLI. Add thin compatibility only where an existing
   script or operator command needs it.
3. **Cut over one board.** Exercise the integrations the users rely on, including
   opt-in owner notices, backup and Git delivery where used. Rehearse a stopped
   writer, verified backup, restart, and rollback. With owner approval, stop V1
   for that board before V2 takes over. Expand to the rest of the workspace after
   the first board works in daily use.

## Proposed backlog changes

| Existing scope | Release disposition |
| --- | --- |
| M19–M24 | Preserve M19 evidence and remove the full baseline from the release path. Defer installed-capability proof and readiness kits. Fix a measured read or write problem when it affects the copied board. |
| M25–M29 | Defer briefing copy and send. Fold owner notices into one opt-in V1-parity slice with current target, enable/disable, one Human edit delivery, and restart persistence. |
| M30–M32 | Group backup, Git and restart in one operator slice. Match the V1 workflows actually in use. Keep hot update work focused on the behavior the users need. |
| M33, M39–M46 | Combine workspace adoption, import, attachments, active integrations and rollback into the copied-board path. Keep external snapshot replacement on the parity list for workflows that use it. |
| M34–M38 | Keep daily V1 parity. Deliver it in small usable slices on the copied board. |
| M47–M48 | Drop the standalone evidence-ledger milestone. Fold the release check into the actual install, daily-use, restore and rollback exercise. |

These are proposed roadmap edits. The approved V1 baseline still applies to
features that are deferred during the copied-board exercise.

## Release check

Build once from the pinned source and run the normal repository checks. Use a
small synthetic fixture for a new or fixed invariant. Use the copied board for
one short daily-use run that covers start, open, find, edit, review, restart and
restore. Add a targeted regression when a defect is found. An exhaustive stress
matrix, a separate qualification kit, or an installed-identity proof campaign
is outside this release path.
