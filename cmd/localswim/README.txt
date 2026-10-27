Current normal entry: see LOCAL.txt for the L02 loopback HTTP browser/CLI path.
No old setup, code or --config command is registered by main. The P01 composition
below is preserved as a legacy component description and isolated test fixture.

Historical P01 executable composition

routes.go mounts the accepted T01, K01, A01, H01 and H02 handlers. Selection
never cleans paths, redirects, changes a method or retries another adapter.
Each adapter retains its owned route grammar and shared authentication boundary.
Static files are supplied only by the verified embedded-assets handler.

service.go composes an explicitly selected preexisting private board and exact
board/incarnation identity with S01 Existing, G02 SourceRoot, T01 sessions and
dual-loopback issuer-pinned HTTPS. It never provisions a board, bootstraps actor
attribution or grants, enrolls trust, activates a restore or chooses another port.
Shutdown drains transport before closing sessions, native authority and storage.
Storage/schema refusal is preserved without migration or implicit recovery.

cli.go selects the owning core/admin/archive runner and forwards the original
arguments and streams unchanged. The existing parsers, evidence, credential-file,
terminal confirmation and private console adapters remain authoritative.

Approved runtime binding
The exact interface is docs/contracts/runtime-bindings.txt:
  localswim serve --config FILE
  localswim --config FILE EXISTING-CLI-PREFIX-AND-COMMAND
Setup retains its D01 syntax and may omit config. No config search, working-directory
inference, hidden creation or default credential is permitted. Config is strict
bounded JSON selection data, never authentication or authority. Its installation
must match the actual current N01 source before service binding. All ordinary CLI
selectors remain mandatory and existing runners receive the unchanged arguments.

The native config reader pins ancestor/file identities and rejects reparse points,
hardlinks, device/UNC/ADS aliases, unsupported local filesystems and oversized files.
Protected inventory always includes actual N01 ROOT, evidenceDirectory and a
configured board directory, plus the explicitly supplied protectedRoots. Existing
archive adapters enforce overlap exclusion and all native publication policy.

Only GET / and /index.html carry the active native installation UUID in the
X-Localswim-Installation header. Embedded HTML bytes stay unchanged. It discloses
no board, principal, grant, private path or secret and supplies no authentication.
The browser remains unqualified for mutation until its separate evidence exists.

Verification
Ordinary tests inspect route and CLI family dispatch without opening any real
installation, trust store, board, grant or credential. p01_native tests require a
compiled private-root-only setup overlay and sentinel, synthetic protected files,
real N01/G02/T01 and accepted adapters. Missing overlay fails compilation.
Synthetic grant/credential creation is limited to that private fixture. Native
positive confirmation, login-code console presentation, browser trust and power-loss
safety are not established by these tests. The compiled private-overlay executable
suite uses an isolated hidden child console/group and targets only that owned child
for graceful stop. Product release and actual build-sequence qualification remain
separate downstream work.
