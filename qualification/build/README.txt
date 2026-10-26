Q04 - TWO CLEAN DISTRIBUTION BUILDS
2026-10-26

Outcome
The exact accepted source commit recorded in results.json passed two independent
clean source builds on the recorded Windows/amd64 local NTFS environment. Generated
schema/declarations, embedded assets, freshly emitted JavaScript and executable
SHA-256 values agree. This is build reproducibility for these frozen inputs;
V01-V04 product/native/browser qualification and owner actions remain separate.

Inputs and replay
inputs.json binds the complete git-archive source inventory and the exact official
Go/Node/npm archives. The private input archive also retains Go module and npm
content caches and their complete inventories. Dependency versions, lock bytes,
generator/configuration and checked-in assets are unchanged from that commit.
The report commit adds evidence/documentation; it is not the tested source commit.

Extract the recorded source archive into two distinct new directories. Neither
tree may contain node_modules, web/dist or dist initially. Use the exact verified
Go/Node/npm versions and archived locked caches. Set CGO_ENABLED=0, GOOS=windows,
GOARCH=amd64, GOTOOLCHAIN=local and GOFLAGS=-mod=readonly. Select private per-build
module/npm/Go/temp paths and short synthetic S01/N01/A03/A04/P01 test roots.
Use the pinned Node to invoke the exact npm CLI; put its Node and Go on PATH.
Use offline npm/module inputs, with no package downloads or toolchain substitution.

Execute the commands in results.json in each clean tree, in order. Each npm ci
creates a separate new local installation from the unchanged lock. Check must emit
nothing; build must create the declared staging tree. Contractgen and assetstage
check independently regenerate and compare without rewriting checked-in outputs.
The Go test invocation preserves ./... while adding serial package scheduling,
fresh execution, JSON evidence and a per-package timeout. No package is excluded.
Verified compiler-cache inputs are shared; this is not cold-cache qualification.

Archive every command, exit code, log, source-before inventory and resulting file
hash. No failed command or skipped test, tracked-source change, missing required output or unexplained
mismatch may pass. Compare the complete recorded output set across both builds.
Four compiled packages have no test files; these package-level Go records are
listed separately from test skips. All 1,370 test cases pass in each build.
Keep private paths, runtime records and any credentials out of public evidence.
No executable is activated or published as a release by this task.

Evidence boundaries
The full ordinary Go suite runs on real Windows; it does not execute the separate
V01-V04 native/browser qualification commands. No live installation, issuer trust
change, human grant, login, activation or power-loss qualification is performed.
Browser candidate metadata is frozen for later work; no browser is run here.
Earlier preparation/candidate records retain their historical status and limits.
