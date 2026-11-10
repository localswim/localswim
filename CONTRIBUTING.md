# Contributing

## Product remit

V1 is the reference for V2's UI, UX, and functionality. Preserve the browser,
CLI, permission, audit, save, and recovery behavior people rely on.

Use one loopback service and port for the UI, workspace, and every board. Keep
V1 and V2 independent, with fresh V2 data. Build quickly with a lean implementation.

## Conventions

Use snake_case for JSON members, path parameter identifiers, and query parameters.
Standard HTTP headers retain their defined spelling. Go and JavaScript identifiers
follow their language conventions.

Application timestamps use `YYYY-MM-DD HH:MM:SS+00:00`, normalized to UTC and whole
seconds. Revisions distinguish edits within the same second. Native snapshots use
`format_version: 2`.

String inputs mean UTF-8. Preserve user text exactly, including whitespace and
Unicode. Entity and build IDs are immutable UUIDv4 values. The service_instance_id
uses UUIDv7, with the instance creation time encoded as Unix milliseconds.
Publish UUIDs in lowercase. Accepted UUID input may use either hex case.

The reboot has only Terry and Bot as users. Keep one current API, CLI, and storage
contract. Follow the current owner-approved direction and the rules in [AGENTS.md](AGENTS.md).

## Checks

Install the pinned development compiler.

```powershell
npm --prefix web ci --ignore-scripts
```

Run the complete quality gate before handoff.

```powershell
.\check.ps1
```

It requires clean Go formatting, go vet, Go tests, strict TypeScript checking of
the browser source, browser-client tests, and clean Git whitespace. Fix findings
at their source and justify any narrow suppression.

## Data and build staging

Native board snapshots live in the data directory's `boards/` folder. Verification
and export use the same durable format. Workspace pin order has its own revision,
starting at 1.

For local build staging, place `candidate.json` and the native `candidate.exe`
(Windows) or `candidate` (Unix) in the data directory. The manifest contains
`version` and lowercase SHA-256 `sha256`. The `/service` response identifies the
running service instance, and `/builds` reports staged validation. Activation is a
separate milestone.

## References

- [API contract](docs/openapi.yaml)
- [API decisions](docs/adr/0001-api-contract.md)
- [Milestones](milestone-plan.md)
- [Execution plan](execution-plan.md)
- [Browser client](web/README.md)
