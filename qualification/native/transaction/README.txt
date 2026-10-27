V01 staged native transaction/fault evidence

Current staged result: PASS. The unchanged original seven cases and all seven
private continuation child cases pass, with no skips. Fresh independent review
passed for this staged V01 scope; see docs/reviews/v01-native-transactions-review.txt
from the repository root. CONTINUATION.txt and results.json record exact limits.

Pre-COMMIT process termination preserves the closed valid main DB byte-for-byte
and retains its journal. Three restart attempts safely refuse before SQLite,
preserving the complete inventory and bytes. The original stays unresolved
UNKNOWN; no authoritative lookup, recovered rollback, successful hot-journal
reopen or resumed write is claimed. S01's pending-recovery limitation remains.
Clean postcommit restart and busy/FULL relational rollback are separate rows.

The staged command also requires canonical private V01_ARTIFACT_ROOT outside
the public repository, alongside the inputs below. It retains each unique
attempt and fails on missing/unsupported/zero/skip/source/root mismatch.
The continuation first requires a specific three-symbol missing-overlay build
failure, then uses exactly three hash-bound private overlays. No uninstrumented
child executes and all 247 original provider hashes remain unchanged.

The first continuation attempt and its source freeze remain immutable. That
attempt incorrectly expected a successful hot-journal reopen; the accepted
contract requires safe refusal with unresolved UNKNOWN pending recovery. The
corrected test uses the same existing provider and three overlays. It neither
adds recovery behavior nor removes or bypasses sidecars.

Historical frozen seven-case subset description follows. Its statements about
unexecuted journeys and no listeners apply to that original subset only.
The original subset review does not review or qualify the continuation.

V01 native transaction evidence: historical incomplete subset

This staged suite has seven native Windows/amd64 cases. Its passing test result
does not qualify V01: mandatory fault and CLI journeys remain unexecuted. See
results.json for the coverage matrix. V04 owns the root native dispatch.

From the repository root, with exact Go 1.27.3, CGO_ENABLED=0,
GOTOOLCHAIN=local, GOOS=windows, GOARCH=amd64 and GOFLAGS=-mod=readonly:

  go test -tags=nativequalification ./qualification/native/transaction

Set V01_NATIVE_ROOT to an explicit canonical private fixture directory outside
the public repository, on a fixed local NTFS volume. Keep GOCACHE, GOMODCACHE
and GOTMPDIR private. Missing input and unsupported platforms fail; no native
case skips or substitutes another platform. Each invocation creates short n1-*
directories and retains its invented data for inspection. Do not reuse these
fixtures as a user installation, real board or credential store.

Executed native behavior
- Concurrent product card writers serialize through the shared writer; one
  exact-revision create commits and the other retains NOT_COMMITTED. Original
  identity resubmission returns the same terminal classification with unchanged
  state counts; receipt-byte equality is not asserted by this subset.
- Product grant revocation commits before the other card writer is admitted.
  The start signal does not establish that the other writer had already entered
  the queue or was waiting on a lock. This proves revocation-first admission.
- A real independent SQLite reader transaction blocks product edit COMMIT.
  Successful effect staging is observed; UNKNOWN and absent lookup remain
  UNKNOWN. Current text, history, activity, receipts and byte/reference counts
  remain unchanged. A deliberate distinct later identity succeeds.
- A native SQLite max_page_count limit produces verified SQLITE_FULL (13)
  without filling the host disk. Product attachment admission then fails with
  UNKNOWN and no partial current state, history, receipt, BLOB or pin counts.
- Explicit Store.Fence refuses writes until close and validated reopen. This
  tests fence enforcement, not automatic fencing after uncertain cleanup.
- Real private evidence CREATE_NEW, flush/readback and exact semantic identity
  are checked. Independent Windows owner/DACL inspection verifies the two
  private principals. Duplicate creation and a deliberately unsafe ACL refuse.
- A child process commits create/edit, signals its checkpoint and is killed
  without graceful store shutdown. Reopen preserves current and prior prose
  plus receipts. A later comment survives graceful restart and original lookup
  adds no second comment. The child only opens its assigned synthetic board.

Fixture and inference limits
- Synthetic credential verifiers supply invented authenticated principals.
  Product G01 current rights and S02 admission execute against real SQLite.
  This does not qualify G02 credentials, pinned HTTPS or native-owner setup.
- The write failure is SQLite's real database-size-limit error, not an OS
  disk-full, device write error or failed FlushFileBuffers. Reader contention
  supplies a real COMMIT failure; no callback substitutes for either error.
- Explicit fence enforcement does not prove detection of a failed rollback.
- The process is killed after confirmed COMMIT. Interrupted precommit/hot
  journal recovery, OS restart and physical power-loss safety remain unproved.
- The direct engine original lookup is not a CLI lost-response journey.
- No provider source, schema, module lock, real user ROOT, trust store, human
  enrollment, login, activation or live board is changed. No listener is used.

Retained work requiring scoped seams/environment
- Test-only private setup root plus a compiled sentinel, so actual pinned CLI
  journeys cannot reach the user's production root.
- Existing private writer fault callbacks for labeled synthetic before/after
  COMMIT, readback and rollback uncertainty; no production bypass API.
- Existing private evidence flush/readback callbacks for labeled synthetic
  CLI predispatch failure. A safe native flush-fault environment remains a
  separate requirement: callback injection cannot establish hardware behavior.
- Actual pinned CLI commit/disconnect/restart/original-evidence lookup,
  automatic uncertain-cleanup fencing and product log redaction evidence.

The final subset was executed with fresh private runtime/log/result paths.
Earlier runner and compile failures are retained privately and are not counted
as native passes. Public results contain sanitized summaries only. Independent
review and all missing mandatory rows are still required for qualification.
