# #0582 — observed V1 contract

## Status and evidence boundary

T1 packet for epic #0581, observed on 2026-11-13. This packet specifies the
foundation for #0583 and #0584. It does not implement T2 or claim V2 parity.
#0582 remains In Progress until the Architect authorizes its handoff.

The UI Lead operated running V1 build **46d5d58** through the Chrome plugin.
Only rendered screenshots, accessibility text, documented launch/CLI interfaces,
fresh fixture data and written requirements were consulted. No V1 implementation,
HTML, CSS, scripts, templates or image assets were read or copied. The existing
V2 presentation and rejected UI candidate were not used as design inputs.
The older candidate's package manifest and installed compiler/lint/formatter
executables were used solely as qualified tooling, not presentation source.

The historical `0514-v1-ux-inventory.md` and `0501-web-audit.md` are observation
leads, not new qualification evidence. Their implementation links were not followed.
New code provenance and component ownership are in [0582-contracts.md](0582-contracts.md).

## Reproduction environment

- Isolated runtime: `http://127.0.0.1:8831/`, workspace keys `alpha` and `beta`.
- Empty workspace: `http://127.0.0.1:8835/`.
- V1 installed from the frozen legacy project into a disposable environment;
  no development dependencies, publishing or notifications enabled.
- Private runtime root: `C:/Users/TDO-XPS15-2024/.codex/visualizations/2026/11/13/01a1d4d6-5f2d-7d83-bacb-8b91564f53f8`.
- Chrome screenshots are **1800 × 1318 pixels**, except the empty-workspace frame at **1800 × 1262 pixels**. No viewport or zoom override
  was applied. T2 must explicitly record CSS viewport, device scale and zoom
  before claiming a matched comparison; image dimensions alone do not prove them.
- Production 8811 was used only for the authorized ticket transition. The old
  demo on 8812 and production application data were not used as fixture inputs.

Inputs are under `fixture-inputs/`; generated board snapshots under
`v1-workspace/`. The V1 CLI's `board verify` passed after 23 card creations,
two comments and the two lane-control configuration changes. Existing source
files and production snapshots were not changed.

## Screenshot/state ledger

All image paths below are relative to the private root's `output/` directory.
The generated [evidence manifest](0582-evidence.json) records hashes and paths.
Screenshots are reference evidence only and must never become UI assets.

| State | Image | Observation |
| --- | --- | --- |
| S01 empty Alpha | `0582-v1-empty-alpha.jpg` | Root selects first board and resolves `/board/alpha`; seven empty full-height lanes, no card-placeholder panel. |
| S02 empty Beta | `0582-v1-empty-beta.jpg` | `/board/beta`, selected-row styling moves; only Backlog and Ready For Work expose create buttons. |
| S03 populated Alpha | `0582-v1-populated.jpg` | 23 cards, Backlog 18; five other lanes 1 each; Completed 0. Sidebar attention 1/1/1 and comment badge 2. |
| S04 drawer | `0582-v1-drawer.jpg` | Right-side white drawer, dimmed board; ticket, priority, title, owner, description, attachments, relationships, comments, audit. |
| S05 new card | `0582-v1-create.jpg` | Centered New card dialog; title receives focus, priority P3 and owner Bot default; cancel closes without a new card. |
| S06 filter | `0582-v1-filter.jpg` | `release` produces one card and `1 found`; lane counts reflect matches, sidebar attention retains board totals. |
| S07 collapsed | `0582-v1-collapsed.jpg` | Sidebar becomes a narrow toggle rail; the board expands and lane titles reflow. |
| S08 empty workspace | `0582-v1-empty-workspace.jpg` | Sidebar says `No boards found in this folder.` Main area says `Board unavailable`, explanation and Retry. |
| S09 scrolled | `0582-v1-scrolled.jpg` | Backlog scrolled independently to #0023 while lane headings, other lanes and workspace sidebar stay fixed. |
| S10 Back | `0582-v1-back-restored.jpg` | Select Beta, then browser Back restores Alpha and the Backlog scroll position. |
| S11 unknown bookmark | `0582-v1-unavailable.jpg` | Unknown board key retains workspace navigation and shows `Board unavailable`, bookmark explanation and Retry. Selecting Alpha recovers. |
| L01 loading | Accessibility observation, no image | On first entry and initial Beta selection, main area briefly says `Loading Parity Alpha…` or `Loading Parity Beta…`; shared header/sidebar already exist. |

