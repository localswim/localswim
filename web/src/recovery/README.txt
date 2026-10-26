Private browser recovery store

This module stores profile-private plaintext drafts and submitted evidence in
IndexedDB. It does not supply UI, credentials, HTTP dispatch, replay, domain
admission, export, bootstrap or a browser canonicalization implementation.
Go remains authoritative. Client feedback and retained classifications are
advisory; a client record cannot extend server outcome availability.

Boundary and consumer gates

- Request and terminal types import the generated Go boundary declarations.
  PreparedEvidence carries exact wire and canonical UTF-8 strings, original
  installation/board/incarnation/actor, draft identity, command, fingerprint
  version/hash and attachment metadata/digest/length. Wire preconditions, target
  and operation identity remain in the immutable request bytes.
- A trusted BoundaryVerifier is mandatory. The future composition adapter must
  provide exact runtime schema/Unicode/duplicate-key validation and verify that
  the canonical bytes, request, command and original identity correspond to the
  authoritative contracts. A hash check alone cannot do that. No production
  provider currently ships; missing providers fail closed. Do not replace this
  seam with the independent test canonical reference or a permissive assertion.
- The store checks the recovery envelope, original context, request identity,
  preconditions, attachment correspondence, prohibited authentication keys and
  canonical SHA-256 binding. It does not duplicate the Go command/admission model.
  Terminal verification additionally requires the same board/incarnation/actor/
  operation/version/hash/command/target/preconditions as the inserted evidence.
- ProfileQualification is supplied by a trusted composition root, bound to the
  current installation and profile. It defaults to absent. The product browser
  profile qualification belongs to later qualification work; this module's
  synthetic test assertion is not product qualification. Do not derive a positive
  assertion from feature detection or these tests alone.
- Use the production IndexedDBDriver. A custom driver is a trusted adapter and
  must honor its serial transaction and completion contract. Every write requests
  strict durability and refuses implementations that do not report it. Submitted
  evidence produces a dispatch-ready receipt only after transaction COMPLETE and
  a fresh transaction reads back exactly the inserted bytes. Request success is
  insufficient. Unqualified/unsupported/denied/full/aborted storage, failed
  verification or failed readback produces no dispatch-ready receipt. Preserve
  caller input and the original identity on every failure; a failed readback may
  follow a successful commit. Never infer noncommit or automatically retry.
- A receipt permits the caller's single deliberate initial dispatch. It is not
  permission to replay after cancellation, timeout, navigation or reload. Duplicate
  insertion is refused even for identical bytes. Loading evidence never returns
  a dispatch-ready receipt. Reauthenticate and reconcile only its original
  identity under current rights. Missing/inaccessible/expired evidence, denied
  lookup, unknown/refused responses and HTTP errors cannot classify noncommit.

Drafts and retention

Persist each editing change with saveDraft and the previously observed revision.
The caller owns pending/saved/failed presentation and retains input until success.
Atomic revision CAS prevents two tabs overwriting each other. A stale revision
creates a fresh draft identity and preserves the winning draft and losing input;
the returned state is forked. A concurrent newer save before readback fails the
older saved announcement. Cap refusal also retains input with the caller.

Draft input is an editor's serialized prose/input string, never an authentication
DTO. Context and identities are separate validated fields. Existing draft context
is immutable; copyDraft is a deliberate action requiring a new draft identity and
explicit destination context. It leaves the source and every submission intact.
Restored incarnations do not rekey old submissions or grant destination authority.

Limits apply per installation in one origin/profile database, across boards:

- Drafts: 20 records and 2 MiB of complete stored JSON UTF-8, seven days since
  last edit. Only expired drafts may be removed automatically. Inclusive expiry
  is editedAt + seven days <= current time. Live input is never evicted at cap.
- Submitted evidence: 100 unresolved records and 8 MiB of retained submission
  plus reserved terminal-status JSON UTF-8. Admission reserves enough space for
  every accepted record's longest valid status in the existing format, including
  maximum local-clock width. Resolution consumes its reservation and cannot be
  refused by the admission cap. The reservation remains charged during resolved
  retention until expiry or explicit clearing; another board cannot consume it.
  Actual retained submission/status bytes stay within 8 MiB. Physical storage
  quota/transaction/readback failures can still prevent recording a status and
  must be reported without claiming resolution. Unresolved evidence never expires.
  Resolved evidence expires inclusively seven days after terminal status is
  recorded locally. Duplicate status does not refresh that retention clock.
- Expired records are hidden on reads and removed atomically with the next
  successful mutation. Failed mutations do not discard them. Metadata overhead
  and Unicode bytes count toward caps. Limits do not promise physical disk usage.

Submitted evidence uses add only and is independent of editable drafts. A matching
terminal classification appends to a separate status store, never updates intent
or draft bytes. Repeated identical terminal classification is idempotent;
contradictory classification is refused. Unknown is represented by absence of
terminal status. Retained terminal status is historical local feedback, not fresh
authorization or a guarantee that a future server lookup remains available.

Privacy, inventory and explicit clearing

Inventory returns context/identity/status metadata without draft or request prose.
Full private records load only through their original context and identity.
There is no export interface. Do not include this database in board exports,
public packages, logs, diagnostics or telemetry. APIs never accept a bearer,
login code, credential, password, private key or private authentication hash as
an envelope field. The boundary verifier must reject such data in request DTOs.
Arbitrary user prose cannot be classified as secret by a store: callers must
keep all authentication material in their separate credential/memory lifecycle
and must never serialize it into editor prose or prepared bytes.

clear accepts one exact-context draft/submission or all this installation's data.
It requires the caller to display a private-prose deletion warning and pass the
deliberate discard acknowledgement. If any selected evidence is unresolved, a
separate warning and acknowledgement must explain that later duplicate effects
may become unknowable. The flags are caller trust boundaries, not proof a human
saw a warning. Clear checks the unresolved condition inside the same transaction
and verifies deletion in a fresh transaction. It leaves other installations alone.
Logout/revocation clears credentials and visible content in its owning adapter;
it must not call clear or remove this database. Clearing credentials and clearing
private prose are distinct controls.

Qualification limits and tests

Strict durability is a requested browser policy. Completion and readback do not
prove power-loss safety, eviction immunity, same-account confidentiality, platform
storage correctness or recovery after site/device clearing. Known loss means
recovery unavailable, never a negative operation outcome. No production browser
profile, TLS, session, accessibility or end-to-end UI qualification is claimed.

store.test.ts exports a synthetic isolated-browser suite. Its boundary verifier
and qualification assertion are explicitly test doubles, not product providers.
Actual IndexedDB cases cover CAS races, close/reopen, immutable submissions,
late matching terminal responses, identity mismatch, private-field exclusion,
cross-board/installation limits, exact/near 8 MiB boundaries, concurrent admission
and terminal resolution at cap, reserved capacity through resolved retention and
clearing, UTF-8 accounting, inclusive expiry, unresolved retention and warned
clearing. Genuine submission/status add-success followed by transaction abort
exercises the production adapter's completion gate. Separate fault adapter
cases cover unavailable storage, absent strict durability, delayed completion,
abort, readback denial/mismatch and asynchronous caller mutation. Run these with
the pinned TypeScript compiler and browser runtime in an isolated fresh profile;
no package scripts or dependency installation are supplied by this module.
