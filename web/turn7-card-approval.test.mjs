import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents:
      'export { mountCardApproval } from "./src/card-approval.ts"; export { createBrowserApi } from "./api.mjs";',
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  write: false,
  format: "esm",
});
const { createBrowserApi, mountCardApproval } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const cardId = "d2ef6295-ace1-4e6e-bbf7-b1c1823f56fc";
const otherCardId = "31cde98d-064a-4774-9136-ec50ff5be1bc";
const actorId = "84b4ae21-e224-4f5a-9fca-9abf8e0c86bc";
const otherActorId = "45506874-50b3-4005-951e-dc8efb30a8ed";
const review = "ceab10a8-1492-4c90-aa07-13b83138919a";
const completed = "c7c715f0-4bd5-49ea-b879-5bf04dac302b";
const commentId = "d506a393-8362-4093-8d07-0f601a30d365";
const policyId = "67a89dc6-6441-4280-8d49-2cb49937bc37";
const tag = (id, revision) => JSON.stringify(id + "-" + revision);
const context = (extra = {}) => ({
  actorId,
  cardId,
  cardEtag: tag(cardId, 3),
  boardEtag: tag(boardId, 5),
  eligible: true,
  writable: true,
  ...extra,
});

class Element extends EventTarget {
  replaceChildren(...children) {
    this.children = children;
  }
  setAttribute() {}
  click() {
    if (!this.disabled && !this.hidden) this.dispatchEvent(new Event("click"));
  }
}

function approvalReply(id, approvedCardId, status = 201) {
  const at = "2026-11-13 12:34:56+00:00";
  const base = { board_id: boardId, revision: 6, actor_id: actorId, at };
  return Response.json(
    {
      id,
      board_id: boardId,
      actor_id: actorId,
      at,
      board_revision: 6,
      card_ids: [approvedCardId],
      changes: [
        {
          ...base,
          id,
          resource_type: "card",
          resource_id: approvedCardId,
          operation: "approve",
          field: "lane_id",
          before: review,
          after: completed,
        },
        {
          ...base,
          id: "019adba4-4fc9-7354-870c-54c82ce41a68",
          resource_type: "comment",
          resource_id: commentId,
          operation: "comment",
          field: null,
          before: null,
          after: null,
        },
      ],
    },
    {
      status,
      headers: {
        ETag: tag(id, 1),
        Location: "/api/2026-11-10/approvals/" + id,
        "X-Board-ETag": tag(boardId, 6),
      },
    },
  );
}

function boardReply(revision) {
  return Response.json(
    { id: boardId, revision, name: "Synthetic board", policy_id: policyId, availability: "open" },
    { headers: { ETag: tag(boardId, revision) } },
  );
}

function fixture(fetch, initial = context()) {
  let current = initial;
  let leased = false;
  let refreshes = 0;
  const requests = [];
  const confirmations = [];
  const saved = new Map();
  const storage = {
    getItem: (key) => saved.get(key) ?? null,
    setItem: (key, value) => saved.set(key, value),
    removeItem: (key) => saved.delete(key),
  };
  const api = createBrowserApi({
    getCapability: async () => "synthetic-capability",
    async fetchImpl(path, init) {
      const body = init.body ? JSON.parse(init.body) : undefined;
      requests.push({ method: init.method, path, body });
      return fetch(init.method, path, body);
    },
  });
  function begin() {
    if (leased) return null;
    leased = true;
    return () => {
      leased = false;
    };
  }
  function mount() {
    const lifetime = new AbortController();
    const host = new Element();
    host.ownerDocument = { createElement: () => new Element() };
    const approval = mountCardApproval(host, {
      api,
      boardId,
      signal: lifetime.signal,
      storage,
      context: () => current,
      actorId: () => current?.actorId,
      readWithSession: (read) => read(),
      beginWrite: begin,
      beginInspection: begin,
      onConfirmed: (id) => confirmations.push(id),
      onRefresh: () => refreshes++,
    });
    const [approve, inspect, status] = host.children;
    return {
      ...approval,
      host,
      approve,
      inspect,
      status,
      unmount: () => lifetime.abort(),
      focus(next) {
        current = next;
        approval.update();
      },
    };
  }
  return { mount, requests, confirmations, saved, refreshes: () => refreshes };
}

