import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { setImmediate as flush } from "node:timers/promises";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents: `
      export { mountCreateCard } from "./src/create-card.ts";
      export { mountUploads } from "./src/uploads.ts";
      export { ApiError } from "./api.mjs";
    `,
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  write: false,
  format: "esm",
});
const { ApiError, mountCreateCard, mountUploads } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);
globalThis.InputEvent = class InputEvent extends Event {};

class Element extends EventTarget {
  children = [];
  attributes = new Map();
  isConnected = true;
  constructor(tag, doc) {
    super();
    this.tag = tag;
    this.ownerDocument = doc;
  }
  get options() {
    return this.children;
  }
  append(...children) {
    this.children.push(...children.filter((child) => child instanceof Element));
  }
  replaceChildren(...children) {
    this.children = [];
    this.append(...children);
  }
  setAttribute(name, value) {
    this.attributes.set(name, value);
  }
  remove() {}
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

const uuid = (value) => `ac2e106d-7e81-477a-a5ad-${String(value).padStart(12, "0")}`;
const board = uuid(1);
const otherBoard = uuid(2);
const lane = uuid(3);
const owner = uuid(4);
const priority = uuid(5);
const relationship = { id: uuid(6), title: "Existing target", ticket: 6 };
const tag = (id, revision) => `"${id}-${revision}"`;
const file = (name, contents = name, type) => new File([contents], name, { type });
const image = file("screen.png", "image", "image/png");

function fixture() {
  const doc = { createElement: (name) => new Element(name, doc) };
  const values = new Map();
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  };
  const stagedFiles = new Map();
  const posts = [];
  const uploads = [];
  const confirmations = [];
  const admission = [];
  const state = { revision: 20, admitted: true, createMode: "confirm", uploadStatus: null };
  let saved;
  let links = [];
  let leased = false;
  const write = () => {
    assert.equal(leased, false);
    leased = true;
    return () => {
      leased = false;
    };
  };
  const api = {
    async createCard(payload) {
      assert.equal(leased, true);
      posts.push(payload);
      if (state.createMode === "refuse") throw new ApiError("http_error", { status: 403 });
      state.revision++;
      saved = {
        ...payload,
        owner_id: owner,
        priority_id: priority,
        archived: false,
        ticket: 7,
        revision: state.revision,
      };
      links = (payload.relationships ?? []).map(({ kind, target_card_id }) => ({
        id: uuid(9),
        kind,
        source_card_id: saved.id,
        target_card_id,
        revision: saved.revision,
      }));
      if (state.createMode === "lost") throw new TypeError("Response lost.");
      if (state.createMode === "changed")
        return { data: { ...saved, id: uuid(10) }, etag: tag(uuid(10), saved.revision) };
      return {
        data: saved,
        etag: tag(saved.id, saved.revision),
        boardEtag: tag(board, state.revision),
      };
    },
    async getCard(cardId) {
      assert.equal(cardId, saved.id);
      return { data: saved, etag: tag(saved.id, saved.revision) };
    },
    async getBoardAvailability() {
      return {
        data: { availability: "open", revision: state.revision },
        etag: tag(board, state.revision),
      };
    },
    async listRelationships() {
      return { data: { relationships: links, next_cursor: null } };
    },
    async uploadAttachment(cardId, expected, selected, id) {
      assert.equal(leased, true);
      assert.equal(cardId, saved.id);
      assert.equal(expected, tag(cardId, saved.revision));
      const journal = JSON.parse(storage.getItem(`localswim:v2:uploads:${board}:${cardId}`));
      assert.equal(journal.expected, expected);
      assert.equal(journal.remaining[0].id, id);
      assert.equal(stagedFiles.has(board), false);
      uploads.push({ name: selected.name, id });
      if (selected.name === "remaining.txt" && state.uploadStatus)
        throw new ApiError("http_error", { status: state.uploadStatus });
      const data = {
        id,
        board_id: board,
        card_id: cardId,
        original_name: selected.name,
        size: selected.size,
        sha2_256: createHash("sha256")
          .update(Buffer.from(await selected.arrayBuffer()))
          .digest("hex"),
        active: true,
        revision: ++state.revision,
      };
      saved = { ...saved, revision: data.revision };
      return { data, etag: tag(id, data.revision) };
    },
  };
  function mount(boardId = board) {
    const life = new AbortController();
    const { signal } = life;
    const host = doc.createElement("div");
    const uploadHost = doc.createElement("div");
    const readWithSession = (read) => read();
    const uploader = mountUploads(uploadHost, {
      api,
      boardId,
      signal,
      storage,
      readWithSession,
      pasteTarget: doc.createElement("div"),
      canUpload: () => true,
      async awaitUploadAdmission(requestSignal, revision) {
        if (admission.length === 0) assert.equal(leased, false);
        admission.push(revision);
        await flush();
        return state.admitted && !requestSignal.aborted;
      },
      beginWrite: write,
      beginInspect: write,
      onChange() {},
    });
    const ui = mountCreateCard(host, {
      api,
      boardId,
      signal,
      stagedFiles,
      attachments: { canStage: () => true, upload: uploader.submitCreated },
      storage,
      readWithSession,
      context: () => ({
        etag: tag(boardId, state.revision),
        writable: true,
        lanes: [{ id: lane, label: "Ready" }],
        users: [{ id: owner, label: "Human" }],
        priorities: [{ id: priority, label: "Normal" }],
        defaultOwnerId: owner,
        defaultPriorityId: priority,
        relationshipTargets: [relationship],
      }),
      beginWrite: write,
      beginInspection: write,
      onConfirmed: (...args) => confirmations.push(args),
      onStale() {},
      focusFallback() {},
    });
    const find = (predicate) => host.descendants().find(predicate);
    const button = (label) => find((node) => node.tag === "button" && node.textContent === label);
    function open() {
      ui.open(lane, doc.createElement("button"));
    }
    function title(value) {
      const input = find((node) => node.tag === "textarea");
      input.value = value;
      input.dispatchEvent(new Event("input"));
    }
    function pick(...files) {
      const input = find((node) => node.type === "file");
      input.files = files;
      input.dispatchEvent(new Event("change"));
    }
    function paste(...files) {
      const event = new Event("paste", { cancelable: true });
      event.clipboardData = { files, types: ["Files"] };
      find((node) => node.tag === "dialog").dispatchEvent(event);
      return event.defaultPrevented;
    }
    function seed() {
      button("Add relationship").click();
      const input = find((node) => node.type === "text");
      input.value = "6";
      input.dispatchEvent(new Event("input"));
    }
    function rows() {
      return host.descendants().filter((node) => node.className === "create-card-attachment-row");
    }
    function resume(...files) {
      uploader.show({
        cardId: saved.id,
        etag: tag(saved.id, saved.revision),
        readable: true,
        editable: true,
      });
      const picker = uploadHost.descendants().find((node) => node.type === "file");
      picker.files = files;
      picker.dispatchEvent(new Event("change"));
      uploadHost
        .descendants()
        .find((node) => node.textContent === "Resume remaining uploads")
        .click();
    }
    return { button, life, open, paste, pick, resume, rows, seed, title };
  }
  function remaining() {
    const raw = storage.getItem(`localswim:v2:uploads:${board}:${saved.id}`);
    return raw === null ? null : JSON.parse(raw);
  }
  return {
    admission,
    confirmations,
    mount,
    posts,
    remaining,
    stagedFiles,
    state,
    uploads,
    changeSaved: (changes) => {
      saved = { ...saved, ...changes };
    },
    removeLinks: () => {
      links = [];
    },
  };
}

