// Deterministic synthetic browser components. These exercise real private IDB
// and production clients against HTTP doubles, not native host qualification.
import type { LocalBootstrap, MutationRequest, SessionRead, Terminal, WorkspaceResolve } from "../../generated/transport/contracts.js";
import { parse, prepare, validate } from "./boundary.js";
import { BoardClient } from "./client.js";
import { readLocalBootstrap } from "./local-session.js";
import { readRuntimeMode } from "./runtime.js";
import { openWorkspaceSession } from "./workspace-session.js";

function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
async function refuses(action: () => Promise<unknown>): Promise<void> {
  let refused = false; try { await action(); } catch { refused = true; }
  assert(refused, "Invalid/stale selection accepted");
}
function reply(value: unknown): Response { return new Response(typeof value === "string" ? value : JSON.stringify(value), { headers: { "Cache-Control": "no-store" } }); }

export async function runWorkspaceSessionSmoke(): Promise<number> {
  let passed = 0;
  const workspaceId = crypto.randomUUID(), workspaceEpoch = crypto.randomUUID();
  const makeBoard = (boardKey: string): { selection: WorkspaceResolve; bootstrap: LocalBootstrap } => {
    const installationId = crypto.randomUUID(), boardId = crypto.randomUUID(), incarnation = crypto.randomUUID(), epoch = crypto.randomUUID();
    const session: SessionRead = { apiVersion: 1, kind: "human", actorId: crypto.randomUUID(), grant: { grantId: crypto.randomUUID(), boardId, incarnation, rights: ["create", "read", "read-own-outcome"] } };
    return { selection: { apiVersion: 1, workspaceId, configRevision: 1, member: { projectKey: "harbor", boardKey, label: boardKey,
      availability: "ready", identity: { installationId, boardId, incarnation }, memberEpoch: epoch } },
      bootstrap: { apiVersion: 1, installationId, boardId, incarnation, epoch, session } };
  };
  const a = makeBoard("a"), b = makeBoard("b"), boards = [a, b];
  const requests: { path: string; method: string; credentials: RequestCredentials | undefined; auth: string | null; body: BodyInit | null | undefined; header: string | null }[] = [];
  let mode: unknown = { apiVersion: 1, mode: "workspace", workspaceId }, mutationCount = 0, lookupCount = 0;
  let wrongBootstrap: unknown, wrongResolve: unknown, wrongWorkspace: unknown;
  let holdSession = false, releaseSession: (() => void) | undefined, sessionStarted: (() => void) | undefined;
  let delayedSessionStatus = 200;
  let holdSessionBody = false, releaseSessionBody: (() => void) | undefined, sessionBodyStarted: (() => void) | undefined;
  let holdMutation = false, releaseMutation: (() => void) | undefined, mutationStarted: (() => void) | undefined;
  const fetcher: typeof fetch = async (input, init = {}) => {
    const path = String(input), headers = new Headers(init.headers);
    requests.push({ path, method: init.method ?? "GET", credentials: init.credentials, auth: headers.get("Authorization"), body: init.body, header: headers.get("X-Localswim-Client") });
    if (path === "/api/v1/runtime-mode") return reply(mode);
    if (path === "/api/v1/workspace/bootstrap") return reply(wrongWorkspace ?? { apiVersion: 1, workspaceId, epoch: workspaceEpoch, mode: "human" });
    const selected = boards.find(item => path === "/api/v1/workspace/projects/harbor/boards/" + item.selection.member.boardKey);
    if (selected) return reply(wrongResolve ?? selected.selection);
    const board = boards.find(item => path.startsWith("/api/v1/boards/" + item.bootstrap.boardId + "/"));
    assert(board, "Unexpected route");
    if (path.endsWith("/local/bootstrap")) {
      assert(init.body === JSON.stringify({ apiVersion: 1, installationId: board.bootstrap.installationId, incarnation: board.bootstrap.incarnation }), "Selected bootstrap omitted expected tuple");
      return reply(wrongBootstrap ?? board.bootstrap);
    }
    if (path.endsWith("/session")) {
      if (holdSession) { holdSession = false; const captured = structuredClone(board.bootstrap.session), status = delayedSessionStatus;
        sessionStarted?.(); await new Promise<void>(resolve => { releaseSession = resolve; }); return new Response(JSON.stringify(captured), { status }); }
      if (holdSessionBody) {
        holdSessionBody = false; const captured = JSON.stringify(board.bootstrap.session);
        const stream = new ReadableStream<Uint8Array>({ start(controller) {
          releaseSessionBody = () => { controller.enqueue(new TextEncoder().encode(captured)); controller.close(); };
        } });
        sessionBodyStarted?.(); return new Response(stream);
      }
      return reply(board.bootstrap.session);
    }
    if (path.includes("/operations/")) { lookupCount++; return new Response("{}", { status: 503 }); }
    mutationCount++;
    if (holdMutation) { holdMutation = false; mutationStarted?.(); await new Promise<void>(resolve => { releaseMutation = resolve; }); }
    const request = JSON.parse(String(init.body)) as MutationRequest;
    const evidence = await prepare("card.create", request, { installationId: board.bootstrap.installationId, boardId: board.bootstrap.boardId,
      incarnation: board.bootstrap.incarnation, actorId: board.bootstrap.session.actorId }, crypto.randomUUID());
    const createdId = crypto.randomUUID();
    const terminal: Terminal = { apiVersion: 1, boardId: board.bootstrap.boardId, incarnation: board.bootstrap.incarnation, actorId: board.bootstrap.session.actorId,
      opId: request.opId, fingerprintVersion: 1, fingerprint: evidence.fingerprint, command: "card.create", target: null, expectedRevision: 0,
      expectedCardRevision: null, outcome: "committed", code: "OK", terminalAt: "2026-10-28T00:00:00Z", revision: 1, cardRevision: 1, retention: "full",
      result: { cardId: createdId }, resultRefs: [{ kind: "card", id: createdId, revision: 1 }], byteFacts: [], removal: null };
    validate("transport:TerminalCardCreateCommittedFull", parse(JSON.stringify(terminal)));
    return reply(terminal);
  };

  for (const bad of [null, {}, { apiVersion: 2, mode: "workspace", workspaceId }, { apiVersion: 1, mode: "workspace" },
    { apiVersion: 1, mode: "standalone", workspaceId }, { apiVersion: 1, mode: "workspace", workspaceId, url: "https://invalid" },
    '{"apiVersion":1,"mode":"standalone","mode":"workspace"}']) {
    mode = bad; const before = requests.length;
    await refuses(() => readLocalBootstrap(fetcher));
    assert(requests.length === before + 1 && requests.at(-1)!.path === "/api/v1/runtime-mode", "Bad mode reached bootstrap"); passed++;
  }
  mode = { apiVersion: 1, mode: "workspace", workspaceId };
  await refuses(() => BoardClient.open({ installationId: a.bootstrap.installationId, profileId: "must-refuse", transport: "local-cookie", credential: () => "nonsecret-generation" })); passed++;
  await refuses(() => readLocalBootstrap(fetcher)); passed++;
  assert((await readRuntimeMode(fetcher)).mode === "workspace", "Valid discriminator refused"); passed++;
  wrongWorkspace = { apiVersion: 1, workspaceId: crypto.randomUUID(), epoch: workspaceEpoch, mode: "human" };
  await refuses(() => openWorkspaceSession(workspaceId, fetcher)); passed++;
  wrongWorkspace = { apiVersion: 1, workspaceId, epoch: workspaceEpoch, mode: "human", token: "forbidden" };
  await refuses(() => openWorkspaceSession(workspaceId, fetcher)); passed++; wrongWorkspace = undefined;
  const access = await openWorkspaceSession(workspaceId, fetcher);
  let aa: Awaited<ReturnType<typeof access.openBoard>> | undefined, bb: typeof aa;
  try {
    for (const bad of [ { ...a.selection, workspaceId: crypto.randomUUID() }, { ...a.selection, extra: true },
      { ...a.selection, member: { ...a.selection.member, boardKey: "b" } },
      { ...a.selection, member: { ...a.selection.member, availability: "busy", identity: a.selection.member.identity, memberEpoch: null } } ]) {
      wrongResolve = bad; await refuses(() => access.resolve("harbor", "a")); passed++;
    }
    wrongResolve = undefined;
    const resolved = await access.resolve("harbor", "a");
    for (const bad of [ { ...a.bootstrap, installationId: b.bootstrap.installationId }, { ...a.bootstrap, boardId: b.bootstrap.boardId },
      { ...a.bootstrap, incarnation: b.bootstrap.incarnation }, { ...a.bootstrap, epoch: crypto.randomUUID() },
      { ...a.bootstrap, session: { ...a.bootstrap.session, kind: "automation" } },
      { ...a.bootstrap, session: { ...a.bootstrap.session, grant: { ...a.bootstrap.session.grant, boardId: b.bootstrap.boardId } } },
      { ...a.bootstrap, extra: true } ]) {
      wrongBootstrap = bad; await refuses(() => access.openBoard(resolved)); passed++;
    }
    wrongBootstrap = undefined;
    aa = await access.openBoard(resolved); bb = await access.openBoard(await access.resolve("harbor", "b"));
    assert(aa.recoveryReady && bb.recoveryReady, "Strict private browser recovery unavailable"); passed++;
    const context = { installationId: a.bootstrap.installationId, boardId: a.bootstrap.boardId, incarnation: a.bootstrap.incarnation, actorId: a.bootstrap.session.actorId };
    const other = { installationId: b.bootstrap.installationId, boardId: b.bootstrap.boardId, incarnation: b.bootstrap.incarnation, actorId: b.bootstrap.session.actorId };
    const before = requests.length;
    await refuses(() => aa!.client.board(other.boardId));
    await refuses(() => aa!.client.card(other.boardId, crypto.randomUUID()));
    assert((await aa.client.lookup(other, crypto.randomUUID())).state === "unknown" && requests.length === before, "A client addressed B with B cookie present"); passed++;
    const wrongIntent = await aa.client.submit({ command: "card.create", context: other, draftId: crypto.randomUUID(), request: { opId: crypto.randomUUID(), incarnation: other.incarnation,
      expectedRevision: 0, expectedCardRevision: null, target: null, input: { title: "wrong", description: "retained" } } }, () => {});
    assert(wrongIntent.state === "unsent" && requests.length === before, "A client dispatched B intent"); passed++;

    const observed = new Promise<void>(resolve => { sessionStarted = resolve; }); holdSession = true;
    const oldSession = aa.client.session(); const oldResult = oldSession.then(() => false, () => true); await observed;
    a.bootstrap = { ...a.bootstrap, epoch: crypto.randomUUID() };
    a.selection = { ...a.selection, member: { ...a.selection.member, memberEpoch: a.bootstrap.epoch } } as WorkspaceResolve;
    await aa.reconnect(); releaseSession!();
    assert(await oldResult, "Old session response crossed connection generation"); passed++;
    assert((await bb.client.session()).actorId === other.actorId, "A reconnect altered B session"); passed++;
    const bodyObserved = new Promise<void>(resolve => { sessionBodyStarted = resolve; }); holdSessionBody = true;
    const oldBody = aa.client.session().then(() => false, () => true); await bodyObserved;
    await aa.reconnect(); releaseSessionBody!();
    assert(await oldBody, "Old body completion crossed connection generation"); passed++;
    const denialObserved = new Promise<void>(resolve => { sessionStarted = resolve; }); holdSession = true; delayedSessionStatus = 401;
    const oldDenial = aa.client.session().then(() => false, () => true); await denialObserved;
    await aa.reconnect(); releaseSession!(); assert(await oldDenial, "Late denial reused connection");
    assert((await aa.client.session()).actorId === context.actorId, "Late denial cleared replacement generation"); passed++; delayedSessionStatus = 200;

    const draftId = crypto.randomUUID(), opId = crypto.randomUUID();
    await aa.client.recovery.saveDraft({ context, draftId, expectedRevision: 0, input: "original A input" });
    holdMutation = true; const transmitted = new Promise<void>(resolve => { mutationStarted = resolve; });
    const pending = aa.client.submit({ command: "card.create", context, draftId, request: { opId, incarnation: context.incarnation,
      expectedRevision: 0, expectedCardRevision: null, target: null, input: { title: "Synthetic A", description: "original A input" } } }, () => {});
    await transmitted;
    await aa.reconnect(); releaseMutation!(); const outcome = await pending;
    assert(outcome.state === "unknown" && mutationCount === 1 && lookupCount === 0 && outcome.evidence?.context.boardId === context.boardId, "Reconnect replayed, looked up or rebound original"); passed++;
    assert(!!await aa.client.recovery.loadSubmission(context, opId) && (await bb.client.lookup(context, opId)).state === "unknown", "Original evidence lost or resolved using B"); passed++;
    const savedActor = a.bootstrap.session;
    a.bootstrap = { ...a.bootstrap, session: { ...savedActor, actorId: crypto.randomUUID() } };
    await refuses(() => aa!.reconnect());
    const stopped = requests.length; await refuses(() => aa!.client.session()); assert(requests.length === stopped, "Changed actor retained authority"); passed++;
    a.bootstrap = { ...a.bootstrap, session: savedActor };
    await aa.reconnect();
    a.bootstrap = { ...a.bootstrap, session: { ...savedActor, grant: { ...savedActor.grant, grantId: crypto.randomUUID() } } };
    await refuses(() => aa!.reconnect()); assert(mutationCount === 1 && lookupCount === 0, "Changed grant replayed or looked up original"); passed++;
    a.bootstrap = { ...a.bootstrap, session: savedActor };
    await aa.reconnect();
    const originalIdentity = a.selection.member.identity;
    a.selection = { ...a.selection, member: { ...a.selection.member, identity: b.selection.member.identity } } as WorkspaceResolve;
    await refuses(() => aa!.reconnect()); assert(mutationCount === 1 && lookupCount === 0, "Replacement selection replayed or looked up original"); passed++;
    a.selection = { ...a.selection, member: { ...a.selection.member, identity: originalIdentity } } as WorkspaceResolve;
    assert(requests.every(r => r.auth === null && r.credentials === "same-origin" && r.header === "browser"), "Cookie channel leaked bearer or omitted browser protection"); passed++;
    assert(!requests.some(r => r.path === "/api/v1/session" || r.path === "/api/v1/local/bootstrap"), "Workspace used standalone default routes"); passed++;
    access.clear(); const stoppedMetadata = requests.length;
    await refuses(() => access.resolve("harbor", "a")); assert(requests.length === stoppedMetadata, "Cleared workspace reused metadata authority"); passed++;
  } finally { aa?.clear(); bb?.clear(); aa?.client.close(); bb?.client.close(); access.clear(); }
  return passed;
}
