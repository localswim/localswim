import type { ConfiguredWorkspaceBoardRequest, WorkspaceControlResult } from "../../generated/transport/contracts.js";
import { createConfiguredWorkflowClient, ConfiguredWorkflowRefusal } from "./configured-workflow.js";
import { fakeWorkspaceAccess } from "./catalog.test.js";
import { createWorkflowDraft } from "../workspace-ui/workflow-editor.js";

function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
async function refuses(work: Promise<unknown>): Promise<void> { let failed = false; try { await work; } catch { failed = true; } assert(failed, "Unsafe setup accepted"); }
export function setupRequest(): ConfiguredWorkspaceBoardRequest {
  const draft = createWorkflowDraft("harbor"); draft.boardKey = "configured"; draft.label = "Configured board";
  return { apiVersion: 1, workspaceId: "10000000-0000-4000-8000-000000000001", expectedConfigRevision: 1, ...draft };
}
export async function runConfiguredWorkflowTransportTests(): Promise<number> {
  let count = 0;
  const request = setupRequest(), access = fakeWorkspaceAccess(request.workspaceId), calls: { path: string; init: RequestInit }[] = [];
  const result: WorkspaceControlResult = { apiVersion: 1, workspaceId: request.workspaceId, configRevision: 2, projectKey: request.projectKey, boardKey: request.boardKey,
    status: "registered", identity: { installationId: crypto.randomUUID(), boardId: crypto.randomUUID(), incarnation: crypto.randomUUID() } };
  const reply = (body: unknown, status = 200): Response => new Response(JSON.stringify(body), { status, headers: { "Cache-Control": "no-store" } });
  let respond = async (): Promise<Response> => reply(result);
  const client = createConfiguredWorkflowClient(access, async (input, init = {}) => { calls.push({ path: String(input), init }); return respond(); });
  try {
    assert((await client.create(request)).status === "registered", "Create result lost");
    const call = calls[0]!;
    assert(call.path === "/api/v1/workspace/boards/configured" && call.init.method === "POST" && call.init.credentials === "same-origin" && call.init.redirect === "error" && call.init.cache === "no-store" &&
      new Headers(call.init.headers).get("X-Localswim-Client") === "browser" && new Headers(call.init.headers).get("Content-Type") === "application/json" && !new Headers(call.init.headers).has("Authorization"), "Wrong workspace setup channel"); count++;
    const { expectedConfigRevision: _, label: __, ...status } = request;
    await client.status({ ...status, expectedIdentity: result.identity! }); assert(calls[1]!.path.endsWith("/status") && JSON.parse(String(calls[1]!.init.body)).workflow.lanes.length === 4, "Status did not preserve complete definition"); count++;
    const before = calls.length; respond = async () => { throw Error("lost response"); }; await refuses(client.create(request)); assert(calls.length === before + 1, "Create replayed or looked up automatically"); count++;
    for (const changed of [{ ...result, boardKey: "foreign" }, { ...result, workspaceId: crypto.randomUUID() }, { ...result, identity: null }, { ...result, privateRoot: "forbidden" }]) { respond = async () => reply(changed); await refuses(client.create(request)); count++; }
    respond = async () => reply({ apiVersion: 1, configRevision: 3, outcome: "refused", error: { code: "CONFIG_CHANGED", field: null, message: "Changed" } }, 412);
    let failure: unknown; try { await client.create(request); } catch (error) { failure = error; } assert(failure instanceof ConfiguredWorkflowRefusal, "Known refusal became success"); count++;
    const beforeWrong = calls.length; await refuses(client.create({ ...request, workspaceId: crypto.randomUUID() })); assert(calls.length === beforeWrong, "Wrong workspace dispatched"); count++;
    let release: (() => void) | undefined, started: (() => void) | undefined;
    const held = new Promise<void>(resolve => { started = resolve; });
    respond = async () => { started!(); await new Promise<void>(resolve => { release = resolve; }); return reply(result); };
    const old = client.create(request).then(() => false, () => true); await held; await access.reconnect(); release!(); assert(await old, "Late setup response crossed connection generation"); count++;
  } finally { client.dispose(); }
  return count;
}
