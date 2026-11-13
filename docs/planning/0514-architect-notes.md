# Epic #0514 architect notes

Status snapshot: 2026-11-13. These notes preserve the in-flight assessment;
they are not new implementation authorization or production cutover approval.

## Overall assessment

The architecture is holding up. Most current complexity is in browser recovery
and coordination rather than the Go backend. Review has not established a need
for an architectural reboot or a large rewrite.

Significant findings have clustered around uncertain writes, stale browser state,
and action availability. Examples include recovery controls left locked after a
lost response, late responses affecting newer state, restored comments remaining
cached when their count did not change, and lane approval controls becoming stale
after responsive CSS changed visible membership.

Returned findings through T7 have been corrected and independently retested.
Individual story acceptance is not whole-product qualification or cutover approval.

## Useful follow-up work

Finish T8 and T9, perform Terry's demos, then consider a small separately scoped
cleanup pass. These are proposals, not additional current acceptance gates.

| Opportunity | Value | Boundary |
| --- | --- | --- |
| Document common write/recovery invariants, then consolidate identical helpers | Reduce repeated mistakes in ownership, late-response handling and retry eligibility | Recommended first step; extract proven duplication rather than a universal transaction framework |
| Extract cohesive board-session coordination from `web/src/main.ts` | Make permissions, pending work, refreshes and feature wiring easier to reason about | Keep feature-specific behavior local; avoid moving code merely to reduce line counts |
| Define consistent authoritative-change invalidation | Ensure board, card, comments and attachments refresh together when required | Preserve drafts and unresolved operations; do not replace every read with a full reload |
| Retain compact regression coverage for recurring failure patterns | Catch lost responses, stale reads and navigation during operations | Keep the concrete reproductions; avoid broad repeated suites without new risk |

Upload, restore, approval and publication have different proof requirements.
Do not force them into one generic transaction abstraction.

## Execution and acceptance checkpoint

- T1–T7: all 30 stories #0548–#0577 verified Ready for Review.
- Accepted T7 candidate: `bd066df1be1ccd600be4fa2e9b034a6ba2b98196`.
- T8: #0578 and #0579 remain In Progress; corrections are underway.
- T8 reviewed candidate: `965adfae08b88890eb572fc9e261bbc6289d3614`.
- T9: #0580 has not been released. Its entry gate remains closed.
- Candidate branch: `codex/0514-ui-candidate`, isolated in
  `.worktrees/0514-ui-candidate`.
- UI Lead owns execution; Architect owns acceptance and turn entry.
  Terry owns Completed and production cutover approval.

Recheck the live board and current candidate before resuming. This dated snapshot
must not override later acceptance or correction evidence.

## Open T8 findings

Independent focused baseline suites passed, but additional probes reproduced the
following defects. UI Lead acknowledged them and assigned corrections.

| Card | Finding | Required correction and retest |
| --- | --- | --- |
| #0578 | Manual staged attachment handoff rejects the original card after ticket renumbering | Bind identity to board and card UUID, retain fresh validators, and refresh the displayed ticket; verify renumbering between creation and manual handoff |
| #0578 | A second definite upload refusal during Resume drops the remaining original's journal entry | Preserve remaining originals across repeated refusals; verify successful first file followed by two refused attempts for the second |
| #0578 | Failed clipboard-only raster loses its in-memory original during handoff while recovery ignores paste | Keep the current-page original usable or permit exact clipboard reselection under existing identity/hash checks; do not persist file bytes merely to solve this |
| #0579 | Lost lane-create conflict followed by an identical refused retry leaves settings locked at an unchanged board revision | Provide a safe exit after proof the original cannot later commit, preserving late-original protection; verify recovery across remount |

Relevant implementation areas are `web/src/create-card.ts`, `web/src/uploads.ts`
and `web/src/lane-settings.ts`. The independent baseline runs passed 36 staged
creation/upload tests and 15 lane tests. Desktop/mobile Chrome passed the nominated
candidate's demonstrated workflows; that does not clear these additional findings.

The bounded exact-text Undo history and native-value fallback outside retained
history were already accepted in T4. They are not a new T8 blocker. No expansion
of initial lane-label/key creation scope was requested.

## Resumption evidence

Architect private records are under the current Architect chat's visualization
directory, in `output/0514-execution-gates.json`,
`output/0514-t8-architect-review.md`, and `output/0514-ui-lead-watch.json`.
The UI Lead's private `0514/t8-review.md` and associated exact-candidate evidence
hold verification details. These local artifacts supplement this committed record.

Architect chat: `01a1c3a6-8477-7c33-88f6-e11ea8c6030a`.
UI Lead chat: `01a1d28b-bb44-7652-9c19-8307cce9b336`.
Dev board: `39b278e7-d1ac-4e5a-88fb-8eabf141dd4d`.

Before T9 entry, independently retest the corrected T8 candidate, verify applicable
quality and visual evidence, authorize both T8 cards into RFR, and verify their
live moves. Release T9 separately. Production cutover still requires Terry's
explicit approval after demos.