Screenshots were captured after the transition settled. The browser sometimes
returned an accessibility update before its screenshot reflected the same frame;
those transient combined captures were not selected as the saved reference.

## Visible foundation requirements

Measurements here are image observations, not extracted style values. Match
the actual pixels and behavior during T2; do not turn approximations into a
license for layout changes.

### Workspace and header

- Dark, single compact header approximately 44 image pixels high. Left to right:
  status dot, small multicolor brand mark and `localswim`, selected board title;
  toward the right, card search, build, last-card age, clock and green CONNECTED.
- Pale sidebar approximately 312 image pixels wide, `Boards` and hamburger at
  top, full-width board search, fixed red/yellow/blue Blocked/Terry/Review legend,
  then board rows. Selected row has pale blue fill and a narrow dark blue left edge.
- Board rows contain title, pin outline and circular nonzero attention counts.
  Zero attention is visually quiet. Alpha counts are 1/1/1; Beta has no count badges.
- Bottom of sidebar: `Open board in new window ↗`, then collapsed Cache settings.
  A slim resize grip is visible at the right edge. Collapsed width is approximately
  44 image pixels; title/search/rows/footer disappear, toggle remains.
- Root selects the first available board. Clicking another board changes the URL
  in place. The observed route is `/board/<workspace key>`. V2 API routes remain
  UUID-addressed; the UI Lead owns presentation-key resolution and history state.
  Do not expose an unapproved redesigned URL/navigation flow to solve this boundary.

### Board and cards

- Seven lanes share the available width, ordered Backlog, Ready For Work,
  In Progress, Ready For Review, Needs Terry, Blocked, Completed.
- In the expanded-sidebar frame, lane left edges are approximately 322, 533,
  744, 955, 1166, 1377 and 1588 image pixels. Small consistent gutters separate
  pale-gray rounded columns with a blue top edge; Completed uses green.
- Uppercase bold lane title, white circular count badge and permitted create `+`
  form the first header row. Reorder and green thumbs-up approval controls occupy
  their observed header positions when enabled. Titles wrap naturally; all lane
  permission captions and first-card tops align to the tallest header.
- Captions read `IN: Bot + Terry · OUT: Bot + Terry` in this fixture, except
  Completed `IN: Terry · OUT: Bot + Terry`. Captions describe move grants,
  not create grants. Beta omits create buttons for five lanes despite those captions.
- Dense white rounded cards have a colored priority square at upper left, bold
  four-digit ticket at upper right, title below and a dark comment badge at lower
  right when nonzero. #0001 is red P1, #0002/#0006 yellow P2, P3 dark slate, P4 gray.
- The board fills the viewport vertically. Each lane's card area scrolls independently;
  headings stay fixed. No page-level scroll displaced the header or sidebar in S09.

### Interaction details already observed

- `/` focuses the global card search. Typing `release` leaves #0001 only and
  reports `1 found`. Escape clears it. The browser URL remained `/board/alpha`.
- Opening #0001 leaves the URL unchanged and focuses Close card. Escape closes
  the drawer and returns focus to that card's open button.
- The settled drawer is approximately 560 image pixels wide at the right edge,
  starts at the top of the viewport, and overlays a dimmed board. Comments are
  newest first. The visible hint says Enter posts and Shift+Enter adds a line break.
  Posting behavior is a remaining #0595 observation, not tested here.
- Backlog `+` opens New card with title focused. It includes title, priority,
  owner, description, attachments and relationships; the description hint says
  Enter adds a line and Add card submits. Cancel closed it; observed focus then
  returned to the document, not the `+` button. Preserve observed behavior unless
  Terry approves a change. No browser create was submitted in T1.
- The create panel is centered on the full captured viewport, approximately
  x=590–1210 and y=247–1073, with distinct header and footer. Its backdrop dims
  the header and board while the workspace sidebar stays undimmed. These are
  image-pixel observations; #0588 must confirm responsive behavior separately.
