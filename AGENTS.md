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
