L02 localhost product composition

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
