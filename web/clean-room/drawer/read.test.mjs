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
const [{ createEntryFixture }, { readDrawer, readDrawerTicket }, { readFailure }] =
  await Promise.all([import("../fixtures.ts"), import("./read.ts"), import("../http.ts")]);
hook.deregister();

const api = "/api/2026-11-10";
const opaqueCursor = "opaque:page 2/+";
const writerId = "019be000-0000-7000-8000-000000000001";
const uuid = (/** @type {number} */ index) =>
  `58700000-0000-4000-8000-${String(index).padStart(12, "0")}`;

function fixture() {
  const state = createEntryFixture().board;
  assert.equal(state.status, "ready");
  const { board, cards } = state.current.value.view;
  const [selected, parentCard, childCard] = cards;
  assert.ok(selected && parentCard && childCard);
  const parent = { ...parentCard, archived: true, child_ids: [selected.id] };
  const child = { ...childCard, parent_id: selected.id };
  const card = { ...selected, parent_id: parent.id, child_ids: [child.id], comment_count: 51 };
  const at = card.created_at;
  const comments = Array.from({ length: 51 }, (_, index) => ({
    id: uuid(index + 1),
    board_id: board.id,
    card_id: card.id,
    created_at: at,
    text: `Comment ${51 - index}`,
    author_id: card.owner_id,
    ticket: card.ticket,
  }));
  const activity = {
    id: "019be000-0000-7000-8000-000000000002",
    board_id: board.id,
    revision: card.revision,
    at,
    actor_id: card.owner_id,
    operation: "create",
    resource_type: "card",
    resource_id: card.id,
    field: null,
    before: null,
    after: null,
  };
  return { board, card, parent, child, comments, activity };
}

/** @template {{readonly id: string; readonly revision: number}} T @param {T} entity */
function responseWithTag(entity) {
  return Response.json(entity, { headers: { ETag: `"${entity.id}-${entity.revision}"` } });
}

/**
 * @param {import("node:test").TestContext} context
 * @param {ReturnType<typeof fixture>} values
 * @param {(resource: string, query: URLSearchParams) => Response | undefined} override
 */
function mockRead(context, values, override = () => undefined) {
  const { board, card, parent, child, comments } = values;
  const session = createEntryFixture().session;
  const accessToken = Buffer.alloc(32, 5).toString("base64url");
  /** @type {typeof fetch} */
  const fetchMock = async (path, options) => {
    assert.ok(typeof path === "string" && options !== undefined);
    const url = new URL(path, "http://127.0.0.1:53146");
    assert.ok(url.pathname.startsWith(`${api}/`));
    const resource = url.pathname.slice(api.length);
    const query = url.searchParams;
    if (resource === "/browser-session") {
      assert.equal(options.method, "POST");
      return Response.json({
        ...session,
        access_token: accessToken,
        expires_on_service_replacement: true,
      });
    }
    assert.equal(options.method, "GET");
    assert.equal(new Headers(options.headers).get("Authorization"), `Bearer ${accessToken}`);
    const replaced = override(resource, query);
    if (replaced !== undefined) return replaced;
    if (resource === `/boards/${board.id}`) return responseWithTag(board);
    for (const item of [card, parent, child]) {
      if (resource === `/cards/${item.id}`) return responseWithTag(item);
    }
    assert.equal(query.get("board_id"), board.id);
    if (resource === "/cards") {
      assert.equal(query.get("archived"), "include");
      const tickets = [card, parent, child].filter(
        (item) => String(item.ticket) === query.get("ticket"),
      );
      return Response.json({ cards: tickets, next_cursor: null });
    }
    assert.equal(query.get("card_id"), card.id);
    assert.equal(query.get("limit"), "50");
    if (resource === "/comments") {
      assert.equal(query.get("archived"), "include");
      if (query.has("cursor")) {
        assert.equal(query.get("cursor"), opaqueCursor);
        return Response.json({ comments: comments.slice(50), next_cursor: null });
      }
      return Response.json({ comments: comments.slice(0, 50), next_cursor: opaqueCursor });
    }
    if (resource === "/relationships") {
      const edge = {
        id: uuid(90),
        revision: card.revision,
        board_id: board.id,
        created_at: card.created_at,
        updated_at: card.updated_at,
        source_card_id: parent.id,
        target_card_id: card.id,
        kind: "blocks",
      };
      return Response.json({ relationships: [edge], next_cursor: null });
    }
    assert.equal(resource, "/activities");
    assert.equal(query.get("view"), "full");
    assert.equal(query.get("to"), "9999-12-31 23:59:59+00:00");
    return Response.json(activityPage(values, query));
  };
  context.mock.method(globalThis, "fetch", fetchMock);
}

/** @param {ReturnType<typeof fixture>} values @param {URLSearchParams} query */
function activityPage({ board, activity }, query) {
  return {
    activities: [activity],
    next_cursor: null,
    writer_id: writerId,
    board_revision: board.revision,
    coverage: {
      from: query.get("from"),
      to: query.get("to"),
      complete: false,
      code: "startup_gap",
    },
    pagination_complete: true,
    bot_only: false,
  };
}

await test("reads archived linked work and exact ticket, preserving comment and audit order", async (context) => {
  const values = fixture();
  mockRead(context, values);
  const { board, card, parent, child, comments, activity } = values;
  const data = await readDrawer(board.id, card.id);
  assert.equal(data.card.id, card.id);
  assert.equal(data.parent?.archived, true);
  assert.equal(data.parent.id, parent.id);
  assert.equal(data.children[0]?.id, child.id);
  const relationship = data.relationships[0];
  assert.equal(relationship?.direction, "incoming");
  assert.equal(relationship.card.id, parent.id);
  assert.deepEqual(data.comments, comments);
  assert.deepEqual(data.activity, [activity]);
  assert.deepEqual(await readDrawerTicket(board.id, parent.ticket), data.parent);
  assert.equal(await readDrawerTicket(board.id, 999), null);
});

await test("rejects a foreign linked card", async (context) => {
  const values = fixture();
  const { board, card, parent } = values;
  mockRead(context, values, (resource) => {
    if (resource === `/cards/${parent.id}`)
      return responseWithTag({ ...parent, board_id: uuid(99) });
    return undefined;
  });
  await assert.rejects(
    readDrawer(board.id, card.id),
    (error) => readFailure(error).kind === "invalid_response",
  );
});

await test("rejects a mismatched audit revision when the board anchor has not changed", async (context) => {
  const values = fixture();
  const { board, card } = values;
  mockRead(context, values, (resource, query) => {
    if (resource === "/activities") {
      return Response.json({ ...activityPage(values, query), board_revision: board.revision + 1 });
    }
    return undefined;
  });
  await assert.rejects(
    readDrawer(board.id, card.id),
    (error) => readFailure(error).kind === "invalid_response",
  );
});

await test("restarts the drawer read when the writer advances the board revision", async (context) => {
  const values = fixture();
  const { board, card } = values;
  const revised = { ...board, revision: board.revision + 1 };
  let boardReads = 0;
  let activityReads = 0;
  mockRead(context, values, (resource, query) => {
    if (resource === `/boards/${board.id}`) {
      boardReads += 1;
      return responseWithTag(boardReads === 1 ? board : revised);
    }
    if (resource === "/activities") {
      activityReads += 1;
      return Response.json(activityPage({ ...values, board: revised }, query));
    }
    return undefined;
  });
  const data = await readDrawer(board.id, card.id);
  assert.equal(data.boardRevision, revised.revision);
  assert.equal(boardReads, 3);
  assert.equal(activityReads, 2);
});
