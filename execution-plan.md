# V2 development execution plan

Revision 1. Scope comes from approved board cards #0375 and #0376, [the milestone plan](milestone-plan.md), and [the API contract](docs/openapi.yaml). This is the staffing proposal for Turns 1-10. The milestone demos, estimates, and approved turn placement remain the scope.

## Staffing rule

Each milestone gets one implementation lead and one fresh independent reviewer after implementation. M01, M29, M34, and M39 also get one narrowly assigned verification helper. Two independent staffing reviews support these counts.

Build agents means the lead plus any helper active together. The reviewer replaces finished authors in the active pool. This is two distinct assignments per ordinary milestone, or three for the four helper milestones, with a peak of one or two active workers per milestone. A reviewer must not have authored that milestone or its tests.

Use Sol / Medium for ordinary implementation. The primary assigns Sol / High to authentication, sensitive storage/recovery, and process/concurrency work. The table records that choice explicitly. Both use model `gpt-6.1-sol`, with reasoning effort `medium` or `high`.

## Turn roster and capacity

| Turn | Milestones | Work dispatched | Backend build agents | Including browser + CLI |
| --- | ---: | --- | ---: | ---: |
| 1 | 8 | M01, M16, M08, M28, M26, M27, M32, M38 | 9 | 11 |
| 2 | 3 | M02, M17, M31 | 3 | 5 |
| 3 | 5 | M03, M05, M04, M18, M23 | 5 | 7 |
| 4 | 5 | M06, M14, M09, M24, M33 | 5 | 7 |
| 5 | 5 | M07, M15, M19, M10, M25 | 5 | 7 |
| 6 | 5 | M20, M11, M12, M21, M29 | 6 | 8 |
| 7 | 4 | M13, M22, M30, M34 | 5 | 7 |
| 8 | 2 | M35, M36 | 2 | 4 |
| 9 | 1 | M37 | 1 | 3 |
| 10 | 1 | M39 | 2 | 4 |

The baseline wave peaks at 11 active subagents plus the primary. The standing ceiling is 16 active subagents plus the primary. Before dispatch, count carryover authors, helpers, reviewers, and client workers against that ceiling. Defer new assignments when capacity is full. These planned counts are not a requirement to keep workers occupied. Release a blocked or finished worker instead of leaving it waiting.

The browser author is `sol_medium_browser_author`. The CLI author is `sol_medium_cli_author`. Each uses Sol / Medium and starts with the shared API contract, then integrates against real endpoints. Each client receives a fresh Sol / High review when its changes are ready. Those reviewers replace released workers. Client implementation and review are additional to the backend's 82.5-hour estimate.

## Milestone assignments

