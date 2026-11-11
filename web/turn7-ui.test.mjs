import assert from "node:assert/strict";
import fs from "node:fs/promises";
import vm from "node:vm";
import test from "node:test";
import { newOperationId } from "./operation-id.mjs";
import {ApiError, createBrowserApi, validPublishing} from "./api.mjs";
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
async function harness(api, pathname = "/boards/" + board, {
  openSession = async () => "capability",
  pollTimer = setTimeout,
} = {}) {
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
    MouseEvent: class {}, location: {pathname, search: ""}, history: {}, URL, URLSearchParams,
    Blob, setTimeout: pollTimer, AbortSignal, crypto: globalThis.crypto, ApiError, validPublishing, newOperationId,
    createBrowserApi: ({getCapability}) => typeof api === "function" ? api(getCapability) : api,
    openBrowserSession: openSession});
  let source = await fs.readFile(new URL("./app.mjs", import.meta.url), "utf8");
  source = source.replace(/^import .*?;\r?\n/gm, "").replace(/void load\(\);\s*$/, "");
  vm.runInContext(source + "\nglobalThis.hooks = {load, readWithSession, renderCardSearch, renderBoardActions, busy:()=>writing, inspect:()=>cardInspection.inspect()};", context);
  async function settled() { for (let count = 0; count < 40; count++) { await new Promise((resolve) => setTimeout(resolve, 0)); if (!context.hooks.busy()) return; } throw Error("UI did not settle"); }
  const find = (root, tagName, text) => root.querySelectorAll(tagName).find((node) => node.ownText === text);
  const field = (root, label) => find(root, "label", label)?.children[0];
  const submit = (root, label) => { const control = find(root, "button", label); assert.ok(control, label); if (!control.disabled) control.parent.dispatch("submit"); return control; };
  async function navigate(path) {
    context.location.pathname = path;
    await context.hooks.load();
  }
  return {nodes, ...context.hooks, settled, find, field, submit, navigate, focused: () => document.activeElement};
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
    getBoardAvailability: async () => ({data: {id: board, name: "Board", availability: "open", policy_id: backup, revision}, etag: tag(board, revision)}),
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

const publishingOff = {enabled: false, repository: null, last_success_at: null, pending: false, error_code: null};

test("board loading retries a changed revision and adopts current publishing status at a matching revision", async () => {
  const {api} = boardApi();
  const readLanes = api.getBoardLanes;
  let reads = 0;
  api.getBoardLanes = async () => ({...await readLanes(), etag: tag(board, ++reads)});
  api.getBoardIfChanged = async () => ({
    data: {id: board, publishing: {...publishingOff, enabled: true, pending: true}},
    etag: tag(board, 2),
  });
  const ui = await harness(api);
  await ui.load();
  assert.equal(reads, 2);
  assert.match(ui.nodes.main.querySelector("#board-publishing-status").textContent, /publishing is on.*Publication pending/);
  assert.ok(ui.find(ui.nodes.main, "button", "#1 Original"));
});

