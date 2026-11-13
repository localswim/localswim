# T2 PR #1 correction evidence

This packet supersedes the original PR screenshots and release sizes for the
corrected candidate. The earlier native reads, configured stylesheet check,
route behavior, and Git hook qualification remain recorded in the original
handoff and independent reports.

The corrected executable SHA-256 is
`daab22293fb03a43428f456fbcbd8747289df3f016ef1c663d744f4cfdcd7bb3`.
V1 remains at http://127.0.0.1:8831/ and V2 at http://127.0.0.1:8838/.
The empty workspaces remain available on ports 8842 and 8841 respectively.

## Corrections

- The checked Node build, hook and clean-room test project now enables all six
  missing compiler flags. A fresh Sol High reviewer checked the effective
  TypeScript 7.0.2 configuration and reproduced `TS2532` for unchecked
  indexing and `TS6133` for an unused local in an isolated checked MJS probe.
  The same probe compiled at the original PR head.
- Freshly drawn pushpin and disclosure shapes follow enlarged V1 render
  crops. The outline pin has angled shoulders and a tapered stem. The
  cache row has a solid six-by-seven-pixel triangle.
- Keyboard focus outlines the entire selected row with V1's measured
  stroke, offset and corner radius. The empty and unknown-bookmark
  panes expose the same 198 px horizontal extent in their 78 px narrow
  viewport. A separate selected-board read failure also exposes a 198 px
  scroll extent and keeps Retry reachable.
- Loading text remains visible while the board stylesheet is pending.
  The lane list waits for its styles and restores scrolling and focus
  once the stylesheet settles. Custom styles apply after that baseline
  settles. A separate delayed-stylesheet check observed the label while
  the first card was hidden, then observed the first card after loading.
- Desktop backlog measurements match the 127 px first card and 122.5 px
  wrapped cards. The seven measured rows from #0001 through #0023 have
  top positions within 0.011 CSS px of V1. The gap is 7 px.
- Both settled populated and empty selected boards show the update label
  in the header. Empty workspace and unknown bookmark suppress it.
  At 390 px, the Beta text starts at x=106.90, y=78.83 in V1 and
  x=107.00, y=78.83 in V2. Age values come from each runtime.

## Exact executable browser evidence

The corrected manifest contains nine matched JPEG pairs at 1366 × 900 and
390 × 844 with effective DPR 1 and visual scale 1. It records populated Alpha,
keyboard-focused Beta, empty workspace and unknown bookmark at both widths.
The desktop pair also records Beta loading. The loading captures use two
disposable loopback proxies that delay only the selected board's read by two
seconds. They forward the installed UI and native API unchanged. The proxies
allow the actual loading states to paint before capture.
The additional V2-only frame records loading while the stylesheet is delayed.

Keyboard Enter selected Beta in both runtimes and left focus on the Beta row.
The unknown-bookmark link targets the current missing path. The empty workspace
has no external-window destination. Browser state and geometry measurements are
retained beside the screenshots. Five bounded observation receipts were
validated through the shared queue and the mailbox was deleted. The receipts
record manual browser observations.

## Verification

- Fresh strict TypeScript, typed lint, canonical formatting, and optimized
  asset verification passed. All 273 browser tests passed on the corrected
  source. The full repository check reran all Go packages without the test
  result cache and returned success. The final focus and stylesheet assets
  were also verified by the exact pushed-snapshot check.
- Release sizes in bytes, raw / gzip: HTML 381 / 242, JavaScript 32881 /
  11690, workspace CSS 8178 / 2497 and board CSS 5565 / 1693. The upstream
  Inter WOFF2 and OFL are byte-identical to the original candidate.
- Independent tooling and correction review results are recorded alongside
  the earlier workspace, board, and visual reviews. The Architect owns the
  PR merge decision. Terry owns the visual checkpoint before T3.
