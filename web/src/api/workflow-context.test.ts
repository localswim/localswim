import { BoardClient } from "./client.js";
import { configuredWorkflowFixture } from "../board/workflow.test.js";
import { emptySearch } from "./search.js";
import type { BoardRead, SessionRead } from "../../generated/transport/contracts.js";

// Controlled transport responses; actual backend evidence is separate.
export async function runWorkflowReadContextProbe(input: BoardRead, session: SessionRead): Promise<number> {
  const board = structuredClone(input), workflow = configuredWorkflowFixture();
  Object.assign(board.result, { workflow });
  for (const card of board.result.cards) Object.assign(card, { lane: workflow.lanes.find(lane => lane.role === "initial")!.id, currentApprovalId: null });
  let response: unknown = board;
  const client = await BoardClient.open({ installationId: "00000000-0000-4000-8000-000000000090", profileId: "synthetic-workflow-read",
    transport: "local-cookie", selection: { mode: "standalone", boardId: board.result.identity.boardId, incarnation: board.result.identity.incarnation, actorId: session.actorId, grantId: session.grant.grantId },
    credential: () => "synthetic", databaseName: "workflow-read-probe-" + crypto.randomUUID(), fetch: (async () => new Response(JSON.stringify(response))) as typeof fetch });
  let count = 0;
  const rejects = async (read: () => Promise<unknown>): Promise<void> => { let refused = false; try { await read(); } catch { refused = true; } if (!refused) throw Error("Incoherent workflow read accepted"); count++; };
  try {
    await client.board(board.result.identity.boardId); count++;
    response = { ...board, result: { ...board.result, workflow: { ...workflow, lanes: workflow.lanes.map((lane, index) => index === 0 ? { ...lane, label: "Changed" } : lane) } } };
    await rejects(() => client.board(board.result.identity.boardId));
    const query = { ...emptySearch(), lane: "00000000-0000-4000-8000-000000000099" };
    response = { apiVersion: 1, scope: "card-search", result: { identity: board.result.identity, revision: board.result.revision, workflow, query, items: [], nextCursor: null } };
    await rejects(() => client.search(board.result.identity.boardId, query));
    const validQuery = emptySearch();
    response = { apiVersion: 1, scope: "card-search", result: { identity: board.result.identity, revision: board.result.revision, query: validQuery, items: [], nextCursor: null } };
    await rejects(() => client.search(board.result.identity.boardId, validQuery));
    return count;
  } finally { client.close(); }
}
