import assert from "node:assert/strict";
import test from "node:test";
import { newOperationId } from "./operation-id.mjs";
import { ApiError, createBrowserApi, readWorkspaceOverview } from "./api.mjs";

const OPERATION = "019b1111-2222-7333-a444-555555555555";
const BOARD = "9d86fce9-1991-428b-a784-d41588192126";
const TAG = '"workspace_tag_07"';
const PUBLISHING_OFF = {
  enabled: false,
  repository: null,
  last_success_at: null,
  pending: false,
  error_code: null,
};

const SERVICE_TAG = '"019aa833-764f-746b-a3f0-e2f7af0b9e3f-0"';
const SERVICE = {
  service_instance_id: "019aa833-764f-746b-a3f0-e2f7af0b9e3f",
  build_id: BOARD,
  port: 53146,
  state: "running",
  api_version: "2026-11-10",
  restart_error_code: null,
};

function client(fetchImpl, getCapability = () => "fixture-capability") {
  return createBrowserApi({ fetchImpl, getCapability });
}

test("reads the workspace and catalog as independent same-origin resources", async () => {
  const calls = [];
  let capabilityRequests = 0;
  const api = client(
    async (url, options) => {
      calls.push(url);
      assert.equal(options.method, "GET");
      assert.equal(options.mode, "same-origin");
      assert.equal(options.credentials, "omit");
      assert.equal(options.redirect, "error");
      assert.equal(options.cache, "no-store");
      assert.equal(options.headers.get("Authorization"), "Bearer fixture-capability");
      const workspace = url.endsWith("/workspace");
      return Response.json(
        workspace
          ? { pinned_board_ids: [], revision: 1, publishing: PUBLISHING_OFF }
          : { boards: [], next_cursor: null },
        {
          headers: { ETag: workspace ? TAG : '"board_catalog_04"' },
        },
      );
    },
    () => {
      capabilityRequests++;
      return "fixture-capability";
    },
  );

  assert.deepEqual(await readWorkspaceOverview(api), {
    workspace: {
      data: { pinned_board_ids: [], revision: 1, publishing: PUBLISHING_OFF },
      etag: TAG,
    },
    boards: { data: { boards: [], next_cursor: null }, etag: '"board_catalog_04"' },
  });
  assert.deepEqual(calls, ["/api/2026-11-10/workspace", "/api/2026-11-10/boards"]);
  assert.equal(capabilityRequests, 2);
});

test("reads and updates workspace JSON using its separate HTTP validator", async () => {
  const calls = [];
  const nextTag = '"workspace_tag_08"';
  const publishing = PUBLISHING_OFF;
  const original = { revision: 7, pinned_board_ids: [], publishing };
  const updated = { revision: 8, pinned_board_ids: [BOARD], publishing };
  const api = client(async (url, options) => {
    calls.push(options.method);
    assert.equal(url, "/api/2026-11-10/workspace");
    if (options.method === "GET") {
      assert.equal(options.headers.has("If-Match"), false);
      return Response.json(original, { headers: { ETag: TAG } });
    }
    assert.equal(options.method, "PATCH");
    assert.equal(options.headers.get("If-Match"), TAG);
    assert.deepEqual(JSON.parse(options.body), { pinned_board_ids: [BOARD] });
    return Response.json(updated, { headers: { ETag: nextTag } });
  });

  const workspace = await api.getWorkspace();
  assert.deepEqual(workspace, { data: original, etag: TAG });
  assert.equal(Object.hasOwn(workspace.data, "etag"), false);
  const saved = await api.setPinnedBoards([BOARD], workspace.etag);
  assert.deepEqual(saved, { data: updated, etag: nextTag });
  assert.equal(Object.hasOwn(saved.data, "etag"), false);
  assert.deepEqual(calls, ["GET", "PATCH"]);
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
    name: "ApiError",
    code: "http_error",
    status: 412,
    outcomeUnknown: false,
  });
  assert.equal(calls, 1);
});

