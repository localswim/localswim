A02 export snapshot and stored ZIP writer

Prepare requires an explicit identity/revision, UTC export timestamp, and a
mandatory trusted-adapter verifier. H02 and native CLI composition MUST bind
their authenticated actor and route and call the real G01 Require with read and
export rights through the provided writer.Read. A nonnil callback alone is not
an authentication claim. The package has no permissive default or network API.

The shared S03 RegistryFor/BeginExportSnapshot owns one actual SQLite reader and
its immutable byte pins. Identity/revision and current authority are checked in
that reader before board content is projected. All metadata, current references,
full/removed history, retained activity, compact/full terminal operations, source
archives, lineage and protected BLOB bytes come from the same pinned revision.
Current grants, sessions, credentials and machine paths never become archive rows.
Source groups are the sorted identity union of explicit source_archive_group
rows and noncurrent operation origins. Empty declared groups survive independently
of lineage; lineage alone never invents a group. Complete coverage-gap tuples,
including distinct fromExclusive and recordedAt values, remain separate logical
facts. Physical layout 4's surrogate gap identity is never projected; logical
archive, API and fingerprint versions remain 1.

S03 owns registry/domain-pin acquisition before A02 receives the snapshot. A02
adds no global domain-pin population policy and claims no total-process memory
bound for that upstream materialization. Its member ceiling is checked before
further A02 metadata staging; the package/staging/output limits below are A02's.

Preparation stages private bounded copies, validates shared C03 DTOs/references
and exact readable JSON inventory, verifies every protected byte length/digest,
and releases the reader before releasing its pins. Failure exposes no Prepared.
Missing/tampered protected bytes fence the affected Store. Unsupported archive
size refuses preparation without treating a legitimate large board as corruption.

Prepared holds no live reader or pin. Its staged bytes survive later clearing;
slow output cannot block the storage writer. Output receives fresh bounded
scratch copies, so a Writer retaining or changing its arguments cannot mutate
Prepared. There is no exported backing slice or promoted WriterTo shortcut.

Limits are inclusive: 144 MiB exact ZIP bytes including headers and directory;
16,384 members including manifest/COMPLETE; 32 MiB each JSON, 64 MiB five-JSON
sum; depth 32; 10 MiB each BLOB and 64 MiB distinct protected BLOB sum. Collection
lengths are checked inside SQLite before driver allocation, incremental per-doc
and total budgets bound projection, and exact readable-length preflight precedes
shared JSON encoding. The actual encoded sizes and exact classic ZIP overhead
are checked again. No ZIP64, compression, path input, or arbitrary member exists.

Write orders manifest, four payload JSONs, digest-ordered raw BLOBs, then COMPLETE.
Inventory paths are sorted independently. COMPLETE is the exact manifest R-byte
SHA-256 plus LF. Raw ZIP headers use known size/CRC without data descriptors.
Any write, cancellation or finalization error leaves delivery unconfirmed. A
successful Write proves stream construction, never verified native publication,
browser local save, trusted origin, remote copy or power-loss durability.

Focused synthetic tests cover DTO/R inventory and raw/empty/deduplicated bytes,
current/history/full-COMMITTED-only pins, compact refused non-pinning facts,
separate source provenance, real G01 denial/preconditions, concurrent clear,
corruption fencing, bounds/depth preflight, interruption and mutable Writers.
Removed-link workflow/clearing remains exportable: tombstones expose actorId only
for comments, retaining createdAt and source provenance for link tombstones too.
Native Windows tests require an explicit private S01_TEST_ROOT outside the public
repository and use synthetic user-restricted stores. They open no listening port
and access no real board, credentials or certificate store. A02 implements no
archive validator, restore, HTTP, CLI, UI or native publication path.
