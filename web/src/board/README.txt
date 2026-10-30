Board views and explicit interaction

Construct BoardView(root, client, boardId), then await open(). The caller owns the
ephemeral credential lifecycle and authenticated composition. destroy clears visible
content and prevents later async announcements; it preserves recovery. The owning
session adapter must invoke destroy on logout/revocation. Entry/assets, recovery
inventory/reload/clearing, server recovery administration and attachment/export
controls are separate composition work.

Current reads label board, incarnation, revision and credential-bound actor. Failed
reads retain a visibly stale board, never present an empty board as current. Card
references, assigned priority, lanes, comments and link kinds remain visible. Go
enforces grants, prerequisites, review handoff and human-only approval. Completed
cards expose disabled mutation controls; approval requires visible human session rights.

Archive status selects Active (default), Archived or All rows from the complete
validated BoardRead. Display rows sort explicit P0-P5, then ticket and UUID;
transport ordering and the reference graph are unchanged. Same-identity linked
targets can be opened even when archive filtering hides their own row. Details
remain mounted through filtering and close to the visible opener, else Read
current board. The filter is private per window and board, bounded on reload,
and remains usable in memory if window storage refuses.

Archive card and Unarchive card open an exclusive archived:Boolean card.edit
draft with Confirm archive card/Confirm unarchive card and Cancel archive/Cancel
unarchive buttons. Dirty editor input first uses the explicit discard/keep gate.
Cancellation does not erase private history or retained originals. The ordinary
storage, stale-base and pending/UNKNOWN guards still apply. A confirmed mutation
marks the board stale; deliberately read current board to update visible rows.
After a confirmed archive/unarchive of the selected card, details issue fresh
authoritative card/history reads and update its archived label without replacing
the editor or moving its focus. Responses must match the selected context and
reach the receipt revisions; failed reads retain truthfully stale prior facts.

Create/edit/comment/link-add/link-remove/start-work/handoff/approve open semantic
forms with an explicit Save button and original board/card bases. Every input change
persists a private revision-CAS draft; pending/saved/failed/forked feedback retains
the caller's input. Blur and IME composition/confirmation never submit. A deliberate
nonrepeated Enter in a single-line input activates Save once; textarea newline
and select keyboard behavior remain native. Tab follows semantic controls;
Enter/Space activates explicit buttons without a second form submission.
Navigation restores the matching logical card control, with heading fallback.

Rename card opens the same card.edit draft focused on title. An unsubmitted dirty
editor must be finished or explicitly discarded before another editor replaces it.
Escape/Discard closes a clean editor directly; dirty input asks Keep editing or
Confirm discard editor input, with focus restoration. This closes the current
editor/reference, not retained recovery history or an immutable original operation.
Pending/UNKNOWN submissions cannot be cancelled or unlocked through that control.
Untouched confirmed title/description values retain their exact source text when
native input/textarea line-ending normalization would otherwise alter them.

Save captures immutable input before awaiting and dispatches only after W01 verifies
its durable evidence receipt. Pending/unknown original operations prevent replacement
submission. Stop waiting never claims cancellation of a possible commit. A late
matching response updates only the original operation's announced status, preserving
newer editor text and focus. No automatic retries, fresh IDs or confirmations occur.

Conflicts preserve original input/base and offer current confirmed text; unavailable
current text is explicit. Read the current board and deliberately open a fresh editor
to reconcile after definitive noncommit. Confirmed saves mark the older board stale.
This applies equally to initial and original-lookup receipts, full or compact. An
authenticated same-incarnation read at or beyond the committed revision remains
usable; a receipt alone never supplies current text or updates an editor's base.
Original-outcome lookup never resends. Ordinary HTTP refusals cannot prove noncommit.

All prose uses textContent or native input value, including status and reference
text; no user-provided HTML, script, link protocol or markup is executed. Drafts
contain editor prose plus non-secret original command/target/revisions, never tokens,
login codes or session objects. Submitted evidence remains independent of drafts.

