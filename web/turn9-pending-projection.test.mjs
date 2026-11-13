import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents: `
      export { mountBoardView } from "./src/board-view.ts";
      export { mountCardAssignment } from "./src/card-assignment.ts";
      export { createCardMovement } from "./src/card-movement.ts";
      export { createCardTextDrafts } from "./src/drafts.ts";
      export { ApiError } from "./api.mjs";
    `,
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  write: false,
  format: "esm",
  footer: { js: "//# sourceURL=pending-projection-tests.mjs" },
});
const { ApiError, mountBoardView, mountCardAssignment, createCardMovement, createCardTextDrafts } =
  await import(
    "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
  );

class Element extends EventTarget {
  children = [];
  dataset = {};
  attributes = new Map();
  classList = { contains: () => false, toggle() {} };
  value = "";
  constructor(doc) {
    super();
    this.ownerDocument = doc;
    this.children.item = (index) => this.children[index] ?? null;
  }
  append(...nodes) {
    for (const node of nodes) if (node instanceof Element) this.insertBefore(node, null);
  }
  after(node) {
    const parent = this.parentElement;
    parent.insertBefore(node, parent.children[parent.children.indexOf(this) + 1] ?? null);
  }
  insertBefore(node, before) {
    node.remove();
    this.children.splice(before ? this.children.indexOf(before) : this.children.length, 0, node);
    node.parentElement = this;
  }
  replaceChildren(...nodes) {
    for (const node of [...this.children]) node.remove();
    this.append(...nodes);
  }
  remove() {
    const parent = this.parentElement;
    if (parent) parent.children.splice(parent.children.indexOf(this), 1);
    this.parentElement = null;
  }
  attachShadow() {
    this.shadowRoot = new Element(this.ownerDocument);
    return this.shadowRoot;
  }
  setAttribute(name, value) {
    this.attributes.set(name, value);
  }
  removeAttribute(name) {
    this.attributes.delete(name);
  }
  checkVisibility() {
    return !this.hidden;
  }
  descendants() {
    return this.children.flatMap((child) => [child, ...child.descendants()]);
  }
}

const uuid = (value) => `ac2e106d-7e81-477a-a5ad-${String(value).padStart(12, "0")}`;
const [boardId, otherBoard, sourceLane, targetLane, ownerId, p1, p3, first, second, created] =
  Array.from({ length: 10 }, (_, index) => uuid(index + 1));
const tag = (id, revision) => `"${id}-${revision}"`;
const data = {
  board: { id: boardId, revision: 7, user_css: "" },
  etag: tag(boardId, 7),
  lanes: [
    { id: sourceLane, label: "Ready" },
    { id: targetLane, label: "Working" },
  ],
  cards: [
    { id: first, ticket: 1, title: "Original", revision: 6, lane_id: sourceLane, priority_id: p1 },
    { id: second, ticket: 2, title: "Other", revision: 7, lane_id: sourceLane, priority_id: p3 },
  ],
  users: [{ id: ownerId, label: "Human", kind: "human" }],
  priorities: [
    { id: p1, label: "Urgent", rank: 1 },
    { id: p3, label: "Normal", rank: 3 },
  ],
};

function boardFixture() {
  const doc = new EventTarget();
  doc.createElement = (tag) => Object.assign(new Element(doc), { tag });
  doc.defaultView = {
    Node: Element,
    HTMLElement: Element,
    cancelAnimationFrame() {},
    CSSStyleSheet: class {
      replaceSync() {}
    },
  };
  const host = doc.createElement("div");
  const control = doc.createElement("button");
  const opened = [];
  let changes = [];
  const board = mountBoardView(host, data, {
    pendingChanges: () => changes,
    onOpenCard: (card) => opened.push(card),
    renderLaneActions(lane, container) {
      if (lane.id === sourceLane) container.append(control);
    },
  });
  const shadow = host.descendants().find((node) => node.shadowRoot).shadowRoot;
  const lane = (id) => shadow.descendants().find((node) => node.dataset.laneId === id);
  const card = (id) => shadow.descendants().find((node) => node.dataset.cardId === id);
  const order = (id) =>
    lane(id)
      .descendants()
      .filter((node) => node.dataset.cardId)
      .map((node) => node.dataset.cardId);
  return {
    board,
    card,
    control,
    doc,
    lane,
    opened,
    order,
    project(...pending) {
      changes = pending.map((change) => ({ boardId, ...change }));
      board.refreshPending();
    },
  };
}

