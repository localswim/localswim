import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { createBrowserApi } from "./api.mjs";

const compiled = await build({
  entryPoints: [fileURLToPath(new URL("./src/batch-priority.ts", import.meta.url))],
  bundle: true,
  write: false,
  format: "esm",
  plugins: [
    {
      name: "shared-api-module",
      setup(builder) {
        builder.onResolve({ filter: /^\.\.\/api\.mjs$/ }, () => ({
          path: new URL("./api.mjs", import.meta.url).href,
          external: true,
        }));
      },
    },
  ],
});
const { mountBatchPriority } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const regular = "163b1fa0-2a32-424b-8489-6bbd7761201c";
const urgent = "163b1fa0-2a32-424b-8489-6bbd7761201d";
const unknownPriority = "163b1fa0-2a32-424b-8489-6bbd7761201e";
const lane = "ceab10a8-1492-4c90-aa07-13b83138919a";
const owner = "84b4ae21-e224-4f5a-9fca-9abf8e0c86bc";
const at = "2026-11-10 12:00:00+00:00";
const tag = (revision) => `"${boardId}-${revision}"`;
const card = (ticket, extra) => ({
  id: `d2ef6295-ace1-4e6e-bbf7-b1c1823f56f${ticket}`,
  board_id: boardId,
  lane_id: lane,
  owner_id: owner,
  priority_id: regular,
  title: "Card",
  description: "",
  ticket,
  revision: 4,
  parent_id: null,
  child_ids: [],
  archived: false,
  cover_attachment_id: null,
  comment_count: 0,
  attachment_count: 0,
  blocking_depth: 0,
  blocked_card_count: 0,
  triage_score: 300,
  completed_at: null,
  created_at: at,
  updated_at: at,
  ...extra,
});

class Element extends EventTarget {
  children = [];
  value = "";
  replaceChildren(...children) {
    this.children = children;
  }
  append(...children) {
    this.children.push(...children);
  }
  setAttribute() {}
  click() {
    if (!this.hidden && !this.disabled) this.dispatchEvent(new Event("click"));
  }
}

