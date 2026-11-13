# #0514 full staffing plan

Independently reviewed and approved for planning.

Revision 0514-staff-r1 covers the 33 active stories #0548–#0580. All story titles begin with T1–T9 and all are in Ready For Work. The earliest dependency schedule remains nine turns.

**Staffing and ownership**

- The primary is Sol / High and owns integration, the compiler queue, release evidence and final verification.
- Each story has a named author and a fresh independent reviewer below. Authors start first; reviewers join as their inputs become ready. All assignments use Standard speed and depth one.
- Peak planned load is 11 authors plus a Terra visual reviewer and a Luna check runner, 13 direct workers and the primary. The hard ceiling remains 16 direct workers. Correction work uses remaining capacity without reserving idle reviewer pairs.
- Feature authors use isolated worktrees. The primary owns the shared entrypoint, application types, asset manifest, build configuration and common stylesheet integration. Authors submit bounded shared-file changes for serial integration.
- Parallel feature work uses the exclusive files listed below. A shared-file change is integrated before another author depends on it. Examples include the text/draft contract in #0555, creation/link integration in #0567, and creation/upload integration in #0578.

**Review and verification flow**

Author implementation and focused checks lead to fresh source review. The primary then integrates the reviewed changes and produces the combined candidate. One Luna / Light worker runs prescribed checks under the primary compiler lease while Terra / Medium reviews the rendered candidate. Material integration corrections receive focused re-review. The story moves to RFR when development and review finish; Terry controls Completed. Cards reach RFR on technical completion; owner demos run at turn end.

Go builds, vet and full Go test runs are serialized under the primary. Independent Node/TypeScript jobs are capped at two. Browser control has one operator. Each candidate uses isolated ports, data, outputs and the required shared diagnostic queue.

**Turn staffing**

| Turn | Stories | Author slots | Visual/check support | Planned direct peak | Largest story estimate |
|---|---|---:|---|---:|---:|
| T1 | #548 | 1 | Terra / Medium; Luna / Light | 3 | 2.5 h |
| T2 | #549, #550 | 2 | Terra / Medium; Luna / Light | 4 | 2.25 h |
| T3 | #551, #552, #553 | 3 | Terra / Medium; Luna / Light | 5 | 2.25 h |
| T4 | #554, #555 | 2 | Terra / Medium; Luna / Light | 4 | 2.5 h |
| T5 | #556, #557, #558, #559, #560, #561, #562, #563, #564, #565, #566 | 11 | Terra / Medium; Luna / Light | 13 | 2.5 h |
| T6 | #567, #568, #569, #570, #571, #572, #573 | 7 | Terra / Medium; Luna / Light | 9 | 2.5 h |
| T7 | #574, #575, #576, #577 | 4 | Terra / Medium; Luna / Light | 6 | 2.25 h |
| T8 | #578, #579 | 2 | Terra / Medium; Luna / Light | 4 | 2.25 h |
| T9 | #580 | 1 | Terra / Medium; Luna / Light | 3 | 2.5 h |

The peak is a ceiling for useful concurrent work. Support workers join when a reviewed candidate exists, and fresh reviewers replace completed authors. The three spare slots at the widest turn accommodate corrections or an additional ready review.

**Named story assignments**

