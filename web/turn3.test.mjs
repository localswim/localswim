import assert from "node:assert/strict";
import test from "node:test";
import { ApiError, createBrowserApi, openBrowserSession } from "./api.mjs";

const cardId = "7133bef8-ef9e-4878-a1d9-53b33575110f";
const resourceId = "466eeb0d-57fc-44ae-8ecc-79a21fe7358e";
const tag = '"card_0123456789abcdef"';
const capability = "A".repeat(43);

const savedComment = {
  id: resourceId,
  created_at: "2026-11-10 18:00:00+00:00",
  board_id: resourceId,
  card_id: cardId,
  text: "Comment",
  author_id: resourceId,
};

const savedAttachment = {
  id: resourceId,
  board_id: resourceId,
  card_id: cardId,
  revision: 17,
  created_at: "2026-11-10 18:00:00+00:00",
  updated_at: "2026-11-10 18:00:00+00:00",
  original_name: "test.txt",
  size: 5,
  sha2_256: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
  media_type: "text/plain",
  previewable: false,
  active: true,
  added_by: resourceId,
};

const savedCard = {
  id: cardId,
  revision: 1,
  created_at: "2026-11-10 18:00:00+00:00",
  updated_at: "2026-11-10 18:00:00+00:00",
  board_id: resourceId,
  ticket: 1,
  title: "Exact title",
  description: "",
  lane_id: resourceId,
  owner_id: resourceId,
  priority_id: resourceId,
  parent_id: null,
  child_ids: [],
  archived: false,
  cover_attachment_id: null,
  comment_count: 0,
  attachment_count: 0,
  blocking_depth: 0,
  blocked_card_count: 0,
  triage_score: 100,
  completed_at: null,
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
      principal_id: resourceId,
      kind: "human",
      api_version: "2026-11-10",
      access_token: capability,
      expires_on_service_replacement: true,
      workspace_admin: false,
    });
  });
  assert.equal(token, capability);
  assert.equal(requests, 1);
});

test("card edits forward the original card ETag and never repeat a failed write", async () => {
  for (const [mode, status, unknown] of [
    ["conflict", 412, false],
    ["transport", null, true],
    ["malformed", 200, true],
  ]) {
    let calls = 0;
    const api = createBrowserApi({
      getCapability: () => capability,
      fetchImpl: async (url, options) => {
        ++calls;
        assert.equal(url, "/api/2026-11-10/cards/" + cardId);
        assert.equal(options.method, "PATCH");
        assert.equal(options.headers.get("If-Match"), tag);
        assert.deepEqual(JSON.parse(options.body), { title: "Draft" });
        if (mode === "transport") throw new Error("connection lost");
        return Response.json({}, { status, headers: { ETag: tag } });
      },
    });
    await assert.rejects(
      api.patchCard(cardId, tag, { title: "Draft" }),
      (error) =>
        error instanceof ApiError && error.status === status && error.outcomeUnknown === unknown,
    );
    assert.equal(calls, 1);
  }
});

test("comment and multipart upload carry caller IDs and original card ETags", async () => {
  const requests = [];
  const api = createBrowserApi({
    getCapability: () => capability,
    fetchImpl: async (url, options) => {
      requests.push([url, options]);
      return Response.json(url.endsWith("/comments") ? savedComment : savedAttachment, {
        status: 201,
        headers: { ETag: tag },
      });
    },
  });
  assert.deepEqual(await api.createComment(cardId, tag, "Comment", resourceId), {
    data: savedComment,
    etag: tag,
  });
  await api.uploadAttachment(cardId, tag, new File(["hello"], "test.txt"), resourceId);
  const [[commentUrl, comment], [uploadUrl, upload]] = requests;
  assert.equal(commentUrl, "/api/2026-11-10/comments");
  assert.equal(uploadUrl, "/api/2026-11-10/attachments");
  assert.deepEqual(JSON.parse(comment.body), {
    card_id: cardId,
    expected_card_etag: tag,
    text: "Comment",
    id: resourceId,
  });
  assert.equal(upload.headers.has("Content-Type"), false);
  assert.equal(upload.body.get("card_id"), cardId);
  assert.equal(upload.body.get("expected_card_etag"), tag);
  assert.equal(upload.body.get("id"), resourceId);
  assert.equal(upload.body.get("file").name, "test.txt");
  assert.equal(await upload.body.get("file").text(), "hello");
});

