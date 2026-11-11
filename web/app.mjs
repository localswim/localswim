import { newOperationId } from "./operation-id.mjs";
import { ApiError, createBrowserApi, openBrowserSession, validPublishing } from "./api.mjs";
import { createBoardRefresh, watchBoard } from "./board-updates.mjs";

/** @typedef {{id: string, label: string}} Labeled */
/** @typedef {Labeled & {kind: "human"|"bot"}} User */
/** @typedef {import("./api.mjs").RelationshipKind} RelationshipKind */
/** @typedef {{id: string, revision: number, board_id: string, source_card_id: string, target_card_id: string,
 * kind: RelationshipKind}} Relationship */
/** @typedef {{type: HTMLSelectElement, direction: RelationshipKind, expectedRevision: number}} RelationshipEditor */
/** @typedef {{resource_type: string, resource_id: string, operation: string, field: string|null,
 * actor_id: string, board_id: string, at: string, revision: number, before: string|null, after: string|null}} Change */
/** @typedef {import("./api.mjs").Publishing} Publishing */
/** @typedef {{id: string, name: string, revision: number, default_owner_id: string, user_css?: string, notifications?: object, publishing?: unknown}} Board */
/** @typedef {{blocked: number, needs_input: number, ready_for_review: number}} Attention */
/** @typedef {{id: string, name: string|null, attention: Attention|null, last_card_edit_at: string|null}} BoardSummary */
/** @typedef {Labeled & {revision: number, key: string, retired: boolean, position: number, role: string, approval_enabled: boolean}} Lane */
/** @typedef {{id: string, text: string, created_at: string, author_id: string, card_id: string}} Comment */
/** @typedef {{id: string, card_id: string, original_name: string, created_at: string, added_by: string, size: number, board_id: string, active: boolean, previewable: boolean, revision: number, sha2_256: string}} Attachment */
/** @typedef {{id: string, title: string, description: string, board_id: string, lane_id: string,
 * owner_id: string, priority_id: string, ticket: number, revision: number, comment_count: number, attachment_count: number,
 * archived: boolean, cover_attachment_id: string|null, parent_id: string|null, child_ids: string[]}} Card */
/** @typedef {{ticket: string, kind: RelationshipKind}} RelationshipSeedDraft */
/** @typedef {RelationshipSeedDraft & {row: RelationshipSeedDraft}} SubmittedRelationshipSeed */
/** @typedef {{rows: RelationshipSeedDraft[], discard?: (submitted: SubmittedRelationshipSeed[]) => void}} RelationshipSeedState */
/** @typedef {{data: object, etag: string|null}} Reply */
/** @typedef {() => Promise<void|boolean>} InspectionRead */
/** @typedef {{scope: string, label: string, require: (read: InspectionRead) => void,
 * inspect: () => void, sync: () => void}} Inspection */
/** @typedef {{title?: string, description?: string, lane?: string, owner?: string, priority?: string, comment?: string}} CardDraft */
/** @typedef {{query: string, content: boolean, order: "triage"|"ticket", lane: string, limit: string}} SearchFields */
/** @typedef {{open: boolean, fields: SearchFields, submitted?: SearchFields}} BoardSearch */
/** @typedef {{card?: {id: string, ticket: string|null}, search?: BoardSearch, scroll?: {left: number, lanes: Record<string, number>}}} BoardView */
/** @typedef {import("./api.mjs").Service} Service */
/** @typedef {import("./api.mjs").Build} Build */
/** @typedef {{prior: Service, activation?: {id: string, buildId: string}, acknowledged: boolean, refusal?: string}} LifecycleOperation */


const status = /** @type {HTMLParagraphElement|null} */ (document.getElementById("status"));
const identity = document.getElementById("identity");
const navigation = document.getElementById("boards");
const main = document.querySelector("main");
const details = document.getElementById("details");
const refresh = document.getElementById("refresh");
if (!status || !identity || !navigation || !main || !details || !refresh) {
  throw new Error("Missing page structure.");
}

let capability = "";
/** @type {Promise<string>|undefined} */
let connecting;
const api = createBrowserApi({ getCapability: () => capability });
let activeView = 0;
let activeCard = 0;
let writing = false;
/** @type {AbortController|undefined} */
let activeBoardLoad;
/** @type {ReturnType<typeof createBoardRefresh>|undefined} */
let activeBoardRefresh;
/** @type {LifecycleOperation|undefined} */
let pendingLifecycle;
/** @type {(() => Promise<void>)|undefined} */
let refreshActiveService;
/** @type {Inspection|undefined} */
let boardInspection;
/** @type {Inspection|undefined} */
let cardInspection;
/** @type {(() => void)|undefined} */
let retrySelectedCard;
/** @type {(() => void)|undefined} */
let retainCardDraft;
/** @type {(() => void)|undefined} */
let retainBoardDraft;
/** @type {(() => void)|undefined} */
let retainBoardView;
/** @type {Map<string, CardDraft>} */
const cardDrafts = new Map();
/** @type {Map<string, string[]>} */
const boardDrafts = new Map();
/** @type {Map<string, RelationshipSeedState>} */
const relationshipSeedDrafts = new Map();
/** @type {Map<string, BoardView>} */
const boardViews = new Map();
/** @type {Map<string, boolean>} */
const publishingDrafts = new Map();
/** @type {Map<string, Promise<void>>} */
const preferenceWrites = new Map();
/** @type {string[]|undefined} */
let pinDraft;
/** @type {Map<string, {id: string, backupId: string}>} */
const pendingPublications = new Map();
/** @type {Map<string, {read: InspectionRead, isCurrent: () => boolean}>} */
const pendingInspections = new Map();
/** @type {{id: string, control: HTMLTextAreaElement}|undefined} */
let activeComment;
/** @type {{boardId: string, id: string, close: () => void,
 * refreshWorkflow: (canApply?: () => boolean, automatic?: boolean) => Promise<void>,
 * refreshPriority: (requested: string, effective: Labeled|undefined, replaced: boolean) => Promise<void>}|undefined} */
let activeDrawer;
/** @type {(() => void)|undefined} */
let releaseCardMedia;
/** @type {(() => void)|undefined} */
let releaseBoardMedia;
const boardStyle = new CSSStyleSheet();
document.adoptedStyleSheets = [...document.adoptedStyleSheets, boardStyle];

/** @param {string} boardId */
function viewStorageKey(boardId) {
  return "localswim:board-view:" + boardId;
}

/** @param {string} boardId @returns {BoardView} */
function boardViewState(boardId) {
  const existing = boardViews.get(boardId);
  if (existing) return existing;
  /** @type {BoardView} */
  let state = {};
  try {
    const stored = window.sessionStorage.getItem(viewStorageKey(boardId));
    const parsed = stored === null ? null : JSON.parse(stored);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) state = parsed;
  } catch {
    // Keep the in-page view available if tab storage is inaccessible.
  }
  boardViews.set(boardId, state);
  return state;
}

/** @param {string} boardId */
function persistBoardView(boardId) {
  try {
    window.sessionStorage.setItem(viewStorageKey(boardId), JSON.stringify(boardViewState(boardId)));
  } catch {
    // The current tab can still switch boards using its in-memory state.
  }
}

/** @param {string} boardId @param {string} [id] */
function rememberCard(boardId, id) {
  const state = boardViewState(boardId);
  if (id === undefined) delete state.card;
  else state.card = { id, ticket: new URLSearchParams(location.search).get("ticket") };
  persistBoardView(boardId);
}

/** @param {string} boardId @param {string|null} ticket @returns {string|undefined} */
function rememberedCard(boardId, ticket) {
  const state = boardViewState(boardId);
  const selected = state.card;
  if (!selected || typeof selected.id !== "string"
      || !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(selected.id)) {
    if (Object.hasOwn(state, "card")) rememberCard(boardId);
    return undefined;
  }
  return ticket === null || selected.ticket === ticket ? selected.id : undefined;
}

/** @param {string} phase @param {string} message */
function announce(phase, message) {
  if (!status) return;
  status.dataset.phase = phase;
  status.textContent = message;
}

function finishWriting() {
  writing = false;
  activeBoardRefresh?.resume();
}

/** @param {Publishing} state */
function publishingStatus(state) {
  const parts = [state.enabled ? "Automatic publishing is on." : "Automatic publishing is off."];
  if (state.error_code !== null) {
    switch (state.error_code) {
      case "git_push_failed": parts.push("Git push failed."); break;
      case "repository_busy": parts.push("The repository is busy."); break;
      default: parts.push("Publishing failed.");
    }
  }
  if (state.pending) parts.push(state.enabled ? "Publication pending." : "Publication pending while publishing is off.");
  if (state.last_success_at !== null) parts.push("Last successful publish at " + state.last_success_at + ".");
  return parts.join(" ");
}

/** @param {string} key @param {Promise<void>} write */
function trackPreferenceWrite(key, write) {
  preferenceWrites.set(key, write);
  void write.finally(() => { preferenceWrites.delete(key); });
}

/** @param {string} key @param {(saving: boolean) => void} update */
function watchPreferenceWrite(key, update) {
  const write = preferenceWrites.get(key);
  if (!write) return;
  update(true);
  void write.finally(() => { update(false); });
}

/** @param {"board"|"workspace"} scope @param {string} key
 * @param {{data: Publishing, etag: string}|undefined} initial
 * @param {() => Promise<{data: Publishing, etag: string}>} read
 * @param {(enabled: boolean, tag: string) => Promise<unknown>} save
 * @param {() => Promise<void>} afterSave @param {Inspection} inspection */
function renderPublishing(scope, key, initial, read, save, afterSave, inspection) {
  const panel = element("section");
  const enabled = element("input");
  enabled.type = "checkbox";
  enabled.checked = publishingDrafts.get(key) ?? initial?.data.enabled ?? false;
  const current = element("p");
  current.id = scope + "-publishing-status";
  current.setAttribute("aria-live", "polite");
  current.textContent = initial ? publishingStatus(initial.data) : "Publishing status is unavailable.";
  let saved = initial;
  /** @param {{data: Publishing, etag: string}} reply */
  function show(reply) {
    if (!panel.isConnected) return;
    if (!publishingDrafts.has(key) && (!saved || enabled.checked === saved.data.enabled)) {
      enabled.checked = reply.data.enabled;
    }
    saved = reply;
    retain();
    current.textContent = publishingStatus(reply.data);
    delete settings.dataset.unavailable;
    inspection.sync();
  }
  function retain() {
    if (!saved) return;
    if (enabled.checked === saved.data.enabled) publishingDrafts.delete(key);
    else publishingDrafts.set(key, enabled.checked);
  }
  enabled.addEventListener("change", () => {
    if (saved) retain();
    else publishingDrafts.set(key, enabled.checked);
  });
  const option = field("Enable " + scope + " publishing", enabled);
  option.className = "check-option";
  /** @param {boolean} saving */
  const disableWhileSaving = (saving) => { enabled.disabled = saving; };
  watchPreferenceWrite(key, disableWhileSaving);
  const settings = form("Save " + scope + " publishing", [option], (submit) => {
    if (!saved) return;
    const requested = enabled.checked;
    const expected = saved.etag;
    trackPreferenceWrite(key, writeResource(submit, () => save(requested, expected), async () => {
      await afterSave();
      const reply = await read();
      show(reply);
      return reply.data.enabled === requested;
    }, "Publishing settings saved.", inspection));
    watchPreferenceWrite(key, disableWhileSaving);
  });
  if (!saved) {
    settings.dataset.unavailable = "true";
    const submit = /** @type {HTMLButtonElement} */ (settings.querySelector("button"));
    submit.disabled = true;
  }
  const refreshStatus = button("Refresh " + scope + " publishing status", () => {
    if (writing) return;
    if (pendingInspections.has(inspection.scope)) {
      inspection.inspect();
      return;
    }
    writing = true;
    refreshStatus.disabled = true;
    void readWithSession(read).then(show).catch(() => {
      if (panel.isConnected) current.textContent = "Publishing status could not be read. Try refreshing again.";
    }).finally(() => {
      finishWriting();
      refreshStatus.disabled = false;
    });
  });
  panel.append(element("h3", scope === "board" ? "Board publishing" : "Workspace publishing"),
    current, settings, refreshStatus);
  return { panel, show, retain };
}

/** @template {keyof HTMLElementTagNameMap} Tag @param {Tag} tag @param {string} [text] */
function element(tag, text = "") {
  const node = document.createElement(tag);
  node.textContent = text;
  return node;
}

/** @param {string} title @param {HTMLElement} control */
function field(title, control) {
  const label = element("label", title);
  label.append(control);
  return label;
}

/** @param {string} title @param {() => void} action */
function button(title, action) {
  const node = element("button", title);
  node.type = "button";
  node.addEventListener("click", action);
  return node;
}

/** @param {Labeled[]} choices @param {string|null} selected */
function select(choices, selected) {
  const node = element("select");
  setOptions(node, choices, selected);
  return node;
}

/** @param {HTMLSelectElement} node @param {Labeled[]} choices @param {string|null} selected */
function setOptions(node, choices, selected) {
  node.replaceChildren();
  for (const choice of choices) {
    const option = element("option", choice.label);
    option.value = choice.id;
    node.append(option);
  }
  if (selected !== null) node.value = selected;
}

/** @param {string} title @param {HTMLElement[]} controls @param {(submit: HTMLButtonElement) => void} action */
function form(title, controls, action) {
  const node = element("form");
  const submit = element("button", title);
  submit.type = "submit";
  node.append(...controls, submit);
  node.addEventListener("submit", (event) => {
    event.preventDefault();
    if (!writing && !submit.disabled && node.closest('[data-unavailable="true"]') === null) action(submit);
  });
  return node;
}

/** @template T @param {Reply} reply @param {string} collection @returns {T[]} */
function items(reply, collection) {
  const value = /** @type {Record<string, unknown>} */ (reply.data)[collection];
  if (!Array.isArray(value)) throw new ApiError("invalid_response");
  return value;
}

/** @template T @param {(cursor?: string) => Promise<Reply>} request
 * @param {string} collection @param {AbortSignal} [signal] @returns {Promise<T[]>} */
async function collect(request, collection, signal) {
  /** @type {T[]} */
  const result = [];
  /** @type {string|undefined} */
  let cursor;
  const seen = new Set();
  for (;;) {
    signal?.throwIfAborted();
    const reply = await request(cursor);
    signal?.throwIfAborted();
    result.push(.../** @type {T[]} */ (items(reply, collection)));
    const next = /** @type {{next_cursor?: unknown}} */ (reply.data).next_cursor;
    if (next === null) return result;
    if (typeof next !== "string" || !next || seen.has(next)) throw new ApiError("invalid_response");
    seen.add(next);
    cursor = next;
  }
}

/** @param {Reply} reply */
function etag(reply) {
  if (reply.etag === null) throw new ApiError("invalid_response");
  return reply.etag;
}

/** @param {AbortSignal} [signal] */
async function ensureCapability(signal) {
  signal?.throwIfAborted();
  if (capability) return;
  const session = connecting ??= openBrowserSession().then((token) => { capability = token; return token; })
    .finally(() => { connecting = undefined; });
  if (!signal) {
    await session;
    return;
  }
  /** @type {() => void} */
  let aborted = () => {};
  try {
    await new Promise((resolve, reject) => {
      aborted = () => reject(signal.reason);
      signal.addEventListener("abort", aborted, { once: true });
      session.then(resolve, reject);
    });
  } finally {
    signal.removeEventListener("abort", aborted);
  }
}

/** @template T @param {() => Promise<T>} read @param {AbortSignal} [signal] */
async function readWithSession(read, signal) {
  await ensureCapability(signal);
  signal?.throwIfAborted();
  const original = capability;
  try {
    return await read();
  } catch (error) {
    if (!(error instanceof ApiError) || error.status !== 401) throw error;
    if (capability === original) capability = "";
    await ensureCapability(signal);
    signal?.throwIfAborted();
    return read();
  }
}

