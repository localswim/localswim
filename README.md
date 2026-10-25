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
