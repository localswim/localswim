S02 - serialized writer and current terminal evidence

Authority and boundary
Accepted slice01 03/04/18 and corrected09/20/21 control transaction/outcome
behavior. Current docs/contracts/transport-envelopes.txt and
archive-recovery-schema.txt control exact envelopes and retained facts. The
owner's acceptance records establish authority independently of proposal headers.
Public MIT FOSS, synthetic fixtures only. Prior exposure is disclosed; no
independently isolated clean-room certification is claimed.

This is a shared internal transaction seam, not a command dispatcher, HTTP/CLI
adapter, authentication implementation, grant policy, archive reader, retention
executor or recovery tool. S01's qualified Store/Writer/Reader/Lease.Do remain
unchanged. C01 Intent supplies immutable semantic bytes/identity; C02
FreezeMutation supplies exact D03 wire refinement. Base additionally rejects a
missing/zero existing-card precondition. No v1 implementation is copied.

API and authority obligations
- New(store).Submit(ctx, frozenIntent, callbacks) requires Authority. It first
  checks the current ledger in the serialized transaction. A matching retained
  identity requires only current OwnOutcome authority; it does not require
  Submission authority or Admit/Apply callbacks and cannot execute them.
  New identities require Authority(Submission), Admit and Apply; missing required
  callbacks fail closed without effects. Mismatch/corrupt retained evidence also
  requires OwnOutcome before disclosing a retained-key classification.
- Authority receives AuthorityRequest with the requested LookupIdentity and
  immutable OperationIdentity metadata. Submission metadata comes from frozen
  semantic evidence; matching retained metadata comes only from the stored row,
  including compact rows. It includes command, target and exact preconditions,
  permitting G01's current command/own-grant restrictions without a payload or
  fabricated Intent. Metadata is absent for missing/mismatching/corrupt evidence;
  authority must fail closed when its policy requires unavailable metadata.
- Authority(Submission) must independently authenticate the live credential,
  bind its actor and requested board/incarnation, and establish current authority
  to record a new identity's outcome. It does not gate resolution of a retained
  identity. A callback's presence, Intent actor, a
  constructed principal, cached grants or a previous request is not this proof.
- Admit must read current grants/session admission, policy and command/domain
  invariants inside this serialized transaction. It returns either a typed
  RefusalResult, nil for admission, or an internal failure. Domain-specific
  counters, workflow, ownership, quota and command permissions remain consumer
  obligations. The writer checks board/card CAS and their revision ceilings.
- Authority(OwnOutcome) must independently check current read-own-outcome
  authority for the original actor/identity, including current revocation and
  session admission. It runs before exposing any matching terminal receipt,
  including repeat submission and post-COMMIT reconciliation. A synthetic
  allow/deny callback in a test is not a production authentication provider.
- Lookup(ctx, lookupIdentity, authority) accepts exactly D03's route board/opId,
  query incarnation/fingerprintVersion/fingerprint and independently authenticated
  actor. It validates those values, reads current-ledger metadata and checks
  current OwnOutcome authority in one reader snapshot before exposing facts.
  No Intent, command, target, preconditions, payload, new body or additional wire
  fields are required or fabricated. A compact row therefore resolves after full
  intent/result pruning. The stored actor and identity/version/hash must match.
  Submit and post-Submit reconciliation still match the complete original frozen
  Intent, including its command/target/preconditions and attempted attachment fact.
  LookupIdentity/AuthorityRequest are internal arguments, not new wire/file DTOs.
  T01/G01 retain exact query parsing, credential authentication and integration.
- Read offers bounded SELECT-only Query/Scan. Write additionally permits single
  INSERT/UPDATE/DELETE statements. Leading transaction commands, PRAGMA, WITH,
  comments, semicolons, NULs and multi-statement strings are refused by the narrow
  statement gate. Values go through bound parameters. No sql.Conn, Raw, sql.Tx,
  transaction-control method or alternate connection is exposed to extensions.
- Extensions are trusted internal Go code, not a sandbox or hostile SQL policy.
  Close all returned Rows before returning; use seams sequentially only inside
  the callback, never retain them or start goroutines. Do not perform external
  IO, acquire another lease, mutate identity/ledger tables, or publish a result.
  Code with other package/global authority can bypass this contract. The query
  gate is deliberately conservative, not a general SQL parser. Callbacks must
  honor cancellation; the SQL context has a ten-second ceiling, which cannot
  forcibly stop arbitrary Go code that blocks or ignores cancellation.

