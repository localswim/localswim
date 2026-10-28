// Synthetic browser boundary tests; native host/UI evidence is separate.
import type { WorkspaceCatalog, WorkspaceProjects } from "../../generated/transport/contracts.js";
import type { WorkspaceSessionAccess } from "./workspace-session.js";
import { createWorkspaceCatalogClient } from "./catalog.js";

function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
async function refuses(action: () => Promise<unknown>): Promise<void> { let failed = false; try { await action(); } catch { failed = true; } assert(failed, "Unsafe catalog accepted"); }
function reply(value: unknown, options?: ResponseInit): Response {
  return new Response(typeof value === "string" ? value : JSON.stringify(value), { headers: { "Cache-Control": "no-store" }, ...options });
}
export function catalogFixture(): { catalog: WorkspaceCatalog; projects: WorkspaceProjects } {
  const workspaceId = "10000000-0000-4000-8000-000000000001";
  return { projects: { apiVersion: 1, workspaceId, configRevision: 1, projects: [{ key: "harbor", label: "Harbor" }, { key: "empty", label: "Empty" }] },
    catalog: { apiVersion: 1, workspaceId, configRevision: 1, observedAt: "2026-10-28T00:00:00Z", entries: [{ projectKey: "harbor", boardKey: "dispatch", projectLabel: "Harbor", label: "Dispatch",
      identity: { installationId: "20000000-0000-4000-8000-000000000001", boardId: "30000000-0000-4000-8000-000000000001", incarnation: "40000000-0000-4000-8000-000000000001" },
      memberEpoch: "50000000-0000-4000-8000-000000000001", availability: "ready", freshness: "fresh", ageSeconds: 0, lastObservedAt: "2026-10-28T00:00:00Z", boardRevision: 4,
      activity: { state: "known", lastCommittedAt: "2026-10-29T00:00:00Z", coverage: { historyFloor: null, activityFloor: null, outcomePayloadFloor: null, gaps: [] } },
      attention: { readyForReview: { state: "known", value: 0 }, blocked: { state: "not_configured", value: null }, needsTerry: { state: "not_configured", value: null } } }] } };
}
export function fakeWorkspaceAccess(workspaceId: string): WorkspaceSessionAccess & { clearCount(): number } {
  let clears = 0;
  const unavailable = new Set<() => void>(), connected = new Set<() => void>();
  return { initial: { apiVersion: 1, workspaceId, epoch: "60000000-0000-4000-8000-000000000001", mode: "human" },
    async resolve() { throw Error("No resolve in metadata tests"); }, async openBoard() { throw Error("No board bootstrap in metadata tests"); },
    clear() { clears++; for (const callback of unavailable) callback(); }, clearCount() { return clears; },
    async reconnect() { this.clear(); for (const callback of connected) callback(); return this.initial; },
    onUnavailable(callback) { unavailable.add(callback); return () => { unavailable.delete(callback); }; },
    onConnected(callback) { connected.add(callback); return () => { connected.delete(callback); }; },
  };
}

