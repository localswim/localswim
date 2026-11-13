/// <reference types="node" />
import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import { registerHooks } from "node:module";
import test from "node:test";
import { setImmediate } from "node:timers/promises";

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
const [{ startLive }, { readLiveActor }, { createEntryFixture }] = await Promise.all([
  import("./controller.ts"),
  import("./activity.ts"),
  import("../fixtures.ts"),
]);
hook.deregister();

/** @typedef {import("./controller.ts").LiveRefresh} LiveRefresh */
/** @typedef {import("./controller.ts").LiveConnection} LiveConnection */
/** @typedef {import("./controller.ts").LiveEvent} LiveEvent */
const api = "/api/2026-11-10";
const alpha = "a1a1a1a1-0000-4000-8000-000000000001";
const beta = "b2b2b2b2-0000-4000-8000-000000000002";
const writer = "019a0123-4567-7000-8000-000000000001";
const principal = "80a80a80-0000-4000-8000-000000000001";
const token = Buffer.alloc(32, 1).toString("base64url");
const at = "2026-11-13 14:00:00+00:00";
const encoder = new TextEncoder();

/** @param {AbortSignal} signal */
function stream(signal) {
  /** @type {ReadableStreamDefaultController<Uint8Array> | undefined} */
  let output;
  let closed = false;
  const response = new Response(
    new ReadableStream({
      start(controller) {
        output = controller;
      },
      cancel() {
        closed = true;
      },
    }),
    { headers: { "Content-Type": "text/event-stream; charset=UTF-8" } },
  );
  signal.addEventListener(
    "abort",
    () => {
      if (closed) return;
      closed = true;
      output?.error(signal.reason);
    },
    { once: true },
  );
  return {
    response,
    signal,
    /** @param {string} text */
    send(text) {
      assert.ok(output && !closed);
      output.enqueue(encoder.encode(text));
    },
    finish() {
      assert.ok(output && !closed);
      closed = true;
      output.close();
    },
  };
}

/**
 * @param {import("node:test").TestContext} context
 * @param {(url: URL) => Response} [readActivity]
 * @param {(boardId: string | null) => Response | undefined} [rejectStream]
 */
function mockApi(context, readActivity, rejectStream) {
  /** @type {Map<string|null, ReturnType<typeof stream>[]>} */
  const streams = new Map();
  /** @type {typeof fetch} */
  const fetchMock = (path, options) => {
    assert.ok(typeof path === "string" && options !== undefined);
    const url = new URL(path, "http://127.0.0.1:53146");
    const headers = new Headers(options.headers);
    assert.equal(options.credentials, "omit");
    if (url.pathname === `${api}/browser-session`) {
      assert.equal(options.method, "POST");
      assert.equal(headers.has("Authorization"), false);
      return Promise.resolve(
        Response.json({
          principal_id: principal,
          kind: "human",
          api_version: "2026-11-10",
          workspace_admin: true,
          access_token: token,
          expires_on_service_replacement: true,
        }),
      );
    }
    assert.equal(headers.get("Authorization"), `Bearer ${token}`);
    assert.equal(options.method, "GET");
    if (url.pathname === `${api}/activities`) {
      assert.ok(readActivity);
      return Promise.resolve(readActivity(url));
    }
    assert.equal(url.pathname, `${api}/events`);
    assert.match(headers.get("Accept") ?? "", /text\/event-stream/u);
    assert.ok(options.signal instanceof AbortSignal && !options.signal.aborted);
    const boardId = url.searchParams.get("board_id");
    const rejected = rejectStream?.(boardId);
    if (rejected !== undefined) return Promise.resolve(rejected);
    const opening = stream(options.signal);
    const previous = streams.get(boardId) ?? [];
    previous.push(opening);
    streams.set(boardId, previous);
    return Promise.resolve(opening.response);
  };
  context.mock.method(globalThis, "fetch", fetchMock);

  /** @param {string | null} boardId */
  return function latestStream(boardId) {
    const result = streams.get(boardId)?.at(-1);
    assert.ok(result);
    return result;
  };
}

/** @param {Record<string, unknown>} data */
function frame(data) {
  return `data: ${JSON.stringify(data)}\n\n`;
}

/** @param {string | null} boardId */
function snapshot(boardId) {
  return (
    frame({ type: "snapshot", resource: "workspace", workspace_revision: 1 }) +
    frame({ type: "snapshot", resource: "catalog" }) +
    (boardId === null
      ? ""
      : frame({ type: "snapshot", resource: "board", board_id: boardId, board_revision: 1 }))
  );
}