test("approval sends only the original board and card validators and confirms the addressed card", async () => {
  const result = Promise.withResolvers();
  const f = fixture(async (_method, _path, body) => {
    assert.equal(f.saved.size, 1);
    return result.promise.then(() => approvalReply(body.id, cardId));
  });
  const view = f.mount();
  view.focus(context({ eligible: false }));
  assert.equal(view.host.hidden, true);
  view.focus(context({ writable: false }));
  assert.equal(view.approve.disabled, true);
  view.focus(context());
  view.approve.click();
  view.approve.click();
  await flush();
  assert.equal(view.blocksCard(cardId), true);
  assert.equal(view.blocksCard(otherCardId), false);
  assert.equal(f.requests.length, 1);
  const [request] = f.requests;
  const { id } = request.body;
  assert.match(id, /^[0-9a-f-]+-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.deepEqual(request, {
    method: "POST",
    path: "/api/2026-11-10/approvals",
    body: {
      id,
      board_id: boardId,
      expected_board_etag: tag(boardId, 5),
      cards: [{ card_id: cardId, etag: tag(cardId, 3) }],
    },
  });
  view.focus(context({ cardId: otherCardId, cardEtag: tag(otherCardId, 4) }));
  result.resolve();
  await flush();
  assert.deepEqual(f.confirmations, [cardId]);
  assert.equal(f.saved.size, 0);
});

test("after a lost response and reload, approval inspects the same receipt without a new POST", async () => {
  let receiptId;
  const f = fixture((method, path, body) => {
    if (method === "POST") {
      receiptId = body.id;
      throw new TypeError("Network failure");
    }
    assert.equal(path, "/api/2026-11-10/approvals/" + receiptId);
    return approvalReply(receiptId, cardId, 200);
  });
  const original = f.mount();
  original.approve.click();
  await flush();
  assert.match(original.status.textContent, /outcome is unknown/);
  original.unmount();
  const restored = f.mount();
  restored.focus(context({ actorId: otherActorId, eligible: false }));
  assert.equal(restored.inspect.hidden, true);
  restored.focus(context({ eligible: false, writable: false }));
  assert.equal(restored.inspect.hidden, false);
  assert.equal(restored.approve.hidden, true);
  restored.inspect.click();
  restored.inspect.click();
  await flush();
  assert.deepEqual(
    f.requests.map(({ method }) => method),
    ["POST", "GET"],
  );
  assert.deepEqual(f.confirmations, [cardId]);
  assert.equal(f.saved.size, 0);
});

test("stale approval inspects its receipt and clears only after its validator is fenced", async () => {
  const notFound = () => Response.json({}, { status: 404 });
  let original;
  const f = fixture((method, path, body) => {
    if (method === "POST") {
      original = body.id;
      return Response.json({}, { status: 412 });
    }
    if (path === "/api/2026-11-10/boards/" + boardId) return boardReply(6);
    assert.equal(path, "/api/2026-11-10/approvals/" + original);
    return notFound();
  });
  const view = f.mount();
  view.approve.click();
  await flush();
  assert.equal(view.blocksCard(cardId), true);
  assert.equal(view.inspect.hidden, false);
  view.inspect.click();
  await flush();
  assert.deepEqual(
    f.requests.map(({ path }) => path),
    [
      "/api/2026-11-10/approvals",
      "/api/2026-11-10/approvals/" + original,
      "/api/2026-11-10/boards/" + boardId,
      "/api/2026-11-10/approvals/" + original,
      "/api/2026-11-10/boards/" + boardId,
    ],
  );
  assert.deepEqual(f.confirmations, []);
  assert.equal(f.refreshes(), 2);
  assert.equal(f.saved.size, 0);
  assert.equal(view.approve.disabled, true);
  view.focus(context({ boardEtag: tag(boardId, 6) }));
  assert.equal(view.approve.disabled, false);
});

test("a receipt miss leaves the approval unresolved while original validators may still succeed", async () => {
  let receiptId;
  const f = fixture((method, path, body) => {
    if (method === "POST") {
      receiptId = body.id;
      throw new TypeError("Network failure");
    }
    if (path === "/api/2026-11-10/boards/" + boardId) return boardReply(5);
    if (path === "/api/2026-11-10/cards/" + cardId) {
      return Response.json(
        {
          id: cardId,
          board_id: boardId,
          revision: 3,
          ticket: 1,
          created_at: "2026-11-13 12:00:00+00:00",
          updated_at: "2026-11-13 12:00:00+00:00",
          title: "Synthetic card",
          description: "",
          lane_id: review,
          priority_id: policyId,
          owner_id: actorId,
          parent_id: null,
          child_ids: [],
          archived: false,
          cover_attachment_id: null,
          comment_count: 0,
          attachment_count: 0,
          blocking_depth: 0,
          blocked_card_count: 0,
          triage_score: 0,
          completed_at: null,
        },
        { headers: { ETag: tag(cardId, 3) } },
      );
    }
    assert.equal(path, "/api/2026-11-10/approvals/" + receiptId);
    return Response.json({}, { status: 404 });
  });
  const view = f.mount();
  view.approve.click();
  await flush();
  view.inspect.click();
  await flush();
  view.approve.click();
  assert.equal(f.requests.filter(({ method }) => method === "POST").length, 1);
  assert.equal(view.blocksCard(cardId), true);
  assert.match(view.status.textContent, /has not been found/);
  assert.deepEqual(f.confirmations, []);
});

test("a definitive refusal leaves the drawer open and preserves a separate comment draft", async () => {
  const f = fixture(() => Response.json({}, { status: 403 }));
  const draftKey = "comment-draft";
  f.saved.set(draftKey, "An unsent comment.");
  const view = f.mount();
  view.approve.click();
  await flush();
  assert.match(view.status.textContent, /Approval was refused/);
  assert.deepEqual(f.confirmations, []);
  assert.equal(f.refreshes(), 1);
  assert.deepEqual([...f.saved], [[draftKey, "An unsent comment."]]);
});

test("settlement of an older approval attempt cannot clear a replacement journal", async () => {
  const result = Promise.withResolvers();
  const f = fixture((_method, _path, body) =>
    result.promise.then(() => approvalReply(body.id, cardId)),
  );
  const view = f.mount();
  view.approve.click();
  await flush();
  const [[key, original]] = f.saved;
  const replacement = JSON.stringify({
    ...JSON.parse(original),
    id: "019adba4-4fc9-7354-870c-54c82ce41a68",
  });
  f.saved.set(key, replacement);
  result.resolve();
  await flush();
  assert.equal(f.saved.get(key), replacement);
  assert.equal(view.blocksCard(cardId), true);
  assert.deepEqual(f.confirmations, []);
});
