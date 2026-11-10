import assert from "node:assert/strict";
import test from "node:test";
import { ApiError, createBrowserApi, readWorkspaceOverview } from "./api.mjs";

const BOARD = "9d86fce9-1991-428b-a784-d41588192126";
const TAG = '"workspace_tag_07"';

function client(fetchImpl, getCapability = () => "fixture-capability") {
  return createBrowserApi({ fetchImpl, getCapability });
}

test("reads the workspace and catalog as independent same-origin resources", async () => {
  const calls = [];
  let capabilityRequests = 0;
  const api = client(async (url, options) => {
    calls.push(url);
    assert.equal(options.method, "GET");
    assert.equal(options.mode, "same-origin");
    assert.equal(options.credentials, "omit");
    assert.equal(options.redirect, "error");
    assert.equal(options.cache, "no-store");
    assert.equal(options.headers.get("Authorization"), "Bearer fixture-capability");
    const workspace = url.endsWith("/workspace");
    return Response.json(workspace ? { pinned_board_ids: [], revision: 1 } : { boards: [], next_cursor: null }, {
      headers: { ETag: workspace ? TAG : '"board_catalog_04"' },
    });
  }, () => {
    capabilityRequests++;
    return "fixture-capability";
  });

  assert.deepEqual(await readWorkspaceOverview(api), {
    workspace: { data: { pinned_board_ids: [], revision: 1 }, etag: TAG },
    boards: { data: { boards: [], next_cursor: null }, etag: '"board_catalog_04"' },
  });
  assert.deepEqual(calls, ["/api/2026-11-10/workspace", "/api/2026-11-10/boards"]);
  assert.equal(capabilityRequests, 2);
});

test("submits pin order using its original workspace tag and surfaces stale refusal once", async () => {
  let calls = 0;
  const api = client(async (url, options) => {
    calls++;
    assert.equal(url, "/api/2026-11-10/workspace");
    assert.equal(options.method, "PATCH");
    assert.equal(options.headers.get("Content-Type"), "application/json");
    assert.equal(options.headers.get("If-Match"), TAG);
    assert.deepEqual(JSON.parse(options.body), { pinned_board_ids: [BOARD] });
    return new Response(null, { status: 412 });
  });

  await assert.rejects(api.setPinnedBoards([BOARD], TAG), {
    name: "ApiError", code: "http_error", status: 412, outcomeUnknown: false,
  });
  assert.equal(calls, 1);
});

test("classifies lost or unusable write responses without resubmitting", async () => {
  for (const [response, code, status] of [
    [() => { throw new Error("internal fixture detail"); }, "transport_failed", null],
    [() => new Response("<invalid>", { status: 201, headers: { ETag: TAG } }), "invalid_response", 201],
    [() => Response.json(null, { status: 201, headers: { ETag: TAG } }), "invalid_response", 201],
    [() => new Response(null, { status: 500 }), "http_error", 500],
  ]) {
    const calls = [];
    const api = client(async (url, options) => {
      calls.push([url, options]);

      return response();
    });

    await assert.rejects(api.createBoard({ name: "Fixture" }), (error) =>
      error instanceof ApiError && error.code === code && error.status === status
      && error.outcomeUnknown && !error.cause);
    assert.equal(calls.length, 1);
    const [[url, options]] = calls;
    assert.equal(url, "/api/2026-11-10/boards");
    assert.equal(options.method, "POST");
    assert.equal(options.headers.has("If-Match"), false);
  }
});

test("refuses an unavailable capability and unsafe selectors before dispatch", async () => {
  let calls = 0;
  const api = client(async () => { calls++; }, () => {
    throw new Error("internal adapter detail");
  });

  assert.throws(() => api.getBoard("../service"), TypeError);
  assert.throws(() => api.setPinnedBoards([BOARD], "*"), TypeError);
  await assert.rejects(api.getWorkspace(), {
    name: "ApiError", message: "capability_unavailable", outcomeUnknown: false,
  });
  assert.equal(calls, 0);
});

