import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { createBrowserApi } from "./api.mjs";

const compiled = await build({
  entryPoints: [fileURLToPath(new URL("./src/lane-approval.ts", import.meta.url))],
  bundle: true,
  write: false,
  format: "esm",
  external: ["../api.mjs"],
});
const moduleUrl = new URL("./api.mjs", import.meta.url).href;
const source = compiled.outputFiles[0].text.replaceAll('"../api.mjs"', JSON.stringify(moduleUrl));
const { mountLaneApproval } = await import(
  "data:text/javascript;base64," + Buffer.from(source).toString("base64")
);

const uuid = (suffix) => "87b11b54-e045-4ff0-912e-a429e2ef4a" + suffix;
const [board, lane, completed, actor, first, second, another, otherActor, policy] = Array.from(
  { length: 9 },
  (_, index) => uuid(60 + index),
);
const auditId = (suffix) => "01a1d28b-bb44-7652-9c19-8307cce9b3" + suffix;
const tag = (id, revision) => `"${id}-${revision}"`;
const approvalPath = "/api/2026-11-10/approvals";
const boardPath = "/api/2026-11-10/boards/" + board;
const versions = [
  { id: first, revision: 3 },
  { id: second, revision: 5 },
];
const custom = "Approved — café  ☕\nReady to ship.";

class Element extends EventTarget {
  isConnected = true;
  children = [];
  append(...children) {
    this.children.push(...children);
  }
  setAttribute() {}
  matches() {
    return this.disabled;
  }
  focus() {
    this.focused = true;
  }
  showModal() {
    this.open = true;
  }
  close() {
    this.open = false;
  }
  remove() {
    this.isConnected = false;
  }
  click() {
    if (!this.disabled && !this.hidden) this.dispatchEvent(new Event("click"));
  }
}

function savedReceipt(id, method, receiptActor = actor) {
  const base = {
    board_id: board,
    actor_id: receiptActor,
    at: "2026-11-13 14:20:00+00:00",
  };
  const changes = versions.flatMap(({ id: cardId }, index) => [
    {
      ...base,
      id: index === 0 ? id : auditId(12),
      revision: 9,
      operation: "approve",
      resource_type: "card",
      resource_id: cardId,
      field: "lane_id",
      before: lane,
      after: completed,
      ...(index === 0 ? { bulk_approval_comment: custom } : {}),
    },
    {
      ...base,
      id: auditId(20 + index),
      revision: 9,
      operation: "comment",
      resource_type: "comment",
      resource_id: uuid(71 + index),
      field: null,
      before: null,
      after: null,
    },
  ]);
  return Response.json(
    { ...base, id, board_revision: 9, card_ids: [first, second], changes },
    method === "POST"
      ? {
          status: 201,
          headers: { Location: approvalPath + "/" + id, "X-Board-ETag": tag(board, 9) },
        }
      : { headers: { ETag: tag(id, 1) } },
  );
}

function boardReply(revision, availability = "open") {
  return Response.json(
    { id: board, name: "Board", policy_id: policy, revision, availability },
    { headers: { ETag: tag(board, revision) } },
  );
}

function unavailableRefusal() {
  return Response.json(
    {
      type: "about:blank",
      title: "Service Unavailable",
      status: 503,
      code: "unavailable",
      detail: "This board is closed or draining.",
      request_id: actor,
      outcome: "not_applied",
      errors: [],
    },
    { status: 503, headers: { "Content-Type": "application/problem+json" } },
  );
}

