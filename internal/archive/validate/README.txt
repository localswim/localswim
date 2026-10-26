A03 - OFFLINE D02 PACKAGE VALIDATOR

Read(context, reader) consumes actual input through EOF and returns a Package
only after complete container, bytes and logical-document validation. It never
writes a destination, publishes files, restores a database or authenticates origin.
Source stability and exclusive offline ownership remain the native adapter's work.

The private handle retains immutable owned bytes, with no reader/writer aliases.
Member and MemberNames return independent copies; Manifest freshly decodes nested
values. The zero value is unusable. A later restore consumer must use this complete
handle and independently validate its transform, destination and publication.

Validation includes:
- Actual package length including headers, names, extras and comments; exact EOF;
  classic single-disk ZIP only, no ZIP64, opaque prefix, gaps or trailing records.
- Stored entries in exact D02 physical order; central listing order may differ.
  Both header sets independently bound names, flags, methods, CRC and actual sizes.
  Reject duplicate/aliased/unsafe names, links, directories, encryption, descriptors,
  unsupported methods/platforms and interpretation-changing metadata.
- Ordinary DOS attributes or UNIX regular-file permissions; reader versions 1.0
  and 2.0; flags zero or UTF-8; inert internal text advisory. Creator version is
  informational. Header timestamps may differ and are never canonicalized.
- Bounded entry/archive comments and well-formed timestamp-only extras: extended
  Unix timestamps (including the central abbreviated form) and NTFS timestamps.
  Unknown, duplicate, malformed, ZIP64, path/encoding/size/link overrides refuse.
- Declared member/JSON/BLOB counts and lengths before decoding; actual lengths,
  hashes and CRC; exact manifest inventory and COMPLETE manifest hash.
- C03 strict DecodeReadable and ValidateDocuments: exact R JSON, schemas, versions,
  supported recovery scope, Unicode, depth, ordering, references, provenance,
  outcome facts, coverage and the exact protected BLOB union. No silent repair.

Limits are inclusive: 144 MiB actual package; 16,384 members; 32 MiB each JSON,
64 MiB aggregate JSON; 10 MiB each BLOB, 64 MiB distinct BLOB aggregate; depth 32.
Private buffering is bounded by input scope; this is not a total-process-memory
or time-bound guarantee. Cancellation is checked between input/header/member steps.
An arbitrary blocking Reader remains the caller's responsibility.

ErrLimit identifies unsupported size/count scope; ErrInvalid identifies malformed,
unsupported or inconsistent packages. IO/context failures propagate. Every failure
returns no usable handle. Hash consistency never proves trusted origin or durability.

Verification
  go test -count=1 ./internal/archive/validate
  go vet ./internal/archive/validate

The Windows/amd64 test creates a synthetic private NTFS store and consumes actual
A02 Prepare/Write output, with G01 read/export verification. Set A03_TEST_ROOT to
an existing private absolute fixture directory to preserve native fixtures; set
A03_EVIDENCE_DIR to an existing private directory to retain the generated ZIP.
Other tests independently produce/perturb stored ZIPs, including the accepted
rich D02 synthetic package, malicious metadata and documents, exact limits and
source/accessor mutation. No listener, real board, credential, trust or UI is used.
Native restore/publication, power-loss and complete qualification remain separate.