test("card comment reads keep the required board scope", async () => {
  let calls = 0;
  const listed = { ...savedComment, ticket: 1 };
  const api = createBrowserApi({
    getCapability: () => capability,
    fetchImpl: async (url) => {
      ++calls;
      assert.equal(url, "/api/2026-11-10/comments?board_id=" + resourceId + "&card_id=" + cardId);
      return Response.json({ comments: [listed], next_cursor: null });
    },
  });
  assert.deepEqual((await api.listComments({ board_id: resourceId, card_id: cardId })).data, {
    comments: [listed],
    next_cursor: null,
  });
  assert.equal(calls, 1);
});

test("a created comment or upload must identify the caller's submission", async () => {
  let calls = 0;
  const api = createBrowserApi({
    getCapability: () => capability,
    fetchImpl: async () => {
      ++calls;
      return Response.json({}, { status: 201, headers: { ETag: tag } });
    },
  });
  const uncertain = (error) =>
    error instanceof ApiError && error.status === 201 && error.outcomeUnknown;
  await assert.rejects(api.createComment(cardId, tag, "Comment", resourceId), uncertain);
  await assert.rejects(
    api.uploadAttachment(cardId, tag, new File(["hello"], "test.txt"), resourceId),
    uncertain,
  );
  assert.equal(calls, 2);
});

test("an exact attachment 503 refusal leaves its original ID eligible for explicit retry", async () => {
  const problem = {
    type: "about:blank",
    title: "Service Unavailable",
    status: 503,
    code: "unavailable",
    detail: "Board unavailable.",
    request_id: resourceId,
    outcome: "not_applied",
    errors: [],
  };
  const file = new File(["hello"], "test.txt");
  const cases = [
    [JSON.stringify(problem), "application/problem+json", "unavailable", false],
    [
      JSON.stringify({ ...problem, code: "outcome_unknown", outcome: "unknown" }),
      "application/problem+json",
      "http_error",
      true,
    ],
    [JSON.stringify(problem), "application/json", "http_error", true],
    ["invalid JSON", "application/problem+json", "http_error", true],
    [" ".repeat(4096) + JSON.stringify(problem), "application/problem+json", "http_error", true],
    [null, null, "transport_failed", true],
  ];
  for (const [body, contentType, code, outcomeUnknown] of cases) {
    let calls = 0;
    const api = createBrowserApi({
      getCapability: () => capability,
      fetchImpl: async (url, options) => {
        calls++;
        assert.equal(url, "/api/2026-11-10/attachments");
        assert.equal(options.method, "POST");
        assert.equal(options.body.get("id"), resourceId);
        if (body === null) throw new TypeError("Disconnected");
        return new Response(body, { status: 503, headers: { "Content-Type": contentType } });
      },
    });
    await assert.rejects(api.uploadAttachment(cardId, tag, file, resourceId), {
      code,
      status: body === null ? null : 503,
      outcomeUnknown,
    });
    assert.equal(calls, 1);
  }
});

test("comment creation requires a strong HTTP validator without replay", async () => {
  for (const responseTag of [null, "W/" + tag, "invalid"]) {
    let calls = 0;
    const api = createBrowserApi({
      getCapability: () => capability,
      fetchImpl: async () => {
        ++calls;
        return Response.json(savedComment, {
          status: 201,
          headers: responseTag === null ? {} : { ETag: responseTag },
        });
      },
    });
    await assert.rejects(
      api.createComment(cardId, tag, "Comment", resourceId),
      (error) => error instanceof ApiError && error.status === 201 && error.outcomeUnknown,
    );
    assert.equal(calls, 1);
  }
});

