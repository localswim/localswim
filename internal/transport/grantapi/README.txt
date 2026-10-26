Grant HTTP adapter

This package registers only POST /api/v1/grants and DELETE
/api/v1/grants/{grantId}. P01 owns mounting it in the executable. Every request
passes the shared T01 HTTPS, Host, Origin, preflight and JSON boundary. Ordinary
browser/CLI callers pass T01 authentication; native bearers require G02's actual
current provider verification before any grant-scope read or attribution effect.

C02 freezes the exact strict request with the authenticated actor. Create binds
the validated body board and null target. Revoke binds the exact route grant and
independently stored board scope; a submitted board mismatch refuses. G01 and S02
own current authority, incarnation, exact board revision, null card revision,
grant changes, ledger identity and commit reconciliation. Native changes retain
G02's guarded first-owner attribution and full service lifetime.

New grant commits use HTTP 201. Matching retained receipts, revocations and
retained not_committed outcomes use 200. Known-identity uncertainty uses 503;
prevalidation/authentication refusals retain their D03 refusal shape. A refusal,
lost response or missing evidence never establishes an original noncommit.

No enrollment/terminal consent, credential issuance, session, export, ordinary
domain mutation or outcome-lookup route is provided. Human terminal consent
remains K03's responsibility. Automation grants cannot receive approve,
grant-admin or history-admin. The adapter does not replay uncertain operations.

Ordinary tests do not construct a native provider or touch installation roots
or the fixed service port. Explicitly tagged native tests require a fresh private
qualification root and a compile-time overlay sentinel; production root,
cryptography, ACL and lock behavior are preserved. Synthetic tests establish the
specified adapter behavior, not native power-loss, hostile-site or platform
qualification.