test("board publishing inspects an uncertain save once and renders automatic push recovery", async () => {
  const {api} = boardApi();
  const readLanes = api.getBoardLanes;
  let publication = {...publishingOff};
  let readFails = true;
  let revision = 1;
  const writes = [];
  api.getBoardLanes = async () => {
    const reply = await readLanes();
    return {data: {...reply.data, board: {...reply.data.board, publishing: publication}}, etag: tag(board, revision)};
  };
  api.configureBoardPublishing = async (id, enabled, etag) => {
    writes.push({id, enabled, etag});
    revision++;
    publication = {...publication, enabled, pending: true, error_code: "git_push_failed"};
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  api.getBoardPublishing = async () => {
    if (readFails) throw new ApiError("transport_failed");
    return {data: publication, etag: tag(board, revision)};
  };
  const ui = await harness(api);
  await ui.load();
  const title = ui.field(ui.nodes.main, "New title");
  title.value = "Draft survives publishing";
  const enabled = ui.field(ui.nodes.main, "Enable board publishing");
  const status = ui.nodes.main.querySelector("#board-publishing-status");
  const refresh = ui.find(ui.nodes.main, "button", "Refresh board publishing status");
  enabled.checked = true;
  enabled.dispatch("change");
  const save = ui.submit(ui.nodes.main, "Save board publishing");
  await ui.settled();
  assert.equal(save.disabled, true);
  assert.equal(enabled.checked, true);
  assert.match(ui.nodes.status.textContent, /Readback failed/);
  ui.submit(ui.nodes.main, "Save board publishing");
  readFails = false;
  refresh.click();
  await ui.settled();
  assert.equal(save.disabled, false);
  assert.match(status.textContent, /Git push failed.*Publication pending/);
  publication = {...publication, pending: false, error_code: null, last_success_at: "2026-11-11 00:00:00+00:00"};
  refresh.click();
  await ui.settled();
  assert.match(status.textContent, /Last successful publish at 2026-11-11/);
  assert.doesNotMatch(status.textContent, /pending|failed/);
  assert.equal(title.value, "Draft survives publishing");
  assert.deepEqual(writes, [{id: board, enabled: true, etag: tag(board, 1)}]);
});

test("workspace publishing and pin order save independently against current workspace tags", async () => {
  let workspace = {revision: 1, pinned_board_ids: [], publishing: {...publishingOff}};
  const workspaceTag = () => tag("workspace-test-id", workspace.revision);
  const writes = [];
  const second = {id: backup, name: "Second board"};
  const api = {
    getSession: async () => ({data: {kind: "human", workspace_admin: true}}),
    listBoards: async () => page("boards", [{id: board, name: "First board"}, second]),
    getWorkspace: async () => ({data: workspace, etag: workspaceTag()}),
    configureWorkspacePublishing: async (enabled, etag) => {
      writes.push({enabled, etag});
      assert.equal(etag, workspaceTag());
      workspace = {...workspace, revision: workspace.revision + 1, publishing: {...workspace.publishing, enabled}};
    },
    setPinnedBoards: async (pins, etag) => {
      writes.push({pins: Array.from(pins), etag});
      assert.equal(etag, workspaceTag());
      workspace = {...workspace, revision: workspace.revision + 1, pinned_board_ids: pins};
    },
  };
  const ui = await harness(api, "/");
  await ui.load();
  const main = ui.nodes.main;
  for (const name of ["First board", "Second board"]) {
    const pin = ui.field(main, "Pin " + name);
    pin.checked = true;
    pin.dispatch("change");
  }
  ui.find(main, "button", "Move Second board up").click();
  const name = ui.field(main, "Board name");
  name.value = "Unsaved board name";
  const enabled = ui.field(main, "Enable workspace publishing");
  enabled.checked = true;
  enabled.dispatch("change");
  ui.submit(main, "Save workspace publishing");
  await ui.settled();
  assert.deepEqual(workspace.pinned_board_ids, []);
  assert.equal(ui.field(main, "Pin Second board").checked, true);
  assert.equal(ui.field(main, "Pin First board").checked, true);
  ui.submit(main, "Save pin order");
  await ui.settled();
  assert.equal(workspace.publishing.enabled, true);
  assert.deepEqual(writes, [
    {enabled: true, etag: tag("workspace-test-id", 1)},
    {pins: [backup, board], etag: tag("workspace-test-id", 2)},
  ]);
  assert.equal(name.value, "Unsaved board name");
  assert.match(main.querySelector("#workspace-publishing-status").textContent, /publishing is on/);
  assert.equal(ui.nodes.boards.children[0].textContent, "Second board");
});

test("board publishing keeps an explicit off draft across an unavailable read and navigation", async () => {
  const {api} = boardApi();
  const readLanes = api.getBoardLanes;
  const publishing = {...publishingOff, enabled: true};
  let available = true;
  api.getBoardLanes = async () => {
    const reply = await readLanes();
    return {data: {...reply.data, board: {...reply.data.board, publishing: available ? publishing : undefined}}, etag: reply.etag};
  };
  api.getBoardPublishing = async () => ({data: publishing, etag: tag(board, 1)});
  const ui = await harness(api);
  const {main} = ui.nodes;
  const enabled = () => ui.field(main, "Enable board publishing");
  await ui.load();
  assert.equal(enabled().checked, true);
  enabled().checked = false;
  enabled().dispatch("change");
  available = false;
  await ui.load();
  assert.match(main.querySelector("#board-publishing-status").textContent, /unavailable/);
  await ui.navigate("/");
  await ui.navigate("/boards/" + board);
  ui.find(main, "button", "Refresh board publishing status").click();
  await ui.settled();
  assert.match(main.querySelector("#board-publishing-status").textContent, /publishing is on/);
  assert.equal(enabled().checked, false);
});

test("workspace recovery retains explicit off and unpin-all drafts and adopts saved values in a clean view", async () => {
  const workspace = {revision: 1, pinned_board_ids: [board], publishing: {...publishingOff, enabled: true}};
  let available = true;
  const api = {
    getSession: async () => ({data: {kind: "human", workspace_admin: true}}),
    listBoards: async () => page("boards", [{id: board, name: "First board"}]),
    getWorkspace: async () => {
      if (!available) throw new ApiError("transport_failed");
      return {data: workspace, etag: tag("workspace-test-id", 1)};
    },
  };
  const ui = await harness(api, "/");
  const {main} = ui.nodes;
  const enabled = () => ui.field(main, "Enable workspace publishing");
  const pin = () => ui.field(main, "Pin First board");
  await ui.load();
  for (const control of [enabled(), pin()]) {
    assert.equal(control.checked, true);
    control.checked = false;
    control.dispatch("change");
  }
  available = false;
  await ui.load();
  assert.match(main.querySelector("#workspace-publishing-status").textContent, /unavailable/);
  await ui.navigate("/missing");
  await ui.navigate("/");
  const clean = await harness(api, "/");
  await clean.load();
  available = true;
  for (const view of [ui, clean]) {
    view.find(view.nodes.main, "button", "Refresh workspace publishing status").click();
    await view.settled();
  }
  assert.match(main.querySelector("#workspace-publishing-status").textContent, /publishing is on/);
  assert.equal(enabled().checked, false);
  assert.equal(pin().checked, false);
  assert.equal(clean.field(clean.nodes.main, "Enable workspace publishing").checked, true);
  assert.equal(clean.field(clean.nodes.main, "Pin First board").checked, true);
});

test("workspace saves freeze only their own editors through readback and navigation", async () => {
  let workspace = {revision: 1, pinned_board_ids: [board], publishing: {...publishingOff}};
  let finishPublishing;
  let finishPins;
  const api = {
    getSession: async () => ({data: {kind: "human", workspace_admin: true}}),
    listBoards: async () => page("boards", [{id: board, name: "First board"}]),
    getWorkspace: async () => ({data: workspace, etag: tag("workspace-test-id", workspace.revision)}),
    configureWorkspacePublishing: (enabled) => new Promise((resolve) => {
      finishPublishing = () => {
        workspace = {...workspace, revision: 2, publishing: {...workspace.publishing, enabled}};
        resolve();
      };
    }),
    setPinnedBoards: (pinned_board_ids) => new Promise((resolve) => {
      finishPins = () => {
        workspace = {...workspace, revision: 3, pinned_board_ids};
        resolve();
      };
    }),
  };
  const ui = await harness(api, "/");
  const {main} = ui.nodes;
  const enabled = () => ui.field(main, "Enable workspace publishing");
  const pin = () => ui.field(main, "Pin First board");
  function toggle(control, checked) {
    assert.equal(control.disabled, false);
    control.checked = checked;
    control.dispatch("change");
  }
  await ui.load();
  toggle(enabled(), true);
  ui.submit(main, "Save workspace publishing");
  assert.equal(enabled().disabled, true);
  toggle(pin(), false);
  finishPublishing();
  await ui.settled();
  assert.equal(pin().checked, false);

  ui.submit(main, "Save pin order");
  assert.equal(pin().disabled, true);
  toggle(enabled(), false);
  await ui.navigate("/missing");
  await ui.navigate("/");
  assert.equal(pin().disabled, true);
  assert.equal(enabled().checked, false);
  finishPins();
  await ui.settled();
  assert.equal(pin().disabled, false);
  ui.find(main, "button", "Refresh workspace publishing status").click();
  await ui.settled();
  assert.equal(enabled().checked, false);
  assert.equal(pin().checked, false);
  assert.equal(workspace.pinned_board_ids.length, 0);
  assert.equal(workspace.publishing.enabled, true);
});

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


function boardAvailabilityApi() {
  const {api} = boardApi();
  const savedBoard = {id: board, name: "Board", policy_id: backup, revision: 1, availability: "open"};
  const writes = [];
  let mutationError;
  let readError;
  let allowed = true;
  let closedOperationalReads = 0;
  const baseRead = api.getBoardLanes;
  api.getSession = async () => ({data: {kind: "human", principal_id: user, workspace_admin: false}});
  api.getPolicy = async () => ({data: {id: backup, board_id: board, resource_grants: [
    {user_id: user, capabilities: allowed ? ["read", "close_board"] : ["read"]},
    {user_id: priority, capabilities: ["close_board"]},
  ]}});
  api.getBoardAvailability = async () => {
    if (readError) throw readError;
    return {data: {...savedBoard}, etag: tag(board, savedBoard.revision)};
  };
  api.getBoardLanes = async () => {
    if (savedBoard.availability === "closed") {
      closedOperationalReads++;
      throw new ApiError("http_error", {status: 503});
    }
    return {...await baseRead(), etag: tag(board, savedBoard.revision)};
  };
  api.setBoardAvailability = async (id, availability, originalTag) => {
    writes.push([id, availability, originalTag]);
    if (originalTag !== tag(board, savedBoard.revision)) throw new ApiError("http_error", {status: 412});
    if (mutationError && !mutationError.outcomeUnknown) throw mutationError;
    savedBoard.availability = availability;
    savedBoard.revision++;
    if (mutationError) throw mutationError;
    return api.getBoardAvailability();
  };
  return {api, savedBoard, writes, closedOperationalReads: () => closedOperationalReads,
    failMutation: (error) => { mutationError = error; },
    failRead: (error) => { readError = error; },
    deny: () => { allowed = false; }};
}

test("board close and reopen use the original tags and retain drafts and selection across workspace navigation", async () => {
  const fixture = boardAvailabilityApi();
  const {api, savedBoard, writes} = fixture;
  const ui = await harness(api);
  const {main, details} = ui.nodes;
  await ui.load();
  ui.find(main, "button", "#1 Original").click();
  await ui.settled();
  ui.field(main, "New title").value = "New card draft";
  ui.field(details, "Description").value = "Saved in memory";
  ui.field(details, "Comment").value = "Unsubmitted comment";

  ui.submit(main, "Close board");
  await ui.settled();
  assert.equal(savedBoard.availability, "closed");
  assert.equal(details.hidden, true);
  assert.equal(ui.find(main, "button", "Add card"), undefined);
  assert.match(main.textContent, /This board is closed/);
  await ui.navigate("/");
  assert.ok(ui.find(main, "h1", "Workspace"));
  await ui.navigate("/boards/" + board);
  ui.submit(main, "Reopen board");
  await ui.settled();
  assert.equal(savedBoard.availability, "open");
  assert.equal(details.hidden, false);
  assert.equal(ui.field(main, "New title").value, "New card draft");
  assert.equal(ui.field(details, "Description").value, "Saved in memory");
  assert.equal(ui.field(details, "Comment").value, "Unsubmitted comment");
  assert.deepEqual(writes, [[board, "closed", tag(board, 1)], [board, "open", tag(board, 2)]]);
  assert.equal(fixture.closedOperationalReads(), 0);
});

test("availability actions require the selected board's grant for the current human", async () => {
  const fixture = boardAvailabilityApi();
  fixture.deny();
  const ui = await harness(fixture.api);
  const {main} = ui.nodes;
  await ui.load();
  assert.ok(ui.find(main, "button", "Add card"));
  assert.equal(ui.find(main, "button", "Close board"), undefined);
  fixture.savedBoard.availability = "closed";
  await ui.load();
  assert.match(main.textContent, /This board is closed/);
  assert.equal(ui.find(main, "button", "Reopen board"), undefined);
  assert.deepEqual(fixture.writes, []);
});

test("an unconfirmed close waits for readable board metadata and never replays the PATCH", async () => {
  const fixture = boardAvailabilityApi();
  const ui = await harness(fixture.api);
  const {main, status} = ui.nodes;
  await ui.load();
  fixture.failMutation(new ApiError("transport_failed", {outcomeUnknown: true}));
  fixture.failRead(new ApiError("transport_failed"));
  const close = ui.submit(main, "Close board");
  await ui.settled();
  assert.equal(close.disabled, true);
  assert.match(status.textContent, /Readback failed/);
  ui.submit(main, "Close board");
  fixture.failRead(undefined);
  ui.find(main, "button", "Inspect saved board").click();
  await ui.settled();
  assert.match(main.textContent, /This board is closed/);
  assert.equal(ui.find(main, "button", "Reopen board").disabled, false);
  assert.equal(fixture.closedOperationalReads(), 0);
  assert.deepEqual(fixture.writes, [[board, "closed", tag(board, 1)]]);
});

test("stale or refused availability actions retain board content and require deliberate retry", async () => {
  const fixture = boardAvailabilityApi();
  const ui = await harness(fixture.api);
  const {main, status} = ui.nodes;
  await ui.load();
  fixture.savedBoard.revision = 2;
  ui.submit(main, "Close board");
  await ui.settled();
  assert.match(status.textContent, /State changed/);
  assert.ok(ui.find(main, "button", "#1 Original"));
  fixture.failMutation(new ApiError("unavailable", {status: 503}));
  ui.submit(main, "Close board");
  await ui.settled();
  assert.match(status.textContent, /refused/);
  assert.equal(fixture.savedBoard.availability, "open");
  assert.deepEqual(fixture.writes, [[board, "closed", tag(board, 1)], [board, "closed", tag(board, 2)]]);
});


test("navigation while close is in flight preserves the selected card and draft for reopening", async () => {
  const {api, savedBoard} = boardAvailabilityApi();
  const setAvailability = api.setBoardAvailability;
  let finishClose;
  const delayed = new Promise((resolve) => { finishClose = resolve; });
  api.setBoardAvailability = async (id, availability, etag) => {
    if (availability === "closed") await delayed;
    return setAvailability(id, availability, etag);
  };
  const ui = await harness(api);
  const {main, details} = ui.nodes;
  await ui.load();
  ui.find(main, "button", "#1 Original").click();
  await ui.settled();
  ui.field(details, "Comment").value = "Comment before leaving";
  ui.submit(main, "Close board");
  await ui.navigate("/");
  assert.equal(savedBoard.availability, "open");
  assert.ok(ui.find(main, "h1", "Workspace"));
  finishClose();
  await ui.settled();
  assert.equal(savedBoard.availability, "closed");
  assert.ok(ui.find(main, "h1", "Workspace"));
  await ui.navigate("/boards/" + board);
  ui.submit(main, "Reopen board");
  await ui.settled();
  assert.equal(ui.field(details, "Comment").value, "Comment before leaving");
});

test("refresh after another client closes the board preserves the drawer and draft for reopening", async () => {
  for (const refresh of [(ui) => ui.load(), (ui) => ui.nodes.refresh.click()]) {
    const {api, savedBoard} = boardAvailabilityApi();
    const ui = await harness(api);
    const {main, details} = ui.nodes;
    await ui.load();
    ui.find(main, "button", "#1 Original").click();
    await ui.settled();
    ui.field(details, "Comment").value = "Comment before refresh";
    savedBoard.availability = "closed";
    savedBoard.revision++;
    await refresh(ui);
    await ui.settled();
    assert.match(main.textContent, /This board is closed/);
    assert.equal(details.hidden, true);
    ui.submit(main, "Reopen board");
    await ui.settled();
    assert.equal(ui.field(details, "Comment").value, "Comment before refresh");
  }
});


test("overlapping full loads after an external close retain only a still-selected drawer", async () => {
  const {api, savedBoard} = boardAvailabilityApi();
  const ui = await harness(api);
  const {main, details} = ui.nodes;
  await ui.load();
  ui.find(main, "button", "#1 Original").click();
  await ui.settled();
  ui.field(details, "Comment").value = "Draft through overlapping reads";

  savedBoard.availability = "closed";
  savedBoard.revision++;
  const readAvailability = api.getBoardAvailability;
  const firstStarted = Promise.withResolvers();
  const releaseFirst = Promise.withResolvers();
  api.getBoardAvailability = async () => {
    api.getBoardAvailability = readAvailability;
    firstStarted.resolve();
    await releaseFirst.promise;
    return readAvailability();
  };
  const earlier = ui.load();
  await firstStarted.promise;
  await ui.load();
  assert.match(main.textContent, /This board is closed/);
  releaseFirst.resolve();
  await earlier;

  ui.submit(main, "Reopen board");
  await ui.settled();
  assert.equal(ui.field(details, "Comment").value, "Draft through overlapping reads");
  ui.find(details, "button", "Close card").click();
  savedBoard.availability = "closed";
  savedBoard.revision++;
  await ui.load();
  ui.submit(main, "Reopen board");
  await ui.settled();
  assert.equal(details.hidden, true);
});

test("returning to the same board during close reconciles the new view from saved state", async () => {
  const {api, savedBoard, writes} = boardAvailabilityApi();
  const setAvailability = api.setBoardAvailability;
  const closeStarted = Promise.withResolvers();
  const finishClose = Promise.withResolvers();
  api.setBoardAvailability = async (id, availability, etag) => {
    if (availability === "closed") {
      closeStarted.resolve();
      await finishClose.promise;
    }
    return setAvailability(id, availability, etag);
  };
  const ui = await harness(api);
  const {main, details} = ui.nodes;
  await ui.load();
  ui.find(main, "button", "#1 Original").click();
  await ui.settled();
  ui.field(details, "Comment").value = "Draft from the original view";
  ui.submit(main, "Close board");
  await closeStarted.promise;
  await ui.navigate("/");
  await ui.navigate("/boards/" + board);
  assert.equal(savedBoard.availability, "open");
  assert.ok(ui.find(main, "button", "Close board"));

  finishClose.resolve();
  await ui.settled();
  assert.match(main.textContent, /This board is closed/);
  assert.equal(ui.find(main, "button", "Close board"), undefined);
  assert.deepEqual(writes, [[board, "closed", tag(board, 1)]]);
  ui.submit(main, "Reopen board");
  await ui.settled();
  assert.equal(ui.field(details, "Comment").value, "Draft from the original view");
});

function workspaceServiceApi(overrides) {
  return {
    getSession: async () => ({data: {kind: "human", workspace_admin: true}}),
    listBoards: async () => page("boards", [{id: board, name: "Board"}]),
    getWorkspace: async () => ({data: {revision: 1, pinned_board_ids: [], publishing: publishingOff}, etag: tag(board, 1)}),
    ...overrides,
  };
}

const serviceTag = '"019aa833-764f-746b-a3f0-e2f7af0b9e3f-0"';
const runningService = {
  service_instance_id: "019aa833-764f-746b-a3f0-e2f7af0b9e3f", build_id: board, port: 53146,
  state: "running",
  api_version: "2026-11-10", restart_error_code: null,
};

test("workspace activation inspects the rotated service using a fresh session without replay or lost draft", async () => {
  const staged = {id: backup, version: "next", validation: "valid", error_code: null};
  const calls = [];
  let sessions = 0;
  let activated = false;
  const ui = await harness((getCapability) => workspaceServiceApi({
    getService: async () => {
      if (activated && getCapability() === "session-1") throw new ApiError("http_error", {status: 401});
      return {data: activated
        ? {...runningService, build_id: backup, service_instance_id: "019aa833-7650-746b-a3f0-e2f7af0b9e3f"}
        : runningService, etag: serviceTag};
    },
    listBuilds: async () => page("builds", [staged]),
    activateBuild: async (...args) => {
      calls.push(args);
      activated = true;
      throw new ApiError("transport_failed", {outcomeUnknown: true});
    },
    getActivation: async (id, buildId) => {
      assert.equal(id, calls[0][0]);
      assert.equal(buildId, backup);
      assert.equal(getCapability(), "session-2");
      return {id, build_id: buildId, state: "activated", error_code: null};
    },
  }), "/", {openSession: async () => "session-" + ++sessions});
  await ui.load();
  await ui.settled();
  const {main} = ui.nodes;
  const name = ui.field(main, "Board name");
  name.value = "Draft stays open";
  const control = ui.find(main, "button", "Activate staged build");
  assert.equal(control.disabled, false);
  control.click();
  control.click();
  await ui.settled();
  assert.equal(sessions, 2);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].slice(1), [backup, serviceTag]);
  assert.match(main.querySelector("#service-operation-status").textContent, /Build activated. Reconnected on the same port/);
  assert.equal(control.disabled, true);
  assert.equal(name.value, "Draft stays open");
});

