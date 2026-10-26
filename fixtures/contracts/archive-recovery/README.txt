D02 SYNTHETIC DOCUMENTARY VECTORS

All IDs/content are invented. No real board, grant, credential, private submission
store or filesystem path appears in the serialized fixtures. MIT repository license.
Read docs/contracts/archive-recovery-schema.txt before using these vectors.

package/ is an exact logical archive-member tree, not a ZIP. COMPLETE anchors the
manifest; its inventory anchors readable JSON and two raw BLOBs. note.txt contains
exactly Evidence A without LF; the other BLOB has zero bytes. Compact refusal byte
facts do not create an unprotected BLOB. Unicode/combining text/CRLF/quotes/backslash,
nulls/empty collections, cleared snapshot tombstones and source outcome provenance
are intentional. Hashes are recorded, not invented placeholders, except compact
historical fingerprints whose removed intents cannot be recomputed, and the older
lineage manifest hash whose package is deliberately outside this fixture's scope.

refusal-cases.json identifies a full NOT_COMMITTED/NOT_FOUND attachment attempt
against a never-existing card and a compact NOT_FOUND revoke against a never-existing
grant. Their exact fingerprints have separate canonical byte vectors; their rows
survive unchanged in the prepared source archive, with no invented entities or
tombstones. Attempted attachment bytes were never stored and do not pin a BLOB.
The contrasting broken-committed-reference case must fail the semantic predicate.
Ordinary empty-attachment creation and its full-history snapshot share the same
second; no monotonic-wallclock policy or timestamp-based ordering is inferred.

preview-clear and preview-retention select exact retained records from package/.
An empty post-clear byte-reclaim set is intentional: current references still pin
both BLOBs. Confirm canonical bytes bind nested input+previewHash and null target.
Prepared target JSON shows fresh incarnation and moved source outcomes. The receipt
stores only source manifest and logical prepared inventory, no duplicate prose.
Preparing/prepared/failed receipts and unknown status distinguish missing completion
from a confirmed published copy. An incomplete directory must never report success.

canonical/*.bin, *.canonical.bin and *.hash-input.bin contain exact canonical UTF-8
bytes WITHOUT LF. Negative .bin fixtures intentionally include invalid encodings or
schemas; they are not public prose files. Raw BLOB files preserve exact bytes.
vectors.json includes expected byte counts, lowercase SHA-256 and primitive hex.
negative-cases.json also records future hostile ZIP/native/status cases: those
descriptions are planned qualification, not executed runtime evidence.
Its jsonCases, binaryCases and semanticCases are separate. Binary cases dispatch
before JSON parsing and explicitly check same-length tampering versus truncated
bytes. Semantic cases check omitted/substituted attachment facts, conflicting
lengths, nonbyte-command facts and broken committed references with exact expected
failure predicates. These selected checks are not a complete schema validator.

From repository root:
  python fixtures/contracts/archive-recovery/verify_fixtures.py
Regenerate only when deliberately revising the proposed contract:
  python fixtures/contracts/archive-recovery/make_fixtures.py

Verifier scope: fixture UTF-8/LF/JSON shapes, R/C encodings, references/selection,
inventory/COMPLETE/receipt/preview hashes, protected-byte mapping and negative inputs.
It imports only the fixture encoder; it calls no product/board/network APIs and
performs no build, install, trust action or native ZIP/SQLite/browser probe. It is
not a production parser/schema/qualification suite. Reusing its algorithm in product
code would not independently qualify cross-client canonicalization. Fresh non-author
review and owner acceptance remain required; native fault/durability/publication,
ACLs, races and browser evidence are later tasks. Prior source exposure is disclosed;
no independently isolated clean-room certification is claimed.
