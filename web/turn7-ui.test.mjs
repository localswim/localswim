import assert from "node:assert/strict";
import fs from "node:fs/promises";
import vm from "node:vm";
import test from "node:test";
import {ApiError, createBrowserApi} from "./api.mjs";
const board = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const lane = "ceab10a8-1492-4c90-aa07-13b83138919a";
const card = "d2ef6295-ace1-4e6e-bbf7-b1c1823f56fc";
const user = "84b4ae21-e224-4f5a-9fca-9abf8e0c86bc";
const priority = "163b1fa0-2a32-424b-8489-6bbd7761201c";
const backup = "f7e04b53-cfd9-417c-af8e-b6316af0a085";
const tag = (id, revision) => JSON.stringify(id + "-" + revision);
const page = (key, rows) => ({data: {[key]: rows, next_cursor: null}, etag: null});

// A small DOM surface executes app.mjs event handlers and asynchronous reads.
// Browser rendering and platform behavior remain covered by the real-browser gate.
async function harness(api) {
  let document;
  class Element {
    constructor(tagName) { this.tagName = tagName; this.children = []; this.dataset = {}; this.attributes = {}; this.events = {}; this.value = ""; this.ownText = ""; this.disabled = false; this.hidden = false; }
    set textContent(value) { this.ownText = value; this.children = []; }
    get textContent() { return this.ownText + this.children.map((node) => node.textContent).join(""); }
    get isConnected() { return this.root === true || Boolean(this.parent?.isConnected); }
    append(...nodes) { for (const node of nodes) { node.parent = this; this.children.push(node); } }
    replaceChildren(...nodes) { for (const node of this.children) node.parent = undefined; this.children = []; this.ownText = ""; this.append(...nodes); }
    prepend(node) { node.parent = this; this.children.unshift(node); }
    setAttribute(key, value) { this.attributes[key] = value; }
    removeAttribute(key) { delete this.attributes[key]; }
    addEventListener(key, handler) { (this.events[key] ??= []).push(handler); }
    dispatch(key) { for (const handler of this.events[key] ?? []) handler({preventDefault() {}}); }
    click() { if (!this.disabled) this.dispatch("click"); }
    focus() { document.activeElement = this; }
    closest(selector) { if (selector === '[data-unavailable="true"]' && this.dataset.unavailable === "true") return this; return this.parent?.closest(selector) ?? null; }
    contains(node) { return node === this || this.children.some((child) => child.contains(node)); }
    hasChildNodes() { return this.children.length > 0; }
    cloneNode() { return new this.constructor(this.tagName); }
    querySelectorAll(selector) {
      const descendants = this.children.flatMap((child) => [child, ...child.querySelectorAll("*")]);
      if (selector === "*") return descendants;
      if (selector === "form button[type=submit]") return descendants.filter((child) => child.tagName === "button" && child.type === "submit" && child.parent.tagName === "form");
      if (selector.startsWith("#")) return descendants.filter((child) => child.id === selector.slice(1));
      if (selector.startsWith("button[data-card-id=")) return descendants.filter((child) => child.tagName === "button" && selector.includes(child.dataset.cardId));
      return descendants.filter((child) => child.tagName === selector);
    }
    querySelector(selector) { return this.querySelectorAll(selector)[0] ?? null; }
  }
  class Input extends Element { get valueAsNumber() { return Number(this.value); } }
  class Textarea extends Element {}
  class Button extends Element {}
  class Select extends Element {
    get options() { return this.children; }
    get selectedOptions() { return this.options.filter((option) => option.selected); }
  }
  const nodes = Object.fromEntries(["status", "identity", "boards", "main", "details", "refresh"].map((id) => {
    const node = new Element(id === "main" ? "main" : "div"); node.root = true; return [id, node];
  }));
  document = {getElementById: (id) => nodes[id], querySelector: (selector) => nodes[selector] ?? null,
    adoptedStyleSheets: [], createElement: (tag) => new ({input: Input, textarea: Textarea, button: Button, select: Select}[tag] ?? Element)(tag)};
  const context = vm.createContext({document, window: {addEventListener() {}}, CSSStyleSheet: class {replaceSync() {}},
    HTMLButtonElement: Button, HTMLInputElement: Input, HTMLTextAreaElement: Textarea, HTMLSelectElement: Select,
    MouseEvent: class {}, location: {pathname: "/boards/" + board, search: ""}, history: {}, URL, URLSearchParams,
    Blob, setTimeout, crypto: globalThis.crypto, ApiError, createBrowserApi: () => api, openBrowserSession: async () => "capability"});
  let source = await fs.readFile(new URL("./app.mjs", import.meta.url), "utf8");
  source = source.replace(/^import .*?;\r?\n/, "").replace(/void load\(\);\s*$/, "");
  vm.runInContext(source + "\nglobalThis.hooks = {load, renderCardSearch, renderBoardActions, busy:()=>writing, inspect:()=>cardInspection.inspect()};", context);
  async function settled() { for (let count = 0; count < 40; count++) { await new Promise((resolve) => setTimeout(resolve, 0)); if (!context.hooks.busy()) return; } throw Error("UI did not settle"); }
  const find = (root, tagName, text) => root.querySelectorAll(tagName).find((node) => node.ownText === text);
  const field = (root, label) => find(root, "label", label)?.children[0];
  const submit = (root, label) => { const control = find(root, "button", label); assert.ok(control, label); if (!control.disabled) control.parent.dispatch("submit"); return control; };
  return {nodes, ...context.hooks, settled, find, field, submit, focused: () => document.activeElement};
}

