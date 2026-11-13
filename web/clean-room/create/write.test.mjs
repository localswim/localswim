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
const { createCard, inspectCardCreation } = await import("./write.ts");
hook.deregister();

const api = "/api/2026-11-10";
const stamp = "2026-11-13 12:00:00+00:00";
const boardEtag = '"aaaaaaaaaaaaaaaa"';
const nextBoardEtag = '"bbbbbbbbbbbbbbbb"';
const cardEtag = '"cccccccccccccccc"';

/** @param {number} index */
function id(index) {
  return `80a80a80-0000-4000-8000-${String(index).padStart(12, "0")}`;
}

/** @type {import("./contracts.js").CreateInput} */
const input = {
  requestId: id(11),
  cardId: id(1),
  boardId: id(2),
  boardEtag,
  boardRevision: 7,
  laneId: id(3),
  title: "  Exact title ✨ ",
  description: "  Exact\n\nbody  ",
  ownerId: id(4),
  priorityId: id(5),
  priorities: [
    { id: id(5), rank: 1 },
    { id: id(6), rank: 0 },
  ],
  completed: false,
  relationships: [
    { kind: "blocks", targetCardId: id(7), direction: "outgoing" },
    { kind: "references", targetCardId: id(7), direction: "incoming" },
  ],
};

