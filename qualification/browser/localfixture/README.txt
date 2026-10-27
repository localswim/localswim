V02 local fixture (qualification only)

prepare.py snapshots exact current Go/module/embedded inputs into a fresh private
attempt, builds the ordinary application and a separate tagged fixture offline,
and records input hashes before/after. A changed input refuses publication. Failed
attempts retain logs and runtime directories. The Go tool hash/version and pinned
browser executable are checked; manifest v2 contains no credentials or assertions
of authority/readiness. Browser qualification is a separate required execution.

Run prepare.py with the frozen tools and offline caches available. --artifacts
selects the private existing artifact root. Native socket/file tests may require
host execution under the managed environment; retain the failed attempt first.
The published run-manifest.json is consumed by the browser launcher.

The executable accepts --data ABSOLUTE-FRESH-DATA-DIR --port PORT. It uses actual
local.Start and verified embedded assets. It refuses an existing initial data
directory. stdout is bounded secret-free NDJSON: one ready record, then one result
per stdin request {"op":"status|dropNext|delayNext|release|restart|stop"}.
Unknown/duplicate fields, malformed/oversized input and unknown operations refuse.
An oversized line ends the controller with graceful cleanup and nonzero exit.
EOF releases delivery barriers, drains/closes the service, and exits. stop closes
before acknowledgment. restart closes before reopening the same data and port.

The private runtime.go overlay only wraps the actual http.Server Handler and adds
the unique V02QualificationDelivery sentinel. The normal tagged build must fail
at that missing sentinel. Production sources and provider behavior are unchanged.
The wrapper counts original board/card HTTP mutation submissions cumulatively,
including refused requests. Bootstrap/session/GET requests do not consume faults.
It runs the real handler before injecting response-body truncation or delaying
its exact status, headers and body. dropNext forwards the real status and headers,
sets Content-Length to the captured body length, flushes those headers, then closes
the socket before sending the body. This explicitly labeled truncation prevents
an empty pre-header disconnect from inviting a transparent browser POST retry.
The runtime admission gate and SQL lifetime remain authoritative.
release/restart/EOF release barriers before normal Close drains requests.

Mechanical tests cover strict control frames, delayed real response preservation,
actual socket drop, real embedded root, identity/capability rotation on same-data
restart and EOF cleanup. A separate ordinary no-overlay lifecycle test checks the
normal runtime startup path. These tests do not prove browser behavior or physical
power-loss durability. There is no control HTTP endpoint, reverse proxy, SQL write,
issuer, TLS, enrollment, login code, delegation or browser-profile approval.
