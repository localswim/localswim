C01 - BOUNDED MUTATION CONTRACT CORE

Authority
Accepted slice01 03/04/05/06/18/19 and corrected 09/20/21, plus current
docs/contracts/setup.txt revision 3 (D01) and archive-recovery-schema.txt (D02).
The explicit C01 -> D01 prerequisite applies to credential/provider boundaries;
the older task JSON's shorter dependency list does not waive it.

Implemented surface
- All 12 accepted mutation commands: strict wire metadata, typed inputs,
  independent route/authenticated actor binding, immutable full-target canonical
  intent, exact safe integer/Unicode encoding and SHA256.
- Typed Go input extraction preserves omission as nil; canonical omitted optional
  payload fields become null. No create priority default enters the fingerprint.
  Create description is required (empty allowed); only priority is optional.
- Exact D02 retained committed/refusal result objects, generated request/result
  JSON Schema and TypeScript declarations, deterministic drift checking.
- Every existing-card target is frozen separately from link peer/linkId. Grant
  scope is checked against independently resolved board scope. Recovery payload
  is {input,previewHash}; preview hash is not an authentication credential.

Use
Freeze accepts the selected route/command/board/target and actor ID from the
authentication layer. Adapters must derive those arguments independently; merely
constructing the Go structs confers no authority. Resolve grant.revoke board scope
from storage. Match route method/action against the registered Commands mapping.
Client actorId, bearer, credential selector, origin or multipart fields are not
accepted domain fields. Attachment metadata binds digest/length; verifying actual
file bytes is the attachment boundary's responsibility. Input returns fresh typed
values and Context returns copied identity/preconditions. CanonicalBytes copies.

This parser is boundary validation, not state admission. Current grants, native
owner entitlement, policy, incarnation/revision comparison, valid workflow,
link ownership/cycles, quotas, original-outcome lookup and atomic persistence
remain downstream responsibilities. Completed/no-op/automation-right rules are
not inferred here. Empty edit input is structurally accepted; it grants no effect.

Generation and checks (module root)
  go run ./cmd/contractgen
  go run ./cmd/contractgen -check
  go test ./internal/contract ./cmd/contractgen

-check fails without rewriting any output, including missing outputs. Generated
schemas describe decoded structure; strict DecodeJSON remains mandatory for
duplicates, Unicode, depth and decimal-number lexemes. x-max-utf8-bytes,
x-nonblank and x-order explicitly record extra Go-enforced constraints that
generic JSON Schema tools do not enforce. TypeScript types are advisory and do
not validate runtime data. No second browser domain model is emitted.

Native canonical reference
Supply absolute C01_NODE (qualified Node 26.12.1), C01_TSC (qualified TS7 7.0.2
launcher) and a NEW private C01_WORK directory. Under qualified Windows/amd64 Go
1.27.3 with GOTOOLCHAIN=local, CGO_ENABLED=0 and GOFLAGS=-mod=readonly:
  go test -count=1 -tags=nativecompilerqualification ./internal/contract ./cmd/contractgen
The test executes strict/noEmit checks, expected declaration errors and emitted
JavaScript canonical byte/hash/rejection vectors. The independent test-only
canonical reference in testdata is not distributed browser/domain code.

Explicit incomplete breadth / affected consumer gates
This bounded foundation milestone is not all wire/archive DTO coverage. Before the affected
consumers integrate these families, finish and independently review:
- HTTP terminal committed/not_committed/unknown/refused envelopes and lookup
  errors/conflicts, plus board/card/history/session read DTOs: S02, K01/K02, W01/W02,
  T01/G01 and H01. The current exact result validators concern retained D02 result
  objects, not an invented exhaustive HTTP envelope schema.
- D02 manifest/board/history/activity/outcome graph, coverage/tombstones,
  preview inventory/response, restore receipt/target inventory/status DTOs and
  cross-record constraints: S03, A02/A03/A04, K01/K03, W03 and H02.
- D01 secret CLI credential issue wire response, secret-free credential CLI envelope
  and explicit ordinary/native-owner selector boundary types: T01, G01, K02/K03
  and P01. Native setup/status and private local-material file DTOs are N01-owned;
  do not duplicate that family in C01. Use N01's current native credential provider;
  do not add selectors/secrets to semantic intent or substitute a board bearer for
  the narrowly authorized native-owner grant-operation lookup entitlement.
These are recorded remainders, not permission to invent policy or silently create
a second contract in a consumer. Full affected-consumer readiness is not claimed.

Evidence limits
Go and actual TS7/Node tests qualify this bounded contract/canonical surface only.
Q03's earlier sample is not product equivalence. No authentication, server, board
admission, DB, archive implementation, CLI runtime, browser, trust enrollment,
native crash/power-loss, distribution build or final product qualification occurs.
Public MIT FOSS; synthetic identities only. No credentials or board data.
