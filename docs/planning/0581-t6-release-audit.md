# T6 release audit for #0601

The audited product is `43d08076285a3ebe97317fdcbcec3ed556b4bd8e`, tree
`e8abb3151c0877be588f6c3c27624d4c21eb53a9`, the integrated
`origin/ui/v1-clean-room` candidate released for R20. The live card entered
In Progress at revision 6102. This packet records the Git, source, and asset
audit and binds it to the executable used for the integrated run. The integrated
run records the quality gate, fixtures, browser actions, and native readback.

## Git provenance

The PR metadata records all five PRs as merged into `ui/v1-clean-room`. Each
final PR head and its squash commit have identical Git trees. The linked
Architect approval comments name the exact heads. PR #2 changed only the plan.
The T1 baseline on `main` is `e5d01c049b1600c7e51d5fc9988d7999391a3e80`.

| PR | Approved head | Squash commit | Approval |
| --- | --- | --- | --- |
| #1 | `84d73adc1f8f64c8630617375fb39e571fa35684` | `9f9ced3ebf56baf9669c1825d72da44dabadf189` | [T2](https://github.com/localswim/localswim/pull/1#issuecomment-6855564295) |
| #2 | `eec866377345b6c2a36438d05ff56affbe0b63db` | `ac307e5cac3bfe3a10781297d0ff7903aeb47df6` | [Planning](https://github.com/localswim/localswim/pull/2#issuecomment-6855699846) |
| #3 | `575831fc54e8ec950fd3a2d151eef4b0343be636` | `e17790b17f329b4492c85bac9cd4d2bb87dde968` | [T3](https://github.com/localswim/localswim/pull/3#issuecomment-6862127331) |
| #4 | `d11f280fa578c613cf31a01655c884348e4000db` | `b589700e40a4e87a2650b1a4aecc18a7f87af9df` | [T4](https://github.com/localswim/localswim/pull/4#issuecomment-6881945921) |
| #5 | `b71bfd5fd836a41a394103ca58465af5d281c008` | `43d08076285a3ebe97317fdcbcec3ed556b4bd8e` | [T5](https://github.com/localswim/localswim/pull/5#issuecomment-6883247569) |

