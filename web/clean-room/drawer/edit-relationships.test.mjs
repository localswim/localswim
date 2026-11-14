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
const [{ createEntryFixture }, { applyRelationshipReceipt, writeRelationshipOperation }] =
  await Promise.all([import("../fixtures.ts"), import("./edit-relationships.ts")]);
hook.deregister();

void test("confirmed relationship removal uses the original 204 and removes the row", async (context) => {
  const { board, session } = createEntryFixture();
  assert.equal(board.status, "ready");
  const { view, boardEtag } = board.current.value;
  const [card, other] = view.cards;
  assert.ok(card && other);
  const id = "58700000-0000-4000-8000-000000000001";
  const requestId = "58700000-0000-4000-8000-000000000002";
  const etag = '"opaque_relationship_entity_tag"';
  const nextTag = '"opaque_new_board_entity_tag"';
  /** @type {import("./contracts.ts").DrawerData} */
  const drawer = {
    card,
    boardEtag,
    boardRevision: view.board.revision,
    cardEtag: '"opaque_card_entity_tag"',
    parent: null,
    children: [],
    relationships: [{ id, kind: "references", direction: "outgoing", card: other }],
    comments: [],
    activity: [],
  };
  /** @type {import("./edit-relationships.ts").RelationshipOperation} */
  const input = {
    action: "remove",
    actorId: session.principal_id,
    id,
    boardId: view.board.id,
    cardId: card.id,
    boardEtag,
    boardRevision: drawer.boardRevision,
    etag,
    before: { kind: "references", source_card_id: card.id, target_card_id: other.id },
  };
  context.mock.method(
    globalThis,
    "fetch",
    async (/** @type {string} */ path, /** @type {RequestInit} */ options) => {
      if (path.endsWith("/browser-session")) {
        return Response.json({
          ...session,
          access_token: Buffer.alloc(32, 5).toString("base64url"),
          expires_on_service_replacement: true,
        });
      }
      assert.equal(options.method, "DELETE");
      assert.equal(path, `/api/2026-11-10/relationships/${id}`);
      assert.equal(options.body, undefined);
      const headers = new Headers(options.headers);
      assert.equal(headers.get("If-Match"), etag);
      assert.equal(headers.get("X-Expected-Board-ETag"), boardEtag);
      assert.equal(headers.get("X-Request-ID"), requestId);
      return new Response(null, {
        status: 204,
        headers: { "X-Request-ID": requestId, "X-Board-ETag": nextTag },
      });
    },
  );
  const result = await writeRelationshipOperation(input, requestId);
  assert.equal(result.outcome, "committed");
  const next = applyRelationshipReceipt(drawer, input, result.value, view.cards);
  assert.equal(drawer.relationships.length, 1);
  assert.deepEqual(next.relationships, []);
  assert.equal(next.boardRevision, drawer.boardRevision + 1);
  assert.equal(next.boardEtag, nextTag);
});
