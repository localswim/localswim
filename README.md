# localswim

## V2 release direction

- **Same product.** V1 is the reference for the UI, UX, and functionality.
  Build V2 fresh and match the browser, CLI, and integration behavior people
  rely on, including permissions, audit history, and reliable saves and recovery.
- **One port.** One loopback service serves the UI, workspace, and every board.
  The browser and CLI share a date-versioned REST API with flat resource URLs,
  for example `/api/2026-11-10/cards/{cardId}`. Each
  board has one authoritative writer. No per-board ports or forwarding gateway.
- **Fresh starts.** Run V1 and V2 side by side on separate ports with separate
  data directories. Existing projects stay in V1. New projects start in V2 when
  it is ready. No data migration or cutover.
- **Ship quickly.** Aim for a working board on day one and the three-day delivery target
  in the execution plan. Create a board, add and move cards, and restart with
  the changes intact. Check parity as we build. Focus testing on failures
  that can lose edits, break approvals, or prevent recovery.

This direction supersedes the previous milestone plan, its broad baseline
campaign, and the import and cutover proposal.

[Execution plan](execution-plan.md) · [API contract](docs/openapi.yaml) · [Decision record](docs/adr/0001-api-contract.md)
