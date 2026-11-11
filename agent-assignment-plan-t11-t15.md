# Agent assignments for T11–T15

Assignment revision 1 covers all 62 milestones, M040–M101. **Use up to 16 active direct subagents with one primary.** Give each milestone one author and a fresh independent reviewer. The schedule minimizes cumulative elapsed time in the stated model, including serialized primary integration. [Execution plan](execution-plan-t11-t15.md) · [Milestone scope and demos](docs/v1-parity-milestones/README.md) · [Machine-readable roster](agent-assignment-plan-t11-t15.json)

## Forecast and assumptions

The scheduled implementation, review, integration and final checks take **23 hours** across the five turns. The separate [parity walkthrough](docs/v1-parity-milestones/qualification.md) adds two hours, for **25 hours** before additional individual demo time, findings and owner waits.

| Turn | Milestones | Peak active subagents | Turn estimate | Cumulative | Primary final-check window |
| --- | ---: | ---: | ---: | ---: | --- |
| [T11](#t11) | 24 | 16 | 5h 50m | 5h 50m | 5:30–5:50 |
| [T12](#t12) | 20 | 16 | 5h 20m | 11h 10m | 5:00–5:20 |
| [T13](#t13) | 11 | 11 | 4h 15m | 15h 25m | 3:55–4:15 |
| [T14](#t14) | 5 | 5 | 3h 55m | 19h 20m | 3:35–3:55 |
| [T15](#t15) | 2 | 2 | 3h 40m | 23h | 3:20–3:40 |

All row windows below are hours and minutes elapsed **within that turn**. Start T12 after the T11 handoff and prerequisite acceptance, and repeat through T15. Owner response time sits outside the forecast. A numbered slot is capacity in the pool. Each named author and reviewer is a distinct agent.

| Modeled work | Estimate used |
| --- | --- |
| Implementation and focused tests | Existing milestone estimate, treated as one author's elapsed work |
| Independent review | 25 minutes for ordinary review, 40 minutes for High review |
| Primary integration | 10 minutes per reviewed milestone, one at a time, overlapping agent work |
| Final integrated gate | 20 minutes per turn for required checks and handoff evidence |
| Findings and conflicts | Add measured correction, re-review and integration time when discovered |

The review and integration estimates are planning assumptions. The 119h 10m of author effort comes from the milestone files. Assigning more workers to one milestone has no assumed speed benefit. Shared-file conflicts or delayed interfaces can extend these windows.

## Why this allocation minimizes modeled time

Start the first author batch immediately. As slots free, alternate queued authors and fresh reviewers in the order below so the primary receives reviewed changes early and keeps integrating. This balances the author queue against the serial integration queue. Give scope-ready implementation the initial slots, then use completed authors' released slots for independent review.

For each milestone, its author estimate plus review estimate is the earliest possible review completion, even with unlimited agents. Sort those hypothetical completion times. Starting from zero, repeatedly calculate `finish = max(finish, earliest_review_completion) + 10 minutes`, then add the 20-minute final gate. This produces an optimistic lower bound with an always-available primary.

The actual 16-slot roster reaches that bound in every turn.

| Turn | Optimistic bound including integration and final checks | Scheduled |
| --- | ---: | ---: |
| T11 | 5h 50m | 5h 50m |
| T12 | 5h 20m | 5h 20m |
| T13 | 4h 15m | 4h 15m |
| T14 | 3h 55m | 3h 55m |
| T15 | 3h 40m | 3h 40m |

The bound proves optimality for these fixed durations, serial integration and turn barriers. Real execution updates the durations as evidence arrives.

| Available subagent slots | Best tested five-turn schedule with the same assumptions |
| --- | ---: |
| 8 | 28h 10m |
| 12 | 23h 45m |
| 16 — **RECOMMENDED** | 23h |

The 16-slot choice reaches the model's lower bound. The smaller-pool schedules are the best found in the scheduling search. Keeping this roster and changing primary integration from ten minutes to five gives 21 hours; fifteen minutes gives 27h 10m. Measure integration during T11 and refresh the remaining forecast.

## Ownership and integration

The primary uses **Sol / High**, model `gpt-6.1-sol` with `high` reasoning, and owns the integrated branch, shared contracts, review findings, final checks, commits and handoff. All agents use Standard speed. Ordinary authors use **Sol / Medium**, model `gpt-6.1-sol` with `medium` reasoning. Authorization, transaction, recovery and concurrency authors use **Sol / High**. Every row names its selected profile.

Authors work in isolated worktrees and return small, scope-specific changes. A shared file's integrated version has one merge owner, the primary. Authors coordinate proposed interfaces before coding against them. Reuse current files and helpers where practical. Introduce focused modules only where the feature warrants them.

| Work area | Shared integration surfaces | Coordination |
| --- | --- | --- |
| Workspace browser | [web/app.mjs](web/app.mjs), [web/app.css](web/app.css), [web/api.mjs](web/api.mjs), [internal/v2/catalog.go](internal/v2/catalog.go), [internal/v2/events.go](internal/v2/events.go) | Navigation, subscriptions, sidebar and saved view state. |
| Card browser | [web/app.mjs](web/app.mjs), [web/app.css](web/app.css), [web/api.mjs](web/api.mjs) | Card face, drawer, filtering, formatting and guarded pending edits. |
| Browser files and background jobs | [web/app.mjs](web/app.mjs), [web/api.mjs](web/api.mjs), [internal/v2/attachments.go](internal/v2/attachments.go), [internal/v2/backups.go](internal/v2/backups.go) | Upload selection, operation identity, replay checks and job-scoped state. |
| Workflow and presentation | [internal/v2/lanes_edit.go](internal/v2/lanes_edit.go), [internal/v2/default_workflow.go](internal/v2/default_workflow.go), [internal/v2/custom_workflow.go](internal/v2/custom_workflow.go), [internal/v2/approvals.go](internal/v2/approvals.go), [internal/v2/boards.go](internal/v2/boards.go) | Lane configuration, grants, approval audit and presentation. |
| CLI commands and projections | [cmd/localswim/cli.go](cmd/localswim/cli.go), [internal/client/client.go](internal/client/client.go), [internal/v2/card_search.go](internal/v2/card_search.go), [internal/v2/comments.go](internal/v2/comments.go) | Dispatch, ticket resolution, offline reads and shared report projection. |
| Activity and notices | [internal/v2/activity.go](internal/v2/activity.go), [internal/v2/owner_notices.go](internal/v2/owner_notices.go), [internal/v2/notification_settings.go](internal/v2/notification_settings.go) | Historical identity, coverage, routing and committed operation metadata. |
| Durability and native recovery | [internal/v2/store.go](internal/v2/store.go), [internal/v2/store_files.go](internal/v2/store_files.go), [internal/v2/snapshots.go](internal/v2/snapshots.go), [internal/v2/backup_archive.go](internal/v2/backup_archive.go) | Durable admission, retained recovery authority and restore. |
| Git publication | [internal/v2/publication_git.go](internal/v2/publication_git.go), [internal/v2/publication_checkpoint.go](internal/v2/publication_checkpoint.go), [internal/v2/automatic_publication.go](internal/v2/automatic_publication.go) | Capture inventory, remote/ref selection, index and process cleanup. |
| Windows operation and performance | [cmd/localswim/main.go](cmd/localswim/main.go), [cmd/localswim/service_replacement.go](cmd/localswim/service_replacement.go), [cmd/localswim/service_replacement_windows.go](cmd/localswim/service_replacement_windows.go), [internal/v2/service_lifecycle.go](internal/v2/service_lifecycle.go) | Detached launch, replacement and workload measurement. |
| Cross-cutting contract and browser assets | [model.go](internal/v2/model.go), [input.go](internal/v2/input.go), [routes.go](internal/v2/routes.go), [OpenAPI](docs/openapi.yaml), [web/assets.go](web/assets.go), [browser.go](internal/v2/browser.go), [web/package.json](web/package.json) | The primary integrates schema, dispatch, embedding and shared test-registration changes. |

Particular handoffs need coordination within a turn. [M040](docs/v1-parity-milestones/m40.md), [M041](docs/v1-parity-milestones/m41.md), [M045](docs/v1-parity-milestones/m45.md) and the card authors share browser wiring. [M089](docs/v1-parity-milestones/m89.md) and [M091](docs/v1-parity-milestones/m91.md) share the Git implementation. [M080](docs/v1-parity-milestones/m80.md) supplies private notice settings used by later recovery. [M084](docs/v1-parity-milestones/m84.md) and [M086](docs/v1-parity-milestones/m86.md) share archive and recovery boundaries. Review the integrated result again when conflict resolution changes behavior.

## Kickoff and completion rules

- Verify scope, lane and accepted prerequisites, then move the actual milestone card to In Progress before implementation, tests or review.
- Use the linked milestone file as acceptance authority. Table time offsets guide dispatch as actual work frees capacity.
- Keep the active direct-agent count at or below 16. Release finished agents promptly and create each reviewer with a fresh context. Reviewers assess the author's complete change and focused evidence.
- The author owns affected code, focused tests, help, OpenAPI and documentation. Add a narrowly scoped helper only after identifying work that can genuinely shorten the remaining path, and recompute the pool schedule.
- The primary performs each listed integration window and runs the final gate, including `check.ps1`, on the combined build. Browser verification uses the shared queue and one active UI operator.
- Move finished and independently reviewed milestones to Ready for Review when their integrated checks pass. Track pending demos separately. Terry controls Completed and prerequisite acceptance.
- Demo eligible RFR milestones at turn end and at each three-hour build break. Present one milestone at a time, with CLI help and complete output where applicable.
- At handoff, preserve this baseline and append an assignment revision with actual durations, findings and carryover. Refresh named authors and fresh reviewers for the next three remaining turns. The initial rolling window is T11–T13; T14–T15 are also fully assigned below.

## T11

The turn begins 0h into the cumulative forecast. Start 16 authors immediately. Refill author slots as listed below. The primary final gate runs at 5:30–5:50.

**Initial author dispatch**

- Slot 01 · [M040](docs/v1-parity-milestones/m40.md) · `sol_medium_m040_author` · Sol / Medium
- Slot 02 · [M100](docs/v1-parity-milestones/m100.md) · `sol_medium_m100_author` · Sol / Medium
- Slot 03 · [M070](docs/v1-parity-milestones/m70.md) · `sol_medium_m070_author` · Sol / Medium
- Slot 04 · [M049](docs/v1-parity-milestones/m49.md) · `sol_medium_m049_author` · Sol / Medium
- Slot 05 · [M067](docs/v1-parity-milestones/m67.md) · `sol_medium_m067_author` · Sol / Medium
- Slot 06 · [M063](docs/v1-parity-milestones/m63.md) · `sol_high_m063_author` · Sol / High
- Slot 07 · [M069](docs/v1-parity-milestones/m69.md) · `sol_medium_m069_author` · Sol / Medium
- Slot 08 · [M096](docs/v1-parity-milestones/m96.md) · `sol_high_m096_author` · Sol / High
- Slot 09 · [M058](docs/v1-parity-milestones/m58.md) · `sol_high_m058_author` · Sol / High
- Slot 10 · [M065](docs/v1-parity-milestones/m65.md) · `sol_high_m065_author` · Sol / High
- Slot 11 · [M045](docs/v1-parity-milestones/m45.md) · `sol_high_m045_author` · Sol / High
- Slot 12 · [M074](docs/v1-parity-milestones/m74.md) · `sol_high_m074_author` · Sol / High
- Slot 13 · [M041](docs/v1-parity-milestones/m41.md) · `sol_high_m041_author` · Sol / High
- Slot 14 · [M050](docs/v1-parity-milestones/m50.md) · `sol_medium_m050_author` · Sol / Medium
- Slot 15 · [M047](docs/v1-parity-milestones/m47.md) · `sol_medium_m047_author` · Sol / Medium
- Slot 16 · [M076](docs/v1-parity-milestones/m76.md) · `sol_medium_m076_author` · Sol / Medium

**Later author dispatch**

- 1:10 · Slot 16 · [M092](docs/v1-parity-milestones/m92.md) · `sol_high_m092_author` · Sol / High
- 1:25 · Slot 14 · [M082](docs/v1-parity-milestones/m82.md) · `sol_high_m082_author` · Sol / High
- 1:40 · Slot 04 · [M051](docs/v1-parity-milestones/m51.md) · `sol_medium_m051_author` · Sol / Medium
- 1:40 · Slot 07 · [M064](docs/v1-parity-milestones/m64.md) · `sol_medium_m064_author` · Sol / Medium
- 2:00 · Slot 05 · [M088](docs/v1-parity-milestones/m88.md) · `sol_high_m088_author` · Sol / High
- 2:00 · Slot 12 · [M077](docs/v1-parity-milestones/m77.md) · `sol_medium_m077_author` · Sol / Medium
- 2:00 · Slot 15 · [M068](docs/v1-parity-milestones/m68.md) · `sol_medium_m068_author` · Sol / Medium
- 2:10 · Slot 06 · [M062](docs/v1-parity-milestones/m62.md) · `sol_high_m062_author` · Sol / High

**Detailed roster**

| Milestone and scope | Author | Author window / slot | Fresh reviewer | Review window / slot | Primary integration | Prerequisites |
| --- | --- | --- | --- | --- | --- | --- |
| [M040](docs/v1-parity-milestones/m40.md) · See Attention across projects | Sol / Medium<br>`sol_medium_m040_author` | 0:00–1:45 / 01 | Sol / Medium<br>`sol_medium_m040_review` | 1:45–2:10 / 06 | 2:10–2:20 | T10 accepted baseline |
| [M041](docs/v1-parity-milestones/m41.md) · Watch the open board update | Sol / High<br>`sol_high_m041_author` | 0:00–2:15 / 13 | Sol / High<br>`sol_high_m041_review` | 2:15–2:55 / 08 | 3:20–3:30 | T10 accepted baseline |
| [M045](docs/v1-parity-milestones/m45.md) · Resume a board view | Sol / High<br>`sol_high_m045_author` | 0:00–1:45 / 11 | Sol / High<br>`sol_high_m045_review` | 1:45–2:25 / 11 | 2:30–2:40 | T10 accepted baseline |
| [M047](docs/v1-parity-milestones/m47.md) · Start and move work where permitted | Sol / Medium<br>`sol_medium_m047_author` | 0:00–2:00 / 15 | Sol / High<br>`sol_high_m047_review` | 2:00–2:40 / 03 | 2:50–3:00 | T10 accepted baseline |
| [M049](docs/v1-parity-milestones/m49.md) · Scan cards and lane totals | Sol / Medium<br>`sol_medium_m049_author` | 0:00–1:15 / 04 | Sol / Medium<br>`sol_medium_m049_review` | 1:15–1:40 / 07 | 1:40–1:50 | T10 accepted baseline |
| [M050](docs/v1-parity-milestones/m50.md) · Set one card's priority directly | Sol / Medium<br>`sol_medium_m050_author` | 0:00–1:00 / 14 | Sol / Medium<br>`sol_medium_m050_review` | 1:00–1:25 / 14 | 1:25–1:35 | T10 accepted baseline |
| [M051](docs/v1-parity-milestones/m51.md) · Navigate and seed related work | Sol / Medium<br>`sol_medium_m051_author` | 1:40–4:05 / 04 | Sol / Medium<br>`sol_medium_m051_review` | 4:05–4:30 / 04 | 5:10–5:20 | T10 accepted baseline |
| [M058](docs/v1-parity-milestones/m58.md) · Paste and upload several files to a card | Sol / High<br>`sol_high_m058_author` | 0:00–2:15 / 09 | Sol / High<br>`sol_high_m058_review` | 2:15–2:55 / 13 | 3:30–3:40 | T10 accepted baseline |
| [M062](docs/v1-parity-milestones/m62.md) · Keep working while a backup runs | Sol / High<br>`sol_high_m062_author` | 2:10–3:40 / 06 | Sol / High<br>`sol_high_m062_review` | 3:40–4:20 / 03 | 4:30–4:40 | T10 accepted baseline |
| [M063](docs/v1-parity-milestones/m63.md) · Opt a lane into dependency reorder | Sol / High<br>`sol_high_m063_author` | 0:00–1:45 / 06 | Sol / High<br>`sol_high_m063_review` | 1:45–2:25 / 01 | 2:40–2:50 | T10 accepted baseline |
| [M064](docs/v1-parity-milestones/m64.md) · See lane notes and board actor context | Sol / Medium<br>`sol_medium_m064_author` | 1:40–3:45 / 07 | Sol / High<br>`sol_high_m064_review` | 3:45–4:25 / 06 | 4:40–4:50 | T10 accepted baseline |
| [M065](docs/v1-parity-milestones/m65.md) · Approve a configured lane as Terry | Sol / High<br>`sol_high_m065_author` | 0:00–2:35 / 10 | Sol / High<br>`sol_high_m065_review` | 2:35–3:15 / 10 | 4:00–4:10 | T10 accepted baseline |
| [M067](docs/v1-parity-milestones/m67.md) · Pick next work across lanes | Sol / Medium<br>`sol_medium_m067_author` | 0:00–2:00 / 05 | Sol / Medium<br>`sol_medium_m067_review` | 2:25–2:50 / 01 | 3:00–3:10 | T10 accepted baseline |
| [M068](docs/v1-parity-milestones/m68.md) · Inspect a ticket and its context safely | Sol / Medium<br>`sol_medium_m068_author` | 2:00–4:00 / 15 | Sol / Medium<br>`sol_medium_m068_review` | 4:00–4:25 / 07 | 4:50–5:00 | T10 accepted baseline |
| [M069](docs/v1-parity-milestones/m69.md) · Catch up on recent comments by ticket | Sol / Medium<br>`sol_medium_m069_author` | 0:00–1:15 / 07 | Sol / Medium<br>`sol_medium_m069_review` | 1:15–1:40 / 04 | 1:50–2:00 | T10 accepted baseline |
| [M070](docs/v1-parity-milestones/m70.md) · Act on a card by ticket | Sol / Medium<br>`sol_medium_m070_author` | 0:00–2:00 / 03 | Sol / High<br>`sol_high_m070_review` | 2:10–2:50 / 02 | 3:10–3:20 | T10 accepted baseline |
| [M074](docs/v1-parity-milestones/m74.md) · Inspect and export saved work with the writer down | Sol / High<br>`sol_high_m074_author` | 0:00–2:00 / 12 | Sol / High<br>`sol_high_m074_review` | 2:25–3:05 / 11 | 3:50–4:00 | T10 accepted baseline |
| [M076](docs/v1-parity-milestones/m76.md) · Fail automation on failed board verification | Sol / Medium<br>`sol_medium_m076_author` | 0:00–0:45 / 16 | Sol / Medium<br>`sol_medium_m076_review` | 0:45–1:10 / 16 | 1:10–1:20 | T10 accepted baseline |
| [M077](docs/v1-parity-milestones/m77.md) · Inspect activity without leaking card prose | Sol / Medium<br>`sol_medium_m077_author` | 2:00–3:30 / 12 | Sol / High<br>`sol_high_m077_review` | 3:30–4:10 / 02 | 4:20–4:30 | T10 accepted baseline |
| [M082](docs/v1-parity-milestones/m82.md) · Protect a board whose saved data changed | Sol / High<br>`sol_high_m082_author` | 1:25–3:50 / 14 | Sol / High<br>`sol_high_m082_review` | 3:50–4:30 / 01 | 5:20–5:30 | T10 accepted baseline |
| [M088](docs/v1-parity-milestones/m88.md) · Keep ignored boards out of Git | Sol / High<br>`sol_high_m088_author` | 2:00–3:45 / 05 | Sol / High<br>`sol_high_m088_review` | 3:45–4:25 / 05 | 5:00–5:10 | T10 accepted baseline |
| [M092](docs/v1-parity-milestones/m92.md) · Start V2 independently on Windows | Sol / High<br>`sol_high_m092_author` | 1:10–3:10 / 16 | Sol / High<br>`sol_high_m092_review` | 3:10–3:50 / 01 | 4:10–4:20 | T10 accepted baseline |
| [M096](docs/v1-parity-milestones/m96.md) · Validate a large portable backup locally | Sol / High<br>`sol_high_m096_author` | 0:00–2:15 / 08 | Sol / High<br>`sol_high_m096_review` | 2:15–2:55 / 09 | 3:40–3:50 | T10 accepted baseline |
| [M100](docs/v1-parity-milestones/m100.md) · Apply board-specific browser styling | Sol / Medium<br>`sol_medium_m100_author` | 0:00–1:30 / 02 | Sol / High<br>`sol_high_m100_review` | 1:30–2:10 / 02 | 2:20–2:30 | T10 accepted baseline |

## T12

The turn begins 5h 50m into the cumulative forecast. Start 16 authors immediately. Refill author slots as listed below. The primary final gate runs at 5:00–5:20.

**Initial author dispatch**

- Slot 01 · [M078](docs/v1-parity-milestones/m78.md) · `sol_high_m078_author` · Sol / High
- Slot 02 · [M044](docs/v1-parity-milestones/m44.md) · `sol_medium_m044_author` · Sol / Medium
- Slot 03 · [M083](docs/v1-parity-milestones/m83.md) · `sol_high_m083_author` · Sol / High
- Slot 04 · [M098](docs/v1-parity-milestones/m98.md) · `sol_medium_m098_author` · Sol / Medium
- Slot 05 · [M048](docs/v1-parity-milestones/m48.md) · `sol_medium_m048_author` · Sol / Medium
- Slot 06 · [M042](docs/v1-parity-milestones/m42.md) · `sol_high_m042_author` · Sol / High
- Slot 07 · [M043](docs/v1-parity-milestones/m43.md) · `sol_medium_m043_author` · Sol / Medium
- Slot 08 · [M072](docs/v1-parity-milestones/m72.md) · `sol_medium_m072_author` · Sol / Medium
- Slot 09 · [M089](docs/v1-parity-milestones/m89.md) · `sol_high_m089_author` · Sol / High
- Slot 10 · [M091](docs/v1-parity-milestones/m91.md) · `sol_high_m091_author` · Sol / High
- Slot 11 · [M052](docs/v1-parity-milestones/m52.md) · `sol_medium_m052_author` · Sol / Medium
- Slot 12 · [M056](docs/v1-parity-milestones/m56.md) · `sol_high_m056_author` · Sol / High
- Slot 13 · [M061](docs/v1-parity-milestones/m61.md) · `sol_medium_m061_author` · Sol / Medium
- Slot 14 · [M046](docs/v1-parity-milestones/m46.md) · `sol_high_m046_author` · Sol / High
- Slot 15 · [M059](docs/v1-parity-milestones/m59.md) · `sol_medium_m059_author` · Sol / Medium
- Slot 16 · [M053](docs/v1-parity-milestones/m53.md) · `sol_medium_m053_author` · Sol / Medium

**Later author dispatch**

- 1:25 · Slot 13 · [M071](docs/v1-parity-milestones/m71.md) · `sol_medium_m071_author` · Sol / Medium
- 1:40 · Slot 02 · [M079](docs/v1-parity-milestones/m79.md) · `sol_high_m079_author` · Sol / High
- 1:40 · Slot 16 · [M075](docs/v1-parity-milestones/m75.md) · `sol_medium_m075_author` · Sol / Medium
- 1:45 · Slot 15 · [M066](docs/v1-parity-milestones/m66.md) · `sol_high_m066_author` · Sol / High

**Detailed roster**

| Milestone and scope | Author | Author window / slot | Fresh reviewer | Review window / slot | Primary integration | Prerequisites |
| --- | --- | --- | --- | --- | --- | --- |
| [M042](docs/v1-parity-milestones/m42.md) · Watch workspace Attention update | Sol / High<br>`sol_high_m042_author` | 0:00–2:00 / 06 | Sol / High<br>`sol_high_m042_review` | 2:00–2:40 / 10 | 2:50–3:00 | [M040](docs/v1-parity-milestones/m40.md), [M041](docs/v1-parity-milestones/m41.md) |
| [M043](docs/v1-parity-milestones/m43.md) · Organize pins from the sidebar | Sol / Medium<br>`sol_medium_m043_author` | 0:00–1:45 / 07 | Sol / Medium<br>`sol_medium_m043_review` | 1:45–2:10 / 04 | 2:10–2:20 | [M040](docs/v1-parity-milestones/m40.md) |
| [M044](docs/v1-parity-milestones/m44.md) · Resize and collapse the sidebar | Sol / Medium<br>`sol_medium_m044_author` | 0:00–1:15 / 02 | Sol / Medium<br>`sol_medium_m044_review` | 1:15–1:40 / 02 | 1:40–1:50 | [M040](docs/v1-parity-milestones/m40.md) |
| [M046](docs/v1-parity-milestones/m46.md) · Reopen a recent board immediately | Sol / High<br>`sol_high_m046_author` | 0:00–2:15 / 14 | Sol / High<br>`sol_high_m046_review` | 2:15–2:55 / 03 | 3:20–3:30 | [M041](docs/v1-parity-milestones/m41.md), [M045](docs/v1-parity-milestones/m45.md) |
| [M048](docs/v1-parity-milestones/m48.md) · Drag a card to an allowed lane | Sol / Medium<br>`sol_medium_m048_author` | 0:00–1:45 / 05 | Sol / High<br>`sol_high_m048_review` | 1:45–2:25 / 07 | 2:30–2:40 | [M047](docs/v1-parity-milestones/m47.md) |
| [M052](docs/v1-parity-milestones/m52.md) · Filter work without leaving the board | Sol / Medium<br>`sol_medium_m052_author` | 0:00–1:45 / 11 | Sol / Medium<br>`sol_medium_m052_review` | 1:45–2:10 / 11 | 2:20–2:30 | [M045](docs/v1-parity-milestones/m45.md), [M049](docs/v1-parity-milestones/m49.md) |
| [M053](docs/v1-parity-milestones/m53.md) · Review recent or older Completed work | Sol / Medium<br>`sol_medium_m053_author` | 0:00–1:15 / 16 | Sol / Medium<br>`sol_medium_m053_review` | 1:15–1:40 / 16 | 1:50–2:00 | [M049](docs/v1-parity-milestones/m49.md) |
| [M056](docs/v1-parity-milestones/m56.md) · Recover drafts and submitted actions | Sol / High<br>`sol_high_m056_author` | 0:00–2:15 / 12 | Sol / High<br>`sol_high_m056_review` | 2:15–2:55 / 08 | 3:30–3:40 | [M045](docs/v1-parity-milestones/m45.md) |
| [M059](docs/v1-parity-milestones/m59.md) · Create a card with files | Sol / Medium<br>`sol_medium_m059_author` | 0:00–1:45 / 15 | Sol / High<br>`sol_high_m059_review` | 2:10–2:50 / 04 | 3:10–3:20 | [M058](docs/v1-parity-milestones/m58.md) |
| [M061](docs/v1-parity-milestones/m61.md) · See an image preview on the card | Sol / Medium<br>`sol_medium_m061_author` | 0:00–1:00 / 13 | Sol / Medium<br>`sol_medium_m061_review` | 1:00–1:25 / 13 | 1:25–1:35 | [M049](docs/v1-parity-milestones/m49.md) |
| [M066](docs/v1-parity-milestones/m66.md) · Start a board with Terry's workflow | Sol / High<br>`sol_high_m066_author` | 1:45–3:30 / 15 | Sol / High<br>`sol_high_m066_review` | 3:30–4:10 / 04 | 4:50–5:00 | [M063](docs/v1-parity-milestones/m63.md), [M065](docs/v1-parity-milestones/m65.md) |
| [M071](docs/v1-parity-milestones/m71.md) · Manage hierarchy and links by ticket | Sol / Medium<br>`sol_medium_m071_author` | 1:25–3:25 / 13 | Sol / High<br>`sol_high_m071_review` | 3:25–4:05 / 02 | 4:20–4:30 | [M070](docs/v1-parity-milestones/m70.md) |
| [M072](docs/v1-parity-milestones/m72.md) · Run routine writes without composing JSON | Sol / Medium<br>`sol_medium_m072_author` | 0:00–2:15 / 08 | Sol / High<br>`sol_high_m072_review` | 2:15–2:55 / 11 | 3:40–3:50 | [M070](docs/v1-parity-milestones/m70.md) |
| [M075](docs/v1-parity-milestones/m75.md) · Triage and catch up offline | Sol / Medium<br>`sol_medium_m075_author` | 1:40–3:25 / 16 | Sol / High<br>`sol_high_m075_review` | 3:25–4:05 / 03 | 4:30–4:40 | [M067](docs/v1-parity-milestones/m67.md), [M068](docs/v1-parity-milestones/m68.md), [M069](docs/v1-parity-milestones/m69.md), [M074](docs/v1-parity-milestones/m74.md) |
| [M078](docs/v1-parity-milestones/m78.md) · Verify what an owner notice covers | Sol / High<br>`sol_high_m078_author` | 0:00–2:35 / 01 | Sol / High<br>`sol_high_m078_review` | 2:35–3:15 / 01 | 4:10–4:20 | [M077](docs/v1-parity-milestones/m77.md) |
| [M079](docs/v1-parity-milestones/m79.md) · Explicitly route notices to the owning chat | Sol / High<br>`sol_high_m079_author` | 1:40–3:25 / 02 | Sol / High<br>`sol_high_m079_review` | 3:25–4:05 / 01 | 4:40–4:50 | [M077](docs/v1-parity-milestones/m77.md) |
| [M083](docs/v1-parity-milestones/m83.md) · Start with one damaged board and keep working | Sol / High<br>`sol_high_m083_author` | 0:00–2:15 / 03 | Sol / High<br>`sol_high_m083_review` | 2:15–2:55 / 12 | 3:50–4:00 | [M082](docs/v1-parity-milestones/m82.md) |
| [M089](docs/v1-parity-milestones/m89.md) · Publish to the intended Git destination | Sol / High<br>`sol_high_m089_author` | 0:00–2:25 / 09 | Sol / High<br>`sol_high_m089_review` | 2:25–3:05 / 05 | 4:00–4:10 | [M088](docs/v1-parity-milestones/m88.md) |
| [M091](docs/v1-parity-milestones/m91.md) · Publish without disturbing staged work | Sol / High<br>`sol_high_m091_author` | 0:00–2:00 / 10 | Sol / High<br>`sol_high_m091_review` | 2:00–2:40 / 06 | 3:00–3:10 | [M088](docs/v1-parity-milestones/m88.md) |
| [M098](docs/v1-parity-milestones/m98.md) · Work with files and batches using ticket references | Sol / Medium<br>`sol_medium_m098_author` | 0:00–1:45 / 04 | Sol / High<br>`sol_high_m098_review` | 1:45–2:25 / 05 | 2:40–2:50 | [M070](docs/v1-parity-milestones/m70.md) |

## T13

The turn begins 11h 10m into the cumulative forecast. Start 11 authors immediately. The primary final gate runs at 3:55–4:15.

**Initial author dispatch**

- Slot 01 · [M057](docs/v1-parity-milestones/m57.md) · `sol_high_m057_author` · Sol / High
- Slot 02 · [M095](docs/v1-parity-milestones/m95.md) · `sol_high_m095_author` · Sol / High
- Slot 03 · [M097](docs/v1-parity-milestones/m97.md) · `sol_high_m097_author` · Sol / High
- Slot 04 · [M054](docs/v1-parity-milestones/m54.md) · `sol_medium_m054_author` · Sol / Medium
- Slot 05 · [M060](docs/v1-parity-milestones/m60.md) · `sol_high_m060_author` · Sol / High
- Slot 06 · [M080](docs/v1-parity-milestones/m80.md) · `sol_high_m080_author` · Sol / High
- Slot 07 · [M073](docs/v1-parity-milestones/m73.md) · `sol_high_m073_author` · Sol / High
- Slot 08 · [M081](docs/v1-parity-milestones/m81.md) · `sol_high_m081_author` · Sol / High
- Slot 09 · [M087](docs/v1-parity-milestones/m87.md) · `sol_high_m087_author` · Sol / High
- Slot 10 · [M101](docs/v1-parity-milestones/m101.md) · `sol_high_m101_author` · Sol / High
- Slot 11 · [M055](docs/v1-parity-milestones/m55.md) · `sol_medium_m055_author` · Sol / Medium

**Detailed roster**

| Milestone and scope | Author | Author window / slot | Fresh reviewer | Review window / slot | Primary integration | Prerequisites |
| --- | --- | --- | --- | --- | --- | --- |
| [M054](docs/v1-parity-milestones/m54.md) · Read and find formatted card prose | Sol / Medium<br>`sol_medium_m054_author` | 0:00–2:15 / 04 | Sol / High<br>`sol_high_m054_review` | 2:15–2:55 / 04 | 2:55–3:05 | [M052](docs/v1-parity-milestones/m52.md) |
| [M055](docs/v1-parity-milestones/m55.md) · Edit and review by keyboard | Sol / Medium<br>`sol_medium_m055_author` | 0:00–1:30 / 11 | Sol / Medium<br>`sol_medium_m055_review` | 1:30–1:55 / 11 | 1:55–2:05 | [M052](docs/v1-parity-milestones/m52.md), [M063](docs/v1-parity-milestones/m63.md), [M064](docs/v1-parity-milestones/m64.md), [M065](docs/v1-parity-milestones/m65.md) |
| [M057](docs/v1-parity-milestones/m57.md) · See slow edits while they settle | Sol / High<br>`sol_high_m057_author` | 0:00–2:30 / 01 | Sol / High<br>`sol_high_m057_review` | 2:30–3:10 / 01 | 3:45–3:55 | [M041](docs/v1-parity-milestones/m41.md), [M048](docs/v1-parity-milestones/m48.md), [M056](docs/v1-parity-milestones/m56.md), [M059](docs/v1-parity-milestones/m59.md) |
| [M060](docs/v1-parity-milestones/m60.md) · Resume a partial upload safely | Sol / High<br>`sol_high_m060_author` | 0:00–2:15 / 05 | Sol / High<br>`sol_high_m060_review` | 2:15–2:55 / 05 | 3:05–3:15 | [M056](docs/v1-parity-milestones/m56.md), [M059](docs/v1-parity-milestones/m59.md) |
| [M073](docs/v1-parity-milestones/m73.md) · Provision and adjust workflows from the CLI | Sol / High<br>`sol_high_m073_author` | 0:00–1:45 / 07 | Sol / High<br>`sol_high_m073_review` | 1:45–2:25 / 07 | 2:25–2:35 | [M066](docs/v1-parity-milestones/m66.md), [M072](docs/v1-parity-milestones/m72.md) |
| [M080](docs/v1-parity-milestones/m80.md) · Restore without rolling back notice authorization | Sol / High<br>`sol_high_m080_author` | 0:00–2:15 / 06 | Sol / High<br>`sol_high_m080_review` | 2:15–2:55 / 06 | 3:15–3:25 | [M079](docs/v1-parity-milestones/m79.md) |
| [M081](docs/v1-parity-milestones/m81.md) · Recognize approval and lane changes in notices | Sol / High<br>`sol_high_m081_author` | 0:00–1:45 / 08 | Sol / High<br>`sol_high_m081_review` | 1:45–2:25 / 08 | 2:35–2:45 | [M078](docs/v1-parity-milestones/m78.md), [M079](docs/v1-parity-milestones/m79.md) |
| [M087](docs/v1-parity-milestones/m87.md) · See durable health changes from the open workspace | Sol / High<br>`sol_high_m087_author` | 0:00–1:45 / 09 | Sol / High<br>`sol_high_m087_review` | 1:45–2:25 / 09 | 2:45–2:55 | [M042](docs/v1-parity-milestones/m42.md), [M082](docs/v1-parity-milestones/m82.md), [M083](docs/v1-parity-milestones/m83.md) |
| [M095](docs/v1-parity-milestones/m95.md) · Identify a damaged board and its recovery point | Sol / High<br>`sol_high_m095_author` | 0:00–2:25 / 02 | Sol / High<br>`sol_high_m095_review` | 2:25–3:05 / 02 | 3:25–3:35 | [M083](docs/v1-parity-milestones/m83.md) |
| [M097](docs/v1-parity-milestones/m97.md) · Retry a pending push to its original destination | Sol / High<br>`sol_high_m097_author` | 0:00–2:25 / 03 | Sol / High<br>`sol_high_m097_review` | 2:25–3:05 / 03 | 3:35–3:45 | [M089](docs/v1-parity-milestones/m89.md) |
| [M101](docs/v1-parity-milestones/m101.md) · Inspect saved activity with the writer down | Sol / High<br>`sol_high_m101_author` | 0:00–1:35 / 10 | Sol / High<br>`sol_high_m101_review` | 1:35–2:15 / 10 | 2:15–2:25 | [M074](docs/v1-parity-milestones/m74.md), [M078](docs/v1-parity-milestones/m78.md) |

## T14

The turn begins 15h 25m into the cumulative forecast. Start 5 authors immediately. The primary final gate runs at 3:35–3:55.

**Initial author dispatch**

- Slot 01 · [M084](docs/v1-parity-milestones/m84.md) · `sol_high_m084_author` · Sol / High
- Slot 02 · [M094](docs/v1-parity-milestones/m94.md) · `sol_high_m094_author` · Sol / High
- Slot 03 · [M090](docs/v1-parity-milestones/m90.md) · `sol_high_m090_author` · Sol / High
- Slot 04 · [M086](docs/v1-parity-milestones/m86.md) · `sol_high_m086_author` · Sol / High
- Slot 05 · [M099](docs/v1-parity-milestones/m99.md) · `sol_medium_m099_author` · Sol / Medium

**Detailed roster**

| Milestone and scope | Author | Author window / slot | Fresh reviewer | Review window / slot | Primary integration | Prerequisites |
| --- | --- | --- | --- | --- | --- | --- |
| [M084](docs/v1-parity-milestones/m84.md) · Recover a board that cannot load | Sol / High<br>`sol_high_m084_author` | 0:00–2:35 / 01 | Sol / High<br>`sol_high_m084_review` | 2:35–3:15 / 01 | 3:15–3:25 | [M080](docs/v1-parity-milestones/m80.md), [M083](docs/v1-parity-milestones/m83.md), [M095](docs/v1-parity-milestones/m95.md) |
| [M086](docs/v1-parity-milestones/m86.md) · Restore a downloaded attachment-heavy backup | Sol / High<br>`sol_high_m086_author` | 0:00–2:05 / 04 | Sol / High<br>`sol_high_m086_review` | 2:05–2:45 / 04 | 2:45–2:55 | [M080](docs/v1-parity-milestones/m80.md), [M096](docs/v1-parity-milestones/m96.md) |
| [M090](docs/v1-parity-milestones/m90.md) · Recover cleanly from a stalled publication | Sol / High<br>`sol_high_m090_author` | 0:00–2:15 / 03 | Sol / High<br>`sol_high_m090_review` | 2:15–2:55 / 03 | 2:55–3:05 | [M097](docs/v1-parity-milestones/m97.md) |
| [M094](docs/v1-parity-milestones/m94.md) · Keep busy workspaces responsive | Sol / High<br>`sol_high_m094_author` | 0:00–2:35 / 02 | Sol / High<br>`sol_high_m094_review` | 2:35–3:15 / 02 | 3:25–3:35 | [M046](docs/v1-parity-milestones/m46.md), [M057](docs/v1-parity-milestones/m57.md), [M060](docs/v1-parity-milestones/m60.md), [M062](docs/v1-parity-milestones/m62.md), [M087](docs/v1-parity-milestones/m87.md) |
| [M099](docs/v1-parity-milestones/m99.md) · Discover commands from their own help | Sol / Medium<br>`sol_medium_m099_author` | 0:00–1:30 / 05 | Sol / Medium<br>`sol_medium_m099_review` | 1:30–1:55 / 05 | 1:55–2:05 | [M072](docs/v1-parity-milestones/m72.md), [M073](docs/v1-parity-milestones/m73.md), [M098](docs/v1-parity-milestones/m98.md) |

## T15

The turn begins 19h 20m into the cumulative forecast. Start 2 authors immediately. The primary final gate runs at 3:20–3:40.

**Initial author dispatch**

- Slot 01 · [M085](docs/v1-parity-milestones/m85.md) · `sol_high_m085_author` · Sol / High
- Slot 02 · [M093](docs/v1-parity-milestones/m93.md) · `sol_high_m093_author` · Sol / High

**Detailed roster**

| Milestone and scope | Author | Author window / slot | Fresh reviewer | Review window / slot | Primary integration | Prerequisites |
| --- | --- | --- | --- | --- | --- | --- |
| [M085](docs/v1-parity-milestones/m85.md) · Contain an uncertain commit to its actual scope | Sol / High<br>`sol_high_m085_author` | 0:00–2:30 / 01 | Sol / High<br>`sol_high_m085_review` | 2:30–3:10 / 01 | 3:10–3:20 | [M082](docs/v1-parity-milestones/m82.md), [M084](docs/v1-parity-milestones/m84.md) |
| [M093](docs/v1-parity-milestones/m93.md) · Reconnect after a Windows crash or upgrade | Sol / High<br>`sol_high_m093_author` | 0:00–1:45 / 02 | Sol / High<br>`sol_high_m093_review` | 1:45–2:25 / 02 | 2:25–2:35 | [M084](docs/v1-parity-milestones/m84.md), [M092](docs/v1-parity-milestones/m92.md) |

## Roster validation

The machine-readable roster contains all 62 scope references, 62 authors, 62 fresh reviewers, 62 serial integration windows and five final gates. Its source baseline is `060c993`. The dependency turns match the published execution plan. Validate the active-slot sweep, author-before-review order, review-before-integration order, primary exclusivity and complete milestone coverage before using a revised roster.
