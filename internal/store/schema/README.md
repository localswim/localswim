# Version-seven physical relational layout

Version and PRAGMA user_version are7; retained board.schema_version stays5
as the immutable base-schema discriminator, including fresh physical7 stores.
Validation checks both intended values and exact schema, not a header alone.

Layout6 adds card_parent and its parent index without rebuilding base tables or
rewriting old rows. Roots have no edge; unique child/same-incarnation full-card
foreign keys/self refusal protect adjacency. Go admission and full validation
enforce acyclicity,64-edge/1,000-card family bounds (not total board-card limits).

Ordinary open never migrates. Explicit offline exact5->6 then6->7 upgrades under existing-only
native exclusion and one validated transaction preserves old rows/canonical bytes.
Unknown layouts, corruption and dirty sidecars refuse without repair. API/archive/
fingerprint versions stay1 with optional parent fields. See
[the hierarchy guide](../../../docs/runbooks/m04-hierarchy.txt).

The schema identifies a board and one storage incarnation. Every relation carries
that storage incarnation, including restored historical rows. `origin_board` and
`origin_incarnation` preserve source attribution separately from storage FKs.
IDs follow the frozen lowercase nonzero UUID shape; counters use exact SQLite
integers up to 9007199254740991. `STRICT`, NOT NULL, CHECK, PK, unique, typed and
incarnation-qualified FKs are part of the layout, not optional application hints.

Card title bounds count 1..200 Unicode scalars, including every U+0000. The
CHECK counts JSON-quoted text after collapsing encoded backslashes, quotes and
controls to one scalar each, then removes the two outer quotes. Encoded
backslashes collapse first so literal escape text stays literal. This avoids
SQLite's plain text length stopping at NUL; the stored title remains byte-exact.
Go admission continues to own valid Unicode and nonblank-title policy.

Board defaults are revision 0, next ticket/event 1, policy version 1, retention
90/365/180 days and retention version 1. Cards default P3 and Ready for Work.
Advance the next-ticket counter before inserting a persistent `ticket` allocation;
ticket numbers/identity allocations cannot be changed or deleted. A counter at
the safe-integer ceiling cannot be incremented. S02 must refuse overflow before
mutation and increment board/card revisions exactly as the command requires.

The typed `entity` registry preserves identity, card ownership, origin and optional
removal metadata. Full `card`, `comment`, `link`, `attachment` and `approval` rows
resolve through it. Current reference lists live in `current_reference`; full
earlier snapshots carry separate `revision_reference` lists. Full current/history
references cannot be replaced by tombstones. A permitted compaction first removes
the applicable pins/full-history protection, records removal metadata, and then
removes the full historical entity. Current cards and approval facts cannot be
reduced to tombstones. This package executes none of those domain operations.
Every historical reference includes the entity's owning card in its FK, for full
and removed snapshots alike. A snapshot cannot borrow another card's comment,
link, attachment or approval merely because that entity exists.

`grant_fact` is non-authoritative historical existence metadata; `grant_record`
and `grant_right` alone store active grant state. Restore may preserve grant facts
without creating grants. Grant policy and forbidden automation authority are G01
admission responsibilities; actor attribution is retained independently.

`cli_credential` stores private persistent authentication metadata only: the
bearer's SHA-256, public UUIDv4 credential ID, UTC whole-second issue/expiry times,
and exact installation/origin/board/incarnation/grant/actor bindings. No bearer,
rights snapshot, browser session or login code is stored. Installation IDs are
UUIDv4; origins have D01's canonical HTTPS random-hex `.localhost` hostname and
explicit decimal port 1024..65535. Expiry is exactly 30*86400 seconds after issue.
Metadata is immutable; explicit delete plus insert replaces a grant's sole row
inside the caller's serialized transaction. Credential IDs and hashes are unique
across grants. A composite FK binds the current grant's exact actor; a historical
`grant_fact` alone cannot support a credential. Inserts require an enabled current
grant and the current board/incarnation. Open audits those logical bindings.
Disabling a grant preserves its credential metadata and remains valid on open;
every authorization must independently consult the grant's current enabled state.

G02 owns the real issue/replace/self-revoke/verifier service under the S01 writer
lease, serialized with grant revocation. It must compare the configured exact
installation/origin and current time, generate/hash the random bearer, authenticate
the native owner, and check current grant/policy. This storage boundary neither
establishes configured installation identity nor authenticates a hash-shaped value.
Credential writes produce no board/card revision, operation or activity. These
private rows survive normal close/reopen but are excluded from logical archives
and source grant restoration; future archive code must explicitly omit them.

