import { ApiError, createBrowserApi, openBrowserSession } from "./api.mjs";

/** @typedef {{id: string, label: string}} Labeled */
/** @typedef {Labeled & {kind: "human"|"bot"}} User */
/** @typedef {import("./api.mjs").RelationshipKind} RelationshipKind */
/** @typedef {{id: string, revision: number, board_id: string, source_card_id: string, target_card_id: string,
 * kind: RelationshipKind}} Relationship */
/** @typedef {{resource_type: string, resource_id: string, operation: string, field: string|null,
 * actor_id: string, at: string, revision: number, before: string|null, after: string|null}} Change */
/** @typedef {{id: string, name: string, default_owner_id: string, user_css?: string, notifications?: object}} Board */
/** @typedef {{id: string, name: string|null}} BoardSummary */
/** @typedef {Labeled & {revision: number, retired: boolean, position: number, role: string}} Lane */
/** @typedef {{id: string, text: string, created_at: string, author_id: string, card_id: string}} Comment */
/** @typedef {{id: string, card_id: string, original_name: string, created_at: string, added_by: string, size: number, board_id: string, active: boolean, previewable: boolean, revision: number, sha2_256: string}} Attachment */
/** @typedef {{id: string, title: string, description: string, board_id: string, lane_id: string,
 * owner_id: string, ticket: number, revision: number, comment_count: number, attachment_count: number,
 * archived: boolean, cover_attachment_id: string|null}} Card */
/** @typedef {{data: object, etag: string|null}} Reply */
/** @typedef {() => Promise<void|boolean>} InspectionRead */
/** @typedef {{scope: string, label: string, require: (read: InspectionRead) => void,
 * inspect: () => void, sync: () => void}} Inspection */
/** @typedef {{title?: string, description?: string, lane?: string, owner?: string, comment?: string}} CardDraft */

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
/** @type {Inspection|undefined} */
let boardInspection;
/** @type {Inspection|undefined} */
let cardInspection;
/** @type {(() => void)|undefined} */
let retainCardDraft;
/** @type {(() => void)|undefined} */
let retainBoardDraft;
/** @type {Map<string, CardDraft>} */
const cardDrafts = new Map();
/** @type {Map<string, string[]>} */
const boardDrafts = new Map();
/** @type {Map<string, {read: InspectionRead, isCurrent: () => boolean}>} */
const pendingInspections = new Map();
/** @type {{id: string, control: HTMLTextAreaElement}|undefined} */
let activeComment;
/** @type {(() => void)|undefined} */
let releaseCardMedia;
/** @type {(() => void)|undefined} */
let releaseBoardMedia;
const boardStyle = new CSSStyleSheet();
document.adoptedStyleSheets = [...document.adoptedStyleSheets, boardStyle];

/** @param {string} phase @param {string} message */
function announce(phase, message) {
  if (!status) return;
  status.dataset.phase = phase;
  status.textContent = message;
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
  if (selected !== null && choices.some(({ id }) => id === selected)) node.value = selected;
}

