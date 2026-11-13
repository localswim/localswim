# T3 interaction candidate

Terry accepted the T2 workspace and board comparison on 2026-11-13. The
Architect opened T3. Cards #0585–#0593 entered In Progress before implementation
or review and remain there pending the Architect’s turn decision.

The TypeScript presentation was authored from the running V1 interface,
disposable browser observations, and the accepted #0582 behavioral contract.
It builds on the accepted clean-room T2 composition and the native V2
resources. Implementation scope follows Terry’s clarifications on drag/drop,
batch priority, and ordered endpoints for relationship seeds. The workflow
guides map the browser and CLI/Bot sequences separately.

## Evidence by story

| Story | Verified | Still to establish |
| --- | --- | --- |
| #0585 Workspace controls | Pin and unpin, sidebar resize and collapse/expand in the browser. Focused tests cover original workspace validators and live updates during pending readback. | Two-pin keyboard reorder in paired browsers and live lost-response qualification. |
| #0586 Search and Completed | Browser title, description, and comment search, Escape, and matched V1/V2 older Completed fixtures. The count stays stable and the toggle controls whether the old ticket matches. | Matched clear-button, zero-result and scrolled-lane restoration evidence. |
| #0587 Drawer | Keyboard open, native comment read, relationship navigation controls and Ready for Review action placement. Focused tests cover coherent reads and stale context. | Matched archived hierarchy and long-prose views. Single-card approval is scheduled under #0597. |
| #0588 Create | Inverse relationship creation and post-commit response loss on a disposable service. The browser recovered the original card and incoming blocks edge. Native inspection confirms ticket 2 and its incoming edge. Go tests cover ordered seeds, rollback and committed-card readback while fenced. | Matched create-dialog frames for each state. Attachment ingestion is #0600 and the current control is disabled. |
| #0589 Move | Pointer drag moved a disposable card and the board showed the saved lane. Focused tests verify dispatch guards, exact transition readback and single application dispatch. | Matched forbidden-drop, focus and scroll evidence plus live stale/loss cases. When readback is inconclusive, the board keeps the original move pending. |
| #0590 Lane approval | V1 uses a single click and shows the approval toast. A truncated success response in V2 was recovered through the original receipt. Native card 27 reached Completed at revision 38 with one approve activity and one approval comment. | Matched partially filtered approval behavior. V2 currently refuses a filtered subset. |
| #0591 Dependency order | Preview rendered with no ticket change on a disposable fixture. Code review verified exact ETag/mapping, pre-dispatch checks, and read-only recovery. | Browser apply with actual number changes, stale/lost response, and hidden-old Completed scope. V1 did not open a preview under a search filter in the observed fixture. |
| #0592 Batch priority | Native CLI used the dated V2 API and the saved receipt records two changes at one revision. Focused Go coverage supplies dependency floors and stale refusal evidence. | No new browser control is in scope. The live receipt does not capture the stale refusal or floor challenge. |
| #0593 Live updates | Bot changed a background board and the selected draft remained intact. After stopping and restarting the disposable V2 writer the UI recovered the exact draft and re-enabled submission. Focused controller/status tests cover queued invalidations and connection changes. | Matched V1 reconnect and Bot/Human cues. V2 actor attribution currently requires scoped activity evidence and the catalog cannot distinguish every same-second background edit. |

## Review and build

- Fresh non-author Sol reviewers examined each story. Corrected findings
  include same-origin dispatch context, pending status reachability, stale-read
  protection, filter restoration, and independent service and catalog refresh
  ownership. The final service-epoch and per-lane status corrections passed
  strict web checks and still need candidate-level review.
- Terra’s initial paired visual review is in [the visual report](0581-t3-visual-review.md).
  It recorded fixture differences, an attachment control difference, and
  clipping in the initial ticket-order dialog. Footer spacing, header height,
  and approval presentation were corrected after those captures.
- `GOFLAGS=-count=1` with `check.ps1` passed on Windows, including Go vet, the
  uncached Go suite, TypeScript, Oxlint, Prettier, release asset verification
  and 301 web tests.
- Browser captures and bounded shared Valkey/Webdis observation receipts are
  in the UI Lead’s private `output/t3-v1` evidence directory. The queue receipts
  record observations. Native readback and tests supply the operation evidence.
- The first approval-loss run closed the connection before response headers.
  The browser transport sent an additional request that received HTTP 412.
  The truncated-body rerun sent one request and verified the original receipt.
  The client code issues one fetch per action and the API precondition prevents
  a second write.

T3 is a draft candidate. The Architect’s review, matching visual and interaction
evidence, and the integration decision are still required before the handoff
lane or T4 release.