function boardApi() {
  let revision = 1;
  let saved = {id: card, board_id: board, title: "Original", description: "Original description", ticket: 1,
    lane_id: lane, owner_id: user, priority_id: priority, revision: 1, archived: false, cover_attachment_id: null};
  let comments = [{id: backup, card_id: card, author_id: user, text: "Original comment", created_at: "2026-11-10 00:00:00+00:00"}];
  let attachments = [{id: priority, board_id: board, card_id: card, original_name: "Old file", size: 4, added_by: user, created_at: "2026-11-10 00:00:00+00:00", active: true, previewable: false}];
  const lanes = [{id: lane, label: "Ready", key: "ready", retired: false, position: 0, role: "ordinary", revision: 1}];
  let patched;
  const api = {
    getSession: async () => ({data: {kind: "human"}}), listBoards: async () => page("boards", [{id: board, name: "Board"}]),
    getBoardLanes: async () => ({data: {board: {id: board, name: "Board"}, cards: saved ? [saved] : []}, etag: tag(board, revision)}),
    getBoardIfChanged: async () => ({data: null}), listLanes: async () => page("lanes", lanes),
    listPriorities: async () => page("priorities", [{id: priority, label: "Normal"}]),
    listUsers: async () => page("users", [{id: user, label: "Human", kind: "human"}]),
    getCard: async () => { if (!saved) throw new ApiError("http_error", {status: 404}); return {data: saved, etag: tag(card, revision)}; },
    listComments: async () => page("comments", comments), listAttachments: async () => page("attachments", attachments),
    listRelationships: async () => page("relationships", []), listCards: async () => page("cards", []),
    patchCard: async (_id, etag, change) => { patched = etag; saved = {...saved, ...change}; return {data: saved, etag: tag(card, revision)}; },
  };
  function restore(absent = false) { revision = 7; saved = absent ? undefined : {...saved, title: "Recovered", description: "Recovered description", ticket: 9, revision: 7}; comments = []; attachments = []; }
  api.getSnapshot = async () => ({data: {board: {id: board, revision}}, etag: tag(board, revision)});
  api.restoreSnapshot = async () => { restore(); };
  return {api, restore, patched: () => patched};
}

test("native restore refreshes the open drawer through board reload while preserving drafts and focus", async () => {
  const state = boardApi();
  const ui = await harness(state.api);
  await ui.load();
  ui.find(ui.nodes.main, "button", "#1 Original").click();
  await ui.settled();
  const title = ui.field(ui.nodes.details, "Title");
  const description = ui.field(ui.nodes.details, "Description");
  const comment = ui.field(ui.nodes.details, "Comment");
  title.value = "My draft";
  comment.value = "My comment draft";
  title.focus();
  ui.field(ui.nodes.main, "Native backup ZIP").files = [new Blob(["ZIP"])];
  ui.submit(ui.nodes.main, "Restore native backup");
  await ui.settled();
  assert.equal(ui.nodes.details.querySelector("h2").textContent, "#9 Recovered");
  assert.equal(title.value, "My draft");
  assert.equal(description.value, "Recovered description");
  assert.equal(comment.value, "My comment draft");
  assert.equal(ui.focused(), title);
  assert.equal(ui.nodes.details.textContent.includes("Original comment"), false);
  assert.equal(ui.nodes.details.textContent.includes("Old file"), false);
  ui.submit(ui.nodes.details, "Save text");
  await ui.settled();
  assert.equal(state.patched(), tag(card, 7));
});

