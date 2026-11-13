import { mountAttention } from "./attention.js";
import { createBoardState } from "./board-state.js";
import type { BoardStateView } from "./board-state.js";
import { mountBoardView } from "./board-view.js";
import type { BoardViewData } from "./board-view.js";
import { mountCardEditor } from "./card-editor.js";
import { mountCardView } from "./card-view.js";
import type { DrawerTextContext } from "./card-view.js";
import { createCardTextDrafts } from "./drafts.js";
import type { ConfirmedCardText } from "./drafts.js";
import { parseRoute } from "./model.js";
import { createSidebarPins } from "./sidebar-state.js";
import { createViewState, linkedCard, retainActiveLaneScroll } from "./view-state.js";
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
  const views = createViewState(view);
  let restored = selectedBoardId ? views.load(selectedBoardId) : undefined;
  const linkSelection = linkedCard(view.location.search);
  let selection = linkSelection ?? restored?.selection ?? null;
  let pendingViewSave = false;
  let title = "localswim";
  let totalAttention = 0;
  let board: ReturnType<typeof mountBoardView> | undefined;
  let boardData: BoardViewData | undefined;
  let boardState: ReturnType<typeof createBoardState> | undefined;
  let drawer: ReturnType<typeof mountCardView> | undefined;
  let drawerText: DrawerTextContext | null = null;
  let confirmedText: ConfirmedCardText | null = null;
  let textBinding: AbortController | undefined;
  let editor: ReturnType<typeof mountCardEditor> | undefined;
  let writesBlocked = true;
  let drawerNeedsConfirmation = false;
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
    selectBoard: (id) => {
      saveBoardView();
      view.location.assign(`/boards/${id}`);
    },
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
    beginReorder: () => sidebar.beginReorder(),
    endReorder: () => sidebar.endReorder(),
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
    onChange(catalog, periodic) {
      attention.update(catalog, selectedBoardId);
      sidebar.updateCatalog(catalog);
      boardState?.observeCatalog(catalog, periodic);
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
  const drafts = createCardTextDrafts({
    api: {
      getCard: (id, readSignal) =>
        workspace.readWithSession(() => workspace.api.getCard(id, readSignal), readSignal),
      patchCard: (id, etag, text) => workspace.api.patchCard(id, etag, text),
    },
  });
  void sidebar.refresh();

  function updateTitle() {
    doc.title = totalAttention > 0 ? `(${totalAttention}) ${title}` : title;
  }

  function saveBoardView() {
    pendingViewSave = false;
    if (!selectedBoardId || !board || !restored) return;
    restored = { ...restored, ...board.captureViewport(), selection };
    views.save(selectedBoardId, restored);
  }

  function consumeCardLink() {
    if (!linkSelection || !view) return;
    const current = new URL(view.location.href);
    if (!current.searchParams.has("ticket") && !current.searchParams.has("card")) return;
    current.searchParams.delete("ticket");
    current.searchParams.delete("card");
    const historyState: unknown = view.history.state;
    view.history.replaceState(historyState, "", current);
  }

  function queueBoardViewSave() {
    if (pendingViewSave) return;
    pendingViewSave = true;
    view?.requestAnimationFrame(() => {
      if (!signal.aborted && pendingViewSave) saveBoardView();
    });
  }

  function setPhase(phase: "loading" | "ready" | "missing" | "error", message = "") {
    root.dataset.phase = phase;
    status.dataset.phase = phase;
    if (status.textContent !== message) status.textContent = message;
  }

  function rememberSelection(reference: typeof selection, explicit: boolean) {
    selection = reference;
    if (explicit) consumeCardLink();
    saveBoardView();
  }

  function showMissing() {
    if (!missing.isConnected) boardHost.replaceChildren(missing);
    setPhase("missing", "Board not found.");
  }

  function updateDrawerEditor(next: DrawerTextContext | null) {
    if (!next || !drawer || !selectedBoardId) {
      textBinding?.abort();
      editor?.dispose();
      textBinding = undefined;
      drawerText = null;
      confirmedText = null;
      editor = undefined;
      return;
    }
    const sameCard = drawerText?.cardId === next.cardId;
    drawerText = next;
    if (!sameCard || confirmedText === null || next.revision >= confirmedText.revision) {
      confirmedText = {
        title: next.title,
        description: next.description,
        etag: next.etag,
        revision: next.revision,
      };
    }
    if (sameCard) {
      editor?.update();
      return;
    }
    textBinding?.abort();
    editor?.dispose();
    textBinding = new AbortController();
    const controller = drafts.bind(
      { boardId: selectedBoardId, cardId: next.cardId },
      {
        signal: AbortSignal.any([signal, textBinding.signal]),
        context: () => ({
          confirmed: confirmedText,
          writable: !writesBlocked && drawerText?.editable === true,
        }),
        onChange: () => editor?.update(),
        onConfirmed(saved) {
          if (confirmedText === null || saved.revision >= confirmedText.revision) {
            confirmedText = saved;
          }
          boardState?.retry();
          drawer?.refresh();
        },
      },
    );
    editor = mountCardEditor(drawer.editor, controller, () => confirmedText, {
      title: drawer.title,
      description: drawer.description,
    });
  }

  function renderBoard(state: BoardStateView) {
    const { availability, connection, data, name, readiness, retryAvailable, writesFenced } = state;
    writesBlocked = writesFenced;
    editor?.update();
    retry.hidden = !retryAvailable;
    root.dataset.boardWritesFenced = String(writesFenced);
    root.dataset.boardConnection = connection;
    if (connection === "disconnected" || connection === "unavailable") {
      drawerNeedsConfirmation = true;
    }
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
          onViewChange: queueBoardViewSave,
        });
        if (restored) {
          board.restoreViewport({
            ...restored,
            laneScroll: retainActiveLaneScroll(
              restored.laneScroll,
              data.lanes.map(({ id }) => id),
            ),
          });
        }
        if (selection) drawer?.open(selection, undefined, false);
        queueBoardViewSave();
      }
      if (previous && previous.board.revision !== data.board.revision) {
        drawer?.refresh();
        if (!writesFenced) drawerNeedsConfirmation = false;
      }
    }
    if (!writesFenced && drawerNeedsConfirmation) {
      drawerNeedsConfirmation = false;
      drawer?.refresh();
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
      const message =
        availability === "unhealthy"
          ? "This board’s saved state is unhealthy."
          : "This board is unavailable.";
      setPhase("error", `${message}${retained}`);
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
      onSelectionChange: rememberSelection,
      onTextContext: updateDrawerEditor,
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
    drawerNeedsConfirmation = true;
    void workspace.refresh().then(() => boardState?.retry());
  };
  retry.addEventListener("click", retryLoad);

  function dispose() {
    if (signal.aborted) return;
    lifetime.abort();
    textBinding?.abort();
    editor?.dispose();
    drawer?.dispose();
    boardState?.dispose();
    board?.dispose();
    attention.dispose();
    workspace.dispose();
    retry.removeEventListener("click", retryLoad);
    view?.removeEventListener("pagehide", disposeOnExit);
    view?.removeEventListener("pageshow", resumeOnReturn);
  }

  function disposeOnExit(event: PageTransitionEvent) {
    saveBoardView();
    if (!event.persisted) {
      dispose();
      return;
    }
    sidebar.suspend();
    attention.cancelReorder();
  }

  function resumeOnReturn(event: PageTransitionEvent) {
    if (!event.persisted) return;
    void sidebar.resume();
    drafts.refresh();
    drawerNeedsConfirmation = true;
    void workspace.refresh().then(() => boardState?.retry());
  }

  view.addEventListener("pagehide", disposeOnExit);
  view.addEventListener("pageshow", resumeOnReturn);
  return dispose;
}

const root = document.querySelector<HTMLElement>("[data-app]");
if (root) mount(root);