- Selecting Beta after scrolling Alpha and using browser Back restored Alpha's
  prior lane scroll. This is an observed single navigation sequence; full
  reload/draft/cache qualification belongs to #0599.
- Unknown-key failure retains the sidebar and allows selection of a healthy board.
  This is not a service outage or failed-write recovery test.

## Exact equivalent fixture recipe

`createEntryFixture` in `web/clean-room/fixtures.ts` supplies the typed seed.
Its UUIDs are newly authored V2 identities, never V1 IDs. Both runtimes must use
the same display values, order, grants and card contents for T2 comparisons.

- Alpha and Beta use Terry (Human, `#2266AA`) and Bot (`#AA6622`), default owner Bot.
  Priorities: P1 Urgent, P2 High, P3 Normal (default), P4 Low.
- Seven lanes in the order above; note text is `Fixture lane <label>`.
  Every directed move between distinct lanes is granted to Terry. Bot has those
  moves except a destination of Completed. Alpha grants creation in every lane;
  Beta grants creation only in Backlog and Ready For Work.
- Alpha enables Backlog dependency reorder and Ready For Review approval with
  `Fixture reviewed and approved`. Beta has both controls disabled. Neither board
  is initially pinned; use a separate pinned variant for #0585.
- Alpha #0001–#0006 occupy the first six lanes respectively. Their titles are
  `Review the release checklist`, `Prepare the next demonstration`,
  `Match the selected board`, `Verify the keyboard workflow`,
  `Confirm the fixture requirements`, `Wait for sample material`.
  Priorities are P1, P2, P3, P4, P3, P2. All owners are Bot; each description is
  `Disposable parity fixture. Preserve this exact text.`
- #0007–#0023 are Backlog/P3/Bot, each titled
  `Backlog fixture N with a longer wrapping title` (un-padded N), description empty.
- #0001 has `First fixture comment`, then `Second fixture comment`, by Bot.
  All other comment and attachment counts are zero. No relationships/parents,
  archived cards, Completed cards or covers exist in this foundation variant.
- Beta is empty. Empty-Alpha uses its configured lanes/grants with no cards.
  Empty workspace has no catalog rows. Unknown bookmark is a distinct scenario.
- Timestamps/build/clock are volatile labels. T2 must record runtime times and
  compare their placement/format and correct underlying values; do not fabricate
  backend update times to make screenshot text equal. Completed-age variants
  will need controlled timestamps in the feature-specific fixture.

## Historical inventory mapping and remaining observations

These rows retain the historical audit's distinction between browser tasks,
CLI-only tasks and V2 additions. A mapping is an observation backlog, not a claim
that T1 demonstrated that workflow. No new V2 administration panels are authorized
by this packet; retained API/CLI operations stay available.

