K02 - Explicit board CLI adapter

Authority and surface
Accepted D03 transport-envelopes.txt, D01 setup.txt credential selectors and the
accepted Slice01 04/05/18 contracts control this package. This is a library;
executable wiring remains a separate composition task. No board, actor, credential
or installation is inferred from the working directory, environment or file list.

Runner.Run accepts the exact ordinary prefix:
  [--json] --installation ID --board B --incarnation I --grant G COMMAND
Selectors occur once, before the command. It supports board read, card read C,
card history C, card create, card edit C, comment add C, card link-add C,
card link-remove C, card review-handoff C and card approve C. Mutations require
--input FILE; --input - reads bounded UTF-8 JSON from stdin. Shared D03/C01
validation rejects ambiguous or unsupported input and binds the positional card.
The supplied incarnation must match the selected incarnation; nothing is repaired.
Selectors and positional IDs must have their exact raw canonical UUID spelling;
literal escape sequences, quotes and control characters are never decoded into IDs.

Original lookup is operation read O --evidence FILE. The separate native-owner
prefix is [--json] --board B operation read O --evidence FILE --native-owner.
It refuses ordinary-only selectors and is limited to original grant.create or
grant.revoke evidence. There are no setup, grant administration, credential issue,
attachment, export, restore, recovery-admin or private-clear commands here.

Authentication and transport composition
Runner requires an explicit Provider and evidence.Store; absent dependencies fail
closed. Provider.Open must load exactly the current N01-validated selected private
credential file and disabled marker. It must verify account-private ACLs, no
reparse links, installation/origin/scope, expiry and the current native context.
Provider.NativeOwner must load the current N01 owner source for that original
installation. Neither method may issue credentials, bootstrap attribution, select
another grant or substitute a different actor. The actual credential-file provider
and executable registration remain K03/P01 work; this package supplies no default.
The shared authenticated Client facade composes downstream attachment/export/admin
calls with this same pinned transport, selector/session binding and evidence path.

NewBoardConnection composes validated N01 connection material with validated
credential metadata and an ephemeral bearer. NewNativeOwnerConnection accepts the
typed current N01 owner source. TLS trusts only that installation's validated
issuer, checks the original hostname, and dials the exact N01-validated stored
port directly on loopback. First setup defaults to 47443; an explicitly chosen
stored port in 1024..65535 remains fixed for that installation. There is no CLI
port override, search or fallback. There is no proxy, system-root substitution,
HTTP downgrade, redirect, certificate-warning bypass or automatic HTTP replay.
Returned connection objects
refuse JSON serialization and redact ordinary formatting.

Before each ordinary invocation's board request, GET /api/v1/session authenticates
the bearer at the pinned origin. Its server-derived actor and board/incarnation/
grant must equal the selected validated metadata. The server still rechecks current
rights and revocation on the actual request. Reads verify the returned selected
identity and card reference before exposing content. No new incarnation is adopted.

Evidence, outcomes and output
Mutations freeze their independently selected route and authenticated actor using
the shared semantic contract. The evidence store must exclusively publish, flush
and independently read back identical private bytes. Runner then loads and compares
the stored record again before dispatch. Every storage error prevents the board
mutation, preserving input. It sends only the frozen original wire content once.

Lookup loads existing validated evidence first and compares original installation,
origin, actor, board, incarnation and operation. It sends only a GET with original
incarnation/version/hash. It never reconstructs absent evidence, executes an intent,
changes an opId, rekeys evidence or retries. Native-owner lookup also remains subject
to the server's narrow current-owner own-grant entitlement.

Only a strictly decoded authenticated terminal response matching every original
identity, command, target, fingerprint and precondition closes uncertainty:
committed exits 0; not_committed exits 2. A nonterminal HTTP refusal, echoed opId,
missing row, malformed response, disconnect, cancellation or failed lookup does
not prove noncommit. Possible transmission without terminal proof exits 4 and
preserves original KnownIdentityUnknown. Without complete validated identity,
lookup emits NonterminalRefusal and still exits 4. A late response after cancellation
does not change the result or replay the request.

New invocation/IO errors before dispatch exit 1, ordinary read/selector refusals
exit 2, and provably unavailable reads/preflight exit 3. These classifications
never downgrade original lookup uncertainty. Diagnostics use fixed inert categories
on stderr; invalid bodies, secrets and private paths are not echoed. JSON mode
emits exactly one validated D03 envelope with its final LF. Human output identifies
the outcome and selected board, followed by the same readable facts. Output failure
after a possible save exits 4.
An invalid human-output board selector is replaced by the inert label unavailable;
rejected selector text never reaches the terminal outcome line or diagnostics.
Ordinary board read appends separate Dependency pressure and Prerequisites/readiness
lines derived only from that complete validated current snapshot. Direct active
blocking links identify dependents and prerequisites with their assigned priority,
lane and archived state; priorities never change. A prerequisite is satisfied only
with current human approval under the current policy and reviewed card revision.
Foreign/missing targets stay unsatisfied; review handoff awaits human acceptance.
These facts are advisory: the server rechecks a deliberate start, including its
other admission rules. Use board read for dependency advisory; card read lacks the
complete context. JSON output retains exactly the existing transport envelope.
Nonprinting characters in received diagnostic codes are escaped for stderr;
ordinary safe codes stay readable and the exact JSON/proof fields are unchanged.

