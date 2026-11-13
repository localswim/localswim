import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { createBrowserApi } from "./api.mjs";

const compiled = await build({
  entryPoints: [fileURLToPath(new URL("./src/ticket-order.ts", import.meta.url))],
  bundle: true,
  write: false,
  format: "esm",
  external: ["../api.mjs"],
});
const moduleUrl = new URL("./api.mjs", import.meta.url).href;
const source = compiled.outputFiles[0].text.replaceAll('"../api.mjs"', JSON.stringify(moduleUrl));
const { mountTicketOrder } = await import(
  "data:text/javascript;base64," + Buffer.from(source).toString("base64")
);

const board = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const lane = "87b11b54-e045-4ff0-912e-a429e2ef4a67";
const first = "87b11b54-e045-4ff0-912e-a429e2ef4a68";
const second = "87b11b54-e045-4ff0-912e-a429e2ef4a69";
const attachment = "87b11b54-e045-4ff0-912e-a429e2ef4a70";
const tag = (revision) => `"${board}-${revision}"`;
const current = [
  { card_id: first, ticket: 5 },
  { card_id: second, ticket: 11 },
];
const proposed = [
  { card_id: second, ticket: 5 },
  { card_id: first, ticket: 11 },
];
const boardCards = [
  { id: first, ticket: 5, title: "First", cover_attachment_id: attachment },
  { id: second, ticket: 11, title: "Second" },
];