test("workspace shutdown reports final publication refusal and leaves a healthy service actionable", async () => {
  const staged = {id: backup, version: "broken", validation: "invalid", error_code: "candidate_binary_incompatible"};
  let writes = 0;
  const ui = await harness(workspaceServiceApi({
    getService: async () => ({data: writes ? {...runningService, restart_error_code: "git_push_failed"} : runningService, etag: serviceTag}),
    listBuilds: async () => page("builds", [staged]),
    stopService: async (prior, etag) => {
      assert.equal(Object.hasOwn(prior, "etag"), false);
      assert.equal(etag, serviceTag);
      writes++;
      throw new ApiError("final_publication_failed", {status: 503});
    },
  }), "/");
  await ui.load();
  await ui.settled();
  const {main} = ui.nodes;
  assert.match(main.querySelector("#service-controls").textContent, /Staged build broken is invalid/);
  assert.equal(ui.find(main, "button", "Activate staged build").disabled, true);
  const control = ui.find(main, "button", "Shut down service");
  control.click();
  control.click();
  await ui.settled();
  assert.equal(writes, 1);
  assert.match(main.querySelector("#service-operation-status").textContent, /Final publication failed. The service remains running/);
  assert.match(main.querySelector("#service-controls").textContent, /git_push_failed/);
  assert.equal(control.disabled, false);
});

