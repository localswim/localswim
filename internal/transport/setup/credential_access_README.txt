EXACT NATIVE CLI CREDENTIAL ACCESS

Manager.PrepareCredential validates the current installation and exact selectors,
pins the directory chain, and holds an independent per-credential OS lock until
Close. The lock does not acquire the installation lease, and ordinary reads do
not require an active native owner bearer. All native filesystem operations reuse
the existing setup provider's protected current-account/System ACL creation and
handle validation, no-reparse/hardlink guards, flush/readback and write-through
rename primitives. No alternate root or credential search exists.

The exact credential file is cli/ID/B/I/G.json. Its twelve exact fields are
schemaVersion:1 plus the validated credential response fields excluding apiVersion.
Private G.lock is an implementation exclusion object. G.json.next and
G.disabled.removed are fixed incomplete-publication objects; their presence stops
new use and is never silently cleaned up or adopted. No arbitrary path component
comes from credential JSON.

Issuance orchestration must prepare storage and call Disable before the HTTP
request. Disable writes, flushes and reads back the exact G.disabled marker.
Publish accepts only a fully validated response matching the selected identities,
writes private staging material, flushes/readbacks it, atomically replaces the
selected file, validates its bindings again, and moves the marker with the native
write-through primitive before removing the cleanup object and checking absence.
An incomplete cleanup object prevents ordinary use. Publication failures return
CREDENTIAL_OUTCOME_UNKNOWN and preserve available files for investigation. There
is no automatic issuance, retrieval, retry, rollback or recovery adoption.

For self-revocation, prepare and LoadForRevocation while locally enabled. Capture
the bearer once through WithToken to construct the session-bound revocation
client, then complete its session preflight before Disable. The consuming client
must enforce that Disable precedes its sole DELETE session request and disallow
all ordinary calls after that point. WithToken never captures a disabled bearer
or permits a second revocation capture. A preexisting marker makes the explicit
revocation load return CREDENTIAL_OUTCOME_UNKNOWN without a token or preflight.
A confirmed response permits Remove; uncertain responses
retain the marker and cannot be retried through ordinary OpenCredential. Remove
retains the marker even after confirmed credential-file removal.

OpenCredential verifies the current installation before opening the selected
private file and rejects disabled, malformed, mismatched, inaccessible or expired
material. It holds native exclusion throughout the returned material's lifetime.
Connection construction may consume WithToken transiently; its Close must release
the material. Close is idempotent. Secret objects refuse JSON and redact ordinary
formatting; no bearer appears in public metadata, fixed errors or evidence.

Focused native tests require the k03credentialqualification build tag and a private
root-only nativeRoot overlay supplying the compiled qualification sentinel. Missing
overlay fails compilation. Tests use synthetic credentials and a fake trust store;
they do not qualify live trust, human grants, product dispatch, secure erasure,
hostile same-account isolation or power-loss/all-crash durability. Returned-error
checkpoint injection is distinguished from native filesystem/child-process tests.
