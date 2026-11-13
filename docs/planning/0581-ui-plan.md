# Epic #0581 clean-room UI plan

Terry approved the restart on 2026-11-13. The previous epic and its 33 stories
are archived. The old UI Lead chat is archived. V1 is the observable specification;
authors must not read or reuse its implementation. The fresh TypeScript UI uses
the V2 API. This plan passed independent review of scope, dependencies and staffing.

T1 and T2 reached Ready for Review. The Architect squash-merged PR #1 into
`ui/v1-clean-room` as `9f9ced3` and verified its tree against the reviewed
`84d73ad` head. Terry confirmed the integrated comparison and the Architect
opened T3. All nine T3 stories remain In Progress during qualification.
Later turn entry requires Architect approval and squash merge of all preceding-turn PRs into ui/v1-clean-room. Terry controls Completed, the final demo/project approval and UI-branch merge to main. See [PR workflow](0581-pr-workflow.md).

## Board mapping

| Turn | Reference | Ticket | Story | Estimate |
| --- | --- | --- | --- | --- |
| T1 | R01 | #0582 | Observed contract and shared interfaces | 2.5 h |
| T2 | R02 | #0583 | Fresh workspace entry | 2.5 h |
| T2 | R03 | #0584 | Fresh selected-board composition | 2.5 h |
| T3 | R04 | #0585 | Workspace navigation controls | 2.5 h |
| T3 | R05 | #0586 | Board filtering and completed visibility | 2 h |
| T3 | R06 | #0587 | Read-only card drawer | 2.5 h |
| T3 | R09 | #0588 | Create card dialog | 2.5 h |
| T3 | R11 | #0589 | Card movement | 2.5 h |
| T3 | R13 | #0590 | Lane approval | 2 h |
| T3 | R14 | #0591 | Dependency ordering | 1.5 h |
| T3 | R15 | #0592 | Batch priority | 1.5 h |
| T3 | R18 | #0593 | Live status and reconnect | 2.5 h |
| T4 | R07 | #0594 | Drawer field and lifecycle actions | 2.5 h |
| T4 | R08 | #0595 | Comment composition | 1.5 h |
| T4 | R10 | #0596 | Relationship editing | 2 h |
| T4 | R12 | #0597 | Single-card approval | 1.5 h |
| T4 | R16 | #0598 | Existing attachments | 2.5 h |
| T4 | R19 | #0599 | History navigation and cached reads | 2.5 h |
| T5 | R17 | #0600 | Attachment ingestion and recovery | 2.75 h |
| T6 | R20 | #0601 | Bounded integrated qualification | 2.5 h |

Detailed scope, dependencies and planned assignments follow. Ticket numbers are ascending by turn.


Current assignment revision 7 covers the next three turns, T3 through T5.
Earlier rosters and the archived #0514 evidence remain available. R01–R20 map
to the live tickets above. [Rolling roster](0581-rolling-assignments.md)

## Acceptance and provenance

- Reproduce running V1 appearance and behavior exactly with a fresh TypeScript presentation layer against V2. Match layout, density, spacing, controls, navigation, dialogs, drawers, keyboard/focus, feedback and recovery.
- Authors use behavioral specifications, screenshots and interaction records. Never read or reuse V1 source, HTML, CSS, scripts, templates or assets, including through developer tools. Screenshots are references only. Each author records consulted sources and reused code.
- Retain only existing V2 contracts and nonpresentation operations with documented V2 provenance. Use metadata and V2 records without examining V1 code. Freshly implement candidates whose provenance is unproven. Build fresh presentation modules and freshly constructed equivalent fixtures.
- The historical inventories guide observation. Classify browser, CLI-only and V2-only capabilities accurately. Preserve V2 API/CLI capabilities and intentionally review retained browser-control placement while keeping V1 entry screens exact.
- Compare running V1/V2 with equivalent disposable data, matching viewport, zoom and state. Each feature first confirms its remaining V1 observations, then supplies visual and interaction evidence. Terry explicitly approves any deliberate visible departure.

## Leadership and gates

- A fresh UI Lead owns implementation, integration, evidence and the single compiler/integration and browser-operator leases. The Architect independently reviews every turn and explicitly authorizes its cards for RFR before opening the next turn. Terry controls Completed and production cutover.
- Move eligible cards In Progress before any work or delegation and keep them there through independent review. Preserve strict TypeScript/UI release standards and run the required check script on integrated candidates.
- At the end of T2, compose R02/R03 and show Terry workspace and selected-board entry side by side, including navigation and scrolling. This is an integrated owner checkpoint, with no separate checkpoint ticket. Resolve findings within T2 before broad functionality begins.
- After R20 and any scoped blocking defects, the Architect demonstrates all inventoried V1 browser workflows in the replacement. This is the epic handoff gate, with no separate demo ticket. Cutover still needs Terry’s explicit approval.
- Use the healthy shared Valkey/Webdis queue for bounded real-browser diagnostics, safe fields and authoritative readback. Release browser control after each bounded action. Report queue failure immediately and pause queue-dependent verification.

