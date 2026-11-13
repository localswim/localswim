# #0582 clean-room interface boundary

## Scope and status

R01 supplies freshly authored declarations and disposable fixture seeds for the
independent R02 workspace and R03 board authors. The UI Lead supplies the rendered
V1 observation ledger and integrates these contracts with that evidence.
The Architect reviews the complete R01 package before opening T2.

- `web/clean-room/api-types.ts` declares the required native V2 read DTOs.
- `web/clean-room/contracts.ts` defines component, read-state, navigation, draft
  and operation-result boundaries.
- `web/clean-room/fixtures.ts` supplies independently constructed data for
  populated, empty-workspace, empty-board, loading, unavailable and retained
  unavailable states.

Runtime component and HTTP behavior begins with T2. Compilation, formatting,
lint and full `check.ps1` qualification belong to the UI Lead's integration lease.
R01 author inspection covers source and contract consistency.

## Source and provenance

The author inspected the current V2 OpenAPI contract, accepted API ADR,
TypeScript/UI standards, plan, and the listed V2 backend files. Git metadata
establishes the backend's independent V2 origin. Fresh TypeScript declarations
preserve wire names, nullability, enumerations and revision fields.

| Contract or operation | Evidence | V2 origin |
| --- | --- | --- |
| Wire DTOs and timestamps | `docs/openapi.yaml`, `docs/adr/0001-api-contract.md`, `internal/v2/model.go`, `internal/v2/wire.go` | `c302ca1`, `bc8ee24` |
| Actor session | `internal/v2/session.go` | `c0e0f7b` |
| Workspace preferences and board summaries | `internal/v2/workspace.go`, `internal/v2/catalog.go`, `internal/v2/boards.go` | `bc8ee24`, subsequent V2 commits including `7548c0d` |
| Resource routes and board read composition | `internal/v2/routes.go`, `docs/openapi.yaml` | `bc8ee24`, subsequent V2 API commits |
| Lane approval, future R13 adapter | `internal/v2/approvals.go` path creation metadata and OpenAPI `/approvals` | `139ad2c` |
| Ordering and priority batches, future R14/R15 adapters | `internal/v2/ticket_order_apply.go`, `internal/v2/batch_priority.go` path creation metadata and OpenAPI operations | `7548c0d`, `0ab4dcd` respectively |

Reuse the V2 HTTP/domain operations at those boundaries. Each new UI feature
authors its own adapter and validates untrusted JSON before presenting these
types. Type aliases for UUIDs, timestamps and entity tags document wire meaning.
Their runtime format and numeric bounds require adapter validation.

## Ownership and composition

| Owner | Files and responsibility |
| --- | --- |
| UI Lead | `web/clean-room` shared contracts, root composition, browser session, adapters, routing/history, per-board retained view/draft state, shared entry/build integration |
| R02 workspace author | New `web/clean-room/workspace/` files, workspace DOM and board-slot host, selection intent and workspace scroll reporting |
| R03 board author | New `web/clean-room/board/` files, board DOM, lane/card presentation, board and lane scroll/focus reporting |
| Later feature authors | Own feature files and precise draft/operation types, added through Lead-owned composition changes |

Each component exports a `MountComponent<Props>` compatible function. It mounts
inside its supplied host, updates through `update`, and removes its listeners and
owned DOM through `dispose`. R02 receives R03 through `WorkspaceSlots.mountBoard`.
R02 retains one board component while its selected board is active, forwards
updates, and disposes it on selection change or teardown. The Lead owns supplying
the matching selected-board props and implementation. Authors can compile their
component independently using the slot type and fixture data.

The Lead owns browser URL parsing and serialization. `RouteState` records
selection/search/drawer/completed visibility without prescribing URL spelling.
The observed V1 root selection and `/board/alpha` slug route need explicit mapping
to the fresh V2 board UUIDs. User navigation requests history push or replace.
Back/Forward restoration comes from the Lead's retained per-board state.

`BoardViewState` holds each board's search, drawer selection, completed visibility,
scroll and stable focus target. Components report changes through their props.
The Lead keys feature drafts by board/card and feature using `DraftKey`, preserving
exact text, the base revision and dirty state through refresh and navigation.
Later features supply concrete draft value types. Refresh does not silently
replace a dirty value or its base revision. DOM elements and browser capabilities
stay outside retained state.

## Read and operation semantics

- `ReadState` distinguishes initial loading, confirmed data and an unavailable
  read. A refresh may retain a previous confirmed value for rendering. Writes
  require current ready state and the feature's current authorization checks.
  Component session props remain null during bootstrap or capability replacement
  so loading and unavailable views can render before authenticated context exists.
- DTOs remain native JSON shapes. `workspaceEtag` and `boardEtag` carry the
  corresponding HTTP validators outside those DTOs. They are opaque. Supplementary
  resource validators are retained by the operation adapters that need them.
