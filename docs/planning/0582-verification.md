# #0582 verification and handoff

## Candidate scope

Fresh shared TypeScript declarations, component/state/operation contracts and
disposable fixture factory under `web/clean-room/`; written V1 observation
packet, evidence manifest, independent reviews and rolling staffing revision 3.
No frontend rendering implementation, backend change or production deployment.

Base: `26eddc42562c3d3b4acd80c72757e562cc55f7a0`. Candidate commit is recorded
in the live ticket handoff; this file does not embed its own commit hash.

## Verification

| Check | Result |
| --- | --- |
| Strict TypeScript 7.0.2, `web/clean-room/tsconfig.json` | Passed, including all newly required strict options and recursive author-module coverage |
| Independent workspace and board consumer compilation | Passed; separate disposable consumers import only shared contracts and each compiles with the strict config |
| Oxlint 1.90.0 / oxlint-tsgolint 7.0.2005 | Passed, three TypeScript files selected, zero structured diagnostics; existing qualified tool installation used |
| Prettier 3.9.10 | Passed for new TypeScript/config, two spaces, double quotes, LF, explicit 100-column target |
| Fixture comparison to actual disposable V1 snapshot | Passed for both boards: exact card fields/counts, lane settings/notes, create grants, attention, eight read-state variants and fresh-object isolation |
| V1 CLI `board verify` after seeding | Passed, 23 cards and two comments; no browser mutation submitted |
| Screenshot metadata | Terra independently verified all eleven JPEG encodings, dimensions and hashes after correction |
| Shared queue | Healthy; six bounded browser observation receipts validated/exported, then mailbox deleted |
| Full `check.ps1` | Go format/vet/tests, TypeScript and all 253 browser tests passed; final whitespace check found an extra EOF blank line, corrected and rechecked separately |
| Git whitespace | Passed |

The standalone fixture comparison and independent consumer inputs are retained in
the private output directory named by `0582-evidence.json`, along with
`0582-check.log` and queue receipts. The comparison uses esbuild 0.28.2 only to
load the fresh fixture factory in Node; it is not a product release build.

The full script returned nonzero only at its final Git whitespace phase. The
corrected observation document has one final newline. Both staged and unstaged
`git diff --check` pass after correction; no code changed after the full test run.
The already-passed suites were not repeated for this Markdown-only correction.

## Independent review

- [Technical review](0582-technical-review.md): Sol / High, source and backend
  provenance review, followed by re-review of Beta controls, native reorder
  permission, recursive compiler coverage and board-only failure/loading states.
- [Visual packet review](0582-visual-review.md): Terra / Medium, all V1 reference
  images and ledger; metadata findings corrected, explicit evidence limits retained.

The reviewer did not author the contract. The implementation author corrected
source findings; the Lead corrected evidence metadata. Both reviews are of the
T1 packet. Neither review claims a V2 visual match or authorizes T2.

## Remaining gates and practical limits

- Architect reviews the exact committed T1 packet and alone authorizes RFR/T2.
  Keep #0582 In Progress while that review runs. Terry retains Completed.
- T2 records actual CSS viewport, device scale and zoom, captures the missing
  visual loading state, and compares matching V1/V2 workspace and board states.
  The restart checkpoint calls for desktop 1366px and narrow 390px observations.
- Full HTTP adapters, response validation/coherent joins, rendering and recovery
  behavior begin in their authorized implementation stories. Types are not
  runtime validators. Manual queue receipts are not V1 product instrumentation.
- The main checkout's existing tooling predates the approved source/release
  standard. T1 scoped declarations pass strict/compiler/lint/format checks;
  T2 must integrate the full standard, release pipeline and Git-hook installation
  and qualification before the Architect opens T3. No archived UI is accepted.
- No V1 code, styles, assets or templates were reused. Screenshot files are
  evidence only. Old UI worktrees and the Architect's separate plan-rendering
  artifacts are outside this change and preserved.

## Runtime cleanup

The Lead closed the three created browser tabs and stopped the disposable Alpha
and Beta writers via documented CLI shutdown, then stopped only the verified
owned workspace/sender processes. No owned observation processes remain. Private
fixture files and evidence are preserved for the next authorized comparison.
The shared message queue, production 8811 and old demo 8812 were not stopped.