| Turn | Story | Author | Independent reviewer | Prerequisites |
|---|---|---|---|---|
| T1 | #548 Open the compiled TypeScript shell | Sol Medium 0548 Shell | Sol High 0548 Review | Epic kickoff |
| T2 | #549 Scan the familiar board | Sol Medium 0549 Board | Sol Medium 0549 Review | #548 |
| T2 | #550 See live workspace attention | Sol Medium 0550 Attention | Sol High 0550 Review | #548 |
| T3 | #551 Read a card in context | Sol Medium 0551 Drawer | Sol Medium 0551 Review | #549 |
| T3 | #552 Keep the selected board current safely | Sol Medium 0552 Live State | Sol High 0552 Review | #549, #550 |
| T3 | #553 Pin boards and adjust the sidebar | Sol Medium 0553 Pins | Sol High 0553 Review | #550; #0512 completed |
| T4 | #554 Return to a board with its view intact | Sol Medium 0554 Navigation | Sol High 0554 Review | #551, #552 |
| T4 | #555 Edit exact card text with recoverable drafts | Sol Medium 0555 Text Drafts | Sol High 0555 Review | #551, #552 |
| T5 | #556 Show confirmed recent boards within a cache budget | Sol Medium 0556 Cache | Sol High 0556 Review | #552, #554 |
| T5 | #557 Create a card from its lane | Sol Medium 0557 Create | Sol High 0557 Review | #549, #555 |
| T5 | #558 Move cards with pointer and keyboard | Sol Medium 0558 Move | Sol High 0558 Review | #555 |
| T5 | #559 Assign owners and individual priorities | Sol Medium 0559 Assignment | Sol High 0559 Review | #555 |
| T5 | #560 Read and post comments and activity | Sol Medium 0560 Comments | Sol High 0560 Review | #555 |
| T5 | #561 Follow and maintain typed relationships | Sol Medium 0561 Relationships | Sol High 0561 Review | #551, #555 |
| T5 | #562 Find and restore active or archived work | Sol Medium 0562 Search Archive | Sol High 0562 Review | #551, #554, #555 |
| T5 | #563 Preview and manage saved attachments | Sol Medium 0563 Attachment Read | Sol High 0563 Review | #551, #555 |
| T5 | #564 Configure and inspect automatic publishing | Sol Medium 0564 Publishing | Sol High 0564 Review | #550, #553, #555 |
| T5 | #565 Create, close and reopen boards | Sol Medium 0565 Board Lifecycle | Sol High 0565 Review | #552, #555 |
| T5 | #566 Inspect and stop the service | Sol Medium 0566 Service Status | Sol High 0566 Review | #552, #555 |
| T6 | #567 Seed typed links during creation | Sol Medium 0567 Seed Links | Sol High 0567 Review | #557, #561 |
| T6 | #568 Approve one reviewed card | Sol Medium 0568 Approve | Sol High 0568 Review | #555, #558, #560 |
| T6 | #569 Upload files to an existing card | Sol Medium 0569 Upload | Sol High 0569 Review | #555, #563 |
| T6 | #570 Set batch priorities | Sol Medium 0570 Batch Priority | Sol High 0570 Review | #555, #559, #562 |
| T6 | #571 Preview and apply dependency ticket order | Sol Medium 0571 Ticket Order | Sol High 0571 Review | #555, #562 |
| T6 | #572 Capture and download a native backup | Sol Medium 0572 Backup Capture | Sol High 0572 Review | #555, #563 |
| T6 | #573 Activate a staged build and reconnect | Sol Medium 0573 Activation | Sol High 0573 Review | #566 |
| T7 | #574 Approve a complete lane | Sol Medium 0574 Bulk Approve | Sol High 0574 Review | #568 |
| T7 | #575 Resume interrupted file batches | Sol Medium 0575 Upload Recovery | Sol High 0575 Review | #569 |
| T7 | #576 Restore a native backup | Sol Medium 0576 Restore | Sol High 0576 Review | #572 |
| T7 | #577 Publish a captured backup | Sol Medium 0577 Publish Backup | Sol High 0577 Review | #572, #564 |
| T8 | #578 Stage attachments during card creation | Sol Medium 0578 Create Files | Sol High 0578 Review | #557, #567, #575 |
| T8 | #579 Maintain lane configuration | Sol Medium 0579 Lane Settings | Sol High 0579 Review | #555, #574 |
| T9 | #580 Qualify the complete candidate for handoff | Sol High 0580 Qualification | Sol High 0580 Review | #556, #578, #579, #570, #571, #576, #577, #565, #573; #0512 completed |

**Exclusive feature files**

These are implementation ownership boundaries. The primary remains the serial integration owner of shared files and interfaces.

