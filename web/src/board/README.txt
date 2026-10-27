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

Create/edit/comment/link-add/link-remove/start-work/handoff/approve open semantic
forms with an explicit Save button and original board/card bases. Every input change
persists a private revision-CAS draft; pending/saved/failed/forked feedback retains
the caller's input. Blur, Enter inside a field and IME composition never submit.
Tab follows native semantic controls; Enter/Space activates explicit buttons.
Navigation restores the matching logical card control, with heading fallback.

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
