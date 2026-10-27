V04 root native dispatch

From the repository root, with the qualified offline Go 1.27.3 environment:
  go test -tags=nativequalification ./qualification/native

Required environment: CGO_ENABLED=0, GOTOOLCHAIN=local,
GOFLAGS=-mod=readonly, GOPROXY=off, GOSUMDB=off, populated qualified
GOMODCACHE and GOCACHE, and Go on PATH for the archive nested command.
V04_NATIVE_ROOT must be an explicit canonical local absolute parent outside
this public repository, at most 70 characters. Use a short ordinary-account
private fixture location. Each invocation creates a new ACL-protected attempt
and separate V01 native/artifact and V03 roots. No attempt is removed or reused.
No real boards, trust enrollment or browser profile is used.

The root invokes only transaction and archive, never itself; each invocation
uses nativequalification, count=1, JSON, and a four-minute test timeout inside
a four-and-a-half-minute process deadline per suite (both fit the root
command's default ten-minute timeout). Missing configuration or unsupported platforms
fail. Default untagged tests cover pure report parsing and dispatch failures;
those controlled reports are not positive native qualification evidence.

inventory.json binds the actual mandatory test names from the suites:
8 V01 outer tests, 7 private continuation child tests, 14 V03 outer tests
(including all six inventory rows and seven refusal subtests), and 4 V03
interruption tests (parent plus three interruption stages). Empty, skipped,
failed, malformed, duplicate or missing mandatory reports fail closed. Child
exit failures propagate regardless of report content. Each private attempt
retains both complete outer JSON streams, nested suite evidence, source-before
files, source hashes before/after, inventory and results. Source changes fail.

The V01 provider manifest now enumerates the current complete sorted unique
internal file set. Exact paths, count and SHA256 bytes are checked before and
after execution, including the isolated continuation module. Original V01
results and review records remain historical; this refresh does not relabel
them or modify native provider semantics. New execution evidence belongs to V04.

Scope: actual Windows amd64, local NTFS, synthetic private fixtures and loopback
HTTP. Process termination, fence and safe-refusal checks do not certify power
loss or hot-journal recovery. No missing receipt is promoted from UNKNOWN.
The primary owns the separate final browser run and two clean product builds;
a native root pass alone does not claim integrated product qualification.
