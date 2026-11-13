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
| #0585 Workspace controls | Pin and unpin, sidebar resize and collapse/expand in the browser. In both versions Arrow Up moved Alpha above Beta, kept focus on Alpha's reorder button, and saved the order across reload. After a truncated V2 response, the browser reported that the current order matched and the native CLI confirmed it at workspace revision 5. | Candidate review. |
| #0586 Search and Completed | Browser title, description, and comment search, Escape, and matched V1/V2 older Completed fixtures. The count stays stable and the toggle controls whether the old ticket matches. Both versions display `no match` and reset the lane scroll when clearing a search. Pointer clear was exercised in both browsers. | Candidate review. |
| #0587 Drawer | Keyboard open, native comment read, relationship navigation controls and Ready for Review action placement. Both browsers displayed the same long description and could follow an archived child from its parent. V2 now preserves literal description formatting, as observed in V1. | Candidate review. Single-card approval is scheduled under #0597. |
| #0588 Create | Inverse relationship creation and post-commit response loss on a disposable service. The browser recovered the original card and incoming blocks edge. Native inspection confirms ticket 2 and its incoming edge. Go tests cover ordered seeds, rollback and committed-card readback while fenced. | Matched create-dialog frames for each state. Attachment ingestion is #0600 and the current control is disabled. |
| #0589 Move | Native readback confirmed V1 and V2 Human-permitted drags into Completed. Terry confirmed that he can complete cards by dragging and usually uses Reviewed & Approved. After a truncated response, the native CLI confirmed one lane transition at revision 39. After a concurrent edit, the stale move returned HTTP 412 and the original lane remained saved. | Matched forbidden-drop and drag focus/scroll evidence. An inconclusive readback keeps the original move pending. |
| #0590 Lane approval | V1 uses a single click and shows the approval toast. In both versions a search matching one of two review cards left the action enabled with a count of two and the action moved both cards to Completed. A truncated success response in V2 was recovered through the original receipt. Native card 27 reached Completed at revision 38 with one approve activity and one approval comment. | Candidate review. |
| #0591 Dependency order | Paired 19-card preview and Apply produced the same ticket mappings. V2 recovered a truncated success body and the native preview confirmed the settled 21-card order at revision 41. A stale Apply returned HTTP 412, asked for a refreshed preview, and left the 22-card proposal unapplied. | Hidden-old Completed scope if enabled for dependency ordering. V1 did not open a preview under a search filter in the observed fixture. |
| #0592 Batch priority | Native CLI used the dated V2 API and the saved receipt records two changes at one revision. Focused Go coverage supplies dependency floors and stale refusal evidence. | No new browser control is in scope. The live receipt does not capture the stale refusal or floor challenge. |
| #0593 Live updates | Bot changed a background board and the selected draft remained intact. Paired V1/V2 tests retained the same draft through a Bot edit, writer outage and reconnect. V1 displayed `REFRESHING` and left Add card clickable during the outage. Terry explicitly approved keeping V2 submission guarded until fresh state is confirmed and directed us to fail safe on changes. V2 uses the observed status label and keeps the draft editable. | Paired Human cue and visual recheck. V2 actor attribution requires scoped activity evidence and the catalog cannot distinguish every same-second background edit. |

Terry clarified that a pending write should leave the last confirmed view in
place. A successful response is enough to show the change. Pin and reorder now
show a saving status with the original order until the service confirms the
new order. The focused test also checks that a successful PATCH sends no
follow-up GET.

The delayed-response browser run kept the previous pin order until HTTP 200.
It also held an approval response after the service committed. The browser
showed the pending status and kept the card in Ready for Review until HTTP 201
arrived. For a Bot move, the browser showed `REFRESHING` with the original lane
while the read was held, replayed the confirmed move, and settled in Ready for
Work. The replay is skipped when reduced motion is requested or either card
position is out of view. Single-card approval timing is recorded on #0597 and
in the T4 scope.

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
- The paired browser run used the same Alpha and Beta fixture data at 1280 by
  720. It captured keyboard pin order, empty search and scroll, filtered lane
  approval, and a changed order before and after Apply. Follow-up captures
  show the corrected V2 behavior.
- The Architect found two integration races. Manual Retry now adopts the
  workspace, service and identity responses under independent read ownership.
  With the manual service response delayed, separate board-only and catalog
  updates both settled and the browser could open New card again. When the
  drawer read a newer board while live notifications were held, the prior search
  result changed from one match to `no match` and the stale card left the list.
- The first approval-loss run closed the connection before response headers.
  The browser transport sent an additional request that received HTTP 412.
  The truncated-body rerun sent one request and verified the original receipt.
  The client code issues one fetch per action and the API precondition prevents
  a second write.

T3 is a draft candidate. The Architect’s review, matching visual and interaction
evidence, and the integration decision are still required before the handoff
lane or T4 release.
