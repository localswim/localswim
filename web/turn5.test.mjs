import assert from "node:assert/strict";
import test from "node:test";
import { ApiError, createBrowserApi } from "./api.mjs";

const prefix = "/api/2026-11-10";
const approvalId = "cda7b0d0-26d5-7632-859c-fc9af8ccb1f5";
const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const reviewLane = "ceab10a8-1492-4c90-aa07-13b83138919a";
const completedLane = "7e90e6f9-e7eb-45c8-808e-e5f49b23c259";
const cardIds = ["d2ef6295-ace1-4e6e-bbf7-b1c1823f56fc", "163b1fa0-2a32-424b-8489-6bbd7761201c"];
const actor = "84b4ae21-e224-4f5a-9fca-9abf8e0c86bc";
const attachmentId = "f7e04b53-cfd9-417c-af8e-b6316af0a085";
const at = "2026-11-10 12:00:00+00:00";
const tag = (id, version) => '"' + id + "-" + version + '"';
const boardTag = tag(boardId, 8);
const committedTag = tag(boardId, 9);
const cards = cardIds.map((id, index) => ({ id, revision: index + 4 }));
const auditIds = [approvalId, "84236db3-7230-79fd-a1d6-94b50282e1ef",
  "df8eb525-06d2-785a-8535-af1919969b0e", "c28dd74e-b5c1-7b9a-bb3f-c90349455853"];
const commentIds = ["f9fb5025-5111-4f5f-9ee5-2b0b9e8f1574", "71a8c2c7-2107-493c-a13c-c5d16e8cc175"];
const revision = { board_id: boardId, at, actor_id: actor, revision: 9 };
const changes = cardIds.flatMap((cardId, index) => [
  { ...revision, id: auditIds[index * 2], operation: "approve", resource_type: "card", resource_id: cardId,
    field: "lane_id", before: reviewLane, after: completedLane },
  { ...revision, id: auditIds[index * 2 + 1], operation: "comment", resource_type: "comment",
    resource_id: commentIds[index], field: null, before: null, after: null },
]);
const receipt = { id: approvalId, board_id: boardId, card_ids: cardIds, actor_id: actor,
  at, board_revision: 9, changes };
const approvalPath = prefix + "/approvals";
const client = (fetchImpl) => createBrowserApi({ getCapability: () => "test-capability", fetchImpl });

test("lane approval submits every original card version without a preflight read", async () => {
  let requests = 0;
  const api = client(async (path, request) => {
    requests++;
    if (request.method === "GET") {
      assert.equal(path, approvalPath + "/" + approvalId);
      return Response.json(receipt, { headers: { ETag: tag(approvalId, 1) } });
    }
    assert.equal(path, approvalPath);
    assert.equal(request.method, "POST");
    assert.deepEqual(JSON.parse(request.body), { id: approvalId, board_id: boardId, lane_id: reviewLane,
      expected_board_etag: boardTag, cards: cards.map(({ id, revision }) => ({ card_id: id, etag: tag(id, revision) })) });
    return Response.json(receipt, { status: 201,
      headers: { Location: approvalPath + "/" + approvalId, "X-Board-ETag": committedTag } });
  });
  await api.approveLane(approvalId, boardId, boardTag, reviewLane, completedLane, cards);
  assert.equal(requests, 1);
  await api.inspectLaneApproval(approvalId, boardId, boardTag, reviewLane, completedLane, cardIds);
  assert.equal(requests, 2);
});

test("lane approval refuses stale guards and flags invalid successful receipts without replay", async () => {
  for (const response of [
    new Response(null, { status: 412 }),
    Response.json({ ...receipt, card_ids: [...cardIds].reverse() }, { status: 201,
      headers: { Location: approvalPath + "/" + approvalId, "X-Board-ETag": committedTag } }),
    Response.json({ ...receipt, changes: [...changes.slice(0, 3), { ...changes[3], revision: 8 }] },
      { status: 201, headers: { Location: approvalPath + "/" + approvalId, "X-Board-ETag": committedTag } }),
  ]) {
    let calls = 0;
    const api = client(async () => { calls++; return response; });
    await assert.rejects(api.approveLane(approvalId, boardId, boardTag, reviewLane, completedLane, cards),
      (error) => error instanceof ApiError && error.status === response.status
        && error.outcomeUnknown === (response.status === 201));
    assert.equal(calls, 1);
  }
});

