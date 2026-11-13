# Epic #0581 staffing plan - revision 4

Use multiple authors where independently owned modules shorten delivery. This roster replaces the one-author-per-story default. Counts describe useful ready work, not reserved processes. All workers use Standard speed.

| Turn | Stories | Author packets | Evidence packets | Fresh technical reviewers, later | Terra, later | Maximum initial worker wave |
| --- | --- | --- | --- | --- | --- | --- |
| T1 | 1 | 1 plus UI Lead observations | 0 | 1 | 1 | 1 |
| T2 | 2 | 4 | 0 | 2 | 1 | 4 |
| T3 | 9 | 13 | 0 | 9 | 1 | 13 |
| T4 | 6 | 9 | 0 | 6 | 1 | 9 |
| T5 | 1 | 2 | 0 | 1 | 1 | 2 |
| T6 | 1 | 1 | 2 | 1 | 1 | 3 |

Counts exclude UI Lead and Architect. At most 16 direct subagents may run. T1's actual author and reviews are preserved. Future authors start only after ticket and turn gates open. T3 additionally requires Terry's T2 visual confirmation.

## Coauthor ownership

All implementation authors use Sol / Medium unless specified. Session/authentication and substantive architecture use Sol / High. Each listed module is independently owned. Names use the ticket followed by the responsibility, for example Sol Medium 0584 Render and Sol Medium 0584 Data.

| Card | Author A | Author B | Boundary |
| --- | --- | --- | --- |
| #0583 | Workspace rendering, selection and scroll intents | Sol / High session/bootstrap and validated workspace API adapter | Renderer consumes typed read/session projections; no credential logic in rendering |
| #0584 | Board renderer and its scoped styles | Board read adapter, users/priorities/policy composition | Frozen DTO/props interface; renderer owns DOM and CSS together |
| #0585 | Pins/search, persistence and failed-save inspection | Sidebar resize/collapse and layout preferences | Ordered pin-state owner separated from geometry |
| #0587 | Drawer renderer, focus/scroll and feature slots | Validated card/hierarchy/comment/activity reads | Read module supplies immutable drawer projection |
| #0588 | Creation form, exact draft inputs and cancellation | Create/link transaction adapter and original-intent inspection | One submit coordinator owns mutation state |
| #0593 | Sol / High live/reconnect state controller | Status/attention presenter | Presenter consumes state, controller alone advances it |
| #0594 | Exact title/description editor and adapter | Owner/priority/archive controls and adapters | One shared per-card revision/operation coordinator, integrated by Lead |
| #0598 | Attachment rendering, preview/download and cover presentation | Attachment reads, mutation adapters and inspection | One attachment state owner; renderer emits intents |
| #0599 | History/route codec and restore intents | Sol / High retained-view/cache state and eviction | One retained-state authority; history requests restoration |
| #0600 | Picker/paste/staging and per-file presentation | Sol / High upload/recovery coordinator | Stable staging IDs; coordinator owns per-file transitions and original bytes |

#0586, #0589, #0590, #0591, #0592, #0595, #0596 and #0597 each have one Sol / Medium author. These are already independent parallel stories. #0582 retains its actual Sol / High contract author plus UI Lead observation work.

T6 uses a Sol / High qualification author and two bounded evidence workers. One Sol / High worker audits behavioral coverage; one checks release assets, source provenance and result linkage. They prepare independent packets while the Lead owns execution of shared checks. A fresh Sol / High reviewer reviews the assembled result independently of all three evidence authors.

## Relay and review

1. Start eligible author packets with explicit files, interfaces, outputs and completion criteria. Never give two authors concurrent write ownership of the same file.
2. Each author supplies its code, focused checks and compact handoff, then exits. Do not keep finished authors idle.
3. As author slots free, launch fresh reviewers for completed, integrated stories. Reviewers must be independent of every coauthor on that story. They can overlap authors working on other stories.
4. Use Sol / High for authentication, concurrency, original-write recovery and atomic operations. Use Sol / Medium for ordinary presentation review. #0583 now requires Sol / High review because its scope includes the session boundary.
5. Run Terra / Medium as soon as the rendered integrated surface is ready. It checks matched V1/V2 state, viewport, layout and interaction. Terra exits after that packet and returns only for a material visual correction.
6. Route findings to bounded correction work, then obtain independent re-review. Integrate at the Lead and submit the complete turn to Architect for RFR and next-turn authorization.

No developer/reviewer pair reserves idle capacity. Authors supply test fixtures and module checks. One compiler lease and one browser operator remain shared. Independent checks may overlap when they do not contend for those resources.

## Integration ownership

The UI Lead alone edits shared contracts, root composition, entry/build files and shared styles. Lead-exclusive ownership means integration ownership; it does not prevent explicit delegation of a new isolated runtime module. Before dispatch, the Lead records concrete file paths and a minimal typed interface for each coauthor. Coordinate shared state through one owner rather than duplicating it.

Do not split DOM rendering from tightly coupled CSS without stable hooks. Do not create extra layers just to manufacture a second author. Reassess a split if coordination costs exceed its time saving.

All authors use observed V1 behavior, screenshots and written specifications. They must not read, copy, translate or port V1 source. Fresh TypeScript presentation uses the V2 API.

## Current rolling assignments

The next three remaining turns are in [0581-rolling-assignments.md](0581-rolling-assignments.md). The complete named packet roster and model/effort values are in [0581-ui-plan.json](0581-ui-plan.json). Refresh the next three turns after each handoff, preserving actual T1 work and prior rosters as evidence.
