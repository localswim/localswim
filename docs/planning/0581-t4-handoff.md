# T4 drawer workflows

T4 started from the accepted T3 integration commit `e17790b`. The Architect
released #0594–#0599 and the live board recorded their move to In Progress at
revisions 6077–6082. The nine packets in staffing revision 4 delivered their
modules. Revision 9 records the current T4–T6 roster.

| Story | Evidence collected | Remaining qualification |
| --- | --- | --- |
| #0594 Card fields | V1 title and description editors, priority select, and direct owner cycle observed. V2 saved a title with combining Unicode. The rebuilt UI focused and selected the whole title, restored title focus on Escape, and focused the description editor with Save and Cancel visible. | Native archive and restore adapters exist. A V1 browser entry has yet to be established. Verify stale and lost edits, owner and priority, and matched editor states. |
| #0595 Comments | V2 posted a multiline comment using Shift+Enter and Enter. The CLI confirmed one exact comment on disposable Completed card #0038. After a proxy dropped a committed response, a reload restored the draft and blocked reposting. Checking the original receipt cleared the draft and showed one comment. A held HTTP 201 kept the new comment out of the drawer until release, and newer unsent text survived receipt and reload. | Qualify switching boards with an unsent draft and posting on an archived fixture. |
| #0596 Relationships | V1 and V2 saved References to Referenced by. Native tests cover ordered endpoints, identity, stale validators, duplicate conflict, symmetric no-op, rollback and priority floor. A regression test validates the original DELETE 204 and projects removal immediately. | Verify browser lost-response recovery, settled feedback, and follow/edit/remove parity with matched data. A writer fence can delay the coherent drawer read used in recovery. |
| #0597 Single approval | A disposable proxy held HTTP 201 after the atomic write. The browser closed the tray while the card remained in Ready for Review. On keyboard approval, the card animated into Completed after the response and kept keyboard focus. Regression tests cover original-receipt recovery and an earlier actor's refusal leaving a later actor's approval pending. | Verify stale refusal on the final build. |
| #0598 Attachments | V1 exposed separate preview and download links, size, cover and Remove. Both rendered the same valid raster at 400×200. V2 selected a cover and displayed it on the board. The browser download matched the original SHA-256. A detached fixture disappeared from the list and remained in the audit trail. | Verify lost-response recovery and the V1 detach interaction. |
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
