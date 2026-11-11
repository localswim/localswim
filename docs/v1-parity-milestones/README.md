# Post-T10 V1 parity milestones

Terry and Bot can finish every V1 task in V2 once these milestones pass. Keep one loopback service, the dated REST API, and fresh V2 data. Use the simplest V2 workflow that achieves the same outcome. V1 data migration, legacy syntax, legacy watchers and per-board service processes remain outside the plan.

The 62 estimates total **119h 10m of development and focused verification across 9 workstreams**. The longest dependency path is 12h 10m with independent workers. That is a lower bound on elapsed development time. Staffing, overlapping code ownership, review, demos and the [final walkthrough](qualification.md) add time. [T01–T10 plan](../../milestone-plan.md) · [Post-T10 audit](../v1-parity-gaps.md)

## Parallel order

Each row shows the first dependency turn a milestone can start. Milestones in the same cell can run in parallel when they have separate owners and change surfaces. Use one integration owner for shared browser wiring, store transactions and OpenAPI changes. Integrate shared changes before dependent edits. Take as many eligible milestones as staffing allows.

| Turn | Workspace | Card UI | Files | Workflow | Bot CLI | Activity | Recovery | Publication | Operations |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| T11 | [M40](m40.md), [M41](m41.md), [M45](m45.md) | [M47](m47.md), [M49](m49.md), [M50](m50.md), [M51](m51.md) | [M58](m58.md), [M62](m62.md) | [M63](m63.md), [M64](m64.md), [M65](m65.md), [M100](m100.md) | [M67](m67.md), [M68](m68.md), [M69](m69.md), [M70](m70.md), [M74](m74.md), [M76](m76.md) | [M77](m77.md) | [M82](m82.md), [M96](m96.md) | [M88](m88.md) | [M92](m92.md) |
| T12 | [M42](m42.md), [M43](m43.md), [M44](m44.md), [M46](m46.md) | [M48](m48.md), [M52](m52.md), [M53](m53.md), [M56](m56.md) | [M59](m59.md), [M61](m61.md) | [M66](m66.md) | [M71](m71.md), [M72](m72.md), [M75](m75.md), [M98](m98.md) | [M78](m78.md), [M79](m79.md) | [M83](m83.md) | [M89](m89.md), [M91](m91.md) | — |
| T13 | [M87](m87.md) | [M54](m54.md), [M55](m55.md), [M57](m57.md) | [M60](m60.md) | — | [M73](m73.md) | [M80](m80.md), [M81](m81.md), [M101](m101.md) | [M95](m95.md) | [M97](m97.md) | — |
| T14 | — | — | — | — | [M99](m99.md) | — | [M84](m84.md), [M86](m86.md) | [M90](m90.md) | [M94](m94.md) |
| T15 | — | — | — | — | — | — | [M85](m85.md) | — | [M93](m93.md) |

Demo completed milestones in Ready for Review at the end of a turn and at each three-hour build break. Use the milestones ready at that break. Move finished, independently reviewed work to Ready for Review as soon as its checks pass. A pending demo does not hold the card in In Progress. Split work that outgrows its estimate at a usable outcome with a separate demo.

## Milestone details

