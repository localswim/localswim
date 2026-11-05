import type { WorkspaceCatalog, WorkspaceProjects } from "../../generated/transport/contracts.js";
import type { WorkspaceSessionAccess } from "./workspace-session.js";
import { parse, validate } from "./boundary.js";
import { isLocalOrigin } from "./client.js";
import { validateAttentionAssignments } from "./workflow.js";

export type CatalogSnapshot = Readonly<{ catalog: WorkspaceCatalog; projects: WorkspaceProjects }>;
export type WorkspaceCatalogClient = Readonly<{
  read(): Promise<CatalogSnapshot>;
  reconnect(): Promise<void>;
  onUnavailable(callback: () => void): () => void;
  onConnected(callback: () => void): () => void;
  dispose(): void;
}>;

const maxBodyBytes = 2 * 1024 * 1024;

function validateCatalogObservations(catalog: WorkspaceCatalog): void {
  const keys = ["blocked", "needsTerry", "readyForReview"] as const;
  for (const entry of catalog.entries) {
    const context = entry.attentionContext;
    if (entry.freshness === "never") {
      if (context !== null || keys.some(key => entry.attention[key].state !== "unknown" || entry.attention[key].value !== null)) throw Error("Unobserved Attention context");
      continue;
    }
    if (!context) throw Error("Missing Attention context");
    validateAttentionAssignments({ lanes: context.lanes, attention: context.assignments });
    if (context.assignments.blocked.length + context.assignments.needsTerry.length > 12) throw Error("Too many assigned waiting lanes");
    if (context.lanes.length !== keys.reduce((sum, key) => sum + context.assignments[key].length, 0)) throw Error("Incomplete Attention context");
    if (context.source === "implicit" && (context.assignments.blocked.length !== 0 || context.assignments.needsTerry.length !== 0 || context.assignments.readyForReview.length !== 1)) throw Error("Invalid implicit Attention context");
    if (context.workflowId === null && (context.profile !== "default" || context.policyVersion !== 1 || context.source !== "implicit" || context.lanes.length !== 1 || context.lanes[0]!.id !== "Ready for Review" || context.lanes[0]!.label !== "Ready for Review")) throw Error("Invalid default Attention context");
    if (context.workflowId !== null && context.profile === "default" && context.policyVersion < 2) throw Error("Invalid edited-default policy");
    for (const [index, lane] of context.lanes.entries()) {
      if (index > 0 && context.lanes[index - 1]!.id >= lane.id || /^\p{White_Space}|\p{White_Space}$/u.test(lane.label)) throw Error("Invalid Attention lane order or label");
      const literal = Array.from(lane.label, character => `\\u{${character.codePointAt(0)!.toString(16)}}`).join("");
      const foldedLabel = new RegExp(`^(?:${literal})$`, "iu");
      if (context.lanes.slice(0, index).some(previous => foldedLabel.test(previous.label))) throw Error("Duplicate Attention lane label");
      if (context.profile === "default" && lane.role === "review" && lane.id !== "Ready for Review") throw Error("Invalid default review identity");
      if ((context.profile === "configured" || lane.role === "waiting") && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(lane.id)) throw Error("Invalid Attention lane identity");
    }
    for (const key of keys) {
      const configured = context.assignments[key].length > 0;
      if (entry.attention[key].state !== (configured ? entry.freshness === "fresh" ? "known" : "stale" : "not_configured")) throw Error("Attention freshness mismatch");
    }
  }
}

