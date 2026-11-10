import assert from "node:assert/strict";
import test from "node:test";
import { ApiError, createBrowserApi, openBrowserSession } from "./api.mjs";

const cardId = "7133bef8-ef9e-4878-a1d9-53b33575110f";
const resourceId = "466eeb0d-57fc-44ae-8ecc-79a21fe7358e";
const tag = '"card_0123456789abcdef"';
const capability = "A".repeat(43);


const savedCard = {
  id: cardId, revision: 1, created_at: "2026-11-10 18:00:00+00:00",
  updated_at: "2026-11-10 18:00:00+00:00", board_id: resourceId, ticket: 1,
  title: "Exact title", description: "", lane_id: resourceId, owner_id: resourceId,
  priority_id: resourceId, parent_id: null, child_ids: [], archived: false,
  cover_attachment_id: null, comment_count: 0, attachment_count: 0,
  blocking_depth: 0, blocked_card_count: 0, triage_score: 100, completed_at: null,
};

test("bootstrap requests only the same-origin Human session", async () => {
  let requests = 0;
  const token = await openBrowserSession(async (url, options) => {
    ++requests;
    assert.equal(url, "/api/2026-11-10/browser-session");
    assert.equal(options.method, "POST");
    assert.equal(options.mode, "same-origin");
    assert.equal(options.credentials, "omit");
    assert.equal(options.redirect, "error");
    assert.equal(options.cache, "no-store");
    assert.equal(options.body, undefined);
    assert.equal(options.headers, undefined);
    return Response.json({
      principal_id: resourceId, kind: "human", api_version: "2026-11-10", access_token: capability,
      expires_on_service_replacement: true, workspace_admin: false,
    });
  });
  assert.equal(token, capability);
  assert.equal(requests, 1);
});

test("card edits forward the original card ETag and never repeat a failed write", async () => {
  for (const [mode, status, unknown] of [["conflict", 412, false], ["transport", null, true], ["malformed", 200, true]]) {
    let calls = 0;
    const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (url, options) => {
      ++calls;
      assert.equal(url, "/api/2026-11-10/cards/" + cardId);
      assert.equal(options.method, "PATCH");
      assert.equal(options.headers.get("If-Match"), tag);
      assert.deepEqual(JSON.parse(options.body), { title: "Draft" });
      if (mode === "transport") throw new Error("connection lost");
      return Response.json({}, { status, headers: { ETag: tag } });
    } });
    await assert.rejects(api.patchCard(cardId, tag, { title: "Draft" }),
      (error) => error instanceof ApiError && error.status === status && error.outcomeUnknown === unknown);
    assert.equal(calls, 1);
  }
});

test("comment and multipart upload carry caller IDs and original card ETags", async () => {
  const requests = [];
  const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (url, options) => {
    requests.push([url, options]);
    return Response.json({ id: resourceId }, { status: 201, headers: { ETag: tag } });
  } });
  await api.createComment(cardId, tag, "Comment", resourceId);
  await api.uploadAttachment(cardId, tag, new File(["hello"], "test.txt"), resourceId);
  const [[commentUrl, comment], [uploadUrl, upload]] = requests;
  assert.equal(commentUrl, "/api/2026-11-10/comments");
  assert.equal(uploadUrl, "/api/2026-11-10/attachments");
  assert.deepEqual(JSON.parse(comment.body),
    { card_id: cardId, expected_card_etag: tag, text: "Comment", id: resourceId });
  assert.equal(upload.headers.has("Content-Type"), false);
  assert.equal(upload.body.get("card_id"), cardId);
  assert.equal(upload.body.get("expected_card_etag"), tag);
  assert.equal(upload.body.get("id"), resourceId);
  assert.equal(upload.body.get("file").name, "test.txt");
  assert.equal(await upload.body.get("file").text(), "hello");
});

test("card comment reads keep the required board scope", async () => {
  let calls = 0;
  const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (url) => {
    ++calls;
    assert.equal(url, "/api/2026-11-10/comments?board_id=" + resourceId + "&card_id=" + cardId);
    return Response.json({ comments: [], next_cursor: null });
  } });
  await api.listComments({ board_id: resourceId, card_id: cardId });
  assert.equal(calls, 1);
});

