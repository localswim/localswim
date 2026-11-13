/// <reference types="node" />
import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import { registerHooks } from "node:module";
import test from "node:test";

// Browser imports use .js specifiers. Node strips types directly from the source for this test.
const hook = registerHooks({
  resolve(specifier, context, nextResolve) {
    if (
      context.parentURL?.startsWith(new URL("../", import.meta.url).href) &&
      specifier.startsWith(".") &&
      specifier.endsWith(".js")
    ) {
      return nextResolve(`${specifier.slice(0, -3)}.ts`, context);
    }
    return nextResolve(specifier, context);
  },
});
const [{ createEntryFixture }, { readBoard }, { readFailure }] = await Promise.all([
  import("../fixtures.ts"),
  import("./read.ts"),
  import("../http.ts"),
]);
hook.deregister();

const api = "/api/2026-11-10";
const cursor = "opaque:page 2/+";
const requestId = "80a80a80-0000-4000-8000-000000000001";
const missingPolicy = {
  type: "about:blank",
  title: "Not Found",
  status: 404,
  code: "not_found",
  detail: "The resource does not exist.",
  request_id: requestId,
  outcome: "not_applicable",
  errors: [],
};

function policyNotFound() {
  return Response.json(missingPolicy, {
    status: 404,
    headers: { "Content-Type": "application/problem+json", "X-Request-ID": requestId },
  });
}

/** @typedef {import("../contracts.js").BoardData} BoardData */

function boardFixture() {
  const state = createEntryFixture().board;
  assert.equal(state.status, "ready");
  return state.current.value;
}

/**
 * @param {import("node:test").TestContext} context
 * @param {BoardData} fixture
 * @param {(resource: string, query: URLSearchParams) => Response | undefined} override
 */
function mockRead(context, fixture, override = () => undefined) {
  /** @type {string[]} */
  const calls = [];
  const board = fixture.view.board;
  const boardPath = `/boards/${board.id}`;
  const signal = new AbortController().signal;
  const accessToken = Buffer.alloc(32, 1).toString("base64url");
  const principal = fixture.users[0];
  assert.ok(principal);
  /** @type {typeof fetch} */
  const fetchMock = (path, options) => {
    assert.ok(typeof path === "string" && options !== undefined);
    const url = new URL(path, "http://127.0.0.1:53146");
    assert.ok(url.pathname.startsWith(`${api}/`));
    const resource = url.pathname.slice(api.length);
    if (resource === "/browser-session") {
      assert.equal(options.method, "POST");
      return Promise.resolve(
        Response.json({
          principal_id: principal.id,
          kind: "human",
          api_version: "2026-11-10",
          workspace_admin: true,
          access_token: accessToken,
          expires_on_service_replacement: true,
        }),
      );
    }
    assert.equal(options.method, "GET");
    assert.ok(options.signal instanceof AbortSignal && !options.signal.aborted);
    assert.equal(new Headers(options.headers).get("Authorization"), `Bearer ${accessToken}`);
    calls.push(`${resource}${url.search}`);
    const response = override(resource, url.searchParams);
    if (response !== undefined) return Promise.resolve(response);
    switch (resource) {
      case boardPath:
        return Promise.resolve(
          Response.json(url.searchParams.has("view") ? fixture.view : board, {
            headers: { ETag: fixture.boardEtag },
          }),
        );
      case `/policies/${board.policy_id}`:
        return Promise.resolve(Response.json(fixture.policy));
      case "/users":
      case "/priorities": {
        assert.equal(url.searchParams.get("board_id"), board.id);
        assert.equal(url.searchParams.get("limit"), "200");
        const key = resource === "/users" ? "users" : "priorities";
        const items = fixture[key];
        const nextPage = url.searchParams.has("cursor");
        if (nextPage) assert.equal(url.searchParams.get("cursor"), cursor);
        return Promise.resolve(
          Response.json({
            [key]: nextPage ? items.slice(200) : items.slice(0, 200),
            next_cursor: !nextPage && items.length > 200 ? cursor : null,
          }),
        );
      }
      default:
        throw new Error(`Unexpected resource ${resource}`);
    }
  };
  context.mock.method(globalThis, "fetch", fetchMock);
  return { calls, signal };
}

