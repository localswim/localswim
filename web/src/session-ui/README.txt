W03 SESSION LIFECYCLE

SessionView consumes the shared API session factory. The exact one-use code is
entered into a password form and cleared before awaiting exchange; it never enters
a URL or recovery store. The API provider owns exact validation and memory-only
bearer storage. UI receives only non-secret SessionRead identity and scope.

Explicit reauthentication clears the prior workspace. Logout captures the old
credential through the provider, clears local authority/visible content immediately,
then makes at most one best-effort DELETE. Lost response means server revocation
unverified; it never causes a retry. Authentication refusal invalidates the visible
workspace. Epoch checks prevent late exchange/read responses restoring logged-out
content. Private drafts and original submissions remain in W01's store.

SessionWorkspace composes existing BoardView and W03 controls without an entry
page or asset pipeline. Following explicit authentication it reads the current board
and private inventory; original lookup, attachment, recovery confirmation and export
always need deliberate actions. No trust enrollment, grants or native bootstrap.

W03 outcomes reach BoardView's existing committed-receipt observer. A newer commit
marks an older displayed board stale and gates Save; an authenticated read already
at or beyond that receipt remains usable. Editors, original bases and focus remain
unchanged, including compact original lookup and late attachment responses.

Existing editors register private writes at the action event into one shared
clear-domain queue. Logout destroys views and cancels a private discard still
waiting for admission; it preserves earlier editing evidence. A new session does
not replay old actions. Atomic private clear holds subsequent deliberate edits
until its result settles, across live clients using the same recovery database and
installation in this workspace. Separate browser-realm fencing is not claimed.

The product profile qualification gate remains V02. Synthetic headless components
do not establish real browser/TLS/trust or storage durability qualification.

M01.05 PRIVATE WINDOW LIFETIME

The ordinary local entry reads the strict runtime-mode discriminator before any
bootstrap. Standalone uses its existing local channel. Workspace obtains its own
metadata channel and an explicit /board/<projectKey>/<boardKey> selection, then
the selected board's channel. Workspace root reports that the M01.07 chooser is
pending; it does not select an implicit board. No catalog, pins or final sidebar
are implemented here.

Initial connection has one 20-second acquisition deadline across runtime mode,
workspace metadata, selected bootstrap, current reads and private hydration. The
initial view stays detached until acquisition succeeds. Timeout aborts pending
fetches, disposes acquired partial views and fences late results; it never replaces
Retry with a late application. Successful acquisition clears its timer without
invalidating the live client. Publication reapplies focus/selection/scroll after
the retained DOM is connected. Later selected navigation has its own total budget.

WindowBoardController owns a retained LocalSessionView for each full installation,
board and incarnation tuple. Selection suspends and detaches the actual view DOM;
returning reattaches that same editor, original base, private detail, form state,
focus, text selection and scroll. It does not recreate an editor from current board
text. Each board keeps its original callbacks and outcomes, including late results.
Read generations are invalidated on suspension. Selected clients additionally bind
all network work to the immutable board/actor/grant and connection generation.

The public composition seam is WindowBoardController.navigate/select, reconnect,
canLeave, flushPrivateInput, discardActiveInput, discardUnpersistedInput and destroy. app.workspaceController
exposes the mounted controller for later shell integration. M01.07 owns the final
chooser/routes/sidebar controls and their own collapse/width preference presentation;
it must use this controller's navigation gate rather than remounting private views.

WindowState stores a bounded window identity and non-secret board draft references
in sessionStorage. Reload and browser back/forward preserve this window's identity;
a newly navigated window starts a fresh identity even if opener storage was copied.
Editor prose, base snapshots and command fields remain in existing RecoveryStore
draft.input, not a new recovery schema. Only the current window's explicit draft
reference is restored. Other windows' shared inventory records are never attached
automatically; deliberately viewing a private copy starts a distinct draft ID and
records that window's selection. Reload restores its own copy, including the selected
private textarea. Start-work editors retain their lane-only field shape on reload.

Pending/failed private writes keep input in memory. Controller navigation and
browser Back/Forward wait for writes and refuse a failed save until the user saves
or explicitly discards current editor input. beforeunload requests the browser's
native warning while private input remains unpersisted. Forced reload, disabled
warnings or unavailable storage cannot promise lossless durable recovery. Recovery
retention, immutable original evidence and origin boundaries remain unchanged.
Reconnect and view selection submit no mutation and perform no original lookup.

Existing attachment inputs join the same private-input gate. Suspension retains
the actual file input node and File object, invalidates reads, aborts pending
downloads/exports and defers attachment outcome presentation until its own view
returns. File bytes remain memory-only; no reload-restoration promise is made.
This narrow attachment boundary does not add own-form hydration. Any visible
attachment editor therefore pauses app/history navigation and requests the browser
unload warning until submission or explicit discard, even after metadata persistence.
Input stays in this window; finish or discard it before leaving. Forced reload
cannot promise to restore that editor or its selected file bytes.

window-controller.test.ts exports synthetic browser probes for identity, retained
DOM/focus/selection, late A completion while B is active, and refused persistence
navigation. runPrivateReloadProbe covers lane-only and selected private-copy reload;
runAttachmentLifetimeProbe covers attachment input refusal and file-node retention.
These supplements make no actual browser-profile qualification claim.
app.test.ts supplies a manual acquisition-clock/composite-phase regression without
a real 20-second wait, including abort-ignoring late completion and a connected
client that remains usable after acquisition succeeds.
