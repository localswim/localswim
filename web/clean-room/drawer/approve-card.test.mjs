/// <reference types="node" />
import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import { registerHooks } from "node:module";
import test from "node:test";
import { setImmediate } from "node:timers/promises";

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
const [{ createEntryFixture }, { createCardApproval }] = await Promise.all([
  import("../fixtures.ts"),
  import("./approve-card.ts"),
]);
hook.deregister();

void test("single approval closes immediately and settles only on the exact receipt", async (context) => {
  const { board, session } = createEntryFixture();
  assert.equal(board.status, "ready");
  const data = board.current.value;
  const review = data.view.lanes.find(({ role }) => role === "ready_for_review");
  const completed = data.view.lanes.find(({ role }) => role === "completed");
  assert.ok(review && completed);
  const card = data.view.cards.find(({ lane_id }) => lane_id === review.id);
  assert.ok(card);
  const drawer = {
    card,
    cardEtag: '"opaque_card_entity_tag"',
    boardRevision: data.view.board.revision,
    boardEtag: data.boardEtag,
    parent: null,
    children: [],
    relationships: [],
    comments: [],
    activity: [],
  };
  const at = "2026-11-13 12:00:00+00:00";
  const revision = drawer.boardRevision + 1;
  const boardId = data.view.board.id;
  let writes = 0;
  let inspections = 0;
  /** @type {string | undefined} */
  let approvalId;
  /** @type {(() => void) | undefined} */
  let release;
  const hold = new Promise((resolve) => {
    release = () => resolve(undefined);
  });
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
      if (options.method === "POST") {
        writes += 1;
        assert.ok(path.endsWith("/approvals"));
        /** @type {unknown} */
        const body = JSON.parse(String(options.body));
        assert.ok(
          body !== null && typeof body === "object" && "id" in body && typeof body.id === "string",
        );
        approvalId = body.id;
        assert.deepEqual(body, {
          id: approvalId,
          board_id: boardId,
          expected_board_etag: data.boardEtag,
          cards: [{ card_id: card.id, etag: drawer.cardEtag }],
        });
        await hold;
        throw new TypeError("Simulated lost response");
      }
      inspections += 1;
      assert.equal(options.method, "GET");
      assert.equal(path, `/api/2026-11-10/approvals/${approvalId}`);
      const common = { at, board_id: boardId, actor_id: session.principal_id };
      return Response.json(
        {
          ...common,
          id: approvalId,
          board_revision: revision,
          card_ids: [card.id],
          changes: [
            {
              ...common,
              id: approvalId,
              revision,
              operation: "approve",
              resource_type: "card",
              resource_id: card.id,
              field: "lane_id",
              before: review.id,
              after: inspections === 1 ? review.id : completed.id,
            },
            {
              ...common,
              id: "01940000-0000-7000-8000-000000000002",
              revision,
              operation: "comment",
              resource_type: "comment",
              resource_id: "58700000-0000-4000-8000-000000000001",
              field: null,
              before: null,
              after: null,
            },
          ],
        },
        { headers: { ETag: '"opaque_approval_receipt"' } },
      );
    },
  );
  /** @type {string[]} */
  const phases = [];
  /** @type {import("../board/approve-lane.ts").ConfirmedApproval | undefined} */
  let confirmed;
  const controller = createCardApproval({
    current: () => ({ board: data, session, drawer }),
    currentBoard: () => ({ board: data, session }),
    currentActor: () => session.principal_id,
    onStart: () => phases.push("close"),
    onStateChange(id, state) {
      assert.equal(id, boardId);
      phases.push(state.status);
      confirmed = state.receipt;
    },
  });
  context.after(() => controller.dispose());
  controller.approve();
  assert.deepEqual(phases, ["close", "pending"]);
  controller.approve();
  await setImmediate();
  assert.equal(writes, 1);
  assert.equal(confirmed, undefined);
  assert.ok(release);
  release();
  await setImmediate();
  assert.equal(controller.recoverable(boardId, card.id), true);
  assert.deepEqual(phases, ["close", "pending", "checking", "unknown"]);
  controller.checkStatus(boardId, card.id);
  await setImmediate();
  assert.deepEqual(phases, ["close", "pending", "checking", "unknown", "checking", "committed"]);
  assert.deepEqual(confirmed, {
    baseTag: data.boardEtag,
    revision,
    at,
    fromLaneId: review.id,
    toLaneId: completed.id,
    cardIds: [card.id],
  });
  assert.equal(writes, 1);
});
