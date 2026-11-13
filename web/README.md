# Browser application

The browser page and its local assets are embedded in the V2 executable. Open
the service's loopback URL or a board link at `/boards/{board_id}`. The page opens
a same-origin Human session and keeps the capability in memory. The HTML and
local assets are independent of the capability.

From the repository root, run `node web/tooling/git-hooks/setup.mjs` to install
the pinned web tools and configure Git hooks. The TypeScript entry point is
`src/main.ts`. After editing browser sources, run `npm --prefix web run build`
and commit the corresponding files in `dist/`.
The Go build embeds the optimized HTML, JavaScript and CSS. Matching maps
support debugging. Run `npm --prefix web run check` for the compiler, lint,
canonical formatting and release asset checks.

The isolated candidate opens the workspace attention view and direct board URLs.
It displays confirmed lanes and cards with inline filtering, an inspection
drawer, and live refresh. During an outage it retains the last loaded cards
and offers a focused retry. Workspace pins share saved order across clients,
and each tab remembers its sidebar width and collapsed state.

The tab also remembers each board's filter, lane scroll positions and selected
card. The card editor keeps exact title and description drafts in the current
tab. Inspect the saved card after an uncertain submission before retrying.

## Current production UI reference

The following controls describe the installed V2 UI used as the reference for
replacement qualification.

The selected board updates when another client commits an edit. Live connection
status is separate from the loaded board, so an interruption leaves the last
view and drafts available. A replacement connection checks the current board
revision and reads any missed changes. Use **Retry board update** to reread the
board while the stream reconnects or after a refresh failure.

Open a card to edit its text, lane, or owner. Each submission uses the last card
ETag read from the HTTP response header. Card JSON exposes its positive integer
`revision`; the drawer retains the header validator separately. A stale edit or uncertain response triggers a focused
read so you can compare your draft with the saved card before deciding whether
to submit again. Inspect or Refresh rereads the active editor and preserves
the draft. If readback fails, inspect the affected card or board before
submitting another change there. The page makes one write attempt per submission.

Press Enter or Space on a card to open its drawer. In **Title**, Enter saves
the text once input composition has finished. Use **Close card** to return focus
to the visible card opener or to **Filter by title or ticket** if it is missing.
Lane action buttons include the lane name in their accessible name. The board's
Human accent supplies the action fill, with black or white text selected for
at least 4.5:1 contrast.

While a card edit, move, or creation is saving, the lane shows the requested
title or destination with a pending label. A new card receives its ticket
number when the service confirms it. Ticket previews and lane-wide approvals
use confirmed lane membership. A refusal restores the last confirmed display
and keeps the draft. If a response is lost, the page reads saved state and
offers inspection of the original card or creation.

Each tab keeps title, description, and comment drafts with their board and card
IDs. The new-card draft also remembers its destination. Reload restores text
exactly within the tab's storage budget. The newest drafts take precedence when
the budget fills. Larger drafts remain available while the page stays open.

After a lost create response, open **Inspect pending creation** on the
originating board and choose **Inspect creation**. The page opens the original
card when it finds it and reports any differences from the submitted draft.
For a comment, open its card and choose **Inspect original comment**.

If inspection finds the original ID absent and the original validators still
current, an explicit retry uses the same ID, text, and validators. A stable
newer board revision and a confirmed absence allow dismissal and release the
draft for editing. Inspection that cannot verify the saved state keeps the
original attempt available for another read. Session capabilities and file
contents stay in memory.

**Add card** appears in each active lane where the current actor can start a
card. It opens the shared new-card form with that destination selected. In a
card drawer, **Move card** offers the active destinations granted to the current
actor from the saved lane. The browser reads these grants from the board's
policy and refreshes them with the board. The service checks its current policy
and the original conditional-write validator when it receives either action.

Drag an active card to a highlighted destination lane, or use **Move card** in
the drawer. A drop checks that the card is still at its displayed revision and
uses that revision's HTTP validator. The drawer and its drafts stay available
after a refusal or uncertain response. If readback fails, open the affected card
and use **Inspect saved card**. The browser reads the original card without
repeating the move.

