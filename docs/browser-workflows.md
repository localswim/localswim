# Browser workflows

The fresh UI reproduces V1's observed interface using the V2 API. This guide
maps user workflows to requests. All API paths below start with
`/api/2026-11-10`. Braced values are resource IDs. Reads listed together may run
concurrently; follow-up validation reads verify that assembled data is coherent.

## Available and in-progress workflows

T2 was accepted into `ui/v1-clean-room` and passed Terry's visual checkpoint.
T3 is in development. Its rows describe the current implementation contract,
not a production deployment or completed acceptance.

| User workflow | Status | API sequence |
| --- | --- | --- |
| Open the workspace | T2 accepted | `POST /browser-session` establishes the browser capability; `GET /session` reads the principal. Read `GET /workspace`, `GET /boards` through every cursor, and `GET /service`. |
| Select a board or follow a bookmark | T2 accepted | `GET /boards/{board_id}?view=lanes`; load board-scoped users and priorities and `GET /policies/{policy_id}`; reread `GET /boards/{board_id}` to confirm a coherent revision. |
| Retry a failed board load | T2 accepted | Repeat the authorized board-read sequence. Preserve the last confirmed view where read admission permits it. |
| Return with Back/Forward and restore scrolling | T2 foundation; fuller retained-state work planned in #0599 | Browser history and local view state select the board and offsets; the normal board-read sequence confirms current server data. Scrolling itself sends no mutation. |
| Pin, unpin or reorder boards | T3, #0585 | Read `GET /workspace` and the board catalog, then `PATCH /workspace` with the complete ordered `pinned_board_ids` and current workspace precondition. Reread workspace after an uncertain response. |
| Resize/collapse the sidebar | T3, #0585 | Local presentation state. No board-data mutation request. |
| Filter cards and reveal older Completed cards | T3, #0586 | Local projection of confirmed board data for simple filtering; server-backed matching uses `GET /cards?board_id={board_id}` with the selected search filters. Visibility changes do not mutate cards. |
| Open a card drawer and follow its context | T3, #0587 | Read `GET /cards/{card_id}`, relevant `GET /cards`, `GET /relationships`, `GET /comments` and `GET /activities` with board/card filters; confirm board/card identity and revision before displaying the assembled context. |
| Create a card with relationships | T3, #0588 | Read allowed board/lane/owner/priority choices, then `POST /cards` with the chosen card ID, board precondition and relationship seeds. Read the original card and relationship collection when the outcome needs inspection. |
| Drag a card to a lane | T3, #0589 | Read the focused card and use the rendered board precondition; `PATCH /cards/{card_id}` with `lane_id` and card/board preconditions. Inspect card/activity after uncertainty before another submission. |
| Approve a full reviewed lane | T3, #0590 | Obtain the complete current membership and card validators; `POST /approvals` with operation ID, board ID, board validator, lane ID and cards. The server reads the configured comment from the lane. `GET /approvals/{approval_id}` inspects the original operation. |
| Preview/apply dependency ticket order | T3, #0591 | `GET /ticket-orders/{lane_id}?preview=true` with the chosen scope; `PUT /ticket-orders/{lane_id}` submits the reviewed scope and saved board precondition. Reread after uncertainty. |
| Observe live updates | T3, #0593 | Authenticated `GET /events` stream triggers authorized workspace/board refreshes. `GET /activities` supplies scoped activity when needed. Reconnect obtains fresh state. |

The browser movement workflow follows V1 drag/drop. Batch priority is provided
through the CLI/API workflow rather than a new browser control. See the
[CLI/Bot guide](cli-workflows.md).

## Approved later UI work

These workflows have existing V2 endpoints and approved UI stories. Their fresh
browser implementations remain pending the applicable turn gates.

| Workflow | Story | Request sequence |
| --- | --- | --- |
| Edit card text, owner or priority; archive/restore a card | #0594 | Read `GET /cards/{card_id}` and board context, then conditional `PATCH /cards/{card_id}` for the selected fields. Preserve drafts and inspect the original outcome when uncertain. |
| Post comments | #0595 | Read the card/board validators; `POST /comments`; refresh `GET /comments` and inspect the original comment ID after uncertainty. |
| Maintain relationships | #0596 | Read endpoints and `GET /relationships`; `POST /relationships`, conditional `PATCH /relationships/{id}`, or `DELETE /relationships/{id}`. Endpoint order determines the card-relative label. |
| Approve one card | #0597 | Read card/board state; `POST /approvals`; inspect `GET /approvals/{approval_id}`. |
| View/download/cover/detach attachments | #0598 | `GET /attachments` and `GET /attachments/{id}`; download `/attachments/{id}/content`; conditional card PATCH changes the cover; conditional attachment DELETE detaches. |
| Upload files and recover a partial batch | #0600 | Read card/board validators; `POST /attachments` for each original file; inspect the original attachment ID after uncertainty before deciding whether any remaining file can be sent. |

Use the [relationship guide](api-relationships.md) for incoming/outgoing creation
examples and the [OpenAPI contract](openapi.yaml) for exact payloads, headers,
pagination and authorization. The Architect updates this guide when a PR changes
the workflow or its status. Endpoint availability alone does not authorize adding
a browser control outside the V1-equivalent scope.