- Board presentation needs users, priorities and policy alongside `BoardView`.
  The adapter must verify matching board ownership and avoid joining stale
  descriptors to a newer board revision. Paginated collections must be exhausted
  within explicit bounds before declaring a complete workspace or board read.
- A validated success response or authoritative inspection establishes
  `committed`. A validated problem with `outcome: not_applied` establishes the
  corresponding refusal. HTTP status alone cannot establish mutation outcome.
- A dispatched write with a lost, aborted, unreadable, malformed or uncertain
  response remains `unknown`. Preserve its UUIDv4 request correlation and draft.
  Inspect affected resource state or its documented receipt before permitting
  another submission. `InspectionResult.unknown` keeps that requirement open.
- Inspection can establish `not_applied` only from operation-specific evidence.
  An inconclusive list read or missing candidate amid concurrent edits preserves
  `unknown`. Request correlation is not an idempotency key. Each operation adapter
  determines its receipt/identity strategy from the native V2 operation.

## Disposable fixture recipe

The Lead's rendered observations supplied the following display names. The
fixture independently maps them to new UUIDv4 identities.

- Boards are **Parity Alpha** and **Parity Beta**, using `entryFixtureBoards`.
  Alpha uses the `58200000` UUID prefix and Beta uses `58300000` for board-owned
  entities. Users are shared workspace identities.
- Lane order is Backlog, Ready For Work, In Progress, Ready For Review,
  Needs Terry, Blocked and Completed. The final four map to their native V2 roles.
- Priorities are P1 Urgent, P2 High, P3 Normal and P4 Low. Owners are Bot and Terry,
  with colors `#AA6622` and `#2266AA` respectively. Priority colors remain empty
  for the fresh V1-style presentation mapping. The workspace initially has no pins.
- Alpha has 23 Bot-owned cards. Backlog has 18, each of the next five lanes has
  one, and Completed has zero. Tickets 1–6 use the exact observed titles and
  description `Disposable parity fixture. Preserve this exact text.` Their
  priorities are P1, P2, P3, P4, P3 and P2. Ticket 1 has two comments.
- Tickets 7–23 have titles `Backlog fixture N with a longer wrapping title`,
  priority P3 and empty descriptions. Beta is empty. The `empty_board` scenario
  also empties Alpha while preserving the board's lane and policy configuration.
- Every lane has the note `Fixture lane LABEL`. Alpha's Backlog enables reorder,
  and Terry has its required `reorder_tickets` resource capability. Alpha's
  Ready For Review enables approval with `Fixture reviewed and approved`.
  Beta keeps both lane controls disabled.
  Alpha grants both actors creation in all seven lanes. Beta grants creation in
  Backlog and Ready For Work. Both grant directed moves between distinct lanes,
  with Completed destinations available only to Terry.
- Alpha attention is one blocked, one needs-input and one ready-for-review.
  Beta and the empty Alpha scenario have zero attention counts.
- `createEntryFixture(scenario, selectedBoard)` constructs fresh objects on every
  call. Its entity tags and service metadata are isolated test values.
- The seeds provide type-complete inputs matching the Lead's observed recipe.
  Matching screenshots still require the same disposable service data, viewport
  and fresh presentation styles recorded in the observation ledger.

## Backend boundaries for T2

- `BoardView` returns board, lanes and cards. T2 adapters must fetch the explicit
  V2 user, priority and policy resources to render owner,
  priority and permission captions.
- Board catalog rows may have null name/revision/attention and availability
  `unknown` or `unhealthy`. Preserve those distinctions during workspace rendering.
  A null name cannot be used as a stable slug.
- V2 route identity is UUID-based. The observed V1 browser slug is a presentation
  route requirement to resolve in the new root adapter.
- Live status and publishing health can change without an entity revision
  change. Revision equality alone cannot suppress their refresh.

Exact captions, colors, card indicators, responsive behavior and URL restoration
remain governed by rendered V1 observations and the approved T2 checkpoint.

## Explicit module delegation for staffing revision 4

The staffing roster delegates implementation of new session/workspace adapters,
board-read adapters, live-state controllers, route/history and cache modules to
named coauthors. The UI Lead owns their interfaces and integration, plus shared
contracts, root composition, entry/build files and shared styles. Coauthors have
exclusive write ownership of their assigned feature files; they do not edit the
same source file concurrently. Concrete paths are recorded before dispatch.

Each pair agrees its typed input/output boundary before work. Rendering consumes
read projections and emits intents. One coordinator owns per-card revision and
pending-operation state. One cache authority owns retained view state. One upload
coordinator owns staging IDs and per-file transitions. Avoid duplicate authorities
or new abstraction layers created solely to give another author work.

See 0581-staffing-plan.md and the machine-readable coauthor_assignments roster.
