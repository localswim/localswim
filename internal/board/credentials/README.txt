G02 - durable CLI credentials and current native-owner composition

Authority
Accepted D01 setup.txt sections 6a/6b/6c, D03 transport-envelopes.txt, G01 current
grant admission, S04 layout 2 and the owner-accepted native-owner attribution
proposal control this package. Public MIT FOSS; synthetic fixtures only. Prior
exposure is disclosed; no independently isolated clean-room claim or v1 copy.

Composition
- SourceRoot loads the actual N01 Manager and acquires its actual ServerLease.
  It owns that installation exclusion until Close. Each native authentication
  reloads validated active N01 material and checks exact lease context, active
  token, time and stable owner actor. Caller-supplied principals, flags, owner
  snapshots and actor claims cannot establish authority. The private provider
  interface is trusted internal lifecycle composition, not hostile plugin isolation.
- T01 may obtain Context/TLSConfig for its listener. Close waits for requests;
  the returned TLS config refuses new handshakes after Close and N01 checks leaf
  time. Startup may perform N01's existing stopped certificate lifecycle. It does
  not perform trust enrollment, create a board, issue credentials or register actors.
- Admission accepts only mode cli and composes real persisted hash verification
  with G01 in the caller's supplied DB snapshot. Every callback resolves the
  current active exact credential and G01's current enabled grant/kind/rights.
  No rights or authentication result is cached. Native callback access is private:
  use SubmitNative/NativeLookup so the lease spans the complete S02 journey.
  CLI callbacks may already hold an S01 lease and use nonblocking lifecycle
  admission. If Close owns or queues exclusion, they promptly refuse current
  authority; they never wait behind Close while a native request waits for S01.
  Browser registry/code exchange/restart epoch and HTTP/Host/Origin are T01.

Durable CLI service
- Issue requires the exact current native bearer and requested installation/B/I/G.
  In one S01 writer transaction, resolve the preexisting enabled current grant's
  actual actor/kind/allowed rights; create a cryptorand UUIDv4 and 256-bit canonical
  unpadded base64url token; persist only its SHA-256 and exact public bindings.
  Issuance uses transaction time truncated to seconds and exactly 30 days.
  Delete/insert replaces the sole active credential for G, with no overlap.
- S01's same writer serializes Issue with S02 grant revocation. Revoke-first
  refuses issue; issue-first cannot bypass a subsequent disabled current grant.
  Hash/expiry survive a normal close/reopen. Replacement and SelfRevoke deny the
  prior exact token. SelfRevoke authenticates only the exact current CLI bearer;
  it cannot delete a replacement or revoke a grant. Expired/revoked/foreign scope
  fails closed. Native owner and browser tokens cannot enter this path.
- These are auth-state changes only: no domain revision, operation, activity,
  actor, grant/right, policy or historical provenance changes. IssueResult's JSON
  and formatted output contain public metadata only. The only secret serializer
  delegates explicitly to C02 MarshalEphemeral; T01 must deliver no-store and
  discard bytes. No token retrieval, automatic retry or recovery/export record
  exists. Public locallyAvailable remains false; K03 owns verified publication.
- Before possible COMMIT, established rollback may return failed/refused.
  A COMMIT attempt, readback/cleanup uncertainty or lost delivery remains unknown.
  No secret/public success result is exposed for uncertain issuance. Unestablished
  rollback or close fences writes through S01; recovery requires explicit reopen.

Explicit first-owner preparation
- SubmitNative accepts only a strictly frozen grant.create/revoke for an
  independently resolved route. Hold the already owned N01 service lease through
  preparation and S02 Submit; never acquire another native lease in a handler.
  Lock order is native lifetime first, board writer second.
- Under an explicit S01 writer transaction, revalidate current N01 owner, exact
  board/incarnation and frozen actor=actual ownerActorId. Insert only that actual
  actor, kind human, inert label native owner, current provenance. Existing human
  kind/label/original provenance is preserved; conflicting kind/identity refuses.
  Exact attribution readback is required. This fact supplies no grant/right or
  meaningful-human-review proof and changes no domain revision/activity/receipt.
- Known rollback refusal stops before Submit. Uncertain preparation also stops
  without claiming a board result or retrying. A legitimate actor fact may remain.
  S02/G01 independently recheck current authority/time and revisions; first stale
  or denied requests can produce durable NOT_COMMITTED receipts. Successful
  preparation never means a grant succeeded. No other path prepares attribution.
- NativeLookup checks the original exact installation/origin before S02/G01.
  Current native authority permits only the same actor's retained grant.create/
  revoke outcome in the current board/incarnation, including compact receipts.
  Missing/wrong evidence or failed current authority resolves nothing. No source
  receipt adoption, other actor lookup, ordinary read or rights bypass exists.

Verification and limits
Normal go test/vet need no native setup root and never construct the N01 Manager
from this package's ordinary tests. The separately tagged g02_native suite requires
an explicit private qualification overlay; without it, compilation fails closed.
The overlay changes only N01 nativeRoot resolution plus a test-only root sentinel.
It does not modify provider/schema source or load an actual LocalAppData root.
All positive composition uses the real N01 crypto/files/ACL/lifetime provider and
real S01/S02 database, with fresh private synthetic roots outside the repository.

Use Go1.27.3 Windows/amd64, CGO_ENABLED=0, GOTOOLCHAIN=local,
GOFLAGS=-mod=readonly, frozen Q01 module cache and fresh private cache/tmp/root.
The private qualification runner records the exact overlay/source/log hashes.
Run normal package tests/vet and tagged private tests twice. Native cases cover
first success/stale/denied durable refusal, actor/provenance/context conflicts,
real source-root lifecycle/reload/expiry/Close, current rights, replacement and
restart, auth-only invariants, entropy faults, both revoke/issue writer orders,
commit/readback/rollback/close uncertainty/fences, compact original-context lookup
and secret exclusion. Fault injection is not power-loss or crash qualification.

No HTTP endpoint, browser session, terminal consent, private CLI publication,
actual trust/human enrollment, executable composition, hostile same-account
isolation, secure erasure or complete native/browser qualification is claimed.
T01/K03/P01 retain those boundaries and owner acceptance remains separate.
