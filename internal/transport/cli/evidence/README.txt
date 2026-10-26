K02 - Private immutable CLI submission evidence

Record and authority
Accepted D03, D01 and Slice01 04/05/18 require original semantic evidence before
board dispatch. This private versioned file format is not a public transport DTO.
Freeze delegates strict mutation validation, route/target/actor binding, omission
normalization and hashing to the shared D03/C01 contract. A record retains validated
installation/origin metadata, route, normalized original wire, exact canonical
semantic bytes and fingerprint. It contains no bearer, login code or credential.
Decode rejects incomplete, corrupt, unsupported or mismatching records instead of
manufacturing missing original identity. Byte/identity access returns owned copies.

The semantic identity includes board, incarnation, operation, authenticated actor,
command, target, payload and exact preconditions. MatchesTerminal compares the
entire original identity and fingerprint. Evidence alone proves neither authority,
dispatch nor a terminal result; current authenticated server proof remains required.

Native storage
NewWindowsStore requires an explicit absolute private per-user evidence directory.
Composition must select one shared per-user directory across boards; separate
board roots would bypass the accepted shared caps. No implicit/default root or
weaker filesystem fallback exists. Unsupported platforms/volumes fail closed.

The Windows amd64 provider requires fixed-drive NTFS and canonical local paths.
It creates protected owner=current-process-account and current-user+SYSTEM full
control ACLs at creation. It validates effective owner, protected DACL and exact
allow entries through native handles. It rejects broad/inherited ACLs, reparses,
hardlinked files, redirecting ancestors, unsupported volumes and unsafe names.
Ancestor directory handles stay pinned without delete sharing during operations.
The cooperative same-account boundary is not hostile-process isolation.

One native exclusive lock serializes admission and resolution across instances.
Save exclusively creates OP.json.pending, writes all bytes, flushes the file,
closes it and independently checks private ACLs and identical readback. It moves
exclusively with write-through to OP.json without replacement, then checks final
readback. Load is read-only and requires the exact validated operation filename in
the selected root. Existing files are never overwritten or repaired. Any failure
refuses dispatch and retains original/incomplete files for explicit inspection.
There is no automatic cleanup/retry of failed submissions or replay API.

Limits and resolution
Per-user submitted evidence retains at most 100 unresolved records and 8 MiB of
reserved storage across boards. Every admitted record reserves 512 bytes within
that unchanged 8 MiB for its bounded later resolution marker. Actual status bytes
never create an unbounded copy of a received response. Partial original/status
files retain their reservation, and unknown material fails admission closed.

Resolve requires a validated exact matching D03 terminal proof and appends only
version, original fingerprint, terminal class and local whole-second resolution
time. It privately creates, flushes, readback-verifies and exclusively moves that
bounded marker. It never edits original semantic bytes, consumes a new submission
slot or changes board state. An existing matching marker is observed without
rewriting its first resolution time. A failed marker leaves original evidence
unresolved; the caller retains the already received terminal proof separately.

Resolved records do not count against the 100 unresolved limit but keep their
storage reservation until expiry. ExpireResolved is an explicit composition API
that removes only validated records resolved at least seven days earlier. It
never expires unresolved evidence. Removal starts with the marker so an interrupted
expiry conservatively retains unresolved original evidence. No automatic eviction
of unexpired input or unresolved records occurs. Private inventory/discard CLI
controls, including the separate warning before unresolved loss, remain outside
the scoped core grammar and must not be inferred from this expiry API.

Verification and limits
Pure tests cover immutable aliases, strict private decoding, Unicode/control-text
roundtrips, route mismatch and complete terminal identity. The k02_native suite
uses newly created private synthetic NTFS roots and exercises exclusive creation,
actual protected ACL checks, hardlink/junction refusal, lock contention, injected
flush/readback failures with source preservation, cross-board shared caps, reserved
resolution at record/byte cap and seven-day resolved/unresolved retention.

All fixtures are invented. No real board, credential, trust store or user recovery
is used. Native readback and write-through are requested primitives, not a claim
of hardware power-loss durability, secure erasure or same-account confidentiality.
The caller owns current authentication, exact per-user root composition, original
outcome lookup, explicit warned discard and executable registration.
