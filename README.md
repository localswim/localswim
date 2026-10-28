# localswim

Fresh Go and TypeScript implementation of localswim, beginning with Spike 01.
This repository is under development; no qualified product release is claimed.

The current [localhost scope reset](docs/decisions/localhost-scope-reset-2026-10-27.txt)
returns V2 to the V1 product baseline: both browser UI and CLI use the same
loopback-only HTTP Go service, backed by SQLite. TLS, certificate enrollment and
manual login/delegation ceremonies are outside V2. Human/bot approval boundaries
and SQLite durability remain. The [local runtime](docs/decisions/local-runtime-composition-2026-10-27.txt)
implements ordinary startup; complete browser/archive/release qualification still
follows. [Assignment and runnable local restore](docs/decisions/assignment-and-local-restore-2026-10-27.txt)
complete required V1-style outcomes with explicit qualification limits. Earlier
HTTPS/setup documentation below describes legacy components.

Spike 02 adds the [workspace runtime bridge](docs/decisions/workspace-runtime-bridge-2026-10-28.txt):
independent listener-free boards, one local host, scoped sessions and compatible
`--data` connections. The [explicit registry and lifecycle](docs/decisions/workspace-registry-lifecycle-2026-10-28.txt)
adds `serve --workspace`, persistent membership, create/register/status and reopen.
Workspace CLI selection and browser navigation remain subsequent packages;
these components are not a finished workspace application.

The Go service, CLI, domain rules and contract generator live in one Go module.
The browser is a separate TypeScript package under `web/`; the executable will
serve its verified embedded assets. Shared contracts and assets change together.

