# 0001 - API identity and wire contract

Date: 2026-11-10

Status: Accepted for the owner-approved decisions below. The detailed OpenAPI document is the proposed implementation contract.

## Decisions

- Use flat, plural resource addresses. Cards, boards, lanes, comments, attachments, and relationships each have their own canonical address. Use query parameters for collection filters and requested views.
- Version the API by ISO 8601 calendar date. The first namespace is `/api/2026-11-10`. API contract dates, software builds, and resource revisions are separate values.
- Use immutable UUIDv4 entity IDs generated with a cryptographically secure random source. Return lowercase, hyphenated UUIDs. Accept mixed-case UUIDv4 input and normalize before lookup, reference checks, and duplicate detection.
- Use UUIDv7 for service_instance_id so it encodes the service instance creation time.
- Keep human ticket numbers and board-local lane/priority keys separate from entity IDs. M20 changes a lane key, never its UUID. Preserve the completed-lane role independently of its display label.
- Use `snake_case` for JSON member names, path parameter identifiers, and query parameter names across the V2 API. Standard HTTP header names keep their defined spelling. Preserve enum values and user text. Source-language identifiers follow Go and JavaScript conventions.
- Keep timestamps and ordering explicit. Use 16-byte UUID storage where practical.
- Publish closed response schemas with canonical values. Accept harmless input variation without weakening meaning or authority. Preserve user text exactly.
- Name each collection response array for its plural resource, such as `boards`, `cards`, or `builds`. Both activity views use `activities`. Monitor entries contain only event identity, UTC time, board revision, actor identity and kind, operation, and ticket numbers. Workspace board summaries use `boards`. Empty collections return an empty array, and `next_cursor` retains its pagination meaning.

During the reboot, use only the current API, CLI, and storage contracts. Use fresh test data when a contract changes.

## Contract details

[OpenAPI contract](../openapi.yaml) uses OpenAPI 3.2.1. It describes the planned full API, with each operation mapped to its milestone. Published fields, status codes, media types, response headers, and preconditions are explicit.

Ordinary input objects ignore unknown, non-protected fields. Security-sensitive commands are closed. Mixed-case UUIDs and documented enum spellings are accepted. Duplicate JSON keys, conflicting selectors, wrong types, protected attribution/identity writes, and malformed known values are refused. Empty or unknown-only patches refuse. External chat UUIDs have their own schema and can use UUID versions other than v4.

Full card responses, card version responses, and native card snapshots require a positive safe-integer `revision` identifying the board commit that last changed that card. Unrelated board edits preserve this value, and successive card revisions may skip values. Card creation and patch inputs reject this service-managed field. HTTP `ETag` headers continue to provide conditional validators. Other resource JSON version fields retain their existing contracts. Explicit mutation preconditions, including approval card-version inputs, continue to carry entity tags.

Full lane responses and native lane snapshots require a positive safe-integer `revision` identifying the board commit that created or last changed that lane, including its position. Initial workflow lanes use revision 1. Unrelated board edits preserve this value, and successive lane revisions may skip values. Lane creation and patch inputs reject this service-managed field. Lane responses retain their HTTP `ETag` headers, and lane mutation requests retain both lane and board conditional validators. Native snapshots remain format version 2 under the current contract.

Full attachment responses and native attachment snapshots require a service-managed
positive safe-integer `revision` identifying the board commit that uploaded the
attachment or last changed its metadata. Unrelated board edits and no-op operations
preserve this value, and successive attachment revisions may skip values. Attachment
HTTP validators remain in headers.

The attachment digest field is `sha2_256`, the lowercase hexadecimal SHA-2-256 digest
of the file bytes. Hash names in API fields and documentation explicitly identify
their hash family and digest size.

CLI updates accept `--revision N` to require the positive numeric `revision` from a prior read. The CLI fetches the current representation, compares any supplied revision, and uses the returned ETag for the write. With no explicit revision, only changes after that fetch are guarded. For a card write, comment creation, or attachment upload, the optional numeric revision checks the addressed card during preflight. Lane rename, retirement, and approval-button configuration check the addressed lane revision. Attachment detach checks the addressed attachment revision and supplies both resource and board validators. Card creation, policy changes, and `lane add` check the board revision. The server guards the addressed resource version at commit.

`card patch`, `card description`, `comment post`, and `lane add` require a positional input source. Pass a file path or `-` for standard input. Card descriptions take UTF-8 text and preserve whitespace and line endings. The other three commands take a JSON object.

Item writes use the addressed resource's strong ETag. Operations spanning resources carry explicit board/card preconditions in request bodies or in the documented HTTP headers. Data, permission checks, derived changes, and audit commit together. A lost or unreadable write response requires inspection before another attempt.

The proposed security baseline is one loopback origin, exact authority checks, separate runtime-issued Human and Bot capabilities, and authenticated API calls. A same-origin browser bootstrap issues a separate, process-scoped Human capability. Foreign/null origins are refused. CORS grants name only the configured origin and enumerated headers/methods. Tokens stay out of URLs, persisted browser storage, logs, and error bodies. Browser streaming and file reads use authenticated fetch.

Board lifecycle uses `PATCH /boards/{board_id}` with a board validator and
the board's `close_board` grant. Close drains admitted board operations and
flushes any enabled publisher through the last accepted open revision. The
availability change then commits as one audited revision. On flush failure,
the board remains open. Board metadata, catalog entries and shared event
streams remain readable across close and reopen. The same board ID, URL and
stored cards survive the transition.

Process lifecycle operations require workspace administration. Shutdown uses
`PATCH /service` with the current service validator. Activation uses
`POST /activations` with a validated build UUID and the current service
validator. The receipt can be inspected by its UUID after reconnecting. Both
operations drain admitted work and streams and flush enabled board and workspace
publishers. A failed flush leaves the service running. Successful activation
retains the bound port and board URLs, creates a new service instance, and
rotates the browser capability. Configured Human and Bot runtime credentials
remain valid for the local CLI. Read back the original request or service health
after an uncertain response before deciding whether to submit again.

Board recovery preserves existing global user configuration. A recovery candidate with an unknown or incompatible user identity refuses before mutation.

A resolved workflow must permit creation in at least one active lane through a lane grant for a board member with the `create_card` capability. Every active lane reachable from a creation lane must have a path through allowed directed transitions to the Completed lane. Transitions along a path may be performed by different actors. Apply the shared validator after shorthand expansion at board creation, on lane retirement, on policy updates, and during native snapshot admission.

OpenAPI describes messages and authentication schemes. Runtime enforcement is still required for origin/authority checks, forbidden headers, duplicate-key detection, authorization, transaction behavior, and uncertain outcomes. Those requirements are recorded in the contract's `x-*` policy fields.

Every card has a non-null `owner_id` referencing a board member. Card creation
may omit it to use `board.default_owner_id`; assignment patches require a member
UUID and cannot clear the owner. Published boards and native snapshots require
`default_owner_id`. Board creation may omit the default when the global runtime
Bot ID is a member. Custom boards without Bot must supply a member explicitly.
Changing the default requires `manage_board` and applies to subsequent card
creation. Owner assignment audit values record the previous and new user UUIDs.

## Sources

- [OpenAPI 3.2.1](https://spec.openapis.org/oas/v3.2.1.html)
- [UUIDs, RFC 9562](https://www.rfc-editor.org/rfc/rfc9562.html)
- [HTTP semantics, RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html)
- [Fetch and CORS](https://fetch.spec.whatwg.org/#http-cors-protocol)
