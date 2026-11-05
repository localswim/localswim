import { BoardClient } from "./client.js";
import { configuredWorkflowFixture } from "../board/workflow.test.js";
import { emptySearch } from "./search.js";
import type { BoardRead, SessionRead, WorkflowPreview, WorkflowPreviewRequest } from "../../generated/transport/contracts.js";

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
    response = { ...board, result: { ...board.result, policyVersion: 2, workflow: undefined, workflowPolicies: undefined } };
    await rejects(() => client.board(board.result.identity.boardId));
    response = { ...board, result: { ...board.result, policyVersion: 2 } };
    await rejects(() => client.board(board.result.identity.boardId));
    return count;
  } finally { client.close(); }
}


export async function runWorkflowPreviewContextProbe(input: BoardRead): Promise<number> {
  const current = configuredWorkflowFixture(), candidate = structuredClone(current);
  Object.assign(candidate, { revision: 2, lanes: current.lanes.map((lane, index) => index === 0 ? { ...lane, label: "Changed" } : lane) });
  const request: WorkflowPreviewRequest = { apiVersion: 1, boardId: input.result.identity.boardId, incarnation: input.result.identity.incarnation, expectedRevision: 5, expectedPolicyVersion: 1, workflow: candidate };
  const preview: WorkflowPreview = { apiVersion: 1, boardId: request.boardId, incarnation: request.incarnation, revision: 5, policyVersion: 1, currentWorkflow: current, candidate, wouldChange: true, nextPolicyVersion: 2,
    changes: { renamed: [{ laneId: current.lanes[0]!.id, before: current.lanes[0]!.label, after: "Changed" }], orderChanged: false, added: [], retired: [], transitionsChanged: false, rights: { human: { added: [], removed: [] }, automation: { added: [], removed: [] } } },
    occupancy: current.lanes.map(lane => ({ laneId: lane.id, currentCards: 0, archivedCards: 0 })).sort((a,b) => a.laneId < b.laneId ? -1 : 1),
    approval: { preservedCurrentCount: 0, rule: "retained-issuing-policy" }, capacity: { policyRecords: 1, policyRecordsAfter: 2, canonicalPolicyBytesAfter: 1000, maxPolicyRecords: 128, maxCanonicalPolicyBytes: 8388608 } };
  let response = structuredClone(preview), count = 0;
  const client = await BoardClient.open({ installationId: "00000000-0000-4000-8000-000000000090", profileId: "synthetic-workflow-preview", credential: () => "synthetic", fetch: (async () => new Response(JSON.stringify(response))) as typeof fetch });
  try {
    await client.previewWorkflow(request); count++;
    const mutations: Array<(value: WorkflowPreview) => void> = [
      value => Object.assign(value, { occupancy: [] }),
      value => Object.assign(value, { nextPolicyVersion: 99 }),
      value => Object.assign(value.capacity, { policyRecordsAfter: 128 }),
      value => Object.assign(value.capacity, { policyRecords: 2 }),
      value => Object.assign(value, { wouldChange: false }),
      value => Object.assign(value.changes, { renamed: [] }),
    ];
    for (const mutate of mutations) {
      response = structuredClone(preview); mutate(response);
      let rejected = false; try { await client.previewWorkflow(request); } catch { rejected = true; }
      if (!rejected) throw Error("Contradictory workflow preview accepted"); count++;
    }
    return count;
  } finally { client.close(); }
}
