import { ApiError, createBrowserApi, openBrowserSession } from "../api.mjs";
import { waitForRetry, watchUpdates } from "../board-updates.mjs";

type Attention = Readonly<{ blocked: number; needsInput: number; readyForReview: number }>;

export type WorkspaceBoard = Readonly<{
  id: string;
  name: string | null;
  revision: number | null;
  availability: "open" | "closed" | "unhealthy" | "unknown";
  attention: Attention | null;
  lastCardEditAt: number | null;
  errorCode: string | null;
}>;

export type WorkspaceView = Readonly<{
  status: "loading" | "ready" | "error";
  boards: readonly WorkspaceBoard[];
  observedAt: number | null;
  totalAttention: number;
}>;

type WorkspaceOptions = Readonly<{
  signal: AbortSignal;
  afterWatch?: Promise<void>;
  board?: NonNullable<Parameters<typeof watchUpdates>[0]["board"]>;
  onChange: (view: WorkspaceView, periodic: boolean) => void;
  onWorkspaceChange?: () => void;
}>;

const CATALOG_TIMEOUT_MS = 15_000;
const CATALOG_RETRY_MS = 250;
const CATALOG_POLL_MS = 15_000;
const BOARD_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const TIMESTAMP = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\+00:00$/;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isCount(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function invalidCatalog() {
  return new ApiError("invalid_response", { status: 200 });
}

function parseAttention(value: unknown): Attention | null {
  if (value === null) return null;
  if (!isRecord(value)) throw invalidCatalog();
  const { blocked, needs_input: needsInput, ready_for_review: readyForReview } = value;
  if (!isCount(blocked) || !isCount(needsInput) || !isCount(readyForReview)) throw invalidCatalog();
  return Object.freeze({ blocked, needsInput, readyForReview });
}

function parseEditTime(value: unknown): number | null {
  if (value === null) return null;
  if (typeof value !== "string" || !TIMESTAMP.test(value)) throw invalidCatalog();
  const time = Date.parse(value.replace(" ", "T"));
  if (
    !Number.isFinite(time) ||
    new Date(time).toISOString().replace("T", " ").replace(".000Z", "+00:00") !== value
  ) {
    throw invalidCatalog();
  }
  return time;
}

function parseBoard(value: unknown): WorkspaceBoard {
  if (!isRecord(value)) throw invalidCatalog();
  const { id, name, revision, availability, attention, last_card_edit_at, error_code } = value;
  if (
    typeof id !== "string" ||
    !BOARD_ID.test(id) ||
    (name !== null && (typeof name !== "string" || name.length === 0)) ||
    (revision !== null && (!isCount(revision) || revision < 1)) ||
    (availability !== "open" &&
      availability !== "closed" &&
      availability !== "unhealthy" &&
      availability !== "unknown") ||
    (error_code !== null && (typeof error_code !== "string" || error_code.length > 80))
  ) {
    throw invalidCatalog();
  }
  return Object.freeze({
    id: id.toLowerCase(),
    name,
    revision,
    availability,
    attention: parseAttention(attention),
    lastCardEditAt: parseEditTime(last_card_edit_at),
    errorCode: error_code,
  });
}

export function createWorkspaceState({
  signal: ownerSignal,
  afterWatch,
  board,
  onChange,
  onWorkspaceChange,
}: WorkspaceOptions) {
  const lifetime = new AbortController();
  const signal = AbortSignal.any([ownerSignal, lifetime.signal]);
  let capability: string | undefined;
  let openingSession: Promise<void> | undefined;
  let watching = afterWatch ?? Promise.resolve();
  let stream = new AbortController();
  signal.addEventListener(
    "abort",
    () => {
      capability = undefined;
    },
    { once: true },
  );
  const api = createBrowserApi({ getCapability: () => capability });

  async function ensureCapability(
    readSignal: AbortSignal,
    rejectedCapability?: string,
  ): Promise<void> {
    readSignal.throwIfAborted();
    if (
      openingSession === undefined &&
      capability !== undefined &&
      capability !== rejectedCapability
    ) {
      return;
    }
    // Reads and the stream share one session open. Each caller can stop waiting independently.
    openingSession ??= openBrowserSession((input, options) =>
      fetch(input, {
        ...options,
        signal: options?.signal ? AbortSignal.any([signal, options.signal]) : signal,
      }),
    )
      .then((token) => {
        signal.throwIfAborted();
        capability = token;
      })
      .finally(() => {
        openingSession = undefined;
      });

    let cancel = () => {};
    const cancelled = new Promise<never>((_resolve, reject) => {
      cancel = () => reject(readSignal.reason);
      readSignal.addEventListener("abort", cancel, { once: true });
    });
    try {
      await Promise.race([openingSession, cancelled]);
    } finally {
      readSignal.removeEventListener("abort", cancel);
    }
  }

  async function readWithSession<T>(
    read: () => Promise<T>,
    requestSignal?: AbortSignal,
  ): Promise<T> {
    const readSignal = requestSignal ? AbortSignal.any([signal, requestSignal]) : signal;
    await ensureCapability(readSignal);
    readSignal.throwIfAborted();
    const original = capability;
    try {
      return await read();
    } catch (error) {
      readSignal.throwIfAborted();
      if (!(error instanceof ApiError) || error.status !== 401) throw error;
    }
    // In-flight reads can still send the old capability while its replacement opens.
    await ensureCapability(readSignal, original);
    readSignal.throwIfAborted();
    return read();
  }

  let view: WorkspaceView = Object.freeze({
    status: "loading",
    boards: Object.freeze([]),
    observedAt: null,
    totalAttention: 0,
  });
  let tail: Promise<unknown> = Promise.resolve();
  let queued: Promise<boolean> | undefined;
  let queuedPeriodic = false;
  let catalogEpoch = 0;

  async function readCatalog(): Promise<WorkspaceView> {
    const timeout = new AbortController();
    const timer = setTimeout(() => timeout.abort(), CATALOG_TIMEOUT_MS);
    const readSignal = AbortSignal.any([signal, timeout.signal]);
    let continuation = false;

    async function collect(): Promise<WorkspaceView> {
      const boards = new Map<string, WorkspaceBoard>();
      const cursors = new Set<string>();
      let totalAttention = 0;
      let cursor: string | undefined;
      do {
        readSignal.throwIfAborted();
        continuation = cursor !== undefined;
        const { data } = await api.listBoards(cursor, readSignal);
        readSignal.throwIfAborted();
        if (!isRecord(data)) throw invalidCatalog();
        const { boards: rows, next_cursor: nextCursor } = data;
        if (!Array.isArray(rows) || rows.length > 200) throw invalidCatalog();
        for (const row of rows as unknown[]) {
          const board = parseBoard(row);
          if (boards.has(board.id)) throw invalidCatalog();
          boards.set(board.id, board);
          if (board.attention !== null) {
            const { blocked, needsInput, readyForReview } = board.attention;
            totalAttention += blocked + needsInput + readyForReview;
          }
        }
        cursor = undefined;
        if (nextCursor !== null) {
          if (
            typeof nextCursor !== "string" ||
            nextCursor.length === 0 ||
            nextCursor.length > 2048 ||
            cursors.has(nextCursor)
          ) {
            throw invalidCatalog();
          }
          cursors.add(nextCursor);
          cursor = nextCursor;
        }
      } while (cursor !== undefined);
      if (!Number.isSafeInteger(totalAttention)) throw invalidCatalog();
      return Object.freeze({
        status: "ready",
        boards: Object.freeze([...boards.values()]),
        observedAt: Date.now(),
        totalAttention,
      });
    }

    try {
      return await readWithSession(collect, readSignal);
    } catch (error) {
      readSignal.throwIfAborted();
      if (!(error instanceof ApiError)) throw error;
      const staleCursor = continuation && error.status === 400;
      const temporary = error.code === "transport_failed" || (error.status ?? 0) >= 500;
      if (!staleCursor && !temporary) throw error;
      if (temporary) await waitForRetry(readSignal, CATALOG_RETRY_MS);
      return await readWithSession(collect, readSignal);
    } finally {
      clearTimeout(timer);
    }
  }

  function refresh(periodic = false): Promise<boolean> {
    if (signal.aborted) return Promise.resolve(false);
    queuedPeriodic ||= periodic;
    // A hint during a paginated read queues one follow-up after the current read settles.
    queued ??= tail
      .then(async () => {
        queued = undefined;
        const checkRecovery = queuedPeriodic;
        queuedPeriodic = false;
        if (signal.aborted) return false;
        const epoch = catalogEpoch;
        let next: WorkspaceView;
        try {
          next = await readCatalog();
        } catch {
          next = Object.freeze({ ...view, status: "error" });
        }
        if (signal.aborted || epoch !== catalogEpoch) return false;
        view = next;
        onChange(view, checkRecovery);
        return next.status === "ready";
      })
      .catch((error: unknown) => {
        globalThis.reportError(error);
        return false;
      });
    tail = queued;
    return queued;
  }

  function restartWatch() {
    stream.abort();
    stream = new AbortController();
    const watchSignal = AbortSignal.any([signal, stream.signal]);
    watching = watching
      .then(() => {
        if (watchSignal.aborted) return;
        return watchUpdates({
          board,
          signal: watchSignal,
          invalidateCatalog: () => {
            void refresh();
          },
          invalidateWorkspace: onWorkspaceChange,
          connect: (boardId, streamSignal, receive) =>
            readWithSession(async () => {
              for await (const event of api.streamEvents({ boardId, signal: streamSignal })) {
                receive(event);
              }
            }, streamSignal),
        });
      })
      .catch((error: unknown) => globalThis.reportError(error));
  }

  queueMicrotask(() => {
    if (signal.aborted) return;
    onChange(view, false);
    void refresh();
    restartWatch();
    void checkDurableHealth().catch((error: unknown) => globalThis.reportError(error));
  });

  async function checkDurableHealth() {
    while (!signal.aborted) {
      await waitForRetry(signal, CATALOG_POLL_MS);
      if (!signal.aborted) await refresh(true);
    }
  }

  return {
    api,
    readWithSession,
    renewSession(requestSignal: AbortSignal) {
      return ensureCapability(AbortSignal.any([signal, requestSignal]), capability);
    },
    refresh,
    requireFreshCatalog() {
      catalogEpoch++;
    },
    restartWatch,
    dispose() {
      lifetime.abort();
      return watching;
    },
  };
}