Use **Filter by title or ticket** to narrow the active cards on the board as you
type. Matching uses a case-insensitive literal title or displayed ticket
fragment. Press **/** from outside an editor to focus the filter, and press
**Escape** in the filter to clear it. The filter and lane headings show matching
and total counts while the filter is active. Clear the filter to preview ticket
order or approve all cards in a lane where the filter hides cards.

Lane headings show the number of active cards, including zero in empty lanes.
The Completed lane initially shows cards completed in the last 24 hours and
cards whose completion time is unknown. **Show older cards** reveals older
matches for the current board visit. Matching counts include those cards before
they are revealed. Show the full lane before previewing a ticket reorder. Older
cards also remain accessible through card search and ticket links.
Card faces show nonzero comment and active attachment counts. The card opener's
accessible description names the exact counts. The board refreshes them from
saved state after edits.

Card details also show attributed comments and attachments. New comments and
uploads have client-assigned IDs. Immutable comment JSON contains identity, parent
references, author, text, and creation time. Lists also include the card's current
ticket number. The comment validator is returned in the HTTP response header.
Posting uses the parent card's current validator and advances
its revision. Comment lists and focused attachment reads
can identify the exact submission. The board offers its newest comments and lane
insertion, rename, and retirement controls. Server-side policy determines
which actions the Human session may perform. Lane JSON carries a positive safe integer
`revision`. `renameLane(laneId, revision, boardEtag, label)` reads the lane afresh,
checks the expected numeric revision, and uses the returned HTTP ETag for its
conditional PATCH. It retains the caller's board ETag precondition. An invalid,
stale, or failed read prevents the PATCH. Lane list and write responses validate
the numeric revision and response shape. HTTP validators stay in response headers.

Archive a card from its drawer. Expand Archived cards to reopen it and restore
it to the same lane while that lane is active. Retired lanes stay visible in Manage
lanes and in archived card details. Active cards prevent retirement, and Completed
is protected. Retired lanes are omitted from the board and lane selectors. Expand
History in the drawer to inspect attributed activity for that ticket, including
attachment detaches and priority changes. On a Ready for Review card, Reviewed & approved posts the approval
comment and moves the card to Completed in one action. The drawer closes on a
confirmed approval and retains any comment draft. After a stale or uncertain
response, open the original card and choose **Inspect approval** to check its
receipt. Manage lanes can enable or disable
Approve all in lane for Ready for Review. The action submits the complete rendered
active membership and its original versions, including the rendered board version.
A stale set requires inspection before another submission.

Use the drawer's parent and child links to open those cards, including archived
cards. Add a typed link from the drawer. If you can manage relationships, choose
**Add relationship** in the new card form to add each link. The service saves
the card and all its links together. Each relationship is visible from both cards.
Link types are Blocks, Duplicates, References, and Relates to. Incoming links
identify the other card as the source. Changing a link type preserves its source
and target.
Saved descriptions and comments display `**strong**`, `*emphasis*`, and single
backtick code spans within a line. Code displays its contents literally,
including asterisks and ticket references. Descriptions and Human comments link
references such as `#0042` to that ticket on the same board, including archived
tickets. Bot comments use the same formatting and leave ticket references as
text. HTML is displayed as text, and editors show the source markup. When you
edit an existing description, the saved line endings outside the changed span
are retained where they keep the displayed breaks intact. The changed span uses
the browser's line endings. Card priorities are shown on the board and in the
drawer and refresh after link edits.

Use **Set priority** in the card drawer to change that card's priority against
its displayed revision. The drawer shows the effective saved priority and
reports when a dependency floor prevents the requested demotion. After a
stale or uncertain result, compare the saved priority with your selection
before submitting again.

In **New card**, use **Files to attach** or paste files into the form. You can
remove individual files before submitting. Staged files stay in the current tab
for that board until the card is confirmed. After creation, the same card drawer
shows per-file upload results. The card and its confirmed files stay saved if a
later upload fails.

Use **Files to attach** in the card drawer to select several attachments, then choose
**Upload attachments**. You can also paste a clipboard image into the open drawer.
Each file has its own outcome. The drawer rejects files larger than 10 MiB before
uploading and submits the remaining files one at a time for that card. You can
open another card while the batch finishes on the original card.

Each tab saves pending file IDs, names, sizes, and SHA-256 fingerprints for the
original card before uploading. Files stay out of tab storage. If an upload
response is lost, open the original card and choose **Inspect saved upload**.
The drawer checks the original ID and fingerprint. An upload may still be
running when the first inspection finds no saved attachment. In that case
the card stays fenced and you can inspect again.

When recovery is available after confirmation or a known refusal, reselect all
remaining originals in **Files** and choose **Resume pending uploads**. The
drawer checks each file's name, size, and contents and reads its original ID
before sending it with the current card revision. **Discard pending files** clears the saved selection
when there is no unresolved attempt. Uploads require room for their recovery
details in tab storage.

Attachments offer authenticated downloads. Raster attachments also offer a preview
and cover selection. A card in its lane shows the selected cover or, when no cover
is selected, the first active raster attachment. Detaching the selected cover clears
that selection and shows the first remaining raster attachment when available.
Detach revokes active file access. History retains the attachment ID and attribution.

Use **Pin** or **Unpin** beside a board in the Attention sidebar. In **Pinned
boards**, drag a board by its grip or focus the grip and press Up or Down to move
it. The order is saved in shared workspace preferences, so another client sees
it when it opens or refreshes the workspace. If the order changes during a
save, the browser reads the latest order and applies the same edit to it once.
If the change remains pending, review the saved order before choosing **Retry
pin change** or **Cancel pending pin change**. If saved pins are missing from
the board list, choose **Remove unavailable pins** to clear them together.
The browser checks the complete board list and the current workspace revision
before saving.

Expand **Board actions** to turn automatic publishing on or off for that board.
Administrators can also save workspace publishing on the **Workspace** page.
Publishing and pin edits share the workspace HTTP validator. The board uses
its own validator.

Use **Refresh board publishing status** or **Refresh workspace publishing
status** to inspect pending work, failed pushes, and the last successful publish.
A failed push can recover automatically without another edit. If a settings
save has an uncertain outcome, the browser reads the saved setting before
another submission and keeps the draft available for comparison.

### Attention sidebar

Drag the separator beside **Attention** to resize the sidebar. With the
separator focused, use Left and Right Arrow or Home and End to resize it.
Use **Hide Attention** or Enter on the separator to collapse the sidebar, and
**Show Attention** to reopen it. The tab remembers the expanded width and
visibility across reloads. On a smaller viewport the width adjusts to fit
and returns to the saved width when space is available.

### Board availability

Members with the board's `close_board` grant can select **Close board** under
**Board settings**. A closed board stays at the same address and offers **Reopen
board** to those members. Other boards and the Workspace remain available. The
tab restores the selected card and its saved draft when the board reopens.

Each action sends one availability PATCH with the board's current HTTP ETag.
The browser reads the saved board after success, a stale validator, or an
uncertain response. Use **Inspect board availability** to reconcile a pending
change before choosing an explicit retry.

### Service lifecycle

Service JSON contains runtime state without an ETag field. `getService()` retains
the HTTP validator separately as `reply.etag`. Lifecycle controls capture that
validator alongside the service data and pass it to `stopService(service, etag)`
or `activateBuild(id, buildId, etag)` for conditional requests.

Workspace administrators can use **Activate staged build** when the Service
panel reports a valid candidate. The browser reconnects on the original port,
obtains a fresh session capability, and reads the activation receipt. Board
links stay the same. **Shut down service** requests a graceful stop and reports
when the service accepts the drain. A final publication failure leaves the
service running and available for status inspection.

Each action makes one write attempt. After an uncertain response, use
**Refresh service status** to check the service and the original activation
receipt before submitting another action.

### Browser API client

`api.mjs` is a dependency-free ES module for the same-origin V2 API. It calls
the supplied `getCapability` callback for each authenticated request.

```js
import { createBrowserApi, readWorkspaceOverview } from "./api.mjs";

const api = createBrowserApi({ getCapability: currentBrowserCapability });
const { workspace, boards } = await readWorkspaceOverview(api);
```

JSON payload fields and query names use `snake_case`. The JavaScript adapter
keeps its existing language-convention method, argument, and error property names.

Each successful result exposes the JSON object as `data` and the server's HTTP ETag
header as `etag`. Workspace JSON contains its numeric `revision`, `pinned_board_ids`,
and `publishing`. Pass the separate workspace result's `etag` back to
`setPinnedBoards(boardIds, etag)` for its `If-Match` header. The sidebar
handles a `412` by applying the intended edit to a fresh workspace read.
Read the saved notification route with `getOwnerNoticeRoute(boardId)`. Pass that
result's independent ETag to `configureOwnerNotices(boardId, settings, etag)`.
Pass only the route fields to change. An explicit `target_thread_id: null` clears
the target when the resulting route is disabled. After an uncertain write, read
the route again before submitting another change.

Stream messages identify their `resource` as `board`, `workspace`, or `catalog`.
Board messages contain `board_id` and `board_revision`; workspace messages contain
`workspace_revision`. Catalog messages request a catalog refresh. Each message
uses `snapshot` on connection and `change` after a committed update. Conditional
reads use ETags retained from HTTP response headers.

When `outcomeUnknown` is true, inspect authoritative state before another write.

### Card and live board reads

Create a card with `createCard(card)` using the board's current
`expected_board_etag` in the request body. On success, `etag` identifies the card
and `boardEtag` identifies the updated board. Supply your own UUIDv4 `id` when
you need focused readback after an uncertain response. Use `getCard(id)` for that
readback or `listCards(boardId, { ticket })` to find an active card by its ticket.

Use `getBoardIfChanged(boardId, etag)` with the HTTP ETag returned by `getBoard`. An unchanged
board normally returns `{ data: null, etag }`. When live publishing status is
available, the response can include current board data with the same ETag.
Compare the JSON `revision` for board versions and use the returned publishing status
to refresh its display.

`streamEvents({ boardId, watch, signal })` is an async iterable for the selected
board and optional retained boards. Each connection uses the current browser
capability. Breaking out of iteration releases the stream. An AbortSignal can
also cancel a pending read. Call again to reconnect and receive current state.

The workspace and board views use one event connection per tab. Board views
select the current board on that connection. A catalog snapshot or change
triggers a fresh read of the workspace's Attention counts, availability, and
recency. The sidebar uses the catalog's board order and refreshes without
closing the current drawer. The tab title quietly shows the sum of Blocked,
Needs Terry, and Ready for Review counts across the available boards.

While the workspace is open, the browser waits 15 seconds between scheduled
catalog checks. These checks use the same refresh path as event hints. If a saved
snapshot becomes unreadable or diverges from the accepted state, its Attention
counts become unavailable. A selected board labels its contents as previously
loaded data and keeps its recovery controls. Repairing the saved snapshot
updates both views, including when the board revision stays the same.

If the service refuses to watch the selected board, the connection continues
with workspace Attention for that view. A transient catalog failure or a refused
page cursor gets one retry from the first page. The last complete catalog remains
visible until a fresh read succeeds.

Run the focused checks with `npm --prefix web run check` and `npm --prefix web test`.

### Turn 6 board actions

The board uses `GET /boards/{board_id}?view=lanes` for the server's visible
priority and dependency order. Archived cards remain in the separate archived
section. In lanes with dependency reorder enabled and at least two active cards,
open **Ticket order** and choose **Preview ticket order**. It displays the
current and proposed assignments for the visible cards at the captured
revision. Preview is read-only. Applying a change requires the full lane
with the title and ticket filter cleared.

In **Manage lanes**, **Change lane key** preserves the lane UUID. Saved lane
labels display their current keys. The Completed lane is excluded from this
control. The request retains the original lane and board validators.

In **Board settings**, **Set batch priority** accepts distinct ticket numbers,
including archived unfinished cards. The service evaluates the selection in
one revision-checked transaction. The result lists selected effective priorities
and every actual priority change, including upstream promotions. Completed
selections and invalid ticket selections are refused. After an uncertain
response, use **Inspect saved priorities** to compare the saved values with
the submitted selection.

**Create backup** captures the current board revision and verified attachment
inventory. You can edit cards and switch boards while capture runs. Its progress,
result, and download remain with the originating board in the same tab.
**Download backup** retrieves the ZIP for the displayed captured revision and
checks its exact size and SHA-2-256 digest before offering the download. After a
lost response, **Refresh backup status** checks the original backup UUID. If
the backup is still unavailable, the capture remains pending and the next
refresh checks that same UUID.

### Turn 7 find, renumber, restore, and publish

Expand **Find cards** to search a literal title or ticket. **Search descriptions
and comments** opts into matching their displayed text. Formatting delimiters
that are hidden on screen are absent from this search, and literal code remains
searchable. **Next work** uses the service's
whole-board triage score before lane filtering and the result limit (1–200). **Ticket
number** keeps numeric order for lookup. Search results open the usual card
drawer. When a board change makes the displayed results stale, the search keeps
your filters and prompts you to run it again. Relationship ticket lookup keeps
its default numeric order.

Each tab remembers its inline filter, deeper search filters and results query,
selected card, and lane scroll positions for each board. Returning through the
board list, Back, or Forward, or reloading the tab restores that board's view.
Restored search results and drawers are read from the service. A retired search
lane resets to All lanes, and a missing saved card clears the selection.

A changed **Preview ticket order** offers **Apply ticket order**. Apply submits
the captured board validator and proposed ordered card UUIDs exactly once.
Readback checks the new assignments. A stale preview refuses and requires a fresh
preview. The service preserves card UUIDs and attachment anchors.

In **Board actions**, choose a **Native backup ZIP** and submit **Restore native
backup**. The page reads the snapshot validator, checks that its board revision
matches the rendered board, and sends one conditional ZIP PUT. Damaged backups
are refused. A successful restore refreshes the board at the same address. Card
and board text drafts remain available. An open drawer refreshes saved card fields,
comments, and attachment rows. When a restored board omits the open card, the
drawer retains its drafts and disables its write controls. An uncertain response
requires saved-state inspection before another explicit submission.

After **Create backup**, **Publish backup** publishes that exact captured backup.
The service data root must already be inside a Git worktree with an initial
commit, local branch, configured identity, and origin remote. The page displays
the publication outcome, its captured revision, and any fixed error code on the
originating board while card edits and navigation continue. It retains the
generated publication UUID for focused readback after an uncertain response.
**Refresh publication status** stays available while the publication is queued
or running, including after navigating away and returning. A new submission
requires a terminal status.
Publication does not require a board ETag because its selected backup is immutable.

Backup, publication, restoration, build activation, audit event, and approval receipt IDs use UUIDv7. Client-supplied operation IDs accept mixed case and are serialized in lowercase. Backup and publication creation time is encoded in the ID. Completion time remains in `finished_at`, and audit, approval, and restoration `at` records execution time. Other resource IDs remain UUIDv4.

### Recent boards

Returning to a recent board in the same tab shows the last confirmed lanes
and cards immediately. You can open a retained card to read its title and
description while the board refreshes. Editing and attachment previews resume
after the service confirms the board. If the refresh fails, **Retry board update**
checks the service again while keeping the retained view visible. If the service
reports that the board is closed, unhealthy, or unavailable, the view updates
to show that state. If Attention cannot refresh when you return, **Retry Attention**
reads the workspace counts again while the board stays open.

Open **Recent board cache** on the workspace page to set the budget for this tab.
The default is 4 MiB and the maximum is 32 MiB. Set it to 0 to turn the cache off.
The budget counts UTF-8 bytes of serialized board display data. The oldest
accessed entries are removed first when the budget is exceeded or reduced.
An entry larger than the budget is skipped. Reloading the page clears the
retained board data and keeps the tab's budget preference.
