# #0514 staffing plan r3

Independent review passed. Revision 0514-staff-r3 supersedes r2. The 33 stories, T1-T9 order, four two-author splits and named independent technical reviewers are retained. Reviewer names are planned assignments; their processes begin only when review work is ready.

**Dispatch on ready work**

Each turn starts its ready author packets and zero reviewers. Coauthors dispatch when their agreed interface and fixture are ready. Authors finish their packets, save the handoff and exit. The primary assembles a reviewable result, then launches its fresh technical reviewer in released capacity. A reviewer reports findings and exits; correction and re-review workers are started when their work is ready.

Keep Terra as the visual reviewer in every turn. Each Terra pass starts once a rendered candidate is ready, with browser control explicitly handed over. No current turn has a Terra lead, so all nine passes are scheduled. A Terra-led visual task may cover routine visual inspection while retaining independent non-author logic review.

The primary runs standard checks by default. A Luna / Light worker is optional when a ready prescribed check packet can usefully run in parallel with primary work. There is no automatic check-runner assignment.

**Sub-turn worker lifespan**

Every subagent owns one ready bounded packet and exits when it finishes. A named lead is an accountability assignment, not a reason to remain active while another packet runs. The primary joins completed packets or resumes the lead for a concrete integration step. Review findings trigger a correction packet and later re-review. Terra performs each ready visual pass and exits; later rendered changes can trigger another pass.

**Per-turn staffing**

| Turn | Author packets | Technical reviewers, later | Terra passes, later | Automatic check workers | Total planned | Planned peak active |
|---|---:|---:|---:|---:|---:|---:|
| T1 | 2 | 1 | 1 | 0 | 4 | 2 |
| T2 | 4 | 2 | 1 | 0 | 7 | 4 |
| T3 | 3 | 3 | 1 | 0 | 7 | 4 |
| T4 | 3 | 2 | 1 | 0 | 6 | 3 |
| T5 | 11 | 11 | 1 | 0 | 23 | 12 |
| T6 | 7 | 7 | 1 | 0 | 15 | 8 |
| T7 | 4 | 4 | 1 | 0 | 9 | 5 |
| T8 | 2 | 2 | 1 | 0 | 5 | 3 |
| T9 | 1 | 1 | 1 | 0 | 3 | 2 |
| Total | 37 | 33 | 9 | 0 | 79 | 12 maximum |

All counts exclude the primary. Reviewers replace finished authors, so the total assigned through a turn is larger than concurrent staffing. The peak allows useful overlap between technical review and a ready Terra pass. Optional verification or correction work consumes actual free capacity under the 16-direct-worker ceiling. No capacity is reserved for idle workers.

**Full technical roster**

