# T2 paired visual review

Reviewed the `t2-final-v1` and `t2-final-v2` JPEGs in the 2026-11-13 visualization output at 1366 × 900 and 390 × 844. These are image observations, not Terry's visual acceptance. Build IDs and clock values naturally differ; capture state and focus can also affect comparisons.

## Concrete differences

- **Desktop populated board typography and density:** In `1366-alpha`, backlog card #0007 wraps to four lines in V1 and three in V2. The subsequent backlog cards likewise occupy less height in V2, exposing more cards. The lane IN/OUT text also breaks at different words. Match the V1 text metrics and available line width, then compare the same cards again.
- **Empty workspace:** In `1366-empty`, the explanatory sentence wraps to two lines in V1 and one in V2; Retry consequently sits about one line higher in V2. Match the text width and vertical placement.
- **Selected board focus appearance:** In `1366-beta` and `390-beta`, V1 shows a rounded blue outline around the selected Beta board while V2 shows a dark, square inset outline. Confirm both screenshots represent the same focus and input state, then match the V1 focus treatment if so.

## Observations and limits

- Desktop header, sidebar boundary and seven lane positions are broadly aligned in the paired frames. The lane controls and card badges are present in the populated desktop captures, but these images alone do not establish exact interaction parity.
- The `390-alpha` V2 image shows `Loading Parity Alpha...` rather than cards; it cannot establish mobile populated-board parity. A settled, same-viewport capture is needed.
- The paired mobile Beta frames differ in the last-card-update line in the header; the V1 frame omits it and the V2 frame shows it. Check equivalent data and timing before classifying this difference.
- `1366-unavailable` has only a V2 image in this set, so the missing-bookmark error state cannot be compared from this evidence. The empty workspace pair is a different state.
- Screenshots do not establish keyboard behavior, controls' actions, recovery, or other uncaptured states. Terry retains visual acceptance.

## Settled re-review

The final captures replaced the earlier files. The initial findings above are retained as history and superseded where noted here.

- Desktop Alpha now wraps #0007 into the same four lines as V1, and the other visible backlog titles and IN/OUT labels match the observed line breaks. Card heights remain slightly different: the lower visible cards rise a few pixels in V2. Lane and sidebar positions, counts, controls and badges appear closely aligned; this is not a pixel-identical finding.
- Desktop empty workspace now wraps the description to the same two lines. The parent measured the body at y=174.35 in V1 and y=170.67 in V2; Retry is correspondingly slightly higher. The same small vertical difference is visible in the newly paired unavailable-bookmark state.
- Beta's selected board outline is now rounded and blue in both frames. The screenshots show small differences in its outline/inset; exact focus state is not independently established from the stills.
- The new `390-alpha` V2 JPEG displays the entire desktop-width board scaled down within the image, while the V1 image shows a narrow viewport with horizontal overflow. This is not a valid matched mobile pair; a correctly sized settled capture is needed. The `390-beta` frames both show the narrow layout. The V2 header includes a truncated update timestamp absent in the captured V1 frame; timing and selection state may explain the difference and require matched-state confirmation.
- Build identifiers and status times reflect separate runtimes. Neither these captures nor this review establish pixel identity, uncaptured interaction parity, or Terry's visual acceptance.

## Narrow Alpha capture closeout

The corrected settled `390-alpha` V2 image now shows the narrow layout: the sidebar occupies approximately 312 px, with only the leading portion of Backlog visible and a horizontal scrollbar, as in the V1 frame. The parent reports a matching 894 px board scroll width from live measurements. This resolves the invalid-capture blocker above. The visible backlog card positions still drift slightly vertically; the V2 update timestamp appears while it is absent in the V1 frame, subject to the timing qualification above. The measured desktop differences remain for Architect and Terry to assess; this is not visual acceptance or a claim of pixel identity.
