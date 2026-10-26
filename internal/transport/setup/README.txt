NATIVE SETUP LIBRARY

Authority: docs/contracts/setup.txt, accepted D01 revision 3. This component owns
local installation material and native owner loading only. Later grant admission,
HTTP/session/CLI adapters and executable composition own the integrated credential
and original-outcome lookup journey (D01 section 6c). No board is selected here.

Public entry points
- New resolves Windows LocalAppData through KnownFolderPath; it accepts no custom
  production root, hostname, environment setting or remote address.
- Init, Status, RenewCertificate and RenewOwner return secret-free setup responses.
  Terminal adapters must enforce the accepted grammar, print the required warnings
  and supply the exact cooperative Confirmation. No human-proof claim follows.
- ReadContext preserves installation/origin/issuer identity. Its TLSConfig trusts
  only that issuer and verifies the configured hostname with normal TLS checking.
  Later CLI dialing must still connect directly to loopback and bypass proxies.
- LoadNativeOwner supplies a current private client credential. Authorization is
  secret transport only; NativeOwner refuses JSON serialization and redacts fmt.
  ActiveTokenMatches checks its snapshot, expiry and installation, not board rights.
  Consumers must load the current snapshot under the service lifetime lease; an
  obsolete cached snapshot is not an independent source of current authority.
- AcquireServer renews a near-expiry/expired leaf through the same stopped
  lifecycle, then holds the native installation exclusion until Close. TLSConfig
  refuses new handshakes outside the leaf validity interval. Later service code
  must apply that config, dual exclusive loopback binds and exact Host/Origin rules.
- PlanTrust exposes the required public warning/confirmation context. ApplyTrust
  accepts only an injected physical-current-user-store adapter after confirmation.
  This package links no live trust-store implementation. A nil adapter reports
  membership unavailable. Removal validates intact public identity even after key
  loss/expiry and never pretends that public-only validation proves private health.

Publication and intentional stops
Only the exact validated active selector selects authority. New generations use
private-at-creation protected current-SID/System DACLs, independent P256 keys,
file flush/readback, exclusive native generation moves and final selector replace.
Prior generations remain private. Complete pending candidates reconcile exactly;
incomplete/multiple/foreign/uncertain-selector material stops for a repair decision.
Uncertain final publication/store effects stay unknown. No timestamp selection,
issuer reset, token replay, rollback, secure deletion or evidence rewriting exists.
Issuer expiry/key loss and ambiguous recovery intentionally stop availability.

Verification and limits
Run on the pinned Windows/amd64 Go toolchain with CGO_ENABLED=0,
GOTOOLCHAIN=local and GOFLAGS=-mod=readonly:
  go test -count=1 ./internal/transport/setup
  go vet ./internal/transport/setup
Tests use private synthetic installations beneath the OS test temporary directory;
N01_TEST_ROOT optionally selects an external test-artifact container only. The
production library never reads that variable. No test touches the real setup root,
certificate store, browser, real board, human grant or persistent configuration.

Native tests exercise ACL/security-descriptor inspection, flushed/readback files,
generation/selector publication, same-process and child-process exclusion, exclusive
dual-family loopback bind observations, and real crypto/x509 validation. Injected
checkpoints cover interruptions/unknowns; fake-store cases cover trust boundaries.
The default execution sandbox denied initial hardlink fixture creation (Win32
error 5); that failure remains private evidence. A focused authorized host run
subsequently created synthetic NTFS junction/hardlink fixtures and demonstrated
their refusal. Separate metadata guard vectors remain explicitly injected.
Path lexical/normalized-alias checks are native. Hostile reparse races, all crash
boundaries, power-loss persistence and forensic erasure are not demonstrated.
Cleanup attempts to retain the exact pending record after a returned native error;
if cleanup and metadata retention both fail, unknown recovery stops remain.
Browser adoption, CurrentUser physical-store behavior, real owner actions and the
integrated service/CLI/bootstrap/lookup journey require later scoped qualification.
No same-account hostile isolation or meaningful-human-review proof is claimed.

Public MIT FOSS. UTF-8, LF, final newline. Synthetic source fixtures only; no v1
source/tests/internal designs or private operational material are included.