async function settle() {
  for (let i = 0; i < 20; i++) await flush();
}

test("picker and raster paste stay scoped to the draft across board navigation", () => {
  const f = fixture();
  const a = f.mount();
  a.open();
  a.pick(file("first.txt"));
  a.pick(file("second.txt"));
  assert.equal(a.paste(file("unsafe.svg", "", "image/svg+xml")), false);
  assert.equal(a.paste(image), true);
  assert.equal(a.rows().length, 3);
  a.rows()[0].children[1].click();
  assert.deepEqual(
    a.rows().map((row) => row.children[0].textContent),
    ["second.txt", "screen.png"],
  );
  a.life.abort();
  const b = f.mount(otherBoard);
  b.open();
  assert.equal(b.rows().length, 0);
  const again = f.mount();
  again.open();
  assert.equal(again.rows().length, 2);
  assert.deepEqual(f.posts, []);
});

test("creates the card and seeds once and resumes the same remaining upload identity", async () => {
  const f = fixture();
  const view = f.mount();
  view.open();
  view.title("Exact card");
  view.seed();
  view.paste(image);
  view.pick(file("remaining.txt"));
  f.state.uploadStatus = 403;
  view.button("Add card").click();
  await settle();
  assert.equal(f.posts.length, 1);
  assert.deepEqual(f.posts[0].relationships, [
    { kind: "relates_to", target_card_id: relationship.id },
  ]);
  assert.deepEqual(f.confirmations, [[f.posts[0].id, 7, true]]);
  assert.deepEqual(f.admission, [21, 22]);
  assert.deepEqual(
    f.uploads.map(({ name }) => name),
    ["screen.png", "remaining.txt"],
  );
  const [original] = f.remaining().remaining;
  assert.equal(f.remaining().expected, null);
  assert.equal(original.id, f.uploads[1].id);
  f.state.uploadStatus = null;
  view.resume(file("remaining.txt", "wrong contents"));
  await settle();
  assert.equal(f.uploads.length, 2);
  view.resume(file("remaining.txt"));
  await settle();
  assert.deepEqual(f.uploads[2], f.uploads[1]);
  assert.equal(f.remaining(), null);
  assert.equal(f.posts.length, 1);
});

