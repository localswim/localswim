const browserEnvironment = {
    visible: () => document.visibilityState === "visible", now: () => Date.now(),
    every(callback, milliseconds) { const timer = setInterval(callback, milliseconds); return () => clearInterval(timer); },
    onFocus(callback) { window.addEventListener("focus", callback); return () => window.removeEventListener("focus", callback); },
    onVisibility(callback) { document.addEventListener("visibilitychange", callback); return () => document.removeEventListener("visibilitychange", callback); },
};
// One window-wide pipeline covers projects and catalog. A trigger during a
// request queues at most one follow-up, never abort/restart overlap.
export class WorkspaceCatalogRefresh {
    client;
    options;
    generation = 0;
    started = false;
    disposed = false;
    connected = true;
    queued = false;
    running;
    renewing;
    remove = [];
    trusted;
    receivedAt = null;
    state = { phase: "loading", catalog: null, projects: null, receivedAt: null, refreshing: false };
    constructor(client, options) {
        this.client = client;
        this.options = options;
    }
    get env() { return this.options.environment ?? browserEnvironment; }
    get snapshot() { return structuredClone(this.state); }
    publish(next) {
        this.state = next;
        try {
            this.options.onChange(this.snapshot);
        }
        catch { /* A view cannot alter the polling lifecycle. */ }
    }
    start() {
        if (this.started || this.disposed)
            return;
        this.started = true;
        this.remove.push(this.client.onUnavailable(() => this.invalidate()), this.client.onConnected(() => {
            if (this.disposed)
                return;
            this.generation++;
            this.connected = true;
            this.trigger();
        }), this.env.every(() => { if (this.env.visible())
            this.trigger(); }, 5_000), this.env.onFocus(() => { if (this.env.visible())
            this.trigger(); }), this.env.onVisibility(() => { if (this.env.visible())
            this.trigger(); }));
        this.publish(this.state);
        if (this.env.visible())
            this.trigger();
    }
    trigger() {
        if (!this.started || this.disposed || !this.connected)
            return;
        this.queued = true;
        this.drain();
    }
    drain() {
        if (this.running || this.renewing || !this.queued || !this.connected || this.disposed || !this.env.visible())
            return;
        this.queued = false;
        const generation = this.generation;
        this.publish({ ...this.state, refreshing: true });
        const work = async () => {
            try {
                const next = await this.client.read();
                if (this.disposed || !this.connected || generation !== this.generation)
                    return;
                // Replace the entire projection, including unknown/revoked/replaced
                // rows. Prior successful facts never patch holes in a new response.
                this.trusted = structuredClone(next);
                this.receivedAt = this.env.now();
                this.publish({ phase: "ready", catalog: next.catalog, projects: next.projects, receivedAt: this.receivedAt, refreshing: false });
            }
            catch {
                if (this.disposed || !this.connected || generation !== this.generation)
                    return;
                this.publish({ phase: this.trusted ? "stale" : "unavailable", catalog: this.trusted ? staleCatalog(this.trusted.catalog, this.receivedAt, this.env.now()) : null,
                    projects: this.trusted?.projects ?? null, receivedAt: this.receivedAt, refreshing: false });
            }
            // Preferences share these triggers and this single pipeline, while their
            // failure cannot hide a successful catalog or impair the active editor.
            if (!this.disposed && this.connected && generation === this.generation)
                await this.options.refreshPreferences?.().catch(() => { });
        };
        this.running = work().finally(() => { this.running = undefined; this.drain(); });
    }
    invalidate() {
        if (this.disposed)
            return;
        this.generation++;
        this.connected = false;
        this.queued = false;
        this.trusted = undefined;
        this.receivedAt = null;
        this.publish({ phase: "unavailable", catalog: null, projects: null, receivedAt: null, refreshing: false });
    }
    reconnect() {
        if (this.disposed)
            return Promise.reject(Error("Workspace catalog unavailable"));
        if (this.renewing)
            return this.renewing;
        this.invalidate();
        const reconnect = async () => {
            await this.running;
            if (this.disposed)
                return;
            await this.client.reconnect();
            if (this.disposed)
                return;
            this.connected = true;
            this.queued = true;
        };
        this.renewing = reconnect().finally(() => { this.renewing = undefined; this.drain(); });
        return this.renewing;
    }
    dispose() {
        if (this.disposed)
            return;
        this.disposed = true;
        this.generation++;
        this.queued = false;
        for (const remove of this.remove)
            remove();
        this.remove = [];
        this.trusted = undefined;
        this.receivedAt = null;
        this.state = { phase: "unavailable", catalog: null, projects: null, receivedAt: null, refreshing: false };
        this.client.dispose();
    }
}
function staleCount(count) { return count.state === "known" ? { state: "stale", value: count.value } : count; }
function staleEntry(entry, elapsed) {
    return { ...entry, freshness: entry.freshness === "never" ? "never" : "stale",
        ageSeconds: entry.ageSeconds === null ? null : entry.ageSeconds + elapsed,
        activity: entry.activity.state === "known" ? { ...entry.activity, state: "stale" } : entry.activity,
        attention: { readyForReview: staleCount(entry.attention.readyForReview), blocked: staleCount(entry.attention.blocked), needsTerry: staleCount(entry.attention.needsTerry) } };
}
function staleCatalog(catalog, receivedAt, now) {
    const elapsed = Math.max(0, Math.floor((now - receivedAt) / 1_000));
    return { ...catalog, entries: catalog.entries.map(entry => staleEntry(entry, elapsed)) };
}