test("a lost shutdown response disables repeat commands until the running service is explicitly inspected", async () => {
  let writes = 0;
  let reachable = true;
  const ui = await harness(workspaceServiceApi({
    getService: async () => {
      if (!reachable) throw new ApiError("transport_failed");
      return {data: runningService, etag: serviceTag};
    },
    listBuilds: async () => page("builds", []),
    stopService: async () => {
      writes++;
      reachable = false;
      throw new ApiError("transport_failed", {outcomeUnknown: true});
    },
  }), "/");
  await ui.load();
  await ui.settled();
  const {main} = ui.nodes;
  const control = ui.find(main, "button", "Shut down service");
  control.click();
  await ui.settled();
  const outcome = main.querySelector("#service-operation-status");
  assert.match(outcome.textContent, /Shutdown outcome is uncertain/);
  assert.equal(control.disabled, true);
  control.click();
  reachable = true;
  ui.find(main, "button", "Refresh service status").click();
  await ui.settled();
  assert.match(outcome.textContent, /Shutdown could not be confirmed. The service is running/);
  assert.equal(control.disabled, false);
  assert.equal(writes, 1);
});

test("an accepted shutdown leaves lifecycle controls disabled and reports later unreachability only after inspection", async () => {
  let reads = 0;
  let writes = 0;
  const ui = await harness(workspaceServiceApi({
    getService: async () => {
      reads++;
      if (writes) throw new ApiError("transport_failed");
      return {data: runningService, etag: serviceTag};
    },
    listBuilds: async () => page("builds", []),
    stopService: async () => {
      writes++;
      return {...runningService, state: "draining"};
    },
  }), "/");
  await ui.load();
  await ui.settled();
  const {main} = ui.nodes;
  const control = ui.find(main, "button", "Shut down service");
  const outcome = main.querySelector("#service-operation-status");
  control.click();
  await ui.settled();
  assert.equal(control.disabled, true);
  assert.equal(reads, 1);
  assert.match(outcome.textContent, /Shutdown accepted. The service is draining/);
  ui.find(main, "button", "Refresh service status").click();
  await ui.settled();
  assert.equal(reads, 2);
  assert.equal(writes, 1);
  assert.match(outcome.textContent, /Shutdown accepted. The service is unreachable/);
});