await test("revalidates snapshots, coalesces invalidations, and cancels obsolete reads", async (context) => {
  const latestStream = mockApi(context);
  /** @type {LiveConnection[]} */
  const connection = [];
  /** @type {LiveEvent[]} */
  const invalidations = [];
  /** @type {{request: LiveRefresh, signal: AbortSignal, finish: (ready: boolean) => void}[]} */
  const reads = [];
  const controller = startLive({
    onConnection: (status) => connection.push(status),
    onInvalidate: (event) => invalidations.push(event),
    refresh: (request, signal) => {
      return new Promise((resolve) => {
        reads.push({ request, signal, finish: resolve });
      });
    },
  });
  context.after(() => controller.dispose());
  controller.update(alpha);
  await setImmediate();
  const first = latestStream(alpha);
  const opening = snapshot(alpha);
  first.send(opening.slice(0, 8));
  await setImmediate();
  assert.equal(reads.length, 0);
  first.send(opening.slice(8));
  await setImmediate();
  const initial = reads[0];
  assert.ok(initial);
  assert.deepEqual(initial.request, {
    reason: "snapshot",
    workspace: true,
    service: true,
    boardId: alpha,
  });
  assert.deepEqual(connection, []);
  assert.deepEqual(invalidations, []);
  initial.finish(true);
  await setImmediate();
  assert.deepEqual(connection, ["connected"]);
  first.send(": heartbeat\n\n");
  await setImmediate();
  assert.equal(reads.length, 1);
  first.send(
    frame({ type: "change", resource: "catalog" }) +
      frame({ type: "change", resource: "board", board_id: alpha, board_revision: 2 }),
  );
  await setImmediate();
  const changes = reads[1];
  assert.ok(changes);
  assert.deepEqual(changes.request, {
    reason: "change",
    workspace: true,
    service: false,
    boardId: alpha,
  });
  assert.deepEqual(invalidations, [
    { type: "change", resource: "board", boardId: alpha, revision: 2 },
  ]);
  first.send(frame({ type: "change", resource: "board", board_id: alpha, board_revision: 3 }));
  await setImmediate();
  assert.equal(reads.length, 2);
  assert.deepEqual(invalidations.at(-1), {
    type: "change",
    resource: "board",
    boardId: alpha,
    revision: 3,
  });
  controller.update(beta);
  assert.ok(changes.signal.aborted);
  assert.ok(first.signal.aborted);
  assert.deepEqual(connection, ["connected", "connecting"]);
  changes.finish(true);
  await setImmediate();
  assert.equal(reads.length, 2);
  assert.deepEqual(connection, ["connected", "connecting"]);
  const second = latestStream(beta);
  second.send(snapshot(beta));
  await setImmediate();
  const next = reads[2];
  assert.ok(next);
  assert.equal(next.request.boardId, beta);
  assert.deepEqual(connection, ["connected", "connecting"]);
  next.finish(true);
  await setImmediate();
  assert.deepEqual(connection, ["connected", "connecting", "connected"]);
  controller.dispose();
  assert.ok(next.signal.aborted);
  assert.ok(second.signal.aborted);
});

await test("rejects invalid frames and retries immediately on explicit recovery", async (context) => {
  const latestStream = mockApi(context);
  /** @type {LiveConnection[]} */
  const connection = [];
  /** @type {LiveRefresh[]} */
  const reads = [];
  const controller = startLive({
    onConnection: (status) => connection.push(status),
    refresh: (request) => {
      reads.push(request);
      return Promise.resolve(true);
    },
  });
  context.after(() => controller.dispose());
  controller.update(null);
  await setImmediate();
  const first = latestStream(null);
  first.send(frame({ type: "snapshot", resource: "workspace", workspace_revision: -1 }));
  await setImmediate();
  assert.deepEqual(connection, ["disconnected"]);
  assert.equal(reads.length, 0);
  controller.retry();
  assert.deepEqual(connection, ["disconnected", "connecting"]);
  await setImmediate();
  const second = latestStream(null);
  assert.notEqual(first, second);
  second.send(snapshot(null));
  await setImmediate();
  assert.deepEqual(connection, ["disconnected", "connecting", "connected"]);
  assert.deepEqual(reads, [{ reason: "snapshot", workspace: true, service: true, boardId: null }]);
  second.finish();
  await setImmediate();
  assert.deepEqual(connection, ["disconnected", "connecting", "connected", "disconnected"]);
});

