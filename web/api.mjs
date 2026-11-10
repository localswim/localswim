const API_PREFIX = "/api/2026-11-10";
const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const STRONG_ETAG = /^"[A-Za-z0-9_-]{16,128}"$/;
const CAPABILITY = /^[A-Za-z0-9_-]{43,128}$/;
const REQUEST_TIMEOUT_MS = 15000;
const MAX_ATTACHMENT_BYTES = 10 * 1024 * 1024;
const RASTER_MEDIA_TYPES = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);

/** @typedef {"blocks"|"blocked_by"|"duplicates"|"duplicated_by"|"references"|"referenced_by"|"relates_to"} RelationshipKind */

export class ApiError extends Error {
  /**
   * @param {string} code
   * @param {{ status?: number | null, outcomeUnknown?: boolean }} [options]
   */
  constructor(code, { status = null, outcomeUnknown = false } = {}) {
    super(code);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
    this.outcomeUnknown = outcomeUnknown;
  }
}

/** @param {typeof fetch} [fetchImpl] */
export async function openBrowserSession(fetchImpl = globalThis.fetch.bind(globalThis)) {
  let response;
  try {
    response = await fetchImpl(API_PREFIX + "/browser-session", {
      method: "POST",
      mode: "same-origin",
      credentials: "omit",
      redirect: "error",
      cache: "no-store",
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    });
  } catch {
    throw new ApiError("transport_failed");
  }
  if (response.status !== 200) {
    await discardBody(response);
    throw new ApiError("http_error", { status: response.status });
  }
  const session = /** @type {Record<string, unknown>} */ (await responseObject(response, false));
  if (session.kind !== "human" || !isId(session.principal_id) || session.api_version !== "2026-11-10"
      || typeof session.workspace_admin !== "boolean" || session.expires_on_service_replacement !== true
      || typeof session.access_token !== "string" || !CAPABILITY.test(session.access_token)) {
    throw new ApiError("invalid_response", { status: response.status });
  }
  return session.access_token;
}

/** @param {unknown} id */
function resourceId(id) {
  if (typeof id !== "string" || !UUID_V4.test(id)) {
    throw new TypeError("Expected a UUIDv4 resource ID.");
  }
  return id;
}

/** @param {unknown} etag */
function strongTag(etag) {
  if (typeof etag !== "string" || !STRONG_ETAG.test(etag)) {
    throw new TypeError("Expected an original strong ETag.");
  }
  return etag;
}

/** @param {Response} response */
async function discardBody(response) {
  await response.body?.cancel().catch(() => {});
}

/** @param {unknown} value @returns {value is string} */
function isId(value) {
  return typeof value === "string" && UUID_V4.test(value);
}

/** @param {unknown} value @returns {value is number} */
function isInteger(value) {
  return typeof value === "number" && Number.isSafeInteger(value);
}

/** @param {unknown} value */
function isTimestamp(value) {
  return typeof value === "string" && /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\+00:00$/.test(value);
}

/** @param {object} data @param {string} [id] @param {string} [boardId] */
function validLane(data, id, boardId) {
  const lane = /** @type {Record<string, unknown>} */ (data);
  return Object.keys(lane).length === 12
    && isId(lane.id) && (id === undefined || lane.id.toLowerCase() === id.toLowerCase())
    && isId(lane.board_id) && (boardId === undefined || lane.board_id.toLowerCase() === boardId.toLowerCase())
    && isInteger(lane.revision) && lane.revision > 0
    && isTimestamp(lane.created_at) && isTimestamp(lane.updated_at)
    && typeof lane.key === "string" && typeof lane.label === "string" && lane.label.length > 0
    && isInteger(lane.position) && lane.position >= 0
    && typeof lane.role === "string" && typeof lane.retired === "boolean"
    && typeof lane.approval_enabled === "boolean" && typeof lane.reorder_enabled === "boolean";
}

