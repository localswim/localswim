import { mountAttention } from "./attention.js";
import { mountCardArchive } from "./archive.js";
import { mountBoardLifecycle } from "./board-lifecycle.js";
import { readBoardPolicy, readBrowserPrincipal } from "./board-policy.js";
import type { BoardPolicyContext, BrowserPrincipal } from "./board-policy.js";
import { createBoardState } from "./board-state.js";
import type { BoardStateView } from "./board-state.js";
import { mountBoardView } from "./board-view.js";
import type { BoardViewData } from "./board-view.js";
import { mountCardAssignment } from "./card-assignment.js";
import { mountCardEditor } from "./card-editor.js";
import { mountCardView } from "./card-view.js";
import type { DrawerTextContext } from "./card-view.js";
import { createCardTextDrafts } from "./drafts.js";
import type { ConfirmedCardText } from "./drafts.js";
import { parseRoute } from "./model.js";
import { mountPublishingSettings } from "./publishing-settings.js";
import { createRecentBoards } from "./recent-boards.js";
import type { RecentBoards } from "./recent-boards.js";
import { mountCardSearch } from "./search.js";
import { mountServiceTools } from "./service-tools.js";
import { createSidebarPins } from "./sidebar-state.js";
import { createViewState, linkedCard, retainActiveLaneScroll } from "./view-state.js";
import { createWorkspaceState } from "./workspace-state.js";
import { mountWorkspaceCreate } from "./workspace-create.js";
import { mountWorkspaceSettings } from "./workspace-settings.js";

type RouteRuntime = {
  cache: RecentBoards;
  navigate(url: string): void;
  workspace?: ReturnType<typeof createWorkspaceState>;
  streamClosed?: Promise<void>;
  writes: Set<string>;
  drafts: ReturnType<typeof createCardTextDrafts>;
};