test("a lost response needs explicit inspection and admission, then keeps files for manual handoff", async () => {
  const f = fixture();
  const first = f.mount();
  first.open();
  first.title("Exact card");
  first.pick(image);
  f.state.createMode = "lost";
  first.button("Add card").click();
  await settle();
  assert.deepEqual(f.admission, []);
  first.life.abort();
  const view = f.mount();
  f.state.admitted = false;
  view.button("Inspect pending creation").click();
  view.button("Inspect creation").click();
  await settle();
  assert.deepEqual(f.admission, [21]);
  assert.deepEqual(f.uploads, []);
  assert.equal(f.stagedFiles.get(board).files[0], image);
  assert.equal(view.button("Attach staged files").hidden, true);
  view.button("Review card #7").click();
  f.state.admitted = true;
  view.button("Attach staged files").click();
  await settle();
  assert.equal(f.posts.length, 1);
  assert.equal(f.uploads.length, 1);
});

test("refusal, mismatched acknowledgement, and diverging inspected links never send files implicitly", async () => {
  const f = fixture();
  const view = f.mount();
  view.open();
  view.title("Exact card");
  view.seed();
  view.pick(image);
  f.state.createMode = "refuse";
  view.button("Add card").click();
  await settle();
  assert.equal(view.rows().length, 1);
  f.state.createMode = "changed";
  view.button("Add card").click();
  await settle();
  assert.deepEqual(f.confirmations, []);
  f.removeLinks();
  view.button("Inspect creation").click();
  await settle();
  assert.deepEqual(f.confirmations, [[f.posts[1].id, 7, true]]);
  assert.deepEqual(f.admission, []);
  assert.deepEqual(f.uploads, []);
  assert.equal(f.stagedFiles.get(board).files[0], image);
});

test("a revision change before upload preserves files without dispatch", async () => {
  const f = fixture();
  const view = f.mount();
  view.open();
  view.title("Exact card");
  view.pick(image);
  view.button("Add card").click();
  await flush();
  f.changeSaved({ revision: 22 });
  await settle();
  assert.deepEqual(f.admission, [21]);
  assert.deepEqual(f.uploads, []);
  assert.equal(f.stagedFiles.get(board).files[0], image);
});
