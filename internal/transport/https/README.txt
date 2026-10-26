HTTPS - INSTALLATION AND SESSION TRANSPORT BOUNDARY

Implements accepted D01 and Slice01 05 Host/Origin/TLS rules plus the exact
D03/C02 session routes. New uses G02's current connection identity; Listen
borrows its already-acquired N01 service lifetime and certificate configuration.
It never acquires a second setup lease or installs trust. Both exclusive IPv4
and IPv6-only loopback binds must succeed at the stored fixed port. Startup
refuses otherwise; no alternate port, wildcard listener or HTTP fallback exists.

Every request requires TLS and exact configured Host. Present Origin must equal
the configured origin; null, alternate authorities and preflight refuse. Browser
exchange and writes require exact Origin and JSON. Safe browser GET without
Origin requires Sec-Fetch-Site same-origin. The absent-Origin CLI exception
requires a current persisted G02 CLI credential; a claimed header is insufficient.
No CORS or cookie authentication is provided. Replies disable caching, restrict
script/connect sources to self and deny framing. Diagnostics contain fixed inert
text and exact nonterminal refusals, without received prose, URLs or credentials.

Session paths, methods and DTOs are exact. Query strings and escaped path aliases
are refused. JSON is bounded to 4096 bytes; GET/DELETE session have empty bodies.
DELETE carries application/json even with its empty body. Secret success bodies
are delivered once using explicit C02 ephemeral serialization and no-store.
The dedicated native-owner issue routes delegate authentication to G02; ordinary
native-owner tokens cannot acquire a board SessionRead. Generic Wrap authenticates
ordinary browser/CLI consumers and supplies trusted mode/token context only;
later adapters still perform current G01/S02 authorization and exact route/query
validation. Native applies only the shared transport checks to dedicated native
adapters. It never sets authenticated context: those adapters must invoke guarded
G02 methods before any authority-sensitive read or effect. JSON write enforcement
allows only the accepted exact attachment-upload route's multipart exception;
the attachment adapter still owns bounded part validation. Grant, domain,
attachment and export handlers remain their task owners.

The server suppresses unsafe HTTP diagnostic logging and uses bounded header,
read, write and idle timeouts. The composition owner stops transport before
closing sessions and the G02 native source. Browser UI and executable mounting
belong to later tasks. Native component tests use only explicit synthetic-root
qualification overlays; actual browser-profile qualification remains separate.