function fixture() {
  const records = new Map();
  const storage = {
    getItem: (key) => records.get(key) ?? null,
    setItem: (key, value) => records.set(key, value),
    removeItem: (key) => records.delete(key),
  };
  const requests = [];
  const confirmations = [];
  let refreshes = 0;
  let response = ({ method, path, body }) =>
    savedReceipt(body?.id ?? path.split("/").at(-1), method);
  let context = {
    laneLabel: "Ready for Review",
    boardEtag: tag(board, 8),
    completedLaneId: completed,
    bulkComment: custom,
    cards: versions,
    complete: true,
    writable: true,
  };
  let principal = actor;
  let leased = false;
  const begin = () => {
    if (leased) return null;
    leased = true;
    return () => (leased = false);
  };
  const api = createBrowserApi({
    getCapability: () => "test-capability",
    fetchImpl(path, request) {
      const call = {
        method: request.method,
        path,
        body: request.body ? JSON.parse(request.body) : undefined,
      };
      requests.push(call);
      return response(call);
    },
  });
  return {
    requests,
    records,
    confirmations,
    storage,
    response: (next) => (response = next),
    context: (next) => (context = next === null ? null : { ...context, ...next }),
    principal: (next) => (principal = next),
    refreshes: () => refreshes,
    mount() {
      const host = new Element();
      host.ownerDocument = { createElement: () => new Element() };
      const controller = mountLaneApproval(host, {
        boardId: board,
        api,
        signal: new AbortController().signal,
        actorId: () => principal,
        isPageActive: () => true,
        getContext: (id) => (id === lane ? context : null),
        readWithSession: (read) => read(),
        beginWrite: begin,
        beginInspection: begin,
        onConfirmed: (ids) => confirmations.push(ids),
        onRefresh: () => refreshes++,
        focusFallback() {},
        storage,
      });
      const [dialog, recovery] = host.children;
      const [heading, status, summary, comment, actions] = dialog.children;
      const [close, approve, inspect] = actions.children;
      const opener = new Element();
      return {
        ...controller,
        open: () => controller.open(lane, opener),
        dialog,
        heading,
        status,
        summary,
        comment,
        close,
        approve,
        inspect,
        recovery,
      };
    },
  };
}