/** @param {HTMLInputElement|HTMLTextAreaElement} control @param {string} previous @param {string} saved */
function adoptText(control, previous, saved) {
  if (control.value === previous) {
    control.value = saved;
    return control.value;
  }
  // Use the browser's value normalization without disturbing the dirty control.
  const probe = /** @type {HTMLInputElement|HTMLTextAreaElement} */ (control.cloneNode(false));
  probe.value = saved;
  return probe.value;
}

/** @param {unknown} error */
function writePhase(error) {
  if (!(error instanceof ApiError)) return "error";
  if (error.status === 401) capability = "";
  if (error.status === 412) return "conflict";
  if (error.outcomeUnknown) return "unknown";
  return "error";
}

/** @param {HTMLElement} region @param {() => boolean} isCurrent @param {InspectionRead} read
 * @param {string} resource @param {string} scope @returns {Inspection} */
function createInspection(region, isCurrent, read, resource, scope) {
  const label = "Inspect saved " + resource;

  /** @param {boolean} disabled */
  function setSubmissionsDisabled(disabled) {
    for (const control of region.querySelectorAll("form button[type=submit]")) {
      if (control instanceof HTMLButtonElement) control.disabled = disabled
        || control.closest('[data-unavailable="true"]') !== null || control.dataset.publicationPending === "true";
    }
  }

  return {
    scope,
    label,
    require(focusedRead) {
      pendingInspections.set(scope, { read: focusedRead, isCurrent });
      if (cardInspection?.scope === scope) cardInspection.sync();
      if (boardInspection?.scope === scope) boardInspection.sync();
    },
    sync() {
      if (isCurrent()) setSubmissionsDisabled(pendingInspections.has(scope));
    },
    inspect() {
      if (writing || !isCurrent()) return;
      writing = true;
      announce("loading", "Reading saved " + resource + "…");
      const pending = pendingInspections.get(scope);
      void readWithSession(async () => {
        if (!pending) return read();
        const confirmed = await pending.read();
        if (!pending.isCurrent() && isCurrent()) await read();
        return confirmed;
      }).then((confirmed) => {
        if (pendingInspections.get(scope) === pending) pendingInspections.delete(scope);
        if (cardInspection?.scope === scope) cardInspection.sync();
        if (boardInspection?.scope === scope) boardInspection.sync();
        if (!isCurrent()) return;
        if (confirmed === false) {
          announce("unknown", "The submission could not be confirmed. Compare the saved state with your draft.");
        } else {
          announce("ready", "Saved " + resource + " read. Compare it with your draft.");
        }
      }).catch(() => {
        if (isCurrent()) announce("error", "Saved state could not be read.");
      }).finally(finishWriting);
    },
  };
}

/** @param {HTMLButtonElement} submit @param {() => Promise<unknown>} request
 * @param {InspectionRead} update @param {string} success @param {Inspection} inspection */
async function writeResource(submit, request, update, success, inspection) {
  const view = activeView;
  writing = true;
  submit.disabled = true;
  announce("writing", "Saving…");
  let phase = "ready";
  try {
    await request();
  } catch (error) {
    phase = writePhase(error);
    if (phase === "error" && view === activeView) {
      announce(phase, error instanceof ApiError && error.status === 413
        ? error.code === "snapshot_too_large" ? "Native backups are limited to 96 MiB." : "Attachments are limited to 10 MiB."
        : error instanceof ApiError && error.code === "invalid_target"
          ? "Choose a different, existing ticket on this board."
          : "The service refused the change. Your draft is still here.");
    }
  }
  let inspected = false;
  try {
    if (phase !== "error") {
      const confirmed = await readWithSession(update);
      inspected = true;
      if (phase === "ready" && confirmed === false) phase = "unknown";
      if (view === activeView) {
        if (phase === "unknown" && confirmed === true) {
          announce("ready", success + " Confirmed by readback.");
        } else {
          announce(phase, phase === "ready" ? success
            : phase === "conflict" ? "State changed. Inspect the latest state before making another edit."
              : "The outcome was uncertain. Inspect the latest state before making another edit.");
        }
      }
    }
  } catch {
    inspection.require(update);
    if (view === activeView) {
      announce(phase === "ready" ? "error" : phase,
        "Readback failed. Use " + inspection.label + " before making another edit.");
    }
  } finally {
    finishWriting();
    submit.disabled = (phase !== "error" && !inspected) || submit.dataset.publicationPending === "true"
      || submit.closest('[data-unavailable="true"]') !== null;
  }
}

/** @param {unknown} error */
function readError(error) {
  return error instanceof ApiError && error.status === 404
    ? "This board or card is unavailable."
    : "The latest state could not be read. Use Refresh to try again.";
}

/** @param {string} href */
function go(href) {
  if (writing) return;
  history.pushState(null, "", href);
  void load();
}

/** @param {string} href @param {string} title */
function link(href, title) {
  const node = element("a", title);
  node.href = href;
  node.addEventListener("click", (event) => followLink(event, href));
  return node;
}

