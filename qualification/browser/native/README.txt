V02 native fixture preparation

This directory contains qualification-only source, excluded from ordinary Go
builds unless v02qualification is selected. It is not product executable wiring.
The ordinary source tree intentionally cannot compile the tagged fixture: its
required V02QualificationRoot sentinel exists only in the private root overlay.

Preparation
Run prepare.py from the working checkout. It creates a new private attempt and
canonical short NTFS runtime for every invocation, snapshots current source,
uses qualified local Go/dependency/cache inputs offline, and first requires the
normal missing-overlay compile to fail at the specific sentinel. It then builds
with one hash-bound nativeRoot-only overlay and executes only preparation.
The compiled nativeRoot is an immutable one-entry exact allowlist. SourceRoot's
New(nil) uses that same guarded setup package; there is no normal-root fallback.
No provider source is edited in the working checkout. The unchanged production
routes.go is copied to the private fixture command, and verified embedded assets
are served by the same product adapters after the later owner gates.

Preparation creates fresh synthetic N01 issuer/material, an empty product board,
and private profile/evidence directories. Native setup's normal temporary port
availability bind is closed; preparation starts no service or control listener.
The private manifest, ACL report, DER, exact human grant request and owner action
commands are reviewable before any approval request. They contain no bearer or
browser code. Private native setup files retain their normal secret material.

Owner actions
The trust wrappers call the existing setup Runner. The human wrapper calls the
existing admin Runner with its actual console recognition and typed challenge.
No synthetic terminal proof, unattended confirmation or --yes path is provided.
Bootstrap serving requires observed physical CurrentUser Root issuer membership.
It exposes no broker. Qualified serving additionally requires an actual enabled
human grant with exactly the reviewed minimum rights and an exact delegation
line read from a real console. That scope enumerates code issuance for the three
fixed actor IDs, provisioning/revocation, post-handler drop/delay/release,
same-store restart with code/session invalidation, status/audit and stop. Starting either server requires separate owner
authorization; preparing this code does not authorize that action.

The native-owner delegation covers only the predeclared synthetic automation
actors, their exact rights, browser code issuance, dedicated revocation and
transport delivery/restart controls. It gives no automated human approval right.
Positive browser qualification still needs real trusted TLS, the pinned browser,
and installation/profile-bound runtime evidence. No positive journey has been
executed by preparing the fixture.

Private IPC
One local Windows named pipe uses protected current-user and SYSTEM access,
FIRST_PIPE_INSTANCE, remote rejection, overlapped I/O and bounded deadlines.
Each connection accepts one strict NDJSON request of at most 1024 bytes and one
response of at most 8192 bytes. The client closes after consuming its response.
Unknown operations/fields, duplicate JSON keys and bad subjects are refused.
CodeSecret.code travels only in pipe memory through the production explicit
ephemeral encoder, then compacts into one bounded NDJSON frame. Owned input,
compact and framed byte buffers are cleared after use. Requests and responses are never
logged; no HTTP control route, URL secret or product CLI secret channel is added.

status returns actual trust/human/delegation readiness, exact identities, grants,
revision and counters. Counters mean HTTP mutation/lookup submissions, not commit
claims, and survive restart. auditProfile performs a read-only complete native ACL/type/reparse audit. Its
profile root remains protected with only the owning account and SYSTEM. For the
pinned unbranded browser, descendants may additionally contain only the exact
Windows-derived lpacChromeNetworkSandbox capability in Default/Network,
Default/Cache and Default/Shared Dictionary, with the reviewed mapped/inherit-only
masks and flags. A restrictive Everyone FILE_EXECUTE-only file deny is recognized.
Every object retains its current-account owner and ordinary current-user/SYSTEM
grants; unknown principals/ACE forms fail. No ACL is changed. Native ROOT, board,
evidence and named-pipe predicates remain unchanged. Output includes aggregate
counts, with no file names or account SIDs. Browser pin fields must match the
reviewed executable/hash/version before applying these profile-only semantics.
Delivery controls buffer only after the actual product handler returns. Restart
drains handlers, closes sessions/native/source and reopens the same validated
store/TLS. These controls require later owner-approved activation and live proof.

Evidence
Every attempt retains its source, overlay, build logs, result and runtime.
Failed attempts are never silently reused. Preparation results bind original
root source, overlay, binary, copied production routes and source hashes. Public
evidence must exclude private paths, real account SIDs and native secret files.
Trust removal remains an exact typed owner action after stopping the service;
manual cleanup is limited to the exact private anchor after evidence review.

Native capability API reference:
https://learn.microsoft.com/en-us/windows/win32/api/securitybaseapi/nf-securitybaseapi-derivecapabilitysidsfromname

This private candidate includes reviewed fixed-stage diagnostics and has not
replaced the current live fixture binary or manifest. Its fixture main remains
unexecuted; a separate read-only audit command may inspect the existing closed
profile with browser pin metadata supplied in memory only.
