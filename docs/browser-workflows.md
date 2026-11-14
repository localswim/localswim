# Browser workflows

The fresh UI reproduces V1's observed interface using the V2 API. This guide
maps user workflows to requests. All API paths below start with
`/api/2026-11-10`. Braced values are resource IDs. Reads listed together may run
concurrently; follow-up validation reads verify that assembled data is coherent.

The browser shows a requested change after the service confirms it. While a
write is pending, the browser keeps the last confirmed state and can show
progress. A successful response confirms the change without a separate readback.
When the response is missing or unusable, inspect the original operation before
retrying it.

## Available and in-progress workflows

T2 was accepted into `ui/v1-clean-room` and passed Terry's visual checkpoint.
T3, T4 and T5 were accepted and merged into `ui/v1-clean-room` in PRs #3,
#4 and #5. T6 integrated qualification is in progress. Final demo, cutover
and merge to `main` still require Terry's approval.

| User workflow | Status | API sequence |
| --- | --- | --- |
| Open the workspace | T2 accepted | `POST /browser-session` establishes the browser capability; `GET /session` reads the principal. Read `GET /workspace`, `GET /boards` through every cursor, and `GET /service`. |
| Select a board or follow a bookmark | T2 accepted | `GET /boards/{board_id}?view=lanes`; load board-scoped users and priorities and `GET /policies/{policy_id}`; reread `GET /boards/{board_id}` to confirm a coherent revision. |
| Retry a failed board load | T2 accepted | Repeat the authorized board-read sequence. Preserve the last confirmed view where read admission permits it. |
| Return with Back/Forward and restore scrolling | T4 accepted, #0599 | Browser history and local view state select the board and offsets; the normal board-read sequence confirms current server data. Scrolling itself sends no mutation. |
| Pin, unpin or reorder boards | T3, #0585 | Read `GET /workspace` and the board catalog, then `PATCH /workspace` with the complete ordered `pinned_board_ids` and current workspace precondition. Reread workspace after an uncertain response. |
| Resize/collapse the sidebar | T3, #0585 | Local presentation state. No board-data mutation request. |
| Filter cards and reveal older Completed cards | T3, #0586 | For a nonempty search, read all pages of `GET /cards?board_id={board_id}&q={query}&search_content=true&limit=200`, following `next_cursor`. Apply the matching IDs and Completed visibility setting to the local projection of confirmed board data. Clearing the search removes the matching-ID filter. Visibility changes do not mutate cards. |
| Open a card drawer and follow its context | T3, #0587 | Read `GET /cards/{card_id}`, relevant `GET /cards`, `GET /relationships`, `GET /comments` and `GET /activities` with board/card filters; confirm board/card identity and revision before displaying the assembled context. |
| Create a card with relationships | T3, #0588 | Read allowed board/lane/owner/priority choices, then `POST /cards` with the chosen card ID, board precondition and relationship seeds. Read the original card and relationship collection when the outcome needs inspection. |
| Drag a card to a lane | T3, #0589 | Read the focused card and use the rendered board precondition; `PATCH /cards/{card_id}` with `lane_id` and card/board preconditions. Terry can drag a card to Completed when the policy grants that move. The Reviewed & Approved action is a separate workflow. Inspect card/activity after uncertainty before another submission. |
| Approve a full reviewed lane | T3, #0590 | Obtain the complete current membership and card validators; `POST /approvals` with operation ID, board ID, board validator, lane ID and cards. The server reads the configured comment from the lane. `GET /approvals/{approval_id}` inspects the original operation. |
| Preview/apply dependency ticket order | T3, #0591 | `GET /ticket-orders/{lane_id}?preview=true` with the chosen scope; `PUT /ticket-orders/{lane_id}` submits the reviewed scope and saved board precondition. Reread after uncertainty. |
| Observe live updates | T3, #0593 | Authenticated `GET /events` stream triggers authorized workspace/board refreshes. `GET /activities` supplies scoped activity when needed. During reconnect, the draft remains editable and submission waits for the board and session to be confirmed again. |

The browser movement workflow follows V1 drag/drop. Batch priority is provided
through the CLI/API workflow rather than a new browser control. See the
[CLI/Bot guide](cli-workflows.md).

## T4 drawer and history work

These workflows are accepted on the UI integration branch.

| Workflow | Story | Request sequence |
| --- | --- | --- |
| Edit card text, owner or priority | #0594 | Read `GET /cards/{card_id}` and board context, then conditional `PATCH /cards/{card_id}` for the selected field. Preserve drafts and inspect the original outcome when uncertain. |
| Post comments | #0595 | Read the card/board validators; `POST /comments`; refresh `GET /comments` and inspect the original comment ID after uncertainty. |
| Maintain relationships | #0596 | Read endpoints and `GET /relationships`; `POST /relationships`, conditional `PATCH /relationships/{id}`, or `DELETE /relationships/{id}`. Endpoint order determines the card-relative label. |
| Approve one card | #0597 | Start closing the tray on click. Send `POST /approvals` with the captured card and board validators. Display the move after confirmation. Inspect `GET /approvals/{approval_id}` after uncertainty. |
| View/download/cover/detach attachments | #0598 | `GET /attachments` and `GET /attachments/{id}`; download `/attachments/{id}/content`; conditional card PATCH changes the cover; conditional attachment DELETE detaches. |
| Restore board navigation and retain board reads | #0599 | History and a per-tab cache retain view state. An authorized board read confirms the current revision before writes resume. |

## T5 attachment ingestion

Read the card and board validators, then `POST /attachments` for each original
file. Inspect the original attachment ID after uncertainty before deciding
whether any remaining file can be sent. This workflow is accepted under #0600.
Create-dialog files remain staged until card creation is confirmed. A partial
batch preserves each original attachment ID; after reload, unsent files require
exact file reselection and explicit Retry. Encoded multipart filename metadata
preserves supported UTF-8 names and original file bytes.

The bounded V1 drawer and navigation survey exposed no browser archive/restore
control. Those lifecycle operations remain available through the CLI/API.

Use the [relationship guide](api-relationships.md) for incoming/outgoing creation
examples and the [OpenAPI contract](openapi.yaml) for exact payloads, headers,
pagination and authorization. The Architect updates this guide when a PR changes
the workflow or its status. Endpoint availability alone does not authorize adding
a browser control outside the V1-equivalent scope.
