import assert from "node:assert/strict";
import fs from "node:fs/promises";
import vm from "node:vm";
import test from "node:test";
import { newOperationId } from "./operation-id.mjs";
import {ApiError, createBrowserApi, MAX_ATTACHMENT_BYTES, validPublishing} from "./api.mjs";
import {createBoardRefresh, watchBoard} from "./board-updates.mjs";
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
  clock = Date,
  storage = tabStorage(),
} = {}) {
  let document;
  class Element {
    scrollTop = 0;
    scrollLeft = 0;
    constructor(tagName) { this.tagName = tagName; this.children = []; this.dataset = {}; this.attributes = {}; this.events = {}; this.value = ""; this.ownText = ""; this.disabled = false; this.hidden = false; }
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
    click() { if (!this.disabled) this.dispatch("click"); }
    focus(options) { document.activeElement = this; this.focusOptions = options; }
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
  const windowEvents = {};
  const window = {sessionStorage: storage, addEventListener: (name, handler) => { windowEvents[name] = handler; }};
  const context = vm.createContext({document, window, CSSStyleSheet: class {replaceSync() {}},
    HTMLButtonElement: Button, HTMLInputElement: Input, HTMLTextAreaElement: Textarea, HTMLSelectElement: Select,
    MouseEvent: class {}, location: {pathname, search: ""}, history: {}, URL, URLSearchParams,
    Blob, setTimeout: pollTimer, Date: clock, AbortSignal, AbortController, crypto: globalThis.crypto, ApiError, validPublishing, MAX_ATTACHMENT_BYTES, newOperationId,
    createBoardRefresh, watchBoard: typeof api.streamEvents === "function" ? watchBoard : async () => {},
    createBrowserApi: ({getCapability}) => typeof api === "function" ? api(getCapability) : api,
    openBrowserSession: openSession});
  let source = await fs.readFile(new URL("./app.mjs", import.meta.url), "utf8");
  source = source.replace(/^import .*?;\r?\n/gm, "").replace(/void load\(\);\s*$/, "");
  vm.runInContext(source + "\nglobalThis.hooks = {load, readWithSession, renderCardSearch, renderBoardActions, busy:()=>writing, uploadingToCard, inspect:()=>cardInspection.inspect()};", context);
  async function settled() { for (let count = 0; count < 40; count++) { await new Promise((resolve) => setTimeout(resolve, 0)); if (!context.hooks.busy()) return; } throw Error("UI did not settle"); }
  const find = (root, tagName, text) => root.querySelectorAll(tagName).find((node) => node.ownText === text);
  const field = (root, label) => find(root, "label", label)?.children[0];
  const submit = (root, label) => { const control = find(root, "button", label); assert.ok(control, label); if (!control.disabled) control.parent.dispatch("submit"); return control; };
  async function navigate(path) {
    context.location.pathname = path;
    await context.hooks.load();
  }
  async function popstate(href) {
    const url = new URL(href, "http://localhost");
    context.location.pathname = url.pathname;
    context.location.search = url.search;
    windowEvents.popstate();
    await settled();
  }
  return {nodes, ...context.hooks, settled, find, field, submit, navigate, popstate,
    pagehide: () => windowEvents.pagehide(), focused: () => document.activeElement};
}

function tabStorage() {
  const entries = new Map();
  return {getItem: (key) => entries.get(key) ?? null, setItem: (key, value) => entries.set(key, value)};
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
    getBoardLanes: async () => ({data: {board: {id: board, name: "Board", revision}, cards: saved ? [saved] : []}, etag: tag(board, revision)}),
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
  columns(ui).scrollLeft = 88;
  columns(ui).children[0].scrollTop = 320;
  await ui.navigate(otherUrl);
  assert.equal(ui.nodes.details.hidden, true);
  assert.equal(query(ui).value, "");
  assert.equal(columns(ui).children[0].scrollTop, 0);
  query(ui).value = "Second query";
  ui.field(searchPanel(ui), "Search lane").value = otherLane;
  columns(ui).children[0].scrollTop = 130;

  await ui.popstate(url);
  assert.equal(searchPanel(ui).open, true);
  assert.equal(query(ui).value, "Unsubmitted text");
  assert.equal(ui.field(searchPanel(ui), "Search lane").value, lane);
  assert.equal(columns(ui).scrollLeft, 88);
  assert.equal(columns(ui).children[0].scrollTop, 320);
  assert.equal(ui.nodes.details.querySelector("h2").textContent, "#1 Original");
  assert.ok(ui.find(searchPanel(ui), "button", "#1 Original"));
  assert.equal(ui.focused()?.isConnected, false);
  assert.deepEqual(searches.map(({id, q}) => [id, q]), [[board, "Original"], [board, "Original"]]);
  await ui.popstate(otherUrl);
  assert.equal(query(ui).value, "Second query");
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
  assert.equal(columns(reloaded).children[0].scrollTop, 275);
  assert.equal(reloaded.nodes.details.querySelector("h2").textContent, "#1 Original");
  reloaded.find(reloaded.nodes.details, "button", "Close card").click();
  const closed = await harness(api, url, {storage});
  await closed.load();
  assert.equal(closed.nodes.details.hidden, true);

  const separate = await harness(api, url);
  await separate.load();
  assert.equal(query(separate).value, "");
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
  const getBoardLanes = api.getBoardLanes;
  api.getBoardLanes = async (...args) => {
    const result = await getBoardLanes(...args);
    return {...result, data: {...result.data, cards: result.data.cards.map((item) => ({...item, cover_attachment_id: backup}))}};
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
  assert.equal(ui.nodes.boards.querySelector("a").dataset.boardId, second.id);
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
  api.listBoards = async () => {
    reads++;
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
  await ui.settled();
  assert.equal(ui.nodes.status.textContent, "Card saved.");
  assert.ok(ui.find(ui.nodes.main, "button", "#1 Saved during catalog outage"));
  assert.equal(reads, 2);
  assert.equal(ui.nodes.boards.querySelector("a").children[3].textContent, "1");
  assert.equal(attentionStatus.hidden, false);

  unavailable = false;
  row.attention.ready_for_review = 2;
  ui.nodes.refresh.click();
  await ui.settled();
  assert.equal(reads, 3);
  assert.equal(ui.nodes.boards.querySelector("a").children[3].textContent, "2");
  assert.equal(attentionStatus.hidden, true);
});

test("an old Attention read cannot replace the catalog after navigation", async () => {
  const {api} = boardApi();
  const started = Promise.withResolvers();
  const late = Promise.withResolvers();
  let reads = 0;
  api.listBoards = async () => {
    reads++;
    if (reads === 2) {
      started.resolve();
      return late.promise;
    }
    const name = reads === 1 ? "Original catalog" : "New view catalog";
    return page("boards", [{id: board, name}]);
  };
  const ui = await harness(api);
  await ui.load();
  ui.nodes.refresh.click();
  await started.promise;
  await ui.navigate("/");
  late.resolve(page("boards", [{id: board, name: "Old view catalog"}]));
  await ui.settled();
  assert.ok(ui.find(ui.nodes.boards, "span", "New view catalog"));
  assert.equal(ui.find(ui.nodes.boards, "span", "Old view catalog"), undefined);
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

const runningService = {
  service_instance_id: "019aa833-764f-746b-a3f0-e2f7af0b9e3f", build_id: board, port: 53146,
  state: "running", etag: '"019aa833-764f-746b-a3f0-e2f7af0b9e3f-0"',
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
        : runningService};
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
  assert.deepEqual(calls[0].slice(1), [backup, runningService.etag]);
  assert.match(main.querySelector("#service-operation-status").textContent, /Build activated. Reconnected on the same port/);
  assert.equal(control.disabled, true);
  assert.equal(name.value, "Draft stays open");
});

test("workspace shutdown reports final publication refusal and leaves a healthy service actionable", async () => {
  const staged = {id: backup, version: "broken", validation: "invalid", error_code: "candidate_binary_incompatible"};
  let writes = 0;
  const ui = await harness(workspaceServiceApi({
    getService: async () => ({data: writes ? {...runningService, restart_error_code: "git_push_failed"} : runningService}),
    listBuilds: async () => page("builds", [staged]),
    stopService: async (prior) => {
      assert.equal(prior.etag, runningService.etag);
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
      return {data: runningService};
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
      return {data: runningService};
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
    getService: async () => ({data: runningService}),
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
    getService: async () => ({data: runningService}),
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
      return {data: runningService};
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
    getService: async () => ({data: runningService}),
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
  api.streamEvents = ({ boardId, signal }) => new ReadableStream({
    start(controller) {
      let closed = false;
      const close = () => {
        if (closed) return;
        closed = true;
        controller.close();
      };
      signal.addEventListener("abort", close, {once: true});
      streams.push({boardId, signal, close,
        send(type, revision, id = boardId) {
          controller.enqueue({type, resource: "board", board_id: id, board_revision: revision});
        }});
    },
  }).values();
  return streams;
}

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
    return reply;
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

test("live link repaint keeps the dirty selector, focus, and its relationship revision", async (t) => {
  const {api, streams, commit} = changingBoard();
  const edited = {id: backup, board_id: board, source_card_id: card, target_card_id: user,
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
  const selector = () => row() && ui.field(row(), "Link type");
  const savedType = () => row()?.querySelector("p").ownText;
  const staleNotice = () => ui.find(row(), "p", "Saved link changed. Review the saved type and change the selection before saving.");
  ui.find(main, "button", "#1 Original").click();
  await until(selector);
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
  await until(() => savedType() === "References " && readiness.textContent === "");
  assert.equal(selector().value, "blocks");
  assert.equal(ui.focused(), selector());
  assert.equal(staleNotice().hidden, false);
  ui.submit(row(), "Change link type");
  await ui.settled();
  assert.deepEqual(changes, [[edited.id, 1, tag(board, 3), "blocks"]]);
  assert.match(status.textContent, /State changed/);
  assert.equal(selector().value, "blocks");
  assert.equal(savedType(), "References ");

  selector().value = "duplicated_by";
  selector().dispatch("change");
  assert.equal(staleNotice().hidden, true);
  ui.submit(row(), "Change link type");
  await ui.settled();
  assert.deepEqual(changes[1], [edited.id, 3, tag(board, 3), "duplicated_by"]);
  assert.equal(savedType(), "Duplicated by ");
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
  assert.equal(lists, 3); // Initial catalog plus one Attention refresh per successful board repaint.
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
  api.createCard = async () => { writes++; };
  const ui = await harness(api);
  t.after(() => ui.navigate("/"));
  await ui.load();
  await until(() => streams.length === 1);
  const {main} = ui.nodes;
  streams[0].send("snapshot", 1);
  streams[0].send("change", commit("Paused live read"));
  await entered.promise;
  const title = ui.field(main, "New title");
  title.value = "Card being created";
  ui.field(main, "New card lane").value = lane;
  ui.submit(main, "Add card");
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

const uploadBoard = "b4979c23-4be2-4553-bbeb-66a47b7c021c";

async function attachmentUploadFixture(secondBoard = board) {
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
  const boardData = (id) => ({id, name: id === board ? "Board" : "Other board", revision: revisions.get(id)});
  api.listBoards = async () => page("boards", [...revisions.keys()].map(boardData));
  api.getBoardAvailability = async (id) => ({
    data: {...boardData(id), availability: "open", policy_id: priority}, etag: tag(id, revisions.get(id)),
  });
  api.getBoardLanes = async (id) => ({data: {board: boardData(id),
    cards: [...cards.values()].filter((entry) => entry.board_id === id)}, etag: tag(id, revisions.get(id))});
  api.getCard = async (id) => ({data: cards.get(id), etag: tag(id, cards.get(id).revision)});
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
  const ui = await harness(api);
  await ui.load();
  ui.find(ui.nodes.main, "button", "#1 Original").click();
  await until(() => ui.find(ui.nodes.details, "button", "Upload attachments"));
  const choose = (...files) => {
    ui.field(ui.nodes.details, "Files").files = files;
    return ui.submit(ui.nodes.details, "Upload attachments");
  };
  const paste = (files, types = ["Files"], target = ui.nodes.details) =>
    ui.nodes.details.dispatch("paste", {target, clipboardData: {files, types}});
  return {ui, api, cards, calls, readbacks, saved, behavior, commit, choose, paste};
}

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
  assert.equal(calls.length, 1);
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
  assert.equal(ui.find(ui.nodes.details, "button", "Upload attachments").disabled, false);
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

test("attachment inspection preserves an independent pending link readback on the same card", async () => {
  const {ui, api, calls, behavior, commit, choose} = await attachmentUploadFixture();
  const relationship = {
    id: uploadBoard, board_id: board, source_card_id: card, target_card_id: backup, kind: "relates_to", revision: 1,
  };
  let failLinkRead = false;
  api.listRelationships = async () => {
    if (failLinkRead) throw new ApiError("transport_failed");
    return page("relationships", [relationship]);
  };
  api.removeRelationship = async () => {
    failLinkRead = true;
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  await ui.load();
  await until(() => ui.find(ui.nodes.details, "button", "Remove link"));
  const finish = Promise.withResolvers();
  const file = {name: "uncertain.txt", size: 1};
  behavior.write = async () => {
    await finish.promise;
    throw new ApiError("transport_failed", {outcomeUnknown: true});
  };
  choose(file);
  ui.submit(ui.nodes.details, "Remove link");
  await ui.settled();
  assert.match(ui.nodes.status.textContent, /Readback failed/);
  finish.resolve();
  await until(() => !ui.uploadingToCard(board, card));
  assert.match(ui.nodes.details.textContent, /Outcome uncertain/);
  failLinkRead = false;
  commit(card, file, calls[0].attachmentId);
  ui.find(ui.nodes.details, "button", "Inspect saved card").click();
  await ui.settled();
  assert.match(ui.nodes.details.textContent, /uncertain\.txt — Uploaded\. Confirmed by readback/);
  assert.match(ui.nodes.status.textContent, /The submission could not be confirmed/);
  assert.equal(ui.find(ui.nodes.details, "button", "Remove link").disabled, false);
});