test("workspace members can read service health without lifecycle controls", async () => {
  let buildReads = 0;
  const ui = await harness(workspaceServiceApi({
    getSession: async () => ({data: {kind: "human", workspace_admin: false}}),
    getService: async () => ({data: runningService, etag: serviceTag}),
    listBuilds: async () => {
      buildReads++;
      throw new ApiError("http_error", {status: 403});
    },
  }), "/");
  await ui.load();
  await ui.settled();
  const {main} = ui.nodes;
  const content = main.querySelector("#service-controls").textContent;
  assert.match(content, /Service is running/);
  assert.doesNotMatch(content, /Staged build status is unavailable/);
  assert.equal(buildReads, 0);
  assert.equal(ui.find(main, "button", "Shut down service"), undefined);
  assert.equal(ui.find(main, "button", "Activate staged build"), undefined);
});

test("workspace navigation reconnects after another tab replaces the service", async () => {
  const original = boardApi().api;
  let replaced = false;
  let sessions = 0;
  const ui = await harness((getCapability) => ({
    ...original,
    getSession: async () => {
      if (replaced && getCapability() === "session-1") throw new ApiError("http_error", {status: 401});
      return original.getSession();
    },
    getService: async () => ({data: runningService, etag: serviceTag}),
    listBuilds: async () => page("builds", []),
  }), "/", {openSession: async () => "session-" + ++sessions});
  await ui.load();
  await ui.settled();
  replaced = true;
  await ui.navigate("/boards/" + board);
  assert.equal(sessions, 2);
  assert.ok(ui.find(ui.nodes.main, "button", "#1 Original"));
});

