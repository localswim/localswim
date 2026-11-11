import assert from "node:assert/strict";
import test from "node:test";
import { ApiError, createBrowserApi } from "./api.mjs";

const boardId = "7133bef8-ef9e-4878-a1d9-53b33575110f";
const cardId = "466eeb0d-57fc-44ae-8ecc-79a21fe7358e";
const otherCard = "f86b1842-a0b3-47f2-a536-6a9583491946";
const approvalId = "9908fb8f-f6b9-77ed-aa92-5650cd120602";
const actorId = "22e1ffb8-7d2a-43ed-a8cf-8871165a98e9";
const cardTag = '"card_0123456789abcdef"';
const boardTag = '"board_0123456789abcdef"';
const updatedBoardTag = `"${boardId}-4"`;
const capability = "A".repeat(43);
const prefix = "/api/2026-11-10";
const at = "2026-11-10 16:55:00+00:00";
const id = "9908fb8f-f6b9-47ed-aa92-5650cd120602";
const relationship = { id, board_id: boardId, revision: 7, created_at: at, updated_at: at,
  source_card_id: cardId, target_card_id: otherCard, kind: "blocks" };
const approvalMove = { id: approvalId, board_id: boardId, revision: 4, at, actor_id: actorId,
  operation: "approve", resource_type: "card", resource_id: cardId, field: "lane_id",
  before: "c5d47d1d-17a5-4b6a-9db6-5878c4013d2d", after: "3c27731a-d7b8-48aa-a57e-1da339b8160e" };
const approvalNote = { id: "988428d1-691b-7404-91e5-ff78694cde65", board_id: boardId, revision: 4, at,
  actor_id: actorId, operation: "comment", resource_type: "comment", resource_id: otherCard,
  field: null, before: null, after: null };
const approval = { id: approvalId, board_id: boardId, card_ids: [cardId], actor_id: actorId,
  at, board_revision: 4, changes: [approvalMove, approvalNote] };

test("a focused approval sends the board and card versions and validates its receipt", async () => {
  const calls = [];
  const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (url, options) => {
    calls.push([url, options]);
    if (options.method === "POST") return Response.json(approval, { status: 201,
      headers: { Location: prefix + "/approvals/" + approvalId, "X-Board-ETag": updatedBoardTag } });
    return Response.json(approval, { headers: { ETag: cardTag } });
  } });
  await api.approveCard(approvalId, boardId, boardTag, cardId, cardTag);
  await api.inspectApproval(approvalId, boardId, cardId);
  assert.equal(calls.length, 2);
  assert.equal(calls[0][0], prefix + "/approvals");
  assert.equal(calls[0][1].method, "POST");
  assert.deepEqual(JSON.parse(calls[0][1].body),
    { id: approvalId, board_id: boardId, expected_board_etag: boardTag, cards: [{ card_id: cardId, etag: cardTag }] });
  assert.equal(calls[1][0], prefix + "/approvals/" + approvalId);
  assert.equal(calls[1][1].method, "GET");
  await assert.rejects(api.inspectApproval(approvalId, boardId, otherCard), (error) =>
    error instanceof ApiError && error.code === "invalid_response" && !error.outcomeUnknown);
});

test("a stale, lost, or wrong-card approval response does not cause a replay", async () => {
  for (const failure of ["stale", "lost", "wrong card"]) {
    let calls = 0;
    const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async () => {
      ++calls;
      if (failure === "lost") throw new Error("Connection lost.");
      if (failure === "stale") return Response.json({}, { status: 412 });
      return Response.json({ ...approval, card_ids: [otherCard] }, { status: 201,
        headers: { Location: prefix + "/approvals/" + approvalId, "X-Board-ETag": updatedBoardTag } });
    } });
    await assert.rejects(api.approveCard(approvalId, boardId, boardTag, cardId, cardTag), (error) =>
      error instanceof ApiError && error.outcomeUnknown === (failure !== "stale")
      && (failure !== "stale" || error.status === 412));
    assert.equal(calls, 1);
  }
});