view.test.ts supplies an explicitly synthetic fixture for actual headless component
tests using production IndexedDB and the concrete prepared verifier. A synthetic
qualification assertion is test-only. API-fixture cross-direction changes do not
claim the later real browser/CLI/backend acceptance journey or product qualification.

Hierarchy in nonmodal details uses a captured complete validated BoardRead, never
search summaries or a focused card response. Parent navigation and Children show
exact identities, archive labels and direct/descendant counts. Descendants use
ticket/UUID sibling order in depth-first preorder, 20 local rows per page, exact
range/total/page/end, with First children page, Previous children and Next children.
A background board revision marks the captured hierarchy stale and keeps its
pages. Read current hierarchy deliberately reads a complete board, resets page 1
and preserves unrelated input/focus. Identity/generation fences discard late reads.

Create child, Set parent and Clear parent reuse the same editor, private drafts,
immutable originals and dispatch guards. Set parent chooses an exact full card
from the complete board, including archived/Completed targets; selecting an option
does not save. Clear parent retains explicit null. Current parent refs use the
board identity, independently of the target's CreatedIn provenance. Parent edits
are exclusive input and Completed children remain read-only. No hierarchy action
settles/replays a pending original. Historical parents show exact snapshot-time
identity as unresolved/unavailable; a standalone history read cannot resolve them.
After a validated hierarchy commit receipt, a still-matching selected child is
read authoritatively at or above the receipt's board/card revisions and its exact
current parent is announced. Create child follows the new receipt card identity
only while that submitting view/editor and source-parent selection still match.
Selection/request/view-generation changes, close, suspend or reload prevent late
follow-up navigation. These reads neither take focus nor replace editor/private
originals, rebase input, replay operations or update the captured complete graph.
hierarchy.test.ts exports focused boundary, navigation, editor, history and
recovery supplements; these synthetic checks do not claim product qualification.

Assignee uses actual board.result.actors IDs, with a plain Unassigned option.
Cards and current-text comparisons display the assignment. Create/edit capture
UUID or null in the same immutable draft and request as title/priority; refresh
and conflicts retain the original editor and selected assignment. Responsibility
does not affect the authenticated actor or permit approval.

Each ordinary board card separately shows Dependency pressure (direct incoming
active blocking dependents with their assigned priority and lane) and
Prerequisites/readiness (direct outgoing active blocking targets). Priority is
never changed or scored. Informational and removed links do not contribute.
Only an exact current board/incarnation target resolves; unavailable or foreign
targets remain unsatisfied. Archived targets and targets without a current human
approval matching the current policy and preceding card revision are unsatisfied,
including Ready for Review targets awaiting human acceptance.

These facts use the complete validated board read. Refresh updates them and failed
reads retain the existing stale disclosure. A satisfied prerequisite display does
not establish permission or card-lane eligibility; the server rechecks every
deliberate start. Card-only current-text reads do not derive board-wide readiness.

Find cards focuses Search cards. Apply filters (or non-composing Enter) reads
committed current summaries from the server using exact query text, Lane, Owner
(blank means any, unassigned or a canonical actor UUID), Explicit priority and
the single shared Archive status control. That archive choice also immediately
changes the local complete-board visibility projection; draft text/lane/owner/
priority never approximate Go's authoritative matching in JavaScript.

Search pages contain at most 20 summaries, never a total or full graph. Next
results uses the returned cursor; First page explicitly refreshes the last
applied filters. Apply filters uses the visible draft; Clear filters resets
everything including Active and reads a fresh first page. Failed, stale or late
reads retain input and mark old rows stale; commits disable continuation until
an explicit fresh read. Open card reads independent current details. Search
cannot settle an original operation, unlock a submitted editor or change its
base. A disappearing opener returns Close focus to Find cards.

Draft and applied filters are bounded private window/exact-board state. Cursors
and result pages are never persisted; reload reads a fresh first page. Storage
failure retains visible input in memory and explicitly reports the limitation.