async function until(check) {
  for (let attempt = 0; attempt < 80; attempt++) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  assert.fail("Workspace service state did not settle.");
}

test("a detached activation read cannot clear a newer lifecycle operation", async () => {
  const staged = {id: backup, version: "next", validation: "valid", error_code: null};
  let disposition = "accepted";
  let settleDetached;
  let shutdown = false;
  const ui = await harness(workspaceServiceApi({
    getService: async () => {
      if (shutdown) throw new ApiError("transport_failed");
      return {data: runningService, etag: serviceTag};
    },
    listBuilds: async () => page("builds", [staged]),
    activateBuild: async () => {},
    getActivation: async (id, build_id) => {
      if (disposition === "hold") return new Promise((resolve) => {
        settleDetached = () => resolve({id, build_id, state: "failed", error_code: "late_failure"});
      });
      return {id, build_id, state: disposition, error_code: null};
    },
    stopService: async () => {
      shutdown = true;
      throw new ApiError("transport_failed", {outcomeUnknown: true});
    },
  }), "/", {pollTimer: (callback) => setTimeout(callback, 0)});
  await ui.load();
  const {main} = ui.nodes;
  const activate = () => ui.find(main, "button", "Activate staged build");
  const stop = () => ui.find(main, "button", "Shut down service");
  const outcome = () => main.querySelector("#service-operation-status").textContent;
  await until(() => !activate().disabled);
  activate().click();
  await ui.settled();

  disposition = "hold";
  await ui.navigate("/");
  await until(() => settleDetached !== undefined);

  disposition = "failed";
  await ui.navigate("/");
  await until(() => !stop().disabled && /Build activation failed/.test(outcome()));
  stop().click();
  await ui.settled();
  assert.match(outcome(), /Shutdown outcome is uncertain/);

  settleDetached();
  await new Promise((resolve) => setTimeout(resolve, 0));
  shutdown = false;
  ui.find(main, "button", "Refresh service status").click();
  await until(() => !stop().disabled);
  assert.match(outcome(), /Shutdown could not be confirmed/);
});