| Turn | Story | Lead author | Coauthor | Fresh technical reviewer | Prerequisites |
|---|---|---|---|---|---|
| T1 | #548 Open the compiled TypeScript shell | Sol Medium 0548 Shell | Sol Medium 0548 Assets | Sol High 0548 Review | Epic kickoff |
| T2 | #549 Scan the familiar board | Sol Medium 0549 Board | Terra Medium 0549 Style | Sol Medium 0549 Review | #548 |
| T2 | #550 See live workspace attention | Sol Medium 0550 Attention | Sol Medium 0550 Render | Sol High 0550 Review | #548 |
| T3 | #551 Read a card in context | Sol Medium 0551 Drawer |  | Sol Medium 0551 Review | #549 |
| T3 | #552 Keep the selected board current safely | Sol Medium 0552 Live State |  | Sol High 0552 Review | #549, #550 |
| T3 | #553 Pin boards and adjust the sidebar | Sol Medium 0553 Pins |  | Sol High 0553 Review | #550; #0512 completed |
| T4 | #554 Return to a board with its view intact | Sol Medium 0554 Navigation |  | Sol High 0554 Review | #551, #552 |
| T4 | #555 Edit exact card text with recoverable drafts | Sol Medium 0555 Text Drafts | Sol Medium 0555 Editor | Sol High 0555 Review | #551, #552 |
| T5 | #556 Show confirmed recent boards within a cache budget | Sol Medium 0556 Cache |  | Sol High 0556 Review | #552, #554 |
| T5 | #557 Create a card from its lane | Sol Medium 0557 Create |  | Sol High 0557 Review | #549, #555 |
| T5 | #558 Move cards with pointer and keyboard | Sol Medium 0558 Move |  | Sol High 0558 Review | #555 |
| T5 | #559 Assign owners and individual priorities | Sol Medium 0559 Assignment |  | Sol High 0559 Review | #555 |
| T5 | #560 Read and post comments and activity | Sol Medium 0560 Comments |  | Sol High 0560 Review | #555 |
| T5 | #561 Follow and maintain typed relationships | Sol Medium 0561 Relationships |  | Sol High 0561 Review | #551, #555 |
| T5 | #562 Find and restore active or archived work | Sol Medium 0562 Search Archive |  | Sol High 0562 Review | #551, #554, #555 |
| T5 | #563 Preview and manage saved attachments | Sol Medium 0563 Attachment Read |  | Sol High 0563 Review | #551, #555 |
| T5 | #564 Configure and inspect automatic publishing | Sol Medium 0564 Publishing |  | Sol High 0564 Review | #550, #553, #555 |
| T5 | #565 Create, close and reopen boards | Sol Medium 0565 Board Lifecycle |  | Sol High 0565 Review | #552, #555 |
| T5 | #566 Inspect and stop the service | Sol Medium 0566 Service Status |  | Sol High 0566 Review | #552, #555 |
| T6 | #567 Seed typed links during creation | Sol Medium 0567 Seed Links |  | Sol High 0567 Review | #557, #561 |
| T6 | #568 Approve one reviewed card | Sol Medium 0568 Approve |  | Sol High 0568 Review | #555, #558, #560 |
| T6 | #569 Upload files to an existing card | Sol Medium 0569 Upload |  | Sol High 0569 Review | #555, #563 |
| T6 | #570 Set batch priorities | Sol Medium 0570 Batch Priority |  | Sol High 0570 Review | #555, #559, #562 |
| T6 | #571 Preview and apply dependency ticket order | Sol Medium 0571 Ticket Order |  | Sol High 0571 Review | #555, #562 |
| T6 | #572 Capture and download a native backup | Sol Medium 0572 Backup Capture |  | Sol High 0572 Review | #555, #563 |
| T6 | #573 Activate a staged build and reconnect | Sol Medium 0573 Activation |  | Sol High 0573 Review | #566 |
| T7 | #574 Approve a complete lane | Sol Medium 0574 Bulk Approve |  | Sol High 0574 Review | #568 |
| T7 | #575 Resume interrupted file batches | Sol Medium 0575 Upload Recovery |  | Sol High 0575 Review | #569 |
| T7 | #576 Restore a native backup | Sol Medium 0576 Restore |  | Sol High 0576 Review | #572 |
| T7 | #577 Publish a captured backup | Sol Medium 0577 Publish Backup |  | Sol High 0577 Review | #572, #564 |
| T8 | #578 Stage attachments during card creation | Sol Medium 0578 Create Files |  | Sol High 0578 Review | #557, #567, #575 |
| T8 | #579 Maintain lane configuration | Sol Medium 0579 Lane Settings |  | Sol High 0579 Review | #555, #574 |
| T9 | #580 Qualify the complete candidate for handoff | Sol High 0580 Qualification |  | Sol High 0580 Review | #556, #578, #579, #570, #571, #576, #577, #565, #573; #0512 completed |

**Visual roster**

| Turn | Terra reviewer | Starts when |
|---|---|---|
| T1 | Terra Medium T01 Visual | Rendered candidate ready; browser control available |
| T2 | Terra Medium T02 Visual | Rendered candidate ready; browser control available |
| T3 | Terra Medium T03 Visual | Rendered candidate ready; browser control available |
| T4 | Terra Medium T04 Visual | Rendered candidate ready; browser control available |
| T5 | Terra Medium T05 Visual | Rendered candidate ready; browser control available |
| T6 | Terra Medium T06 Visual | Rendered candidate ready; browser control available |
| T7 | Terra Medium T07 Visual | Rendered candidate ready; browser control available |
| T8 | Terra Medium T08 Visual | Rendered candidate ready; browser control available |
| T9 | Terra Medium T09 Visual | Rendered candidate ready; browser control available |

**Ownership and verification**