The accepted [Spike 01 architecture and staged plan](https://github.com/localswim/v2-architecture/tree/4e544e63a8db30dc5cc1d2c2f8bbaa002863508b/docs/slice01)
defines the implementation and qualification gates. Initial work freezes native
setup and archive/recovery encodings and selects exact toolchain candidates.
Candidate manifests do not establish compatibility or runtime qualification.

All localswim repositories are public, MIT-licensed FOSS. Source text is UTF-8
with Unix-style LF and a final newline; binary fixture bytes remain exact.
Real boards, credentials and private operational/recovery records stay outside
the repository. Tests use synthetic fixtures.

The current [JavaScript build/test toolchain](qualification/toolchain/README.txt)
uses Node26.12.1 and npm12.2.1, with TypeScript7.0.2 and Playwright1.64.1.
These are development/qualification tools; the packaged Go application remains
self-contained. Earlier qualification records retain their original tool pins.

## Spike 01 preparation

- [Native setup and credential contract](docs/contracts/setup.txt), with its
  [independent closure review](docs/reviews/d01-setup-review.txt).
- [Archive and recovery encodings](docs/contracts/archive-recovery-schema.txt),
  [synthetic vectors](fixtures/contracts/archive-recovery/README.txt), and
  [independent closure review](docs/reviews/d02-schema-review.txt).
- [Exact candidate environment](qualification/environment.json), Go/npm locks,
  and [independent candidate review](docs/reviews/q01-candidates-review.txt).

These proposed freezes require owner acceptance before their dependent tasks.
Candidate integrity and documentary fixture checks are distinct from the later
native SQLite, compiler, browser, filesystem and product qualification evidence.

## Isolated qualification

- [SQLite connection harness](qualification/driver/README.md) and its
  [independent native review](docs/reviews/q02-driver-qualification-review.txt).
  The recorded runner host is PowerShell 7.6.9; see its replay boundary and limits.
- [Go/TypeScript/embed harness](qualification/compiler/README.txt) and its
  [independent native review](docs/reviews/q03-compiler-qualification-review.txt).
- [Module completeness addendum](docs/decisions/go-module-completeness-2026-10-25.txt)
  records required indirect declarations without changing selected versions or checksums.

These results cover the documented isolated Windows samples. Owner acceptance
still gates the dependent product tasks; product recovery, browser, fault and
release qualification remain separate work.

## Foundation libraries

- [Mutation/canonical contract core](internal/contract/README.txt) and
  [independent review](docs/reviews/c01-contract-foundation-review.txt).
- [Storage schema/open boundary](internal/store/open/README.md) and
  [corrected independent review](docs/reviews/s01-storage-foundation-review.txt).
- [Atomic writer and outcome ledger](internal/store/writer/README.txt) and
  [independent review](docs/reviews/s02-writer-outcome-review.txt), with explicit
  authority/admission callbacks and commit uncertainty handling.
- [Grant admission foundation](internal/board/grants/README.txt) and
  [independent review](docs/reviews/g01-grant-admission-review.txt), with an
  [owner attribution proposal](docs/decisions/native-owner-attribution-bootstrap-proposal-2026-10-26.txt).
- [Durable credential and native-owner service](internal/board/credentials/README.txt)
  and [independent review](docs/reviews/g02-credential-service-review.txt), including
  explicit first-owner attribution and current credential verification.
- [Card command admission](internal/board/cards/README.txt) and
  [independent review](docs/reviews/b01-card-admission-review.txt), with the
  [accepted no-change policy](docs/decisions/card-edit-no-change-policy-2026-10-26.txt).
- [Link, handoff and approval admission](internal/board/workflow/README.txt)
  and [independent review](docs/reviews/b02-workflow-review.txt).
- [Recovery preview, retention and byte pins](internal/store/recovery/README.txt)
  and [independent review](docs/reviews/s03-recovery-retention-review.txt), with
  [composition boundaries](docs/decisions/recovery-retention-composition-2026-10-26.txt).
- [Attachment storage and forced downloads](internal/board/attachments/README.txt)
  and [independent review](docs/reviews/a01-attachment-adapter-review.txt), with
  [byte/transport composition](docs/decisions/attachment-byte-composition-2026-10-26.txt).
- [Pinned export snapshots and stored ZIP output](internal/archive/export/README.txt)
  and [independent review](docs/reviews/a02-export-snapshot-review.txt).
- [Offline archive validation](internal/archive/validate/README.txt) and
  [independent review](docs/reviews/a03-offline-package-review.txt).
- [Separate native restore and status](internal/archive/restore/README.txt),
  [publication guards](internal/archive/publication/README.txt) and
  [independent review](docs/reviews/a04-restore-publication-review.txt).
- [HTTP export delivery](internal/transport/exportapi/README.txt) and
  [independent review](docs/reviews/h02-http-export-review.txt).
- [Board, history, lookup and recovery HTTP adapters](internal/transport/boardapi/README.txt)
  and [independent review](docs/reviews/k01-board-http-review.txt), with
  [export and HTTP composition](docs/decisions/export-board-http-composition-2026-10-26.txt).
- [Private browser recovery store](web/src/recovery/README.txt) and
  [independent review](docs/reviews/w01-private-recovery-review.txt), with explicit
  prepared-boundary and qualified-profile dispatch gates.
- [Core CLI and immutable local evidence](internal/transport/cli/core/README.txt)
  and [independent review](docs/reviews/k02-cli-evidence-review.txt).
- [Administrative CLI](internal/transport/cli/admin/README.txt),
  [attachment/archive commands](internal/transport/cli/archive/README.txt) and
  [independent review](docs/reviews/k03-admin-archive-cli-review.txt), using the
  [approved CLI bindings](docs/contracts/k03-cli-bindings.txt) and
  [shared provider composition](docs/decisions/admin-archive-cli-composition-2026-10-26.txt).
- [Board forms and request outcomes](web/src/board/README.txt) and
  [independent review](docs/reviews/w02-board-interaction-review.txt), with
  [archive/client evidence boundaries](docs/decisions/archive-client-evidence-composition-2026-10-26.txt).
- [Recovery, session and archive controls](web/src/recovery-ui/README.txt) and
  [independent review](docs/reviews/w03-recovery-attachment-review.txt), with
  [restore and recovery UI boundaries](docs/decisions/restore-recovery-ui-composition-2026-10-26.txt).
- [Grant HTTP adapters](internal/transport/grantapi/README.txt) and
  [independent review](docs/reviews/h01-grant-http-review.txt), using the
  [shared provider facades](docs/decisions/workflow-grant-provider-facades-2026-10-26.txt).
- [Session and HTTPS boundary](internal/transport/session/README.txt) and
  [independent review](docs/reviews/t01-https-session-review.txt), including the
  [session provider adapter](docs/decisions/session-native-scope-adapter-2026-10-26.txt).
- [Native setup/lifecycle core](internal/transport/setup/README.txt) and
  [independent review](docs/reviews/n01-native-setup-review.txt).
- [Proposed public transport encodings](docs/contracts/transport-envelopes.txt)
  and [corrected independent review](docs/reviews/d03-transport-envelope-review.txt).
- [Archive/preview/receipt contract library](internal/contract/archive/README.txt)
  and [independent review](docs/reviews/c03-archive-contract-review.txt), including
  Go-owned generated schema/declarations and shared history-reference helpers.
- [Public transport contract library](internal/contract/transport/README.txt)
  and [independent review](docs/reviews/c02-transport-contract-review.txt), covering
  terminal/read/refusal and session/credential boundaries with shared archive rows.
- The explicit Windows physical CurrentUser Root provider is described in the
  [native setup library](internal/transport/setup/README.txt) and its
  [independent provider review](docs/reviews/n02-windows-trust-provider-review.txt).

The [contract decomposition](docs/decisions/contract-foundation-decomposition-2026-10-25.txt)
records the bounded transport and archive DTO bundles. Both now join the mutation
core in the aggregate contract generator. The
[native trust-provider boundary](docs/decisions/native-trust-provider-boundary-2026-10-25.txt)
records the explicit concrete adapter and remaining live-store qualification.
These libraries do not yet constitute the integrated application or complete
service/CLI/bootstrap/browser/recovery qualification.

The [thin executable](cmd/localswim/README.txt) composes the existing service and
CLI adapters using [explicit runtime bindings](docs/contracts/runtime-bindings.txt).
[Declared embedded assets](internal/assets/README.txt) are staged and checked by
[assetstage](cmd/assetstage/README.txt). See the
[P01 independent review](docs/reviews/p01-executable-assets-review.txt) and
[composition boundaries](docs/decisions/executable-assets-composition-2026-10-26.txt).
The [web package scripts](web/scripts/README.txt) provide locked compiler and
pinned browser-launcher wiring. See the
[P02 independent review](docs/reviews/p02-package-scripts-review.txt) and
[script boundaries](docs/decisions/package-script-wiring-2026-10-26.txt).
The [two clean distribution builds](qualification/build/README.txt) reproduce the
recorded frozen source, generated files, assets and executable; see the
[Q04 independent review](docs/reviews/q04-clean-builds-review.txt).
The [staged native transaction/fault suite](qualification/native/transaction/README.txt)
records real Windows cases and labeled private fault injections; see the
[V01 independent review](docs/reviews/v01-native-transactions-review.txt). Interrupted
precommit restart safely refuses the retained journal and preserves UNKNOWN;
automatic hot-journal recovery is outside the current storage contract.
Browser, archive and root-dispatch qualification remain separately gated.

The [credential decomposition](docs/decisions/credential-admission-decomposition-2026-10-26.txt)
records the G01/S04/G02 boundaries. The
[credential composition](docs/decisions/credential-service-composition-2026-10-26.txt)
records G02's historical service and its native lease ownership. HTTP/browser delivery,
terminal confirmation/private files and integrated qualification remain separate
consumer work for that legacy path. Current physical layout5 adds nullable card
assignment while retaining accepted Unicode and outcome/gap representation.
Older layouts, including4, are refused without migration. Logical API/archive
versions remain1 with optional ownerId compatibility.
