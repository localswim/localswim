# localswim

Fresh Go and TypeScript implementation of localswim, beginning with Spike 01.
This repository is under development; no qualified product release is claimed.

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
- [Board, history, lookup and recovery HTTP adapters](internal/transport/boardapi/README.txt)
  and [independent review](docs/reviews/k01-board-http-review.txt), with
  [export and HTTP composition](docs/decisions/export-board-http-composition-2026-10-26.txt).
- [Private browser recovery store](web/src/recovery/README.txt) and
  [independent review](docs/reviews/w01-private-recovery-review.txt), with explicit
  prepared-boundary and qualified-profile dispatch gates.
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

The [credential decomposition](docs/decisions/credential-admission-decomposition-2026-10-26.txt)
records the G01/S04/G02 boundaries. The
[credential composition](docs/decisions/credential-service-composition-2026-10-26.txt)
records G02's service and its native lease ownership. HTTP/browser delivery,
terminal confirmation/private files and integrated qualification remain separate
consumer work. Physical layout 3 includes the accepted-Unicode title constraint
correction and rejects older layouts without migration; logical
API/archive versions remain 1.
