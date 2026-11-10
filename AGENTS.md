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