function fixture() {
  const changes = [];
  const storage = new Map();
  const before = [
    card(1),
    card(2, { archived: true }),
    card(3, { archived: true }),
    card(4, { completed_at: at }),
  ];
  const priorities = [
    { id: regular, label: "P3" },
    { id: urgent, label: "P1" },
  ];
  const state = {
    revision: 4,
    localRevision: 4,
    cards: before,
    writable: true,
    outcome: "confirmed",
    blockCard: null,
    refreshes: 0,
    boardReads: 0,
    inventoryReads: 0,
    dispatch: null,
    writeScope: null,
  };
  let leased = false;
  const api = createBrowserApi({
    getCapability: () => "test-capability",
    async fetchImpl(path, request) {
      const url = new URL(path, "http://localhost");
      if (url.pathname.endsWith(`/boards/${boardId}`)) {
        state.boardReads++;
        const headers = { ETag: tag(state.revision) };
        if (new Headers(request.headers).get("If-None-Match") === headers.ETag)
          return new Response(null, { status: 304, headers });
        return Response.json(
          {
            id: boardId,
            revision: state.revision,
            name: "Test",
            policy_id: lane,
            availability: "open",
          },
          { headers },
        );
      }
      assert.equal(url.pathname, "/api/2026-11-10/cards");
      if (request.method === "GET") {
        state.inventoryReads++;
        assert.equal(url.searchParams.get("board_id"), boardId);
        assert.equal(url.searchParams.get("archived"), "include");
        const cursor = url.searchParams.get("cursor");
        return Response.json({
          cards: cursor ? state.cards.slice(2) : state.cards.slice(0, 2),
          next_cursor: cursor ? null : "next",
        });
      }
      assert.equal(request.method, "PATCH");
      assert.equal(leased, true);
      assert.equal(storage.size, 1);
      changes.push(JSON.parse(request.body));
      if (state.dispatch) await state.dispatch;
      if (state.outcome === "stale") {
        state.revision++;
        return new Response(null, { status: 412 });
      }
      if (state.outcome === "refused") return new Response(null, { status: 409 });
      if (state.outcome === "no-op") {
        return Response.json(
          {
            board_id: boardId,
            board_revision: state.revision,
            cards: [state.cards[0]],
            changes: [],
          },
          { headers: { "X-Board-ETag": tag(state.revision) } },
        );
      }
      state.revision++;
      const promoted = state.cards.slice(0, 3).map((row) => ({
        ...row,
        priority_id: urgent,
        revision: state.revision,
      }));
      state.cards = [...promoted, state.cards[3]];
      if (state.outcome === "unknown") throw new Error("Response lost after the write.");
      const audit = promoted.map((row, index) => ({
        id: `f7e04b53-cfd9-717c-af8e-b6316af0a08${index}`,
        board_id: boardId,
        resource_id: row.id,
        resource_type: "card",
        actor_id: owner,
        revision: state.revision,
        at,
        field: "priority_id",
        operation: index === 2 ? "promote" : "patch",
        before: state.outcome === "misbound" && index === 2 ? urgent : regular,
        after: state.outcome === "unknown-priority" && index === 2 ? unknownPriority : urgent,
      }));
      const selected = promoted.slice(0, 2);
      if (state.outcome === "stale-card") selected[0] = { ...selected[0], revision: 4 };
      if (state.outcome === "promote-only") selected.splice(0, 2, ...before.slice(0, 2));
      if (state.outcome === "unchanged-wrong-revision")
        selected[1] = { ...before[1], revision: state.revision };
      if (state.outcome === "mixed") selected[1] = before[1];
      return Response.json(
        {
          board_id: boardId,
          board_revision: state.revision,
          cards: selected,
          changes:
            state.outcome === "promote-only"
              ? audit.slice(2)
              : ["unchanged-wrong-revision", "mixed"].includes(state.outcome)
                ? [audit[0], audit[2]]
                : audit,
        },
        { headers: { "X-Board-ETag": tag(state.revision) } },
      );
    },
  });
  const begin = (ids = [], affectedIds) => {
    if (affectedIds) state.writeScope = { ids, affectedIds };
    if (leased || (affectedIds ?? ids).includes(state.blockCard)) return null;
    leased = true;
    return () => {
      leased = false;
    };
  };
  function mount() {
    const root = new Element();
    root.ownerDocument = { createElement: () => new Element() };
    const life = new AbortController();
    const controller = mountBatchPriority(root, {
      api,
      signal: life.signal,
      boardId,
      getContext: () => ({
        board: {
          board: { id: boardId, revision: state.localRevision },
          etag: tag(state.localRevision),
          priorities,
        },
        etag: tag(state.localRevision),
        writable: state.writable,
      }),
      readWithSession: (read) => read(),
      beginWrite: begin,
      beginInspection: begin,
      onRefresh: () => {
        state.refreshes++;
      },
      storage: {
        getItem: (key) => storage.get(key) ?? null,
        setItem: (key, value) => storage.set(key, value),
        removeItem: (key) => storage.delete(key),
      },
    });
    const [form, status, result] = root.children;
    const [[, tickets], [, priority]] = form.children.slice(0, 2).map((label) => label.children);
    const [, , submit, inspect, clear] = form.children;
    return {
      ...controller,
      life,
      tickets,
      priority,
      submit,
      inspect,
      clear,
      status,
      result,
      edit(text, selected = urgent) {
        tickets.value = text;
        tickets.dispatchEvent(new Event("input"));
        priority.value = selected;
        priority.dispatchEvent(new Event("change"));
      },
      async save() {
        form.dispatchEvent(new Event("submit", { cancelable: true }));
        await flush();
      },
      rows: () => result.children.map((row) => row.textContent),
    };
  }
  return { changes, storage, state, mount, before };
}