| Turn | Milestone | Build agents | Lead | Helper | Fresh review |
| --- | --- | ---: | --- | --- | --- |
| 1 | **M01** - Create a workspace and boards | 2 | Sol / High | Sol / High | Sol / High |
| 1 | **M16** - Create a custom workflow | 1 | Sol / Medium | - | Sol / High |
| 1 | **M08** - Set a parent | 1 | Sol / Medium | - | Sol / High |
| 1 | **M28** - Verify and export native board state | 1 | Sol / High | - | Sol / High |
| 1 | **M26** - Show workspace attention and recency | 1 | Sol / Medium | - | Sol / Medium |
| 1 | **M27** - Persist pinned board order | 1 | Sol / Medium | - | Sol / High |
| 1 | **M32** - Configure owner notices | 1 | Sol / Medium | - | Sol / High |
| 1 | **M38** - Report build health before activation | 1 | Sol / High | - | Sol / High |
| 2 | **M02** - Create and find cards | 1 | Sol / Medium | - | Sol / High |
| 2 | **M17** - Grant policy permissions | 1 | Sol / High | - | Sol / High |
| 2 | **M31** - Stream live board and workspace changes | 1 | Sol / High | - | Sol / High |
| 3 | **M03** - Move and assign cards | 1 | Sol / Medium | - | Sol / High |
| 3 | **M05** - Comment and read newest comments | 1 | Sol / Medium | - | Sol / High |
| 3 | **M04** - Browser session and conflict-safe edits | 1 | Sol / High | - | Sol / High |
| 3 | **M18** - Add and rename lanes | 1 | Sol / Medium | - | Sol / High |
| 3 | **M23** - Upload and list attachments | 1 | Sol / High | - | Sol / High |
| 4 | **M06** - Archive and restore cards | 1 | Sol / Medium | - | Sol / High |
| 4 | **M14** - Approve one review | 1 | Sol / Medium | - | Sol / High |
| 4 | **M09** - Manage typed links | 1 | Sol / Medium | - | Sol / High |
| 4 | **M24** - Download, preview, and select a cover | 1 | Sol / Medium | - | Sol / High |
| 4 | **M33** - Deliver a committed Human notice | 1 | Sol / High | - | Sol / High |
| 5 | **M07** - Read audited activity | 1 | Sol / Medium | - | Sol / Medium |
| 5 | **M15** - Approve a review lane | 1 | Sol / High | - | Sol / High |
| 5 | **M19** - Retire a lane | 1 | Sol / Medium | - | Sol / High |
| 5 | **M10** - Apply blocking priority floors | 1 | Sol / Medium | - | Sol / High |
| 5 | **M25** - Detach an attachment | 1 | Sol / Medium | - | Sol / High |
| 6 | **M20** - Change a lane key | 1 | Sol / Medium | - | Sol / High |
| 6 | **M11** - Change priorities as a batch | 1 | Sol / Medium | - | Sol / High |
| 6 | **M12** - Order visible swimlanes | 1 | Sol / Medium | - | Sol / Medium |
| 6 | **M21** - Preview dependency ticket order | 1 | Sol / Medium | - | Sol / Medium |
| 6 | **M29** - Capture a coherent backup | 2 | Sol / High | Sol / High | Sol / High |
| 7 | **M13** - Search and find next work | 1 | Sol / Medium | - | Sol / Medium |
| 7 | **M22** - Apply dependency ticket order | 1 | Sol / Medium | - | Sol / High |
| 7 | **M30** - Restore or admit a native snapshot | 1 | Sol / High | - | Sol / High |
| 7 | **M34** - Publish an exact Git backup | 2 | Sol / High | Sol / High | Sol / High |
| 8 | **M35** - Publish automatically and recover a failed push | 1 | Sol / High | - | Sol / High |
| 8 | **M36** - Publish workspace preferences independently | 1 | Sol / Medium | - | Sol / High |
| 9 | **M37** - Stop and reopen one board | 1 | Sol / High | - | Sol / High |
| 10 | **M39** - Activate a build and stop cleanly | 2 | Sol / High | Sol / High | Sol / High |

Planned names expand from the milestone and effort. For example, M01 uses `sol_high_m01_author`, `sol_high_m01_checks`, and fresh `sol_high_m01_review`. M02 uses `sol_medium_m02_author` and fresh `sol_high_m02_review`. Apply the same naming rule to every row.

The 39 leads and four helpers make 43 backend author assignments. The 39 independent milestone reviews make 82 distinct backend assignments across the plan, reused through a much smaller active pool.

## Helper scope

| Milestone | Separate helper work |
| --- | --- |
| M01 | Competing-writer, durable restart, and atomic-write failure checks in isolated test files. The lead owns schema, store, and startup code. |
| M29 | Coherent backup capture while edits continue, immutable attachment inventory, and failure cases. The lead owns capture code. |
| M34 | Disposable Git repositories/remotes, staged-file preservation, exact-path publication, and shared-lock checks. The lead owns publishing code. |
| M39 | Native Windows activation, same-port handoff, credential rotation, drain, and failed-final-push scenarios. The lead owns lifecycle code. |

Start a helper after the relevant interface is settled and a separate file boundary is available. Otherwise use the lead alone. Review the combined implementation and helper changes together.

## Readiness and integration

Turns are dispatch waves. Carry unfinished work visibly into later turns. A live demo waits for its real prerequisites even when implementation could start earlier against a shared interface.

Turn 1 has an explicit integration dependency. The primary and M01 lead own the minimal shared store, IDs, actor context, revision checks, and route composition. M08 can prepare parent rules in Turn 1, but its live card demo needs M02. M26's full recency/archive demo needs the relevant card and project-name operations. M17 can prepare grants in Turn 2, but its live newly authorized move needs M03. M16, M28, M32, and M38 also need the shared service before their live demonstrations. Early assignment is not evidence that those demonstrations can finish in the same turn.

The primary owns shared schema/API decisions, integration, required checks, commits, pushes, and demo assembly. Feature authors own separate files and do not independently redesign the shared transaction or routing layer. M03/M04/M05 share card writes; M10/M11/M12/M21 share graph data; recovery and publishing share the attachment inventory. Set these boundaries before concurrent edits.

