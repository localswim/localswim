// Controlled read-boundary checks; callers supply validated synthetic history.
// These checks do not replace the ordinary executable/browser/CLI journey.
import type { CardRead, HistoryRead } from "../../generated/transport/contracts.js";
import { BoardClient } from "./client.js";

export async function checkHistoryBoundary(fixture: HistoryRead): Promise<string[]> {
  const passed: string[] = [];
  const boardId = fixture.result.identity.boardId, cardId = fixture.result.cardId;
  const other = "00000000-0000-4000-8000-000000000099";
  const rejected = async (run: () => Promise<unknown>, label: string): Promise<void> => {
    let failed = false;
    try { await run(); } catch { failed = true; }
    if (!failed) throw Error(label + " accepted an invalid/unavailable history read");
    passed.push(label);
  };
  async function client(fetcher: typeof fetch, credential: () => string = () => "component-generation"): Promise<BoardClient> {
    return BoardClient.open({ installationId: "00000000-0000-4000-8000-000000000090", profileId: "synthetic-history-boundary",
      databaseName: "history-boundary-" + crypto.randomUUID(), credential, fetch: fetcher,
      transport: "local-cookie", selection: { mode: "workspace", boardId, incarnation: fixture.result.identity.incarnation,
        actorId: "00000000-0000-4000-8000-000000000091", grantId: "00000000-0000-4000-8000-000000000092" } });
  }
  const response = (value: unknown, status = 200): Response => new Response(JSON.stringify(value), { status });
  let calls = 0;
  const good = await client((async (url) => {
    calls++;
    if (String(url) !== "/api/v1/boards/" + boardId + "/cards/" + cardId + "/history") throw Error("Wrong history route");
    return response(fixture);
  }) as typeof fetch);
  try {
    const result = await good.history(boardId, cardId);
    if (result.result.cardId !== cardId || result.result.records.length !== fixture.result.records.length) throw Error("Lost committed facts");
    passed.push("valid-selected-history-route-and-facts");
    const before = calls;
    await rejected(() => good.history(boardId, "ticket-1"), "invalid-card-before-fetch");
    await rejected(() => good.history(other, cardId), "wrong-selected-board-before-fetch");
    if (calls !== before) throw Error("Invalid selection reached fetch");
  } finally { good.close(); }
  for (const [label, mutate] of [
    ["wrong-response-board", (x: HistoryRead) => { Object.assign(x.result.identity, { boardId: other }); }],
    ["wrong-response-incarnation", (x: HistoryRead) => { Object.assign(x.result.identity, { incarnation: other }); }],
    ["wrong-response-card", (x: HistoryRead) => { Object.assign(x.result, { cardId: other }); }],
  ] as const) {
    const value = structuredClone(fixture); mutate(value);
    const c = await client((async () => response(value)) as typeof fetch);
    try { await rejected(() => c.history(boardId, cardId), label); } finally { c.close(); }
  }
  for (const [label, fetcher] of [
    ["invalid-history-schema", (async () => response({ ...fixture, apiVersion: 2 })) as typeof fetch],
    ["unavailable-is-not-empty", (async () => response({}, 503)) as typeof fetch],
    ["malformed-history-json", (async () => new Response("{")) as typeof fetch],
  ] as const) {
    const c = await client(fetcher);
    try { await rejected(() => c.history(boardId, cardId), label); } finally { c.close(); }
  }
  if (!fixture.result.records.length || !fixture.result.comments.length) throw Error("History fixture needs referenced prior facts");
  for (const [label, mutate] of [
    ["foreign-card-snapshot", (x: HistoryRead) => { Object.assign(x.result.records[0]!, { cardId: other }); }],
    ["foreign-origin-snapshot", (x: HistoryRead) => { Object.assign(x.result.records[0]!.origin, { boardId: other }); }],
    ["unknown-snapshot-actor", (x: HistoryRead) => { Object.assign(x.result.records[0]!, { actorId: other }); }],
    ["snapshot-not-prior", (x: HistoryRead) => { Object.assign(x.result.records[0]!, { cardRevision: x.result.cardRevision }); }],
    ["duplicate-snapshot", (x: HistoryRead) => { Object.assign(x.result, { records: [...x.result.records, x.result.records[0]!] }); }],
    ["foreign-comment-owner", (x: HistoryRead) => { Object.assign(x.result.comments[0]!, { cardId: other }); }],
    ["missing-comment-reference", (x: HistoryRead) => { Object.assign(x.result, { comments: x.result.comments.slice(1) }); }],
    ["unknown-comment-actor", (x: HistoryRead) => { Object.assign(x.result.comments[0]!, { actorId: other }); }],
    ["missing-snapshot-reference", (x: HistoryRead) => { Object.assign(x.result.records[0]!.references, { commentIds: [other] }); }],
    ["unknown-snapshot-owner", (x: HistoryRead) => { Object.assign(x.result.records[0]!.state!, { ownerId: other }); }],
    ["duplicate-comment-fact", (x: HistoryRead) => { Object.assign(x.result, { comments: [...x.result.comments, x.result.comments[0]!] }); }],
  ] as const) {
    const value = structuredClone(fixture); mutate(value);
    const c = await client((async () => response(value)) as typeof fetch);
    try { await rejected(() => c.history(boardId, cardId), label); } finally { c.close(); }
  }
  let release!: (value: Response) => void, generation = "first-component-generation";
  const late = await client((() => new Promise<Response>(resolve => { release = resolve; })) as typeof fetch, () => generation);
  try {
    const pending = late.history(boardId, cardId);
    generation = "second-component-generation";
    release(response(fixture));
    await rejected(() => pending, "late-history-generation-refused");
  } finally { late.close(); }
  return passed;
}

