Separate offline restore preparation

Prepare consumes only A03's opaque, completely validated package. It preserves
logical IDs, revisions, historical origins/createdIn, immutable intent/fingerprint
bytes and protected raw bytes. It allocates a fresh incarnation outside source
and lineage, remaps current storage FKs and internal CardRefs, moves source current
operations into a separate source archive, and starts an empty current ledger.
Inherited source groups remain separate. An empty source current ledger does not
manufacture an empty group. No grants, credentials, sessions or recovery previews
are reconstructed. Historical grant facts satisfy references without giving rights.

Source floors become origin-qualified expired gaps with the receipt creation time
and source revision; destination floors reset to null. Existing gaps and the full
lineage persist, adding the immediate source manifest hash/identity/revision tuple.
The source manifest hash and target inventory remain distinct anchors.

Construct creates layout 4 through S01 exclusively in an existing private empty
data directory. SQL import uses explicit BEGIN IMMEDIATE/COMMIT and the exact
shared schema, with deferred reference checks and full schema/FK/byte validation.
No file is adopted, overwritten, repaired, migrated or automatically retried.
Failed candidates are retained. NativeInventory independently closes/reopens and
reprojects the prepared database through shared S01 InspectExisting and A02
ProjectOffline under exclusive existing-only native ownership, SQLite mode=ro,
query_only, and an actual reader transaction. Read the exact distinct
current/history/full-operation protected pin union in that same transaction.
No live export registry, pin-release GC, writer checkout or domain cleanup runs
during verification, including on an invalid, interrupted or cancelled read.
Complete transformed DTOs,
lineage and raw bytes must match before returning an inventory anchor. Status
recomputes every logical entry length/hash/aggregate and checks inactive operational
tables. The shared inspection provider opens both data files only with
OPEN_EXISTING and holds actual data-access exclusion handles, closing the
missing-board.lock race. Ordinary S01 Create/Existing are not used for status.
Missing or rebound files refuse without creating or repairing metadata.
Status preserves the complete filesystem inventory and all file bytes, including
unprotected BLOB evidence that makes the candidate invalid; repetition stays unknown.

Complete provenance representation

Layout 4 persists every declared source archive group independently of operation
rows, including empty inherited groups. A02 projects their union with historical
operation origins; lineage alone never invents a group. Complete gap uniqueness
preserves distinct fromExclusive and recordedAt fields, including an expiry gap
and the later restore gap derived from its source floor. Restore deduplicates only
an identical complete tuple. The prior layout3 representation exclusions are
superseded. Layouts 1, 2 and 3 require refusal without writes or migration; logical
archive/API/fingerprint/receipt versions remain 1.

The archive and transformation retain D02 size/depth/schema/reference constraints.
Unsupported scope, invalid source, incomplete construction or absent status is
never evidence of an original source operation's noncommit. Source operation
lookup at the destination remains source-authority-unavailable under S02.

Native synthetic tests are dispatched with A04_TEST_ROOT outside the public
implementation repository, using the frozen local Go toolchain, CGO=0, offline
module cache and -mod=readonly. They exercise real A02 authenticated export, A03
validation, restore construction, native publication/status and source preservation.
Physical4 cases run actual expiry and human retention preview/confirmation, export
the resulting source, and restore in a later wall-clock second while retaining both
gap recording times. Native nested roundtrips retain empty groups absent lineage,
lineage-only identities without invented groups, and empty current operation ledgers.
Private fixture paths and raw logs stay outside public source. Fresh human bootstrap
is a future explicit activation gate; test fixtures contain only synthetic scopes.
No trust change, real credential, migration, service, port or UI is involved.