/** @param {string} title @param {HTMLElement[]} controls @param {(submit: HTMLButtonElement) => void} action */
function form(title, controls, action) {
  const node = element("form");
  const submit = element("button", title);
  submit.type = "submit";
  node.append(...controls, submit);
  node.addEventListener("submit", (event) => {
    event.preventDefault();
    if (!writing && !submit.disabled) action(submit);
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
 * @param {string} collection @returns {Promise<T[]>} */
async function collect(request, collection) {
  /** @type {T[]} */
  const result = [];
  /** @type {string|undefined} */
  let cursor;
  const seen = new Set();
  for (;;) {
    const reply = await request(cursor);
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

async function ensureCapability() {
  if (capability) return;
  connecting ??= openBrowserSession().then((token) => { capability = token; return token; })
    .finally(() => { connecting = undefined; });
  await connecting;
}

/** @template T @param {() => Promise<T>} read */
async function readWithSession(read) {
  await ensureCapability();
  const original = capability;
  try {
    return await read();
  } catch (error) {
    if (!(error instanceof ApiError) || error.status !== 401) throw error;
    if (capability === original) capability = "";
    await ensureCapability();
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
      if (control instanceof HTMLButtonElement) control.disabled = disabled;
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
        if (!pending.isCurrent()) await read();
        return confirmed;
      }).then((confirmed) => {
        pendingInspections.delete(scope);
        if (!isCurrent()) return;
        setSubmissionsDisabled(false);
        if (confirmed === false) {
          announce("unknown", "The submission could not be confirmed. Compare the saved state with your draft.");
        } else {
          announce("ready", "Saved " + resource + " read. Compare it with your draft.");
        }
      }).catch(() => {
        if (isCurrent()) announce("error", "Saved state could not be read.");
      }).finally(() => { writing = false; });
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
        ? "Attachments are limited to 10 MiB."
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
    writing = false;
    submit.disabled = phase !== "error" && !inspected;
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
 * @param {() => string} boardTag @param {() => Promise<void>} reloadBoard @param {HTMLElement} opener */
async function openCard(boardId, summary, lanes, users, boardTag, reloadBoard, opener) {
  if (!details) return;
  retainCardDraft?.();
  releaseCardMedia?.();
  releaseCardMedia = undefined;
  retainCardDraft = undefined;
  cardInspection = undefined;
  activeComment = undefined;
  const view = details;
  const cardView = ++activeCard;
  view.hidden = false;
  view.replaceChildren(element("p", "Loading card…"));
  try {
    const reply = await readWithSession(() => api.getCard(summary.id));
    if (cardView !== activeCard) return;
    const card = /** @type {Card} */ (reply.data);
    if (card.id !== summary.id || card.board_id !== boardId) throw new ApiError("invalid_response");
    let cardTag = etag(reply);
    let savedCard = card;
    const title = element("input");
    title.value = card.title;
    const description = element("textarea");
    description.value = card.description;
    let titleBaseline = title.value;
    let descriptionBaseline = description.value;
    const lane = select(lanes, card.lane_id);
    const owner = select(users, card.owner_id);
    owner.required = true;
    const current = element("div");
    const commentList = element("div");
    const attachmentList = element("div");
    const historyPanel = element("details");
    const historyList = element("div");
    const relationshipList = element("div");
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
    if (previous?.lane !== undefined && lanes.some(({ id }) => id === previous.lane)) lane.value = previous.lane;
    if (previous?.owner !== undefined && users.some(({ id }) => id === previous.owner)) owner.value = previous.owner;
    if (previous?.comment !== undefined) comment.value = previous.comment;
    retainCardDraft = () => {
      /** @type {CardDraft} */
      const draft = {};
      if (title.value !== titleBaseline) draft.title = title.value;
      if (description.value !== descriptionBaseline) draft.description = description.value;
      if (lane.value !== savedCard.lane_id) draft.lane = lane.value;
      if (owner.value !== savedCard.owner_id) draft.owner = owner.value;
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
      ++activeCard;
      view.hidden = true;
      view.replaceChildren();
      const replacement = main?.querySelector(`button[data-card-id="${card.id}"]`);
      const target = replacement instanceof HTMLButtonElement ? replacement : opener.isConnected ? opener : refresh;
      target?.focus();
    }
    const close = button("Close card", () => { if (!writing) closeDrawer(); });
    const heading = element("h2", `#${card.ticket} ${card.title}`);
    heading.tabIndex = -1;
    const inspection = createInspection(view, () => cardView === activeCard, async () => {
      await Promise.all([readCurrent(), refreshRelationships()]);
    }, "card", "card:" + card.id);
    cardInspection = inspection;

    /** @param {Card} saved */
    function showCurrent(saved) {
      heading.textContent = `#${saved.ticket} ${saved.title}`;
      updateActions(saved);
      const body = element("p", saved.description);
      body.className = "body-text";
      current.replaceChildren(element("h3", "Saved card"), element("p", saved.title), body,
        element("p", "Lane: " + (lanes.find(({ id }) => id === saved.lane_id)?.label ?? "Unavailable")),
        ...(saved.archived ? [element("p", "Archived")] : []),
        element("p", "Owner: " + (users.find(({ id }) => id === saved.owner_id)?.label ?? "Unknown user")));
    }

    /** @param {Card} saved */
    function adoptSaved(saved) {
      titleBaseline = adoptText(title, titleBaseline, saved.title);
      descriptionBaseline = adoptText(description, descriptionBaseline, saved.description);
      if (lane.value === savedCard.lane_id) lane.value = saved.lane_id;
      if (owner.value === savedCard.owner_id) owner.value = saved.owner_id;
      savedCard = saved;
      showCurrent(saved);
    }

    async function readSavedCard() {
      const latest = await api.getCard(card.id);
      const saved = /** @type {Card} */ (latest.data);
      const tag = etag(latest);
      if (saved.id !== card.id || saved.board_id !== boardId) {
        throw new ApiError("invalid_response");
      }
      if (cardView === activeCard) {
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
        writing = false;
        if (cardView === activeCard) submit.disabled = !allowSubmit;
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
      const [resources] = await Promise.all([refreshLists(submittedAttachment), reloadBoard(),
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
      if (item.id !== id || item.card_id !== card.id) throw new ApiError("invalid_response");
      return item;
    }

    let historyRead = 0;
    async function refreshHistory() {
      const read = ++historyRead;
      try {
        const snapshot = await readWithSession(() => api.getSnapshot(boardId));
        const data = /** @type {{board?: {id: string}}} */ (snapshot.data);
        if (data.board?.id !== boardId) throw new ApiError("invalid_response");
        const entries = /** @type {Change[]} */ (items(snapshot, "activity")).filter((entry) =>
          entry.resource_type === "card" && entry.resource_id === card.id);
        if (cardView !== activeCard || read !== historyRead || !historyPanel.open) return;
        historyList.replaceChildren(...entries.reverse().map((entry) => {
          const row = element("article");
          row.append(element("small", userLabel(entry.actor_id) + " · " + entry.at + " · Revision " + entry.revision),
            element("p", entry.field ?? entry.operation));
          if (entry.field !== null) {
            for (const [label, value] of [["Previous", entry.before], ["Saved", entry.after]]) {
              const text = element("p", label + ": " + (value ?? "—"));
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
      row.append(note);
      return row;
    }

    let relationshipRead = 0;
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
      relationshipList.replaceChildren(...activeLinks.map((entry) => {
        const other = targets.get(entry.source_card_id === card.id ? entry.target_card_id : entry.source_card_id);
        const direction = relationshipDirection(entry, card.id);
        const row = element("article");
        const title = element("p", relationshipLabels[direction] + " ");
        if (other) title.append(link(ticketHref(boardId, other.ticket), "#" + other.ticket + " " + other.title));
        row.append(title);
        const type = select(relationshipChoices, direction);
        row.append(form("Change link type", [field("Link type", type)], (submit) => {
          const wanted = /** @type {RelationshipKind} */ (type.value);
          if (wanted === direction) return;
          const canonical = entry.source_card_id === card.id ? wanted : inverseRelationships[wanted] ?? wanted;
          void writeResource(submit, () => api.replaceRelationship(entry.id, entry.revision, boardTag(), canonical),
            async () => (await readLinks()).some((value) => value.id === entry.id
              && relationshipDirection(value, card.id) === wanted), "Link type changed.", inspection);
        }), form("Remove link", [], (submit) => {
          void writeResource(submit, () => api.removeRelationship(entry.id, entry.revision, boardTag()),
            async () => !(await readLinks()).some((value) => value.id === entry.id), "Link removed.", inspection);
        }));
        return row;
      }));
      if (!activeLinks.length) relationshipList.textContent = "No links.";
      inspection.sync();
      return activeLinks;
    }

    async function readLinks() {
      const [links] = await Promise.all([refreshRelationships(), readSavedCard(), reloadBoard()]);
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

    const archiveForm = form("Archive card", [], (submit) => {
      const archived = !savedCard.archived;
      void writeResource(submit, () => api.patchCard(card.id, cardTag, { archived }),
        async () => (await readCurrent()).saved.archived === archived,
        archived ? "Card archived." : "Card restored.", inspection);
    });
    archiveAction.append(archiveForm);
    const approveForm = form("Reviewed & approved", [], (submit) => {
      const id = crypto.randomUUID();
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

    /** @param {Card} saved */
    function updateActions(saved) {
      const control = archiveForm.querySelector("button");
      if (control) control.textContent = saved.archived ? "Restore card" : "Archive card";
      approvalAction.hidden = saved.archived || !lanes.some(({ id, role }) =>
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
      form("Move card", [field("Lane", lane)], (submit) => void save({ lane_id: lane.value }, submit)),
      form("Assign owner", [field("Owner", owner)], (submit) => void save({ owner_id: owner.value }, submit)),
      element("h3", "Relationships"), relationshipList, addLink,
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
    heading.focus();
    void readWithSession(refreshRelationships).catch(() => {
      if (cardView === activeCard && !relationshipList.hasChildNodes()) relationshipList.textContent = "Links could not be read.";
    });
    void readWithSession(() => refreshLists()).catch(() => {
      if (cardView !== activeCard || commentList.hasChildNodes() || attachmentList.hasChildNodes()) return;
      commentList.textContent = "Comments could not be read.";
      attachmentList.textContent = "Attachments could not be read.";
    });
  } catch (error) {
    if (cardView === activeCard) view.replaceChildren(element("p", readError(error)));
  }
}

/** @param {string} boardId */
async function readBoardLanes(boardId) {
  for (let attempt = 0; attempt < 2; attempt++) {
    const board = await api.getBoard(boardId);
    const lanes = /** @type {Lane[]} */ (await collect((cursor) => api.listLanes(boardId, cursor), "lanes"));
    const check = await api.getBoardIfChanged(boardId, etag(board));
    if (check.data === null) return { board, lanes: activeLanes(lanes) };
  }
  throw new ApiError("board_changed_during_read", { status: 412 });
}

async function load() {
  if (!identity || !navigation || !main || !details) return;
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
  activeComment = undefined;
  const view = ++activeView;
  ++activeCard;
  details.hidden = true;
  details.replaceChildren();
  main.replaceChildren();
  boardStyle.replaceSync("");
  announce("loading", "Loading…");
  try {
    await ensureCapability();
    if (view !== activeView) return;
    const [session, boards] = await Promise.all([
      api.getSession(), /** @type {Promise<BoardSummary[]>} */ (collect(api.listBoards, "boards")),
    ]);
    if (view !== activeView) return;
    const context = /** @type {{kind?: unknown, workspace_admin?: unknown}} */ (session.data);
    if (context.kind !== "human") throw new ApiError("invalid_response");
    identity.textContent = "Human";
    const boardId = location.pathname.match(/^\/boards\/([0-9a-f-]{36})\/?$/i)?.[1]?.toLowerCase();
    /** @param {BoardSummary[]} rows */
    function showBoards(rows) {
      navigation?.replaceChildren(...rows.map((board) => {
        const item = link("/boards/" + board.id, board.name ?? "Unavailable board");
        if (board.id === boardId) item.setAttribute("aria-current", "page");
        return item;
      }));
    }
    showBoards(boards);
    if (!boardId && location.pathname === "/") {
      main.replaceChildren(element("h1", "Workspace"),
        element("p", boards.length ? "Select a board." : "No boards are available."));
      if (context.workspace_admin === true) {
        const boardName = element("input");
        boardName.required = true;
        boardName.maxLength = 256;
        boardName.value = boardDrafts.get("workspace")?.[0] ?? "";
        retainBoardDraft = () => { boardDrafts.set("workspace", [boardName.value]); };
        const inspection = createInspection(main, () => view === activeView, async () => {
          const latest = /** @type {BoardSummary[]} */ (await collect(api.listBoards, "boards"));
          if (view === activeView) showBoards(latest);
        }, "workspace", "workspace");
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
        }));
        inspection.sync();
      }
      announce("ready", "Workspace loaded.");
      return;
    }
    if (!boardId) throw new ApiError("http_error", { status: 404 });
    const [{ board: boardReply, lanes }, users] = await Promise.all([
      readBoardLanes(boardId),
      /** @type {Promise<User[]>} */ (collect((cursor) => api.listUsers(boardId, cursor), "users")),
    ]);
    if (view !== activeView) return;
    const board = /** @type {Board} */ (boardReply.data);
    let boardTag = etag(boardReply);
    const columns = element("div");
    columns.className = "lanes";

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

    /** @param {Card} card @param {HTMLElement} opener @param {number} read */
    async function showCover(card, opener, read) {
      const id = card.cover_attachment_id;
      if (!id) return;
      const current = () => view === activeView && read === coverRead && opener.isConnected;
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
        opener.prepend(image);
      } catch {
        // The card remains usable when the cover cannot be read.
      }
    }

    /** @param {Card} card */
    function cardOpener(card) {
      const opener = button("#" + card.ticket + " " + card.title, () => {
        void openCard(board.id, card, lanes, users, () => boardTag, reloadBoard, opener);
      });
      opener.className = "card";
      opener.dataset.cardId = card.id;
      return opener;
    }

    async function reloadCards() {
      const cards = /** @type {Card[]} */ (await collect((cursor) => api.listCards(boardId, { cursor }), "cards"));
      if (view !== activeView) return;
      const focused = document.activeElement;
      const focusedId = focused instanceof HTMLButtonElement && columns.contains(focused) ? focused.dataset.cardId : undefined;
      clearCovers();
      const render = coverRead;
      columns.replaceChildren(...lanes.map((lane) => {
        const column = element("section");
        column.className = "lane";
        column.append(element("h2", lane.label));
        for (const card of cards.filter(({ lane_id }) => lane_id === lane.id)) {
          const opener = cardOpener(card);
          column.append(opener);
          if (card.cover_attachment_id) void showCover(card, opener, render);
        }
        return column;
      }));
      if (focusedId) [...columns.querySelectorAll("button")].find((control) =>
        control.dataset.cardId === focusedId)?.focus();
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

    await reloadCards();
    if (view !== activeView) return;
    boardStyle.replaceSync(board.user_css ?? "");
    const addTitle = element("input");
    addTitle.required = true;
    const addDescription = element("textarea");
    const addLane = select(lanes, lanes[0]?.id ?? null);
    const seedTicket = ticketInput();
    const seedType = select(relationshipChoices, "relates_to");
    const previous = boardDrafts.get(board.id);
    addTitle.value = previous?.[0] ?? "";
    addDescription.value = previous?.[1] ?? "";
    if (lanes.some(({ id }) => id === previous?.[2])) addLane.value = previous?.[2] ?? "";
    seedTicket.value = previous?.[3] ?? "";
    if (previous?.[4] && Object.hasOwn(relationshipLabels, previous[4])) seedType.value = previous[4];
    const editor = element("section");
    editor.className = "editor";
    const inspection = createInspection(main, () => view === activeView, reloadBoard, "board", "board:" + board.id);
    boardInspection = inspection;
    editor.append(button(inspection.label, inspection.inspect));
    const seedControls = element("details");
    seedControls.append(element("summary", "Link the new card"),
      field("Target ticket", seedTicket), field("Link type", seedType));
    seedControls.open = Boolean(seedTicket.value);
    editor.append(form("Add card", [field("New title", addTitle), field("New description", addDescription),
      field("New card lane", addLane), seedControls], (submit) => {
      const id = crypto.randomUUID();
      let created = false;
      const draft = { title: addTitle.value, description: addDescription.value, lane_id: addLane.value };
      const targetTicket = seedTicket.valueAsNumber;
      const linkKind = /** @type {RelationshipKind} */ (seedType.value);
      const expected = boardTag;
      void writeResource(submit, async () => {
        let relationships;
        if (seedTicket.value !== "") {
          const target = await readWithSession(() => findCardByTicket(board.id, targetTicket));
          if (!target || view !== activeView) throw new ApiError("invalid_target");
          relationships = [{ target_card_id: target.id, kind: linkKind }];
        }
        await api.createCard({ ...draft, id, board_id: board.id, expected_board_etag: expected, relationships });
        created = true;
      }, async () => {
        if (!created) {
          try {
            const reply = await api.getCard(id);
            created = /** @type {Card} */ (reply.data).id === id;
          } catch (error) {
            if (!(error instanceof ApiError) || error.status !== 404) throw error;
          }
        }
        if (created) {
          if (addTitle.value === draft.title) addTitle.value = "";
          if (addDescription.value === draft.description) addDescription.value = "";
          if (seedTicket.valueAsNumber === targetTicket) seedTicket.value = "";
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

    async function reloadBoard() {
      const latest = await readBoardLanes(board.id);
      if (view !== activeView) return;
      lanes.splice(0, lanes.length, ...latest.lanes);
      boardTag = etag(latest.board);
      boardStyle.replaceSync(/** @type {Board} */ (latest.board.data).user_css ?? "");
      setOptions(addLane, lanes, addLane.value);
      laneEditor.showLanes();
      await Promise.all([reloadCards(), reloadArchived()]);
    }
    const laneEditor = renderLaneEditor(board.id, lanes, users, () => boardTag, reloadBoard, inspection);
    retainBoardDraft = () => {
      boardDrafts.set(board.id, [addTitle.value, addDescription.value, addLane.value, seedTicket.value, seedType.value]);
      laneEditor.retainDraft();
    };
    main.replaceChildren(element("h1", board.name), editor, recent, laneEditor.editor, columns, archived);
    inspection.sync();
    announce("ready", "Board loaded.");
    const ticketValue = new URLSearchParams(location.search).get("ticket");
    if (ticketValue !== null) {
      const ticket = Number(ticketValue);
      let selected;
      try {
        if (/^[1-9][0-9]*$/.test(ticketValue) && Number.isSafeInteger(ticket)) {
          selected = await readWithSession(() => findCardByTicket(board.id, ticket));
        }
      } catch (error) {
        if (view === activeView) announce("error", readError(error));
        return;
      }
      if (view !== activeView || !refresh) return;
      if (selected) void openCard(board.id, selected, lanes, users, () => boardTag, reloadBoard, refresh);
      else announce("error", "This ticket is unavailable.");
    }
  } catch (error) {
    if (view !== activeView) return;
    if (error instanceof ApiError && error.status === 401) capability = "";
    main.replaceChildren();
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
  const after = select(lanes, null);
  const createFor = select(users, null);
  const connectFor = select(users, null);
  for (const grants of [createFor, connectFor]) {
    grants.multiple = true;
    for (const option of grants.options) option.selected = false;
  }
  const target = select(lanes, null);
  const renameName = element("input");
  renameName.required = true;
  renameName.maxLength = 256;
  const names = element("p");
  const draftKey = boardId + ":lanes";
  const previous = boardDrafts.get(draftKey);
  addName.value = previous?.[0] ?? "";
  renameName.value = previous?.[1] ?? "";
  if (lanes.some(({ id }) => id === previous?.[2])) after.value = previous?.[2] ?? "";
  if (lanes.some(({ id }) => id === previous?.[3])) target.value = previous?.[3] ?? "";
  for (const [control, value] of [[createFor, previous?.[4]], [connectFor, previous?.[5]]]) {
    if (!(control instanceof HTMLSelectElement) || typeof value !== "string") continue;
    const ids = value.split(",");
    for (const option of control.options) option.selected = ids.includes(option.value);
  }

  function showLanes() {
    setOptions(after, lanes, after.value);
    setOptions(target, lanes, target.value);
    names.textContent = "Saved lanes: " + lanes.map(({ label }) => label).join(", ");
  }

  /** @param {HTMLSelectElement} control */
  const selected = (control) => Array.from(control.selectedOptions, ({ value }) => value);
  showLanes();
  editor.append(element("summary", "Manage lanes"), names,
    form("Add lane", [field("Lane label", addName), field("Insert after", after),
      field("Create rights", createFor), field("Connect rights", connectFor)], (submit) => {
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
    }));
  return { editor, showLanes, retainDraft() {
    boardDrafts.set(draftKey, [addName.value, renameName.value, after.value, target.value,
      selected(createFor).join(","), selected(connectFor).join(",")]);
  } };
}

document.querySelector("header a")?.addEventListener("click", (event) => {
  if (event instanceof MouseEvent) followLink(event, "/");
});
refresh.addEventListener("click", () => {
  if (writing) return;
  const inspection = cardInspection ?? boardInspection;
  if (inspection) inspection.inspect();
  else void load();
});
window.addEventListener("popstate", () => void load());
void load();