test("native restore invalidates saved and in-flight search results while preserving search inputs", async () => {
  const state = boardApi();
  const requests = [];
  let releaseOldSearch;
  state.api.listCards = async (_board, filters) => {
    requests.push(filters);
    const results = page("cards", [(await state.api.getCard()).data]);
    if (requests.length !== 2) return results;
    return new Promise((resolve) => { releaseOldSearch = () => resolve(results); });
  };
  const ui = await harness(state.api);
  await ui.load();
  const search = ui.find(ui.nodes.main, "summary", "Find cards").parent;
  ui.field(search, "Title or ticket").value = "Original";
  ui.field(search, "Search descriptions and comments").checked = true;
  ui.field(search, "Search lane").value = lane;
  ui.field(search, "Result order").value = "ticket";
  ui.field(search, "Maximum results").value = "2";
  ui.submit(search, "Find cards"); await ui.settled();
  assert.ok(ui.find(search, "button", "#1 Original"));
  ui.field(search, "Title or ticket").value = "Recovered";
  const findCards = ui.submit(search, "Find cards"); await ui.settled();
  assert.ok(releaseOldSearch);
  assert.equal(findCards.disabled, true);
  ui.field(ui.nodes.main, "Native backup ZIP").files = [new Blob(["ZIP"])];
  ui.submit(ui.nodes.main, "Restore native backup"); await ui.settled();
  assert.equal(ui.nodes.status.textContent, "Native backup restored.");
  assert.equal(ui.find(search, "button", "#1 Original"), undefined);
  assert.equal(findCards.disabled, false);
  assert.match(search.textContent, /Run the search again/);
  assert.equal(requests.length, 2);
  ui.submit(search, "Find cards"); await ui.settled();
  releaseOldSearch(); await ui.settled();
  assert.equal(ui.find(search, "button", "#1 Original"), undefined);
  assert.ok(ui.find(search, "button", "#9 Recovered"));
  assert.deepEqual({...requests[2]}, {q: "Recovered", searchContent: true, sort: "ticket", laneId: lane, limit: 2});
});

test("a card absent after restore retains visible drafts and disables mutation controls", async () => {
  const state = boardApi();
  const ui = await harness(state.api);
  await ui.load();
  ui.find(ui.nodes.main, "button", "#1 Original").click();
  await ui.settled();
  const title = ui.field(ui.nodes.details, "Title"); title.value = "Retained draft";
  state.api.restoreSnapshot = async () => { state.restore(true); };
  ui.field(ui.nodes.main, "Native backup ZIP").files = [new Blob(["ZIP"])];
  ui.submit(ui.nodes.main, "Restore native backup");
  await ui.settled();
  assert.equal(ui.nodes.details.querySelector("h2").textContent, "Card unavailable");
  assert.equal(title.value, "Retained draft");
  assert.ok(ui.nodes.details.querySelectorAll("form button[type=submit]").every((control) => control.disabled));
  ui.find(ui.nodes.details, "button", "Save text").disabled = false;
  ui.submit(ui.nodes.details, "Save text");
  await ui.settled();
  assert.equal(state.patched(), undefined);
  assert.equal(ui.nodes.details.textContent.includes("Original comment"), false);
});

test("default next-work control omits blank q and sends supported triage filters through actual adapter", async () => {
  let query;
  const api = createBrowserApi({getCapability: () => "test", fetchImpl: async (path) => {
    query = new URL(path, "http://localhost").searchParams;
    return Response.json({cards: [], next_cursor: null});
  }});
  const ui = await harness(api);
  const {panel} = ui.renderCardSearch(board, [], () => {});
  ui.nodes.main.append(panel);
  ui.submit(panel, "Find cards");
  await ui.settled();
  assert.equal(query.has("q"), false);
  assert.equal(query.get("sort"), "triage");
  assert.equal(query.get("limit"), "20");
  ui.field(panel, "Title or ticket").value = "   ";
  ui.submit(panel, "Find cards"); await ui.settled();
  assert.equal(query.has("q"), false);
});

