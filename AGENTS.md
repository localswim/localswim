# Repository guidance

This is the shared Go/TypeScript localswim implementation monorepo. Follow the
accepted Spike 01 contracts and reviewed staged task decomposition in the public
v2-architecture repository; newer owner direction takes precedence.

- Keep authoritative domain, permissions, revisions and admission in Go. Browser
  feedback is advisory; generated boundary contracts must remain consistent.
- The Go module and web/ package are separate build boundaries in one repository.
  Ship only verified embedded assets. Do not introduce project-local board forks.
- Public MIT FOSS: UTF-8, LF, final newline; preserve binary bytes. Never commit
  real board data, runtime credentials, private rosters or recovery stores.
- Use synthetic fixtures. Do not copy v1 source/tests/internal designs or claim
  independently isolated clean-room certification; prior exposure is disclosed.
- Distinguish candidate availability, isolated qualification and product evidence.
  Do not install issuer trust or issue human grants without the required explicit
  owner action. Mocks and process termination do not prove native power-loss safety.
- Use the shared installed CLI for administrative boards; never edit their JSON
  directly. Move an eligible task In Progress before work or delegation. Scoped
  dependent work stays Ready for Work; dependencies and acceptance gates remain.
- Keep a task In Progress through implementation and fresh non-author review.
  Only the owner controls Completed. Review/approval alone belongs Ready for Review.

## Fresh-eyes review exception

Terry clarified on 2026-10-29 that a fresh-eyes subagent review of work authored
by the parent is always permitted, even when no useful work can overlap. An
independent audit without the parent's preconceived conclusions is a useful
use of credits; parallel work is not a prerequisite for this review.

This supersedes earlier parallel-only delegation restrictions for that review.
It does not require fixed reviewer pairs or idle staffing. Keep other purely
serial work in the parent, provide evidence without prescribing a verdict, and
describe actual independence truthfully. Preserve task authorization, required
verification and explicit model/effort, depth and concurrency limits.

## Shared board owner notifications

Use the built-in owner notifications in the healthy shared board service from
`C:\Projects\localswim`. No legacy JSON watcher, pool, optional monitor extra or
duplicate service is needed. The browser SSE/status-bar indicator is separate.

For an explicit connect request, use an absolute owned BOARD path and a chat UUID
verified from current Codex app metadata. Run the shared frozen CLI's
`notifications status --json`, then `notifications target THREAD_UUID` only if
needed, `notifications enable` only if disabled, and verify status/identity/target.
Use `uv run --project C:\Projects\localswim --frozen --offline localswim-cli BOARD`.
Check exits and inspect status after uncertain writes; never blindly replay.
Target changes preserve enabled state; disable retains the target. Documentation
or service startup alone does not activate delivery. Honor explicit shutdown.

Committed Human edits coalesce after 15 seconds of quiet, then one bounded public
`codex queue` attempt can wake the owning chat without Terry typing a prompt.
Bot edits do not notify. Delivery is best effort: no durable cursor, catch-up or
retry, and shutdown can lose a pending burst. Windows needs native `codex.exe` on
the service PATH. Queue acceptance is not completed work or a ticket authorization.

For queued alerts inspect sanitized `activity monitor-between SINCE UNTIL --json`
first, with bounded pagination. Only valid, complete, nonempty all-Bot metadata
permits suppression; otherwise inspect focused indicated cards and honor normal
work gates. Legacy filesystem watcher/pool commands and their optional extra are
retired: do not launch/reload/stop old interfaces, reinstall the native watcher
dependency or recreate local watchers from historical code. Stale launchers fail
with built-in guidance; no compatibility flag/environment override enables old
startup. Preserve legacy target/cursor/pending/lock files and never act on a
remembered PID without verifying current process/ownership. Project caches,
runtime and test evidence stay outside the shared installation.

## Mandatory shared message queue workflow for web-client development

Terry approved the Valkey/Webdis replacement on 2026-11-05. This section
supersedes the earlier HTTP-to-file workflow.

### Rollout and ownership

Cutover completed on 2026-11-05. Use the qualified shared queue at
`http://127.0.0.1:53147`. The legacy dated API and staging53170 are retired.
Windows task `Agent Message Queue` starts at Terry's sign-in and runs independently
of Codex. Its registered trigger and manual execution are verified; an actual
sign-out/sign-in cycle remains to be observed. Preserve operator ownership.

The server uses Valkey 9.0.7 and Webdis 0.1.25 with volatile in-memory messages,
plain HTTP and wildcard CORS. Use the installed native client
`C:\Projects\shared-message-queue\bin\localpost-client.exe`.
The frozen contract is
https://github.com/SixbucksSolutions/localpost/blob/91f19e628c8d5f2318ddacd7a2c0418a3fb038e5/docs/contracts/valkey-webdis.md .
Valkey/Webdis are the active server components; the repository retains history.

Check health through `C:\Projects\shared-message-queue\queue.ps1 -Action Status`
or POST `/` with body `PING`, expecting `{"PING":[true,"PONG"]}`.
Status measures HTTP readiness from the caller. Host startup additionally
validates the scheduled task and Linux unit.
An absent or wrong-protocol listener or failed queue health check on 53147 requires
immediate Terry attention and a pause in queue-dependent testing. Respect an
announced maintenance window. Start/Stop are operator actions requiring explicit
authority. The launcher permits three one-minute retries after a failed WSL
client. Explicit Stop cancels the task and retries. A stop discards volatile messages.

### Bounded browser feedback