void test("joins the native lane view, paged users and priorities, and policy", async (context) => {
  const initial = boardFixture();
  const sampleUser = initial.users[1];
  const samplePriority = initial.priorities[0];
  assert.ok(sampleUser && samplePriority);
  const users = [...initial.users];
  const priorities = [...initial.priorities];
  // Retired lanes remain in the board's ID registry. Archived parents are omitted from the view.
  const laneIds = [...initial.view.board.lane_ids, "80a80a80-0000-4000-8000-000000000002"];
  const cards = initial.view.cards.map((card, index) =>
    index === 0 ? { ...card, parent_id: "80a80a80-0000-4000-8000-000000000003" } : card,
  );
  for (let index = 0; index < 200; index += 1) {
    const suffix = String(index).padStart(12, "0");
    users.push({ ...sampleUser, id: `44444444-0000-4000-8000-${suffix}` });
    priorities.push({
      ...samplePriority,
      id: `55555555-0000-4000-8000-${suffix}`,
      key: `priority_${index}`,
      rank: index + 5,
    });
  }
  const board = {
    ...initial.view.board,
    lane_ids: laneIds,
    user_ids: users.map((user) => user.id),
    priority_ids: priorities.map((priority) => priority.id),
  };
  const fixture = { ...initial, users, priorities, view: { ...initial.view, board, cards } };
  const { calls, signal } = mockRead(context, fixture);
  assert.deepEqual(await readBoard(board.id.toUpperCase(), signal), fixture);
  assert.equal(calls[0], `/boards/${board.id}?view=lanes`);
  assert.equal(calls.at(-1), `/boards/${board.id}`);
  assert.equal(calls.filter((path) => path.startsWith("/users?")).length, 2);
  assert.equal(calls.filter((path) => path.startsWith("/priorities?")).length, 2);
});

void test("retries the whole board read once when the board revision changes", async (context) => {
  const fixture = boardFixture();
  const board = fixture.view.board;
  const nextBoard = { ...board, revision: board.revision + 1 };
  const nextTag = '"fixture-board-alpha-2"';
  const nextView = { ...fixture.view, board: nextBoard };
  let changed = false;
  const { calls, signal } = mockRead(context, fixture, (resource, query) => {
    if (resource !== `/boards/${board.id}`) return undefined;
    if (query.has("view")) {
      return changed ? Response.json(nextView, { headers: { ETag: nextTag } }) : undefined;
    }
    changed = true;
    return Response.json(nextBoard, { headers: { ETag: nextTag } });
  });
  assert.deepEqual(await readBoard(board.id, signal), {
    ...fixture,
    boardEtag: nextTag,
    view: nextView,
  });
  assert.equal(calls.filter((path) => path.endsWith("?view=lanes")).length, 2);
  assert.equal(calls.filter((path) => path.startsWith("/policies/")).length, 2);
});

void test("accepts native large rank and triage values in canonical view order", async (context) => {
  const initial = boardFixture();
  const boardId = initial.view.board.id;
  const rank = Number.MAX_SAFE_INTEGER + 1;
  const triageScore = -rank;
  const priorities = initial.priorities.map((priority, index) =>
    index < 2 ? { ...priority, rank } : priority,
  );
  const cards = initial.view.cards.map((card) => ({ ...card, triage_score: triageScore }));
  const fixture = { ...initial, priorities, view: { ...initial.view, cards } };
  const { signal } = mockRead(context, fixture, (resource) => {
    if (resource !== "/priorities") return undefined;
    // Two distinct valid Go integers round to the same JavaScript Number.
    const body = JSON.stringify({ priorities, next_cursor: null }).replace(
      `"rank":${rank}`,
      '"rank":9007199254740993',
    );
    return new Response(body, { headers: { "Content-Type": "application/json" } });
  });
  assert.deepEqual(await readBoard(boardId, signal), fixture);
});

void test("retries when restored membership differs from the opening view", async (context) => {
  const fixture = boardFixture();
  const board = fixture.view.board;
  const sample = fixture.users[1];
  assert.ok(sample);
  const users = [...fixture.users, { ...sample, id: requestId }];
  const nextBoard = {
    ...board,
    revision: board.revision + 1,
    user_ids: users.map((user) => user.id),
  };
  const nextView = { ...fixture.view, board: nextBoard };
  const nextTag = '"fixture-board-alpha-2"';
  let summaryChecked = false;
  const { calls, signal } = mockRead(context, fixture, (resource, query) => {
    if (resource === "/users") return Response.json({ users, next_cursor: null });
    if (resource !== `/boards/${board.id}`) return undefined;
    if (query.has("view")) {
      return summaryChecked ? Response.json(nextView, { headers: { ETag: nextTag } }) : undefined;
    }
    summaryChecked = true;
    return Response.json(nextBoard, { headers: { ETag: nextTag } });
  });
  assert.deepEqual(await readBoard(board.id, signal), {
    ...fixture,
    users,
    view: nextView,
    boardEtag: nextTag,
  });
  assert.equal(calls.filter((path) => path.endsWith("?view=lanes")).length, 2);
});