/** @param {MouseEvent} event @param {string} href */
function followLink(event, href) {
  if (event.button !== 0 || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
  event.preventDefault();
  go(href);
}

/** @param {string} boardId @param {number} ticket */
function ticketHref(boardId, ticket) {
  return "/boards/" + boardId + "?ticket=" + ticket;
}

/** @param {string} boardId @param {number} ticket */
async function findCardByTicket(boardId, ticket) {
  const matches = /** @type {Card[]} */ (items(await api.listCards(boardId, { ticket, archived: "include", limit: 2 }), "cards"));
  return matches.length === 1 && matches[0].board_id === boardId && matches[0].ticket === ticket ? matches[0] : undefined;
}

/** @param {HTMLElement} body @param {Comment} comment @param {string} boardId @param {User[]} users */
function renderCommentText(body, comment, boardId, users) {
  body.className = "body-text";
  if (!users.some((user) => user.id === comment.author_id && user.kind === "human")) {
    body.textContent = comment.text;
    return;
  }
  let last = 0;
  for (const match of comment.text.matchAll(/(?<![\p{L}\p{N}_#])#([0-9]+)(?![\p{L}\p{N}_])/gu)) {
    const ticket = Number(match[1]);
    if (!Number.isSafeInteger(ticket) || ticket < 1) continue;
    body.append(comment.text.slice(last, match.index), link(ticketHref(boardId, ticket), match[0]));
    last = match.index + match[0].length;
  }
  body.append(comment.text.slice(last));
}

const relationshipLabels = Object.freeze({
  blocks: "Blocks", blocked_by: "Blocked by", duplicates: "Duplicates", duplicated_by: "Duplicated by",
  references: "References", referenced_by: "Referenced by", relates_to: "Relates to",
});
/** @type {Partial<Record<RelationshipKind, RelationshipKind>>} */
const inverseRelationships = { blocks: "blocked_by", duplicates: "duplicated_by", references: "referenced_by",
  blocked_by: "blocks", duplicated_by: "duplicates", referenced_by: "references" };
const relationshipChoices = Object.entries(relationshipLabels).map(([id, label]) => ({ id, label }));

/** @param {Relationship} entry @param {string} cardId */
function relationshipDirection(entry, cardId) {
  return entry.source_card_id === cardId ? entry.kind : inverseRelationships[entry.kind] ?? entry.kind;
}

function ticketInput() {
  const control = element("input");
  control.type = "number";
  control.min = "1";
  control.max = String(Number.MAX_SAFE_INTEGER);
  return control;
}

/** @param {RelationshipSeedDraft} row @param {SubmittedRelationshipSeed[]} submitted */
function matchesSubmittedSeed(row, submitted) {
  return submitted.some((seed) => seed.row === row && seed.ticket === row.ticket && seed.kind === row.kind);
}

/** @param {string} boardId */
function renderRelationshipSeeds(boardId) {
  const panel = element("details");
  const list = element("div");
  const state = relationshipSeedDrafts.get(boardId) ?? { rows: [] };
  relationshipSeedDrafts.set(boardId, state);
  /** @type {Map<RelationshipSeedDraft, {node: HTMLElement, ticket: HTMLInputElement, kind: HTMLSelectElement}>} */
  const controls = new Map();
  const add = button("Add relationship", () => {
    if (state.rows.length >= 1000) return;
    append({ ticket: "", kind: "relates_to" }).focus();
    add.disabled = state.rows.length >= 1000;
  });
  panel.append(element("summary", "Link the new card"), list, add);

  /** @param {RelationshipSeedDraft} row */
  function mount(row) {
    const node = element("fieldset");
    const ticket = ticketInput();
    ticket.value = row.ticket;
    const kind = select(relationshipChoices, row.kind);
    node.append(element("legend", "Relationship"), field("Target ticket", ticket), field("Link type", kind),
      button("Remove relationship", () => {
        remove(row);
        add.focus();
      }));
    controls.set(row, { node, ticket, kind });
    list.append(node);
    return ticket;
  }

  /** @param {RelationshipSeedDraft} row */
  function append(row) {
    state.rows.push(row);
    return mount(row);
  }

  /** @param {RelationshipSeedDraft} row */
  function remove(row) {
    controls.get(row)?.node.remove();
    controls.delete(row);
    state.rows = state.rows.filter((candidate) => candidate !== row);
    add.disabled = false;
  }

  function syncValues() {
    for (const [row, { ticket, kind }] of controls) {
      row.ticket = ticket.value;
      row.kind = /** @type {RelationshipKind} */ (kind.value);
    }
  }

  for (const row of state.rows) mount(row);
  if (!state.rows.length) append({ ticket: "", kind: "relates_to" });
  panel.open = state.rows.some((row) => row.ticket !== "");
  add.disabled = state.rows.length >= 1000;
  state.discard = (submitted) => {
    syncValues();
    for (const row of state.rows) {
      if (matchesSubmittedSeed(row, submitted)) remove(row);
    }
    if (!state.rows.length) append({ ticket: "", kind: "relates_to" });
  };
  return {
    panel,
    capture() {
      syncValues();
      return state.rows.filter((row) => row.ticket !== "").map((row) => ({ ...row, row }));
    },
    /** @param {SubmittedRelationshipSeed[]} submitted */
    clearSubmitted(submitted) {
      if (!submitted.length) return;
      if (state.discard) state.discard(submitted);
      else state.rows = state.rows.filter((row) => !matchesSubmittedSeed(row, submitted));
    },
    retain() {
      syncValues();
      delete state.discard;
    },
  };
}

/** @param {string} boardId @param {SubmittedRelationshipSeed[]} submitted @param {() => boolean} isCurrent */
async function resolveRelationshipSeeds(boardId, submitted, isCurrent) {
  /** @type {Map<number, string>} */
  const targets = new Map();
  for (const { ticket } of submitted) {
    const number = Number(ticket);
    if (!isCurrent() || !Number.isSafeInteger(number) || number < 1) throw new ApiError("invalid_target");
    if (targets.has(number)) continue;
    const target = await readWithSession(() => findCardByTicket(boardId, number));
    if (!target || !isCurrent()) throw new ApiError("invalid_target");
    targets.set(number, target.id);
  }
  return submitted.map(({ ticket, kind }) => {
    const id = targets.get(Number(ticket));
    if (!id) throw new ApiError("invalid_target");
    return { target_card_id: id, kind };
  });
}

/** @param {string} name */
function downloadName(name) {
  const safe = name.replace(/[<>:"/\\|?*\p{Cc}\p{Cf}]/gu, "_").replace(/^[ .]+|[ .]+$/g, "");
  if (!safe) return "attachment";
  return /^(?:con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³])(?:\s*\.|$)/i.test(safe) ? "_" + safe : safe;
}

/** @param {string} boardId @param {string} cardId @param {Attachment} attachment */
function activeAttachment(boardId, cardId, attachment) {
  return attachment.board_id === boardId && attachment.card_id === cardId && attachment.active === true;
}

/** @param {string} boardId @param {Pick<Card, "id">} summary @param {Lane[]} lanes @param {User[]} users
 * @param {Labeled[]} priorities @param {() => string} boardTag @param {() => Promise<void>} reloadBoard
 * @param {HTMLElement} opener @param {boolean} [restoring] */
async function openCard(boardId, summary, lanes, users, priorities, boardTag, reloadBoard, opener, restoring = false) {
  if (!details) return;
  retainCardDraft?.();
  releaseCardMedia?.();
  releaseCardMedia = undefined;
  retainCardDraft = undefined;
  cardInspection = undefined;
  retrySelectedCard = undefined;
  activeComment = undefined;
  activeDrawer = undefined;
  const view = details;
  const cardView = ++activeCard;
  rememberCard(boardId, summary.id);
  const isSelected = () => cardView === activeCard && boardViewState(boardId).card?.id === summary.id;
  const initialFocus = document.activeElement;
  let focusHeading = !restoring;
  view.hidden = false;
  delete view.dataset.unavailable;
  view.replaceChildren(element("p", "Loading card…"));
  let invalidSelection = false;
  try {
    let reply;
    for (let attempt = 0; attempt < 2; attempt++) {
      // The board tag changes with its in-place paint, before auxiliary reads finish.
      const requestedTag = boardTag();
      try {
        const result = await readWithSession(() => api.getCard(summary.id));
        if (!isSelected()) return;
        if (requestedTag === boardTag()) {
          reply = result;
          break;
        }
      } catch (error) {
        if (!isSelected()) return;
        if (requestedTag === boardTag()) throw error;
      }
      focusHeading = false;
    }
    if (!reply) throw new Error("The board changed during both card reads.");
    const card = /** @type {Card} */ (reply.data);
    if (card.id !== summary.id || card.board_id !== boardId) {
      invalidSelection = true;
      throw new ApiError("invalid_response");
    }
    let cardTag = etag(reply);
    let savedCard = card;
    const title = element("input");
    title.value = card.title;
    const description = element("textarea");
    description.value = card.description;
    let titleBaseline = title.value;
    let descriptionBaseline = description.value;
    const lane = select(activeLanes(lanes), card.lane_id);
    lane.required = true;
    const owner = select(users, card.owner_id);
    owner.required = true;
    const priority = select(priorities, card.priority_id);
    priority.required = true;
    const current = element("div");
    const commentList = element("div");
    const attachmentList = element("div");
    const historyPanel = element("details");
    const historyList = element("div");
    const relationshipList = element("div");
    const hierarchy = element("section");
    const hierarchyList = element("div");
    hierarchy.append(element("h3", "Hierarchy"), hierarchyList);
    hierarchy.hidden = true;
    let hierarchyRead = 0;
    const archiveAction = element("div");
    const approvalAction = element("div");
    let attachmentGeneration = 0;
    /** @type {Set<string>} */
    const previewUrls = new Set();
    function clearPreviews() {
      ++attachmentGeneration;
      for (const url of previewUrls) URL.revokeObjectURL(url);
      previewUrls.clear();
    }
    releaseCardMedia = clearPreviews;
    const comment = element("textarea");
    comment.required = true;
    activeComment = { id: card.id, control: comment };
    const upload = element("input");
    upload.type = "file";
    upload.required = true;
    const previous = cardDrafts.get(card.id);
    if (previous?.title !== undefined) title.value = previous.title;
    if (previous?.description !== undefined) description.value = previous.description;
    if (previous?.lane !== undefined && activeLanes(lanes).some(({ id }) => id === previous.lane)) lane.value = previous.lane;
    if (previous?.owner !== undefined && users.some(({ id }) => id === previous.owner)) owner.value = previous.owner;
    if (previous?.priority !== undefined && priorities.some(({ id }) => id === previous.priority)) priority.value = previous.priority;
    if (previous?.comment !== undefined) comment.value = previous.comment;
    retainCardDraft = () => {
      /** @type {CardDraft} */
      const draft = {};
      if (title.value !== titleBaseline) draft.title = title.value;
      if (description.value !== descriptionBaseline) draft.description = description.value;
      if (lane.value && lane.value !== savedCard.lane_id) draft.lane = lane.value;
      if (owner.value !== savedCard.owner_id) draft.owner = owner.value;
      if (priority.value && priority.value !== savedCard.priority_id) draft.priority = priority.value;
      if (comment.value) draft.comment = comment.value;
      if (Object.keys(draft).length) cardDrafts.set(card.id, draft);
      else cardDrafts.delete(card.id);
    };
    /** @param {string} id */
    const userLabel = (id) => users.find((user) => user.id === id)?.label ?? "Unknown user";
    function closeDrawer() {
      if (cardView !== activeCard) return;
      retainCardDraft?.();
      clearPreviews();
      releaseCardMedia = undefined;
      retainCardDraft = undefined;
      cardInspection = undefined;
      activeComment = undefined;
      activeDrawer = undefined;
      rememberCard(boardId);
      ++activeCard;
      view.hidden = true;
      view.replaceChildren();
      const replacement = main?.querySelector(`button[data-card-id="${card.id}"]`);
      const target = replacement instanceof HTMLButtonElement ? replacement : opener.isConnected ? opener : refresh;
      target?.focus();
    }
    let workflowRead = 0;
    /** @param {() => boolean} [canApply] @param {boolean} [automatic] */
    async function refreshWorkflow(canApply = () => true, automatic = false) {
      const read = ++workflowRead;
      const isCurrent = () => cardView === activeCard && read === workflowRead && canApply();
      let cardRead = false;
      try {
        await readSavedCard(isCurrent);
        cardRead = true;
        if (!isCurrent()) return;
        await Promise.all([refreshLists(), refreshRelationships(), historyPanel.open ? refreshHistory() : undefined]);
      } catch (error) {
        if (!isCurrent()) return;
        if (cardRead || !(error instanceof ApiError) || error.status !== 404) {
          if (!automatic && !pendingInspections.has(inspection.scope)) inspection.require(refreshWorkflow);
          throw error;
        }
        retainCardDraft?.();
        rememberCard(boardId);
        view.dataset.unavailable = "true";
        ++listRead;
        ++relationshipRead;
        ++hierarchyRead;
        ++historyRead;
        clearPreviews();
        heading.textContent = "Card unavailable";
        current.replaceChildren(element("p", "This card is absent from the current board. Your drafts are retained."));
        commentList.replaceChildren();
        attachmentList.replaceChildren();
        relationshipList.replaceChildren();
        hierarchy.hidden = true;
        hierarchyList.replaceChildren();
        historyList.replaceChildren();
        archiveAction.hidden = true;
        approvalAction.hidden = true;
        inspection.sync();
      }
    }
    activeDrawer = { boardId, id: card.id, close: closeDrawer, refreshWorkflow, refreshPriority };
    const close = button("Close card", () => { if (!writing) closeDrawer(); });
    const heading = element("h2", `#${card.ticket} ${card.title}`);
    heading.tabIndex = -1;
    const inspection = createInspection(view, () => cardView === activeCard, async () => {
      await reloadBoard();
    }, "card", "card:" + card.id);
    cardInspection = inspection;

    /** @param {Card} saved */
    async function refreshHierarchy(saved) {
      const read = ++hierarchyRead;
      const ids = [...(saved.parent_id ? [saved.parent_id] : []), ...(saved.child_ids ?? [])];
      hierarchy.hidden = ids.length === 0;
      if (!ids.length) {
        hierarchyList.replaceChildren();
        return;
      }
      const isCurrent = () => cardView === activeCard && read === hierarchyRead;
      try {
        const targets = await readWithSession(async () => {
          /** @type {Card[]} */
          const found = [];
          for (let i = 0; i < ids.length && isCurrent(); i += 16) {
            found.push(...await Promise.all(ids.slice(i, i + 16).map(async (id) => {
              const target = /** @type {Card} */ ((await api.getCard(id)).data);
              if (target.board_id !== boardId || target.id !== id) throw new ApiError("invalid_response");
              return target;
            })));
          }
          return found;
        });
        if (!isCurrent()) return;
        const focusedId = [...hierarchyList.querySelectorAll("a")].find((node) =>
          node === document.activeElement)?.dataset.hierarchyCardId;
        const rows = targets.map((target) => {
          const row = element("p", (target.id === saved.parent_id ? "Parent" : "Child") + " ");
          const openerLink = element("a", "#" + target.ticket + " " + target.title);
          openerLink.href = ticketHref(boardId, target.ticket);
          openerLink.dataset.hierarchyCardId = target.id;
          openerLink.addEventListener("click", (event) => {
            if (event.button !== 0 || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
            event.preventDefault();
            if (!writing && cardView === activeCard
                && (savedCard.parent_id === target.id || savedCard.child_ids?.includes(target.id))) {
              void openCard(boardId, target, lanes, users, priorities, boardTag, reloadBoard, opener);
            }
          });
          row.append(openerLink);
          return row;
        });
        hierarchyList.replaceChildren(...rows);
        if (focusedId) [...hierarchyList.querySelectorAll("a")].find((node) =>
          node.dataset.hierarchyCardId === focusedId)?.focus({ preventScroll: true });
      } catch {
        if (isCurrent()) hierarchyList.replaceChildren(element("p", "Hierarchy could not be read."));
      }
    }

    /** @param {Card} saved */
    function showCurrent(saved) {
      heading.textContent = `#${saved.ticket} ${saved.title}`;
      updateActions(saved);
      void refreshHierarchy(saved);
      const body = element("p", saved.description);
      body.className = "body-text";
      const savedLane = lanes.find(({ id }) => id === saved.lane_id);
      current.replaceChildren(element("h3", "Saved card"), element("p", saved.title), body,
        element("p", "Lane: " + (savedLane?.label ?? "Unavailable") + (savedLane?.retired ? " (retired)" : "")),
        element("p", "Priority: " + (priorities.find(({ id }) => id === saved.priority_id)?.label ?? "Unavailable")),
        ...(saved.archived ? [element("p", "Archived")] : []),
        element("p", "Owner: " + (users.find(({ id }) => id === saved.owner_id)?.label ?? "Unknown user")));
    }

    /** @param {Card} saved */
    function adoptSaved(saved) {
      titleBaseline = adoptText(title, titleBaseline, saved.title);
      descriptionBaseline = adoptText(description, descriptionBaseline, saved.description);
      if (!lane.value || lane.value === savedCard.lane_id) lane.value = saved.lane_id;
      if (owner.value === savedCard.owner_id) owner.value = saved.owner_id;
      if (priority.value === savedCard.priority_id) priority.value = saved.priority_id;
      savedCard = saved;
      showCurrent(saved);
    }

    let savedRead = 0;
    /** @param {() => boolean} [canApply] */
    async function readSavedCard(canApply = () => true) {
      const read = ++savedRead;
      const latest = await api.getCard(card.id);
      const saved = /** @type {Card} */ (latest.data);
      const tag = etag(latest);
      if (saved.id !== card.id || saved.board_id !== boardId) {
        throw new ApiError("invalid_response");
      }
      if (cardView === activeCard && read === savedRead && canApply()) {
        delete view.dataset.unavailable;
        const laneSelection = lane.value || savedCard.lane_id;
        const ownerSelection = owner.value || savedCard.owner_id;
        setOptions(lane, activeLanes(lanes).some(({ id }) => id === laneSelection)
          ? activeLanes(lanes) : [...activeLanes(lanes), { id: laneSelection, label: "Unavailable lane draft" }], laneSelection);
        setOptions(owner, users.some(({ id }) => id === ownerSelection)
          ? users : [...users, { id: ownerSelection, label: "Unavailable owner draft" }], ownerSelection);
        cardTag = tag;
        adoptSaved(saved);
      }
      return saved;
    }

    /** @param {Record<string, string>} draft @param {HTMLButtonElement} submit */
    async function save(draft, submit) {
      if (cardView !== activeCard) return;
      writing = true;
      submit.disabled = true;
      announce("writing", "Saving card…");
      let refreshed = false;
      let allowSubmit = true;
      try {
        const result = await api.patchCard(card.id, cardTag, draft);
        if (cardView !== activeCard) return;
        const saved = /** @type {Card} */ (result.data);
        if (saved.board_id !== boardId) throw new ApiError("invalid_response", { outcomeUnknown: true });
        cardTag = etag(result);
        adoptSaved(saved);
        refreshed = true;
        announce("ready", "Card saved.");
      } catch (error) {
        const phase = writePhase(error);
        if (cardView !== activeCard) {
          if (phase !== "error") inspection.require(async () => { await readSavedCard(); });
          return;
        }
        if (phase === "error") {
          announce("error", "Card edit refused. Your draft is still here.");
        } else {
          try {
            await readWithSession(readSavedCard);
            if (cardView !== activeCard) return;
            refreshed = true;
            announce(phase, phase === "conflict"
              ? "The card changed. Compare your draft with the saved card before saving again."
              : "The save outcome was uncertain. Compare your draft with the saved card before saving again.");
          } catch {
            allowSubmit = false;
            announce(phase, "Readback failed. Use Inspect saved card before making another edit.");
            inspection.require(async () => { await readSavedCard(); });
            return;
          }
        }
      } finally {
        if (refreshed && cardView === activeCard) await reloadBoard().catch(() => {});
        finishWriting();
        if (cardView === activeCard) submit.disabled = !allowSubmit || view.dataset.unavailable === "true";
      }
    }

    let listRead = 0;
    /** @param {Attachment} [submittedAttachment] */
    async function refreshLists(submittedAttachment) {
      const read = ++listRead;
      const [comments, attachments] = await Promise.all([
        /** @type {Promise<Comment[]>} */ (collect((cursor) =>
          api.listComments({ board_id: boardId, card_id: card.id }, undefined, cursor), "comments")),
        /** @type {Promise<Attachment[]>} */ (collect((cursor) => api.listAttachments(card.id, cursor), "attachments")),
      ]);
      if (submittedAttachment && !attachments.some(({ id }) => id === submittedAttachment.id)) {
        attachments.push(submittedAttachment);
      }
      if (cardView !== activeCard || read !== listRead) return { comments, attachments };
      commentList.replaceChildren(...comments.map((entry) => {
        const item = element("article");
        const body = element("p");
        renderCommentText(body, entry, boardId, users);
        item.append(element("small", userLabel(entry.author_id) + " · " + entry.created_at), body);
        return item;
      }));
      clearPreviews();
      attachmentList.replaceChildren(...attachments.filter((entry) => activeAttachment(boardId, card.id, entry))
        .map(renderAttachment));
      inspection.sync();
      return { comments, attachments };
    }

    /** @param {Attachment} [submittedAttachment] */
    async function readCurrent(submittedAttachment) {
      const saved = await readSavedCard();
      await reloadBoard();
      const [resources] = await Promise.all([refreshLists(submittedAttachment),
        cardView === activeCard && historyPanel.open ? refreshHistory() : undefined]);
      return { ...resources, saved };
    }

    /** @param {string} id */
    async function readSubmittedAttachment(id) {
      let reply;
      try {
        reply = await api.getAttachment(id);
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) return undefined;
        throw error;
      }
      const item = /** @type {Attachment} */ (reply.data);
      if (item.id !== id || item.card_id !== card.id || item.board_id !== boardId) {
        throw new ApiError("invalid_response");
      }
      return item;
    }

    let historyRead = 0;
    async function refreshHistory() {
      const read = ++historyRead;
      try {
        const entries = /** @type {Change[]} */ (await readWithSession(() => collect((cursor) =>
          api.listCardActivity(boardId, "0001-01-01 00:00:00+00:00", "9999-12-31 23:59:59+00:00", savedCard.ticket, cursor),
        "activities")));
        if (entries.some((entry) => entry.board_id !== boardId)) throw new ApiError("invalid_response");
        if (cardView !== activeCard || read !== historyRead || !historyPanel.open) return;
        historyList.replaceChildren(...entries.reverse().map((entry) => {
          const row = element("article");
          const field = entry.resource_type === "relationship" ? null : entry.field;
          row.append(element("small", userLabel(entry.actor_id) + " · " + entry.at + " · Revision " + entry.revision),
            element("p", field === "priority_id" ? "Priority" : field ?? entry.operation));
          if (entry.resource_type === "attachment") row.append(element("small", "Attachment " + entry.resource_id));
          if (field !== null) {
            for (const [label, value] of [["Previous", entry.before], ["Saved", entry.after]]) {
              const shown = entry.field === "priority_id"
                ? priorities.find(({ id }) => id === value)?.label ?? value : value;
              const text = element("p", label + ": " + (shown ?? "—"));
              text.className = "body-text";
              row.append(text);
            }
          }
          return row;
        }));
        if (!entries.length) historyList.textContent = "No card history.";
      } catch {
        if (cardView === activeCard && read === historyRead && historyPanel.open) {
          historyList.textContent = "Card history could not be read. Close and reopen History to retry.";
        }
      }
    }
    historyPanel.append(element("summary", "History"), historyList);
    historyPanel.addEventListener("toggle", () => {
      historyList.replaceChildren();
      if (historyPanel.open) {
        historyList.textContent = "Reading card history…";
        void refreshHistory();
      }
    });

    /** @param {Attachment} entry */
    function renderAttachment(entry) {
      const row = element("article");
      row.className = "attachment";
      const file = element("p", entry.original_name + " · " + entry.size + " bytes · "
        + userLabel(entry.added_by) + " · " + entry.created_at);
      file.className = "body-text";
      row.append(file);
      const note = element("p");
      note.setAttribute("role", "status");
      const generation = attachmentGeneration;
      const current = () => cardView === activeCard && generation === attachmentGeneration && row.isConnected;
      /** @param {"attachment"|"inline"} disposition @param {HTMLButtonElement} control */
      async function readFile(disposition, control) {
        if (control.disabled || !current()) return undefined;
        control.disabled = true;
        note.textContent = "";
        try {
          const blob = await readWithSession(() => api.getAttachmentContent(entry.id, disposition, entry.size, entry.sha2_256));
          return current() ? blob : undefined;
        } catch {
          if (current()) note.textContent = "Attachment could not be read.";
          return undefined;
        } finally {
          control.disabled = false;
        }
      }
      const download = button("Download", () => {
        void readFile("attachment", download).then((blob) => {
          if (!blob || !current()) return;
          const url = URL.createObjectURL(blob);
          const anchor = element("a");
          anchor.href = url;
          anchor.download = downloadName(entry.original_name);
          row.append(anchor);
          anchor.click();
          anchor.remove();
          setTimeout(() => URL.revokeObjectURL(url), 1000);
        });
      });
      download.setAttribute("aria-label", "Download " + entry.original_name);
      row.append(download);
      if (entry.previewable) {
        const image = element("img");
        image.alt = entry.original_name;
        image.hidden = true;
        let url = "";
        function clearPreview() {
          image.hidden = true;
          image.removeAttribute("src");
          preview.textContent = "Preview";
          if (url) {
            URL.revokeObjectURL(url);
            previewUrls.delete(url);
            url = "";
          }
        }
        const preview = button("Preview", () => {
          if (url) { clearPreview(); return; }
          void readFile("inline", preview).then((blob) => {
            if (!blob || !current()) return;
            url = URL.createObjectURL(blob);
            previewUrls.add(url);
            image.src = url;
            image.hidden = false;
            preview.textContent = "Hide preview";
          });
        });
        image.addEventListener("error", () => {
          if (!current()) return;
          clearPreview();
          note.textContent = "The raster preview could not be displayed.";
        });
        row.append(preview, image);
        const selected = savedCard.cover_attachment_id === entry.id;
        row.append(form(selected ? "Clear cover" : "Set cover", [], (submit) => {
          if (!current()) return;
          const cover = selected ? null : entry.id;
          void writeResource(submit, () => api.patchCard(card.id, cardTag, { cover_attachment_id: cover }),
            async () => (await readCurrent()).saved.cover_attachment_id === cover,
            selected ? "Cover cleared." : "Cover set.", inspection);
        }));
      }
      const detach = form("Detach", [], (submit) => {
        if (!current()) return;
        const expected = boardTag();
        void writeResource(submit, () => api.detachAttachment(entry.id, entry.revision, expected), async () => {
          const metadata = await readSubmittedAttachment(entry.id);
          const { saved } = await readCurrent();
          return metadata?.active === false && saved.cover_attachment_id !== entry.id;
        }, "Attachment detached.", inspection);
      });
      detach.querySelector("button")?.setAttribute("aria-label", "Detach " + entry.original_name);
      row.append(detach, note);
      return row;
    }

    let relationshipRead = 0;
    /** @type {Map<string, RelationshipEditor>} */
    let relationshipEditors = new Map();
    async function refreshRelationships() {
      const read = ++relationshipRead;
      const links = /** @type {Relationship[]} */ (await collect((cursor) =>
        api.listRelationships(boardId, card.id, cursor), "relationships"));
      const activeLinks = links.filter((entry) => entry.board_id === boardId
        && (entry.source_card_id === card.id || entry.target_card_id === card.id));
      const targets = new Map(await Promise.all([...new Set(activeLinks.map((entry) =>
        entry.source_card_id === card.id ? entry.target_card_id : entry.source_card_id))].map(async (id) => {
        const other = /** @type {Card} */ ((await api.getCard(id)).data);
        if (other.board_id !== boardId || other.id !== id) throw new ApiError("invalid_response");
        return /** @type {const} */ ([id, other]);
      })));
      if (cardView !== activeCard || read !== relationshipRead) return activeLinks;
      const focusedId = [...relationshipEditors].find(([, editor]) => editor.type === document.activeElement)?.[0];
      /** @type {Map<string, RelationshipEditor>} */
      const nextEditors = new Map();
      const rows = activeLinks.map((entry) => {
        const other = targets.get(entry.source_card_id === card.id ? entry.target_card_id : entry.source_card_id);
        const direction = relationshipDirection(entry, card.id);
        const row = element("article");
        const title = element("p", relationshipLabels[direction] + " ");
        if (other) title.append(link(ticketHref(boardId, other.ticket), "#" + other.ticket + " " + other.title));
        row.append(title);
        const previous = relationshipEditors.get(entry.id);
        const selected = previous && previous.type.value !== previous.direction ? previous.type.value : direction;
        const type = select(relationshipChoices, selected);
        const editor = { type, direction, expectedRevision: selected === direction ? entry.revision
          : previous?.expectedRevision ?? entry.revision };
        nextEditors.set(entry.id, editor);
        const stale = element("p", "Saved link changed. Review the saved type and change the selection before saving.");
        stale.hidden = editor.expectedRevision === entry.revision;
        type.addEventListener("change", () => {
          editor.expectedRevision = entry.revision;
          stale.hidden = true;
        });
        row.append(form("Change link type", [field("Link type", type), stale], (submit) => {
          const wanted = /** @type {RelationshipKind} */ (type.value);
          if (wanted === direction) return;
          const canonical = entry.source_card_id === card.id ? wanted : inverseRelationships[wanted] ?? wanted;
          void writeResource(submit, () => api.replaceRelationship(entry.id, editor.expectedRevision, boardTag(), canonical),
            async () => (await readLinks()).some((value) => value.id === entry.id
              && relationshipDirection(value, card.id) === wanted), "Link type changed.", inspection);
        }), form("Remove link", [], (submit) => {
          void writeResource(submit, () => api.removeRelationship(entry.id, entry.revision, boardTag()),
            async () => !(await readLinks()).some((value) => value.id === entry.id), "Link removed.", inspection);
        }));
        return row;
      });
      relationshipList.replaceChildren(...rows);
      relationshipEditors = nextEditors;
      if (!activeLinks.length) relationshipList.textContent = "No links.";
      inspection.sync();
      if (focusedId) relationshipEditors.get(focusedId)?.type.focus({ preventScroll: true });
      return activeLinks;
    }

    async function readLinks() {
      const [links] = await Promise.all([refreshRelationships(), readSavedCard(), reloadBoard()]);
      if (cardView === activeCard && historyPanel.open) await refreshHistory();
      return links;
    }
    const linkTicket = ticketInput();
    linkTicket.required = true;
    const linkType = select(relationshipChoices, "relates_to");
    const addLink = form("Add link", [field("Target ticket", linkTicket), field("Link type", linkType)], (submit) => {
      const id = crypto.randomUUID();
      const targetTicket = linkTicket.valueAsNumber;
      const kind = /** @type {RelationshipKind} */ (linkType.value);
      const expected = boardTag();
      void writeResource(submit, async () => {
        const target = await readWithSession(() => findCardByTicket(boardId, targetTicket));
        if (!target || target.id === card.id || cardView !== activeCard) throw new ApiError("invalid_target");
        await api.createRelationship(id, boardId, expected, card.id, target.id, kind);
      }, async () => {
        const confirmed = (await readLinks()).some((entry) => entry.id === id);
        if (confirmed && linkTicket.valueAsNumber === targetTicket) linkTicket.value = "";
        return confirmed;
      }, "Link added.", inspection);
    });

    /** @param {Card} saved */
    function canChangeArchive(saved) {
      return !saved.archived || lanes.some(({ id, retired }) => id === saved.lane_id && !retired);
    }
    const archiveForm = form("Archive card", [], (submit) => {
      if (cardView !== activeCard || !canChangeArchive(savedCard)) return;
      const archived = !savedCard.archived;
      void writeResource(submit, () => api.patchCard(card.id, cardTag, { archived }),
        async () => (await readCurrent()).saved.archived === archived,
        archived ? "Card archived." : "Card restored.", inspection);
    });
    archiveAction.append(archiveForm);
    const approveForm = form("Reviewed & approved", [], (submit) => {
      const id = newOperationId();
      let approved = false;
      void writeResource(submit, async () => {
        await api.approveCard(id, boardId, boardTag(), card.id, cardTag);
        approved = true;
      }, async () => {
        if (!approved) {
          try {
            await api.inspectApproval(id, boardId, card.id);
            approved = true;
          } catch (error) {
            if (!(error instanceof ApiError) || error.status !== 404) throw error;
          }
        }
        if (approved) {
          await reloadBoard().catch(() => {});
          closeDrawer();
        } else {
          await readCurrent();
        }
        return approved;
      }, "Reviewed and moved to Completed.", inspection);
    });
    approvalAction.append(approveForm);

    const priorityAction = element("div");
    const priorityResult = element("p");
    priorityResult.setAttribute("aria-live", "polite");
    priority.addEventListener("change", () => { priorityResult.textContent = ""; });
    /** @param {string} requested @param {Labeled|undefined} effective @param {boolean} replaced */
    async function refreshPriority(requested, effective, replaced) {
      try {
        await reloadBoard();
      } catch (error) {
        if (replaced && cardView === activeCard) {
          priorityResult.textContent = "Readback failed. Use " + inspection.label + " before making another edit.";
        }
        throw error;
      }
      if (cardView !== activeCard || view.dataset.unavailable === "true") return;
      if (effective) {
        if (priority.value === requested) priority.value = savedCard.priority_id;
        priorityResult.textContent = effective.id === requested
          ? "Effective priority: " + effective.label + "."
          : "Dependency floor kept the effective priority at " + effective.label + ".";
      } else if (replaced) {
        priorityResult.textContent = "Compare the saved priority with your selection before making another edit.";
      }
    }
    const setPriority = form("Set priority", [field("Priority", priority)], (submit) => {
      if (cardView !== activeCard) return;
      const requested = priority.value;
      if (requested === savedCard.priority_id || !priorities.some(({ id }) => id === requested)) return;
      const expected = cardTag;
      priorityResult.textContent = "";
      /** @type {Labeled | undefined} */
      let effective;
      void writeResource(submit, async () => {
        const reply = await api.patchCard(card.id, expected, { priority_id: requested });
        const saved = /** @type {Card} */ (reply.data);
        effective = priorities.find(({ id }) => id === saved?.priority_id);
        if (saved?.id !== card.id || saved.board_id !== boardId || !effective) {
          effective = undefined;
          throw new ApiError("invalid_response", { outcomeUnknown: true });
        }
      }, async () => {
        const drawer = activeDrawer;
        const sameCard = drawer?.boardId === boardId && drawer.id === card.id;
        if (sameCard) await drawer.refreshPriority(requested, effective, cardView !== activeCard);
        const ticket = new URLSearchParams(location.search).get("ticket");
        if ((!sameCard || drawer !== activeDrawer) && boardAtLocation() === boardId
          && rememberedCard(boardId, ticket) === card.id) {
          // A replacement drawer was still opening or changed during readback.
          await load();
        }
        return effective !== undefined;
      }, "Priority saved.", inspection);
    });
    priorityAction.append(setPriority, priorityResult);

    /** @param {Card} saved */
    function updateActions(saved) {
      archiveAction.hidden = !canChangeArchive(saved);
      const control = archiveForm.querySelector("button");
      if (control) control.textContent = saved.archived ? "Restore card" : "Archive card";
      approvalAction.hidden = saved.archived || !activeLanes(lanes).some(({ id, role }) =>
        id === saved.lane_id && role === "ready_for_review");
    }

    const inspect = button(inspection.label, inspection.inspect);
    showCurrent(card);
    view.replaceChildren(close, inspect, heading, current, archiveAction, approvalAction, historyPanel,
      form("Save text", [field("Title", title), field("Description", description)], (submit) => {
        /** @type {Record<string, string>} */
        const draft = {};
        if (title.value !== titleBaseline) {
          if (!title.value) {
            announce("error", "Enter a title.");
            title.focus();
            return;
          }
          draft.title = title.value;
        }
        if (description.value !== descriptionBaseline) draft.description = description.value;
        if (Object.keys(draft).length) void save(draft, submit);
      }),
      form("Move card", [field("Lane", lane)], (submit) => {
        if (activeLanes(lanes).some(({ id }) => id === lane.value)) void save({ lane_id: lane.value }, submit);
      }),
      form("Assign owner", [field("Owner", owner)], (submit) => void save({ owner_id: owner.value }, submit)),
      priorityAction,
      hierarchy, element("h3", "Relationships"), relationshipList, addLink,
      element("h3", "Comments"), commentList,
      form("Post comment", [field("Comment", comment)], (submit) => {
        const id = crypto.randomUUID();
        const text = comment.value;
        void writeResource(submit, () => api.createComment(card.id, cardTag, text, id), async () => {
          const confirmed = (await readCurrent()).comments.some((entry) => entry.id === id);
          if (confirmed) {
            if (activeComment?.id === card.id && activeComment.control.value === text) activeComment.control.value = "";
            const savedDraft = cardDrafts.get(card.id);
            if (savedDraft?.comment === text) delete savedDraft.comment;
          }
          return confirmed;
        }, "Comment saved.", inspection);
      }),
      element("h3", "Attachments"), attachmentList,
      form("Upload attachment", [field("File", upload)], (submit) => {
        const file = upload.files?.[0];
        if (!file) return;
        if (file.size > 10 * 1024 * 1024) {
          announce("error", "Attachments are limited to 10 MiB.");
          return;
        }
        const id = crypto.randomUUID();
        void writeResource(submit, () => api.uploadAttachment(card.id, cardTag, file, id), async () => {
          const item = await readSubmittedAttachment(id);
          if (item && upload.files?.[0] === file) upload.value = "";
          await readCurrent(item);
          return item !== undefined;
        }, "Attachment saved.", inspection);
      }));
    inspection.sync();
    if (focusHeading && document.activeElement === initialFocus) heading.focus();
    void readWithSession(refreshRelationships).catch(() => {
      if (cardView === activeCard && !relationshipList.hasChildNodes()) relationshipList.textContent = "Links could not be read.";
    });
    void readWithSession(() => refreshLists()).catch(() => {
      if (cardView !== activeCard || commentList.hasChildNodes() || attachmentList.hasChildNodes()) return;
      commentList.textContent = "Comments could not be read.";
      attachmentList.textContent = "Attachments could not be read.";
    });
  } catch (error) {
    if (!isSelected()) return;
    if (invalidSelection || (error instanceof ApiError && error.status === 404)) {
      rememberCard(boardId);
      if (restoring) {
        view.hidden = true;
        view.replaceChildren();
        return;
      }
    } else {
      retrySelectedCard = () => {
        if (isSelected()) void openCard(boardId, summary, lanes, users, priorities, boardTag, reloadBoard, opener, true);
      };
    }
    view.replaceChildren(element("p", readError(error)));
  }
}

/** @param {string} boardId @param {AbortSignal} signal */
async function readBoardContents(boardId, signal) {
  for (let attempt = 0; attempt < 2; attempt++) {
    signal.throwIfAborted();
    const ordered = await api.getBoardLanes(boardId, signal);
    signal.throwIfAborted();
    const data = /** @type {{board: Board, cards: Card[]}} */ (ordered.data);
    const board = { data: data.board, etag: ordered.etag };
    const cards = data.cards;
    const [lanes, priorities] = await Promise.all([
      /** @type {Promise<Lane[]>} */ (collect((cursor) => api.listLanes(boardId, cursor, signal), "lanes", signal)),
      /** @type {Promise<Labeled[]>} */ (collect((cursor) => api.listPriorities(boardId, cursor, signal), "priorities", signal)),
    ]);
    signal.throwIfAborted();
    const check = await api.getBoardIfChanged(boardId, etag(board), signal);
    if (check.data !== null) {
      if (check.etag !== board.etag) continue;
      const summary = /** @type {Board} */ (check.data);
      if (validPublishing(summary.publishing)) data.board.publishing = summary.publishing;
    }
    const active = new Set(activeLanes(lanes).map(({ id }) => id));
    if (new Set(cards.map(({ id }) => id)).size !== cards.length || cards.some((card) =>
      card.board_id !== boardId || card.archived !== false || !active.has(card.lane_id)
      || !Number.isSafeInteger(card.revision) || card.revision < 1)) throw new ApiError("invalid_response");
    return { board, lanes, cards, priorities };
  }
  throw new ApiError("board_changed_during_read", { status: 412 });
}

/** @param {string[]} left @param {string[]} right */
function sameOrder(left, right) {
  return left.length === right.length && left.every((id, index) => id === right[index]);
}

/** @param {BoardSummary[]} boards @param {(pins: string[]) => void} showSavedOrder
 * @param {Awaited<ReturnType<typeof api.getWorkspace>>|undefined} initial @param {Inspection} inspection */
function renderWorkspacePreferences(boards, showSavedOrder, initial, inspection) {
  const panel = element("section");
  panel.className = "editor";
  let saved = initial;
  let draft = [...(pinDraft ?? saved?.data.pinned_board_ids ?? [])];
  const pins = element("div");
  const pinStatus = element("p");
  pinStatus.setAttribute("aria-live", "polite");
  const pinSection = element("section");
  let pinSaving = false;
  /** @param {boolean} saving */
  function disablePinsWhileSaving(saving) {
    pinSaving = saving;
    if (panel.isConnected) paintPins();
  }
  watchPreferenceWrite("workspace-pins", disablePinsWhileSaving);
  const pinForm = form("Save pin order", [pins], (submit) => {
    if (!saved) return;
    const expected = saved.etag;
    const requested = [...draft];
    trackPreferenceWrite("workspace-pins", writeResource(submit, () => api.setPinnedBoards(requested, expected), async () => {
      const reply = await read();
      return sameOrder(reply.data.pinned_board_ids, requested);
    }, "Pin order saved.", inspection));
    watchPreferenceWrite("workspace-pins", disablePinsWhileSaving);
  });
  if (!saved) pinForm.dataset.unavailable = "true";
  pinSection.append(element("h3", "Pin order"), pinStatus, pinForm);

  function retain() {
    if (saved) pinDraft = sameOrder(draft, saved.data.pinned_board_ids) ? undefined : [...draft];
    publishing.retain();
  }

  /** @param {BoardSummary[]} rows */
  function showBoards(rows) {
    boards = rows;
    paintPins();
    showSavedOrder(saved?.data.pinned_board_ids ?? []);
  }

  /** @param {string} [focusId] @param {"up"|"down"|"pin"} [focusKind] */
  function paintPins(focusId, focusKind) {
    const names = new Map(boards.map(({ id, name }) => [id, name]));
    /** @type {HTMLElement|undefined} */
    let nextFocus;
    const rows = [...draft, ...boards.map(({ id }) => id).filter((id) => !draft.includes(id))].map((id) => {
      const name = names.get(id) ?? "Unavailable board";
      const row = element("div");
      row.className = "pin-row";
      const pin = element("input");
      pin.type = "checkbox";
      const index = draft.indexOf(id);
      pin.checked = index >= 0;
      pin.disabled = !saved || pinSaving;
      pin.addEventListener("change", () => {
        if (pin.checked && index < 0) draft.push(id);
        if (!pin.checked && index >= 0) draft.splice(index, 1);
        retain();
        paintPins(id, "pin");
      });
      const option = field("Pin " + name, pin);
      option.className = "check-option";
      row.append(option);
      if (focusId === id && focusKind === "pin") nextFocus = pin;
      if (index >= 0) {
        /** @param {"up"|"down"} direction @param {number} offset */
        function move(direction, offset) {
          const to = index + offset;
          const control = button("Move " + name + " " + direction, () => {
            [draft[index], draft[to]] = [draft[to], draft[index]];
            retain();
            paintPins(id, direction);
          });
          control.disabled = !saved || pinSaving || to < 0 || to >= draft.length;
          if (focusId === id && focusKind === direction) nextFocus = control.disabled ? pin : control;
          return control;
        }
        row.append(move("up", -1), move("down", 1));
      }
      return row;
    });
    pins.replaceChildren(...rows);
    nextFocus?.focus();
    pinStatus.textContent = saved ? "" : "Saved pin order is unavailable. Inspect saved workspace before saving.";
  }

  async function read() {
    const reply = await api.getWorkspace();
    if (panel.isConnected) {
      if (pinDraft === undefined && (!saved || sameOrder(draft, saved.data.pinned_board_ids))) {
        draft = [...reply.data.pinned_board_ids];
      }
      saved = reply;
      retain();
      delete pinForm.dataset.unavailable;
      paintPins();
      showSavedOrder(reply.data.pinned_board_ids);
      publishing.show({ data: reply.data.publishing, etag: reply.etag });
    }
    return reply;
  }

  const publishing = renderPublishing("workspace", "workspace", saved && { data: saved.data.publishing, etag: saved.etag },
    async () => {
      const reply = await read();
      return { data: reply.data.publishing, etag: reply.etag };
    }, api.configureWorkspacePublishing, async () => {}, inspection);
  async function inspect() {
    await read();
  }
  paintPins();
  panel.append(pinSection, publishing.panel);
  return { panel, showBoards, inspect, retain };
}

/** @param {boolean} canManage @param {() => boolean} isCurrent */
function renderServiceControls(canManage, isCurrent) {
  const panel = element("section");
  panel.id = "service-controls";
  const health = element("p", "Reading service status…");
  const stage = element("p");
  const outcome = element("p");
  outcome.id = "service-operation-status";
  outcome.setAttribute("role", "status");
  let busy = false;
  let inspectAgain = false;
  /** @type {Service|undefined} */
  let service;
  /** @type {Build|undefined} */
  let candidate;
  const activate = button("Activate staged build", () => {
    if (service && candidate) void submit(service, { id: newOperationId(), buildId: candidate.id });
  });
  const stop = button("Shut down service", () => {
    if (service) void submit(service);
  });
  const refreshStatus = button("Refresh service status", () => {
    if (!writing && !busy) void inspect(true);
  });

  function sync() {
    const disabled = busy || pendingLifecycle !== undefined || service?.state !== "running";
    activate.disabled = disabled || candidate === undefined;
    stop.disabled = disabled;
    refreshStatus.disabled = busy;
  }

  /** @param {AbortSignal} [signal] */
  async function readService(signal) {
    try {
      const { data } = await readWithSession(() => api.getService(signal), signal);
      if (!isCurrent()) return;
      service = data;
      health.textContent = "Service is " + service.state + " on port " + service.port
        + ". Running build " + service.build_id + "."
        + (service.restart_error_code ? " Last lifecycle error: " + service.restart_error_code + "." : "");
    } catch {
      if (!isCurrent()) return;
      service = undefined;
      health.textContent = "Service status is unavailable.";
    }
    return service;
  }

  async function readStage() {
    if (!canManage || !isCurrent()) return;
    candidate = undefined;
    if (service?.state !== "running") {
      stage.textContent = "";
      return;
    }
    try {
      const { data } = await readWithSession(api.listBuilds);
      if (!isCurrent()) return;
      stage.textContent = data.builds.length ? data.builds.map((build) => "Staged build " + build.version
        + " is " + build.validation + (build.error_code ? " (" + build.error_code + ")." : ".")).join(" ")
        : "No staged build is available.";
      const candidates = data.builds.filter((build) => build.validation === "valid" && build.id !== service?.build_id);
      if (candidates.length === 1) candidate = candidates[0];
    } catch {
      if (isCurrent()) stage.textContent = "Staged build status is unavailable.";
    }
  }

  /** @param {boolean} explicit @param {AbortSignal} [signal] */
  async function readOutcome(explicit, signal) {
    const operation = canManage ? pendingLifecycle : undefined;
    const latest = await readService(signal);
    if (!operation || !isCurrent() || pendingLifecycle !== operation) return;
    let message = operation.refusal;
    if (!message && latest) {
      const prior = operation.prior;
      const changed = latest.service_instance_id !== prior.service_instance_id;
      if (operation.activation) {
        const { id, buildId } = operation.activation;
        try {
          const result = await readWithSession(() => api.getActivation(id, buildId, signal), signal);
          if (result.state === "activated" && changed && latest.build_id === buildId
              && latest.port === prior.port && latest.state === "running") {
            message = "Build activated. Reconnected on the same port.";
          } else if (result.state === "failed") {
            message = "Build activation failed" + (result.error_code ? " (" + result.error_code + ")." : ".");
          }
        } catch (error) {
          if (explicit && !operation.acknowledged && latest.state === "running"
              && error instanceof ApiError && error.status === 404) {
            message = "Activation could not be confirmed. Review the current service status.";
          }
        }
      } else if (changed && latest.state === "running") {
        message = "The service is running on a new instance.";
      } else if (latest.state === "running" && latest.restart_error_code && latest.etag !== prior.etag) {
        message = "Shutdown did not complete. The service is still running.";
      } else if (explicit && latest.state === "running" && !operation.acknowledged) {
        message = "Shutdown could not be confirmed. The service is running.";
      }
    }
    if (!message || !isCurrent() || pendingLifecycle !== operation) return;
    pendingLifecycle = undefined;
    outcome.textContent = message;
  }

  /** @param {boolean} explicit */
  async function inspect(explicit) {
    if (!isCurrent()) return;
    if (busy) {
      inspectAgain = true;
      return;
    }
    busy = true;
    sync();
    try {
      await readOutcome(explicit);
      await readStage();
      if (!isCurrent() || !canManage || !pendingLifecycle) return;
      if (pendingLifecycle.acknowledged && !pendingLifecycle.activation) {
        outcome.textContent = !service ? "Shutdown accepted. The service is unreachable."
          : service.state === "draining" ? "Shutdown accepted. The service is draining."
            : "Shutdown accepted. Refresh service status to check progress.";
      } else {
        outcome.textContent = "The outcome is unconfirmed. Refresh service status to inspect it.";
      }
    } finally {
      busy = false;
      sync();
      if (inspectAgain && isCurrent()) {
        inspectAgain = false;
        void inspect(false);
      }
    }
  }

  /** @param {Service} prior @param {{id: string, buildId: string}} [activation] */
  async function submit(prior, activation) {
    if (writing || busy || pendingLifecycle || !isCurrent()) return;
    /** @type {LifecycleOperation} */
    const operation = { prior, activation, acknowledged: false };
    const submittingCapability = capability;
    writing = busy = true;
    pendingLifecycle = operation;
    sync();
    outcome.textContent = activation ? "Requesting build activation…" : "Requesting shutdown…";
    try {
      try {
        if (activation) {
          await api.activateBuild(activation.id, activation.buildId, prior.etag);
        } else {
          const draining = await api.stopService(prior);
          if (isCurrent() && pendingLifecycle === operation) service = draining;
        }
        if (pendingLifecycle !== operation) return;
        operation.acknowledged = true;
      } catch (error) {
        if (pendingLifecycle !== operation) return;
        const phase = error instanceof ApiError && error.status === 401 && capability !== submittingCapability
          ? "error" : writePhase(error);
        if (phase !== "unknown") {
          operation.refusal = phase === "conflict" ? "Service changed. Review the current status."
            : activation ? "The service refused build activation."
              : error instanceof ApiError && error.code === "final_publication_failed"
                ? "Final publication failed. The service remains running."
                : "The service refused shutdown.";
          if (isCurrent()) {
            await readOutcome(false);
            await readStage();
          }
          return;
        }
      }
      if (!isCurrent()) return;
      if (activation) {
        outcome.textContent = operation.acknowledged ? "Activation accepted. Reconnecting…"
          : "Activation outcome is uncertain. Inspecting service status…";
        const signal = AbortSignal.timeout(8000);
        for (let attempt = 0; attempt < 8 && pendingLifecycle === operation; attempt++) {
          if (attempt > 0) await new Promise((resolve) => setTimeout(resolve, 750));
          if (signal.aborted || !isCurrent()) break;
          await readOutcome(false, signal);
        }
        if (isCurrent() && pendingLifecycle === operation) {
          outcome.textContent = "Activation outcome is unconfirmed. Refresh service status to inspect it.";
        }
        await readStage();
      } else if (operation.acknowledged) {
        health.textContent = "Service is draining on port " + prior.port + ".";
        stage.textContent = "";
        outcome.textContent = "Shutdown accepted. The service is draining.";
      } else {
        await readOutcome(false);
        if (isCurrent() && pendingLifecycle === operation) {
          outcome.textContent = "Shutdown outcome is uncertain. Refresh service status to inspect it.";
        }
      }
    } finally {
      writing = busy = false;
      sync();
      if (!isCurrent()) void refreshActiveService?.();
    }
  }

  panel.append(element("h2", "Service"), health);
  if (canManage) panel.append(stage, outcome);
  panel.append(refreshStatus);
  if (canManage) panel.append(activate, stop);
  sync();
  return { panel, inspect: () => inspect(false) };
}

function boardAtLocation() {
  return location.pathname.match(/^\/boards\/([0-9a-f-]{36})\/?$/i)?.[1]?.toLowerCase();
}

/** @param {import("./api.mjs").BoardAvailability} board @param {unknown} principalId */
async function canSetAvailability(board, principalId) {
  if (typeof principalId !== "string" || board.availability === "unhealthy") return false;
  try {
    const reply = await readWithSession(() => api.getPolicy(board.policy_id));
    const policy = /** @type {{id?: unknown, board_id?: unknown, resource_grants?: {user_id?: unknown, capabilities?: unknown}[]}} */ (reply.data);
    return policy.id === board.policy_id && policy.board_id === board.id && Array.isArray(policy.resource_grants)
      && policy.resource_grants.some((grant) => grant?.user_id === principalId && Array.isArray(grant.capabilities)
        && grant.capabilities.includes("close_board"));
  } catch {
    return false;
  }
}

/** @type {readonly {key: keyof Attention, label: string}[]} */
const attentionColumns = [
  { key: "blocked", label: "Blocked" },
  { key: "needs_input", label: "Needs Terry" },
  { key: "ready_for_review", label: "Ready for Review" },
];

/** @param {BoardSummary} board @param {number} now */
function attentionRecency(board, now) {
  if (board.last_card_edit_at) {
    const editedAt = Date.parse(board.last_card_edit_at.replace(" ", "T"));
    if (Number.isFinite(editedAt)) {
      const minutes = Math.max(0, Math.floor((now - editedAt) / 60_000));
      return "Last card edit " + minutes + (minutes === 1 ? " minute ago" : " minutes ago");
    }
  }
  return board.attention ? "No recorded card edits" : "Card edit history unavailable";
}

/** @param {HTMLElement} sidebar @param {BoardSummary[]} rows @param {string[]} pins
 * @param {string|undefined|null} selected */
function renderAttention(sidebar, rows, pins, selected) {
  const byId = new Map(rows.map((board) => [board.id, board]));
  const pinIds = new Set(pins);
  const header = element("div");
  header.className = "attention-grid attention-header";
  header.append(element("h2", "Attention"));
  for (const { key, label } of attentionColumns) {
    const heading = element("span", label);
    heading.className = "attention-label";
    heading.dataset.attention = key;
    heading.setAttribute("aria-hidden", "true");
    header.append(heading);
  }

  const focusedId = [...sidebar.querySelectorAll("a")]
    .find((item) => item === document.activeElement)?.dataset.boardId;
  const groups = [
    { label: "Pinned boards", boards: pins.flatMap((id) => byId.get(id) ?? []) },
    { label: "Other boards", boards: rows.filter(({ id }) => !pinIds.has(id)) },
  ];
  const sections = groups.map(({ label, boards }) => {
    const section = element("section");
    section.className = "attention-group";
    section.append(element("h3", label));
    for (const board of boards) {
      const name = board.name ?? "Unavailable board";
      const item = link("/boards/" + board.id, "");
      item.className = "attention-grid attention-row";
      item.dataset.boardId = board.id;
      if (board.id === selected) item.setAttribute("aria-current", "page");
      const title = element("span", name);
      title.className = "attention-name";
      item.append(title);
      const accessible = [];
      for (const { key, label } of attentionColumns) {
        const count = board.attention?.[key];
        const slot = element("span");
        slot.className = "attention-slot";
        slot.setAttribute("aria-hidden", "true");
        slot.dataset.attention = key;
        if (count == null) {
          slot.textContent = "—";
        } else if (count > 0) {
          const badge = element("span", count > 99 ? "99+" : String(count));
          badge.className = "attention-count";
          slot.append(badge);
        }
        accessible.push(label + " " + (count ?? "unavailable"));
        item.append(slot);
      }
      const summary = element("span");
      summary.hidden = true;
      summary.id = "attention-" + board.id;
      item.setAttribute("aria-describedby", summary.id);
      item.append(summary);
      const countSummary = accessible.join(". ");
      const updateRecency = () => {
        item.title = attentionRecency(board, Date.now());
        summary.textContent = countSummary + ". " + item.title;
      };
      updateRecency();
      item.addEventListener("pointerenter", updateRecency);
      item.addEventListener("focus", updateRecency);
      section.append(item);
    }
    return section;
  });
  sidebar.replaceChildren(header, ...sections);
  if (focusedId) {
    [...sidebar.querySelectorAll("a")].find((item) => item.dataset.boardId === focusedId)?.focus();
  }
}

/** @param {string} boardId @param {AbortSignal} signal @param {() => number} revision
 * @param {(automatic: boolean) => Promise<boolean>} read */
function boardUpdates(boardId, signal, revision, read) {
  const panel = element("div");
  const connection = element("p", "Connecting live updates…");
  connection.id = "board-live-connection";
  connection.setAttribute("role", "status");
  const readiness = element("p");
  readiness.id = "board-live-readiness";
  readiness.setAttribute("role", "status");
  const retry = button("Retry board update", () => {
    if (!writing) void updates.request().catch(() => {});
  });
  retry.hidden = true;
  panel.append(connection, readiness, retry);

  /** @type {import("./board-updates.mjs").Connection} */
  let connectionState = "connecting";
  /** @type {import("./board-updates.mjs").Readiness} */
  let readState = "ready";
  const messages = {
    connecting: "Connecting live updates…",
    connected: "Live updates connected.",
    disconnected: "Live updates disconnected. Reconnecting…",
  };

  function show() {
    const connectionText = messages[connectionState];
    const readinessText = readState === "error" ? "Board update could not be completed."
      : readState === "refreshing" ? "Checking for board updates…" : "";
    if (connection.textContent !== connectionText) connection.textContent = connectionText;
    if (readiness.textContent !== readinessText) readiness.textContent = readinessText;
    retry.hidden = connectionState !== "disconnected" && readState !== "error";
    retry.disabled = readState === "refreshing";
  }

  const updates = createBoardRefresh({
    signal, read, revision, canReadAutomatically: () => !writing,
    report: (state) => { readState = state; show(); },
  });
  return {
    panel, request: updates.request,
    start() {
      activeBoardRefresh = updates;
      void watchBoard({
        boardId, signal, invalidate: updates.invalidate,
        report: (state) => { connectionState = state; show(); },
        connect: (streamSignal, receive) => readWithSession(async () => {
          for await (const event of api.streamEvents({ boardId, signal: streamSignal })) receive(event);
        }, streamSignal),
      });
    },
  };
}

/** @param {Awaited<ReturnType<typeof api.getBoardAvailability>>} [savedAvailability] */
async function load(savedAvailability) {
  if (!identity || !navigation || !main || !details) return;
  retainBoardView?.();
  retainBoardView = undefined;
  retainCardDraft?.();
  retainBoardDraft?.();
  releaseCardMedia?.();
  releaseBoardMedia?.();
  releaseCardMedia = undefined;
  releaseBoardMedia = undefined;
  retainCardDraft = undefined;
  retainBoardDraft = undefined;
  cardInspection = undefined;
  boardInspection = undefined;
  retrySelectedCard = undefined;
  activeComment = undefined;
  activeDrawer = undefined;
  const view = ++activeView;
  activeBoardLoad?.abort();
  activeBoardRefresh = undefined;
  activeBoardLoad = new AbortController();
  const signal = activeBoardLoad.signal;
  refreshActiveService = undefined;
  ++activeCard;
  details.hidden = true;
  details.replaceChildren();
  main.replaceChildren();
  boardStyle.replaceSync("");
  announce("loading", "Loading…");
  try {
    const [session, boards] = await readWithSession(() => Promise.all([
      api.getSession(), /** @type {Promise<BoardSummary[]>} */ (collect(api.listBoards, "boards")),
    ]));
    if (view !== activeView) return;
    const context = /** @type {{kind?: unknown, principal_id?: unknown, workspace_admin?: unknown}} */ (session.data);
    if (context.kind !== "human") throw new ApiError("invalid_response");
    identity.textContent = "Human";
    const boardId = boardAtLocation();
    /** @type {string[]} */
    let visiblePins = [];
    let latestBoards = boards;
    let catalogRead = 0;
    const attentionStatus = element("p", "Attention could not be refreshed. Showing the last saved counts.");
    attentionStatus.setAttribute("role", "status");
    attentionStatus.hidden = true;
    /** @param {BoardSummary[]} rows */
    function showBoards(rows) {
      if (!navigation) return;
      renderAttention(navigation, rows, visiblePins, boardId);
      navigation.append(attentionStatus);
    }
    async function refreshAttention() {
      if (view !== activeView) return;
      const read = ++catalogRead;
      try {
        const rows = /** @type {BoardSummary[]} */ (await readWithSession(() => collect(api.listBoards, "boards")));
        if (view !== activeView || read !== catalogRead) return;
        latestBoards = rows;
        attentionStatus.hidden = true;
        showBoards(latestBoards);
      } catch {
        if (view === activeView && read === catalogRead) attentionStatus.hidden = false;
      }
    }
    showBoards(latestBoards);
    const readPreferences = context.workspace_admin === true
      ? readWithSession(api.getWorkspace).catch(() => undefined) : undefined;
    if (!boardId && location.pathname === "/") {
      main.replaceChildren(element("h1", "Workspace"),
        element("p", boards.length ? "Select a board." : "No boards are available."));
      if (context.workspace_admin === true) {
        const preferences = await readPreferences;
        if (view !== activeView) return;
        visiblePins = preferences?.data.pinned_board_ids ?? [];
        showBoards(latestBoards);
        const boardName = element("input");
        boardName.required = true;
        boardName.maxLength = 256;
        boardName.value = boardDrafts.get("workspace")?.[0] ?? "";
        const inspection = createInspection(main, () => view === activeView, async () => {
          const latest = /** @type {BoardSummary[]} */ (await collect(api.listBoards, "boards"));
          if (view !== activeView) return;
          latestBoards = latest;
          workspaceControls.showBoards(latestBoards);
          await workspaceControls.inspect();
        }, "workspace", "workspace");
        const workspaceControls = renderWorkspacePreferences(latestBoards, (pins) => {
          visiblePins = pins;
          showBoards(latestBoards);
        }, preferences, inspection);
        retainBoardDraft = () => {
          boardDrafts.set("workspace", [boardName.value]);
          workspaceControls.retain();
        };
        boardInspection = inspection;
        main.append(button(inspection.label, inspection.inspect),
          form("Create board", [field("Board name", boardName)], (submit) => {
          const id = crypto.randomUUID();
          const name = boardName.value;
          void writeResource(submit, () => api.createBoard({ id, name }), async () => {
            try {
              await api.getBoard(id);
            } catch (error) {
              if (error instanceof ApiError && error.status === 404) return false;
              throw error;
            }
            if (view !== activeView) return true;
            if (boardName.value === name) boardName.value = "";
            history.pushState(null, "", "/boards/" + id);
            await load();
            return true;
          }, "Board created.", inspection);
        }), workspaceControls.panel);
        inspection.sync();
      }
      const controls = renderServiceControls(context.workspace_admin === true, () => view === activeView);
      main.append(controls.panel);
      refreshActiveService = controls.inspect;
      void refreshActiveService();
      announce("ready", "Workspace loaded.");
      return;
    }
    if (!boardId) throw new ApiError("http_error", { status: 404 });
    void readPreferences?.then((preferences) => {
      if (view === activeView && preferences) {
        visiblePins = preferences.data.pinned_board_ids;
        showBoards(latestBoards);
      }
    });
    /** @param {AbortSignal} [readSignal] */
    const readAvailability = (readSignal) => api.getBoardAvailability(boardId, readSignal);
    let metadata = savedAvailability?.data.id === boardId ? savedAvailability
      : await readWithSession(() => readAvailability(signal), signal);
    const readContents = () => readWithSession(async () => {
      const group = new AbortController();
      const readSignal = AbortSignal.any([signal, group.signal]);
      try {
        return await Promise.all([readBoardContents(boardId, readSignal),
          /** @type {Promise<User[]>} */ (collect((cursor) => api.listUsers(boardId, cursor, readSignal), "users", readSignal)),
        ]);
      } finally {
        group.abort();
      }
    }, signal);
    /** @type {Awaited<ReturnType<typeof readContents>>|undefined} */
    let contents;
    if (metadata.data.availability === "open") {
      try {
        contents = await readContents();
      } catch (error) {
        if (!(error instanceof ApiError) || error.status !== 503) throw error;
        metadata = await readWithSession(() => readAvailability(signal), signal);
        if (metadata.data.availability === "open") throw error;
      }
    }
    const allowed = await canSetAvailability(metadata.data, context.principal_id);
    if (view !== activeView) return;

    /** @param {Awaited<ReturnType<typeof readAvailability>>} latest */
    async function showAvailability(latest) {
      if (view !== activeView) return;
      await load(latest);
    }

    /** @param {import("./api.mjs").AvailabilityChange} requested @param {() => string} currentTag
     * @param {(latest: Awaited<ReturnType<typeof readAvailability>>) => Promise<void>} refreshCurrent @param {Inspection} inspection */
    function availabilityForm(requested, currentTag, refreshCurrent, inspection) {
      return form(requested === "open" ? "Reopen board" : "Close board", [], (submit) => {
        const expected = currentTag();
        const { id, availability: prior } = metadata.data;
        void writeResource(submit, () => api.setBoardAvailability(id, requested, expected), async () => {
          const latest = await readAvailability();
          if (latest.data.availability !== prior) {
            if (view === activeView) await showAvailability(latest);
            else if (boardAtLocation() === id) await load();
          } else if (view === activeView) await refreshCurrent(latest);
          return latest.data.availability === requested && latest.etag !== expected;
        }, requested === "open" ? "Board reopened." : "Board closed.", inspection);
      });
    }

    if (!contents) {
      const message = metadata.data.availability === "closed" ? "This board is closed." : "This board is unhealthy.";
      const updates = boardUpdates(boardId, signal, () => metadata.data.revision, async (automatic) => {
        const latest = await readWithSession(() => readAvailability(signal), signal);
        if (signal.aborted || (automatic && writing)) return false;
        if (latest.etag !== metadata.etag || latest.data.availability !== metadata.data.availability) {
          await showAvailability(latest);
        }
        return true;
      });
      const inspection = createInspection(main, () => view === activeView,
        updates.request, "board", "board:" + boardId);
      boardInspection = inspection;
      main.replaceChildren(element("h1", metadata.data.name), element("p", message),
        updates.panel, button(inspection.label, inspection.inspect));
      if (allowed && metadata.data.availability === "closed") {
        main.append(availabilityForm("open", () => metadata.etag, async (latest) => {
          metadata = latest;
        }, inspection));
      }
      inspection.sync();
      announce(metadata.data.availability === "closed" ? "ready" : "error", message);
      updates.start();
      return;
    }
    const [{ board: boardReply, lanes, cards, priorities }, users] = contents;
    const board = /** @type {Board} */ (boardReply.data);
    let boardTag = etag(boardReply);
    let paintedRevision = board.revision;
    const updates = boardUpdates(boardId, signal, () => paintedRevision, applyBoardRefresh);
    const reloadBoard = updates.request;
    let currentCards = cards;
    const columns = element("div");
    columns.className = "lanes";
    const state = boardViewState(boardId);
    /** @type {Map<string, HTMLElement>} */
    const laneColumns = new Map();

    function retainScroll() {
      if (!columns.isConnected) return;
      state.scroll = { left: columns.scrollLeft,
        lanes: Object.fromEntries([...laneColumns].map(([id, column]) => [id, column.scrollTop])) };
    }

    function restoreScroll() {
      if (!columns.isConnected) return;
      const saved = state.scroll;
      if (typeof saved?.left === "number" && Number.isFinite(saved.left)) columns.scrollLeft = saved.left;
      for (const [id, column] of laneColumns) {
        const top = saved?.lanes?.[id];
        if (typeof top === "number" && Number.isFinite(top)) column.scrollTop = top;
      }
    }

    const archived = element("details");
    const archivedHeading = element("summary", "Archived cards");
    const archivedList = element("div");
    archived.append(archivedHeading, archivedList);
    let archivedRead = 0;
    let coverRead = 0;
    /** @type {Set<string>} */
    const covers = new Set();
    function clearCovers() {
      ++coverRead;
      for (const url of covers) URL.revokeObjectURL(url);
      covers.clear();
    }
    releaseBoardMedia = clearCovers;

    /** @param {Card} card @param {HTMLElement} slot @param {number} read */
    async function showCover(card, slot, read) {
      const id = card.cover_attachment_id;
      if (!id) return;
      const current = () => view === activeView && read === coverRead && slot.isConnected;
      try {
        const blob = await readWithSession(async () => {
          const meta = /** @type {Attachment} */ ((await api.getAttachment(id)).data);
          if (!current() || meta.id !== id || !activeAttachment(board.id, card.id, meta) || !meta.previewable) return;
          return api.getAttachmentContent(id, "inline", meta.size, meta.sha2_256);
        });
        if (!current() || !blob) return;
        const url = URL.createObjectURL(blob);
        covers.add(url);
        const image = element("img");
        image.className = "card-cover";
        image.alt = "";
        image.addEventListener("error", () => {
          image.remove();
          covers.delete(url);
          URL.revokeObjectURL(url);
        });
        image.src = url;
        slot.replaceChildren(image);
      } catch {
        // The card remains usable when the cover cannot be read.
      }
    }

    /** @param {number} count @param {string} noun */
    function countLabel(count, noun) {
      return count + " " + noun + (count === 1 ? "" : "s");
    }

    /** @param {Card} card */
    function cardOpener(card) {
      const opener = button("#" + card.ticket + " " + card.title, () => {
        if (!writing) void openCard(board.id, card, lanes, users, priorities, () => boardTag, reloadBoard, opener);
      });
      opener.className = "card";
      opener.dataset.cardId = card.id;
      const description = [];
      const priority = priorities.find(({ id }) => id === card.priority_id);
      if (priority) {
        const label = element("small", priority.label);
        label.className = "card-priority";
        label.setAttribute("aria-hidden", "true");
        description.push("Priority: " + priority.label);
        opener.append(label);
      }
      if (card.comment_count > 0 || card.attachment_count > 0) {
        const counts = element("span");
        counts.className = "card-counts";
        counts.setAttribute("aria-hidden", "true");
        if (card.comment_count > 0) {
          const comments = element("span", "\u{1f4ac} " + card.comment_count);
          comments.className = "card-comment-count";
          counts.append(comments);
          description.push(countLabel(card.comment_count, "comment"));
        }
        if (card.attachment_count > 0) {
          const attachments = element("span", "\u{1f4ce} " + card.attachment_count);
          attachments.className = "card-attachment-count";
          counts.append(attachments);
          description.push(countLabel(card.attachment_count, "attachment"));
        }
        opener.append(counts);
      }
      if (description.length) opener.setAttribute("aria-description", description.join(", "));
      return opener;
    }

    /** @param {Card[]} visibleCards @param {string} renderedTag */
    function renderCards(visibleCards, renderedTag) {
      retainScroll();
      laneColumns.clear();
      const focused = document.activeElement;
      const focusedId = focused instanceof HTMLButtonElement && columns.contains(focused) ? focused.dataset.cardId : undefined;
      const focusedPreview = focused instanceof HTMLInputElement && columns.contains(focused) ? focused.dataset.previewLane : undefined;
      const previews = new Map([...columns.querySelectorAll("input")].map((input) => [input.dataset.previewLane, input.value]));
      clearCovers();
      const render = coverRead;
      const active = activeLanes(lanes);
      const completed = active.find(({ role }) => role === "completed");
      columns.replaceChildren(...active.map((lane) => {
        const column = element("section");
        column.className = "lane";
        laneColumns.set(lane.id, column);
        const members = visibleCards.filter(({ lane_id }) => lane_id === lane.id);
        const heading = element("h2", lane.label);
        heading.className = "lane-heading";
        const total = element("small", countLabel(members.length, "card"));
        total.className = "lane-total";
        heading.append(total);
        column.append(heading);
        const preview = element("div");
        const selectedTickets = element("input");
        selectedTickets.dataset.previewLane = lane.id;
        selectedTickets.value = previews.get(lane.id) ?? "";
        selectedTickets.placeholder = "All active cards";
        selectedTickets.setAttribute("aria-label", "Tickets to preview in " + lane.label);
        const previewButton = button("Preview ticket order", () => {
          let ids;
          try {
            ids = selectedTickets.value === "" ? undefined : ticketSelection(selectedTickets.value, members).map(({ id }) => id);
          } catch {
            preview.textContent = "Enter distinct active ticket numbers from this lane.";
            return;
          }
          previewButton.disabled = true;
          void readWithSession(() => api.previewTicketOrder(board.id, lane.id, ids)).then((reply) => {
            if (!preview.isConnected || view !== activeView) return;
            const result = /** @type {{board_revision: number, current: {card_id: string, ticket: number}[], proposed: {card_id: string, ticket: number}[], changed: boolean}} */ (reply.data);
            preview.replaceChildren(element("p", "Preview at board revision " + result.board_revision));
            for (const entry of result.proposed) {
              const before = result.current.find(({ card_id }) => card_id === entry.card_id);
              preview.append(element("p", "#" + before?.ticket + " → #" + entry.ticket + " "
                + (members.find(({ id }) => id === entry.card_id)?.title ?? entry.card_id)));
            }
            if (!result.proposed.length) preview.append(element("p", "No active cards in this lane."));
            if (result.changed) {
              const originalTag = etag(reply);
              preview.append(form("Apply ticket order", [], (submit) => {
                if (!preview.isConnected || view !== activeView) return;
                void writeResource(submit, () => api.applyTicketOrder(board.id, lane.id, originalTag, result.proposed),
                  async () => {
                    await reloadBoard();
                    return result.proposed.every((entry) => currentCards.some((card) => card.id === entry.card_id && card.ticket === entry.ticket));
                  }, "Ticket order applied.", inspection);
              }));
            }
          }).catch(() => {
            if (preview.isConnected) preview.textContent = "Preview refused or unavailable. A selected dependency cycle cannot be reordered.";
          }).finally(() => { previewButton.disabled = false; });
        });
        previewButton.dataset.action = "preview-ticket-order";
        column.append(selectedTickets, previewButton, preview);
        if (completed && lane.role === "ready_for_review" && lane.approval_enabled
            && members.length > 0 && members.length <= 1000) {
          const versions = members.map(({ id, revision }) => ({ id, revision }));
          const cardIds = versions.map(({ id }) => id);
          column.append(form("Approve all in lane", [], (submit) => {
            if (view !== activeView || !column.isConnected) return;
            const id = newOperationId();
            const drawer = activeDrawer;
            let approved = false;
            void writeResource(submit, async () => {
              await api.approveLane(id, board.id, renderedTag, lane.id, completed.id, versions);
              approved = true;
            }, async () => {
              if (!approved) {
                try {
                  await api.inspectLaneApproval(id, board.id, renderedTag, lane.id, completed.id, cardIds);
                  approved = true;
                } catch (error) {
                  if (!(error instanceof ApiError) || error.status !== 404) throw error;
                }
              }
              await reloadBoard();
              if (approved && drawer && activeDrawer === drawer && drawer.boardId === boardId
                  && cardIds.includes(drawer.id)) drawer.close();
              return approved;
            }, "Lane approved and moved to Completed.", inspection);
          }));
        }
        for (const card of members) {
          const opener = cardOpener(card);
          column.append(opener);
          if (card.cover_attachment_id) {
            const slot = element("span");
            slot.className = "card-cover-space";
            slot.setAttribute("aria-hidden", "true");
            opener.prepend(slot);
            void showCover(card, slot, render);
          }
        }
        return column;
      }));
      restoreScroll();
      if (focusedId) [...columns.querySelectorAll("button")].find((control) =>
        control.dataset.cardId === focusedId)?.focus({ preventScroll: true });
      if (focusedPreview) [...columns.querySelectorAll("input")].find((control) =>
        control.dataset.previewLane === focusedPreview)?.focus({ preventScroll: true });
    }

    async function reloadArchived() {
      if (!archived.open || view !== activeView) return;
      const read = ++archivedRead;
      try {
        const cards = /** @type {Card[]} */ (await readWithSession(() => collect((cursor) =>
          api.listCards(boardId, { cursor, archived: "only" }), "cards")));
        if (view !== activeView || read !== archivedRead || !archived.open) return;
        const focused = document.activeElement;
        const focusedId = focused instanceof HTMLButtonElement && archivedList.contains(focused) ? focused.dataset.cardId : undefined;
        archivedList.replaceChildren(...cards.map(cardOpener));
        if (!cards.length) archivedList.textContent = "No archived cards.";
        if (focusedId) ([...archivedList.querySelectorAll("button")].find((control) =>
          control.dataset.cardId === focusedId) ?? archivedHeading).focus();
      } catch {
        if (view === activeView && read === archivedRead && archived.open) {
          archivedList.textContent = "Archived cards could not be read. Close and reopen to retry.";
        }
      }
    }
    archived.addEventListener("toggle", () => {
      archivedList.replaceChildren();
      if (archived.open) {
        archivedList.textContent = "Reading archived cards…";
        void reloadArchived();
      }
    });

    boardStyle.replaceSync(board.user_css ?? "");
    const addTitle = element("input");
    addTitle.required = true;
    const addDescription = element("textarea");
    const addLane = select(activeLanes(lanes), null);
    addLane.required = true;
    const seeds = renderRelationshipSeeds(board.id);
    const previous = boardDrafts.get(board.id);
    addTitle.value = previous?.[0] ?? "";
    addDescription.value = previous?.[1] ?? "";
    if (previous?.[2] !== undefined) addLane.value = previous[2];
    const editor = element("section");
    editor.className = "editor";
    const inspection = createInspection(main, () => view === activeView, reloadBoard, "board", "board:" + board.id);
    boardInspection = inspection;
    editor.append(button(inspection.label, inspection.inspect));
    editor.append(form("Add card", [field("New title", addTitle), field("New description", addDescription),
      field("New card lane", addLane), seeds.panel], (submit) => {
      if (!addLane.value) return;
      const id = crypto.randomUUID();
      let created = false;
      const draft = { title: addTitle.value, description: addDescription.value, lane_id: addLane.value };
      const submitted = seeds.capture();
      const expected = boardTag;
      void writeResource(submit, async () => {
        const relationships = submitted.length
          ? await resolveRelationshipSeeds(board.id, submitted, () => view === activeView)
          : undefined;
        if (view !== activeView) throw new ApiError("invalid_target");
        await api.createCard({ ...draft, id, board_id: board.id, expected_board_etag: expected, relationships });
        created = true;
      }, async () => {
        if (!created) {
          try {
            const reply = await api.getCard(id);
            const saved = /** @type {Card} */ (reply.data);
            created = saved.id === id && saved.board_id === board.id;
          } catch (error) {
            if (!(error instanceof ApiError) || error.status !== 404) throw error;
          }
        }
        if (created) {
          if (addTitle.value === draft.title) addTitle.value = "";
          if (addDescription.value === draft.description) addDescription.value = "";
          seeds.clearSubmitted(submitted);
        }
        await reloadBoard();
        return created;
      }, "Card created.", inspection);
    }));
    const recent = element("details");
    const recentList = element("div");
    recent.append(element("summary", "Newest comments"), recentList);
    recent.addEventListener("toggle", () => {
      if (!recent.open) return;
      void api.listComments({ board_id: board.id }, 2).then((reply) => {
        const comments = /** @type {Comment[]} */ (items(reply, "comments"));
        recentList.replaceChildren(...comments.map((entry) => {
          const user = users.find(({ id }) => id === entry.author_id)?.label ?? "Unknown user";
          const item = element("article");
          const body = element("p");
          renderCommentText(body, entry, board.id, users);
          item.append(element("small", user + " · " + entry.created_at), body);
          return item;
        }));
      }).catch(() => { recentList.textContent = "Comments could not be read."; });
    });

    /** @param {boolean} automatic */
    async function applyBoardRefresh(automatic) {
      const canApply = () => !signal.aborted && (!automatic || !writing);
      let next;
      try {
        next = await readContents();
      } catch (error) {
        if (!(error instanceof ApiError) || error.status !== 503) throw error;
        const latest = await readWithSession(() => readAvailability(signal), signal);
        if (!canApply()) return false;
        if (latest.data.availability === "open") throw error;
        await showAvailability(latest);
        return true;
      }
      const [latest, latestUsers] = next;
      if (!canApply()) return false;
      const updatedBoard = /** @type {Board} */ (latest.board.data);
      const latestTag = etag(latest.board);
      if (validPublishing(updatedBoard.publishing)) boardPublishing.show({ data: updatedBoard.publishing, etag: latestTag });
      if (automatic && latestTag === boardTag && paintedRevision === updatedBoard.revision) return true;
      users.splice(0, users.length, ...latestUsers);
      board.name = updatedBoard.name;
      const heading = main?.querySelector("h1");
      if (heading) heading.textContent = board.name;
      lanes.splice(0, lanes.length, ...latest.lanes);
      currentCards = latest.cards;
      priorities.splice(0, priorities.length, ...latest.priorities);
      const boardChanged = latestTag !== boardTag;
      boardTag = latestTag;
      boardStyle.replaceSync(updatedBoard.user_css ?? "");
      setOptions(addLane, activeLanes(lanes), addLane.value);
      laneEditor.showLanes();
      search.refreshBoard(boardChanged);
      const batchPriority = boardActions.querySelector("#batch-priority-value");
      if (batchPriority instanceof HTMLSelectElement) setOptions(batchPriority, priorities, batchPriority.value);
      renderCards(latest.cards, boardTag);
      inspection.sync();
      void refreshAttention();
      if (activeDrawer?.boardId === board.id) await activeDrawer.refreshWorkflow(canApply, automatic);
      if (!canApply()) return false;
      await reloadArchived();
      if (!canApply()) return false;
      paintedRevision = updatedBoard.revision;
      return true;
    }
    const laneEditor = renderLaneEditor(board.id, lanes, users, () => boardTag, reloadBoard, inspection);
    const boardActions = renderBoardActions(board.id, priorities, () => currentCards, () => boardTag, reloadBoard, inspection);
    const boardPublishing = renderPublishing("board", board.id,
      validPublishing(board.publishing) ? { data: board.publishing, etag: boardTag } : undefined,
      () => api.getBoardPublishing(board.id),
      (enabled, tag) => api.configureBoardPublishing(board.id, enabled, tag), reloadBoard, inspection);
    boardActions.append(boardPublishing.panel);
    if (allowed) boardActions.append(availabilityForm("closed", () => boardTag, reloadBoard, inspection));
    const search = renderCardSearch(board.id, lanes, (card, opener) => {
      if (!writing) void openCard(board.id, card, lanes, users, priorities, () => boardTag, reloadBoard, opener);
    });
    retainBoardDraft = () => {
      boardDrafts.set(board.id, [addTitle.value, addDescription.value, addLane.value]);
      seeds.retain();
      laneEditor.retainDraft();
      boardPublishing.retain();
    };
    renderCards(cards, boardTag);
    main.replaceChildren(element("h1", board.name), updates.panel, editor, search.panel, recent,
      laneEditor.editor, boardActions, columns, archived);
    retainBoardView = () => {
      retainScroll();
      search.retain();
    };
    restoreScroll();
    search.restore();
    inspection.sync();
    announce("ready", "Board loaded.");
    updates.start();
    const ticketValue = new URLSearchParams(location.search).get("ticket");
    const selectedCard = rememberedCard(boardId, ticketValue);
    if (selectedCard && refresh) {
      const opener = [...columns.querySelectorAll("button")].find((control) => control.dataset.cardId === selectedCard);
      void openCard(board.id, { id: selectedCard }, lanes, users, priorities, () => boardTag, reloadBoard, opener ?? refresh, true);
      restoreScroll();
      return;
    }
    if (ticketValue !== null) {
      const cardView = activeCard;
      const ticket = Number(ticketValue);
      let selected;
      try {
        if (/^[1-9][0-9]*$/.test(ticketValue) && Number.isSafeInteger(ticket)) {
          selected = await readWithSession(() => findCardByTicket(board.id, ticket));
        }
      } catch (error) {
        if (view === activeView && cardView === activeCard) announce("error", readError(error));
        return;
      }
      if (view !== activeView || cardView !== activeCard || !refresh) return;
      if (selected) void openCard(board.id, selected, lanes, users, priorities, () => boardTag, reloadBoard, refresh);
      else announce("error", "This ticket is unavailable.");
    }
  } catch (error) {
    if (view !== activeView) return;
    if (error instanceof ApiError && error.status === 401) capability = "";
    main.replaceChildren();
    if (boardAtLocation()) main.append(button("Retry board update", () => void load()));
    announce("error", readError(error));
  }
}

/** @param {Lane[]} lanes */
function activeLanes(lanes) {
  return lanes.filter(({ retired }) => !retired)
    .sort((a, b) => a.position - b.position);
}

/** @param {string} boardId @param {Lane[]} lanes @param {Labeled[]} users
 * @param {() => string} boardTag @param {() => Promise<void>} reloadBoard @param {Inspection} inspection */
function renderLaneEditor(boardId, lanes, users, boardTag, reloadBoard, inspection) {
  const editor = element("details");
  editor.className = "editor";
  const addName = element("input");
  addName.required = true;
  addName.maxLength = 256;
  const after = select(activeLanes(lanes), null);
  const createFor = select(users, null);
  const connectFor = select(users, null);
  for (const grants of [createFor, connectFor]) {
    grants.multiple = true;
    for (const option of grants.options) option.selected = false;
  }
  const target = select(activeLanes(lanes), null);
  const reviewLane = select(activeLanes(lanes).filter(({ role }) => role === "ready_for_review"), null);
  const retireLane = select(activeLanes(lanes).filter(({ role }) => role !== "completed"), null);
  for (const control of [after, target, reviewLane, retireLane]) control.required = true;
  const renameName = element("input");
  renameName.required = true;
  renameName.maxLength = 256;
  const keyLane = select(activeLanes(lanes).filter(({ role }) => role !== "completed"), null);
  keyLane.id = "lane-key-target";
  keyLane.required = true;
  const keyValue = element("input");
  keyValue.id = "lane-key-value";
  keyValue.required = true;
  keyValue.maxLength = 256;
  const names = element("p");
  const draftKey = boardId + ":lanes";
  const previous = boardDrafts.get(draftKey);
  addName.value = previous?.[0] ?? "";
  renameName.value = previous?.[1] ?? "";
  if (previous?.[2] !== undefined) after.value = previous[2];
  if (previous?.[3] !== undefined) target.value = previous[3];
  if (previous?.[6] !== undefined) reviewLane.value = previous[6];
  if (previous?.[7] !== undefined) retireLane.value = previous[7];
  if (previous?.[8] !== undefined) keyLane.value = previous[8];
  keyValue.value = previous?.[9] ?? "";
  for (const [control, value] of [[createFor, previous?.[4]], [connectFor, previous?.[5]]]) {
    if (!(control instanceof HTMLSelectElement) || typeof value !== "string") continue;
    const ids = value.split(",");
    for (const option of control.options) option.selected = ids.includes(option.value);
  }

  function showLanes() {
    for (const control of [createFor, connectFor]) {
      const selectedIds = new Set(Array.from(control.selectedOptions, ({ value }) => value));
      setOptions(control, users, null);
      for (const option of control.options) option.selected = selectedIds.has(option.value);
    }
    const active = activeLanes(lanes);
    setOptions(after, active, after.value);
    setOptions(target, active, target.value);
    setOptions(keyLane, active.filter(({ role }) => role !== "completed"), keyLane.value);
    setOptions(reviewLane, active.filter(({ role }) => role === "ready_for_review"), reviewLane.value);
    setOptions(retireLane, active.filter(({ role }) => role !== "completed"), retireLane.value);
    updateApprovalAction();
    retireForm.hidden = !retireLane.options.length;
    names.textContent = "Saved lanes: " + lanes.map(({ label, key, retired }) => label + " [" + key + "]" + (retired ? " (retired)" : "")).join(", ");
  }

  function updateApprovalAction() {
    const lane = activeLanes(lanes).find(({ id }) => id === reviewLane.value);
    approvalForm.hidden = !reviewLane.options.length;
    const control = approvalForm.querySelector("button");
    if (control) control.textContent = lane?.approval_enabled ? "Disable lane approval" : "Enable lane approval";
  }
  const approvalForm = form("Enable lane approval", [field("Review lane", reviewLane)], (submit) => {
    const lane = activeLanes(lanes).find(({ id, role }) => id === reviewLane.value && role === "ready_for_review");
    if (!lane) return;
    const enabled = !lane.approval_enabled;
    void writeResource(submit, () => api.updateLaneSettings(boardId, lane.id, lane.revision, boardTag(), { approval_enabled: enabled }),
      async () => {
        await reloadBoard();
        return lanes.find(({ id }) => id === lane.id)?.approval_enabled === enabled;
      }, enabled ? "Lane approval enabled." : "Lane approval disabled.", inspection);
  });
  reviewLane.addEventListener("change", updateApprovalAction);
  const retireForm = form("Retire lane", [field("Lane to retire", retireLane)], (submit) => {
    const lane = activeLanes(lanes).find(({ id, role }) => id === retireLane.value && role !== "completed");
    if (!lane) return;
    void writeResource(submit, () => api.updateLaneSettings(boardId, lane.id, lane.revision, boardTag(), { retired: true }),
      async () => {
        await reloadBoard();
        return lanes.find(({ id }) => id === lane.id)?.retired === true;
      }, "Lane retired.", inspection);
  });

  /** @param {HTMLSelectElement} control */
  const selected = (control) => Array.from(control.selectedOptions, ({ value }) => value);
  showLanes();
  editor.append(element("summary", "Manage lanes"), names,
    form("Add lane", [field("Lane label", addName), field("Insert after", after),
      field("Create rights", createFor), field("Connect rights", connectFor)], (submit) => {
      if (!after.value) return;
      const request = {
        label: addName.value, after_id: after.value, create_for: selected(createFor), connect_for: selected(connectFor),
      };
      void writeResource(submit, () => api.createLane(boardId, boardTag(), request), reloadBoard, "Lane added.", inspection);
    }),
    form("Rename lane", [field("Lane to rename", target), field("New lane label", renameName)], (submit) => {
      const lane = lanes.find(({ id }) => id === target.value);
      if (!lane) return;
      void writeResource(submit, () => api.renameLane(lane.id, lane.revision, boardTag(), renameName.value),
        reloadBoard, "Lane renamed.", inspection);
    }), form("Change lane key", [field("Lane key target", keyLane), field("New lane key", keyValue)], (submit) => {
      const lane = lanes.find(({ id, role }) => id === keyLane.value && role !== "completed");
      if (!lane) return;
      const key = keyValue.value;
      void writeResource(submit, () => api.updateLaneSettings(boardId, lane.id, lane.revision, boardTag(), { key }),
        async () => {
          await reloadBoard();
          return /** @type {Lane & {key?: string} | undefined} */ (lanes.find(({ id }) => id === lane.id))?.key === key;
        }, "Lane key changed.", inspection);
    }), approvalForm, retireForm);
  return { editor, showLanes, retainDraft() {
    boardDrafts.set(draftKey, [addName.value, renameName.value, after.value, target.value,
      selected(createFor).join(","), selected(connectFor).join(","), reviewLane.value, retireLane.value, keyLane.value, keyValue.value]);
  } };
}


/** @param {string} boardId @param {Lane[]} lanes @param {(card: Card, opener: HTMLButtonElement) => void} open */
function renderCardSearch(boardId, lanes, open) {
  const view = activeView;
  const state = boardViewState(boardId);
  /** @param {unknown} value @returns {value is SearchFields} */
  function validFields(value) {
    if (!value || typeof value !== "object") return false;
    const fields = /** @type {Partial<SearchFields>} */ (value);
    return typeof fields.query === "string" && typeof fields.content === "boolean"
      && (fields.order === "triage" || fields.order === "ticket")
      && typeof fields.lane === "string" && typeof fields.limit === "string";
  }

  const saved = state.search;
  const fields = validFields(saved?.fields) ? saved.fields : undefined;
  const laneChoices = () => [{ id: "", label: "All lanes" }, ...activeLanes(lanes)];
  const choices = laneChoices();
  const panel = element("details");
  panel.className = "editor";
  panel.open = saved?.open === true;
  panel.append(element("summary", "Find cards"));
  const query = element("input");
  query.type = "search";
  query.value = fields?.query ?? "";
  const content = element("input");
  content.type = "checkbox";
  content.checked = fields?.content ?? false;
  const contentOption = field("Search descriptions and comments", content);
  contentOption.className = "search-content-option";
  const order = select([{ id: "triage", label: "Next work" }, { id: "ticket", label: "Ticket number" }], fields?.order ?? "triage");
  const lane = select(choices, choices.some(({ id }) => id === fields?.lane) ? fields?.lane ?? "" : "");
  const limit = element("input");
  limit.type = "number";
  limit.min = "1";
  limit.max = "200";
  limit.value = fields?.limit ?? "20";
  limit.required = true;
  const results = element("div");
  results.setAttribute("aria-live", "polite");
  let read = 0;
  let submitted = validFields(saved?.submitted) ? saved.submitted : undefined;
  const isCurrent = () => view === activeView && panel.isConnected;
  /** @returns {SearchFields} */
  function controls() {
    return { query: query.value, content: content.checked, order: order.value === "ticket" ? "ticket" : "triage",
      lane: lane.value, limit: limit.value };
  }
  function retain() {
    if (!isCurrent()) return;
    state.search = { open: panel.open, fields: controls(), submitted };
    persistBoardView(boardId);
  }
  /** @param {SearchFields} requested */
  function run(requested) {
    const generation = ++read;
    const current = () => isCurrent() && generation === read;
    submitButton.disabled = true;
    const filters = { q: requested.query.trim() === "" ? undefined : requested.query, searchContent: requested.content,
      sort: requested.order, laneId: requested.lane || undefined, limit: Number(requested.limit) };
    void readWithSession(() => api.listCards(boardId, filters))
      .then((reply) => {
        if (!current()) return;
        const cards = /** @type {Card[]} */ (items(reply, "cards"));
        results.replaceChildren(...cards.map((card) => {
          const opener = button("#" + card.ticket + " " + card.title, () => {
            if (current()) open(card, opener);
          });
          return opener;
        }));
        if (!cards.length) results.textContent = "No matching cards.";
      }).catch(() => {
        if (current()) results.textContent = "Cards could not be read. Submit the search again.";
      }).finally(() => {
        if (current()) submitButton.disabled = false;
      });
  }
  const searchForm = form("Find cards", [field("Title or ticket", query), contentOption,
    field("Result order", order), field("Search lane", lane), field("Maximum results", limit)], () => {
    if (!isCurrent()) return;
    submitted = controls();
    retain();
    run(submitted);
  });
  const submitButton = /** @type {HTMLButtonElement} */ (searchForm.querySelector("button"));
  panel.append(searchForm, results);
  return { panel, retain,
    restore() {
      if (!isCurrent() || !submitted) return;
      const count = Number(submitted.limit);
      if (Number.isInteger(count) && count >= 1 && count <= 200 && choices.some(({ id }) => id === submitted?.lane)) {
        run(submitted);
      } else {
        submitted = undefined;
        retain();
      }
    },
    /** @param {boolean} changed */
    refreshBoard(changed) {
      const selectedLane = lane.value;
      const choices = laneChoices();
      setOptions(lane, choices, choices.some(({ id }) => id === selectedLane) ? selectedLane : "");
      if (!changed || !submitted) return;
      submitted = undefined;
      ++read;
      results.textContent = "Board changed. Run the search again for current results.";
      submitButton.disabled = false;
      retain();
    },
  };
}

/** @param {string} value @param {Card[]} cards */
function ticketSelection(value, cards) {
  const tickets = value.split(/[\s,]+/);
  if (!tickets.length || tickets.some((ticket) => !/^[1-9][0-9]*$/.test(ticket))) throw new Error();
  const selected = tickets.map((ticket) => cards.find((card) => card.ticket === Number(ticket)));
  if (selected.some((card) => !card) || new Set(selected).size !== selected.length) throw new Error();
  return /** @type {Card[]} */ (selected);
}

/** @param {string} boardId @param {string} backupId @param {Inspection} inspection */
function renderPublicationControls(boardId, backupId, inspection) {
  const panel = element("div");
  const publicationResult = element("p");
  const publish = form("Publish backup", [], (submit) => {
    if (pendingPublications.has(boardId)) return;
    const publicationId = newOperationId();
    pendingPublications.set(boardId, { id: publicationId, backupId: backupId });
    submit.dataset.publicationPending = "true";
    refreshPublication.hidden = false;
    void writeResource(submit, async () => {
      try { await api.publishBackup(publicationId, boardId, backupId); }
      catch (error) {
        if (error instanceof ApiError && !error.outcomeUnknown) {
          pendingPublications.delete(boardId);
          delete submit.dataset.publicationPending;
          refreshPublication.hidden = true;
        }
        throw error;
      }
    }, readPublication, "Publication recorded.", inspection);
  });
  const publishButton = /** @type {HTMLButtonElement} */ (publish.querySelector("button"));
  async function readPublication() {
    const pending = pendingPublications.get(boardId);
    if (!pending) return true;
    const publication = await api.getPublication(pending.id, boardId, pending.backupId);
    if (!panel.isConnected || pendingPublications.get(boardId) !== pending) return true;
    const item = /** @type {{state: string, error_code: string|null}} */ (publication.data);
    publicationResult.textContent = "Publication " + item.state + (item.error_code ? " (" + item.error_code + ")" : "") + ".";
    if (item.state === "succeeded" || item.state === "failed") {
      pendingPublications.delete(boardId);
      delete publishButton.dataset.publicationPending;
      publishButton.disabled = false;
      refreshPublication.hidden = true;
    }
    return true;
  }
  const refreshPublication = button("Refresh publication status", () => {
    if (writing) return;
    refreshPublication.disabled = true;
    void readWithSession(readPublication).catch(() => {
      if (panel.isConnected) publicationResult.textContent = "Publication status could not be read. Refresh status before publishing again.";
    }).finally(() => { refreshPublication.disabled = false; });
  });
  const pending = pendingPublications.has(boardId);
  if (pending) publishButton.dataset.publicationPending = "true";
  publishButton.disabled = pending;
  refreshPublication.hidden = !pending;
  if (pending) publicationResult.textContent = "A publication is pending. Refresh its status before publishing again.";
  panel.append(publish, refreshPublication, publicationResult);
  return panel;
}

/** @param {string} boardId @param {Labeled[]} priorities @param {() => Card[]} cards
 * @param {() => string} boardTag @param {() => Promise<void>} reloadBoard @param {Inspection} inspection */
function renderBoardActions(boardId, priorities, cards, boardTag, reloadBoard, inspection) {
  const panel = element("details");
  panel.className = "editor";
  panel.append(element("summary", "Board actions"));
  const publicationPanel = element("div");
  const pendingPublication = pendingPublications.get(boardId);
  if (pendingPublication) publicationPanel.append(renderPublicationControls(boardId, pendingPublication.backupId, inspection));
  panel.append(publicationPanel);
  const tickets = element("input");
  tickets.id = "batch-priority-tickets";
  tickets.required = true;
  tickets.placeholder = "1, 2, 3";
  const priority = select(priorities, null);
  priority.id = "batch-priority-value";
  priority.required = true;
  const result = element("div");
  result.setAttribute("aria-live", "polite");
  panel.append(form("Set batch priority", [field("Ticket numbers", tickets), field("Priority", priority)], (submit) => {
    const requestedTickets = tickets.value;
    const requestedPriority = priority.value;
    const requestedTag = boardTag();
    /** @type {{cards: Card[], changes: {resource_id: string, before: string, after: string}[]} | undefined} */
    let saved;
    void writeResource(submit, async () => {
      const allCards = /** @type {Card[]} */ (await collect((cursor) => api.listCards(boardId, { cursor, archived: "include" }), "cards"));
      let selected;
      try { selected = ticketSelection(requestedTickets, allCards); }
      catch { result.textContent = "Enter distinct ticket numbers from this board."; throw new ApiError("invalid_target"); }
      const reply = await api.setPriorities(boardId, requestedTag, selected.map(({ id }) => id), requestedPriority);
      saved = /** @type {{cards: Card[], changes: {resource_id: string, before: string, after: string}[]}} */ (reply.data);
    }, async () => {
      await reloadBoard();
      if (!saved) return false;
      result.replaceChildren(element("p", saved.changes.length + " priority changes committed."));
      for (const card of saved.cards) result.append(element("p", "#" + card.ticket + " "
        + (priorities.find(({ id }) => id === card.priority_id)?.label ?? card.priority_id)));
      for (const change of saved.changes) {
        const changedCard = cards().find(({ id }) => id === change.resource_id) ?? saved.cards.find(({ id }) => id === change.resource_id);
        result.append(element("p", (changedCard ? "#" + changedCard.ticket : change.resource_id)
          + " " + (priorities.find(({ id }) => id === change.before)?.label ?? change.before)
          + " → " + (priorities.find(({ id }) => id === change.after)?.label ?? change.after)));
      }
      return true;
    }, "Batch priorities saved.", inspection);
  }), result);
  const backupResult = element("div");
  backupResult.setAttribute("aria-live", "polite");
  panel.append(form("Create backup", [], (submit) => {
    const id = newOperationId();
    void writeResource(submit, () => api.createBackup(id, boardId, boardTag()), async () => {
      let reply;
      try { reply = await api.getBackup(id, boardId); }
      catch (error) {
        if (error instanceof ApiError && error.status === 404) return false;
        throw error;
      }
      const backup = /** @type {{id: string, board_revision: number, size: number, sha2_256: string, attachment_ids: string[]}} */ (reply.data);
      const download = button("Download backup", () => {
        download.disabled = true;
        void readWithSession(() => api.getBackupContent(backup.id, backup.size, backup.sha2_256)).then((blob) => {
          const url = URL.createObjectURL(blob);
          const anchor = element("a");
          anchor.href = url;
          anchor.download = "localswim-" + boardId + "-r" + backup.board_revision + ".zip";
          anchor.click();
          setTimeout(() => URL.revokeObjectURL(url), 1000);
        }).catch(() => { announce("error", "Backup download could not be verified. Try downloading again."); })
          .finally(() => { download.disabled = false; });
      });
      download.id = "download-backup";
      backupResult.replaceChildren(element("p", "Captured revision " + backup.board_revision + " with "
        + backup.attachment_ids.length + (backup.attachment_ids.length === 1 ? " attachment file." : " attachment files.")), download);
      publicationPanel.replaceChildren(renderPublicationControls(boardId, backup.id, inspection));
      return true;
    }, "Backup captured.", inspection);
  }), backupResult);
  const restore = element("input");
  restore.type = "file";
  restore.accept = ".zip,application/zip";
  restore.required = true;
  panel.append(form("Restore native backup", [field("Native backup ZIP", restore)], (submit) => {
    const file = restore.files?.[0];
    if (!file) return;
    const requestedTag = boardTag();
    let restored = false;
    void writeResource(submit, async () => {
      const snapshot = await readWithSession(() => api.getSnapshot(boardId));
      const captured = /** @type {{board?: {id?: string, revision?: number}}} */ (snapshot.data).board;
      if (captured?.id !== boardId || typeof captured.revision !== "number"
          || requestedTag !== JSON.stringify(boardId + "-" + captured.revision)) throw new ApiError("http_error", { status: 412 });
      try { await api.restoreSnapshot(boardId, etag(snapshot), new Blob([file], { type: "application/zip" })); }
      catch (error) {
        if (error instanceof ApiError && error.status === 413) throw new ApiError("snapshot_too_large", { status: 413 });
        throw error;
      }
      restored = true;
    }, async () => { await reloadBoard(); return restored; }, "Native backup restored.", inspection);
  }));
  return panel;
}
document.querySelector("header a")?.addEventListener("click", (event) => {
  if (event instanceof MouseEvent) followLink(event, "/");
});
refresh.addEventListener("click", () => {
  if (writing) return;
  if (retrySelectedCard) {
    retrySelectedCard();
    return;
  }
  const inspection = cardInspection ?? boardInspection;
  if (inspection) inspection.inspect();
  else void load();
});
window.addEventListener("popstate", () => void load());
window.addEventListener("pagehide", () => retainBoardView?.());
void load();