test("pending title and priority preserve authoritative card identities and lane controls", () => {
  const { board, card, control, lane, opened, order, project } = boardFixture();
  const originalFace = card(first);
  const originalLane = lane(sourceLane);
  project(
    { kind: "title", cardId: first, revision: 6, title: "  Submitted ☕" },
    { kind: "priority", cardId: second, revision: 7, priorityId: p1 },
  );
  assert.equal(card(first), originalFace);
  assert.equal(card(first).dataset.pending, "title");
  assert.equal(card(first).attributes.get("aria-label"), "#1   Submitted ☕, Pending");
  assert.match(card(second).attributes.get("aria-description"), /Priority Urgent/);
  card(first).dispatchEvent(new Event("click"));
  assert.equal(opened[0], data.cards[0]);
  assert.deepEqual(board.laneMembership(sourceLane), {
    cards: data.cards,
    revision: 7,
    complete: true,
  });
  const promoted = { kind: "priority", cardId: second, revision: 7, priorityId: p1 };
  project(promoted, { kind: "priority", cardId: first, revision: 6, priorityId: p3 });
  assert.deepEqual(order(sourceLane), [second, first]);
  project({ kind: "priority", cardId: first, revision: 6, priorityId: p3, boardId: otherBoard });
  assert.equal(card(first).dataset.pending, undefined);
  assert.equal(card(first).attributes.get("aria-label"), "#1 Original");
  assert.equal(lane(sourceLane), originalLane);
  assert.ok(originalLane.descendants().includes(control));
});

test("pending move and unnumbered creation cannot enter a confirmed lane scope", () => {
  const { board, card, order, project } = boardFixture();
  const move = {
    kind: "move",
    cardId: first,
    revision: 6,
    sourceLaneId: sourceLane,
    laneId: targetLane,
  };
  const create = {
    kind: "create",
    cardId: created,
    boardEtag: data.etag,
    laneId: targetLane,
    ownerId,
    priorityId: p1,
    title: "New card",
  };
  project(move, create);
  assert.deepEqual(order(sourceLane), [second]);
  assert.deepEqual(order(targetLane), [first, created]);
  assert.equal(card(first).dataset.pending, "move");
  assert.equal(card(created).dataset.pending, "create");
  assert.equal(card(created).disabled, true);
  assert.equal(card(created).attributes.get("aria-label"), "New card, Pending creation");
  assert.equal(
    card(created)
      .descendants()
      .some((node) => node.className === "board-view-card-ticket"),
    false,
  );
  assert.equal(board.laneMembership(sourceLane).complete, false);
  assert.deepEqual(board.laneMembership(targetLane), { cards: [], revision: 7, complete: false });

  const advanced = { ...data, board: { ...data.board, revision: 9 }, etag: tag(boardId, 9) };
  const savedMove = { ...data.cards[0], lane_id: targetLane, revision: 8 };
  const savedCreate = { ...savedMove, id: created, ticket: 3, title: create.title, revision: 9 };
  board.update({ ...advanced, cards: [data.cards[1], savedMove, savedCreate] });
  assert.deepEqual(order(targetLane), [first, created]);
  assert.equal(card(first).dataset.pending, undefined);
  assert.equal(card(created).dataset.pending, undefined);
  assert.equal(card(created).attributes.get("aria-label"), "#3 New card");
  assert.deepEqual(board.laneMembership(targetLane), {
    cards: [savedMove, savedCreate],
    revision: 9,
    complete: true,
  });
});

function tabStorage() {
  const values = new Map();
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  };
}

test("an unresolved title keeps the original draft after route change without replaying", async () => {
  const storage = tabStorage();
  const signal = new AbortController();
  const request = Promise.withResolvers();
  const confirmed = { title: "Original", description: "", revision: 6, etag: tag(first, 6) };
  const key = `localswim:v2:card-text:${boardId}:${first}`;
  const scope = { boardId, cardId: first };
  let projected;
  let writes = 0;
  const drafts = createCardTextDrafts({
    storage,
    projectPending(change) {
      assert.equal(JSON.parse(storage.getItem(key)).phase, "submitted");
      projected = change;
      return () => {
        projected = undefined;
      };
    },
    api: {
      getCard: () => assert.fail("No inspection was requested"),
      patchCard(id, etag, text) {
        writes++;
        assert.equal(id, first);
        assert.equal(etag, confirmed.etag);
        assert.deepEqual(text, { title: "  Submit é" });
        return request.promise;
      },
    },
  });
  const bindings = {
    context: () => ({ confirmed, writable: true }),
    onChange() {},
    onConfirmed: () => assert.fail("No write was confirmed"),
  };
  const controller = drafts.bind(scope, { ...bindings, signal: signal.signal });
  controller.edit({ ...confirmed, title: "  Submit é" }, "title");
  assert.equal(projected, undefined);
  const submitted = controller.submit();
  assert.deepEqual(projected, { ...scope, kind: "title", revision: 6, title: "  Submit é" });
  signal.abort();
  const reopened = drafts.bind(scope, { ...bindings, signal: new AbortController().signal });
  request.reject(new TypeError("Response lost"));
  await submitted;
  assert.equal(projected, undefined);
  assert.equal(reopened.snapshot().phase, "unknown");
  assert.deepEqual(drafts.retainedText(scope), { title: "  Submit é" });
  await reopened.submit();
  assert.equal(writes, 1);
});

