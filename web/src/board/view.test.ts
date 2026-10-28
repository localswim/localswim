// Synthetic fixture only. This assertion never qualifies a product profile.
import type { BoardRead, SessionRead, Terminal } from "../../generated/transport/contracts.js";
import { BoardClient } from "../api/client.js";
import { prepare } from "../api/boundary.js";
import type { BoardCommand } from "../api/boundary.js";
import { BoardView } from "./view.js";

export async function mountSyntheticBoard(boardInput: BoardRead, sessionInput: SessionRead, qualified = true, databaseName = "w02-test-" + crypto.randomUUID()) {
  const board = structuredClone(boardInput), session = structuredClone(sessionInput);
  const auth: SessionRead = { ...session, grant: { ...sessionInput.grant, rights: ["approve", "attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"] } };
  const installationId = "00000000-0000-4000-8000-000000000090";
  const state = { mode: "commit", mutations: 0, lookups: 0, releases: [] as (() => void)[], wire: [] as string[], readUnavailable: false, terminals: new Map<string, Terminal>() };
  const client = await BoardClient.open({ installationId, profileId: "synthetic-only", credential: () => "synthetic-ephemeral-token",
    ...(qualified ? { qualification: { installationId, profileId: "synthetic-only", strictDurabilityQualified: true as const, evidenceReference: "synthetic browser component test only" } } : {}),
    databaseName,
    ...(location.protocol === "http:" ? { transport: "local-cookie" as const, selection: { mode: "standalone" as const, boardId: session.grant.boardId,
      incarnation: session.grant.incarnation, actorId: session.actorId, grantId: session.grant.grantId } } : {}),
    fetch: (async (url, init) => {
      const path = String(url), json = (value: unknown, status = 200): Response => new Response(JSON.stringify(value), { status });
      if (path === "/api/v1/session") return json(auth);
      if (path.includes("/operations/")) { state.lookups++; const op = path.split("/operations/")[1]!.split("?")[0]!; return json(state.terminals.get(op) ?? { outcome: "unknown" }); }
      if (!init?.body) {
        if (state.readUnavailable) return json({}, 503);
        if (path.includes("/cards/")) return json({ apiVersion: 1, scope: "card", result: { ...board.result, card: board.result.cards[0], cards: undefined, policyVersion: undefined, retention: undefined, retentionVersion: undefined, historyReferences: [] } });
        return json(board);
      }
      state.mutations++; state.wire.push(String(init.body));
      const request = JSON.parse(String(init.body));
      const command: BoardCommand = path.endsWith("/comments") ? "comment.add" : path.endsWith("/link-add") ? "link.add" : path.endsWith("/link-remove") ? "link.remove" : path.endsWith("/review-handoff") ? "review-handoff" : path.endsWith("/approve") ? "approve" : init.method === "PATCH" ? "card.edit" : "card.create";
      if (state.mode === "conflict") return json({ apiVersion: 1, outcome: "refused", opId: request.opId, error: { code: "REVISION_CONFLICT", message: "Synthetic conflict", field: null }, conflict: { incarnation: board.result.identity.incarnation, revision: board.result.revision, cardId: request.target.cardId, cardRevision: board.result.cards[0]!.revision } }, 409);
      if (state.mode === "http403") return json({ apiVersion: 1, outcome: "refused", opId: request.opId, error: { code: "FORBIDDEN", message: "Synthetic refusal", field: null }, conflict: null }, 403);
      const context = client.context(board, auth), e = await prepare(command, request, context, crypto.randomUUID());
      let card = request.target ? board.result.cards.find(c => c.cardId === request.target.cardId)! : undefined;
      if (command === "card.create") {
        card = { archived: false, attachmentIds: [], cardId: crypto.randomUUID(), commentIds: [], createdIn: board.result.identity,
          ownerId: request.input.ownerId, currentApprovalId: null, description: request.input.description, lane: "Ready for Work", linkIds: [], priority: request.input.priority ?? "P3", revision: 1, ticket: board.result.cards.length + 1, title: request.input.title };
        (board.result.cards as unknown as object[]).push(card);
      } else {
        Object.assign(card!, { revision: card!.revision + 1 });
        if (command === "card.edit") Object.assign(card!, request.input);
      }
      Object.assign(board.result, { revision: board.result.revision + 1 });
      const result: Record<string, string> = { cardId: card!.cardId };
      if (command === "comment.add") {
        result.commentId = crypto.randomUUID();
        (board.result.comments as unknown as object[]).push({ actorId: auth.actorId, cardId: card!.cardId, commentId: result.commentId, createdAt: "2026-10-01T00:00:00Z", createdIn: board.result.identity, text: request.input.text });
        (card!.commentIds as string[]).push(result.commentId);
      }
      if (command === "link.add" || command === "link.remove") result.linkId = request.input.linkId ?? crypto.randomUUID();
      if (command === "approve") result.approvalId = crypto.randomUUID();
      const terminal = { apiVersion: 1, actorId: auth.actorId, boardId: context.boardId, incarnation: context.incarnation, opId: request.opId,
        fingerprintVersion: 1, fingerprint: e.fingerprint, command, target: request.target, expectedRevision: request.expectedRevision,
        expectedCardRevision: request.expectedCardRevision, outcome: "committed", retention: "full", code: "OK", revision: board.result.revision,
        cardRevision: card!.revision, terminalAt: "2026-10-01T00:00:00Z", result, resultRefs: Object.entries(result).map(([key, id]) => ({ kind: key.replace(/Id$/, ""), id, revision: key === "cardId" ? card!.revision : null })).sort((a, b) => a.kind < b.kind ? -1 : 1), byteFacts: [], removal: null } as unknown as Terminal;
      state.terminals.set(request.opId, terminal);
      if (state.mode === "late") await new Promise<void>(resolve => state.releases.push(resolve));
      return json(terminal);
    }) as typeof fetch,
  });
  const root = document.createElement("main"); document.body.replaceChildren(root);
  const view = new BoardView(root, client, board.result.identity.boardId); await view.open();
  return { view, client, state, board, auth };
}