void test("rejects collection membership mismatch at a stable revision", async (context) => {
  const fixture = boardFixture();
  const { calls, signal } = mockRead(context, fixture, (resource) =>
    resource === "/users" ? Response.json({ users: [], next_cursor: null }) : undefined,
  );
  await assert.rejects(readBoard(fixture.view.board.id, signal), (/** @type {unknown} */ error) => {
    assert.deepEqual(readFailure(error), { kind: "invalid_response" });
    return true;
  });
  assert.equal(calls.at(-1), `/boards/${fixture.view.board.id}`);
  assert.equal(calls.filter((path) => path.endsWith("?view=lanes")).length, 1);
});

void test("retries with the new policy ID after a concurrent restore", async (context) => {
  const fixture = boardFixture();
  const board = fixture.view.board;
  const policy = { ...fixture.policy, id: requestId, revision: board.revision + 1 };
  const nextBoard = { ...board, revision: policy.revision, policy_id: policy.id };
  const nextView = { ...fixture.view, board: nextBoard };
  const nextTag = '"fixture-board-alpha-2"';
  let restored = false;
  const { calls, signal } = mockRead(context, fixture, (resource, query) => {
    if (resource === `/policies/${board.policy_id}`) {
      restored = true;
      return policyNotFound();
    }
    if (resource === `/policies/${policy.id}`) return Response.json(policy);
    if (resource !== `/boards/${board.id}` || !restored) return undefined;
    return Response.json(query.has("view") ? nextView : nextBoard, { headers: { ETag: nextTag } });
  });
  assert.deepEqual(await readBoard(board.id, signal), {
    ...fixture,
    boardEtag: nextTag,
    policy,
    view: nextView,
  });
  assert.deepEqual(
    calls.filter((path) => path.startsWith("/policies/")),
    [`/policies/${board.policy_id}`, `/policies/${policy.id}`],
  );
});

void test("preserves a policy 404 at a stable board revision", async (context) => {
  const fixture = boardFixture();
  const board = fixture.view.board;
  const { calls, signal } = mockRead(context, fixture, (resource) =>
    resource === `/policies/${board.policy_id}` ? policyNotFound() : undefined,
  );
  await assert.rejects(readBoard(board.id, signal), (/** @type {unknown} */ error) => {
    assert.deepEqual(readFailure(error), { kind: "http", problem: missingPolicy });
    return true;
  });
  assert.equal(calls.at(-1), `/boards/${board.id}`);
  assert.equal(calls.filter((path) => path.endsWith("?view=lanes")).length, 1);
});

void test("rejects a valid-looking policy from a different board", async (context) => {
  const initial = boardFixture();
  const fixture = { ...initial, policy: { ...initial.policy, board_id: requestId } };
  const { signal } = mockRead(context, fixture);
  await assert.rejects(readBoard(fixture.view.board.id, signal), (/** @type {unknown} */ error) => {
    assert.deepEqual(readFailure(error), { kind: "invalid_response" });
    return true;
  });
});

void test("preserves a known unavailable board response", async (context) => {
  const fixture = boardFixture();
  const problem = {
    type: "about:blank",
    title: "Service Unavailable",
    status: 503,
    code: "unavailable",
    detail: "This board is closed or draining.",
    request_id: requestId,
    outcome: "not_applicable",
    errors: [],
  };
  const { calls, signal } = mockRead(context, fixture, () =>
    Response.json(problem, {
      status: 503,
      headers: { "Content-Type": "application/problem+json", "X-Request-ID": requestId },
    }),
  );
  await assert.rejects(readBoard(fixture.view.board.id, signal), (/** @type {unknown} */ error) => {
    assert.deepEqual(readFailure(error), { kind: "http", problem });
    return true;
  });
  assert.equal(calls.length, 1);
});