test("uses documented paths and board scoping for Turn 1 reads", async () => {
  const paths = [];
  const api = client(async (url) => {
    paths.push(url);
    return Response.json({});
  });

  await api.getBoard(BOARD);
  await api.listLanes(BOARD);
  await api.listPriorities(BOARD);
  await api.listUsers(BOARD);
  await api.getPolicy(BOARD);
  await api.getVerification(BOARD);
  await api.getSnapshot(BOARD);
  await api.getService();
  await api.listBuilds();
  await api.getBuild(BOARD);
  assert.deepEqual(paths, [
    "/boards/" + BOARD,
    "/lanes?board_id=" + BOARD,
    "/priorities?board_id=" + BOARD,
    "/users?board_id=" + BOARD,
    "/policies/" + BOARD,
    "/verifications/" + BOARD,
    "/snapshots/" + BOARD,
    "/service",
    "/builds",
    "/builds/" + BOARD,
  ].map((path) => "/api/2026-11-10" + path));
});

test("owner notice configuration sends only explicitly supplied fields", async () => {
  const boardTag = '"board_record_006"';
  const api = client(async (url, options) => {
    assert.equal(url, "/api/2026-11-10/boards/" + BOARD);
    assert.equal(options.method, "PATCH");
    assert.equal(options.headers.get("If-Match"), boardTag);
    assert.deepEqual(JSON.parse(options.body), {
      notifications: { target_thread_id: null },
    });
    return Response.json({ notifications: { enabled: true, target_thread_id: null } }, {
      headers: { ETag: '"board_record_007"' },
    });
  });
  assert.equal((await api.configureOwnerNotices(BOARD, { target_thread_id: null }, boardTag))
    .data.notifications.enabled, true);
});

test("requires the documented write status and a canonical response ETag", async () => {
  const writes = [
    { send: (api) => api.createBoard({ name: "Fixture" }), status: 201, wrongStatus: 200 },
    { send: (api) => api.setPinnedBoards([BOARD], TAG), status: 200, wrongStatus: 201 },
  ];

  for (const { send, status, wrongStatus } of writes) {
    const confirmed = await send(client(() => Response.json({}, {
      status, headers: { ETag: TAG },
    })));
    assert.equal(confirmed.etag, TAG);

    for (const [responseStatus, etag] of [
      [wrongStatus, TAG], [status, null], [status, '"short"'], [status, "W/" + TAG],
    ]) {
      let calls = 0;
      const api = client(() => {
        calls++;
        return Response.json({}, {
          status: responseStatus,
          headers: etag === null ? {} : { ETag: etag },
        });
      });
      await assert.rejects(send(api), {
        name: "ApiError", code: "invalid_response", status: responseStatus, outcomeUnknown: true,
      });
      assert.equal(calls, 1);
    }
  }
});

test("creates and finds cards using the distinct board and card tags", async () => {
  const card = "e30fa21d-76d7-4eba-b1a4-10962bb6a582";
  const payload = {
    id: card, board_id: BOARD, lane_id: BOARD, expected_board_etag: TAG, title: "  Exact title  ",
  };
  const boardEtag = '"updated_board_tag"';
  const created = {
    id: card, etag: TAG, created_at: "2026-11-10 18:00:00+00:00",
    updated_at: "2026-11-10 18:00:00+00:00", board_id: BOARD, ticket: 1,
    title: payload.title, description: "", lane_id: BOARD, owner_id: null,
    priority_id: BOARD, parent_id: null, child_ids: [], archived: false,
    cover_attachment_id: null, comment_count: 0, attachment_count: 0,
    blocking_depth: 0, blocked_card_count: 0, triage_score: 100, completed_at: null,
  };
  const calls = [];
  const api = client(async (url, options) => {
    calls.push(url);
    if (options.method === "POST") {
      assert.equal(options.headers.has("If-Match"), false);
      assert.deepEqual(JSON.parse(options.body), payload);
      return Response.json(created, {
        status: 201, headers: { ETag: TAG, "X-Board-ETag": boardEtag },
      });
    }
    return Response.json(url.includes("?") ? { cards: [{ id: card }] } : { id: card });
  });

  assert.deepEqual(await api.createCard(payload), { data: created, etag: TAG, boardEtag });

  for (const malformed of [
    { ...created, id: BOARD }, { ...created, board_id: card }, { ...created, ticket: 0 },
    { ...created, title: undefined }, { ...created, owner_id: undefined, extra: null },
    { ...created, etag: boardEtag },
  ]) {
    let calls = 0;
    const invalid = client(async () => {
      calls++;
      return Response.json(malformed, { status: 201, headers: { ETag: TAG, "X-Board-ETag": boardEtag } });
    });
    await assert.rejects(invalid.createCard(payload), {
      name: "ApiError", code: "invalid_response", status: 201, outcomeUnknown: true,
    });
    assert.equal(calls, 1);
  }
  assert.equal((await api.getCard(card)).data.id, card);
  assert.equal((await api.listCards(BOARD, { ticket: 1, cursor: "opaque&cursor" })).data.cards[0].id, card);
  assert.throws(() => api.listCards(BOARD, { ticket: -1 }), TypeError);
  assert.deepEqual(calls, [
    "/cards", "/cards/" + card, "/cards?board_id=" + BOARD + "&ticket=1&cursor=opaque%26cursor",
  ].map((path) => "/api/2026-11-10" + path));
});

