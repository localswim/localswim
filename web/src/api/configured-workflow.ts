import type { ConfiguredWorkspaceBoardRequest, ConfiguredWorkspaceStatusRequest, WorkspaceControlResult } from "../../generated/transport/contracts.js";
import type { WorkspaceSessionAccess } from "./workspace-session.js";
import { parse, validate } from "./boundary.js";
import { isLocalOrigin } from "./client.js";

export class ConfiguredWorkflowRefusal extends Error {
  constructor(readonly code: string, readonly configRevision: number) { super("Configured board request refused"); }
}
export type ConfiguredWorkflowClient = Readonly<{
  create(request: ConfiguredWorkspaceBoardRequest): Promise<WorkspaceControlResult>;
  status(request: ConfiguredWorkspaceStatusRequest): Promise<WorkspaceControlResult>;
  dispose(): void;
}>;

// One explicit call means one POST. This channel never copies board credentials,
// reconnects, retries creation, or performs an implicit status lookup.
export function createConfiguredWorkflowClient(access: WorkspaceSessionAccess,
  fetcher: typeof fetch = globalThis.fetch.bind(globalThis)): ConfiguredWorkflowClient {
  const origin = location.origin, workspaceId = access.initial.workspaceId;
  let generation = 0, connected = true, disposed = false, busy = false;
  const off = access.onUnavailable(() => { generation++; connected = false; });
  const on = access.onConnected(() => { generation++; connected = true; });
  const current = (expected: number): void => {
    if (disposed || !connected || expected !== generation || location.origin !== origin || !isLocalOrigin()) throw Error("Workspace setup unavailable");
  };
  const send = async (input: ConfiguredWorkspaceBoardRequest | ConfiguredWorkspaceStatusRequest, status: boolean): Promise<WorkspaceControlResult> => {
    const expected = generation; current(expected);
    if (busy) throw Error("Workspace setup request already active");
    const request = structuredClone(input), body = JSON.stringify(request);
    validate(status ? "transport:ConfiguredWorkspaceStatusRequest" : "transport:ConfiguredWorkspaceBoardRequest", parse(body));
    if (request.workflow.profile !== undefined || request.workflow.revision !== 1) throw Error("Invalid initial workflow");
    if (request.workspaceId !== workspaceId || new TextEncoder().encode(body).length > 65536) throw Error("Invalid workspace setup request");
    busy = true;
    try {
      const signal = AbortSignal.timeout(20_000);
      const path = "/api/v1/workspace/boards/configured" + (status ? "/status" : "");
      const response = await fetcher(path, { method: "POST", body, credentials: "same-origin", cache: "no-store", redirect: "error", signal,
        headers: { "Content-Type": "application/json", "X-Localswim-Client": "browser" } });
      if (generation === expected && (response.status === 401 || response.status === 403)) access.clear();
      try { current(expected); } catch (error) { await response.body?.cancel().catch(() => {}); throw error; }
      if (response.redirected || (response.url && response.url !== new URL(path, origin).href) || response.headers.get("Cache-Control") !== "no-store") {
        await response.body?.cancel().catch(() => {}); throw Error("Uncertain workspace setup response");
      }
      const value = parse(await boundedBody(response, signal)); current(expected);
      if (response.status !== 200) {
        validate("transport:WorkspaceControlError", value);
        const failure = value as unknown as { error: { code: string }; configRevision: number };
        if (response.status >= 400 && response.status < 500) throw new ConfiguredWorkflowRefusal(failure.error.code, failure.configRevision);
        throw Error("Uncertain workspace setup response");
      }
      validate("transport:WorkspaceControlResult", value);
      const result = structuredClone(value) as unknown as WorkspaceControlResult;
      if (result.workspaceId !== workspaceId || result.projectKey !== request.projectKey || result.boardKey !== request.boardKey ||
        ((result.status === "registered" || result.status === "initialized-unregistered") && !result.identity)) throw Error("Workspace setup response mismatch");
      if ("expectedIdentity" in request && request.expectedIdentity && result.identity &&
        ["installationId", "boardId", "incarnation"].some(key => result.identity![key as keyof typeof result.identity] !== request.expectedIdentity![key as keyof typeof request.expectedIdentity])) throw Error("Workspace setup identity mismatch");
      current(expected); return result;
    } finally { busy = false; }
  };
  return Object.freeze({ create: (request: ConfiguredWorkspaceBoardRequest) => send(request, false), status: (request: ConfiguredWorkspaceStatusRequest) => send(request, true),
    dispose(): void { disposed = true; generation++; off(); on(); } });
}

async function boundedBody(response: Response, signal: AbortSignal): Promise<string> {
  const length = response.headers.get("Content-Length");
  if (length !== null && (!/^\d+$/.test(length) || Number(length) > 65536)) { await response.body?.cancel(); throw Error("Setup response too large"); }
  const reader = response.body?.getReader(); if (!reader) throw Error("Setup response unavailable");
  const parts: Uint8Array[] = []; let total = 0;
  let abort: (() => void) | undefined;
  const aborted = new Promise<never>((_, reject) => { abort = () => { void reader.cancel().catch(() => {}); reject(Error("Setup response timed out")); }; signal.addEventListener("abort", abort, { once: true }); if (signal.aborted) abort(); });
  try {
    for (;;) { const next = await Promise.race([reader.read(), aborted]); if (next.done) break; total += next.value.length; if (total > 65536) throw Error("Setup response too large"); parts.push(next.value); }
    const bytes = new Uint8Array(total); let offset = 0; for (const part of parts) { bytes.set(part, offset); offset += part.length; }
    return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes);
  } finally { if (abort) signal.removeEventListener("abort", abort); await reader.cancel().catch(() => {}); reader.releaseLock(); }
}
