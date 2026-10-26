Web package commands

Run from the repository root with the pinned Node/npm environment and the
locked web dependencies already installed:
  npm --prefix web run check
  npm --prefix web run build
  npm --prefix web run qualification:browser

Check and build resolve the installed local TypeScript package's tsc CLI.
Its version must match both package.json and package-lock.json exactly.
Both use -p tsconfig.json from web; check adds --noEmit and build adds
--noEmitOnError. The configuration declares dist/js as the emitted tree.
Missing tools, mismatched pins, compiler errors and a two-minute timeout fail.
No PATH, npx, download or alternate compiler fallback is used.

Browser dispatch convention

qualification/browser/run.mjs is the V02-owned Playwright configuration module.
The launcher resolves the exact locked local @playwright/test CLI and invokes:
  playwright test --config <repository>/qualification/browser/run.mjs --reporter=json

V02 owns the suite inventory and assertions that every mandatory journey is
included. This configuration must collect all required suites. The launcher
accepts no extra arguments that could filter them. Missing configuration or
runner, malformed/empty reports, zero tests, skipped/failed/incomplete tests,
expected failures, retries that failed, inconsistent totals, nonzero exits and
a five-minute timeout fail. Output is bounded to eight MiB. V02 must fit its
bounded suite within this limit or obtain a separately reviewed scope change.

P02 proves package wiring only. Its synthetic tests execute the actual pinned
compiler and runner but exercise no browser, trust, session or recovery journey.
Successful synthetic wiring tests do not establish browser qualification.
Without the real V02 configuration, qualification:browser must fail.

After installing the locked dependencies, test the scripts with:
  node --test web/scripts/scripts.test.mjs

Tests clone the current scripts, source and installed dependencies into fresh
temporary fixtures. They do not alter the repository dependency installation.
