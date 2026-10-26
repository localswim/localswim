C03 - D02 ARCHIVE AND RECOVERY BOUNDARY CONTRACTS

Authority and scope
Accepted archive-recovery-schema.txt (D02), transport-envelopes.txt (D03), C01
and the contract-foundation-decomposition-2026-10-25 decision control this bundle.
Go owns the shared row/scalar definitions, validation and generated declarations.
There is no browser domain implementation, native private-file DTO or new policy.

Use
- Decode[T] performs C01 strict JSON parsing, exact required/null/unknown/type
  checks and local semantic invariants. DecodeReadable[T] additionally requires
  exact D02 R bytes. Validate applies the same boundary to in-memory values.
- Canonical and Readable encode exact C/R bytes: sorted ASCII keys, exact Unicode
  and array order, lowercase control escapes, no HTML escaping, and only R's final
  LF. Neither encoder silently applies defaults, trims text or repairs Unicode.
- FullOutcome keeps command-selected objects as json.RawMessage because C01 owns
  that union. Validate the enclosing Operation to check exact command input/result,
  normalized canonical intent, fingerprint and all compact/full bindings. Do not
  treat decoding an isolated raw object as command validation or authentication.
- ValidateDocuments checks supplied logical documents together: coherent identity,
  revision/policy/coverage, ordering, references, ownership, source outcome grouping,
  result mappings, digest-length agreement and the protected byte inventory union.
  It compares supplied JSON projections with declared inventory lengths/hashes and
  returns distinct protected ByteFacts. It reads no archive, filesystem or database.
- ValidateHistoryReferences is the shared C02 seam. Supply Identity, current card
  ID/revision, returned Snapshots and Rows. It requires exactly the immutable full
  rows/tombstones/actors needed by those records, with complete metadata for full
  snapshots. It preserves removed availability and original source attribution.
- ValidateOperationReferences checks a single retained operation against supplied
  current cards and shared Rows. Attempted NOT_COMMITTED identifiers may never have
  existed; neither full nor compact refusals manufacture entities or pin bytes.
- ValidateBytes compares an already supplied byte slice against a ByteFact. This
  does not establish safe streaming, size-before-allocation or publication.

Shared rows and source boundaries
Current/full history references require complete entity rows. Only genuinely
removed history may use retained entity tombstones; Actor/Approval facts stay full.
Full COMMITTED attachment outcomes require matching complete attachment metadata,
typed result, result references and byteFacts. Only that operation class adds pins;
compact/refused byteFacts still participate in digest-length consistency checks.
SourceArchives remain distinct from the current operation collection. These types
grant no authority to resolve source operations at a restored destination.

Generation and verification
The parent-owned cmd/contractgen aggregates this bundle's GeneratedFiles with C01.
Output paths are schemas/archive/contracts.schema.json and
web/generated/archive/contracts.d.ts. C01 descriptors supply retained canonical
intent variants; its existing result schema/declarations are referenced directly.
  go run ./cmd/contractgen
  go run ./cmd/contractgen -check
  go test ./internal/contract/archive ./cmd/contractgen

Schemas describe decoded shape, not all cross-field/graph invariants. Strict Go
decoding remains necessary for duplicate keys, number lexemes, Unicode and depth;
Go semantic/reference validation remains necessary after schema/type checking.
TypeScript declarations are advisory and do not validate runtime objects.

Actual TS7 declaration check: supply absolute C03_NODE (Node 24.20.0), C03_TSC
(TS7 7.0.2 launcher) and a NEW private C03_WORK directory. With qualified native
Windows/amd64 Go 1.27.3, GOTOOLCHAIN=local, CGO_ENABLED=0, GOFLAGS=-mod=readonly:
  go test -count=1 -tags=nativecompilerqualification ./internal/contract/archive
This checks accepted D02 objects and expected declaration failures, not product
or browser equivalence. The test writes synthetic evidence only to C03_WORK.

Retained consumer gates
A03/A04/S03 still own complete hostile-ZIP/member/header/container validation,
streaming/raw-byte IO/hash checks, total package size including overhead, complete
source-to-destination transformation, offline ownership/status reproduction,
database retention transactions, native paths/ACLs/flush/publication and concurrency.
Receipt/preview hashes and structural success do not establish any of these facts,
authenticate an actor, grant rights, confirm publication or prove crash durability.
Preview selection/admission must recheck current state/rights and use immutable
original preview times. This bundle does not execute selection or confirmation.
Public MIT FOSS, synthetic fixtures only; no real data, credentials or v1 sources.