// Synthetic component evidence only; live product qualification uses the normal local runtime.
export async function runAssignmentEditorProbe(boardInput: BoardRead, sessionInput: SessionRead): Promise<number> {
  const input = structuredClone(boardInput), owner = input.result.actors[0]!;
  Object.assign(input.result.cards[0]!, { ownerId: owner.actorId });
  let mounted = await mountSyntheticBoard(input, sessionInput);
  const findButton = (label: string): HTMLButtonElement => [...document.querySelectorAll("button")].find(n => n.textContent === label)!;
  const selector = (): HTMLSelectElement => document.querySelector('select[name="ownerId"]')!;
  const settle = async (test: () => boolean): Promise<void> => { for (let i = 0; i < 250; i++) { if (test()) return; await new Promise(resolve => setTimeout(resolve, 20)); } throw Error("Synthetic component did not settle"); };
  try {
    if (!document.body.textContent!.includes("Assignee: " + owner.label)) throw Error("Assignment display missing: " + document.body.textContent);
    findButton("Edit card").click();
    const control = selector();
    if (control.value !== owner.actorId || control.parentElement!.textContent!.startsWith("Assignee ") !== true || control.options[0]!.textContent !== "Unassigned" || [...control.options].map(o => o.value).join() !== ["", ...input.result.actors.map(a => a.actorId)].join()) throw Error("Assignment editor choices mismatch");
    mounted.state.mode = "conflict";
    control.value = ""; control.dispatchEvent(new Event("change", { bubbles: true }));
    const title = document.querySelector<HTMLInputElement>('input[name="title"]')!;
    title.value = "Retained conflict input"; title.dispatchEvent(new Event("input", { bubbles: true }));
    await settle(() => !findButton("Save").disabled); findButton("Save").click();
    await settle(() => mounted.state.wire.length === 1 && document.body.textContent!.includes("Conflict. Input retained;"));
    if (JSON.parse(mounted.state.wire[0]!).input.ownerId !== null || control.value !== "" || title.value !== "Retained conflict input") throw Error("Unassignment or conflict input lost");
    await mounted.view.refresh();
    if (selector() !== control || control.value !== "" || title.value !== "Retained conflict input") throw Error("Refresh replaced assignment draft");
    mounted.view.destroy(); mounted.client.close();
    mounted = await mountSyntheticBoard(input, sessionInput);
    findButton("Create card").click();
    const createTitle = document.querySelector<HTMLInputElement>('input[name="title"]')!;
    createTitle.value = "Created assigned"; createTitle.dispatchEvent(new Event("input", { bubbles: true }));
    selector().value = owner.actorId; selector().dispatchEvent(new Event("change", { bubbles: true }));
    await settle(() => !findButton("Save").disabled); findButton("Save").click();
    await settle(() => mounted.state.wire.length === 1);
    if (JSON.parse(mounted.state.wire[0]!).input.ownerId !== owner.actorId) throw Error("Assignee UUID not submitted");
    await settle(() => document.body.textContent!.includes("Confirmed saved."));
    return 7;
  } finally { mounted.view.destroy(); mounted.client.close(); }
}

