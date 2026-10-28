import type { WorkspaceCatalog, WorkspaceProjects, WorkspaceCatalogEntry, CatalogCount } from "../../generated/transport/contracts.js";
import type { CatalogSnapshot, WorkspaceCatalogClient } from "../api/catalog.js";

export type WorkspaceCatalogState = Readonly<{
  phase: "loading" | "ready" | "stale" | "unavailable";
  catalog: WorkspaceCatalog | null;
  projects: WorkspaceProjects | null;
  receivedAt: number | null;
  refreshing: boolean;
}>;

export type CatalogRefreshEnvironment = Readonly<{
  visible(): boolean;
  now(): number;
  every(callback: () => void, milliseconds: number): () => void;
  onFocus(callback: () => void): () => void;
  onVisibility(callback: () => void): () => void;
}>;
const browserEnvironment: CatalogRefreshEnvironment = {
  visible: () => document.visibilityState === "visible", now: () => Date.now(),
  every(callback, milliseconds) { const timer = setInterval(callback, milliseconds); return () => clearInterval(timer); },
  onFocus(callback) { window.addEventListener("focus", callback); return () => window.removeEventListener("focus", callback); },
  onVisibility(callback) { document.addEventListener("visibilitychange", callback); return () => document.removeEventListener("visibilitychange", callback); },
};

// One window-wide pipeline covers projects and catalog. A trigger during a
// request queues at most one follow-up, never abort/restart overlap.
export class WorkspaceCatalogRefresh {
  private generation = 0;
  private started = false;
  private disposed = false;
  private connected = true;
  private queued = false;
  private running: Promise<void> | undefined;
  private renewing: Promise<void> | undefined;
  private remove: (() => void)[] = [];
  private trusted: CatalogSnapshot | undefined;
  private receivedAt: number | null = null;
  private state: WorkspaceCatalogState = { phase: "loading", catalog: null, projects: null, receivedAt: null, refreshing: false };

  constructor(private readonly client: WorkspaceCatalogClient, private readonly options: {
    onChange(snapshot: WorkspaceCatalogState): void; environment?: CatalogRefreshEnvironment;
    refreshPreferences?(): Promise<void>;
  }) {}

  private get env(): CatalogRefreshEnvironment { return this.options.environment ?? browserEnvironment; }
  get snapshot(): WorkspaceCatalogState { return structuredClone(this.state); }
  private publish(next: WorkspaceCatalogState): void {
    this.state = next;
    try { this.options.onChange(this.snapshot); } catch { /* A view cannot alter the polling lifecycle. */ }
  }
  start(): void {
    if (this.started || this.disposed) return;
    this.started = true;
    this.remove.push(this.client.onUnavailable(() => this.invalidate()), this.client.onConnected(() => {
      if (this.disposed) return;
      this.generation++; this.connected = true; this.trigger();
    }), this.env.every(() => { if (this.env.visible()) this.trigger(); }, 5_000),
    this.env.onFocus(() => { if (this.env.visible()) this.trigger(); }),
    this.env.onVisibility(() => { if (this.env.visible()) this.trigger(); }));
    this.publish(this.state);
    if (this.env.visible()) this.trigger();
  }
  trigger(): void {
    if (!this.started || this.disposed || !this.connected) return;
    this.queued = true;
    this.drain();
  }
  private drain(): void {
    if (this.running || this.renewing || !this.queued || !this.connected || this.disposed || !this.env.visible()) return;
    this.queued = false;
    const generation = this.generation;
    this.publish({ ...this.state, refreshing: true });
    const work = async (): Promise<void> => {
      try {
        const next = await this.client.read();
        if (this.disposed || !this.connected || generation !== this.generation) return;
        // Replace the entire projection, including unknown/revoked/replaced
        // rows. Prior successful facts never patch holes in a new response.
        this.trusted = structuredClone(next); this.receivedAt = this.env.now();
        this.publish({ phase: "ready", catalog: next.catalog, projects: next.projects, receivedAt: this.receivedAt, refreshing: false });
      } catch {
        if (this.disposed || !this.connected || generation !== this.generation) return;
        this.publish({ phase: this.trusted ? "stale" : "unavailable", catalog: this.trusted ? staleCatalog(this.trusted.catalog, this.receivedAt!, this.env.now()) : null,
          projects: this.trusted?.projects ?? null, receivedAt: this.receivedAt, refreshing: false });
      }
      // Preferences share these triggers and this single pipeline, while their
      // failure cannot hide a successful catalog or impair the active editor.
      if (!this.disposed && this.connected && generation === this.generation) await this.options.refreshPreferences?.().catch(() => {});
    };
    this.running = work().finally(() => { this.running = undefined; this.drain(); });
  }
  invalidate(): void {
    if (this.disposed) return;
    this.generation++; this.connected = false; this.queued = false;
    this.trusted = undefined; this.receivedAt = null;
    this.publish({ phase: "unavailable", catalog: null, projects: null, receivedAt: null, refreshing: false });
  }
  reconnect(): Promise<void> {
    if (this.disposed) return Promise.reject(Error("Workspace catalog unavailable"));
    if (this.renewing) return this.renewing;
    this.invalidate();
    const reconnect = async (): Promise<void> => {
      await this.running;
      if (this.disposed) return;
      await this.client.reconnect();
      if (this.disposed) return;
      this.connected = true; this.queued = true;
    };
    this.renewing = reconnect().finally(() => { this.renewing = undefined; this.drain(); });
    return this.renewing;
  }
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true; this.generation++; this.queued = false;
    for (const remove of this.remove) remove(); this.remove = [];
    this.trusted = undefined; this.receivedAt = null;
    this.state = { phase: "unavailable", catalog: null, projects: null, receivedAt: null, refreshing: false };
    this.client.dispose();
  }
}

function staleCount(count: CatalogCount): CatalogCount { return count.state === "known" ? { state: "stale", value: count.value } : count; }
function staleEntry(entry: WorkspaceCatalogEntry, elapsed: number): WorkspaceCatalogEntry {
  return { ...entry, freshness: entry.freshness === "never" ? "never" : "stale",
    ageSeconds: entry.ageSeconds === null ? null : entry.ageSeconds + elapsed,
    activity: entry.activity.state === "known" ? { ...entry.activity, state: "stale" } : entry.activity,
    attention: { readyForReview: staleCount(entry.attention.readyForReview), blocked: staleCount(entry.attention.blocked), needsTerry: staleCount(entry.attention.needsTerry) } };
}
function staleCatalog(catalog: WorkspaceCatalog, receivedAt: number, now: number): WorkspaceCatalog {
  const elapsed = Math.max(0, Math.floor((now - receivedAt) / 1_000));
  return { ...catalog, entries: catalog.entries.map(entry => staleEntry(entry, elapsed)) };
}