test("a lifecycle refusal arriving after navigation updates the active service panel", async () => {
  let refuse;
  const ui = await harness(workspaceServiceApi({
    getService: async () => ({data: runningService, etag: serviceTag}),
    listBuilds: async () => page("builds", []),
    stopService: async () => new Promise((_, reject) => {
      refuse = () => reject(new ApiError("final_publication_failed", {status: 503}));
    }),
  }), "/");
  await ui.load();
  const {main} = ui.nodes;
  const stop = () => ui.find(main, "button", "Shut down service");
  await until(() => !stop().disabled);
  stop().click();
  await ui.navigate("/");
  refuse();
  await until(() => !stop().disabled);
  assert.match(main.querySelector("#service-operation-status").textContent, /Final publication failed/);
});

test("aborting a lifecycle read does not cancel a shared session bootstrap", async () => {
  let finish;
  let sessions = 0;
  const ui = await harness({}, "/", {openSession: (...args) => {
    sessions++;
    assert.deepEqual(args, []);
    return new Promise((resolve) => { finish = () => resolve("replacement-capability"); });
  }});
  const timeout = new AbortController();
  const poll = ui.readWithSession(async () => "poll", timeout.signal);
  const navigation = ui.readWithSession(async () => "navigation");
  const interrupted = assert.rejects(poll, {name: "AbortError"});
  timeout.abort();
  await interrupted;
  finish();
  assert.equal(await navigation, "navigation");
  assert.equal(sessions, 1);
});


test("shutdown inspection compares the separate HTTP validator after a lost response", async () => {
  let writes = 0;
  const ui = await harness(workspaceServiceApi({
    getService: async () => ({
      data: writes ? {...runningService, restart_error_code: "git_push_failed"} : runningService,
      etag: writes ? serviceTag.replace('-0"', '-1"') : serviceTag,
    }),
    listBuilds: async () => page("builds", []),
    stopService: async (prior, etag) => {
      assert.equal(Object.hasOwn(prior, "etag"), false);
      assert.equal(etag, serviceTag);
      writes++;
      throw new ApiError("transport_failed", {outcomeUnknown: true});
    },
  }), "/");
  await ui.load();
  await ui.settled();
  const control = ui.find(ui.nodes.main, "button", "Shut down service");
  control.click();
  await ui.settled();
  assert.equal(writes, 1);
  assert.equal(control.disabled, false);
  assert.match(ui.nodes.main.querySelector("#service-operation-status").textContent,
    /Shutdown did not complete. The service is still running/);
});