test("submits distinct explicit unfinished tickets once, including archived, and shows upstream promotions", async () => {
  const { changes, storage, state, mount, before } = fixture();
  const view = mount();
  view.edit("2, 1");
  await Promise.all([view.save(), view.save()]);
  assert.deepEqual(changes, [
    {
      board_id: boardId,
      expected_board_etag: tag(4),
      card_ids: [before[1].id, before[0].id],
      priority_id: urgent,
    },
  ]);
  assert.deepEqual(state.writeScope, {
    ids: [before[1].id, before[0].id],
    affectedIds: before.slice(0, 3).map(({ id }) => id),
  });
  assert.deepEqual(view.rows(), [
    "3 priority changes committed.",
    "#2 effective priority · P1",
    "#1 effective priority · P1",
    "#1 · P3 → P1",
    "#2 · P3 → P1",
    "#3 upstream promotion · P3 → P1",
  ]);
  assert.equal(state.inventoryReads, 2);
  assert.equal(state.refreshes, 1);
  assert.equal(storage.size, 0);
  assert.equal(view.tickets.disabled, true);
  state.localRevision = 5;
  view.update();
  assert.equal(view.tickets.disabled, false);
});

test("invalid, completed, foreign and stale selections never send a partial write", async () => {
  const { changes, state, mount } = fixture();
  const view = mount();
  for (const text of ["1 1", "1, 0", "1,,2", "1 9007199254740992", "1 #2"]) {
    view.edit(text);
    await view.save();
    assert.match(view.status.textContent, /distinct positive ticket/);
  }
  assert.equal(state.inventoryReads, 0);
  view.edit("1 4");
  await view.save();
  assert.match(view.status.textContent, /#4 is Completed/);
  view.edit("1 9");
  await view.save();
  assert.match(view.status.textContent, /#9 is unavailable/);
  view.edit("1 2");
  state.revision++;
  await view.save();
  assert.match(view.status.textContent, /board changed/);
  assert.equal(view.submit.disabled, true);
  assert.equal(changes.length, 0);
});

test("local revision changes and unfinished card write fences preserve the original intent", async () => {
  const { changes, state, mount, before } = fixture();
  const view = mount();
  view.edit("1 2");
  state.localRevision++;
  state.revision++;
  view.update();
  await view.save();
  assert.equal(state.inventoryReads, 0);
  assert.match(view.status.textContent, /board changed/);
  view.clear.click();
  assert.equal(view.tickets.value, "");
  view.edit("1 2");
  state.blockCard = before[1].id;
  await view.save();
  assert.match(view.status.textContent, /operation in progress/);
  assert.equal(changes.length, 0);
  state.blockCard = before[2].id;
  await view.save();
  assert.match(view.status.textContent, /operation in progress/);
  assert.equal(changes.length, 0);
  state.blockCard = before[3].id;
  await view.save();
  assert.equal(changes.length, 1);
  assert.equal(changes[0].expected_board_etag, tag(5));
});

test("service stale rejection preserves form intent and requires a fresh explicit selection", async () => {
  const { changes, storage, state, mount } = fixture();
  state.outcome = "stale";
  const view = mount();
  view.edit("1, 2");
  await view.save();
  assert.equal(storage.size, 0);
  assert.equal(view.tickets.value, "1, 2");
  assert.equal(view.submit.disabled, true);
  assert.match(view.status.textContent, /board changed/);
  await view.save();
  assert.equal(changes.length, 1);
});

test("a lost response survives remount and authoritative readback never replays the batch", async () => {
  const { changes, storage, state, mount, before } = fixture();
  state.outcome = "unknown";
  const original = mount();
  original.edit("1, 2");
  await original.save();
  assert.equal(changes.length, 1);
  assert.equal(storage.size, 1);
  assert.match(original.status.textContent, /outcome is unknown/);
  original.life.abort();

  state.localRevision = 5;
  state.revision = 6;
  state.cards = [{ ...state.cards[0], ticket: 7 }, state.cards[2], state.cards[3]];
  const remounted = mount();
  assert.equal(remounted.tickets.value, "1, 2");
  assert.equal(remounted.clear.hidden, true);
  await remounted.save();
  remounted.inspect.click();
  await flush();
  assert.match(remounted.status.textContent, /earlier batch outcome remains unknown/);
  assert.deepEqual(remounted.rows(), [
    "Requested priority · P1",
    "#7 (originally #1) saved priority · P1",
    "Original #2 · unavailable on this board",
  ]);
  assert.equal(storage.size, 1);
  assert.equal(changes.length, 1);
  remounted.clear.click();
  assert.equal(storage.size, 0);
  assert.equal(remounted.tickets.disabled, true);
  assert.deepEqual(changes[0].card_ids, [before[0].id, before[1].id]);
});

test("an unchanged service floor is a confirmed no-op and displays effective priority", async () => {
  const { changes, storage, state, mount } = fixture();
  state.outcome = "no-op";
  const view = mount();
  view.edit("1");
  await view.save();
  assert.equal(changes.length, 1);
  assert.equal(storage.size, 0);
  assert.deepEqual(view.rows(), ["0 priority changes committed.", "#1 effective priority · P3"]);
});

test("a receipt inconsistent with the original inventory requires readback", async () => {
  for (const outcome of [
    "misbound",
    "stale-card",
    "unknown-priority",
    "promote-only",
    "unchanged-wrong-revision",
  ]) {
    const { changes, storage, state, mount } = fixture();
    state.outcome = outcome;
    const view = mount();
    view.edit("1, 2");
    await view.save();
    assert.equal(changes.length, 1, outcome);
    assert.equal(storage.size, 1, outcome);
    assert.deepEqual(view.rows(), [], outcome);
    assert.match(view.status.textContent, /outcome is unknown/, outcome);
  }
});

test("a partial effective change preserves the unchanged card's own revision", async () => {
  const { changes, storage, state, mount } = fixture();
  state.outcome = "mixed";
  const view = mount();
  view.edit("1, 2");
  await view.save();
  assert.equal(changes.length, 1);
  assert.equal(storage.size, 0);
  assert.deepEqual(view.rows().slice(0, 3), [
    "2 priority changes committed.",
    "#1 effective priority · P1",
    "#2 effective priority · P3",
  ]);
});

test("a confirmed response after navigation preserves the original attempt for inspection", async () => {
  const { changes, storage, state, mount } = fixture();
  const response = Promise.withResolvers();
  state.dispatch = response.promise;
  const original = mount();
  original.edit("1, 2");
  await original.save();
  original.life.abort();

  const current = mount();
  response.resolve();
  await flush();
  assert.equal(state.refreshes, 1);
  assert.equal(storage.size, 1);
  current.update();
  assert.equal(current.inspect.hidden, false);
  current.inspect.click();
  await flush();
  assert.deepEqual(current.rows(), [
    "Requested priority · P1",
    "#1 saved priority · P1",
    "#2 saved priority · P1",
  ]);
  assert.equal(changes.length, 1);
});

test("late settlement preserves a newer record owned by a different mount", async () => {
  const { changes, storage, state, mount } = fixture();
  const response = Promise.withResolvers();
  state.dispatch = response.promise;
  const original = mount();
  original.edit("1, 2");
  await original.save();
  assert.equal(changes.length, 1);
  original.life.abort();

  const [[key, prior]] = storage;
  const next = JSON.stringify({ ...JSON.parse(prior), attempt: crypto.randomUUID() });
  storage.set(key, next);
  const current = mount();
  current.inspect.click();
  await flush();
  assert.match(current.status.textContent, /board operation is still in progress/);
  response.resolve();
  await flush();
  assert.equal(storage.get(key), next);
  assert.equal(current.inspect.hidden, false);
  assert.equal(changes.length, 1);
});