/** @param {object} data @param {string} [id] @param {string} [boardId] */
function validRelationship(data, id, boardId) {
  const link = /** @type {Record<string, unknown>} */ (data);
  return Object.keys(link).length === 8 && !Object.hasOwn(link, "etag")
    && isId(link.id) && (id === undefined || link.id.toLowerCase() === id.toLowerCase())
    && isId(link.board_id) && (boardId === undefined || link.board_id.toLowerCase() === boardId.toLowerCase())
    && isInteger(link.revision) && link.revision > 0
    && isTimestamp(link.created_at) && isTimestamp(link.updated_at)
    && isId(link.source_card_id) && isId(link.target_card_id)
    && typeof link.kind === "string"
    && ["blocks", "blocked_by", "duplicates", "duplicated_by", "references", "referenced_by", "relates_to"].includes(link.kind);
}

/** @param {object} data @param {string} boardId @param {string | undefined} id */
function validCard(data, boardId, id) {
  const card = /** @type {Record<string, unknown>} */ (data);
  return Object.keys(card).length === 21
    && isId(card.id) && (id === undefined || card.id.toLowerCase() === id.toLowerCase())
    && isId(card.board_id) && card.board_id.toLowerCase() === boardId.toLowerCase()
    && isInteger(card.revision) && card.revision > 0
    && isInteger(card.ticket) && card.ticket > 0
    && isTimestamp(card.created_at) && isTimestamp(card.updated_at)
    && typeof card.title === "string" && card.title.length > 0
    && typeof card.description === "string"
    && isId(card.lane_id) && isId(card.priority_id)
    && isId(card.owner_id)
    && (card.parent_id === null || isId(card.parent_id))
    && Array.isArray(card.child_ids) && card.child_ids.every(isId)
    && typeof card.archived === "boolean"
    && (card.cover_attachment_id === null || isId(card.cover_attachment_id))
    && isInteger(card.comment_count) && card.comment_count >= 0
    && isInteger(card.attachment_count) && card.attachment_count >= 0
    && isInteger(card.blocking_depth) && card.blocking_depth >= 0
    && isInteger(card.blocked_card_count) && card.blocked_card_count >= 0
    && isInteger(card.triage_score)
    && (card.completed_at === null || isTimestamp(card.completed_at));
}

/** @param {object} data @param {string} id @param {string} boardId @param {string} cardId */
function matchesApproval(data, id, boardId, cardId) {
  const approval = /** @type {Record<string, unknown>} */ (data);
  if (Object.keys(approval).length !== 7 || approval.id !== id || approval.board_id !== boardId
      || !isId(approval.actor_id) || !isTimestamp(approval.at)
      || typeof approval.board_etag !== "string" || !STRONG_ETAG.test(approval.board_etag)
      || !Array.isArray(approval.card_ids) || approval.card_ids.length !== 1 || approval.card_ids[0] !== cardId
      || !Array.isArray(approval.changes) || approval.changes.length !== 2) return false;
  /** @param {unknown} raw @returns {raw is Record<string, unknown>} */
  const matchesChange = (raw) => {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return false;
    const change = /** @type {Record<string, unknown>} */ (raw);
    return Object.keys(change).length === 11 && isId(change.id) && isId(change.resource_id)
      && change.board_id === boardId && change.actor_id === approval.actor_id && change.at === approval.at
      && isInteger(change.revision) && change.revision > 1;
  };
  const [move, note] = /** @type {unknown[]} */ (approval.changes);
  return matchesChange(move) && matchesChange(note) && move.id === id && note.id !== id
    && move.revision === note.revision && move.operation === "approve" && move.resource_type === "card"
    && move.resource_id === cardId && move.field === "lane_id" && isId(move.before) && isId(move.after)
    && move.before !== move.after && note.operation === "comment" && note.resource_type === "comment"
    && note.field === null && note.before === null && note.after === null;
}

/** @param {Response} response @param {boolean} write */
async function responseObject(response, write) {
  try {
    /** @type {unknown} */
    const data = await response.json();
    if (data !== null && typeof data === "object" && !Array.isArray(data)) {
      return data;
    }
  } catch {
    // Keep raw server content and parser details out of errors.
  }
  throw new ApiError("invalid_response", { status: response.status, outcomeUnknown: write });
}

