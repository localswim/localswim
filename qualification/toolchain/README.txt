CURRENT JAVASCRIPT BUILD/TEST TOOLCHAIN
2026-10-27 | #0062 freshness selection

Use Node26.12.1 and npm12.2.1. .node-version and web/package.json select the
same exact runtime; packageManager selects npm12.2.1. The original Q01
qualification/environment.json remains historical input evidence, not the
current Node/npm selection. current.json records artifact checksums and sources;
new qualification results are separate from immutable earlier reports.

Official Node index selects26.12.1 as latest stable26 on the recorded date.
It is Current on2026-10-27, with LTS scheduled2026-10-28. No future LTS release
or automatic upgrade is claimed. npm latest is12.2.1; TypeScript7.0.2 and
Playwright1.64.1 remain the registry latest stable direct dependencies, so their
locked package versions/integrities and matching native compiler remain unchanged.

Provisioning uses the official Node Windows x64 ZIP/SHASUMS256 and npm registry
tarball integrity. The verified project-local runtime preserves the original
bundled npm separately and installs verified npm12.2.1 behind its normal shims.
Its paths are private execution evidence, never source configuration. No global
PATH or other project's installation is changed. For a separately installed
developer runtime, choose these versions through normal Node tooling and confirm
node --version / npm --version before building. The packaged Go app itself needs
neither Node nor npm at runtime.

Commands from repository root
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
Go/module/SQLite pins and domain behavior are outside this JS freshness change.
V04 still verifies final integrated dispatch and two clean product builds.