test("incomplete or inconsistent approval audits cannot confirm a write or readback", async () => {
  for (const changes of [[], [{}, {}], [approvalMove, { ...approvalNote, revision: 5 }],
    [{ ...approvalMove, resource_id: otherCard }, approvalNote]]) {
    const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (_url, options) => {
      const body = { ...approval, changes };
      if (options.method === "POST") return Response.json(body, { status: 201,
        headers: { Location: prefix + "/approvals/" + approvalId, "X-Board-ETag": updatedBoardTag } });
      return Response.json(body, { headers: { ETag: cardTag } });
    } });
    await assert.rejects(api.approveCard(approvalId, boardId, boardTag, cardId, cardTag), (error) =>
      error instanceof ApiError && error.code === "invalid_response" && error.outcomeUnknown);
    await assert.rejects(api.inspectApproval(approvalId, boardId, cardId), (error) =>
      error instanceof ApiError && error.code === "invalid_response" && !error.outcomeUnknown);
  }
});

test("approval receipts require a positive safe board revision matching every change", async () => {
  const { board_revision: _revision, ...missingRevision } = approval;
  const invalid = [missingRevision, { ...missingRevision, board_etag: updatedBoardTag },
    { ...approval, board_etag: updatedBoardTag },
    ...[null, true, "4", 0, -1, 4.5, Number.MAX_SAFE_INTEGER + 1, 5].map((board_revision) =>
      ({ ...approval, board_revision }))];
  for (const body of invalid) {
    let writes = 0;
    const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (_url, options) => {
      if (options.method === "POST") {
        writes++;
        return Response.json(body, { status: 201,
          headers: { Location: prefix + "/approvals/" + approvalId, "X-Board-ETag": updatedBoardTag } });
      }
      return Response.json(body, { headers: { ETag: cardTag } });
    } });
    await assert.rejects(api.approveCard(approvalId, boardId, boardTag, cardId, cardTag), (error) =>
      error instanceof ApiError && error.code === "invalid_response" && error.outcomeUnknown);
    await assert.rejects(api.inspectApproval(approvalId, boardId, cardId), (error) =>
      error instanceof ApiError && error.code === "invalid_response" && !error.outcomeUnknown);
    assert.equal(writes, 1);
  }
});

test("approval receipts accept the largest safe board commit revision", async () => {
  const board_revision = Number.MAX_SAFE_INTEGER;
  const body = { ...approval, board_revision,
    changes: approval.changes.map((change) => ({ ...change, revision: board_revision })) };
  const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (_url, options) =>
    options.method === "POST"
      ? Response.json(body, { status: 201, headers: { Location: prefix + "/approvals/" + approvalId,
        "X-Board-ETag": `"${boardId}-${board_revision}"` } })
      : Response.json(body, { headers: { ETag: cardTag } }) });
  await api.approveCard(approvalId, boardId, boardTag, cardId, cardTag);
  await api.inspectApproval(approvalId, boardId, cardId);
});

test("approval writes require the board header to match the committed receipt revision", async () => {
  for (const tag of [null, boardTag, `"${boardId}-5"`, `"${otherCard}-4"`]) {
    const headers = { Location: prefix + "/approvals/" + approvalId };
    if (tag !== null) headers["X-Board-ETag"] = tag;
    const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async () =>
      Response.json(approval, { status: 201, headers }) });
    await assert.rejects(api.approveCard(approvalId, boardId, boardTag, cardId, cardTag), (error) =>
      error instanceof ApiError && error.code === "invalid_response" && error.outcomeUnknown);
  }
});

test("a lost approval response is recovered by its immutable receipt without another write", async () => {
  const methods = [];
  const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (_url, options) => {
    methods.push(options.method);
    if (options.method === "POST") throw new Error("Connection lost.");
    return Response.json(approval, { headers: { ETag: cardTag } });
  } });
  await assert.rejects(api.approveCard(approvalId, boardId, boardTag, cardId, cardTag), (error) =>
    error instanceof ApiError && error.outcomeUnknown);
  await api.inspectApproval(approvalId, boardId, cardId);
  assert.deepEqual(methods, ["POST", "GET"]);
});

