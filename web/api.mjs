const API_PREFIX = "/api/2026-11-10";
const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const STRONG_ETAG = /^"[A-Za-z0-9_-]{16,128}"$/;

export class ApiError extends Error {
  constructor(code, { status = null, outcomeUnknown = false } = {}) {
    super(code);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
    this.outcomeUnknown = outcomeUnknown;
  }
}

function resourceId(id) {
  if (typeof id !== "string" || !UUID_V4.test(id)) {
    throw new TypeError("Expected a UUIDv4 resource ID.");
  }
  return id;
}

function strongTag(etag) {
  if (typeof etag !== "string" || !STRONG_ETAG.test(etag)) {
    throw new TypeError("Expected an original strong ETag.");
  }
  return etag;
}

// The embedding page supplies the current browser capability from memory.
export function createBrowserApi({ getCapability, fetchImpl = globalThis.fetch.bind(globalThis) }) {
  if (typeof getCapability !== "function" || typeof fetchImpl !== "function") {
    throw new TypeError("A browser capability adapter and fetch are required.");
  }

  async function request(path, { method = "GET", data, etag, successStatus = 200 } = {}) {
    const body = data === undefined ? undefined : JSON.stringify(data);
    let headers;
    try {
      const capability = await getCapability();
      if (typeof capability !== "string" || !capability || /\s/.test(capability)) {
        throw new Error();
      }
      headers = new Headers({
        Accept: "application/json",
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

    const write = method !== "GET";
    let response;
    try {
      response = await fetchImpl(API_PREFIX + path, {
        method,
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

    if (!response.ok) {
      throw new ApiError("http_error", {
        status: response.status,
        outcomeUnknown: write && response.status >= 500,
      });
    }

    const responseEtag = response.headers.get("ETag");
    if (write && (response.status !== successStatus || !STRONG_ETAG.test(responseEtag ?? ""))) {
      throw new ApiError("invalid_response", { status: response.status, outcomeUnknown: true });
    }

    let result;
    try {
      result = await response.json();
    } catch {
      throw new ApiError("invalid_response", { status: response.status, outcomeUnknown: write });
    }
    if (result === null || typeof result !== "object" || Array.isArray(result)) {
      throw new ApiError("invalid_response", { status: response.status, outcomeUnknown: write });
    }
    return { data: result, etag: responseEtag };
  }

  return Object.freeze({
    getWorkspace: () => request("/workspace"),
    listBoards: () => request("/boards"),
    getBoard: (id) => request(`/boards/${resourceId(id)}`),
    createBoard: (data) => request("/boards", { method: "POST", data, successStatus: 201 }),
    setPinnedBoards: (pinnedBoardIds, etag) => request("/workspace", {
      method: "PATCH",
      data: { pinned_board_ids: pinnedBoardIds.map(resourceId) },
      etag: strongTag(etag),
    }),
    listLanes: (boardId) => request(`/lanes?board_id=${resourceId(boardId)}`),
    listPriorities: (boardId) => request(`/priorities?board_id=${resourceId(boardId)}`),
    listUsers: (boardId) => request(`/users?board_id=${resourceId(boardId)}`),
    getPolicy: (policyId) => request(`/policies/${resourceId(policyId)}`),
    getVerification: (boardId) => request(`/verifications/${resourceId(boardId)}`),
    getSnapshot: (boardId) => request(`/snapshots/${resourceId(boardId)}`),
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
    getBuild: (id) => request(`/builds/${resourceId(id)}`),
  });
}

export async function readWorkspaceOverview(api) {
  const [workspace, boards] = await Promise.all([api.getWorkspace(), api.listBoards()]);
  return { workspace, boards };
}
