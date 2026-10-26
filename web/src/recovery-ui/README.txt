W03 PRIVATE RECOVERY AND SERVER RECOVERY CONTROLS

RecoveryView uses W01's actual IndexedDB store through the same BoardClient used
for dispatch. Reload reads metadata only; retained drafts open in a separate
editable private editor with revision CAS. Original lookup uses the retained
board/incarnation/actor/opId and immutable evidence. Nothing applies recovered
input over another editor or replays a submission. Late results do not move focus
from an editor. Storage loss/unavailability and missing authority remain unknown.

Clear-one/all are local-only, separate warned actions. Both prose discard and the
possibility that unresolved outcomes become unknowable require explicit checks.
All registered main, admin, attachment and recovery editors in the workspace
share a FIFO by actual origin/database/installation, including different clients
and boards. Confirmation installs a boundary immediately. Earlier queued writes
finish before atomic private clear/readback; later deliberate edits wait behind
it and remain recoverable through existing CAS/fork behavior. Visible editor input,
original bases and focus stay intact. A pending mutation releases admission after
its immutable evidence completes, without waiting on its network response; late
terminal append cannot recreate evidence removed by W01. No operation is replayed.

Pending private reads and file processing cannot automatically recreate discarded
input. Cancel/logout prevents a queued clear that has not entered storage; already
admitted storage may finish, so a stopped wait or failed completion is unverified.
New edits remain queued behind that result. Separate databases/installations are
isolated. This ephemeral registry covers live clients in this JS workspace;
separate browser tabs/realms require their own coordination/qualification and are
not claimed as fenced by this component. Discard affects currently stored profile
records; other open tabs and later edits may store recovery again. The warning
states this limit before confirmation. Browser/site clearing can still lose data.

RecoveryAdminView uses current human read+history-admin authority, an explicit
read and server preview, and a separate warned Confirm. It shows the complete
immutable inventory, hash, times, original identity/revision and loss set. Provider
validation and server admission recheck exact inputs/current authority. The UI
never renews a hash or automatically confirms/retries. A spent confirmation can
only use original lookup while unknown; a new preview requires deliberate action
after resolution. Local profile qualification is absent by default and remains
V02's product gate; private recovery is advisory, never server authority.

All prose is inert DOM text/value. Forms suppress implicit submission and IME
composition; native buttons support keyboard actions. SessionWorkspace destroys
the visible controls on logout/revocation while preserving private records.

Synthetic headless DOM/IndexedDB checks are component evidence only. They do not
qualify a product profile, TLS/trust, real boards, eviction, power loss or native
storage. No browser restore, grant enrollment or native bootstrap is provided.
