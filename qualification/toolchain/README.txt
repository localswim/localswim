CURRENT JAVASCRIPT BUILD/TEST TOOLCHAIN
2026-11-06 | Current V2 maintenance selection

Use Node26.13.0 and npm12.2.2. .node-version and web/package.json select the
same exact runtime; packageManager selects npm12.2.2. The original Q01
qualification/environment.json remains historical input evidence, not the
current Node/npm selection. current.json records artifact checksums and sources;
new qualification results are separate from immutable earlier reports.

Official Node index selects26.13.0 as the latest stable release on the recorded
date, with Lithium LTS status. Prereleases are excluded. npm latest is12.2.2;
TypeScript7.0.2 and Playwright1.65.0 are the registry latest stable direct
dependencies. The lock binds Playwright's exact resolved packages and the native
TypeScript compiler. Earlier qualification results retain their original
versions and do not qualify the updated selection.

Provisioning verifies the official Node Windows x64 ZIP against SHASUMS256 and
the npm tarball against registry integrity. Install npm12.2.2 in a separate
project-local tool directory and invoke its npm-cli.js with the verified Node
executable. These paths belong in private execution configuration. For a
separately installed developer runtime, confirm node --version and npm --version
before building. The packaged Go app itself needs neither Node nor npm at runtime.

Commands from repository root

These examples assume a developer shell with the pinned shims on PATH.
Isolated qualification invokes the verified Node executable and npm-cli.js
explicitly from the private runtime profile.

  npm --prefix web ci
  npm --prefix web run check
  npm --prefix web run build
  node --test web/scripts/scripts.test.mjs web/scripts/browser-report.test.mjs
  go run ./cmd/contractgen -check
  go run ./cmd/assetstage -check
  go test ./...
  npm --prefix web run qualification:browser

The last command requires a fresh private localfixture manifest/profile and
pinned browser, supplied automatically by the qualification preparation flow.
It is not a manual profile-approval or trust ceremony. Current active compiler
and browser qualification guards use Node26; no Node24 fallback is accepted.
Go1.27.3 remains current. SQLite is selected at v1.62.0 with its exact required
libc v1.80.1; golang.org/x/sys is a direct dependency at v0.49.0. See
go-current.json and refresh-2026-11-07-sqlite.txt for the current selection and
native maintenance verification. Domain behavior changes are outside this scope.
V04 still verifies final integrated dispatch and two clean product builds.

Lint and type-check policy

Use the pinned Go formatter and standard go vet checks. Keep vetted warning
coverage in the existing gate. The TypeScript build checks strict types,
switch fallthrough and missing returns. The reviewed extra flow checks pass
on current sources without cleanup or emitted-code changes. Keep broader
unused, indexed access and exact optional property checks under evidence-based
review before enabling them project-wide. The compiler and current build/test
checks cover this project without a separate JavaScript lint framework.

Review stable tool upgrades and the enabled checks at milestone preflight.
Trial changes against the actual source and record the findings and maintenance
cost. Keep any exceptions narrow and retain earlier qualification evidence.

Upstream references

- https://pkg.go.dev/cmd/vet
- https://www.typescriptlang.org/tsconfig/#noFallthroughCasesInSwitch
- https://www.typescriptlang.org/tsconfig/#noImplicitReturns
