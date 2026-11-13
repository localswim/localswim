import type { Uuid } from "../api-types.js";
import { invalidResponse, readFailure } from "../http.js";
import { readEventStream, type LiveEvent } from "./stream.js";

export type { LiveEvent } from "./stream.js";

const initialRetryMs = 1_000;
const maxRetryMs = 15_000;
const streamIdleMs = 35_000;

export type LiveConnection = "connecting" | "connected" | "disconnected";

export interface LiveRefresh {
  readonly reason: "snapshot" | "change";
  readonly workspace: boolean;
  readonly service: boolean;
  readonly boardId: Uuid | null;
}

interface LiveOptions {
  readonly refresh: (request: LiveRefresh, signal: AbortSignal) => Promise<boolean>;
  readonly onConnection: (connection: LiveConnection) => void;
  readonly onInvalidate?: (event: LiveEvent) => void;
}

export interface LiveController {
  update(boardId: Uuid | null): void;
  retry(): void;
  dispose(): void;
}

export function startLive(options: LiveOptions): LiveController {
  let target: Uuid | null | undefined;
  let connection: LiveConnection = "connecting";
  let active: AbortController | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let retryMs = initialRetryMs;
  let disposed = false;

  function setConnection(next: LiveConnection): void {
    if (next !== connection) {
      connection = next;
      options.onConnection(next);
    }
  }

  function stop(): void {
    clearTimeout(timer);
    const previous = active;
    active = undefined;
    previous?.abort();
  }

  function start(boardId: Uuid | null): void {
    const attempt = new AbortController();
    const signal = attempt.signal;
    active = attempt;
    let idle: ReturnType<typeof setTimeout>;
    let initial = true;
    let reading = false;
    let refreshWorkspace = false;
    let refreshBoard = false;
    const snapshots = new Set<LiveEvent["resource"]>();
    const snapshotCount = boardId === null ? 2 : 3;

    function fail(): void {
      if (active !== attempt) return;
      active = undefined;
      attempt.abort();
      const delay = retryMs;
      retryMs = Math.min(maxRetryMs, retryMs * 2);
      timer = setTimeout(() => {
        start(boardId);
      }, delay);
      setConnection("disconnected");
    }

    function received(): void {
      clearTimeout(idle);
      idle = setTimeout(fail, streamIdleMs);
    }

    async function readChanges(): Promise<void> {
      if (signal.aborted) return;
      const request: LiveRefresh = {
        reason: initial ? "snapshot" : "change",
        workspace: refreshWorkspace,
        service: initial,
        boardId: refreshBoard ? boardId : null,
      };
      refreshWorkspace = false;
      refreshBoard = false;
      const confirmed = await options.refresh(request, signal);
      if (signal.aborted) return;
      reading = false;
      if (!confirmed) {
        fail();
        return;
      }
      initial = false;
      setConnection("connected");
      readPending();
    }

    function readPending(): void {
      if (signal.aborted || reading || (!refreshWorkspace && !refreshBoard)) return;
      reading = true;
      // Frames written together can request a single workspace and board read.
      queueMicrotask(() => {
        void readChanges().catch(fail);
      });
    }

    function onFrame(event: LiveEvent): void {
      if (signal.aborted) return;
      if (event.resource === "board" && event.boardId !== boardId) return invalidResponse();
      received();

      if (event.type === "snapshot") {
        if (snapshots.size === snapshotCount || snapshots.has(event.resource)) {
          return invalidResponse();
        }
        snapshots.add(event.resource);
        if (snapshots.size === snapshotCount) {
          refreshWorkspace = true;
          refreshBoard = boardId !== null;
          readPending();
        }
        return;
      }
      if (snapshots.size !== snapshotCount) return invalidResponse();
      if (event.resource !== "catalog") options.onInvalidate?.(event);
      if (event.resource === "board") refreshBoard = true;
      else refreshWorkspace = true;
      readPending();
    }

    function onHeartbeat(): void {
      if (signal.aborted) return;
      if (snapshots.size !== snapshotCount) return invalidResponse();
      if (!initial) retryMs = initialRetryMs;
      received();
    }

    async function subscribe(): Promise<void> {
      try {
        await readEventStream(boardId, signal, onFrame, onHeartbeat);
      } catch (error) {
        const failure = readFailure(error);
        // A selected board can lose read access before the catalog frame arrives.
        // Give the root an authoritative catalog read so it can replace the target.
        if (
          !signal.aborted &&
          boardId !== null &&
          snapshots.size === 0 &&
          failure.kind === "http" &&
          (failure.problem.code === "not_found" || failure.problem.code === "forbidden")
        ) {
          await options.refresh(
            { reason: "snapshot", workspace: true, service: true, boardId },
            signal,
          );
        }
      } finally {
        clearTimeout(idle);
        fail();
      }
    }

    signal.addEventListener("abort", () => clearTimeout(idle), { once: true });
    received();
    void subscribe().catch(fail);
  }

  function restart(boardId: Uuid | null): void {
    stop();
    retryMs = initialRetryMs;
    start(boardId);
    setConnection("connecting");
  }

  return {
    update(boardId) {
      if (disposed || target === boardId) return;
      target = boardId;
      restart(boardId);
    },
    retry() {
      if (disposed || target === undefined) return;
      restart(target);
    },
    dispose() {
      disposed = true;
      stop();
    },
  };
}
