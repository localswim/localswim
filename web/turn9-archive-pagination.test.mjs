import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { ApiError } from "./api.mjs";

const compiled = await build({
  entryPoints: [fileURLToPath(new URL("./src/search.ts", import.meta.url))],
  bundle: true,
  write: false,
  format: "esm",
  external: ["../api.mjs"],
});
const apiUrl = new URL("./api.mjs", import.meta.url).href;
const source = compiled.outputFiles[0].text.replaceAll('"../api.mjs"', JSON.stringify(apiUrl));
const { mountCardSearch } = await import(
  "data:text/javascript;base64," + Buffer.from(source).toString("base64")
);

const board = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const lane = "87b11b54-e045-4ff0-912e-a429e2ef4a67";

function card(ticket, archived = true) {
  return {
    id: `e2a64472-08aa-4d0c-96ca-${String(ticket).padStart(12, "0")}`,
    board_id: board,
    ticket,
    title: `Archived ${ticket}`,
    archived,
  };
}

function page(cards, next_cursor = null) {
  return { data: { cards, next_cursor } };
}

class Element extends EventTarget {
  children = [];
  dataset = {};
  attributes = new Map();
  value = "";
  hidden = false;

  constructor(tag, doc) {
    super();
    this.tag = tag;
    this.ownerDocument = doc;
  }

  append(...children) {
    this.children.push(...children.filter((child) => typeof child !== "string"));
  }

  replaceChildren(...children) {
    this.children = [];
    this.append(...children);
  }

  contains(node) {
    return node === this || this.children.some((child) => child.contains(node));
  }

  querySelectorAll(tag) {
    return this.children.flatMap((child) =>
      child.tag === tag ? [child] : child.querySelectorAll(tag),
    );
  }

  querySelector(tag) {
    return this.querySelectorAll(tag)[0] ?? null;
  }

  closest(tag) {
    return this.tag === tag ? this : null;
  }

  setAttribute(key, value) {
    this.attributes.set(key, value);
  }

  removeAttribute(key) {
    this.attributes.delete(key);
  }

  getAttribute(key) {
    return key === "data-card-id"
      ? (this.dataset.cardId ?? null)
      : (this.attributes.get(key) ?? null);
  }

  focus() {
    this.ownerDocument.activeElement = this;
  }
}

function harness() {
  const doc = { createElement: (tag) => new Element(tag, doc) };
  const host = doc.createElement("div");
  const owner = new AbortController();
  const calls = [];
  const opened = [];
  const controller = mountCardSearch(host, {
    boardId: board,
    signal: owner.signal,
    readWithSession: (read) => read(),
    api: {
      listCards(boardId, options, signal) {
        const { promise, resolve, reject } = Promise.withResolvers();
        calls.push({ boardId, options, signal, resolve, reject });
        return promise;
      },
    },
    onOpenCard: (reference, opener) => opened.push({ reference, opener }),
    onViewChange() {},
  });
  const lanes = [{ id: lane, label: "Completed" }];
  controller.update(lanes, 1);
  controller.restore({
    open: true,
    query: "Archived",
    content: true,
    order: "ticket",
    laneId: lane,
    limit: "200",
    archive: "only",
    submitted: false,
  });
  const [panel] = host.children;
  const [, form, status, results, more] = panel.children;
  const [query] = form.querySelectorAll("input");
  const [, , visibility] = form.querySelectorAll("select");
  const submit = form.querySelector("button");
  return {
    doc,
    calls,
    opened,
    status,
    more,
    owner,
    submit,
    open(opener) {
      const event = new Event("click");
      Object.defineProperty(event, "target", { value: opener });
      results.dispatchEvent(event);
    },
    rows: () => results.querySelectorAll("button"),
    search: () => form.dispatchEvent(new Event("submit", { cancelable: true })),
    updateQuery(value) {
      query.value = value;
      form.dispatchEvent(new Event("input"));
    },
    updateScope(value) {
      visibility.value = value;
      form.dispatchEvent(new Event("input"));
    },
    updateRevision: (revision) => controller.update(lanes, revision),
    loadMore: () => more.dispatchEvent(new Event("click")),
  };
}