test("approval confirms the rendered count and configured comment, then submits the original guards once", async () => {
  const f = fixture();
  const view = f.mount();
  view.open();
  assert.match(view.heading.textContent, /Ready for Review/);
  assert.match(view.summary.textContent, /2 cards will move to Completed/);
  assert.equal(view.comment.textContent, custom);
  assert.equal(f.requests.length, 0);
  view.approve.click();
  view.approve.click();
  await flush();
  assert.equal(f.requests.length, 1);
  const [request] = f.requests;
  assert.equal(request.path, approvalPath);
  assert.equal(request.method, "POST");
  assert.match(request.body.id, /^[\da-f]{8}-[\da-f]{4}-7[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/);
  assert.deepEqual(request.body, {
    id: request.body.id,
    board_id: board,
    lane_id: lane,
    expected_board_etag: tag(board, 8),
    cards: versions.map(({ id, revision }) => ({ card_id: id, etag: tag(id, revision) })),
  });
  assert.deepEqual(f.confirmations, [[first, second]]);
  assert.match(view.status.textContent, /approved and moved/);
  assert.equal(f.records.size, 0);
  view.close.click();
  view.open();
  assert.equal(view.dialog.open, false);
});

test("a stale rendered scope or changed authority cannot submit", () => {
  for (const change of [
    { complete: false },
    { writable: false },
    { cards: [versions[0], { id: another, revision: 5 }] },
    { cards: [versions[0], { ...versions[1], revision: 6 }] },
    { boardEtag: tag(board, 9) },
    { bulkComment: "Changed" },
    null,
  ]) {
    const f = fixture();
    const view = f.mount();
    view.open();
    f.context(change);
    view.approve.click();
    assert.equal(f.requests.length, 0);
  }
  const f = fixture();
  const view = f.mount();
  f.context({ complete: false });
  view.open();
  assert.equal(view.dialog.open, undefined);
  f.context({ complete: true });
  view.open();
  f.principal(otherActor);
  view.approve.click();
  assert.equal(f.requests.length, 0);
});

test("an uncertain response recovers the original receipt after remount even without the lane", async () => {
  const f = fixture();
  f.response(({ method }) => {
    assert.equal(method, "POST");
    throw new TypeError("Connection lost");
  });
  const original = f.mount();
  original.open();
  original.approve.click();
  await flush();
  assert.match(original.status.textContent, /outcome is unknown/);
  const [saved] = [...f.records.values()].map((value) => JSON.parse(value));
  assert.equal(saved.id, f.requests[0].body.id);
  assert.deepEqual(saved.cards, versions);
  assert.equal(saved.bulkComment, custom);
  original.dispose();
  f.context(null);
  const view = f.mount();
  assert.equal(view.blocksCard(first), true);
  assert.equal(view.blocksCard(another), false);
  assert.equal(view.recovery.hidden, false);
  view.recovery.click();
  assert.equal(view.approve.hidden, true);
  f.response(({ method, path }) => {
    assert.equal(method, "GET");
    assert.equal(path, approvalPath + "/" + saved.id);
    return savedReceipt(saved.id, method);
  });
  view.inspect.click();
  await flush();
  assert.deepEqual(f.confirmations, [[first, second]]);
  assert.equal(f.records.size, 0);
  assert.equal(view.blocksCard(first), false);
  assert.deepEqual(
    f.requests.map(({ method }) => method),
    ["POST", "GET"],
  );
});

test("a stale POST clears only after the original receipt is absent across a stable advanced board revision", async () => {
  const f = fixture();
  let revision = 8;
  f.response(({ method, path }) => {
    if (method === "POST") return new Response(null, { status: 412 });
    return path === boardPath ? boardReply(revision) : new Response(null, { status: 404 });
  });
  const view = f.mount();
  view.open();
  view.approve.click();
  await flush();
  assert.match(view.status.textContent, /board or lane changed/);
  const receiptPath = approvalPath + "/" + f.requests[0].body.id;
  view.inspect.click();
  await flush();
  assert.equal(f.records.size, 1);
  assert.match(view.status.textContent, /has not been found/);
  revision = 9;
  view.inspect.click();
  await flush();
  assert.deepEqual(
    f.requests.map(({ path }) => path),
    [approvalPath, receiptPath, boardPath, receiptPath, boardPath, receiptPath, boardPath],
  );
  assert.equal(f.records.size, 0);
  assert.equal(f.refreshes(), 2);
  assert.deepEqual(f.confirmations, []);
});

test("an otherwise valid receipt for another actor cannot confirm the operation", async () => {
  const f = fixture();
  f.response(({ method, path, body }) =>
    savedReceipt(body?.id ?? path.split("/").at(-1), method, otherActor),
  );
  const view = f.mount();
  view.open();
  view.approve.click();
  await flush();
  assert.match(view.status.textContent, /outcome is unknown/);
  view.inspect.click();
  await flush();
  assert.match(view.status.textContent, /could not be verified/);
  assert.equal(f.records.size, 1);
  assert.deepEqual(f.confirmations, []);
});

test("the service's canonical unavailable refusal leaves no uncertain approval", async () => {
  const f = fixture();
  f.response(unavailableRefusal);
  const view = f.mount();
  view.open();
  view.approve.click();
  await flush();
  assert.match(view.status.textContent, /approval was refused/);
  assert.equal(f.records.size, 0);
  assert.equal(view.inspect.hidden, true);
  assert.deepEqual(f.confirmations, []);
});

test("definitive refusal clears its record after route detach or a principal change", async () => {
  for (const [detach, refusal] of [
    [(view) => view.dispose(), () => new Response(null, { status: 401 })],
    [(_view, f) => f.principal(otherActor), unavailableRefusal],
  ]) {
    const f = fixture();
    const deferred = Promise.withResolvers();
    f.response(() => deferred.promise);
    const original = f.mount();
    original.open();
    original.approve.click();
    await flush();
    assert.equal(f.records.size, 1);
    detach(original, f);
    deferred.resolve(refusal());
    await flush();
    assert.equal(f.records.size, 0);
    assert.equal(f.refreshes(), 0);
    assert.deepEqual(f.confirmations, []);
    assert.deepEqual(
      f.requests.map(({ method }) => method),
      ["POST"],
    );

    // The original validator is still current because the request was refused.
    f.principal(actor);
    const view = f.mount();
    assert.equal(view.recovery.hidden, true);
    view.open();
    assert.equal(view.approve.disabled, false);
  }
});

test("an older refusal cannot clear or report over a replacement attempt", async () => {
  const f = fixture();
  const deferred = Promise.withResolvers();
  f.response(() => deferred.promise);
  const view = f.mount();
  view.open();
  view.approve.click();
  await flush();
  const [[key, raw]] = f.records;
  const next = JSON.stringify({ ...JSON.parse(raw), id: auditId(99) });
  f.records.set(key, next);
  deferred.resolve(unavailableRefusal());
  await flush();
  assert.equal(f.records.get(key), next);
  assert.equal(f.refreshes(), 0);
  assert.doesNotMatch(view.status.textContent, /was refused/);
  assert.deepEqual(f.confirmations, []);
});

test("stale and uncertain outcomes retain the original record after route detach", async () => {
  for (const settle of [
    ({ resolve }) => resolve(new Response(null, { status: 412 })),
    ({ reject }) => reject(new TypeError("Connection lost")),
  ]) {
    const f = fixture();
    const deferred = Promise.withResolvers();
    f.response(() => deferred.promise);
    const original = f.mount();
    original.open();
    original.approve.click();
    await flush();
    const [[key, raw]] = f.records;
    original.dispose();
    settle(deferred);
    await flush();
    assert.equal(f.records.get(key), raw);
    assert.equal(f.refreshes(), 0);
    const view = f.mount();
    assert.equal(view.recovery.hidden, false);
    assert.deepEqual(
      f.requests.map(({ method }) => method),
      ["POST"],
    );
  }
});
