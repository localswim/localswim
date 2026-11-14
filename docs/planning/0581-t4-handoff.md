# T4 drawer workflows

T4 started from the accepted T3 integration commit `e17790b`. The Architect
released #0594–#0599 and the live board recorded their move to In Progress at
revisions 6077–6082. The nine packets in staffing revision 4 delivered their
modules. Revision 10 records the current T4–T6 roster.

| Story | Evidence collected | Remaining qualification |
| --- | --- | --- |
| #0594 Card fields | V1 title and description editors, priority select, and direct owner cycle observed. V2 saved a title with combining Unicode, selected the whole title on entry, restored focus on Escape, and focused the description editor with Save and Cancel visible. A stale title PATCH returned HTTP 412, kept the browser draft, and preserved the concurrent CLI edit. | Native archive and restore adapters exist. A V1 browser entry has yet to be established. Verify lost edits, owner and priority, and matched editor states. |
| #0595 Comments | V2 posted a multiline comment using Shift+Enter and Enter. The CLI confirmed one exact comment on disposable Completed card #0038. After a proxy dropped a committed response, a reload restored the draft and blocked reposting. Checking the original receipt cleared the draft and showed one comment. A held HTTP 201 kept the new comment out of the drawer until release, and newer unsent text survived receipt and reload. | Qualify switching boards with an unsent draft and posting on an archived fixture. |
| #0596 Relationships | V1 and V2 saved References to Referenced by. The browser recovered a reversed relationship after a disposable proxy dropped the committed PATCH response. Native tests cover ordered endpoints, identity, stale validators, duplicate conflict, symmetric no-op, rollback and priority floor. A regression test validates the original DELETE 204 and projects removal immediately. | Verify settled feedback and follow/edit/remove parity with matched data. A writer fence can delay the coherent drawer read used in recovery. |
| #0597 Single approval | A disposable proxy held HTTP 201 after the atomic write. The browser closed the tray while the card remained in Ready for Review. On keyboard approval, the card animated into Completed after the response and kept keyboard focus. A concurrent edit produced HTTP 412, and the CLI confirmed the card remained in Ready for Review with zero comments. Regression tests cover original-receipt recovery and an earlier actor's refusal leaving a later actor's approval pending. | Architect review. |
| #0598 Attachments | V1 exposes preview and download links, size, cover and one-click Remove with a named toast. Both UIs rendered the wide fixture at 400×200. V2 portrait and square previews preserved their proportions at browser DPR 1.5. The original download had the expected SHA-256. V2 recovered committed cover clear and detach writes after lost responses. An ordinary V2 detach showed the matching toast, removed the row, and preserved the audit entry. | Architect review. |
| #0599 History and cache | V1 closes the drawer on reload and shows a 256 MiB cache default. The V2 browser opened cache settings, switched boards with the drawer open, restored the drawer on Back, returned to Beta with Forward, and closed the drawer on reload. Unit tests cover principal/catalog scoping and zero-budget eviction. A workspace 403 canceled both held board responses and the browser exposed only Retry. | Verify preference behavior and retained read-only content on a transport failure. |

The working browser evidence is in the UI Lead's private `output/t3-v1`
directory under the `t4-` capture prefix. The shared queue receipts record the
V1 observations. The held approval response is recorded in the timing proxy
output. Terra's independent comparison of earlier builds found the valid preview
consistent at 400×200 and identified a cache control placement and spinner
difference. The rebuilt browser capture includes the CSS correction. Different
fixtures and scroll positions limit comparisons elsewhere. The final PR needs a
candidate-specific verification record.

T4 remains In Progress during review and qualification. The Architect owns
acceptance and integration before T5 opens.

## Reboot checkpoint

- [Draft PR #4](https://github.com/localswim/localswim/pull/4) is the restart point.
  The Architect verified the five original corrections at `0a4f1a5`. The next
  commit carries measured portrait and square preview sizes, matched detach
  feedback, and the latest stale-write and recovery evidence.
- The resumed change guards `inspectCreate` and `submitCreate` by captured actor
  and intent. Navigation invalidates deferred drawer presentation independently.
  Three regression tests cover late submit and inspection results after an
  actor change and a late result after switching boards.
- T4 stays In Progress during the Architect's review. T5 requires the
  Architect's release.
- Browser evidence is saved under the UI Lead's private `output/t3-v1` folder.
  The disposable fixtures can be restarted from their existing data. Keep the
  live board and shared queue under their existing owners.

## Resumed browser qualification

- #0594. Both versions cycled the owner and selected P1 Urgent. Both displayed
  the changed owner and saved priority. The V2 CLI verified the resulting
  values. A truncated title response recovered the committed title and
  dismissed the editor using its existing audit recovery. When the proxy
  closed before response headers, Chromium resent the PATCH and received
  HTTP 412. The first request committed and the second applied no mutation.
  The client keeps the draft and reports the 412 conflict. The existing
  unknown-response path checks the card, actor, field, old and new values,
  and exact revision in the audit. The audit has no HTTP request ID, so
  clients sharing the Human identity can produce indistinguishable events.
  The resumed editor comparison aligned the focused card title, description,
  priority, owner and lane at 1280×720. Both selected the whole title on edit
  and exposed Save and Cancel in the description editor. Surrounding board
  data remains different. Archive and restore are available through the CLI.
  A V1 browser entry has yet to be observed.
- #0595. An unsent draft survived Alpha → Beta → Alpha. Following a link to
  an archived card allowed one comment, verified by CLI.
- #0596. Both browsers followed the linked card and removed the relationship
  in one action. The V2 CLI returned zero remaining relationships after
  removal. A matched source and target also verified the direction change
  and the confirmed toast wording in both versions.
- #0597. The current browser build recovered a truncated HTTP 201 by the
  original approval receipt and moved the fixture into Completed. The CLI
  confirmed exactly one fixed approval comment by the Human actor.
- #0599. Both versions accepted 64 MiB and reset to 256 MiB on reload. The
  corrected V2 field uses a 1 MiB minimum and no HTML maximum, matching V1.
  A failed board transport preserved the last confirmed card list and
  disabled Add card. The earlier HTTP 403 scenario cleared the retained data.