test("publishing settings send only the flag and use separate validators", async () => {
  const calls = [];
  const boardTag = JSON.stringify(BOARD + "-8");
  const publishing = { ...PUBLISHING_OFF, enabled: true };
  const api = client(async (url, options) => {
    const workspace = url.endsWith("/workspace");
    calls.push([url, options.method, options.headers.get("If-Match"), JSON.parse(options.body)]);
    return Response.json(
      workspace
        ? { revision: 8, pinned_board_ids: [BOARD], publishing }
        : { id: BOARD, revision: 8, publishing },
      {
        headers: { ETag: workspace ? TAG : boardTag },
      },
    );
  });
  assert.equal((await api.configureWorkspacePublishing(true, TAG)).data.publishing.enabled, true);
  assert.equal((await api.configureBoardPublishing(BOARD, true, boardTag)).data.enabled, true);
  assert.deepEqual(calls, [
    ["/api/2026-11-10/workspace", "PATCH", TAG, { publishing: { enabled: true } }],
    ["/api/2026-11-10/boards/" + BOARD, "PATCH", boardTag, { publishing: { enabled: true } }],
  ]);
});

test("publishing reads and writes reject malformed status or mismatched board identity without replay", async () => {
  const boardTag = JSON.stringify(BOARD + "-8");
  const configureBoard = (api) => api.configureBoardPublishing(BOARD, true, boardTag);
  for (const [body, configure, etag] of [
    [{ id: "other-board", revision: 8, publishing: PUBLISHING_OFF }, configureBoard, boardTag],
    [{ id: BOARD, revision: 7, publishing: PUBLISHING_OFF }, configureBoard, boardTag],
    [{ id: BOARD, revision: 0, publishing: PUBLISHING_OFF }, configureBoard, boardTag],
    [
      { revision: 1, pinned_board_ids: [], publishing: { ...PUBLISHING_OFF, pending: "true" } },
      (api) => api.configureWorkspacePublishing(true, TAG),
      TAG,
    ],
  ]) {
    let calls = 0;
    const api = client(async () => {
      calls++;
      return Response.json(body, { headers: { ETag: etag } });
    });
    await assert.rejects(configure(api), {
      code: "invalid_response",
      status: 200,
      outcomeUnknown: true,
    });
    assert.equal(calls, 1);
  }
  const api = client(async () =>
    Response.json(
      { id: BOARD, revision: 8, publishing: { enabled: false } },
      {
        headers: { ETag: boardTag },
      },
    ),
  );
  await assert.rejects(api.getBoardPublishing(BOARD), {
    code: "invalid_response",
    outcomeUnknown: false,
  });
});

test("a matching board ETag can carry live publishing status without a new revision", async () => {
  const boardTag = JSON.stringify(BOARD + "-8");
  const board = {
    id: BOARD,
    revision: 8,
    publishing: { ...PUBLISHING_OFF, enabled: true, pending: true },
  };
  const api = client(async (_url, options) => {
    assert.equal(options.headers.get("If-None-Match"), boardTag);
    return Response.json(board, { headers: { ETag: boardTag } });
  });
  assert.deepEqual(await api.getBoardIfChanged(BOARD, boardTag), { data: board, etag: boardTag });
});

test("classifies lost or unusable write responses without resubmitting", async () => {
  for (const [response, code, status] of [
    [
      () => {
        throw new Error("internal fixture detail");
      },
      "transport_failed",
      null,
    ],
    [
      () => new Response("<invalid>", { status: 201, headers: { ETag: TAG } }),
      "invalid_response",
      201,
    ],
    [() => Response.json(null, { status: 201, headers: { ETag: TAG } }), "invalid_response", 201],
    [() => new Response(null, { status: 500 }), "http_error", 500],
  ]) {
    const calls = [];
    const api = client(async (url, options) => {
      calls.push([url, options]);

      return response();
    });

    await assert.rejects(
      api.createBoard({ name: "Fixture" }),
      (error) =>
        error instanceof ApiError &&
        error.code === code &&
        error.status === status &&
        error.outcomeUnknown &&
        !error.cause,
    );
    assert.equal(calls.length, 1);
    const [[url, options]] = calls;
    assert.equal(url, "/api/2026-11-10/boards");
    assert.equal(options.method, "POST");
    assert.equal(options.headers.has("If-Match"), false);
  }
});

