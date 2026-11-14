# T6 integrated qualification

The Architect released T6 after squash-merging T5 in PR #5. The exact
baseline is `43d08076285a3ebe97317fdcbcec3ed556b4bd8e` with tree
`e8abb3151c0877be588f6c3c27624d4c21eb53a9`. #0601 entered In Progress at
revision 6102. [Coverage](0581-t6-coverage.md) · [Release audit](0581-t6-release-audit.md)

## Integrated run

| Scope | Result |
| --- | --- |
| Workspace and board entry | V1 and V2 selected empty Beta by keyboard at 1366×900 and 390×844. The matching empty lane data and focus were verified. Alpha's background attention differs between fixture histories. |
| Mutable journey | Paired Chrome at 1707×1262 CSS pixels and DPR 1.25. V1 #0029 and corrected V2 #0047 created in Ready for Review with an atomic incoming reference from #0002. One staged native-pasted PNG uploaded after each create. Both returned to the board. The keyboard-opened drawer displayed the attachment and accepted a comment and approval. Native readback verified Completed, both comments, the ordered relationship and byte-identical 4,418-byte PNGs. Alpha carries different unrelated history. |
| Keyboard and focus | Keyboard create without files returned focus to the document in both versions. Keyboard open and approve returned focus to the Completed opener on V1 #0029 and corrected V2 #0046. V2 opener focus was measured in its shadow root. |
| Disconnect and draft | Both Alpha views matched the same search result and retained the same draft across a disposable API and stream interruption. V1 left Add card enabled. As approved by Terry, V2 disabled Add card and restored it after confirming a current board revision. Readback found zero commits for the test draft. |
| Native and release | Native triage order returned successfully. All six candidate assets matched tracked bytes and served the expected media types, CSP, no-store and nosniff. Each of the three external map routes returned HTTP 404. The matching maps are retained privately. |
| Quality gate | The baseline passed `check.ps1` with 315 web tests. The corrected product passed `check.ps1` with 316 web tests. The tightened create regression also fails against the previous implementation at the route assertion. |

The exact baseline executable was built from a Git archive. It is
`t6-exact-43d0807.exe`, SHA-256
`02927abfdacdf17ed2a6fb6a772710551b861cdfddee7b4d7c1aecff37866df0`.
Primary, paired and native observations use disposable fixtures, with V2
direct on port 8851 or through the reconnect proxy on 8853. Evidence and
the candidate manifest are in the UI Lead's private `output/t6` directory.

The paired write, approval, reconnect and response-loss runs used
`t6-verified.exe` (SHA-256
`c4483f984088f58946d124f0b6bd48bdbd93056395a58207d95ec74b7fc37a26`).
The final narrow-search comparison used
`t6-exact-candidate.exe` (SHA-256
`1b6b7c556e74d5f6914e6ff2cf9ba05b52a6adf564c2771bfce961a1c28b331f`).
That build adds header markup and layout only. The final lane-heading sweep
used `t6-review.exe` (SHA-256
`2df80117b28fd07095b1a19cc385e67f6e5962da6f7e1487360da9af57e57ef8`).
It adds the measured board title CSS. The final transitional comparison
and release audit used `t6-candidate-final.exe` (SHA-256
`a1064acfdb55319c6a334fd8066b243aa7c670fb9d7fe3e1e3a4e1da2f74924b`).
Its only product delta is the transitional header and search layout.
The manifest maps captures and readback to their corresponding executables.

## Scoped corrections

The empty Beta desktop comparison found Needs Terry wrapped to two lines
in V2 at a title width of 116.85 CSS pixels. V1 used one line. The Architect
authorized #0602 and the live board verified it In Progress at revision 6105.
It blocks #0601 acceptance.

The correction adjusts title padding and width in the measured Needs Terry
lane ranges. At 1366×900, both title boxes measure about 117 pixels and render
one line. At 1280×720, both measure about 105 pixels and render two lines.
The final empty Beta sweep at 900 pixels high matched line count at all 13
sampled widths from 1280 through 1600, including the second two-line interval
verified at 1434, 1440, 1500 and 1532 pixels. Independent source review
verified scope and generated CSS.

#0603 matched the two desktop header breaks with the search empty and with
results visible. At measured widths of 1280, 1282, 1366, 1400, 1402, 1440 and
1442 CSS pixels, desktop header geometry matched within 0.6 pixels. V1 and V2
used a 120-pixel search field at 1280 and 190 pixels through the larger desktop
samples. The runtime viewport rounded odd requested widths to even measurements.
At 320, 390 and 440 pixels, the mobile input stays 120 pixels with zero, one or
multiple results. Both versions wrap the connection status at 320 without
shifting the board title. At 390 and 440, their status positions matched within
0.1 pixels with one or multiple results and 2.7 pixels with zero results.
Fresh review verified the CSS boundaries and row isolation. The transitional
layout keeps the query on the first row from the measured 444-pixel state
through 680 pixels and wraps the result count and connection status as space
runs out. At 560 pixels, both versions show the first-row search and the
second-row connection status. The result count and status rows match at the
498-pixel and 628-pixel one-result edges, the 638-pixel zero-result edge,
and both adjacent samples for each edge. These comparisons use the same search
text and DPR 1.

#0604 returned ordinary confirmed creates to the board, including when an
attachment upload continues. Existing original-ID inspection and recovery
remain available. The corrected-build response-loss fixture committed #0048 once
and inspected the original UUID. The regression suite covers pending board
navigation and principal changes. Fresh independent review cleared the change.

The Architect's documentation-only commit `ad32254` updated stale workflow
and service activation instructions. The Architect retains T6 acceptance and
integration. Terry retains final demo, Completed, main merge and cutover.
