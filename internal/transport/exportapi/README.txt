H02 HTTP EXPORT ADAPTER

New borrows the source Store, native credential service and session service.
The composition owner mounts this handler; executable mounting remains P01.
Only POST /api/v1/boards/{boardId}/exports is selected, with no path aliases or
query semantics. The exact D03 request is incarnation, expectedRevision, input:{}.
The shared decoder rejects unknown/duplicate fields and malformed Unicode/numbers.

Every request passes T01 Wrap for HTTPS, Host, Origin, media and current bearer
checks. The route board must match the credential's board. Native owner authority
does not supply ordinary export rights. The current session selects an actor and
scope, never cached authority: mandatory A02 Verify calls the credential-bound
G01 Require(read, export) through A02's actual pinned snapshot. A02 checks exact
identity/revision, projects and validates the bounded archive before any output.

Prestream errors use D03 NonterminalRefusal. Successful delivery is application/zip
with the Prepared byte length and a forced inert download name. Only A02 writes
the stored ZIP, exact inventory and final COMPLETE member. No archive is rebuilt
here; no server path, operation receipt or JSON success status is produced.
A writer failure, short write or cancellation aborts the HTTP response without a
JSON trailer. HTTP delivery never proves browser download offered or verified
local save; client status/publication and independent saved board state remain
separate obligations. There is no automatic replay.

Verification
- Ordinary route, strict envelope, body bound and fail-closed construction tests.
- Native synthetic stores exercise current CLI/browser authority, absent rights,
  revoked grants/credentials, exact preconditions, stored inventory/COMPLETE and
  arbitrary binary bytes through the accepted services.
- Pinned TLS delivery and real truncated HTTP response, plus injected zero,
  partial, short and cancelled output, never produce a local-save success claim.
- Native tests require h02_native and a private setup-root resolver overlay with
  a compiled sentinel; absence fails compilation. All storage, setup material,
  grants and credentials are synthetic and private. No issuer trust is installed.

These component tests do not qualify executable activation, browser save UX,
offline validation/restore, human enrollment or native power-loss durability.
