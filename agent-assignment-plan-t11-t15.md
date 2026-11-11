# Agent assignments for T11–T15

**Assignment revision 2. Start all 24 T11 milestone authors together.** Use four integration owners for browser, workflow, CLI/activity and durability/operations work. They integrate completed author changes before fresh independent milestone review. This replaces the single global ten-minute-per-milestone integration queue from revision 1. [Execution plan](execution-plan-t11-t15.md) · [Milestone scope and demos](docs/v1-parity-milestones/README.md) · [Machine-readable roster](agent-assignment-plan-t11-t15.json) · [Revision 1](https://github.com/localswim/localswim/blob/c251e3a46681b993d56a2d1a695324e68543318c/agent-assignment-plan-t11-t15.md)

## Execution capacity

The current single-root tool pool exposes 16 direct-worker slots. The 24-author start requires two independent execution chats with isolated worktrees, or an execution pool with that verified capacity. Each chat uses a Sol / High lead and at most 16 direct workers, with depth one.

| Execution chat | T11 author assignments | Initial authors | On-demand integration owners | Maximum named author/integration positions |
| --- | --- | ---: | ---: | ---: |
| Browser and workflow | [M040](docs/v1-parity-milestones/m40.md), [M041](docs/v1-parity-milestones/m41.md), [M045](docs/v1-parity-milestones/m45.md), [M047](docs/v1-parity-milestones/m47.md), [M049](docs/v1-parity-milestones/m49.md), [M050](docs/v1-parity-milestones/m50.md), [M051](docs/v1-parity-milestones/m51.md), [M058](docs/v1-parity-milestones/m58.md), [M062](docs/v1-parity-milestones/m62.md), [M063](docs/v1-parity-milestones/m63.md), [M064](docs/v1-parity-milestones/m64.md), [M065](docs/v1-parity-milestones/m65.md), [M100](docs/v1-parity-milestones/m100.md) | 13 | 2 | 15 |
| Operators | [M067](docs/v1-parity-milestones/m67.md), [M068](docs/v1-parity-milestones/m68.md), [M069](docs/v1-parity-milestones/m69.md), [M070](docs/v1-parity-milestones/m70.md), [M074](docs/v1-parity-milestones/m74.md), [M076](docs/v1-parity-milestones/m76.md), [M077](docs/v1-parity-milestones/m77.md), [M082](docs/v1-parity-milestones/m82.md), [M088](docs/v1-parity-milestones/m88.md), [M092](docs/v1-parity-milestones/m92.md), [M096](docs/v1-parity-milestones/m96.md) | 11 | 2 | 13 |

Completed author changes enter their integration group. A fresh independent reviewer then checks the complete integrated milestone at a pinned commit. The reviewer uses a released slot. Integration owners use available slots when they have authored work to combine. The modeled active-worker peak is 24, with 28 author/integration roles available across the two chats. Additional workers are authorized when a concrete test, review or merge bottleneck can be shortened. Assign that work and update the capacity calculation before launch.

The browser/workflow lead also owns the final shared branch, combined checks, commits and pushes. The operators lead delivers reviewed integration branches. Provision this execution topology before starting the clocks. This roster records planned capacity.

## Revised forecast

| Turn | Milestones starting at turn kickoff | Peak active workers | Turn estimate | Cumulative |
| --- | ---: | ---: | ---: | ---: |
| [T11](#t11) | 24 | 24 | 3h 45m | 3h 45m |
| [T12](#t12) | 20 | 20 | 3h 45m | 7h 30m |
| [T13](#t13) | 11 | 11 | 3h 45m | 11h 15m |
| [T14](#t14) | 5 | 5 | 3h 45m | 15h |
| [T15](#t15) | 2 | 2 | 3h 40m | 18h 40m |

The model forecasts **18h 40m** for implementation, review, integration and final checks, followed by the separately budgeted two-hour [parity walkthrough](docs/v1-parity-milestones/qualification.md). Individual demos, correction work, unresolved interface handoffs and owner waits add elapsed time.

| Modeled work | Estimate |
| --- | --- |
| Author implementation and focused tests | Existing per-milestone estimate, one author |
| Fresh independent review | 25 minutes for Medium review, 40 minutes for High review |
| Integration of one authored milestone | 10 minutes within its integration group |
| Combined group handoff and final gate | 20 minutes per turn on the primary |
| Sharing assumptions | The identified hooks and shared sections have one owner; feature bodies and tests develop concurrently |

The old 23-hour forecast included one globally serialized integration queue. More authors alone would leave that bottleneck. The revised estimate gains time from the 24-author start and concurrent integration groups. Ten-minute integration and twenty-minute final-gate durations remain planning assumptions. Measure them during T11 and update subsequent forecasts.

All times below are hours and minutes within that turn. A later turn begins after the prior handoff and required prerequisite acceptance. Human response time is excluded from the modeled clock.

## Shared-code findings

Source review supports parallel T11 implementation with short coordinated handoffs for the following code sections. Authors start by developing their separable logic and tests. The assigned owner composes the shared section as its contributions become ready.

| Milestones | Shared-section owner | Code seam | Handoff |
| --- | --- | --- | --- |
| M041, M045 | Browser owner | [load and reloadBoard](web/app.mjs#L1520), navigation epoch and restored view state | Agree the small lifecycle hooks during kickoff. The authors build streaming and tab state concurrently. |
| M047, M051 | Browser owner | [New card form](web/app.mjs#L1889) | Combine eligibility and relationship controls in one assembly patch. Separate policy and relationship logic remain parallel. |
| M049, M063, M064, M065 | Browser owner, with Workflow owner supplying reviewed controls | [renderCards](web/app.mjs#L1766) | One owner composes card totals, notes, reorder and approval controls. Keep each feature's tests separate. |
| M062 and drawer authors | Browser owner | [writeResource](web/app.mjs#L397) | Settle the scoped-operation call before wiring backup and drawer callers. Preserve uncertainty/readback handling. |
| M063, M064, M065 | Workflow owner | [lane patch parser](internal/v2/lanes_edit.go#L68), application and audit | Agree fields, defaults and audit shapes, then integrate that shared parser once. |
| M064, M100 | Workflow owner | [board create and patch](internal/v2/boards.go#L24), model and validation | Compose the separate presentation fields and OpenAPI changes together. |
| M068, M070, M074 | CLI owner | Focused projection, ticket resolver and local read source | Agree the small result shapes. Return board ID/revision and card ID/revision from resolution. Build projection, mutation and native reads against those shapes concurrently. |
| M074, M076 | CLI owner | [executeCLI](cmd/localswim/cli.go#L520) | M074 supplies source dispatch. M076 supplies structured verification output and exit handling. |
| M070 and card UI mutations | CLI owner and Workflow owner, with Browser owner consuming | [card patch preconditions](internal/v2/card_patch.go#L95) | Agree the optional board-revision guard alongside the existing card guard before adding callers. |
| M082 | Durability owner | [existing durable-state check](internal/v2/board_availability.go#L180) and ordinary writes | Reuse the accepted/durable comparison in ordinary-write admission. |
| All groups | Workflow owner for schema, CLI owner for command dispatch, Browser owner for assets | [OpenAPI](docs/openapi.yaml), shared model/routes, CLI dispatch, [web/package.json](web/package.json) | Collect changes to each shared section under one owner. The primary combines the four reviewed branch results. |

Use the existing functions and validation paths. Focused helpers are appropriate where required by the feature. The work remains within each milestone's approved scope.

## Integration ownership

| Group | Named owner | Model and effort | Execution chat |
| --- | --- | --- | --- |
| Workflow and API contracts | `sol_high_workflow_integrate` | Sol / High · `gpt-6.1-sol`, `high` | Browser and workflow |
| CLI and activity | `sol_high_cli_activity_integrate` | Sol / High · `gpt-6.1-sol`, `high` | Operators |
| Recovery, publication and Windows | `sol_high_durability_integrate` | Sol / High · `gpt-6.1-sol`, `high` | Operators |
| Browser application and workspace | `sol_high_browser_integrate` | Sol / High · `gpt-6.1-sol`, `high` | Browser and workflow |

Authors use isolated worktrees. Integration owners work on isolated group branches, own the listed shared sections and run focused combined checks. The assigned fresh milestone reviewer assesses all author and integration-authored behavior at a pinned commit, including clean assembly. Later behavioral changes to a shared section trigger fresh affected-scope review. The primary combines the reviewed groups and runs the full repository gate. Behavioral changes made during final consolidation also require fresh review and extend the forecast.

The model permits concurrent group integration after code ownership is settled. Any remaining shared-section queue adds measured time to that model. Treat each integration duration as a forecast and update it with actual measurements.

## Kickoff and rolling handoff

- Verify each real card's lane, scope and accepted prerequisites, then move it In Progress before implementation, tests or review.
- Launch every listed author at the start of its assigned turn once those gates hold. Set the explicit model and effort shown below and use Standard speed.
- Both execution leads use `gpt-6.1-sol` with `high` reasoning. Every subagent is directly assigned by its lead and stays within the per-chat runtime ceiling.
- Assign the scope-linked code, focused tests, help, documentation and OpenAPI work to the author. Give shared-section edits to the named integration owner.
- Integrate each completed author change, then start its fresh milestone reviewer against the complete integrated change. Release authors and reviewers when their assignments end. Start an integration owner only when its queue has useful work.
- Preserve one active browser operator and use the shared queue for browser diagnostics. Coordinate tests through disposable boards, files and remotes.
- The primary owns the final combined `check.ps1`, source commit and handoff. Move finished, independently reviewed work to Ready for Review when its integrated checks pass. Terry controls Completed.
- Demo available RFR milestones at turn end and each three-hour build break, one milestone at a time. Show CLI help and complete output where relevant.
- Refresh the next three remaining turns with measured durations, actual blockers and new assignments at each handoff. The current rolling window is T11–T13. T14–T15 remain fully assigned below.

## T11

Start all 24 authors together. Cumulative modeled start is 0h. The primary's group handoff and final checks run 3:25–3:45.

| Milestone and scope | Chat | Author | Author window / slot | Integration group and window | Fresh reviewer | Review window / slot |
| --- | --- | --- | --- | --- | --- | --- |
| [M040](docs/v1-parity-milestones/m40.md) · See Attention across projects | Browser/workflow | Sol / Medium<br>`sol_medium_m040_author` | 0:00–1:45 / 01 | Browser application and workspace<br>1:55–2:05 | Sol / Medium<br>`sol_medium_m040_review` | 2:05–2:30 / 01 |
| [M041](docs/v1-parity-milestones/m41.md) · Watch the open board update | Browser/workflow | Sol / High<br>`sol_high_m041_author` | 0:00–2:15 / 02 | Browser application and workspace<br>2:15–2:25 | Sol / High<br>`sol_high_m041_review` | 2:25–3:05 / 02 |
| [M045](docs/v1-parity-milestones/m45.md) · Resume a board view | Browser/workflow | Sol / High<br>`sol_high_m045_author` | 0:00–1:45 / 03 | Browser application and workspace<br>1:45–1:55 | Sol / High<br>`sol_high_m045_review` | 1:55–2:35 / 03 |
| [M047](docs/v1-parity-milestones/m47.md) · Start and move work where permitted | Browser/workflow | Sol / Medium<br>`sol_medium_m047_author` | 0:00–2:00 / 04 | Browser application and workspace<br>2:05–2:15 | Sol / High<br>`sol_high_m047_review` | 2:15–2:55 / 04 |
| [M049](docs/v1-parity-milestones/m49.md) · Scan cards and lane totals | Browser/workflow | Sol / Medium<br>`sol_medium_m049_author` | 0:00–1:15 / 05 | Browser application and workspace<br>1:15–1:25 | Sol / Medium<br>`sol_medium_m049_review` | 1:25–1:50 / 05 |
| [M050](docs/v1-parity-milestones/m50.md) · Set one card's priority directly | Browser/workflow | Sol / Medium<br>`sol_medium_m050_author` | 0:00–1:00 / 06 | Browser application and workspace<br>1:00–1:10 | Sol / Medium<br>`sol_medium_m050_review` | 1:10–1:35 / 06 |
| [M051](docs/v1-parity-milestones/m51.md) · Navigate and seed related work | Browser/workflow | Sol / Medium<br>`sol_medium_m051_author` | 0:00–2:25 / 07 | Browser application and workspace<br>2:35–2:45 | Sol / Medium<br>`sol_medium_m051_review` | 2:45–3:10 / 07 |
| [M058](docs/v1-parity-milestones/m58.md) · Paste and upload several files to a card | Browser/workflow | Sol / High<br>`sol_high_m058_author` | 0:00–2:15 / 08 | Browser application and workspace<br>2:25–2:35 | Sol / High<br>`sol_high_m058_review` | 2:35–3:15 / 08 |
| [M062](docs/v1-parity-milestones/m62.md) · Keep working while a backup runs | Browser/workflow | Sol / High<br>`sol_high_m062_author` | 0:00–1:30 / 09 | Browser application and workspace<br>1:30–1:40 | Sol / High<br>`sol_high_m062_review` | 1:40–2:20 / 09 |
| [M063](docs/v1-parity-milestones/m63.md) · Opt a lane into dependency reorder | Browser/workflow | Sol / High<br>`sol_high_m063_author` | 0:00–1:45 / 10 | Workflow and API contracts<br>1:45–1:55 | Sol / High<br>`sol_high_m063_review` | 1:55–2:35 / 10 |
| [M064](docs/v1-parity-milestones/m64.md) · See lane notes and board actor context | Browser/workflow | Sol / Medium<br>`sol_medium_m064_author` | 0:00–2:05 / 11 | Workflow and API contracts<br>2:05–2:15 | Sol / High<br>`sol_high_m064_review` | 2:15–2:55 / 11 |
| [M065](docs/v1-parity-milestones/m65.md) · Approve a configured lane as Terry | Browser/workflow | Sol / High<br>`sol_high_m065_author` | 0:00–2:35 / 12 | Workflow and API contracts<br>2:35–2:45 | Sol / High<br>`sol_high_m065_review` | 2:45–3:25 / 12 |
| [M067](docs/v1-parity-milestones/m67.md) · Pick next work across lanes | Operators | Sol / Medium<br>`sol_medium_m067_author` | 0:00–2:00 / 01 | CLI and activity<br>2:20–2:30 | Sol / Medium<br>`sol_medium_m067_review` | 2:30–2:55 / 01 |
| [M068](docs/v1-parity-milestones/m68.md) · Inspect a ticket and its context safely | Operators | Sol / Medium<br>`sol_medium_m068_author` | 0:00–2:00 / 02 | CLI and activity<br>2:30–2:40 | Sol / Medium<br>`sol_medium_m068_review` | 2:40–3:05 / 02 |
| [M069](docs/v1-parity-milestones/m69.md) · Catch up on recent comments by ticket | Operators | Sol / Medium<br>`sol_medium_m069_author` | 0:00–1:15 / 03 | CLI and activity<br>1:15–1:25 | Sol / Medium<br>`sol_medium_m069_review` | 1:25–1:50 / 03 |
| [M070](docs/v1-parity-milestones/m70.md) · Act on a card by ticket | Operators | Sol / Medium<br>`sol_medium_m070_author` | 0:00–2:00 / 04 | CLI and activity<br>2:00–2:10 | Sol / High<br>`sol_high_m070_review` | 2:10–2:50 / 04 |
| [M074](docs/v1-parity-milestones/m74.md) · Inspect and export saved work with the writer down | Operators | Sol / High<br>`sol_high_m074_author` | 0:00–2:00 / 05 | CLI and activity<br>2:10–2:20 | Sol / High<br>`sol_high_m074_review` | 2:20–3:00 / 05 |
| [M076](docs/v1-parity-milestones/m76.md) · Fail automation on failed board verification | Operators | Sol / Medium<br>`sol_medium_m076_author` | 0:00–0:45 / 06 | CLI and activity<br>0:45–0:55 | Sol / Medium<br>`sol_medium_m076_review` | 0:55–1:20 / 06 |
| [M077](docs/v1-parity-milestones/m77.md) · Inspect activity without leaking card prose | Operators | Sol / Medium<br>`sol_medium_m077_author` | 0:00–1:30 / 07 | CLI and activity<br>1:30–1:40 | Sol / High<br>`sol_high_m077_review` | 1:40–2:20 / 07 |
| [M082](docs/v1-parity-milestones/m82.md) · Protect a board whose saved data changed | Operators | Sol / High<br>`sol_high_m082_author` | 0:00–2:25 / 08 | Recovery, publication and Windows<br>2:25–2:35 | Sol / High<br>`sol_high_m082_review` | 2:35–3:15 / 08 |
| [M088](docs/v1-parity-milestones/m88.md) · Keep ignored boards out of Git | Operators | Sol / High<br>`sol_high_m088_author` | 0:00–1:45 / 09 | Recovery, publication and Windows<br>1:45–1:55 | Sol / High<br>`sol_high_m088_review` | 1:55–2:35 / 09 |
| [M092](docs/v1-parity-milestones/m92.md) · Start V2 independently on Windows | Operators | Sol / High<br>`sol_high_m092_author` | 0:00–2:00 / 10 | Recovery, publication and Windows<br>2:00–2:10 | Sol / High<br>`sol_high_m092_review` | 2:10–2:50 / 10 |
| [M096](docs/v1-parity-milestones/m96.md) · Validate a large portable backup locally | Operators | Sol / High<br>`sol_high_m096_author` | 0:00–2:15 / 11 | Recovery, publication and Windows<br>2:15–2:25 | Sol / High<br>`sol_high_m096_review` | 2:25–3:05 / 11 |
| [M100](docs/v1-parity-milestones/m100.md) · Apply board-specific browser styling | Browser/workflow | Sol / Medium<br>`sol_medium_m100_author` | 0:00–1:30 / 13 | Workflow and API contracts<br>1:30–1:40 | Sol / High<br>`sol_high_m100_review` | 1:40–2:20 / 13 |

Every prerequisite for these milestones is listed in its linked scope file and in the JSON roster.

## T12

Start all 20 authors together. Cumulative modeled start is 3h 45m. The primary's group handoff and final checks run 3:25–3:45.

| Milestone and scope | Chat | Author | Author window / slot | Integration group and window | Fresh reviewer | Review window / slot |
| --- | --- | --- | --- | --- | --- | --- |
| [M042](docs/v1-parity-milestones/m42.md) · Watch workspace Attention update | Browser/workflow | Sol / High<br>`sol_high_m042_author` | 0:00–2:00 / 01 | Browser application and workspace<br>2:05–2:15 | Sol / High<br>`sol_high_m042_review` | 2:15–2:55 / 01 |
| [M043](docs/v1-parity-milestones/m43.md) · Organize pins from the sidebar | Browser/workflow | Sol / Medium<br>`sol_medium_m043_author` | 0:00–1:45 / 02 | Browser application and workspace<br>2:35–2:45 | Sol / Medium<br>`sol_medium_m043_review` | 2:45–3:10 / 02 |
| [M044](docs/v1-parity-milestones/m44.md) · Resize and collapse the sidebar | Browser/workflow | Sol / Medium<br>`sol_medium_m044_author` | 0:00–1:15 / 03 | Browser application and workspace<br>1:15–1:25 | Sol / Medium<br>`sol_medium_m044_review` | 1:25–1:50 / 03 |
| [M046](docs/v1-parity-milestones/m46.md) · Reopen a recent board immediately | Browser/workflow | Sol / High<br>`sol_high_m046_author` | 0:00–2:15 / 04 | Browser application and workspace<br>2:15–2:25 | Sol / High<br>`sol_high_m046_review` | 2:25–3:05 / 04 |
| [M048](docs/v1-parity-milestones/m48.md) · Drag a card to an allowed lane | Browser/workflow | Sol / Medium<br>`sol_medium_m048_author` | 0:00–1:45 / 05 | Browser application and workspace<br>1:45–1:55 | Sol / High<br>`sol_high_m048_review` | 1:55–2:35 / 05 |
| [M052](docs/v1-parity-milestones/m52.md) · Filter work without leaving the board | Browser/workflow | Sol / Medium<br>`sol_medium_m052_author` | 0:00–1:45 / 06 | Browser application and workspace<br>2:45–2:55 | Sol / Medium<br>`sol_medium_m052_review` | 2:55–3:20 / 06 |
| [M053](docs/v1-parity-milestones/m53.md) · Review recent or older Completed work | Browser/workflow | Sol / Medium<br>`sol_medium_m053_author` | 0:00–1:15 / 07 | Browser application and workspace<br>1:25–1:35 | Sol / Medium<br>`sol_medium_m053_review` | 1:35–2:00 / 07 |
| [M056](docs/v1-parity-milestones/m56.md) · Recover drafts and submitted actions | Browser/workflow | Sol / High<br>`sol_high_m056_author` | 0:00–2:15 / 08 | Browser application and workspace<br>2:25–2:35 | Sol / High<br>`sol_high_m056_review` | 2:35–3:15 / 08 |
| [M059](docs/v1-parity-milestones/m59.md) · Create a card with files | Browser/workflow | Sol / Medium<br>`sol_medium_m059_author` | 0:00–1:45 / 09 | Browser application and workspace<br>1:55–2:05 | Sol / High<br>`sol_high_m059_review` | 2:05–2:45 / 09 |
| [M061](docs/v1-parity-milestones/m61.md) · See an image preview on the card | Browser/workflow | Sol / Medium<br>`sol_medium_m061_author` | 0:00–1:00 / 10 | Browser application and workspace<br>1:00–1:10 | Sol / Medium<br>`sol_medium_m061_review` | 1:10–1:35 / 10 |
| [M066](docs/v1-parity-milestones/m66.md) · Start a board with Terry's workflow | Browser/workflow | Sol / High<br>`sol_high_m066_author` | 0:00–1:45 / 11 | Workflow and API contracts<br>1:45–1:55 | Sol / High<br>`sol_high_m066_review` | 1:55–2:35 / 11 |
| [M071](docs/v1-parity-milestones/m71.md) · Manage hierarchy and links by ticket | Operators | Sol / Medium<br>`sol_medium_m071_author` | 0:00–2:00 / 01 | CLI and activity<br>2:05–2:15 | Sol / High<br>`sol_high_m071_review` | 2:15–2:55 / 01 |
| [M072](docs/v1-parity-milestones/m72.md) · Run routine writes without composing JSON | Operators | Sol / Medium<br>`sol_medium_m072_author` | 0:00–2:15 / 02 | CLI and activity<br>2:15–2:25 | Sol / High<br>`sol_high_m072_review` | 2:25–3:05 / 02 |
| [M075](docs/v1-parity-milestones/m75.md) · Triage and catch up offline | Operators | Sol / Medium<br>`sol_medium_m075_author` | 0:00–1:45 / 03 | CLI and activity<br>1:45–1:55 | Sol / High<br>`sol_high_m075_review` | 1:55–2:35 / 03 |
| [M078](docs/v1-parity-milestones/m78.md) · Verify what an owner notice covers | Operators | Sol / High<br>`sol_high_m078_author` | 0:00–2:35 / 04 | CLI and activity<br>2:35–2:45 | Sol / High<br>`sol_high_m078_review` | 2:45–3:25 / 04 |
| [M079](docs/v1-parity-milestones/m79.md) · Explicitly route notices to the owning chat | Operators | Sol / High<br>`sol_high_m079_author` | 0:00–1:45 / 05 | CLI and activity<br>1:55–2:05 | Sol / High<br>`sol_high_m079_review` | 2:05–2:45 / 05 |
| [M083](docs/v1-parity-milestones/m83.md) · Start with one damaged board and keep working | Operators | Sol / High<br>`sol_high_m083_author` | 0:00–2:15 / 06 | Recovery, publication and Windows<br>2:15–2:25 | Sol / High<br>`sol_high_m083_review` | 2:25–3:05 / 06 |
| [M089](docs/v1-parity-milestones/m89.md) · Publish to the intended Git destination | Operators | Sol / High<br>`sol_high_m089_author` | 0:00–2:25 / 07 | Recovery, publication and Windows<br>2:25–2:35 | Sol / High<br>`sol_high_m089_review` | 2:35–3:15 / 07 |
| [M091](docs/v1-parity-milestones/m91.md) · Publish without disturbing staged work | Operators | Sol / High<br>`sol_high_m091_author` | 0:00–2:00 / 08 | Recovery, publication and Windows<br>2:00–2:10 | Sol / High<br>`sol_high_m091_review` | 2:10–2:50 / 08 |
| [M098](docs/v1-parity-milestones/m98.md) · Work with files and batches using ticket references | Operators | Sol / Medium<br>`sol_medium_m098_author` | 0:00–1:45 / 09 | CLI and activity<br>2:25–2:35 | Sol / High<br>`sol_high_m098_review` | 2:35–3:15 / 09 |

Every prerequisite for these milestones is listed in its linked scope file and in the JSON roster.

## T13

Start all 11 authors together. Cumulative modeled start is 7h 30m. The primary's group handoff and final checks run 3:25–3:45.

| Milestone and scope | Chat | Author | Author window / slot | Integration group and window | Fresh reviewer | Review window / slot |
| --- | --- | --- | --- | --- | --- | --- |
| [M054](docs/v1-parity-milestones/m54.md) · Read and find formatted card prose | Browser/workflow | Sol / Medium<br>`sol_medium_m054_author` | 0:00–2:15 / 01 | Browser application and workspace<br>2:15–2:25 | Sol / High<br>`sol_high_m054_review` | 2:25–3:05 / 01 |
| [M055](docs/v1-parity-milestones/m55.md) · Edit and review by keyboard | Browser/workflow | Sol / Medium<br>`sol_medium_m055_author` | 0:00–1:30 / 02 | Browser application and workspace<br>1:30–1:40 | Sol / Medium<br>`sol_medium_m055_review` | 1:40–2:05 / 02 |
| [M057](docs/v1-parity-milestones/m57.md) · See slow edits while they settle | Browser/workflow | Sol / High<br>`sol_high_m057_author` | 0:00–2:30 / 03 | Browser application and workspace<br>2:35–2:45 | Sol / High<br>`sol_high_m057_review` | 2:45–3:25 / 03 |
| [M060](docs/v1-parity-milestones/m60.md) · Resume a partial upload safely | Browser/workflow | Sol / High<br>`sol_high_m060_author` | 0:00–2:15 / 04 | Browser application and workspace<br>2:25–2:35 | Sol / High<br>`sol_high_m060_review` | 2:35–3:15 / 04 |
| [M073](docs/v1-parity-milestones/m73.md) · Provision and adjust workflows from the CLI | Operators | Sol / High<br>`sol_high_m073_author` | 0:00–1:45 / 01 | CLI and activity<br>1:45–1:55 | Sol / High<br>`sol_high_m073_review` | 1:55–2:35 / 01 |
| [M080](docs/v1-parity-milestones/m80.md) · Restore without rolling back notice authorization | Operators | Sol / High<br>`sol_high_m080_author` | 0:00–2:15 / 02 | CLI and activity<br>2:15–2:25 | Sol / High<br>`sol_high_m080_review` | 2:25–3:05 / 02 |
| [M081](docs/v1-parity-milestones/m81.md) · Recognize approval and lane changes in notices | Operators | Sol / High<br>`sol_high_m081_author` | 0:00–1:45 / 03 | CLI and activity<br>1:55–2:05 | Sol / High<br>`sol_high_m081_review` | 2:05–2:45 / 03 |
| [M087](docs/v1-parity-milestones/m87.md) · See durable health changes from the open workspace | Browser/workflow | Sol / High<br>`sol_high_m087_author` | 0:00–1:45 / 05 | Browser application and workspace<br>1:45–1:55 | Sol / High<br>`sol_high_m087_review` | 1:55–2:35 / 05 |
| [M095](docs/v1-parity-milestones/m95.md) · Identify a damaged board and its recovery point | Operators | Sol / High<br>`sol_high_m095_author` | 0:00–2:25 / 04 | Recovery, publication and Windows<br>2:25–2:35 | Sol / High<br>`sol_high_m095_review` | 2:35–3:15 / 04 |
| [M097](docs/v1-parity-milestones/m97.md) · Retry a pending push to its original destination | Operators | Sol / High<br>`sol_high_m097_author` | 0:00–2:25 / 05 | Recovery, publication and Windows<br>2:35–2:45 | Sol / High<br>`sol_high_m097_review` | 2:45–3:25 / 05 |
| [M101](docs/v1-parity-milestones/m101.md) · Inspect saved activity with the writer down | Operators | Sol / High<br>`sol_high_m101_author` | 0:00–1:35 / 06 | CLI and activity<br>1:35–1:45 | Sol / High<br>`sol_high_m101_review` | 1:45–2:25 / 06 |

Every prerequisite for these milestones is listed in its linked scope file and in the JSON roster.

## T14

Start all 5 authors together. Cumulative modeled start is 11h 15m. The primary's group handoff and final checks run 3:25–3:45.

| Milestone and scope | Chat | Author | Author window / slot | Integration group and window | Fresh reviewer | Review window / slot |
| --- | --- | --- | --- | --- | --- | --- |
| [M084](docs/v1-parity-milestones/m84.md) · Recover a board that cannot load | Operators | Sol / High<br>`sol_high_m084_author` | 0:00–2:35 / 01 | Recovery, publication and Windows<br>2:35–2:45 | Sol / High<br>`sol_high_m084_review` | 2:45–3:25 / 01 |
| [M086](docs/v1-parity-milestones/m86.md) · Restore a downloaded attachment-heavy backup | Operators | Sol / High<br>`sol_high_m086_author` | 0:00–2:05 / 02 | Recovery, publication and Windows<br>2:05–2:15 | Sol / High<br>`sol_high_m086_review` | 2:15–2:55 / 02 |
| [M090](docs/v1-parity-milestones/m90.md) · Recover cleanly from a stalled publication | Operators | Sol / High<br>`sol_high_m090_author` | 0:00–2:15 / 03 | Recovery, publication and Windows<br>2:15–2:25 | Sol / High<br>`sol_high_m090_review` | 2:25–3:05 / 03 |
| [M094](docs/v1-parity-milestones/m94.md) · Keep busy workspaces responsive | Browser/workflow | Sol / High<br>`sol_high_m094_author` | 0:00–2:35 / 01 | Browser application and workspace<br>2:35–2:45 | Sol / High<br>`sol_high_m094_review` | 2:45–3:25 / 01 |
| [M099](docs/v1-parity-milestones/m99.md) · Discover commands from their own help | Operators | Sol / Medium<br>`sol_medium_m099_author` | 0:00–1:30 / 04 | CLI and activity<br>1:30–1:40 | Sol / Medium<br>`sol_medium_m099_review` | 1:40–2:05 / 04 |

Every prerequisite for these milestones is listed in its linked scope file and in the JSON roster.

## T15

Start all 2 authors together. Cumulative modeled start is 15h. The primary's group handoff and final checks run 3:20–3:40.

| Milestone and scope | Chat | Author | Author window / slot | Integration group and window | Fresh reviewer | Review window / slot |
| --- | --- | --- | --- | --- | --- | --- |
| [M085](docs/v1-parity-milestones/m85.md) · Contain an uncertain commit to its actual scope | Operators | Sol / High<br>`sol_high_m085_author` | 0:00–2:30 / 01 | Recovery, publication and Windows<br>2:30–2:40 | Sol / High<br>`sol_high_m085_review` | 2:40–3:20 / 01 |
| [M093](docs/v1-parity-milestones/m93.md) · Reconnect after a Windows crash or upgrade | Operators | Sol / High<br>`sol_high_m093_author` | 0:00–1:45 / 02 | Recovery, publication and Windows<br>1:45–1:55 | Sol / High<br>`sol_high_m093_review` | 1:55–2:35 / 02 |

Every prerequisite for these milestones is listed in its linked scope file and in the JSON roster.

## Model verification

The roster contains 62 authors, 62 distinct reviewers, four integration groups, 62 group integration windows and five primary final gates. Verify each prerequisite turn, author-before-integration order, integration-before-review order, group queue exclusivity, per-chat slot ceiling and combined active-worker peak. Final-gate time includes combining the reviewed groups. Measured contention or required re-review extends the forecast and becomes visible in the next assignment revision.
