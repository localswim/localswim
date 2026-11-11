const CONNECT_TIMEOUT_MS = 15000;
const INITIAL_RETRY_MS = 500;
const MAX_RETRY_MS = 8000;

/** @typedef {import("./api.mjs").StreamEvent} StreamEvent */
/** @typedef {"connecting"|"connected"|"disconnected"} Connection */
/** @typedef {"ready"|"refreshing"|"error"} Readiness */

/**
 * Manual reads and live invalidations share one read and one queued follow-up.
 * @param {{signal: AbortSignal, read: (automatic: boolean) => Promise<boolean>,
 * revision: () => number, canReadAutomatically: () => boolean,
 * report: (state: Readiness) => void}} options
 */
export function createBoardRefresh({ signal, read, revision, canReadAutomatically, report }) {
  let hintedRevision = 0;
  let catchUp = false;
  let tail = Promise.resolve();
  /** @type {"ready"|"error"} */
  let settledState = "ready";
  /** @type {{manual: boolean, result: Promise<void>}|undefined} */
  let queued;

  /** @param {boolean} manual */
  function queue(manual) {
    if (signal.aborted) return Promise.resolve();
    if (queued) {
      queued.manual ||= manual;
      return queued.result;
    }
    const job = { manual, result: tail.catch(() => {}).then(async () => {
      queued = undefined;
      if (signal.aborted) return;
      if (!job.manual && (!canReadAutomatically() || (!catchUp && hintedRevision <= revision()))) return;
      const target = hintedRevision;
      const reconnect = catchUp;
      hintedRevision = 0;
      catchUp = false;
      report("refreshing");
      try {
        const applied = await read(!job.manual);
        if (signal.aborted) return;
        if (applied) {
          settledState = "ready";
          report(settledState);
        } else {
          // A write started during the read. Its own readback may cover this hint.
          hintedRevision = Math.max(hintedRevision, target);
          catchUp ||= reconnect;
          report(settledState);
          resume();
        }
      } catch (error) {
        if (signal.aborted) return;
        settledState = "error";
        report(settledState);
        if (job.manual) throw error;
      }
    }) };
    queued = job;
    tail = job.result;
    return job.result;
  }

  function resume() {
    if (canReadAutomatically() && (catchUp || hintedRevision > revision())) void queue(false);
  }

  return {
    request: () => queue(true),
    resume,
    /** @param {number} next @param {boolean} needsCatchUp */
    invalidate(next, needsCatchUp) {
      hintedRevision = Math.max(hintedRevision, next);
      catchUp ||= needsCatchUp;
      resume();
    },
  };
}

/** @param {AbortSignal} signal @param {number} delay */
function waitToReconnect(signal, delay) {
  return new Promise((resolve) => {
    if (signal.aborted) {
      resolve(undefined);
      return;
    }
    const timer = setTimeout(finish, delay);
    function finish() {
      clearTimeout(timer);
      signal.removeEventListener("abort", finish);
      resolve(undefined);
    }
    signal.addEventListener("abort", finish, { once: true });
  });
}

/**
 * Each connection starts with the current server revision. A replacement
 * connection also requests catch-up when the revision happens to match.
 * @param {{boardId: string, signal: AbortSignal,
 * connect: (signal: AbortSignal, receive: (event: StreamEvent) => void) => Promise<void>,
 * report: (state: Connection) => void,
 * invalidate: (revision: number, needsCatchUp: boolean) => void}} options
 */
export async function watchBoard({ boardId, signal, connect, report, invalidate }) {
  let retryDelay = INITIAL_RETRY_MS;
  let reconnecting = false;
  if (!signal.aborted) report("connecting");
  while (!signal.aborted) {
    const attempt = new AbortController();
    const attemptSignal = AbortSignal.any([signal, attempt.signal]);
    const timer = setTimeout(() => attempt.abort(), CONNECT_TIMEOUT_MS);
    let connected = false;
    try {
      await connect(attemptSignal, (event) => {
        if (attemptSignal.aborted || event.resource !== "board" || event.board_id !== boardId) return;
        const first = !connected;
        if (first) {
          if (event.type !== "snapshot") return;
          connected = true;
          clearTimeout(timer);
          retryDelay = INITIAL_RETRY_MS;
          report("connected");
        }
        invalidate(event.board_revision, first && reconnecting);
      });
    } catch {
      // EOF, invalid frames, and transport failures all require a fresh snapshot.
    } finally {
      clearTimeout(timer);
      attempt.abort();
    }
    if (signal.aborted) return;
    report("disconnected");
    await waitToReconnect(signal, retryDelay);
    retryDelay = Math.min(retryDelay * 2, MAX_RETRY_MS);
    reconnecting = true;
  }
}
