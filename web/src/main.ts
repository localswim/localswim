import { MAX_LANE_APPROVAL_CARDS } from "../api.mjs";
import { waitForRetry } from "../board-updates.mjs";
import { mountAttention } from "./attention.js";
import { mountCardArchive } from "./archive.js";
import { mountAttachmentCover, mountAttachments } from "./attachments.js";
import { mountBackup } from "./backup.js";
import { mountBackupPublication } from "./backup-publication.js";
import { mountBatchPriority } from "./batch-priority.js";
import { mountBoardLifecycle } from "./board-lifecycle.js";
import { readBoardPolicy, readBrowserPrincipal } from "./board-policy.js";
import type { BoardPolicyContext, BrowserPrincipal } from "./board-policy.js";
import { createBoardState } from "./board-state.js";
import type { BoardStateView } from "./board-state.js";
import { mountBoardView } from "./board-view.js";
import type { BoardViewData } from "./board-view.js";
import { mountCardApproval } from "./card-approval.js";
import { mountCardAssignment } from "./card-assignment.js";
import { createCardMovement, mountCardMovement } from "./card-movement.js";
import { mountCardEditor } from "./card-editor.js";
import { mountCardView } from "./card-view.js";
import { mountCardComments } from "./comments.js";
import { mountCreateCard, type CreateCardFileDraft } from "./create-card.js";
import type { DrawerTextContext } from "./card-view.js";
import { createCardTextDrafts } from "./drafts.js";
import type { ConfirmedCardText } from "./drafts.js";
import { mountLaneApproval } from "./lane-approval.js";
import { mountLaneSettings } from "./lane-settings.js";
import { parseRoute } from "./model.js";
import { mountPublishingSettings } from "./publishing-settings.js";
import { createRecentBoards } from "./recent-boards.js";
import type { RecentBoards } from "./recent-boards.js";
import { mountRelationships } from "./relationships.js";
import { hasPendingBoardRestore, mountRestore } from "./restore.js";
import { mountCardSearch } from "./search.js";
import { hasPendingServiceActivation, mountServiceTools } from "./service-tools.js";
import { createSidebarPins } from "./sidebar-state.js";
import { mountTicketOrder } from "./ticket-order.js";
import { hasPendingBoardUploads, mountUploads } from "./uploads.js";
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
  stagedFiles: Map<string, CreateCardFileDraft>;
  cardVersions: Map<string, { revision: number; etag: string }>;
  onWriteSettled?: () => void;
  onServicePendingChange?: () => void;
  refreshBoard?: (boardId: string) => void;
  refreshRestoredBoard?: (boardId: string, revision?: number) => void;
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
  let principalRead: Promise<void> | undefined;
  let principalReadFailed = false;
  let policy: BoardPolicyContext | null = null;
  let policyVersion = "";
  let confirmedPolicyVersion = "";
  let policyGeneration = 0;
  let policyReadFailed = false;
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
  let movement: ReturnType<typeof createCardMovement> | undefined;
  let movementPanel: ReturnType<typeof mountCardMovement> | undefined;
  let createCard: ReturnType<typeof mountCreateCard> | undefined;
  let comments: ReturnType<typeof mountCardComments> | undefined;
  let commentsBinding: AbortController | undefined;
  let commentsHost: HTMLElement | undefined;
  let attachments: ReturnType<typeof mountAttachments> | undefined;
  let uploads: ReturnType<typeof mountUploads> | undefined;
  let approval: ReturnType<typeof mountCardApproval> | undefined;
  let laneApproval: ReturnType<typeof mountLaneApproval> | undefined;
  let laneSettings: ReturnType<typeof mountLaneSettings> | undefined;
  let batchPriority: ReturnType<typeof mountBatchPriority> | undefined;
  let ticketOrder: ReturnType<typeof mountTicketOrder> | undefined;
  let backup: ReturnType<typeof mountBackup> | undefined;
  let backupPublication: ReturnType<typeof mountBackupPublication> | undefined;
  let restore: ReturnType<typeof mountRestore> | undefined;
  let relationships: ReturnType<typeof mountRelationships> | undefined;
  let previewsAllowed = false;
  let policyRefreshFrame = 0;
  let laneVisibilityFrame = 0;
  let publishingTag: string | null = null;
  let writesBlocked = true;
  let pinsView: Parameters<typeof attention.updatePins>[0] | undefined;
  let focusedTextConfirmed = false;
  let drawerNeedsConfirmation = false;
  let retainedRestoreSelection: string | undefined;
  let laneApprovalDrawer: string | undefined;
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
  const access = doc.createElement("div");
  const accessStatus = doc.createElement("p");
  const retryAccess = doc.createElement("button");
  accessStatus.setAttribute("role", "status");
  retryAccess.type = "button";
  retryAccess.textContent = "Retry access check";
  access.hidden = true;
  access.append(accessStatus, retryAccess);
  content.append(retry, boardTools, searchHost, boardHost);
  if (route.kind === "workspace") workspaceTools.append(access);
  else content.prepend(access);
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
      if (catalog.status === "ready") {
        for (const entry of catalog.boards) {
          if (entry.availability === "closed" || entry.availability === "unhealthy") {
            runtime.cache.delete(entry.id);
          }
        }
      }
      boardState?.observeCatalog(catalog, periodic);
      totalAttention = catalog.totalAttention;
      updateTitle();
      if (route.kind === "workspace") {
        setPhase(catalog.status);
      }
    },
    onWorkspaceChange() {
      void sidebar.refresh();
      if (!selectedBoardId) void publishing?.refresh();
    },
  });
  const sidebar = createSidebarPins({
    signal,
    read: () => workspace.readWithSession(() => workspace.api.getWorkspace()),
    write: (ids, etag) => workspace.api.setPinnedBoards(ids, etag),
    beginWrite: beginWorkspaceWrite,
    refreshCatalog: workspace.refresh,
    onChange(pins) {
      pinsView = pins;
      updatePinControls();
    },
  });
  runtime.workspace = workspace;
  const { drafts } = runtime;
  void sidebar.refresh();

  const context = { api: workspace.api, signal };
  const { readWithSession } = workspace;
  const serviceOptions = {
    ...context,
    readWithSession,
    canManage: () => principal?.workspaceAdmin === true,
    onRefreshIdentity: refreshPrincipal,
    onPendingChange: () => runtime.onServicePendingChange?.(),
    async onReconnect(requestSignal: AbortSignal) {
      boardState?.requireFreshConfirmation();
      workspace.requireFreshCatalog();
      await workspace.renewSession(requestSignal);
      requestSignal.throwIfAborted();
      await refreshPrincipal(requestSignal);
      requestSignal.throwIfAborted();
      if (principal?.workspaceAdmin !== true) throw new Error("Workspace access is unavailable.");
      workspace.restartWatch();
      void workspace.refresh().then(() => boardState?.retry());
    },
  };
  runtime.onServicePendingChange = updateAccessControls;
  if (route.kind === "workspace") {
    const createHost = doc.createElement("section");
    const publishHost = doc.createElement("section");
    const serviceHost = doc.createElement("section");
    workspaceTools.append(createHost, publishHost, serviceHost);
    workspaceCreate = mountWorkspaceCreate(createHost, {
      ...context,
      readWithSession,
      canCreate: () => principal?.workspaceAdmin === true && !hasPendingServiceActivation(),
      beginWrite: beginWorkspaceWrite,
      beginInspection: () => beginWrite("workspace"),
      onCreated: (id) => runtime.navigate(`/boards/${id}`),
    });
    publishing = mountPublishingSettings(publishHost, {
      ...context,
      readWithSession,
      scope: { kind: "workspace" },
      canPublish: () => principal?.workspaceAdmin === true,
      writeUnavailable: () =>
        hasPendingServiceActivation() ? "Inspect the service activation before saving." : undefined,
      beginWrite: beginWorkspaceWrite,
      beginInspection: () => beginWrite("workspace"),
      onSaved: () => {
        void sidebar.refresh();
      },
    });
    serviceTools = mountServiceTools(serviceHost, serviceOptions);
    void publishing.refresh();
  }
  function refreshPrincipal(requestSignal?: AbortSignal): Promise<void> {
    if (principalRead) return principalRead;
    if (signal.aborted) return Promise.resolve();
    if (principal !== null) {
      principal = null;
      resetPolicy();
      updateAccessControls();
    }
    retryAccess.disabled = true;
    const readSignal = requestSignal ? AbortSignal.any([signal, requestSignal]) : signal;
    principalRead = workspace
      .readWithSession(() => readBrowserPrincipal({ ...context, signal: readSignal }), readSignal)
      .then(
        (identity) => {
          if (signal.aborted) return;
          principalReadFailed = false;
          access.hidden = true;
          accessStatus.textContent = "";
          principal = identity;
          updateAccessControls();
          refreshPolicy();
        },
        () => {
          if (signal.aborted) return;
          principalReadFailed = true;
          access.hidden = false;
          accessStatus.textContent = "Could not verify your access.";
          updateAccessControls();
        },
      )
      .finally(() => {
        principalRead = undefined;
        retryAccess.disabled = false;
      });
    return principalRead;
  }
  retryAccess.addEventListener("click", () => void refreshPrincipal(), { signal });
  void refreshPrincipal();

  function beginWrite(key: string, secondKey?: string): (() => void) | null {
    const keys = secondKey && secondKey !== key ? [key, secondKey] : [key];
    if (signal.aborted || keys.some((id) => runtime.writes.has(id))) return null;
    for (const id of keys) runtime.writes.add(id);
    return () => {
      for (const id of keys) runtime.writes.delete(id);
      runtime.onWriteSettled?.();
    };
  }

  function beginWorkspaceWrite() {
    return pageActive && !hasPendingServiceActivation() ? beginWrite("workspace") : null;
  }

  function updatePinControls() {
    if (!pinsView) return;
    attention.updatePins(
      hasPendingServiceActivation()
        ? { ...pinsView, canChange: false, canRemoveUnavailable: false, canRetry: false }
        : pinsView,
    );
  }

  function updateWriteControls() {
    updateRestore();
    lifecycle?.update();
    const previewChanged = updatePreviewAdmission();
    if (previewChanged && previewsAllowed && boardData) board?.update(boardData);
    editor?.update();
    assignment?.update();
    updateArchive();
    movementPanel?.update();
    comments?.update();
    createCard?.update();
    batchPriority?.update();
    ticketOrder?.update();
    laneApproval?.update();
    laneSettings?.update();
    backup?.update();
    publishing?.updatePermission();
    updateDrawerTools();
  }
  runtime.onWriteSettled = updateWriteControls;
  runtime.refreshBoard = (boardId) => {
    if (boardId === selectedBoardId) refreshBoardAndDrawer();
  };
  runtime.refreshRestoredBoard = (boardId, revision) => {
    if (boardId !== selectedBoardId) return;
    if (revision !== undefined || hasPendingBoardRestore(doc, boardId)) rememberRestoreSelection();
    drawerNeedsConfirmation = true;
    boardState?.requireFreshConfirmation();
    void workspace.refresh().then(() => boardState?.retry());
  };

  function rememberRestoreSelection() {
    if (selection && "id" in selection) retainedRestoreSelection = selection.id;
  }

  function updateRestore() {
    if (selectedBoardId && hasPendingBoardRestore(doc, selectedBoardId)) rememberRestoreSelection();
    restore?.update();
  }

  function cardWorkPending(cardId: string) {
    return (
      runtime.writes.has(cardId) ||
      movement?.blocksCard(cardId) === true ||
      approval?.blocksCard(cardId) === true ||
      laneApproval?.blocksCard(cardId) === true ||
      uploads?.blocksCard(cardId) === true
    );
  }

  function boardWritable() {
    return boardWriteReady() && (!selectedBoardId || !hasPendingBoardRestore(doc, selectedBoardId));
  }

  function boardWriteReady() {
    return (
      pageActive &&
      !writesBlocked &&
      policyReady() &&
      !lifecycle?.blocksWrites() &&
      !hasPendingServiceActivation()
    );
  }

  function canUpload() {
    return (
      selectedBoardId !== undefined &&
      !signal.aborted &&
      boardWritable() &&
      !runtime.writes.has(selectedBoardId) &&
      policy?.capabilities.has("manage_attachments") === true
    );
  }

  async function awaitUploadAdmission(requestSignal: AbortSignal, confirmedCardRevision: number) {
    const confirmation = AbortSignal.any([signal, requestSignal, AbortSignal.timeout(5000)]);
    boardState?.retry();
    while (
      !confirmation.aborted &&
      pageActive &&
      boardSnapshot?.availability === "open" &&
      !lifecycle?.blocksWrites() &&
      !hasPendingServiceActivation()
    ) {
      if (canUpload() && (boardSnapshot?.confirmedRevision ?? 0) >= confirmedCardRevision)
        return true;
      if (policyReady() && policy?.capabilities.has("manage_attachments") !== true) return false;
      await waitForRetry(confirmation, 50);
    }
    return false;
  }

  function beginCardWrite(cardId: string, related?: string): (() => void) | null {
    if (
      !boardWritable() ||
      cardWorkPending(cardId) ||
      (related && cardWorkPending(related)) ||
      (selectedBoardId && runtime.writes.has(selectedBoardId))
    )
      return null;
    return beginWrite(cardId, related);
  }

  function beginBoardCardWrite(cardIds: readonly string[]) {
    if (!selectedBoardId || !boardWritable() || cardIds.some(cardWorkPending)) return null;
    return beginWrite(selectedBoardId);
  }

  function selectedCardId() {
    const selected = drawer?.selected;
    return selected && "id" in selected
      ? selected.id
      : boardData?.cards.find(({ ticket }) => ticket === selected?.ticket)?.id;
  }

  function policyReady() {
    return principal !== null && policy !== null && policyVersion === confirmedPolicyVersion;
  }

  function resetPolicy() {
    policy = null;
    policyVersion = confirmedPolicyVersion = "";
    policyReadFailed = false;
    policyGeneration++;
  }

  function updateAccessControls() {
    updatePinControls();
    workspaceCreate?.update();
    serviceTools?.update();
    updateWriteControls();
    board?.refreshMovement();
    createCard?.update();
    updateLaneCreate();
    view?.cancelAnimationFrame(policyRefreshFrame);
    policyRefreshFrame =
      view?.requestAnimationFrame(() => {
        if (!signal.aborted && board && boardData) board.update(boardData);
      }) ?? 0;
  }

  function updateLaneCreate() {
    const shadow = boardHost.querySelector(".board-view-host")?.shadowRoot;
    shadow?.querySelectorAll<HTMLButtonElement>("[data-lane-create]").forEach((button) => {
      button.disabled = !boardWritable();
    });
    shadow?.querySelectorAll<HTMLButtonElement>("[data-lane-approval]").forEach((button) => {
      const laneId = button.dataset.laneApproval ?? "";
      const tooLarge =
        (boardData?.cards.filter(({ lane_id }) => lane_id === laneId).length ?? 0) >
        MAX_LANE_APPROVAL_CARDS;
      const complete = !tooLarge && confirmedLaneMembership(laneId)?.complete === true;
      const feedback = button.nextElementSibling;
      const reason = tooLarge
        ? `Lane approval supports up to ${MAX_LANE_APPROVAL_CARDS.toLocaleString("en-US")} cards.`
        : complete
          ? ""
          : "Show the full unfiltered lane to approve all cards.";
      button.disabled =
        tooLarge || !complete || !boardWritable() || !laneApprovalDestination(laneId);
      if (feedback instanceof HTMLElement) {
        feedback.hidden = reason === "";
        if (feedback.textContent !== reason) feedback.textContent = reason;
      }
    });
  }

  function onLaneVisibilityChange() {
    if (laneVisibilityFrame !== 0) return;
    laneVisibilityFrame =
      view?.requestAnimationFrame(() => {
        laneVisibilityFrame = 0;
        if (signal.aborted) return;
        laneApproval?.update();
        updateLaneCreate();
      }) ?? 0;
  }
  view.addEventListener("resize", onLaneVisibilityChange, { signal });

  function refreshPolicy() {
    const snapshot = boardSnapshot;
    const identity = principal;
    const policyId = snapshot?.policyId;
    if (!selectedBoardId || !identity || !policyId || snapshot?.confirmedRevision === null) return;
    const version = `${policyId}:${snapshot?.confirmedRevision}`;
    if (version === policyVersion) return;
    policyVersion = version;
    policyReadFailed = false;
    const generation = ++policyGeneration;
    void workspace
      .readWithSession(() => readBoardPolicy(context, identity, selectedBoardId, policyId), signal)
      .then(
        (next) => {
          if (signal.aborted || generation !== policyGeneration) return;
          policy = next;
          confirmedPolicyVersion = version;
          policyReadFailed = false;
          retry.hidden = !boardSnapshot?.retryAvailable;
          updateAccessControls();
        },
        () => {
          if (signal.aborted || generation !== policyGeneration) return;
          policyVersion = "";
          policyReadFailed = true;
          retry.hidden = false;
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
      if (boardSnapshot?.confirmedRevision === null) {
        drawer?.showRetained(selection, undefined, false);
        drawerNeedsConfirmation = true;
      } else {
        drawer?.open(selection, undefined, false);
      }
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
    search?.restore(currentView.deeperSearchState, currentView.deeperSearch);
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
      commentsBinding?.abort();
      commentsHost?.replaceChildren();
      commentsBinding = undefined;
      comments = undefined;
      updateDrawerTools();
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
      comments?.update();
      updateDrawerTools();
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
            boardWritable() &&
            focusedTextConfirmed &&
            drawerText?.editable === true &&
            policy?.capabilities.has("edit_card") === true &&
            !cardWorkPending(next.cardId) &&
            !assignment?.hasUncertainSubmission(next.cardId),
        }),
        onChange: () => editor?.update(),
        beginWrite: () => beginCardWrite(next.cardId),
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
    commentsBinding?.abort();
    commentsHost?.replaceChildren();
    if (commentsHost) {
      commentsBinding = new AbortController();
      comments = mountCardComments(commentsHost, {
        ...context,
        signal: AbortSignal.any([signal, commentsBinding.signal]),
        readWithSession,
        scope: { boardId: selectedBoardId, cardId: next.cardId },
        canInspectSaved: () => pageActive && !hasPendingBoardRestore(doc, selectedBoardId),
        context: () => {
          const card = drawerText;
          const actorId = principal?.principalId;
          const boardEtag = boardSnapshot?.confirmedEtag ?? null;
          const boardRevision = boardSnapshot?.confirmedRevision ?? null;
          if (
            !card ||
            card.cardId !== next.cardId ||
            !actorId ||
            !boardEtag ||
            boardRevision === null
          ) {
            return null;
          }
          return {
            actorId,
            boardEtag,
            boardRevision,
            cardEtag: card.etag,
            revision: card.revision,
            commentCount: card.commentCount,
            writable:
              boardWritable() &&
              focusedTextConfirmed &&
              policy?.capabilities.has("comment") === true &&
              !cardWorkPending(card.cardId),
          };
        },
        authorName(id) {
          const user = boardData?.users.find((entry) => entry.id === id);
          return user
            ? (boardData?.board.actor_presentation?.[user.kind]?.label ?? user.label)
            : "Unknown author";
        },
        authorKind: (id) => boardData?.users.find((user) => user.id === id)?.kind,
        ticketLink: drawer.ticketLink,
        beginWrite: beginCardWrite,
        onConfirmed: refreshBoardAndDrawer,
        onStale: refreshBoardAndDrawer,
      });
    }
    updateDrawerTools();
  }

  function renderBoard(state: BoardStateView) {
    boardSnapshot = state;
    if (principal === null && !principalReadFailed && state.connection === "connected") {
      void refreshPrincipal();
    }
    refreshPolicy();
    const { availability, connection, data, name, readiness, retryAvailable, writesFenced } = state;
    if (!writesBlocked && writesFenced) search?.suspend();
    writesBlocked = writesFenced;
    updateRestore();
    lifecycle?.update();
    const previewChanged = updatePreviewAdmission();
    editor?.update();
    assignment?.update();
    publishing?.updatePermission();
    updateArchive();
    movementPanel?.update();
    board?.refreshMovement();
    createCard?.update();
    comments?.update();
    retry.hidden = !retryAvailable && !policyReadFailed;
    root.dataset.boardWritesFenced = String(writesFenced);
    root.dataset.boardConnection = connection;
    if (connection === "disconnected" || connection === "unavailable") {
      drawerNeedsConfirmation = true;
      publishingTag = null;
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
    if (data === null && board) {
      if (availability === "closed") {
        saveBoardView();
        drawerNeedsConfirmation = true;
        drawer?.hideForBoardClose();
      } else {
        drawer?.close(false);
      }
      board.dispose();
      board = undefined;
      boardData = undefined;
    }
    if (data !== null && data !== boardData) {
      const previous = boardData;
      boardData = data;
      if (board) {
        board.update(data);
      } else {
        boardHost.replaceChildren();
        board = mountBoardView(boardHost, data, {
          onOpenCard: (card, opener) => {
            retainedRestoreSelection = undefined;
            if (boardSnapshot?.confirmedRevision === null) {
              drawer?.showRetained({ id: card.id }, opener);
              drawerNeedsConfirmation = true;
            } else {
              drawer?.open({ id: card.id }, opener);
            }
          },
          onViewChange: queueBoardViewSave,
          onMembershipChange() {
            ticketOrder?.update();
            laneApproval?.update();
            updateLaneCreate();
          },
          movement,
          mountCover(host, card, coverSignal) {
            if (!previewsAllowed || !selectedBoardId) return;
            mountAttachmentCover(host, {
              api: workspace.api,
              signal: AbortSignal.any([signal, coverSignal]),
              boardId: selectedBoardId,
              cardId: card.id,
              coverAttachmentId: card.cover_attachment_id,
              readWithSession,
            });
          },
          renderLaneActions(lane, host) {
            if (
              policy?.createLaneIds.has(lane.id) === true &&
              policy.capabilities.has("create_card")
            ) {
              const add = doc.createElement("button");
              add.type = "button";
              add.textContent = "New card";
              add.setAttribute("aria-label", `New card in ${lane.label}`);
              add.disabled = !boardWritable();
              add.dataset.laneCreate = lane.id;
              add.addEventListener("click", () => createCard?.open(lane.id, add), { signal });
              host.append(add);
            }
            if (
              lane.reorder_enabled &&
              (boardData?.cards.filter(({ lane_id }) => lane_id === lane.id).length ?? 0) >= 2
            ) {
              const order = doc.createElement("button");
              order.type = "button";
              order.textContent = "Ticket order";
              order.setAttribute("aria-label", `Ticket order for ${lane.label}`);
              order.addEventListener("click", () => ticketOrder?.open(lane.id, order), { signal });
              host.append(order);
            }
            if (
              laneApprovalDestination(lane.id) &&
              boardData?.cards.some(({ lane_id }) => lane_id === lane.id)
            ) {
              const bulk = doc.createElement("button");
              bulk.type = "button";
              bulk.dataset.laneApproval = lane.id;
              bulk.textContent = "Approve all in lane";
              bulk.setAttribute("aria-label", `Approve all in ${lane.label}`);
              bulk.addEventListener("click", () => laneApproval?.open(lane.id, bulk), { signal });
              const feedback = doc.createElement("p");
              feedback.id = `lane-approval-feedback-${lane.id}`;
              feedback.className = "board-view-lane-approval-feedback";
              feedback.hidden = true;
              bulk.setAttribute("aria-describedby", feedback.id);
              host.append(bulk, feedback);
            }
          },
        });
        restoreBoardView();
      }
      if (previous && previous.board.revision !== data.board.revision) {
        drawer?.refresh();
        if (!writesFenced) drawerNeedsConfirmation = false;
      }
    } else if (previewChanged && board && boardData) {
      board.update(boardData);
    }
    updateLaneCreate();
    batchPriority?.update();
    ticketOrder?.update();
    laneApproval?.update();
    laneSettings?.update();
    backup?.update();
    updateRestore();
    if (!writesFenced && data && state.confirmedEtag === data.etag) {
      search?.update(data.lanes, data.board.revision);
      if (boardTools.open && publishingTag !== state.confirmedEtag) {
        publishingTag = state.confirmedEtag;
        void publishing?.refresh();
      }
    }
    updateDrawerTools();
    if (!writesFenced && drawerNeedsConfirmation) {
      drawerNeedsConfirmation = false;
      if (selection && drawer?.selected === null) drawer.open(selection, undefined, false);
      else drawer?.refresh();
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

  function updatePreviewAdmission() {
    const next =
      pageActive &&
      !writesBlocked &&
      !lifecycle?.blocksWrites() &&
      !hasPendingServiceActivation() &&
      (!selectedBoardId || !hasPendingBoardRestore(doc, selectedBoardId));
    if (previewsAllowed === next) return false;
    previewsAllowed = next;
    if (!next) board?.revokeCovers();
    return true;
  }

  function updateArchive() {
    const card = drawerText;
    archive?.update(
      card && {
        cardId: card.cardId,
        revision: card.revision,
        laneId: card.laneId,
        archived: card.archived,
        etag: card.etag,
        writable:
          boardWritable() &&
          focusedTextConfirmed &&
          policy?.capabilities.has("archive_card") === true &&
          !cardWorkPending(card.cardId),
        laneActive: boardData?.lanes.some(({ id }) => id === card.laneId) === true,
      },
    );
  }

  function updateDrawerTools() {
    const card = drawerText;
    const boardEtag = boardSnapshot?.confirmedEtag;
    const writable = boardWritable() && focusedTextConfirmed;
    const admitted =
      pageActive &&
      boardSnapshot?.availability === "open" &&
      boardSnapshot.confirmedRevision !== null &&
      boardSnapshot.readiness !== "error" &&
      boardSnapshot.connection === "connected";
    attachments?.show(
      card && admitted
        ? {
            cardId: card.cardId,
            etag: card.etag,
            attachmentCount: card.attachmentCount,
            coverAttachmentId: card.coverAttachmentId,
            readable: previewsAllowed && focusedTextConfirmed,
            editable:
              writable &&
              policy?.capabilities.has("manage_attachments") === true &&
              !cardWorkPending(card.cardId),
          }
        : null,
    );
    uploads?.show(
      card && admitted
        ? {
            cardId: card.cardId,
            etag: card.etag,
            readable: previewsAllowed && focusedTextConfirmed,
            editable: writable && !card.archived && canUpload(),
          }
        : null,
    );
    approval?.update();
    relationships?.update(
      card && boardEtag && boardData
        ? {
            cardId: card.cardId,
            archived: card.archived,
            boardEtag,
            canManage: policy?.capabilities.has("manage_relationships") === true,
            cards: boardData.cards,
          }
        : null,
      writable && card !== null && !cardWorkPending(card.cardId),
    );
  }

  function refreshBoardAndDrawer() {
    if (signal.aborted) return;
    search?.suspend();
    boardState?.retry();
    drawer?.refresh();
  }

  function closeApprovedDrawer(cardIds: readonly string[]) {
    const selectedId = selectedCardId();
    if (!drawer || !selectedId || !cardIds.includes(selectedId)) return;
    const focus = doc.activeElement;
    drawer.close(focus === doc.body || drawer.panel.contains(focus), true);
  }

  function laneApprovalDestination(laneId: string) {
    const source = boardData?.lanes.find(({ id }) => id === laneId);
    const completed = boardData?.lanes.find(({ role }) => role === "completed");
    if (
      source?.role !== "ready_for_review" ||
      !source.approval_enabled ||
      !completed ||
      !policyReady() ||
      !policy?.capabilities.has("approve") ||
      !policy.moveLaneIds.get(laneId)?.has(completed.id)
    )
      return undefined;
    return completed.id;
  }

  function confirmedLaneMembership(laneId: string) {
    const lane = boardData?.lanes.find(({ id }) => id === laneId);
    const membership = board?.laneMembership(laneId);
    const boardEtag = boardSnapshot?.confirmedEtag;
    const boardRevision = boardSnapshot?.confirmedRevision;
    if (
      !lane ||
      !membership ||
      !boardEtag ||
      !boardRevision ||
      membership.revision !== boardRevision
    )
      return undefined;
    const fullLane = boardData?.cards.filter(({ lane_id }) => lane_id === laneId) ?? [];
    const visibleIds = new Set(membership.cards.map(({ id }) => id));
    return {
      lane,
      boardEtag,
      boardRevision,
      cards: membership.cards,
      complete:
        membership.complete &&
        fullLane.length === visibleIds.size &&
        fullLane.every(({ id }) => visibleIds.has(id)),
    };
  }

  if (selectedBoardId) {
    drawer = mountCardView(shellElement("#card-drawer-root"), {
      boardId: selectedBoardId,
      api: workspace.api,
      signal,
      readWithSession: workspace.readWithSession,
      getBoardData: () => boardData,
      retainedCard(reference) {
        return boardData?.cards.find((card) =>
          "id" in reference ? card.id === reference.id : card.ticket === reference.ticket,
        );
      },
      retainMissing: (reference) => "id" in reference && reference.id === retainedRestoreSelection,
      retainedDraft: (reference) =>
        "id" in reference
          ? drafts.retainedText({ boardId: selectedBoardId, cardId: reference.id })
          : undefined,
      onSelectionChange: rememberSelection,
      onTextContext: updateDrawerEditor,
      onTextReadiness(confirmed) {
        focusedTextConfirmed = confirmed;
        editor?.update();
        assignment?.update();
        updateArchive();
        movementPanel?.update();
        comments?.update();
        updateDrawerTools();
      },
      acceptTextVersion(cardId, revision, etag) {
        const saved = drafts.latestConfirmed({ boardId: selectedBoardId, cardId });
        const assigned = runtime.cardVersions.get(cardId);
        const moved = movement?.latestConfirmed(cardId);
        if (
          moved &&
          (revision < moved.revision || (revision === moved.revision && etag !== moved.etag))
        )
          return false;
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
    const priorityHost = doc.createElement("section");
    priorityHost.className = "batch-priority";
    const backupHost = doc.createElement("section");
    const publicationHost = doc.createElement("section");
    const restoreHost = doc.createElement("section");
    restoreHost.className = "board-restore";
    const recoveryHost = doc.createElement("section");
    const laneSettingsHost = doc.createElement("section");
    const orderHost = doc.createElement("div");
    const laneApprovalHost = doc.createElement("div");
    boardTools.append(
      availabilityHost,
      publishHost,
      priorityHost,
      backupHost,
      publicationHost,
      restoreHost,
      laneSettingsHost,
    );
    content.prepend(recoveryHost);
    content.append(orderHost, laneApprovalHost);
    serviceTools = mountServiceTools(recoveryHost, { ...serviceOptions, recoveryOnly: true });
    batchPriority = mountBatchPriority(priorityHost, {
      ...context,
      boardId: selectedBoardId,
      readWithSession,
      getContext: () => ({
        board: boardData ?? null,
        etag: boardSnapshot?.confirmedEtag ?? null,
        writable: boardWritable() && policy?.capabilities.has("set_priority") === true,
      }),
      beginWrite: (cardIds, affectedCardIds) =>
        Array.from(runtime.writes).some((key) => key !== "workspace") ||
        hasPendingBoardUploads(selectedBoardId) ||
        movement?.status().pending
          ? null
          : beginBoardCardWrite(affectedCardIds ?? cardIds),
      beginInspection: () => beginWrite(selectedBoardId),
      onRefresh: () => runtime.refreshBoard?.(selectedBoardId),
    });
    ticketOrder = mountTicketOrder(orderHost, {
      ...context,
      boardId: selectedBoardId,
      readWithSession,
      getContext(laneId) {
        const membership = confirmedLaneMembership(laneId);
        if (!membership?.lane.reorder_enabled) return null;
        return {
          laneLabel: membership.lane.label,
          boardEtag: membership.boardEtag,
          boardRevision: membership.boardRevision,
          cards: membership.cards,
          complete: membership.complete,
          writable: boardWritable() && policy?.capabilities.has("reorder_tickets") === true,
        };
      },
      beginWrite: beginBoardCardWrite,
      beginInspection: () => beginWrite(selectedBoardId),
      onRefresh: refreshBoardAndDrawer,
      focusFallback: () => board?.focusFilter(),
    });
    laneApproval = mountLaneApproval(laneApprovalHost, {
      ...context,
      boardId: selectedBoardId,
      readWithSession,
      actorId: () => principal?.principalId,
      isPageActive: () => pageActive,
      getContext(laneId) {
        const membership = confirmedLaneMembership(laneId);
        const completedLaneId = laneApprovalDestination(laneId);
        if (!membership || !completedLaneId) return null;
        return {
          laneLabel: membership.lane.label,
          boardEtag: membership.boardEtag,
          completedLaneId,
          bulkComment: membership.lane.bulk_approval_comment ?? "",
          cards: membership.cards,
          complete: membership.complete,
          writable: boardWritable(),
        };
      },
      beginWrite: (cardIds) => {
        const release = beginBoardCardWrite(cardIds);
        if (release) laneApprovalDrawer = selectedCardId();
        return release;
      },
      beginInspection: () => beginWrite(selectedBoardId),
      onConfirmed(cardIds) {
        if (!pageActive || signal.aborted) return;
        if (laneApprovalDrawer === selectedCardId()) closeApprovedDrawer(cardIds);
        laneApprovalDrawer = undefined;
        refreshBoardAndDrawer();
      },
      onRefresh: () => runtime.refreshBoard?.(selectedBoardId),
      focusFallback: () => board?.focusFilter(),
    });
    backup = mountBackup(backupHost, {
      ...context,
      boardId: selectedBoardId,
      readWithSession,
      canRead: () => pageActive && boardSnapshot?.availability === "open",
      captureContext: () => ({
        etag: boardSnapshot?.confirmedEtag ?? null,
        revision: boardSnapshot?.confirmedRevision ?? null,
        allowed: boardWritable() && policy?.capabilities.has("backup") === true,
      }),
      onChange: () => backupPublication?.update(),
    });
    backupPublication = mountBackupPublication(publicationHost, {
      ...context,
      boardId: selectedBoardId,
      readWithSession,
      canRead: () =>
        pageActive &&
        !!boardSnapshot?.confirmedEtag &&
        (boardSnapshot.availability === "open" || boardSnapshot.availability === "closed"),
      canPublish: () => boardWritable() && policy?.capabilities.has("publish") === true,
      capturedBackup: backup.capturedBackup,
    });
    if (hasPendingBoardRestore(doc, selectedBoardId)) rememberRestoreSelection();
    restore = mountRestore(restoreHost, {
      ...context,
      boardId: selectedBoardId,
      readWithSession,
      getContext: () => ({
        name: boardSnapshot?.name ?? null,
        revision:
          boardSnapshot?.availability === "closed" ||
          boardData?.etag === boardSnapshot?.confirmedEtag
            ? (boardSnapshot?.confirmedRevision ?? null)
            : null,
        allowed: boardWriteReady() && policy?.capabilities.has("restore") === true,
        readable:
          pageActive &&
          boardSnapshot?.confirmedRevision !== null &&
          (boardSnapshot?.availability === "open" ||
            boardSnapshot?.availability === "closed" ||
            boardSnapshot?.availability === "unhealthy"),
      }),
      beginWrite: () =>
        boardWriteReady() &&
        !Array.from(runtime.writes).some((key) => key !== "workspace") &&
        !hasPendingBoardUploads(selectedBoardId)
          ? beginWrite(selectedBoardId)
          : null,
      onStateChange: () => runtime.onWriteSettled?.(),
      onReconciled: () => comments?.refreshSaved(),
      refresh: (boardId, revision) => runtime.refreshRestoredBoard?.(boardId, revision),
    });
    const canInspectLaneSettings = () =>
      pageActive && !hasPendingServiceActivation() && !hasPendingBoardRestore(doc, selectedBoardId);
    laneSettings = mountLaneSettings(laneSettingsHost, {
      ...context,
      boardId: selectedBoardId,
      readWithSession,
      getContext: () => ({
        data: boardData?.etag === boardSnapshot?.confirmedEtag ? (boardData ?? null) : null,
        actorId: policy?.principalId,
        allowed: policyReady() && policy?.capabilities.has("manage_workflow") === true,
        writable: boardWritable(),
      }),
      beginWrite: () => (boardWritable() ? beginWrite(selectedBoardId) : null),
      canInspectSaved: canInspectLaneSettings,
      beginInspection: () => (canInspectLaneSettings() ? beginWrite(selectedBoardId) : null),
      onChange: () => runtime.refreshBoard?.(selectedBoardId),
    });
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
        canChange:
          policyReady() &&
          policy?.canClose === true &&
          !hasPendingServiceActivation() &&
          !hasPendingBoardRestore(doc, selectedBoardId) &&
          boardSnapshot?.lifecycleReady === true,
      }),
      beginWrite: () => beginWrite(selectedBoardId),
      beginInspection: () => beginWrite(selectedBoardId),
      onBusyChange() {
        if (!signal.aborted) updateAccessControls();
      },
      onChange() {
        boardState?.requireFreshConfirmation();
        void workspace.refresh().then(() => boardState?.retry());
      },
    });
    publishing = mountPublishingSettings(publishHost, {
      ...context,
      readWithSession,
      scope: { kind: "board", boardId: selectedBoardId },
      canPublish: () => policyReady() && policy?.capabilities.has("publish") === true,
      writeUnavailable: () => {
        if (boardSnapshot?.availability === "closed") {
          return "Reopen the board before saving publishing settings.";
        }
        if (lifecycle?.blocksWrites())
          return "Resolve the board availability change before saving.";
        if (hasPendingServiceActivation())
          return "Inspect the service activation before saving publishing settings.";
        if (hasPendingBoardRestore(doc, selectedBoardId))
          return "Inspect the native restore before saving publishing settings.";
        if (writesBlocked)
          return "Reconnect and confirm the board before saving publishing settings.";
        return undefined;
      },
      beginWrite: () => (boardWritable() ? beginWrite(selectedBoardId) : null),
      beginInspection: () => beginWrite(selectedBoardId),
      onSaved: refreshBoardAndDrawer,
    });
    boardTools.addEventListener(
      "toggle",
      () => {
        if (boardTools.open) {
          publishingTag = boardSnapshot?.confirmedEtag ?? null;
          void publishing?.refresh();
        }
      },
      { signal },
    );
    const assignmentHost = doc.createElement("section");
    const archiveHost = doc.createElement("section");
    const movementHost = doc.createElement("section");
    const attachmentsHost = doc.createElement("section");
    const uploadsHost = doc.createElement("section");
    const approvalHost = doc.createElement("section");
    const relationshipsHost = doc.createElement("section");
    commentsHost = doc.createElement("section");
    drawer.tools.append(
      assignmentHost,
      movementHost,
      archiveHost,
      attachmentsHost,
      uploadsHost,
      relationshipsHost,
      commentsHost,
      approvalHost,
    );
    attachments = mountAttachments(attachmentsHost, {
      ...context,
      readWithSession,
      boardId: selectedBoardId,
      getBoardEtag: () => boardSnapshot?.confirmedEtag,
      beginWrite: beginCardWrite,
      beginInspect: beginWrite,
      onChange: refreshBoardAndDrawer,
    });
    uploads = mountUploads(uploadsHost, {
      ...context,
      boardId: selectedBoardId,
      pasteTarget: drawer.panel,
      readWithSession,
      canUpload,
      awaitUploadAdmission,
      beginWrite: (cardId) =>
        boardWritable() &&
        canUpload() &&
        !movement?.blocksCard(cardId) &&
        !approval?.blocksCard(cardId) &&
        !laneApproval?.blocksCard(cardId)
          ? beginWrite(cardId)
          : null,
      beginInspect: beginWrite,
      onChange: () => runtime.refreshBoard?.(selectedBoardId),
    });
    approval = mountCardApproval(approvalHost, {
      ...context,
      boardId: selectedBoardId,
      readWithSession,
      actorId: () => principal?.principalId,
      isPageActive: () => pageActive,
      context: () => {
        const card = drawerText;
        const actorId = principal?.principalId;
        const boardEtag = boardSnapshot?.confirmedEtag;
        if (!card || !actorId || !boardEtag) return null;
        const lane = boardData?.lanes.find(({ id }) => id === card.laneId);
        const completed = boardData?.lanes.find(({ role }) => role === "completed");
        return {
          cardId: card.cardId,
          cardEtag: card.etag,
          boardEtag,
          actorId,
          eligible:
            !card.archived &&
            lane?.role === "ready_for_review" &&
            !!completed &&
            policy?.capabilities.has("approve") === true &&
            policy.moveLaneIds.get(lane.id)?.has(completed.id) === true &&
            boardSnapshot?.availability === "open",
          writable:
            boardWritable() &&
            focusedTextConfirmed &&
            !uploads?.blocksCard(card.cardId) &&
            !movement?.blocksCard(card.cardId) &&
            !laneApproval?.blocksCard(card.cardId),
        };
      },
      beginWrite: (cardId) =>
        boardWritable() &&
        !uploads?.blocksCard(cardId) &&
        !movement?.blocksCard(cardId) &&
        !laneApproval?.blocksCard(cardId)
          ? beginWrite(selectedBoardId, cardId)
          : null,
      beginInspection: beginWrite,
      onConfirmed(cardId) {
        if (!pageActive || signal.aborted) return;
        closeApprovedDrawer([cardId]);
        refreshBoardAndDrawer();
      },
      onRefresh: refreshBoardAndDrawer,
    });
    relationships = mountRelationships(relationshipsHost, {
      ...context,
      readWithSession,
      boardId: selectedBoardId,
      ticketLink: drawer.ticketLink,
      beginWrite: beginCardWrite,
      beginInspection: beginWrite,
      refresh: refreshBoardAndDrawer,
    });
    movement = createCardMovement({
      ...context,
      readWithSession,
      boardId: selectedBoardId,
      getBoardData: () => boardData,
      getPolicy: () => policy,
      canWrite: () => boardWritable() && !runtime.writes.has(selectedBoardId),
      canMoveCard: (cardId) => !cardWorkPending(cardId),
      beginWrite: (cardId) =>
        !uploads?.blocksCard(cardId) &&
        !approval?.blocksCard(cardId) &&
        !laneApproval?.blocksCard(cardId)
          ? beginWrite(cardId)
          : null,
      onChange() {
        movementPanel?.update();
        board?.refreshMovement();
        editor?.update();
      },
      onRefresh: refreshBoardAndDrawer,
    });
    movementPanel = mountCardMovement(
      movementHost,
      movement,
      () => drawerText,
      () => focusedTextConfirmed,
    );
    createCard = mountCreateCard(shellElement("#card-create-root"), {
      ...context,
      readWithSession,
      boardId: selectedBoardId,
      isPageActive: () => pageActive,
      stagedFiles: runtime.stagedFiles,
      ...(uploads ? { attachments: { canStage: canUpload, upload: uploads.submitCreated } } : {}),
      beginWrite: (relatedCardIds = []) => beginBoardCardWrite(relatedCardIds),
      beginInspection: () => beginWrite(selectedBoardId),
      context: () => {
        if (!boardData || !policy || !boardSnapshot?.confirmedEtag) return null;
        return {
          etag: boardSnapshot.confirmedEtag,
          writable: boardWritable(),
          ...(policy.capabilities.has("manage_relationships")
            ? { relationshipTargets: boardData.cards }
            : {}),
          lanes: policy.capabilities.has("create_card")
            ? boardData.lanes.filter(({ id }) => policy?.createLaneIds.has(id))
            : [],
          users: boardData.users,
          priorities: boardData.priorities,
          defaultOwnerId: boardData.board.default_owner_id,
          defaultPriorityId: policy.defaultPriorityId,
        };
      },
      onConfirmed(cardId, _ticket, showCreatedCard) {
        if (!pageActive || signal.aborted) return;
        refreshBoardAndDrawer();
        if (showCreatedCard) drawer?.open({ id: cardId });
      },
      onStale: refreshBoardAndDrawer,
      focusFallback: () => board?.focusFilter(),
    });
    archive = mountCardArchive(archiveHost, {
      ...context,
      readWithSession,
      boardId: selectedBoardId,
      beginWrite: beginCardWrite,
      beginInspect: beginWrite,
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
            boardWritable() &&
            focusedTextConfirmed &&
            drawerText.editable &&
            !cardWorkPending(drawerText.cardId),
          canAssignOwner: policy?.capabilities.has("edit_card") === true,
          canSetPriority: policy?.capabilities.has("set_priority") === true,
        },
      beginWrite: beginCardWrite,
      beginInspection: beginWrite,
      onChange: () => editor?.update(),
      onConfirmed(saved) {
        const previous = runtime.cardVersions.get(saved.cardId);
        if (!previous || saved.revision > previous.revision) {
          runtime.cardVersions.set(saved.cardId, saved);
          if (runtime.cardVersions.size > 64) {
            const oldest = runtime.cardVersions.keys().next().value;
            if (oldest) runtime.cardVersions.delete(oldest);
          }
        }
        runtime.refreshBoard?.(selectedBoardId);
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
    void refreshPrincipal();
    refreshPolicy();
    void workspace.refresh().then(() => boardState?.retry());
  };
  retry.addEventListener("click", retryLoad);

  function dispose() {
    if (signal.aborted) return;
    saveBoardView();
    lifetime.abort();
    view?.cancelAnimationFrame(policyRefreshFrame);
    view?.cancelAnimationFrame(laneVisibilityFrame);
    textBinding?.abort();
    commentsBinding?.abort();
    editor?.dispose();
    drawer?.dispose();
    boardState?.dispose();
    board?.dispose();
    movementPanel?.dispose();
    createCard?.dispose();
    attachments?.dispose();
    uploads?.dispose();
    ticketOrder?.dispose();
    laneApproval?.dispose();
    relationships?.dispose();
    attention.dispose();
    runtime.streamClosed = workspace.dispose();
    if (runtime.workspace === workspace) runtime.workspace = undefined;
    if (runtime.onWriteSettled === updateWriteControls) {
      runtime.onWriteSettled = undefined;
      runtime.refreshBoard = undefined;
      runtime.refreshRestoredBoard = undefined;
    }
    if (runtime.onServicePendingChange === updateAccessControls) {
      runtime.onServicePendingChange = undefined;
    }
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
    ticketOrder?.suspend();
    laneApproval?.suspend();
    publishing?.suspend();
    attention.cancelReorder();
    board?.cancelMovement();
    board?.revokeCovers();
    attachments?.show(null);
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
    backup?.update();
    drafts.refresh();
    comments?.refreshSaved();
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
    stagedFiles: new Map(),
    cardVersions: new Map(),
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
      if (
        !link ||
        link.target ||
        link.hasAttribute("download") ||
        link.origin !== view.location.origin ||
        link.protocol !== view.location.protocol
      )
        return;
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