test("archive search appends the final page beyond the API maximum and opens its cards", async () => {
  const h = harness();
  h.search();
  const [first] = h.calls;
  assert.equal(first.boardId, board);
  assert.deepEqual(first.options, {
    q: "Archived",
    searchContent: true,
    sort: "ticket",
    laneId: lane,
    limit: 200,
    cursor: undefined,
    archived: "only",
  });
  const initial = Array.from({ length: 200 }, (_, index) => card(index + 1));
  first.resolve(page(initial, "archive-page-2"));
  await flush();
  assert.equal(h.rows().length, 200);
  assert.equal(h.more.hidden, false);
  const firstOpener = h.rows()[0];

  h.more.focus();
  h.loadMore();
  h.loadMore();
  assert.equal(h.calls.length, 2);
  assert.equal(h.more.getAttribute("aria-disabled"), "true");
  const second = h.calls[1];
  assert.equal(second.boardId, board);
  assert.deepEqual(second.options, { ...first.options, cursor: "archive-page-2" });
  const last = card(201);
  second.resolve(page([last]));
  await flush();

  const rows = h.rows();
  assert.deepEqual(
    rows.map((row) => row.dataset.cardId),
    [...initial, last].map(({ id }) => id),
  );
  assert.equal(rows[0], firstOpener);
  assert.equal(h.doc.activeElement, rows[200]);
  assert.equal(h.more.hidden, true);
  assert.match(h.status.textContent, /Showing all 201/);
  h.open(rows[200]);
  assert.deepEqual(h.opened, [{ reference: { id: last.id }, opener: rows[200] }]);
  h.loadMore();
  assert.equal(h.calls.length, 2);
});

test("a failed or overlapping page keeps existing rows and retries the same cursor", async () => {
  const h = harness();
  h.search();
  h.calls[0].resolve(page([card(1)], "second"));
  await flush();
  const [opener] = h.rows();

  h.more.focus();
  h.loadMore();
  h.calls[1].reject(new ApiError("http_error", { status: 503 }));
  await flush();
  assert.equal(h.doc.activeElement, h.more);
  assert.equal(h.more.textContent, "Retry loading more");
  assert.deepEqual(h.rows(), [opener]);

  h.loadMore();
  h.calls[2].resolve(page([card(1)], "third"));
  await flush();
  assert.deepEqual(h.rows(), [opener]);
  assert.equal(h.more.textContent, "Retry loading more");

  h.loadMore();
  opener.focus();
  h.calls[3].resolve(page([card(2)], "third"));
  await flush();
  assert.equal(h.doc.activeElement, opener);
  assert.equal(h.rows().length, 2);
  assert.equal(h.more.textContent, "Load more");
  assert.deepEqual(
    h.calls.slice(1).map(({ options }) => options.cursor),
    Array(3).fill("second"),
  );
});

test("query and board revision changes discard late pages and reset the cursor", async () => {
  const h = harness();
  h.search();
  h.calls[0].resolve(page([card(1)], "old-page"));
  await flush();
  h.loadMore();
  h.updateQuery("New query");
  assert.equal(h.calls[1].signal.aborted, true);
  assert.equal(h.more.hidden, true);
  h.search();
  assert.equal(h.calls[2].options.cursor, undefined);
  assert.equal(h.calls[2].options.q, "New query");
  h.calls[2].resolve(page([card(3)], "new-page"));
  await flush();
  h.calls[1].resolve(page([card(2)], "old-third"));
  await flush();
  assert.deepEqual(
    h.rows().map((row) => row.dataset.cardId),
    [card(3).id],
  );

  h.more.focus();
  h.loadMore();
  assert.equal(h.calls[3].options.cursor, "new-page");
  h.updateRevision(2);
  h.calls[3].resolve(page([card(4)]));
  await flush();
  assert.equal(h.calls[3].signal.aborted, true);
  assert.equal(h.more.hidden, true);
  assert.equal(h.doc.activeElement, h.submit);
  assert.match(h.status.textContent, /out of date/);
  assert.deepEqual(
    h.rows().map((row) => row.dataset.cardId),
    [card(3).id],
  );
});

test("a rejected continuation requires a fresh search", async () => {
  const h = harness();
  h.search();
  h.calls[0].resolve(page([card(1)], "stale-page"));
  await flush();
  const [opener] = h.rows();

  h.more.focus();
  h.loadMore();
  h.calls[1].reject(new ApiError("http_error", { status: 400 }));
  await flush();
  assert.equal(h.more.hidden, true);
  assert.equal(h.doc.activeElement, h.submit);
  assert.match(h.status.textContent, /out of date. Search again/);
  assert.deepEqual(h.rows(), [opener]);
  h.loadMore();
  assert.equal(h.calls.length, 2);

  h.search();
  assert.equal(h.calls[2].options.cursor, undefined);
  h.calls[2].resolve(page([card(2)]));
  await flush();
  assert.deepEqual(
    h.rows().map((row) => row.dataset.cardId),
    [card(2).id],
  );
});

test("active and mixed search keep the existing result cap", async () => {
  const h = harness();
  for (const scope of ["active", "include"]) {
    h.updateScope(scope);
    h.search();
    const call = h.calls.at(-1);
    assert.equal(call.options.archived, scope === "active" ? undefined : scope);
    call.resolve(page([card(1, false)], "unused"));
    await flush();
    assert.equal(h.more.hidden, true);
    assert.match(h.status.textContent, /Showing the first/);
  }
});
