K01 board HTTP adapter

New borrows the existing S01 Store, G02 native lifetime and T01 session service.
The composition owner mounts it beside the session, attachment, grant and export
adapters. It does not open another native lifetime or mount those other routes.

Exact routes cover board/card/history reads, original operation lookup, card
create/edit, comments, links, review handoff, approval and recovery preview/confirm.
T01 enforces TLS, exact Host/Origin, browser fetch/write rules and current bearer
authentication. C02 freezes strict input with the route-selected board/card/command
and authenticated actor. B01/B02/G01/S02 retain transactional rights, workflow,
revision checks, NO_CHANGE, terminal recording and receipt-first reconciliation.

Reads project the existing D03/C03 types from one authorized SQLite snapshot.
History keeps original provenance and current card revision separate; full rows,
known removed references, tombstones and coverage gaps retain C03 validation.
Any query, decoding, validation or cleanup failure refuses the entire read.

Lookup supplies only original incarnation/version/fingerprint with route board/op
and authenticated actor to S02. It never constructs an intent or executes a command.
The narrow native-owner branch delegates only to G02's own grant-outcome authority.
Lookup refusals remain nonterminal; consumers retain the original uncertainty.
S02's internal LookupCompleted flag distinguishes a completed unresolved lookup
(HTTP 200) from unavailable execution or cleanup (HTTP 503). It is excluded from
JSON and does not alter the original identity, evidence class or current rights.

Recovery delegates to S03 with current G01 admission on every call. Credential
digests key private in-memory service instances; successful previews retain their
exact five-minute expiry, including concurrent previews. Expired inactive entries
are removed during recovery access. Confirm-only traffic does not retain instances
or extend preview lifetime. After cache loss, a fresh S03 instance still calls S02
so matching retained outcomes resolve before any missing-preview admission check.
No credentials, previews, card prose or internal errors enter diagnostic messages.

Ordinary package tests exercise exact routing, evidence query shape, status/proof
separation, body bounds and cache lifecycle. The windows/amd64 k01_native suite
requires a private root-only setup overlay with a compiled sentinel; without the
overlay it fails compilation. It uses synthetic native material and private board
fixtures for real service admission, retained outcomes, source/compact history,
recovery, browser boundaries and issuer-pinned loopback HTTPS. The loopback test
requires exclusive access to the configured fixed port. It installs no trust.

These bounded tests do not qualify product dispatch, browser storage, crash or
power-loss durability. CLI/UI, schema/contracts, attachment byte handling, grant
administration, export, restore and executable route composition remain separate.

M03 archive visibility
Archive/unarchive is an exclusive archived Boolean card.edit through the existing
writer, revisions and original outcome path. Completed refuses before no-op;
unchanged unfinished flags retain NO_CHANGE without history/activity advancement.
Existing unfinished-card edit admission and exact retained references remain.
GET cards is a separate partial empty-query listing (active by default, archived
or all explicitly), ordered by priority/ticket/UUID with at most 20 summaries.
Each continuation rechecks current read authority in one SQLite snapshot. HMAC
cursors bind handler epoch, actor, board/incarnation/revision and exact canonical
filters; stale/tampered cursors refuse CURSOR_STALE. BoardRead stays complete.