test("a refused move clears its projection before readback and retains a failed inspection journal", async () => {
  const storage = tabStorage();
  const request = Promise.withResolvers();
  const signal = new AbortController().signal;
  const key = `localswim:v2:card-move:${boardId}`;
  const original = { cardId: first, laneId: sourceLane, revision: 6, etag: tag(first, 6) };
  let projected;
  let writes = 0;
  let inspections = 0;
  const movement = createCardMovement({
    boardId,
    signal,
    storage,
    getBoardData: () => data,
    getPolicy: () => ({ moveLaneIds: new Map([[sourceLane, new Set([targetLane])]]) }),
    readWithSession: (read) => read(),
    canWrite: () => true,
    beginWrite: () => () => {},
    onChange() {},
    onRefresh() {},
    projectPending(change) {
      assert.deepEqual(JSON.parse(storage.getItem(key)), { ...original, destination: targetLane });
      projected = change;
      return () => {
        projected = undefined;
      };
    },
    api: {
      patchCard(id, etag, body) {
        writes++;
        assert.equal(id, first);
        assert.equal(etag, original.etag);
        assert.deepEqual(body, { lane_id: targetLane });
        return request.promise;
      },
      getCard() {
        inspections++;
        assert.equal(projected, undefined);
        return Promise.reject(new TypeError("Readback unavailable"));
      },
    },
  });
  const submitted = movement.move({ ...original, archived: false }, targetLane);
  assert.deepEqual(projected, {
    kind: "move",
    boardId,
    cardId: first,
    revision: 6,
    sourceLaneId: sourceLane,
    laneId: targetLane,
  });
  request.reject(new ApiError("http_error", { status: 403 }));
  await submitted;
  assert.equal(projected, undefined);
  assert.equal(inspections, 1);
  assert.equal(movement.status().canInspect, true);
  assert.deepEqual(JSON.parse(storage.getItem(key)), { ...original, destination: targetLane });
  await movement.move({ ...original, archived: false }, targetLane);
  assert.equal(writes, 1);
});

test("individual priority projects the requested rank and settles to the confirmed floor", async () => {
  const { doc } = boardFixture();
  const storage = tabStorage();
  const root = doc.createElement("div");
  const response = Promise.withResolvers();
  const current = { cardId: first, revision: 6, etag: tag(first, 6), ownerId, priorityId: p1 };
  const confirmed = [];
  let projected;
  mountCardAssignment(root, {
    boardId,
    signal: new AbortController().signal,
    storage,
    getBoardData: () => data,
    getContext: () => ({ ...current, writable: true, canSetPriority: true }),
    beginWrite: () => () => {},
    beginInspection: () => assert.fail("No inspection was requested"),
    onConfirmed: (saved) => confirmed.push(saved),
    projectPending(change) {
      projected = change;
      return () => {
        projected = undefined;
      };
    },
    api: {
      patchCard(id, etag, change) {
        assert.equal(id, first);
        assert.equal(etag, current.etag);
        assert.deepEqual(change, { priority_id: p3 });
        return response.promise;
      },
    },
  });
  const select = root.descendants().filter(({ tag }) => tag === "select")[1];
  const save = root.descendants().find(({ textContent }) => textContent === "Set priority");
  select.value = p3;
  select.dispatchEvent(new Event("change"));
  assert.equal(projected, undefined);
  save.dispatchEvent(new Event("click"));
  assert.deepEqual(projected, {
    kind: "priority",
    boardId,
    cardId: first,
    revision: 6,
    priorityId: p3,
  });
  response.resolve({
    data: { id: first, board_id: boardId, revision: 6, owner_id: ownerId, priority_id: p1 },
    etag: current.etag,
  });
  await flush();
  assert.equal(projected, undefined);
  assert.deepEqual(confirmed, [current]);
  assert.equal(select.value, p1);
});
