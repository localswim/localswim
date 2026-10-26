// Synthetic fixture only. This assertion never qualifies a product profile.
import type { BoardRead, SessionRead, Terminal } from "../../generated/transport/contracts";
import { BoardClient } from "../api/client";
import { prepare } from "../api/boundary";
import type { BoardCommand } from "../api/boundary";
import { BoardView } from "./view";

export async function mountSyntheticBoard(boardInput: BoardRead, sessionInput: SessionRead, qualified = true, databaseName = "w02-test-" + crypto.randomUUID()) {
  const board = structuredClone(boardInput), session = structuredClone(sessionInput);
  const auth: SessionRead = { ...session, grant: { ...sessionInput.grant, rights: ["approve", "comment", "create", "edit", "read", "read-own-outcome", "review-handoff"] } };
  const installationId = "00000000-0000-4000-8000-000000000090";
  const state = { mode: "commit", mutations: 0, lookups: 0, releases: [] as (() => void)[], wire: [] as string[], readUnavailable: false, terminals: new Map<string, Terminal>() };
  const client = await BoardClient.open({ installationId, profileId: "synthetic-only", credential: () => "synthetic-ephemeral-token",
    ...(qualified ? { qualification: { installationId, profileId: "synthetic-only", strictDurabilityQualified: true as const, evidenceReference: "synthetic browser component test only" } } : {}),
    databaseName,
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
          currentApprovalId: null, description: request.input.description, lane: "Ready for Work", linkIds: [], priority: request.input.priority ?? "P3", revision: 1, ticket: board.result.cards.length + 1, title: request.input.title };
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
