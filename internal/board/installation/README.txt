M18 AUTHORITY-BOUND INSTALLED IDENTITY ENGINE

The installation Inspector owns ONE shared metadata/actual-operation slot per
physical Runtime. Existing Inspect remains metadata-only; it never chooses the
actual engine's candidate or invokes the native bridge.

Production methods
- Control(ctx, runtimeGeneration, configurationGeneration, Check) returns only
  the current ready/running/draining generation and native join observation.
- Probe(ctx, runtimeGeneration, configurationGeneration, probeGeneration, Check)
  consumes one server-created generation after fresh current admission.
- Stop(ctx, the same exact tuple, Check) cancels only that current actual operation
  or observes its one joined tombstone. It cannot cancel metadata inspection.
- Close fences admission, cancels current work/checks and reports incomplete after
  at most two foreground seconds while preserving every unresolved owner.

Check is current credential/read authority, exact saved configuration generation
and physical board/root/database binding, supplied by the selected Runtime. It
must not renew browser idle expiry. All callbacks are serialized separately from
state and file work. The active-only watcher has a 100 ms target interval; no
worker runs while idle. Errors from a cancelled watcher check do not invent an
authority-loss observation; live-context denials stay retained and disclosure
rechecks current authority after the watcher joins. Check cancellation is
cooperative, never an SQL/native
completion SLA. Started Control/Stop/final-disclosure checks retain Runtime
custody until they actually return, including after the HTTP caller has ended.

State is bounded to the current operation, one available crypto UUIDv4, one
joined tuple tombstone and one private candidate fingerprint. There is no captured
output/version cache, durable native journal, retry, automatic discovery or idle
renewal. An incomplete or ambiguously closed operation holds the shared slot.
Entropy failure cannot expose a zero/fallback UUID or start an invocation.

Actual preparation independently qualifies the complete M17-INSPECTOR-R3 native
resolver. Its held release directory, manifest and executable identities/security
and exact executable/manifest digests form the private runtime fingerprint.
A later changed qualified candidate requires a new Runtime; it never rebinds.
Held file cursors are used sequentially; the authority watcher never hashes them.
Provider qualification requires the SDK's complete 38-step zero-error inventory
and zero provider/final/signer errors. Native provider, signer, certificate and
CERT_CONTEXT address words remain uintptr through bounded ReadProcessMemory
copies; only at most 1 MiB of copied DER reaches the Go certificate parser.
Unavailable, partial or contradictory native evidence is INSPECTION_UNKNOWN.

The work subtree is fixed directly beneath the verified process token's profile
(PROFILE below comes only from that native token/profile API):
  PROFILE/.localswim-native-runtime/runtime-UUID/probe-UUID
The dedicated application base must be an exact protected account/SYSTEM-only
private directory; an absent base is created with that ACL. Existing unsafe state is
refused, never repaired. Runtime/probe leaves are exclusively created, with pinned
non-reparse ancestry and empty private home/appdata/temp/CODEX_HOME children.
No request or ambient HOME/TEMP value chooses a production path. Cleanup uses
held-object disposition and never follows child-created reparse targets. A
failed/ambiguous cleanup retains the operation and cannot authorize a successor.

The only production native bridge is nativecustody.ExecuteVersion. Its argv is
literal --version, with the strict detached/no-child/atomic-job profile. Public
results contain only finite state/code, actual joined and the exact validated
version on supported/OK. Raw bytes, paths, hashes, accounts, signer data and native
errors remain private. Manifest metadata never supplies an executed version.

Verification
- Default installation tests use injected package-private backends only. They
  cover one-use generations, shared metadata exclusion, tombstones, denied and
  expired admission, entropy failure, current watcher revocation, serialized
  checks, retained Stop/Close checks, late disclosure denial/deadline, candidate
  rebinding refusal, malformed backend evidence and ambiguous cleanup.
- Default parser tests cover exact line/endings, strict 96-byte ASCII SemVer,
  malformed encoding, stderr/extra output and manifest mismatch.
- Tagged nativecustodyqualification tests use ONLY owned compiled helpers and a
  fresh protected NATIVECUSTODY_TEST_ROOT with TEMP/TMP beneath that root. They
  include the restricted production bridge, actual private-root deletion,
  collision/unsafe-existing-root refusal and an external reparse escape canary.
  Missing setup fails; a zero-case/skip is not qualification.
- No M18.02 test invokes installed codex.exe. Whole-source II.05a review precedes
  the first installed invocation. Controlled helper PASS does not establish
  installed compatibility or the later complete native/total-process evidence.
