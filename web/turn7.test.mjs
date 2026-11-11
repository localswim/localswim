import assert from "node:assert/strict";
import test from "node:test";
import { ApiError, createBrowserApi } from "./api.mjs";
const board = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const lane = "ceab10a8-1492-4c90-aa07-13b83138919a";
const first = "d2ef6295-ace1-4e6e-bbf7-b1c1823f56fc";
const second = "163b1fa0-2a32-424b-8489-6bbd7761201c";
const backup = "f7e04b53-cfd9-417c-af8e-b6316af0a085";
const publication = "84b4ae21-e224-4f5a-9fca-9abf8e0c86bc";
const at = "2026-11-10 12:00:00+00:00";
const tag = (id, revision) => `"${id}-${revision}"`;
const client = (fetchImpl) => createBrowserApi({ getCapability: () => "test-capability", fetchImpl });
const proposed = [{card_id: second, ticket: 1}, {card_id: first, ticket: 2}];
const current = [{card_id: first, ticket: 1}, {card_id: second, ticket: 2}];
const order = { board_id: board, lane_id: lane, board_revision: 4, selected_card_ids: [first, second], current, proposed, changed: true };
const published = { id: publication, board_id: board, workspace: false, state: "succeeded", created_at: at,
  finished_at: at, backup_id: backup, error_code: null };

test("search preserves literal UTF-8 query and explicit content/triage/lane/limit filters", async () => {
  const api = client(async (path, request) => {
    const url = new URL(path, "http://localhost");
    assert.equal(request.method, "GET");
    assert.equal(url.searchParams.get("q"), "#12 + café");
    assert.equal(url.searchParams.get("search_content"), "true");
    assert.equal(url.searchParams.get("sort"), "triage");
    assert.equal(url.searchParams.get("lane_id"), lane);
    assert.equal(url.searchParams.get("limit"), "2");
    return Response.json({ cards: [], next_cursor: null });
  });
  await api.listCards(board, { q: "#12 + café", searchContent: true, sort: "triage", laneId: lane, limit: 2 });
});

test("relationship lookup keeps default ticket order and refuses invalid explicit filters locally", async () => {
  let calls = 0;
  const api = client(async (path) => {
    calls++;
    const url = new URL(path, "http://localhost");
    assert.equal(url.searchParams.has("sort"), false);
    assert.equal(url.searchParams.get("ticket"), "12");
    return Response.json({ cards: [], next_cursor: null });
  });
  await api.listCards(board, { ticket: 12 });
  for (const filters of [{sort: "invalid"}, {limit: 0}, {limit: true}, {limit: 201}, {laneId: "invalid"}]) {
    assert.throws(() => api.listCards(board, filters), TypeError);
  }
  assert.equal(calls, 1);
});

test("apply uses captured preview validator and ordered proposed UUIDs in one PUT", async () => {
  let calls = 0;
  const api = client(async (path, request) => {
    calls++;
    assert.equal(path, `/api/2026-11-10/ticket-orders/${lane}`);
    assert.equal(request.method, "PUT");
    assert.deepEqual(JSON.parse(request.body), {board_id: board, expected_board_etag: tag(board, 3), card_ids: [second, first]});
    return Response.json(order, {headers: {"X-Board-ETag": tag(board, 4)}});
  });
  assert.equal((await api.applyTicketOrder(board, lane, tag(board, 3), proposed)).data.changed, true);
  assert.equal(calls, 1);
});

test("apply stale refusal and malformed successes never replay", async () => {
  for (const response of [new Response(null, {status: 412}), new Response("broken"),
    Response.json({...order, proposed: current}, {headers: {"X-Board-ETag": tag(board, 4)}}),
    Response.json({...order, selected_card_ids: [first, first]}, {headers: {"X-Board-ETag": tag(board, 4)}})]) {
    let calls = 0;
    const api = client(async () => { calls++; return response; });
    await assert.rejects(api.applyTicketOrder(board, lane, tag(board, 3), proposed),
      (error) => error instanceof ApiError && (error.status === 412 ? !error.outcomeUnknown : error.outcomeUnknown));
    assert.equal(calls, 1);
  }
});

test("restore sends raw ZIP with snapshot validator and validates target board/revision", async () => {
  const file = new Blob([new Uint8Array([80,75,3,4])], {type: "application/zip"});
  const api = client(async (path, request) => {
    assert.equal(path, `/api/2026-11-10/snapshots/${board}`);
    assert.equal(request.method, "PUT");
    assert.equal(request.headers.get("If-Match"), tag(board, 3));
    assert.equal(request.headers.get("Content-Type"), "application/zip");
    assert.equal(request.body, file);
    return Response.json({id: board, revision: 4}, {headers: {"X-Board-ETag": tag(board, 4)}});
  });
  assert.equal((await api.restoreSnapshot(board, tag(board, 3), file)).data.id, board);
});

test("damaged restore refusals and lost responses remain single attempt", async () => {
  for (const response of [new Response(null, {status: 400}), new Response("broken"),
    Response.json({id: second, revision: 4}, {headers: {"X-Board-ETag": tag(board, 4)}})]) {
    let calls = 0;
    const api = client(async () => { calls++; return response; });
    await assert.rejects(api.restoreSnapshot(board, tag(board, 3), new Blob()),
      (error) => error instanceof ApiError && (error.status === 400 ? !error.outcomeUnknown : error.outcomeUnknown));
    assert.equal(calls, 1);
  }
});

test("publish keeps original backup and publication UUID for focused readback", async () => {
  let writes = 0;
  const api = client(async (path, request) => {
    if (request.method === "POST") {
      writes++;
      assert.equal(path, "/api/2026-11-10/publications");
      assert.deepEqual(JSON.parse(request.body), {id: publication, board_id: board, backup_id: backup});
      return Response.json(published, {status: 202, headers: {Location: `/api/2026-11-10/publications/${publication}`}});
    }
    assert.equal(path, `/api/2026-11-10/publications/${publication}`);
    return Response.json(published, {headers: {ETag: tag(publication, 1)}});
  });
  await api.publishBackup(publication, board, backup);
  assert.equal((await api.getPublication(publication, board, backup)).data.state, "succeeded");
  assert.equal(writes, 1);
});

test("unusable publication success requires inspection with no replay", async () => {
  for (const response of [new Response("broken", {status: 202}), Response.json({...published, backup_id: second},
    {status: 202, headers: {Location: `/api/2026-11-10/publications/${publication}`}})]) {
    let calls = 0;
    const api = client(async () => { calls++; return response; });
    await assert.rejects(api.publishBackup(publication, board, backup),
      (error) => error instanceof ApiError && error.outcomeUnknown);
    assert.equal(calls, 1);
  }
});
