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

const uuid = (value) => `ad2e106d-7e81-477a-a5ad-${String(value).padStart(12, "0")}`;
const boardId = uuid(1);
const laneId = uuid(2);
const ownerId = uuid(3);
const normal = uuid(4);
const urgent = uuid(5);
const active = { id: uuid(6), ticket: 6, title: "Active", priority_id: urgent, completed_at: null };
const archived = {
  id: uuid(7),
  board_id: boardId,
  ticket: 7,
  title: "Archived",
  priority_id: normal,
  completed_at: null,
  archived: true,
};
const expectedSeeds = [
  { target_card_id: active.id, kind: "blocks" },
  { target_card_id: archived.id, kind: "relates_to" },
];
const idle = () =>
  new Promise((resolve) => {
    setImmediate(resolve);
  });

class Element extends EventTarget {
  constructor(tag, doc) {
    super();
    this.tag = tag;
    this.ownerDocument = doc;
    this.children = [];
    this.attributes = new Map();
    this.isConnected = true;
  }
  get options() {
    return this.children;
  }
  append(...children) {
    for (const child of children) {
      if (!(child instanceof Element)) continue;
      child.parent = this;
      this.children.push(child);
    }
  }
  replaceChildren(...children) {
    this.children = [];
    this.append(...children);
  }
  setAttribute(name, value) {
    this.attributes.set(name, value);
  }
  remove() {
    if (this.parent) this.parent.children = this.parent.children.filter((child) => child !== this);
    this.isConnected = false;
  }
  showModal() {
    this.open = true;
  }
  close() {
    this.open = false;
  }
  focus() {
    this.ownerDocument.activeElement = this;
  }
  click() {
    if (!this.hidden && !this.disabled) this.dispatchEvent(new Event("click"));
  }
  descendants() {
    return this.children.flatMap((child) => [child, ...child.descendants()]);
  }
}

