/// <reference types="node" />
import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import { registerHooks } from "node:module";
import { setImmediate } from "node:timers/promises";
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
const [{ createEntryFixture }, { createLaneApproval }] = await Promise.all([
  import("../fixtures.ts"),
  import("./approve-lane.ts"),
]);
hook.deregister();

await test("lane approval checks exact active membership and recovers a lost response by receipt", async (t) => {
  const { board, session } = createEntryFixture();
  assert.equal(board.status, "ready");
  const data = board.current.value;
  const review = data.view.lanes.find((lane) => lane.role === "ready_for_review");
  const completed = data.view.lanes.find((lane) => lane.role === "completed");
  assert.ok(review && completed);
  const original = data.view.cards.find((card) => card.lane_id === review.id);
  assert.ok(original);
  const extra = { ...original, id: "24b7f571-e662-4e2b-8357-9fd747bb7416", ticket: 100 };
  const archived = { ...original, id: "7879807a-3704-4834-8901-f84d27df73df", archived: true };
  const active = [original, extra];
  const view = { ...data.view, cards: [...data.view.cards, extra, archived] };
  const changesAt = "2026-11-13 12:00:00+00:00";
  const tags = ['"opaque_card_version_original"', '"opaque_card_version_extra"'];
  let writes = 0;
  let inspections = 0;
  let refreshes = 0;
  /** @type {{ id: string; card_ids: string[] } | undefined} */
  let receipt;
  const status = { dataset: {}, textContent: "", setAttribute() {}, remove() {} };
  Object.defineProperties(globalThis, {
    document: { value: { createElement: () => status }, configurable: true },
    window: { value: { setTimeout }, configurable: true },
  });
  /** @type {typeof fetch} */
  const fetchMock = async (path, options) => {
    assert.ok(typeof path === "string" && options !== undefined);
    const headers = new Headers(options.headers);
    if (path.endsWith("/browser-session")) {
      return Response.json({
        ...session,
        access_token: Buffer.alloc(32, 2).toString("base64url"),
        expires_on_service_replacement: true,
      });
    }
    const index = active.findIndex((card) => path.endsWith(`/cards/${card.id}`));
    const tag = tags[index];
    if (index >= 0 && tag !== undefined)
      return Response.json(active[index], { headers: { ETag: tag } });
    if (options.method === "POST" && path.endsWith("/approvals")) {
      writes += 1;
      assert.ok(typeof options.body === "string");
      /** @type {unknown} */
      const input = JSON.parse(options.body);
      assert.ok(input !== null && typeof input === "object" && "id" in input);
      assert.ok(typeof input.id === "string");
      assert.match(input.id, /^[\da-f]{8}-[\da-f]{4}-7[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/u);
      assert.ok(
        "board_id" in input &&
          "lane_id" in input &&
          "expected_board_etag" in input &&
          "cards" in input,
      );
      assert.equal(input.board_id, view.board.id);
      assert.equal(input.lane_id, review.id);
      assert.equal(input.expected_board_etag, data.boardEtag);
      assert.deepEqual(
        input.cards,
        active.map((card, i) => ({ card_id: card.id, etag: tags[i] })),
      );
      const common = {
        board_id: view.board.id,
        at: changesAt,
        actor_id: session.principal_id,
      };
      const revision = view.board.revision + 1;
      const operationId = input.id;
      const created = {
        ...common,
        id: operationId,
        card_ids: active.map((card) => card.id),
        board_revision: revision,
        changes: active.flatMap((card, i) => [
          {
            ...common,
            id: i === 0 ? operationId : "01940000-0000-7000-8000-000000000002",
            revision,
            operation: "approve",
            resource_type: "card",
            resource_id: card.id,
            field: "lane_id",
            before: review.id,
            after: completed.id,
            ...(i === 0 ? { bulk_approval_comment: review.bulk_approval_comment } : {}),
          },
          {
            ...common,
            id: `01940000-0000-7000-8000-00000000000${i + 3}`,
            revision,
            operation: "comment",
            resource_type: "comment",
            resource_id: `24b7f571-e662-4e2b-8357-9fd747bb741${i + 7}`,
            field: null,
            before: null,
            after: null,
          },
        ]),
      };
      receipt = created;
      throw new TypeError("response lost after commit");
    }
    assert.ok(receipt);
    assert.equal(path, `/api/2026-11-10/approvals/${receipt.id}`);
    inspections += 1;
    if (inspections === 1) {
      const requestId = headers.get("X-Request-ID") ?? "58200000-0000-4000-8000-000000000002";
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
    const result = inspections === 2 ? { ...receipt, card_ids: [original.id] } : receipt;
    return Response.json(result, { headers: { ETag: '"opaque_approval_receipt"' } });
  };
  t.mock.method(globalThis, "fetch", fetchMock);
  const controller = createLaneApproval(
    /** @type {HTMLElement} */ (/** @type {unknown} */ ({ append() {} })),
    {
      current: () => ({ data: { ...data, view }, session }),
      refresh: () => {
        refreshes += 1;
      },
      onStateChange() {},
    },
  );
  t.after(() => {
    controller.dispose();
    Reflect.deleteProperty(globalThis, "document");
    Reflect.deleteProperty(globalThis, "window");
  });
  controller.open(review.id);
  controller.open(review.id);
  assert.equal(controller.busy(review.id), true);
  await setImmediate();
  assert.equal(inspections, 1);
  assert.equal(controller.recoverable(review.id), true);
  controller.open(review.id);
  await setImmediate();
  assert.equal(inspections, 2);
  assert.equal(controller.recoverable(review.id), true);
  assert.equal(refreshes, 0);
  controller.open(review.id);
  await setImmediate();
  assert.equal(inspections, 3);
  assert.equal(writes, 1);
  assert.equal(refreshes, 1);
  assert.equal(status.textContent, "Approved 2 review card(s), moving to Completed.");
});

await test("lane approval skips dispatch when the confirmed board changes during card verification", async (t) => {
  const { board, session } = createEntryFixture();
  assert.equal(board.status, "ready");
  const data = board.current.value;
  const review = data.view.lanes.find((lane) => lane.role === "ready_for_review");
  assert.ok(review);
  const card = data.view.cards.find((candidate) => candidate.lane_id === review.id);
  assert.ok(card);
  const context = { data, session };
  let current = context;
  let cardReads = 0;
  let writes = 0;
  const status = { dataset: {}, textContent: "", setAttribute() {}, remove() {} };
  Object.defineProperties(globalThis, {
    document: { value: { createElement: () => status }, configurable: true },
    window: { value: { setTimeout }, configurable: true },
  });
  /** @type {typeof fetch} */
  const fetchMock = (path) => {
    assert.ok(typeof path === "string");
    if (path.endsWith(`/cards/${card.id}`)) {
      cardReads += 1;
      current = { ...context, data: { ...data, boardEtag: '"new_board_entity_tag"' } };
      return Promise.resolve(
        Response.json(card, { headers: { ETag: '"opaque_card_version_original"' } }),
      );
    }
    writes += 1;
    return Promise.reject(new Error("Unexpected request after the board changed"));
  };
  t.mock.method(globalThis, "fetch", fetchMock);
  const controller = createLaneApproval(
    /** @type {HTMLElement} */ (/** @type {unknown} */ ({ append() {} })),
    { current: () => current, refresh() {}, onStateChange() {} },
  );
  t.after(() => {
    controller.dispose();
    Reflect.deleteProperty(globalThis, "document");
    Reflect.deleteProperty(globalThis, "window");
  });
  controller.open(review.id);
  await setImmediate();
  assert.equal(cardReads, 1);
  assert.equal(writes, 0);
  assert.equal(controller.busy(review.id), false);
  assert.match(status.textContent, /Review the updated lane/u);
});
