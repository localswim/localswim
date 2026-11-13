# V2 development rules

## V1 drop-in browser replacement

- Terry clarified on 2026-11-13 that the V2 web UI must look and work exactly
  like the fully functional V1 site. V1's observable UI and UX are the specification.
- Build a freshly designed TypeScript frontend against the V2 backend, using the
  known final requirements to reduce technical debt. Preserve V1 layout, styling,
  density, control placement, navigation, drawers, dialogs, keyboard behavior,
  interaction feedback and workflows.
- Functional parity, broad visual similarity, passing tests and documented
  differences do not establish acceptance of a changed UI or interaction.
  Any deliberate user-visible departure requires Terry's explicit approval.
- Verify matching states with the same data and viewport against running V1.
  Include workspace and board entry, ordinary work and failure/recovery states.
  Terry's hands-on review and explicit cutover approval remain required.

## Epic #0514 turn gates

- The Architect chat owns entry gates for T1-T9. The UI Lead owns execution and integration.
- Before opening turn N+1, the Architect independently reviews all work from turn N
  and explicitly authorizes every turn-N card to move to Ready for Review.
- The UI Lead sends the exact candidate commits, scoped changes, checks, technical
  review and Terra evidence for that review. Corrections remain in the current turn.
- T3 entry requires the Architect to verify that the integrated candidate meets
  all applicable standards documented in Git, including the TypeScript quality
  standard and UI source and release standards. Implementation, passing checks
  and independent review evidence must establish compliance before T3 opens.
  Maintain that standard throughout subsequent turns.
- Terry retains control of Completed. Owner demos follow technical handoff.
- The UI Lead owns execution, the Architect owns acceptance, and Terry owns
  production cutover approval. After all T1-T9 cards reach RFR, the Architect
  demos all V1 functionality in the replacement UI. Production cutover requires
  Terry's explicit approval after those demos.

## Implementation conventions

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
- CLI commands that read or write payload files require an explicit file path
  or `-` for stdin/stdout.
  Output files must be new paths, matching attachment and backup downloads.
- CLI operations on board-owned resources require `BOARD_ID` before the resource
  selector. Card selectors accept a UUID or ticket within that board. Each card
  ID belongs to exactly one board. Keep REST routes UUID-addressed.
- Required CLI values, including required modes, are positional. Validate controlled
  choices against explicit enums and show their allowed values in help. Optional
  modifiers use flags. Reject whitespace-only card titles and preserve accepted text.
- Prefer clear short aliases alongside descriptive long options, such as
  `-h`/`--help` and `-f`/`--force`.
- Treat implicit whole-board and query-selected multi-card writes as candidates
  for scope confirmation. Explicit `--lane UUID` selection needs no extra prompt.
  Use judgment for each operation. Where appropriate,
  explain the scope and ask `Continue? [Y/n]`, with `-f`/`--force` as the explicit
  bypass. Explicit card UUID or ticket lists usually need only the normal guards.
- “String” means UTF-8 unless explicitly stated otherwise. Preserve user-supplied
  text exactly without silent replacement, trimming, or normalization.
- Run `.\check.ps1` before handoff. Keep the strict compiler and lint gates enabled,
  fix findings at their source, and justify any narrowly scoped suppression.
- Follow the approved [TypeScript quality standard](docs/typescript-standard.md)
  for compiler options, Oxlint rules, formatting and qualification evidence.
- Follow the approved [UI source and release standards](docs/ui-standards.md)
  for optimized assets, image sizing, debugging artifacts and release verification.
- Use `YYYY-MM-DD HH:MM:SS+00:00` for application timestamps, normalized to UTC
  and whole seconds. Preserve revision ordering for edits within the same second.
- Immutable comments and user records store and expose only `created_at`. Keep `updated_at` on
  mutable objects, including when it currently equals `created_at`.
- Use UUIDv7 for backup, publication, restoration, build activation, audit event,
  approval receipt, and service instance IDs. Keep ordinary entities, comments,
  attachments, builds, and request correlation UUIDv4. Accept mixed-case input of
  the declared version and emit lowercase UUIDs.
- When displaying backup or publication creation time, decode it from the ID
  and format it in UTC whole seconds. Preserve publication `finished_at` and
  audit, approval, and restoration `at` as actual execution times, independent
  of ID generation.

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
