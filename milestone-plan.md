# Localswim V2 milestone plan

Build a fresh Go backend. One loopback listener serves every board and the workspace. The browser and CLI share the REST API. Keep V2 data and its port separate from V1. New projects start in V2. V1 data migration and legacy schema conversion are outside this plan.

## Three-day delivery target

**Day 1. Put a working board in the browser.** Build M01–M03 as the first runnable path and do M04 alongside it. Start the browser and CLI against shared request and response examples immediately. Wire the browser to the real service that day and demonstrate creating and switching boards, adding, editing, and moving a card, and reopening after restart with the changes intact.

**Day 2. Use it for daily work.** Integrate comments, archive, activity, search, approvals, attention and pins, and attachment basics as those streams land. Run the browser and CLI through the same service.

**Day 3. Integrate the remaining parity work and demo it.** Exercise graph and lane operations, restore, notices, Git publication, and service replacement on the integrated Windows build. Use actual demo results to update the release forecast. Browser and CLI work, integration, review, and demo time are separate from the backend estimates below.

The 82.5 hours are aggregate backend development, not a serial schedule. With six backend owners the largest stream is 16.5 hours before cross-stream waits and integration. The browser and CLI have separate owners and start on day one.

| Backend workstream | Milestones | Dev estimate | Start and integration dependencies |
| --- | --- | ---: | --- |
| Board and card API | M01–M03, M05–M07 | 12 h | Start here. Own the first durable board, card writes, and audit contract. |
| Sessions, review, and workflow | M04, M14–M20 | 16.5 h | Build M04 alongside the card API. Review uses the move and comment transaction. Retirement uses archive behavior. |
| Relationships, priority, and search | M08–M13, M21–M22 | 16.5 h | Start graph rules against small fixtures. Live demos use real cards, comments, and archive state. Renumbering follows the blocking graph. |
| Attachments, integrity, and recovery | M23–M25, M28–M30 | 13.5 h | Start file and backup handling against synthetic data. Integrate upload after card creation. Backup and restore use the audited attachment inventory. |
| Workspace, events, and notices | M26–M27, M31–M33 | 10.5 h | Start catalog and pin work when board identity is settled. Streams use health from M28. Notices consume committed Human edits and need M32 settings. |
| Git publishing and lifecycle | M34–M39 | 13.5 h | Build against disposable repos and staged binaries. Live publishing needs M29, workspace publishing needs M27, and final shutdown needs M31 and M35. |

Agree on board and card IDs, actor context, revision checks, and one mutation transaction before work fans out. Give one integration owner the shared schema and routes. Other owners work in separate feature files. Use small fixtures to build before upstream code lands. Each milestone finishes with its own live API demo on the integrated service, once its prerequisites are available.

## API demos

The routes are proposed contracts. `B` means `/api/boards/{board}` and `C` means `B/cards/{card}`. Estimates include coding and focused tests. Demo time is separate. Aim for 2 hours and cap development at 3 hours per milestone. M01–M03 take an estimated 7 development hours on the backend path.

