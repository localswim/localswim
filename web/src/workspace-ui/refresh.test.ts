// Deterministic visible-window lifecycle tests; not native host qualification.
import type { CatalogSnapshot, WorkspaceCatalogClient } from "../api/catalog.js";
import { catalogFixture } from "../api/catalog.test.js";
import { WorkspaceCatalogRefresh } from "./refresh.js";
import type { CatalogRefreshEnvironment, WorkspaceCatalogState } from "./refresh.js";

function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
async function settle(): Promise<void> { for (let i = 0; i < 12; i++) await Promise.resolve(); }

export async function runCatalogRefreshTests(): Promise<number> {
  let passed = 0, visible = true, now = Date.parse("2026-10-28T00:00:00Z"), active = 0, maximum = 0, reads = 0, disposed = false;
  let timer = (): void => {}, focus = (): void => {}, visibility = (): void => {};
  let resolveRead: ((value: CatalogSnapshot) => void) | undefined, rejectRead: (() => void) | undefined;
  const unavailable = new Set<() => void>(), connected = new Set<() => void>();
  const environment: CatalogRefreshEnvironment = { visible: () => visible, now: () => now,
    every(callback, milliseconds) { assert(milliseconds === 5_000, "Wrong polling interval"); timer = callback; return () => { timer = () => {}; }; },
    onFocus(callback) { focus = callback; return () => { focus = () => {}; }; }, onVisibility(callback) { visibility = callback; return () => { visibility = () => {}; }; } };
  const client: WorkspaceCatalogClient = {
    read() { reads++; active++; maximum = Math.max(maximum, active); return new Promise<CatalogSnapshot>((resolve, reject) => {
      resolveRead = value => { active--; resolve(structuredClone(value)); }; rejectRead = () => { active--; reject(Error("Unavailable")); };
    }); },
    async reconnect() { for (const callback of unavailable) callback(); for (const callback of connected) callback(); },
    onUnavailable(callback) { unavailable.add(callback); return () => { unavailable.delete(callback); }; },
    onConnected(callback) { connected.add(callback); return () => { connected.delete(callback); }; },
    dispose() { disposed = true; },
  };
  const states: WorkspaceCatalogState[] = [], fixture = catalogFixture();
  const refresh = new WorkspaceCatalogRefresh(client, { onChange: state => states.push(state), environment });
  refresh.start(); assert(reads === 1 && refresh.snapshot.refreshing, "Visible startup did not poll"); passed++;
  timer(); focus(); refresh.trigger(); refresh.trigger();
  assert(reads === 1 && maximum === 1, "Triggers overlapped initial request");
  resolveRead!(fixture); await settle(); assert(Number(reads) === 2 && maximum === 1, "Triggers did not coalesce one follow-up"); passed++;
  resolveRead!(fixture); await settle();
  // A trigger queued while visible must not turn into a background request
  // after the window becomes hidden while the first response is pending.
  refresh.trigger(); refresh.trigger(); const beforeHiddenSettlement = reads;
  visible = false; visibility(); resolveRead!(fixture); await settle();
  assert(reads === beforeHiddenSettlement && active === 0, "Queued follow-up launched after the window became hidden");
  visible = true; visibility(); assert(reads === beforeHiddenSettlement + 1 && Number(active) === 1, "Visible transition lost the deferred follow-up");
  resolveRead!(fixture); await settle(); passed++;
  visible = false; const beforeHidden = reads; timer(); focus(); visibility(); await settle(); assert(reads === beforeHidden, "Hidden window polled"); passed++;
  visible = true; visibility(); assert(reads === beforeHidden + 1, "Visible transition did not refresh");
  resolveRead!(fixture); await settle(); focus(); assert(reads === beforeHidden + 2, "Focus did not refresh"); passed++;
  resolveRead!(fixture); await settle();
  now += 11_000; refresh.trigger(); rejectRead!(); await settle();
  const stale = refresh.snapshot;
  assert(stale.phase === "stale" && stale.catalog!.entries[0]!.freshness === "stale" && stale.catalog!.entries[0]!.ageSeconds === 11 && stale.catalog!.entries[0]!.attention.readyForReview.state === "stale" && stale.catalog!.entries[0]!.attention.readyForReview.value === 0 && stale.catalog!.entries[0]!.activity.state === "stale", "Failure lost trusted rows or fabricated freshness"); passed++;
  assert(stale.catalog!.entries[0]!.activity.lastCommittedAt === "2026-10-29T00:00:00Z" && stale.catalog!.entries[0]!.attention.blocked.value === null, "Stale conversion changed future activity or unsupported count"); passed++;
  const unknown: CatalogSnapshot = { ...fixture, catalog: { ...fixture.catalog, entries: [{ ...fixture.catalog.entries[0]!, availability: "unavailable", freshness: "never", ageSeconds: null,
    identity: null, memberEpoch: null, lastObservedAt: null, boardRevision: null, activity: { state: "unknown", lastCommittedAt: null, coverage: null },
    attention: { readyForReview: { state: "unknown", value: null }, blocked: { state: "not_configured", value: null }, needsTerry: { state: "not_configured", value: null } } }] } };
  refresh.trigger(); resolveRead!(unknown); await settle(); refresh.trigger(); rejectRead!(); await settle();
  assert(refresh.snapshot.catalog!.entries[0]!.identity === null && refresh.snapshot.catalog!.entries[0]!.attention.readyForReview.state === "unknown", "Unknown response resurrected prior identity/count"); passed++;
  refresh.trigger(); for (const callback of unavailable) callback(); resolveRead!(fixture); await settle();
  assert(refresh.snapshot.phase === "unavailable" && refresh.snapshot.catalog === null && refresh.snapshot.projects === null, "Late response repainted after authority loss");
  const readsAtLoss = reads; timer(); focus(); refresh.trigger(); assert(reads === readsAtLoss, "Auth loss automatically reused metadata"); passed++;
  for (const callback of connected) callback(); assert(reads === readsAtLoss + 1, "Validated reconnect did not resume polling");
  resolveRead!(fixture); await settle(); passed++;

  // Reconnect waits for the previous request rather than aborting and replacing
  // it; the late success is fenced and only the new generation can publish.
  refresh.trigger(); const readsBeforeReconnect = reads; const reconnecting = refresh.reconnect();
  await settle(); assert(reads === readsBeforeReconnect && Number(active) === 1 && refresh.snapshot.catalog === null, "Reconnect overlapped a pending read");
  resolveRead!(fixture); await reconnecting; await settle();
  assert(reads === readsBeforeReconnect + 1 && maximum === 1, "Reconnect did not serialize replacement poll"); resolveRead!(fixture); await settle(); passed++;
  refresh.trigger(); const countBeforeDispose = states.length; refresh.dispose(); resolveRead!(unknown); await settle(); timer(); focus(); visibility(); refresh.trigger();
  assert(disposed && states.length === countBeforeDispose && maximum === 1, "Disposed refresh published or retained listeners"); passed++;
  return passed;
}
