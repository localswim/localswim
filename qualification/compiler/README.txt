Q03 - ISOLATED GENERATOR / RELEASED TS7 / EMBED HARNESS
2026-10-25

Scope
This is a synthetic compiler qualification fixture. It owns no application schema,
command, authorization or browser behavior. The Go descriptor emits the checked
sample JSON Schema and TypeScript declaration. The sample runtime validator is
explicit, small and independently written in TypeScript for bounded comparisons.
Neither declaration types nor JSON Schema alone validate runtime data.

Run
Use native Windows/amd64, Go 1.27.3, Node 26.12.1, npm 12.2.1 and released
TypeScript 7.0.2 with @typescript/typescript-win32-x64 7.0.2. All paths supplied
below must be absolute. Use a fresh private work directory, outside this source.

1. Verify official Node ZIP SHA256 and npm tarball SHA512 against environment.json.
   Extract privately; invoke that Node executable and exact npm-cli.js explicitly.
   Copy web/package.json and package-lock.json into a private install directory.
   Run: NODE NPM_CLI --prefix PRIVATE_INSTALL ci --ignore-scripts --no-audit --no-fund
   This uses the unchanged accepted lock. No lifecycle scripts or global installs.
2. Set process-local GOTOOLCHAIN=local, CGO_ENABLED=0, GOOS=windows, GOARCH=amd64,
   GOFLAGS=-mod=readonly. Optionally select private GOCACHE and GOMODCACHE paths.
3. Set Q03_NODE to that node.exe; Q03_NPM_CLI to exact npm bin/npm-cli.js;
   Q03_TSC to PRIVATE_INSTALL/node_modules/typescript/bin/tsc; Q03_WORK to the
   fresh private work directory. The harness fails if required inputs are absent.
4. From repository root run:
   go test -count=1 -v -tags=nativecompilerqualification ./qualification/compiler

Generator inspection (writes only the explicitly chosen output directory):
   go run ./qualification/compiler -out PRIVATE_GENERATED_DIRECTORY

The harness records raw commands/outputs and result.json only in Q03_WORK. These
records can contain local paths; keep them private. Checked-in results.json is a
sanitized outcome record. Ordinary untagged Go tests do not run this native suite.
No missing environment or required case is converted into a skipped pass.

Checks
- Regeneration exactly matches checked synthetic schema/declaration bytes.
- Schema shape, safe-integer bounds and declaration shape are checked explicitly.
- Thirteen fixed expected vectors agree in descriptor-driven Go validation and
  actual emitted JavaScript under pinned Node: optional absent/string/null,
  required/null/type/unknown-key cases, zero/max/negative/fraction/overflow integer,
  empty string, and a preserved non-ASCII/combining/astral string.
- Actual local native TS CLI runs -p with strict, exactOptionalPropertyTypes,
  noEmitOnError, ES2022 modules, and a declared assets output directory. --noEmit
  creates no output. Five rejected declarations require specific diagnostics;
  the failed compile must create no rejected.js.
- Two distinct clean temporary source trees produce identical generated files,
  emitted JS, and -trimpath/-buildvcs=false Go executable hashes. Shared verified
  tools and Go cache are allowed; this is not a cold-cache product build claim.
- Each native Go executable embeds exactly three declared assets and verifies
  their SHA256 hashes. A changed embedded asset must fail at execution; an absent
  declared asset must fail compilation with the expected missing-file diagnostic.

Limits
Only these samples and exact Windows compiler/toolchain pins are exercised.
No product-schema equivalence, canonical encoder, duplicate-key/invalid-Unicode
wire validation, application build, product reproducibility, browser execution,
SQLite behavior, domain admission or final release qualification is established.
TypeScript number types do not themselves impose integer bounds; the explicit
runtime sample check does. Future C01/P01/P02/Q04 and native/browser work retain
all their acceptance gates. No root manifests, locks, product packages or scripts
are changed by this harness. Fresh independent review and owner acceptance remain.
