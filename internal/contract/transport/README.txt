C02 - D03 public transport boundaries

Authority
Accepted docs/contracts/transport-envelopes.txt controls exact public encodings.
Accepted D01 setup.txt controls credential issuance/CLI facts; accepted D02
archive-recovery-schema.txt controls shared retained rows, results and ordering.
Acceptance is recorded by the owner, independently of historical proposal headers.
Public MIT FOSS; invented fixtures only. Prior exposure is disclosed; this is not
an independently isolated clean-room certification.

Implemented surface
- Terminal full/compact committed/not_committed envelopes, complete immutable
  identity metadata, command-selected target/preconditions and exact C01 results.
  C03 retained-fact validation binds revisions, result references and byte facts;
  full results additionally bind each returned result ID to its recorded fact.
- KnownIdentityUnknown and nonterminal refusal are separate exact families.
  Required nullable conflict/error fields remain present. Refusal opId is absent
  or a UUID, never null, and never original terminal identity proof.
- Coherent board/card/history projection DTOs use C03's exact shared row types.
  History delegates reference closure and full/tombstone rules to C03 Rows and
  ValidateHistoryReferences. Current reads validate row membership, ownership,
  provenance, attribution, approval references and history-reference ordering.
  Complete board views resolve same-current-identity link targets against their
  full card collection. Single-card views and inherited/foreign target identities
  do not imply target-card availability in the returned projection.
- Public session/code/exchange/credential boundaries; exact rights, automation
  exclusions, canonical secret encodings, UUIDv4 installation/credential IDs and
  issuance expiry intervals. Secret-free CLI outcomes preserve unavailable facts
  as required nulls and require complete public facts for confirmed issuance.
- Export requests/status have destination=null, with validated source facts
  required for confirmed/offered status. Recovery preview/confirm and restore
  status remain aliases to standalone C03 objects, with no outer wrapper/hash.
- FreezeMutation refines C01's foundation request shape to accepted D03: every
  expectedCardRevision is present, null for noncard commands and positive for an
  existing-card target. It then reuses C01 Freeze for strict command inputs,
  independent route/actor binding and unchanged original semantic bytes/hashes.
  Optional input omissions retain their existing meaning; no defaults are added.

Public APIs and serialization
- Decode[T] accepts the declared transport models and standalone C03 aliases,
  using C01 strict JSON and C03 exact structural/scalar/row checks before D03
  validation. It rejects ambiguous JSON, unsupported roots/versions/enums, missing
  required keys, extra keys, invalid Unicode/numbers, duplicate/unordered rows,
  wrong required nulls and inconsistent outer facts. Errors do not echo input.
- Validate checks constructed values, including original Go Unicode and required
  arrays; it does not repair text or default nil arrays to empty collections.
- Readable validates a declared secret-free public model and emits D02 R bytes:
  sorted ASCII keys, UTF-8, LF and exactly one final LF. Original prose is unchanged.
- CodeSecret, ExchangeRequest, ExchangeSecret and CredentialSecret reject ordinary
  encoding/json marshaling. Their default string/debug formatting is redacted.
  MarshalEphemeral is the explicit value-based transport serialization opt-in.
  Its returned bytes require Cache-Control: no-store, ephemeral transport only,
  and exclusion from stdout, logs, evidence, intents, ledgers, recovery and exports.
  It creates no random material, credential publication or network request.
- SecretCode/SecretToken are typed sensitive values, not a general DLP facility.
  Explicit extraction/conversion or alternate encoders can bypass presentation
  guards. Never log fields, serialize untyped received bodies, or persist secrets.
  Diagnostic message authors must supply inert safe text; structural validation
  cannot infer whether arbitrary text contains private data or secret material.

Generation and validation
GeneratedFiles is pure and returns only schemas/transport/contracts.schema.json
and web/generated/transport/contracts.d.ts for the common contractgen aggregator.
Generated rows reference C03; command results and foundation inputs reference
C01. Terminal types discriminate command, terminal class and detail retention;
credential/export success types require their known public facts. JSON Schema
and TypeScript are advisory: Go strict parsing, semantic/reference checks and
independent current authentication remain required. TypeScript numeric types
cannot encode the positive/safe-integer rules or cross-reference provenance.

The tests consume 60 independent accepted D03 positive fixtures and 20 negative
fixtures, anchor their bytes/hashes and roundtrip exact R bytes. Additional cases
exercise required fields, strict scalars, targets/results/references, secret
exclusion/expiry, current/history dependencies and all 12 original semantic hash
anchors. The tagged nativecompilerqualification test requires absolute C02_NODE,
C02_TSC and a new private C02_WORK; it runs pinned Node 24.20.0 and TypeScript
7.0.2 against all 60 fixtures plus type rejection/discriminator narrowing cases.
Use the accepted fixed Go 1.27.3 toolchain, CGO_ENABLED=0, GOTOOLCHAIN=local and
GOFLAGS=-mod=readonly. No manifests, locks or tool installations are changed.

Limits and affected consumers
These are pure boundary contracts, not HTTP/multipart framing, authorization,
domain admission, database transactions, ZIP, native publication, session runtime,
credential storage, random generation or crash/power-loss behavior. Attachment
metadata requests reuse FreezeMutation. C03 ValidateBytes checks an already
supplied in-memory slice against its declared byte fact; A01/A03 retain streaming,
raw transport/hash IO and package/native integrity implementation obligations.
No current-credential selector, private native file or actor-authority DTO is added.

A structurally valid terminal envelope, matching text/hash or echoed opId does
not prove authenticity, commitment or noncommit. Consumers must bind original
immutable submission evidence and independently authenticated current authority.
Lookup failure or possible transmission preserves unknown/CLI exit4; ordinary
read/refusal classifications cannot downgrade that uncertainty. This package
does not dispatch, replay, choose exit/status codes or infer those caller contexts.
Review missing full details as unavailable. Export offered is not delivery;
session revoked is not grant revocation or a durable domain terminal receipt.

S03/A02/A03/A04, T01, G01, K01/K02/K03, W01/W02/W03 and H02 retain their
implementation/qualification gates. Consumer code must use the D03 refinements
for exact public requests rather than treating C01's earlier foundation request
declarations as the complete accepted D03 wire surface. This milestone alone
qualifies neither authentication nor any native/HTTP/product acceptance scenario.
