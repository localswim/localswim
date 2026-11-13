# #0583 independent technical review

## Assessment

Reviewed the T2 workspace source on 2026-11-13 with Sol / High in
`codex/0581-t2-foundation`. The review used the native V2 Go and OpenAPI
contracts, the #0582 observed-behavior ledger, and its reference screenshots.

- Same-origin, credentialless bootstrap keeps the bearer capability in module
  memory. Concurrent reads share bootstrap. A validated 401 can renew once,
  and an older request can clear only the promise it used. A caller's
  cancellation leaves shared bootstrap available to other readers.
- The workspace adapter checks native record shape, UUID versions, UTC
  timestamps, revision bounds, and strong ETags. Catalog traversal rejects
  duplicate IDs, cursor loops, and an incomplete bounded traversal. It
  returns the server's order and preserves unavailable summary metadata.
- Root entry replaces its history entry with the selected board. Sidebar
  selection pushes a history entry. Back/Forward resolves the current
  pathname and changes the mounted board. An abandoned read cannot
  overwrite a newer selection. Sidebar rows stay keyed by board UUID,
  and per-board memory keeps confirmed data, scroll, and focus across visits.
- The Architect clarified that the native board UUID is the stable browser
  key under `/board/<key>`. V1's filesystem key has no native V2 counterpart.
  The integrated packet records the bookmark identity decision.

## Corrections re-reviewed

- The shared HTTP layer now gives authenticated reads a deadline, including
  response body consumption, while keeping navigation cancellation. The
  workspace also bounds the full paginated read. The mocked stalled-body
  regression covers both deadline and caller cancellation.
- The HTTP layer now bounds the response body and rejects malformed UTF-8
  before parsing JSON. The split-chunk regression distinguishes malformed
  bytes from a valid literal U+FFFD.
- The selected board's Retry now refreshes the workspace, service, and
  identity. Session props clear while setup is revalidated. A failed
  setup read keeps workspace Retry reachable with any available sidebar
  rows. Selecting a retained row or using Back/Forward now exposes the
  board Retry and any last-confirmed view while the workspace remains
  unavailable. See `web/clean-room/main.ts:128` and `:176`.

- The external-window link now follows the observed V1 states. An unknown
  bookmark uses the current bookmark path and opens it in a new window.
  An empty workspace retains the label without a destination. Selected
  boards still open their stable browser URL. See
  `web/clean-room/workspace/render.ts:116` and `:301`.
- The workspace loads Inter from a bundled same-origin WOFF2 with the
  upstream license. Board rows have an explicit keyboard focus outline.
  See `web/clean-room/entry.css:3` and
  `web/clean-room/workspace/workspace.css:311`.

## Verification

The focused source findings are resolved. The UI Lead owns executable checks,
native fixture and browser verification, and the exact candidate identifiers.
The matched-state visual review and Terry's visual checkpoint have their own
evidence.
