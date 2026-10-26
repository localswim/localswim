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