Before actual implementation, check the ticket's lane, scope, and prerequisite acceptance. Move each eligible ticket In Progress before author, helper, test, or review work. Keep it there throughout correction and independent review. Terry controls Completed. A planned assignment, landing, or demo does not waive that gate.

## Demo and rolling handoff

**Include in every turn kickoff.** Move a milestone card to Ready for Review as soon as its assigned subagents have finished and implementation, required checks, and independent review are complete. Demos do not gate the lane move. Record any outstanding live walkthrough as Demo pending with its prerequisites, then show it at the next eligible demo break. Terry controls Completed.

Demo at least once every three hours of build time. At the end of a turn, demo any milestones in Ready for Review that can demo, even if less than three hours have elapsed.

Finish the T01 walkthroughs before starting T02. T02 starts with its demo clock at zero. T01 build time does not carry into that clock. Prefer actual CLI or web UI demonstrations where implemented, showing the available command help and real output. Present one milestone per chat turn at Terry’s pace.

Track pending demos separately from unfinished implementation or review. Demo-only carryover does not reserve a developer or reviewer slot, or add a prerequisite to downstream implementation. If a demo exposes a defect, record the actual finding as work carryover.

At the end of each execution turn, reconcile actual landings, reviews, Terry's acceptance, work carryover, and the pending demo list. Refresh the next three remaining-turn assignments with the same explicit author, helper, reviewer, model/effort, scope, and readiness fields. Preserve the original turn IDs as this baseline and retain previous assignment revisions. The initial remaining-turn window is Turns 1, 2, and 3 above.

The three-day goal remains a delivery target. Author estimates alone exclude client work, integration, independent review, and demo time. The first integrated browser demonstration is the checkpoint for updating that forecast.

## Rolling roster revision 4