test("publication keeps original ID and blocks replacement until explicit status refresh reaches terminal", async () => {
  const ids = [];
  let state = "running";
  const api = {createBackup: async () => {}, getBackup: async () => ({data: {id: backup, board_revision: 1, size: 0, sha2_256: "0".repeat(64), attachment_ids: []}}),
    publishBackup: async (id) => {ids.push(id); throw new ApiError("transport_failed", {outcomeUnknown: true});},
    getPublication: async (id) => {assert.equal(id, ids[0]); return {data: {state, error_code: null}};}};
  const ui = await harness(api);
  const inspection = {sync() {}, require() {}};
  const panel = ui.renderBoardActions(board, [], () => [], () => tag(board, 1), async () => {}, inspection);
  ui.nodes.main.append(panel);
  ui.submit(panel, "Create backup"); await ui.settled();
  const publish = ui.submit(panel, "Publish backup"); await ui.settled();
  assert.equal(publish.disabled, true);
  const status = ui.find(panel, "button", "Refresh publication status");
  assert.equal(status.hidden, false);
  status.click(); await ui.settled();
  assert.equal(ids.length, 1);
  assert.equal(publish.disabled, true);
  const revisited = ui.renderBoardActions(board, [], () => [], () => tag(board, 1), async () => {}, inspection);
  ui.nodes.main.replaceChildren(revisited);
  const revisitedStatus = ui.find(revisited, "button", "Refresh publication status");
  assert.ok(revisitedStatus);
  assert.equal(revisitedStatus.hidden, false);
  assert.equal(ui.find(revisited, "button", "Publish backup").disabled, true);
  state = "succeeded";
  revisitedStatus.click(); await ui.settled();
  assert.equal(ids.length, 1);
  assert.equal(ui.find(revisited, "button", "Publish backup").disabled, false);
  assert.equal(revisitedStatus.hidden, true);
});

test("known publication refusal clears pending status and permits an explicit later attempt", async () => {
  let writes = 0;
  const api = {createBackup: async () => {}, getBackup: async () => ({data: {id: backup, board_revision: 1, size: 0, sha2_256: "0".repeat(64), attachment_ids: []}}),
    publishBackup: async () => { writes++; throw new ApiError("http_error", {status: 403}); }};
  const ui = await harness(api);
  const panel = ui.renderBoardActions(board, [], () => [], () => tag(board, 1), async () => {}, {sync() {}, require() {}});
  ui.nodes.main.append(panel);
  ui.submit(panel, "Create backup"); await ui.settled();
  const publish = ui.submit(panel, "Publish backup"); await ui.settled();
  assert.equal(writes, 1);
  assert.equal(publish.disabled, false);
  assert.equal(ui.find(panel, "button", "Refresh publication status").hidden, true);
});

test("an older publication status response cannot clear a newer pending publication", async () => {
  const ids = [];
  let oldReads = 0;
  let releaseOld;
  const status = (state) => ({data: {state, error_code: null}});
  const api = {createBackup: async () => {}, getBackup: async () => ({data: {id: backup, board_revision: 1, size: 0, sha2_256: "0".repeat(64), attachment_ids: []}}),
    publishBackup: async (id) => { ids.push(id); },
    getPublication: async (id) => {
      if (id !== ids[0]) return status("running");
      oldReads++;
      if (oldReads === 2) return new Promise((resolve) => { releaseOld = () => resolve(status("succeeded")); });
      return status(oldReads === 1 ? "running" : "succeeded");
    }};
  const ui = await harness(api);
  const render = () => ui.renderBoardActions(board, [], () => [], () => tag(board, 1), async () => {}, {sync() {}, require() {}});
  const panel = render(); ui.nodes.main.append(panel);
  ui.submit(panel, "Create backup"); await ui.settled();
  ui.submit(panel, "Publish backup"); await ui.settled();
  ui.find(panel, "button", "Refresh publication status").click(); await ui.settled();
  assert.ok(releaseOld);
  const revisited = render(); ui.nodes.main.replaceChildren(revisited);
  const refreshStatus = ui.find(revisited, "button", "Refresh publication status");
  refreshStatus.click(); await ui.settled();
  const publish = ui.submit(revisited, "Publish backup"); await ui.settled();
  assert.equal(ids.length, 2);
  assert.equal(publish.disabled, true);
  releaseOld(); await ui.settled();
  refreshStatus.click(); await ui.settled();
  assert.equal(publish.disabled, true);
  assert.equal(refreshStatus.hidden, false);
  assert.match(revisited.textContent, /Publication running/);
});
