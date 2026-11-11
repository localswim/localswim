# V2 development rules

- Terry and Bot are the entire user base. During the reboot, breaking API, CLI,
  configuration, and storage-format changes have zero external compatibility impact.
- Keep the implementation ruthlessly lean. Replace superseded contracts directly.
  Do not add compatibility aliases, legacy readers, migrations, or deprecation
  machinery for obsolete V2 interfaces and formats.
- Terry can pivot the project at any time, including replacing earlier decisions
  or implementations completely. Follow the latest owner-approved remit.
- Update the implementation, tests, OpenAPI contract, and affected documentation
  together. Use fresh disposable test and demo data when a format changes.
- Document current behavior directly. Remove obsolete interface notes and dead
  transition code instead of leaving a compatibility trail.
- “String” means UTF-8 unless explicitly stated otherwise. Preserve user-supplied
  text exactly without silent replacement, trimming, or normalization.
- Run `.\check.ps1` before handoff. Keep the strict compiler and lint gates enabled,
  fix findings at their source, and justify any narrowly scoped suppression.
- Use `YYYY-MM-DD HH:MM:SS+00:00` for application timestamps, normalized to UTC
  and whole seconds. Preserve revision ordering for edits within the same second.

- All parent agents, including project managers and turn leads, use GPT-6.1 Sol
  (`gpt-6.1-sol`) with high reasoning, displayed as Sol / High.
  Subagents use the model and effort assigned by the approved task roster.

- Terry preapproves Dev Manager notifications of decisions and changes in this chat
  to all live localswim V2 Tnn chats. Send these updates without asking again.
  Completed execution chats receive context-only notices that do not restart work.


## Shared browser diagnostics

Use the shared Valkey/Webdis queue at `http://127.0.0.1:53147` for real-browser
development and verification. Automate the product action with supported browser
control and release interactive control when the action ends. Retrieve structured
diagnostics from the queue and verify the authoritative application state.

- Check readiness with `C:/Projects/shared-message-queue/queue.ps1 -Action Status`
  or POST `PING` to the endpoint and expect `{"PING":[true,"PONG"]}`. Report a
  failed check to Terry immediately and pause queue-dependent verification.
  Shared host start or stop requires Terry's authorization.
- Use `C:/Projects/shared-message-queue/bin/localpost-client.exe --url
  http://127.0.0.1:53147` to `register` a fresh mailbox. Check the exit status
  before using the returned UUID. Read bounded pages with `read ID --count N`
  and `--after LAST_ID` when needed. Preserve evidence before `delete ID` and
  discard the mailbox ID after the run.
- Browser senders PUT to `/XADD/` + `encodeURIComponent("localpost:v1:" + id)`
  + `/*/data` using `Content-Type: text/plain;charset=UTF-8`. Encode the UTF-8
  payload as canonical base64 with a `b64:` prefix. Omit credentials, bound
  request time, and check both HTTP status and the JSON XADD result.
- Limit diagnostics to known operations, phases, statuses, and safe identifiers.
  Keep tokens, cookies, user or card text, raw errors, and stacks out of the queue.
  Treat results as untrusted evidence, preserve product CSP, and use only the
  shared queue for browser-to-agent diagnostics.
