Attachment HTTP adapter

The exact supported routes are:
- POST /api/v1/boards/{boardId}/cards/{cardId}/attachments
- GET /api/v1/boards/{boardId}/attachments/{attachmentId}

New borrows the existing store, native authority lifetime and session service.
Every mount applies the T01 ordinary Wrap boundary, including TLS, exact Host,
Origin/fetch mode and current bearer authentication. P01 owns executable mounting.
Native owner credentials, cookies and attach-only grants provide no read bypass.
Queries, encoded paths, aliases and unsupported methods have no route semantics.

POST accepts exactly one metadata and one file part. Metadata is at most 16 KiB
with application/json; file is at most 10 MiB with application/octet-stream.
Part Content-Disposition names must match, filenames are ignored, and duplicate,
extra, encoded or unsupported part headers refuse. NextRawPart prevents implicit
quoted-printable decoding. A global bound covers file, metadata and at most
16 KiB framing. A bounded raw delimiter scanner requires the first closing
boundary to finish the request, including buffered-ahead bytes; truncated bodies,
preambles, epilogues and marker-ended epilogues refuse before S02 is called.
C02 FreezeMutation binds command/route/current credential actor and validates
strict UTF-8, exact JSON fields, numbers and preconditions. Add verifies the same
immutable raw upload buffer's complete length and hash before writer checkout.

GET requires an empty body and returns only complete, validated raw bytes with
application/octet-stream, nosniff and a forced generated .bin attachment name.
Untrusted names/media labels never enter response headers, executable filenames,
inline content or error prose. No success JSON wrapper, encoding or CORS is added.
Safe typed errors precede streaming; a later delivery interruption is unavailable.

Ordinary tests exercise parser/route bounds and never invoke the native root or
listener. The windows/amd64 a01_native tests require an explicit private root-only
N01 overlay and a compiled sentinel absent from production. They exercise real
G02/T01 credentials, browser state, mounted refusals, replay and issuer-pinned
loopback TLS without installing trust or using real board data. Native tests must
run serially because the accepted stored port is fixed. These are isolated
synthetic qualification evidence, not product activation or power-loss proof.