const EVENT_TYPES = new Set(["snapshot", "change"]);
const MAX_EVENT_CHARS = 64 * 1024;

/** @param {string} frame */
function parseEvent(frame) {
  const dataLines = frame.split(/\r?\n/)
    .filter((line) => line === "data" || line.startsWith("data:"))
    .map((line) => line.slice(5).replace(/^ /, ""));
  if (dataLines.length === 0) {
    return null;
  }

  /** @type {unknown} */
  const value = JSON.parse(dataLines.join("\n"));
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error();
  }
  const { type, resource, board_id, board_revision, workspace_revision } =
    /** @type {Record<string, unknown>} */ (value);
  if (typeof type !== "string" || !EVENT_TYPES.has(type)) {
    throw new Error();
  }
  const count = Object.keys(value).length;
  if (resource === "board") {
    if (count !== 4 || typeof board_id !== "string" || !UUID_V4.test(board_id)
        || !Number.isSafeInteger(board_revision) || Number(board_revision) < 1) {
      throw new Error();
    }
  } else if (resource === "workspace") {
    if (count !== 3 || !Number.isSafeInteger(workspace_revision) || Number(workspace_revision) < 1) {
      throw new Error();
    }
  } else if (resource !== "catalog" || count !== 2) {
    throw new Error();
  }
  return value;
}

