import assert from "node:assert/strict";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents: `
      export { mountCreateCard } from "./src/create-card.ts";
      export { ApiError } from "./api.mjs";
    `,
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  write: false,
  format: "esm",
});
const { ApiError, mountCreateCard } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);
globalThis.InputEvent = class InputEvent extends Event {};

const board = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const lane = "ceab10a8-1492-4c90-aa07-13b83138919a";
const otherLane = "7e90e6f9-e7eb-45c8-808e-e5f49b23c259";
const user = "84b4ae21-e224-4f5a-9fca-9abf8e0c86bc";
const otherUser = "889906a2-1870-47b8-8317-b5043a2d2556";
const priority = "163b1fa0-2a32-424b-8489-6bbd7761201c";
const otherPriority = "88aa7d55-c894-4833-b6e2-484f34721b0a";
const etag = '"abcdefghijklmnop"';

class Element extends EventTarget {
  constructor(tag, ownerDocument) {
    super();
    this.tag = tag;
    this.ownerDocument = ownerDocument;
    this.children = [];
    this.isConnected = true;
  }
  get options() {
    return this.children;
  }
  append(...items) {
    this.children.push(...items.filter((item) => item instanceof Element));
  }
  replaceChildren(...items) {
    this.children = items;
  }
  setAttribute() {}
  showModal() {
    this.open = true;
  }
  close() {
    this.open = false;
  }
  focus() {
    this.ownerDocument.activeElement = this;
  }
  remove() {
    this.isConnected = false;
  }
  click() {
    if (!this.hidden && !this.disabled) this.dispatchEvent(new Event("click"));
  }
  descendants() {
    return this.children.flatMap((child) => [child, ...child.descendants()]);
  }
}

const idle = () =>
  new Promise((resolve) => {
    setImmediate(resolve);
  });

function fixture() {
  const journals = new Map();
  const posts = [];
  const reads = [];
  const confirmed = [];
  const state = { responseLost: true, readCard: (card) => card };
  let saved;
  const doc = { createElement: (tag) => new Element(tag, doc) };
  const storage = {
    getItem: (key) => journals.get(key) ?? null,
    setItem: (key, value) => journals.set(key, value),
    removeItem: (key) => journals.delete(key),
  };
  const api = {
    async createCard(payload) {
      posts.push(payload);
      saved = { owner_id: user, priority_id: priority, ...payload, ticket: 42 };
      if (state.responseLost) throw new TypeError("Response was lost");
      return { data: state.readCard(saved), etag };
    },
    async getCard(id) {
      reads.push(id);
      assert.equal(id, saved.id);
      return { data: state.readCard(saved), etag };
    },
  };

  function mount() {
    const host = doc.createElement("div");
    const ui = mountCreateCard(host, {
      boardId: board,
      api,
      storage,
      signal: new AbortController().signal,
      readWithSession: (read) => read(),
      onConfirmed: (...args) => confirmed.push(args),
      onStale() {},
      focusFallback() {},
      context: () => ({
        etag,
        writable: true,
        lanes: [{ id: lane, label: "Ready" }],
        users: [
          { id: user, label: "Human" },
          { id: otherUser, label: "Bot" },
        ],
        priorities: [
          { id: priority, label: "Normal" },
          { id: otherPriority, label: "High" },
        ],
        defaultOwnerId: user,
        defaultPriorityId: priority,
      }),
    });
    const nodes = host.descendants();
    const button = (label) =>
      nodes.find((node) => node.tag === "button" && node.textContent === label);
    return { host, ui, button };
  }

  async function submit() {
    const view = mount();
    view.ui.open(lane, doc.createElement("button"));
    const controls = view.host.descendants();
    const [title, description] = controls.filter((node) => node.tag === "textarea");
    const [, owner, priorityChoice] = controls.filter((node) => node.tag === "select");
    for (const [control, value, event] of [
      [title, "  Original title  ", "input"],
      [description, "Original description", "input"],
      [owner, otherUser, "change"],
      [priorityChoice, otherPriority, "change"],
    ]) {
      control.value = value;
      control.dispatchEvent(new Event(event));
    }
    view.button("Add card").click();
    await idle();
    assert.equal(posts.length, 1);
    assert.equal(posts[0].owner_id, otherUser);
    assert.equal(posts[0].priority_id, otherPriority);
    return view;
  }

  async function inspectAfterReload(view) {
    view.ui.dispose();
    const reloaded = mount();
    reloaded.button("Inspect pending creation").click();
    reloaded.button("Inspect creation").click();
    await idle();
    return reloaded;
  }

  return { journals, posts, reads, confirmed, state, submit, inspectAfterReload };
}

test("recovers the original card after a lost create response and later field changes", async () => {
  const f = fixture();
  const first = await f.submit();
  const [original] = f.posts;
  f.state.readCard = (card) => ({
    ...card,
    lane_id: otherLane,
    title: "Edited title",
    description: "Edited description",
    owner_id: user,
    priority_id: priority,
  });
  const reloaded = await f.inspectAfterReload(first);
  assert.deepEqual(f.reads, [original.id]);
  assert.equal(reloaded.button("Dismiss unresolved creation").hidden, true);
  reloaded.button("Dismiss unresolved creation").click();
  reloaded.button("Add card").click();
  await idle();
  assert.equal(f.posts.length, 1);
  assert.equal(f.journals.size, 0);
  assert.equal(reloaded.ui.hasUncertainSubmission(), false);
  assert.equal(reloaded.host.children[0].open, false);
  assert.deepEqual(f.confirmed, [[original.id, 42, true]]);
  assert.match(reloaded.host.children[2].textContent, /original card #42.*details differ/);
});

test("a divergent create acknowledgement still needs a trustworthy focused read", async () => {
  const f = fixture();
  f.state.responseLost = false;
  f.state.readCard = (card) => ({ ...card, priority_id: priority });
  const first = await f.submit();
  assert.equal(first.ui.hasUncertainSubmission(), true);
  assert.deepEqual(f.confirmed, []);
  f.state.readCard = (card) => ({ ...card, board_id: otherLane });
  const reloaded = await f.inspectAfterReload(first);
  assert.equal(reloaded.ui.hasUncertainSubmission(), true);
  assert.equal(reloaded.button("Dismiss unresolved creation").hidden, true);
  assert.deepEqual(f.confirmed, []);
  f.state.readCard = (card) => card;
  reloaded.button("Inspect creation").click();
  await idle();
  assert.deepEqual(f.confirmed, [[f.posts[0].id, 42, true]]);
  assert.equal(f.journals.size, 0);
});

test("an absent original can be explicitly dismissed with the draft retained", async () => {
  const f = fixture();
  const first = await f.submit();
  f.state.readCard = () => {
    throw new ApiError("http_error", { status: 404 });
  };
  const reloaded = await f.inspectAfterReload(first);
  assert.equal(reloaded.ui.hasUncertainSubmission(), true);
  assert.equal(reloaded.button("Dismiss unresolved creation").hidden, false);
  reloaded.button("Dismiss unresolved creation").click();
  assert.equal(reloaded.ui.hasUncertainSubmission(), false);
  assert.deepEqual(f.confirmed, []);
  assert.equal(f.posts.length, 1);
  const [raw] = f.journals.values();
  const { title, description, submission } = JSON.parse(raw);
  assert.equal(title, "  Original title  ");
  assert.equal(description, "Original description");
  assert.equal(submission, undefined);
});
