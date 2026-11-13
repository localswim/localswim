import { ApiError } from "../api.mjs";
import { createBoardRefresh } from "../board-updates.mjs";
import type { BoardWatch, Connection } from "../board-updates.mjs";
import { getBoardView } from "./board-view.js";
import type { BoardViewData } from "./board-view.js";
import type { AppContext } from "./model.js";
import type { WorkspaceView } from "./workspace-state.js";

type Availability = "unknown" | "open" | "closed" | "unhealthy" | "unavailable" | "missing";
type Readiness = "loading" | "ready" | "refreshing" | "error";

export type BoardStateView = Readonly<{
  data: BoardViewData | null;
  name: string | null;
  availability: Availability;
  connection: Connection;
  readiness: Readiness;
  confirmedRevision: number | null;
  confirmedEtag: string | null;
  writesFenced: boolean;
  retryAvailable: boolean;
}>;

type BoardStateOptions = AppContext &
  Readonly<{
    boardId: string;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    onChange: (view: BoardStateView) => void;
    onWatchRecovery?: () => void;
    retained?: BoardViewData;
  }>;

export function createBoardState({
  boardId,
  signal: ownerSignal,
  api,
  readWithSession,
  onChange,
  onWatchRecovery,
  retained,
}: BoardStateOptions) {
  const lifetime = new AbortController();
  const signal = AbortSignal.any([ownerSignal, lifetime.signal]);
  let started = false;
  let data: BoardViewData | null = retained ?? null;
  let showingRetained = retained !== undefined;
  let name: string | null = retained?.board.name ?? null;
  let focusedAvailability: Availability = "unknown";
  let connection: Connection = "connecting";
  let readiness: Readiness = "loading";
  let confirmedRevision: number | null = null;
  let confirmedEtag: string | null = null;
  let hintedRevision = 0;
  let catalogAvailability: "open" | "closed" | "unhealthy" | "unknown" = "unknown";
  let catalogReady = false;
  let catalogName: string | null = null;
  let catalogRevision: number | null = null;
  let confirmationEpoch = 0;
  let confirmedEpoch = -1;
  let hadOutage = false;
  let watchRecovery: "idle" | "requested" | "deferred" = "idle";
  let explicitWriteFence = false;

  function publish() {
    if (!started || signal.aborted) return;
    let availability = focusedAvailability;
    const currentCatalog = catalogRevision === null || catalogRevision >= (confirmedRevision ?? 0);
    if (
      catalogAvailability === "unhealthy" ||
      (currentCatalog && catalogAvailability === "closed")
    ) {
      availability = catalogAvailability;
    }
    if (
      showingRetained &&
      (availability === "closed" ||
        availability === "unhealthy" ||
        availability === "missing" ||
        availability === "unavailable")
    ) {
      data = null;
      showingRetained = false;
    }
    onChange({
      data,
      name: name ?? catalogName,
      availability,
      connection,
      readiness,
      confirmedRevision,
      confirmedEtag,
      writesFenced:
        explicitWriteFence ||
        availability !== "open" ||
        !catalogReady ||
        catalogAvailability !== "open" ||
        connection !== "connected" ||
        readiness !== "ready" ||
        confirmedEpoch !== confirmationEpoch ||
        (confirmedRevision ?? 0) < hintedRevision,
      retryAvailable:
        readiness !== "loading" &&
        (readiness === "error" ||
          availability === "closed" ||
          availability === "unhealthy" ||
          availability === "unavailable" ||
          connection === "disconnected" ||
          connection === "unavailable"),
    });
  }

  function confirm(revision: number, etag: string, epoch: number) {
    // A slow response can arrive after a newer revision was confirmed or announced.
    if (revision < Math.max(confirmedRevision ?? 0, hintedRevision)) {
      throw new ApiError("invalid_response");
    }
    confirmedRevision = revision;
    confirmedEtag = etag;
    confirmedEpoch = epoch;
  }

  async function read(): Promise<boolean> {
    const epoch = confirmationEpoch;
    const superseded = () => signal.aborted || epoch !== confirmationEpoch;
    const readAvailability = () =>
      readWithSession(() => api.getBoardAvailability(boardId, signal), signal);
    try {
      if (focusedAvailability === "open" && data !== null) {
        const previous = data;
        const check = await readWithSession(
          () => api.getBoardIfChanged(boardId, previous.etag, signal),
          signal,
        );
        if (superseded()) return false;
        if (check.etag === previous.etag) {
          confirm(previous.board.revision, previous.etag, epoch);
          return true;
        }
      }

      let metadata = await readAvailability();
      if (superseded()) return false;
      if (metadata.data.availability === "open") {
        try {
          const next = await readWithSession(() => getBoardView({ api, signal }, boardId), signal);
          if (superseded()) return false;
          confirm(next.board.revision, next.etag, epoch);
          if (next.etag !== data?.etag) data = next;
          showingRetained = false;
          name = next.board.name;
          focusedAvailability = "open";
          return true;
        } catch (error) {
          if (superseded()) return false;
          if (!(error instanceof ApiError) || error.status !== 503) throw error;
          // The board may have closed between its metadata and contents reads.
          metadata = await readAvailability();
          if (superseded()) return false;
          if (metadata.data.availability === "open") throw error;
        }
      }
      confirm(metadata.data.revision, metadata.etag, epoch);
      name = metadata.data.name;
      focusedAvailability = metadata.data.availability;
      return true;
    } catch (error) {
      if (superseded()) return false;
      if (error instanceof ApiError) {
        switch (error.status) {
          case 404:
            focusedAvailability = confirmedRevision === null ? "missing" : "unavailable";
            return true;
          case 401:
          case 403:
          case 503:
            focusedAvailability = "unavailable";
            break;
          case null:
            break;
        }
      }
      throw error;
    }
  }

  const updates = createBoardRefresh({
    signal,
    read,
    revision: () => confirmedRevision ?? 0,
    canReadAutomatically: () => started,
    report(state) {
      const initialReadPending = readiness === "loading" && focusedAvailability === "unknown";
      if (!initialReadPending || state === "error") readiness = state;
      publish();
      if (
        state === "ready" &&
        connection === "unavailable" &&
        onWatchRecovery &&
        watchRecovery === "idle" &&
        catalogReady &&
        catalogAvailability === "open" &&
        focusedAvailability === "open" &&
        confirmedEpoch === confirmationEpoch &&
        !signal.aborted
      ) {
        watchRecovery = "requested";
        onWatchRecovery();
      }
    },
  });

  const watch: BoardWatch = {
    id: boardId,
    report(state) {
      if (signal.aborted || connection === state) return;
      connection = state;
      if (state === "disconnected" || state === "unavailable") {
        hadOutage = true;
        if (state === "unavailable") {
          confirmationEpoch++;
          if (watchRecovery === "requested") {
            // A replacement watch also refused access. Its catalog-only snapshot can
            // arrive immediately, so wait for a periodic or manual read before restarting.
            watchRecovery = "deferred";
          } else if (watchRecovery === "idle") {
            updates.invalidate(hintedRevision, true);
          }
        }
      } else if (state === "connected") {
        watchRecovery = "idle";
        if (hadOutage) {
          hadOutage = false;
          confirmationEpoch++;
          updates.invalidate(hintedRevision, true);
        }
      }
      publish();
    },
    invalidate(revision, needsCatchUp) {
      if (signal.aborted) return;
      if (revision > hintedRevision) {
        hintedRevision = revision;
        publish();
      }
      updates.invalidate(revision, needsCatchUp);
    },
  };

  function observeCatalog(view: WorkspaceView, periodic = false) {
    if (signal.aborted || view.status === "loading") return;
    const row = view.boards.find(({ id }) => id === boardId);
    const revision = row?.revision ?? null;
    if (view.status === "ready" && revision !== null && revision < (confirmedRevision ?? 0)) {
      return;
    }
    const previousAvailability = catalogAvailability;
    const previouslyReady = catalogReady;
    const previousName = catalogName;
    // An incomplete catalog refresh retains its last durable warning until rechecked.
    catalogReady = view.status === "ready";
    if (view.status === "ready") {
      catalogAvailability = row?.availability ?? "unknown";
      catalogRevision = revision;
      catalogName = row?.name ?? null;
    }
    const previousHint = hintedRevision;
    if (view.status === "ready" && revision !== null) {
      hintedRevision = Math.max(hintedRevision, revision);
    }
    const availabilityChanged = previousAvailability !== catalogAvailability;
    const readinessChanged = previouslyReady !== catalogReady;
    const firstOpenObservation =
      !previouslyReady &&
      previousAvailability === "unknown" &&
      catalogAvailability === "open" &&
      confirmedEpoch < 0;
    const needsConfirmation =
      catalogReady && !firstOpenObservation && (availabilityChanged || readinessChanged);
    if (needsConfirmation) confirmationEpoch++;

    let retryConfirmation = false;
    if (periodic && catalogReady && catalogAvailability === "open") {
      const retryWatch = connection === "unavailable" && watchRecovery === "deferred";
      if (retryWatch) watchRecovery = "idle";
      retryConfirmation =
        retryWatch ||
        readiness === "error" ||
        (readiness === "ready" && confirmedEpoch !== confirmationEpoch);
    }
    if (
      availabilityChanged ||
      readinessChanged ||
      previousName !== catalogName ||
      previousHint !== hintedRevision
    ) {
      publish();
    }
    if (catalogReady) updates.invalidate(hintedRevision, needsConfirmation || retryConfirmation);
  }

  function retry() {
    if (!started || signal.aborted) return;
    if (connection === "unavailable" && watchRecovery === "deferred") watchRecovery = "idle";
    void updates.request().catch(() => {
      // The refresh controller reports the failed read and preserves the last confirmed view.
    });
  }

  return {
    watch,
    start() {
      if (signal.aborted || started) return;
      started = true;
      publish();
      retry();
    },
    retry,
    observeCatalog,
    requireFreshConfirmation() {
      catalogReady = false;
      confirmationEpoch++;
      publish();
    },
    fenceWrites() {
      if (explicitWriteFence) return;
      explicitWriteFence = true;
      publish();
    },
    dispose: () => lifetime.abort(),
  };
}