class Element extends EventTarget {
  isConnected = true;
  children = [];
  append(...children) {
    this.children.push(...children);
  }
  replaceChildren(...children) {
    this.children = children;
  }
  setAttribute() {}
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

function response(revision, assignments = proposed) {
  return Response.json(
    {
      board_id: board,
      lane_id: lane,
      board_revision: revision,
      selected_card_ids: [first, second],
      current,
      proposed: assignments,
      changed: assignments === proposed,
    },
    { headers: { "X-Board-ETag": tag(revision) } },
  );
}

function harness() {
  const entries = new Map();
  const storage = {
    getItem: (key) => entries.get(key) ?? null,
    setItem: (key, value) => entries.set(key, value),
    removeItem: (key) => entries.delete(key),
  };
  const calls = [];
  let refreshes = 0;
  let leases = 0;
  let view = {
    laneLabel: "Doing",
    boardEtag: tag(4),
    boardRevision: 4,
    cards: boardCards,
    complete: true,
    writable: true,
  };
  let previewResponse = () => response(4);
  let applyResponse = () => response(5);
  let readResponse = () => ({
    etag: tag(5),
    data: {
      board: { revision: 5 },
      cards: boardCards.map((card) => ({
        ...card,
        ticket: proposed.find(({ card_id }) => card_id === card.id).ticket,
      })),
    },
  });
  const api = createBrowserApi({
    getCapability: async () => "test-capability",
    fetchImpl(path, request) {
      const url = new URL(path, "http://localhost");
      calls.push({
        method: request.method,
        path: url.pathname,
        query: url.searchParams,
        body: request.body,
      });
      return request.method === "PUT" ? applyResponse() : previewResponse();
    },
  });
  const begin = () => {
    if (leases) return null;
    leases++;
    return () => leases--;
  };

  return {
    entries,
    calls,
    refreshes: () => refreshes,
    view: (change) => (view = change === null ? null : { ...view, ...change }),
    previewResponse: (fn) => (previewResponse = fn),
    applyResponse: (fn) => (applyResponse = fn),
    readResponse: (fn) => (readResponse = fn),
    mount() {
      const host = new Element();
      host.ownerDocument = { createElement: () => new Element() };
      const controller = mountTicketOrder(host, {
        boardId: board,
        api: {
          ...api,
          getBoardLanes: async (id) => {
            calls.push({ method: "READBACK", id });
            return readResponse();
          },
        },
        signal: new AbortController().signal,
        readWithSession: (read) => read(),
        getContext: (id) => (id === lane ? view : null),
        beginWrite: begin,
        beginInspection: begin,
        onRefresh: () => refreshes++,
        focusFallback() {},
        storage,
      });
      const [dialog, recovery] = host.children;
      const [, status, list, actions] = dialog.children;
      const [close, preview, apply, inspect, dismiss] = actions.children;
      return {
        ...controller,
        dialog,
        recovery,
        status,
        list,
        close,
        preview,
        apply,
        inspect,
        dismiss,
      };
    },
  };
}

test("ticket preview reads exact visible UUIDs and sends the captured order once", async () => {
  const fixture = harness();
  const panel = fixture.mount();
  const opener = new Element();
  panel.open(lane, opener);
  assert.equal(fixture.calls.length, 0);
  panel.preview.click();
  await flush();
  assert.deepEqual(
    fixture.calls.map(({ method }) => method),
    ["GET"],
  );
  assert.deepEqual(fixture.calls[0].query.getAll("card_id"), [first, second]);
  assert.equal(fixture.calls[0].query.get("preview"), "true");
  assert.deepEqual(
    panel.list.children.map(({ textContent }) => textContent),
    ["#11 → #5 Second", "#5 → #11 First"],
  );
  assert.equal(panel.apply.disabled, false);

  panel.apply.click();
  panel.apply.click();
  await flush();
  assert.deepEqual(
    fixture.calls.map(({ method }) => method),
    ["GET", "PUT", "READBACK"],
  );
  assert.deepEqual(JSON.parse(fixture.calls[1].body), {
    board_id: board,
    expected_board_etag: tag(4),
    card_ids: [second, first],
  });
  assert.equal(fixture.calls[2].id, board);
  assert.match(panel.status.textContent, /Saved ticket assignments match/);
  assert.equal(fixture.entries.size, 0);
  panel.close.click();
  assert.equal(opener.focused, true);
});

test("a partial, unchanged, or invalidated preview offers no Apply", async () => {
  const fixture = harness();
  const panel = fixture.mount();
  fixture.view({ complete: false });
  panel.open(lane, new Element());
  panel.preview.click();
  await flush();
  assert.equal(panel.apply.hidden, true);
  assert.match(panel.status.textContent, /full unfiltered lane/);

  fixture.view({ complete: true });
  panel.update();
  assert.equal(panel.apply.hidden, true);
  fixture.previewResponse(() => response(4, current));
  panel.preview.click();
  await flush();
  assert.match(panel.status.textContent, /already in dependency order/);
  assert.equal(panel.apply.hidden, true);

  fixture.previewResponse(() => response(4));
  panel.preview.click();
  await flush();
  assert.equal(panel.apply.hidden, false);
  fixture.view({ boardRevision: 5, boardEtag: tag(5) });
  panel.update();
  panel.apply.click();
  assert.equal(panel.apply.hidden, true);
  assert.equal(panel.list.hidden, true);
  assert.deepEqual(
    fixture.calls.map(({ method }) => method),
    ["GET", "GET", "GET"],
  );
});

test("a stale Apply is refused without replay and refreshes the board", async () => {
  const fixture = harness();
  fixture.applyResponse(() => new Response(null, { status: 412 }));
  const panel = fixture.mount();
  panel.open(lane, new Element());
  panel.preview.click();
  await flush();
  panel.apply.click();
  await flush();
  assert.deepEqual(
    fixture.calls.map(({ method }) => method),
    ["GET", "PUT"],
  );
  assert.match(panel.status.textContent, /board changed before Apply/);
  assert.equal(fixture.refreshes(), 1);
  assert.equal(fixture.entries.size, 0);
  assert.equal(panel.apply.hidden, true);
});

test("an uncertain Apply survives remount and inspection reports saved UUID assignments", async () => {
  const fixture = harness();
  fixture.applyResponse(() => {
    throw new TypeError("disconnected");
  });
  fixture.readResponse(() => ({
    etag: tag(5),
    data: { board: { revision: 5 }, cards: boardCards },
  }));
  const panel = fixture.mount();
  panel.open(lane, new Element());
  panel.preview.click();
  await flush();
  panel.apply.click();
  await flush();
  assert.match(panel.status.textContent, /outcome is unknown/);
  assert.equal(fixture.entries.size, 1);
  assert.equal(panel.apply.hidden, true);
  panel.dispose();

  fixture.view(null);
  const remounted = fixture.mount();
  assert.equal(remounted.recovery.hidden, false);
  remounted.recovery.click();
  assert.equal(remounted.inspect.hidden, false);
  assert.equal(remounted.dismiss.hidden, true);
  remounted.inspect.click();
  await flush();
  assert.deepEqual(
    fixture.calls.map(({ method }) => method),
    ["GET", "PUT", "READBACK"],
  );
  assert.deepEqual(
    remounted.list.children.map(({ textContent }) => textContent),
    ["Requested #5, saved #11 Second", "Requested #11, saved #5 First"],
  );
  assert.equal(remounted.dismiss.hidden, false);
  remounted.dismiss.click();
  assert.equal(fixture.entries.size, 0);
});