| Milestone | Dev estimate | What the API demo shows |
| --- | ---: | --- |
| M01 | 3 h | **Create a workspace and boards.** On one loopback port, `POST /api/boards` twice with the built-in workflow. `GET /api/boards` lists both. Restart and fetch each by ID to show durable, isolated state. A competing writer refuses the same data root. |
| M02 | 2 h | **Create and find cards.** `POST B/cards` allocates a ticket and default priority. Read it by stable ID and ticket. Reject a duplicate explicit ID or stale board revision without allocating another ticket. |
| M03 | 2 h | **Move and assign cards.** `PATCH C` moves a card along an allowed transition and assigns an owner in a separate edit. Read the saved lane and attribution. A disallowed Human or Bot action leaves the revision unchanged. |
| M04 | 2.5 h | **Browser session and conflict-safe edits.** `GET B/session` gives the same-origin browser its Human context and board styling. `PATCH C` edits the title and description. Show a stale card conflict, an unrelated edit that leaves the card token valid, and readback after a lost reply. |
| M05 | 1.5 h | **Comment and read newest comments.** `POST C/comments` records text, actor, and time. `GET B/comments?limit=2` returns the newest attributed comments across cards. |
| M06 | 1.5 h | **Archive and restore cards.** `PATCH C` with `archived: true` hides the card from ordinary lists. A focused read still shows its history. Unarchive and confirm the same ID, ticket, and lane. |
| M07 | 2 h | **Read audited activity.** `GET B/activity` with a UTC interval returns attributed changes. Its monitor view returns bounded actor, time, ticket, and operation metadata without card prose. Reject malformed bounds. |
| M08 | 1.5 h | **Set a parent.** `PATCH C` sets or clears a parent. Focused reads show the corresponding child list and audit. A parent cycle is refused. |
| M09 | 2.5 h | **Manage typed links.** `POST B/cards` creates a card with valid related-card links in one write. Replace a link type through `C/relationships` and show both directions on readback. An invalid target rolls back creation. Show Human `#ticket` references linked from a comment and Bot text left unchanged. |
| M10 | 2.5 h | **Apply blocking priority floors.** Create a `blocks` relationship through `C/relationships` and show audited transitive promotions in one revision. Demonstrate a demotion floor, an archived unfinished blocker, a Completed cutoff, and no automatic demotion after unlinking. |
| M11 | 2 h | **Change priorities as a batch.** `PATCH B/cards/priorities` evaluates selected priorities together and returns effective priorities plus cascades. A duplicate reference or Completed selection refuses the whole batch. |
| M12 | 2 h | **Order visible swimlanes.** `GET B?view=lanes` shows priority first, then same-rank blockers before blocked cards. Show a cycle in ticket order and exclude archived cards from the visible graph. |
| M13 | 2 h | **Search and find next work.** `GET B/cards?sort=triage&lane=backlog&limit=2` applies the V1 whole-board triage score before filtering. Add a search term to match title or ticket, and demonstrate explicit content search and numeric order for relationship lookup. |
| M14 | 1.5 h | **Approve one review.** Human `POST C/approvals` adds the review comment and moves the card to Completed in one revision. Bot and stale requests cannot approve or duplicate the comment. |
| M15 | 2.5 h | **Approve a review lane.** Enable the optional action on Ready for Review. `POST B/lanes/{lane}/approvals` approves the exact rendered set under board and card versions. A changed membership refuses the entire action. |
| M16 | 2 h | **Create a custom workflow.** `POST /api/boards` accepts named lanes, users, priorities, and transition rules. Read the resolved stable IDs. A project rename keeps the board address. An invalid workflow refuses creation. |
| M17 | 2 h | **Grant policy permissions.** `PATCH B/permissions` adds validated creation and transition grants atomically. Exercise the newly authorized move. Refuse an invalid grant set and a browser attempt at CLI-only maintenance. |
| M18 | 2 h | **Add and rename lanes.** `POST B/lanes` inserts a lane beside a stable anchor with explicit rights. `PATCH B/lanes/{lane}` changes the label while preserving the ID, cards, and grants. |
| M19 | 1.5 h | **Retire a lane.** Retire an empty active lane through `PATCH B/lanes/{lane}`. Archived cards and history remain inspectable. Active occupancy, Completed, and in-place unarchive are refused. |
| M20 | 2.5 h | **Change a lane ID.** `PUT B/lanes/{lane}/identity` changes the stable ID and updates policy, card states, and history atomically. Attempts involving the reserved Completed ID refuse. |
| M21 | 2 h | **Preview dependency ticket order.** `GET B/lanes/{lane}/ticket-order` previews a selected ticket pool using full blocker paths. The revision stays unchanged. A selected cycle refuses. |
| M22 | 2 h | **Apply dependency ticket order.** `PUT B/lanes/{lane}/ticket-order` applies a current preview. Read back the new ticket numbers, unchanged card IDs and attachment anchors, and the renumber audit. A stale preview refuses. |
| M23 | 2.5 h | **Upload and list attachments.** Create a card, then `POST C/attachments` for a valid file and a file over 10 MiB. List the successful file with original name and attribution. Readback resolves an uncertain upload without blindly repeating it. |
| M24 | 2 h | **Download, preview, and select a cover.** `GET C/attachments/{file}` returns verified bytes. Show raster preview and safe generic download. Select an active raster as the cover through `PATCH C` and read it back. |
| M25 | 1.5 h | **Detach an attachment.** `DELETE C/attachments/{file}` removes active access and clears its selected cover. Focused history still records the attachment for backup. |
| M26 | 2 h | **Show workspace attention and recency.** `GET /api/boards` shows the three attention counts and exact latest audited card edit. Compare archived edits, a project rename, and an unavailable board to show the correct ordering and distinction between unknown and zero. |
| M27 | 2 h | **Persist pinned board order.** `PUT /api/workspace/preferences` saves manual pin order under a separate revision. Another client and a restart read the same order. Stale saves and invalid stored preferences produce visible failures. |
| M28 | 2.5 h | **Verify and export native board state.** `GET B/verification` and `GET B/snapshot` verify and export board policy, cards, and audit. Compare a valid snapshot with a damaged one. Status reports failed durable state separately from the last accepted view. The CLI can run the same validator offline. |
| M29 | 2.5 h | **Capture a coherent backup.** `POST B/backups` captures one revision plus the exact verified attachment bytes, including detached files. Download the backup. A concurrent edit stays live and appears in the next capture. |
| M30 | 2.5 h | **Restore or admit a native snapshot.** `PUT B/snapshot` refuses a damaged V2 backup. Apply the valid backup to recover the existing board at the same address with a higher revision. Read back the recovered card and attachment bytes. |
| M31 | 2.5 h | **Stream live board and workspace changes.** `GET /api/events?board=a&watch=b` streams current catalog, selected-board, and retained-board versions after committed edits. Reconnect gets current state. An unchanged board can return `304`. Disconnect releases the subscription. |
| M32 | 1.5 h | **Configure owner notices.** `GET` and `PATCH B/notifications` show the private target and off-by-default state. Retargeting preserves enablement. Enable and disable persist across restart. |
| M33 | 2.5 h | **Deliver a committed Human notice.** Make Human and Bot edits with notices enabled. Observe one bounded metadata-only notice after the quiet interval through the native queue command and an approved test destination. A delivery failure does not undo a saved edit. |
| M34 | 2.5 h | **Publish an exact Git backup.** `POST B/publications` publishes a captured backup to a disposable Git remote. Verify the committed paths match the capture and unrelated staged work survives. Refuse unsupported hooks, filters, or filesystem monitors before staging. |
| M35 | 2.5 h | **Publish automatically and recover a failed push.** Enable board publishing through `PATCH B/publishing`. A committed edit publishes after quiet. Show a failed push in status, then recovery of the pending push without another card edit. |
| M36 | 1.5 h | **Publish workspace preferences independently.** Enable workspace publishing through `PATCH /api/workspace/publishing` and save pin order. The test remote receives just the preferences artifact. Show that board and workspace publishing use the same repository lock. |
| M37 | 2 h | **Stop and reopen one board.** `PATCH B/availability` closes and flushes the selected board. Another board continues serving on the same port. Reopen the selected board at its existing address and read the saved revision. |
| M38 | 2 h | **Report build health before activation.** `GET /api/service` reports the running build and a staged replacement's validation status. Stage an invalid candidate and show its refusal while the running service continues serving valid data. |
| M39 | 3 h | **Activate a build and stop cleanly.** `POST /api/service/activations` applies a valid staged build. Reconnect to the same port and board URLs with fresh credentials. An authorized shutdown drains admitted writes and streams and flushes enabled publishing. A failed final push refuses shutdown and is reported in status. |

Before M01, confirm the Go toolchain, an available loopback port, and a local store that can commit atomically and reject a competing writer. Start with one built-in workflow and local credentials. From the first write, check actor authority and the applicable revision before committing data and audit together. Show refusals and uncertain outcomes without replaying writes.

Keep the cap by splitting at a useful HTTP result. If setup runs long, demo reading one newly initialized durable board before adding API creation. If upload verification, retained-board streaming, Git publishing, or Windows handoff runs long, split at upload versus readback, selected versus retained boards, commit versus push, or graceful shutdown versus process activation. Use disposable Git repositories and an approved test destination for the notification demo.

Native snapshot admission is an explicit V2 operation. Before calling parity complete, check any operator workflow that depends on V1 detecting an external file replacement or applying a source update automatically. The corresponding demos cover the recovery and same-port replacement behavior. Browser layout, accessibility, drafts, navigation, and CLI presentation still need side-by-side checks against V1 using this API.

[V1 behavior reference](https://github.com/TerryOtt/localswim/blob/46d5d58695aa807c246c42e45f0615860fdcbbe5/README.md)