export async function checkCurrentBoundary(fixture: CardRead): Promise<string[]> {
  const passed: string[] = [], boardId = fixture.result.identity.boardId, cardId = fixture.result.card.cardId;
  const other = "00000000-0000-4000-8000-000000000099";
  if (!fixture.result.comments.length || !fixture.result.historyReferences.length) throw Error("Current fixture needs comments and prior references");
  const cases = [
    ["foreign-current-comment-owner", (x: CardRead) => { Object.assign(x.result.comments[0]!, { cardId: other }); }],
    ["foreign-current-comment-origin", (x: CardRead) => { Object.assign(x.result.comments[0]!.createdIn, { boardId: other }); }],
    ["missing-current-comment", (x: CardRead) => { Object.assign(x.result, { comments: x.result.comments.slice(1) }); }],
    ["unknown-current-owner", (x: CardRead) => { Object.assign(x.result.card, { ownerId: other }); }],
    ["unknown-current-comment-actor", (x: CardRead) => { Object.assign(x.result.comments[0]!, { actorId: other }); }],
    ["foreign-current-card-origin", (x: CardRead) => { Object.assign(x.result.card.createdIn, { boardId: other }); }],
    ["current-history-not-prior", (x: CardRead) => { Object.assign(x.result.historyReferences[0]!, { cardRevision: x.result.card.revision }); }],
    ["current-history-wrong-card", (x: CardRead) => { Object.assign(x.result.historyReferences[0]!, { cardId: other }); }],
  ] as const;
  for (const [label, mutate] of [["valid-current-facts", undefined], ...cases] as const) {
    const value = structuredClone(fixture); mutate?.(value);
    const client = await BoardClient.open({ installationId: "00000000-0000-4000-8000-000000000090", profileId: "synthetic-current-boundary",
      databaseName: "current-boundary-" + crypto.randomUUID(), credential: () => "component-generation",
      transport: "local-cookie", selection: { mode: "workspace", boardId, incarnation: fixture.result.identity.incarnation,
        actorId: "00000000-0000-4000-8000-000000000091", grantId: "00000000-0000-4000-8000-000000000092" },
      fetch: (async () => new Response(JSON.stringify(value))) as typeof fetch });
    let failed = false;
    try { await client.card(boardId, cardId); } catch { failed = true; } finally { client.close(); }
    if (failed !== (mutate !== undefined)) throw Error(label + " did not enforce current fact membership");
    passed.push(label);
  }
  return passed;
}