- The primary is Sol / High and owns shared integration, the compiler queue and final verification.
- Author packets retain their separate worktree branches and exclusive files listed below. Shared entrypoint, types, asset and package changes are integrated serially by the primary.
- One Go build/vet/test job runs at a time. Independent Node checks may use two slots. One operator controls the browser.
- Before story work starts, verify that its prerequisites meet the owner execution gate and move the story to In Progress.
- Independent source review, required combined checks, browser verification and primary verification precede RFR. Technical reviewers assess the combined result. Sol / High remains assigned to authentication, concurrency and recovery review. The Terra author on #0549 is separate from its final visual reviewer.
- Cards enter RFR when technical work and review finish. Owner demos run at the turn boundary.
- An early reviewer may start for a stable completed packet when that bounded review unblocks current work. The reviewer exits after that task; no slot is reserved ahead of time.

**Author packet boundaries**

| Story | Worker | Worktree | Files |
|---|---|---|---|
| #548 | Sol Medium 0548 Shell | codex/0514-0548-shell | web/src/main.ts; web/src/model.ts; web/index.html |
| #548 | Sol Medium 0548 Assets | codex/0514-0548-assets | web/package.json; package lock if required; web/tsconfig.json; web/assets.go; internal/v2/browser.go |
| #549 | Sol Medium 0549 Board | codex/0514-0549-board | web/src/board-view.ts |
| #549 | Terra Medium 0549 Style | codex/0514-0549-style | web/src/board.css; web/src/tokens.css |
| #550 | Sol Medium 0550 Attention | codex/0514-0550-attention | web/src/workspace-state.ts |
| #550 | Sol Medium 0550 Render | codex/0514-0550-render | web/src/attention.ts; web/src/attention.css |
| #551 | Sol Medium 0551 Drawer | codex/0514-0551-drawer | web/src/card-view.ts; web/src/drawer.css |
| #552 | Sol Medium 0552 Live State | codex/0514-0552-live-state | web/src/board-state.ts |
| #553 | Sol Medium 0553 Pins | codex/0514-0553-pins | web/src/attention.ts; web/src/sidebar-state.ts; web/src/attention.css |
| #554 | Sol Medium 0554 Navigation | codex/0514-0554-navigation | web/src/view-state.ts |
| #555 | Sol Medium 0555 Text Drafts | codex/0514-0555-text-drafts | web/src/operations.ts; web/src/drafts.ts |
| #555 | Sol Medium 0555 Editor | codex/0514-0555-editor | web/src/card-editor.ts |
| #556 | Sol Medium 0556 Cache | codex/0514-0556-cache | web/src/recent-boards.ts; web/src/workspace-settings.ts |
| #557 | Sol Medium 0557 Create | codex/0514-0557-create | web/src/create-card.ts; web/src/create-card.css |
| #558 | Sol Medium 0558 Move | codex/0514-0558-move | web/src/card-movement.ts; web/src/board-view.ts |
| #559 | Sol Medium 0559 Assignment | codex/0514-0559-assignment | web/src/card-assignment.ts |
| #560 | Sol Medium 0560 Comments | codex/0514-0560-comments | web/src/comments.ts; web/src/history.ts |
| #561 | Sol Medium 0561 Relationships | codex/0514-0561-relationships | web/src/relationships.ts |
| #562 | Sol Medium 0562 Search Archive | codex/0514-0562-search-archive | web/src/search.ts; web/src/archive.ts; web/src/view-state.ts |
| #563 | Sol Medium 0563 Attachment Read | codex/0514-0563-attachment-read | web/src/attachments.ts |
| #564 | Sol Medium 0564 Publishing | codex/0514-0564-publishing | web/src/publishing-settings.ts |
| #565 | Sol Medium 0565 Board Lifecycle | codex/0514-0565-board-lifecycle | web/src/board-lifecycle.ts; web/src/workspace-create.ts |
| #566 | Sol Medium 0566 Service Status | codex/0514-0566-service-status | web/src/service-tools.ts |
| #567 | Sol Medium 0567 Seed Links | codex/0514-0567-seed-links | web/src/create-card.ts; web/src/relationships.ts |
| #568 | Sol Medium 0568 Approve | codex/0514-0568-approve | web/src/card-approval.ts |
| #569 | Sol Medium 0569 Upload | codex/0514-0569-upload | web/src/uploads.ts |
| #570 | Sol Medium 0570 Batch Priority | codex/0514-0570-batch-priority | web/src/batch-priority.ts |
| #571 | Sol Medium 0571 Ticket Order | codex/0514-0571-ticket-order | web/src/ticket-order.ts |
| #572 | Sol Medium 0572 Backup Capture | codex/0514-0572-backup-capture | web/src/backup.ts |
| #573 | Sol Medium 0573 Activation | codex/0514-0573-activation | web/src/service-tools.ts; web/src/session.ts |
| #574 | Sol Medium 0574 Bulk Approve | codex/0514-0574-bulk-approve | web/src/lane-approval.ts |
| #575 | Sol Medium 0575 Upload Recovery | codex/0514-0575-upload-recovery | web/src/uploads.ts; web/src/upload-recovery.ts |
| #576 | Sol Medium 0576 Restore | codex/0514-0576-restore | web/src/restore.ts |
| #577 | Sol Medium 0577 Publish Backup | codex/0514-0577-publish-backup | web/src/backup-publication.ts |
| #578 | Sol Medium 0578 Create Files | codex/0514-0578-create-files | web/src/create-card.ts; web/src/uploads.ts |
| #579 | Sol Medium 0579 Lane Settings | codex/0514-0579-lane-settings | web/src/lane-settings.ts |
| #580 | Sol High 0580 Qualification | codex/0514-0580-qualification | web tests and candidate verification report; web/assets.go; web/index.html; replacement capability ledger |

