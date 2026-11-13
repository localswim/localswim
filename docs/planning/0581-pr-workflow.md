# Epic #0581 PR and turn workflow

Terry approved this workflow on 2026-11-13.

## Branches and authority

- `ui/v1-clean-room` is the integration branch for the fresh UI.
- UI Lead owns implementation, author branches, PR preparation and corrections.
  Implementation PRs target `ui/v1-clean-room`.
- Architect independently reviews each PR against its story, the exact V1
  appearance/behavior specification, and all applicable project standards.
- Architect approves and squash-merges accepted PRs into `ui/v1-clean-room`.
  Record the PR, reviewed head and resulting squash commit in turn evidence.
- Terry owns the final demo, project approval and merge from the UI branch to
  `main`. Standing general commit permission does not delegate this final merge.

T1 commit `e5d01c049b1600c7e51d5fc9988d7999391a3e80` predates this workflow
and forms the integration branch baseline. Preserve its accepted evidence.
Carry existing uncommitted T2 work into implementation branches for PR review.

## Turn entry

1. Recheck story prerequisites and move each eligible card In Progress before
   implementation or review.
2. Authors work in disjoint files and hand completed packets to fresh reviewers.
   UI Lead submits cohesive PRs with scope, provenance, tests and matching V1/V2
   visual/interaction evidence.
3. Architect independently reviews and requests corrections as needed. Passing
   tests alone does not establish V1 UI/UX fidelity.
4. Architect approves and squash-merges every PR from the turn. Validate the
   integrated result and record the resulting commits.
5. Only after all turn PRs are approved and squash-merged does Architect authorize
   completed turn cards into RFR and explicitly open the next turn.

T3 also requires Terry's direct confirmation of the integrated T2 workspace and
board. Terry retains control of Completed. PR submission or an RFR card does not
open a later turn.

## Scheduled progress check

The Architect check runs at minute 00, 10, 20, 30, 40 and 50 of each hour.
Start with one compact UI Lead status snapshot. If active, end immediately.
Do not interrupt or queue a nudge to an active chat.

If idle with authorized work remaining, resolve any pending Architect review
and send a concise next-step nudge. Compare progress with the prior nudge.
After two consecutive unsuccessful idle checks, pause the schedule and report
the precise obstacle to Terry. Active checks never count as failed nudges.
Finish the watch when implementation and Architect integration are complete
and only Terry's final demo, approval and main merge remain.