| Milestone | Workstream | Estimate | Starts after | Demo outcome |
| --- | --- | --- | --- | --- |
| [M40](m40.md) | Workspace | 1h 45m | T10 | See Attention across projects |
| [M41](m41.md) | Workspace | 2h 15m | T10 | Watch the open board update |
| [M42](m42.md) | Workspace | 2h | [M40](m40.md), [M41](m41.md) | Watch workspace Attention update |
| [M43](m43.md) | Workspace | 1h 45m | [M40](m40.md) | Organize pins from the sidebar |
| [M44](m44.md) | Workspace | 1h 15m | [M40](m40.md) | Resize and collapse the sidebar |
| [M45](m45.md) | Workspace | 1h 45m | T10 | Resume a board view |
| [M46](m46.md) | Workspace | 2h 15m | [M41](m41.md), [M45](m45.md) | Reopen a recent board immediately |
| [M47](m47.md) | Card UI | 2h | T10 | Start and move work where permitted |
| [M48](m48.md) | Card UI | 1h 45m | [M47](m47.md) | Drag a card to an allowed lane |
| [M49](m49.md) | Card UI | 1h 15m | T10 | Scan cards and lane totals |
| [M50](m50.md) | Card UI | 1h | T10 | Set one card's priority directly |
| [M51](m51.md) | Card UI | 2h 25m | T10 | Navigate and seed related work |
| [M52](m52.md) | Card UI | 1h 45m | [M45](m45.md), [M49](m49.md) | Filter work without leaving the board |
| [M53](m53.md) | Card UI | 1h 15m | [M49](m49.md) | Review recent or older Completed work |
| [M54](m54.md) | Card UI | 2h 15m | [M52](m52.md) | Read and find formatted card prose |
| [M55](m55.md) | Card UI | 1h 30m | [M52](m52.md), [M63](m63.md), [M64](m64.md), [M65](m65.md) | Edit and review by keyboard |
| [M56](m56.md) | Card UI | 2h 15m | [M45](m45.md) | Recover drafts and submitted actions |
| [M57](m57.md) | Card UI | 2h 30m | [M41](m41.md), [M48](m48.md), [M56](m56.md), [M59](m59.md) | See slow edits while they settle |
| [M58](m58.md) | Files | 2h 15m | T10 | Paste and upload several files to a card |
| [M59](m59.md) | Files | 1h 45m | [M58](m58.md) | Create a card with files |
| [M60](m60.md) | Files | 2h 15m | [M56](m56.md), [M59](m59.md) | Resume a partial upload safely |
| [M61](m61.md) | Files | 1h | [M49](m49.md) | See an image preview on the card |
| [M62](m62.md) | Files | 1h 30m | T10 | Keep working while a backup runs |
| [M63](m63.md) | Workflow | 1h 45m | T10 | Opt a lane into dependency reorder |
| [M64](m64.md) | Workflow | 2h 5m | T10 | See lane notes and board actor context |
| [M65](m65.md) | Workflow | 2h 35m | T10 | Approve a configured lane as Terry |
| [M66](m66.md) | Workflow | 1h 45m | [M63](m63.md), [M65](m65.md) | Start a board with Terry's workflow |
| [M67](m67.md) | Bot CLI | 2h | T10 | Pick next work across lanes |
| [M68](m68.md) | Bot CLI | 2h | T10 | Inspect a ticket and its context safely |
| [M69](m69.md) | Bot CLI | 1h 15m | T10 | Catch up on recent comments by ticket |
| [M70](m70.md) | Bot CLI | 2h | T10 | Act on a card by ticket |
| [M71](m71.md) | Bot CLI | 2h | [M70](m70.md) | Manage hierarchy and links by ticket |
| [M72](m72.md) | Bot CLI | 2h 15m | [M70](m70.md) | Run routine writes without composing JSON |
| [M73](m73.md) | Bot CLI | 1h 45m | [M66](m66.md), [M72](m72.md) | Provision and adjust workflows from the CLI |
| [M74](m74.md) | Bot CLI | 2h | T10 | Inspect and export saved work with the writer down |
| [M75](m75.md) | Bot CLI | 1h 45m | [M67](m67.md), [M68](m68.md), [M69](m69.md), [M74](m74.md) | Triage and catch up offline |
| [M76](m76.md) | Bot CLI | 45m | T10 | Fail automation on failed board verification |
| [M77](m77.md) | Activity | 1h 30m | T10 | Inspect activity without leaking card prose |
| [M78](m78.md) | Activity | 2h 35m | [M77](m77.md) | Verify what an owner notice covers |
| [M79](m79.md) | Activity | 1h 45m | [M77](m77.md) | Explicitly route notices to the owning chat |
| [M80](m80.md) | Activity | 2h 15m | [M79](m79.md) | Restore without rolling back notice authorization |
| [M81](m81.md) | Activity | 1h 45m | [M78](m78.md), [M79](m79.md) | Recognize approval and lane changes in notices |
| [M82](m82.md) | Recovery | 2h 25m | T10 | Protect a board whose saved data changed |
| [M83](m83.md) | Recovery | 2h 15m | [M82](m82.md) | Start with one damaged board and keep working |
| [M84](m84.md) | Recovery | 2h 35m | [M80](m80.md), [M83](m83.md), [M95](m95.md) | Recover a board that cannot load |
| [M85](m85.md) | Recovery | 2h 30m | [M82](m82.md), [M84](m84.md) | Contain an uncertain commit to its actual scope |
| [M86](m86.md) | Recovery | 2h 5m | [M80](m80.md), [M96](m96.md) | Restore a downloaded attachment-heavy backup |
| [M87](m87.md) | Workspace | 1h 45m | [M42](m42.md), [M82](m82.md), [M83](m83.md) | See durable health changes from the open workspace |
| [M88](m88.md) | Publication | 1h 45m | T10 | Keep ignored boards out of Git |
| [M89](m89.md) | Publication | 2h 25m | [M88](m88.md) | Publish to the intended Git destination |
| [M90](m90.md) | Publication | 2h 15m | [M97](m97.md) | Recover cleanly from a stalled publication |
| [M91](m91.md) | Publication | 2h | [M88](m88.md) | Publish without disturbing staged work |
| [M92](m92.md) | Operations | 2h | T10 | Start V2 independently on Windows |
| [M93](m93.md) | Operations | 1h 45m | [M84](m84.md), [M92](m92.md) | Reconnect after a Windows crash or upgrade |
| [M94](m94.md) | Operations | 2h 35m | [M46](m46.md), [M57](m57.md), [M60](m60.md), [M62](m62.md), [M87](m87.md) | Keep busy workspaces responsive |
| [M95](m95.md) | Recovery | 2h 25m | [M83](m83.md) | Identify a damaged board and its recovery point |
| [M96](m96.md) | Recovery | 2h 15m | T10 | Validate a large portable backup locally |
| [M97](m97.md) | Publication | 2h 25m | [M89](m89.md) | Retry a pending push to its original destination |
| [M98](m98.md) | Bot CLI | 1h 45m | [M70](m70.md) | Work with files and batches using ticket references |
| [M99](m99.md) | Bot CLI | 1h 30m | [M72](m72.md), [M73](m73.md), [M98](m98.md) | Discover commands from their own help |
| [M100](m100.md) | Workflow | 1h 30m | T10 | Apply board-specific browser styling |
| [M101](m101.md) | Activity | 1h 35m | [M74](m74.md), [M78](m78.md) | Inspect saved activity with the writer down |

