/// <reference types="node" />
import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import { registerHooks } from "node:module";
import test from "node:test";

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
const [{ projectCards, readCardMatches }, { readFailure }, { createEntryFixture }] =
  await Promise.all([import("./filter.ts"), import("../http.ts"), import("../fixtures.ts")]);
hook.deregister();

const boardId = "58200000-0000-4000-8000-000000000001";
const requestId = "58200000-0000-4000-8000-000000000002";
const cursor = "opaque /+ token";

/** @param {number} index */
function match(index) {
  const suffix = String(index).padStart(12, "0");
  return { id: `58200000-0000-4000-8001-${suffix}`, board_id: boardId, archived: false };
}

/**
 * @param {import("node:test").TestContext} context
 * @param {(query: URLSearchParams) => Response} page
 */
function mockSearch(context, page) {
  /** @type {typeof fetch} */
  const fetchMock = (path, options) => {
    assert.equal(typeof path, "string");
    const url = new URL(String(path), "http://localhost");
    if (url.pathname === "/api/2026-11-10/browser-session") {
      return Promise.resolve(
        Response.json({
          principal_id: requestId,
          kind: "human",
          workspace_admin: true,
          api_version: "2026-11-10",
          expires_on_service_replacement: true,
          access_token: Buffer.alloc(32, 1).toString("base64url"),
        }),
      );
    }
    assert.equal(url.pathname, "/api/2026-11-10/cards");
    assert.equal(options?.method, "GET");
    assert.equal(url.searchParams.get("board_id"), boardId);
    assert.equal(url.searchParams.get("search_content"), "true");
    assert.equal(url.searchParams.get("limit"), "200");
    return Promise.resolve(page(url.searchParams));
  };
  context.mock.method(globalThis, "fetch", fetchMock);
}

void test("queries native title, ticket and readable content search and follows an opaque cursor", async (context) => {
  const cards = Array.from({ length: 201 }, (_, index) => match(index));
  mockSearch(context, (query) => {
    assert.equal(query.get("q"), "#0001 & completion");
    if (!query.has("cursor"))
      return Response.json({ cards: cards.slice(0, 200), next_cursor: cursor });
    assert.equal(query.get("cursor"), cursor);
    return Response.json({ cards: cards.slice(200), next_cursor: null });
  });
  const matches = await readCardMatches(boardId, " #0001 & completion ");
  assert.deepEqual(
    [...matches],
    cards.map(({ id }) => id),
  );
});

void test("restarts a stale result selection once and discards the incomplete page", async (context) => {
  const cards = Array.from({ length: 200 }, (_, index) => match(index));
  let reads = 0;
  mockSearch(context, (query) => {
    reads += 1;
    if (query.has("cursor")) {
      return Response.json(
        {
          type: "about:blank",
          title: "Bad Request",
          status: 400,
          code: "invalid_request",
          detail: "Invalid or stale cursor.",
          request_id: requestId,
          outcome: "not_applicable",
          errors: [{ field: "cursor", code: "invalid_value" }],
        },
        {
          status: 400,
          headers: { "Content-Type": "application/problem+json", "X-Request-ID": requestId },
        },
      );
    }
    if (reads === 1) return Response.json({ cards, next_cursor: cursor });
    return Response.json({ cards: [match(200)], next_cursor: null });
  });
  assert.deepEqual([...(await readCardMatches(boardId, "changed"))], [match(200).id]);
  assert.equal(reads, 3);
});

void test("rejects cards outside the selected board and duplicate IDs", async (context) => {
  const row = match(0);
  let cards = [{ ...row, board_id: requestId }];
  mockSearch(context, () => Response.json({ cards, next_cursor: null }));
  /** @param {unknown} error */
  const invalid = (error) => readFailure(error).kind === "invalid_response";
  await assert.rejects(readCardMatches(boardId, "term"), invalid);
  cards = [row, row];
  await assert.rejects(readCardMatches(boardId, "term"), invalid);
});

void test("projects cards in lane order, keeping the most recent 24 hours of Completed visible", () => {
  const state = createEntryFixture().board;
  assert.equal(state.status, "ready");
  const { view } = state.current.value;
  const lane = view.lanes.find(({ role }) => role === "completed");
  const [old, cutoff, recent, unknown, ordinary, archived] = view.cards;
  assert.ok(lane && old && cutoff && recent && unknown && ordinary && archived);
  const seeded = {
    ...view,
    cards: [
      { ...old, lane_id: lane.id, completed_at: "2026-11-12 11:59:59+00:00" },
      { ...cutoff, lane_id: lane.id, completed_at: "2026-11-12 12:00:00+00:00" },
      { ...recent, lane_id: lane.id, completed_at: "2026-11-13 11:59:59+00:00" },
      { ...unknown, lane_id: lane.id },
      ordinary,
      { ...archived, archived: true, lane_id: lane.id, completed_at: "2026-11-11 12:00:00+00:00" },
    ],
  };
  const now = Date.parse("2026-11-13T12:00:00Z");
  const collapsed = projectCards(seeded, false, null, now);
  assert.deepEqual(
    collapsed.cardsByLane.get(lane.id)?.map(({ id }) => id),
    [cutoff.id, recent.id, unknown.id],
  );
  assert.equal(collapsed.found, 4);
  assert.equal(collapsed.olderCompleted, 1);
  const expanded = projectCards(seeded, true, null, now);
  assert.deepEqual(
    expanded.cardsByLane.get(lane.id)?.map(({ id }) => id),
    [old.id, cutoff.id, recent.id, unknown.id],
  );
  assert.equal(expanded.found, 5);
  assert.equal(expanded.olderCompleted, 1);
  const hiddenMatch = projectCards(seeded, false, new Set([old.id]), now);
  assert.equal(hiddenMatch.cardsByLane.size, 0);
  assert.equal(hiddenMatch.found, 0);
  assert.equal(hiddenMatch.olderCompleted, 1);
  const search = projectCards(seeded, true, new Set([old.id, ordinary.id, archived.id]), now);
  assert.deepEqual(
    search.cardsByLane.get(lane.id)?.map(({ id }) => id),
    [old.id],
  );
  assert.equal(search.found, 2);
  assert.equal(search.olderCompleted, 1);
  const empty = projectCards(seeded, true, new Set(), now);
  assert.equal(empty.cardsByLane.size, 0);
  assert.equal(empty.found, 0);
  assert.equal(empty.olderCompleted, 1);
});
