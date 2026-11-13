import assert from "node:assert/strict";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  entryPoints: [fileURLToPath(new URL("./src/relationships.ts", import.meta.url))],
  bundle: true,
  write: false,
  format: "esm",
});
const { mountRelationships } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const cardId = "d2ef6295-ace1-4e6e-bbf7-b1c1823f56fc";
const relatedId = "163b1fa0-2a32-424b-8489-6bbd7761201c";
const journalKey = "localswim.relationships.v1." + boardId;

class Element extends EventTarget {
  constructor(tagName, ownerDocument) {
    super();
    this.tagName = tagName;
    this.ownerDocument = ownerDocument;
    this.children = [];
    this.attributes = new Map();
    this.dataset = {};
    this.textContent = "";
    this.value = "";
  }
  get options() {
    return this.children;
  }
  append(...children) {
    this.children.push(...children.filter((child) => child instanceof Element));
  }
  replaceChildren(...children) {
    this.children = children;
  }
  setAttribute(name, value) {
    this.attributes.set(name, value);
  }
  contains(node) {
    return this === node || this.children.some((child) => child.contains(node));
  }
  descendants() {
    return this.children.flatMap((child) => [child, ...child.descendants()]);
  }
  querySelectorAll(tags) {
    return this.descendants().filter((child) => tags.split(", ").includes(child.tagName));
  }
}

async function until(predicate) {
  for (let turn = 0; turn < 50; turn++) {
    if (predicate()) return;
    await new Promise((resolve) => {
      setImmediate(resolve);
    });
  }
  assert.fail("Relationship UI did not settle.");
}

function fixture() {
  const storage = new Map();
  const leases = new Set();
  const reads = [];
  const writes = [];
  const inspectedEndpoints = [];
  const write = Promise.withResolvers();
  const state = { revision: 4, links: [] };
  const tag = () => `"${boardId}-${state.revision}"`;
  const doc = {
    defaultView: {
      sessionStorage: {
        getItem: (key) => storage.get(key) ?? null,
        setItem: (key, value) => storage.set(key, value),
        removeItem: (key) => storage.delete(key),
      },
    },
    createElement: (tagName) => new Element(tagName, doc),
  };
  const acquire = (source, target) => {
    if (leases.has(source) || leases.has(target)) return null;
    leases.add(source);
    leases.add(target);
    return () => {
      leases.delete(source);
      leases.delete(target);
    };
  };
  const api = {
    listRelationships: async (_board, source) => {
      reads.push(source);
      return { data: { relationships: state.links, next_cursor: null } };
    },
    getBoardIfChanged: async () => ({ etag: tag() }),
    getBoardAvailability: async () => ({ data: { revision: state.revision }, etag: tag() }),
    createRelationship: (id, board, etag, source, target, kind) => {
      writes.push({ id, board, etag, source, target, kind });
      return write.promise;
    },
  };
  function open() {
    const root = doc.createElement("section");
    const ui = mountRelationships(root, {
      api,
      boardId,
      signal: new AbortController().signal,
      readWithSession: (read) => read(),
      ticketLink: () => doc.createElement("a"),
      beginWrite: acquire,
      beginInspection(source, target) {
        inspectedEndpoints.push([source, target]);
        return acquire(source, target);
      },
      refresh() {},
    });
    ui.update(
      {
        cardId,
        boardEtag: tag(),
        canManage: true,
        archived: false,
        cards: [{ id: relatedId, ticket: 2, title: "Related" }],
      },
      true,
    );
    const nodes = root.descendants();
    return {
      ...ui,
      add: nodes.find((node) => node.textContent === "Add relationship"),
      inspect: nodes.find((node) => node.textContent === "Inspect saved relationship"),
      status: nodes.find((node) => node.attributes.get("role") === "status"),
      submit() {
        nodes.find((node) => node.tagName === "input").value = "2";
        nodes.find((node) => node.tagName === "form").dispatchEvent(new Event("submit"));
      },
    };
  }
  return { api, state, reads, writes, inspectedEndpoints, storage, leases, write, open };
}

test("a remounted drawer waits for the original relationship write and retains its UUID through a late commit", async () => {
  const { open, state, reads, writes, inspectedEndpoints, storage, leases, write } = fixture();
  const first = open();
  await until(() => !first.add.disabled);
  first.submit();
  await until(() => writes.length === 1);
  const [request] = writes;
  const journal = storage.get(journalKey);
  assert.equal(JSON.parse(journal).id, request.id);
  first.dispose();

  const replacement = open();
  await until(() => reads.length === 2);
  replacement.inspect.dispatchEvent(new Event("click"));
  assert.match(replacement.status.textContent, /operation is still running/);
  assert.deepEqual(inspectedEndpoints, [[cardId, relatedId]]);
  assert.equal(reads.length, 2);
  assert.equal(storage.get(journalKey), journal);

  write.reject(new Error("Response lost"));
  await until(() => leases.size === 0);
  replacement.inspect.dispatchEvent(new Event("click"));
  await until(() => /still unknown/.test(replacement.status.textContent));
  assert.equal(storage.get(journalKey), journal);
  assert.equal(replacement.add.disabled, true);

  state.revision++;
  state.links = [
    {
      id: request.id,
      source_card_id: request.source,
      target_card_id: request.target,
      kind: request.kind,
      revision: state.revision,
    },
  ];
  replacement.inspect.dispatchEvent(new Event("click"));
  await until(
    () => /matches the submitted change/.test(replacement.status.textContent) && leases.size === 0,
  );
  assert.equal(storage.has(journalKey), false);
  assert.equal(writes.length, 1);
  assert.equal(leases.size, 0);
});

test("inspection cannot clear a replacement journal written during the read", async () => {
  const { api, open, storage, leases } = fixture();
  const original = {
    id: "b3a1e3ae-93ab-4e36-b832-c5580f4510cc",
    source_card_id: cardId,
    target_card_id: relatedId,
    kind: "blocks",
    action: "add",
    baseRevision: 3,
  };
  storage.set(journalKey, JSON.stringify(original));
  const ui = open();
  await until(() => ui.status.textContent.includes("unknown"));
  const waiting = Promise.withResolvers();
  const availability = api.getBoardAvailability;
  api.getBoardAvailability = () => waiting.promise;
  ui.inspect.dispatchEvent(new Event("click"));
  await until(() => /Inspecting/.test(ui.status.textContent));
  const replacement = JSON.stringify({ ...original, kind: "references" });
  storage.set(journalKey, replacement);
  waiting.resolve(await availability());
  await until(() => leases.size === 0);
  assert.equal(storage.get(journalKey), replacement);
  assert.equal(ui.inspect.hidden, false);
});
