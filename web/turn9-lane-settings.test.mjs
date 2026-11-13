import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const bundle = await build({
  stdin: {
    contents: `
      export { mountLaneSettings } from "./src/lane-settings.ts";
      export { createBrowserApi } from "./api.mjs";
    `,
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  write: false,
  format: "esm",
});
const { mountLaneSettings, createBrowserApi } = await import(
  "data:text/javascript;base64," + Buffer.from(bundle.outputFiles[0].text).toString("base64")
);

const [boardId, actorId, botId, ordinaryId, reviewId, completedId, policyId] = Array.from(
  { length: 7 },
  (_, index) => `efb11b54-e045-4ff0-912e-a429e2ef4a66`.slice(0, -1) + index,
);
const prefix = "/api/2026-11-10";
const version = (id, revision) => `"${id}-${revision}"`;
const at = "2026-11-13 15:00:00+00:00";
const lane = (id, label, position, role, fields = {}) => ({
  id,
  board_id: boardId,
  revision: 3,
  key: role === "ordinary" ? "ordinary" : role,
  label,
  position,
  role,
  retired: false,
  approval_enabled: false,
  reorder_enabled: false,
  created_at: at,
  updated_at: at,
  ...fields,
});
const ordinary = lane(ordinaryId, "Work", 1, "ordinary");
const review = lane(reviewId, "Review", 2, "ready_for_review", {
  bulk_approval_comment: "  Existing\r\ntext\r  ",
});
const completed = lane(completedId, "Completed", 3, "completed");
const reply = (value, status = 200, etag = version(value.id, value.revision)) =>
  Response.json(value, {
    status,
    headers: { ETag: etag, "X-Board-ETag": version(boardId, value.revision) },
  });
const boardReply = (revision, availability = "open") =>
  reply({ id: boardId, revision, name: "Fixture", policy_id: policyId, availability });

globalThis.InputEvent = class extends Event {
  constructor(type, options) {
    super(type);
    this.inputType = options.inputType;
  }
};

class Element extends EventTarget {
  children = [];
  value = "";
  textContent = "";
  selectionStart = 0;
  selectionEnd = 0;
  hidden = false;
  disabled = false;
  checked = false;
  constructor(doc, tag) {
    super();
    this.ownerDocument = doc;
    this.tag = tag;
  }
  setAttribute() {}
  focus() {}
  setSelectionRange(start, end) {
    this.selectionStart = start;
    this.selectionEnd = end;
  }
  append(...children) {
    this.children.push(...children);
    for (const child of children) if (child instanceof Element) child.parent = this;
  }
  replaceChildren(...children) {
    this.children = [];
    this.append(...children);
    if (this.tag === "select") this.value = children[0]?.value ?? "";
  }
  click() {
    for (let current = this; current; current = current.parent) {
      if (current.hidden || current.disabled) return;
    }
    if (this.type === "submit") this.ancestor("form").dispatchEvent(new Event("submit"));
    else this.dispatchEvent(new Event("click"));
  }
  ancestor(tag) {
    for (let current = this; current; current = current.parent)
      if (current.tag === tag) return current;
  }
  all(tag) {
    return [
      ...(this.tag === tag ? [this] : []),
      ...this.children.flatMap((child) => (child instanceof Element ? child.all(tag) : [])),
    ];
  }
  control(label) {
    const field = this.all("label").find(({ children }) => children[0] === label);
    assert.ok(field, `missing label ${label}`);
    return field.children[1];
  }
  button(text) {
    const node = this.all("button").find(({ textContent }) => textContent === text);
    assert.ok(node, `missing button ${text}`);
    return node;
  }
  grant(name, actor) {
    return this.all("fieldset")
      .find(({ children }) => children[0]?.textContent === name)
      .control(actor);
  }
  input(value) {
    this.value = value;
    this.dispatchEvent(new InputEvent("input", { inputType: "insertText" }));
  }
  select(value) {
    this.value = value;
    this.dispatchEvent(new Event("change"));
  }
}

function fixture(fetchImpl) {
  const storage = new Map();
  const api = createBrowserApi({ getCapability: () => "capability", fetchImpl });
  const doc = { createElement: (tag) => new Element(doc, tag) };
  const snapshot = (revision = 4, lanes = [ordinary, review, completed], cards = []) => ({
    board: { id: boardId, revision },
    etag: version(boardId, revision),
    lanes,
    cards,
    users: [
      { id: actorId, label: "Human" },
      { id: botId, label: "Bot" },
    ],
  });
  let active = false;
  const lease = () => {
    if (active) return null;
    active = true;
    return () => {
      active = false;
    };
  };
  function mount(data = snapshot()) {
    const life = new AbortController();
    const host = doc.createElement("section");
    const context = { data, actorId, allowed: true, writable: true };
    let changes = 0;
    const { update } = mountLaneSettings(host, {
      api,
      signal: life.signal,
      boardId,
      getContext: () => context,
      readWithSession: (read) => read(),
      beginWrite: lease,
      beginInspection: lease,
      onChange: () => changes++,
      storage: {
        getItem: (key) => storage.get(key) ?? null,
        setItem: (key, value) => storage.set(key, value),
        removeItem: (key) => storage.delete(key),
      },
    });
    return { life, host, context, update, changes: () => changes };
  }
  return { snapshot, mount, storage };
}

function body(request) {
  return JSON.parse(request.body);
}
const status = (host) =>
  host
    .all("p")
    .map(({ textContent }) => textContent)
    .join(" ");

test("adds after the selected lane with exact scoped grants and a stable UUID", async () => {
  const calls = [];
  const { mount, storage } = fixture((path, request) => {
    calls.push({ path, request });
    const { id, ...payload } = body(request);
    assert.match(id, /^[0-9a-f-]{14}4[0-9a-f-]{21}$/);
    assert.equal(path, `${prefix}/lanes`);
    assert.deepEqual(payload, {
      label: "  Added lane 🧭  ",
      after_id: ordinaryId,
      create_for: [botId],
      connect_for: [actorId],
      board_id: boardId,
      board_etag: version(boardId, 4),
    });
    assert.equal(JSON.parse([...storage.values()][0]).value.id, id);
    return reply(lane(id, payload.label, 2, "ordinary", { revision: 5 }), 201);
  });
  const { host, changes } = mount();
  host.control("New lane label").input("  Added lane 🧭  ");
  host.control("Insert after").select(ordinaryId);
  host.grant("Create rights", "Bot").checked = true;
  host.grant("Connect rights", "Human").checked = true;
  host.button("Add lane").click();
  host.button("Add lane").click();
  await flush();
  assert.equal(calls.length, 1);
  assert.equal(changes(), 1);
  assert.equal(storage.size, 0);
  assert.match(status(host), /Lane added/);
});

test("edits the same lane UUID with original validators and preserves approval line endings", async () => {
  const calls = [];
  const { mount, snapshot } = fixture((path, request) => {
    calls.push(path);
    assert.equal(path, `${prefix}/lanes/${reviewId}`);
    assert.equal(request.headers.get("If-Match"), version(reviewId, 3));
    assert.deepEqual(body(request), {
      board_etag: version(boardId, 5),
      label: "  Revised  ",
      key: "revised_key",
      approval_enabled: true,
      bulk_approval_comment: "  Existing!\r\ntext\r  ",
    });
    return reply({
      ...review,
      ...Object.fromEntries(Object.entries(body(request)).filter(([key]) => key !== "board_etag")),
      revision: 6,
    });
  });
  const first = mount();
  const { host } = first;
  host.control("Lane to edit").select(completedId);
  assert.equal(host.control("Lane key").disabled, true);
  assert.equal(host.button("Retire lane").hidden, true);
  host.control("Lane to edit").select(reviewId);
  host.control("Lane label").input("  Revised  ");
  host.control("Lane key").input("revised_key");
  host.control("Enable lane approval").checked = true;
  host.control("Enable lane approval").dispatchEvent(new Event("change"));
  host.control("Bulk approval comment").input("  Existing!\ntext\n  ");
  first.context.data = snapshot(5);
  first.update();
  host.button("Save lane settings").click();
  await flush();
  assert.equal(calls.length, 1);
  assert.match(status(host), /Lane settings saved/);
});

test("blocks nonempty retirement and invalidates confirmation on a changed board", async () => {
  const calls = [];
  const { mount, snapshot } = fixture((path, request) => {
    calls.push(path);
    assert.equal(path, `${prefix}/lanes/${ordinaryId}`);
    assert.equal(request.headers.get("If-Match"), version(ordinaryId, 3));
    assert.deepEqual(body(request), { board_etag: version(boardId, 6), retired: true });
    return reply({ ...ordinary, retired: true, revision: 7 });
  });
  const first = mount(snapshot(4, [ordinary, review, completed], [{ lane_id: ordinaryId }]));
  const { host } = first;
  assert.match(status(host), /Move or archive the active cards/);
  host.button("Retire lane").click();
  assert.equal(host.button("Confirm retirement").hidden, true);
  first.context.data = snapshot(5);
  first.update();
  host.button("Retire lane").click();
  assert.equal(host.button("Confirm retirement").hidden, false);
  first.context.data = snapshot(6);
  first.update();
  host.button("Confirm retirement").click();
  assert.equal(calls.length, 0);
  host.button("Retire lane").click();
  host.button("Confirm retirement").click();
  await flush();
  assert.equal(calls.length, 1);
  assert.match(status(host), /Lane retired/);
});

test("lost create response recovers the original UUID after remount without a new write", async () => {
  let saved;
  let posts = 0;
  const { mount, storage } = fixture((path, request) => {
    if (request.method === "POST") {
      posts++;
      const { id, label } = body(request);
      saved = lane(id, label, 2, "ordinary", { revision: 5 });
      throw new Error("lost response");
    }
    assert.equal(request.method, "GET");
    if (path === `${prefix}/boards/${boardId}`) return boardReply(5);
    if (path === `${prefix}/boards/${boardId}?view=lanes`)
      return reply(
        {
          board: { id: boardId, revision: 5 },
          lanes: [ordinary, saved, { ...review, position: 3 }, { ...completed, position: 4 }],
          cards: [],
        },
        200,
        version(boardId, 5),
      );
    if (path === `${prefix}/policies/${policyId}`)
      return reply({
        id: policyId,
        board_id: boardId,
        revision: 5,
        create_grants: [],
        move_grants: [],
      });
    assert.equal(path, `${prefix}/lanes/${saved.id}`);
    return reply(saved);
  });
  const first = mount();
  first.host.control("New lane label").input("Lost response");
  first.host.button("Add lane").click();
  await flush();
  assert.equal(storage.size, 1);
  first.life.abort();
  const second = mount();
  assert.equal(second.host.button("Add lane").disabled, true);
  second.host.button("Inspect lane change").click();
  await flush();
  assert.equal(posts, 1);
  assert.equal(storage.size, 0);
  assert.match(status(second.host), /contains the new lane/);
});

test("an inspected retry uses original validators and re-inspects a late original commit", async () => {
  let patches = 0;
  let saved;
  const { mount, storage } = fixture((path, request) => {
    if (request.method === "PATCH") {
      patches++;
      assert.equal(request.headers.get("If-Match"), version(ordinaryId, 3));
      assert.deepEqual(body(request), { board_etag: version(boardId, 4), label: "Pending" });
      if (patches === 1) throw new Error("lost response");
      saved = { ...ordinary, label: "Pending", revision: 5 };
      return new Response(null, { status: 412 });
    }
    if (path === `${prefix}/boards/${boardId}`) {
      return boardReply(saved ? 5 : 4);
    }
    assert.equal(path, `${prefix}/lanes/${ordinaryId}`);
    return reply(saved ?? ordinary);
  });
  const { host } = mount();
  host.control("Lane label").input("Pending");
  host.button("Save lane settings").click();
  await flush();
  assert.equal(host.button("Retry lane change").hidden, true);
  host.button("Inspect lane change").click();
  await flush();
  assert.equal(host.button("Retry lane change").hidden, false);
  host.button("Retry lane change").click();
  await flush();
  assert.equal(storage.size, 1);
  assert.match(status(host), /Inspect the original lane change again/);
  host.button("Inspect lane change").click();
  await flush();
  assert.equal(patches, 2);
  assert.equal(storage.size, 0);
  assert.match(status(host), /saved lane matches/);
});

test("an advancing board during inspection cannot authorize dismissal of an uncertain create", async () => {
  let posts = 0;
  let boards = 0;
  let saved;
  const { mount, storage } = fixture((path, request) => {
    if (request.method === "POST") {
      posts++;
      const { id, label } = body(request);
      saved = lane(id, label, 2, "ordinary", { revision: 5 });
      throw new Error("lost response");
    }
    if (path === `${prefix}/boards/${boardId}`) return boardReply(++boards === 1 ? 4 : 5);
    if (path === `${prefix}/boards/${boardId}?view=lanes`)
      return reply(
        {
          board: { id: boardId, revision: 5 },
          lanes: [ordinary, saved, { ...review, position: 3 }, { ...completed, position: 4 }],
          cards: [],
        },
        200,
        version(boardId, 5),
      );
    if (path === `${prefix}/policies/${policyId}`)
      return reply({
        id: policyId,
        board_id: boardId,
        revision: 5,
        create_grants: [],
        move_grants: [],
      });
    assert.equal(path, `${prefix}/lanes/${saved.id}`);
    return boards === 1 ? new Response(null, { status: 404 }) : reply(saved);
  });
  const { host } = mount();
  host.control("New lane label").input("Late commit");
  host.button("Add lane").click();
  await flush();
  host.button("Inspect lane change").click();
  await flush();
  assert.equal(storage.size, 1);
  assert.equal(host.button("Dismiss pending change").hidden, true);
  assert.equal(host.button("Retry lane change").hidden, true);
  assert.match(status(host), /board changed during inspection/i);
  host.button("Inspect lane change").click();
  await flush();
  assert.equal(posts, 1);
  assert.equal(storage.size, 0);
  assert.match(status(host), /contains the new lane/);
});

test("a verified conflict after a lost create permits editing under the original UUID and validator", async () => {
  const attempts = [];
  const { mount, storage } = fixture((path, request) => {
    if (request.method === "POST") {
      assert.equal(path, `${prefix}/lanes`);
      const draft = body(request);
      attempts.push(draft);
      if (attempts.length === 1) throw new Error("lost original conflict");
      if (attempts.length === 2) {
        return new Response(
          JSON.stringify({
            type: "about:blank",
            title: "Conflict",
            status: 409,
            code: "conflict",
            detail: "The requested identity conflicts with existing state.",
            request_id: actorId,
            outcome: "not_applied",
            errors: [],
          }),
          { status: 409, headers: { "Content-Type": "application/problem+json" } },
        );
      }
      return reply(lane(draft.id, draft.label, 3, "ordinary", { revision: 5 }), 201);
    }
    if (path === `${prefix}/boards/${boardId}`) return boardReply(4);
    assert.equal(path, `${prefix}/lanes/${attempts[0].id}`);
    return new Response(null, { status: 404 });
  });
  const first = mount();
  first.host.control("New lane label").input("  Duplicate label  ");
  first.host.control("Insert after").select(reviewId);
  first.host.grant("Create rights", "Bot").checked = true;
  first.host.grant("Connect rights", "Human").checked = true;
  first.host.button("Add lane").click();
  await flush();
  first.host.button("Inspect lane change").click();
  await flush();
  assert.equal(first.host.button("Edit lane draft").hidden, true);
  first.host.button("Retry lane change").click();
  await flush();
  assert.deepEqual(attempts[1], attempts[0]);
  assert.equal(first.host.button("Edit lane draft").hidden, true);
  first.life.abort();
  const { host } = mount();
  host.button("Inspect lane change").click();
  await flush();
  assert.equal(host.button("Retry lane change").hidden, true);
  assert.equal(host.button("Dismiss pending change").hidden, true);
  host.button("Edit lane draft").click();
  assert.match(status(host), /Lane draft restored/);
  assert.equal(storage.size, 1);
  assert.equal(host.control("New lane label").value, "  Duplicate label  ");
  assert.equal(host.control("Insert after").value, reviewId);
  assert.equal(host.grant("Create rights", "Bot").checked, true);
  assert.equal(host.grant("Connect rights", "Human").checked, true);
  host.control("New lane label").input("  Corrected label  ");
  host.button("Add lane").click();
  await flush();
  assert.equal(attempts.length, 3);
  assert.deepEqual(attempts[2], { ...attempts[0], label: "  Corrected label  " });
  assert.equal(storage.size, 0);
  assert.match(status(host), /Lane added/);
});