| Story | Author-owned feature area |
|---|---|
| #548 | web/src/main.ts; web/src/model.ts; web/index.html; web/package.json; web/tsconfig.json; web/assets.go; internal/v2/browser.go |
| #549 | web/src/board-view.ts; web/src/board.css; web/src/tokens.css |
| #550 | web/src/attention.ts; web/src/attention.css; web/src/workspace-state.ts |
| #551 | web/src/card-view.ts; web/src/drawer.css |
| #552 | web/src/board-state.ts |
| #553 | web/src/attention.ts; web/src/sidebar-state.ts; web/src/attention.css |
| #554 | web/src/view-state.ts |
| #555 | web/src/card-editor.ts; web/src/drafts.ts; web/src/operations.ts |
| #556 | web/src/recent-boards.ts; web/src/workspace-settings.ts |
| #557 | web/src/create-card.ts; web/src/create-card.css |
| #558 | web/src/card-movement.ts; web/src/board-view.ts |
| #559 | web/src/card-assignment.ts |
| #560 | web/src/comments.ts; web/src/history.ts |
| #561 | web/src/relationships.ts |
| #562 | web/src/search.ts; web/src/archive.ts; web/src/view-state.ts |
| #563 | web/src/attachments.ts |
| #564 | web/src/publishing-settings.ts |
| #565 | web/src/board-lifecycle.ts; web/src/workspace-create.ts |
| #566 | web/src/service-tools.ts |
| #567 | web/src/create-card.ts; web/src/relationships.ts |
| #568 | web/src/card-approval.ts |
| #569 | web/src/uploads.ts |
| #570 | web/src/batch-priority.ts |
| #571 | web/src/ticket-order.ts |
| #572 | web/src/backup.ts |
| #573 | web/src/service-tools.ts; web/src/session.ts |
| #574 | web/src/lane-approval.ts |
| #575 | web/src/uploads.ts; web/src/upload-recovery.ts |
| #576 | web/src/restore.ts |
| #577 | web/src/backup-publication.ts |
| #578 | web/src/create-card.ts; web/src/uploads.ts |
| #579 | web/src/lane-settings.ts |
| #580 | web tests and candidate verification report; web/assets.go; web/index.html; replacement capability ledger |

**Current rolling window**

- T1 assigns Sol Medium 0548 Shell and Sol High 0548 Review. #0548 is dependency-ready when the epic is started.
- T2 assigns #0549 and #0550 to their named authors and reviewers after #0548 acceptance.
- T3 assigns #0551 after #0549; #0552 after #0549 and #0550; and #0553 after #0550 plus the already-Completed #0512 contrast requirement. Their named authors and reviewers are listed above.
- Refresh these remaining first three turns after each execution handoff, including carryover and any changed prerequisites.

**Sizing and risk**

The story estimates total 69.5 hours, with individual estimates from 1.5 to 2.5 hours. The sum of each turn's largest author estimate is 21.5 hours. The weighted dependency path is 20.5 hours. Nine turns is the dependency minimum, not a promised completion duration. The 21.5-hour barrier baseline excludes serialized integration, reviews, combined checks and owner waiting. These are original development-sizing baselines; actual agent runtime, review, integration and owner waiting are recorded separately.

#0555 (shared text/draft recovery) and #0580 (integrated qualification) are the tightest estimates. Their reviews must verify reuse of accepted contracts and accumulated feature evidence. Findings change the remaining staffing plan and receive explicit scope handling.

**FGA consultation**

FGA PM supplied the current r67 staffing roster and T04 parallel-work precedent. The local plan adopts rolling author-to-reviewer allocation, isolated worktrees, single-writer shared files and an owned combined check. FGA supplied advisory input; local execution authority remains with Terry and this primary.

[FGA current staffing plan](C:/Users/TDO-XPS15-2024/Documents/ChatGPT/FGA/rebaseline-v1-20261111/CURRENT.txt) · [FGA T04 precedent](C:/Users/TDO-XPS15-2024/Documents/ChatGPT/FGA/t04-tracking-20261111/T04-STAFFING.txt)

**Scope and startup**

The author follows the saved story scope and demo. Prerequisites must meet Terry’s execution gate before the card enters In Progress. All new presentation and state wiring are TypeScript, with the accepted V2 transport and domain contracts retained. Production activation follows full candidate acceptance and explicit release authority.
