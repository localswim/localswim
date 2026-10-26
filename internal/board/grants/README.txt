G01 - current grant admission foundation and first-owner policy proposal

Authority and limits
Accepted slice01 03/05/18, corrected 09/20/21, current setup.txt D01 revision 3,
transport-envelopes.txt D03 and archive-recovery-schema.txt control this seam.
Public MIT FOSS; synthetic fixtures only. Prior exposure is disclosed; no
independently isolated clean-room certification is claimed. No v1 code is copied.

Terry approved the bounded G01 foundation and S04/G02 decomposition on 2026-10-26.
This package supplies reusable current-grant admission and ledgered grant mutation
callbacks. S04 (#0055) supplies the missing auth-state schema/provider; G02 (#0056)
retains D01 section 6c's mandatory durable CLI credential issue/replace/hash
persistence/revocation service, real native-provider composition and first-owner
initialization. Those obligations remain unfinished, rather than being replaced
by a verifier callback or reassigned to HTTP delivery. Existing accepted providers
are unchanged by G01. B01/T01/H01 runtime consumers await accepted G02 as well as
their other prerequisites. Proposed workers or implementation availability cannot
waive acceptance gates.

G01 also includes the first-owner policy proposal at
docs/decisions/native-owner-attribution-bootstrap-proposal-2026-10-26.txt.
That proposal requires Terry acceptance before G02 implements it. It proposes an
explicit current-provider-authenticated attribution preparation step before S02
Submit, including first-command durable refusal, with no grant/right or domain
revision effect. This foundation does not perform that preparation or bootstrap.
Do not compose a synthetic verifier into a product or claim authentication from
these tests. No endpoint, executable, browser runtime, CLI confirmation/private
file publication or actual human enrollment/bootstrap is supplied here.

API and current authority
- New requires a trusted Verifier plus the fixed installation/origin. There is
  no default verifier. The verifier must independently resolve the presented
  credential, check its CURRENT hash/replacement/revocation/restart admission,
  and return exact credential bindings using the supplied transaction snapshot.
  It is trusted internal Go code, not a sandbox or cryptographic attestation.
  Credential fields, a constructed principal, Intent.actorId, cached rights,
  assignment and bearer text do not independently establish authority.
- Verifier runs on every Authority, Admit and Require call; no prior request's
  authentication result is reused. Use Read sequentially, close Rows, honor
  cancellation, and never retain the seam or perform external IO. The production
  durable verifier is unavailable until S04/G02 are implemented and accepted.
- Each check binds installation/origin, actor, current board/incarnation and
  enabled exact grant; actor kind and rights come from current storage. CLI and
  browser absolute validity limits, browser idle expiry and future issuance are
  checked again here. The verifier owns active hash and server restart epoch.
  Browser idle refresh/session-code consumption are not implemented.
- ValidateRights requires known, sorted, unique rights. Empty rights imply none.
  Automation can never receive approve, history-admin or grant-admin; even
  synthetic forbidden stored rights fail closed. No right implies another.
- Require checks ordinary current rights in the caller's snapshot. Consumers
  choose the rights required by their read/export operation. Admit checks command
  rights; domain consumers still own domain invariants. Recovery administration
  requires read AND history-admin; lookup separately requires read-own-outcome.
- Authority binds S02 LookupIdentity on Submission and OwnOutcome. Submission
  establishes current identity before a command-right refusal can be ledgered.
  OwnOutcome independently rechecks current read-own-outcome for the exact
  original actor, including repeat submission and post-COMMIT reconciliation.

Native owner boundary
A trusted verifier may return native-owner only after checking the CURRENT N01
native capability's active hash, time, installation, stable ownerActorId and
origin. An N01 snapshot alone cannot assert currentness; later composition must
hold the installation service lease and use the current validated provider.
This package supplies no fake native token matcher, native secret store or
unverified fallback. A native credential must have no ordinary grant scope.

Native owner can submit only ledgered grant.create/revoke, and can read ONLY its
own retained grant.create/revoke receipt. S02 supplies stored OperationIdentity
including compact evidence; missing metadata refuses. Native owner gets no
ordinary read, arbitrary-actor outcome, board/card/history/export, approve or
history-admin bypass. S02's exact fingerprint/target/identity and current-ledger
checks remain mandatory; source archive evidence cannot settle original outcome.
The acting actor must already exist as a human attribution row. No bootstrap
actor or fake grant/right is inserted to satisfy a foreign key. An empty S01 board
therefore cannot dispatch a first native grant through this foundation alone.
The linked proposal addresses legitimate first-owner attribution before S02's
refusal-capable transaction; acceptance and runtime implementation remain G02
gates. Native capability and attribution kind never prove a human at the keyboard.

Grant mutation callbacks
H01 supplies Route independently: validated create board scope, or revoke's stored
scope and exact route grant ID. GrantCallbacks copies it and rechecks it against
the frozen intent and current stored revoke target inside the serialized writer.
Create requires null target; revoke requires its exact grant target. Both require
null card revision; S02 enforces exact board revision without implicit rebase.
Existing grantee kind cannot change. Explicit creation may insert the actual new
grantee attribution row and the fresh grant fact/record/rights atomically; rights
are never synthesized to authorize the caller. Revoke disables the current grant
without deleting actor, historical grant fact or historical attribution.

S02 owns board revision increment, current ledger, durable NOT_COMMITTED refusal,
receipt reconciliation and COMMIT. Apply creates one activity event, advances its
counter once and returns only the non-secret grant ID/fact. It never issues a
token, returns a token/hash, changes card revisions or replays an operation.
Refusals change no domain revision/activity/grant/actor. Event counter overflow
refuses before effects. Apply rechecks admission in the same transaction.
Current authority is checked again on outcome readback: revoking the submitting
grant can commit while its ordinary credential cannot read the receipt afterward.
The narrow current native entitlement may resolve its own administrative receipt.

Verification
Go1.27.3 Windows/amd64, CGO_ENABLED=0, GOTOOLCHAIN=local, GOFLAGS=-mod=readonly,
frozen qualified module cache; private fresh GOCACHE/GOTMPDIR/S01_TEST_ROOT outside
the public repository. Missing S01_TEST_ROOT skips native tests and is not a pass.
Run:
  go vet ./internal/board/grants
  go test -count=2 -timeout=120s -v ./internal/board/grants

Synthetic native S01/S02 fixtures cover human/automation rights, no implied rights,
invalid rights and credential bindings/times, enabled current grants, exact grant
scope/target, atomic create/revoke/activity/receipt, refusal invariants, own-grant
revocation and restricted native full/compact outcome lookup, verifier rechecking,
absent verification/callbacks and both serialized revoke/admit orders.
These exercise trusted synthetic principals, not production token verification,
CLI hash durability, restart/session lifecycle, actual enrollment/confirmation,
HTTP Host/Origin, browser behavior, hostile same-account isolation, crash/flush/
power-loss durability or complete native qualification.
