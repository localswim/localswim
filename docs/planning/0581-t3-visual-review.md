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

## Second pass: additional paired captures

- `v1-long-drawer.jpg` and `v2-long-drawer.jpg` show the same apparent description rendered differently: V1 displays literal hyphen-prefixed lines and plain `#0005`; V2 renders bullets and an underlined ticket link. Background fixtures and lane metadata differ, so confirm the exact stored description before attributing the discrepancy solely to rendering.
- In `v1-search-zero.jpg` and `v1-partial-filter-approval.jpg`, the reorder control remains dark; the corresponding corrected V2 captures show it gray. The screenshots alone do not prove whether either control responds to input.
- The changed-order preview pair has 19 rows in each runtime and confirms the earlier clipping observation: V2's bottom action buttons touch and are clipped by the dialog boundary, while V1's are fully visible. The board histories in the background differ.
- In the pin-keyboard pair, V1 uses filled blue pin icons for the two pinned boards; V2 shows outlined gray pins. Focus surrounds the reorder handle in both captures. Screenshots alone do not establish keyboard ordering or saved state.
- The partial-filter approval captures show one visible review card in each before action and an `Approved 2 review card(s)` toast afterward. The V1 committed frame is transient (`REFRESHING`) and its card animation overlaps; it is not a settled visual comparison. The corrected zero-search and cleared-search captures show the same result text and restored first cards, subject to the shared layout differences noted above.

## Final pass on `d326d01fb425`

- The final V2 captures show filled blue pins, a dark reorder control while filtered, the full reorder footer, and literal description lines with a plain ticket number. The browser measured about 20 px below the footer controls in the scroll position shown.
- The paired empty create forms align closely. The relationship draft captures have different card labels from earlier fixture edits. V2 still shows the disabled attachment control pending #0600.
- The paired reconnect captures preserve the draft. Terry approved waiting for fresh state before enabling V2 submission. The original V2 outage capture predates the `REFRESHING` label correction.
- Human creation captures both show updated ages and the same tab title. V1 shows a creation toast in its frame. V2 has no corresponding toast in the captured state. Fixture histories and timing differ. The Needs Terry heading still wraps differently, and the header has a small vertical offset at this viewport.