An exact terminal result appends a separate bounded resolution marker through the
evidence adapter. A marker write failure is reported on stderr while preserving the
confirmed board terminal result. It does not erase a saved board or rewrite its
original evidence. Export/local delivery has no save implication and is outside
this adapter's command set.

Verification and limits
Ordinary synthetic tests cover all commands, exact output families, exits 0-4,
stdin/files, malformed input, selector/current-session failures, route/identity
binding, no dispatch on evidence failure, no replay and lost/late response handling.
The k02_native suite requires a private root-only N01 overlay and a compiled
qualification sentinel. It uses actual N01/G02/T01/K01 source, current synthetic
credentials, issuer-pinned dual-loopback HTTPS and private NTFS evidence, including
commit-disconnect-original-lookup and narrow native-owner reconciliation.

Mocks do not qualify authentication, native ACLs, filesystem durability or network
delivery. Native fixtures never install trust or touch real boards/credentials.
Process interruption and flush/readback do not prove power-loss durability. Product
credential-file selection, executable composition and broader release qualification
retain their separate gates. Public source uses synthetic fixtures only.

K03 authenticated facade
Open validates mandatory canonical selectors, explicitly injected current private
credentials and the exact GET session actor/B/I/G before exposing a Client.
Context and Metadata are value copies; Close is idempotent and releases transport
plus an optional credential lifetime callback supplied to NewBoardConnection.
ParsePrefix and ValidateSelectors share K02 prefix-only, once-only raw UUID rules
with subcommand parsers; absent selectors never select a credential implicitly.

Request permits only selected-board reads, recovery preview and export. It returns
bounded exact bytes with copied response headers (maximum 144 MiB), rejects
content encoding, truncated declared lengths and cancellation, and cannot bypass
immutable mutation dispatch. Consumers still validate exact DTO, binary media,
disposition, identity, digest and archive integrity before local delivery.
Dispatch and DispatchAttachment reuse K02's Freeze, private Save and independent
Load/readback, one HTTP attempt, exact terminal classification and separate
resolution marker. Attachment bytes are bounded, snapshotted and length/hash
checked before evidence publication; only frozen metadata and those exact bytes
enter the two-part multipart body. A fresh attachment/grant creation may return
201; lookup and retained receipts still require 200. No transport/admission/schema
or semantic fingerprint algorithm changes. Outcome contains only public validated
terminal/refusal/unknown envelopes plus the exit, never a bearer or private error.

OpenCurrent requires separate explicit NativeCurrentProvider injection, without
ordinary credential fallback. Native clients can dispatch only grant.create and
grant.revoke and reconcile only their own original grant records. They cannot
read general boards/cards/history/export or dispatch history/card mutations.
IssueCredential and IssueCode accept typed exact requests and invoke an explicit
in-memory consumer only after strict scoped D03 decode and no-store validation.
They expose no raw success body or public Outcome. A strictly decoded refusal
with the exact expected 4xx status/code stays refused;
malformed, mismatched, 5xx, lost or post-publication failures stay unknown, without
retry. This exception is confined to credential/code issuance; ledgered mutations
and original lookups still require exact terminal proof. Callbacks must not
log/serialize secrets or call back into that Client. Secret delivery grammar and
native storage publication remain separately authorized composition work.

Credential self-revocation
RevokeCredential requires an ordinary Client already bound by Open's GET session.
It spends that Client, calls the supplied exact local credential disable callback,
then sends the same captured bearer once with DELETE /api/v1/session, an empty
zero-byte body and Content-Type application/json as required by the shared T01
write boundary. An empty object is never substituted.
The callback must flush/read back the disabled marker before success; it must not
reenter the Client. An unclassified callback failure stays unknown; a fixed
Failure may preserve a proven local class. Cancellation before network dispatch
stays unavailable; after possible transmission only exact HTTP200 SessionRevoked
confirms. A strictly decoded current-credential refusal with the exact expected
4xx status/category returns refused while the local disabled marker remains.
Malformed/foreign/unexpected responses, 5xx, disconnect and late cancellation stay
unknown. This credential-action classification supplies no original-domain proof.
No retry, extra GET, ordinary use or private mutation evidence publication
follows on that spent Client. Its secret-free Metadata remains available for the
D01 CredentialCLI result. Native owner clients cannot self-revoke board credentials.
CONFIRMATION_REQUIRED and CONFIRMATION_REFUSED retain their fixed public diagnostic
categories; no supplied confirmation text or unsafe response enters output.

Configured-port conformance preserves the evidence format/version and original
installation/origin identity. Canonical explicit ports are accepted across the
N01 range; authority, hostname/SNI and pinned issuer still match exactly. The
native fixture also verifies an explicitly initialized nondefault port, private
evidence publication, commit-disconnect and original lookup without replay, plus
rejection of evidence carrying a different original origin.
