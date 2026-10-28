import type { BoardBootstrapRequest, LocalBootstrap, WorkspaceBootstrap, WorkspaceResolve } from "../../generated/transport/contracts.js";
import { parse, validate } from "./boundary.js";
import { openBoundLocalSession, assertLocalBootstrap } from "./local-session.js";
import type { LocalSessionAccess } from "./local-session.js";
import { readLocalBoundary, readRuntimeMode } from "./runtime.js";

export type WorkspaceSessionAccess = Readonly<{
  initial: WorkspaceBootstrap;
  resolve(projectKey: string, boardKey: string): Promise<WorkspaceResolve>;
  openBoard(selected: WorkspaceResolve): Promise<LocalSessionAccess>;
  reconnect(): Promise<WorkspaceBootstrap>;
  clear(): void;
  onUnavailable(callback: () => void): () => void;
  onConnected(callback: () => void): () => void;
}>;
const key = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
function selectedKeys(projectKey: string, boardKey: string): string {
  if (![projectKey, boardKey].every(value => value.length >= 1 && value.length <= 64 && key.test(value))) throw Error("Invalid board selection");
  return "/api/v1/workspace/projects/" + projectKey + "/boards/" + boardKey;
}

// Workspace metadata has its own channel, never a board actor/grant. Derive
// board bootstrap/session endpoints solely from the validated selected BoardID.
export async function openWorkspaceSession(workspaceId: string, fetcher: typeof fetch = globalThis.fetch.bind(globalThis)): Promise<WorkspaceSessionAccess> {
  const origin = location.origin;
  const bootstrapWorkspace = async (): Promise<WorkspaceBootstrap> => {
    const mode = await readRuntimeMode(fetcher);
    if (location.origin !== origin || mode.mode !== "workspace" || mode.workspaceId !== workspaceId) throw Error("Workspace identity changed");
    const response = await fetcher("/api/v1/workspace/bootstrap", { method: "POST", body: "{}", credentials: "same-origin", cache: "no-store", redirect: "error",
      signal: AbortSignal.timeout(20_000), headers: { "Content-Type": "application/json", "X-Localswim-Client": "browser" } });
    const value = await readLocalBoundary<WorkspaceBootstrap>(response, "transport:WorkspaceBootstrap", origin);
    if (value.workspaceId !== workspaceId) throw Error("Workspace identity changed");
    return value;
  };
  const initial = await bootstrapWorkspace();
  let marker: string | undefined = initial.epoch + ":" + crypto.randomUUID(), generation = 0;
  const callbacks = new Set<() => void>(), connected = new Set<() => void>();
  const clear = (): void => { marker = undefined; generation++; for (const callback of callbacks) { try { callback(); } catch {} } };
  const current = (expected?: string): string => {
    if (!marker || location.origin !== origin || (expected !== undefined && marker !== expected)) throw Error("Workspace connection unavailable");
    return marker;
  };
  const reconnect = async (): Promise<WorkspaceBootstrap> => {
    clear(); const started = generation;
    const next = await bootstrapWorkspace();
    if (generation !== started) throw Error("Workspace connection changed");
    marker = next.epoch + ":" + crypto.randomUUID();
    for (const callback of connected) { try { callback(); } catch { /* Observers do not supply authority. */ } }
    return next;
  };
  const resolve = async (projectKey: string, boardKey: string): Promise<WorkspaceResolve> => {
    const connection = current(), path = selectedKeys(projectKey, boardKey);
    const response = await fetcher(path, { method: "GET", credentials: "same-origin", cache: "no-store", redirect: "error", signal: AbortSignal.timeout(20_000), headers: { "X-Localswim-Client": "browser" } });
    if ((response.status === 401 || response.status === 403) && marker === connection) clear();
    const result = await readLocalBoundary<WorkspaceResolve>(response, "transport:WorkspaceResolve", origin); current(connection);
    if (result.workspaceId !== workspaceId || result.member.projectKey !== projectKey || result.member.boardKey !== boardKey) throw Error("Selected board mismatch");
    return result;
  };
  return Object.freeze({ initial: Object.freeze({ ...initial }), resolve, reconnect, clear,
    onConnected(callback: () => void): () => void { connected.add(callback); return () => { connected.delete(callback); }; },
    onUnavailable(callback: () => void): () => void { callbacks.add(callback); return () => { callbacks.delete(callback); }; },
    async openBoard(selection: WorkspaceResolve): Promise<LocalSessionAccess> {
      current(); const selected = structuredClone(selection);
      validate("transport:WorkspaceResolve", parse(JSON.stringify(selected)));
      selectedKeys(selected.member.projectKey, selected.member.boardKey);
      if (selected.workspaceId !== workspaceId || selected.member.availability !== "ready") throw Error("Selected board unavailable");
      const identity = Object.freeze({ ...selected.member.identity });
      let first = true;
      const bootstrap = async (): Promise<LocalBootstrap> => {
        if (!first) await reconnect();
        const connection = current();
        const mode = await readRuntimeMode(fetcher); current(connection);
        if (mode.mode !== "workspace" || mode.workspaceId !== workspaceId) throw Error("Workspace identity changed");
        const next = first ? selected : await resolve(selected.member.projectKey, selected.member.boardKey);
        first = false;
        if (next.member.availability !== "ready" || next.member.identity.installationId !== identity.installationId ||
          next.member.identity.boardId !== identity.boardId || next.member.identity.incarnation !== identity.incarnation) throw Error("Selected identity changed");
        const request: BoardBootstrapRequest = { apiVersion: 1, installationId: identity.installationId, incarnation: identity.incarnation };
        validate("transport:BoardBootstrapRequest", parse(JSON.stringify(request)));
        const response = await fetcher("/api/v1/boards/" + identity.boardId + "/local/bootstrap", { method: "POST", body: JSON.stringify(request), credentials: "same-origin",
          signal: AbortSignal.timeout(20_000), cache: "no-store", redirect: "error", headers: { "Content-Type": "application/json", "X-Localswim-Client": "browser" } });
        const result = await readLocalBoundary<LocalBootstrap>(response, "transport:LocalBootstrap", origin); current(connection); assertLocalBootstrap(result);
        if (result.installationId !== identity.installationId || result.boardId !== identity.boardId || result.incarnation !== identity.incarnation || result.epoch !== next.member.memberEpoch) throw Error("Selected bootstrap mismatch");
        return result;
      };
      const access = await openBoundLocalSession("workspace", bootstrap, fetcher);
      try { current(); } catch (error) { access.clear(); access.client.close(); throw error; }
      return access;
    },
  });
}
