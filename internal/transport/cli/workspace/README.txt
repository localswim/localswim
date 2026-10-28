Workspace CLI connection and private selection

Open verifies the authenticated workspace channel using bounded private metadata
and one Projects observation. Control mutations reuse that observed revision and
send once. Typed stale/refusal results remain distinct from UNKNOWN. UNKNOWN
contains last-observed revision only and no unverified identity; status inspection
is deliberate, never automatic replay or a second control attempt.

Select binds explicit keys to private physical roots/full identity and live
resolution/member epoch/local metadata, then verifies the existing automation
session. The workspace capability cannot authorize board reads or mutations.
Returned selections feed the existing core/archive adapters and board-local
evidence store. Client and selection formatting/JSON refuse secret disclosure.

Locate is private mapping/evidence discovery with no live or SQL requirement.
It permits safely loaded originals to remain UNKNOWN when live authority is
unavailable. It never authorizes domain dispatch or widens evidence root guards.
Callers retain nonnil cleanup owners even on errors. Close is retryable; Done
closes only when native custody is released. Cleanup retry is not command replay.

Native and controlled transport tests are component evidence. Actual executable
and joined customer qualification remain separately recorded by the primary.