function fixture() {
  const posts = [];
  const lookups = [];
  const writes = [];
  const confirmed = [];
  const inspected = [];
  let journal = null;
  let savedCard;
  let links = [];
  const state = {
    revision: 20,
    canManage: true,
    nextPost: "lost-after",
    lookup: (ticket) => (ticket === archived.ticket ? [archived] : []),
    mutateLinks: (saved) => saved,
    afterList() {},
  };
  const tag = () => `"${boardId}-${state.revision}"`;
  const doc = { createElement: (tag) => new Element(tag, doc) };
  const api = {
    async createCard(payload) {
      assert.deepEqual(JSON.parse(journal).submission.seeds, payload.relationships);
      posts.push(payload);
      if (state.nextPost === "lost-before") throw new TypeError("Response lost");
      if (state.nextPost === "refuse") throw new ApiError("http_error", { status: 400 });
      if (savedCard) throw new ApiError("http_error", { status: 409 });
      assert.equal(payload.expected_board_etag, tag());
      state.revision++;
      savedCard = {
        ...payload,
        priority_id: urgent,
        owner_id: ownerId,
        ticket: 42,
        revision: state.revision,
      };
      links = payload.relationships.map(({ target_card_id, kind }, index) => ({
        id: uuid(100 + index),
        source_card_id: kind === "relates_to" ? target_card_id : payload.id,
        target_card_id: kind === "relates_to" ? payload.id : target_card_id,
        kind,
        revision: state.revision,
      }));
      if (state.nextPost === "lost-after") throw new TypeError("Response lost");
      return { data: savedCard, boardEtag: tag() };
    },
    async listCards(id, filters) {
      assert.equal(id, boardId);
      assert.equal(filters.archived, "include");
      lookups.push(filters.ticket);
      return { data: { cards: await state.lookup(filters.ticket), next_cursor: null } };
    },
    async getCard(id) {
      assert.equal(id, posts[0].id);
      if (!savedCard) throw new ApiError("http_error", { status: 404 });
      return { data: savedCard };
    },
    async getBoardAvailability(id) {
      assert.equal(id, boardId);
      return { etag: tag(), data: { availability: "open", revision: state.revision } };
    },
    async listRelationships(id, cardId, cursor) {
      assert.equal(id, boardId);
      assert.equal(cardId, posts[0].id);
      inspected.push(cursor);
      const all = state.mutateLinks(links);
      const page = cursor ? all.slice(1) : all.slice(0, 1);
      state.afterList();
      return {
        data: { relationships: page, next_cursor: !cursor && all.length > 1 ? "p2" : null },
      };
    },
  };
  function mount() {
    const host = doc.createElement("div");
    const ui = mountCreateCard(host, {
      api,
      boardId,
      signal: new AbortController().signal,
      context: () => ({
        etag: tag(),
        writable: true,
        lanes: [{ id: laneId, label: "Ready", role: "work" }],
        users: [{ id: ownerId, label: "Human" }],
        priorities: [
          { id: normal, label: "Normal", rank: 1 },
          { id: urgent, label: "Urgent", rank: 0 },
        ],
        defaultOwnerId: ownerId,
        defaultPriorityId: normal,
        relationshipTargets: state.canManage ? [active] : undefined,
      }),
      storage: {
        getItem: () => journal,
        setItem: (_, value) => {
          journal = value;
        },
        removeItem: () => {
          journal = null;
        },
      },
      beginWrite: (ids) => {
        writes.push(ids);
        return () => {};
      },
      readWithSession: (read) => read(),
      onConfirmed: (...args) => confirmed.push(args),
      onStale() {},
      focusFallback() {},
    });
    const find = (predicate) => host.descendants().find(predicate);
    const button = (label) => find((node) => node.tag === "button" && node.textContent === label);
    function input(node, value, event = "input") {
      node.value = value;
      node.dispatchEvent(new Event(event));
    }
    function start() {
      ui.open(laneId, doc.createElement("button"));
      input(
        find((node) => node.tag === "textarea"),
        "New card",
      );
    }
    function add(ticket, kind) {
      button("Add relationship").click();
      const row = host
        .descendants()
        .filter((node) => node.className === "create-card-relationship-row")
        .at(-1);
      input(
        row.descendants().find((node) => node.tag === "input"),
        ticket,
      );
      input(
        row.descendants().find((node) => node.tag === "select"),
        kind,
        "change",
      );
    }
    return { ui, find, button, start, add };
  }
  async function submit() {
    const view = mount();
    view.start();
    view.add("#6", "blocks");
    view.add("7", "relates_to");
    view.button("Add card").click();
    await idle();
    return view;
  }
  async function inspect(view) {
    view.ui.dispose();
    const next = mount();
    next.button("Inspect pending creation").click();
    next.button("Inspect creation").click();
    await idle();
    return next;
  }
  return {
    state,
    mount,
    submit,
    inspect,
    posts,
    lookups,
    writes,
    confirmed,
    inspected,
    get journal() {
      return journal === null ? null : JSON.parse(journal);
    },
  };
}

test("commits multiple typed seeds with the original card and accepts the priority floor", async () => {
  const f = fixture();
  f.state.nextPost = "success";
  await f.submit();
  assert.deepEqual(
    f.posts.map(({ relationships }) => relationships),
    [expectedSeeds],
  );
  assert.deepEqual(f.lookups, [7]);
  assert.deepEqual(f.writes, [[active.id, archived.id]]);
  assert.deepEqual(f.confirmed, [[f.posts[0].id, 42, true]]);
  assert.equal(f.journal, null);
  assert.deepEqual(f.inspected, []);
});

test("reload inspects the exact submitted seeds through pagination and symmetric readback", async () => {
  const f = fixture();
  const view = await f.submit();
  assert.deepEqual(f.journal.submission.seeds, expectedSeeds);
  assert.equal(f.journal.submission.savedPriorityId, urgent);
  f.state.canManage = false;
  const next = await f.inspect(view);
  assert.deepEqual(f.inspected, [undefined, "p2"]);
  assert.deepEqual(f.confirmed, [[f.posts[0].id, 42, true]]);
  assert.equal(f.journal, null);
  assert.equal(next.find((node) => node.tag === "dialog").open, false);
  assert.equal(f.posts.length, 1);
});

