# #0582 independent visual evidence review

Reviewed the written observation packet and all eleven manifest images on
2026-11-13. This is a review of the V1 reference packet, not a V2 parity review
or authorization to open T2. No browser, V1 implementation or prior V2
presentation was inspected.

## Findings

- **Image metadata (resolved; see closeout below).**
  The originally named `0582-v1-empty-workspace.png` is 1800 × 1262, not 1800 × 1318 as the
  manifest's global dimensions and the reproduction section imply. The other
  ten images are 1800 × 1318. All eleven files contain JPEG data despite their
  `.png` extensions. Record actual dimensions and encoding per image, or
  accurately qualify the global metadata. The empty-workspace frame cannot be
  assumed to match the other frames' viewport.
- **The reference is limited to one known image size, not a qualified browser
  configuration.** CSS viewport, device scale and zoom were not captured. The
  packet correctly acknowledges this. Reproduce and record them for both V1 and
  V2 before a matched comparison; image-pixel estimates are not CSS dimensions.
- **Loading has no visual reference.** L01 is an accessibility observation only.
  #0583 needs a captured rendered loading state, including header, sidebar and
  content placement, before its loading appearance can be compared.
- **Static frames do not independently prove transitions.** S09 and S10 show
  the same lower Backlog position, but do not by themselves establish the Back
  sequence or exact retained offset. S01/S02 and S11 similarly cannot prove URL,
  selection or recovery actions from pixels. Focus, Escape and cancellation
  claims require the recorded interaction observation rather than the images.
  Repeat and record the relevant action and result during matched T2 testing.

## What the images support

The populated, empty Alpha and empty Beta frames support the seven-lane order,
selected sidebar row, fixture counts and differing create-button visibility.
The filter frame supports one visible matching card with `1 found` and unchanged
sidebar attention badges. The collapsed, drawer and create frames support the
reported broad placement and visible controls. The scrolled frame shows a
lower Backlog position while the lane headers and other visible columns remain
in place. Empty-workspace and unknown-bookmark frames show distinct explanatory
text and Retry within the retained shell. No material contradiction was found
in those visible descriptions.

In `0582-v1-create.jpg` the dialog is centered on the full image, including the
sidebar, rather than in the remaining board area: approximately x=590–1210
and y=247–1073 in the 1800 × 1318 capture. Its white panel has a distinct
header and bottom action bar; the backdrop dims the header and board while the
sidebar remains undimmed. These are image-pixel observations, not CSS values.

## T2 observation needs

- Capture matched V1/V2 entry states with explicitly recorded CSS viewport,
  scale and zoom, equivalent fixture data and runtime context. Include populated,
  empty-board, empty-workspace, loading and unknown-bookmark states.
- Reproduce board selection, direct bookmark and recovery; observe and record
  focus, URL and visible feedback as actions, not inferred from stills.
- Compare board geometry, card wrapping and independent lane scrolling at the
  same viewport, including the observed Back restoration sequence. Observe any
  additional viewport or long-label states before claiming their behavior.
- Treat the per-story table as a future observation backlog, not qualification
  of later workflows. Terry's integrated visual confirmation and the Architect's
  turn gates remain separate from this review.

## Metadata closeout

Verified all eleven corrected `.jpg` files against the revised manifest: actual
JPEG encoding, dimensions and SHA-256 hashes match each entry. The contract now
states the 1800 × 1262 empty-workspace exception. The separate viewport, scale
and zoom limitation and the remaining observation needs above still apply.
