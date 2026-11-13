import assert from "node:assert/strict";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const bundle = await build({
  entryPoints: [fileURLToPath(new URL("./src/attachments.ts", import.meta.url))],
  bundle: true,
  write: false,
  format: "esm",
  footer: { js: "export { ApiError };" },
});
const { ApiError, mountAttachments } = await import(
  "data:text/javascript;base64," + Buffer.from(bundle.outputFiles[0].text).toString("base64")
);
const board = "00000000-0000-4000-8000-000000000001";
const card = "00000000-0000-4000-8000-000000000002";
const attachment = "00000000-0000-4000-8000-000000000003";
const tag = (id, revision) => '"' + id + "-" + revision + '"';
const context = {
  cardId: card,
  etag: tag(card, 2),
  attachmentCount: 1,
  coverAttachmentId: null,
  editable: true,
  readable: true,
};
const item = {
  id: attachment,
  card_id: card,
  board_id: board,
  revision: 2,
  active: true,
  previewable: true,
  original_name: "image.png",
  media_type: "image/png",
};
const key = "localswim:v2:attachments:" + board + ":" + card;
const settle = () =>
  new Promise((resolve) => {
    setImmediate(resolve);
  });

class Element extends EventTarget {
  constructor(name, ownerDocument) {
    super();
    this.name = name;
    this.ownerDocument = ownerDocument;
  }
  children = [];
  dataset = {};
  attributes = new Map();
  append(...children) {
    this.children.push(...children);
  }
  replaceChildren(...children) {
    this.children = children;
  }
  setAttribute(name, value) {
    this.attributes.set(name, value);
  }
  getAttribute(name) {
    return this.attributes.get(name);
  }
  querySelectorAll(selector) {
    const [tagName, attr] = selector.replace("]", "").split("[");
    const property = attr
      ?.replace("data-attachment-", "attachment-")
      .replace(/-([a-z])/g, (_match, letter) => letter.toUpperCase());
    return this.children.flatMap((child) => [
      ...(child.name === tagName && (!property || property in child.dataset) ? [child] : []),
      ...child.querySelectorAll(selector),
    ]);
  }
  click() {
    assert.ok(!this.hidden && !this.disabled);
    this.dispatchEvent(new Event("click"));
  }
}

function fixture(overrides = {}, saved, loseFirstWrite = false) {
  const doc = { createElement: (name) => new Element(name, doc) };
  const root = new Element("div", doc);
  const reads = [];
  const writes = [];
  const journal = new Map(saved ? [[key, JSON.stringify(saved)]] : []);
  let revision = 2;
  let cover = null;
  let inventory = [item];
  const api = {
    async listAttachments() {
      return { data: { attachments: inventory, next_cursor: null } };
    },
    async getCard() {
      reads.push("card");
      return {
        data: { id: card, board_id: board, revision, cover_attachment_id: cover },
        etag: tag(card, revision),
      };
    },
    async getBoardAvailability() {
      return { data: { revision }, etag: tag(board, revision) };
    },
    async patchCard(id, etag, patch) {
      writes.push(["cover", id, etag, patch]);
      if (loseFirstWrite && writes.length === 1) {
        throw new ApiError("transport_failed", { outcomeUnknown: true });
      }
      revision++;
      cover = patch.cover_attachment_id;
      return api.getCard();
    },
    async detachAttachment(id, version, etag) {
      writes.push(["detach", id, version, etag]);
      if (loseFirstWrite && writes.length === 1) {
        throw new ApiError("transport_failed", { outcomeUnknown: true });
      }
      revision++;
      inventory = [];
    },
    ...overrides,
  };
  const drawer = mountAttachments(root, {
    api,
    boardId: board,
    signal: new AbortController().signal,
    beginWrite: () => () => {},
    beginInspect: () => () => {},
    getBoardEtag: () => tag(board, revision),
    readWithSession: (read) => read(),
    onChange() {},
    storage: {
      getItem: (name) => journal.get(name) ?? null,
      setItem: (name, value) => journal.set(name, value),
      removeItem: (name) => journal.delete(name),
    },
  });
  drawer.show(context);
  return {
    drawer,
    reads,
    writes,
    journal,
    button: (label) =>
      root.querySelectorAll("button").find(({ textContent }) => textContent === label),
    image: () => root.querySelectorAll("img")[0],
    anchor: () => root.querySelectorAll("a")[0],
  };
}

test("a readiness fence cancels pending Preview and Download across readmission", async () => {
  const waiting = Promise.withResolvers();
  const signals = [];
  const ui = fixture({
    getAttachmentContent(_id, _disposition, _size, _hash, signal) {
      signals.push(signal);
      return waiting.promise;
    },
  });
  await settle();
  ui.button("Preview").click();
  ui.button("Download").click();
  assert.equal(signals.length, 2);
  ui.drawer.show({ ...context, readable: false });
  assert.ok(signals.every((signal) => signal.aborted));
  ui.drawer.show(context);
  waiting.resolve(new Blob(["content"], { type: "image/png" }));
  await settle();
  assert.equal(ui.image().hidden, true);
  assert.equal(ui.anchor(), undefined);
  assert.equal(ui.button("Download").disabled, false);
  assert.equal(ui.button("Preview").disabled, false);
});

test("an inspection crossing a readiness fence cannot clear the journal or authorize retry", async () => {
  const waiting = Promise.withResolvers();
  const saved = { kind: "cover", cover: attachment, cardEtag: context.etag };
  const ui = fixture({ getCard: () => waiting.promise }, saved);
  await settle();
  ui.button("Inspect attachments").click();
  ui.drawer.show({ ...context, readable: false });
  ui.drawer.show(context);
  waiting.resolve({
    data: { id: card, board_id: board, revision: 3, cover_attachment_id: attachment },
    etag: tag(card, 3),
  });
  await settle();
  assert.equal(ui.journal.get(key), JSON.stringify(saved));
  assert.equal(ui.button("Retry saved change").hidden, true);
  assert.equal(ui.button("Inspect attachments").disabled, false);
});

for (const [saved, expected] of [
  [
    { kind: "cover", cover: attachment, cardEtag: context.etag },
    ["cover", card, context.etag, { cover_attachment_id: attachment }],
  ],
  [
    { kind: "detach", id: attachment, revision: 2, boardEtag: tag(board, 2) },
    ["detach", attachment, 2, tag(board, 2)],
  ],
]) {
  test("same-validator " + saved.kind + " requires inspection and explicit retry", async () => {
    const ui = fixture({}, undefined, true);
    await settle();
    ui.button(saved.kind === "cover" ? "Set cover" : "Detach").click();
    await settle();
    assert.deepEqual(ui.writes, [expected]);
    assert.equal(ui.journal.get(key), JSON.stringify(saved));
    assert.equal(ui.button("Retry saved change").hidden, true);
    ui.button("Inspect attachments").click();
    await settle();
    assert.equal(ui.journal.get(key), JSON.stringify(saved));
    assert.deepEqual(ui.writes, [expected]);
    ui.button("Retry saved change").click();
    await settle();
    assert.deepEqual(ui.writes, [expected, expected]);
    assert.equal(ui.journal.has(key), false);
  });
}