T01 held M08 (#0391) and M26 (#0409) In Progress while waiting for live demos. Both are now in Ready for Review. Carry their demos separately from implementation and review. M38 (#0421) is also in Ready for Review following Terry’s approval of the two-route demo.

### Demo pending

- **M08 (#0391).** The card PATCH and read demo needs M02 (#0385).
- **M26 (#0409).** The archive and recency demo needs M02 (#0385) for cards, M06 (#0389) for archiving, and M16 (#0399) for project rename.

### Next three remaining waves

Turns 2–4 retain the author and reviewer assignments recorded in revision 3. Before starting M31, verify acceptance of M26 (#0409) and M28 (#0411). Both are in Ready for Review.

## Rolling roster revision 3

Historical Turn 1 handoff. Revision 4 supersedes the lane and demo gates below.

2026-11-10. Turn 1 implementation and review evidence are recorded below. The approved milestone and staffing tables above remain the baseline.

Plan cards #0375, #0376, and #0377 were observed in Completed. [Implementation commit bc8ee24](https://github.com/localswim/localswim/commit/bc8ee24d7b603bad3cb39fcaf9033867b08f068d)

- #384 M01, Ready for Review. Native two-board restart, writer exclusion, and failed replacement passed.
- #399 M16, Ready for Review. Custom workflow, atomic rejection, and stable board rename passed.
- #391 M08, In Progress. Reviewed durable parent and audit fixture passed. Public card PATCH/read demo awaits M02.
- #411 M28, Ready for Review. Native verification/export and offline validation passed for valid and damaged data.
- #409 M26, In Progress. Reviewed catalog fixtures and live zero-versus-unknown check passed. Full archive/recency demo awaits M02 and M06 plus M16 acceptance.
- #410 M27, Ready for Review. Workspace pin order, stale refusal, restart, and damaged settings passed.
- #415 M32, Ready for Review. Notice target and toggles persisted, and unauthorized management was refused.
- #421 M38, Needs Terry. Reviewed combined service/build demo passed. Terry must reconcile the milestone wording with the closed Service schema.

### Turn 1 carryover

- #391 M08. Live demo held until M02 #0385 provides accepted public card resources and card-scoped admission.
  Planned author sol_medium_0391_demo (gpt-6.1-sol / medium). Fresh reviewer sol_high_0391_demo_review (gpt-6.1-sol / high).
- #409 M26. Full live demo held until accepted M02 #0385 and M06 #0389 card/archive operations and M16 #0399 project rename. M31 remains held by this gate.
  Planned author sol_medium_0409_demo (gpt-6.1-sol / medium). Fresh reviewer sol_medium_0409_demo_review (gpt-6.1-sol / medium).
- #421 M38. Terry decides whether to accept the two-route demonstration. Primary records acceptance. Rework is contingent on explicit contract-change authority.
  Planned author sol_high_0421_contract (gpt-6.1-sol / high). Fresh reviewer sol_high_0421_contract_review (gpt-6.1-sol / high).

### Next three remaining waves

Scope for each row comes from approved #0375 and #0376, the corresponding milestone demo, and the OpenAPI contract. Original turn numbers are preserved. A listed assignment does not grant execution authority. Verify Terry acceptance and move the story In Progress before work.

| Remaining wave | Baseline turn | Ticket | Planned author | Fresh reviewer | Prerequisite status |
| ---: | ---: | --- | --- | --- | --- |
| 1 | 2 | #0385 M02 | sol_medium_0385_author (gpt-6.1-sol / medium) | sol_high_0385_review (gpt-6.1-sol / high) | M01 #384 Ready for Review |
| 1 | 2 | #0400 M17 | sol_high_0400_author (gpt-6.1-sol / high) | sol_high_0400_review (gpt-6.1-sol / high) | M16 #399 Ready for Review |
| 1 | 2 | #0414 M31 | sol_high_0414_author (gpt-6.1-sol / high) | sol_high_0414_review (gpt-6.1-sol / high) | M26 #409 In Progress, M28 #411 Ready for Review and M26 full demo awaits M06 |
| 2 | 3 | #0386 M03 | sol_medium_0386_author (gpt-6.1-sol / medium) | sol_high_0386_review (gpt-6.1-sol / high) | M02 #385 Backlog |
| 2 | 3 | #0388 M05 | sol_medium_0388_author (gpt-6.1-sol / medium) | sol_high_0388_review (gpt-6.1-sol / high) | M02 #385 Backlog |
| 2 | 3 | #0387 M04 | sol_high_0387_author (gpt-6.1-sol / high) | sol_high_0387_review (gpt-6.1-sol / high) | M02 #385 Backlog |
| 2 | 3 | #0401 M18 | sol_medium_0401_author (gpt-6.1-sol / medium) | sol_high_0401_review (gpt-6.1-sol / high) | M17 #400 Backlog |
| 2 | 3 | #0406 M23 | sol_high_0406_author (gpt-6.1-sol / high) | sol_high_0406_review (gpt-6.1-sol / high) | M02 #385 Backlog |
| 3 | 4 | #0389 M06 | sol_medium_0389_author (gpt-6.1-sol / medium) | sol_high_0389_review (gpt-6.1-sol / high) | M05 #388 Backlog |
| 3 | 4 | #0397 M14 | sol_medium_0397_author (gpt-6.1-sol / medium) | sol_high_0397_review (gpt-6.1-sol / high) | M03 #386 Backlog, M04 #387 Backlog, M05 #388 Backlog |
| 3 | 4 | #0392 M09 | sol_medium_0392_author (gpt-6.1-sol / medium) | sol_high_0392_review (gpt-6.1-sol / high) | M05 #388 Backlog |
| 3 | 4 | #0407 M24 | sol_medium_0407_author (gpt-6.1-sol / medium) | sol_high_0407_review (gpt-6.1-sol / high) | M23 #406 Backlog |
| 3 | 4 | #0416 M33 | sol_high_0416_author (gpt-6.1-sol / high) | sol_high_0416_review (gpt-6.1-sol / high) | M05 #388 Backlog, M32 #415 Ready for Review |

The browser and CLI authors remain Sol / Medium under their baseline names. Fresh client reviewers use Sol / High. The browser first-page API adapter and CLI passed Turn 1 review. Live browser bootstrap and CSP evidence await M04.

The primary owns integration, Windows verification, demo coordination, publishing, and acceptance recording. Release finished authors before dispatching fresh reviewers. Keep the existing capacity, authorization, and independent review gates.

## Rolling roster revision 5

The next three planning waves are T03–T05, rebased to 1–3. Scope for every row comes from approved #0375, #0376, and #0377, the ticket’s milestone demo, and the API contract. Each listed prerequisite must be Completed before dispatch. Terry retains control of acceptance. This roster grants no future execution authority.

| Wave | Baseline | Ticket | Planned author | Fresh reviewer | Prerequisites |
| ---: | --- | --- | --- | --- | --- |
| 1 | T03 | #0386 M03 | sol_medium_0386_author (gpt-6.1-sol / medium) | sol_high_0386_review (gpt-6.1-sol / high) | #0385 Terry acceptance pending |
| 1 | T03 | #0388 M05 | sol_medium_0388_author (gpt-6.1-sol / medium) | sol_high_0388_review (gpt-6.1-sol / high) | #0385 Terry acceptance pending |
| 1 | T03 | #0387 M04 | sol_high_0387_author (gpt-6.1-sol / high) | sol_high_0387_review (gpt-6.1-sol / high) | #0385 Terry acceptance pending |
| 1 | T03 | #0401 M18 | sol_medium_0401_author (gpt-6.1-sol / medium) | sol_high_0401_review (gpt-6.1-sol / high) | #0400 Terry acceptance pending |
| 1 | T03 | #0406 M23 | sol_high_0406_author (gpt-6.1-sol / high) | sol_high_0406_review (gpt-6.1-sol / high) | #0385 Terry acceptance pending |
| 2 | T04 | #0389 M06 | sol_medium_0389_author (gpt-6.1-sol / medium) | sol_high_0389_review (gpt-6.1-sol / high) | #0388 Backlog |
| 2 | T04 | #0397 M14 | sol_medium_0397_author (gpt-6.1-sol / medium) | sol_high_0397_review (gpt-6.1-sol / high) | #0386 Backlog, #0387 Backlog, #0388 Backlog |
| 2 | T04 | #0392 M09 | sol_medium_0392_author (gpt-6.1-sol / medium) | sol_high_0392_review (gpt-6.1-sol / high) | #0388 Backlog |
| 2 | T04 | #0407 M24 | sol_medium_0407_author (gpt-6.1-sol / medium) | sol_high_0407_review (gpt-6.1-sol / high) | #0406 Backlog |
| 2 | T04 | #0416 M33 | sol_high_0416_author (gpt-6.1-sol / high) | sol_high_0416_review (gpt-6.1-sol / high) | #0388 Backlog, #0415 Completed |
| 3 | T05 | #0390 M07 | sol_medium_0390_author (gpt-6.1-sol / medium) | sol_medium_0390_review (gpt-6.1-sol / medium) | #0386 Backlog, #0389 Backlog |
| 3 | T05 | #0398 M15 | sol_high_0398_author (gpt-6.1-sol / high) | sol_high_0398_review (gpt-6.1-sol / high) | #0397 Backlog |
| 3 | T05 | #0402 M19 | sol_medium_0402_author (gpt-6.1-sol / medium) | sol_high_0402_review (gpt-6.1-sol / high) | #0389 Backlog, #0401 Backlog |
| 3 | T05 | #0393 M10 | sol_medium_0393_author (gpt-6.1-sol / medium) | sol_high_0393_review (gpt-6.1-sol / high) | #0389 Backlog, #0391 Terry acceptance pending, #0392 Backlog |
| 3 | T05 | #0408 M25 | sol_medium_0408_author (gpt-6.1-sol / medium) | sol_high_0408_review (gpt-6.1-sol / high) | #0407 Backlog |

The primary owns shared integration, Windows checks, demo coordination, and publication. Each wave plans `sol_medium_browser_author` and `sol_medium_cli_author` (gpt-6.1-sol / medium) for its client changes, followed by fresh `sol_high_browser_review` and `sol_high_cli_review` (gpt-6.1-sol / high). Release finished authors before dispatching reviewers and honor the active capacity authorized for each future wave.

M17’s newly granted move demonstration awaits M03 (#0386). M26’s fuller archive and recency walkthrough awaits M06 (#0389). The browser application walkthrough still needs M04 (#0387).

## Rolling assignments revision 6

The remaining window is T04–T06, rebased to remaining turns 1–3. Each row takes scope from owner-accepted #0375, #0376, and #0377, the corresponding milestone acceptance, and the published OpenAPI contract. Terry’s T02 prerequisite override applied only to T03. Terry accepted #0385 during T03. #0400 and #0414 remained in Ready for Review at the last verification. No T04 execution is authorized. Before dispatch, check actual acceptance and move each eligible card In Progress.

| Remaining turn | Turn | Ticket | Author | Fresh reviewer | Prerequisite lanes at T03 handoff |
| ---: | --- | --- | --- | --- | --- |
| 1 | T04 | #0389 M06 | `sol_medium_0389_author` (gpt-6.1-sol / medium) | `sol_high_0389_review` (gpt-6.1-sol / high) | #0388 Ready for Review |
| 1 | T04 | #0397 M14 | `sol_medium_0397_author` (gpt-6.1-sol / medium) | `sol_high_0397_review` (gpt-6.1-sol / high) | #0386 Ready for Review, #0387 Ready for Review, #0388 Ready for Review |
| 1 | T04 | #0392 M09 | `sol_medium_0392_author` (gpt-6.1-sol / medium) | `sol_high_0392_review` (gpt-6.1-sol / high) | #0388 Ready for Review |
| 1 | T04 | #0407 M24 | `sol_medium_0407_author` (gpt-6.1-sol / medium) | `sol_high_0407_review` (gpt-6.1-sol / high) | #0406 Ready for Review |
| 1 | T04 | #0416 M33 | `sol_high_0416_author` (gpt-6.1-sol / high) | `sol_high_0416_review` (gpt-6.1-sol / high) | #0388 Ready for Review, #0415 Completed |
| 2 | T05 | #0390 M07 | `sol_medium_0390_author` (gpt-6.1-sol / medium) | `sol_medium_0390_review` (gpt-6.1-sol / medium) | #0386 Ready for Review, #0389 Backlog |
| 2 | T05 | #0398 M15 | `sol_high_0398_author` (gpt-6.1-sol / high) | `sol_high_0398_review` (gpt-6.1-sol / high) | #0397 Backlog |
| 2 | T05 | #0402 M19 | `sol_medium_0402_author` (gpt-6.1-sol / medium) | `sol_high_0402_review` (gpt-6.1-sol / high) | #0389 Backlog, #0401 Ready for Review |
| 2 | T05 | #0393 M10 | `sol_medium_0393_author` (gpt-6.1-sol / medium) | `sol_high_0393_review` (gpt-6.1-sol / high) | #0389 Backlog, #0391 Ready for Review, #0392 Backlog |
| 2 | T05 | #0408 M25 | `sol_medium_0408_author` (gpt-6.1-sol / medium) | `sol_high_0408_review` (gpt-6.1-sol / high) | #0407 Backlog |
| 3 | T06 | #0403 M20 | `sol_medium_0403_author` (gpt-6.1-sol / medium) | `sol_high_0403_review` (gpt-6.1-sol / high) | #0402 Backlog |
| 3 | T06 | #0394 M11 | `sol_medium_0394_author` (gpt-6.1-sol / medium) | `sol_high_0394_review` (gpt-6.1-sol / high) | #0393 Backlog |
| 3 | T06 | #0395 M12 | `sol_medium_0395_author` (gpt-6.1-sol / medium) | `sol_medium_0395_review` (gpt-6.1-sol / medium) | #0393 Backlog |
| 3 | T06 | #0404 M21 | `sol_medium_0404_author` (gpt-6.1-sol / medium) | `sol_medium_0404_review` (gpt-6.1-sol / medium) | #0393 Backlog |
| 3 | T06 | #0412 M29 | `sol_high_0412_author` (gpt-6.1-sol / high) | `sol_high_0412_review` (gpt-6.1-sol / high) | #0408 Backlog, #0411 Completed |

T04 and T05 plan five backend authors and the two client authors, with a peak of seven. T06 plans five backend authors, the M29 verification helper `sol_high_0412_checks` (gpt-6.1-sol / high), and the two client authors, with a planned peak of eight subject to the active session limit. `sol_medium_browser_author` (gpt-6.1-sol / medium) and `sol_medium_cli_author` (gpt-6.1-sol / medium) own the corresponding client work in each wave. Fresh client reviews use `sol_high_browser_review` (gpt-6.1-sol / high) and `sol_high_cli_review` (gpt-6.1-sol / high). Reviewers replace finished authors as slots open. The primary owns shared integration, final checks, publishing, and demonstration assembly.

T03 delivered #0386 M03, #0387 M04, #0388 M05, #0401 M18, and #0406 M23 to Ready for Review. Terry’s acceptance is the remaining gate for their downstream work. The M17 newly granted move and M04 production browser checks were exercised in T03. The fuller M26 archive and recency demonstration still depends on M06. Its owner acceptance remains recorded on #0409. The completed M08 parent demonstration remains in the T02 record.
