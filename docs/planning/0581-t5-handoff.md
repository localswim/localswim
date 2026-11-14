# T5 attachment ingestion and recovery

The Architect accepted this turn in [PR #5](https://github.com/localswim/localswim/pull/5).
The integrated squash `43d0807` matches the reviewed tree and #0600 is Ready
for Review. The remaining statements below describe the qualification at T5.

The Architect released #0600 on `b589700` after accepting T4 in PR #4.
The live board verified In Progress at revision 6098. T6 remains gated.

The browser picker and paste controls use the existing attachment upload
API. Create files remain in the draft until the card creation receipt is
confirmed. Each attachment has its own original UUID, request correlation,
card and board validators, and exact filename, size and SHA-256. A lost
response preserves its original ID for inspection. Subsequent unsent files
wait for explicit retry, and reload requires the original file contents.

## Evidence

- V1 drawer picker accepts one file. Its create picker accepts several and
  labels each `Added to draft; not yet uploaded`. The disposable V1 captures
  are in the UI Lead's private `output/t5` directory.
- V2 browser create #0042 staged two originals, created the card, and displayed
  both attachments. The CLI verified exactly one of each filename and matching
  SHA-256. See `v2-create-staged-working.jpg` and `v2-create-uploaded-working.jpg`.
- V2 browser create #0043 staged three originals. The loopback proxy committed
  the second upload then truncated its response, leaving the third unsent.
  Reload restored the recovery controls. Checking the second upload read the
  original UUID, and the third uploaded only after original-file reselection
  and explicit Retry. The proxy saw three upload POSTs and the CLI verified
  three distinct stored attachments with the original hashes. See
  `v2-partial-unknown-working.jpg`, `v2-partial-recovered-working.jpg`, and
  `browser-uploads-queue.json`.
- Four focused tests exercise the upload and reload journal, mismatched or
  missing original lookup, file reselection, storage failures, cancellation,
  and explicit resume. The existing late-create tests remain enabled.
- The upload transport uses the existing multipart parser's extended UTF-8
  filename parameter. It percent-encodes the header value and preserves
  the original filename and file bytes. Browser adapter and native HTTP
  tests cover decomposed Unicode, quote, backslash, CR/LF and NUL.

- On the rebuilt candidate, native image paste staged a PNG in create and
  preserved ordinary pasted description text. Cancel created no card. Pasting
  in the existing drawer committed one `clipboard.png` with the same SHA-256
  as the source image. See `v2-native-paste-final.jpg`,
  `v2-drawer-paste-final.jpg`, and `native-paste-final-queue.json`.
- The final build matches V1's generated `screen-clip-<UUID>.png` label for
  unnamed pasted PNGs. The paired crop images use the same 1707×1262 Chrome
  viewport and the same focused card or staged PNG. Terra / Medium found no
  material difference in attachment controls, previews or staging layout.
  See the `v1/v2-final-dialog-crop.jpg` and `v1/v2-final-drawer-crop.jpg`
  pairs and the manifest in `output/t5`.
- A spacing retest measured the drawer heading, picker and helper within
  0.2 CSS pixels of V1 with the first preview position unchanged in V2.
  The create fields measured within 0.5 CSS pixels. Corrected crops are
  `v2-spacing-drawer-crop.jpg` and `v2-spacing-dialog-crop.jpg`.

The two create-upload runs predate final corrections for storage failure,
recovered receipt presentation and busy reselection. Those cases have
focused regression or source review coverage. The Architect owns
acceptance and integration before T6 can begin.