test("archived-card and typed-link reads keep board and card scoping", async () => {
  const calls = [];
  const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (url) => {
    calls.push(url);
    return Response.json({ relationships: [relationship] });
  } });
  await api.listCards(boardId, { archived: "only", cursor: "next" });
  await api.listCards(boardId, { archived: "include", ticket: 4, limit: 2 });
  await api.listRelationships(boardId, cardId, "next");
  assert.deepEqual(calls, [
    prefix + "/cards?board_id=" + boardId + "&archived=only&cursor=next",
    prefix + "/cards?board_id=" + boardId + "&ticket=4&limit=2&archived=include",
    prefix + "/relationships?board_id=" + boardId + "&card_id=" + cardId + "&cursor=next",
  ]);
});

test("typed-link writes send the correct resource and board preconditions", async () => {
  const calls = [];
  const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (url, options) => {
    calls.push([url, options]);
    if (options.method === "DELETE") return new Response(null, { status: 204,
      headers: { "X-Board-ETag": updatedBoardTag } });
    return Response.json(relationship, { status: options.method === "POST" ? 201 : 200, headers: { ETag: cardTag } });
  } });
  await api.createRelationship(id, boardId, boardTag, cardId, otherCard, "blocks");
  await api.replaceRelationship(id, 7, boardTag, "relates_to");
  await api.removeRelationship(id, 7, boardTag);
  assert.deepEqual(calls.map(([url, options]) => [url, options.method]), [
    [prefix + "/relationships", "POST"], [prefix + "/relationships/" + id, "GET"],
    [prefix + "/relationships/" + id, "PATCH"], [prefix + "/relationships/" + id, "GET"],
    [prefix + "/relationships/" + id, "DELETE"],
  ]);
  assert.deepEqual(JSON.parse(calls[0][1].body), { id, board_id: boardId, expected_board_etag: boardTag,
    source_card_id: cardId, target_card_id: otherCard, kind: "blocks" });
  assert.equal(calls[2][1].headers.get("If-Match"), cardTag);
  assert.deepEqual(JSON.parse(calls[2][1].body), { expected_board_etag: boardTag, kind: "relates_to" });
  assert.equal(calls[4][1].headers.get("If-Match"), cardTag);
  assert.equal(calls[4][1].headers.get("X-Expected-Board-ETag"), boardTag);
  assert.equal(calls[4][1].body, undefined);
});

test("authenticated attachment content permits only bounded raster previews and inert download blobs", async () => {
  const data = new Uint8Array([1, 2, 3]);
  for (const [requested, served, type, size, expected] of [
    ["inline", "inline", "image/png", 3, "image/png"],
    ["attachment", "attachment", "text/html", 3, "application/octet-stream"],
    ["inline", "attachment", "application/octet-stream", 3, null],
    ["inline", "inline", "image/svg+xml", 3, null],
    ["inline", "inline", "image/png", 2, null],
    ["attachment", "attachment", "application/octet-stream", 4, null],
  ]) {
    let calls = 0;
    const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (url, options) => {
      ++calls;
      assert.equal(url, prefix + "/attachments/" + id + "/content?disposition=" + requested);
      assert.equal(options.method, "GET");
      assert.equal(options.headers.get("Authorization"), "Bearer " + capability);
      assert.equal(options.mode, "same-origin");
      assert.equal(options.redirect, "error");
      return new Response(data, { headers: { "Content-Type": type, "Content-Disposition": served } });
    } });
    if (expected) {
      const blob = await api.getAttachmentContent(id, requested, size, "039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81");
      assert.equal(blob.type, expected);
      assert.deepEqual(new Uint8Array(await blob.arrayBuffer()), data);
    } else {
      await assert.rejects(api.getAttachmentContent(id, requested, size, "039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81"), (error) =>
        error instanceof ApiError && error.code === "invalid_response" && !error.outcomeUnknown);
    }
    assert.equal(calls, 1);
  }
});