## Audit coverage

| Audit section | Milestones that close it |
| --- | --- |
| [Live Attention workspace](../v1-parity-gaps.md#1-live-attention-workspace) | [M40](m40.md), [M41](m41.md), [M42](m42.md), [M43](m43.md), [M44](m44.md), [M45](m45.md), [M46](m46.md), [M87](m87.md), [M94](m94.md) |
| [Daily board and drawer work](../v1-parity-gaps.md#2-daily-board-and-drawer-work) | [M47](m47.md), [M48](m48.md), [M49](m49.md), [M50](m50.md), [M51](m51.md), [M52](m52.md), [M53](m53.md), [M54](m54.md), [M55](m55.md), [M56](m56.md), [M57](m57.md), [M58](m58.md), [M59](m59.md), [M60](m60.md), [M61](m61.md), [M62](m62.md) |
| [Workflow setup and approval controls](../v1-parity-gaps.md#3-workflow-setup-and-approval-controls) | [M63](m63.md), [M64](m64.md), [M65](m65.md), [M66](m66.md), [M73](m73.md), [M100](m100.md) |
| [Bot inspection and coordination](../v1-parity-gaps.md#4-bot-inspection-and-coordination) | [M67](m67.md), [M68](m68.md), [M69](m69.md), [M70](m70.md), [M71](m71.md), [M72](m72.md), [M73](m73.md), [M74](m74.md), [M75](m75.md), [M76](m76.md), [M77](m77.md), [M78](m78.md), [M79](m79.md), [M80](m80.md), [M81](m81.md), [M98](m98.md), [M99](m99.md), [M101](m101.md) |
| [Recovery, isolation and publication](../v1-parity-gaps.md#5-recovery-isolation-and-publication) | [M62](m62.md), [M80](m80.md), [M82](m82.md), [M83](m83.md), [M84](m84.md), [M85](m85.md), [M86](m86.md), [M87](m87.md), [M88](m88.md), [M89](m89.md), [M90](m90.md), [M91](m91.md), [M94](m94.md), [M95](m95.md), [M96](m96.md), [M97](m97.md) |
| [Cutover boundary](../v1-parity-gaps.md#6-cutover-boundary) | [M92](m92.md), [M93](m93.md), [M94](m94.md) and the [side-by-side walkthrough](qualification.md) |

Use disposable boards and remotes for demos. Use a test sink for notices or an authorized test recipient. Show CLI help before running demo commands and include full structured responses. Use the shared browser diagnostics workflow for real-browser checks. Keep opt-in agent notices separate from passive browser attention cues.

Update affected help, documentation and OpenAPI with implementation. Preserve snake_case output, UTF-8 text, approved UTC timestamps and revision guards. Reuse existing service operations and keep the native read-only CLI path on the same validation and report logic.