const card = {
  id: input.cardId,
  board_id: input.boardId,
  revision: 8,
  created_at: stamp,
  updated_at: stamp,
  ticket: 10,
  title: input.title,
  description: input.description,
  lane_id: input.laneId,
  owner_id: input.ownerId,
  priority_id: id(6),
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

const links = [
  {
    id: id(8),
    source_card_id: card.id,
    target_card_id: id(7),
    kind: "blocks",
    board_id: card.board_id,
    revision: card.revision,
    created_at: stamp,
    updated_at: stamp,
  },
  {
    id: id(9),
    source_card_id: id(7),
    target_card_id: card.id,
    kind: "references",
    board_id: card.board_id,
    revision: card.revision,
    created_at: stamp,
    updated_at: stamp,
  },
];

function browserSession() {
  return Response.json({
    principal_id: input.ownerId,
    kind: "human",
    workspace_admin: true,
    api_version: "2026-11-10",
    expires_on_service_replacement: true,
    access_token: Buffer.alloc(32, 1).toString("base64url"),
  });
}

/** @param {HeadersInit | undefined} headers */
function createdResponse(headers) {
  const requestId = new Headers(headers).get("X-Request-ID");
  assert.equal(requestId, input.requestId);
  return Response.json(card, {
    status: 201,
    headers: {
      "X-Request-ID": requestId,
      ETag: cardEtag,
      "X-Board-ETag": nextBoardEtag,
      Location: `${api}/cards/${card.id}`,
    },
  });
}

void test("creates one card with the original UUID, board precondition, exact text and seeds", async (t) => {
  /** @type {typeof fetch} */
  const fetchMock = async (path, options) => {
    if (path === `${api}/browser-session`) return browserSession();
    assert.equal(path, `${api}/cards`);
    assert.ok(options);
    assert.equal(options.method, "POST");
    assert.equal(
      options.body,
      JSON.stringify({
        id: input.cardId,
        board_id: input.boardId,
        expected_board_etag: boardEtag,
        title: input.title,
        description: input.description,
        lane_id: input.laneId,
        owner_id: input.ownerId,
        priority_id: input.priorityId,
        relationships: [
          { kind: "blocks", source_card_id: card.id, target_card_id: id(7) },
          { kind: "references", source_card_id: id(7), target_card_id: card.id },
        ],
      }),
    );
    return createdResponse(options.headers);
  };
  const post = t.mock.method(globalThis, "fetch", fetchMock);
  assert.deepEqual(await createCard(input), { outcome: "committed", value: { card, cardEtag } });
  assert.equal(
    post.mock.calls.filter(({ arguments: [path] }) => path === `${api}/cards`).length,
    1,
  );
});

void test("inspects uncertain creation by original UUID and exact directional links without replay", async (t) => {
  let posts = 0;
  let rows = links;
  let requestedPriority = input.priorityId;
  /** @type {typeof card | null} */
  let current = card;
  /** @type {typeof fetch} */
  const fetchMock = async (path, options) => {
    if (path === `${api}/browser-session`) return browserSession();
    if (options?.method === "POST") {
      posts += 1;
      throw new Error("response lost");
    }
    if (path === `${api}/cards/${card.id}`) {
      if (current === null) {
        const requestId = id(10);
        return Response.json(
          {
            type: "about:blank",
            title: "Not Found",
            status: 404,
            code: "not_found",
            detail: "The resource does not exist.",
            request_id: requestId,
            outcome: "not_applicable",
            errors: [],
          },
          {
            status: 404,
            headers: { "Content-Type": "application/problem+json", "X-Request-ID": requestId },
          },
        );
      }
      return Response.json(current, { headers: { ETag: cardEtag } });
    }
    const url = new URL(String(path), "http://localhost");
    if (url.pathname === `${api}/activities`) {
      assert.deepEqual(Object.fromEntries(url.searchParams), {
        board_id: card.board_id,
        card_id: card.id,
        operation: "promote",
        view: "full",
        from: card.created_at,
        to: card.created_at,
        limit: "200",
      });
      return Response.json({
        activities: [
          {
            id: "80a80a80-0000-7000-8000-000000000001",
            actor_id: input.ownerId,
            board_id: card.board_id,
            resource_type: "card",
            resource_id: card.id,
            operation: "promote",
            revision: card.revision,
            at: card.created_at,
            field: "priority_id",
            before: requestedPriority,
            after: card.priority_id,
          },
        ],
        next_cursor: null,
        pagination_complete: true,
      });
    }
    assert.equal(
      path,
      `${api}/relationships?board_id=${card.board_id}&card_id=${card.id}&limit=200`,
    );
    return Response.json({ relationships: rows, next_cursor: null });
  };
  t.mock.method(globalThis, "fetch", fetchMock);
  assert.equal((await createCard(input)).outcome, "unknown");
  assert.deepEqual(await inspectCardCreation(input), {
    outcome: "committed",
    value: { card, cardEtag },
  });
  requestedPriority = id(12);
  assert.deepEqual(await inspectCardCreation(input), { outcome: "unknown" });
  requestedPriority = input.priorityId;
  current = { ...card, priority_id: id(12) };
  assert.deepEqual(await inspectCardCreation(input), { outcome: "unknown" });
  current = card;
  rows = links.map((link) =>
    link.kind === "references" ? { ...link, source_card_id: card.id, target_card_id: id(7) } : link,
  );
  assert.deepEqual(await inspectCardCreation(input), { outcome: "unknown" });
  current = { ...card, description: "different" };
  assert.deepEqual(await inspectCardCreation(input), { outcome: "unknown" });
  current = null;
  assert.deepEqual(await inspectCardCreation(input), { outcome: "unknown" });
  assert.equal(posts, 1);
});

void test("provides the authenticated session to the create guard before dispatch", async (t) => {
  let dispatches = 0;
  let guardCalls = 0;
  let guardPrincipal = "";
  /** @type {typeof fetch} */
  const fetchMock = async (path) => {
    if (path === `${api}/browser-session`) return browserSession();
    dispatches += 1;
    throw new Error("guarded create should not dispatch");
  };
  t.mock.method(globalThis, "fetch", fetchMock);
  await createCard(input, undefined, (session) => {
    guardCalls += 1;
    guardPrincipal = session.principal_id;
    throw new Error("board changed before dispatch");
  });
  assert.equal(guardCalls, 1);
  assert.equal(guardPrincipal, input.ownerId);
  assert.equal(dispatches, 0);
});
