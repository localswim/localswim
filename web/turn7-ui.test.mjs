import assert from "node:assert/strict";
import {createHash} from "node:crypto";
import fs from "node:fs/promises";
import vm from "node:vm";
import test from "node:test";
import { newOperationId } from "./operation-id.mjs";
import {
  ApiError, createBrowserApi, DEFAULT_APPROVAL_COMMENT, MAX_ATTACHMENT_BYTES, MAX_BULK_APPROVAL_COMMENT_LENGTH,
  MAX_LANE_APPROVAL_CARDS, validPublishing,
} from "./api.mjs";
import {createBoardRefresh, waitForRetry, watchUpdates} from "./board-updates.mjs";
import {appendCardProse} from "./card-prose.mjs";
const board = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const lane = "ceab10a8-1492-4c90-aa07-13b83138919a";
const card = "d2ef6295-ace1-4e6e-bbf7-b1c1823f56fc";
const user = "84b4ae21-e224-4f5a-9fca-9abf8e0c86bc";
const priority = "163b1fa0-2a32-424b-8489-6bbd7761201c";
const backup = "f7e04b53-cfd9-417c-af8e-b6316af0a085";
const tag = (id, revision) => JSON.stringify(id + "-" + revision);
const page = (key, rows) => ({data: {[key]: rows, next_cursor: null}, etag: null});
const createdCardReply = ({id, board_id}, revision = 2) => ({data: {id, board_id}, etag: tag(id, revision)});
const createdAt = "2026-11-10 00:00:00+00:00";
const savedUser = (id, label, kind) => ({id, label, kind, revision: 1, created_at: createdAt, color: "#2463a0"});

// A small DOM surface executes app.mjs event handlers and asynchronous reads.
// Browser rendering and platform behavior remain covered by the real-browser gate.
async function harness(api, pathname = "/boards/" + board, {
  openSession = async () => "capability",
  pollTimer = setTimeout,
  catalogTimer = (signal) => new Promise((resolve) => signal.addEventListener("abort", resolve, {once: true})),
  clock = Date,
  storage = tabStorage(),
  workspaceEvents = false,
  scrollLimit = () => Infinity,
} = {}) {
  let document;
  let pointerHit;
  const downloads = [];
  class Element {
    _scrollTop = 0;
    get scrollTop() {
      this._scrollTop = Math.min(this._scrollTop, scrollLimit(this));
      return this._scrollTop;
    }
    set scrollTop(top) { this._scrollTop = Math.min(top, scrollLimit(this)); }
    scrollLeft = 0;
    constructor(tagName) { this.tagName = tagName; this.children = []; this.dataset = {}; this.attributes = {}; this.events = {}; this.value = ""; this.ownText = ""; this.disabled = false; this.hidden = false; this.style = {setProperty(key, value) { this[key] = value; }}; }
    get ownerDocument() { return document; }
    set textContent(value) { this.ownText = value; this.children = []; }
    get textContent() { return this.ownText + this.children.map((node) => node.textContent).join(""); }
    get isConnected() { return this.root === true || Boolean(this.parent?.isConnected); }
    append(...nodes) {
      for (const child of nodes) {
        const node = typeof child === "string" ? new Element("#text") : child;
        if (typeof child === "string") node.textContent = child;
        node.parent = this;
        this.children.push(node);
      }
    }
    replaceChildren(...nodes) { for (const node of this.children) node.parent = undefined; this.children = []; this.ownText = ""; this.append(...nodes); }
    insertBefore(node, reference) {
      const index = this.children.indexOf(reference);
      assert.notEqual(index, -1);
      node.parent = this;
      this.children.splice(index, 0, node);
    }
    prepend(node) { node.parent = this; this.children.unshift(node); }
    remove() { if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1); this.parent = undefined; }
    setAttribute(key, value) { this.attributes[key] = value; }
    removeAttribute(key) { delete this.attributes[key]; }
    addEventListener(key, handler) { (this.events[key] ??= []).push(handler); }
    dispatch(key, data = {}) {
      const event = {defaultPrevented: false, preventDefault() { this.defaultPrevented = true; }, ...data};
      for (const handler of this.events[key] ?? []) handler(event);
      return event.defaultPrevented;
    }
    click() {
      if (this.disabled) return;
      if (this.tagName === "a" && this.download) downloads.push(this.download);
      this.dispatch("click", {button: 0});
    }
    setPointerCapture(id) { this.capturedPointer = id; }
    focus(options) { document.activeElement = this; this.focusOptions = options; }
    closest(selector) {
      if (selector === '[data-unavailable="true"]' && this.dataset.unavailable === "true") return this;
      if (selector === "[hidden]" && this.hidden) return this;
      if (selector === "details:not([open])" && this.tagName === "details" && !this.open) return this;
      return this.parent?.closest(selector) ?? null;
    }
    contains(node) { return node === this || this.children.some((child) => child.contains(node)); }
    hasChildNodes() { return this.children.length > 0; }
    cloneNode() { return new this.constructor(this.tagName); }
    querySelectorAll(selector) {
      const descendants = this.children.flatMap((child) => [child, ...child.querySelectorAll("*")]);
      if (selector === "*") return descendants;
      if (selector === "form button[type=submit]") return descendants.filter((child) => child.tagName === "button" && child.type === "submit" && child.parent.tagName === "form");
      if (selector.startsWith("#")) return descendants.filter((child) => child.id === selector.slice(1));
      if (selector === "button[data-card-id]") return descendants.filter((child) => child.tagName === "button" && child.dataset.cardId);
      if (selector === "button[data-lane-write]") return descendants.filter((child) => child.tagName === "button" && child.dataset.laneWrite);
      if (selector.startsWith("button[data-card-id=")) return descendants.filter((child) => child.tagName === "button" && selector.includes(child.dataset.cardId));
      return descendants.filter((child) => child.tagName === selector);
    }
    querySelector(selector) { return this.querySelectorAll(selector)[0] ?? null; }
  }
  class Input extends Element {
    select() { this.selectedText = this.value; }
    get valueAsNumber() { return Number(this.value); }
    get willValidate() {
      for (let ancestor = this.parent; ancestor; ancestor = ancestor.parent) {
        if (ancestor.tagName === "fieldset" && ancestor.disabled) return false;
      }
      return !this.disabled;
    }
    checkValidity() {
      if (!this.willValidate) return true;
      if (!this.value) return !this.required;
      return this.type !== "number" || (this.valueAsNumber >= Number(this.min) && this.valueAsNumber <= Number(this.max));
    }
  }
  class Textarea extends Element {
    get value() { return this.normalizedValue; }
    set value(value) { this.normalizedValue = value.replace(/\r\n?/g, "\n"); }
    setCustomValidity(message) { this.validationMessage = message; }
    reportValidity() { return !this.validationMessage; }
  }
  class Button extends Element {}
  class Select extends Element {
    get options() { return this.children; }
    get selectedOptions() { return this.options.filter((option) => option.selected); }
  }
  const nodes = Object.fromEntries(["status", "identity", "boards", "main", "details", "refresh"].map((id) => {
    const node = new Element(id === "main" ? "main" : "div"); node.root = true; return [id, node];
  }));
  const layout = new Element("div");
  layout.root = true;
  const sidebar = nodes["board-sidebar"] = new Element("div");
  const toggle = nodes["toggle-board-sidebar"] = new Button("button");
  const separator = nodes["resize-board-sidebar"] = new Element("div");
  layout.append(sidebar, nodes.main, nodes.details);
  sidebar.parentElement = layout;
  sidebar.append(toggle, separator, nodes.boards);
  sidebar.style.setProperty = (key, value) => { sidebar.style[key] = value; };
  let capturedPointer;
  separator.setPointerCapture = (id) => { capturedPointer = id; };
  separator.hasPointerCapture = (id) => capturedPointer === id;
  separator.releasePointerCapture = () => { capturedPointer = undefined; };
  layout.clientWidth = 1400;
  let drawerWidth = 416;
  nodes.details.hidden = true;
  nodes.details.getBoundingClientRect = () => ({width: drawerWidth});
  const layoutStyle = {paddingLeft: "16px", paddingRight: "16px", columnGap: "16px", flexDirection: "row"};
  let sidebarObserver;
  class ResizeObserver {
    constructor(notify) { sidebarObserver = notify; }
    observe() {}
  }
  const body = new Element("body");
  body.root = true;
  document = {body, getElementById: (id) => nodes[id], querySelector: (selector) => nodes[selector] ?? null,
    title: "localswim", adoptedStyleSheets: [],
    elementFromPoint: () => pointerHit ?? null,
    createElement: (tag) => new ({input: Input, textarea: Textarea, button: Button, select: Select}[tag] ?? Element)(tag)};
  const windowEvents = {};
  const window = {sessionStorage: storage, getComputedStyle: () => layoutStyle,
    addEventListener: (name, handler) => { windowEvents[name] = handler; }};
  class StyleSheet { replaceSync(text) { this.text = text; } }
  const context = vm.createContext({document, window, CSSStyleSheet: StyleSheet, ResizeObserver,
    Node: Element, HTMLElement: Element,
    HTMLButtonElement: Button, HTMLInputElement: Input, HTMLTextAreaElement: Textarea, HTMLSelectElement: Select,
    MouseEvent: class {}, location: {pathname, search: ""}, history: {}, URL, URLSearchParams,
    Blob, TextEncoder, TextDecoder, setTimeout: pollTimer, Date: clock, AbortSignal, AbortController, crypto: globalThis.crypto, ApiError, validPublishing, MAX_ATTACHMENT_BYTES, newOperationId,
    createBoardRefresh, appendCardProse,
    waitForRetry: (signal, delay) => delay === 15_000 ? catalogTimer(signal, delay) : waitForRetry(signal, delay),
    watchUpdates: (options) => typeof api.streamEvents === "function" && (options.board || workspaceEvents)
      ? watchUpdates(options) : Promise.resolve(),
    DEFAULT_APPROVAL_COMMENT, MAX_BULK_APPROVAL_COMMENT_LENGTH, MAX_LANE_APPROVAL_CARDS,
    createBrowserApi: ({getCapability}) => typeof api === "function" ? api(getCapability) : api,
    openBrowserSession: openSession});
  let source = await fs.readFile(new URL("./app.mjs", import.meta.url), "utf8");
  source = source.replace(/^import \{[^}]+\} from "[^"]+";\r?\n/gm, "").replace(/void load\(\);\s*$/, "");
  vm.runInContext(source + "\nglobalThis.hooks = {load, readWithSession, renderCardSearch, renderBoardActions, busy:()=>writing || Boolean(pinWrite), uploadingToCard, inspect:()=>cardInspection.inspect(), abortView:()=>activeBoardLoad?.abort()};", context);
  async function settled() { for (let count = 0; count < 40; count++) { await new Promise((resolve) => setTimeout(resolve, 0)); if (!context.hooks.busy()) return; } throw Error("UI did not settle"); }
  const find = (root, tagName, text) => root.querySelectorAll(tagName).find((node) => node.ownText === text);
  const field = (root, label) => find(root, "label", label)?.children[0];
  const submit = (root, label) => { const control = find(root, "button", label); assert.ok(control, label); if (!control.disabled) control.parent.dispatch("submit"); return control; };
  function submitValidated(root, label) {
    const control = find(root, "button", label);
    assert.ok(control, label);
    if (control.parent.querySelectorAll("input").every((input) => input.checkValidity())) submit(root, label);
  }
  async function navigate(path) {
    context.location.pathname = path;
    await context.hooks.load();
  }
  async function popstate(href, wait = true) {
    const url = new URL(href, "http://localhost");
    context.location.pathname = url.pathname;
    context.location.search = url.search;
    windowEvents.popstate();
    if (wait) await settled();
  }
  function keydown(target, key, options = {}) {
    const event = {target, key, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; }, ...options};
    if (target.dispatch("keydown", event)) return true;
    windowEvents.keydown(event);
    return event.defaultPrevented;
  }
  return {nodes, downloads, ...context.hooks, settled, find, field, submit, submitValidated, navigate, popstate,
    keydown, pointerHit: (node) => { pointerHit = node; },
    pagehide: () => windowEvents.pagehide(), focused: () => document.activeElement, focusBody: () => body.focus(),
    resizeLayout(width, direction = "row", detailsWidth = 416) {
      layout.clientWidth = width;
      layoutStyle.flexDirection = direction;
      drawerWidth = detailsWidth;
      sidebarObserver();
    },
    stylesheet: () => document.adoptedStyleSheets[0].text, title: () => document.title};
}

function tabStorage() {
  const entries = new Map();
  return {getItem: (key) => entries.get(key) ?? null, setItem: (key, value) => entries.set(key, value)};
}

const recoveredDrafts = "localswim:tab-drafts";
const recoveredSubmissions = "localswim:tab-submissions";