Atomic effects
BEGIN IMMEDIATE precedes current evidence/admission. Current evidence is checked
before new-submission authority/admission. A matching retained identity
is never executed again; a different actor/fingerprint/command/target/precondition
or attempted attachment fact refuses reuse without classifying the original.
Writer owns the single board revision increment. Apply owns command-specific
current state, affected-card increments, full prior snapshots, reference lists,
verified attachment bytes/current/history pins, ticket/event allocation and
activity. It receives the one whole-second UTC transaction timestamp. It returns
the exact retained result, sorted fact references and affected card revision.
Writer verifies the board/affected-card revision result, validates retained facts
with C03/C02, inserts full outcome/intent/result and attachment operation pins,
and checks S01's complete schema/relational/hash audit before explicit COMMIT.
The callback is a bounded atomic extension seam; these synthetic implementations
do not qualify B01/G01/A01 or any other domain command.

Typed validation refusals insert full NOT_COMMITTED identity/actor/code/time,
original semantic intent, RefusalResult and attempted attachment byte fact, with
no domain revision, activity, entity, history, byte or pin effect. Admission has
no mutation method. This durable row fences a delayed same-ID submission even
after a deliberate close/reopen. Refusal attachment facts do not fabricate
targets or bytes. Application errors/panics roll back all staged work; they
cannot be converted into a partial committed refusal or a noncommit claim.

Full/compact retained result validation uses the exact provider contracts.
Full committed attachments acquire the operation's matching digest/length pin
in the same transaction. Compact/refused byte facts never pin on their own.
No operation identity overwrite, upsert, deletion, compaction execution or
reconstruction of missing full details is provided.

Uncertainty and recovery
Every failed COMMIT, including actual reader-blocked SQLITE_BUSY, remains unknown
unless an exact independently authorized current retained receipt resolves it.
Cleanup uses a fresh bounded explicit ROLLBACK; the one known no-active-transaction
diagnostic is cleanup completion, never proof of noncommit. Application/before-
COMMIT failure followed by successful rollback still returns unknown when no
receipt exists. After-COMMIT transport/fault uncertainty may resolve through the
retained receipt. Post-transaction readback uses a fresh reader lease/snapshot
and independently rechecks current authority; own-grant revocation cannot expose
an outcome under stale rights. An authority refusal is a nonterminal D03 refusal,
not a NOT_COMMITTED claim about the original submission.

Rollback/recovery uncertainty or reconciliation readback failure calls S01's
one-way Fence. Its lease cleanup independently rolls back and closes failed
connections. No automatic replay, fresh opId, retry, unfence, source-outcome
resolution or recovery-open convenience exists. The operator must separately
close and explicitly validate/reopen storage before writes can resume. Missing,
inaccessible, corrupt, expired full details, wrong identity and source-archive
evidence never infer noncommit. Compact valid terminal metadata still resolves
the exact identity, with full detail unavailable. Source rows are excluded by
current_ledger plus exact storage/origin identity even for the same opId.

Result is an internal sum with exactly one Terminal, Unknown or Refused member;
adapters serialize the chosen C02 object and apply contextual D03 status/exit
semantics. Internal SQL/callback errors are not emitted as diagnostic prose.
Structurally valid receipt bytes alone never prove authenticity to a client.

Verification and limits
Use Go1.27.3, CGO_ENABLED=0, GOTOOLCHAIN=local, GOFLAGS=-mod=readonly and the
qualified frozen module cache. Keep new private GOCACHE/GOTMPDIR and an explicit
S01_TEST_ROOT outside the repository; fixtures are retained. Missing that root
skips native tests, and a skipped run is not evidence. Commands:

  go vet ./internal/store/writer ./internal/store/outcome
  go test -count=2 -timeout=120s -v ./internal/store/writer ./internal/store/outcome

Native synthetic tests cover purpose-specific retained/new authority gates,
descriptor-only full/compact lookup after removing the in-memory Intent,
malformed/actor/hash/identity/source/permission/command-metadata negatives,
exact terminal/repeat/mismatch/current revocation,
durable refusal and deliberate reopen, writer serialization, actual reader-
blocked COMMIT, application panic/failure, before/after-COMMIT/rollback/readback
fault injection, one-way fence, staged state/history/activity/blob/ledger atomicity,
zero-byte full operation pins, refused nonpins, compact proof, absent/corrupt/
wrong-incarnation/source-only evidence, and own-revocation authority rechecking.
Fault hooks are unexported and default nil; they model boundary errors, not native
write/flush failure, process crash, OS reboot, fsync or power-loss durability.
No performance, security, archive, product/browser, other-platform/filesystem or
complete native qualification is claimed. G01 real authority and B01/A01/etc
domain implementations, T01 adapters and later native qualification remain gated.
