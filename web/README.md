# Browser application

`index.html`, `app.mjs`, and `app.css` are embedded in the V2 executable. Open
the service's loopback URL or a board link at `/boards/{board_id}`. The page opens
a same-origin Human session and keeps the capability in memory. The HTML and
local assets are independent of the capability.

Open a card to edit its text, lane, or owner. Each submission uses the last card
ETag read from the HTTP response header. Card JSON exposes its positive integer
`revision`; the drawer retains the header validator separately. A stale edit or uncertain response triggers a focused
read so you can compare your draft with the saved card before deciding whether
to submit again. Inspect or Refresh rereads the active editor and preserves
the draft. If readback fails, inspect the affected card or board before
submitting another change there. The page makes one write attempt per submission.

Card details also show attributed comments and attachments. New comments and
uploads have client-assigned IDs. Immutable comment JSON contains identity, parent
references, author, text, and timestamps. The comment validator is returned in the
HTTP response header. Posting uses the parent card's current validator and advances
its revision. Comment lists and focused attachment reads
can identify the exact submission. Files up to 10 MiB can be uploaded. The board offers its newest
comments and lane insertion, rename, and retirement controls. Server-side policy determines
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
confirmed approval and retains any comment draft. A stale or uncertain response
keeps the draft available for inspection. Manage lanes can enable or disable
Approve all in lane for Ready for Review. The action submits the complete rendered
active membership and its original versions, including the rendered board version.
A stale set requires inspection before another submission.

Add a typed link from the drawer or seed a link while creating a card. The same
relationship is visible from both cards. Human comments link ticket references to
focused board reads, including archived tickets. Bot comments render as text. Card
priorities are shown on the board and in the drawer and refresh after link edits.

Attachments offer authenticated downloads. Raster attachments also offer a preview
and cover selection. The selected cover appears on the card in its lane. Detach
revokes active file access and clears the selected cover when it refers to the file.
History retains the attachment ID and attribution.

Expand **Board actions** to turn automatic publishing on or off for that board.
On the **Workspace** page, administrators can save workspace publishing and
pin order independently. The board uses its own HTTP validator. Both workspace
forms use the workspace validator and retain unsaved changes across readback.

Use **Refresh board publishing status** or **Refresh workspace publishing
status** to inspect pending work, failed pushes, and the last successful publish.
A failed push can recover automatically without another edit. If a settings
save has an uncertain outcome, the browser reads the saved setting before
another submission and keeps the draft available for comparison.

## Board availability

Members with the board's `close_board` grant can select **Close board** under
**Board actions**. A closed board stays at the same address and offers **Reopen
board** to those members. Other boards and the Workspace remain available. The
browser keeps the selected card and unsaved card, add-card, lane, and publishing
drafts in memory for reopening in the same tab.

Each action sends one availability PATCH with the board's current HTTP ETag.
The browser reads the saved board after success, a stale validator, or an
uncertain response. If readback fails, use **Inspect saved board** or **Refresh**
before submitting another change. The browser never replays the PATCH.

## Service lifecycle

Workspace administrators can use **Activate staged build** when the Service
panel reports a valid candidate. The browser reconnects on the original port,
obtains a fresh session capability, and reads the activation receipt. Board
links stay the same. **Shut down service** requests a graceful stop and reports
when the service accepts the drain. A final publication failure leaves the
service running and available for status inspection.

Each action makes one write attempt. After an uncertain response, use
**Refresh service status** to check the service and the original activation
receipt before submitting another action.

## Browser API client

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
`setPinnedBoards(boardIds, etag)` for its `If-Match` header. A
`412` calls for a fresh workspace read and a user decision on the original change.
Use the board ETag for `configureOwnerNotices(boardId, settings, etag)`. Pass only the
notice settings to change. An explicit `target_thread_id: null` clears the target.

Stream messages identify their `resource` as `board`, `workspace`, or `catalog`.
Board messages contain `board_id` and `board_revision`; workspace messages contain
`workspace_revision`. Catalog messages request a catalog refresh. Each message
uses `snapshot` on connection and `change` after a committed update. Conditional
reads use ETags retained from HTTP response headers.

When `outcomeUnknown` is true, inspect authoritative state before another write.

## Card and live board reads

Create a card with `createCard(card)` using the board's current
`expected_board_etag` in the request body. On success, `etag` identifies the card
and `boardEtag` identifies the updated board. Supply your own UUIDv4 `id` when
you need focused readback after an uncertain response. Use `getCard(id)` for that
readback or `listCards(boardId, { ticket })` to find an active card by its ticket.

Use `getBoardIfChanged(boardId, etag)` with a board summary ETag. An unchanged
board normally returns `{ data: null, etag }`. When live publishing status is
available, the response can include current board data with the same ETag.
Use the ETag to compare board revisions and the returned publishing status
to refresh its display.

`streamEvents({ boardId, watch, signal })` is an async iterable for the selected
board and optional retained boards. Each connection uses the current browser
capability. Breaking out of iteration releases the stream. An AbortSignal can
also cancel a pending read. Call again to reconnect and receive current state.

Run the focused checks with `npm --prefix web run check` and `npm --prefix web test`.

## Turn 6 board actions

The board uses `GET /boards/{board_id}?view=lanes` for the server's visible
priority and dependency order. Archived cards remain in the separate archived
section. Per-lane **Preview ticket order** accepts comma- or space-separated
tickets, or an empty selection for all active cards. It displays the current
and proposed assignments at the captured revision. Preview is read-only.

In **Manage lanes**, **Change lane key** preserves the lane UUID. Saved lane
labels display their current keys. The Completed lane is excluded from this
control. The request retains the original lane and board validators.

In **Board actions**, **Set batch priority** accepts distinct ticket numbers,
including archived unfinished cards. The service evaluates the selection in
one revision-checked transaction. The result lists selected effective priorities
and every actual priority change, including upstream promotions. Completed
selections and invalid ticket selections are refused. An uncertain write is
followed by a saved-state read and is never automatically submitted again.

**Create backup** captures the current board revision and verified attachment
inventory. **Download backup** retrieves the ZIP through authenticated fetch,
checks its exact size and SHA-2-256 digest, and then offers the download. A lost
capture response is inspected using the original generated backup UUID. Capture
is never replayed automatically. A later explicit capture uses a fresh UUID.

## Turn 7 find, renumber, restore, and publish

Expand **Find cards** to search a literal title or ticket. **Search descriptions
and comments** opts into content matching. **Next work** uses the service's
whole-board triage score before lane filtering and the result limit (1–200). **Ticket
number** keeps numeric order for lookup. Search results open the usual card
drawer. When a board change makes the displayed results stale, the search keeps
your filters and prompts you to run it again. Relationship ticket lookup keeps
its default numeric order.

Each tab remembers its search filters and results query, selected card, and lane
scroll positions for each board. Returning through the board list, Back, or
Forward, or reloading the tab restores that board's view. Restored search results
and drawers are read from the service. A retired search lane resets to All lanes,
and a missing saved card clears the selection.

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
the publication outcome and fixed error code. It retains the generated publication
UUID for focused readback after an uncertain response. **Refresh publication
status** stays available while the publication is queued or running, including
after navigating away and returning. A new submission requires a terminal status.
Publication does not require a board ETag because its selected backup is immutable.
