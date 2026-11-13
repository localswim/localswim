# T3 visual evidence review

Reviewed the supplied `output/t3-v1` screenshots against `0582-observed-contract.md` on 2026-11-13. This is a screenshot-only review, not an interaction or functional qualification. Captures have differing fixture histories and builds; matching image dimensions alone do not establish matching CSS viewport, scale, or zoom.

## Visible differences

- **Create attachment control:** `create-relationship.jpg` shows an enabled Choose Files control; `v2-create-inverse.jpg` shows it disabled. The selected relationship direction also differs, so the frames are not identical states. Attachment functionality has its own later story; this observation does not establish its intended turn scope.
- **Ticket reorder:** In `v2-ticket-order.jpg`, the bottom edges of Refresh preview, Close and Apply are clipped by the dialog boundary. They are fully visible in `ticket-order.jpg`. The fixtures contain 19 versus 18 rows, so repeat with matching data and the dialog scrolled fully to the bottom.
- **Shared board geometry:** Across the paired board views, including the closely matched Completed hidden/visible captures, V1 wraps NEEDS TERRY to two lines while V2 renders it on one. V2's board/header boundary and lane contents also appear approximately four image pixels higher. Confirm viewport, scale and zoom before assigning precise pixel discrepancies.
- **Drawer metadata:** `drawer-open.jpg` shows `Backlog · fixture - 1`; `v2-drawer.jpg` shows `Backlog`. The fixture data may differ; establish equivalent lane/card metadata before treating this as a rendering defect. The same qualification applies to the RFR drawer pair.

## Evidence boundaries

- Pins, comment search, drawer, create relationship, reorder, approval toast and old Completed visibility have paired captures, but several fixtures differ. In particular, V2's pins/search captures have already moved a review card to Completed, so counts and approval-button state cannot be compared directly with V1. Relationship selections and reorder row counts differ too.
- The Completed hidden/visible pairs show the same displayed old-card toggle and corresponding counts/cards, subject to the geometry differences above. The approval toast wording is visibly the same, but underlying card histories differ.
- `approval-confirm.jpg` does not show a confirmation dialog and there is no corresponding V2 confirmation capture. The provided images alone cannot establish confirmation behavior or mutation correctness.
- V2 has disconnected/reconnected draft frames, but this set has no equivalent V1 reconnect frames. They show the V2 draft text retained across the captured states, not V1 parity or authoritative persistence.
- Screenshots do not establish keyboard behavior, focus restoration, drag/reorder persistence, failure recovery, approval atomicity, or readback. Use matched fixtures and viewport plus interaction and authoritative state evidence for those claims.
