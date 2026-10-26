CLI administration adapter

Runner.Run implements the accepted setup, grant, credential, recovery and browser
session-code commands. Executable registration belongs to composition. Setup,
ordinary credentials, native current authority and immutable evidence are supplied
explicitly; no default board, alternate root, credential search or owner fallback
exists. Provider uses setup.Manager's exact protected credential files and the
shared core connection constructors. Core authenticates current session equality.

Grant and recovery mutations use core.Client.Dispatch and its shared immutable
evidence save/readback, sole dispatch, receipt classification and resolution.
Human enrollment requires the exact approved terminal challenge. Automation never
receives approve, grant-admin or history-admin. The server independently admits
every action under current rights and exact revisions.

Confirmation reads no input until the entire required scope, loss and challenge
display has been written with its exact UTF-8 byte count. Short or failed output
refuses before credential preparation, evidence or mutation effects; partial
prompts are never retried. Cancellation after display also prevents input reads.

Recovery preview returns the unchanged C03 response. Confirm requires --preview
with that original response, strict inventory hash, identity, actor, revision,
command, input and expiry matching, and the exact terminal challenge after showing
losses. It never refreshes a preview, changes a fingerprint or automatically retries.
Original lookups remain core.Runner commands, including narrow native own-grant
lookup; unavailable original outcomes remain unknown.

Credential issuance prepares the protected N01 lease, durably disables local use,
then makes one typed native request and publishes/readbacks the delivered file.
Self-revoke binds GET session to the exact loaded file before marker publication,
then core consumes that client for one DELETE using the captured bearer. Lost
responses retain the disabled marker. A preexisting marker leaves revocation
uncertain without an ordinary GET or token reuse. A new explicit native issue is
a separate action; grant confirmation never implies credential delivery.

Session-code issuance requires an actual console input handle and exact challenge.
Its production sink opens and validates CONOUT$ directly and uses WriteConsole
once. Stdout, stderr, redirected files, clipboard, evidence and ordinary results
never receive the code. Invalid, expired or incomplete presentation stays unknown;
there is no automatic retry. Public SessionCodeCLI reports only presentation and
nonsecret metadata; presentation does not establish login.

Setup delegates native locking, material, trust planning and publication to the
setup manager. It starts no server or browser. Production trust and owner-renewal
confirmation require actual terminal input; JSON supplies no bypass.

Tests use synthetic data and explicit private test seams. The tagged native suite
requires a compiled private-root-only overlay sentinel and fake trust provider,
with actual pinned TLS, protected Windows files, service admission and immutable
evidence. Its positive console and confirmation paths use synthetic callbacks;
they do not qualify interactive console presentation or meaningful human consent.
Actual pipe handles must refuse console use. No live setup root, trust action or
real grant is touched. Mocks and process interruption do not prove power-loss
durability, hostile same-account isolation or secure erasure.