test("does not repeat a stale card creation or a reply missing the board tag", async () => {
  for (const [response, code, status, outcomeUnknown] of [
    [new Response(null, { status: 412 }), "http_error", 412, false],
    [Response.json({}, { status: 201, headers: { ETag: TAG } }), "invalid_response", 201, true],
  ]) {
    let calls = 0;
    const api = client(async () => { calls++; return response; });
    await assert.rejects(api.createCard({
      board_id: BOARD, lane_id: BOARD, expected_board_etag: TAG, title: "Fixture",
    }), { name: "ApiError", code, status, outcomeUnknown });
    assert.equal(calls, 1);
  }
});

test("handles an unchanged board without parsing a body", async () => {
  const api = client(async (url, options) => {
    assert.equal(url, "/api/2026-11-10/boards/" + BOARD);
    assert.equal(options.headers.get("If-None-Match"), TAG);
    return new Response(null, { status: 304, headers: { ETag: TAG } });
  });
  assert.deepEqual(await api.getBoardIfChanged(BOARD, TAG), { data: null, etag: TAG });
  await assert.rejects(api.getBoardIfChanged(BOARD, "W/" + TAG), TypeError);
});

test("reads authenticated event frames and cancels the stream when iteration stops", async () => {
  const signal = new AbortController().signal;
  const event = {
    type: "snapshot", resource: "board", board_id: BOARD, board_revision: 1,
  };
  const other = "e30fa21d-76d7-4eba-b1a4-10962bb6a582";
  let canceled = false;
  const api = client(async (url, options) => {
    assert.equal(url, "/api/2026-11-10/events?board_id=" + BOARD + "&watch=" + other + "&watch=" + BOARD);
    assert.equal(options.headers.get("Accept"), "text/event-stream");
    assert.equal(options.headers.get("Authorization"), "Bearer fixture-capability");
    assert.equal(options.signal, signal);
    assert.equal(options.mode, "same-origin");
    assert.equal(options.credentials, "omit");
    assert.equal(options.headers.has("Last-Event-ID"), false);
    const encoder = new TextEncoder();
    return new Response(new ReadableStream({
      start(controller) {
        controller.enqueue(encoder.encode(": heartbeat\r\n\r\nevent: snapshot\r\nda"));
        controller.enqueue(encoder.encode("ta: " + JSON.stringify(event) + "\r\n\r\n"));
      },
      cancel() { canceled = true; },
    }), { headers: { "Content-Type": "text/event-stream; charset=utf-8" } });
  });

  for await (const snapshot of api.streamEvents({ boardId: BOARD, watch: [other, BOARD], signal })) {
    assert.deepEqual(snapshot, event);
    break;
  }
  assert.equal(canceled, true);
});