// The generated Go boundary is the wire contract. This client keeps only a
// nonsecret connection generation; workspace cookies remain HttpOnly.
export function createWorkspaceCatalogClient(access: WorkspaceSessionAccess,
  fetcher: typeof fetch = globalThis.fetch.bind(globalThis)): WorkspaceCatalogClient {
  const workspaceId = access.initial.workspaceId, origin = location.origin;
  let generation = 0, connected = true, disposed = false;
  let pending: Promise<CatalogSnapshot> | undefined;
  const unavailable = new Set<() => void>(), available = new Set<() => void>();
  const notify = (callbacks: Set<() => void>): void => { for (const callback of callbacks) { try { callback(); } catch {} } };
  const stop = (): void => { generation++; connected = false; notify(unavailable); };
  const removeUnavailable = access.onUnavailable(stop);
  const removeConnected = access.onConnected(() => {
    if (disposed) return;
    generation++; connected = true; notify(available);
  });
  const current = (expected: number): void => {
    if (!disposed && generation === expected && (!isLocalOrigin() || location.origin !== origin)) access.clear();
    if (disposed || !connected || generation !== expected || !isLocalOrigin() || location.origin !== origin) throw Error("Workspace catalog unavailable");
  };
  const readBoundary = async <T>(path: string, schema: string, expected: number): Promise<T> => {
    current(expected);
    const signal = AbortSignal.timeout(20_000);
    const response = await fetcher(path, { method: "GET", credentials: "same-origin", cache: "no-store", redirect: "error", signal,
      headers: { "X-Localswim-Client": "browser" } });
    // An old denial must not clear a replacement workspace connection.
    if (generation === expected && (response.status === 401 || response.status === 403)) access.clear();
    try { current(expected); } catch (error) { await response.body?.cancel().catch(() => {}); throw error; }
    if (response.status !== 200 || response.redirected || response.headers.get("Cache-Control") !== "no-store") {
      await response.body?.cancel().catch(() => {}); throw Error("Workspace catalog unavailable");
    }
    const text = await boundedBody(response, signal); current(expected);
    const value = parse(text); validate(schema, value);
    const result = structuredClone(value) as T & { workspaceId: string };
    if (result.workspaceId !== workspaceId) {
      access.clear(); throw Error("Workspace identity changed");
    }
    current(expected); return result;
  };
  const read = (): Promise<CatalogSnapshot> => {
    if (pending) return pending;
    const expected = generation;
    const work = async (): Promise<CatalogSnapshot> => {
      // Projects first prevents a later project-read failure from discarding a
      // newly observed opaque catalog row in favor of an old trusted row.
      const projects = await readBoundary<WorkspaceProjects>("/api/v1/workspace/projects", "transport:WorkspaceProjects", expected);
      const catalog = await readBoundary<WorkspaceCatalog>("/api/v1/workspace/catalog", "transport:WorkspaceCatalog", expected);
      validateCatalogObservations(catalog);
      current(expected); return { projects, catalog };
    };
    pending = work().finally(() => { pending = undefined; });
    return pending;
  };
  return Object.freeze({ read,
    async reconnect(): Promise<void> {
      if (disposed) throw Error("Workspace catalog unavailable");
      access.clear();
      // Do not start another metadata request while an old response/body is
      // draining. Trigger coalescing never aborts one request to start another.
      await pending?.catch(() => {});
      if (disposed) throw Error("Workspace catalog unavailable");
      await access.reconnect();
    },
    onUnavailable(callback: () => void): () => void { unavailable.add(callback); return () => { unavailable.delete(callback); }; },
    onConnected(callback: () => void): () => void { available.add(callback); return () => { available.delete(callback); }; },
    dispose(): void { if (disposed) return; disposed = true; generation++; connected = false; removeUnavailable(); removeConnected(); unavailable.clear(); available.clear(); },
  });
}

async function boundedBody(response: Response, signal: AbortSignal): Promise<string> {
  const length = response.headers.get("Content-Length");
  if (length !== null && (!/^[0-9]+$/.test(length) || Number(length) > maxBodyBytes)) {
    await response.body?.cancel().catch(() => {}); throw Error("Workspace catalog unavailable");
  }
  const reader = response.body?.getReader();
  if (!reader) throw Error("Workspace catalog unavailable");
  const chunks: Uint8Array[] = []; let size = 0;
  let abort: (() => void) | undefined;
  const aborted = new Promise<never>((_, reject) => {
    abort = () => { void reader.cancel().catch(() => {}); reject(Error("Workspace catalog unavailable")); };
    signal.addEventListener("abort", abort, { once: true });
    if (signal.aborted) abort();
  });
  try {
    for (;;) {
      const part = await Promise.race([reader.read(), aborted]);
      if (part.done) break;
      size += part.value.byteLength;
      if (size > maxBodyBytes) { await reader.cancel().catch(() => {}); throw Error("Workspace catalog unavailable"); }
      chunks.push(part.value);
    }
    const bytes = new Uint8Array(size); let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
    return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes);
  } finally { if (abort) signal.removeEventListener("abort", abort); reader.releaseLock(); }
}
