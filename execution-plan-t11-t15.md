# Localswim V2 execution plan · T11–T15

Schedule all 62 post-T10 parity milestones, M040 through M101, in their earliest dependency turn. Milestones with no post-T10 prerequisites start in T11. Every other milestone starts one turn after its latest upstream milestone. Estimates cover development and focused verification. [Milestone details](docs/v1-parity-milestones/README.md) · [V1 parity audit](docs/v1-parity-gaps.md) · [T01–T10 execution plan](execution-plan.md)

| Turn | Milestones | Total estimated effort |
| --- | ---: | ---: |
| [T11](#t11) | 24 | 44h |
| [T12](#t12) | 20 | 37h 30m |
| [T13](#t13) | 11 | 22h 25m |
| [T14](#t14) | 5 | 11h |
| [T15](#t15) | 2 | 4h 15m |
| **Total** | **62** | **119h 10m** |

The graph takes **five turns**. The two longest upstream branches are `M077 → M079 → M080 → M084` and `M082 → M083 → M095 → M084`. Both reach M084 in T14, followed by M085 and M093 in T15. The table shows dependency eligibility. Actual concurrency depends on staffing and shared code ownership.

Demo ready milestones at the end of each turn and at each three-hour build break. Move checked and independently reviewed work to Ready for Review when it finishes. Run the [side-by-side parity walkthrough](docs/v1-parity-milestones/qualification.md) after the milestone checks pass.

[Detailed agent assignments and timing](agent-assignment-plan-t11-t15.md)

## T11

| Milestone | Outcome | Workstream | Estimate | Upstream |
| --- | --- | --- | ---: | --- |
| [M040](docs/v1-parity-milestones/m40.md) | See Attention across projects | Workspace | 1h 45m | — |
| [M041](docs/v1-parity-milestones/m41.md) | Watch the open board update | Workspace | 2h 15m | — |
| [M045](docs/v1-parity-milestones/m45.md) | Resume a board view | Workspace | 1h 45m | — |
| [M047](docs/v1-parity-milestones/m47.md) | Start and move work where permitted | Card UI | 2h | — |
| [M049](docs/v1-parity-milestones/m49.md) | Scan cards and lane totals | Card UI | 1h 15m | — |
| [M050](docs/v1-parity-milestones/m50.md) | Set one card's priority directly | Card UI | 1h | — |
| [M051](docs/v1-parity-milestones/m51.md) | Navigate and seed related work | Card UI | 2h 25m | — |
| [M058](docs/v1-parity-milestones/m58.md) | Paste and upload several files to a card | Files | 2h 15m | — |
| [M062](docs/v1-parity-milestones/m62.md) | Keep working while a backup runs | Files | 1h 30m | — |
| [M063](docs/v1-parity-milestones/m63.md) | Opt a lane into dependency reorder | Workflow | 1h 45m | — |
| [M064](docs/v1-parity-milestones/m64.md) | See lane notes and board actor context | Workflow | 2h 5m | — |
| [M065](docs/v1-parity-milestones/m65.md) | Approve a configured lane as Terry | Workflow | 2h 35m | — |
| [M067](docs/v1-parity-milestones/m67.md) | Pick next work across lanes | Bot CLI | 2h | — |
| [M068](docs/v1-parity-milestones/m68.md) | Inspect a ticket and its context safely | Bot CLI | 2h | — |
| [M069](docs/v1-parity-milestones/m69.md) | Catch up on recent comments by ticket | Bot CLI | 1h 15m | — |
| [M070](docs/v1-parity-milestones/m70.md) | Act on a card by ticket | Bot CLI | 2h | — |
| [M074](docs/v1-parity-milestones/m74.md) | Inspect and export saved work with the writer down | Bot CLI | 2h | — |
| [M076](docs/v1-parity-milestones/m76.md) | Fail automation on failed board verification | Bot CLI | 45m | — |
| [M077](docs/v1-parity-milestones/m77.md) | Inspect activity without leaking card prose | Activity | 1h 30m | — |
| [M082](docs/v1-parity-milestones/m82.md) | Protect a board whose saved data changed | Recovery | 2h 25m | — |
| [M088](docs/v1-parity-milestones/m88.md) | Keep ignored boards out of Git | Publication | 1h 45m | — |
| [M092](docs/v1-parity-milestones/m92.md) | Start V2 independently on Windows | Operations | 2h | — |
| [M096](docs/v1-parity-milestones/m96.md) | Validate a large portable backup locally | Recovery | 2h 15m | — |
| [M100](docs/v1-parity-milestones/m100.md) | Apply board-specific browser styling | Workflow | 1h 30m | — |

## T12

| Milestone | Outcome | Workstream | Estimate | Upstream |
| --- | --- | --- | ---: | --- |
| [M042](docs/v1-parity-milestones/m42.md) | Watch workspace Attention update | Workspace | 2h | M040, M041 |
| [M043](docs/v1-parity-milestones/m43.md) | Organize pins from the sidebar | Workspace | 1h 45m | M040 |
| [M044](docs/v1-parity-milestones/m44.md) | Resize and collapse the sidebar | Workspace | 1h 15m | M040 |
| [M046](docs/v1-parity-milestones/m46.md) | Reopen a recent board immediately | Workspace | 2h 15m | M041, M045 |
| [M048](docs/v1-parity-milestones/m48.md) | Drag a card to an allowed lane | Card UI | 1h 45m | M047 |
| [M052](docs/v1-parity-milestones/m52.md) | Filter work without leaving the board | Card UI | 1h 45m | M045, M049 |
| [M053](docs/v1-parity-milestones/m53.md) | Review recent or older Completed work | Card UI | 1h 15m | M049 |
| [M056](docs/v1-parity-milestones/m56.md) | Recover drafts and submitted actions | Card UI | 2h 15m | M045 |
| [M059](docs/v1-parity-milestones/m59.md) | Create a card with files | Files | 1h 45m | M058 |
| [M061](docs/v1-parity-milestones/m61.md) | See an image preview on the card | Files | 1h | M049 |
| [M066](docs/v1-parity-milestones/m66.md) | Start a board with Terry's workflow | Workflow | 1h 45m | M063, M065 |
| [M071](docs/v1-parity-milestones/m71.md) | Manage hierarchy and links by ticket | Bot CLI | 2h | M070 |
| [M072](docs/v1-parity-milestones/m72.md) | Run routine writes without composing JSON | Bot CLI | 2h 15m | M070 |
| [M075](docs/v1-parity-milestones/m75.md) | Triage and catch up offline | Bot CLI | 1h 45m | M067, M068, M069, M074 |
| [M078](docs/v1-parity-milestones/m78.md) | Verify what an owner notice covers | Activity | 2h 35m | M077 |
| [M079](docs/v1-parity-milestones/m79.md) | Explicitly route notices to the owning chat | Activity | 1h 45m | M077 |
| [M083](docs/v1-parity-milestones/m83.md) | Start with one damaged board and keep working | Recovery | 2h 15m | M082 |
| [M089](docs/v1-parity-milestones/m89.md) | Publish to the intended Git destination | Publication | 2h 25m | M088 |
| [M091](docs/v1-parity-milestones/m91.md) | Publish without disturbing staged work | Publication | 2h | M088 |
| [M098](docs/v1-parity-milestones/m98.md) | Work with files and batches using ticket references | Bot CLI | 1h 45m | M070 |

## T13

| Milestone | Outcome | Workstream | Estimate | Upstream |
| --- | --- | --- | ---: | --- |
| [M054](docs/v1-parity-milestones/m54.md) | Read and find formatted card prose | Card UI | 2h 15m | M052 |
| [M055](docs/v1-parity-milestones/m55.md) | Edit and review by keyboard | Card UI | 1h 30m | M052, M063, M064, M065 |
| [M057](docs/v1-parity-milestones/m57.md) | See slow edits while they settle | Card UI | 2h 30m | M041, M048, M056, M059 |
| [M060](docs/v1-parity-milestones/m60.md) | Resume a partial upload safely | Files | 2h 15m | M056, M059 |
| [M073](docs/v1-parity-milestones/m73.md) | Provision and adjust workflows from the CLI | Bot CLI | 1h 45m | M066, M072 |
| [M080](docs/v1-parity-milestones/m80.md) | Restore without rolling back notice authorization | Activity | 2h 15m | M079 |
| [M081](docs/v1-parity-milestones/m81.md) | Recognize approval and lane changes in notices | Activity | 1h 45m | M078, M079 |
| [M087](docs/v1-parity-milestones/m87.md) | See durable health changes from the open workspace | Workspace | 1h 45m | M042, M082, M083 |
| [M095](docs/v1-parity-milestones/m95.md) | Identify a damaged board and its recovery point | Recovery | 2h 25m | M083 |
| [M097](docs/v1-parity-milestones/m97.md) | Retry a pending push to its original destination | Publication | 2h 25m | M089 |
| [M101](docs/v1-parity-milestones/m101.md) | Inspect saved activity with the writer down | Activity | 1h 35m | M074, M078 |

## T14

| Milestone | Outcome | Workstream | Estimate | Upstream |
| --- | --- | --- | ---: | --- |
| [M084](docs/v1-parity-milestones/m84.md) | Recover a board that cannot load | Recovery | 2h 35m | M080, M083, M095 |
| [M086](docs/v1-parity-milestones/m86.md) | Restore a downloaded attachment-heavy backup | Recovery | 2h 5m | M080, M096 |
| [M090](docs/v1-parity-milestones/m90.md) | Recover cleanly from a stalled publication | Publication | 2h 15m | M097 |
| [M094](docs/v1-parity-milestones/m94.md) | Keep busy workspaces responsive | Operations | 2h 35m | M046, M057, M060, M062, M087 |
| [M099](docs/v1-parity-milestones/m99.md) | Discover commands from their own help | Bot CLI | 1h 30m | M072, M073, M098 |

## T15

| Milestone | Outcome | Workstream | Estimate | Upstream |
| --- | --- | --- | ---: | --- |
| [M085](docs/v1-parity-milestones/m85.md) | Contain an uncertain commit to its actual scope | Recovery | 2h 30m | M082, M084 |
| [M093](docs/v1-parity-milestones/m93.md) | Reconnect after a Windows crash or upgrade | Operations | 1h 45m | M084, M092 |
