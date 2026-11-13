# #0514 staffing plan r2

Independent review passed. Revision 0514-staff-r2 supersedes r1. It covers all 33 stories #0548-#0580 across their unchanged earliest T1-T9 dependency turns. The objective is cumulative elapsed-time reduction.

**Useful parallel authoring**

Four stories use two authors. Each retains one accountable lead, exclusive author files and one fresh independent reviewer. The work begins with a short lead-owned interface agreement, then independent author packets run together. The lead combines the packets before final review and the primary integrates the accepted story.

| Story | Lead packet | Second author packet | Contract |
|---|---|---|---|
| #548 | Sol Medium 0548 Shell<br>Fresh typed shell, route states and in-memory session/bootstrap integration. | Sol Medium 0548 Assets<br>Deterministic TypeScript build, generated asset layout and Go embedding/routes. | Agree entry module, generated output paths, exported mount shape, HTML anchors, asset URLs and CSP before parallel edits. Shell lead owns index.html; assets author supplies a bounded integration patch. |
| #549 | Sol Medium 0549 Board<br>Confirmed lane/card data, rendering structure, filter behavior and Completed membership. | Terra Medium 0549 Style<br>V1 density, lane/card dimensions, independent scrolling and accepted contrast styling against structural fixtures. | Agree class/attribute names, card slots, scroll ownership, focus states and supported widths. CSS cannot filter or reorder records. Final visual reviewer is a different Terra agent. |
| #550 | Sol Medium 0550 Attention<br>One workspace stream, catalog reads, cancellation, retry and authoritative state. | Sol Medium 0550 Render<br>Pure attention rendering, fixed count columns, selected/unavailable states and navigation intents. | Agree immutable catalog/view snapshot, explicit observation time, callback intents and disposal shape. State lead alone owns subscriptions, timers, capabilities and retry decisions. |
| #555 | Sol Medium 0555 Text Drafts<br>Captured identity/validator, one-write state machine, draft persistence, readback matching and uncertainty fences. | Sol Medium 0555 Editor<br>Title/description controls, composition, exact-text adapter, pending visuals and focus using the agreed operation interface. | Agree resource keys, exact submitted text, operation-state union and submit/inspect/dismiss callbacks. Core is the sole persistence and recovery authority; editor never implements a second retry path. |

**Expected effect on T1-T9**

| Turn | Baseline largest story | Estimated net turn saving |
|---|---:|---:|
| T1 | 2.5 h | 15-30 min |
| T2 | 2.25 h | 20-35 min combined |
| T4 | 2.5 h | 10-20 min |

The provisional cumulative saving is 45-85 minutes, after estimated additional contract, integration and review coordination. The 21.5-hour author-only barrier baseline becomes approximately 20.08-20.75 hours on that sizing model. Common verification, review and owner waiting remain outside that baseline. Nine turns remains the dependency minimum.

T2 is a paired optimization. Its elapsed branch is the slower of #0549 and #0550, followed by their shared join. With a 15-minute joint overhead, that slower completed story must take 85-100 minutes for the modeled gain. Count the turn saving once.

The estimates are unmeasured. Record interface time, author start/end, integration, reviewer corrections and resource waiting. Extra coupling or a review cycle can reduce the gain to zero. Keep the first two-author runs as evidence for subsequent allocation decisions.

**Full roster**

| Turn | Story | Accountable lead | Coauthor | Independent reviewer | Prerequisites |
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

**Turn capacity**

| Turn | Stories | Authors | Planned direct peak including support |
|---|---|---:|---:|
| T1 | #548 | 2 | 4 |
| T2 | #549, #550 | 4 | 6 |
| T3 | #551, #552, #553 | 3 | 5 |
| T4 | #554, #555 | 3 | 5 |
| T5 | #556, #557, #558, #559, #560, #561, #562, #563, #564, #565, #566 | 11 | 13 |
| T6 | #567, #568, #569, #570, #571, #572, #573 | 7 | 9 |
| T7 | #574, #575, #576, #577 | 4 | 6 |
| T8 | #578, #579 | 2 | 4 |
| T9 | #580 | 1 | 3 |

Peak remains 13 direct workers plus the primary in T5. The ceiling is 16 direct workers at depth one, leaving three slots for correction or ready review. Reviewers replace finished authors as inputs become ready; their slots count toward the same ceiling. Support workers join when useful work exists.

**Shared resources and review**

- The primary is Sol / High and owns the integration worktree, shared entrypoint/types, package locks, asset integration and final evidence.
- All author packets have distinct worktree branches in the roster. The lead supplies the agreed fixtures or stubs before parallel implementation begins. The listed file owner is the only author editing that file within the story; shared integration patches pass through the primary.
- One Go build/vet/test lease is granted at a time. At most two independent Node/TypeScript checks run concurrently.
- One browser operator acts at a time. A distinct Terra / Medium worker reviews the integrated visual state. The Terra CSS author on #0549 is separate from that reviewer.
- A Luna / Light worker runs prescribed combined checks. This existing support role is not counted as a new author saving.
- Fresh reviewers inspect every author packet and the combined result. Recovery, concurrency and authentication review uses Sol / High.
- Technically complete stories move to RFR after required verification. Owner demos run at turn end; Terry controls Completed.
- Additional author packets are permitted when they have an independent product and a material effect on remaining elapsed time. The lead can collapse a split when coordination consumes its value.

**File ownership**

| Story | Worker | Worktree branch | Exclusive author files |
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

**Parallel-start fixtures**

- #548: Lead publishes entry/output paths, exported mount signature and minimal typed entry fixture; assets author can verify embedding with the fixture before final shell merge.
- #549: Lead publishes structural lane/card DOM fixture and class contract with long/empty/dense examples; CSS author works against that fixture.
- #550: Lead publishes immutable catalog/view-model type plus zero/active/unavailable fixtures and navigation intent callbacks; render author uses a test stub.
- #555: Lead publishes operation-state union, submit/inspect/dismiss interface and exact-text vectors; editor author uses a test-only fake operation controller.

**Current rolling three-turn window**

- T1: #0548 has shell and asset authors plus Sol High 0548 Review; it is dependency-ready after epic kickoff.
- T2: #0549 has renderer and CSS authors, #0550 has state and renderer authors; start after #0548 acceptance. Reviewers remain separate and named above.
- T3: #0551 follows #0549; #0552 follows #0549/#0550; #0553 follows #0550 and the completed #0512 contrast work. Each remains a single-author story.
- Refresh the remaining first three turns at every execution handoff, preserving carryover, actual acceptance and updated worker allocation.

**Why other stories remain single-author**

- T3 has three tied maxima; accelerating one leaves the turn unchanged.
- T5 and T6 single-story splits are capped at 15 minutes by the next-longest job and add coupling or slot pressure.
- T7 has tied recovery jobs; T8 has one coupled lane-settings authority and only a limited off-critical-path opportunity.
- T9 already has Terra visual review, Luna prescribed checks and primary integration. No new savings are assigned to relabeling those existing duties.

**Scope and evidence**

Story sizes remain 1.5-2.5 hours and total 69.5 hours of original implementation/focused-check sizing. Parallel authoring changes staffing and predicted elapsed time, not the story scope, numbering, dependency graph or approval gates.

FGA PM’s r67 plan and T04 parallel-work precedent remain advisory input. The two independent local analyses informed this revision. Implementation has not started.
