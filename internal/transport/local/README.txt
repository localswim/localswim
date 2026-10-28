L02 localhost product composition

CatalogSnapshot is a read-only workspace projection seam. Current configured read
admission, revision, active Ready for Review count and retained committed activity/
coverage share one SQL snapshot. It joins Runtime request admission/drain, creates
no capability, and changes no board data or storage schema. The one-second attempt
budget does not promise a bound on arbitrary native IO. Workspace owns scheduling.

Workspace engine/host seam (M01.02)
- OpenEngine acquires a listener-free independent Runtime using the canonical
  workspace origin and UUID. Existing members require ExpectedIdentity; a missing
  expected root never creates a directory or initializes replacement bytes.
- StartWorkspaceHost owns exactly one configured numeric-loopback listener.
  Its caller must hold persistent workspace ownership. Attach transfers an open
  engine on success; on refusal the caller still owns it and must close it.
  Detach gates that member then drains outside host bookkeeping locks. Close
  gates host admission and all members before draining/releasing their lifetimes.
- Runtime keeps the stored standalone port, actors, grants and durable tuple.
  Workspace epochs and credentials are ephemeral. Existing store/domain code and
  SQLite schema are unchanged. No member listener, upstream proxy or replay exists.
- Standalone service.json retains exact unversioned identity/origin/epoch/botToken.
  Workspace member metadata is the strict version-2 alternative, adding
  metadataVersion and host:{mode:workspace,workspaceId}; its effective origin may
  differ from identity.Port. ReadClient bounds it to 64 KiB, derives SessionPath
  from validated mode/BoardID, and never treats rendezvous as health.
- GET /api/v1/runtime-mode is strict, nonsecret and no-store. Workspace has no
  root board session/bootstrap. Selected bootstrap takes apiVersion,
  installationId and incarnation at /api/v1/boards/{boardId}/local/bootstrap;
  GET session uses that same board prefix. Every selected dispatch revalidates
  the pinned durable identity, refusing replacement instead of remapping it.
- Board browser cookies include installation/board/incarnation in their names
  and use Path=/api/v1/boards/{boardId} with no trailing slash. Cookie path
  segment matching includes both the exact BoardRead route and its descendants,
  without including another board or the sibling workspace channel. This is the
  bounded M01.02 correction to the accepted contract's trailing-slash spelling.
  Workspace bootstrap uses its own host
  cookie at /api/v1/workspace/. Host Workspace callbacks receive only the
  authenticated WorkspaceChannel (browser or cli), never board rights. The
  automation capability cannot authorize board commands or return human grants.
- Cleanup removes only matching identity/origin/workspace/epoch metadata while
  ownership is held. Failed metadata cleanup retains the owned engine for retry;
  a failed store close retains ownership and its error rather than claiming
  release. Admitted handlers and read/export work drain before native release.

This seam does not persist the registry or host rendezvous, wire serve --workspace,
or implement catalog/preferences/control routes. Those are subsequent packages.

Start requires an explicit absolute data directory. It creates only a new private
root (or reopens an existing valid private root); parent directories must exist.
The numeric IPv4 loopback listener defaults to 8792 for a fresh board. Port zero
on reopen uses the stored port; an explicit conflicting port refuses. No alternate
address, ephemeral port, TLS fallback or global setup root is consulted.

Private layout
- identity.json: version-one local installation/board/incarnation and configured
  human/bot actor/grant selectors plus stable port; no capability.
- board/: existing S01 exact-layout SQLite store and lifetime writer exclusion.
- local.lock: native exclusive handle for metadata/bootstrap ownership.
- service.json: atomically published current epoch/context and automation-only
  ephemeral capability. ReadClient is bounded, validates privacy and context, and
  never establishes health. CLI must verify the live session before writing.
- cli-evidence/: separate private immutable CLI evidence directory.

First-run initialization persists actor/grant policy in the existing schema in an
explicit serialized write transaction. Initialization is not retried or repaired
when a partial store or unknown root contents exist. Existing disabled grants or
changed policy are never replaced on reopen. S01 settings, safe journal refusal
and native path/exclusion policy remain unchanged. HTTP origins are not stored in
legacy TLS credential tables; no schema migration was introduced.
Shutdown closes request admission under the same mutex used for request counting
before stopping HTTP workers. Already admitted requests drain before authority,
SQLite and writer exclusion are released; delayed handler entries refuse 503
without touching those resources, including after the transport has closed.

Browser bootstrap is POST /api/v1/local/bootstrap with {}, exact Origin and
X-Localswim-Client: browser. The response contains context and SessionRead only;
the human bearer stays in the installation-namespaced host-only HttpOnly,
SameSite=Strict, Path=/ cookie. Browser mutations require exact Origin plus that
non-simple header. GET without Origin also requires Sec-Fetch-Site: same-origin.
CLI requests use only the current automation bearer. Mixed authentication,
duplicate/malformed bearer, unknown Host/Origin and preflight refuse. No CORS is
granted. Bootstrap replaces a stale prior browser cookie after restart without
replaying any mutation. Restart rotates all ephemeral authority and epoch.
Raw Cookie fields are strictly checked before either auth channel is selected:
malformed pairs and duplicate names refuse, including pairs net/http would
silently discard. The same parser guards explicit bootstrap; only a well-formed
stale cookie may be replaced there.

Every ordinary route passes mandatory Boundary.Wrap before the unexported domain
dispatcher. Access.Current selects live server-resolved identity; Access.Admission
resolves the hash registry again inside G01/S02's supplied transaction snapshot.
Rights are read from SQLite each time. No owner/grant/retention administration or
legacy exchange/issue route is mounted. Strict card.edit cannot select Completed;
only the existing explicit human approve command can do so. Bot grants contain
neither approve nor administrative rights.

Native synthetic tests exercise actual loopback HTTP and account-private Windows
files without trust installation/enrollment or setup ROOT. L02_TEST_ROOT may select
an existing explicit private-test parent; each test creates a fresh child and
retains it as evidence. Without that variable, normal Go temporary roots are used.
On managed restricted-token executors, run these tests with approved ordinary
Windows-token execution: an AppContainer token cannot reopen newly created files
whose ACL deliberately permits only the desktop account and SYSTEM. Do not add
package principals or weaken privacy to make such a runner pass. These are L02
component/smoke cases, not V02 browser or V03 restored-copy qualification.
