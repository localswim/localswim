Ordinary workspace shell and catalog refresh

WorkspaceShell renders keyed catalog metadata and the homepage chooser; editorRoot
belongs exclusively to WindowBoardController. Row updates preserve editor DOM and
focused metadata links. Navigation callbacks use the controller's private-input
gate. Server ordering and fact states remain authoritative; no optimistic count or
default board is invented. Layout callbacks persist only bounded window state.

WorkspaceCatalogRefresh publishes one Projects/Catalog pipeline at a time, with
visible five-second polling, focus/retry/commit triggers and queued coalescing.
Full read failure labels prior observations stale; new unknown rows and authority
loss clear unsafe facts. Hidden windows do not drain queued reads. Reconnect and
disposal fence late generations and release owned timers/listeners.

Catalog transport is cookie-only local metadata, validates generated boundaries
and exact workspace, and bounds bodies. It grants no board authority. Healthy
refresh is GET-only; explicit unavailable recovery is coordinated by the app.
Ordinary positive browser proof remains distinct from synthetic source probes and
fault supplements. Pins and full joined qualification remain later work.
