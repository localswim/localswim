import { mountAttention } from "./attention.js";
import { createBoardState } from "./board-state.js";
import type { BoardStateView } from "./board-state.js";
import { mountBoardView } from "./board-view.js";
import type { BoardViewData } from "./board-view.js";
import { mountCardView } from "./card-view.js";
import { parseRoute } from "./model.js";
import { createSidebarPins } from "./sidebar-state.js";
import { createWorkspaceState } from "./workspace-state.js";

export function mount(root: HTMLElement): () => void {
  const doc = root.ownerDocument;
  const view = doc.defaultView;
  if (!view) throw new Error("The application shell requires a window.");

  function shellElement(selector: string): HTMLElement {
    const element = root.querySelector<HTMLElement>(selector);
    if (!element) throw new Error(`Missing application shell element ${selector}.`);
    return element;
  }

  const status = shellElement("#status");
  const boardName = shellElement("#board-name");
  const boardRegion = shellElement("#board-region");
  const content = shellElement("#route-content");
  const route = parseRoute(view.location.pathname);
  const lifetime = new AbortController();
  const { signal } = lifetime;
  const selectedBoardId = route.kind === "board" ? route.id : undefined;
  const ticketQuery = new URLSearchParams(view.location.search).get("ticket") ?? "";
  const linkedTicket = /^[1-9]\d*$/.test(ticketQuery) ? Number(ticketQuery) : 0;
  let title = "localswim";
  let totalAttention = 0;
  let board: ReturnType<typeof mountBoardView> | undefined;
  let boardData: BoardViewData | undefined;
  let boardState: ReturnType<typeof createBoardState> | undefined;
  let drawer: ReturnType<typeof mountCardView> | undefined;
  const boardHost = doc.createElement("div");
  const missing = doc.createElement("a");
  missing.href = "/";
  missing.textContent = "Back to boards";
  const retry = doc.createElement("button");
  retry.type = "button";
  retry.textContent = "Retry board update";
  retry.hidden = true;
  content.append(retry, boardHost);
  root.dataset.route = route.kind;
  boardRegion.hidden = route.kind === "workspace";

  const attention = mountAttention(shellElement("#attention"), {
    selectBoard: (id) => view.location.assign(`/boards/${id}`),
    retry: () => {
      void workspace.refresh();
    },
    changePins: (change) => {
      void sidebar.change(change);
    },
    inspectPins: () => {
      void sidebar.inspect();
    },
    retryPins: () => {
      void sidebar.retry();
    },
    cancelPins: () => {
      sidebar.cancel();
    },
  });
  const workspace = createWorkspaceState({
    signal,
    board: selectedBoardId
      ? {
          id: selectedBoardId,
          report: (state) => boardState?.watch.report(state),
          invalidate: (revision, catchUp) => boardState?.watch.invalidate(revision, catchUp),
        }
      : undefined,
    onChange(catalog) {
      attention.update(catalog, selectedBoardId);
      sidebar.updateCatalog(catalog);
      boardState?.observeCatalog(catalog);
      totalAttention = catalog.totalAttention;
      updateTitle();
      if (route.kind === "workspace") {
        setPhase(catalog.status);
      }
    },
    onWorkspaceChange() {
      void sidebar.refresh();
    },
  });
  const sidebar = createSidebarPins({
    signal,
    read: () => workspace.readWithSession(() => workspace.api.getWorkspace()),
    write: (ids, etag) => workspace.api.setPinnedBoards(ids, etag),
    refreshCatalog: workspace.refresh,
    onChange: attention.updatePins,
  });
  void sidebar.refresh();

  function updateTitle() {
    doc.title = totalAttention > 0 ? `(${totalAttention}) ${title}` : title;
  }

  function setPhase(phase: "loading" | "ready" | "missing" | "error", message = "") {
    root.dataset.phase = phase;
    status.dataset.phase = phase;
    if (status.textContent !== message) status.textContent = message;
  }

  function showMissing() {
    if (!missing.isConnected) boardHost.replaceChildren(missing);
    setPhase("missing", "Board not found.");
  }

  function renderBoard(state: BoardStateView) {
    const { availability, connection, data, name, readiness, retryAvailable, writesFenced } = state;
    retry.hidden = !retryAvailable;
    root.dataset.boardWritesFenced = String(writesFenced);
    root.dataset.boardConnection = connection;
    if (state.confirmedRevision !== null) {
      boardRegion.dataset.revision = String(state.confirmedRevision);
    }
    if (name !== null) {
      boardName.textContent = name;
      boardName.hidden = false;
      title = `${name} · localswim`;
      updateTitle();
    }
    if (data !== null && data !== boardData) {
      const previous = boardData;
      boardData = data;
      if (board) {
        board.update(data);
      } else {
        boardHost.replaceChildren();
        board = mountBoardView(boardHost, data, {
          onOpenCard: (card, opener) => drawer?.open({ id: card.id }, opener),
        });
        if (Number.isSafeInteger(linkedTicket) && linkedTicket > 0) {
          drawer?.open({ ticket: linkedTicket });
        }
      }
      if (previous && previous.board.revision !== data.board.revision) drawer?.refresh();
    }
    if (availability === "missing") {
      showMissing();
      return;
    }
    if (availability === "closed") {
      setPhase("ready", "This board is closed.");
      return;
    }
    const retained = data !== null ? " Showing previously loaded cards." : "";
    if (availability === "unhealthy" || availability === "unavailable") {
      setPhase("error", `This board is unavailable.${retained}`);
      return;
    }
    if (readiness === "error") {
      setPhase("error", `Could not update this board.${retained}`);
      return;
    }
    if (data === null) {
      setPhase("loading", "Loading board…");
      return;
    }
    if (connection === "disconnected" || connection === "unavailable") {
      setPhase("ready", `Live updates disconnected.${retained}`);
      return;
    }
    const message =
      readiness === "refreshing"
        ? "Updating board…"
        : connection === "connected"
          ? writesFenced
            ? "Confirming the current board…"
            : "Live updates connected"
          : "Connecting live updates…";
    setPhase("ready", message);
  }

  if (selectedBoardId) {
    drawer = mountCardView(shellElement("#card-drawer-root"), {
      boardId: selectedBoardId,
      api: workspace.api,
      signal,
      readWithSession: workspace.readWithSession,
      getBoardData: () => boardData,
      focusFallback(reference) {
        const id =
          "id" in reference
            ? reference.id
            : boardData?.cards.find((card) => card.ticket === reference.ticket)?.id;
        if (id && board?.focusCard(id)) return;
        if (board) {
          board.focusFilter();
        } else {
          root.querySelector<HTMLElement>("#attention [aria-current=page], #app-bar a")?.focus();
        }
      },
    });
    boardState = createBoardState({
      boardId: selectedBoardId,
      signal,
      api: workspace.api,
      readWithSession: workspace.readWithSession,
      onChange: renderBoard,
      onWatchRecovery: workspace.restartWatch,
    });
    boardState.start();
  } else if (route.kind === "missing") {
    showMissing();
  }
  const retryLoad = () => {
    void workspace.refresh().then(() => boardState?.retry());
  };
  retry.addEventListener("click", retryLoad);

  function dispose() {
    if (signal.aborted) return;
    lifetime.abort();
    drawer?.dispose();
    boardState?.dispose();
    board?.dispose();
    attention.dispose();
    workspace.dispose();
    retry.removeEventListener("click", retryLoad);
    view?.removeEventListener("pagehide", disposeOnExit);
  }

  function disposeOnExit(event: PageTransitionEvent) {
    if (!event.persisted) dispose();
  }

  view.addEventListener("pagehide", disposeOnExit);
  return dispose;
}

const root = document.querySelector<HTMLElement>("[data-app]");
if (root) mount(root);
