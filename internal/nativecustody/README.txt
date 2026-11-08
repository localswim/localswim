DORMANT WINDOWS CUSTODY COMPONENT
M17-INSPECTOR-R3 section 6; no production caller or invocation API.

The package-private owner reserves one operation until actual native cleanup.
CreateProcess uses an explicit image and fixed --version argv, CREATE_SUSPENDED,
JOB_LIST, HANDLE_LIST, DETACHED_PROCESS, and mandatory child-process restriction.
Exact mitigation bits are verified natively before resume, without fallback.
The unnamed noninherited job has kill-on-close and a
16-process ceiling, with no breakaway permission. Only stdin NUL and the two
child pipe writes are inherited. Both parent reads drain concurrently under one
64 KiB capture budget. Root wait, zero active job processes, both drains and
all owned handle closes precede release; job closes last.

A failed CloseHandle is ambiguous: retain custody without retrying its numeric
handle. The test-only pre-close refusal hook proves no close was attempted and
may therefore later allow that same owned object to close. Foreground Wait/Close
may report DRAIN_INCOMPLETE while exclusion remains held. The 10-second deadline
does not promise bounded kernel execution; Close reporting is bounded to 2 seconds.

This is not installation selection, trust validation, an identity parser or an
engine adapter. Future production integration must independently supply all
accepted file/authority/private-directory checks. M17 exposes none of Launch,
Resume, Probe or Stop through a production command, route or environment flag.
The supplied context MUST be cancelled on live authority or generation loss;
check is admission-only. There is no autonomous authority refresh/renewal here.
That future caller adapter is not implemented or qualified by M17.

Controlled native qualification (Windows amd64, owner account)
- Tag: nativecustodyqualification. Untagged builds contain no helper tests.
- Use the exact Go executable/module cache from the current qualified profile.
- Set GOTOOLCHAIN=local, GOPROXY=off, GOSUMDB=off, GOWORK=off,
  GOFLAGS=-mod=readonly, CGO_ENABLED=0, GOOS=windows, GOARCH=amd64, GOAMD64=v1.
- Set GOCACHE to a private per-run cache outside the source tree.
- NATIVECUSTODY_GO is that exact Go executable, never a PATH fallback.
- NATIVECUSTODY_TEST_ROOT is a fresh protected account/SYSTEM-only directory.
  TEMP and TMP must also be beneath that protected root. The maintained private
  root helper is adapted only to the unique m17c- prefix; refuse existing paths.
- Run: go test -count=1 -json -timeout=90s -tags=nativecustodyqualification
  ./internal/nativecustody
- Run full default vet with the same tag. Missing setup fails; no skipped or
  zero-case result is accepted as native qualification.

Required test inventory
TestSuspendedAtomicJobAndNormalJoin
TestCreationFailureAndPreResumeRefusal
TestClosedStdinCleanEnvironmentAndHandleList
TestOutputBoundAndDeadline
TestStrictChildAndConsoleRefusal (five child/console/override variants)
TestCancellationCloseAndRetainedFailure
TestNativeCloseErrorIsNotRetried
TestAmbiguousCloseTerminatesSuspendedRoot
TestCallerRevocationCancelsWholeJob
TestParentDeathKillsOwnedJob

Tests build only testdata/helper, copy it to declared deterministic helper modes,
and never inspect or invoke installed codex.exe. Parent-death proof uses a held
process handle for the strict root, not PID-only cleanup. Broader descendant and
console cases live only in private controlled diagnostic evidence; they do not
establish an admitted production profile or a total-process bound.
The inheritance negative control is a separately owned, finite helper process.
All runtime roots are removed only after native joins; failure evidence stays
outside source. Process termination is not physical power-loss qualification.
