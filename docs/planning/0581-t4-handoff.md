# T4 drawer workflows

T4 started from the accepted T3 integration commit `e17790b`. The Architect
released #0594–#0599 and the live board recorded their move to In Progress at
revisions 6077–6082. The nine packets in staffing revision 4 delivered their
modules. Revision 8 records the current T4–T6 roster.

| Story | Evidence collected | Remaining qualification |
| --- | --- | --- |
| #0594 Card fields | V1 title and description editors, priority select, and direct owner cycle observed. V2 saved a title with combining Unicode. | Native archive and restore adapters exist. A V1 browser entry has not been established. Verify stale and lost edits, owner and priority, and matched editor states. |
| #0595 Comments | V2 posted a multiline comment using Shift+Enter and Enter. The adapter captures the original ID and validates the immutable receipt. | Verify ambiguous responses, draft recovery across navigation and reload, and posting on an archived or Completed fixture. |
| #0596 Relationships | V1 and V2 saved References to Referenced by. Native tests cover the ordered pair, identity, stale validators, duplicate conflict, symmetric no-op, rollback and priority floor. | Verify lost-response recovery, settled feedback, and follow/edit/remove parity with matched data. A writer fence can leave the full drawer read unavailable until recovery. |
| #0597 Single approval | A disposable proxy held HTTP 201 after the atomic write. The browser closed the tray while the card remained in Ready for Review, then placed the card in Completed after the response. | Verify the animation and keyboard focus on the final build, stale refusal, and recovery of a lost receipt. |
| #0598 Attachments | V1 exposed separate image preview and download links, size, cover and Remove. V2 selected and cleared a cover through the guarded native adapter. | Use valid larger images to qualify thumbnail and board cover behavior. Verify original download, detach history and lost-response recovery. The initial PNG fixture is only evidence for broken preview. |
| #0599 History and cache | V1 closes the drawer on reload and shows a 256 MiB cache default. The V2 cache has principal-scoped LRU accounting and only supplies retained read-only views. | Verify Back/Forward, reload, preference behavior, evictions, and failed refresh or access denial on the integrated build. |

The working browser evidence is in the UI Lead's private `output/t3-v1`
directory under the `t4-` capture prefix. The shared queue receipts record the
V1 observations. The held approval response is recorded in the timing proxy
output. Earlier and working builds support these observations. The final PR
needs a candidate-specific verification record.

T4 remains In Progress during review and qualification. The Architect owns
acceptance and integration before T5 opens.