export async function runCatalogTransportTests(): Promise<number> {
  let passed = 0;
  const fixture = catalogFixture(), access = fakeWorkspaceAccess(fixture.catalog.workspaceId);
  const requests: { path: string; init: RequestInit }[] = [];
  let catalog: unknown = fixture.catalog, projects: unknown = fixture.projects;
  let special: (() => Promise<Response>) | undefined;
  const fetcher: typeof fetch = async (input, init = {}) => {
    requests.push({ path: String(input), init });
    if (String(input).endsWith("/projects")) return reply(projects);
    if (special) return special();
    return reply(catalog);
  };
  const client = createWorkspaceCatalogClient(access, fetcher);
  try {
    const valid = await client.read();
    assert(valid.catalog.entries[0]!.attention.readyForReview.value === 0 && valid.projects.projects.length === 2, "Known zero or empty project lost"); passed++;
    assert(requests.every(({ path, init }) => path.startsWith("/api/v1/workspace/") && init.method === "GET" && init.credentials === "same-origin" && init.cache === "no-store" && init.redirect === "error" && new Headers(init.headers).get("X-Localswim-Client") === "browser" && !new Headers(init.headers).has("Authorization") && !init.body), "Catalog channel changed"); passed++;
    for (const value of [ { ...fixture.catalog, extra: "forbidden" }, { ...fixture.catalog, entries: [{ ...fixture.catalog.entries[0], attention: { ...fixture.catalog.entries[0]!.attention, readyForReview: { state: "unknown", value: 0 } } }] },
      '{"apiVersion":1,"apiVersion":1}', { ...fixture.catalog, entries: [{ ...fixture.catalog.entries[0], freshness: "never" }] } ]) {
      catalog = value; await refuses(() => client.read()); passed++;
    }
    catalog = fixture.catalog;
    projects = { ...fixture.projects, projects: [{ key: "empty", label: "Empty", root: "private" }] };
    await refuses(() => client.read()); passed++; projects = fixture.projects;
    special = async () => reply(fixture.catalog, { headers: {} }); await refuses(() => client.read()); passed++;
    special = async () => reply(fixture.catalog, { headers: { "Cache-Control": "no-store", "Content-Length": "2097153" } }); await refuses(() => client.read()); passed++;
    special = async () => new Response(new ReadableStream<Uint8Array>({ start(controller) {
      controller.enqueue(new Uint8Array(1024 * 1024)); controller.enqueue(new Uint8Array(1024 * 1024 + 1)); controller.close();
    } }), { headers: { "Cache-Control": "no-store" } }); await refuses(() => client.read()); passed++;
    special = async () => new Response(new Uint8Array([0xff]), { headers: { "Cache-Control": "no-store" } }); await refuses(() => client.read()); passed++;
    special = undefined;
    catalog = { ...fixture.catalog, workspaceId: "70000000-0000-4000-8000-000000000001" };
    const beforeWrongIdentity = access.clearCount(); await refuses(() => client.read());
    assert(access.clearCount() === beforeWrongIdentity + 1, "Workspace replacement retained metadata authority"); passed++;
    await access.reconnect(); catalog = fixture.catalog;

    let releaseBody: (() => void) | undefined;
    let started: (() => void) | undefined;
    const held = new Promise<void>(resolve => { started = resolve; });
    special = async () => { started!(); return new Response(new ReadableStream<Uint8Array>({ start(controller) {
      releaseBody = () => { controller.enqueue(new TextEncoder().encode(JSON.stringify(fixture.catalog))); controller.close(); };
    } }), { headers: { "Cache-Control": "no-store" } }); };
    const oldBody = client.read(), oldResult = oldBody.then(() => false, () => true);
    assert(client.read() === oldBody, "Concurrent metadata reads were not coalesced");
    await held; await access.reconnect(); releaseBody!();
    assert(await oldResult, "Late body crossed workspace generation"); passed++;

    let releaseDenial: (() => void) | undefined;
    const denialStarted = new Promise<void>(resolve => { started = resolve; });
    special = async () => { started!(); await new Promise<void>(resolve => { releaseDenial = resolve; }); return reply({}, { status: 401 }); };
    const denied = client.read().then(() => false, () => true); await denialStarted; await access.reconnect();
    const beforeLateDenial = access.clearCount(); releaseDenial!();
    assert(await denied && access.clearCount() === beforeLateDenial, "Old denial cleared renewed metadata"); passed++;
    special = async () => reply({}, { status: 403 }); const beforeDenial = access.clearCount();
    await refuses(() => client.read()); assert(access.clearCount() === beforeDenial + 1, "Current auth loss retained metadata"); passed++;
    await access.reconnect(); special = undefined;
    assert((await client.read()).catalog.workspaceId === fixture.catalog.workspaceId, "Deliberate reconnect failed"); passed++;
  } finally { client.dispose(); }
  const stopped = requests.length; await refuses(() => client.read());
  assert(requests.length === stopped, "Disposed catalog reused authority"); passed++;
  return passed;
}
