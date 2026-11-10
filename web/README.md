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
comments and lane insertion and rename controls. Server-side policy determines
which actions the Human session may perform. Lane JSON carries a positive safe integer
`revision`. `renameLane(laneId, revision, boardEtag, label)` reads the lane afresh,
checks the expected numeric revision, and uses the returned HTTP ETag for its
conditional PATCH. It retains the caller's board ETag precondition. An invalid,
stale, or failed read prevents the PATCH. Lane list and write responses validate
the numeric revision and response shape. HTTP validators stay in response headers.

Archive a card from its drawer. Expand Archived cards to reopen it and restore
it to the same lane. Expand History in the drawer to inspect that card's audited
changes. On a Ready for Review card, Reviewed & approved posts the approval
comment and moves the card to Completed in one action. The drawer closes on a
confirmed approval and retains any comment draft. A stale or uncertain response
keeps the draft available for inspection.

Add a typed link from the drawer or seed a link while creating a card. The same
relationship is visible from both cards. Human comments link ticket references to
focused board reads, including archived tickets. Bot comments render as text.

Attachments offer authenticated downloads. Raster attachments also offer a preview
and cover selection. The selected cover appears on the card in its lane.

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

Each successful result exposes the JSON object as `data` and the server's ETag as
`etag`. Pass the workspace ETag back to `setPinnedBoards(boardIds, etag)`. A
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
board returns `{ data: null, etag }` and the caller can retain its existing data.

`streamEvents({ boardId, watch, signal })` is an async iterable for the selected
board and optional retained boards. Each connection uses the current browser
capability. Breaking out of iteration releases the stream. An AbortSignal can
also cancel a pending read. Call again to reconnect and receive current state.

Run the focused checks with `npm --prefix web run check` and `npm --prefix web test`.
