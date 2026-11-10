# localswim

## V2 release direction

V1 proved the product. V2 is a fresh implementation of the same local app.
Use V1 as the reference for how it looks, feels, and works. Write V2's code from
that behavior. Match the board and workspace UI, keyboard controls, drafts,
search, review, Attention, and the CLI and integrations people rely on. Preserve
permissions, audit history, and reliable saves and recovery for V2 projects.

### One port and one API

V2 runs one loopback HTTP service on one port. That service serves the browser
UI, the workspace and its board catalog, and every board. Board identity belongs
in the URL. The service handles each request directly, without per-board
listeners or a gateway that forwards requests to other local ports.

Use a resource-based REST API for the full V1 feature set. For example,
`GET /api/boards` lists boards, `POST /api/boards` creates one, and
`/api/boards/{boardId}/cards/{cardId}` identifies a card. Give comments,
attachments, and other related objects their own resources. The browser and CLI
use the same API and apply the same rules for edits, permissions, and revisions.

### Fresh projects, side by side

Run V2 alongside V1 with its own port and data directory. Existing projects
stay in V1. Start new projects in V2 when it is ready. Import, format conversion,
and moving live projects between versions are outside the release scope.

### Build and ship quickly

Get a runnable app in hand on day one. Create a new board, open it in the
familiar workspace, add and move cards, and restart with everything intact.
Aim for a local deployment candidate on day two. Close parity gaps in the
running app and compare real browser and CLI workflows with V1 as we go.

Keep the implementation small and keep one authoritative writer for each board.
Run the normal checks and add focused verification where mistakes can lose
edits, break approvals, or prevent recovery. Keep M19 and the old milestone
schedule as historical evidence. This section supersedes the earlier
[import and cutover proposal](docs/RELEASE.md).

## Implementation history

The sections below record earlier slices and their original evidence boundaries.

M18 adds a deliberate installed-version check and generation-bound Stop through
both the ordinary browser and actual CLI. Follow the
[identity and Stop guide](docs/runbooks/m18-identity-checks.txt) for the selected
board, readable and JSON results, and unknown-outcome recovery. A version is
reported only after the actual command returns a qualified identity and joins.
The [reviewed model](docs/decisions/m18-identity-probe-contract-2026-11-08.txt)
and [acceptance inventory](docs/examples/m18-identity-probe-acceptance.json)
keep capability, readiness and delivery as separate work.

The [current qualification](qualification/m18/joined-r1.txt) and
[exact extracted review kit](qualification/m18/HANDOFF.txt) bind the reviewed
source, reproduced executable and fresh ordinary demonstrations.

M17 adds deliberate installation inspection through the
[ordinary browser](docs/runbooks/m17-installation-browser.txt) and
[actual CLI](docs/runbooks/m17-installation-cli.txt). It reports a current
candidate, absence from the supported installation location, or qualification
not established. Inspection runs no installed command and establishes no
version, capability or delivery readiness. The
[inspection contract](docs/decisions/m17-installation-inspector-contract-2026-11-08.txt)
records the supported managed installation and current-authority checks.
[Current qualification](qualification/m17/joined-r1.txt) records the complete
candidate evidence. The [exact extracted-kit handoff](qualification/m17/HANDOFF.txt)
records the package and its fresh browser/CLI demonstration.

M16 adds one fixed private recipient policy through the ordinary browser and
[actual CLI](docs/runbooks/m16-recipient-cli.txt). Save once, reopen and inspect
the exact binding; equivalent replacement requests refuse. The
[browser guide](docs/runbooks/m16-recipient-browser.txt) covers private input,
current scope and ambiguous outcomes. Saving does not activate delivery.
[Exact review-kit handoff](qualification/m16/HANDOFF.txt) and
[current qualification](qualification/m16/joined-r1.txt) record the verified
configuration scope; executable probes and external delivery remain later work.

The M09 [card-file reader](docs/runbooks/m09-card-files.txt) adds selected-card
current/retained metadata, source-based attribution, bounded paging and verified
downloads through the ordinary browser and actual CLI. Its delivery evidence is
maintained separately from the remaining upload and detach lifecycle work.
The [upload guide](docs/runbooks/m09-upload.txt) covers captured file identity,
explicit archived-card selection and retained input through failed submissions.
The [detach guide](docs/runbooks/m09-detach.txt) explains deliberate removal,
retained evidence and the explicit layout10-to11 upgrade.

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
The [workspace CLI](docs/decisions/workspace-cli-selection-2026-10-28.txt) adds
explicit project/board selection and controls through the real executable.
The [private browser context](docs/decisions/workspace-private-context-2026-10-28.txt)
retains board/window input and original uncertainty. The
[Go catalog](docs/decisions/workspace-catalog-2026-10-28.txt) adds authorized counts,
activity and bounded refresh. The [ordinary workspace browser](docs/decisions/workspace-browser-navigation-2026-10-28.txt)
adds its chooser/sidebar, safe routes and automatic refresh.
[Shared pins](docs/decisions/workspace-shared-pins-2026-10-28.txt) persist manual
order without sharing active-window input or layout. The
[joined synthetic qualification](qualification/workspace/README.txt) records the
M01 workspace evidence and its exact candidate/source bindings. Follow the
[startup, use and recovery guide](docs/runbooks/m01-workspace-start-use-recovery.txt)
and [customer demonstration](docs/runbooks/m01-customer-demo.txt) for the bounded
M01 handoff. Its evidence covers that workspace slice. Broader workflow and
full parity were still open at the time.