// Actual DOM rendering with synthetic transport; current product journey is separate.
export async function runDependencyViewProbe(boardInput: BoardRead, sessionInput: SessionRead): Promise<number> {
  const input = structuredClone(boardInput), identity = input.result.identity;
  const alpha = { ...input.result.cards[0]!, ticket: 1, title: "Alpha <img src=x onerror=alert(1)>", priority: "P3" as const, lane: "Ready for Review" as const, currentApprovalId: null, revision: 4, archived: false };
  const beta = { ...alpha, cardId: "00000000-0000-4000-8000-000000000063", ticket: 2, title: "Beta", priority: "P0" as const, lane: "Ready for Work" as const };
  Object.assign(input.result, { cards: [alpha, beta], links: [{ active: true, cardId: beta.cardId, target: { ...identity, cardId: alpha.cardId }, kind: "blocking", linkId: "00000000-0000-4000-8000-000000000064", createdIn: identity, createdAt: "2026-10-01T00:00:00Z" }], approvals: [] });
  const mounted = await mountSyntheticBoard(input, sessionInput);
  let checks = 0;
  const check = (condition: boolean, message: string): void => { if (!condition) throw Error(message); checks++; };
  const articles = (): HTMLElement[] => [...document.querySelectorAll<HTMLElement>('section[aria-label="Cards"] > article')];
  try {
    check(articles()[0]!.querySelector('[aria-label="Dependency pressure"]')!.textContent!.includes("Dependency pressure: #2 Beta (P0; Ready for Work)"), "Pressure section missing");
    check(articles()[1]!.querySelector('[aria-label="Prerequisites/readiness"]')!.textContent!.includes("awaiting human acceptance (not satisfied)"), "RFR readiness missing");
    check(document.querySelector("img") === null && document.body.textContent!.includes(alpha.title), "Advisory prose executed markup");
    const edit = articles()[1]!.querySelector<HTMLButtonElement>("button")!; edit.click();
    const title = document.querySelector<HTMLInputElement>('input[name="title"]')!;
    title.value = "Keep my draft"; title.dispatchEvent(new Event("input", { bubbles: true }));
    const currentAlpha = mounted.board.result.cards[0]!, human = mounted.board.result.actors.find(actor => actor.kind === "human")!;
    Object.assign(currentAlpha, { lane: "Completed", currentApprovalId: "00000000-0000-4000-8000-000000000065", revision: 5 });
    Object.assign(mounted.board.result, { approvals: [{ approvalId: currentAlpha.currentApprovalId, cardId: alpha.cardId, actorId: human.actorId, policyVersion: mounted.board.result.policyVersion, reviewedCardRevision: 4, reviewedBoardRevision: mounted.board.result.revision, createdIn: identity, createdAt: "2026-10-01T00:00:00Z" }] });
    await mounted.view.refresh();
    check(articles()[1]!.textContent!.includes("Prerequisites satisfied (advisory; server rechecks at deliberate start)."), "Accepted refresh not reflected");
    check(title === document.querySelector('input[name="title"]') && title.value === "Keep my draft", "Advisory refresh replaced draft");
    mounted.state.readUnavailable = true; await mounted.view.refresh();
    check(document.body.textContent!.includes("Unavailable; displayed board is stale.") && articles()[1]!.textContent!.includes("Prerequisites satisfied"), "Stale disclosure or old view lost");
    mounted.state.readUnavailable = false; Object.assign(mounted.board.result, { links: [] }); await mounted.view.refresh();
    check(articles()[0]!.textContent!.includes("Dependency pressure: none.") && currentAlpha.priority === "P3", "Unlink retained pressure or changed priority");
    check(mounted.state.mutations === 0, "Advisory performed a mutation");
    return checks;
  } finally { mounted.view.destroy(); mounted.client.close(); }
}