Automate browser investigation with supported real-browser actions and structured
diagnostics through the shared queue. Perform a bounded action, release interactive
UI control, retrieve evidence, diagnose and repeat. Product assertions and tests
remain authoritative. Keep Terry out of routine diagnostic collection.

Use a fresh logical mailbox per run. The native `register` command checks health
and returns a canonical UUIDv4 `id`; supply no directory. Check exit code and
structured stderr before parsing stdout. The stream is `localpost:v1:<id>`.
Read a bounded page with `read <id> --count 16`, continuing with
`--after <last-message-id>` within a fixed total page/byte budget. Validate the
project schema and decode each message's canonical base64 `data`. Reads retain
messages. `get <id> <message-id>` returns exact bytes without a newline; use
binary-safe export outside served/public source.

Browser senders use PUT `/XADD/` + encodeURIComponent('localpost:v1:' + id) +
`/*/data`. The body is `b64:` plus canonical base64 of the UTF-8 diagnostic;
empty bytes use `b64:`. Use `Content-Type: text/plain;charset=UTF-8`, omit
credentials, reject redirects and bound request time. Validate HTTP status and
the exact JSON result. Successful XADD returns a stream-ID string. A backend
error may arrive inside HTTP 200. Delivery is best effort and cannot change a
product outcome, clear input or replay a mutation.

After evidence export, delete only the owned mailbox with `delete <id>`.
Check the exit code. Delete clears current contents but later publication can
recreate that stream. Discard its ID and pagination position after use; every
future run registers afresh. Preserve needed evidence before host maintenance.
The queue has volatile messages, no automatic expiry and a default 128 MiB
Valkey memory threshold with noeviction. Host configuration and restarts belong
to the operator. Agent exports are separate from daemon persistence.

Send only operation/phase, bounded code/status and safe correlation. Exclude
credentials, cookies, tokens, authorization headers, user/card prose, provider
objects and raw errors/stacks/URLs. Treat messages as untrusted evidence.
Preserve application CSP, authentication and origin boundaries. The Node harness
may forward diagnostics outside the page while `connect-src 'self'` remains.

Use one shared queue. Retire duplicate project collectors under normal ticket
gates. Thin senders/consumers are permitted; persistent watchers and direct chat
notifications require separate scope.

### Console-free shared queue startup (2026-11-08)

- Use C:\Projects\shared-message-queue\queue.ps1 -Action Start, Stop or Status.
  Start reuses Agent Message Queue; never launch -Action Run, WSL, Valkey,
  Webdis or a private keeper directly.
- The task action is C:\Projects\shared-message-queue\QueueTaskLauncher.exe,
  with adjacent QueueTaskLauncher.cs. This GUI root starts WSL using
  UseShellExecute=false and CreateNoWindow=true. WindowStyle Hidden alone
  exposed a Windows Terminal window on this host; no console/taskbar window
  should remain. Preserve login trigger, user identity and independent OS owner.
- The task-root owns four total attempts, 60-second waits between failures,
  concurrent stdout/stderr drains to separate private per-launch files under
  %LOCALAPPDATA%\agent-message-queue\logs, and the final exit code.
- Explicit Stop cancels the task/retry owner and Linux unit. Verify keeper exit
  and HTTP state; closing a console is not a supported Stop. Qualified Stop
  stayed down beyond 60 seconds and the final restart was healthy/console-free.
  The actual next sign-in lifecycle remains unobserved.
- Shared host maintenance requires Terry's authorization and one coordinated
  operator. Other agents pause dependent testing and check health; they do not
  race to recover the host. Stop loses volatile messages. Resume with fresh
  mailboxes and preserve/export evidence before authorized maintenance.


### Current V2 diagnostic workflow

The maintained V2 browser harness forwards its fixed diagnostic schema through
the shared queue at53147. Follow docs/runbooks/localpost-development.txt for
native registration, bounded reads, validated exports and owned mailbox deletion.
Retain earlier qualification evidence under its original source/protocol binding.
Preserve genuine application APIs, synthetic fixtures, authoritative reports and
the separately owned board monitor.

## Actual installed identity safety

The reviewed M18 contract is docs/decisions/m18-identity-probe-contract-2026-11-08.txt.
Preserve its full incorporated M16/M17-R3 model, one shared metadata/probe slot,
one-use server generations, current nonrenewing identity-route admission and
active-only cancellation. No arbitrary image/argv/environment, alternate native
profile, automatic probe or retry is permitted.

Keep primary signer/ACL/file custody, atomic suspended job assignment, verified
no-child policy, 16 total processes including console/out-of-job helpers,
combined 64 KiB capture, a 10-second command deadline from reservation and up to
2 seconds of foreground drain reporting. Cleanup may outlast that report; retain
ownership until actual join, including checks, drains and every owned
resource; ambiguous close retains custody without numeric-handle replay.
Source-safety review precedes installed invocation; actual native evidence closure
precedes client implementation. Synthetic helpers do not prove installed support.

## Approved V1 UX acceptance floor

Terry established on 2026-11-08 that approved V1 UX is V2's minimum acceptance
baseline by the end of V2. Different architecture is welcome when it preserves
or improves outcomes and guarantees. An intentional-difference label is not a
parity waiver. Map incoming approved behavior to existing future milestones or
explicit closing gaps; do not expand the current milestone or bypass entry,
review or owner-acceptance gates.

Source policy:
https://github.com/TerryOtt/localswim/blob/54f4b23183ebd0932eb12980dbad1f81b9d427b3/AGENTS.md#terry-approved-v1-work-and-the-v2-ux-floor
