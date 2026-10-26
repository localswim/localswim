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
