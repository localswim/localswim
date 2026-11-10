# 0001 - API identity and wire contract

Date: 2026-11-10

Status: Accepted for the owner-approved decisions below. The detailed OpenAPI document is the proposed implementation contract.

## Decisions

- Use flat, plural resource addresses. Cards, boards, lanes, comments, attachments, and relationships each have their own canonical address. Use query parameters for collection filters and requested views.
- Version the API by ISO 8601 calendar date. The first namespace is `/api/2026-11-10`. API contract dates, software builds, and resource revisions are separate values.
- Use immutable UUIDv4 entity IDs generated with a cryptographically secure random source. Return lowercase, hyphenated UUIDs. Accept mixed-case UUIDv4 input and normalize before lookup, reference checks, and duplicate detection.
- Keep human ticket numbers and board-local lane/priority keys separate from entity IDs. M20 changes a lane key, never its UUID. Preserve the completed-lane role independently of its display label.
- Use `snake_case` for JSON member names, path parameter identifiers, and query parameter names across the V2 API. Standard HTTP header names keep their defined spelling. Preserve enum values and user text. Source-language identifiers follow Go and JavaScript conventions.
- Keep timestamps and ordering explicit. Use 16-byte UUID storage where practical.
- Publish closed response schemas with canonical values. Accept harmless input variation without weakening meaning or authority. Preserve user text exactly.
- Name each collection response array for its plural resource, such as `boards`, `cards`, or `builds`. Both activity views use `activities`. Workspace board summaries use `boards`. Empty collections return an empty array, and `next_cursor` retains its pagination meaning.

During the reboot, use only the current API, CLI, and storage contracts. Use fresh test data when a contract changes.

## Contract details

[OpenAPI contract](../openapi.yaml) uses OpenAPI 3.2.1. It describes the planned full API, with each operation mapped to its milestone. Published fields, status codes, media types, response headers, and preconditions are explicit.

Ordinary input objects ignore unknown, non-protected fields. Security-sensitive commands are closed. Mixed-case UUIDs and documented enum spellings are accepted. Duplicate JSON keys, conflicting selectors, wrong types, protected attribution/identity writes, and malformed known values are refused. Empty or unknown-only patches refuse. External chat UUIDs have their own schema and can use UUID versions other than v4.

CLI updates accept `--revision N` to require the numeric `revision` from a prior read. The CLI fetches the current representation, compares any supplied revision, and uses the returned ETag for the write. With no explicit revision, only changes after that fetch are guarded.

Item writes use the addressed resource's strong ETag. Operations spanning resources carry explicit board/card preconditions in their bodies. Data, permission checks, derived changes, and audit commit together. A lost or unreadable write response requires inspection before another attempt.

The proposed security baseline is one loopback origin, exact authority checks, separate runtime-issued Human and Bot capabilities, and authenticated API calls. A same-origin browser bootstrap supplies only the Human capability. Foreign/null origins are refused. CORS grants name only the configured origin and enumerated headers/methods. Tokens stay out of URLs, persisted browser storage, logs, and error bodies. Browser streaming and file reads use authenticated fetch.

Board recovery preserves existing global user configuration. A recovery candidate with an unknown or incompatible user identity refuses before mutation.

OpenAPI describes messages and authentication schemes. Runtime enforcement is still required for origin/authority checks, forbidden headers, duplicate-key detection, authorization, transaction behavior, and uncertain outcomes. Those requirements are recorded in the contract's `x-*` policy fields.

## Sources

- [OpenAPI 3.2.1](https://spec.openapis.org/oas/v3.2.1.html)
- [UUIDs, RFC 9562](https://www.rfc-editor.org/rfc/rfc9562.html)
- [HTTP semantics, RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html)
- [Fetch and CORS](https://fetch.spec.whatwg.org/#http-cors-protocol)