| Inventory surface | Story / next observation |
| --- | --- |
| Workspace discovery, selection and bookmarks | #0583; foundation S01/S02/S08/S11, then matched V2 loading/error entry. |
| Pinned/other boards, attention, inaccessible counts and recency | #0585/#0593; pin grouping, unavailable rows, background edit, exact ordering. |
| Bot/Human live edits and tab cues/reconnect | #0593; source transition, disconnect, draft retention, recovery. |
| Pin/unpin and keyboard/pointer order | #0585; permitted moves, save failure and readback. |
| Sidebar resize/collapse and history restoration | #0585/#0599; S07/S10 start the ledger; keyboard resize, reload and retained drafts remain. |
| Browser cache budget, eviction and failed refresh | #0599; inspect Cache settings and each stale/read-only state. |
| Lane/card counts, captions and notes | #0584; S02/S03/S09; equivalent V2 rendering, empty/long label variants. |
| In-place title/ticket/content search | #0586; S06 only; content, no results, scroll and clearing variants remain. |
| Recent/older Completed visibility | #0586; seed old/recent completions and inspect exact cutoff/control. |
| Drawer identity, attribution, prose, comments and audit | #0587; S04; formatted prose, ticket links, hierarchy, long scrolling remain. |
| Create card with typed links | #0588; S05; relationships, validation, submission and lost-response inspection remain. |
| Move dropdown, keyboard and drag/drop | #0589; observe exact gesture/feedback and refused destinations. |
| Title/description editing | #0594; inline editor focus, cancellation, exact text and unknown outcome. |
| Owner assignment | #0594; owner control, selection and confirmation. |
| Individual/batch priority and dependency floor | #0594/#0592; inspect batch entry and scope. |
| Parent/children browse; parent mutation | #0587 browse; parent mutation remains CLI/API-only per historical inventory. Reconfirm before adding browser controls. |
| Typed relationship add/change/remove/follow | #0596; observe existing link rows and editor states. |
| Comment composition and uncertain submission | #0595; Enter/Shift+Enter, one commit, preserved draft. |
| Single Human approval | #0597; exact drawer control and receipt/readback. |
| Lane approval and configuration | #0590; button observed S03 only; confirmation, atomicity and custom text remain. Configuration retention does not authorize new settings UI. |
| Archive/find archived/restore | #0594/#0587; establish actual V1 browser entry points before presentation. |
| Attachment file/paste ingestion and resume | #0600; #0588 slot; selection, cancel, partial upload and reload states. |
| Attachment preview/download/cover/detach | #0598; no attachment variant in T1. |
| Draft and submitted-action recovery | Each mutation story/#0593/#0599; no generic acceptance by checklist. |
| Initialize workflow/rename/default owner/grants | Retain V2 API/CLI; no T1 browser implementation. |
| Lane add/rename/key/retirement/order | Retain V2 API/CLI; reconfirm V1 browser surface before placing any control. |
| Dependency ticket numbering | #0591; enabled control observed only; preview/apply/stale outcome remain. |
| Actor display names/lane notes/board CSS | #0584 rendering; configuration API/CLI retained, no copied V1 CSS. |
| Offline snapshot/export/verify | CLI-only; preserve existing V2 qualification. |
| Full activity/sanitized notice paging | Drawer scope #0587; full/offline monitor remains CLI-only. |
| Owner notice target/enable/disable | CLI-only; no change to live target or enabled state. |
| Backup/download/restore | V2 retained API/CLI; not evidence of V1 browser parity. |
| Damaged board quarantine/recovery | #0583 error display/#0593 reconnect; recovery API/CLI retained. |
| Git publishing/status/retry | Retained V2 API/CLI; observe before any browser placement decision. |
| Close/reopen board | V2 addition; retain API/CLI, do not introduce a new V1 entry control. |
| Launch/activate/shutdown | V2 operations retained; browser placement is not a T1 redesign authority. |
| Command help/invalid invocation | CLI-only; existing checks retained. |

Each feature author first observes the unresolved V1 states, records fresh evidence
with equivalent disposable data, and then implements only its authorized story.
#0601 performs the bounded integrated matrix; it does not absorb unlimited defects.

## Queue and authoritative readback

Six schema-1 observation receipts were sent by a browser-operated, loopback-hosted
static sender via native Webdis PUT to the shared queue. The sender validates a
fixed phase list, uses canonical UTF-8 base64 with `b64:`, omits credentials, has
a 3-second timeout, and checks HTTP plus XADD result. The parent released browser
control before reading one bounded page (six messages, 580 decoded bytes total).

These receipts explicitly report **manual rendered observations**. They are not
instrumentation from inside V1 and do not independently establish product behavior.
Screenshots/accessibility observations plus the V1 CLI fixture verification are
the product evidence. `0582-queue-receipts.json` is private evidence; no card prose,
tokens, raw URLs or errors were sent. The temporary mailbox is discarded after
export. T2 must add fresh product diagnostics to its independently authored UI.

## T2 acceptance boundary

#0583/#0584 cannot start until the Architect reviews this exact packet and releases
T2. Their independent components must compile against the shared contract without
importing one another. The UI Lead composes them and shows the matching workspace
and board to Terry. All strict compiler/lint/format/release and Git-hook standards
remain mandatory before T3, including actual hook qualification; T1's declarations
and tooling checks do not qualify an executable or accept the archived candidate.