**Four useful two-author splits**

**#548**
Agree entry module, generated output paths, exported mount shape, HTML anchors, asset URLs and CSP before parallel edits. Shell lead owns index.html; assets author supplies a bounded integration patch.
Lead publishes entry/output paths, exported mount signature and minimal typed entry fixture; assets author can verify embedding with the fixture before final shell merge.

**#549**
Agree class/attribute names, card slots, scroll ownership, focus states and supported widths. CSS cannot filter or reorder records. Final visual reviewer is a different Terra agent.
Lead publishes structural lane/card DOM fixture and class contract with long/empty/dense examples; CSS author works against that fixture.

**#550**
Agree immutable catalog/view snapshot, explicit observation time, callback intents and disposal shape. State lead alone owns subscriptions, timers, capabilities and retry decisions.
Lead publishes immutable catalog/view-model type plus zero/active/unavailable fixtures and navigation intent callbacks; render author uses a test stub.

**#555**
Agree resource keys, exact submitted text, operation-state union and submit/inspect/dismiss callbacks. Core is the sole persistence and recovery authority; editor never implements a second retry path.
Lead publishes operation-state union, submit/inspect/dismiss interface and exact-text vectors; editor author uses a test-only fake operation controller.

**Current rolling three-turn window**

- T1 starts both #0548 authors. Once both exit and the candidate is assembled, start Sol High 0548 Review and the ready Terra visual pass.
- T2 starts the two #0549 authors and two #0550 authors after #0548 acceptance. Launch each story reviewer after its author packets finish; launch Terra after the rendered combined candidate is ready.
- T3 starts the three single authors when their linked prerequisites are accepted. #0553 also uses the already-Completed #0512 contrast requirement. Spawn reviewers only for finished stories, then the ready Terra visual pass.
- Rebase this window after each execution handoff, retaining unfinished work and actual prerequisite status.

**Savings model and consultation**

The four two-author splits retain the r2 provisional target of 45-85 minutes saved across T1-T9 after estimated extra coordination. T2 savings are counted once across both stories. The model remains unmeasured and includes a zero-gain sensitivity case.

Original story sizing totals 69.5 hours. The original author-only turn barrier was 21.5 hours, while the weighted dependency path was 20.5 hours. The nine-turn graph depth is a minimum, not a promised elapsed duration. Actual author, merge, review, resource-wait and owner-wait times are recorded separately.

This dispatch revision removes nine automatic check-runner assignments, reducing total planned agents from 88 to 79. It preserves all nine Terra passes and all 33 technical reviews. No runtime saving is claimed merely from reducing that headcount.

FGA PM’s r67 plan remains advisory input. The current roster retains rolling review, isolated worktrees and one integration owner; local execution decisions remain with Terry and this primary.
