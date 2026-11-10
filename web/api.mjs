const API_PREFIX = "/api/2026-11-10";
const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const STRONG_ETAG = /^"[A-Za-z0-9_-]{16,128}"$/;

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

/** @param {object} data @param {string} etag @param {string} boardId @param {string | undefined} id */
function validCreatedCard(data, etag, boardId, id) {
  const card = /** @type {Record<string, unknown>} */ (data);
  return Object.keys(card).length === 21
    && isId(card.id) && (id === undefined || card.id.toLowerCase() === id.toLowerCase())
    && isId(card.board_id) && card.board_id.toLowerCase() === boardId.toLowerCase()
    && card.etag === etag && isInteger(card.ticket) && card.ticket > 0
    && isTimestamp(card.created_at) && isTimestamp(card.updated_at)
    && typeof card.title === "string" && card.title.length > 0
    && typeof card.description === "string"
    && isId(card.lane_id) && isId(card.priority_id)
    && (card.owner_id === null || isId(card.owner_id))
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
   * @param {{ method?: string, data?: unknown, etag?: string, ifNoneMatch?: string, accept?: string, signal?: AbortSignal }} [options]
   */
  async function send(path, { method = "GET", data, etag, ifNoneMatch, accept = "application/json", signal } = {}) {
    const body = data === undefined ? undefined : JSON.stringify(data);
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

    const write = method !== "GET";
    let response;
    try {
      response = await fetchImpl(API_PREFIX + path, {
        method,
        signal,
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
   * @param {{ method?: string, data?: unknown, etag?: string, successStatus?: number }} [options]
   */
  async function request(path, { method = "GET", data, etag, successStatus = 200 } = {}) {
    const response = await send(path, { method, data, etag });
    const write = method !== "GET";
    const responseEtag = response.headers.get("ETag");
    if (write && (response.status !== successStatus || !STRONG_ETAG.test(responseEtag ?? ""))) {
      await discardBody(response);
      throw new ApiError("invalid_response", { status: response.status, outcomeUnknown: true });
    }
    return { data: await responseObject(response, write), etag: responseEtag };
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
     * @param {{ ticket?: number, limit?: number, cursor?: string }} [filters]
     */
    listCards(boardId, { ticket, limit, cursor } = {}) {
      const query = new URLSearchParams({ board_id: resourceId(boardId) });
      if (ticket !== undefined) {
        if (!Number.isSafeInteger(ticket) || ticket < 1) {
          throw new TypeError("Expected a positive safe integer ticket.");
        }
        query.set("ticket", String(ticket));
      }
      if (limit !== undefined) {
        query.set("limit", String(limit));
      }
      if (cursor !== undefined) {
        query.set("cursor", cursor);
      }
      return request("/cards?" + query);
    },
    /** @param {unknown} cardId */
    getCard: (cardId) => request("/cards/" + resourceId(cardId)),
    /**
     * @param {{ id?: string, board_id: string, expected_board_etag: string, title: string,
     *   description?: string, lane_id: string, priority_id?: string }} card
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
      if (!validCreatedCard(data, etag, boardId, id)) {
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
    listBoards: () => request("/boards"),
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
    /** @param {unknown} boardId */
    listLanes: (boardId) => request(`/lanes?board_id=${resourceId(boardId)}`),
    /** @param {unknown} boardId */
    listPriorities: (boardId) => request(`/priorities?board_id=${resourceId(boardId)}`),
    /** @param {unknown} boardId */
    listUsers: (boardId) => request(`/users?board_id=${resourceId(boardId)}`),
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