test("refuses an unavailable capability and unsafe selectors before dispatch", async () => {
  let calls = 0;
  const api = client(
    async () => {
      calls++;
    },
    () => {
      throw new Error("internal adapter detail");
    },
  );

  assert.throws(() => api.getBoard("../service"), TypeError);
  assert.throws(() => api.setPinnedBoards([BOARD], "*"), TypeError);
  await assert.rejects(api.getWorkspace(), {
    name: "ApiError",
    message: "capability_unavailable",
    outcomeUnknown: false,
  });
  assert.equal(calls, 0);
});

test("uses documented paths and board scoping for Turn 1 reads", async () => {
  const paths = [];
  const api = client(async (url) => {
    paths.push(url);
    if (url.endsWith("/service")) return Response.json(SERVICE, { headers: { ETag: SERVICE_TAG } });
    if (url.endsWith("/builds"))
      return Response.json({ builds: [], next_cursor: null }, { headers: { ETag: TAG } });
    return Response.json(url.includes("/lanes?") ? { lanes: [], next_cursor: null } : {});
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
  assert.deepEqual(
    paths,
    [
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
    ].map((path) => "/api/2026-11-10" + path),
  );
});

test("owner notice routing uses a focused read and sends only explicitly supplied fields", async () => {
  const original = { board_id: BOARD, revision: 6, enabled: false, target_thread_id: OPERATION };
  const updated = { ...original, revision: 7, target_thread_id: null };
  const originalTag = '"notifications-' + BOARD + '-6"';
  const updatedTag = '"notifications-' + BOARD + '-7"';
  const methods = [];
  const api = client(async (url, options) => {
    assert.equal(url, "/api/2026-11-10/notifications/" + BOARD);
    methods.push(options.method);
    if (options.method === "GET") {
      assert.equal(options.headers.get("If-Match"), null);
      assert.equal(options.cache, "no-store");
      return Response.json(original, { headers: { ETag: originalTag } });
    }
    assert.equal(options.method, "PATCH");
    assert.equal(options.headers.get("If-Match"), originalTag);
    assert.deepEqual(JSON.parse(options.body), { target_thread_id: null });
    return Response.json(updated, { headers: { ETag: updatedTag } });
  });

  const route = await api.getOwnerNoticeRoute(BOARD.toUpperCase());
  assert.deepEqual(route, { data: original, etag: originalTag });
  assert.deepEqual(await api.configureOwnerNotices(BOARD, { target_thread_id: null }, route.etag), {
    data: updated,
    etag: updatedTag,
  });
  assert.deepEqual(methods, ["GET", "PATCH"]);
});

test("owner notice route rejects board validators and unconfirmed settings", async () => {
  const route = { board_id: BOARD, revision: 6, enabled: false, target_thread_id: OPERATION };
  const originalTag = '"notifications-' + BOARD + '-6"';
  const wrongTag = client(() => Response.json(route, { headers: { ETag: '"' + BOARD + '-6"' } }));
  await assert.rejects(
    wrongTag.getOwnerNoticeRoute(BOARD),
    (error) =>
      error instanceof ApiError && error.code === "invalid_response" && !error.outcomeUnknown,
  );
  const unconfirmed = client(() => Response.json(route, { headers: { ETag: originalTag } }));
  await assert.rejects(
    unconfirmed.configureOwnerNotices(BOARD, { target_thread_id: null }, originalTag),
    (error) =>
      error instanceof ApiError && error.code === "invalid_response" && error.outcomeUnknown,
  );
});

test("requires the documented write status and a canonical response ETag", async () => {
  const writes = [
    {
      send: (api) => api.createBoard({ name: "Fixture" }),
      body: {},
      status: 201,
      wrongStatus: 200,
    },
    {
      send: (api) => api.setPinnedBoards([BOARD], TAG),
      body: { revision: 1, pinned_board_ids: [BOARD], publishing: PUBLISHING_OFF },
      status: 200,
      wrongStatus: 201,
    },
  ];

  for (const { send, body, status, wrongStatus } of writes) {
    const confirmed = await send(
      client(() =>
        Response.json(body, {
          status,
          headers: { ETag: TAG },
        }),
      ),
    );
    assert.equal(confirmed.etag, TAG);

    for (const [responseStatus, etag] of [
      [wrongStatus, TAG],
      [status, null],
      [status, '"short"'],
      [status, "W/" + TAG],
    ]) {
      let calls = 0;
      const api = client(() => {
        calls++;
        return Response.json(
          {},
          {
            status: responseStatus,
            headers: etag === null ? {} : { ETag: etag },
          },
        );
      });
      await assert.rejects(send(api), {
        name: "ApiError",
        code: "invalid_response",
        status: responseStatus,
        outcomeUnknown: true,
      });
      assert.equal(calls, 1);
    }
  }
});

test("creates and finds cards using the distinct board and card tags", async () => {
  const card = "e30fa21d-76d7-4eba-b1a4-10962bb6a582";
  const payload = {
    id: card,
    board_id: BOARD,
    lane_id: BOARD,
    expected_board_etag: TAG,
    title: "  Exact title  ",
  };
  const boardEtag = '"updated_board_tag"';
  const created = {
    id: card,
    revision: 1,
    created_at: "2026-11-10 18:00:00+00:00",
    updated_at: "2026-11-10 18:00:00+00:00",
    board_id: BOARD,
    ticket: 1,
    title: payload.title,
    description: "",
    lane_id: BOARD,
    owner_id: BOARD,
    priority_id: BOARD,
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
  const calls = [];
  const api = client(async (url, options) => {
    calls.push(url);
    if (options.method === "POST") {
      assert.equal(options.headers.has("If-Match"), false);
      assert.deepEqual(JSON.parse(options.body), payload);
      return Response.json(created, {
        status: 201,
        headers: { ETag: TAG, "X-Board-ETag": boardEtag },
      });
    }
    return Response.json(url.includes("?") ? { cards: [created] } : created, {
      headers: { ETag: TAG },
    });
  });

  assert.deepEqual(await api.createCard(payload), { data: created, etag: TAG, boardEtag });

  for (const malformed of [
    { ...created, id: BOARD },
    { ...created, board_id: card },
    { ...created, ticket: 0 },
    { ...created, title: undefined },
    { ...created, owner_id: undefined, extra: null },
    { ...created, owner_id: null },
    { ...created, owner_id: "" },
    { ...created, revision: undefined },
    ...[null, false, 0, -1, 1.5, "1", Number.MAX_SAFE_INTEGER + 1].map((revision) => ({
      ...created,
      revision,
    })),
    { ...created, etag: TAG },
  ]) {
    let calls = 0;
    const invalid = client(async () => {
      calls++;
      return Response.json(malformed, {
        status: 201,
        headers: { ETag: TAG, "X-Board-ETag": boardEtag },
      });
    });
    await assert.rejects(invalid.createCard(payload), {
      name: "ApiError",
      code: "invalid_response",
      status: 201,
      outcomeUnknown: true,
    });
    assert.equal(calls, 1);
  }
  assert.equal((await api.getCard(card)).data.id, card);
  assert.equal(
    (await api.listCards(BOARD, { ticket: 1, cursor: "opaque&cursor" })).data.cards[0].id,
    card,
  );
  assert.throws(() => api.listCards(BOARD, { ticket: -1 }), TypeError);
  assert.deepEqual(
    calls,
    [
      "/cards",
      "/cards/" + card,
      "/cards?board_id=" + BOARD + "&ticket=1&cursor=opaque%26cursor",
    ].map((path) => "/api/2026-11-10" + path),
  );
});

test("does not repeat a stale card creation or a reply missing the board tag", async () => {
  for (const [response, code, status, outcomeUnknown] of [
    [new Response(null, { status: 412 }), "http_error", 412, false],
    [Response.json({}, { status: 201, headers: { ETag: TAG } }), "invalid_response", 201, true],
  ]) {
    let calls = 0;
    const api = client(async () => {
      calls++;
      return response;
    });
    await assert.rejects(
      api.createCard({
        board_id: BOARD,
        lane_id: BOARD,
        expected_board_etag: TAG,
        title: "Fixture",
      }),
      { name: "ApiError", code, status, outcomeUnknown },
    );
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
    type: "snapshot",
    resource: "board",
    board_id: BOARD,
    board_revision: 1,
  };
  const other = "e30fa21d-76d7-4eba-b1a4-10962bb6a582";
  let canceled = false;
  const api = client(async (url, options) => {
    assert.equal(
      url,
      "/api/2026-11-10/events?board_id=" + BOARD + "&watch=" + other + "&watch=" + BOARD,
    );
    assert.equal(options.headers.get("Accept"), "text/event-stream");
    assert.equal(options.headers.get("Authorization"), "Bearer fixture-capability");
    assert.equal(options.signal, signal);
    assert.equal(options.mode, "same-origin");
    assert.equal(options.credentials, "omit");
    assert.equal(options.headers.has("Last-Event-ID"), false);
    const encoder = new TextEncoder();
    return new Response(
      new ReadableStream({
        start(controller) {
          controller.enqueue(encoder.encode(": heartbeat\r\n\r\nevent: snapshot\r\nda"));
          controller.enqueue(encoder.encode("ta: " + JSON.stringify(event) + "\r\n\r\n"));
        },
        cancel() {
          canceled = true;
        },
      }),
      { headers: { "Content-Type": "text/event-stream; charset=utf-8" } },
    );
  });

  for await (const snapshot of api.streamEvents({
    boardId: BOARD,
    watch: [other, BOARD],
    signal,
  })) {
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
    [board, true],
    [workspace, true],
    [catalog, true],
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
    const api = client(
      () =>
        new Response("data: " + JSON.stringify(event) + "\n\n", {
          headers: { "Content-Type": "text/event-stream" },
        }),
    );
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
    name: "ApiError",
    message: "invalid_response",
    status: 200,
    outcomeUnknown: false,
  });
  assert.equal(calls, 1);
});

test("cancels unread bodies on refusals or invalid response metadata", async () => {
  for (const [status, headers, code] of [
    [412, {}, "http_error"],
    [201, { ETag: TAG }, "invalid_response"],
  ]) {
    let canceled = false;
    const api = client(
      () =>
        new Response(
          new ReadableStream({
            cancel() {
              canceled = true;
            },
          }),
          { status, headers },
        ),
    );
    await assert.rejects(
      api.createCard({
        board_id: BOARD,
        lane_id: BOARD,
        expected_board_etag: TAG,
        title: "Fixture",
      }),
      { name: "ApiError", code, status },
    );
    assert.equal(canceled, true);
  }
});

test("validates changed and unchanged board tags", async () => {
  const nextTag = '"updated_board_tag"';
  const changed = client(() => Response.json({}, { headers: { ETag: nextTag } }));
  assert.deepEqual(await changed.getBoardIfChanged(BOARD, TAG), { data: {}, etag: nextTag });
  for (const [status, etag] of [
    [304, nextTag],
    [304, null],
    [200, TAG],
    [200, "W/" + TAG],
  ]) {
    const api = client(
      () => new Response(null, { status, headers: etag === null ? {} : { ETag: etag } }),
    );
    await assert.rejects(api.getBoardIfChanged(BOARD, TAG), {
      name: "ApiError",
      code: "invalid_response",
      status,
      outcomeUnknown: false,
    });
  }
});

test("rejects incomplete event frames and handles the exact bound across chunks", async () => {
  const encoder = new TextEncoder();
  function stream(chunks) {
    return client(
      () =>
        new Response(
          new ReadableStream({
            start(controller) {
              for (const chunk of chunks) {
                controller.enqueue(typeof chunk === "string" ? encoder.encode(chunk) : chunk);
              }
              controller.close();
            },
          }),
          { headers: { "Content-Type": "text/event-stream" } },
        ),
    ).streamEvents();
  }
  const frame = ":" + "x".repeat(65535);
  assert.equal((await stream([frame, "\r", "\n\r", "\n"]).next()).done, true);
  for (const chunks of [[frame + "x", "\n\n"], ["data: {}"], [new Uint8Array([0xc3])]]) {
    await assert.rejects(stream(chunks).next(), {
      name: "ApiError",
      code: "invalid_response",
      status: 200,
    });
  }
});

test("lifecycle writes use the service validator and confirm the addressed receipt", async () => {
  const id = "2dd42d30-8545-7e43-bd65-e5430893d196";
  const activation = { id, build_id: BOARD, state: "accepted", error_code: null };
  const calls = [];
  const api = client(async (url, options) => {
    calls.push([
      url,
      options.method,
      options.headers.get("If-Match"),
      options.body && JSON.parse(options.body),
    ]);
    if (options.method === "POST")
      return Response.json(activation, {
        status: 202,
        headers: { Location: "/api/2026-11-10/activations/" + id },
      });
    if (options.method === "GET")
      return Response.json(
        { ...activation, state: "activated" },
        {
          headers: { ETag: '"' + id + '-1"' },
        },
      );
    return Response.json(
      { ...SERVICE, state: "draining" },
      {
        status: 202,
        headers: { ETag: SERVICE_TAG.replace('-0"', '-1"') },
      },
    );
  });
  assert.equal((await api.activateBuild(id.toUpperCase(), BOARD, SERVICE_TAG)).state, "accepted");
  assert.equal((await api.getActivation(id, BOARD)).state, "activated");
  assert.equal((await api.stopService(SERVICE, SERVICE_TAG)).state, "draining");
  assert.deepEqual(calls, [
    [
      "/api/2026-11-10/activations",
      "POST",
      null,
      { id, build_id: BOARD, expected_service_etag: SERVICE_TAG },
    ],
    ["/api/2026-11-10/activations/" + id, "GET", null, undefined],
    ["/api/2026-11-10/service", "PATCH", SERVICE_TAG, { state: "stopped" }],
  ]);
});

test("lifecycle transport loss and malformed 202 receipts remain uncertain without replay", async () => {
  let attempts = 0;
  const api = client(async () => {
    attempts++;
    if (attempts === 1) throw new TypeError("Disconnected");
    return Response.json(
      { ...SERVICE, state: "draining", port: 1 },
      { status: 202, headers: { ETag: SERVICE_TAG } },
    );
  });
  await assert.rejects(api.activateBuild(OPERATION, BOARD, SERVICE_TAG), {
    code: "transport_failed",
    outcomeUnknown: true,
  });
  await assert.rejects(api.stopService(SERVICE, SERVICE_TAG), {
    code: "invalid_response",
    outcomeUnknown: true,
  });
  assert.equal(attempts, 2);
});

test("shutdown distinguishes a validated final publication refusal from a generic unavailable response", async () => {
  const problem = {
    type: "about:blank",
    status: 503,
    code: "unavailable",
    outcome: "not_applied",
    title: "Service unavailable",
    detail: "Final publication failed. The service remains running.",
    request_id: BOARD,
    errors: [],
  };
  let response = problem;
  const api = client(
    async () =>
      new Response(JSON.stringify(response), {
        status: 503,
        headers: { "Content-Type": "application/problem+json" },
      }),
  );
  await assert.rejects(api.stopService(SERVICE, SERVICE_TAG), {
    code: "final_publication_failed",
    status: 503,
    outcomeUnknown: false,
  });
  response = { ...problem, outcome: "unknown" };
  await assert.rejects(api.stopService(SERVICE, SERVICE_TAG), {
    code: "http_error",
    status: 503,
    outcomeUnknown: true,
  });
});

test("lifecycle reads require exact HTTP success, JSON media and strong response tags", async () => {
  const activation = { id: OPERATION, build_id: BOARD, state: "activated", error_code: null };
  const page = { builds: [], next_cursor: null };
  const reads = [
    ["/service", SERVICE, SERVICE_TAG, (api) => api.getService()],
    ["/builds", page, TAG, (api) => api.listBuilds()],
    ["/activations/" + OPERATION, activation, TAG, (api) => api.getActivation(OPERATION, BOARD)],
  ];
  for (const [path, data, tag, read] of reads) {
    const headers = { "Content-Type": "application/json; charset=utf-8", ETag: tag };
    const valid = client(async (url) => {
      assert.equal(url, "/api/2026-11-10" + path);
      return new Response(JSON.stringify(data), { status: 200, headers });
    });
    await read(valid);
    for (const [status, altered] of [
      [201, headers],
      [200, { ...headers, "Content-Type": "text/plain" }],
      [200, { ...headers, ETag: "W/" + tag }],
      [200, { "Content-Type": headers["Content-Type"] }],
    ]) {
      const api = client(
        async () => new Response(JSON.stringify(data), { status, headers: altered }),
      );
      await assert.rejects(read(api), { code: "invalid_response", status, outcomeUnknown: false });
    }
  }
  const separateTag = client(async () => Response.json(SERVICE, { headers: { ETag: TAG } }));
  assert.deepEqual(await separateTag.getService(), { data: SERVICE, etag: TAG });
  const bodyTag = client(async () =>
    Response.json({ ...SERVICE, etag: TAG }, { headers: { ETag: TAG } }),
  );
  await assert.rejects(bodyTag.getService(), { code: "invalid_response" });
});

test("staged build pages validate their closed shape and OpenAPI array and cursor bounds", async () => {
  const build = { id: BOARD, version: "stage", validation: "valid", error_code: null };
  const cursor = "x".repeat(2048);
  let page = { builds: Array(200).fill(build), next_cursor: cursor };
  const api = client(async () => Response.json(page, { headers: { ETag: TAG } }));
  assert.equal((await api.listBuilds()).data.next_cursor, cursor);
  for (page of [
    { builds: [build], next_cursor: null, extra: true },
    { builds: Array(201).fill(build), next_cursor: null },
    { builds: [{ ...build, extra: true }], next_cursor: null },
    { builds: [], next_cursor: cursor + "x" },
    { builds: [], next_cursor: "" },
    { builds: [] },
  ]) {
    await assert.rejects(api.listBuilds(), { code: "invalid_response" });
  }
});

test("lifecycle mutations with non-JSON or missing receipt media stay uncertain after one write", async () => {
  const activation = { id: OPERATION, build_id: BOARD, state: "accepted", error_code: null };
  const draining = { ...SERVICE, state: "draining" };
  const writes = [
    [
      activation,
      { Location: "/api/2026-11-10/activations/" + BOARD },
      (api) => api.activateBuild(OPERATION, BOARD, SERVICE_TAG),
    ],
    [draining, { ETag: SERVICE_TAG }, (api) => api.stopService(SERVICE, SERVICE_TAG)],
  ];
  for (const [body, headers, write] of writes) {
    for (const media of ["text/plain", undefined]) {
      let requests = 0;
      const api = client(async () => {
        requests++;
        return new Response(new TextEncoder().encode(JSON.stringify(body)), {
          status: 202,
          headers: media ? { ...headers, "Content-Type": media } : headers,
        });
      });
      await assert.rejects(write(api), {
        code: "invalid_response",
        status: 202,
        outcomeUnknown: true,
      });
      assert.equal(requests, 1);
    }
  }
});

test("operation IDs carry current milliseconds, RFC version and variant, and random entropy", () => {
  const before = Date.now();
  const ids = Array.from({ length: 100 }, () => newOperationId());
  const after = Date.now();
  assert.equal(new Set(ids).size, ids.length);
  for (const id of ids) {
    assert.match(id, /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    const timestamp = Number.parseInt(id.replaceAll("-", "").slice(0, 12), 16);
    assert.ok(timestamp >= before && timestamp <= after);
  }
});

test("operation inputs accept mixed case and reject swapped resource versions before dispatch", async () => {
  let calls = 0;
  const api = client(async (url, options) => {
    calls++;
    assert.equal(url, "/api/2026-11-10/activations");
    const input = JSON.parse(options.body);
    assert.equal(input.id, OPERATION);
    return Response.json(
      { id: OPERATION, build_id: BOARD, state: "accepted", error_code: null },
      {
        status: 202,
        headers: { Location: "/api/2026-11-10/activations/" + OPERATION },
      },
    );
  });
  await api.activateBuild(OPERATION.toUpperCase(), BOARD, SERVICE_TAG);
  for (const action of [
    () => api.activateBuild(BOARD, BOARD, SERVICE_TAG),
    () => api.activateBuild(OPERATION, OPERATION, SERVICE_TAG),
    () => api.createBackup(BOARD, BOARD, TAG),
    () => api.publishBackup(OPERATION, BOARD, BOARD),
    () => api.approveCard(BOARD, BOARD, TAG, BOARD, TAG),
  ])
    await assert.rejects(action, TypeError);
  assert.equal(calls, 1);
});

test("service JSON stays separate from conditional lifecycle headers", async () => {
  const calls = [];
  const api = client(async (_url, options) => {
    calls.push(options.method);
    if (options.method === "GET") return Response.json(SERVICE, { headers: { ETag: SERVICE_TAG } });
    assert.equal(options.headers.get("If-Match"), SERVICE_TAG);
    assert.deepEqual(JSON.parse(options.body), { state: "stopped" });
    return Response.json(
      { ...SERVICE, state: "draining" },
      {
        status: 202,
        headers: { ETag: SERVICE_TAG.replace('-0"', '-1"') },
      },
    );
  });
  const reply = await api.getService();
  assert.equal(Object.keys(reply.data).length, 6);
  assert.equal(Object.hasOwn(reply.data, "etag"), false);
  assert.equal(reply.etag, SERVICE_TAG);
  assert.equal(Object.hasOwn(await api.stopService(reply.data, reply.etag), "etag"), false);
  await assert.rejects(api.stopService(reply.data, undefined), TypeError);
  assert.deepEqual(calls, ["GET", "PATCH"]);

  const obsolete = client(async () =>
    Response.json(
      { ...SERVICE, state: "draining", etag: SERVICE_TAG },
      {
        status: 202,
        headers: { ETag: SERVICE_TAG },
      },
    ),
  );
  await assert.rejects(obsolete.stopService(SERVICE, SERVICE_TAG), {
    code: "invalid_response",
    outcomeUnknown: true,
  });
});

test("catalog numeric revisions remain distinct from the HTTP collection validator", async () => {
  const boards = [
    { id: BOARD, name: "Board", revision: 7 },
    { id: "a833f302-b6d0-476a-a39d-f70d49610251", name: null, revision: null },
  ];
  const api = client(async () =>
    Response.json({ boards, next_cursor: null }, { headers: { ETag: TAG } }),
  );
  const reply = await api.listBoards();
  assert.deepEqual(reply.data.boards, boards);
  assert.equal(reply.etag, TAG);
  assert.equal(Object.hasOwn(reply.data.boards[0], "etag"), false);
});

test("shutdown requires a strong response HTTP validator without replay", async () => {
  for (const etag of [undefined, "W/" + SERVICE_TAG, "invalid"]) {
    let attempts = 0;
    const api = client(async () => {
      attempts++;
      return Response.json(
        { ...SERVICE, state: "draining" },
        {
          status: 202,
          headers: etag === undefined ? {} : { ETag: etag },
        },
      );
    });
    await assert.rejects(api.stopService(SERVICE, SERVICE_TAG), {
      code: "invalid_response",
      status: 202,
      outcomeUnknown: true,
    });
    assert.equal(attempts, 1);
  }
});