function mountRoute(root: HTMLElement, runtime: RouteRuntime): () => void {
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
  const workspaceTools = shellElement("#workspace-tools");
  const content = shellElement("#route-content");
  const route = parseRoute(view.location.pathname);
  const lifetime = new AbortController();
  const { signal } = lifetime;
  const selectedBoardId = route.kind === "board" ? route.id : undefined;
  const views = createViewState(view);
  const initialSavedView = selectedBoardId ? views.read(selectedBoardId) : null;
  let restored = initialSavedView ?? (selectedBoardId ? views.load(selectedBoardId) : undefined);
  const linkSelection = linkedCard(view.location.search);
  const entryState: unknown = view.history.state;
  const linkAlreadyResolved =
    typeof entryState === "object" &&
    entryState !== null &&
    "localswimCardLink" in entryState &&
    entryState.localswimCardLink === view.location.search;
  let selection =
    linkAlreadyResolved && initialSavedView
      ? initialSavedView.selection
      : (linkSelection ?? restored?.selection ?? null);
  let pendingViewSave = false;
  let pageActive = true;
  let departureView = "";
  let title = "localswim";
  let totalAttention = 0;
  let board: ReturnType<typeof mountBoardView> | undefined;
  let boardData: BoardViewData | undefined;
  let boardState: ReturnType<typeof createBoardState> | undefined;
  let boardSnapshot: BoardStateView | undefined;
  let principal: BrowserPrincipal | null = null;
  let policy: BoardPolicyContext | null = null;
  let policyVersion = "";
  let drawer: ReturnType<typeof mountCardView> | undefined;
  let drawerText: DrawerTextContext | null = null;
  let confirmedText: ConfirmedCardText | null = null;
  let textBinding: AbortController | undefined;
  let editor: ReturnType<typeof mountCardEditor> | undefined;
  let assignment: ReturnType<typeof mountCardAssignment> | undefined;
  let publishing: ReturnType<typeof mountPublishingSettings> | undefined;
  let serviceTools: ReturnType<typeof mountServiceTools> | undefined;
  let workspaceCreate: ReturnType<typeof mountWorkspaceCreate> | undefined;
  let lifecycle: ReturnType<typeof mountBoardLifecycle> | undefined;
  let search: ReturnType<typeof mountCardSearch> | undefined;
  let archive: ReturnType<typeof mountCardArchive> | undefined;
  const assignmentVersions = new Map<string, { revision: number; etag: string }>();
  let writesBlocked = true;
  let focusedTextConfirmed = false;
  let drawerNeedsConfirmation = false;
  const boardHost = doc.createElement("div");
  const boardTools = doc.createElement("details");
  const boardToolsSummary = doc.createElement("summary");
  boardToolsSummary.textContent = "Board settings";
  boardTools.append(boardToolsSummary);
  const searchHost = doc.createElement("div");
  const missing = doc.createElement("a");
  missing.href = "/";
  missing.textContent = "Back to boards";
  const retry = doc.createElement("button");
  retry.type = "button";
  retry.textContent = "Retry board update";
  retry.hidden = true;
  content.append(retry, boardTools, searchHost, boardHost);
  root.dataset.route = route.kind;
  boardRegion.hidden = route.kind === "workspace";
  workspaceTools.hidden = route.kind !== "workspace";
  if (route.kind === "workspace") {
    mountWorkspaceSettings(workspaceTools, runtime.cache, signal);
  }

  const attention = mountAttention(shellElement("#attention"), {
    selectBoard: (id) => {
      saveBoardView();
      runtime.navigate(`/boards/${id}`);
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
    afterWatch: runtime.streamClosed,
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
    beginWrite: () => beginWrite("workspace"),
    refreshCatalog: workspace.refresh,
    onChange: attention.updatePins,
  });
  runtime.workspace = workspace;
  const { drafts } = runtime;
  void sidebar.refresh();

  const context = { api: workspace.api, signal };
  const { readWithSession } = workspace;
  if (route.kind === "workspace") {
    const createHost = doc.createElement("section");
    const publishHost = doc.createElement("section");
    const serviceHost = doc.createElement("section");
    workspaceTools.append(createHost, publishHost, serviceHost);
    workspaceCreate = mountWorkspaceCreate(createHost, {
      ...context,
      readWithSession,
      canCreate: () => principal?.workspaceAdmin === true,
      onCreated: (id) => runtime.navigate(`/boards/${id}`),
    });
    publishing = mountPublishingSettings(publishHost, {
      ...context,
      readWithSession,
      scope: { kind: "workspace" },
      canPublish: () => principal?.workspaceAdmin === true,
      beginWrite: () => beginWrite("workspace"),
      onSaved: () => {
        void sidebar.refresh();
      },
    });
    serviceTools = mountServiceTools(serviceHost, {
      ...context,
      readWithSession,
      canManage: () => principal?.workspaceAdmin === true,
    });
    void publishing.refresh();
  }
  void workspace
    .readWithSession(() => readBrowserPrincipal(context), signal)
    .then(
      (identity) => {
        if (signal.aborted) return;
        principal = identity;
        workspaceCreate?.update();
        publishing?.updatePermission();
        serviceTools?.update();
        refreshPolicy();
      },
      () => {},
    );

  function beginWrite(key: string, secondKey?: string): (() => void) | null {
    const keys = secondKey && secondKey !== key ? [key, secondKey] : [key];
    if (signal.aborted || keys.some((id) => runtime.writes.has(id))) return null;
    for (const id of keys) runtime.writes.add(id);
    return () => {
      for (const id of keys) runtime.writes.delete(id);
      editor?.update();
      assignment?.update();
      updateArchive();
    };
  }

  function refreshPolicy() {
    const snapshot = boardSnapshot;
    const identity = principal;
    const policyId = snapshot?.data?.board.policy_id;
    if (!selectedBoardId || !identity || !policyId || snapshot?.confirmedRevision === null) return;
    const version = `${policyId}:${snapshot?.confirmedRevision}`;
    if (version === policyVersion) return;
    policyVersion = version;
    policy = null;
    void workspace
      .readWithSession(() => readBoardPolicy(context, identity, selectedBoardId, policyId), signal)
      .then(
        (next) => {
          if (signal.aborted || policyVersion !== version) return;
          policy = next;
          editor?.update();
          assignment?.update();
          publishing?.updatePermission();
          lifecycle?.update();
          updateArchive();
        },
        () => {
          if (policyVersion === version) policyVersion = "";
        },
      );
  }

  function updateTitle() {
    doc.title = totalAttention > 0 ? `(${totalAttention}) ${title}` : title;
  }

  function saveBoardView() {
    pendingViewSave = false;
    if (!pageActive || !selectedBoardId || !board || !restored) return;
    restored = { ...restored, ...board.captureViewport(), selection };
    views.save(selectedBoardId, restored);
  }

  function restoreBoardView() {
    if (!board || !boardData || !restored) return;
    const currentView = restored;
    const current = drawer?.selected ?? null;
    const matches =
      selection !== null &&
      current !== null &&
      ("id" in selection
        ? "id" in current && selection.id === current.id
        : "ticket" in current && selection.ticket === current.ticket);
    if (selection && !matches) {
      drawer?.open(selection, undefined, false);
    } else if (!selection && current) {
      drawer?.close(false);
    }
    restored = currentView;
    board.restoreViewport({
      ...currentView,
      laneScroll: retainActiveLaneScroll(
        currentView.laneScroll,
        boardData.lanes.map(({ id }) => id),
      ),
    });
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

  if (linkAlreadyResolved && initialSavedView && linkSelection && selection !== linkSelection) {
    const linkId = "id" in linkSelection ? linkSelection.id : undefined;
    if (!selection || !("id" in selection) || selection.id !== linkId) consumeCardLink();
  }

  function queueBoardViewSave() {
    if (!selectedBoardId || pendingViewSave) return;
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
    if (!pageActive) return;
    selection = reference;
    if (explicit) {
      consumeCardLink();
    } else if (reference && "id" in reference && linkSelection && view) {
      const current = new URL(view.location.href);
      if (current.searchParams.has("ticket")) {
        current.searchParams.delete("ticket");
        current.searchParams.set("card", reference.id);
      }
      if (current.searchParams.has("card")) {
        const historyState: unknown = view.history.state;
        const previous =
          typeof historyState === "object" && historyState !== null ? historyState : {};
        view.history.replaceState({ ...previous, localswimCardLink: current.search }, "", current);
      }
    }
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
          writable:
            !writesBlocked &&
            focusedTextConfirmed &&
            drawerText?.editable === true &&
            policy?.capabilities.has("edit_card") === true,
        }),
        onChange: () => editor?.update(),
        beginWrite: () => beginWrite(next.cardId),
        onConfirmed(saved) {
          if (confirmedText === null || saved.revision >= confirmedText.revision) {
            confirmedText = saved;
            drawer?.showConfirmedText(saved);
          }
          if (pageActive) {
            boardState?.retry();
            drawer?.refresh();
          }
        },
      },
    );
    editor = mountCardEditor(drawer.editor, controller, () => confirmedText, {
      title: drawer.title,
      description: drawer.description,
    });
  }

  function renderBoard(state: BoardStateView) {
    boardSnapshot = state;
    refreshPolicy();
    const { availability, connection, data, name, readiness, retryAvailable, writesFenced } = state;
    writesBlocked = writesFenced;
    editor?.update();
    assignment?.update();
    lifecycle?.update();
    updateArchive();
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
    if (selectedBoardId) {
      if (
        data &&
        availability === "open" &&
        state.confirmedRevision === data.board.revision &&
        state.confirmedEtag === data.etag
      ) {
        runtime.cache.remember(data);
      } else if (
        availability === "closed" ||
        availability === "unhealthy" ||
        availability === "unavailable" ||
        availability === "missing"
      ) {
        runtime.cache.delete(selectedBoardId);
      }
    }
    if (data === null && board && state.confirmedRevision === null) {
      board.dispose();
      board = undefined;
      boardData = undefined;
      drawer?.close(false);
    }
    if (data !== null && data !== boardData) {
      const previous = boardData;
      boardData = data;
      search?.update(data.lanes, data.board.revision);
      if (board) {
        board.update(data);
      } else {
        boardHost.replaceChildren();
        board = mountBoardView(boardHost, data, {
          onOpenCard: (card, opener) => drawer?.open({ id: card.id }, opener),
          onViewChange: queueBoardViewSave,
        });
        restoreBoardView();
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
    if (state.confirmedRevision === null) {
      setPhase("loading", "Showing previously loaded cards while confirming this board.");
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

  function updateArchive() {
    const card = drawerText;
    archive?.update(
      card && {
        cardId: card.cardId,
        laneId: card.laneId,
        archived: card.archived,
        etag: card.etag,
        writable:
          !writesBlocked &&
          focusedTextConfirmed &&
          !lifecycle?.blocksWrites() &&
          policy?.capabilities.has("archive_card") === true,
        laneActive: boardData?.lanes.some(({ id }) => id === card.laneId) === true,
      },
    );
  }

  function refreshBoardAndDrawer() {
    if (signal.aborted) return;
    search?.suspend();
    boardState?.retry();
    drawer?.refresh();
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
      onTextReadiness(confirmed) {
        focusedTextConfirmed = confirmed;
        editor?.update();
        assignment?.update();
        updateArchive();
      },
      acceptTextVersion(cardId, revision, etag) {
        const saved = drafts.latestConfirmed({ boardId: selectedBoardId, cardId });
        const assigned = assignmentVersions.get(cardId);
        if (
          assigned &&
          (revision < assigned.revision ||
            (revision === assigned.revision && etag !== assigned.etag))
        )
          return false;
        if (
          saved &&
          (revision < saved.revision || (revision === saved.revision && etag !== saved.etag))
        ) {
          return false;
        }
        return (
          drawerText?.cardId !== cardId ||
          confirmedText === null ||
          revision > confirmedText.revision ||
          (revision === confirmedText.revision && etag === confirmedText.etag)
        );
      },
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
    search = mountCardSearch(searchHost, {
      ...context,
      readWithSession,
      boardId: selectedBoardId,
      onOpenCard: (reference, opener) => drawer?.open(reference, opener),
      onViewChange(state) {
        if (!restored) return;
        restored = { ...restored, deeperSearch: state.query, deeperSearchState: state };
        saveBoardView();
      },
    });
    search.restore(restored?.deeperSearchState, restored?.deeperSearch);
    const availabilityHost = doc.createElement("section");
    const publishHost = doc.createElement("section");
    boardTools.append(availabilityHost, publishHost);
    lifecycle = mountBoardLifecycle(availabilityHost, {
      ...context,
      readWithSession,
      boardId: selectedBoardId,
      context: () => ({
        availability:
          boardSnapshot?.availability === "open" || boardSnapshot?.availability === "closed"
            ? boardSnapshot.availability
            : null,
        etag: boardSnapshot?.confirmedEtag ?? null,
        canChange: policy?.canClose === true && boardSnapshot?.readiness === "ready",
      }),
      beginWrite: () => beginWrite(selectedBoardId),
      onChange() {
        boardState?.requireFreshConfirmation();
        void workspace.refresh().then(() => boardState?.retry());
      },
    });
    publishing = mountPublishingSettings(publishHost, {
      ...context,
      readWithSession,
      scope: { kind: "board", boardId: selectedBoardId },
      canPublish: () => policy?.capabilities.has("publish") === true,
      beginWrite: () =>
        !writesBlocked && !lifecycle?.blocksWrites() ? beginWrite(selectedBoardId) : null,
      onSaved: refreshBoardAndDrawer,
    });
    boardTools.addEventListener(
      "toggle",
      () => {
        if (boardTools.open) void publishing?.refresh();
      },
      { signal },
    );
    const assignmentHost = doc.createElement("section");
    const archiveHost = doc.createElement("section");
    drawer.tools.append(assignmentHost, archiveHost);
    archive = mountCardArchive(archiveHost, {
      ...context,
      readWithSession,
      boardId: selectedBoardId,
      beginWrite,
      onRefresh: refreshBoardAndDrawer,
    });
    assignment = mountCardAssignment(assignmentHost, {
      ...context,
      readWithSession,
      boardId: selectedBoardId,
      getBoardData: () => boardData,
      getContext: () =>
        drawerText && {
          ...drawerText,
          writable:
            !writesBlocked &&
            focusedTextConfirmed &&
            drawerText.editable &&
            policy?.capabilities.has("edit_card") === true,
        },
      beginWrite,
      onChange: () => editor?.update(),
      onConfirmed(saved) {
        assignmentVersions.set(saved.cardId, saved);
        boardState?.retry();
        if (drawerText?.cardId === saved.cardId) drawer?.refresh();
      },
    });
    boardState = createBoardState({
      boardId: selectedBoardId,
      signal,
      api: workspace.api,
      readWithSession: workspace.readWithSession,
      onChange: renderBoard,
      onWatchRecovery: workspace.restartWatch,
      retained: runtime.cache.get(selectedBoardId)?.data,
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
    saveBoardView();
    lifetime.abort();
    textBinding?.abort();
    editor?.dispose();
    drawer?.dispose();
    boardState?.dispose();
    board?.dispose();
    attention.dispose();
    runtime.streamClosed = workspace.dispose();
    if (runtime.workspace === workspace) runtime.workspace = undefined;
    content.replaceChildren();
    shellElement("#attention").replaceChildren();
    workspaceTools.replaceChildren();
    boardName.hidden = true;
    boardName.textContent = "";
    delete boardRegion.dataset.revision;
    retry.removeEventListener("click", retryLoad);
    view?.removeEventListener("pagehide", disposeOnExit);
    view?.removeEventListener("pageshow", resumeOnReturn);
  }

  function disposeOnExit(event: PageTransitionEvent) {
    saveBoardView();
    departureView = JSON.stringify(restored);
    pageActive = false;
    if (!event.persisted) {
      dispose();
      return;
    }
    sidebar.suspend();
    search?.suspend();
    publishing?.suspend();
    attention.cancelReorder();
    drawer?.suspendRead();
  }

  function resumeOnReturn(event: PageTransitionEvent) {
    if (!event.persisted) return;
    pageActive = true;
    workspace.requireFreshCatalog();
    boardState?.requireFreshConfirmation();
    focusedTextConfirmed = false;
    editor?.update();
    void sidebar.resume();
    void publishing?.resume();
    drafts.refresh();
    if (selectedBoardId) {
      const latest = views.read(selectedBoardId);
      if (latest && JSON.stringify(latest) !== departureView) {
        consumeCardLink();
        restored = latest;
        selection = latest.selection;
        restoreBoardView();
      }
      drawer?.refreshRestored();
    }
    drawerNeedsConfirmation = true;
    void workspace.refresh().then(() => boardState?.retry());
  }

  view.addEventListener("pagehide", disposeOnExit);
  view.addEventListener("pageshow", resumeOnReturn);
  return dispose;
}

const root = document.querySelector<HTMLElement>("[data-app]");

export function mount(root: HTMLElement): () => void {
  const view = root.ownerDocument.defaultView;
  if (!view) throw new Error("The application shell requires a window.");
  let routeDispose: (() => void) | undefined;
  const runtime: RouteRuntime = {
    cache: createRecentBoards(),
    writes: new Set(),
    navigate(url) {
      const target = new URL(url, view.location.href);
      if (target.href === view.location.href) return;
      routeDispose?.();
      view.history.pushState(null, "", target);
      routeDispose = mountRoute(root, runtime);
    },
    drafts: createCardTextDrafts({
      api: {
        getCard(id, signal) {
          const active = runtime.workspace;
          if (!active) return Promise.reject(new Error("The workspace is unavailable."));
          return active.readWithSession(() => active.api.getCard(id, signal), signal);
        },
        patchCard(id, etag, text) {
          const active = runtime.workspace;
          if (!active) return Promise.reject(new Error("The workspace is unavailable."));
          return active.api.patchCard(id, etag, text);
        },
      },
    }),
  };
  function restoreRoute() {
    routeDispose?.();
    routeDispose = mountRoute(root, runtime);
  }
  const controller = new AbortController();
  const { signal } = controller;
  root.addEventListener(
    "click",
    (event) => {
      if (
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.altKey ||
        event.shiftKey ||
        !(event.target instanceof Element)
      )
        return;
      const link = event.target.closest<HTMLAnchorElement>("#app-bar a, #route-content a");
      if (!link || link.target || link.origin !== view.location.origin) return;
      event.preventDefault();
      runtime.navigate(link.href);
    },
    { signal },
  );
  view.addEventListener("popstate", restoreRoute, { signal });
  restoreRoute();
  return () => {
    controller.abort();
    routeDispose?.();
  };
}

if (root) mount(root);
