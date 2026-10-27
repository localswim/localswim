V04 FINAL LOCAL RELEASE VERIFICATION

This directory records final integrated qualification after the localhost scope
reset, assignment/layout5, runnable restore and current JS buildchain changes.
Earlier qualification/build Q04 records retain their original source and tools;
their executable hashes are not final-source evidence.

Native entry, from repository root
  go test -tags=nativequalification ./qualification/native

The root runner dispatches both real transaction and archive subpackages with
the same tag. Complete expected test IDs, native child rows and six archive
inventory rows are mandatory; process failure, missing/zero/skipped coverage or
unsupported prerequisites cannot pass. Ordinary parser tests exercise refusal
handling separately and do not replace native positive execution. Supply a short
canonical V04_NATIVE_ROOT outside the repository; each run creates fresh private
attempts. Existing fixed Go/module/SQLite and private fault/safe-refusal limits
remain. A current provider inventory is checked before/after, not merely trusted
because an earlier version passed.

Browser entry
  npm --prefix web run qualification:browser

Use Node26.12.1/npm12.2.1, the locked TS7.0.2/Playwright1.64.1 and pinned browser.
Preparation supplies a fresh private localfixture manifest/data/profile. All20
cases, final Playwright JSON, launcher/child exits and owned teardown must pass.
No certificate, login code, human enrollment or delegation is part of this path.

Two clean product builds
Each independently extracted final source snapshot starts with empty compiler
cache, fresh npm cache/modules/output and explicit private short native fixture
roots. Both use the same verified tools and read-only module cache. Run dependency
verification, local npm ci, generator check, TS check/build, declared asset check,
ordinary Go suite/vet and trimpath/buildvcs=false executable build. Compare every
declared generated schema/declaration, emitted module, embedded asset and EXE
hash. Match the complete output set, not a selected executable alone. Preserve
source hashes, commands, failures and unmodified snapshot bindings.

No process-kill/flush injection proves physical power-loss safety. Native tests
use synthetic data and label boundary faults; no real V1 board import/cutover,
network exposure/TLS, personal profile, hostile account isolation or full V1 parity
is qualified here. Fresh independent review and owner-controlled acceptance remain.
