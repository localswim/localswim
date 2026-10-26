Board HTTP and prepared-evidence boundary

BoardClient.open composes the production IndexedDB driver, W01 RecoveryStore and
boardBoundaryVerifier. Supply installationId/profileId, an ephemeral in-memory
credential getter, and the trusted composition root's ProfileQualification proof.
Qualification defaults to absent. No feature detection or synthetic test assertion
qualifies the product profile; later browser qualification must supply real proof.
Recovery-open failure still permits authenticated reads but refuses submissions.

session reads the credential-bound actor independently. Board/card reads validate
exact generated structural schemas and selected identity. Go remains authoritative
for grants, coherent projections, admission and domain invariants. HTTPS, same-origin
relative routes, no cookies, no redirect following and no-store are required. Tokens
exist only in the Authorization header; no session/credential DTO enters recovery.

submit detaches the caller's input before awaiting, rechecks the current actor/scope,
prepares exact accepted semantic bytes and inserts immutable evidence before its
single initial dispatch. W01 requires strict transaction completion and fresh exact
readback. The concrete prepared verifier rejects unknown/duplicate keys, unsupported
command/target shapes, invalid Unicode/numbers and incorrect semantic/canonical
correspondence. SHA-256 binds those bytes. Optional omissions normalize to null only
inside the semantic payload; original wire text is retained separately.
Nonblank validation uses C01's exact Go Unicode White_Space set, including U+0085
and excluding U+FEFF. This predicate never trims or normalizes the retained text.

The structural schema snapshot in schema-data.ts is extracted from the generated
mutation, transport and archive schemas, including transitive references. It is
boundary validation, not another domain/admission model. When the contract changes,
compare every selected definition and reference with its generated source before
updating this snapshot. Boundary tests compare all seven supported commands with
accepted independent semantic-byte vectors; Go contract fixtures remain authoritative.

Terminal handling binds the original context/opId/hash/command/target/preconditions
through W01 and verifies exact full/compact schemas, terminal class, revisions,
command-specific result facts and result-ID correspondence. Only matching terminal
proof appends classification. HTTP errors, unknown/refused bodies, cancellation,
lookup denial and unavailable evidence preserve unknown. No retry, replay, automatic
confirmation or unchanged-state/text inference exists. lookup loads original evidence
and only GETs its original operation. Loading evidence never authorizes dispatch.

Synthetic component tests use a fresh isolated profile and an explicitly synthetic
qualification assertion. They establish local DOM/IndexedDB behavior and accepted
byte-vector correspondence, not product browser qualification, TLS/session/backend
security, real CLI interoperability, native durability or power-loss safety.