test("a created comment or upload must identify the caller's submission", async () => {
  let calls = 0;
  const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async () => {
    ++calls;
    return Response.json({}, { status: 201, headers: { ETag: tag } });
  } });
  const uncertain = (error) => error instanceof ApiError && error.status === 201 && error.outcomeUnknown;
  await assert.rejects(api.createComment(cardId, tag, "Comment", resourceId), uncertain);
  await assert.rejects(api.uploadAttachment(cardId, tag, new File(["hello"], "test.txt"), resourceId), uncertain);
  assert.equal(calls, 2);
});

test("attachment readback targets the original submission ID", async () => {
  let calls = 0;
  const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (url, options) => {
    ++calls;
    assert.equal(url, "/api/2026-11-10/attachments/" + resourceId);
    assert.equal(options.method, "GET");
    return Response.json({ id: resourceId, card_id: cardId });
  } });
  assert.equal((await api.getAttachment(resourceId)).data.id, resourceId);
  assert.equal(calls, 1);
});

test("owner edits reject clearing before dispatch", async () => {
  let calls = 0;
  const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async () => {
    ++calls;
    return Response.json({}, { headers: { ETag: tag } });
  } });
  for (const owner_id of [null, "", undefined, "invalid-id"]) {
    await assert.rejects(api.patchCard(cardId, tag, { owner_id }), TypeError);
  }
  assert.equal(calls, 0);
});

test("owner assignment validates the saved owner without repeating an uncertain write", async () => {
  const saved = savedCard;
  for (const owner_id of [resourceId, null]) {
    let calls = 0;
    const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (url, options) => {
      ++calls;
      assert.equal(options.headers.get("If-Match"), tag);
      assert.deepEqual(JSON.parse(options.body), { owner_id: resourceId });
      return Response.json({ ...saved, owner_id }, { headers: { ETag: tag } });
    } });
    const assignment = api.patchCard(cardId, tag, { owner_id: resourceId });
    if (owner_id === null) {
      await assert.rejects(assignment, {
        name: "ApiError", code: "invalid_response", status: 200, outcomeUnknown: true,
      });
    } else {
      assert.deepEqual((await assignment).data, saved);
    }
    assert.equal(calls, 1);
  }
});


test("card reads and writes require numeric revisions and retain HTTP validators", async () => {
  const newerTag = '"unrelated_to_numeric_revision"';
  const cases = [
    [savedCard, newerTag, true],
    [{ ...savedCard, revision: Number.MAX_SAFE_INTEGER }, newerTag, true],
    ...[undefined, null, false, 0, -1, 1.5, "1", Number.MAX_SAFE_INTEGER + 1]
      .map((revision) => [{ ...savedCard, revision }, newerTag, false]),
    [{ ...savedCard, revision: undefined, etag: tag }, newerTag, false],
    [{ ...savedCard, etag: tag }, newerTag, false],
    [{ ...savedCard, id: resourceId }, newerTag, false],
    [savedCard, null, false],
    [savedCard, "W/" + tag, false],
  ];
  for (const method of ["GET", "PATCH"]) {
    for (const [data, responseTag, valid] of cases) {
      let calls = 0;
      const api = createBrowserApi({ getCapability: () => capability, fetchImpl: async (url, options) => {
        ++calls;
        assert.equal(options.method, method);
        if (method === "PATCH") assert.equal(options.headers.get("If-Match"), tag);
        return Response.json(data, { headers: responseTag === null ? {} : { ETag: responseTag } });
      } });
      const result = method === "GET" ? api.getCard(cardId) : api.patchCard(cardId, tag, { title: "Exact title" });
      if (valid) {
        const reply = await result;
        assert.deepEqual(reply.data, data);
        assert.equal(reply.etag, responseTag);
      } else {
        await assert.rejects(result, {
          name: "ApiError", code: "invalid_response", status: 200, outcomeUnknown: method === "PATCH",
        });
      }
      assert.equal(calls, 1);
    }
  }
});