The Go service, CLI, domain rules and contract generator live in one Go module.
The [M02.02 details/history reader](docs/decisions/m02-details-history-2026-10-29.txt)
adds a nonmodal card reader, local comment/history pages and readable focused CLI
facts, with strict selected-card reference checks and honest stale/coverage states.
It preserves the existing protocol and private editors; later M02 work remains.
The [M02.03 editor controls](docs/decisions/m02-daily-edits-2026-10-29.txt) add
focused Rename, single-line Enter and explicit dirty-editor discard, retaining
exact untouched source text and existing no-op/conflict/original-outcome rules.
The [keyboard journey](docs/runbooks/m02-keyboard-read-edit.txt) and
[real CLI guide](docs/runbooks/m02-cli-read-edit.txt) join those controls and
commands into ordinary read/edit use, preserving current authority and outcomes.
The [M02 startup/use/recovery guide](docs/runbooks/m02-start-use-recovery.txt) and
[customer demonstration](docs/runbooks/m02-customer-demo.txt) support the bounded
handoff. The [joined qualification](qualification/m02/README.txt) records exact
candidate bindings and distinguishes normal, controlled, fault and reused evidence.
The [continual process improvement record](docs/continual-process-improvement.md)
captures milestone observations and the changes to evaluate at each later review.

The [M03 card archive slice](docs/decisions/m03-card-archive-2026-10-29.txt)
adds reversible archive/unarchive for unfinished cards, default active rows,
explicit archived inspection and a bounded CLI listing. Follow the
[card archive guide](docs/runbooks/m03-card-archive.txt) for browser/CLI use,
explicit revisions and recovery. Complete JSON board snapshots retain hidden
link targets; archive visibility is not a security boundary or ZIP export.
Completed remains read-only. The [current-card search slice](docs/decisions/m03-card-search-2026-10-29.txt)
adds literal matching, typed filters and per-window query context; follow the
[find/filter guide](docs/runbooks/m03-card-search.txt) for explicit pages and stale
continuation recovery. Joined milestone qualification and final handoff remain
separate M03 stories; no general release is claimed.

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
uses Node26.13.0 and npm12.2.2, with TypeScript7.0.2 and Playwright1.65.0.
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
consumer work for that legacy path. Physical layout6 added hierarchy to the
layout5 assignment/Unicode/outcome base. Layout7 added typed relationships;
current physical layout8 adds immutable configured workflows while the board's
base-schema marker stays5. Normal startup refuses older layouts; explicit offline
5-to-6, 6-to-7 and 7-to-8 steps preserve existing records and never run automatically. API/archive/fingerprint versions stay1 with strict
additive fields and kinds. See [typed relationships and storage compatibility](docs/runbooks/m05-relationships.txt)
and the earlier [hierarchy guide](docs/runbooks/m04-hierarchy.txt). Earlier M04
qualification and kit reports retain their exact recorded physical6 candidate;
general-release qualification is not implied.

The M05 relationship feature adds Related to, References and Duplicates beside
the existing blocking/informational links, with reciprocal exact-target views
from one owned fact. Browser and actual CLI can create a card with a parent and
relationships atomically, or replace its complete owned set while retaining
incoming relationships. Complete reads use local20-row presentation pages;
priority/lane/owner/approval do not propagate. Follow the
[relationship guide](docs/runbooks/m05-relationships.txt) for deliberate saves,
owner-correct removal, the100-entry batch boundary and original UNKNOWN/no-replay
recovery. Complete joined qualification and final kit remain separate M05 stories.

Configured new-board workflows are available in native setup, the actual CLI and
the browser controls slice. The [configured workflow guide](docs/runbooks/m06-configured-workflows.txt)
covers native and CLI creation, reads, moves, reopening and recovery; the
[browser workflow guide](docs/runbooks/m06-browser-workflows.txt) covers the new
board form and configured card controls. Joined M06.04 qualification and the
M06.05 kit remain separate work; no full release is claimed.

The M07.02 native/CLI slice adds existing-workflow show, preview and deliberate
edit, retained policy history, compatible state pins and explicit physical8-to9
upgrade. The [workflow-change guide](docs/runbooks/m07-workflow-changes.txt)
covers these commands, rights, limits and original-outcome handling. Ordinary
browser workflow editing belongs to M07.03, with full joined qualification and
the extracted-kit handoff in M07.04/.05. Category-based Attention remains M08.

M08.02 adds category assignments within retained workflow policies, current
single-board Attention reads and the explicit physical9-to10 upgrade. The
[category configuration guide](docs/runbooks/m08-category-configuration.txt)
covers actual CLI preview, edit, observation, compatibility and recovery.
The [Attention catalog guide](docs/runbooks/m08-attention-catalog.txt) covers
M08.03 workspace category projection, observation states and read recovery.
Browser controls follow in their M08 slice.

Read all registered boards with `localswim --workspace DIR [--json] workspace catalog`.
The catalog reports current policy context and separate Blocked, Needs Terry and
Ready for Review counts, including disabled and unavailable states.
