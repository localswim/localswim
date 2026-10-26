assetstage - declared production assets

Run from the repository root after the locked local TypeScript build:
  npm --prefix web run check
  npm --prefix web run build
  go run ./cmd/assetstage
  go run ./cmd/assetstage -check

P02 owns the npm script wiring. The frozen future build sequence uses -check;
normal generation is an explicit maintainer step before committing the bundle.
No package installation, network lookup, bundler or compiler API is performed.
TypeScript must be exactly 7.0.2. Defaults use node from PATH and the installed
web/node_modules package. -node and -modules may select an existing offline
installation of those same locked tools; absolute tool paths are never emitted.

web/tsconfig.json declares production files and web/dist/js as compiler output.
assetstage independently invokes the actual local tsc CLI into a unique temporary
directory, compares every emitted module against web/dist/js, and rejects missing,
stale or extra output. Only the declared index, stylesheet and emitted modules
enter internal/assets/bundle. Tests, source maps, arbitrary static files, runtime
records and private inputs have no staging path. Symlinks and nonregular inputs
are refused, including ancestors of declared files. Public text is UTF-8/LF with
one final newline. Temporary compiler output is removed after each invocation.

The checked-in manifest binds file bytes, declared source/configuration, package
locks, generator and Go-owned contract-generation inputs, plus the exact local
compiler package/native executable/library bytes. Timestamps are irrelevant.
Check regenerates and compares without changing checked-in bundle files. Compiler
bindings currently describe the candidate platform used to produce the bundle;
matching another platform is a separate qualification task. No cross-platform
product reproducibility or durability claim follows from this mechanism.