`operation` keeps exact compact terminal identity, target/preconditions, result
tokens and removal metadata. `operation_full` holds retained semantic intent/result
JSON. Current-authority ledger entries are explicitly distinguished from source
archive entries by origin and `current_ledger`; a partial unique index protects
current `(incarnation, opId)` identity. Source rows cannot enter that ledger by
setting the flag or reusing a source opId. No terminal identity can be overwritten
or deleted. Full payload deletion precedes recording compaction metadata.

`source_archive_group` independently retains every declared inherited group
identity, including empty groups. Its storage-incarnation FK and identity guards
require the same logical board and a noncurrent incarnation, matching C03. Group
identity is immutable and retained; it carries no invented manifest hash. A02
projects the sorted union of these identities and noncurrent operation origins.
Lineage facts neither create groups nor establish group membership.

`coverage_gap.gap_id` is a private physical surrogate and never enters an archive.
A unique expression index preserves the complete logical tuple, including
`from_exclusive` and `recorded_at`. NULL `from_exclusive` normalizes to an empty
sentinel for duplicate detection; timestamp CHECKs forbid an actual empty value.
Distinct complete tuples remain separate, including overlapping intervals and
otherwise identical gaps recorded at different times.
Gap board revisions use C03's Number range, including zero; they are evidence
metadata and do not allocate or increment a board revision.

Only COMMITTED targets/results carry entity/grant existence obligations. A refused
target may never have existed. Its attempted digest/length needs no fabricated
entity, tombstone or BLOB. Immutable `byte_fact` records retain attempted and
committed attachment facts through compaction. Digest lengths must agree across
stored metadata/facts/BLOBs. Full COMMITTED attachments require complete metadata
and explicit operation pins; compact/refused facts never pin bytes by themselves.
Each operation pin binds the same operation's digest and length through a deferred
composite FK to `byte_fact`. Matching pin/fact rows may be staged in either order
inside one transaction; COMMIT cannot leave the fact absent or different. Insert
guards also reject a mismatch when the counterpart already exists. This adds no
BLOB dependency to an unpinned compact or refused fact.

Byte ownership is explicit in current/history/operation pin tables; FKs prevent
deleting a pinned BLOB. BLOBs enforce the 10 MiB per-object ceiling and exact length,
including empty BLOBs. Open verifies stored BLOB SHA-256, and pin inserts/audit enforce the 64 MiB
distinct protected-byte quota. Upload SHA-256 verification, full typed intent/result
comparison, canonical hashes, active export
inventory, last-pin collection and retention execution belong to later admission/
archive tasks. A digest-shaped string alone is not byte-integrity proof.

`Validate` refuses unexpected/missing/changed schema objects, identity/version
mismatch, SQLite integrity/FK failures and cross-table completeness mismatches.
The reserved SQLite object prefix is matched literally, so legal lookalike names
cannot hide unsupported objects from the exact layout inventory.
These checks include full/tombstoned records, source/current ledger separation,
current/history/operation pin consistency and digest-length agreement. State,
intent, result, affected-fact and retained-preview JSON is stored for later Go-owned
typed decoding. `json_valid` is not a substitute for strict command/archive
validation, semantic field matching or canonical fingerprint verification.

S02/S03 must preserve transaction-boundary completeness after sequential inserts
and compaction; some obligations cannot be per-statement CHECKs. Rights, exact CAS,
fixed transitions, Completed protection, blocking-cycle admission, whitespace/
Unicode validation, timestamps sampled once per transaction, approval invalidation,
retention preview hashes and all terminal outcome decisions remain in Go. Schema
tests/open validation confer no domain, recovery or durability qualification.

Assignment is a nullable card.owner_id referencing the same incarnation actor.
It records responsibility only and grants no authority. Existing layout 4 is
refused before any write; no migration or old fixture relabeling is supported.

Physical7 expands only link kinds and an active unordered related-pair unique index.
Frozen5 and frozen6 exact SQL manifests and independent consistency validators remain
available solely for deliberate offline inspection/adjacent upgrade. Current startup
accepts7 only. Explicit5->6 and6->7 are separate guarded transactions; no startup
migration, direct5->7, graph normalization or original-outcome rewriting occurs.

The unordered related index covers full local internal targets. Exact foreign and
historical unavailable endpoint pairs are already distinct under directed typed
uniqueness; comparing only their card UUIDs would falsely conflate them. Current
new-kind references additionally require a full Board.Identity local peer with an
exact internal target. Historical-only evidence remains readable and conserved.