test("lost comment response survives navigation and reload and inspects only the original submission", async () => {
  const {api} = boardApi();
  const storage = tabStorage();
  const sent = Promise.withResolvers();
  const response = Promise.withResolvers();
  let comments = [];
  let failRead = false;
  const writes = [];
  api.createComment = async (id, _etag, text, commentId) => {
    writes.push({id: commentId, board_id: board, card_id: id, author_id: user, text, created_at: createdAt});
    sent.resolve();
    await response.promise;
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  api.listComments = async (scope) => {
    assert.deepEqual({...scope}, {board_id: board, card_id: card});
    if (failRead) throw new ApiError("transport_failed");
    return page("comments", comments.map((entry) => ({...entry, ticket: 1})));
  };

  const original = await harness(api, undefined, {storage});
  await original.load();
  original.find(original.nodes.main, "button", "#1 Original").click();
  await until(() => original.field(original.nodes.details, "Comment"));
  const comment = original.field(original.nodes.details, "Comment");
  comment.value = "  Exact comment\nΩ  ";
  comment.dispatch("input");
  original.submit(original.nodes.details, "Post comment");
  await sent.promise;
  failRead = true;
  await original.navigate("/");
  response.resolve();
  await original.settled();
  const scope = "card:" + board + ":" + card;
  const [[savedScope, operation]] = JSON.parse(storage.getItem(recoveredSubmissions));
  assert.equal(savedScope, scope);
  assert.deepEqual({...operation, submittedAt: 0}, {id: writes[0].id, kind: "comment", submittedAt: 0});
  assert.deepEqual(JSON.parse(storage.getItem(recoveredDrafts)), [[scope, {comment: comment.value}]]);

  const reloaded = await harness(api, undefined, {storage});
  await reloaded.load();
  const {details} = reloaded.nodes;
  await until(() => reloaded.field(details, "Comment"));
  const restored = reloaded.field(details, "Comment");
  assert.equal(restored.value, writes[0].text);
  const inspect = reloaded.find(details, "button", "Inspect saved card");
  assert.equal(reloaded.submit(details, "Post comment").disabled, true);
  inspect.click();
  await reloaded.settled();
  assert.match(reloaded.nodes.status.textContent, /could not be read/);
  assert.equal(reloaded.submit(details, "Post comment").disabled, true);

  failRead = false;
  comments = [{...writes[0], id: backup}];
  inspect.click();
  await reloaded.settled();
  assert.match(reloaded.nodes.status.textContent, /could not be confirmed/);
  assert.equal(reloaded.submit(details, "Post comment").disabled, true);
  assert.equal(restored.value, writes[0].text);

  restored.value = "A different draft written after reload.";
  restored.dispatch("input");
  comments = writes;
  inspect.click();
  await reloaded.settled();
  assert.match(reloaded.nodes.status.textContent, /Original submission confirmed/);
  assert.equal(restored.value, "A different draft written after reload.");
  assert.equal(reloaded.find(details, "button", "Post comment").disabled, false);
  assert.deepEqual(JSON.parse(storage.getItem(recoveredSubmissions)), []);
  assert.equal(writes.length, 1);
});

test("dismissing or expiring a missing submission releases its scope and retains the draft", async () => {
  const {api} = boardApi();
  const scope = "card:" + board + ":" + card;
  const storage = tabStorage();
  const now = Date.now();
  storage.setItem(recoveredDrafts, JSON.stringify([[scope, {title: "Title draft", description: "", comment: "Draft"}]]));
  storage.setItem(recoveredSubmissions, JSON.stringify([[scope, {kind: "comment", id: priority, submittedAt: now}]]));
  const ui = await harness(api, undefined, {storage});
  await ui.load();
  ui.find(ui.nodes.main, "button", "#1 Original").click();
  await until(() => ui.field(ui.nodes.details, "Comment"));
  assert.equal(ui.field(ui.nodes.details, "Title").value, "Title draft");
  assert.equal(ui.field(ui.nodes.details, "Description").value, "");
  assert.equal(ui.field(ui.nodes.details, "Comment").value, "Draft");
  assert.equal(ui.submit(ui.nodes.details, "Post comment").disabled, true);
  ui.find(ui.nodes.details, "button", "Dismiss unresolved action").click();
  assert.equal(ui.find(ui.nodes.details, "button", "Post comment").disabled, false);
  assert.equal(ui.field(ui.nodes.details, "Comment").value, "Draft");

  storage.setItem(recoveredSubmissions, JSON.stringify([[scope, {kind: "comment", id: priority, submittedAt: now}]]));
  class NextDay extends Date { static now() { return now + 24 * 60 * 60 * 1000; } }
  const expired = await harness(api, undefined, {storage, clock: NextDay});
  await expired.load();
  await until(() => expired.field(expired.nodes.details, "Comment"));
  assert.equal(expired.find(expired.nodes.details, "button", "Post comment").disabled, false);
  assert.equal(expired.field(expired.nodes.details, "Comment").value, "Draft");
});

test("reloaded create inspection uses the original card ID and clears only the submitted text", async () => {
  const {api} = boardApi();
  const storage = tabStorage();
  const getCard = api.getCard;
  const reads = [];
  const writes = [];
  let failRead = true;
  api.createCard = async (input) => {
    writes.push(input);
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  api.getCard = async (id) => {
    if (id === card) return getCard(id);
    reads.push(id);
    if (failRead) throw new ApiError("transport_failed");
    const {data} = await getCard(card);
    return {data: {...data, id, title: "Edited by another client"}, etag: tag(id, 2)};
  };
  const original = await harness(api, undefined, {storage});
  await original.load();
  original.find(original.nodes.main, "button", "Add card").click();
  original.field(original.nodes.main, "New title").value = "Submitted card";
  original.field(original.nodes.main, "New description").value = "Submitted description";
  original.submit(original.nodes.main, "Create card");
  await original.settled();

  const reloaded = await harness(api, undefined, {storage});
  await reloaded.load();
  const {main} = reloaded.nodes;
  assert.equal(reloaded.field(main, "New title").value, "Submitted card");
  assert.equal(reloaded.field(main, "New description").value, "Submitted description");
  assert.equal(reloaded.find(main, "h2", "New card in Ready").hidden, false);
  assert.equal(reloaded.submit(main, "Create card").disabled, true);
  failRead = false;
  reloaded.find(main, "button", "Inspect saved board").click();
  await reloaded.settled();
  assert.deepEqual(reads, [writes[0].id, writes[0].id]);
  assert.equal(reloaded.field(main, "New title").value, "");
  assert.equal(reloaded.field(main, "New description").value, "");
  assert.equal(writes.length, 1);
});

function boardApi() {
  let revision = 1;
  let saved = {id: card, board_id: board, title: "Original", description: "Original description", ticket: 1,
    lane_id: lane, owner_id: user, priority_id: priority, revision: 1, archived: false, cover_attachment_id: null};
  let comments = [{id: backup, board_id: board, card_id: card, ticket: 1, author_id: user,
    text: "Original comment", created_at: createdAt}];
  let attachments = [{id: priority, board_id: board, card_id: card, original_name: "Old file", size: 4, added_by: user, created_at: "2026-11-10 00:00:00+00:00", active: true, previewable: false}];
  const lanes = [{id: lane, label: "Ready", key: "ready", retired: false, position: 0, role: "ordinary", revision: 1}];
  let patched;
  const api = {
    getSession: async () => ({data: {kind: "human", principal_id: user}}), listBoards: async () => page("boards", [{id: board, name: "Board"}]),
    getBoardAvailability: async () => ({data: {id: board, name: "Board", availability: "open", policy_id: backup, revision}, etag: tag(board, revision)}),
    getBoardLanes: async () => ({data: {board: {id: board, name: "Board", policy_id: backup, revision}, cards: saved ? [saved] : []}, etag: tag(board, revision)}),
    getPolicy: async () => ({data: {id: backup, board_id: board, resource_grants: [{user_id: user, capabilities: ["read", "create_card", "manage_relationships"]}],
      create_grants: [{user_id: user, lane_id: lane}], move_grants: []}}),
    getBoardIfChanged: async () => ({data: null}), listLanes: async () => page("lanes", lanes),
    listPriorities: async () => page("priorities", [{id: priority, label: "Normal"}]),
    listUsers: async () => page("users", [savedUser(user, "Human", "human")]),
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

test("Attention sidebar resize and collapse persist independently per tab", async () => {
  const {api} = boardApi();
  const storage = tabStorage();
  const pathname = "/boards/" + board;
  const ui = await harness(api, pathname, {storage});
  const {main, details, boards} = ui.nodes;
  const separator = ui.nodes["resize-board-sidebar"];
  const toggle = ui.nodes["toggle-board-sidebar"];
  const width = () => separator.attributes["aria-valuenow"];
  const pointer = (clientX) => ({clientX, pointerId: 1, isPrimary: true, button: 0});
  await ui.load();
  assert.equal(width(), "336");
  separator.dispatch("keydown", {key: "Home"});
  assert.equal(width(), "312");
  separator.dispatch("keydown", {key: "End"});
  assert.equal(width(), "1096");
  separator.dispatch("keydown", {key: "Home"});
  separator.dispatch("keydown", {key: "ArrowRight"});
  separator.dispatch("keydown", {key: "ArrowRight"});
  assert.equal(width(), "344");
  const row = boards.querySelector("a");
  const opener = ui.find(main, "button", "#1 Original");
  opener.click();
  await ui.settled();
  const title = ui.field(details, "Title");
  title.value = "Unsaved card title";

  assert.equal(separator.dispatch("pointerdown", pointer(400)), true);
  assert.equal(separator.hasPointerCapture(1), true);
  separator.dispatch("pointermove", pointer(480));
  assert.equal(width(), "424");
  separator.dispatch("pointercancel", pointer(480));
  assert.equal(width(), "344");
  separator.dispatch("pointerdown", pointer(400));
  separator.dispatch("pointermove", pointer(510));
  separator.dispatch("pointerup", pointer(520));
  assert.equal(width(), "464");
  assert.equal(separator.dispatch("keydown", {key: "ArrowRight"}), true);
  assert.equal(width(), "480");
  ui.resizeLayout(1000);
  assert.equal(width(), "312");
  separator.dispatch("keydown", {key: "ArrowRight"});
  ui.resizeLayout(1400);
  assert.equal(width(), "480");
  ui.resizeLayout(320, "column");
  assert.equal(width(), "288");
  ui.resizeLayout(1400);
  assert.equal(width(), "480");

  assert.equal(separator.dispatch("keydown", {key: "Enter"}), true);
  assert.equal(toggle.attributes["aria-expanded"], "false");
  assert.equal(boards.hidden, true);
  assert.equal(separator.hidden, true);
  assert.equal(ui.focused(), toggle);
  toggle.click();
  assert.equal(boards.hidden, false);
  assert.equal(separator.hidden, false);
  assert.equal(boards.querySelector("a"), row);
  assert.equal(ui.find(main, "button", "#1 Original"), opener);
  assert.equal(ui.field(details, "Title"), title);
  assert.equal(title.value, "Unsaved card title");
  toggle.click();

  const anotherTab = await harness(api, pathname);
  assert.equal(anotherTab.nodes["resize-board-sidebar"].attributes["aria-valuenow"], "336");
  assert.equal(anotherTab.nodes.boards.hidden, false);
  const reloaded = await harness(api, pathname, {storage});
  assert.equal(reloaded.nodes.boards.hidden, true);
  reloaded.nodes["toggle-board-sidebar"].click();
  assert.equal(reloaded.nodes.boards.hidden, false);
  assert.equal(reloaded.nodes["resize-board-sidebar"].attributes["aria-valuenow"], "480");
});

test("creation and keyboard moves use the current actor's directed workflow and original validators", async () => {
  const {api} = boardApi();
  const source = (await api.listLanes()).data.lanes[0];
  const target = {...source, id: backup, label: "Next", position: 1};
  const forbidden = {...source, id: priority, label: "Other actor", position: 2};
  const retired = {...source, id: user, label: "Retired", position: 3, retired: true};
  api.listLanes = async () => page("lanes", [source, target, forbidden, retired]);
  let actor = user;
  let policyBoard = board;
  let mayCreate = true;
  api.getSession = async () => ({data: {kind: "human", principal_id: actor}});
  api.getPolicy = async () => ({data: {
    id: backup, board_id: policyBoard,
    resource_grants: [{user_id: user, capabilities: mayCreate ? ["read", "create_card"] : ["read"]},
      {user_id: priority, capabilities: ["read", "create_card"]}],
    create_grants: [{user_id: user, lane_id: source.id}, {user_id: user, lane_id: retired.id},
      {user_id: priority, lane_id: forbidden.id}],
    move_grants: [
      {user_id: user, from_lane_id: source.id, to_lane_id: target.id},
      {user_id: user, from_lane_id: target.id, to_lane_id: forbidden.id},
      {user_id: user, from_lane_id: source.id, to_lane_id: retired.id},
      {user_id: priority, from_lane_id: source.id, to_lane_id: forbidden.id},
    ],
  }});
  const creates = [];
  api.createCard = async (input) => { creates.push(input); return createdCardReply(input); };
  const moves = [];
  const patch = api.patchCard;
  let refuse = true;
  api.patchCard = async (...args) => {
    moves.push(args);
    if (refuse) throw new ApiError("http_error", {status: 403});
    return patch(...args);
  };

  const ui = await harness(api);
  await ui.load();
  const {main, details, status} = ui.nodes;
  const addButtons = () => main.querySelectorAll("button").filter((button) => button.dataset.createLane);
  assert.deepEqual(addButtons().map(({dataset}) => dataset.createLane), [source.id]);
  ui.submit(main, "Create card");
  assert.equal(creates.length, 0);
  addButtons()[0].click();
  assert.equal(ui.focused(), ui.field(main, "New title"));
  assert.ok(ui.find(main, "h2", "New card in Ready"));
  ui.field(main, "New title").value = "  Exact title  ";
  ui.submit(main, "Create card");
  await ui.settled();
  assert.equal(creates.length, 1);
  assert.match(creates[0].id, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.deepEqual([creates[0].board_id, creates[0].lane_id, creates[0].expected_board_etag, creates[0].title],
    [board, source.id, tag(board, 1), "  Exact title  "]);

  ui.find(main, "button", "#1 Original").click();
  await ui.settled();
  const destination = ui.field(details, "Move to lane");
  assert.deepEqual(destination.options.map(({value}) => value), ["", target.id]);
  destination.value = forbidden.id;
  ui.submit(details, "Move card");
  assert.equal(moves.length, 0);
  destination.value = target.id;
  ui.submit(details, "Move card");
  await ui.settled();
  assert.equal(moves.length, 1);
  assert.deepEqual([moves[0][0], moves[0][1], moves[0][2].lane_id], [card, tag(card, 1), target.id]);
  assert.match(status.textContent, /refused the change/);
  assert.equal(destination.value, target.id);
  assert.match(details.textContent, /Lane: Ready/);

  refuse = false;
  ui.submit(details, "Move card");
  await ui.settled();
  assert.equal(moves.length, 2);
  assert.match(details.textContent, /Lane: Next/);
  assert.deepEqual(destination.options.map(({value}) => value), ["", forbidden.id]);

  const title = ui.field(main, "New title");
  title.value = "Draft across policy refresh";
  mayCreate = false;
  ui.nodes.refresh.click();
  await ui.settled();
  assert.deepEqual(addButtons(), []);
  assert.equal(title.value, "Draft across policy refresh");
  assert.equal(title.parent.parent.parent.hidden, true);
  actor = priority;
  ui.nodes.refresh.click();
  await ui.settled();
  assert.deepEqual(addButtons().map(({dataset}) => dataset.createLane), [forbidden.id]);
  assert.equal(destination.parent.parent.parent.hidden, true);
  policyBoard = priority;
  ui.nodes.refresh.click();
  await ui.settled();
  assert.deepEqual(addButtons(), []);
  assert.ok(ui.find(main, "button", "#1 Original"));
});

function dragTransfer() {
  const entries = new Map();
  return {
    get types() { return [...entries.keys()]; },
    setData(type, value) { entries.set(type, value); },
    getData(type) { return entries.get(type) ?? ""; },
  };
}

function grantMove(api, target) {
  const getPolicy = api.getPolicy;
  api.getPolicy = async (...args) => {
    const {data} = await getPolicy(...args);
    return {data: {...data, move_grants: [{user_id: user, from_lane_id: lane, to_lane_id: target}]}};
  };
}

test("M48 drag moves only the rendered card to a granted lane with its exact revision", async (t) => {
  const {api, target, commit} = changingBoard();
  const listLanes = api.listLanes;
  api.listLanes = async () => page("lanes", [...(await listLanes()).data.lanes,
    {...target, id: priority, label: "Forbidden", position: 2}]);
  grantMove(api, target.id);
  const writes = [];
  api.patchCard = async (id, expected, patch) => {
    writes.push([id, expected, patch.lane_id]);
    commit("Original", patch.lane_id);
    return api.getCard(id);
  };
  const ui = await harness(api);
  t.after(() => ui.navigate("/"));
  await ui.load();
  const {main, details, status} = ui.nodes;
  const column = (label) => ui.find(main, "h2", label).parent;
  const opener = ui.find(main, "button", "#1 Original");
  assert.equal(opener.draggable, true);
  const next = column(target.label);
  const start = () => {
    const transfer = dragTransfer();
    opener.dispatch("dragstart", {dataTransfer: transfer});
    assert.equal(transfer.effectAllowed, "move");
    return transfer;
  };
  const clear = () => {
    assert.equal(opener.dataset.dragging, undefined);
    assert.equal(next.dataset.dropTarget, undefined);
  };

  let transfer = start();
  assert.equal(next.dispatch("dragover", {dataTransfer: transfer}), true);
  assert.equal(next.dataset.dropTarget, "true");
  assert.equal(transfer.dropEffect, "move");
  next.dispatch("dragleave", {relatedTarget: next.children[0]});
  assert.equal(next.dataset.dropTarget, "true");
  next.dispatch("dragleave", {relatedTarget: column("Forbidden")});
  assert.equal(next.dataset.dropTarget, undefined);
  opener.dispatch("dragend");
  clear();
  next.dispatch("drop", {dataTransfer: transfer}); // A copied payload has no live drag.
  assert.equal(writes.length, 0);

  transfer = start();
  assert.equal(column("Forbidden").dispatch("dragover", {dataTransfer: transfer}), false);
  column("Forbidden").dispatch("drop", {dataTransfer: transfer});
  clear();
  assert.equal(writes.length, 0);

  for (const substitute of [
    () => "{",
    (payload) => JSON.stringify({...JSON.parse(payload), board_id: user}),
    (payload) => JSON.stringify({...JSON.parse(payload), card_id: user}),
    (payload) => JSON.stringify({...JSON.parse(payload), revision: 2}),
  ]) {
    transfer = start();
    const [type] = transfer.types;
    transfer.setData(type, substitute(transfer.getData(type)));
    next.dispatch("drop", {dataTransfer: transfer});
    clear();
  }
  assert.equal(writes.length, 0);

  // A concurrent edit after paint must not upgrade the original drag's validator.
  commit("Changed elsewhere", lane);
  next.dispatch("drop", {dataTransfer: start()});
  clear();
  await ui.settled();
  assert.equal(writes.length, 0);

  // A filter repaint invalidates the previous drag even if its card still matches.
  const priorOpener = ui.find(main, "button", "#1 Changed elsewhere");
  const priorTarget = column(target.label);
  transfer = dragTransfer();
  priorOpener.dispatch("dragstart", {dataTransfer: transfer});
  assert.equal(priorTarget.dispatch("dragover", {dataTransfer: transfer}), true);
  const filter = ui.field(main, "Filter by title or ticket");
  filter.value = "#1";
  filter.dispatch("input");
  assert.equal(priorOpener.dataset.dragging, undefined);
  assert.equal(priorTarget.dataset.dropTarget, undefined);
  priorTarget.dispatch("drop", {dataTransfer: transfer});
  assert.equal(writes.length, 0);

  // A repaint during the focused read must also cancel the move.
  const readCard = api.getCard;
  const releaseRead = Promise.withResolvers();
  const preflights = [];
  api.getCard = async (id) => {
    preflights.push(id);
    await releaseRead.promise;
    return readCard(id);
  };
  const pendingOpener = ui.find(main, "button", "#1 Changed elsewhere");
  const pendingTarget = column(target.label);
  transfer = dragTransfer();
  pendingOpener.dispatch("dragstart", {dataTransfer: transfer});
  pendingTarget.dispatch("drop", {dataTransfer: transfer});
  try {
    await until(() => preflights.length === 1);
    filter.value = "1";
    filter.dispatch("input");
    assert.equal(pendingOpener.isConnected, false);
  } finally {
    releaseRead.resolve();
    await ui.settled();
    api.getCard = readCard;
  }
  assert.deepEqual(preflights, [card]);
  assert.equal(writes.length, 0);
  assert.match(status.textContent, /The board view changed\. Drag the card again/);

  const fresh = ui.find(main, "button", "#1 Changed elsewhere");
  fresh.click();
  await until(() => ui.field(details, "Comment"));
  const draft = ui.field(details, "Comment");
  draft.value = "Keep my draft";
  transfer = dragTransfer();
  fresh.dispatch("dragstart", {dataTransfer: transfer});
  column(target.label).dispatch("drop", {dataTransfer: transfer});
  await ui.settled();
  assert.deepEqual(writes, [[card, tag(card, 2), target.id]]);
  assert.equal(ui.find(main, "button", "#1 Original").parent, column(target.label));
  assert.equal(draft.value, "Keep my draft");
  assert.match(details.textContent, /Lane: Next lane/);
});

test("M48 refuses or inspects the original drop once while retaining the open draft", async (t) => {
  const {api, target, commit} = changingBoard();
  grantMove(api, target.id);
  let refuse = true;
  let failReadback = false;
  const reads = [];
  const writes = [];
  const refused = Promise.withResolvers();
  const readCard = api.getCard;
  api.getCard = async (id) => {
    reads.push(id);
    if (failReadback) throw new ApiError("transport_failed");
    return readCard(id);
  };
  api.patchCard = async (id, expected, patch) => {
    writes.push([id, expected, patch.lane_id]);
    if (refuse) {
      await refused.promise;
      throw new ApiError("http_error", {status: 403});
    }
    commit("Original", patch.lane_id);
    failReadback = true;
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  const ui = await harness(api);
  t.after(() => ui.navigate("/"));
  await ui.load();
  const {main, details, status} = ui.nodes;
  const opener = ui.find(main, "button", "#1 Original");
  opener.click();
  await until(() => ui.field(details, "Title"));
  const title = ui.field(details, "Title");
  title.value = "Unsaved title";
  const drop = () => {
    const opener = ui.find(main, "button", "#1 Original");
    const next = ui.find(main, "h2", target.label).parent;
    const dataTransfer = dragTransfer();
    opener.dispatch("dragstart", {dataTransfer});
    next.dispatch("dragover", {dataTransfer});
    next.dispatch("drop", {dataTransfer});
    assert.equal(opener.dataset.dragging, undefined);
    assert.equal(next.dataset.dropTarget, undefined);
  };

  drop();
  await until(() => writes.length === 1);
  const pending = ui.find(main, "button", "#1 Original");
  assert.equal(pending.dataset.pending, "true");
  assert.equal(pending.parent, ui.find(main, "h2", target.label).parent);
  assert.match(details.textContent, /Moving to Next lane/);
  refused.resolve();
  await ui.settled();
  assert.match(status.textContent, /refused the change/);
  const restored = ui.find(main, "button", "#1 Original");
  assert.equal(restored.dataset.pending, undefined);
  assert.equal(restored.parent, ui.find(main, "h2", "Ready").parent);
  assert.equal(title.value, "Unsaved title");
  assert.match(details.textContent, /Lane: Ready/);

  refuse = false;
  drop();
  await ui.settled();
  assert.match(status.textContent, /Readback failed/);
  assert.equal(title.value, "Unsaved title");
  assert.equal(details.hidden, false);
  drop();
  assert.equal(writes.length, 2);
  failReadback = false;
  ui.find(details, "button", "Inspect saved card").click();
  await ui.settled();
  assert.deepEqual(writes, [[card, tag(card, 1), target.id], [card, tag(card, 1), target.id]]);
  assert.ok(reads.length >= 5 && reads.every((id) => id === card));
  assert.match(details.textContent, /Lane: Next lane/);
  assert.equal(title.value, "Unsaved title");
});

test("lane totals and card counts render authoritative comment and detach readbacks", async () => {
  const {api} = boardApi();
  const original = (await api.getCard(card)).data;
  const ready = (await api.listLanes()).data.lanes[0];
  const comments = (await api.listComments()).data.comments;
  let attachment = {...(await api.listAttachments()).data.attachments[0], revision: 1};
  let revision = 1;
  const writes = [];
  const current = () => ({...original, revision, comment_count: comments.length,
    attachment_count: attachment.active ? 1 : 0});
  api.getBoardLanes = async () => ({
    data: {board: {id: board, name: "Board"}, cards: [
      current(),
      {...original, id: backup, ticket: 2, title: "No activity", comment_count: 0, attachment_count: 0},
      {...original, id: priority, ticket: 3, title: "Files only", comment_count: 0, attachment_count: 123},
    ]}, etag: tag(board, revision),
  });
  api.getCard = async () => ({data: current(), etag: tag(card, revision)});
  api.listLanes = async () => page("lanes", [ready, {...ready, id: backup, key: "empty", label: "Empty", position: 1}]);
  api.listAttachments = async () => page("attachments", attachment.active ? [attachment] : []);
  api.getAttachment = async () => ({data: attachment, etag: tag(attachment.id, attachment.revision)});
  api.createComment = async (id, etag, text, commentId) => {
    writes.push(["comment", id, etag, text]);
    comments.push({...comments[0], id: commentId, text});
    revision++;
  };
  api.detachAttachment = async (id, expected, etag) => {
    writes.push(["detach", id, expected, etag]);
    attachment = {...attachment, active: false, revision: ++revision};
  };

  const ui = await harness(api);
  await ui.load();
  const {main, details} = ui.nodes;
  const face = (text = "#1 Original") => ui.find(main, "button", text);
  const counts = (node) => node.children.find(({className}) => className === "card-counts");
  const badges = (node) => counts(node)?.children.map(({className}) => className) ?? [];
  const total = (lane) => ui.find(main, "h2", lane).children.find(({className}) => className === "lane-total").textContent;

  assert.equal(total("Ready"), "3 cards");
  assert.equal(total("Empty"), "0 cards");
  assert.equal(face().attributes["aria-description"], "Priority: Normal, 1 comment, 1 attachment");
  assert.equal(counts(face()).attributes["aria-hidden"], "true");
  assert.deepEqual(badges(face()), ["card-comment-count", "card-attachment-count"]);
  assert.deepEqual(badges(face("#2 No activity")), []);
  const files = face("#3 Files only");
  assert.equal(files.attributes["aria-description"], "Priority: Normal, 123 attachments");
  assert.match(counts(files).textContent, /123$/);
  assert.deepEqual(badges(files), ["card-attachment-count"]);

  face().click();
  await ui.settled();
  ui.field(details, "Comment").value = "Added comment";
  ui.submit(details, "Post comment");
  await ui.settled();
  assert.equal(face().attributes["aria-description"], "Priority: Normal, 2 comments, 1 attachment");

  ui.submit(details, "Detach");
  await ui.settled();
  assert.equal(face().attributes["aria-description"], "Priority: Normal, 2 comments");
  assert.deepEqual(badges(face()), ["card-comment-count"]);
  assert.equal(total("Ready"), "3 cards");
  assert.deepEqual(writes, [
    ["comment", card, tag(card, 1), "Added comment"],
    ["detach", priority, 1, tag(board, 2)],
  ]);
});

test("inline filter matches literal titles and tickets while keeping drawer drafts and guarded lane actions", async () => {
  const {api} = boardApi();
  const original = {...(await api.getCard()).data, title: "Review [Café]"};
  const second = {...original, id: backup, ticket: 12, title: "Review code"};
  const ready = {...(await api.listLanes()).data.lanes[0], role: "ready_for_review",
    approval_enabled: true, reorder_enabled: true};
  api.listLanes = async () => page("lanes", [ready, {...ready, id: priority, label: "Completed",
    role: "completed", position: 1, approval_enabled: false, reorder_enabled: false}]);
  const getLanes = api.getBoardLanes;
  api.getBoardLanes = async () => {
    const reply = await getLanes();
    return {...reply, data: {...reply.data, cards: [original, second]}};
  };
  api.getCard = async () => ({data: original, etag: tag(card, 1)});
  const previews = [];
  const writes = [];
  const current = [{card_id: card, ticket: 1}, {card_id: backup, ticket: 12}];
  api.previewTicketOrder = async (id, laneId, ids) => {
    previews.push([id, laneId, [...ids]]);
    return {data: {board_revision: 1, current, changed: true,
      proposed: [{card_id: backup, ticket: 1}, {card_id: card, ticket: 12}]}, etag: tag(board, 1)};
  };
  api.applyTicketOrder = async (...args) => writes.push(args);
  api.approveLane = async (...args) => writes.push(args);

  const ui = await harness(api);
  await ui.load();
  const {main, details, refresh} = ui.nodes;
  const filter = ui.field(main, "Filter by title or ticket");
  const find = (label) => ui.find(main, "button", label);
  const total = (label) => ui.find(main, "h2", label).children[0].textContent;
  const count = () => main.querySelectorAll("span").find(({attributes}) => attributes.role === "status").textContent;
  const setFilter = (value) => { filter.value = value; filter.dispatch("input"); };
  const previewInput = () => main.querySelectorAll("input").find(({dataset}) => dataset.previewLane === lane);
  const approve = find("Approve all in lane");
  assert.equal(approve.attributes["aria-label"], "Approve all in lane Ready");
  assert.equal(find("Add card").attributes["aria-label"], "Add card in Ready");
  assert.equal(find("Preview ticket order").attributes["aria-label"], "Preview ticket order in Ready");
  previewInput().value = "1, 12";
  find("Preview ticket order").click();
  assert.equal(find("Preview ticket order").attributes["aria-disabled"], "true");
  await ui.settled();
  const apply = find("Apply ticket order");
  assert.ok(apply);
  assert.equal(apply.attributes["aria-label"], "Apply ticket order in Ready");
  assert.equal(find("Preview ticket order").attributes["aria-disabled"], undefined);
  assert.ok(approve);
  assert.equal(count(), "2 cards");

  assert.equal(ui.keydown(refresh, "/", {ctrlKey: true}), false);
  assert.equal(ui.keydown(refresh, "/", {isComposing: true}), false);
  assert.equal(ui.keydown(refresh, "/"), true);
  assert.equal(ui.focused(), filter);
  setFilter(" [CAFÉ] ");
  assert.equal(count(), "1 of 2 cards match");
  assert.equal(total("Ready"), "1 of 2 cards");
  assert.equal(total("Completed"), "0 of 0 cards");
  assert.equal(find("#12 Review code"), undefined);
  assert.equal(find("Approve all in lane"), undefined);
  assert.equal(find("Preview ticket order").disabled, true);
  find("Preview ticket order").click();
  approve.parent.dispatch("submit");
  apply.parent.dispatch("submit");
  await ui.settled();
  assert.deepEqual(writes, []);
  assert.deepEqual(previews, [[board, lane, [card, backup]]]);

  find("#1 Review [Café]").click();
  await ui.settled();
  const comment = ui.field(details, "Comment");
  comment.value = "Draft / path";
  comment.focus();
  assert.equal(ui.keydown(comment, "/"), false);
  assert.equal(ui.focused(), comment);
  assert.equal(ui.keydown(details.querySelector("h2"), "/"), true);
  assert.equal(filter.selectedText, " [CAFÉ] ");
  assert.equal(ui.keydown(filter, "Escape", {isComposing: true}), false);
  assert.equal(filter.value, " [CAFÉ] ");
  assert.equal(ui.keydown(filter, "Escape"), true);
  assert.equal(filter.value, "");
  assert.equal(previewInput().value, "1, 12");
  assert.equal(find("Preview ticket order").disabled, false);
  assert.ok(find("Approve all in lane"));
  assert.equal(find("Apply ticket order"), undefined);
  assert.equal(details.hidden, false);
  assert.equal(comment.value, "Draft / path");

  setFilter("#12");
  assert.ok(find("#12 Review code"));
  assert.equal(find("#1 Review [Café]"), undefined);
  setFilter("Original description");
  assert.equal(count(), "0 of 2 cards match");
  setFilter(".*");
  assert.equal(count(), "0 of 2 cards match");
  ui.find(details, "button", "Close card").click();
  assert.equal(ui.focused(), filter);
  assert.equal(ui.keydown(filter, "Escape"), true);
  find("#1 Review [Café]").click();
  await ui.settled();
  assert.equal(ui.field(details, "Comment").value, "Draft / path");
  setFilter("Review");
  assert.equal(total("Ready"), "2 of 2 cards");
  assert.ok(find("Approve all in lane"));
  assert.ok(ui.find(main, "summary", "Find cards"));
});

test("title composition defers saves and drawer close follows the current visible card", async () => {
  const {api} = boardApi();
  const writes = [];
  const patch = api.patchCard;
  api.patchCard = async (id, revision, draft) => {
    writes.push([id, revision, {...draft}]);
    return patch(id, revision, draft);
  };
  const ui = await harness(api);
  await ui.load();
  const {main, details} = ui.nodes;
  const filter = ui.field(main, "Filter by title or ticket");
  const source = ui.find(main, "button", "#1 Original");
  source.focus();
  source.click();
  await ui.settled();
  assert.equal(ui.focused(), details.querySelector("h2"));
  const title = ui.field(details, "Title");
  title.focus();
  title.dispatch("compositionstart");
  title.value = "Candidate";
  ui.submit(details, "Save text");
  assert.deepEqual(writes, []);
  assert.equal(ui.keydown(title, "Enter", {isComposing: true}), false);
  title.dispatch("compositionend");
  title.value = "確定";
  ui.submit(details, "Save text");
  assert.deepEqual(writes, []);
  ui.keydown(title, "Enter");
  ui.submit(details, "Save text");
  await ui.settled();
  assert.deepEqual(writes, [[card, tag(card, 1), {title: "確定"}]]);
  assert.equal(ui.focused(), title);
  ui.find(details, "button", "Close card").click();
  const replacement = ui.find(main, "button", "#1 確定");
  assert.notEqual(replacement, source);
  assert.equal(ui.focused(), replacement);
  replacement.click();
  await ui.settled();
  filter.value = "Hidden";
  filter.dispatch("input");
  ui.find(details, "button", "Close card").click();
  assert.equal(ui.focused(), filter);
});

test("configured action colors and lane-action focus follow the current board paint", async () => {
  const {api} = boardApi();
  const getLanes = api.getBoardLanes;
  let accent = "#777777";
  api.getBoardLanes = async () => {
    const reply = await getLanes();
    reply.data.board.actor_presentation = {human: {accent}};
    return reply;
  };
  const ui = await harness(api);
  await ui.load();
  const {main, details, refresh} = ui.nodes;
  const columns = main.querySelectorAll("div").find(({className}) => className === "lanes");
  for (const region of [columns, details]) {
    assert.equal(region.style["--action-fill"], "#777777");
    assert.equal(region.style["--action-ink"], "#000000");
  }
  const original = ui.find(main, "button", "Add card");
  assert.equal(original.className, "board-action");
  original.focus();
  accent = "#767676";
  refresh.click();
  await ui.settled();
  for (const region of [columns, details]) {
    assert.equal(region.style["--action-fill"], "#767676");
    assert.equal(region.style["--action-ink"], "#ffffff");
  }
  const replacement = ui.find(main, "button", "Add card");
  assert.notEqual(replacement, original);
  assert.equal(ui.focused(), replacement);
  assert.equal(replacement.focusOptions?.preventScroll, true);
});

test("single-card approval preserves unrelated focus and restores lost submit focus", async (t) => {
  for (const elsewhere of [true, false]) {
    await t.test(elsewhere ? "filter focused" : "submit focus lost to body", async () => {
      const {api} = boardApi();
      const original = (await api.getCard()).data;
      const second = {...original, id: backup, ticket: 2, title: "Second card"};
      const ready = {...(await api.listLanes()).data.lanes[0], role: "ready_for_review",
        approval_enabled: true, reorder_enabled: true};
      const completed = {...ready, id: priority, role: "completed", label: "Completed", position: 1,
        approval_enabled: false, reorder_enabled: false};
      api.listLanes = async () => page("lanes", [ready, completed]);
      const approval = Promise.withResolvers();
      let saved = original;
      api.approveCard = async () => {
        await approval.promise;
        saved = {...original, lane_id: completed.id, revision: 2};
      };
      api.getCard = async () => ({data: saved, etag: tag(card, saved.revision)});
      const getLanes = api.getBoardLanes;
      api.getBoardLanes = async () => {
        const reply = await getLanes();
        return {data: {board: {...reply.data.board, revision: saved.revision}, cards: [saved, second]},
          etag: tag(board, saved.revision)};
      };
      api.previewTicketOrder = async () => ({
        data: {board_revision: 1, changed: true, current: [{card_id: card, ticket: 1}, {card_id: backup, ticket: 2}],
          proposed: [{card_id: backup, ticket: 1}, {card_id: card, ticket: 2}]}, etag: tag(board, 1),
      });
      const writes = [];
      api.applyTicketOrder = async () => writes.push("apply");
      api.approveLane = async () => writes.push("approve");
      const ui = await harness(api);
      await ui.load();
      const {main, details} = ui.nodes;
      const find = (label) => ui.find(main, "button", label);
      const filter = ui.field(main, "Filter by title or ticket");
      const opener = find("#1 Original");
      opener.click();
      await ui.settled();
      ui.find(details, "button", "Reviewed & approved").focus();
      ui.submit(details, "Reviewed & approved");

      assert.equal(find("Add card").attributes["aria-disabled"], "true");
      assert.equal(find("Approve all in lane").attributes["aria-disabled"], "true");
      assert.equal(find("Preview ticket order").attributes["aria-disabled"], undefined);
      find("Preview ticket order").click();
      await until(() => find("Apply ticket order"));
      assert.equal(find("Apply ticket order").attributes["aria-disabled"], "true");
      ui.submit(main, "Apply ticket order");
      ui.submit(main, "Approve all in lane");
      assert.deepEqual(writes, []);

      if (elsewhere) filter.focus();
      else ui.focusBody();
      approval.resolve();
      await ui.settled();
      assert.equal(details.hidden, true);
      const replacement = find("#1 Original");
      assert.notEqual(replacement, opener);
      assert.equal(ui.focused(), elsewhere ? filter : replacement);
      assert.equal(find("Add card").attributes["aria-disabled"], undefined);
      assert.equal(find("Approve all in lane").attributes["aria-disabled"], undefined);
      assert.equal(find("Preview ticket order").disabled, true);
    });
  }
});

test("pending approval inspection still blocks recreated lane forms and a late ticket-order Apply", async () => {
  const {api} = boardApi();
  const original = (await api.getCard()).data;
  const second = {...original, id: backup, ticket: 2, title: "Second card"};
  const ready = {...(await api.listLanes()).data.lanes[0], role: "ready_for_review",
    approval_enabled: true, reorder_enabled: true};
  api.listLanes = async () => page("lanes", [ready, {...ready, id: priority, label: "Completed",
    role: "completed", position: 1, approval_enabled: false, reorder_enabled: false}]);
  const getLanes = api.getBoardLanes;
  api.getBoardLanes = async () => {
    const reply = await getLanes();
    return {...reply, data: {...reply.data, cards: [original, second]}};
  };
  const preview = Promise.withResolvers();
  api.previewTicketOrder = () => preview.promise;
  const applies = [];
  const approvals = [];
  const inspections = [];
  let available = false;
  api.applyTicketOrder = async (...args) => applies.push(args);
  api.approveLane = async (id) => {
    approvals.push(id);
    throw new ApiError("network_error", {outcomeUnknown: true});
  };
  api.inspectLaneApproval = async (id) => {
    inspections.push(id);
    throw new ApiError("http_error", {status: available ? 404 : 503});
  };

  const ui = await harness(api);
  await ui.load();
  const {main, status} = ui.nodes;
  const find = (label) => ui.find(main, "button", label);
  find("Preview ticket order").click();
  ui.submit(main, "Approve all in lane");
  await ui.settled();
  assert.match(status.textContent, /Readback failed/);
  assert.equal(approvals.length, 1);
  assert.deepEqual(inspections, approvals);

  preview.resolve({data: {board_revision: 1, changed: true,
    current: [{card_id: card, ticket: 1}, {card_id: backup, ticket: 2}],
    proposed: [{card_id: backup, ticket: 1}, {card_id: card, ticket: 2}]}, etag: tag(board, 1)});
  await until(() => find("Apply ticket order"));
  assert.equal(find("Apply ticket order").disabled, true);
  find("Apply ticket order").parent.dispatch("submit");
  assert.deepEqual(applies, []);

  const filter = ui.field(main, "Filter by title or ticket");
  filter.value = "#2";
  filter.dispatch("input");
  assert.equal(find("Approve all in lane"), undefined);
  ui.keydown(filter, "Escape");
  assert.equal(find("Approve all in lane").disabled, true);
  find("Approve all in lane").parent.dispatch("submit");
  assert.equal(approvals.length, 1);

  available = true;
  find("Inspect saved board").click();
  await ui.settled();
  assert.deepEqual(inspections, [approvals[0], approvals[0]]);
  assert.equal(find("Approve all in lane").disabled, false);
});

test("closing a filtered-out Find result focuses the inline filter if Find collapsed", async () => {
  const {api} = boardApi();
  api.listCards = async () => page("cards", [(await api.getCard()).data]);
  const ui = await harness(api);
  await ui.load();
  const {main, details} = ui.nodes;
  const filter = ui.field(main, "Filter by title or ticket");
  filter.value = "Other";
  filter.dispatch("input");
  const find = ui.find(main, "summary", "Find cards").parent;
  find.open = true;
  ui.submit(find, "Find cards");
  await ui.settled();
  const result = ui.find(find, "button", "#1 Original");
  result.click();
  await ui.settled();
  ui.find(details, "button", "Close card").click();
  assert.equal(ui.focused(), result);

  result.click();
  await ui.settled();
  find.open = false;
  assert.equal(result.isConnected, true);
  ui.find(details, "button", "Close card").click();
  assert.equal(ui.focused() === filter, true);
});

test("Completed shows recent and unknown ages, reveals older cards in board order, and honors ticket links", async () => {
  class Clock extends Date {
    static now() { return Date.parse("2026-11-11T12:00:00Z"); }
  }
  const {api} = boardApi();
  const {data: original} = await api.getCard();
  const {data: {lanes: [ordinary]}} = await api.listLanes();
  const completedLane = "c431860a-123f-422e-975f-dc6ffcb21727";
  const malformedId = "70992244-8910-4b56-aa27-c1e4dba5af43";
  const ordinaryId = "59741c0b-7498-4d34-ad17-60fc9e04245e";
  const completed = (id, ticket, title, completed_at) => ({...original,
    id, ticket, title, completed_at, lane_id: completedLane, updated_at: "2026-11-11 12:00:00+00:00"});
  const cards = [
    {...completed(backup, 2, "Older", "2026-11-10 11:59:59+00:00"), attachment_count: 1},
    completed(card, 1, "Recent", "2026-11-11 11:59:59+00:00"),
    completed(priority, 3, "At the cutoff", "2026-11-10 12:00:00+00:00"),
    completed(user, 4, "Unknown", null),
    completed(malformedId, 5, "Invalid date", "2026-02-30 11:59:59+00:00"),
    {...completed(ordinaryId, 6, "Other lane", "2026-11-01 11:59:59+00:00"), lane_id: lane},
  ];
  const read = api.getBoardLanes;
  api.getBoardLanes = async () => {
    const reply = await read();
    return {...reply, data: {...reply.data, cards}};
  };
  api.listLanes = async () => page("lanes", [
    {...ordinary, label: "Completed"},
    {...ordinary, id: completedLane, label: "Delivered", position: 1, role: "completed", reorder_enabled: true},
  ]);
  api.getCard = async (id) => ({data: cards.find((card) => card.id === id), etag: tag(id, 1)});
  api.listCards = async (_, {ticket}) => page("cards", cards.filter((card) => card.ticket === ticket));
  const previewReads = [];
  api.listAttachments = async (id, _cursor, signal) => {
    if (signal) previewReads.push(id);
    return page("attachments", []);
  };

  const ui = await harness(api, "/boards/" + board, {clock: Clock});
  await ui.load();
  const {main, details} = ui.nodes;
  const laneHeading = ui.find(main, "h2", "Delivered");
  assert.equal(laneHeading.children[0].textContent, "5 cards");
  const faces = () => laneHeading.parent.querySelectorAll("button")
    .filter(({dataset}) => dataset.cardId).map(({ownText}) => ownText);
  const preview = ui.find(laneHeading.parent, "button", "Preview ticket order");
  assert.equal(preview.disabled, true);
  assert.deepEqual(faces(), ["#1 Recent", "#3 At the cutoff", "#4 Unknown", "#5 Invalid date"]);
  assert.deepEqual(previewReads, []);
  assert.ok(ui.find(main, "button", "#6 Other lane"));
  const add = ui.find(main, "button", "Add card");

  ui.find(main, "button", "Show 1 older card").click();
  await until(() => previewReads.length > 0);
  assert.deepEqual(previewReads, [backup]);
  assert.equal(preview.disabled, false);
  assert.deepEqual(faces(), ["#2 Older", "#1 Recent", "#3 At the cutoff", "#4 Unknown", "#5 Invalid date"]);
  assert.equal(ui.find(main, "button", "Show 1 older card"), undefined);
  assert.equal(ui.find(main, "button", "Add card"), add);
  const older = ui.find(main, "button", "#2 Older");
  assert.equal(ui.focused(), older);
  older.click();
  await ui.settled();
  assert.equal(ui.field(details, "Title").value, "Older");
  ui.find(details, "button", "Close card").click();

  await ui.popstate("/boards/" + board + "?ticket=2");
  assert.ok(ui.find(main, "button", "Show 1 older card"));
  assert.equal(ui.find(main, "button", "#2 Older"), undefined);
  assert.equal(ui.field(details, "Title").value, "Older");

  const filter = ui.field(main, "Filter by title or ticket");
  filter.value = "Older";
  filter.dispatch("input");
  const filtered = ui.find(main, "h2", "Delivered");
  assert.equal(filtered.children[0].textContent, "1 of 5 cards");
  ui.find(main, "button", "Show 1 older card").click();
  assert.equal(ui.find(filtered.parent, "button", "Preview ticket order").disabled, true);
  assert.ok(ui.find(filtered.parent, "button", "#2 Older"));
  assert.equal(ui.find(filtered.parent, "button", "#1 Recent"), undefined);
  assert.equal(ui.field(details, "Title").value, "Older");
});

test("Back, Forward and reload restore each board's own search, drawer and lane scroll in one tab", async () => {
  const {api} = boardApi();
  const other = "c431860a-123f-422e-975f-dc6ffcb21727";
  const otherLane = "70992244-8910-4b56-aa27-c1e4dba5af43";
  const url = "/boards/" + board;
  const otherUrl = "/boards/" + other;
  const names = [{id: board, name: "First board"}, {id: other, name: "Second board"}];
  api.listBoards = async () => page("boards", names);
  const readAvailability = api.getBoardAvailability;
  api.getBoardAvailability = async (id) => {
    const first = await readAvailability();
    return id === board ? first : {data: {...first.data, ...names[1]}, etag: tag(id, 1)};
  };
  const readContents = api.getBoardLanes;
  api.getBoardLanes = async (id) => id === board ? readContents()
    : {data: {board: names[1], cards: []}, etag: tag(id, 1)};
  const readLanes = api.listLanes;
  api.listLanes = async (id) => id === board ? readLanes()
    : page("lanes", [{id: otherLane, label: "Ready", key: "ready", retired: false, position: 0, role: "ordinary", revision: 1}]);
  const searches = [];
  api.listCards = async (id, fields) => {
    searches.push({id, ...fields});
    return page("cards", id === board ? [(await api.getCard()).data] : []);
  };
  const storage = tabStorage();
  const ui = await harness(api, url, {storage});
  const searchPanel = (ui) => ui.find(ui.nodes.main, "summary", "Find cards").parent;
  const columns = (ui) => ui.nodes.main.querySelectorAll("div").find((node) => node.className === "lanes");
  const query = (ui) => ui.field(searchPanel(ui), "Title or ticket");
  const filter = (ui) => ui.field(ui.nodes.main, "Filter by title or ticket");
  function setFilter(ui, value) {
    filter(ui).value = value;
    filter(ui).dispatch("input");
  }
  await ui.load();
  searchPanel(ui).open = true;
  query(ui).value = "Original";
  ui.field(searchPanel(ui), "Search lane").value = lane;
  ui.field(searchPanel(ui), "Search descriptions and comments").checked = true;
  ui.submit(searchPanel(ui), "Find cards");
  await ui.settled();
  query(ui).value = "Unsubmitted text";
  ui.find(ui.nodes.main, "button", "#1 Original").click();
  await ui.settled();
  setFilter(ui, "#1");
  columns(ui).scrollLeft = 88;
  columns(ui).children[0].scrollTop = 320;
  await ui.navigate(otherUrl);
  assert.equal(ui.nodes.details.hidden, true);
  assert.equal(query(ui).value, "");
  assert.equal(filter(ui).value, "");
  assert.equal(columns(ui).children[0].scrollTop, 0);
  query(ui).value = "Second query";
  setFilter(ui, "Second filter");
  ui.field(searchPanel(ui), "Search lane").value = otherLane;
  columns(ui).children[0].scrollTop = 130;

  await ui.popstate(url);
  assert.equal(searchPanel(ui).open, true);
  assert.equal(query(ui).value, "Unsubmitted text");
  assert.equal(filter(ui).value, "#1");
  assert.equal(ui.field(searchPanel(ui), "Search lane").value, lane);
  assert.equal(columns(ui).scrollLeft, 88);
  assert.equal(columns(ui).children[0].scrollTop, 320);
  assert.equal(ui.nodes.details.querySelector("h2").textContent, "#1 Original");
  assert.ok(ui.find(searchPanel(ui), "button", "#1 Original"));
  assert.equal(ui.focused()?.isConnected, false);
  assert.deepEqual(searches.map(({id, q}) => [id, q]), [[board, "Original"], [board, "Original"]]);
  await ui.popstate(otherUrl);
  assert.equal(query(ui).value, "Second query");
  assert.equal(filter(ui).value, "Second filter");
  assert.equal(ui.field(searchPanel(ui), "Search lane").value, otherLane);
  assert.equal(columns(ui).children[0].scrollTop, 130);
  assert.equal(ui.nodes.details.hidden, true);
  await ui.popstate(url);
  columns(ui).children[0].scrollTop = 275;
  ui.pagehide();

  const reloaded = await harness(api, url, {storage});
  await reloaded.load();
  await reloaded.settled();
  assert.equal(query(reloaded).value, "Unsubmitted text");
  assert.equal(filter(reloaded).value, "#1");
  assert.equal(columns(reloaded).children[0].scrollTop, 275);
  assert.equal(reloaded.nodes.details.querySelector("h2").textContent, "#1 Original");
  reloaded.find(reloaded.nodes.details, "button", "Close card").click();
  const closed = await harness(api, url, {storage});
  await closed.load();
  assert.equal(closed.nodes.details.hidden, true);

  const separate = await harness(api, url);
  await separate.load();
  assert.equal(query(separate).value, "");
  assert.equal(filter(separate).value, "");
  assert.equal(columns(separate).children[0].scrollTop, 0);
  assert.equal(separate.nodes.details.hidden, true);
});

test("missing or foreign saved cards clear selection, and a late refusal leaves the current drawer intact", async () => {
  const {api} = boardApi();
  const url = "/boards/" + board;
  const key = "localswim:board-view:" + board;
  const storage = tabStorage();
  storage.setItem(key, JSON.stringify({card: {id: card, ticket: null}}));
  const readCard = api.getCard;
  const {promise, resolve} = Promise.withResolvers();
  const {promise: started, resolve: start} = Promise.withResolvers();
  api.getCard = async () => {
    api.getCard = readCard;
    start();
    await promise;
    throw new ApiError("http_error", {status: 404});
  };
  const ui = await harness(api, url, {storage});
  await ui.load();
  await started;
  await ui.popstate("/");
  await ui.popstate(url);
  resolve();
  await ui.settled();
  assert.equal(ui.nodes.details.querySelector("h2").textContent, "#1 Original");
  assert.equal(JSON.parse(storage.getItem(key)).card.id, card);

  api.getCard = async () => { throw new ApiError("transport_failed"); };
  await ui.load();
  await ui.settled();
  assert.match(ui.nodes.details.textContent, /Use Refresh/);
  assert.equal(JSON.parse(storage.getItem(key)).card.id, card);
  const retried = [];
  const readBoard = api.getBoardLanes;
  let boardReads = 0;
  api.getBoardLanes = (...args) => { boardReads++; return readBoard(...args); };
  api.getCard = async (id) => { retried.push(id); return readCard(id); };
  ui.nodes.refresh.click();
  await ui.settled();
  assert.deepEqual(retried, [card]);
  assert.equal(boardReads, 0);
  assert.equal(ui.nodes.details.querySelector("h2").textContent, "#1 Original");

  api.getCard = async () => { throw new ApiError("http_error", {status: 404}); };
  await ui.load();
  await ui.settled();
  assert.equal(ui.nodes.details.hidden, true);
  assert.equal(JSON.parse(storage.getItem(key)).card, undefined);

  storage.setItem(key, JSON.stringify({card: {id: card, ticket: null}}));
  api.getCard = async () => ({data: {...(await readCard()).data, board_id: backup}, etag: tag(card, 1)});
  const foreign = await harness(api, url, {storage});
  await foreign.load();
  await foreign.settled();
  assert.equal(foreign.nodes.details.hidden, true);
  assert.equal(JSON.parse(storage.getItem(key)).card, undefined);
});

test("a pending open rereads the selected card after a newer board paint without taking focus", async () => {
  const state = boardApi();
  const readCard = state.api.getCard;
  const old = await readCard(card);
  const started = Promise.withResolvers();
  const pending = Promise.withResolvers();
  const reads = [];
  state.api.getCard = async (id) => {
    reads.push(id);
    if (reads.length === 1) {
      started.resolve();
      return pending.promise;
    }
    return readCard(id);
  };
  const ui = await harness(state.api);
  await ui.load();
  const source = ui.find(ui.nodes.main, "button", "#1 Original");
  source.focus();
  source.click();
  await started.promise;
  state.restore();
  ui.nodes.refresh.click();
  await ui.settled();
  const replacement = ui.find(ui.nodes.main, "button", "#9 Recovered");
  assert.ok(replacement);
  assert.equal(ui.focused(), replacement);
  assert.match(ui.nodes.details.textContent, /Loading card/);
  pending.resolve(old);
  await ui.settled();
  assert.deepEqual(reads, [card, card]);
  assert.equal(ui.nodes.details.querySelector("h2").textContent, "#9 Recovered");
  assert.equal(ui.focused(), replacement);
  ui.field(ui.nodes.details, "Title").value = "Edited after newer paint";
  ui.submit(ui.nodes.details, "Save text");
  await ui.settled();
  assert.equal(state.patched(), tag(card, 7));
});

test("repeated live paints bound automatic drawer reads and leave the exact selection retryable", async (t) => {
  const {api, streams, commit} = changingBoard();
  const getCard = api.getCard;
  const reads = [];
  const started = [Promise.withResolvers(), Promise.withResolvers()];
  const release = [Promise.withResolvers(), Promise.withResolvers()];
  api.getCard = async (id) => {
    const attempt = reads.length;
    reads.push(id);
    const reply = await getCard(id);
    if (attempt < release.length) {
      started[attempt].resolve();
      await release[attempt].promise;
    }
    return reply;
  };
  const url = "/boards/" + board;
  const storage = tabStorage();
  const ui = await harness(api, url, {storage});
  t.after(() => ui.navigate("/"));
  await ui.load();
  await until(() => streams.length === 1);
  const {main, details, refresh} = ui.nodes;
  streams[0].send("snapshot", 1);
  await until(() => main.querySelector("#board-live-connection").textContent === "Live updates connected.");
  const opener = (title) => ui.find(main, "button", "#1 " + title);
  opener("Original").focus();
  opener("Original").click();
  for (const [attempt, title] of ["First update", "Second update"].entries()) {
    await started[attempt].promise;
    streams[0].send("change", commit(title));
    await until(() => opener(title));
    assert.equal(details.querySelector("h2"), null);
    release[attempt].resolve();
  }
  await ui.settled();
  assert.deepEqual(reads, [card, card]);
  assert.match(details.textContent, /Use Refresh/);
  assert.doesNotMatch(details.textContent, /Loading card/);
  assert.equal(details.querySelector("h2"), null);
  assert.equal(JSON.parse(storage.getItem("localswim:board-view:" + board)).card.id, card);
  streams[0].send("change", commit("Third update"));
  await until(() => opener("Third update"));
  assert.deepEqual(reads, [card, card]);
  assert.equal(ui.focused(), opener("Third update"));

  refresh.click();
  await until(() => details.querySelector("h2")?.textContent === "#1 Third update");
  assert.deepEqual(reads, [card, card, card]);
  assert.equal(ui.focused(), opener("Third update"));
});

test("cover space exists before bytes arrive and repaint restores scroll without scrolling focused controls", async () => {
  const {api} = boardApi();
  const listLanes = api.listLanes;
  api.listLanes = async () => page("lanes", (await listLanes()).data.lanes.map((entry) => ({...entry, reorder_enabled: true})));
  const getBoardLanes = api.getBoardLanes;
  api.getBoardLanes = async (...args) => {
    const result = await getBoardLanes(...args);
    const original = result.data.cards[0];
    return {...result, data: {...result.data, cards: [
      {...original, cover_attachment_id: backup},
      {...original, id: user, ticket: 2, title: "Other card"},
    ]}};
  };
  const pending = Promise.withResolvers();
  api.getAttachment = async () => ({data: {id: backup, board_id: board, card_id: card, active: true, previewable: true}});
  api.getAttachmentContent = () => pending.promise;
  const url = "/boards/" + board;
  const storage = tabStorage();
  storage.setItem("localswim:board-view:" + board, JSON.stringify({scroll: {left: 80, lanes: {[lane]: 240}}}));
  const ui = await harness(api, url, {storage});
  await ui.load();
  const columns = ui.nodes.main.querySelectorAll("div").find((node) => node.className === "lanes");
  const opener = () => ui.find(ui.nodes.main, "button", "#1 Original");
  const slot = () => opener().children.find((node) => node.className === "card-cover-space");
  assert.ok(slot());
  assert.equal(slot().children.length, 0);
  assert.equal(columns.scrollLeft, 80);
  assert.equal(columns.children[0].scrollTop, 240);
  columns.scrollLeft = 110;
  columns.children[0].scrollTop = 310;
  pending.resolve(new Blob(["cover"]));
  await ui.settled();
  assert.equal(slot().children[0].className, "card-cover");
  slot().children[0].dispatch("error");
  assert.equal(slot().children.length, 0);
  assert.equal(columns.scrollLeft, 110);
  assert.equal(columns.children[0].scrollTop, 310);

  opener().focus();
  ui.nodes.refresh.click();
  await ui.settled();
  assert.equal(columns.scrollLeft, 110);
  assert.equal(columns.children[0].scrollTop, 310);
  assert.equal(ui.focused(), opener());
  assert.equal(opener().focusOptions?.preventScroll, true);
  const preview = () => columns.querySelector("input");
  preview().focus();
  ui.nodes.refresh.click();
  await ui.settled();
  assert.equal(ui.focused(), preview());
  assert.equal(preview().focusOptions?.preventScroll, true);
});

test("card previews use the first eligible attachment and return to it after detaching an explicit cover", async () => {
  const {api} = boardApi();
  const original = (await api.getCard(card)).data;
  const generic = {...(await api.listAttachments()).data.attachments[0], revision: 1};
  const first = {...generic, id: backup, original_name: "First image", previewable: true, sha2_256: "a".repeat(64)};
  const second = {...first, id: lane, original_name: "Second image", size: 5, sha2_256: "b".repeat(64)};
  const skipped = [{...first, id: user, active: false}, {...first, id: card, board_id: user},
    {...first, id: board, card_id: user}];
  let revision = 1;
  let attachments = [generic, first, second];
  let cover = null;
  let unreadableCover = false;
  const reads = [];
  const current = () => ({...original, revision, cover_attachment_id: cover, attachment_count: attachments.length});
  api.getBoardLanes = async () => ({data: {board: {id: board, name: "Board", revision}, cards: [current()]},
    etag: tag(board, revision)});
  api.getCard = async () => ({data: current(), etag: tag(card, revision)});
  api.listAttachments = async (id, cursor) => {
    assert.equal(id, card);
    return cursor === "images" ? page("attachments", attachments.slice(1)) :
      {data: {attachments: [generic, ...skipped], next_cursor: "images"}, etag: null};
  };
  api.getAttachment = async (id) => {
    assert.equal(id, second.id);
    return {data: {...second, active: attachments.includes(second)}, etag: tag(id, revision)};
  };
  api.getAttachmentContent = async (id, disposition, size, digest) => {
    reads.push([id, disposition, size, digest]);
    if (unreadableCover && id === second.id) throw new ApiError("invalid_response");
    return new Blob([id]);
  };
  api.patchCard = async (id, etag, fields) => {
    assert.deepEqual([id, etag, fields.cover_attachment_id], [card, tag(card, 1), second.id]);
    cover = second.id;
    revision++;
  };
  api.detachAttachment = async (id, expected, etag) => {
    assert.deepEqual([id, expected, etag], [second.id, second.revision, tag(board, 2)]);
    attachments = [generic, first];
    cover = null;
    revision++;
  };

  const ui = await harness(api);
  await ui.load();
  const {main, details} = ui.nodes;
  const face = () => ui.find(main, "button", "#1 Original");
  const image = () => face().querySelector("img");
  const readImage = async (id) => {
    await until(image);
    image().dispatch("load");
    assert.equal(image().parent.hidden, false);
    assert.equal(await (await fetch(image().src)).text(), id);
  };
  await readImage(first.id);
  const fallbackUrl = image().src;
  face().click();
  await ui.settled();
  const secondRow = () => details.querySelectorAll("article").find((row) => row.textContent.includes(second.original_name));
  ui.submit(secondRow(), "Set cover");
  await ui.settled();
  await readImage(second.id);
  await assert.rejects(fetch(fallbackUrl));

  unreadableCover = true;
  ui.nodes.refresh.click();
  await ui.settled();
  await until(() => reads.length === 3);
  assert.equal(image(), null);
  assert.equal(reads.at(-1)[0], second.id);
  ui.submit(secondRow(), "Detach");
  await ui.settled();
  await readImage(first.id);
  assert.deepEqual(reads, [first, second, second, first].map(({id, size, sha2_256}) => [id, "inline", size, sha2_256]));
});

test("fallback cover reads cannot paint after a repaint or navigation", async () => {
  const {api} = boardApi();
  const original = (await api.getCard(card)).data;
  const other = {...original, id: user, ticket: 2, title: "No attachments", attachment_count: 0};
  const entry = {...(await api.listAttachments()).data.attachments[0], previewable: true};
  const originalLanes = api.getBoardLanes;
  api.getBoardLanes = async () => {
    const reply = await originalLanes();
    return {...reply, data: {...reply.data, cards: [{...original, attachment_count: 1}, other]}};
  };
  const content = Promise.withResolvers();
  let contentSignal;
  const lateList = Promise.withResolvers();
  const listStarted = Promise.withResolvers();
  let reads = 0;
  let initialSignal;
  let contentReads = 0;
  api.listAttachments = async (id, cursor, signal) => {
    assert.equal(id, card);
    if (cursor === "irrelevant") throw new ApiError("http_error", {status: 503});
    assert.equal(cursor, undefined);
    assert.ok(signal);
    if (++reads === 3) {
      listStarted.resolve(signal);
      return lateList.promise;
    }
    if (reads === 1) {
      initialSignal = signal;
      return {data: {attachments: [entry], next_cursor: "irrelevant"}, etag: null};
    }
    return page("attachments", [{...entry, previewable: false}]);
  };
  api.getAttachmentContent = (id, disposition, _size, _digest, signal) => {
    contentReads++;
    assert.deepEqual([id, disposition, signal], [entry.id, "inline", initialSignal]);
    contentSignal = signal;
    return content.promise;
  };

  const ui = await harness(api);
  await ui.load();
  const slot = () => ui.find(ui.nodes.main, "button", "#1 Original").children
    .find(({className}) => className === "card-cover-space");
  assert.ok(slot());
  const oldSlot = slot();
  await until(() => contentSignal);
  assert.equal(oldSlot.hidden, true);
  ui.nodes.refresh.click();
  await ui.settled();
  await until(() => reads === 2 && slot().hidden);
  assert.equal(contentSignal.aborted, true);
  content.resolve(new Blob(["stale"]));
  await ui.settled();
  assert.equal(oldSlot.children.length, 0);
  assert.equal(slot().children.length, 0);
  assert.equal(ui.find(ui.nodes.main, "button", "#2 No attachments").querySelector("span"), null);

  ui.nodes.refresh.click();
  await ui.settled();
  const listSignal = await listStarted.promise;
  const navigationSlot = slot();
  await ui.navigate("/");
  assert.equal(listSignal.aborted, true);
  lateList.resolve(page("attachments", [entry]));
  await ui.settled();
  assert.equal(navigationSlot.children.length, 0);
  assert.equal(reads, 3);
  assert.equal(contentReads, 1);
});

test("fallback previews grow a lane only after decode and after an active drag ends", async () => {
  const {api} = boardApi();
  const original = (await api.getCard(card)).data;
  const target = {...(await api.listLanes()).data.lanes[0], id: backup, label: "Next", key: "next", position: 1};
  const getPolicy = api.getPolicy;
  api.getPolicy = async () => ({data: {...(await getPolicy()).data,
    move_grants: [{user_id: user, from_lane_id: lane, to_lane_id: target.id}]}});
  const listLanes = api.listLanes;
  api.listLanes = async () => page("lanes", [...(await listLanes()).data.lanes, target]);
  const generic = (await api.listAttachments()).data.attachments[0];
  let addImages = false;
  const rows = Array.from({length: 8}, (_, index) => ({...original, id: index ? "file-" + index : card,
    ticket: index + 1, title: "Files " + (index + 1), attachment_count: 1}));
  const getBoardLanes = api.getBoardLanes;
  api.getBoardLanes = async () => {
    const reply = await getBoardLanes();
    return {...reply, data: {...reply.data, cards: rows}};
  };
  const firstLists = Promise.withResolvers();
  const listReads = [];
  api.listAttachments = async (id) => {
    listReads.push(id);
    if (!addImages) await firstLists.promise;
    return page("attachments", [{...generic, id: "attachment-" + id, card_id: id,
      previewable: addImages && (id === card || id === rows[1].id || id === rows[2].id)}]);
  };
  const contentReads = [];
  api.getAttachmentContent = async (id) => {
    contentReads.push(id);
    if (id === "attachment-" + rows[1].id) throw new ApiError("invalid_response");
    return new Blob([id]);
  };
  const visibleCovers = (column) => column.querySelectorAll("span")
    .filter((slot) => slot.className === "card-cover-space" && !slot.hidden);
  const storage = tabStorage();
  storage.setItem("localswim:board-view:" + board, JSON.stringify({scroll: {left: 0, lanes: {[lane]: 240}}}));
  // The generic cards overflow by 120px. Each visible cover adds another 128px.
  const scrollLimit = (node) => node.className === "lane" ? 120 + 128 * visibleCovers(node).length : Infinity;
  const ui = await harness(api, "/boards/" + board, {storage, scrollLimit});
  const columns = () => ui.nodes.main.querySelectorAll("section").filter((node) => node.className === "lane");
  await ui.load();
  await until(() => listReads.length === rows.length);
  assert.equal(visibleCovers(columns()[0]).length, 0);
  assert.equal(columns()[0].scrollTop, 120);
  columns()[0].scrollTop = 95;
  firstLists.resolve();
  await ui.settled();
  assert.equal(visibleCovers(columns()[0]).length, 0);
  assert.equal(columns()[0].scrollTop, 95);
  assert.deepEqual(contentReads, []);

  addImages = true;
  await ui.navigate("/"); // The next load starts with a saved offset above the bare lane's limit.
  storage.setItem("localswim:board-view:" + board, JSON.stringify({scroll: {left: 0, lanes: {[lane]: 240}}}));
  const reopened = await harness(api, "/boards/" + board, {storage, scrollLimit});
  await reopened.load();
  const laneColumns = reopened.nodes.main.querySelectorAll("section").filter((node) => node.className === "lane");
  const face = reopened.find(reopened.nodes.main, "button", "#1 Files 1");
  const unreadable = reopened.find(reopened.nodes.main, "button", "#2 Files 2");
  const undecodable = reopened.find(reopened.nodes.main, "button", "#3 Files 3");
  await until(() => face.querySelector("img") && undecodable.querySelector("img"));
  assert.equal(visibleCovers(laneColumns[0]).length, 0);
  assert.equal(laneColumns[0].scrollTop, 120);
  assert.equal(unreadable.querySelector("img"), null);
  undecodable.querySelector("img").dispatch("error");
  assert.equal(visibleCovers(laneColumns[0]).length, 0);

  const dataTransfer = dragTransfer();
  face.dispatch("dragstart", {dataTransfer});
  assert.equal(laneColumns[1].dispatch("dragover", {dataTransfer}), true);
  face.querySelector("img").dispatch("load");
  assert.equal(visibleCovers(laneColumns[0]).length, 0);
  assert.equal(laneColumns[0].scrollTop, 120);
  assert.equal(face.dataset.dragging, "true");
  face.dispatch("dragend");
  assert.equal(visibleCovers(laneColumns[0]).length, 1);
  assert.equal(laneColumns[0].scrollTop, 240);
  assert.equal(face.dataset.dragging, undefined);
  assert.equal(laneColumns[1].dataset.dropTarget, undefined);
});

const publishingOff = {enabled: false, repository: null, last_success_at: null, pending: false, error_code: null};

test("a clamped cover scroll survives warm confirmation and superseded paints until the user scrolls", async () => {
  const {api} = boardApi();
  const getBoard = api.getBoardLanes;
  const entry = {...(await api.listAttachments()).data.attachments[0], previewable: true};
  const confirmation = Promise.withResolvers();
  let gateNextRead = false;
  let attachmentReads = 0;
  api.getBoardLanes = async (...args) => {
    if (gateNextRead) {
      gateNextRead = false;
      await confirmation.promise;
    }
    const reply = await getBoard(...args);
    return {...reply, data: {...reply.data, cards: [{...reply.data.cards[0], attachment_count: 1}]}};
  };
  api.listAttachments = async () => {
    attachmentReads++;
    return page("attachments", [entry]);
  };
  api.getAttachmentContent = async () => new Blob(["image"]);
  const storage = tabStorage();
  storage.setItem("localswim:board-view:" + board, JSON.stringify({scroll: {left: 0, lanes: {[lane]: 240}}}));
  const url = "/boards/" + board;
  const ui = await harness(api, url, {storage, scrollLimit: (node) => {
    if (node.className !== "lane") return Infinity;
    return node.querySelectorAll("span").some((slot) => slot.className === "card-cover-space" && !slot.hidden)
      ? 248 : 120;
  }});
  const {main, refresh, status} = ui.nodes;
  const laneColumn = () => main.querySelectorAll("section").find((node) => node.className === "lane");
  const cover = () => laneColumn()?.querySelector("img");
  const repaint = async (previous) => {
    refresh.click();
    await until(() => cover() && cover() !== previous);
    return cover();
  };
  await ui.load();
  await until(cover);
  cover().dispatch("load");
  assert.equal(laneColumn().scrollTop, 240);
  await ui.navigate("/");

  gateNextRead = true;
  const reopening = ui.navigate(url);
  await until(() => status.textContent === "Showing the last confirmed board.");
  assert.equal(ui.find(main, "button", "Add card"), undefined);
  assert.equal(cover(), null);
  assert.equal(attachmentReads, 1);
  assert.equal(laneColumn().scrollTop, 120);

  confirmation.resolve();
  await reopening;
  await until(cover);
  const confirmedCover = cover();
  assert.equal(status.textContent, "Board loaded.");
  assert.ok(ui.find(main, "button", "Add card"));
  assert.equal(confirmedCover.parent.hidden, true);
  assert.equal(laneColumn().scrollTop, 120);

  const latestCover = await repaint(confirmedCover);
  confirmedCover.dispatch("load");
  assert.equal(confirmedCover.parent.hidden, true);
  assert.equal(laneColumn().scrollTop, 120);
  latestCover.dispatch("load");
  assert.equal(laneColumn().scrollTop, 240);

  const pendingCover = await repaint(latestCover);
  assert.equal(laneColumn().scrollTop, 120);
  laneColumn().scrollTop = 75;
  const userCover = await repaint(pendingCover);
  userCover.dispatch("load");
  assert.equal(laneColumn().scrollTop, 75);
});

for (const recovery of ["catalog hint", "Retry Attention", "scheduled catalog check"]) {
  test("a warm catalog startup failure recovers through " + recovery, async (t) => {
    const {api} = boardApi();
    const streams = boardStreams(api);
    let unavailable = false;
    let failures = 0;
    let blocked = 0;
    const readBoard = api.getBoardLanes;
    let boardReads = 0;
    api.getBoardLanes = async (...args) => {
      boardReads++;
      return readBoard(...args);
    };
    api.listBoards = async () => {
      if (unavailable) {
        failures++;
        if (failures === 1) throw new ApiError("transport_failed");
        throw new ApiError("http_error", {status: 400});
      }
      return page("boards", [{id: board, name: "Selected", availability: "open",
        attention: {blocked, needs_input: 0, ready_for_review: 0}, last_card_edit_at: null}]);
    };
    const waits = [];
    const ui = await harness(api, undefined, {catalogTimer: (signal) => {
      const tick = Promise.withResolvers();
      waits.push({...tick, signal});
      return tick.promise;
    }});
    const activeWaits = () => waits.filter(({signal}) => !signal.aborted);
    t.after(ui.abortView);
    await ui.load();
    await ui.navigate("/");
    unavailable = true;
    await ui.navigate("/boards/" + board);
    const {main, boards} = ui.nodes;
    const opener = ui.find(main, "button", "#1 Original");
    await until(() => failures === 2);
    const retry = ui.find(boards, "button", "Retry Attention");
    assert.ok(retry);
    assert.equal(boards.querySelector("a").attributes["aria-current"], "page");
    assert.equal(ui.title(), "localswim");

    unavailable = false;
    blocked = 2;
    if (recovery === "catalog hint") streams.at(-1).sendCatalog("snapshot");
    else if (recovery === "Retry Attention") retry.click();
    else {
      assert.equal(activeWaits().length, 1);
      activeWaits()[0].resolve();
    }
    await until(() => ui.title() === "(2) localswim");
    await ui.settled();
    assert.equal(activeWaits().length, recovery === "scheduled catalog check" ? 2 : 1);
    assert.equal(boards.querySelector("a").attributes["aria-current"], "page");
    assert.equal(ui.find(boards, "button", "Retry Attention"), undefined);
    assert.equal(ui.find(main, "button", "#1 Original"), opener);
    assert.equal(boardReads, 2);
  });
}

test("a warm board applies its authenticated publishing status and validator after mounting", async () => {
  const {api, restore} = boardApi();
  const readBoard = api.getBoardLanes;
  let publishing = publishingOff;
  api.getBoardLanes = async (...args) => {
    const reply = await readBoard(...args);
    reply.data.board.publishing = publishing;
    return reply;
  };
  api.getBoardPublishing = async () => ({data: publishing, etag: tag(board, 7)});
  const saves = [];
  api.configureBoardPublishing = async (id, enabled, expected) => {
    saves.push([id, enabled, expected]);
    publishing = {...publishing, enabled, pending: false};
  };
  const ui = await harness(api);
  const {main} = ui.nodes;
  await ui.load();
  await ui.navigate("/");
  restore();
  publishing = {...publishingOff, enabled: true, pending: true};
  const reopening = ui.navigate("/boards/" + board);
  assert.equal(ui.find(main, "button", "Save board publishing"), undefined);
  await reopening;
  assert.match(main.querySelector("#board-publishing-status").textContent, /publishing is on.*Publication pending/);
  const enabled = ui.field(main, "Enable board publishing");
  assert.equal(enabled.checked, true);
  enabled.checked = false;
  enabled.dispatch("change");
  ui.submit(main, "Save board publishing");
  await ui.settled();
  assert.deepEqual(saves, [[board, false, tag(board, 7)]]);
});

test("warm lane grant drafts survive until the confirmed user choices arrive", async () => {
  const {api} = boardApi();
  let users = [savedUser(user, "Human", "human"), savedUser(backup, "Another", "human"),
    savedUser(priority, "Bot", "bot")];
  api.listUsers = async () => page("users", users);
  const ui = await harness(api);
  const {main, refresh} = ui.nodes;
  await ui.load();
  ui.field(main, "Lane label").value = "Pending lane";
  const select = (label) => ui.field(main, label);
  for (const option of select("Create rights").options) option.selected = option.value !== priority;
  for (const option of select("Connect rights").options) option.selected = option.value === priority;
  await ui.navigate("/");
  users = users.filter(({id}) => id !== backup);
  const reopening = ui.navigate("/boards/" + board);
  assert.equal(ui.find(main, "summary", "Manage lanes"), undefined);
  await reopening;
  const selected = (label) => select(label).selectedOptions.map(({value}) => value);
  assert.equal(ui.field(main, "Lane label").value, "Pending lane");
  assert.deepEqual(selected("Create rights"), [user]);
  assert.deepEqual(selected("Connect rights"), [priority]);
  select("Connect rights").options.find(({value}) => value === priority).selected = false;
  refresh.click();
  await ui.settled();
  assert.deepEqual(selected("Create rights"), [user]);
  assert.deepEqual(selected("Connect rights"), []);
});

test("restored Find is canceled on warm navigation and late 401 cannot renew the session", async () => {
  const {api} = boardApi();
  const started = Promise.withResolvers();
  const release = Promise.withResolvers();
  let searches = 0;
  api.listCards = async (id, fields, signal) => {
    assert.equal(id, board);
    assert.equal(fields.q, "Original");
    assert.ok(signal instanceof AbortSignal);
    if (++searches === 2) {
      started.resolve(signal);
      await release.promise;
      throw new ApiError("http_error", {status: 401});
    }
    return page("cards", [(await api.getCard()).data]);
  };
  let sessions = 0;
  const ui = await harness(api, "/boards/" + board, {openSession: async () => "capability-" + ++sessions});
  const {main} = ui.nodes;
  const search = () => ui.find(main, "summary", "Find cards").parent;
  await ui.load();
  ui.field(search(), "Title or ticket").value = "Original";
  ui.submit(search(), "Find cards");
  await until(() => ui.find(search(), "button", "#1 Original"));
  await ui.navigate("/");
  await ui.navigate("/boards/" + board);
  const signal = await started.promise;
  assert.equal(signal.aborted, false);
  await ui.navigate("/");
  assert.equal(signal.aborted, true);
  release.resolve();
  await ui.settled();
  await ui.navigate("/boards/" + board);
  await until(() => ui.find(search(), "button", "#1 Original"));
  assert.equal(searches, 3);
  assert.equal(sessions, 1);
});

test("a recent board paints before refresh, survives failure, and upgrades its drawer after Retry", async () => {
  const {api, restore, patched} = boardApi();
  const getBoard = api.getBoardLanes;
  const getCard = api.getCard;
  const started = Promise.withResolvers();
  const blocked = Promise.withResolvers();
  let reads = 0;
  let cardReads = 0;
  api.getBoardLanes = async (...args) => {
    if (++reads === 2) {
      started.resolve();
      await blocked.promise;
    }
    return getBoard(...args);
  };
  api.getCard = async (...args) => {
    cardReads++;
    return getCard(...args);
  };
  const ui = await harness(api);
  const {main, details, status} = ui.nodes;
  await ui.load();
  await ui.navigate("/");
  const reopening = ui.navigate("/boards/" + board);
  await started.promise;
  assert.match(status.textContent, /Showing the last confirmed board/);
  assert.match(main.textContent, /Checking for board updates/);
  assert.equal(ui.find(main, "button", "Add card"), undefined);
  assert.equal(ui.find(main, "button", "Create backup"), undefined);
  ui.find(main, "button", "#1 Original").click();
  assert.match(details.textContent, /Original description/);
  assert.equal(ui.find(details, "button", "Save text"), undefined);
  assert.equal(cardReads, 0);

  blocked.reject(new ApiError("transport_failed"));
  await reopening;
  assert.match(main.textContent, /Board update could not be completed/);
  assert.match(status.textContent, /Showing the last confirmed board/);
  assert.match(details.textContent, /Original description/);
  restore();
  const retry = ui.find(main, "button", "Retry board update");
  assert.equal(retry.hidden, false);
  retry.click();
  retry.click();
  await ui.settled();
  assert.equal(reads, 3);
  assert.equal(cardReads, 1);
  assert.equal(status.textContent, "Board loaded.");
  assert.ok(ui.find(main, "button", "#9 Recovered"));
  assert.equal(ui.field(details, "Title").value, "Recovered");
  ui.field(details, "Title").value = "Edited after confirmation";
  ui.submit(details, "Save text");
  await ui.settled();
  assert.equal(patched(), tag(card, 7));
});

test("reducing the recent board budget evicts the least recently used board and ignores late reads", async () => {
  const {api} = boardApi();
  const second = "d131a3c5-1ae0-4f15-9704-80bb84a6a414";
  const getBoard = api.getBoardLanes;
  const getAvailability = api.getBoardAvailability;
  const delayed = new Map();
  api.listBoards = async () => page("boards", [{id: board, name: "First"}, {id: second, name: "Second"}]);
  api.getBoardAvailability = async (id) => {
    const reply = await getAvailability();
    return {data: {...reply.data, id, name: id === board ? "First" : "Second"}, etag: tag(id, 1)};
  };
  api.getBoardLanes = async (id) => {
    const gate = delayed.get(id);
    if (gate) {
      delayed.delete(id);
      gate.started.resolve();
      await gate.release.promise;
    }
    const reply = await getBoard();
    const name = id === board ? "First" : "Second";
    return {data: {board: {...reply.data.board, id, name}, cards: reply.data.cards.map((card) =>
      ({...card, board_id: id}))}, etag: tag(id, 1)};
  };

  const storage = tabStorage();
  const ui = await harness(api, "/boards/" + board, {storage});
  const {main, status} = ui.nodes;
  await ui.load();
  await ui.navigate("/boards/" + second);
  await ui.navigate("/boards/" + board);
  await ui.navigate("/");
  const {data: {board: {id, name, revision}, cards}} = await api.getBoardLanes(board);
  const {data: {lanes}} = await api.listLanes();
  const {data: {priorities}} = await api.listPriorities();
  const oneEntry = new TextEncoder().encode(JSON.stringify({board: {id, name, revision}, lanes, cards, priorities})).byteLength;
  ui.field(main, "Cache budget in bytes").value = String(oneEntry);
  ui.submit(main, "Save cache budget");

  const secondGate = {started: Promise.withResolvers(), release: Promise.withResolvers()};
  delayed.set(second, secondGate);
  const loadingSecond = ui.navigate("/boards/" + second);
  await secondGate.started.promise;
  assert.equal(status.textContent, "Loading…");
  assert.equal(ui.find(main, "h1", "Second"), undefined);

  const firstGate = {started: Promise.withResolvers(), release: Promise.withResolvers()};
  delayed.set(board, firstGate);
  const reopeningFirst = ui.navigate("/boards/" + board);
  await firstGate.started.promise;
  assert.ok(ui.find(main, "h1", "First"));
  assert.match(status.textContent, /Showing the last confirmed board/);
  secondGate.release.resolve();
  await loadingSecond;
  assert.ok(ui.find(main, "h1", "First"));
  firstGate.release.resolve();
  await reopeningFirst;
  assert.equal(status.textContent, "Board loaded.");

  await ui.navigate("/");
  ui.field(main, "Cache budget in bytes").value = "0";
  ui.submit(main, "Save cache budget");
  delayed.set(board, {started: Promise.withResolvers(), release: Promise.withResolvers()});
  const zero = delayed.get(board);
  const loadingFirst = ui.navigate("/boards/" + board);
  await zero.started.promise;
  assert.equal(status.textContent, "Loading…");
  zero.release.resolve();
  await loadingFirst;
  const reload = await harness(api, "/", {storage});
  await reload.load();
  assert.equal(reload.field(reload.nodes.main, "Cache budget in bytes").value, "0");
});

test("the recent board cache skips entries above its UTF-8 byte budget", async () => {
  const {api} = boardApi();
  const getBoard = api.getBoardLanes;
  let blockRead;
  api.getBoardLanes = async () => {
    if (blockRead) {
      blockRead.started.resolve();
      await blockRead.release.promise;
    }
    const reply = await getBoard();
    reply.data.cards = reply.data.cards.map((card) => ({...card, description: "🌊".repeat(400)}));
    return reply;
  };
  const {data: {board: {id, name, revision}, cards}} = await api.getBoardLanes();
  const {data: {lanes}} = await api.listLanes();
  const {data: {priorities}} = await api.listPriorities();
  const serialized = JSON.stringify({board: {id, name, revision}, lanes, cards, priorities});
  const storage = tabStorage();
  const ui = await harness(api, "/", {storage});
  await ui.load();
  ui.field(ui.nodes.main, "Cache budget in bytes").value = String(serialized.length);
  ui.submit(ui.nodes.main, "Save cache budget");
  await ui.navigate("/boards/" + board);
  await ui.navigate("/");
  blockRead = {started: Promise.withResolvers(), release: Promise.withResolvers()};
  const reopened = ui.navigate("/boards/" + board);
  await blockRead.started.promise;
  assert.equal(ui.nodes.status.textContent, "Loading…");
  blockRead.release.resolve();
  await reopened;
  assert.ok(ui.find(ui.nodes.main, "button", "#1 Original"));
});

test("a definitive refusal clears the retained board and its provisional drawer", async () => {
  const {api} = boardApi();
  const getAvailability = api.getBoardAvailability;
  const started = Promise.withResolvers();
  const release = Promise.withResolvers();
  let removed = false;
  api.getBoardAvailability = async (id) => {
    if (!removed) return getAvailability(id);
    started.resolve();
    await release.promise;
    throw new ApiError("http_error", {status: 404});
  };
  const ui = await harness(api);
  const {main, details, status} = ui.nodes;
  await ui.load();
  await ui.navigate("/");
  removed = true;
  const reopening = ui.navigate("/boards/" + board);
  await started.promise;
  ui.find(main, "button", "#1 Original").click();
  assert.equal(details.hidden, false);
  release.resolve();
  await reopening;
  assert.equal(details.hidden, true);
  assert.equal(ui.find(main, "button", "#1 Original"), undefined);
  assert.match(status.textContent, /unavailable/);
  ui.find(main, "button", "Retry board update").click();
  assert.equal(status.textContent, "Loading…");
  assert.equal(ui.find(main, "button", "#1 Original"), undefined);
  await ui.settled();
  assert.match(status.textContent, /unavailable/);
});

test("board CSS follows the active board and is read again after reload", async () => {
  const {api} = boardApi();
  const second = backup;
  let css = "  .lanes { color: #246; } /* Café */\n ";
  let beforeStyledRead = async () => {};
  const readLanes = api.getBoardLanes;
  const readAvailability = api.getBoardAvailability;
  api.listBoards = async () => page("boards", [{id: board, name: "Styled"}, {id: second, name: "Unstyled"}]);
  api.getBoardAvailability = async (id) => id === board ? readAvailability() :
    {data: {id, name: "Unstyled", availability: "open", revision: 1}, etag: tag(id, 1)};
  api.getBoardLanes = async (id) => {
    if (id === second) return {data: {board: {id, name: "Unstyled", user_css: ""}, cards: []}, etag: tag(id, 1)};
    await beforeStyledRead();
    const reply = await readLanes();
    reply.data.board.user_css = css;
    return reply;
  };
  const ui = await harness(api);
  await ui.load();
  assert.equal(ui.stylesheet(), css);
  const changing = ui.navigate("/boards/" + second);
  assert.equal(ui.stylesheet(), "");
  await changing;
  assert.equal(ui.nodes.main.querySelector("h1").textContent, "Unstyled");
  assert.equal(ui.stylesheet(), "");
  await ui.navigate("/boards/" + board);
  assert.equal(ui.stylesheet(), css);

  const reopened = await harness(api);
  await reopened.load();
  assert.equal(reopened.stylesheet(), css);
  const reading = Promise.withResolvers();
  const unblock = Promise.withResolvers();
  beforeStyledRead = async () => { reading.resolve(); await unblock.promise; };
  const stale = reopened.load();
  await reading.promise;
  await reopened.navigate("/boards/" + second);
  unblock.resolve();
  await stale;
  assert.equal(reopened.stylesheet(), "");
  css = "";
  await reopened.navigate("/boards/" + board);
  assert.equal(reopened.stylesheet(), "");
});

test("board lane notes and Human and Bot attribution preserve literal text", async () => {
  const {api} = boardApi();
  const bot = "90b26733-26a1-41ae-b850-251c72978af1";
  const notes = " Review first.\n<example> ";
  const humanLabel = " Reviewer <Human> ";
  const readLanes = api.getBoardLanes;
  const listLanes = api.listLanes;
  const listUsers = api.listUsers;
  const listComments = api.listComments;
  api.getBoardLanes = async () => {
    const reply = await readLanes();
    reply.data.board.actor_presentation = {human: {label: humanLabel, accent: "#123ABC"}, bot: {accent: "#456def"}};
    return reply;
  };
  api.listLanes = async () => {
    const reply = await listLanes();
    return page("lanes", reply.data.lanes.map((item) => ({...item, notes})));
  };
  api.listUsers = async () => page("users", [...(await listUsers()).data.users, savedUser(bot, "Automation", "bot")]);
  api.listComments = async () => page("comments", [...(await listComments()).data.comments,
    {id: priority, board_id: board, card_id: card, ticket: 1, author_id: bot,
      text: "Bot **comment** #0007", created_at: "2026-11-10 00:00:01+00:00"}]);
  const ui = await harness(api);
  await ui.load();
  assert.equal(ui.find(ui.nodes.main, "p", notes).className, "lane-note body-text");
  const recent = ui.find(ui.nodes.main, "summary", "Newest comments").parent;
  recent.open = true;
  recent.dispatch("toggle");
  await ui.settled();
  assert.equal(ui.find(recent, "span", humanLabel).style.borderInlineStartColor, "#123ABC");
  assert.equal(ui.find(recent, "span", "Automation").style.borderInlineStartColor, "#456def");
  const botBody = recent.querySelectorAll("article").at(-1).querySelector("p");
  assert.equal(botBody.textContent, "Bot comment #0007");
  assert.equal(botBody.querySelector("strong").textContent, "comment");
  assert.equal(botBody.querySelector("a"), null);
});

test("card prose displays the shared search text in both drawer and recent comments", async () => {
  const fixtures = JSON.parse(await fs.readFile(new URL("./testdata/card-prose.json", import.meta.url), "utf8"));
  const {api} = boardApi();
  const raw = "Use **the *literal \ntext**, then **run `<img src=x> #7 *x*`**; *see* (#0007), #0, a#2 and ##3.";
  const title = "**Original**";
  const readCard = api.getCard;
  api.getCard = async (...args) => {
    const reply = await readCard(...args);
    return {...reply, data: {...reply.data, title, description: raw}};
  };
  api.listComments = async () => page("comments", fixtures.map(([, text], index) => ({
    id: "f7e04b53-cfd9-417c-af8e-b6316af0a08" + index,
    board_id: board, card_id: card, ticket: 1, author_id: user, text, created_at: createdAt,
  })));
  const ui = await harness(api);
  await ui.load();
  const recent = ui.find(ui.nodes.main, "summary", "Newest comments").parent;
  recent.open = true;
  recent.dispatch("toggle");
  await until(() => recent.querySelectorAll("article").length === fixtures.length);
  assert.deepEqual(recent.querySelectorAll("article").map((item) => item.querySelector("p").textContent),
    fixtures.map(([, , readable]) => readable));
  assert.deepEqual(recent.querySelectorAll("a").map(({textContent}) => textContent), ["#0007"]);
  assert.equal(recent.querySelector("img"), null);

  ui.find(ui.nodes.main, "button", "#1 Original").click();
  const drawer = ui.nodes.details;
  const drawerComments = () => drawer.querySelectorAll("article").filter(({className}) => className !== "attachment");
  await until(() => drawerComments().length === fixtures.length);
  assert.equal(drawer.querySelector("h2").textContent, "#1 " + title);
  const description = drawer.querySelectorAll("p").find(({className}) => className === "body-text");
  assert.equal(description.textContent, "Use **the *literal \ntext**, then run <img src=x> #7 *x*; see (#0007), #0, a#2 and ##3.");
  assert.equal(description.querySelector("strong").textContent, "run <img src=x> #7 *x*");
  assert.equal(description.querySelector("em").textContent, "see");
  assert.equal(description.querySelector("code").textContent, "<img src=x> #7 *x*");
  assert.deepEqual(description.querySelectorAll("a").map(({href}) => href), ["/boards/" + board + "?ticket=7"]);
  assert.equal(description.querySelector("img"), null);
  assert.equal(ui.field(drawer, "Description").value, raw);
  assert.deepEqual(drawerComments().map((item) => item.querySelector("p").textContent),
    fixtures.map(([, , readable]) => readable));
});

test("saving edited prose keeps unchanged line endings and newly entered breaks", async () => {
  const {api} = boardApi();
  const patch = api.patchCard;
  await patch(card, tag(card, 1), {description: "Café\r\n**Old**\rFollow-up\r\nDone\r\r"});
  const writes = [];
  api.patchCard = (id, etag, change) => {
    writes.push(change);
    return patch(id, etag, change);
  };
  const ui = await harness(api);
  await ui.load();
  const {main, details} = ui.nodes;
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.field(details, "Description"));
  const description = ui.field(details, "Description");
  assert.equal(description.value, "Café\n**Old**\nFollow-up\nDone\n\n");
  ui.field(details, "Title").value = "Renamed";
  ui.submit(details, "Save text");
  await ui.settled();
  assert.deepEqual({...writes[0]}, {title: "Renamed"});

  description.value = "Café\n**New 🚲**\nAdded\nFollow-up\nDone\n\n";
  ui.submit(details, "Save text");
  await ui.settled();
  assert.equal(writes[1].description, "Café\r\n**New 🚲**\nAdded\rFollow-up\r\nDone\r\r");

  description.value = "Café\n**New 🚲**\nAdded\n\nFollow-up\nDone\n\n";
  ui.submit(details, "Save text");
  await ui.settled();
  assert.equal(writes[2].description, "Café\r\n**New 🚲**\nAdded\r\n\nFollow-up\r\nDone\r\r");

  description.value += "\n";
  ui.submit(details, "Save text");
  await ui.settled();
  const saved = (await api.getCard()).data.description;
  assert.equal(saved, "Café\r\n**New 🚲**\nAdded\r\n\nFollow-up\r\nDone\r\r\n\n");
  assert.equal(saved.replace(/\r\n?/g, "\n"), description.value);
});

test("streamed drawer refresh retains the focused occurrence of a saved prose link", async (t) => {
  const {api} = boardApi();
  await api.patchCard(card, tag(card, 1), {description: "First #7 and second #7"});
  const readLanes = api.getBoardLanes;
  const readCard = api.getCard;
  api.getBoardLanes = async () => {
    const reply = await readLanes();
    const revision = reply.data.cards[0].revision;
    return {data: {...reply.data, board: {...reply.data.board, revision}}, etag: tag(board, revision)};
  };
  api.getCard = async () => {
    const reply = await readCard();
    return {...reply, etag: tag(card, reply.data.revision)};
  };
  const streams = boardStreams(api);
  const ui = await harness(api);
  t.after(ui.abortView);
  await ui.load();
  await until(() => streams.length === 1);
  const {main, details} = ui.nodes;
  streams[0].send("snapshot", 1);
  await until(() => main.querySelector("#board-live-connection").textContent === "Live updates connected.");
  ui.find(main, "button", "#1 Original").click();
  const proseLinks = () => details.querySelectorAll("p").find(({className}) =>
    className === "body-text")?.querySelectorAll("a") ?? [];
  await until(() => proseLinks().length === 2);
  const original = proseLinks()[1];
  original.focus();
  await api.patchCard(card, tag(card, 1), {title: "Externally renamed", revision: 2});
  streams[0].send("change", 2);
  await until(() => details.querySelector("h2").textContent === "#1 Externally renamed");
  assert.notEqual(proseLinks()[1], original);
  assert.equal(ui.focused(), proseLinks()[1]);
  assert.equal(ui.focused().focusOptions.preventScroll, true);

  await api.patchCard(card, tag(card, 2), {description: "No references remain.", revision: 3});
  streams[0].send("change", 3);
  await until(() => proseLinks().length === 0);
  assert.equal(ui.focused(), details.querySelector("h2"));
});

async function textSaveBoard() {
  const {api} = boardApi();
  const getBoard = api.getBoardLanes;
  const getAvailability = api.getBoardAvailability;
  let saved = {...(await api.getCard(card)).data, description: "Original\r\nprose"};
  api.getBoardLanes = async (...args) => {
    const reply = await getBoard(...args);
    return {data: {board: {...reply.data.board, revision: saved.revision}, cards: [saved]}, etag: tag(board, saved.revision)};
  };
  api.getCard = async (id) => {
    assert.equal(id, card);
    return {data: saved, etag: tag(card, saved.revision)};
  };
  api.getBoardAvailability = async (...args) => {
    const reply = await getAvailability(...args);
    return {data: {...reply.data, revision: saved.revision}, etag: tag(board, saved.revision)};
  };
  return {api, commit(change) {
    saved = {...saved, ...change, revision: saved.revision + 1};
    return {data: saved, etag: tag(card, saved.revision)};
  }};
}

for (const [lostResponse, replaceDrawer] of [[false, false], [true, false], [false, true], [true, true]]) {
  test(`slow text save preserves a newer draft matching the old baseline (lost: ${lostResponse}, replace drawer: ${replaceDrawer})`, async () => {
    const {api, commit} = await textSaveBoard();
    const sent = Promise.withResolvers();
    const release = Promise.withResolvers();
    const writes = [];
    api.patchCard = async (id, etag, change) => {
      writes.push([id, etag, {...change}]);
      sent.resolve();
      await release.promise;
      const reply = commit(change);
      if (lostResponse) throw new ApiError("transport_failed", {outcomeUnknown: true});
      return reply;
    };
    const ui = await harness(api);
    await ui.load();
    const {main, details} = ui.nodes;
    ui.find(main, "button", "#1 Original").click();
    await until(() => ui.field(details, "Description"));
    const title = ui.field(details, "Title");
    const description = ui.field(details, "Description");
    title.value = "Submitted title";
    description.value = "Submitted\nprose";
    ui.submit(details, "Save text");
    await sent.promise;
    title.value = "Original";
    description.value = "Original\nprose";
    title.dispatch("input");
    description.dispatch("input");
    if (replaceDrawer) {
      await ui.navigate("/");
      await ui.navigate("/boards/" + board);
      await until(() => ui.field(details, "Description"));
      assert.notEqual(ui.field(details, "Title"), title);
    }
    const activeTitle = ui.field(details, "Title");
    activeTitle.focus();
    release.resolve();
    await ui.settled();

    assert.equal(details.querySelector("h2").textContent, "#1 Submitted title");
    assert.equal(activeTitle.value, "Original");
    assert.equal(ui.focused(), activeTitle);
    assert.equal(ui.field(details, "Description").value, "Original\nprose");
    assert.deepEqual(writes, [[card, tag(card, 1), {title: "Submitted title", description: "Submitted\r\nprose"}]]);
  });
}

for (const scenario of ["refused in replacement", "refused while away", "lost without commit", "lost before late commit"]) {
  test(`text reversion follows the settled write and subsequent revision after popstate (${scenario})`, async () => {
    const {api, commit} = await textSaveBoard();
    const writes = [];
    const release = Promise.withResolvers();
    const refused = scenario.startsWith("refused");
    const offscreen = scenario === "refused while away";
    const lateCommit = scenario === "lost before late commit";
    api.patchCard = async (id, etag, change) => {
      writes.push([id, etag, {...change}]);
      await release.promise;
      throw refused ? new ApiError("invalid_request", {status: 400})
        : new ApiError("transport_failed", {outcomeUnknown: true});
    };
    const ui = await harness(api);
    await ui.load();
    const {main, details} = ui.nodes;
    ui.find(main, "button", "#1 Original").click();
    await until(() => ui.field(details, "Description"));
    const title = ui.field(details, "Title");
    const description = ui.field(details, "Description");
    title.value = "Submitted title";
    description.value = "Submitted\nprose";
    ui.submit(details, "Save text");
    await until(() => writes.length === 1);

    title.value = "Original";
    description.value = offscreen ? "Keep exact\nnew prose" : "Original\nprose";
    title.dispatch("input");
    description.dispatch("input");
    await ui.popstate("/", false);
    assert.equal(details.hidden, true);
    if (!offscreen) {
      await ui.popstate("/boards/" + board, false);
      await until(() => ui.field(details, "Description"));
      assert.notEqual(ui.field(details, "Title"), title);
    }
    release.resolve();
    await ui.settled();
    if (offscreen) {
      await ui.popstate("/boards/" + board);
      await until(() => ui.field(details, "Description"));
    }
    const replacement = ui.field(details, "Title");
    assert.equal(replacement.value, "Original");
    replacement.focus();

    const later = lateCommit ? writes[0][2] : {title: "External title", description: "External\r\nprose"};
    commit(later);
    ui.nodes.refresh.click();
    await until(() => details.querySelector("h2")?.textContent === "#1 " + later.title);
    assert.equal(replacement.value, lateCommit ? "Original" : "External title");
    assert.equal(ui.field(details, "Description").value,
      offscreen ? "Keep exact\nnew prose" : lateCommit ? "Original\nprose" : "External\nprose");
    assert.equal(ui.focused(), replacement);
    assert.deepEqual(writes, [[card, tag(card, 1), {title: "Submitted title", description: "Submitted\r\nprose"}]]);
  });
}

for (const refused of [true, false]) {
  test(`a newer revision in a replacement drawer waits for the original PATCH outcome (refused: ${refused})`, async () => {
    const {api, commit} = await textSaveBoard();
    const sent = Promise.withResolvers();
    const release = Promise.withResolvers();
    const writes = [];
    api.patchCard = async (id, etag, change) => {
      writes.push([id, etag, {...change}]);
      const committed = refused ? undefined : commit(change);
      sent.resolve();
      await release.promise;
      if (!committed) throw new ApiError("http_error", {status: 412});
      return committed;
    };
    const ui = await harness(api);
    await ui.load();
    const {main, details} = ui.nodes;
    ui.find(main, "button", "#1 Original").click();
    await until(() => ui.field(details, "Description"));
    const title = ui.field(details, "Title");
    const description = ui.field(details, "Description");
    title.value = "Submitted title";
    description.value = "Submitted\nprose";
    ui.submit(details, "Save text");
    await sent.promise;
    title.value = "Original";
    description.value = "Original\nprose";
    title.dispatch("input");
    description.dispatch("input");
    await ui.popstate("/", false);
    const remote = refused ? writes[0][2] : {title: "Remote title", description: "Remote\r\nprose"};
    commit(remote);
    let replacement;
    try {
      await ui.popstate("/boards/" + board, false);
      await until(() => details.querySelector("h2")?.textContent === "#1 " + remote.title);
      replacement = ui.field(details, "Title");
      assert.notEqual(replacement, title);
      assert.equal(replacement.value, "Original");
      replacement.focus();
      if (refused) {
        const newerDescription = ui.field(details, "Description");
        newerDescription.value = "Newer\nprose";
        newerDescription.dispatch("input");
      }
    } finally {
      release.resolve();
      await ui.settled();
    }
    assert.equal(details.querySelector("h2").textContent, "#1 " + remote.title);
    assert.equal(replacement.value, refused ? remote.title : "Original");
    assert.equal(ui.field(details, "Description").value, refused ? "Newer\nprose" : "Original\nprose");
    assert.equal(ui.focused(), replacement);
    assert.deepEqual(writes, [[card, tag(card, 1), {title: "Submitted title", description: "Submitted\r\nprose"}]]);
  });
}

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
  ui.find(ui.nodes.main, "button", "Add card").click();
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

function sidebarControl(ui, label) {
  const control = ui.nodes.boards.querySelectorAll("button").find((button) => button.attributes["aria-label"] === label);
  assert.ok(control, label);
  return control;
}

function pinnedBoards(ui) {
  return ui.nodes.boards.children[1].querySelectorAll("a").map(({dataset}) => dataset.boardId);
}

test("sidebar pin actions and workspace publishing use the same saved workspace revision", async () => {
  let workspace = {revision: 1, pinned_board_ids: [], publishing: {...publishingOff}};
  const workspaceTag = () => tag("workspace-test-id", workspace.revision);
  const read = async () => ({data: workspace, etag: workspaceTag()});
  const writes = [];
  const api = {
    getSession: async () => ({data: {kind: "human", workspace_admin: true}}),
    listBoards: async () => page("boards", [{id: board, name: "First board"}, {id: backup, name: "Second board"}]),
    getWorkspace: read,
    configureWorkspacePublishing: async (enabled, etag) => {
      writes.push({enabled, etag});
      assert.equal(etag, workspaceTag());
      workspace = {...workspace, revision: workspace.revision + 1, publishing: {...workspace.publishing, enabled}};
      return read();
    },
    setPinnedBoards: async (pins, etag) => {
      writes.push({pins: Array.from(pins), etag});
      assert.equal(etag, workspaceTag());
      workspace = {...workspace, revision: workspace.revision + 1, pinned_board_ids: pins};
      return read();
    },
  };
  const ui = await harness(api, "/");
  await ui.load();
  const main = ui.nodes.main;
  const name = ui.field(main, "Board name");
  name.value = "Unsaved board name";
  const enabled = ui.field(main, "Enable workspace publishing");
  enabled.checked = true;
  enabled.dispatch("change");
  sidebarControl(ui, "Pin First board").click();
  await ui.settled();
  assert.equal(enabled.checked, true);
  assert.equal(workspace.publishing.enabled, false);
  ui.submit(main, "Save workspace publishing");
  await ui.settled();
  sidebarControl(ui, "Pin Second board").click();
  await ui.settled();
  assert.deepEqual(writes, [
    {pins: [board], etag: tag("workspace-test-id", 1)},
    {enabled: true, etag: tag("workspace-test-id", 2)},
    {pins: [board, backup], etag: tag("workspace-test-id", 3)},
  ]);
  assert.equal(name.value, "Unsaved board name");
  assert.match(main.querySelector("#workspace-publishing-status").textContent, /publishing is on/);
  assert.deepEqual(pinnedBoards(ui), [board, backup]);
});

test("sidebar pins rebase keyboard and pointer moves while preserving the selected board and drawer", async () => {
  const state = boardApi();
  const {api} = state;
  const names = [{id: board, name: "First"}, {id: backup, name: "Second"}, {id: priority, name: "Third"}];
  let workspace = {revision: 1, pinned_board_ids: [], publishing: publishingOff};
  const workspaceTag = () => tag("workspace-test-id", workspace.revision);
  const writes = [];
  api.getSession = async () => ({data: {kind: "human", principal_id: user, workspace_admin: true}});
  api.listBoards = async () => page("boards", names);
  api.getWorkspace = async () => ({data: workspace, etag: workspaceTag()});
  api.setPinnedBoards = async (pins, etag) => {
    writes.push({pins: Array.from(pins), etag});
    if (etag !== workspaceTag()) throw new ApiError("stale_revision", {status: 412});
    workspace = {...workspace, revision: workspace.revision + 1, pinned_board_ids: pins};
    return api.getWorkspace();
  };
  const ui = await harness(api);
  await ui.load();
  await ui.settled();
  const separator = ui.nodes["resize-board-sidebar"];
  separator.dispatch("keydown", {key: "ArrowRight"});
  const savedWidth = separator.attributes["aria-valuenow"];
  assert.equal(savedWidth, "352");
  ui.find(ui.nodes.main, "button", "#1 Original").click();
  await ui.settled();
  const comment = ui.field(ui.nodes.details, "Comment");
  comment.value = "Unsubmitted draft";

  for (const name of ["First", "Second"]) {
    sidebarControl(ui, "Pin " + name).click();
    await ui.settled();
  }
  workspace = {...workspace, revision: 4, pinned_board_ids: [priority, board, backup]};
  const grip = sidebarControl(ui, "Reorder Second");
  grip.focus();
  assert.equal(grip.dispatch("keydown", {key: "ArrowUp"}), true);
  await ui.settled();
  assert.deepEqual(writes.slice(-2), [
    {pins: [backup, board], etag: tag("workspace-test-id", 3)},
    {pins: [priority, backup, board], etag: tag("workspace-test-id", 4)},
  ]);
  assert.deepEqual(pinnedBoards(ui), [priority, backup, board]);
  assert.equal(ui.focused(), sidebarControl(ui, "Reorder Second"));

  const pointer = sidebarControl(ui, "Reorder Second");
  pointer.dispatch("pointerdown", {isPrimary: true, button: 0, pointerId: 7});
  assert.equal(pointer.capturedPointer, 7);
  const first = ui.nodes.boards.querySelectorAll("a").find(({dataset}) => dataset.boardId === board);
  ui.pointerHit(first);
  pointer.dispatch("pointermove", {pointerId: 7});
  assert.equal(first.parent.attributes["data-pin-drop"], "true");
  pointer.dispatch("pointerup", {pointerId: 7});
  assert.equal(first.parent.attributes["data-pin-drop"], undefined);
  await ui.settled();
  assert.deepEqual(pinnedBoards(ui), [priority, board, backup]);

  sidebarControl(ui, "Unpin Third").click();
  await ui.settled();
  assert.deepEqual(pinnedBoards(ui), [board, backup]);
  assert.equal(ui.nodes.boards.querySelectorAll("a").find(({dataset}) => dataset.boardId === board).attributes["aria-current"], "page");
  assert.equal(ui.field(ui.nodes.details, "Comment"), comment);
  assert.equal(comment.value, "Unsubmitted draft");
  assert.equal(separator.isConnected, true);
  assert.equal(separator.attributes["aria-valuenow"], savedWidth);
  const second = await harness(api, "/");
  await second.load();
  assert.deepEqual(pinnedBoards(second), [board, backup]);
});

test("unavailable saved pins are removed together using complete catalog and workspace readback", async () => {
  const {api} = boardApi();
  const first = {id: board, name: "Selected"};
  const other = {id: backup, name: "Other"};
  const visiblePin = {id: user, name: "New pin"};
  const returned = {id: lane, name: "Visible again"};
  let workspace = {revision: 1, pinned_board_ids: [priority, lane, board, card], publishing: publishingOff};
  const workspaceTag = () => tag("workspace-test-id", workspace.revision);
  const writes = [];
  const pageStarted = Promise.withResolvers();
  const finishPage = Promise.withResolvers();
  let holdPage = false;
  let restored = false;
  api.getSession = async () => ({data: {kind: "human", principal_id: user, workspace_admin: true}});
  api.listBoards = async (cursor) => {
    if (!cursor) return {data: {boards: [first, other], next_cursor: "more"}, etag: null};
    assert.equal(cursor, "more");
    if (holdPage) {
      holdPage = false;
      pageStarted.resolve();
      await finishPage.promise;
    }
    return page("boards", restored ? [visiblePin, returned] : [visiblePin]);
  };
  api.getWorkspace = async () => ({data: workspace, etag: workspaceTag()});
  api.setPinnedBoards = async (pins, etag) => {
    writes.push({pins: Array.from(pins), etag});
    assert.equal(etag, workspaceTag());
    assert.equal(pins.some((id) => id === priority || id === card), false);
    if (writes.length === 1) {
      workspace = {...workspace, revision: 3, pinned_board_ids: [priority, user, lane, board, card, backup]};
      restored = true;
      throw new ApiError("stale_revision", {status: 412});
    }
    workspace = {...workspace, revision: workspace.revision + 1, pinned_board_ids: pins};
    if (writes.length === 2) throw new ApiError("transport_failed", {outcomeUnknown: true});
    return api.getWorkspace();
  };
  const ui = await harness(api);
  await ui.load();
  await ui.settled();
  ui.find(ui.nodes.main, "button", "#1 Original").click();
  await ui.settled();
  const draft = ui.field(ui.nodes.details, "Comment");
  draft.value = "Pending comment";

  assert.ok(ui.find(ui.nodes.boards, "p", "3 saved pins are unavailable."));
  assert.equal(sidebarControl(ui, "Pin Other").attributes["aria-disabled"], "true");
  assert.deepEqual(writes, []);
  workspace = {...workspace, revision: 2, pinned_board_ids: [priority, lane, board, card, backup]};
  holdPage = true;
  const remove = ui.find(ui.nodes.boards, "button", "Remove unavailable pins");
  remove.focus();
  remove.click();
  await pageStarted.promise;
  assert.deepEqual(writes, []);
  assert.equal(ui.focused(), remove);
  finishPage.resolve();
  await ui.settled();

  assert.deepEqual(writes, [
    {pins: [board, backup], etag: tag("workspace-test-id", 2)},
    {pins: [user, lane, board, backup], etag: tag("workspace-test-id", 3)},
  ]);
  assert.deepEqual(pinnedBoards(ui), [user, lane, board, backup]);
  const selected = ui.nodes.boards.querySelectorAll("a").find(({dataset}) => dataset.boardId === board);
  assert.equal(selected.attributes["aria-current"], "page");
  assert.equal(ui.focused(), selected);
  assert.equal(ui.field(ui.nodes.details, "Comment"), draft);
  assert.equal(draft.value, "Pending comment");
  assert.equal(ui.find(ui.nodes.boards, "button", "Remove unavailable pins").hidden, true);
  sidebarControl(ui, "Unpin Other").click();
  await ui.settled();
  assert.deepEqual(pinnedBoards(ui), [user, lane, board]);
});

test("unavailable pin cleanup preserves a board added before the fresh workspace read", async () => {
  const {api} = boardApi();
  let catalog = [{id: board, name: "Selected"}];
  let workspace = {revision: 1, pinned_board_ids: [priority, board], publishing: publishingOff};
  let addPin = false;
  const writes = [];
  const workspaceTag = () => tag("workspace-test-id", workspace.revision);
  api.getSession = async () => ({data: {kind: "human", principal_id: user, workspace_admin: true}});
  api.listBoards = async () => page("boards", catalog);
  api.getWorkspace = async () => {
    if (addPin) {
      addPin = false;
      catalog = [...catalog, {id: backup, name: "New board"}];
      workspace = {...workspace, revision: 2, pinned_board_ids: [priority, board, backup]};
    }
    return {data: workspace, etag: workspaceTag()};
  };
  api.setPinnedBoards = async (pins, etag) => {
    assert.equal(etag, workspaceTag());
    writes.push({pins: Array.from(pins), etag});
    workspace = {...workspace, revision: workspace.revision + 1, pinned_board_ids: pins};
    return api.getWorkspace();
  };
  const ui = await harness(api);
  await ui.load();
  await ui.settled();

  addPin = true;
  ui.find(ui.nodes.boards, "button", "Remove unavailable pins").click();
  await ui.settled();

  assert.deepEqual(writes, [{pins: [board, backup], etag: tag("workspace-test-id", 2)}]);
  assert.deepEqual(pinnedBoards(ui), [board, backup]);
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

test("Attention keeps pin and catalog order, fixed count slots, accessible zeroes, and audited recency", async () => {
  const counts = (blocked, needs_input, ready_for_review) => ({blocked, needs_input, ready_for_review});
  const sameEdit = "2026-11-11 00:00:00+00:00";
  let now = Date.parse("2026-11-11T00:07:30Z");
  class Clock extends Date { static now() { return now; } }
  const rows = [
    {id: board, name: "Zulu", attention: counts(0, 4, 105), last_card_edit_at: sameEdit},
    {id: backup, name: "Alpha", attention: counts(1, 0, 0), last_card_edit_at: sameEdit},
    {id: user, name: "Empty", attention: counts(0, 0, 0), last_card_edit_at: null},
    {id: priority, name: null, attention: null, last_card_edit_at: null},
  ];
  let requested;
  const preferencesRequested = new Promise((resolve) => { requested = resolve; });
  let resolvePreferences;
  const preferences = new Promise((resolve) => { resolvePreferences = resolve; });
  const ui = await harness({
    getSession: async () => ({data: {kind: "human", workspace_admin: true}}),
    listBoards: async () => page("boards", rows),
    getWorkspace: () => {
      requested();
      return preferences;
    },
  }, "/", {clock: Clock});
  const loading = ui.load();
  await preferencesRequested;
  ui.nodes.boards.querySelector("a").focus();
  resolvePreferences({data: {
    revision: 1, pinned_board_ids: [priority, lane, user], publishing: publishingOff,
  }, etag: tag(board, 1)});
  await loading;
  const [header, pinned, other] = ui.nodes.boards.children;
  const keys = ["blocked", "needs_input", "ready_for_review"];
  assert.deepEqual(header.children.slice(1).map((node) => node.dataset.attention), keys);
  assert.deepEqual(header.children.slice(1).map((node) => node.textContent), ["Blocked", "Needs Terry", "Ready for Review"]);
  assert.equal(pinned.children[0].textContent, "Pinned boards");
  assert.equal(other.children[0].textContent, "Other boards");
  assert.deepEqual(pinned.querySelectorAll("a").map((node) => node.dataset.boardId), [priority, user]);
  assert.deepEqual(other.querySelectorAll("a").map((node) => node.dataset.boardId), [board, backup]);

  const [unavailable, empty, zulu, alpha] = ui.nodes.boards.querySelectorAll("a");
  assert.equal(ui.focused(), zulu);
  for (const row of [unavailable, empty, zulu, alpha]) {
    assert.deepEqual(row.children.slice(1, 4).map((node) => node.dataset.attention), keys);
    assert.equal(row.attributes["aria-describedby"], row.children[4].id);
  }
  const visibleCounts = (row) => row.children.slice(1, 4).map((node) => node.textContent);
  assert.deepEqual(visibleCounts(unavailable), ["—", "—", "—"]);
  assert.deepEqual(visibleCounts(empty), ["", "", ""]);
  assert.deepEqual(visibleCounts(zulu), ["", "4", "99+"]);
  assert.deepEqual(visibleCounts(alpha), ["1", "", ""]);
  assert.match(unavailable.children[4].textContent, /Blocked unavailable\. Needs Terry unavailable\. Ready for Review unavailable/);
  assert.match(empty.children[4].textContent, /Blocked 0\. Needs Terry 0\. Ready for Review 0/);
  assert.match(zulu.children[4].textContent, /Blocked 0\. Needs Terry 4\. Ready for Review 105/);
  assert.equal(unavailable.title, "Card edit history unavailable");
  assert.equal(empty.title, "No recorded card edits");
  assert.equal(zulu.title, "Last card edit 7 minutes ago");
  assert.equal(alpha.title, zulu.title);
  now += 2 * 60_000;
  zulu.dispatch("pointerenter");
  assert.equal(zulu.title, "Last card edit 9 minutes ago");
  assert.match(zulu.children[4].textContent, /Last card edit 9 minutes ago$/);
  assert.deepEqual(other.querySelectorAll("a").map((node) => node.dataset.boardId), [board, backup]);
});

test("an unadmitted board shows a filename identity and leaves a healthy board selectable", async () => {
  const {api, patched} = boardApi();
  const damaged = "eb83343c-b9eb-4ae7-8dd9-63988a53dc2f";
  api.getSession = async () => ({data: {kind: "human", principal_id: user, workspace_admin: true}});
  api.getWorkspace = async () => ({data: {revision: 1, pinned_board_ids: [damaged], publishing: publishingOff},
    etag: tag("workspace-test-id", 1)});
  const getAvailability = api.getBoardAvailability;
  api.getBoardAvailability = async (id) => {
    assert.equal(id, board);
    return getAvailability();
  };
  api.listBoards = async () => page("boards", [
    {id: board, name: "Board", revision: 1, availability: "open",
      attention: {blocked: 0, needs_input: 0, ready_for_review: 0}, last_card_edit_at: null, error_code: null},
    {id: damaged, name: null, revision: null, availability: "unhealthy",
      attention: null, last_card_edit_at: null, error_code: "durable_state_unavailable"},
  ]);
  const ui = await harness(api, "/boards/" + damaged);
  await ui.load();
  const {main, boards, details} = ui.nodes;
  const label = "Unavailable board (eb83343c)";
  await until(() => pinnedBoards(ui).includes(damaged));
  const row = boards.querySelectorAll("a").find((item) => item.dataset.boardId === damaged);
  assert.equal(row.children[0].textContent, label);
  assert.equal(row.attributes["aria-current"], "page");
  assert.match(row.title, /could not load .* saved snapshot at startup/);
  assert.equal(main.querySelector("h1").textContent, label);
  assert.match(main.textContent, new RegExp(damaged));
  assert.match(main.textContent, /could not load .* saved snapshot at startup/);
  assert.equal(main.querySelector("form"), null);
  assert.equal(ui.find(boards, "button", "Remove unavailable pins").hidden, true);

  await ui.navigate("/boards/" + board);
  assert.equal(main.querySelector("h1").textContent, "Board");
  assert.ok(ui.find(boards, "span", label));
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.field(details, "Title"));
  ui.field(details, "Title").value = "Healthy board edit";
  ui.submit(details, "Save text");
  await ui.settled();
  assert.equal(patched(), tag(card, 1));
  assert.ok(ui.find(main, "button", "#1 Healthy board edit"));
});

test("a startup failure evicts a warm board and keeps only the catalog watch", async (t) => {
  const {api} = boardApi();
  const streams = boardStreams(api);
  const catalog = Promise.withResolvers();
  const availability = Promise.withResolvers();
  const started = Promise.withResolvers();
  const getAvailability = api.getBoardAvailability;
  let reopening = false;
  let blocked = 0;
  api.listBoards = async () => {
    const other = {id: backup, name: "Healthy", revision: 1, availability: "open",
      attention: {blocked, needs_input: 0, ready_for_review: 0}, last_card_edit_at: null, error_code: null};
    if (!reopening) return page("boards", [{...other, id: board, name: "Board"}, other]);
    await catalog.promise;
    return page("boards", [other, {id: board, name: null, revision: null, availability: "unhealthy",
      attention: null, last_card_edit_at: null, error_code: "durable_state_unavailable"}]);
  };
  api.getBoardAvailability = async (id, signal) => {
    if (reopening) {
      started.resolve(signal);
      await availability.promise;
    }
    return getAvailability(id);
  };
  const ui = await harness(api, undefined, {workspaceEvents: true});
  t.after(() => {
    catalog.resolve();
    availability.resolve();
    ui.abortView();
  });
  await ui.load();
  await ui.navigate("/");
  reopening = true;
  const warm = ui.navigate("/boards/" + board);
  const boardRead = await started.promise;
  const {main, details} = ui.nodes;
  ui.find(main, "button", "#1 Original").click();
  assert.equal(details.hidden, false);
  assert.equal(ui.find(main, "button", "Add card"), undefined);
  assert.equal(ui.find(details, "button", "Save text"), undefined);
  await until(() => streams.at(-1)?.boardId === board && !streams.at(-1).signal.aborted);
  const selectedStream = streams.at(-1);

  catalog.resolve();
  await until(() => /saved snapshot at startup/.test(main.textContent));
  assert.equal(boardRead.aborted, true);
  assert.equal(details.hidden, true);
  assert.equal(ui.find(main, "button", "#1 Original"), undefined);
  assert.equal(selectedStream.signal.aborted, true);
  await until(() => streams.at(-1)?.boardId === undefined && streams.at(-1)?.signal.aborted === false);
  blocked = 1;
  streams.at(-1).sendCatalog("snapshot");
  await until(() => ui.title() === "(1) localswim");

  availability.resolve();
  await warm;
  assert.match(main.textContent, /saved snapshot at startup/);
  assert.equal(main.querySelector("form"), null);
});

test("a catalog-confirmed offscreen startup failure prevents the old board from painting on return", async (t) => {
  const {api} = boardApi();
  const streams = boardStreams(api);
  const nextCatalog = Promise.withResolvers();
  const nextAvailability = Promise.withResolvers();
  const readingCatalog = Promise.withResolvers();
  const getAvailability = api.getBoardAvailability;
  let failed = false;
  let returning = false;
  let returnAvailabilityReads = 0;
  api.listBoards = async () => {
    if (returning) {
      readingCatalog.resolve();
      await nextCatalog.promise;
    }
    return page("boards", [failed
      ? {id: board, name: null, revision: null, availability: "unhealthy", attention: null,
        last_card_edit_at: null, error_code: "durable_state_unavailable"}
      : {id: board, name: "Board", revision: 1, availability: "open",
        attention: {blocked: 0, needs_input: 0, ready_for_review: 0}, last_card_edit_at: null, error_code: null}]);
  };
  api.getBoardAvailability = async (id) => {
    if (returning) {
      returnAvailabilityReads++;
      await nextAvailability.promise;
    }
    return getAvailability(id);
  };
  const ui = await harness(api, undefined, {workspaceEvents: true});
  t.after(() => {
    nextCatalog.resolve();
    nextAvailability.resolve();
    ui.abortView();
  });
  await ui.load();
  const {main, boards} = ui.nodes;
  assert.ok(ui.find(main, "button", "#1 Original"));
  await ui.navigate("/");
  await until(() => streams.at(-1)?.boardId === undefined && streams.at(-1)?.signal.aborted === false);

  failed = true;
  streams.at(-1).sendCatalog("snapshot");
  await until(() => ui.find(boards, "span", "Unavailable board (87b11b54)"));

  returning = true;
  const reopening = ui.navigate("/boards/" + board);
  await readingCatalog.promise;
  assert.equal(ui.find(main, "button", "#1 Original"), undefined);
  assert.match(ui.nodes.status.textContent, /Loading/);

  nextCatalog.resolve();
  nextAvailability.resolve();
  await reopening;
  assert.equal(returnAvailabilityReads, 0);
  assert.match(main.textContent, /saved snapshot at startup/);
});

test("refreshing an open board repaints Attention in catalog order and retains sidebar focus", async () => {
  const state = boardApi();
  const pinned = {id: user, name: "Pinned", attention: null, last_card_edit_at: null};
  const first = {id: board, name: "First", attention: {blocked: 1, needs_input: 0, ready_for_review: 0},
    last_card_edit_at: "2026-11-11 00:00:00+00:00"};
  const second = {id: backup, name: "Second", attention: {blocked: 0, needs_input: 0, ready_for_review: 0},
    last_card_edit_at: null};
  let catalog = [first, second, pinned];
  let reads = 0;
  state.api.getSession = async () => ({data: {kind: "human", workspace_admin: true}});
  state.api.getWorkspace = async () => ({data: {
    revision: 1, pinned_board_ids: [user], publishing: publishingOff,
  }, etag: tag(board, 1)});
  state.api.listBoards = async () => { reads++; return page("boards", catalog); };
  const ui = await harness(state.api);
  await ui.load();
  await ui.settled();
  const links = () => ui.nodes.boards.querySelectorAll("a");
  assert.deepEqual(links().map((node) => node.dataset.boardId), [user, board, backup]);
  links()[2].focus();

  state.restore();
  catalog = [{...second, attention: {...second.attention, ready_for_review: 3},
    last_card_edit_at: "2026-11-11 00:01:00+00:00"}, first, pinned];
  ui.nodes.refresh.click();
  await ui.settled();
  assert.equal(reads, 2);
  assert.ok(ui.find(ui.nodes.main, "button", "#9 Recovered"));
  assert.match(ui.nodes.status.textContent, /Saved board read/);
  assert.deepEqual(links().map((node) => node.dataset.boardId), [user, backup, board]);
  assert.equal(ui.focused(), links()[1]);
  assert.equal(links()[1].children[3].textContent, "3");
  assert.match(links()[1].title, /^Last card edit /);
  assert.equal(links()[2].attributes["aria-current"], "page");
});

test("a pending Attention read leaves confirmed card saves and the refreshed drawer usable", async () => {
  const {api} = boardApi();
  const patchCard = api.patchCard;
  const getCard = api.getCard;
  api.patchCard = async (...args) => { await patchCard(...args); return getCard(card); };
  let drawerReads = 0;
  api.getCard = async (...args) => { drawerReads++; return getCard(...args); };
  api.listComments = async () => page("comments", []);
  const catalog = (ready_for_review) => page("boards", [{
    id: board, name: "Board", attention: {blocked: 0, needs_input: 0, ready_for_review}, last_card_edit_at: null,
  }]);
  const started = Promise.withResolvers();
  const pending = Promise.withResolvers();
  let catalogReads = 0;
  api.listBoards = async () => {
    if (++catalogReads === 1) return catalog(1);
    started.resolve();
    return pending.promise;
  };

  const ui = await harness(api);
  await ui.load();
  const {main, details, boards, status} = ui.nodes;
  ui.find(main, "button", "#1 Original").click();
  await ui.settled();
  const priorDrawerReads = drawerReads;
  ui.field(details, "Title").value = "Saved with Attention pending";
  const save = ui.submit(details, "Save text");
  await started.promise;
  await ui.settled();
  assert.equal(status.textContent, "Card saved.");
  assert.ok(ui.find(main, "button", "#1 Saved with Attention pending"));
  assert.equal(details.querySelector("h2").textContent, "#1 Saved with Attention pending");
  assert.equal(drawerReads, priorDrawerReads + 1);
  assert.equal(save.disabled, false);
  const link = boards.querySelector("a");
  assert.equal(link.children[3].textContent, "1");
  link.focus();

  pending.resolve(catalog(2));
  await ui.settled();
  assert.equal(boards.querySelector("a").children[3].textContent, "2");
  assert.equal(ui.focused(), boards.querySelector("a"));
  assert.equal(status.textContent, "Card saved.");
});

test("an Attention read failure keeps confirmed card saves and the last catalog visible", async () => {
  const {api} = boardApi();
  const patchCard = api.patchCard;
  api.patchCard = async (...args) => { await patchCard(...args); return api.getCard(); };
  api.listComments = async () => page("comments", []);
  const row = {id: board, name: "Board", attention: {blocked: 0, needs_input: 0, ready_for_review: 1},
    last_card_edit_at: null};
  let unavailable = false;
  let reads = 0;
  const retried = Promise.withResolvers();
  api.listBoards = async () => {
    reads++;
    if (reads === 3) retried.resolve();
    if (unavailable) throw new ApiError("transport_failed");
    return page("boards", [row]);
  };
  const ui = await harness(api);
  await ui.load();
  ui.find(ui.nodes.main, "button", "#1 Original").click();
  await ui.settled();
  const attentionStatus = ui.find(ui.nodes.boards, "p", "Attention could not be refreshed. Showing the last saved counts.");
  assert.equal(attentionStatus.hidden, true);
  const title = ui.field(ui.nodes.details, "Title");
  title.value = "Saved during catalog outage";
  unavailable = true;
  ui.submit(ui.nodes.details, "Save text");
  await retried.promise;
  await ui.settled();
  assert.equal(ui.nodes.status.textContent, "Card saved.");
  assert.ok(ui.find(ui.nodes.main, "button", "#1 Saved during catalog outage"));
  assert.equal(reads, 3);
  assert.equal(ui.nodes.boards.querySelector("a").children[3].textContent, "1");
  assert.equal(attentionStatus.hidden, false);

  unavailable = false;
  row.attention.ready_for_review = 2;
  ui.nodes.refresh.click();
  await ui.settled();
  assert.equal(reads, 4);
  assert.equal(ui.nodes.boards.querySelector("a").children[3].textContent, "2");
  assert.equal(attentionStatus.hidden, true);
});

test("an old Attention read cannot replace the catalog after navigation", async () => {
  const {api} = boardApi();
  const started = Promise.withResolvers();
  const late = Promise.withResolvers();
  let reads = 0;
  api.listBoards = async (_cursor, signal) => {
    reads++;
    if (reads === 2) {
      started.resolve(signal);
      return late.promise;
    }
    const name = reads === 1 ? "Original catalog" : "New view catalog";
    return page("boards", [{id: board, name}]);
  };
  const ui = await harness(api);
  await ui.load();
  ui.nodes.refresh.click();
  const signal = await started.promise;
  await ui.navigate("/");
  assert.equal(signal.aborted, true);
  late.resolve(page("boards", [{id: board, name: "Old view catalog"}]));
  await ui.settled();
  assert.ok(ui.find(ui.nodes.boards, "span", "New view catalog"));
  assert.equal(ui.find(ui.nodes.boards, "span", "Old view catalog"), undefined);
});

test("uncertain pin writes wait for inspection and retain the intended edit through navigation", async () => {
  let workspace = {revision: 1, pinned_board_ids: [board], publishing: {...publishingOff, enabled: true}};
  let available = true;
  let writes = 0;
  const api = {
    getSession: async () => ({data: {kind: "human", workspace_admin: true}}),
    listBoards: async () => page("boards", [{id: board, name: "First board"}]),
    getWorkspace: async () => {
      if (!available) throw new ApiError("transport_failed");
      return {data: workspace, etag: tag("workspace-test-id", workspace.revision)};
    },
    setPinnedBoards: async (pinned_board_ids, etag) => {
      ++writes;
      assert.equal(etag, tag("workspace-test-id", workspace.revision));
      if (!available) throw new ApiError("transport_failed", {outcomeUnknown: true});
      workspace = {...workspace, revision: workspace.revision + 1, pinned_board_ids};
      return api.getWorkspace();
    },
  };
  const ui = await harness(api, "/");
  const {main} = ui.nodes;
  const enabled = () => ui.field(main, "Enable workspace publishing");
  await ui.load();
  enabled().checked = false;
  enabled().dispatch("change");
  available = false;
  sidebarControl(ui, "Unpin First board").click();
  await ui.settled();
  assert.equal(writes, 1);
  assert.deepEqual(pinnedBoards(ui), [board]);
  await ui.navigate("/missing");
  await ui.navigate("/");
  assert.equal(writes, 1);
  assert.equal(enabled().checked, false);
  assert.match(main.querySelector("#workspace-publishing-status").textContent, /unavailable/);

  available = true;
  ui.find(ui.nodes.boards, "button", "Retry pin change").click();
  await ui.settled();
  assert.equal(writes, 2);
  assert.deepEqual(pinnedBoards(ui), []);
  assert.deepEqual(workspace.pinned_board_ids, []);
  assert.equal(enabled().checked, false);
  assert.match(main.querySelector("#workspace-publishing-status").textContent, /publishing is on/);
});

test("a delayed pin write remains serialized across navigation without blocking publishing drafts", async () => {
  let workspace = {revision: 1, pinned_board_ids: [board], publishing: {...publishingOff, enabled: true}};
  let finishPins;
  const api = {
    getSession: async () => ({data: {kind: "human", workspace_admin: true}}),
    listBoards: async () => page("boards", [{id: board, name: "First board"}]),
    getWorkspace: async () => ({data: workspace, etag: tag("workspace-test-id", workspace.revision)}),
    setPinnedBoards: (pinned_board_ids) => new Promise((resolve) => {
      finishPins = () => {
        workspace = {...workspace, revision: 2, pinned_board_ids};
        resolve(api.getWorkspace());
      };
    }),
  };
  const ui = await harness(api, "/");
  await ui.load();
  const enabled = () => ui.field(ui.nodes.main, "Enable workspace publishing");
  sidebarControl(ui, "Unpin First board").click();
  assert.equal(sidebarControl(ui, "Unpin First board").attributes["aria-disabled"], "true");
  enabled().checked = false;
  enabled().dispatch("change");
  await ui.navigate("/missing");
  const reloading = ui.navigate("/");
  finishPins();
  await reloading;
  assert.deepEqual(pinnedBoards(ui), []);
  assert.equal(enabled().checked, false);
  assert.equal(sidebarControl(ui, "Pin First board").attributes["aria-disabled"], undefined);
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
  const {panel} = ui.renderCardSearch(board, [], () => {}, new AbortController().signal);
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

const backupDigest = "0".repeat(64);
const backupReceipt = (id, revision = 1) => ({data: {id, board_revision: revision, size: 3,
  sha2_256: backupDigest, attachment_ids: []}});

test("a delayed backup permits card edits and navigation and downloads the initiating board's earlier revision", async () => {
  const {api, commit} = changingBoard();
  delete api.streamEvents;
  const other = "4cbd90c1-e555-4431-8be2-cf9a96a6bc09";
  const availability = api.getBoardAvailability;
  const lanes = api.getBoardLanes;
  api.listBoards = async () => page("boards", [{id: board, name: "Board"}, {id: other, name: "Other board"}]);
  api.getBoardAvailability = async (id) => id === board ? availability() : {
    data: {...(await availability()).data, id: other, name: "Other board"}, etag: tag(other, 1),
  };
  api.getBoardLanes = async (id) => id === board ? lanes() : {
    data: {board: {...(await lanes()).data.board, id: other, name: "Other board", revision: 1}, cards: []},
    etag: tag(other, 1),
  };
  api.patchCard = async (id, expected, patch) => {
    assert.equal(expected, tag(card, 1));
    assert.equal(commit(patch.title, lane), 2);
    return api.getCard(id);
  };
  const capture = Promise.withResolvers();
  const captured = [];
  api.createBackup = async (...args) => {
    captured.push(args);
    await capture.promise;
  };
  api.getBackup = async (id, boardId) => {
    assert.equal(boardId, board);
    assert.equal(id, captured[0][0]);
    return backupReceipt(id);
  };
  const downloads = [];
  api.getBackupContent = async (...args) => {
    downloads.push(args);
    return new Blob(["ZIP"]);
  };
  const ui = await harness(api);
  await ui.load();
  const {main, details, status} = ui.nodes;
  const create = () => ui.find(main, "button", "Create backup");
  create().click();
  create().click();
  assert.equal(ui.busy(), false);
  assert.equal(create().disabled, true);
  assert.match(main.textContent, /Capturing backup/);
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.field(details, "Title"));
  ui.field(details, "Title").value = "Edited during capture";
  ui.submit(details, "Save text");
  await ui.settled();
  assert.equal((await api.getCard(card)).data.revision, 2);
  assert.match(main.textContent, /Capturing backup/);

  await ui.navigate("/boards/" + other);
  assert.equal(create().disabled, false);
  assert.doesNotMatch(main.textContent, /Capturing backup/);
  await ui.navigate("/boards/" + board);
  assert.equal(create().disabled, true);
  create().click();
  assert.equal(captured.length, 1);
  await ui.navigate("/boards/" + other);
  const otherStatus = status.textContent;
  capture.resolve();
  await ui.settled();
  assert.doesNotMatch(main.textContent, /Captured revision/);
  assert.equal(status.textContent, otherStatus);
  await ui.navigate("/boards/" + board);
  assert.match(main.textContent, /Captured revision 1 with 0 attachment files/);
  assert.equal(create().disabled, false);
  ui.find(main, "button", "Download backup").click();
  await ui.settled();
  assert.equal(captured.length, 1);
  const [captureId, captureBoard, validator] = captured[0];
  assert.match(captureId, /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.equal(captureBoard, board);
  assert.equal(validator, tag(board, 1));
  assert.deepEqual(downloads, [[captureId, 3, backupDigest]]);
  assert.deepEqual(ui.downloads, ["localswim-" + board + "-r1.zip"]);
});

test("early backup 404s preserve the original capture across navigation without replaying the POST", async () => {
  const writes = [];
  const reads = [];
  const {api} = boardApi();
  api.createBackup = async (id) => {
    writes.push(id);
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  api.getBackup = async (id, boardId) => {
    reads.push([id, boardId]);
    if (reads.length <= 2) throw new ApiError("http_error", {status: 404});
    return backupReceipt(id);
  };
  const ui = await harness(api);
  await ui.load();
  const {main} = ui.nodes;
  const create = () => ui.find(main, "button", "Create backup");
  const refresh = () => ui.find(main, "button", "Refresh backup status");
  create().click(); await ui.settled();
  assert.equal(create().disabled, true);
  assert.match(main.textContent, /Backup capture could not be confirmed/);
  assert.equal(ui.busy(), false);
  refresh().click(); await ui.settled();
  assert.equal(create().disabled, true);
  assert.match(main.textContent, /Backup capture could not be confirmed/);
  create().click();
  await ui.navigate("/");
  await ui.navigate("/boards/" + board);
  assert.equal(create().disabled, true);
  assert.equal(refresh().hidden, false);
  create().click();
  refresh().click(); await ui.settled();
  assert.equal(create().disabled, false);
  assert.match(main.textContent, /Captured revision 1/);
  assert.equal(writes.length, 1);
  assert.deepEqual(reads, [[writes[0], board], [writes[0], board], [writes[0], board]]);
});

test("a late backup refusal preserves a newer browser capability", async () => {
  const started = Promise.withResolvers();
  const response = Promise.withResolvers();
  let sessions = 0;
  let capability;
  const ui = await harness((getCapability) => {
    capability = getCapability;
    return {createBackup: async () => { started.resolve(); await response.promise; }};
  }, "/boards/" + board, {openSession: async () => "session-" + ++sessions});
  const panel = ui.renderBoardActions(board, [], () => [], () => tag(board, 1), async () => {}, {sync() {}, require() {}});
  ui.nodes.main.append(panel);
  ui.find(panel, "button", "Create backup").click();
  await started.promise;
  assert.equal(capability(), "session-1");
  await ui.readWithSession(async () => {
    if (capability() === "session-1") throw new ApiError("http_error", {status: 401});
  });
  response.reject(new ApiError("http_error", {status: 401}));
  await ui.settled();
  assert.equal(capability(), "session-2");
  assert.match(panel.textContent, /The service refused to create the backup/);
  assert.equal(sessions, 2);
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
  ui.find(panel, "button", "Create backup").click(); await ui.settled();
  const publish = ui.find(panel, "button", "Publish backup");
  publish.click(); await ui.settled();
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
  assert.match(revisited.textContent, /Publication succeeded/);
});

test("known publication refusal clears pending status and permits an explicit later attempt", async () => {
  let writes = 0;
  const api = {createBackup: async () => {}, getBackup: async () => ({data: {id: backup, board_revision: 1, size: 0, sha2_256: "0".repeat(64), attachment_ids: []}}),
    publishBackup: async () => { writes++; throw new ApiError("http_error", {status: 403}); }};
  const ui = await harness(api);
  const panel = ui.renderBoardActions(board, [], () => [], () => tag(board, 1), async () => {}, {sync() {}, require() {}});
  ui.nodes.main.append(panel);
  ui.find(panel, "button", "Create backup").click(); await ui.settled();
  const publish = ui.find(panel, "button", "Publish backup");
  publish.click(); await ui.settled();
  assert.equal(writes, 1);
  assert.equal(publish.disabled, false);
  assert.equal(ui.find(panel, "button", "Refresh publication status").hidden, true);
  assert.match(panel.textContent, /The service refused to publish revision 1/);
});

test("publication follows its original capture through navigation and a newer backup", async () => {
  let revision = 1;
  const backups = new Map();
  const ids = [];
  const reads = [];
  const dispatch = Promise.withResolvers();
  const finish = Promise.withResolvers();
  const status = (state) => ({data: {state, error_code: null}});
  const api = {createBackup: async (id) => { backups.set(id, revision); },
    getBackup: async (id) => backupReceipt(id, backups.get(id)),
    publishBackup: async (...args) => { ids.push(args); await dispatch.promise; },
    getPublication: async (...args) => {
      reads.push(args);
      if (reads.length === 2) return finish.promise;
      return status(reads.length === 1 ? "running" : "failed");
    }};
  const ui = await harness(api);
  const render = (id = board) => {
    const panel = ui.renderBoardActions(id, [], () => [], () => tag(id, revision), async () => {}, {sync() {}, require() {}});
    ui.nodes.main.replaceChildren(panel);
    return panel;
  };
  const panel = render();
  const control = (view, label) => ui.find(view, "button", label);
  control(panel, "Create backup").click(); await ui.settled();
  control(panel, "Publish backup").click();
  assert.equal(ui.busy(), false);
  const other = render(lane);
  assert.doesNotMatch(other.textContent, /Submitting publication/);
  const duringPost = render();
  assert.match(duringPost.textContent, /Submitting publication for revision 1/);
  revision = 2;
  control(duringPost, "Create backup").click(); await ui.settled();
  assert.match(duringPost.textContent, /Captured revision 2/);
  assert.match(duringPost.textContent, /Submitting publication for revision 1/);
  control(duringPost, "Publish backup").click();
  control(duringPost, "Refresh publication status").click();
  assert.equal(ids.length, 1);
  assert.deepEqual(reads, []);
  dispatch.resolve(); await ui.settled();
  assert.match(duringPost.textContent, /Publication running for revision 1/);

  control(duringPost, "Refresh publication status").click(); await ui.settled();
  const duringRead = render();
  control(duringRead, "Refresh publication status").click();
  assert.deepEqual(reads, [ids[0], ids[0]]);
  assert.equal(control(duringRead, "Publish backup").disabled, true);
  assert.match(duringRead.textContent, /Checking publication status for revision 1/);
  finish.resolve(status("succeeded")); await ui.settled();
  assert.match(duringRead.textContent, /Captured revision 2/);
  assert.match(duringRead.textContent, /Publication succeeded for revision 1/);
  assert.equal(backups.get(ids[0][2]), 1);
  control(duringRead, "Publish backup").click(); await ui.settled();
  assert.equal(ids.length, 2);
  assert.notEqual(ids[1][0], ids[0][0]);
  assert.equal(ids[1][1], ids[0][1]);
  assert.equal(backups.get(ids[1][2]), 2);
  assert.match(duringRead.textContent, /Publication failed for revision 2/);
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
    {user_id: user, capabilities: allowed ? ["read", "close_board", "create_card"] : ["read", "create_card"]},
    {user_id: priority, capabilities: ["close_board"]},
  ], create_grants: [{user_id: user, lane_id: lane}], move_grants: []}});
  api.getBoardAvailability = async () => {
    if (readError) throw readError;
    return {data: {...savedBoard}, etag: tag(board, savedBoard.revision)};
  };
  api.getBoardLanes = async () => {
    if (savedBoard.availability === "closed") {
      closedOperationalReads++;
      throw new ApiError("http_error", {status: 503});
    }
    const {data} = await baseRead();
    return {data: {...data, board: {...data.board, revision: savedBoard.revision}}, etag: tag(board, savedBoard.revision)};
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
  ui.find(main, "button", "Add card").click();
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

test("returning to a board during shutdown unlocks its staged files when the refusal arrives", async () => {
  const response = Promise.withResolvers();
  const {api} = boardApi();
  let requests = 0;
  const ui = await harness(workspaceServiceApi({
    ...api,
    getSession: async () => ({data: {kind: "human", principal_id: user, workspace_admin: true}}),
    getService: async () => ({data: runningService, etag: serviceTag}),
    listBuilds: async () => page("builds", []),
    stopService: async (_service, etag) => {
      assert.equal(etag, serviceTag);
      requests++;
      await response.promise;
      throw new ApiError("final_publication_failed", {status: 503});
    },
  }));
  await ui.load();
  const {main} = ui.nodes;
  ui.find(main, "button", "Add card").click();
  const originalPicker = ui.field(main, "Files to attach");
  originalPicker.files = [{name: "staged.txt", size: 1}];
  originalPicker.dispatch("change");

  await ui.navigate("/");
  const stop = ui.find(main, "button", "Shut down service");
  await until(() => !stop.disabled);
  stop.click();
  const back = ui.popstate("/boards/" + board);
  let picker;
  let remove;
  try {
    await until(() => ui.field(main, "Files to attach"));
    picker = ui.field(main, "Files to attach");
    remove = ui.find(main, "button", "Remove staged.txt");
    assert.equal(picker.disabled, true);
    assert.equal(remove.disabled, true);
  } finally {
    response.resolve();
    await back;
  }
  assert.equal(requests, 1);
  assert.equal(picker.disabled, false);
  assert.equal(remove.disabled, false);
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

test("drawer priority uses the card validator and shows the effective dependency floor", async () => {
  const {api} = boardApi();
  api.listComments = async () => page("comments", []);
  api.listAttachments = async () => page("attachments", []);
  const urgent = "1de08e49-ed9f-4822-b6b8-4981c57b133d";
  const low = "af036e96-7b57-49c9-b8ad-b17d574c1a62";
  const listPriorities = api.listPriorities;
  api.listPriorities = async () => page("priorities", [
    {id: urgent, label: "Urgent"}, ...(await listPriorities()).data.priorities, {id: low, label: "Low"},
  ]);
  const patchCard = api.patchCard;
  const getCard = api.getCard;
  let revision = 1;
  const writes = [];
  api.getCard = async () => ({...await getCard(), etag: tag(card, revision)});
  api.patchCard = async (id, etag, change) => {
    writes.push([id, etag, change.priority_id]);
    assert.equal(etag, tag(card, revision));
    const previous = (await getCard()).data.priority_id;
    const result = await patchCard(id, etag, {priority_id: change.priority_id === low ? priority : change.priority_id});
    if (result.data.priority_id !== previous) revision++;
    return {...result, etag: tag(card, revision)};
  };
  const ui = await harness(api);
  await ui.load();
  ui.find(ui.nodes.main, "button", "#1 Original").click();
  await ui.settled();
  const drawer = ui.nodes.details;
  const choice = ui.field(drawer, "Priority");
  const title = ui.field(drawer, "Title");
  assert.equal(choice.value, priority);
  title.value = "Keep my title draft";

  choice.value = urgent;
  ui.submit(drawer, "Set priority");
  await ui.settled();
  assert.match(drawer.textContent, /Effective priority: Urgent/);
  assert.equal(choice.value, urgent);

  choice.value = low;
  ui.submit(drawer, "Set priority");
  await ui.settled();
  assert.match(drawer.textContent, /Dependency floor kept the effective priority at Normal/);
  assert.equal(choice.value, priority);
  assert.equal(title.value, "Keep my title draft");

  choice.value = low;
  ui.submit(drawer, "Set priority");
  await ui.settled();
  assert.match(drawer.textContent, /Dependency floor kept the effective priority at Normal/);
  assert.equal(choice.value, priority);
  assert.equal(revision, 3);
  assert.deepEqual(writes, [[card, tag(card, 1), urgent], [card, tag(card, 2), low], [card, tag(card, 3), low]]);
});

test("drawer priority refreshes stale and uncertain results without replaying or claiming a floor", async () => {
  const {api} = boardApi();
  api.listComments = async () => page("comments", []);
  api.listAttachments = async () => page("attachments", []);
  const urgent = "1de08e49-ed9f-4822-b6b8-4981c57b133d";
  const listPriorities = api.listPriorities;
  api.listPriorities = async () => page("priorities", [
    {id: urgent, label: "Urgent"}, ...(await listPriorities()).data.priorities,
  ]);
  const writes = [];
  api.patchCard = async (id, etag, change) => {
    writes.push([id, etag, change.priority_id]);
    if (writes.length === 1) throw new ApiError("http_error", {status: 412});
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  const ui = await harness(api);
  await ui.load();
  ui.find(ui.nodes.main, "button", "#1 Original").click();
  await ui.settled();
  const drawer = ui.nodes.details;
  const choice = ui.field(drawer, "Priority");
  choice.value = urgent;

  ui.submit(drawer, "Set priority");
  await ui.settled();
  assert.match(ui.nodes.status.textContent, /State changed/);
  assert.equal(choice.value, urgent);
  assert.equal(writes.length, 1);

  ui.submit(drawer, "Set priority");
  await ui.settled();
  assert.match(ui.nodes.status.textContent, /outcome was uncertain/);
  assert.match(drawer.textContent, /Priority: Normal/);
  assert.doesNotMatch(drawer.textContent, /Dependency floor/);
  assert.equal(choice.value, urgent);
  assert.deepEqual(writes, [[card, tag(card, 1), urgent], [card, tag(card, 1), urgent]]);
});


for (const lostResponse of [false, true]) {
  test("drawer priority reconciles a same-address replacement after " + (lostResponse ? "a lost response" : "a confirmed write"), async () => {
    const {api} = boardApi();
    api.listComments = async () => page("comments", []);
    api.listAttachments = async () => page("attachments", []);
    const urgent = "1de08e49-ed9f-4822-b6b8-4981c57b133d";
    const listPriorities = api.listPriorities;
    api.listPriorities = async () => page("priorities", [
      {id: urgent, label: "Urgent"}, ...(await listPriorities()).data.priorities,
    ]);
    const patchCard = api.patchCard;
    const getCard = api.getCard;
    const started = Promise.withResolvers();
    const finish = Promise.withResolvers();
    let revision = 1;
    const writes = [];
    api.getCard = async () => ({...await getCard(), etag: tag(card, revision)});
    api.patchCard = async (id, etag, change) => {
      writes.push([id, etag, change.priority_id]);
      if (writes.length === 1) {
        started.resolve();
        await finish.promise;
      }
      const saved = await patchCard(id, etag, change);
      const reply = {...saved, etag: tag(card, ++revision)};
      if (lostResponse && writes.length === 1) throw new ApiError("transport_failed", {outcomeUnknown: true});
      return reply;
    };
    const ui = await harness(api);
    const {main, details} = ui.nodes;
    await ui.load();
    ui.find(main, "button", "#1 Original").click();
    await ui.settled();
    ui.field(details, "Title").value = "Keep this title draft";
    ui.field(details, "Priority").value = urgent;
    ui.submit(details, "Set priority");
    await started.promise;
    try {
      await ui.load();
      await new Promise((resolve) => setTimeout(resolve, 0));
      assert.match(details.textContent, /Priority: Normal/);
    } finally {
      finish.resolve();
    }
    await ui.settled();
    assert.match(details.textContent, /Priority: Urgent/);
    assert.equal(ui.field(details, "Title").value, "Keep this title draft");
    assert.deepEqual(writes, [[card, tag(card, 1), urgent]]);
    if (lostResponse) {
      assert.doesNotMatch(details.textContent, /Effective priority:|Dependency floor/);
      assert.match(details.textContent, /Compare the saved priority/);
    }
    else assert.match(details.textContent, /Effective priority: Urgent/);

    ui.field(details, "Priority").value = priority;
    ui.submit(details, "Set priority");
    await ui.settled();
    assert.deepEqual(writes, [[card, tag(card, 1), urgent], [card, tag(card, 2), priority]]);
  });
}

function boardStreams(api) {
  const streams = [];
  api.streamEvents = ({ boardId, watch, signal }) => new ReadableStream({
    start(controller) {
      let closed = false;
      const close = () => {
        if (closed) return;
        closed = true;
        controller.close();
      };
      signal.addEventListener("abort", close, {once: true});
      streams.push({boardId, watch, signal, close,
        sendCatalog(type) { controller.enqueue({type, resource: "catalog"}); },
        send(type, revision, id = boardId) {
          controller.enqueue({type, resource: "board", board_id: id, board_revision: revision});
        }});
    },
  }).values();
  return streams;
}

test("catalog hints refresh background Attention and the tab count without repainting the selected drawer", async (t) => {
  const {api} = boardApi();
  const streams = boardStreams(api);
  const selected = {id: board, name: "Selected", availability: "open",
    attention: {blocked: 1, needs_input: 2, ready_for_review: 0}, last_card_edit_at: "2026-11-11 00:00:00+00:00"};
  const other = {id: backup, name: "Background", availability: "open",
    attention: {blocked: 0, needs_input: 0, ready_for_review: 0}, last_card_edit_at: null};
  let rows = [selected, other];
  let catalogReads = 0;
  api.listBoards = async () => { catalogReads++; return page("boards", rows); };
  let boardReads = 0;
  const readBoard = api.getBoardLanes;
  api.getBoardLanes = async (...args) => { boardReads++; return readBoard(...args); };
  let drawerReads = 0;
  const readCard = api.getCard;
  api.getCard = async (...args) => { drawerReads++; return readCard(...args); };
  class Clock extends Date { static now() { return Date.parse("2026-11-11T00:07:00Z"); } }
  const ui = await harness(api, "/boards/" + board, {clock: Clock});
  t.after(ui.abortView);
  await ui.load();
  await until(() => streams.length === 1);
  const stream = streams[0];
  assert.equal(stream.boardId, board);
  assert.equal(stream.watch, undefined);
  stream.sendCatalog("snapshot");
  stream.send("snapshot", 1);
  await until(() => catalogReads === 2);
  assert.equal(ui.title(), "(3) localswim");

  const {boards, main, details} = ui.nodes;
  ui.find(main, "button", "#1 Original").click();
  await ui.settled();
  const draft = ui.field(details, "Title");
  draft.value = "Unsaved title";
  draft.focus();

  rows = [{...other, attention: {...other.attention, ready_for_review: 1},
    last_card_edit_at: "2026-11-11 00:04:00+00:00"}, selected];
  stream.sendCatalog("change");
  await until(() => ui.title() === "(4) localswim");
  const [background, current] = boards.querySelectorAll("a");
  assert.equal(background.dataset.boardId, backup);
  assert.equal(background.children[3].textContent, "1");
  assert.equal(background.title, "Last card edit 3 minutes ago");
  assert.equal(current.dataset.boardId, board);
  assert.equal(current.attributes["aria-current"], "page");
  assert.equal(details.hidden, false);
  assert.equal(ui.field(details, "Title"), draft);
  assert.equal(draft.value, "Unsaved title");
  assert.equal(ui.focused(), draft);
  assert.equal(boardReads, 1);
  assert.equal(drawerReads, 1);
  assert.equal(streams.length, 1);

  rows = [selected, {...other, availability: "closed", attention: null}];
  stream.sendCatalog("change");
  await until(() => ui.title() === "(3) localswim");
  const closed = boards.querySelectorAll("a")[1];
  assert.equal(closed.children[3].textContent, "—");
  assert.equal(closed.title, "Board closed. Card edit history unavailable");
  assert.equal(ui.focused(), draft);
});

test("scheduled catalog checks show background damage and same-revision repair without a stream hint", async (t) => {
  const {api} = boardApi();
  const selected = {id: board, name: "Selected", revision: 1, availability: "open",
    attention: {blocked: 0, needs_input: 0, ready_for_review: 0}, last_card_edit_at: null, error_code: null};
  const background = {...selected, id: backup, name: "Background",
    attention: {...selected.attention, ready_for_review: 2}};
  let rows = [selected, background];
  let requests = 0;
  const pending = Promise.withResolvers();
  const readStarted = Promise.withResolvers();
  api.listBoards = async (_cursor, signal) => {
    if (++requests === 3) {
      readStarted.resolve(signal);
      await pending.promise;
    }
    return page("boards", rows);
  };
  const waits = [];
  const ui = await harness(api, "/boards/" + board, {catalogTimer: (signal, delay) => {
    const tick = Promise.withResolvers();
    waits.push({...tick, signal, delay});
    return tick.promise;
  }});
  t.after(ui.abortView);
  await ui.load();
  const row = () => ui.nodes.boards.querySelectorAll("a").find((link) => link.dataset.boardId === backup);
  const healthyRow = row();
  assert.equal(healthyRow.children[3].textContent, "2");
  assert.equal(ui.title(), "(2) localswim");

  assert.equal(waits[0].delay, 15_000);
  waits[0].resolve();
  await until(() => waits.length === 2);
  assert.equal(row(), healthyRow);

  rows = [selected, {...background, availability: "unhealthy", attention: null,
    error_code: "durable_state_unavailable"}];
  waits[1].resolve();
  const readSignal = await readStarted.promise;
  assert.equal(waits.length, 2, "a pending catalog read must not schedule another poll");
  assert.equal(readSignal.aborted, false);
  pending.resolve();
  await until(() => waits.length === 3);
  assert.match(row().title, /Board unhealthy/);
  assert.deepEqual(row().children.slice(1, 4).map((slot) => slot.textContent), ["—", "—", "—"]);
  assert.equal(ui.title(), "localswim");
  assert.equal(ui.nodes.main.querySelector("#board-durable-health").hidden, true);

  rows = [selected, background];
  waits[2].resolve();
  await until(() => waits.length === 4);
  assert.equal(row().children[3].textContent, "2");
  assert.equal(ui.title(), "(2) localswim");
  ui.abortView();
  assert.equal(waits[3].signal.aborted, true);
  waits[3].resolve();
  await ui.settled();
  assert.equal(requests, 4);
});

test("selected durable health uses the catalog and preserves the open drawer and recovery controls", async (t) => {
  const {api} = boardApi();
  const healthy = {id: board, name: "Board", revision: 1, availability: "open",
    attention: {blocked: 0, needs_input: 0, ready_for_review: 0}, last_card_edit_at: null, error_code: null};
  let selected = healthy;
  api.listBoards = async () => page("boards", [selected]);
  const waits = [];
  const ui = await harness(api, "/boards/" + board, {catalogTimer: () => {
    const tick = Promise.withResolvers();
    waits.push(tick);
    return tick.promise;
  }});
  t.after(ui.abortView);
  await ui.load();
  const {main, details} = ui.nodes;
  const health = main.querySelector("#board-durable-health");
  const restore = ui.find(main, "button", "Restore native backup");
  assert.ok(restore);
  assert.equal(health.hidden, true);
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.field(details, "Comment"));
  const draft = ui.field(details, "Comment");
  draft.value = "Draft during repair";
  draft.focus();

  selected = {...healthy, revision: 2, availability: "unhealthy", attention: null, error_code: "durable_state_changed"};
  waits[0].resolve();
  await until(() => !health.hidden);
  assert.match(health.textContent, /differs from the last accepted state\. Showing previously loaded board data\./);
  assert.equal(ui.find(main, "button", "Restore native backup"), restore);
  assert.equal(ui.field(details, "Comment"), draft);
  assert.equal(ui.focused(), draft);

  await until(() => waits.length === 2);
  selected = {...selected, error_code: "durable_state_unavailable"};
  waits[1].resolve();
  await until(() => health.textContent.includes("snapshot is unavailable"));
  assert.equal(health.hidden, false);

  await until(() => waits.length === 3);
  selected = {...healthy, revision: 2};
  waits[2].resolve();
  await until(() => health.hidden);
  assert.equal(draft.value, "Draft during repair");
  assert.equal(ui.focused(), draft);
  assert.equal(health.textContent, "");
});

test("live catalog repaints keep pointer capture until a pinned drag ends", async (t) => {
  const {api} = boardApi();
  const streams = boardStreams(api);
  const selected = {id: board, name: "Selected"};
  const other = {id: backup, name: "Other"};
  let catalog = [selected, other];
  let catalogReads = 0;
  let workspace = {revision: 1, pinned_board_ids: [board, backup], publishing: publishingOff};
  const writes = [];
  api.getSession = async () => ({data: {kind: "human", principal_id: user, workspace_admin: true}});
  api.getWorkspace = async () => ({data: workspace, etag: tag("workspace-test-id", workspace.revision)});
  api.listBoards = async () => { catalogReads++; return page("boards", catalog); };
  api.setPinnedBoards = async (pins) => {
    writes.push(Array.from(pins));
    workspace = {...workspace, revision: workspace.revision + 1, pinned_board_ids: pins};
    return api.getWorkspace();
  };
  const ui = await harness(api, "/boards/" + board);
  t.after(ui.abortView);
  await ui.load();
  await until(() => streams.length === 1);
  const stream = streams[0];
  stream.sendCatalog("snapshot");
  stream.send("snapshot", 1);
  await until(() => catalogReads === 2);
  const currentLink = () => ui.nodes.boards.querySelectorAll("a").find(({dataset}) => dataset.boardId === board);
  const pointer = {isPrimary: true, button: 0, pointerId: 7};
  const grip = sidebarControl(ui, "Reorder Other");
  const target = currentLink();
  grip.dispatch("pointerdown", pointer);
  ui.pointerHit(target);
  grip.dispatch("pointermove", pointer);
  assert.equal(target.parent.attributes["data-pin-drop"], "true");

  catalog = [{...selected, attention: {blocked: 2, needs_input: 0, ready_for_review: 0}}, other];
  stream.sendCatalog("change");
  await until(() => ui.title() === "(2) localswim");
  assert.equal(grip.capturedPointer, 7);
  assert.equal(sidebarControl(ui, "Reorder Other"), grip);
  assert.equal(currentLink(), target);
  grip.dispatch("pointerup", pointer);
  await ui.settled();
  assert.deepEqual(writes, [[backup, board]]);
  assert.equal(currentLink().children[1].textContent, "2");
  assert.equal(ui.focused(), sidebarControl(ui, "Reorder Other"));

  const secondGrip = sidebarControl(ui, "Reorder Other");
  secondGrip.dispatch("pointerdown", pointer);
  catalog = [{...selected, attention: {blocked: 4, needs_input: 0, ready_for_review: 0}}, other];
  stream.sendCatalog("change");
  await until(() => ui.title() === "(4) localswim");
  assert.equal(sidebarControl(ui, "Reorder Other"), secondGrip);
  secondGrip.dispatch("lostpointercapture", pointer);
  assert.equal(currentLink().children[1].textContent, "4");
  assert.deepEqual(writes, [[backup, board]]);

  const oldGrip = sidebarControl(ui, "Reorder Other");
  oldGrip.dispatch("pointerdown", pointer);
  ui.pointerHit(currentLink());
  await ui.navigate("/");
  const newGrip = sidebarControl(ui, "Reorder Other");
  oldGrip.dispatch("pointerup", pointer);
  assert.equal(sidebarControl(ui, "Reorder Other"), newGrip);
  assert.deepEqual(writes, [[backup, board]]);
});

test("Workspace coalesces catalog hints and catches up from a reconnect snapshot", async (t) => {
  const {api} = boardApi();
  const streams = boardStreams(api);
  const started = Promise.withResolvers();
  const pending = Promise.withResolvers();
  let reads = 0;
  let review = 1;
  const catalog = (ready_for_review) => page("boards", [{id: board, name: "Board", availability: "open",
    attention: {blocked: 0, needs_input: 0, ready_for_review}, last_card_edit_at: null}]);
  api.listBoards = async () => {
    if (++reads === 2) {
      started.resolve();
      return pending.promise;
    }
    return catalog(review);
  };
  const ui = await harness(api, "/", {workspaceEvents: true});
  t.after(ui.abortView);
  await ui.load();
  await until(() => streams.length === 1);
  const [stream] = streams;
  assert.equal(stream.boardId, undefined);
  assert.equal(stream.watch, undefined);
  assert.equal(ui.title(), "(1) localswim");

  stream.sendCatalog("snapshot");
  await started.promise;
  review = 3;
  for (let count = 0; count < 5; count++) stream.sendCatalog("change");
  await ui.settled();
  assert.equal(reads, 2);
  assert.equal(ui.title(), "(1) localswim");
  pending.resolve(catalog(2));
  await until(() => ui.title() === "(3) localswim");
  assert.equal(reads, 3);

  stream.close();
  review = 0;
  await new Promise((resolve) => setTimeout(resolve, 550));
  await until(() => streams.length === 2);
  assert.equal(streams[1].boardId, undefined);
  streams[1].sendCatalog("snapshot");
  await until(() => ui.title() === "localswim");
  assert.equal(reads, 4);
});

test("an unavailable board route keeps receiving workspace Attention", async (t) => {
  const {api} = boardApi();
  const streams = boardStreams(api);
  api.getBoardAvailability = async () => { throw new ApiError("http_error", {status: 404}); };
  let blocked = 0;
  api.listBoards = async () => page("boards", [{id: backup, name: "Available", availability: "open",
    attention: {blocked, needs_input: 0, ready_for_review: 0}, last_card_edit_at: null}]);
  const ui = await harness(api, "/boards/" + board, {workspaceEvents: true});
  t.after(ui.abortView);
  await ui.load();
  await until(() => streams.length === 1);
  assert.equal(streams[0].boardId, undefined);
  const retry = ui.find(ui.nodes.main, "button", "Retry board update");
  assert.ok(retry);
  assert.equal(ui.title(), "localswim");

  blocked = 1;
  streams[0].sendCatalog("snapshot");
  await until(() => ui.title() === "(1) localswim");
  assert.equal(ui.find(ui.nodes.main, "button", "Retry board update"), retry);
});

test("a refused selected-board reconnect keeps a single catalog stream and the open draft", async (t) => {
  const {api} = boardApi();
  const streams = boardStreams(api);
  const connect = api.streamEvents;
  const attempts = [];
  let readable = true;
  api.streamEvents = (options) => {
    attempts.push(options);
    if (options.boardId === board && !readable) throw new ApiError("http_error", {status: 403});
    return connect(options);
  };
  const row = (id, name, ready_for_review) => ({id, name, availability: "open",
    attention: {blocked: 0, needs_input: 0, ready_for_review}, last_card_edit_at: null});
  let rows = [row(board, "Selected", 0), row(backup, "Other board", 0)];
  api.listBoards = async () => page("boards", rows);
  const ui = await harness(api);
  t.after(ui.abortView);
  await ui.load();
  await until(() => streams.length === 1);
  const [selected] = streams;
  selected.sendCatalog("snapshot");
  selected.send("snapshot", 1);
  const connection = ui.nodes.main.querySelector("#board-live-connection");
  await until(() => connection.textContent === "Live updates connected.");
  ui.find(ui.nodes.main, "button", "#1 Original").click();
  await ui.settled();
  const draft = ui.field(ui.nodes.details, "Title");
  draft.value = "Draft through refusal";
  draft.focus();

  readable = false;
  selected.close();
  await new Promise((resolve) => setTimeout(resolve, 550));
  await until(() => streams.length === 2);
  assert.deepEqual(attempts.map(({boardId}) => boardId), [board, board, undefined]);
  assert.equal(selected.signal.aborted, true);
  assert.equal(attempts[1].signal.aborted, true);
  assert.equal(connection.textContent, "Live updates for this board are unavailable.");
  const catalog = streams[1];
  rows = [row(backup, "Other board", 1)];
  catalog.sendCatalog("snapshot");
  await until(() => ui.title() === "(1) localswim");
  rows = [row(backup, "Other board", 2)];
  catalog.sendCatalog("change");
  await until(() => ui.title() === "(2) localswim");
  assert.equal(ui.nodes.details.hidden, false);
  assert.equal(ui.field(ui.nodes.details, "Title"), draft);
  assert.equal(draft.value, "Draft through refusal");
  assert.equal(ui.focused(), draft);
  assert.equal(attempts.length, 3);

  await ui.navigate("/");
  assert.equal(catalog.signal.aborted, true);
});

test("a single catalog hint recovers from a transient transport failure", async (t) => {
  const {api} = boardApi();
  const streams = boardStreams(api);
  const retried = Promise.withResolvers();
  const row = (ready_for_review) => ({id: board, name: "Board", availability: "open",
    attention: {blocked: 0, needs_input: 0, ready_for_review}, last_card_edit_at: null});
  let reads = 0;
  api.listBoards = async () => {
    reads++;
    if (reads === 2) throw new ApiError("transport_failed");
    if (reads === 3) retried.resolve();
    return page("boards", [row(reads === 1 ? 1 : 2)]);
  };
  const ui = await harness(api, "/", {workspaceEvents: true});
  t.after(ui.abortView);
  await ui.load();
  await until(() => streams.length === 1);
  assert.equal(ui.title(), "(1) localswim");
  streams[0].sendCatalog("snapshot");
  await retried.promise;
  await until(() => ui.title() === "(2) localswim");
  assert.equal(reads, 3);
});

test("leaving the view cancels a scheduled catalog retry", async (t) => {
  const {api} = boardApi();
  const streams = boardStreams(api);
  const failed = Promise.withResolvers();
  const signals = [];
  const row = (name) => ({id: board, name, availability: "open",
    attention: {blocked: 0, needs_input: 0, ready_for_review: 1}, last_card_edit_at: null});
  api.listBoards = async (_cursor, signal) => {
    signals.push(signal);
    if (signals.length === 2) {
      failed.resolve(signal);
      throw new ApiError("http_error", {status: 503});
    }
    return page("boards", [row(signals.length === 1 ? "Old view" : "Current view")]);
  };
  const ui = await harness(api);
  t.after(ui.abortView);
  await ui.load();
  await until(() => streams.length === 1);
  streams[0].sendCatalog("snapshot");
  const signal = await failed.promise;
  await ui.settled();
  await ui.navigate("/");
  assert.equal(signal.aborted, true);
  await new Promise((resolve) => setTimeout(resolve, 300));
  assert.equal(signals.length, 3);
  assert.ok(ui.find(ui.nodes.boards, "span", "Current view"));
});

test("a refused catalog continuation restarts from page one and discards partial rows", async (t) => {
  const {api} = boardApi();
  const streams = boardStreams(api);
  const secondPage = Promise.withResolvers();
  const pending = Promise.withResolvers();
  const cursors = [];
  const row = (id, name, ready_for_review) => ({id, name, availability: "open",
    attention: {blocked: 0, needs_input: 0, ready_for_review}, last_card_edit_at: null});
  api.listBoards = async (cursor) => {
    cursors.push(cursor);
    switch (cursors.length) {
      case 1: return page("boards", [row(board, "Last accepted", 1)]);
      case 2: return {data: {boards: [row(board, "Partial page", 9)], next_cursor: "stale"}, etag: null};
      case 3: secondPage.resolve(); return pending.promise;
      case 4: return {data: {boards: [row(backup, "Fresh first page", 2)], next_cursor: "fresh"}, etag: null};
      default: return page("boards", [row(board, "Fresh second page", 3)]);
    }
  };
  const ui = await harness(api, "/", {workspaceEvents: true});
  t.after(ui.abortView);
  await ui.load();
  await until(() => streams.length === 1);
  streams[0].sendCatalog("snapshot");
  await secondPage.promise;
  assert.equal(ui.title(), "(1) localswim");
  assert.equal(ui.nodes.boards.querySelector("a").children[0].textContent, "Last accepted");
  pending.reject(new ApiError("http_error", {status: 400}));

  await until(() => ui.title() === "(5) localswim");
  assert.deepEqual(cursors, [undefined, undefined, "stale", undefined, "fresh"]);
  assert.deepEqual(ui.nodes.boards.querySelectorAll("a").map((link) => link.children[0].textContent),
    ["Fresh first page", "Fresh second page"]);
});

function changingBoard() {
  const {api} = boardApi();
  const streams = boardStreams(api);
  const target = {id: backup, label: "Next lane", key: "next_lane", retired: false,
    position: 1, role: "ordinary", revision: 1};
  const listLanes = api.listLanes;
  api.listLanes = async () => page("lanes", [...(await listLanes()).data.lanes, target]);
  let revision = 1;
  let changes = {};
  const getBoard = api.getBoardLanes;
  api.getBoardLanes = async () => {
    const {data} = await getBoard();
    return {data: {board: {...data.board, revision}, cards: [{...data.cards[0], ...changes, revision}]},
      etag: tag(board, revision)};
  };
  const getCard = api.getCard;
  api.getCard = async () => ({data: {...(await getCard()).data, ...changes, revision}, etag: tag(card, revision)});
  return {api, streams, target,
    commit(title, lane_id = target.id) { changes = {title, lane_id}; return ++revision; }};
}

test("live changes coalesce behind one selected-board read and preserve open drafts", async (t) => {
  const {api, streams, target, commit} = changingBoard();
  const listLanes = api.listLanes;
  api.listLanes = async () => page("lanes", (await listLanes()).data.lanes.map((entry) =>
    entry.id === lane ? {...entry, reorder_enabled: true} : entry));
  const read = api.getBoardLanes;
  const firstRefresh = Promise.withResolvers();
  const release = Promise.withResolvers();
  let reads = 0;
  api.getBoardLanes = async (...args) => {
    const reply = await read(...args);
    if (++reads === 2) {
      firstRefresh.resolve();
      await release.promise;
    }
    const original = reply.data.cards[0];
    return {...reply, data: {...reply.data, cards: [
      original,
      {...original, id: user, ticket: 2, title: "First remaining card", lane_id: lane},
      {...original, id: priority, ticket: 3, title: "Second remaining card", lane_id: lane},
    ]}};
  };
  const ui = await harness(api);
  t.after(() => ui.navigate("/"));
  await ui.load();
  await until(() => streams.length === 1);
  const {main, details} = ui.nodes;
  assert.equal(streams[0].boardId, board);
  assert.match(main.querySelector("#board-live-connection").textContent, /Connecting/);
  streams[0].send("snapshot", 1);
  await until(() => main.querySelector("#board-live-connection").textContent === "Live updates connected.");
  assert.equal(reads, 1);
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.field(details, "Title"));
  const title = ui.field(details, "Title");
  const comment = ui.field(details, "Comment");
  ui.find(main, "button", "Add card").click();
  const newTitle = ui.field(main, "New title");
  const preview = () => main.querySelectorAll("input").find((input) => input.dataset.previewLane === lane);
  title.value = "Unsaved title";
  comment.value = "Unsaved comment";
  newTitle.value = "Unsaved new card";
  preview().value = "1";
  preview().focus();
  const columns = main.querySelectorAll("div").find((node) => node.className === "lanes");
  columns.scrollLeft = 80;
  columns.children[0].scrollTop = 120;

  streams[0].send("change", commit("First move"));
  await firstRefresh.promise;
  streams[0].send("change", commit("Second move"));
  streams[0].send("change", commit("Latest move"));
  streams[0].send("change", 100, priority);
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(reads, 2);
  release.resolve();
  await until(() => ui.find(main, "button", "#1 Latest move") && /Latest move/.test(details.textContent));
  assert.equal(reads, 3);
  assert.equal(ui.find(main, "button", "#1 Latest move").parent.querySelector("h2").ownText, target.label);
  assert.equal(ui.field(details, "Title"), title);
  assert.equal(title.value, "Unsaved title");
  assert.equal(comment.value, "Unsaved comment");
  assert.equal(newTitle.value, "Unsaved new card");
  assert.equal(preview().value, "1");
  assert.equal(ui.focused(), preview());
  assert.equal(columns.scrollLeft, 80);
  assert.equal(columns.children[0].scrollTop, 120);
});

test("a live drawer read failure preserves the original approval receipt inspection", async (t) => {
  const {api, streams, commit} = changingBoard();
  const listLanes = api.listLanes;
  api.listLanes = async () => page("lanes", (await listLanes()).data.lanes.map((entry) =>
    entry.id === lane ? {...entry, role: "ready_for_review"} : entry));
  const approvals = [];
  const inspections = [];
  let receiptAvailable = false;
  api.approveCard = async (...args) => {
    approvals.push(args);
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  api.inspectApproval = async (...args) => {
    inspections.push(args);
    if (!receiptAvailable) throw new ApiError("transport_failed");
  };
  const getCard = api.getCard;
  let failCardRead = false;
  api.getCard = async (id) => {
    if (failCardRead && id === card) {
      failCardRead = false;
      throw new ApiError("transport_failed");
    }
    return getCard(id);
  };
  const ui = await harness(api);
  t.after(() => ui.navigate("/"));
  await ui.load();
  await until(() => streams.length === 1);
  streams[0].send("snapshot", 1);
  const {main, details, status} = ui.nodes;
  const readiness = main.querySelector("#board-live-readiness");
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.field(details, "Title"));
  ui.submit(details, "Reviewed & approved");
  await ui.settled();
  assert.match(status.textContent, /Readback failed/);
  assert.equal(approvals.length, 1);
  const [originalReceipt] = approvals[0];
  const originalInspection = [originalReceipt, board, card];
  assert.deepEqual(inspections, [originalInspection]);

  failCardRead = true;
  streams[0].send("change", commit("Remote edit", lane));
  await until(() => /could not be completed/.test(readiness.textContent));
  assert.match(status.textContent, /Readback failed/);
  const retry = ui.find(main, "button", "Retry board update");
  failCardRead = true;
  retry.click();
  await until(() => !failCardRead && /could not be completed/.test(readiness.textContent));
  retry.click();
  await until(() => readiness.textContent === "" && /Remote edit/.test(details.textContent));
  assert.equal(ui.find(details, "button", "Reviewed & approved").disabled, true);

  ui.inspect();
  await ui.settled();
  assert.deepEqual(inspections, [originalInspection, originalInspection]);
  assert.equal(ui.find(details, "button", "Reviewed & approved").disabled, true);
  receiptAvailable = true;
  ui.inspect();
  await ui.settled();
  assert.equal(details.hidden, true);
  assert.deepEqual(inspections, [originalInspection, originalInspection, originalInspection]);
  assert.equal(approvals.length, 1);
});

test("hierarchy opens an archived child and returns to the parent with its draft", async () => {
  const {api} = boardApi();
  const getCard = api.getCard;
  const parent = {...(await getCard(card)).data, parent_id: null, child_ids: [backup]};
  const child = {...parent, id: backup, ticket: 8, title: "Archived child",
    archived: true, parent_id: parent.id, child_ids: []};
  api.getCard = async (id) => ({data: id === parent.id ? parent : child, etag: tag(id, 1)});

  const ui = await harness(api);
  await ui.load();
  const {main, details} = ui.nodes;
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.find(details, "a", "#8 Archived child"));
  const childLink = ui.find(details, "a", "#8 Archived child");
  assert.equal(childLink.parent.ownText, "Child ");
  assert.equal(childLink.href, "/boards/" + board + "?ticket=8");
  ui.field(details, "Description").value = "Parent draft";
  childLink.click();

  await until(() => ui.find(details, "a", "#1 Original"));
  const parentLink = ui.find(details, "a", "#1 Original");
  assert.equal(parentLink.parent.ownText, "Parent ");
  assert.ok(ui.find(details, "h2", "#8 Archived child"));
  assert.ok(ui.find(details, "p", "Archived"));
  parentLink.click();

  await until(() => ui.find(details, "h2", "#1 Original"));
  assert.equal(ui.field(details, "Description").value, "Parent draft");
});

test("new card resolves typed seed rows and submits one atomic create with the captured values", async () => {
  const {api} = boardApi();
  const original = (await api.getCard(card)).data;
  const target = {...original, id: backup, board_id: board, ticket: 8, archived: true};
  const reads = [];
  const writes = [];
  const lookup = Promise.withResolvers();
  api.listCards = async (id, query) => {
    reads.push([id, {...query}]);
    await lookup.promise;
    return page("cards", [target]);
  };
  api.createCard = async (draft) => { writes.push(draft); return createdCardReply(draft); };

  const ui = await harness(api);
  await ui.load();
  const {main} = ui.nodes;
  ui.find(main, "button", "Add card").click();
  const add = ui.find(main, "button", "Add relationship");
  const rows = () => add.parent.querySelectorAll("fieldset");
  ui.field(main, "New title").value = "Seeded card";
  add.click();
  ui.field(rows()[0], "Target ticket").value = "8";
  ui.field(rows()[0], "Link type").value = "blocks";
  ui.field(rows()[1], "Target ticket").value = "8";
  ui.field(rows()[1], "Link type").value = "references";
  ui.submit(main, "Create card");
  await until(() => reads.length === 1);
  ui.field(rows()[1], "Link type").value = "duplicates";
  lookup.resolve();
  await ui.settled();

  assert.deepEqual(reads, [[board, {ticket: 8, archived: "include", limit: 2}]]);
  assert.equal(writes.length, 1);
  const {id, ...request} = writes[0];
  assert.match(id, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.equal(request.expected_board_etag, tag(board, 1));
  assert.equal(request.lane_id, lane);
  assert.equal(request.title, "Seeded card");
  assert.deepEqual(Array.from(request.relationships, ({target_card_id, kind}) => [target_card_id, kind]).flat(),
    [target.id, "blocks", target.id, "references"]);
  assert.match(ui.nodes.status.textContent, /Card created/);
  assert.equal(rows().length, 1);
  assert.equal(ui.field(rows()[0], "Target ticket").value, "8");
  assert.equal(ui.field(rows()[0], "Link type").value, "duplicates");
});

test("withdrawing relationship permission preserves seed drafts and still permits a plain card", async () => {
  const {api} = boardApi();
  const readPolicy = api.getPolicy;
  let canLink = true;
  api.getPolicy = async () => {
    const reply = await readPolicy();
    if (!canLink) reply.data.resource_grants[0].capabilities = ["read", "create_card"];
    return reply;
  };
  const writes = [];
  api.createCard = async (draft) => { writes.push(draft); return createdCardReply(draft); };
  let targetReads = 0;
  api.listCards = async () => {
    targetReads++;
    return page("cards", []);
  };
  const ui = await harness(api);
  await ui.load();
  const {main, refresh} = ui.nodes;
  ui.find(main, "button", "Add card").click();
  const panel = ui.find(main, "button", "Add relationship").parent;
  const target = ui.field(panel, "Target ticket");
  panel.open = true;
  target.value = "0";
  assert.equal(target.checkValidity(), false);
  canLink = false;
  refresh.click();
  await until(() => panel.hidden);
  assert.equal(target.value, "0");
  assert.equal(target.willValidate, false);
  assert.equal(target.checkValidity(), true);

  ui.field(main, "New title").value = "Plain card";
  ui.submitValidated(main, "Create card");
  await ui.settled();
  assert.equal(writes.length, 1);
  assert.equal(writes[0].relationships, undefined);
  assert.equal(writes[0].expected_board_etag, tag(board, 1));
  assert.equal(targetReads, 0);

  canLink = true;
  refresh.click();
  await until(() => !panel.hidden);
  assert.equal(target.value, "0");
  assert.equal(target.willValidate, true);
  assert.equal(target.checkValidity(), false);
  ui.field(main, "New title").value = "Still blocked";
  ui.submitValidated(main, "Create card");
  assert.equal(writes.length, 1);
});

test("invalid seed target preserves all drafts and sends no create", async () => {
  const {api} = boardApi();
  const original = (await api.getCard(card)).data;
  api.listCards = async (_id, {ticket}) => page("cards", ticket === original.ticket ? [original] : []);
  let writes = 0;
  api.createCard = async () => { writes++; };

  const ui = await harness(api);
  await ui.load();
  const {main} = ui.nodes;
  ui.find(main, "button", "Add card").click();
  const rows = () => ui.find(main, "button", "Add relationship").parent.querySelectorAll("fieldset");
  ui.find(main, "button", "Add relationship").click();
  ui.field(main, "New title").value = "Draft with missing target";
  ui.field(rows()[0], "Target ticket").value = "1";
  ui.field(rows()[1], "Target ticket").value = "999";
  ui.field(rows()[1], "Link type").value = "duplicates";
  ui.submit(main, "Create card");
  await ui.settled();

  assert.equal(writes, 0);
  assert.match(ui.nodes.status.textContent, /existing ticket/);
  await ui.navigate("/");
  await ui.navigate("/boards/" + board);
  assert.equal(ui.field(main, "New title").value, "Draft with missing target");
  assert.equal(ui.field(rows()[0], "Target ticket").value, "1");
  assert.equal(ui.field(rows()[1], "Target ticket").value, "999");
  assert.equal(ui.field(rows()[1], "Link type").value, "duplicates");
});

test("uncertain seeded create reads the original ID and reconciles the reopened form without replay", async () => {
  const {api} = boardApi();
  const getCard = api.getCard;
  const original = (await getCard(card)).data;
  const dispatched = Promise.withResolvers();
  const finishWrite = Promise.withResolvers();
  const writes = [];
  const readbacks = [];
  api.listCards = async () => page("cards", [original]);
  api.createCard = async (draft) => {
    writes.push(draft);
    dispatched.resolve();
    await finishWrite.promise;
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  api.getCard = async (id) => {
    readbacks.push(id);
    return {data: {...original, id, title: "Created before the response was lost"}, etag: tag(id, 2)};
  };

  const ui = await harness(api);
  await ui.load();
  const {main} = ui.nodes;
  ui.find(main, "button", "Add card").click();
  const rows = () => ui.find(main, "button", "Add relationship").parent.querySelectorAll("fieldset");
  ui.field(main, "New title").value = "Create with readback";
  ui.field(main, "New description").value = "Submitted description";
  ui.field(rows()[0], "Target ticket").value = "1";
  ui.submit(main, "Create card");
  await dispatched.promise;
  await ui.navigate("/");
  await ui.navigate("/boards/" + board);
  ui.find(main, "button", "Add relationship").click();
  ui.field(rows()[1], "Target ticket").value = "5";
  ui.field(main, "New description").value = "Edited after returning";
  finishWrite.resolve();
  await ui.settled();

  assert.equal(writes.length, 1);
  assert.deepEqual(readbacks, [writes[0].id]);
  assert.equal(ui.field(main, "New title").value, "");
  assert.equal(ui.field(main, "New description").value, "Edited after returning");
  assert.equal(rows().length, 1);
  assert.equal(ui.field(rows()[0], "Target ticket").value, "5");
  ui.submitValidated(main, "Create card");
  assert.equal(writes.length, 1);
});


test("acknowledged seeded create reconciles the originating draft while another board is open", async () => {
  const {api} = boardApi();
  const other = "4cbd90c1-e555-4431-8be2-cf9a96a6bc09";
  const availability = await api.getBoardAvailability();
  const lanes = await api.getBoardLanes();
  const policy = await api.getPolicy();
  const original = (await api.getCard(card)).data;
  api.listBoards = async () => page("boards", [{id: board, name: "Board"}, {id: other, name: "Other board"}]);
  api.getBoardAvailability = async (id) => id === board ? availability
    : {data: {...availability.data, id: other, policy_id: other, name: "Other board"}, etag: tag(other, 1)};
  api.getBoardLanes = async (id) => id === board ? lanes
    : {data: {board: {...lanes.data.board, id: other, policy_id: other, name: "Other board"}, cards: []},
      etag: tag(other, 1)};
  api.getPolicy = async (id) => id === backup ? policy : {data: {...policy.data, id: other, board_id: other}};
  api.listCards = async () => page("cards", [original]);
  const dispatched = Promise.withResolvers();
  const finishWrite = Promise.withResolvers();
  const writes = [];
  api.createCard = async (draft) => {
    writes.push(draft);
    dispatched.resolve();
    await finishWrite.promise;
    return createdCardReply(draft);
  };
  let readbacks = 0;
  api.getCard = async () => { readbacks++; };

  const ui = await harness(api, "/boards/" + other);
  await ui.load();
  const {main} = ui.nodes;
  function fillDraft() {
    ui.find(main, "button", "Add card").click();
    ui.field(main, "New title").value = "Same title on both boards";
    ui.field(main, "New description").value = "Same description on both boards";
    ui.field(main, "Target ticket").value = "1";
  }
  fillDraft();
  await ui.navigate("/boards/" + board);
  fillDraft();
  ui.submitValidated(main, "Create card");
  await dispatched.promise;
  ui.field(main, "New title").value = "Edited origin title";
  await ui.navigate("/boards/" + other);
  finishWrite.resolve();
  await ui.settled();

  assert.equal(ui.field(main, "New title").value, "Same title on both boards");
  assert.equal(ui.field(main, "New description").value, "Same description on both boards");
  assert.equal(ui.field(main, "Target ticket").value, "1");
  await ui.navigate("/boards/" + board);
  assert.equal(ui.field(main, "New title").value, "Edited origin title");
  assert.equal(ui.field(main, "New description").value, "");
  assert.equal(ui.field(main, "Target ticket").value, "");
  assert.equal(writes.length, 1);
  assert.equal(readbacks, 0);
});

test("live inbound link repaint preserves direction, dirty selection, focus and revision", async (t) => {
  const {api, streams, commit} = changingBoard();
  const edited = {id: backup, board_id: board, source_card_id: user, target_card_id: card,
    kind: "relates_to", revision: 1};
  const unchanged = {id: priority, board_id: board, source_card_id: card, target_card_id: priority,
    kind: "duplicates", revision: 1};
  let links = [edited, unchanged];
  let linkedTitle = "Linked card";
  const getCard = api.getCard;
  api.getCard = async (id) => {
    if (id === user) return {data: {id, board_id: board, ticket: 2, title: linkedTitle}};
    if (id === priority) return {data: {id, board_id: board, ticket: 3, title: "Other linked card"}};
    return getCard(id);
  };
  api.listRelationships = async () => page("relationships", links);
  const changes = [];
  api.replaceRelationship = async (id, revision, boardTag, kind) => {
    changes.push([id, revision, boardTag, kind]);
    const saved = links.find((entry) => entry.id === id);
    if (revision !== saved.revision) throw new ApiError("http_error", {status: 412});
    const committed = commit("Original", lane);
    links = links.map((entry) => entry.id === id ? {...entry, revision: committed, kind} : entry);
  };

  const ui = await harness(api);
  t.after(() => ui.navigate("/"));
  await ui.load();
  await until(() => streams.length === 1);
  streams[0].send("snapshot", 1);
  const {main, details, status} = ui.nodes;
  const readiness = main.querySelector("#board-live-readiness");
  const row = () => ui.find(details, "a", "#2 " + linkedTitle)?.parent.parent;
  const selector = () => row() && ui.field(row(), "Link type (source to target)");
  const savedType = () => row()?.querySelector("p").ownText;
  const staleNotice = () => ui.find(row(), "p", "Saved link changed. Review the saved type and change the selection before saving.");
  ui.find(main, "button", "#1 Original").click();
  await until(selector);
  assert.deepEqual(Array.from(selector().options, (option) => option.value), ["blocks", "duplicates", "references", "relates_to"]);
  selector().value = "blocks";
  selector().dispatch("change");
  selector().focus();

  linkedTitle = "Renamed linked card";
  links = [unchanged, edited];
  streams[0].send("change", commit("Original", lane));
  await until(() => selector() && readiness.textContent === "");
  assert.equal(selector().value, "blocks");
  assert.equal(ui.focused(), selector());
  assert.equal(savedType(), "Relates to ");
  assert.equal(staleNotice().hidden, true);

  links = [unchanged, {...edited, kind: "references", revision: 3}];
  streams[0].send("change", commit("Original", lane));
  await until(() => savedType() === "Other references this card " && readiness.textContent === "");
  assert.equal(selector().value, "blocks");
  assert.equal(ui.focused(), selector());
  assert.equal(staleNotice().hidden, false);
  ui.submit(row(), "Change link type");
  await ui.settled();
  assert.deepEqual(changes, [[edited.id, 1, tag(board, 3), "blocks"]]);
  assert.match(status.textContent, /State changed/);
  assert.equal(selector().value, "blocks");
  assert.equal(savedType(), "Other references this card ");

  selector().value = "duplicates";
  selector().dispatch("change");
  assert.equal(staleNotice().hidden, true);
  ui.submit(row(), "Change link type");
  await ui.settled();
  assert.deepEqual(changes[1], [edited.id, 3, tag(board, 3), "duplicates"]);
  assert.equal(savedType(), "Other duplicates this card ");
  assert.equal(links.find((entry) => entry.id === edited.id).source_card_id, user);
  assert.match(status.textContent, /Link type changed/);
});

test("live board Retry and reconnect catch up without discarding the loaded board", async (t) => {
  const {api, streams, commit} = changingBoard();
  const read = api.getBoardLanes;
  let fail = false;
  let lists = 0;
  const listBoards = api.listBoards;
  api.listBoards = async (...args) => { lists++; return listBoards(...args); };
  api.getBoardLanes = (...args) => {
    if (fail) throw new ApiError("transport_failed");
    return read(...args);
  };
  const stream = api.streamEvents;
  let rejectOldSession = false;
  api.streamEvents = (options) => {
    if (rejectOldSession) {
      rejectOldSession = false;
      throw new ApiError("http_error", {status: 401});
    }
    return stream(options);
  };
  let sessions = 0;
  const ui = await harness(api, "/boards/" + board, {
    openSession: async () => "capability-" + ++sessions,
  });
  t.after(() => ui.navigate("/"));
  await ui.load();
  await until(() => streams.length === 1);
  const {main} = ui.nodes;
  const connection = main.querySelector("#board-live-connection");
  const readiness = main.querySelector("#board-live-readiness");
  const retry = ui.find(main, "button", "Retry board update");
  const draft = ui.field(main, "New description");
  draft.value = "Keep my draft";
  streams[0].send("snapshot", 1);
  fail = true;
  streams[0].send("change", commit("After retry"));
  await until(() => /could not be completed/.test(readiness.textContent));
  assert.equal(connection.textContent, "Live updates connected.");
  assert.ok(ui.find(main, "button", "#1 Original"));
  assert.equal(retry.hidden, false);

  fail = false;
  retry.click();
  await until(() => retry.hidden && ui.find(main, "button", "#1 After retry"));
  rejectOldSession = true;
  streams[0].close();
  await until(() => /disconnected/.test(connection.textContent));
  assert.ok(ui.find(main, "button", "#1 After retry"));
  assert.equal(retry.hidden, false);
  const missed = commit("While disconnected");
  await new Promise((resolve) => setTimeout(resolve, 550));
  await until(() => streams.length === 2);
  streams[1].send("snapshot", missed);
  await until(() => retry.hidden && ui.find(main, "button", "#1 While disconnected"));
  assert.equal(connection.textContent, "Live updates connected.");
  assert.equal(draft.value, "Keep my draft");
  assert.equal(lists, 2); // Initial catalog plus the manual retry. A board hint alone does not invalidate Attention.
  assert.equal(sessions, 2);
});

test("navigating away aborts the selected stream and discards its in-flight read", async (t) => {
  const {api, streams, commit} = changingBoard();
  const read = api.getBoardLanes;
  const started = Promise.withResolvers();
  const release = Promise.withResolvers();
  let reads = 0;
  api.getBoardLanes = async (boardId, signal) => {
    const reply = await read(boardId);
    if (++reads === 2) {
      started.resolve(signal);
      await release.promise;
    }
    return reply;
  };
  const ui = await harness(api);
  t.after(() => ui.navigate("/"));
  await ui.load();
  await until(() => streams.length === 1);
  const old = streams[0];
  old.send("snapshot", 1);
  old.send("change", commit("Obsolete"));
  const signal = await started.promise;
  old.send("change", commit("Current"));
  await ui.navigate("/");
  assert.equal(old.signal.aborted, true);
  assert.equal(signal.aborted, true);
  await ui.navigate("/boards/" + board);
  await until(() => streams.length === 2);
  streams[1].send("snapshot", 3);
  release.resolve();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.ok(ui.find(ui.nodes.main, "button", "#1 Current"));
  assert.equal(ui.find(ui.nodes.main, "button", "#1 Obsolete"), undefined);
  assert.equal(reads, 3);
});

test("a selected board stream renews a refused session before reading its snapshot", async (t) => {
  const {api, streams, commit} = changingBoard();
  const stream = api.streamEvents;
  let attempts = 0;
  api.streamEvents = (options) => {
    if (++attempts === 1) throw new ApiError("http_error", {status: 401});
    return stream(options);
  };
  let sessions = 0;
  const ui = await harness(api, "/boards/" + board, {
    openSession: async () => "capability-" + ++sessions,
  });
  t.after(() => ui.navigate("/"));
  await ui.load();
  await until(() => streams.length === 1);
  streams[0].send("snapshot", commit("Changed before subscribing"));
  await until(() => ui.find(ui.nodes.main, "button", "#1 Changed before subscribing"));
  assert.equal(sessions, 2);
  assert.equal(attempts, 2);
});

test("live close and reopen keep the selected card and its draft", async (t) => {
  const {api, savedBoard} = boardAvailabilityApi();
  const streams = boardStreams(api);
  const ui = await harness(api);
  t.after(() => ui.navigate("/"));
  await ui.load();
  await until(() => streams.length === 1);
  const {main, details} = ui.nodes;
  streams[0].send("snapshot", 1);
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.field(details, "Comment"));
  ui.field(details, "Comment").value = "Draft while closed";

  savedBoard.availability = "closed";
  streams[0].send("change", ++savedBoard.revision);
  await until(() => streams.length === 2);
  assert.match(main.textContent, /This board is closed/);
  assert.equal(details.hidden, true);
  assert.equal(streams[0].signal.aborted, true);
  streams[1].send("snapshot", savedBoard.revision);
  savedBoard.availability = "open";
  streams[1].send("change", ++savedBoard.revision);
  await until(() => streams.length === 3 && ui.field(details, "Comment"));
  assert.equal(ui.field(details, "Comment").value, "Draft while closed");
  assert.equal(streams[1].signal.aborted, true);
});

test("foreground readback waits for a live read and satisfies its queued invalidations", async (t) => {
  const {api, streams, commit} = changingBoard();
  const read = api.getBoardLanes;
  const entered = Promise.withResolvers();
  const release = Promise.withResolvers();
  let reads = 0;
  api.getBoardLanes = async (...args) => {
    const reply = await read(...args);
    if (++reads === 2) {
      entered.resolve();
      await release.promise;
    }
    return reply;
  };
  let writes = 0;
  api.createCard = async (draft) => { writes++; return createdCardReply(draft); };
  const ui = await harness(api);
  t.after(() => ui.navigate("/"));
  await ui.load();
  await until(() => streams.length === 1);
  const {main} = ui.nodes;
  ui.find(main, "button", "Add card").click();
  streams[0].send("snapshot", 1);
  streams[0].send("change", commit("Paused live read"));
  await entered.promise;
  const title = ui.field(main, "New title");
  title.value = "Card being created";
  ui.submit(main, "Create card");
  await until(() => writes === 1 && title.value === "");
  streams[0].send("change", commit("Latest saved revision"));
  assert.equal(ui.busy(), true);
  assert.equal(reads, 2);
  release.resolve();
  await ui.settled();
  assert.equal(reads, 3);
  assert.ok(ui.find(main, "button", "#1 Latest saved revision"));
  assert.match(ui.nodes.status.textContent, /Card created/);
});

test("live invalidation deferred by a write survives until the write ends", async () => {
  const controller = new AbortController();
  let revision = 1;
  let writing = true;
  let reads = 0;
  const updates = createBoardRefresh({signal: controller.signal, revision: () => revision,
    canReadAutomatically: () => !writing, report: () => {},
    read: async (automatic) => {
      assert.equal(automatic, true);
      reads++;
      revision = 3;
      return true;
    }});
  updates.invalidate(2, false);
  updates.invalidate(3, false);
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(reads, 0);
  writing = false;
  updates.resume();
  await until(() => revision === 3);
  assert.equal(reads, 1);
  controller.abort();
});

test("M63 lane dependency reorder hides disabled lanes and requires two active cards", async () => {
  const {api} = boardApi();
  const original = (await api.getCard()).data;
  const readLanes = api.getBoardLanes;
  let cardCount = 1;
  const second = {...original, id: backup, ticket: 2, title: "Blocker"};
  api.getBoardLanes = async () => {
    const reply = await readLanes();
    const cards = cardCount === 1 ? [original] : [original, second];
    return {...reply, data: {...reply.data, cards}};
  };
  const readLaneList = api.listLanes;
  api.listLanes = async () => {
    const first = (await readLaneList()).data.lanes[0];
    return page("lanes", [
      {...first, label: "Backlog", reorder_enabled: true},
      {...first, id: user, label: "Second lane", position: 1, reorder_enabled: false},
    ]);
  };
  const current = [{card_id: card, ticket: 1}, {card_id: backup, ticket: 2}];
  const proposed = [{card_id: backup, ticket: 1}, {card_id: card, ticket: 2}];
  const previews = [];
  const applies = [];
  let previewRevision = 2;
  api.previewTicketOrder = async (...args) => {
    previews.push(args);
    return {data: {board_revision: previewRevision, current, proposed, changed: true}, etag: tag(board, previewRevision)};
  };
  api.applyTicketOrder = async (...args) => {
    applies.push(args);
    throw new ApiError("http_error", {status: 412});
  };
  const ui = await harness(api);
  const {main} = ui.nodes;
  const previewButton = () => ui.find(main, "button", "Preview ticket order");
  await ui.load();
  assert.equal(main.querySelectorAll("button").filter((control) => control.dataset.action === "preview-ticket-order").length, 1);
  assert.equal(previewButton().disabled, true);
  previewButton().click();
  assert.equal(previews.length, 0);
  assert.equal(ui.find(main, "button", "Apply ticket order"), undefined);

  cardCount = 2;
  await ui.load();
  assert.equal(previewButton().disabled, false);
  const input = main.querySelectorAll("input").find((control) => control.attributes["aria-label"] === "Tickets to preview in Backlog");
  input.value = "1";
  previewButton().click();
  assert.equal(previews.length, 0);
  assert.match(main.textContent, /Choose at least two active tickets/);
  input.value = "";
  previewButton().click();
  await ui.settled();
  assert.deepEqual(previews, [[board, lane, undefined]]);
  assert.match(main.textContent, /The board changed/);
  assert.equal(ui.find(main, "button", "Apply ticket order"), undefined);

  previewRevision = 1;
  previewButton().click();
  await ui.settled();
  assert.match(main.textContent, /Preview at board revision 1/);
  ui.submit(main, "Apply ticket order");
  await ui.settled();
  assert.deepEqual(applies, [[board, lane, tag(board, 1), proposed]]);
});

test("late ticket-order Apply respects a restored unresolved board submission", async () => {
  const storage = tabStorage();
  storage.setItem(recoveredSubmissions, JSON.stringify([["board:" + board,
    {kind: "create-card", id: priority, submittedAt: Date.now(), fingerprint: "a".repeat(64)}]]));
  const {api, ui} = await attachmentUploadFixture(board, {storage});
  const [source] = (await api.listLanes()).data.lanes;
  api.listLanes = async () => page("lanes", [{...source, reorder_enabled: true}]);
  const pendingPreview = Promise.withResolvers();
  api.previewTicketOrder = () => pendingPreview.promise;
  const applies = [];
  api.applyTicketOrder = async (...args) => {
    applies.push(args);
    throw new ApiError("http_error", {status: 412});
  };
  await ui.load();
  const {main} = ui.nodes;
  await until(() => ui.find(main, "button", "Preview ticket order"));
  ui.find(main, "button", "Preview ticket order").click();
  const current = [{card_id: card, ticket: 1}, {card_id: backup, ticket: 2}];
  const proposed = [{card_id: backup, ticket: 1}, {card_id: card, ticket: 2}];
  pendingPreview.resolve({data: {board_revision: 1, current, proposed, changed: true}, etag: tag(board, 1)});
  await until(() => ui.find(main, "button", "Apply ticket order"));
  const apply = ui.find(main, "button", "Apply ticket order");
  assert.equal(apply.disabled, true);
  apply.disabled = false;
  ui.submit(main, "Apply ticket order");
  await ui.settled();
  assert.deepEqual(applies, []);
  ui.find(main, "button", "Dismiss unresolved action").click();
  ui.submit(main, "Apply ticket order");
  await ui.settled();
  assert.deepEqual(applies, [[board, lane, tag(board, 1), proposed]]);
});

const uploadBoard = "b4979c23-4be2-4553-bbeb-66a47b7c021c";
const recoveredUploads = "localswim:tab-uploads";
const uploadDigests = new WeakMap();

function uploadFile({name, size, type, fill = 0}) {
  const bytes = new Uint8Array(size).fill(fill);
  const file = new File([bytes], name, {type});
  uploadDigests.set(file, createHash("sha256").update(bytes).digest("hex"));
  return file;
}

async function attachmentUploadFixture(secondBoard = board, options) {
  const {api} = boardApi();
  const initial = (await api.getCard(card)).data;
  const cards = new Map([
    [card, {...initial, attachment_count: 0}],
    [backup, {...initial, id: backup, board_id: secondBoard, title: "Other card", ticket: 2, attachment_count: 0}],
  ]);
  const revisions = new Map([[board, 1], [secondBoard, 1]]);
  const saved = new Map();
  const calls = [];
  const readbacks = [];
  const boardData = (id) => ({id, name: id === board ? "Board" : "Other board", policy_id: backup, revision: revisions.get(id)});
  api.listCards = async (id, {ticket, archived} = {}) => page("cards", [...cards.values()].filter((entry) =>
    entry.board_id === id && (ticket === undefined || entry.ticket === ticket) && (archived !== "only" || entry.archived)));
  api.listBoards = async () => page("boards", [...revisions.keys()].map(boardData));
  api.getBoardAvailability = async (id) => ({
    data: {...boardData(id), availability: "open", policy_id: priority}, etag: tag(id, revisions.get(id)),
  });
  api.getBoardLanes = async (id) => ({data: {board: boardData(id),
    cards: [...cards.values()].filter((entry) => entry.board_id === id)}, etag: tag(id, revisions.get(id))});
  api.getCard = async (id) => {
    const saved = cards.get(id);
    if (!saved) throw new ApiError("http_error", {status: 404});
    return {data: saved, etag: tag(id, saved.revision)};
  };
  api.listAttachments = async (id) => page("attachments", [...saved.values()].filter((entry) => entry.card_id === id));
  api.getAttachment = async (id) => {
    readbacks.push(id);
    const item = saved.get(id);
    if (!item) throw new ApiError("http_error", {status: 404});
    return {data: item, etag: tag(id, item.revision)};
  };
  function commit(id, file, attachmentId) {
    const prior = cards.get(id);
    const revision = revisions.get(prior.board_id) + 1;
    revisions.set(prior.board_id, revision);
    cards.set(id, {...prior, revision, attachment_count: prior.attachment_count + 1});
    const item = {
      id: attachmentId, card_id: id, board_id: prior.board_id, original_name: file.name, size: file.size, revision,
      sha2_256: uploadDigests.get(file),
      added_by: user, active: true, previewable: false, created_at: "2026-11-10 00:00:00+00:00",
    };
    saved.set(attachmentId, item);
    return {data: item};
  }
  const behavior = {write: commit};
  api.uploadAttachment = async (id, expected, file, attachmentId) => {
    calls.push({id, expected, name: file.name, attachmentId});
    assert.equal(expected, tag(id, cards.get(id).revision));
    assert.match(attachmentId, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    return behavior.write(id, file, attachmentId);
  };
  let getCapability;
  async function open() {
    const ui = await harness((readCapability) => {
      getCapability = readCapability;
      return api;
    }, "/boards/" + board, options);
    await ui.load();
    await until(() => ui.find(ui.nodes.main, "button", "#1 Original"));
    if (!ui.find(ui.nodes.details, "button", "Upload attachments")) {
      ui.find(ui.nodes.main, "button", "#1 Original").click();
      await until(() => ui.find(ui.nodes.details, "button", "Upload attachments"));
    }
    return ui;
  }
  const ui = await open();
  const choose = (...files) => {
    ui.field(ui.nodes.details, "Files").files = files.map(uploadFile);
    return ui.submit(ui.nodes.details, "Upload attachments");
  };
  const paste = (files, types = ["Files"], target = ui.nodes.details) =>
    ui.nodes.details.dispatch("paste", {target, clipboardData: {files: files.map(uploadFile), types}});
  return {ui, api, cards, calls, readbacks, saved, behavior, commit, choose, paste, getCapability, open};
}

test("restored unresolved comment blocks only its own card from dropping", async () => {
  const storage = tabStorage();
  storage.setItem(recoveredSubmissions, JSON.stringify([["card:" + board + ":" + card,
    {kind: "comment", id: priority, submittedAt: Date.now()}]]));
  const {api, ui, cards} = await attachmentUploadFixture(board, {storage});
  const [source] = (await api.listLanes()).data.lanes;
  const target = {...source, id: uploadBoard, key: "next", label: "Next lane", position: 1};
  api.listLanes = async () => page("lanes", [source, target]);
  grantMove(api, target.id);
  const moves = [];
  api.patchCard = async (id, validator, patch) => {
    moves.push([id, validator, patch.lane_id]);
    cards.set(id, {...cards.get(id), ...patch});
    return api.getCard(id);
  };
  await ui.load();
  const {main, details, status} = ui.nodes;
  await until(() => ui.find(details, "button", "Close card"));
  ui.find(details, "button", "Close card").click();
  const drop = (title) => {
    const dataTransfer = dragTransfer();
    const opener = ui.find(main, "button", title);
    const destination = ui.find(main, "h2", target.label).parent;
    opener.dispatch("dragstart", {dataTransfer});
    destination.dispatch("dragover", {dataTransfer});
    destination.dispatch("drop", {dataTransfer});
    assert.equal(destination.dataset.dropTarget, undefined);
    assert.equal(opener.dataset.dragging, undefined);
  };

  drop("#1 Original");
  await ui.settled();
  assert.match(status.textContent, /Inspect saved card before moving it/);
  assert.deepEqual(moves, []);
  drop("#2 Other card");
  await ui.settled();
  assert.deepEqual(moves, [[backup, tag(backup, 1), target.id]]);
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.find(details, "button", "Dismiss unresolved action"));
  ui.find(details, "button", "Dismiss unresolved action").click();
  drop("#1 Original");
  await ui.settled();
  assert.deepEqual(moves, [[backup, tag(backup, 1), target.id], [card, tag(card, 1), target.id]]);
});

test("M60 fences a possibly in-flight upload after reload and resumes only the matching original", async () => {
  const storage = tabStorage();
  const {calls, readbacks, commit, choose, behavior, open} = await attachmentUploadFixture(board, {storage});
  const original = {name: "original.txt", size: 8, fill: 65};
  const pending = {name: "pending.txt", size: 8, fill: 66};
  const sent = Promise.withResolvers();
  behavior.write = (...args) => {
    sent.resolve(args);
    return new Promise(() => {});
  };
  choose(original, pending);
  const sentArgs = await sent.promise;
  const [first, remaining] = JSON.parse(storage.getItem(recoveredUploads))[0][1];
  assert.deepEqual(Object.keys(first).sort(), ["attempted", "digest", "id", "name", "size"]);
  assert.equal(first.id, calls[0].attachmentId);
  assert.equal(first.attempted, true);
  assert.equal(remaining.attempted, false);

  const ui = await open();
  const {details, status} = ui.nodes;
  const inspect = ui.find(details, "button", "Inspect saved card");
  const resume = ui.find(details, "button", "Resume pending uploads");
  assert.equal(ui.find(details, "button", "Upload attachments").disabled, true);
  assert.equal(resume.parent.hidden, true);
  assert.equal(ui.find(details, "button", "Dismiss unresolved action").hidden, true);
  inspect.click();
  await ui.settled();
  assert.match(status.textContent, /may still be running/);
  assert.deepEqual(JSON.parse(storage.getItem(recoveredUploads))[0][1], [first, remaining]);
  assert.equal(ui.find(details, "button", "Upload attachments").disabled, true);
  assert.equal(resume.parent.hidden, true);
  assert.equal(calls.length, 1);

  // The original request commits without returning a response to its page.
  commit(...sentArgs);
  inspect.click();
  await ui.settled();
  assert.match(status.textContent, /Original submission confirmed/);
  assert.deepEqual(JSON.parse(storage.getItem(recoveredUploads))[0][1], [remaining]);
  assert.match(details.textContent, /1 of 2 files processed/);
  assert.equal(resume.parent.hidden, false);

  const select = (file) => { ui.field(details, "Files").files = [uploadFile(file)]; };
  select({...pending, fill: 67});
  resume.click();
  await until(() => !ui.uploadingToCard(board, card));
  assert.match(details.textContent, /must match its recorded name, size and contents/);
  assert.equal(calls.length, 1);
  assert.deepEqual(JSON.parse(storage.getItem(recoveredUploads))[0][1], [remaining]);

  behavior.write = commit;
  select(pending);
  resume.click();
  await until(() => calls.length === 2 && !ui.uploadingToCard(board, card));
  assert.deepEqual(calls.map(({id, attachmentId, expected}) => [id, attachmentId, expected]),
    [[card, first.id, tag(card, 1)], [card, remaining.id, tag(card, 2)]]);
  assert.deepEqual(readbacks, [first.id, first.id, remaining.id]);
  assert.deepEqual(JSON.parse(storage.getItem(recoveredUploads)), []);
  assert.equal(ui.find(details, "button", "Upload attachments").disabled, false);
});

test("M60 locates an attachment after its commit and a reload before its response", async () => {
  const storage = tabStorage();
  const {calls, readbacks, commit, choose, behavior, open, saved} = await attachmentUploadFixture(board, {storage});
  const written = Promise.withResolvers();
  behavior.write = (...args) => {
    written.resolve(commit(...args).data);
    return new Promise(() => {});
  };
  choose({name: "saved.txt", size: 4, fill: 65});
  const item = await written.promise;
  const ui = await open();
  const {details, status} = ui.nodes;
  const inspect = ui.find(details, "button", "Inspect saved card");
  saved.set(item.id, {...item, sha2_256: "0".repeat(64)});
  inspect.click();
  await ui.settled();
  assert.match(status.textContent, /Saved state could not be read/);
  assert.equal(ui.find(details, "button", "Upload attachments").disabled, true);

  saved.set(item.id, item);
  inspect.click();
  await ui.settled();
  assert.match(status.textContent, /Original submission confirmed/);
  assert.deepEqual(readbacks, [item.id, item.id]);
  assert.deepEqual(calls.map(({attachmentId}) => attachmentId), [item.id]);
  assert.deepEqual(JSON.parse(storage.getItem(recoveredUploads)), []);
  assert.equal(ui.find(details, "button", "Upload attachments").disabled, false);
});

test("M60 refuses to send bytes when the tab cannot store recovery details", async () => {
  const storage = tabStorage();
  const setItem = storage.setItem;
  storage.setItem = (key, value) => {
    if (key === recoveredUploads) throw new Error("Tab storage is full");
    setItem(key, value);
  };
  const {ui, calls, choose} = await attachmentUploadFixture(board, {storage});
  choose({name: "pending.txt", size: 1});
  await until(() => !ui.uploadingToCard(board, card));
  assert.match(ui.nodes.details.textContent, /could not save upload recovery details/);
  assert.deepEqual(calls, []);
});

test("M60 keeps a refused upload resumable after the board refresh is inspected", async () => {
  const storage = tabStorage();
  const {ui, api, calls, readbacks, behavior, commit, choose} = await attachmentUploadFixture(board, {storage});
  const {details} = ui.nodes;
  const original = {name: "pending.txt", size: 4, fill: 65};
  const readBoard = api.getBoardLanes;
  let closed = false;
  api.getBoardLanes = async (...args) => {
    if (closed) throw new ApiError("unavailable", {status: 503});
    return readBoard(...args);
  };
  const refused = createBrowserApi({getCapability: () => "capability", fetchImpl: async () => {
    closed = true;
    return Response.json({
      type: "about:blank", title: "Service Unavailable", status: 503, code: "unavailable",
      detail: "Board unavailable.", request_id: user, outcome: "not_applied", errors: [],
    }, {status: 503, headers: {"Content-Type": "application/problem+json"}});
  }});
  behavior.write = (id, file, attachmentId) => refused.uploadAttachment(id, tag(id, 1), file, attachmentId);
  choose(original);
  await until(() => calls.length === 1 && !ui.uploadingToCard(board, card));
  const [{id, attempted}] = JSON.parse(storage.getItem(recoveredUploads))[0][1];
  assert.equal(attempted, false);
  assert.match(details.textContent, /Refused\. The board is unavailable/);
  assert.deepEqual(readbacks, []);

  const resume = ui.find(details, "button", "Resume pending uploads");
  const discard = ui.find(details, "button", "Discard pending files");
  assert.equal(resume.parent.hidden, true);
  assert.equal(resume.disabled, true);
  assert.equal(discard.disabled, true);
  closed = false;
  commit(card, uploadFile({name: "other.txt", size: 1}), priority);
  ui.find(details, "button", "Inspect saved card").click();
  await ui.settled();
  assert.equal(resume.parent.hidden, false);
  assert.equal(resume.disabled, false);
  assert.equal(discard.disabled, false);

  behavior.write = commit;
  ui.field(details, "Files").files = [uploadFile(original)];
  resume.click();
  await until(() => calls.length === 2 && !ui.uploadingToCard(board, card));
  assert.deepEqual(calls.map(({attachmentId, expected}) => [attachmentId, expected]),
    [[id, tag(card, 1)], [id, tag(card, 2)]]);
  assert.deepEqual(readbacks, [id]);
  assert.deepEqual(JSON.parse(storage.getItem(recoveredUploads)), []);
});

test("M48 drag waits for the source card's upload and permits an unrelated card move", async () => {
  const {ui, api, cards, behavior, commit, choose} = await attachmentUploadFixture();
  const [source] = (await api.listLanes()).data.lanes;
  const target = {...source, id: uploadBoard, key: "next", label: "Next lane", position: 1};
  api.listLanes = async () => page("lanes", [source, target]);
  grantMove(api, target.id);
  const writes = [];
  api.patchCard = async (id, expected, patch) => {
    writes.push([id, expected, patch.lane_id]);
    cards.set(id, {...cards.get(id), ...patch});
    return api.getCard(id);
  };
  await ui.load();
  const {main, details, status} = ui.nodes;
  await until(() => ui.find(details, "button", "Upload attachments"));
  const pending = Promise.withResolvers();
  behavior.write = async (...args) => {
    await pending.promise;
    return commit(...args);
  };
  choose({name: "pending.txt", size: 1});
  const drop = (title) => {
    const opener = ui.find(main, "button", title);
    const next = ui.find(main, "h2", target.label).parent;
    const dataTransfer = dragTransfer();
    opener.dispatch("dragstart", {dataTransfer});
    next.dispatch("dragover", {dataTransfer});
    next.dispatch("drop", {dataTransfer});
    assert.equal(opener.dataset.dragging, undefined);
    assert.equal(next.dataset.dropTarget, undefined);
  };
  try {
    drop("#1 Original");
    await ui.settled();
    assert.deepEqual(writes, []);
    assert.match(status.textContent, /Wait for attachments/);
    drop("#2 Other card");
    await ui.settled();
    assert.deepEqual(writes, [[backup, tag(backup, 1), target.id]]);
    assert.equal(ui.uploadingToCard(board, card), true);
  } finally {
    pending.resolve();
    await until(() => !ui.uploadingToCard(board, card));
  }
});

async function newCardFileFixture() {
  const fixture = await attachmentUploadFixture();
  const {ui, api, cards} = fixture;
  const {main} = ui.nodes;
  const writes = [];
  function create(request) {
    assert.equal(request.expected_board_etag, tag(board, 1));
    const saved = {...cards.get(card), id: request.id, title: request.title,
      description: request.description, ticket: 3, attachment_count: 0};
    cards.set(saved.id, saved);
    return {data: saved, etag: tag(saved.id, saved.revision)};
  }
  const creation = {write: create};
  api.createCard = async (request) => {
    writes.push(request);
    return creation.write(request);
  };
  ui.find(main, "button", "Add card").click();
  ui.field(main, "New title").value = "Files on a new card";
  function choose(...files) {
    const picker = ui.field(main, "Files to attach");
    assert.equal(picker.multiple, true);
    picker.files = files.map(uploadFile);
    picker.dispatch("change");
  }
  function paste(files, types = ["Files"]) {
    const target = ui.field(main, "New description");
    return target.parent.parent.parent.dispatch("paste", {target, clipboardData: {files: files.map(uploadFile), types}});
  }
  return {...fixture, writes, creation, create, choose, paste};
}

test("New card stages selected files and a pasted image and keeps earlier uploads after an interruption", async () => {
  const {ui, writes, calls, choose, paste, commit, behavior, saved, readbacks} = await newCardFileFixture();
  const {main, details} = ui.nodes;
  const final = Promise.withResolvers();
  behavior.write = async (id, file, attachmentId) => {
    if (file.type === "image/png") {
      await final.promise;
      throw new ApiError("transport_failed", {outcomeUnknown: true});
    }
    return commit(id, file, attachmentId);
  };
  choose({name: "first.txt", size: 1}, {name: "second.txt", size: 2}, {name: "remove.txt", size: 3});
  ui.find(main, "button", "Remove remove.txt").click();
  const image = {name: "paste.png", size: 4, type: "image/png"};
  assert.equal(paste([image], ["text/plain", "Files"]), false);
  assert.equal(paste([image]), true);
  assert.match(main.textContent, /first\.txt.*second\.txt.*paste\.png/s);
  assert.equal(calls.length, 0);

  ui.submit(main, "Create card");
  ui.submit(main, "Create card");
  await until(() => calls.length === 3 && ui.find(details, "h2", "#3 Files on a new card"));
  assert.equal(writes.length, 1);
  const created = writes[0].id;
  assert.deepEqual(calls.map(({id, name, expected}) => [id, name, expected]), [
    [created, "first.txt", tag(created, 1)], [created, "second.txt", tag(created, 2)],
    [created, "paste.png", tag(created, 3)],
  ]);
  final.resolve();
  await until(() => !ui.uploadingToCard(board, created));
  assert.deepEqual([...saved.values()].map(({card_id, original_name}) => [card_id, original_name]), [
    [created, "first.txt"], [created, "second.txt"],
  ]);
  assert.deepEqual(readbacks, [calls[2].attachmentId]);
  assert.match(details.textContent, /first\.txt — Uploaded\./);
  assert.match(details.textContent, /second\.txt — Uploaded\./);
  assert.match(details.textContent, /paste\.png — Outcome uncertain/);
  assert.equal(ui.find(details, "button", "Post comment").disabled, true);
  assert.equal(ui.find(main, "button", "Remove paste.png"), undefined);
  assert.equal(writes.length, 1);
});

test("New card upload waits for exact creation readback and starts once after explicit inspection", async () => {
  const {ui, api, calls, writes, choose, create, creation} = await newCardFileFixture();
  const {main, details} = ui.nodes;
  const readCard = api.getCard;
  const response = Promise.withResolvers();
  let readable = false;
  const readbacks = [];
  api.getCard = async (id) => {
    if (id === writes[0]?.id) {
      readbacks.push(id);
      if (!readable) throw new ApiError("transport_failed");
    }
    return readCard(id);
  };
  creation.write = async (request) => {
    create(request);
    await response.promise;
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  choose({name: "after-readback.txt", size: 1});
  const picker = ui.field(main, "Files to attach");
  const remove = ui.find(main, "button", "Remove after-readback.txt");
  assert.equal(picker.disabled, false);
  assert.equal(remove.disabled, false);
  ui.submit(main, "Create card");
  assert.equal(picker.disabled, true);
  assert.equal(remove.disabled, true);
  await until(() => writes.length === 1);
  response.resolve();
  await ui.settled();
  assert.equal(writes.length, 1);
  const created = writes[0].id;
  assert.deepEqual(readbacks, [created]);
  assert.equal(calls.length, 0);
  assert.equal(ui.find(main, "button", "Create card").disabled, true);
  assert.equal(picker.disabled, true);
  assert.equal(remove.disabled, true);
  remove.click();
  assert.equal(ui.find(main, "button", "Remove after-readback.txt"), remove);

  readable = true;
  ui.find(main, "button", "Inspect saved board").click();
  await until(() => calls.length === 1 && !ui.uploadingToCard(board, created));
  await ui.settled();
  assert.equal(picker.disabled, false);
  assert.equal(ui.find(main, "button", "Remove after-readback.txt"), undefined);
  assert.equal(writes.length, 1);
  assert.equal(readbacks[1], created);
  assert.equal(calls[0].id, created);
  assert.match(details.textContent, /after-readback\.txt — Uploaded\./);
});

test("dismissing an unresolved create unlocks its retained staged files", async () => {
  const {ui, calls, writes, choose, creation} = await newCardFileFixture();
  const {main} = ui.nodes;
  creation.write = async () => { throw new ApiError("transport_failed", {outcomeUnknown: true}); };
  choose({name: "retained.txt", size: 1});
  const picker = ui.field(main, "Files to attach");
  const remove = ui.find(main, "button", "Remove retained.txt");
  ui.submit(main, "Create card");
  await ui.settled();
  assert.equal(picker.disabled, true);
  assert.equal(remove.disabled, true);

  ui.find(main, "button", "Dismiss unresolved action").click();
  assert.equal(picker.disabled, false);
  assert.equal(remove.disabled, false);
  assert.equal(writes.length, 1);
  assert.equal(calls.length, 0);
});

test("staged files follow the original creation across board navigation", async () => {
  const {ui, calls, writes, choose, create, creation} = await newCardFileFixture();
  const {main, details} = ui.nodes;
  const acknowledged = Promise.withResolvers();
  creation.write = async (request) => {
    await acknowledged.promise;
    return create(request);
  };
  choose({name: "original-board.txt", size: 1});
  await ui.navigate("/");
  await ui.navigate("/boards/" + board);
  assert.ok(ui.find(main, "button", "Remove original-board.txt"));
  ui.submit(main, "Create card");
  await until(() => writes.length === 1);
  await ui.navigate("/");
  acknowledged.resolve();
  const created = writes[0].id;
  await until(() => calls.length === 1 && !ui.uploadingToCard(board, created));
  assert.equal(calls[0].id, created);
  assert.equal(details.hidden, true);
  await ui.navigate("/boards/" + board);
  assert.equal(ui.find(main, "button", "Remove original-board.txt"), undefined);
  ui.find(main, "button", "#3 Files on a new card").click();
  await until(() => ui.find(details, "h2", "#3 Files on a new card"));
  assert.match(details.textContent, /original-board\.txt — Uploaded\./);
});

test("a stale create retains its staged files without dispatching an upload", async () => {
  const {ui, writes, calls, choose, creation} = await newCardFileFixture();
  const {main} = ui.nodes;
  creation.write = async () => { throw new ApiError("http_error", {status: 412}); };
  choose({name: "stay-staged.txt", size: 1});
  ui.submit(main, "Create card");
  await ui.settled();
  assert.equal(writes.length, 1);
  assert.equal(calls.length, 0);
  assert.equal(ui.field(main, "New title").value, "Files on a new card");
  assert.equal(ui.field(main, "Files to attach").disabled, false);
  assert.equal(ui.find(main, "button", "Remove stay-staged.txt").disabled, false);
});

test("attachment picker preflights every file, permits exactly 10 MiB and serializes by card ETag", async () => {
  const {ui, calls, behavior, commit, choose} = await attachmentUploadFixture();
  const first = Promise.withResolvers();
  behavior.write = async (id, file, attachmentId) => {
    if (file.name === "limit.dat") await first.promise;
    if (file.name === "refused.dat") throw new ApiError("invalid_input", {status: 422});
    return commit(id, file, attachmentId);
  };
  const draft = ui.field(ui.nodes.details, "Comment");
  draft.value = "Keep the draft";
  assert.equal(ui.field(ui.nodes.details, "Files").multiple, true);
  choose({name: "limit.dat", size: MAX_ATTACHMENT_BYTES}, {name: "oversized.dat", size: MAX_ATTACHMENT_BYTES + 1},
    {name: "refused.dat", size: 1}, {name: "last.dat", size: 1});
  await until(() => calls.length === 1);
  assert.match(ui.nodes.details.textContent, /limit\.dat — Uploading…/);
  assert.match(ui.nodes.details.textContent, /oversized\.dat — Refused\. Files are limited to 10 MiB/);
  assert.match(ui.nodes.details.textContent, /last\.dat — Waiting/);
  assert.equal(ui.find(ui.nodes.details, "button", "Post comment").disabled, true);
  assert.equal(ui.find(ui.nodes.details, "button", "Close card").disabled, false);
  first.resolve();
  await until(() => !ui.uploadingToCard(board, card));
  assert.deepEqual(calls.map(({name, expected}) => [name, expected]), [
    ["limit.dat", tag(card, 1)], ["refused.dat", tag(card, 2)], ["last.dat", tag(card, 2)],
  ]);
  assert.match(ui.nodes.details.textContent, /4 of 4 files processed/);
  assert.match(ui.nodes.details.textContent, /refused\.dat — Upload refused/);
  assert.match(ui.nodes.details.textContent, /last\.dat — Uploaded/);
  assert.equal(ui.find(ui.nodes.details, "button", "Post comment").disabled, false);
  assert.equal(draft.value, "Keep the draft");
});

test("attachment paste preserves ordinary text paste and independent card uploads across board navigation", async () => {
  const {ui, calls, behavior, commit, paste} = await attachmentUploadFixture(uploadBoard);
  const first = Promise.withResolvers();
  behavior.write = async (id, file, attachmentId) => {
    if (file.name === "origin.png") await first.promise;
    return commit(id, file, attachmentId);
  };
  const editor = ui.field(ui.nodes.details, "Description");
  const clip = {name: "origin.png", type: "image/png", size: 20};
  assert.equal(paste([], ["text/plain"], editor), false);
  assert.equal(paste([clip], ["Files", "text/plain"], editor), false);
  assert.equal(paste([{name: "plain.txt", type: "text/plain", size: 20}]), false);
  assert.equal(calls.length, 0);
  assert.equal(paste([clip, {...clip, name: "origin-second.png"}]), true);
  await ui.navigate("/boards/" + uploadBoard);
  ui.find(ui.nodes.main, "button", "#2 Other card").click();
  await until(() => ui.find(ui.nodes.details, "button", "Upload attachments"));
  assert.doesNotMatch(ui.nodes.details.textContent, /origin\.png/);
  assert.equal(paste([{...clip, name: "other.png"}]), true);
  await until(() => !ui.uploadingToCard(uploadBoard, backup));
  assert.equal(ui.uploadingToCard(board, card), true);
  assert.match(ui.nodes.details.textContent, /other\.png — Uploaded/);
  first.resolve();
  await until(() => !ui.uploadingToCard(board, card));
  assert.deepEqual(calls.map(({id, expected, name}) => [id, expected, name]), [
    [card, tag(card, 1), "origin.png"],
    [backup, tag(backup, 1), "other.png"],
    [card, tag(card, 2), "origin-second.png"],
  ]);
  assert.doesNotMatch(ui.nodes.details.textContent, /origin\.png/);
  await ui.navigate("/boards/" + board);
  await until(() => /origin-second\.png — Uploaded/.test(ui.nodes.details.textContent));
  assert.doesNotMatch(ui.nodes.details.textContent, /other\.png/);
});

test("a late upload refusal keeps the renewed capability for a write on another card", async () => {
  let sessions = 0;
  const {ui, api, cards, behavior, commit, choose, calls, readbacks, getCapability} =
    await attachmentUploadFixture(board, {openSession: async () => "session-" + ++sessions});
  const first = Promise.withResolvers();
  const refused = Promise.withResolvers();
  const uploadCapabilities = [];
  behavior.write = async (...args) => {
    uploadCapabilities.push(getCapability());
    if (args[1].name === "first.txt") {
      await first.promise;
      return commit(...args);
    }
    await refused.promise;
  };
  const otherWrites = [];
  api.patchCard = async (id, expected, draft) => {
    otherWrites.push([id, expected, getCapability()]);
    const saved = {...cards.get(id), ...draft};
    cards.set(id, saved);
    return {data: saved, etag: tag(id, saved.revision)};
  };
  async function renewCapability(prior) {
    await ui.readWithSession(async () => {
      if (getCapability() === prior) throw new ApiError("http_error", {status: 401});
    });
  }
  choose({name: "first.txt", size: 1}, {name: "late.txt", size: 1}, {name: "waiting.txt", size: 1});
  await until(() => uploadCapabilities.length === 1);
  assert.deepEqual(uploadCapabilities, ["session-1"]);
  await renewCapability("session-1");
  first.resolve();
  await until(() => uploadCapabilities.length === 2);
  assert.deepEqual(uploadCapabilities, ["session-1", "session-2"]);
  await renewCapability("session-2");
  ui.find(ui.nodes.main, "button", "#2 Other card").click();
  await until(() => /#2 Other card/.test(ui.nodes.details.textContent));
  refused.reject(new ApiError("http_error", {status: 401}));
  await until(() => !ui.uploadingToCard(board, card));
  assert.equal(getCapability(), "session-3");
  ui.field(ui.nodes.details, "Title").value = "Updated other card";
  ui.submit(ui.nodes.details, "Save text");
  await ui.settled();
  assert.deepEqual(otherWrites, [[backup, tag(backup, 1), "session-3"]]);
  assert.equal(sessions, 3);
  assert.deepEqual(calls.map(({name}) => name), ["first.txt", "late.txt"]);
  assert.deepEqual(readbacks, []);
});

test("attachment upload confirms a lost response by original UUID before advancing", async () => {
  const {ui, calls, readbacks, behavior, commit, choose} = await attachmentUploadFixture();
  behavior.write = (id, file, attachmentId) => {
    const reply = commit(id, file, attachmentId);
    if (file.name === "lost.dat") throw new ApiError("transport_failed", {outcomeUnknown: true});
    return reply;
  };
  choose({name: "lost.dat", size: 1}, {name: "next.dat", size: 2});
  await until(() => !ui.uploadingToCard(board, card));
  assert.equal(calls.length, 2);
  assert.equal(calls[1].expected, tag(card, 2));
  assert.deepEqual(readbacks, [calls[0].attachmentId]);
  assert.match(ui.nodes.details.textContent, /lost\.dat — Uploaded\. Confirmed by readback/);
  assert.match(ui.nodes.details.textContent, /next\.dat — Uploaded/);
});

test("attachment upload rejects foreign identity and requires inspection before another write", async () => {
  const {ui, calls, readbacks, behavior, saved, commit, choose} = await attachmentUploadFixture();
  let committed;
  behavior.write = (id, file, attachmentId) => {
    committed = commit(id, file, attachmentId).data;
    const foreign = {...committed, board_id: uploadBoard};
    saved.set(attachmentId, foreign);
    return {data: foreign};
  };
  choose({name: "inspect.dat", size: 1}, {name: "stop.dat", size: 1});
  await until(() => !ui.uploadingToCard(board, card));
  assert.equal(calls.length, 1);
  assert.deepEqual(readbacks, [calls[0].attachmentId]);
  assert.match(ui.nodes.details.textContent, /inspect\.dat — Outcome uncertain/);
  assert.match(ui.nodes.details.textContent, /stop\.dat — Upload stopped/);
  assert.equal(ui.find(ui.nodes.details, "button", "Upload attachments").disabled, true);
  ui.find(ui.nodes.details, "button", "Inspect saved card").click();
  await ui.settled();
  assert.equal(ui.find(ui.nodes.details, "button", "Upload attachments").disabled, true);
  saved.set(calls[0].attachmentId, committed);
  ui.find(ui.nodes.details, "button", "Inspect saved card").click();
  await ui.settled();
  assert.equal(ui.find(ui.nodes.details, "button", "Resume pending uploads").parent.hidden, false);
  assert.match(ui.nodes.details.textContent, /inspect\.dat — Uploaded\. Confirmed by readback/);
  assert.equal(calls.length, 1);
});

test("attachment upload stops before the next file when the card revision changed after a confirmed upload", async () => {
  const {ui, cards, calls, behavior, commit, choose} = await attachmentUploadFixture();
  behavior.write = (id, file, attachmentId) => {
    const reply = commit(id, file, attachmentId);
    const current = cards.get(id);
    cards.set(id, {...current, revision: current.revision + 1});
    return reply;
  };
  choose({name: "saved.dat", size: 1}, {name: "stopped.dat", size: 1});
  await until(() => !ui.uploadingToCard(board, card));
  assert.equal(calls.length, 1);
  assert.match(ui.nodes.details.textContent, /saved\.dat — Uploaded/);
  assert.match(ui.nodes.details.textContent, /stopped\.dat — Upload stopped/);
  assert.match(ui.nodes.details.textContent, /The card changed during upload/);
});

test("lane approval waits only for uploads on its rendered card members", async () => {
  const {ui, api, cards, calls, behavior, commit, choose} = await attachmentUploadFixture();
  const ready = (await api.listLanes()).data.lanes[0];
  const completed = "a57df7d8-0476-44d6-97a4-3b2e5486650a";
  const other = cards.get(backup);
  cards.set(backup, {...other, lane_id: uploadBoard});
  api.listLanes = async () => page("lanes", [
    {...ready, role: "ready_for_review", approval_enabled: true},
    {...ready, id: uploadBoard, key: "other", position: 1},
    {...ready, id: completed, key: "completed", role: "completed", position: 2},
  ]);
  const approvals = [];
  api.approveLane = async (...args) => { approvals.push(args[5]); };
  await ui.load();
  await until(() => ui.find(ui.nodes.details, "button", "Upload attachments"));
  const memberUpload = Promise.withResolvers();
  const otherUpload = Promise.withResolvers();
  behavior.write = async (id, file, attachmentId) => {
    await (id === card ? memberUpload.promise : otherUpload.promise);
    return commit(id, file, attachmentId);
  };
  choose({name: "member.txt", size: 1});
  ui.submit(ui.nodes.main, "Approve all in lane");
  await ui.settled();
  assert.equal(approvals.length, 0);
  assert.match(ui.nodes.status.textContent, /Wait for attachments/);
  memberUpload.resolve();
  await until(() => !ui.uploadingToCard(board, card));
  ui.find(ui.nodes.main, "button", "#2 Other card").click();
  await until(() => /#2 Other card/.test(ui.nodes.details.textContent));
  choose({name: "unrelated.txt", size: 1});
  ui.submit(ui.nodes.main, "Approve all in lane");
  await ui.settled();
  assert.deepEqual(approvals[0].map(({id}) => id), [card]);
  assert.equal(ui.uploadingToCard(board, backup), true);
  assert.equal(calls.length, 2);
  otherUpload.resolve();
  await until(() => !ui.uploadingToCard(board, backup));
});

test("relationship writes wait for uploads on either endpoint and allow unrelated endpoints", async () => {
  const {ui, api, cards, behavior, commit, choose} = await attachmentUploadFixture();
  const third = "a57df7d8-0476-44d6-97a4-3b2e5486650a";
  cards.set(third, {...cards.get(backup), id: third, ticket: 3, title: "Unrelated"});
  const relationships = [
    {id: uploadBoard, board_id: board, source_card_id: card, target_card_id: backup, kind: "relates_to", revision: 1},
  ];
  api.listRelationships = async () => page("relationships", relationships);
  const writes = [];
  api.replaceRelationship = async (...args) => { writes.push(["patch", ...args]); };
  api.removeRelationship = async (...args) => { writes.push(["remove", ...args]); };
  api.createRelationship = async (id, boardId, _expected, source, target, kind) => {
    writes.push(["add", source, target]);
    relationships.push({id, board_id: boardId, source_card_id: source, target_card_id: target, kind, revision: 1});
  };
  await ui.load();
  await until(() => ui.find(ui.nodes.details, "button", "Change link type"));
  const finish = Promise.withResolvers();
  behavior.write = async (...args) => { await finish.promise; return commit(...args); };
  choose({name: "endpoint.txt", size: 1});
  const {details, main, status} = ui.nodes;
  async function submitLinkActions(ticket) {
    ui.field(details, "Target ticket").value = String(ticket);
    ui.submit(details, "Add link");
    await ui.settled();
    assert.match(status.textContent, /Wait for attachments/);
    const edit = ui.find(details, "button", "Change link type");
    ui.field(edit.parent, "Link type (source to target)").value = "duplicates";
    ui.submit(details, "Change link type");
    await ui.settled();
    assert.match(status.textContent, /Wait for attachments/);
    ui.submit(details, "Remove link");
    await ui.settled();
    assert.match(status.textContent, /Wait for attachments/);
  }
  await submitLinkActions(2);
  ui.find(main, "button", "#2 Other card").click();
  await until(() => /#2 Other card/.test(details.textContent) && ui.find(details, "button", "Change link type"));
  await submitLinkActions(1);
  assert.deepEqual(writes, []);
  ui.field(details, "Target ticket").value = "3";
  ui.submit(details, "Add link");
  await ui.settled();
  assert.deepEqual(writes, [["add", backup, third]]);
  assert.equal(ui.uploadingToCard(board, card), true);
  finish.resolve();
  await until(() => !ui.uploadingToCard(board, card));
});

test("seeded creation preserves its draft while a resolved target uploads", async () => {
  const {ui, api, behavior, commit, choose} = await attachmentUploadFixture();
  const finish = Promise.withResolvers();
  behavior.write = async (...args) => { await finish.promise; return commit(...args); };
  const creates = [];
  api.createCard = async (request) => { creates.push(request); return createdCardReply(request); };
  choose({name: "target.txt", size: 1});
  const {main, status} = ui.nodes;
  ui.find(main, "button", "Add card").click();
  const add = ui.find(main, "button", "Add relationship");
  add.click();
  const row = add.parent.querySelectorAll("fieldset")[0];
  const title = ui.field(main, "New title");
  const target = ui.field(row, "Target ticket");
  title.value = "Retain this draft";
  target.value = "1";
  ui.submit(main, "Create card");
  await ui.settled();
  assert.match(status.textContent, /Wait for attachments/);
  assert.equal(creates.length, 0);
  assert.equal(title.value, "Retain this draft");
  assert.equal(target.value, "1");
  target.value = "2";
  ui.submit(main, "Create card");
  await ui.settled();
  assert.equal(creates.length, 1);
  assert.equal(creates[0].relationships[0].target_card_id, backup);
  assert.equal(ui.uploadingToCard(board, card), true);
  finish.resolve();
  await until(() => !ui.uploadingToCard(board, card));
});

test("batch priority checks only explicitly selected cards for attachment uploads", async () => {
  const {ui, api, cards, behavior, commit, choose} = await attachmentUploadFixture();
  const finish = Promise.withResolvers();
  behavior.write = async (...args) => { await finish.promise; return commit(...args); };
  const selections = [];
  api.setPriorities = async (_board, _expected, ids) => {
    selections.push(Array.from(ids));
    return {data: {cards: Array.from(ids, (id) => cards.get(id)), changes: []}};
  };
  choose({name: "selected.txt", size: 1});
  const {main, status} = ui.nodes;
  const tickets = ui.field(main, "Ticket numbers");
  tickets.value = "1, 2";
  ui.submit(main, "Set batch priority");
  await ui.settled();
  assert.deepEqual(selections, []);
  assert.match(status.textContent, /Wait for attachments/);
  tickets.value = "2";
  ui.submit(main, "Set batch priority");
  await ui.settled();
  assert.deepEqual(selections, [[backup]]);
  assert.equal(ui.uploadingToCard(board, card), true);
  finish.resolve();
  await until(() => !ui.uploadingToCard(board, card));
});

test("ticket order checks only cards whose ticket changes for attachment uploads", async () => {
  const {ui, api, cards, behavior, commit, choose} = await attachmentUploadFixture();
  const listLanes = api.listLanes;
  api.listLanes = async (...args) => page("lanes", (await listLanes(...args)).data.lanes.map((entry) =>
    entry.id === lane ? {...entry, reorder_enabled: true} : entry));
  const third = "a57df7d8-0476-44d6-97a4-3b2e5486650a";
  cards.set(third, {...cards.get(backup), id: third, ticket: 3, title: "Unrelated"});
  const finish = Promise.withResolvers();
  behavior.write = async (...args) => { await finish.promise; return commit(...args); };
  const ids = [card, backup, third];
  const current = ids.map((card_id, index) => ({card_id, ticket: index + 1}));
  let tickets = [2, 1, 3];
  api.previewTicketOrder = async () => ({data: {
    board_revision: 1, changed: true, current, proposed: ids.map((card_id, index) => ({card_id, ticket: tickets[index]})),
  }, etag: tag(board, 1)});
  const applies = [];
  api.applyTicketOrder = async (_board, _lane, _tag, proposed) => {
    applies.push(Array.from(proposed, ({card_id, ticket}) => [card_id, ticket]));
    for (const {card_id, ticket} of proposed) cards.set(card_id, {...cards.get(card_id), ticket});
  };
  const {main, status} = ui.nodes;
  ui.nodes.refresh.click();
  await until(() => ui.find(main, "button", "Preview ticket order"));
  choose({name: "renumbered.txt", size: 1});
  ui.find(main, "button", "Preview ticket order").click();
  await until(() => ui.find(main, "button", "Apply ticket order"));
  ui.submit(main, "Apply ticket order");
  await ui.settled();
  assert.deepEqual(applies, []);
  assert.match(status.textContent, /Wait for attachments/);
  tickets = [1, 3, 2];
  ui.find(main, "button", "Preview ticket order").click();
  await until(() => /#2 → #3/.test(main.textContent));
  ui.submit(main, "Apply ticket order");
  await ui.settled();
  assert.deepEqual(applies, [[[card, 1], [backup, 3], [third, 2]]]);
  assert.equal(ui.uploadingToCard(board, card), true);
  finish.resolve();
  await until(() => !ui.uploadingToCard(board, card));
});

test("native restore waits only on attachment uploads for its target board", async () => {
  const {ui, api, behavior, commit, choose} = await attachmentUploadFixture(uploadBoard);
  const finish = Promise.withResolvers();
  behavior.write = async (...args) => { await finish.promise; return commit(...args); };
  const reads = [];
  const restores = [];
  api.getSnapshot = async (id) => {
    reads.push(id);
    return {data: {board: {id, revision: 1}}, etag: tag(id, 1)};
  };
  api.restoreSnapshot = async (id) => { restores.push(id); };
  choose({name: "origin.txt", size: 1});
  const {main, status} = ui.nodes;
  async function restore() {
    ui.field(main, "Native backup ZIP").files = [new Blob(["backup"])];
    ui.submit(main, "Restore native backup");
    await ui.settled();
  }
  await restore();
  assert.deepEqual(reads, []);
  assert.deepEqual(restores, []);
  assert.match(status.textContent, /Wait for attachments/);
  await ui.navigate("/boards/" + uploadBoard);
  await restore();
  assert.deepEqual(reads, [uploadBoard]);
  assert.deepEqual(restores, [uploadBoard]);
  assert.equal(ui.uploadingToCard(board, card), true);
  finish.resolve();
  await until(() => !ui.uploadingToCard(board, card));
});

for (const lost of [true, false]) {
  test("lane approval waits for a rendered member's " + (lost ? "uncertain upload" : "failed upload refresh"), async () => {
    const {ui, api, behavior, commit, choose, calls, readbacks} = await attachmentUploadFixture();
    const ready = (await api.listLanes()).data.lanes[0];
    api.listLanes = async () => page("lanes", [
      {...ready, role: "ready_for_review", approval_enabled: true},
      {...ready, id: uploadBoard, key: "completed", role: "completed", position: 1},
    ]);
    const approvals = [];
    api.approveLane = async (...args) => { approvals.push(Array.from(args[5], ({id}) => id)); };
    await ui.load();
    const {details, main, status} = ui.nodes;
    await until(() => ui.find(details, "button", "Upload attachments"));
    const readBoard = api.getBoardLanes;
    let refuseBoardRead = false;
    api.getBoardLanes = async (...args) => {
      if (refuseBoardRead) throw new ApiError("transport_failed");
      return readBoard(...args);
    };
    behavior.write = async (...args) => {
      if (lost) throw new ApiError("transport_failed", {outcomeUnknown: true});
      const reply = commit(...args);
      refuseBoardRead = true;
      return reply;
    };
    choose({name: "pending.txt", size: 1});
    await until(() => !ui.uploadingToCard(board, card));
    assert.match(details.textContent, lost ? /Outcome uncertain/ : /Use Inspect saved card to read the saved attachments/);
    ui.submit(main, "Approve all in lane");
    await ui.settled();
    assert.deepEqual(approvals, []);
    assert.match(status.textContent, /Inspect saved card/);
    refuseBoardRead = false;
    ui.find(details, "button", "Inspect saved card").click();
    await ui.settled();
    assert.match(details.textContent, lost ? /Outcome uncertain/ : /pending\.txt — Uploaded/);
    assert.deepEqual(readbacks, lost ? [calls[0].attachmentId, calls[0].attachmentId] : []);
    ui.submit(main, "Approve all in lane");
    await ui.settled();
    assert.deepEqual(approvals, lost ? [] : [[card, backup]]);
  });
}
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

test("a slow text edit projects the submitted title and keeps newer draft text through readback", async (t) => {
  const {api, commit} = changingBoard();
  const response = Promise.withResolvers();
  const checking = Promise.withResolvers();
  const readback = Promise.withResolvers();
  const sent = [];
  const readBoard = api.getBoardLanes;
  api.getBoardLanes = async (...args) => {
    const result = await readBoard(...args);
    if (result.data.board.revision === 2) {
      checking.resolve();
      await readback.promise;
    }
    return result;
  };
  api.patchCard = async (id, validator, {title}) => {
    sent.push([id, validator, title]);
    await response.promise;
    commit(title, lane);
    return api.getCard(id);
  };
  const ui = await harness(api);
  t.after(() => ui.navigate("/"));
  await ui.load();
  const {main, details} = ui.nodes;
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.field(details, "Title"));
  const input = ui.field(details, "Title");
  input.value = "Submitted title";
  ui.submit(details, "Save text");
  assert.deepEqual(sent, [[card, tag(card, 1), "Submitted title"]]);
  assert.equal(ui.find(main, "button", "#1 Submitted title").dataset.pending, "true");
  assert.ok(ui.find(details, "h2", "#1 Submitted title"));
  assert.match(details.textContent, /Saved cardOriginal/);

  input.value = "Newer draft";
  input.dispatch("input");
  await ui.navigate("/");
  assert.doesNotMatch(main.textContent, /Submitted title/);
  await ui.navigate("/boards/" + board);
  assert.equal(ui.find(main, "button", "#1 Submitted title").dataset.pending, "true");
  response.resolve();
  await checking.promise;
  assert.equal(ui.find(main, "button", "#1 Submitted title").dataset.pending, "true");
  readback.resolve();
  await ui.settled();
  assert.equal(ui.find(main, "button", "#1 Submitted title").dataset.pending, undefined);
  assert.match(details.textContent, /Saved cardSubmitted title/);
  assert.equal(ui.field(details, "Title").value, "Newer draft");
  assert.equal(sent.length, 1);
});

test("a slow creation projects an unnumbered card and inspects the same ID after a lost response", async () => {
  const {api} = boardApi();
  const delayed = Promise.withResolvers();
  const sent = [];
  const reads = [];
  let created;
  const getBoard = api.getBoardLanes;
  api.getBoardLanes = async () => {
    const result = await getBoard();
    if (created) {
      result.data.cards.push(created);
      result.data.board.revision = 2;
      result.etag = tag(board, 2);
    }
    return result;
  };
  api.getCard = async (id) => {
    reads.push(id);
    if (!created || created.id !== id) throw new ApiError("http_error", {status: 404});
    return {data: created, etag: tag(id, 2)};
  };
  api.createCard = async (input) => {
    sent.push(input);
    await delayed.promise;
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  const ui = await harness(api);
  await ui.load();
  const {main, status} = ui.nodes;
  ui.find(main, "button", "Add card").click();
  ui.field(main, "New title").value = "Delayed creation";
  const submit = ui.submit(main, "Create card");
  await until(() => sent.length === 1);
  const [{id, expected_board_etag}] = sent;
  const placeholder = ui.find(main, "div", "Delayed creation");
  assert.equal(placeholder.dataset.cardId, id);
  assert.equal(placeholder.dataset.pending, "true");
  assert.equal(placeholder.parent, ui.find(main, "h2", "Ready").parent);
  assert.equal(placeholder.textContent, "Delayed creationCreating…");
  assert.equal(expected_board_etag, tag(board, 1));

  delayed.resolve();
  await ui.settled();
  assert.equal(ui.find(main, "div", "Delayed creation"), undefined);
  assert.match(main.textContent, /Unresolved new card/);
  assert.match(status.textContent, /outcome was uncertain/);
  assert.equal(submit.disabled, true);
  assert.deepEqual(reads, [id]);

  created = {id, board_id: board, title: "Delayed creation", description: "", ticket: 2,
    lane_id: lane, owner_id: user, priority_id: priority, revision: 2, archived: false, cover_attachment_id: null};
  ui.find(main, "button", "Inspect saved board").click();
  await ui.settled();
  assert.deepEqual(reads, [id, id]);
  assert.equal(sent.length, 1);
  assert.equal(ui.find(main, "button", "#2 Delayed creation").dataset.pending, undefined);
  assert.equal(ui.field(main, "New title").value, "");
});

test("lost text save reconciles the same card after navigation during its first readback", async (t) => {
  const {api, commit} = changingBoard();
  const started = Promise.withResolvers();
  const readback = Promise.withResolvers();
  const writes = [];
  const getCard = api.getCard;
  let holdReadback = false;
  api.getCard = async (id) => {
    assert.equal(id, card);
    if (holdReadback) {
      holdReadback = false;
      started.resolve();
      await readback.promise;
    }
    return getCard(id);
  };
  api.patchCard = async (id, expected, {title}) => {
    writes.push([id, expected, title]);
    holdReadback = true;
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  const ui = await harness(api);
  t.after(() => ui.navigate("/"));
  await ui.load();
  const {main, details} = ui.nodes;
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.field(details, "Title"));
  ui.field(details, "Title").value = "Lost response";
  ui.submit(details, "Save text");
  await started.promise;

  await ui.navigate("/");
  await ui.navigate("/boards/" + board);
  await until(() => ui.field(details, "Title"));
  const replacement = ui.find(details, "button", "Save text");
  assert.equal(replacement.disabled, true);
  assert.match(details.textContent, /Saved cardOriginal/);
  assert.equal(ui.field(details, "Title").value, "Lost response");

  commit("Lost response", lane);
  readback.resolve();
  await ui.settled();
  assert.ok(ui.find(main, "button", "#1 Lost response"));
  assert.match(details.textContent, /Saved cardLost response/);
  assert.equal(replacement.disabled, false);
  assert.deepEqual(writes, [[card, tag(card, 1), "Lost response"]]);
});

test("a late failed text readback leaves Workspace status alone and fences its original card", async () => {
  const {api} = boardApi();
  const started = Promise.withResolvers();
  const readback = Promise.withResolvers();
  const writes = [];
  const getCard = api.getCard;
  let holdReadback = false;
  api.getCard = async (id) => {
    assert.equal(id, card);
    if (holdReadback) {
      holdReadback = false;
      started.resolve();
      await readback.promise;
    }
    return getCard(id);
  };
  api.patchCard = async (id, expected, {title}) => {
    writes.push([id, expected, title]);
    holdReadback = true;
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  const ui = await harness(api);
  await ui.load();
  const {main, details, status} = ui.nodes;
  ui.find(main, "button", "#1 Original").click();
  await until(() => ui.field(details, "Title"));
  ui.field(details, "Title").value = "Keep this title";
  ui.submit(details, "Save text");
  await started.promise;

  await ui.navigate("/");
  const workspaceStatus = [status.dataset.phase, status.textContent];
  readback.reject(new ApiError("transport_failed"));
  await ui.settled();
  assert.deepEqual([status.dataset.phase, status.textContent], workspaceStatus);

  await ui.navigate("/boards/" + board);
  await until(() => ui.field(details, "Title"));
  const save = ui.submit(details, "Save text");
  assert.equal(save.disabled, true);
  assert.equal(ui.field(details, "Title").value, "Keep this title");
  ui.find(details, "button", "Inspect saved card").click();
  await ui.settled();
  assert.equal(save.disabled, false);
  assert.match(details.textContent, /Saved cardOriginal/);
  assert.deepEqual(writes, [[card, tag(card, 1), "Keep this title"]]);
});