/** @param {ReadableStream<Uint8Array>} body */
async function* readEvents(body) {
  const reader = body.getReader();
  const decoder = new TextDecoder("utf-8", { fatal: true });
  let pending = "";
  try {
    for (;;) {
      const { done, value } = await reader.read().catch(() => {
        throw new ApiError("transport_failed");
      });
      try {
        pending += decoder.decode(value, { stream: !done });
      } catch {
        throw new ApiError("invalid_response", { status: 200 });
      }
      if (done) {
        if (pending !== "") {
          throw new ApiError("invalid_response", { status: 200 });
        }
        return;
      }
      let delimiter;
      while ((delimiter = /\r?\n\r?\n/.exec(pending)) !== null) {
        const length = delimiter.index;
        if (length > MAX_EVENT_CHARS) {
          throw new ApiError("invalid_response", { status: 200 });
        }
        const frame = pending.slice(0, length);
        pending = pending.slice(length + delimiter[0].length);
        let event;
        try {
          event = parseEvent(frame);
        } catch {
          throw new ApiError("invalid_response", { status: 200 });
        }
        if (event !== null) {
          yield event;
        }
      }
      // A chunk may end partway through the blank-line separator.
      const partialSeparator = /(?:\r?\n\r?|\r)$/.exec(pending)?.[0].length ?? 0;
      if (pending.length - partialSeparator > MAX_EVENT_CHARS) {
        throw new ApiError("invalid_response", { status: 200 });
      }
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
/**
 * The embedding page supplies the current browser capability from memory.
 * @param {{ getCapability: () => unknown, fetchImpl?: typeof fetch }} options
 */
export function createBrowserApi({ getCapability, fetchImpl = globalThis.fetch.bind(globalThis) }) {
  if (typeof getCapability !== "function" || typeof fetchImpl !== "function") {
    throw new TypeError("A browser capability adapter and fetch are required.");
  }

  /**
   * @param {string} path
   * @param {{ method?: string, data?: unknown, form?: FormData, etag?: string, ifNoneMatch?: string, boardEtag?: string, accept?: string, signal?: AbortSignal }} [options]
   */
  async function send(path, { method = "GET", data, form, etag, ifNoneMatch, boardEtag, accept = "application/json", signal } = {}) {
    const body = data === undefined ? form : JSON.stringify(data);
    let headers;
    try {
      const capability = await getCapability();
      if (typeof capability !== "string" || !capability || /\s/.test(capability)) {
        throw new Error();
      }
      headers = new Headers({
        Accept: accept,
        Authorization: `Bearer ${capability}`,
      });
    } catch {
      throw new ApiError("capability_unavailable");
    }

    if (data !== undefined) {
      headers.set("Content-Type", "application/json");
    }
    if (etag !== undefined) {
      headers.set("If-Match", etag);
    }

    if (ifNoneMatch !== undefined) {
      headers.set("If-None-Match", ifNoneMatch);
    }

    if (boardEtag !== undefined) headers.set("X-Expected-Board-ETag", boardEtag);

    const write = method !== "GET";
    let response;
    try {
      response = await fetchImpl(API_PREFIX + path, {
        method,
        signal: signal ?? (accept === "text/event-stream" ? undefined : AbortSignal.timeout(REQUEST_TIMEOUT_MS)),
        headers,
        body,
        mode: "same-origin",
        credentials: "omit",
        redirect: "error",
        cache: "no-store",
      });
    } catch {
      throw new ApiError("transport_failed", { outcomeUnknown: write });
    }

    if (!response.ok && !(response.status === 304 && ifNoneMatch !== undefined)) {
      await discardBody(response);
      throw new ApiError("http_error", {
        status: response.status,
        outcomeUnknown: write && response.status >= 500,
      });
    }

    return response;
  }

  /**
   * @param {string} path
   * @param {{ method?: string, data?: unknown, form?: FormData, etag?: string, successStatus?: number }} [options]
   */
  async function request(path, { method = "GET", data, form, etag, successStatus = 200 } = {}) {
    const response = await send(path, { method, data, form, etag });
    const write = method !== "GET";
    const responseEtag = response.headers.get("ETag");
    if (write && (response.status !== successStatus || !STRONG_ETAG.test(responseEtag ?? ""))) {
      await discardBody(response);
      throw new ApiError("invalid_response", { status: response.status, outcomeUnknown: true });
    }
    return { data: await responseObject(response, write), etag: responseEtag };
  }

  /** @param {string} path @param {string} id @param {{data?: unknown, form?: FormData}} content */
  async function createWithId(path, id, content) {
    const reply = await request(path, { ...content, method: "POST", successStatus: 201 });
    const item = /** @type {{id?: unknown}} */ (reply.data);
    if (item.id !== id.toLowerCase()) {
      throw new ApiError("invalid_response", { status: 201, outcomeUnknown: true });
    }
    return reply;
  }

  /** @param {string} path @param {Record<string, string>} filters @param {string} [cursor] */
  function list(path, filters, cursor) {
    const query = new URLSearchParams(filters);
    if (cursor !== undefined) query.set("cursor", cursor);
    return request(query.size ? path + "?" + query : path);
  }

  /** @param {unknown} relationshipId @param {unknown} revision */
  async function currentRelationship(relationshipId, revision) {
    const id = resourceId(relationshipId);
    if (!isInteger(revision) || revision < 1) {
      throw new TypeError("Expected a positive safe integer relationship revision.");
    }
    const response = await send("/relationships/" + id);
    const tag = response.headers.get("ETag");
    if (response.status !== 200 || !STRONG_ETAG.test(tag ?? "")) {
      await discardBody(response);
      throw new ApiError("invalid_response", { status: response.status });
    }
    const data = await responseObject(response, false);
    if (!validRelationship(data, id)) {
      throw new ApiError("invalid_response", { status: 200 });
    }
    if (/** @type {{revision: number}} */ (data).revision !== revision) {
      throw new ApiError("http_error", { status: 412 });
    }
    return { data, etag: strongTag(tag) };
  }

  return Object.freeze({

    /**
     * @param {unknown} boardId
     * @param {unknown} etag The ETag of the previously read board summary.
     */
    async getBoardIfChanged(boardId, etag) {
      const originalTag = strongTag(etag);
      const response = await send("/boards/" + resourceId(boardId), { ifNoneMatch: originalTag });
      const responseTag = response.headers.get("ETag");
      const unchanged = response.status === 304;
      if (responseTag === null || !STRONG_ETAG.test(responseTag)
          || (unchanged ? responseTag !== originalTag : response.status !== 200 || responseTag === originalTag)) {
        await discardBody(response);
        throw new ApiError("invalid_response", { status: response.status });
      }
      return { data: unchanged ? null : await responseObject(response, false), etag: responseTag };
    },
    /**
     * @param {unknown} boardId
     * @param {{ ticket?: number, limit?: number, cursor?: string, archived?: "include"|"only" }} [filters]
     */
    listCards(boardId, { ticket, limit, cursor, archived } = {}) {
      const query = new URLSearchParams({ board_id: resourceId(boardId) });
      if (ticket !== undefined) {
        if (!Number.isSafeInteger(ticket) || ticket < 1) {
          throw new TypeError("Expected a positive safe integer ticket.");
        }
        query.set("ticket", String(ticket));
      }
      if (limit !== undefined) query.set("limit", String(limit));
      if (archived !== undefined) query.set("archived", archived);
      if (cursor !== undefined) {
        query.set("cursor", cursor);
      }
      return request("/cards?" + query);
    },
    /** @param {unknown} cardId */
    async getCard(cardId) {
      const id = resourceId(cardId);
      const reply = await request("/cards/" + id);
      const card = /** @type {{ board_id?: unknown }} */ (reply.data);
      if (!STRONG_ETAG.test(reply.etag ?? "") || !isId(card.board_id)
          || !validCard(reply.data, card.board_id, id)) {
        throw new ApiError("invalid_response", { status: 200 });
      }
      return reply;
    },
    getSession: () => request("/session"),
    /** @param {{board_id: string, card_id?: string}} filter
     * @param {number} [limit] @param {string} [cursor] */
    listComments(filter, limit, cursor) {
      /** @type {Record<string, string>} */
      const query = { board_id: resourceId(filter.board_id) };
      if (filter.card_id !== undefined) query.card_id = resourceId(filter.card_id);
      if (limit !== undefined) query.limit = String(limit);
      return list("/comments", query, cursor);
    },
    /** @param {unknown} cardId @param {unknown} etag @param {string} text @param {unknown} id */
    createComment(cardId, etag, text, id) {
      const expectedId = resourceId(id);
      return createWithId("/comments", expectedId, {
        data: { card_id: resourceId(cardId), expected_card_etag: strongTag(etag), text, id: expectedId },
      });
    },
    /** @param {unknown} cardId @param {string} [cursor] */
    listAttachments: (cardId, cursor) => list("/attachments", { card_id: resourceId(cardId) }, cursor),
    /** @param {unknown} attachmentId */
    getAttachment: (attachmentId) => request("/attachments/" + resourceId(attachmentId)),
    /** @param {unknown} attachmentId @param {"attachment"|"inline"} disposition @param {number} size */
    async getAttachmentContent(attachmentId, disposition, size) {
      if (!Number.isSafeInteger(size) || size < 0 || size > MAX_ATTACHMENT_BYTES) {
        throw new ApiError("invalid_response");
      }
      const response = await send("/attachments/" + resourceId(attachmentId) + "/content?"
        + new URLSearchParams({ disposition }), { accept: "*/*" });
      const media = response.headers.get("Content-Type")?.split(";", 1)[0].trim().toLowerCase() ?? "";
      const servedDisposition = response.headers.get("Content-Disposition")?.split(";", 1)[0].trim().toLowerCase();
      if (response.status !== 200 || servedDisposition !== disposition
          || (disposition === "inline" && !RASTER_MEDIA_TYPES.has(media))) {
        await discardBody(response);
        throw new ApiError("invalid_response", { status: response.status });
      }
      const reader = response.body?.getReader();
      /** @type {Uint8Array<ArrayBuffer>[]} */
      const chunks = [];
      let received = 0;
      try {
        while (reader) {
          const { value, done } = await reader.read();
          if (done) break;
          received += value.byteLength;
          if (received > size) throw new Error();
          chunks.push(new Uint8Array(value));
        }
        if (received !== size) throw new Error();
      } catch {
        throw new ApiError("invalid_response", { status: response.status });
      } finally {
        await reader?.cancel().catch(() => {});
        reader?.releaseLock();
      }
      return new Blob(chunks, { type: disposition === "inline" ? media : "application/octet-stream" });
    },
    /** @param {unknown} cardId @param {unknown} etag @param {File} file @param {unknown} id */
    uploadAttachment(cardId, etag, file, id) {
      const expectedId = resourceId(id);
      const form = new FormData();
      form.set("card_id", resourceId(cardId));
      form.set("expected_card_etag", strongTag(etag));
      form.set("id", expectedId);
      form.set("file", file);
      return createWithId("/attachments", expectedId, { form });
    },
    /** @param {string} boardId @param {string} cardId @param {string} [cursor] */
    async listRelationships(boardId, cardId, cursor) {
      const board = resourceId(boardId);
      const card = resourceId(cardId);
      const reply = await list("/relationships", { board_id: board, card_id: card }, cursor);
      const page = /** @type {{relationships?: unknown}} */ (reply.data);
      if (!Array.isArray(page.relationships) || !page.relationships.every((link) =>
        link !== null && typeof link === "object" && !Array.isArray(link)
        && validRelationship(link, undefined, board)
        && (link.source_card_id === card || link.target_card_id === card))) {
        throw new ApiError("invalid_response", { status: 200 });
      }
      return reply;
    },
    /** @param {string} id @param {string} boardId @param {string} boardEtag
     * @param {string} source @param {string} target @param {RelationshipKind} kind */
    async createRelationship(id, boardId, boardEtag, source, target, kind) {
      const expectedId = resourceId(id);
      const board = resourceId(boardId);
      const reply = await createWithId("/relationships", expectedId, { data: {
        id: expectedId, board_id: board, expected_board_etag: strongTag(boardEtag),
        source_card_id: resourceId(source), target_card_id: resourceId(target), kind,
      } });
      if (!validRelationship(reply.data, expectedId, board)) {
        throw new ApiError("invalid_response", { status: 201, outcomeUnknown: true });
      }
      return reply;
    },
    /** @param {string} id @param {number} revision @param {string} boardEtag @param {RelationshipKind} kind */
    async replaceRelationship(id, revision, boardEtag, kind) {
      const boardTag = strongTag(boardEtag);
      const current = await currentRelationship(id, revision);
      const reply = await request("/relationships/" + resourceId(id), {
        method: "PATCH", etag: current.etag, data: { expected_board_etag: boardTag, kind },
      });
      if (!validRelationship(reply.data, id, /** @type {{board_id: string}} */ (current.data).board_id)) {
        throw new ApiError("invalid_response", { status: 200, outcomeUnknown: true });
      }
      return reply;
    },
    /** @param {string} id @param {number} revision @param {string} boardEtag */
    async removeRelationship(id, revision, boardEtag) {
      const boardTag = strongTag(boardEtag);
      const current = await currentRelationship(id, revision);
      const response = await send("/relationships/" + resourceId(id), {
        method: "DELETE", etag: current.etag, boardEtag: boardTag,
      });
      if (response.status !== 204 || !STRONG_ETAG.test(response.headers.get("X-Board-ETag") ?? "")) {
        await discardBody(response);
        throw new ApiError("invalid_response", { status: response.status, outcomeUnknown: true });
      }
    },
    /** @param {string} id @param {string} boardId @param {string} boardEtag
     * @param {string} cardId @param {string} cardEtag */
    async approveCard(id, boardId, boardEtag, cardId, cardEtag) {
      const expectedId = resourceId(id);
      const board = resourceId(boardId);
      const card = resourceId(cardId);
      const response = await send("/approvals", { method: "POST", data: {
        id: expectedId, board_id: board, expected_board_etag: strongTag(boardEtag),
        cards: [{ card_id: card, etag: strongTag(cardEtag) }],
      } });
      if (response.status !== 201 || response.headers.get("Location") !== API_PREFIX + "/approvals/" + expectedId) {
        await discardBody(response);
        throw new ApiError("invalid_response", { status: response.status, outcomeUnknown: true });
      }
      const data = await responseObject(response, true);
      const tag = response.headers.get("X-Board-ETag");
      if (!STRONG_ETAG.test(tag ?? "") || /** @type {{board_etag?: unknown}} */ (data).board_etag !== tag
          || !matchesApproval(data, expectedId, board, card)) {
        throw new ApiError("invalid_response", { status: 201, outcomeUnknown: true });
      }
    },
    /** @param {string} id @param {string} boardId @param {string} cardId */
    async inspectApproval(id, boardId, cardId) {
      const reply = await request("/approvals/" + resourceId(id));
      if (!STRONG_ETAG.test(reply.etag ?? "") || !matchesApproval(reply.data, id, boardId, cardId)) {
        throw new ApiError("invalid_response");
      }
    },
    /** @param {unknown} boardId @param {unknown} boardEtag
     * @param {{label: string, after_id: string, create_for: string[], connect_for: string[]}} lane */
    async createLane(boardId, boardEtag, lane) {
      const id = resourceId(boardId);
      const result = await request("/lanes", {
        method: "POST", successStatus: 201,
        data: { ...lane, board_id: id, board_etag: strongTag(boardEtag) },
      });
      if (!validLane(result.data, undefined, id)) {
        throw new ApiError("invalid_response", { status: 201, outcomeUnknown: true });
      }
      return result;
    },
    /** @param {unknown} laneId @param {unknown} revision @param {unknown} boardEtag @param {string} label */
    async renameLane(laneId, revision, boardEtag, label) {
      const id = resourceId(laneId);
      const boardTag = strongTag(boardEtag);
      if (!isInteger(revision) || revision < 1) {
        throw new TypeError("Expected a positive safe integer lane revision.");
      }
      const response = await send("/lanes/" + id);
      const currentTag = response.headers.get("ETag");
      if (response.status !== 200 || !STRONG_ETAG.test(currentTag ?? "")) {
        await discardBody(response);
        throw new ApiError("invalid_response", { status: response.status });
      }
      const current = await responseObject(response, false);
      if (!validLane(current, id)) {
        throw new ApiError("invalid_response", { status: 200 });
      }
      if (/** @type {{revision: number}} */ (current).revision !== revision) {
        throw new ApiError("http_error", { status: 412 });
      }
      const result = await request("/lanes/" + id, {
        method: "PATCH", etag: strongTag(currentTag), data: { board_etag: boardTag, label },
      });
      if (!validLane(result.data, id, /** @type {{board_id: string}} */ (current).board_id)) {
        throw new ApiError("invalid_response", { status: 200, outcomeUnknown: true });
      }
      return result;
    },
    /** @param {unknown} cardId @param {unknown} etag @param {unknown} data */
    async patchCard(cardId, etag, data) {
      const id = resourceId(cardId);
      if (data !== null && typeof data === "object" && "owner_id" in data) {
        resourceId(data.owner_id);
      }
      const response = await request("/cards/" + id, { method: "PATCH", etag: strongTag(etag), data });
      const card = /** @type {{ board_id?: unknown }} */ (response.data);
      if (response.etag === null || !isId(card.board_id)
          || !validCard(response.data, card.board_id, id)) {
        throw new ApiError("invalid_response", { status: 200, outcomeUnknown: true });
      }
      return response;
    },
    /**
     * @param {{ id?: string, board_id: string, expected_board_etag: string, title: string,
     *   description?: string, lane_id: string, priority_id?: string,
     *   relationships?: {target_card_id: string, kind: RelationshipKind}[] }} card
     */
    async createCard(card) {
      const boardId = resourceId(card.board_id);
      const id = card.id === undefined ? undefined : resourceId(card.id);
      resourceId(card.lane_id);
      strongTag(card.expected_board_etag);
      const response = await send("/cards", { method: "POST", data: card });
      const etag = response.headers.get("ETag");
      const boardEtag = response.headers.get("X-Board-ETag");
      if (response.status !== 201 || etag === null || !STRONG_ETAG.test(etag)
          || boardEtag === null || !STRONG_ETAG.test(boardEtag)) {
        await discardBody(response);
        throw new ApiError("invalid_response", { status: response.status, outcomeUnknown: true });
      }
      const data = await responseObject(response, true);
      if (!validCard(data, boardId, id)) {
        throw new ApiError("invalid_response", { status: response.status, outcomeUnknown: true });
      }
      return { data, etag, boardEtag };
    },
    /**
     * @param {{ boardId?: string, watch?: string[], signal?: AbortSignal }} [options]
     */
    async *streamEvents({ boardId, watch = [], signal } = {}) {
      const query = new URLSearchParams();
      if (boardId !== undefined) {
        query.set("board_id", resourceId(boardId));
      }
      if (watch.length > 100) {
        throw new TypeError("Expected at most 100 retained board IDs.");
      }
      for (const id of watch) {
        query.append("watch", resourceId(id));
      }

      const response = await send(query.size ? "/events?" + query : "/events", {
        accept: "text/event-stream", signal,
      });
      const mediaType = response.headers.get("Content-Type")?.split(";", 1)[0].trim().toLowerCase();
      if (response.status !== 200 || mediaType !== "text/event-stream" || response.body === null) {
        await discardBody(response);
        throw new ApiError("invalid_response", { status: response.status });
      }
      yield* readEvents(response.body);
    },
    getWorkspace: () => request("/workspace"),
    /** @param {string} [cursor] */
    listBoards: (cursor) => list("/boards", {}, cursor),
    /** @param {unknown} id */
    getBoard: (id) => request(`/boards/${resourceId(id)}`),
    /** @param {unknown} data */
    createBoard: (data) => request("/boards", { method: "POST", data, successStatus: 201 }),
    /**
     * @param {unknown[]} pinnedBoardIds
     * @param {unknown} etag
     */
    setPinnedBoards: (pinnedBoardIds, etag) => request("/workspace", {
      method: "PATCH",
      data: { pinned_board_ids: pinnedBoardIds.map(resourceId) },
      etag: strongTag(etag),
    }),
    /** @param {unknown} boardId @param {string} [cursor] */
    async listLanes(boardId, cursor) {
      const id = resourceId(boardId);
      const result = await list("/lanes", { board_id: id }, cursor);
      const data = /** @type {{lanes?: unknown}} */ (result.data);
      if (!Array.isArray(data.lanes) || !data.lanes.every((lane) =>
        lane !== null && typeof lane === "object" && !Array.isArray(lane) && validLane(lane, undefined, id))) {
        throw new ApiError("invalid_response", { status: 200 });
      }
      return result;
    },
    /** @param {unknown} boardId */
    listPriorities: (boardId) => request(`/priorities?board_id=${resourceId(boardId)}`),
    /** @param {unknown} boardId @param {string} [cursor] */
    listUsers: (boardId, cursor) => list("/users", { board_id: resourceId(boardId) }, cursor),
    /** @param {unknown} policyId */
    getPolicy: (policyId) => request(`/policies/${resourceId(policyId)}`),
    /** @param {unknown} boardId */
    getVerification: (boardId) => request(`/verifications/${resourceId(boardId)}`),
    /** @param {unknown} boardId */
    getSnapshot: (boardId) => request(`/snapshots/${resourceId(boardId)}`),
    /**
     * @param {unknown} boardId
     * @param {{ enabled?: boolean, target_thread_id?: string | null }} notifications
     * @param {unknown} etag
     */
    configureOwnerNotices: (boardId, { enabled, target_thread_id: targetThreadId }, etag) => request(
      `/boards/${resourceId(boardId)}`,
      {
        method: "PATCH",
        data: { notifications: { enabled, target_thread_id: targetThreadId } },
        etag: strongTag(etag),
      },
    ),
    getService: () => request("/service"),
    listBuilds: () => request("/builds"),
    /** @param {unknown} id */
    getBuild: (id) => request(`/builds/${resourceId(id)}`),
  });
}

/** @param {Pick<ReturnType<typeof createBrowserApi>, "getWorkspace" | "listBoards">} api */
export async function readWorkspaceOverview(api) {
  const [workspace, boards] = await Promise.all([api.getWorkspace(), api.listBoards()]);
  return { workspace, boards };
}