test("relationship revision guards refuse stale or malformed reads before dispatch", async () => {
  for (const action of ["replace", "remove"]) {
    for (const revision of [0, -1, 1.5, "7", null, Number.MAX_SAFE_INTEGER + 1]) {
      let calls = 0;
      const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async () => {
        ++calls;
        return Response.json(relationship, { headers: { ETag: cardTag } });
      } });
      const write = () => action === "replace"
        ? api.replaceRelationship(id, revision, boardTag, "references")
        : api.removeRelationship(id, revision, boardTag);
      await assert.rejects(write, TypeError);
      assert.equal(calls, 0);
    }
    for (const data of [
      { ...relationship, revision: 8 }, { ...relationship, revision: 0 },
      { ...relationship, revision: "7" }, { ...relationship, etag: cardTag },
      { ...relationship, revision: undefined, etag: cardTag },
      { ...relationship, id: otherCard }, { ...relationship, board_id: null },
    ]) {
      let calls = 0;
      const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async () => {
        ++calls;
        return Response.json(data, { headers: { ETag: cardTag } });
      } });
      const write = () => action === "replace"
        ? api.replaceRelationship(id, 7, boardTag, "references")
        : api.removeRelationship(id, 7, boardTag);
      await assert.rejects(write, (error) => error instanceof ApiError && !error.outcomeUnknown);
      assert.equal(calls, 1);
    }
  }
});

test("relationship mutations retain the read validator and never repeat uncertain writes", async () => {
  const freshTag = '"relationship_fresh_00000001"';
  for (const action of ["replace", "remove"]) {
    for (const failure of ["stale", "lost", "invalid"]) {
      let calls = 0;
      const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (_url, options) => {
        ++calls;
        if (options.method === "GET") return Response.json(relationship, { headers: { ETag: freshTag } });
        assert.equal(options.headers.get("If-Match"), freshTag);
        if (failure === "lost") throw new Error("Lost response");
        if (failure === "stale") return Response.json({}, { status: 412 });
        if (action === "remove") return new Response(null, { status: 204 });
        return Response.json({ ...relationship, etag: freshTag }, { headers: { ETag: freshTag } });
      } });
      const write = () => action === "replace"
        ? api.replaceRelationship(id, 7, boardTag, "references")
        : api.removeRelationship(id, 7, boardTag);
      await assert.rejects(write, (error) => error instanceof ApiError
        && error.outcomeUnknown === (failure !== "stale"));
      assert.equal(calls, 2);
    }
  }
});

test("relationship list and create responses require numeric revisions without body etags", async () => {
  for (const malformed of [
    { ...relationship, revision: 0 }, { ...relationship, revision: "7" },
    { ...relationship, etag: cardTag }, { ...relationship, revision: undefined, etag: cardTag },
  ]) {
    const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (_url, options) =>
      Response.json(options.method === "POST" ? malformed : { relationships: [malformed] },
        { status: options.method === "POST" ? 201 : 200, headers: { ETag: cardTag } }) });
    await assert.rejects(api.listRelationships(boardId, cardId), { code: "invalid_response", outcomeUnknown: false });
    await assert.rejects(api.createRelationship(id, boardId, boardTag, cardId, otherCard, "blocks"),
      { code: "invalid_response", outcomeUnknown: true });
  }
});


test("attachment integrity failures never publish download or preview blobs", async () => {
  for (const disposition of ["inline", "attachment"]) {
    let calls = 0;
    const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async () => {
      ++calls;
      return new Response(new Uint8Array([1, 2, 4]), {
        headers: { "Content-Type": "image/png", "Content-Disposition": disposition },
      });
    } });
    await assert.rejects(api.getAttachmentContent(id, disposition, 3,
      "039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81"),
      (error) => error instanceof ApiError && error.code === "invalid_response" && !error.outcomeUnknown);
    assert.equal(calls, 1);
  }
});

test("relationship writes reject inverse kinds before any request", async () => {
  let calls = 0;
  const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async () => { ++calls; throw new Error("unexpected request"); } });
  for (const kind of ["blocked_by", "duplicated_by", "referenced_by"]) {
    await assert.rejects(api.createRelationship(id, boardId, boardTag, cardId, otherCard, kind), TypeError);
    await assert.rejects(api.replaceRelationship(id, 7, boardTag, kind), TypeError);
  }
  assert.equal(calls, 0);
});