test("validates resource-specific event payloads", async () => {
  const board = { type: "change", resource: "board", board_id: BOARD, board_revision: 2 };
  const workspace = { type: "change", resource: "workspace", workspace_revision: 3 };
  const catalog = { type: "change", resource: "catalog" };
  for (const [event, valid] of [
    [board, true], [workspace, true], [catalog, true],
    [{ ...board, type: "snapshot" }, true],
    [{ ...board, board_revision: null }, false],
    [{ ...board, board_revision: 0 }, false],
    [{ ...board, board_revision: -1 }, false],
    [{ ...board, board_revision: 1.5 }, false],
    [{ ...board, board_revision: "2" }, false],
    [{ ...board, board_revision: Number.MAX_SAFE_INTEGER + 1 }, false],
    [{ ...board, board_id: null }, false],
    [{ ...board, board_etag: TAG }, false],
    [{ ...workspace, workspace_revision: 0 }, false],
    [{ ...catalog, board_id: null }, false],
    [{ type: "change", resource: "unknown" }, false],
  ]) {
    const api = client(() => new Response("data: " + JSON.stringify(event) + "\n\n", {
      headers: { "Content-Type": "text/event-stream" },
    }));
    const stream = api.streamEvents();
    if (valid) {
      assert.deepEqual((await stream.next()).value, event);
      await stream.return();
    } else {
      await assert.rejects(stream.next(), { name: "ApiError", code: "invalid_response" });
    }
  }
});

test("fails once on malformed event payloads without exposing their data", async () => {
  let calls = 0;
  const api = client(async () => {
    calls++;
    return new Response('data: {"private_fixture":"detail"}\n\n', {
      headers: { "Content-Type": "text/event-stream" },
    });
  });
  await assert.rejects(api.streamEvents().next(), {
    name: "ApiError", message: "invalid_response", status: 200, outcomeUnknown: false,
  });
  assert.equal(calls, 1);
});

test("cancels unread bodies on refusals or invalid response metadata", async () => {
  for (const [status, headers, code] of [
    [412, {}, "http_error"],
    [201, { ETag: TAG }, "invalid_response"],
  ]) {
    let canceled = false;
    const api = client(() => new Response(new ReadableStream({
      cancel() { canceled = true; },
    }), { status, headers }));
    await assert.rejects(api.createCard({
      board_id: BOARD, lane_id: BOARD, expected_board_etag: TAG, title: "Fixture",
    }), { name: "ApiError", code, status });
    assert.equal(canceled, true);
  }
});

test("validates changed and unchanged board tags", async () => {
  const nextTag = '"updated_board_tag"';
  const changed = client(() => Response.json({}, { headers: { ETag: nextTag } }));
  assert.deepEqual(await changed.getBoardIfChanged(BOARD, TAG), { data: {}, etag: nextTag });
  for (const [status, etag] of [[304, nextTag], [304, null], [200, TAG], [200, "W/" + TAG]]) {
    const api = client(() => new Response(null, { status, headers: etag === null ? {} : { ETag: etag } }));
    await assert.rejects(api.getBoardIfChanged(BOARD, TAG), {
      name: "ApiError", code: "invalid_response", status, outcomeUnknown: false,
    });
  }
});

test("rejects incomplete event frames and handles the exact bound across chunks", async () => {
  const encoder = new TextEncoder();
  function stream(chunks) {
    return client(() => new Response(new ReadableStream({
      start(controller) {
        for (const chunk of chunks) {
          controller.enqueue(typeof chunk === "string" ? encoder.encode(chunk) : chunk);
        }
        controller.close();
      },
    }), { headers: { "Content-Type": "text/event-stream" } })).streamEvents();
  }
  const frame = ":" + "x".repeat(65535);
  assert.equal((await stream([frame, "\r", "\n\r", "\n"]).next()).done, true);
  for (const chunks of [[frame + "x", "\n\n"], ["data: {}"], [new Uint8Array([0xc3])]]) {
    await assert.rejects(stream(chunks).next(), {
      name: "ApiError", code: "invalid_response", status: 200,
    });
  }
});