## Interfaces and parallel execution

- R01 freezes minimal compile-ready TS interfaces/fixture stubs, component slots, route/state ownership and operation outcome semantics. It assigns module ownership and establishes V2 operation provenance before parallel UI authors start.
- R02 and R03 compile independently against those fixed interfaces. R02 owns workspace/root<br>R03 owns selected-board composition. The Lead alone composes them for T2. Feature authors add scoped adapters against the same contract, so create/comments/relationships do not depend on field-edit implementation.
- Use on-demand Sol / Medium authors and fresh Sol / Medium ordinary reviewers. Sol / High reviews recovery, concurrency and atomic operations. Terra / Medium assesses visual/interaction evidence every turn. Launch fresh reviewers as author slots free<br>reserve no idle pairs.
- Use disjoint ready modules in parallel. Serialize composition files, compiler/integration and browser work under the Lead. Turn numbers below are exact dependency-depth earliest opportunities<br>review gates, owner findings or shared-resource capacity can postpone them.

## Bounded stories

Each story includes focused automated checks, matched-state V1 visual/behavior evidence and fresh review. Each mutation owns its scoped stale/lost-response and inspect-before-retry checks. Hours estimate engineering work including focused checks and correction allowance, excluding owner/reviewer waiting.

| Ref | Earliest turn | Hours | Depends on | Demonstrable scope and acceptance |
| --- | --- | --- | --- | --- |
| R01 | 1 | 2.5 | — | **Observed contract and shared interfaces.** Observe V1 workspace and board entry, then map the historical browser inventory to a fresh screenshot/state ledger. Record matching fixture recipes and remaining feature-specific observations. Freeze minimal shared TS interfaces, typed fixture stubs, component slots, route/state ownership, operation outcome semantics and module ownership so R02/R03 compile independently. Architect validates this boundary. Identify reusable V2 operations with documented V2 provenance. No V1 source inspection. |
| R02 | 2 | 2.5 | R01 | **Fresh workspace entry.** Implement fresh TS root/workspace shell, board discovery/selection and read-only workspace status against the frozen interfaces. Match V1 populated, empty, loading and unavailable workspace appearance and behavior. Own workspace files only<br>integrate the separate board slot through its contract. |
| R03 | 2 | 2.5 | R01 | **Fresh selected-board composition.** Implement the independent board component against R01 fixture stubs and slots. Match V1 header, selected-board presentation, lane order/width, permission captions, card density, priority/ticket/title/comment indicators, scrolling and empty board. The Lead composes it with R02 for the T2 checkpoint. |
| R04 | 3 | 2.5 | R02 | **Workspace navigation controls.** Match V1 pin/unpin and pointer/keyboard pin order, board search, sidebar resize/collapse and current attention/recency. Verify persistence, exact placement, focus and feedback. Exercise a failed pin save with safe readback and preserved last-confirmed order. |
| R05 | 3 | 2.0 | R03 | **Board filtering and completed visibility.** Match V1 in-place title/ticket/content search, result counts and recent/older Completed toggle. Compare exact visible membership, empty results, clearing, focus and scrolling with matching data. |
| R06 | 3 | 2.5 | R03 | **Read-only card drawer.** Match V1 drawer opening/closing, sizing, focus return, identity/priority/approval/owner/prose, hierarchy navigation, comment preview and audit presentation. Demonstrate formatted prose, ticket links, archived linked work and long-content scrolling. Supply fixed slots for independent edit/comment/relationship/attachment modules. |
| R07 | 4 | 2.5 | R06 | **Drawer field and lifecycle actions.** Match V1 title/description, owner and individual priority editing plus archive/restore entry points. Verify exact text, allowed actions, cancellation, validation, audit and unchanged identity. Exercise stale/lost edit response using the R01 operation contract, preserved draft and authoritative inspection before any retry. |
| R08 | 4 | 1.5 | R06 | **Comment composition.** Match V1 comment placement, Enter submission, Shift+Enter newline, attribution and history update. Verify exact text and one committed comment. Exercise a lost response, retained unsent/submitted draft and inspection before retry using the fixed operation contract. |
| R09 | 3 | 2.5 | R03 | **Create card dialog.** Match V1 allowed-lane entry and create dialog fields, typed relationship rows, description Enter, validation, cancellation/focus and success navigation. Verify one card with exact fields/links after a lost create response. Reserve the specified attachment slot for R17<br>implement against R01 contracts without depending on R07. |
| R10 | 4 | 2.0 | R06 | **Relationship editing.** Match V1 add/change/remove/follow relationship controls, direction, hierarchy context and invalid choices. Verify endpoint/type/history, preserved drawer placement and safe stale/lost-response inspection. Use R01 operation interfaces independently of field editing. |
| R11 | 3 | 2.5 | R03 | **Card movement.** Match the V1 pointer drag-and-drop card movement, destination permissions and visible refusal feedback. Preserve focus and scrolling. Verify saved lane, revision and audit. Exercise stale refusal and lost-response readback without replay. Terry confirmed V1 has no keyboard move dropdown; do not introduce that control. |
| R12 | 4 | 1.5 | R06 | **Single-card approval.** Match V1 Human approval control and its keyboard/focus/feedback states. Verify Completed lane, fixed approval comment, Human attribution and receipt. Inject one lost approval response and prove one commit by receipt/state inspection before retry. |
| R13 | 3 | 2.0 | R03 | **Lane approval.** Match V1 lane approval control, configured text, confirmation and disabled/pending states. Verify exact membership and atomic approval, custom historical text, Human attribution and safe stale-membership/lost-response handling. Use the existing V2 atomic operation with established provenance. |
| R14 | 3 | 1.5 | R03 | **Dependency ordering.** Match V1 dependency-order control, opt-in/disabled state, preview and apply interaction. Verify affected membership, resulting ticket order and stable card identities. Demonstrate stale preview refusal and inspection of a lost apply response. |
| R15 | 3 | 1.5 | R03 | **Batch-priority CLI/API retention.** Verify the retained native CLI/API batch-priority capability. Terry confirmed V1 has no browser batch-priority control. The CLI card set-priorities uses PATCH /api/2026-11-10/cards with board_id, expected_board_etag and selection. Remove the provisional unwired browser control. Verify a disposable CLI operation and authoritative readback, effective dependency floor and stale-board refusal through the same V2 API. |
| R16 | 4 | 2.5 | R06 | **Existing attachments.** Match V1 attachment list, preview/download, cover select/clear, first-image fallback and detach. Verify bytes, identity, cover and detached history<br>include broken/unsupported preview. For a lost cover/detach response inspect state before retry. |
| R17 | 5 | 2.75 | R09, R16 | **Attachment ingestion and recovery.** Match V1 selection/paste/staging in existing-card and create flows, upload-after-create messaging, cancellation and per-file partial outcomes. Verify stored bytes and one attachment per committed upload. Demonstrate reload recovery, original-file reselection and ambiguous-commit inspection for one partial batch using existing V2 operations. |
| R18 | 3 | 2.5 | R02, R03 | **Live status and reconnect.** Match V1 connection/build/last-update indicators, Bot/Human attention/tab cues, background-board changes and reconnect feedback using the R01 state boundary. Demonstrate an external change while a contract-backed draft is open<br>preserve the draft and last-confirmed state. Verify authoritative state and successful reconnect. |
| R19 | 4 | 2.5 | R02, R03, R06 | **History navigation and cached reads.** Match V1 Back/Forward/reload restoration of board/search/drawer/scroll, last-confirmed cache, observed cache-budget/eviction controls and failed-refresh feedback. Use R01 shared state interfaces independently of R18 implementation. Demonstrate retained draft, stale read-only view and correct return to live state. |
| R20 | 6 | 2.5 | R04, R05, R07, R08, R09, R10, R11, R12, R13, R14, R15, R17, R18, R19 | **Bounded integrated qualification.** Run the prepared cross-feature parity matrix once on the integrated candidate with equivalent V1/V2 fixtures and viewports. Check workspace entry, one full mutable journey, cross-feature draft/reconnect recovery, keyboard/focus, observed responsive states, strict gates and optimized release assets. Reuse reviewed per-feature evidence and verify V2 API/CLI retention. Record the result and exact candidate. Create separately scoped defect cards for failures<br>do not include unlimited fixes in this estimate. Epic handoff remains closed until those defects are independently resolved. |

R20 is a bounded qualification run. Failures produce separately scoped defect cards and a revised plan<br>its estimate includes no unlimited close-all commitment. Focused recovery qualification belongs to each feature and is complete before integrated qualification.

## Current staffing

Revision 6 carries forward the approved coauthor scopes and records current
prerequisite readiness for T3 through T5. Authors hand completed packets to
fresh reviewers as slots become free. [Staffing plan](0581-staffing-plan.md)
and [rolling assignments](0581-rolling-assignments.md)

## Sources

- `docs/planning/0514-v1-drop-in-remit.md` preserves the owner’s exact visible-parity remit<br>epic #0581 replaces the prior execution path.
- `C:/Users/TDO-XPS15-2024/.codex/visualizations/2026/11/10/01a1c3a6-8477-7c33-88f6-e11ea8c6030a/output/0514-v1-ux-inventory.md` supplies historical visible observations.
- `C:/Users/TDO-XPS15-2024/.codex/visualizations/2026/11/10/01a1c3a6-8477-7c33-88f6-e11ea8c6030a/output/0501-web-audit.md` supplies the historical mixed-surface inventory. Its source links were not followed and do not belong in the author packet.