test("lane approval receipts bind the original revision, Completed lane, and focused validator", async () => {
  const wrongLane = { ...receipt, changes: changes.map((change) =>
    change.operation === "approve" ? { ...change, after: attachmentId } : change) };
  const wrongRevision = { ...receipt, board_revision: 10, changes: changes.map((change) =>
    ({ ...change, revision: 10 })) };
  for (const [body, etag, boardEtag] of [
    [wrongLane, tag(approvalId, 1), committedTag],
    [wrongRevision, tag(approvalId, 1), tag(boardId, 10)],
    [receipt, tag(approvalId, 2), committedTag],
  ]) {
    let calls = 0;
    const api = client(async (path, request) => {
      calls++;
      if (request.method === "GET") {
        assert.equal(path, approvalPath + "/" + approvalId);
        return Response.json(body, { headers: { ETag: etag } });
      }
      assert.equal(request.method, "POST");
      return Response.json(body, { status: 201,
        headers: { Location: approvalPath + "/" + approvalId, "X-Board-ETag": boardEtag } });
    });
    if (body !== receipt) {
      await assert.rejects(api.approveLane(approvalId, boardId, boardTag, reviewLane, completedLane, cards),
        (error) => error instanceof ApiError && error.outcomeUnknown);
      assert.equal(calls, 1);
    }
    await assert.rejects(api.inspectLaneApproval(approvalId, boardId, boardTag, reviewLane, completedLane, cardIds),
      (error) => error instanceof ApiError && !error.outcomeUnknown);
    assert.equal(calls, body === receipt ? 1 : 2);
  }
});

test("empty approval accepts the unchanged no-op response", async () => {
  const api = client(async (path, request) => {
    assert.equal(path, approvalPath);
    assert.equal(request.method, "POST");
    assert.deepEqual(JSON.parse(request.body).cards, []);
    return Response.json({ board_id: boardId, board_revision: 8, card_ids: [], changed: false },
      { headers: { "X-Board-ETag": boardTag } });
  });
  await api.approveLane(approvalId, boardId, boardTag, reviewLane, completedLane, []);
});

test("lane settings and detach preserve original board and resource validators", async () => {
  const lane = { id: reviewLane, board_id: boardId, revision: 9, created_at: at, updated_at: at,
    key: "ready_for_review", label: "Ready for Review", position: 3, role: "ready_for_review",
    retired: true, approval_enabled: false, reorder_enabled: false };
  let calls = 0;
  const api = client(async (path, request) => {
    calls++;
    if (request.method === "PATCH") {
      assert.equal(path, prefix + "/lanes/" + reviewLane);
      assert.equal(request.headers.get("If-Match"), tag(reviewLane, 3));
      assert.deepEqual(JSON.parse(request.body), { board_etag: boardTag, retired: true });
      return Response.json(lane, { headers: { ETag: tag(reviewLane, 9) } });
    }
    assert.equal(path, prefix + "/attachments/" + attachmentId);
    assert.equal(request.method, "DELETE");
    assert.equal(request.headers.get("If-Match"), tag(attachmentId, 6));
    assert.equal(request.headers.get("X-Expected-Board-ETag"), committedTag);
    assert.equal(request.body, undefined);
    return new Response(null, { status: 204, headers: { "X-Board-ETag": tag(boardId, 10) } });
  });
  await api.updateLaneSettings(boardId, reviewLane, 3, boardTag, { retired: true });
  await api.detachAttachment(attachmentId, 6, committedTag);
  assert.equal(calls, 2);
});

test("focused history sends a full-activity ticket read with both UTC bounds", async () => {
  const before = "0001-01-01 00:00:00+00:00";
  const after = "9999-12-31 23:59:59+00:00";
  const api = client(async (path, request) => {
    const url = new URL(path, "http://localhost");
    assert.equal(request.method, "GET");
    assert.equal(url.pathname, prefix + "/activities");
    assert.deepEqual(Object.fromEntries(url.searchParams), {
      board_id: boardId, from: before, to: after, view: "full", ticket: "42", cursor: "next",
    });
    return Response.json({ activities: [], next_cursor: null });
  });
  await api.listCardActivity(boardId, before, after, 42, "next");
});