test("a changing board keeps the seed submission and later link divergence resolves original identity", async () => {
  const f = fixture();
  const view = await f.submit();
  f.state.afterList = () => {
    f.state.revision++;
  };
  const next = await f.inspect(view);
  assert.deepEqual(f.confirmed, []);
  assert.deepEqual(f.journal.submission.seeds, expectedSeeds);
  f.state.afterList = () => {};
  f.state.mutateLinks = (links) => links.slice(1);
  next.button("Inspect creation").click();
  await idle();
  assert.deepEqual(f.confirmed, [[f.posts[0].id, 42, true]]);
  assert.match(
    next.find((node) => node.textContent?.includes("saved relationships")).textContent,
    /#42/,
  );
  assert.equal(f.journal, null);
  assert.equal(f.posts.length, 1);
});

test("an explicit same-version retry sends the same resolved seed UUIDs and card ID", async () => {
  const f = fixture();
  f.state.nextPost = "lost-before";
  const view = await f.submit();
  const next = await f.inspect(view);
  f.state.lookup = () => {
    throw new Error("The retry must use journaled UUIDs.");
  };
  f.state.nextPost = "success";
  next.button("Retry original creation").click();
  await idle();
  assert.deepEqual(f.posts, [f.posts[0], f.posts[0]]);
  assert.deepEqual(f.writes, [
    [active.id, archived.id],
    [active.id, archived.id],
  ]);
  assert.deepEqual(f.confirmed, [[f.posts[0].id, 42, true]]);
  assert.equal(f.journal, null);
});

test("missing or duplicated targets block dispatch and permission loss retains editable draft rows", async () => {
  const f = fixture();
  const view = f.mount();
  view.start();
  view.add("6", "blocks");
  view.add("#06", "blocks");
  assert.equal(view.button("Add card").disabled, true);
  view.find((node) => node.attributes.get("aria-label") === "Remove relationship 2").click();
  view.add("999", "references");
  view.button("Add card").click();
  await idle();
  assert.deepEqual(f.posts, []);
  assert.deepEqual(f.writes, []);
  assert.equal(f.journal.submission, undefined);
  assert.deepEqual(f.journal.seeds, [
    { ticket: "6", kind: "blocks" },
    { ticket: "999", kind: "references" },
  ]);
  f.state.canManage = false;
  view.ui.update();
  assert.equal(view.button("Add relationship").hidden, true);
  assert.equal(view.button("Add card").disabled, true);
  view.find((node) => node.attributes.get("aria-label") === "Remove relationship 2").click();
  view.find((node) => node.attributes.get("aria-label") === "Remove relationship 1").click();
  assert.equal(view.button("Add card").disabled, false);
});

test("server rejection retains all seed rows with no follow-up writes", async () => {
  const f = fixture();
  f.state.nextPost = "refuse";
  await f.submit();
  assert.deepEqual(
    f.posts.map(({ relationships }) => relationships),
    [expectedSeeds],
  );
  assert.deepEqual(f.confirmed, []);
  assert.deepEqual(f.inspected, []);
  assert.equal(f.state.revision, 20);
  assert.equal(f.journal.submission, undefined);
  assert.deepEqual(f.journal.seeds, [
    { ticket: "#6", kind: "blocks" },
    { ticket: "7", kind: "relates_to" },
  ]);
});

test("a revision change during archived lookup prevents dispatch", async () => {
  const f = fixture();
  f.state.lookup = () => {
    f.state.revision++;
    return [archived];
  };
  await f.submit();
  assert.deepEqual(f.posts, []);
  assert.deepEqual(f.writes, []);
  assert.equal(f.journal.submission, undefined);
});