- The production import graph starts at `web/clean-room/main.ts` and stays
  under `web/clean-room`. The stylesheet entries and font are in that same
  tree. Local source map inputs resolve there and their embedded source text
  matches the authored files. The exact-head evidence describes the
  fresh presentation and V2 API/domain contracts. This establishes recorded
  provenance and the release input boundary. [T2 provenance](0583-0584-t2-handoff.md#verification-and-provenance), [T3 handoff](0581-t3-handoff.md), [T4 handoff](0581-t4-handoff.md), [T5 handoff](0581-t5-handoff.md)
- Inter Variable 4.1 and its OFL match the T2 upstream asset and notice.
  The production bundle's mapped inputs are runtime clean-room modules. The
  prior browser tests remain part of the native quality gate.
- Source inspection found the required clean-room compiler switches, checked
  Node tooling, typed Oxlint rules and canonical Prettier configuration. The
  T2 review established the compiler's duplicate-parameter refusal for the
  lint rule unavailable in Oxlint. The current checkout has `core.hooksPath`
  set to `.githooks` and the formatter filter marked required. Pre-push checks
  the proposed Git object in a temporary checkout. Both the release comparison
  and behavioral suites are wired into `check.ps1`. [TypeScript quality standard](../typescript-standard.md), [T2 hook qualification](0583-0584-t2-handoff.md#verification-and-provenance)

## Optimized assets

The installed tools match the pins in `web/package.json` and its lockfile,
including Windows x64 packages. Node 26.13.0, npm 12.2.1 and Go 1.27.3 were
available in the audit checkout. The web pins are TypeScript 7.0.2, Oxlint
1.90.0, oxlint-tsgolint 7.0.2005, Prettier 3.9.10 and esbuild 0.28.2.

The release pipeline emits strict TypeScript with source information. Esbuild
bundles and minifies JS and CSS with ordinary identifier mangling, preserves
property names and carries legal comments at the end of the output. HTML
processing removes only line-leading indentation before tags in the current
14-line entry. The allowlist in `web/assets.go` embeds exactly six files.
The build compares fresh bytes to those files and emits external maps to
`web/dist/`. [Build script](../../web/build.mjs), [UI release standard](../ui-standards.md)

The following bytes matched the candidate Git blobs, local `web/dist/`, and
responses saved from the exact executable at `http://127.0.0.1:8851`. Gzip
sizes use Node's `gzipSync` as the build does. The Go handler serves the raw
embedded bytes.

| Asset | Raw bytes | Gzip bytes | SHA-256 |
| --- | ---: | ---: | --- |
| `index.html` | 381 | 242 | `1b73bc25022816573932705178c9b0f5a5013c00ed82a51b75131b586278c1fb` |
| `app.mjs` | 192,573 | 61,920 | `7f45862226c46db68ab756b1fe8c0ef3db44e10e4e92253f0e7aac89fd2171d2` |
| `app.css` | 28,024 | 6,189 | `4c880d5ea2679c4c6b608ad909910e88a29cd39a492078be0b12bea3c14306a3` |
| `board.css` | 7,257 | 2,086 | `23fa539191dc0504f23aaf3839c02b654bd62897fbf0519d7f088c992edc8ab8` |
| `inter-variable.woff2` | 352,240 | 351,777 | `693b77d4f32ee9b8bfc995589b5fad5e99adf2832738661f5402f9978429a8e3` |
| `OFL.txt` | 4,380 | 1,969 | `262481e844521b326f5ecd053e59b98c8b2da78c8ee1bdbb6e8174305e54935a` |

Relative to T4, the JavaScript increment is 18,406 raw bytes and 6,114 gzip
bytes. T5 adds picker/paste handling, staged-file and partial-batch UI,
per-tab upload recovery state, and exact-filename multipart encoding. The application
stylesheet grew 1,531 raw and 248 gzip bytes for the new controls and drawer
spacing. The board stylesheet, entry, font and license are byte-identical to
T4. From T2 through T6, JavaScript gzip size rose 11,690 → 39,081 → 55,806 →
61,920 bytes as board interaction, drawer work and uploads were integrated.

The local external maps have nonempty mappings and 32 JS, 16 application CSS
and two board CSS sources. All source references are relative to `web/dist`,
resolve inside `web/clean-room/` and match the current authored inputs. The
copies retained with the served bodies in the private `output/t6` folder have
the same hashes.

| External map | Raw bytes | SHA-256 |
| --- | ---: | --- |
| `app.mjs.map` | 728,262 | `5df178c9d598bdadeb7ea61f4438e4280361e29a91fc52a9ac2f9baa6eb2abc3` |
| `app.css.map` | 57,427 | `1e027831de82374251c543897d5febe48ef1bcc3403cb75496289008b9da50a7` |
| `board.css.map` | 15,069 | `14a523200bc906e532bb6430db54fe64e6108f610f5dd585da79a4c6e0939a24` |

Card and drawer previews use local WebP derivatives sized from the display
bounds and the current device pixel ratio. The drawer caps display at 400×200
and card covers cap height at 120 CSS pixels. The image elements receive
dimensions from the derivative calculation. The full-size drawer link and
download continue to use the original bytes. T4 checked three aspect ratios,
a corrupt image, cover placement and original-download SHA-256. [Preview implementation](../../web/clean-room/drawer/attachment-view.ts), [T4 attachment evidence](0581-t4-handoff.md#exact-candidate-visual-review)

## Runtime and deployment boundary

- The source ZIP byte-matches a fresh `git archive` of the audited commit and
  has SHA-256 `bca549181a09f59f916be648c203cc7d940474bb5f64d8ff8f962910953f4c08`.
  The Go build from that archive is `t6-exact-43d0807.exe` with SHA-256
  `02927abfdacdf17ed2a6fb6a772710551b861cdfddee7b4d7c1aecff37866df0`.
  Its build metadata reports Go 1.27.3, Windows amd64 and `CGO_ENABLED=0`.
  The archive build has no embedded Git revision. Its identity comes from
  the archive comparison and matching served bytes.
- The Lead verified that PID 58080 served the integrated browser and the six
  body captures from the exact executable on port 8851. The reconnect browser
  scenario used the disposable loopback proxy on port 8853 with port 8851 as
  its upstream. Browser headers and recovery results are in the integrated run.
- The single candidate executable serves the workspace, accepted board links,
  assets, API and native CLI. Browser resources come from an explicit route
  allowlist. External maps stay local. The shell and assets use same-origin
  script, stylesheet, font and connection directives. Preview blob URLs are
  allowed for images. The HTTP handler enforces loopback Host and Origin and
  sets no-store, nosniff and same-origin resource policy on browser assets.
  [HTTP handler](../../internal/v2/http.go), [Browser routes](../../internal/v2/browser.go), [Shell header test](../../internal/v2/session_test.go)
- The native production changes since T1 are the browser-serving switch,
  ordered endpoints on relationship seeds/PATCH, and focused `GET /cards/{id}`
  readback for the durable state of a fenced writer. The endpoint changes
  preserve prior request forms and use existing authorization and validators.
  The CLI code change updates `relationship patch` help for the new optional
  ordered pair. Administration, backup, restore and publication remain native
  API/CLI workflows. Source continuity supports retention. Exact-executable
  tests and focused CLI/API readback supply runtime evidence.
  [OpenAPI](../openapi.yaml), [CLI workflows](../cli-workflows.md)
- The Architect updated the browser workflow guide and the native Windows build
  activation procedure in `ad32254`. The Windows runbook documents the
  `service builds` and `service activate` workflows.
  [Browser workflows](../browser-workflows.md), [Windows runbook](../windows-operations.md#activate-a-staged-windows-build), [Observed contract](0582-observed-contract.md)

## Evidence required from the integrated run

1. The integrated run owns the complete `check.ps1` result from the exact
   product source. The source archive, executable metadata, retained maps,
   runtime origin, and six body hashes are verified above.
2. The integrated run owns the served-header checks and binding browser and
   recovery observations to that same process and equivalent V1 fixtures using
   the shared queue for bounded diagnostics.
3. Record authoritative native readback of the browser journey and a focused
   retained CLI/API flow. Use the native regression suite for the broader
   command and API surface. Corrections require exact candidate evidence and
   independent review of the affected scope.

## Candidate release delta

The exact candidate serves the following release bodies. Gzip estimates use
the repository build tool. Each served body matched the tracked asset, and
the headers and all three map refusals were checked on the executable named
in the [qualification handoff](0581-t6-handoff.md).

| Body | Bytes | Gzip bytes |
| --- | ---: | ---: |
| `index.html` | 381 | 242 |
| `app.mjs` | 192721 | 61926 |
| `app.css` | 30219 | 6522 |
| `board.css` | 7605 | 2178 |
| `inter-variable.woff2` | 352240 | 351777 |
| `OFL.txt` | 4380 | 1969 |

The Architect owns R20 acceptance and the integrated workflow demonstration.
Terry owns production approval and the merge to `main`. Qualification runs on
disposable data and an isolated listener. Staging or activating a candidate in
the live installation is a separate owner-controlled action. [PR workflow](0581-pr-workflow.md), [R20 plan](0581-ui-plan.md)
