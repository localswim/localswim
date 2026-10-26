SESSION - BROWSER AUTHENTICATION LIFETIME

Implements the accepted Slice01 05 session policy and D03/C02 exact code,
exchange, current-session and presented-credential revocation boundaries.
G02 owns the real native source lifetime and durable CLI credential state.
Code issuance authenticates that current native owner and serializes selection
of an existing enabled grant with revocation. It never issues a CLI credential.

Codes contain 128 random bits, expire after 60 seconds and are consumed once.
Browser bearers contain 256 random bits. Only decoded-secret SHA-256 hashes and
exact installation/origin/board/incarnation/grant/actor bindings enter memory.
No credential stores rights. Admission invokes G01 in S02's supplied snapshot;
current grant enablement, actor kind and rights are checked on every request.
Idle expiry slides by 30 minutes, bounded by eight-hour absolute expiry.

New service instances have empty browser registries. Closing clears browser
codes and sessions; G02's separately persisted CLI credentials are unaffected.
Self-revocation affects only the presented browser token or G02 CLI credential,
never a grant, domain operation, original outcome or replay authorization.
Code/exchange delivery uses C02's explicit ephemeral serialization only.

Ordinary tests do not construct setup managers, bind ports or access native
roots/trust. Tagged t01_native tests require a separately compiled test-only
root sentinel and explicit synthetic root overlay; missing overlay fails closed.
The fixture source uses real N01/G02/S01 providers and private synthetic boards.
Fixed-port native package runs must be serialized. These component scenarios
do not establish actual browser-profile, trust-store or power-loss qualification.
