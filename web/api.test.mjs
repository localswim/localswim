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
    return Response.json(workspace ? { pinnedBoardIds: [] } : { items: [] }, {
      headers: { ETag: workspace ? TAG : '"board_catalog_04"' },
    });
  }, () => {
    capabilityRequests++;
    return "fixture-capability";
  });

  assert.deepEqual(await readWorkspaceOverview(api), {
    workspace: { data: { pinnedBoardIds: [] }, etag: TAG },
    boards: { data: { items: [] }, etag: '"board_catalog_04"' },
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
    assert.deepEqual(JSON.parse(options.body), { pinnedBoardIds: [BOARD] });
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
    "/lanes?boardId=" + BOARD,
    "/priorities?boardId=" + BOARD,
    "/users?boardId=" + BOARD,
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
      notifications: { targetThreadId: null },
    });
    return Response.json({ notifications: { enabled: true, targetThreadId: null } }, {
      headers: { ETag: '"board_record_007"' },
    });
  });
  assert.equal((await api.configureOwnerNotices(BOARD, { targetThreadId: null }, boardTag))
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