test("attachment readback targets the original submission ID", async () => {
  let calls = 0;
  const api = createBrowserApi({
    getCapability: () => capability,
    fetchImpl: async (url, options) => {
      ++calls;
      assert.equal(url, "/api/2026-11-10/attachments/" + resourceId);
      assert.equal(options.method, "GET");
      return Response.json(savedAttachment, { headers: { ETag: tag } });
    },
  });
  assert.equal((await api.getAttachment(resourceId)).data.id, resourceId);
  assert.equal(calls, 1);
});

test("owner edits reject clearing before dispatch", async () => {
  let calls = 0;
  const api = createBrowserApi({
    getCapability: () => capability,
    fetchImpl: async () => {
      ++calls;
      return Response.json({}, { headers: { ETag: tag } });
    },
  });
  for (const owner_id of [null, "", undefined, "invalid-id"]) {
    await assert.rejects(api.patchCard(cardId, tag, { owner_id }), TypeError);
  }
  assert.equal(calls, 0);
});

test("owner assignment validates the saved owner without repeating an uncertain write", async () => {
  const saved = savedCard;
  for (const owner_id of [resourceId, null]) {
    let calls = 0;
    const api = createBrowserApi({
      getCapability: () => capability,
      fetchImpl: async (url, options) => {
        ++calls;
        assert.equal(options.headers.get("If-Match"), tag);
        assert.deepEqual(JSON.parse(options.body), { owner_id: resourceId });
        return Response.json({ ...saved, owner_id }, { headers: { ETag: tag } });
      },
    });
    const assignment = api.patchCard(cardId, tag, { owner_id: resourceId });
    if (owner_id === null) {
      await assert.rejects(assignment, {
        name: "ApiError",
        code: "invalid_response",
        status: 200,
        outcomeUnknown: true,
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
    ...[undefined, null, false, 0, -1, 1.5, "1", Number.MAX_SAFE_INTEGER + 1].map((revision) => [
      { ...savedCard, revision },
      newerTag,
      false,
    ]),
    [{ ...savedCard, revision: undefined, etag: tag }, newerTag, false],
    [{ ...savedCard, etag: tag }, newerTag, false],
    [{ ...savedCard, id: resourceId }, newerTag, false],
    [savedCard, null, false],
    [savedCard, "W/" + tag, false],
  ];
  for (const method of ["GET", "PATCH"]) {
    for (const [data, responseTag, valid] of cases) {
      let calls = 0;
      const api = createBrowserApi({
        getCapability: () => capability,
        fetchImpl: async (url, options) => {
          ++calls;
          assert.equal(options.method, method);
          if (method === "PATCH") assert.equal(options.headers.get("If-Match"), tag);
          return Response.json(data, {
            headers: responseTag === null ? {} : { ETag: responseTag },
          });
        },
      });
      const result =
        method === "GET"
          ? api.getCard(cardId)
          : api.patchCard(cardId, tag, { title: "Exact title" });
      if (valid) {
        const reply = await result;
        assert.deepEqual(reply.data, data);
        assert.equal(reply.etag, responseTag);
      } else {
        await assert.rejects(result, {
          name: "ApiError",
          code: "invalid_response",
          status: 200,
          outcomeUnknown: method === "PATCH",
        });
      }
      assert.equal(calls, 1);
    }
  }
});

const savedLane = {
  id: cardId,
  revision: 12,
  created_at: "2026-11-10 18:00:00+00:00",
  updated_at: "2026-11-10 18:00:00+00:00",
  board_id: resourceId,
  key: "ready",
  label: "Ready",
  position: 1,
  role: "ordinary",
  retired: false,
  approval_enabled: false,
  reorder_enabled: false,
};

test("lane rename checks numeric revision and forwards the fresh HTTP validator and original board tag", async () => {
  const freshTag = '"opaque_lane_validator_12"';
  const boardTag = '"opaque_board_validator_99"';
  const requests = [];
  const api = createBrowserApi({
    getCapability: () => capability,
    fetchImpl: async (url, options) => {
      requests.push(options.method);
      assert.equal(url, "/api/2026-11-10/lanes/" + cardId);
      if (options.method === "GET") {
        assert.equal(options.headers.has("If-Match"), false);
        return Response.json(savedLane, { headers: { ETag: freshTag } });
      }
      assert.equal(options.method, "PATCH");
      assert.equal(options.headers.get("If-Match"), freshTag);
      assert.deepEqual(JSON.parse(options.body), {
        board_etag: boardTag,
        label: "  Exact new label  ",
      });
      return Response.json(
        { ...savedLane, label: "  Exact new label  ", revision: 100 },
        { headers: { ETag: tag } },
      );
    },
  });
  const reply = await api.renameLane(cardId, 12, boardTag, "  Exact new label  ");
  assert.equal(reply.data.revision, 100);
  assert.equal(reply.etag, tag);
  assert.deepEqual(requests, ["GET", "PATCH"]);
});

test("lane rename refuses unsafe expected revisions before any request", async () => {
  let calls = 0;
  const api = createBrowserApi({
    getCapability: () => capability,
    fetchImpl: async () => {
      calls++;
    },
  });
  for (const revision of [
    undefined,
    null,
    false,
    0,
    -1,
    1.5,
    "12",
    Number.MAX_SAFE_INTEGER + 1,
    tag,
  ]) {
    await assert.rejects(api.renameLane(cardId, revision, tag, "Draft"), TypeError);
  }
  await assert.rejects(api.renameLane(cardId, 12, "*", "Draft"), TypeError);
  assert.equal(calls, 0);
});

test("lane rename refuses stale, invalid, or failed reads before mutation", async () => {
  const cases = [
    [
      () => Response.json({ ...savedLane, revision: 13 }, { headers: { ETag: tag } }),
      "http_error",
      412,
    ],
    ...[undefined, null, false, 0, -1, 1.5, "12", Number.MAX_SAFE_INTEGER + 1].map((revision) => [
      () => Response.json({ ...savedLane, revision }, { headers: { ETag: tag } }),
      "invalid_response",
      200,
    ]),
    ...[{ ...savedLane, etag: tag }, { ...savedLane, id: resourceId }, []].map((data) => [
      () => Response.json(data, { headers: { ETag: tag } }),
      "invalid_response",
      200,
    ]),
    ...[null, "W/" + tag, "invalid"].map((etag) => [
      () => Response.json(savedLane, { headers: etag === null ? {} : { ETag: etag } }),
      "invalid_response",
      200,
    ]),
    [() => new Response("unusable", { headers: { ETag: tag } }), "invalid_response", 200],
    [
      () => Response.json(savedLane, { status: 201, headers: { ETag: tag } }),
      "invalid_response",
      201,
    ],
    [() => new Response(null, { status: 404 }), "http_error", 404],
    [
      () => {
        throw new Error("private failure");
      },
      "transport_failed",
      null,
    ],
  ];
  for (const [response, code, status] of cases) {
    const methods = [];
    const api = createBrowserApi({
      getCapability: () => capability,
      fetchImpl: async (_, options) => {
        methods.push(options.method);
        return response();
      },
    });
    await assert.rejects(api.renameLane(cardId, 12, tag, "Draft"), {
      name: "ApiError",
      code,
      status,
      outcomeUnknown: false,
    });
    assert.deepEqual(methods, ["GET"]);
  }
});

test("lane rename never replays failed or uncertain writes", async () => {
  for (const [response, code, status, outcomeUnknown] of [
    [() => new Response(null, { status: 412 }), "http_error", 412, false],
    [
      () => {
        throw new Error("lost reply");
      },
      "transport_failed",
      null,
      true,
    ],
    [
      () => Response.json({ ...savedLane, etag: tag }, { headers: { ETag: tag } }),
      "invalid_response",
      200,
      true,
    ],
    [
      () => Response.json({ ...savedLane, revision: 0 }, { headers: { ETag: tag } }),
      "invalid_response",
      200,
      true,
    ],
    [() => Response.json(savedLane), "invalid_response", 200, true],
  ]) {
    const methods = [];
    const api = createBrowserApi({
      getCapability: () => capability,
      fetchImpl: async (_, options) => {
        methods.push(options.method);
        return options.method === "GET"
          ? Response.json(savedLane, { headers: { ETag: tag } })
          : response();
      },
    });
    await assert.rejects(api.renameLane(cardId, 12, tag, "Draft"), {
      name: "ApiError",
      code,
      status,
      outcomeUnknown,
    });
    assert.deepEqual(methods, ["GET", "PATCH"]);
  }
});

test("lane lists and creation require numeric revision and omit JSON validators", async () => {
  for (const data of [
    savedLane,
    { ...savedLane, revision: Number.MAX_SAFE_INTEGER },
    { ...savedLane, revision: undefined },
    { ...savedLane, etag: tag },
    { ...savedLane, board_id: cardId },
  ]) {
    const valid =
      Number.isSafeInteger(data.revision) &&
      !Object.hasOwn(data, "etag") &&
      data.board_id === resourceId;
    for (const method of ["GET", "POST"]) {
      let calls = 0;
      const api = createBrowserApi({
        getCapability: () => capability,
        fetchImpl: async (_, options) => {
          calls++;
          assert.equal(options.method, method);
          return Response.json(method === "GET" ? { lanes: [data], next_cursor: null } : data, {
            status: method === "GET" ? 200 : 201,
            headers: { ETag: tag },
          });
        },
      });
      const result =
        method === "GET"
          ? api.listLanes(resourceId)
          : api.createLane(resourceId, tag, {
              label: "Ready",
              after_id: cardId,
              create_for: [],
              connect_for: [],
            });
      if (valid)
        assert.deepEqual(
          (await result).data,
          method === "GET" ? { lanes: [data], next_cursor: null } : data,
        );
      else
        await assert.rejects(result, {
          name: "ApiError",
          code: "invalid_response",
          outcomeUnknown: method === "POST",
        });
      assert.equal(calls, 1);
    }
  }
});

test("attachment get, list, and upload require numeric revisions and SHA-2-256 metadata", async () => {
  for (const item of [
    savedAttachment,
    { ...savedAttachment, revision: Number.MAX_SAFE_INTEGER },
    ...[undefined, null, false, 0, -1, 1.5, "17"].map((revision) => ({
      ...savedAttachment,
      revision,
    })),
    { ...savedAttachment, etag: tag },
    { ...savedAttachment, sha256: savedAttachment.sha2_256 },
    { ...savedAttachment, sha2_256: undefined },
    { ...savedAttachment, sha2_256: "invalid" },
  ]) {
    const valid =
      Number.isSafeInteger(item.revision) &&
      item.revision > 0 &&
      !Object.hasOwn(item, "etag") &&
      !Object.hasOwn(item, "sha256") &&
      /^[0-9a-f]{64}$/.test(item.sha2_256 ?? "");
    for (const operation of ["get", "list", "upload"]) {
      let calls = 0;
      const api = createBrowserApi({
        getCapability: () => capability,
        fetchImpl: async () => {
          ++calls;
          return Response.json(
            operation === "list" ? { attachments: [item], next_cursor: null } : item,
            { status: operation === "upload" ? 201 : 200, headers: { ETag: tag } },
          );
        },
      });
      const pending =
        operation === "get"
          ? api.getAttachment(resourceId)
          : operation === "list"
            ? api.listAttachments(cardId)
            : api.uploadAttachment(cardId, tag, new File(["hello"], "test.txt"), resourceId);
      if (valid) assert.equal((await pending).etag, tag);
      else
        await assert.rejects(
          pending,
          (error) =>
            error instanceof ApiError &&
            error.code === "invalid_response" &&
            error.outcomeUnknown === (operation === "upload"),
        );
      assert.equal(calls, 1);
    }
  }
});