await test("backs off repeated disconnects and treats missed heartbeats as a disconnect", async (context) => {
  context.mock.timers.enable({ apis: ["setTimeout"] });
  const latestStream = mockApi(context);
  /** @type {LiveConnection[]} */
  const connection = [];
  const controller = startLive({
    onConnection: (status) => connection.push(status),
    refresh: () => Promise.resolve(true),
  });
  context.after(() => controller.dispose());
  controller.update(null);
  await setImmediate();
  const first = latestStream(null);
  first.finish();
  await setImmediate();
  assert.deepEqual(connection, ["disconnected"]);
  context.mock.timers.tick(999);
  await setImmediate();
  assert.equal(latestStream(null), first);
  context.mock.timers.tick(1);
  await setImmediate();
  const second = latestStream(null);
  assert.notEqual(second, first);
  second.finish();
  await setImmediate();
  context.mock.timers.tick(1_999);
  await setImmediate();
  assert.equal(latestStream(null), second);
  context.mock.timers.tick(1);
  await setImmediate();
  const third = latestStream(null);
  assert.notEqual(third, second);
  third.send(snapshot(null));
  await setImmediate();
  assert.deepEqual(connection, ["disconnected", "connected"]);
  context.mock.timers.tick(15_000);
  third.send(": heartbeat\n\n");
  await setImmediate();
  context.mock.timers.tick(34_999);
  await setImmediate();
  assert.equal(third.signal.aborted, false);
  context.mock.timers.tick(1);
  await setImmediate();
  assert.equal(third.signal.aborted, true);
  assert.deepEqual(connection, ["disconnected", "connected", "disconnected"]);
  context.mock.timers.tick(1_000);
  await setImmediate();
  const fourth = latestStream(null);
  assert.notEqual(fourth, third);
  controller.dispose();
  context.mock.timers.tick(50_000);
  await setImmediate();
  assert.equal(latestStream(null), fourth);
  assert.equal(fourth.signal.aborted, true);
});

await test("refreshes the catalog when the watched board becomes inaccessible", async (context) => {
  const latestStream = mockApi(context, undefined, (boardId) => {
    if (boardId !== alpha) return undefined;
    return Response.json(
      {
        type: "about:blank",
        title: "Forbidden",
        status: 403,
        code: "forbidden",
        detail: "The request is not allowed.",
        request_id: principal,
        outcome: "not_applicable",
        errors: [],
      },
      {
        status: 403,
        headers: { "Content-Type": "application/problem+json", "X-Request-ID": principal },
      },
    );
  });
  /** @type {{ request: LiveRefresh, signal: AbortSignal }[]} */
  const reads = [];
  const controller = startLive({
    onConnection: () => {},
    refresh: (request, signal) => {
      reads.push({ request, signal });
      if (request.boardId === alpha) controller.update(null);
      return Promise.resolve(true);
    },
  });
  context.after(() => controller.dispose());
  controller.update(alpha);
  await setImmediate();
  const removed = reads[0];
  assert.ok(removed);
  assert.deepEqual(removed.request, {
    reason: "snapshot",
    workspace: true,
    service: true,
    boardId: alpha,
  });
  assert.equal(removed.signal.aborted, true);
  latestStream(null).send(snapshot(null));
  await setImmediate();
  assert.deepEqual(reads[1]?.request, {
    reason: "snapshot",
    workspace: true,
    service: true,
    boardId: null,
  });
});

await test("attributes only a complete matching audit page sequence at the confirmed revision", async (context) => {
  const fixture = createEntryFixture().board;
  assert.equal(fixture.status, "ready");
  const previous = { ...fixture.current.value.view.board, revision: 2 };
  const current = {
    ...previous,
    revision: 3,
    updated_at: at,
    last_card_edit_at: at,
  };
  let auditRevision = 3;
  let actor = "bot";
  let auditReads = 0;
  mockApi(context, (url) => {
    auditReads += 1;
    assert.equal(url.searchParams.get("board_id"), current.id);
    assert.equal(url.searchParams.get("view"), "monitor");
    assert.equal(url.searchParams.get("from"), at);
    assert.equal(url.searchParams.get("to"), at);
    assert.equal(url.searchParams.get("writer_id"), writer);
    return Response.json({
      writer_id: writer,
      board_revision: auditRevision,
      coverage: { from: at, to: at, complete: false, code: "interval_open" },
      next_cursor: null,
      pagination_complete: true,
      bot_only: false,
      activities: [
        {
          id: writer,
          board_id: current.id,
          revision: 3,
          at,
          actor_id: principal,
          actor_kind: actor,
          operation: "patch",
          operation_group: "Contents modified",
          cards: [{ card_id: alpha, ticket: 1 }],
          tickets: [1],
        },
      ],
    });
  });
  const signal = new AbortController().signal;
  assert.equal(await readLiveActor(previous, current, writer, signal), "bot");
  actor = "human";
  assert.equal(await readLiveActor(previous, current, writer, signal), "human");
  auditRevision = 4;
  assert.equal(await readLiveActor(previous, current, writer, signal), null);
  assert.equal(await readLiveActor(current, current, writer, signal), null);
  assert.equal(auditReads, 3);
});
