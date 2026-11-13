# CLI and Bot workflows

The native CLI is an API client. Its online operations use the same
`/api/2026-11-10` service endpoints as other authorized clients. The configured
principal supplies authority; running a command does not bypass membership,
capability checks, conditional writes or immutable Completed-card rules.

## Read and modify work

API paths below omit the common prefix. Commands are command families; append
`--help` for the current required arguments and modes. Board-owned selectors take
`BOARD_ID` first. Card UUIDs and ticket numbers resolve within that board.

| CLI workflow | Preparation and API calls |
| --- | --- |
| `board list`, `workspace info` | `GET /boards` and `GET /workspace` respectively; follow collection cursors as required. |
| `board get` | `GET /boards/{id}`, with `view=lanes` for the lane/card view. |
| `card list` | `GET /cards?board_id={id}` with the selected filters. |
| `card search`, `card triage-order` | If a lane is supplied, first `GET /lanes/{lane_id}` to validate its board membership. Then `GET /cards?board_id={id}` with the selected search filters and ordering. |
| `card get` | Resolve a ticket through `GET /boards/{id}?view=ticket&ticket=N` when needed, then `GET /cards/{card_id}`. |
| `card inspect` | `GET /boards/{id}?view=ticket&ticket=N`, optionally with `include_prose=true`. A UUID selector is resolved to its ticket first. The response contains the focused card, relationships and hierarchy; activity is a separate workflow. |
| `card lookup` | Accepts search filters, not a list of card selectors. If a lane is supplied, first `GET /lanes/{lane_id}` to validate its board membership. Then `GET /cards?board_id={id}&sort=ticket` with the requested search, content, lane, limit and cursor filters. |
| `card create` | Read board choices/current validator, then `POST /cards`; the CLI supplies `board_id` and `expected_board_etag`. JSON mode can include atomic relationship seeds. |
| `card title`, `description`, `assign`, `set-priority`, `move`, `archive`, `unarchive` | Resolve/read the card and required board state; conditional `PATCH /cards/{id}` updates the selected fields. |
| `card set-priorities` | Resolve explicit selectors when supplied and obtain the current board validator; `PATCH /cards` with `board_id`, `expected_board_etag`, priority and one selection form: `card_ids`, `lane_id`, or `all_unfinished: true`. Read returned effective priorities and affected state. |
| `comment list`, `comment post` | `GET /comments` with board/card filters; posting first reads required card/board validators, then `POST /comments`. |
| `relationship list/get/create/patch/remove` | Read endpoints and validators as needed, then `GET /relationships`, focused GET, POST, conditional PATCH or DELETE on the relationship resource. |
| `attachment list/get/upload/download/detach` | Read required card/board/attachment metadata; GET the collection/resource/content, POST file bytes, or conditional DELETE. Verify downloaded bytes against metadata. |
| `workspace pin set/add/remove` | Read workspace/catalog, derive the ordered pin list, then conditional `PATCH /workspace`. |
| `lane add/rename/set-key/retire/set-approval-button` | Read board/lane preconditions; `POST /lanes` or conditional `PATCH /lanes/{id}` for the requested change. |
| `ticket-order preview/apply` | `GET /ticket-orders/{lane_id}?preview=true`, then `PUT /ticket-orders/{lane_id}` with the saved plan and board precondition. |
| `activity list` | `GET /activities` with board, view, interval and optional resource/actor/operation filters; follow returned cursors. |

Batch-priority examples:

```text
localswim card set-priorities BOARD_ID PRIORITY_ID CARD_ID [CARD_ID ...]
localswim card set-priorities --lane LANE_ID BOARD_ID PRIORITY_ID
localswim card set-priorities --force BOARD_ID PRIORITY_ID
```

Explicit cards and an explicit lane run with the normal validators. Omitting both
selects unfinished cards across the board and prompts unless `--force` is supplied.
Blocking dependencies can promote the effective priority beyond the requested
priority. V1 has no browser batch-priority control.

## Administration and recovery

| CLI workflow | API calls |
| --- | --- |
| `board create`, `board rename/patch/close/reopen` | `POST /boards` or read current board state followed by conditional `PATCH /boards/{id}`. |
| `board verify`, `board recover` | `GET /verifications/{id}`; explicit recovery uses `POST /recoveries/{id}` with its documented mode and preconditions. |
| `board export`, `board restore` | `GET /snapshots/{id}` exports native data; restore reads current metadata then conditional `PUT /snapshots/{id}` with the native archive. |
| `backup create/info/download` | `POST /backups`, `GET /backups/{id}`, then `GET /backups/{id}/content` as needed. File verification operates on the supplied local archive. |
| `restoration create/get` | `POST /restorations` and `GET /restorations/{id}` for a portable restore operation and its receipt. |
| `publication create/get` | `POST /publications` and `GET /publications/{id}`; publish the selected immutable backup and inspect the original job. |
| Board/workspace publishing settings | Read the relevant board/workspace, then conditional PATCH on that resource. |
| `board notifications status/target/enable/disable` | `GET /notifications/{board_id}` and conditional `PATCH /notifications/{board_id}`. |
| `service status/builds/activate/activation/stop` | `GET /service`, `GET /builds`, `POST /activations`, `GET /activations/{id}`, or conditional `PATCH /service` for the chosen command. |

`serve` starts the local service. Supported `--data` inspection modes read saved
data directly and have their own offline semantics. They do not call a stopped
HTTP server. Human approval is exposed through the authorized browser/API flow;
the native CLI has no approval command.

For online writes, `--revision` checks the user-specified revision against the
authoritative read. The client then sends the required HTTP precondition or body
validator. After an uncertain write, inspect the original resource or operation
receipt before retrying. A correlation ID is not a universal idempotency key.

Commands reading or writing payloads use a file path or explicit `-` for
stdin/stdout. See [CLI help](../cmd/localswim/cli_help.go),
[OpenAPI](openapi.yaml), and [relationship direction](api-relationships.md) for
the full command grammar, payloads, headers and examples. The Architect maintains
these sequences alongside implementation changes in each PR.
