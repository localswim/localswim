import type { ServiceDto, SessionDto, Uuid } from "./api-types.js";
import { readBoard } from "./board/read.js";
import { mountBoard } from "./board/render.js";
import { createLaneApproval, type ConfirmedApproval } from "./board/approve-lane.js";
import { applyConfirmedBoardWrite } from "./board/confirmed-write.js";
import { cardSearchQuery, projectCards, readCardMatches } from "./board/filter.js";
import {
  inspectCardMove,
  prepareCardMove,
  requestCardMove,
  type MoveIntent,
  type PreparedCardMove,
} from "./board/move.js";
import {
  openTicketOrder,
  pendingTicketOrderLanes,
  ticketOrderInFlight,
  type ConfirmedTicketOrder,
} from "./board/ticket-order.js";
import type {
  BoardData,
  BoardProps,
  BoardScroll,
  Confirmed,
  FocusTarget,
  NavigationRequest,
  ReadState,
  RouteState,
  ScrollPosition,
  WorkspaceData,
  WorkspaceProps,
} from "./contracts.js";
import { isUuid, readFailure } from "./http.js";
import type {
  CreateAttachmentProps,
  CreateDraft,
  CreateInput,
  CreateProps,
  CreateReceipt,
} from "./create/contracts.js";
import { mountCreate } from "./create/render.js";
import { createCard, inspectCardCreation } from "./create/write.js";
import type { DrawerData } from "./drawer/contracts.js";
import { readDrawer, readDrawerTicket } from "./drawer/read.js";
import { mountDrawer } from "./drawer/render.js";
import { createCardApproval } from "./drawer/approve-card.js";
import {
  prepareCardPatch,
  requestCardPatch,
  inspectCardPatch,
  type CardPatch,
} from "./drawer/card-actions.js";
import {
  postComment,
  inspectComment,
  readCommentDraft,
  readPendingComments,
  writeCommentDraft,
  writePendingComment,
  type CommentDraft,
  type CommentPostInput,
  type CommentReceipt,
} from "./drawer/post-comment.js";
import {
  applyRelationshipReceipt,
  prepareRelationshipOperation,
  writeRelationshipOperation,
  inspectRelationshipOperation,
  type RelationshipIntent,
  type RelationshipReceipt,
} from "./drawer/edit-relationships.js";
import {
  requestAttachmentWrite,
  inspectAttachmentWrite,
  type AttachmentWrite,
} from "./drawer/attachment-data.js";
import {
  createAttachmentUploads,
  type AttachmentUploadStatus,
  type AttachmentUploadTarget,
} from "./drawer/attachment-upload.js";
import {
  mountAttachmentUploadView,
  type AttachmentUploadRow,
  type AttachmentUploadViewProps,
} from "./drawer/attachment-upload-view.js";
import type { InspectionResult, MutationResult, ReadFailure } from "./contracts.js";
import {
  startLive,
  type LiveConnection,
  type LiveEvent,
  type LiveRefresh,
} from "./live/controller.js";
import { readLiveActor } from "./live/activity.js";
import type { ActorKind } from "./api-types.js";
import { readService, readSession, readWorkspace } from "./workspace/read.js";
import { mountWorkspace } from "./workspace/render.js";
import { createRetainedBoardCache } from "./workspace/retained-cache.js";
import { readWorkspaceHistory, writeWorkspaceHistory } from "./workspace/history.js";

type BoardRoute = Extract<RouteState, { kind: "board" }>;
interface BoardMemory {
  scroll: BoardScroll;
  focus: FocusTarget | null;
  confirmed: Confirmed<BoardData> | null;
  search: string;
  showCompleted: boolean;
  matching: ReadonlySet<Uuid> | null;
  searchConfirmed: boolean;
}

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/iu;
const boardPath = (id: Uuid): string => `/board/${id}`;
const workspaceRoute = { kind: "workspace" } as const;
const zero = { left: 0, top: 0 };
const noBoardScroll = { board: zero, lanes: [] };

function selected(id: Uuid): BoardRoute {
  return { kind: "board", boardId: id, cardId: null, search: "", showCompleted: false };
}

function requestedKey(): string | null {
  const match = /^\/boards?\/([\w-]{1,128})$/u.exec(location.pathname);
  return match?.[1] ?? null;
}

function confirmed<T>(value: T): Confirmed<T> {
  const at = new Date().toISOString();
  return { value, confirmedAt: at.slice(0, 19).replace("T", " ") + "+00:00" };
}

function last<T>(read: ReadState<T>): Confirmed<T> | null {
  return read.status === "ready" ? read.current : read.previous;
}

function start(host: HTMLElement): void {
  let workspace: ReadState<WorkspaceData> = { status: "loading", previous: null };
  let service: ReadState<ServiceDto> = { status: "loading", previous: null };
  let board: ReadState<BoardData> = { status: "loading", previous: null };
  let session: SessionDto | null = null;
  let route: RouteState = workspaceRoute;
  let missingKey: string | undefined;
  let sidebarScroll: ScrollPosition = zero;
  let workspaceRead: AbortController | undefined;
  let boardRead: AbortController | undefined;
  let mounted: ReturnType<typeof mountWorkspace> | undefined;
  let connection: LiveConnection = "connecting";
  let liveRefreshSignal: AbortSignal | null = null;
  let liveMoveRevision: number | undefined;
  let lastActor: ActorKind | null = null;
  let lastActorAt: string | null = null;
  let boardReadEpoch = 0;
  let workspaceReadEpoch = 0;
  let serviceReadEpoch = 0;
  let identityReadEpoch = 0;
  let minimumWorkspaceRevision = 0;
  const minimumBoardRevisions = new Map<Uuid, number>();
  let confirmedPresentation: Confirmed<BoardData> | null = null;
  let searchRead: AbortController | undefined;
  let drawerRead: AbortController | undefined;
  let drawerData: ReadState<DrawerData> = { status: "loading", previous: null };
  let drawer: ReturnType<typeof mountDrawer> | undefined;
  let drawerKey: Uuid | null = null;
  type DrawerOperation = {
    readonly boardId: Uuid;
    readonly actorId: Uuid;
    readonly inspect: () => Promise<InspectionResult<unknown>>;
    readonly accepted: (value: unknown) => void;
    readonly comment: boolean;
    readonly holdRevision?: number;
    phase: "pending" | "unknown" | "checking";
    requestId?: Uuid;
    refused?: () => void;
  };
  const drawerOperations = new Map<Uuid, DrawerOperation>();
  const drawerMessages = new Map<Uuid, string>();
  const commentMessageCards = new Set<Uuid>();
  const relationshipOperations = new Map<Uuid, Uuid>();
  let drawerPresentation: Confirmed<DrawerData> | null = null;
  const commentDrafts = new Map<Uuid, CommentDraft>();
  const committedRelationships = new Map<Uuid, RelationshipIntent>();
  const committedText = new Map<
    Uuid,
    { readonly field: "title" | "description"; readonly value: string }
  >();
  const recoverableApprovals = new Map<Uuid, Uuid>();
  let ticketRead: AbortController | undefined;
  let creating: ReturnType<typeof mountCreate> | undefined;
  let draft: CreateDraft | undefined;
  let createLane: Uuid | undefined;
  let createBoard: Uuid | undefined;
  let createPhase: CreateProps["phase"] = "editing";
  let createError: string | null = null;
  let createIntent:
    { readonly input: CreateInput; readonly actorId: Uuid; readonly view: number } | undefined;
  let createView = 0;
  let createUploadTarget: AttachmentUploadTarget | undefined;
  let uncommittedUploadCard: Uuid | undefined;
  const uploadRefreshPending = new Set<Uuid>();
  const activeCreateUploads = new Set<Uuid>();
  let createOpen = false;
  let confirmedCreation: CreateReceipt | undefined;
  let ordering: ReturnType<typeof openTicketOrder> | undefined;
  let moveBusy = false;
  const pendingMoves = new Map<Uuid, { prepared: PreparedCardMove; requestId: Uuid }>();
  const inspectingMoves = new Set<Uuid>();
  const memory = new Map<Uuid, BoardMemory>();
  const cache = createRetainedBoardCache();
  let cacheActor: Uuid | null = null;
  let historyTimer = 0;
  let restoreHistoryOnEntry = true;
  let traversingHistory = false;
  const overlay = document.createElement("div");
  overlay.className = "lsw-interaction-overlay";
  const notice = document.createElement("div");
  notice.className = "lsw-operation-notice";
  notice.setAttribute("role", "status");
  const successNotice = document.createElement("p");
  successNotice.className = "lsw-board-success-status";
  successNotice.setAttribute("role", "status");
  let successNoticeTimer = 0;
  const checkMove = document.createElement("button");
  checkMove.type = "button";
  checkMove.textContent = "Check move status";
  checkMove.hidden = true;
  const checkCreate = document.createElement("button");
  checkCreate.type = "button";
  checkCreate.textContent = "Check creation status";
  checkCreate.hidden = true;
  const checkApproval = document.createElement("button");
  checkApproval.type = "button";
  checkApproval.textContent = "Check approval status";
  checkApproval.hidden = true;
  const checkUploads = document.createElement("button");
  checkUploads.type = "button";
  checkUploads.textContent = "Check attachment uploads";
  checkUploads.hidden = true;
  const orderChecks = new Map<Uuid, HTMLButtonElement>();
  const recovery = document.createElement("div");
  recovery.className = "lsw-operation-recovery";
  recovery.append(checkMove, checkCreate, checkApproval, checkUploads);
  overlay.append(notice, successNotice, recovery);
  host.append(overlay);
  const live = startLive({
    refresh: refreshLive,
    onInvalidate: invalidateLive,
    onConnection(next) {
      connection = next;
      render();
    },
  });
  const approval = createLaneApproval(overlay, {
    selectedBoard: () => (route.kind === "board" ? route.boardId : null),
    current: () => {
      const data = currentBoard();
      return data !== null && session !== null ? { data, session } : null;
    },
    refresh: (id, receipt) => {
      if (!isSelected(id)) return;
      if (receipt) presentConfirmedWrite(id, receipt);
      void refreshBoard(id);
    },
    onStateChange: render,
  });
  const singleApproval = createCardApproval({
    current: () => {
      const context = drawerContext();
      return context && !drawerOperations.has(context.drawer.card.id) ? context : null;
    },
    currentBoard: () => {
      const value = currentBoard(true);
      return value && session ? { board: value, session } : null;
    },
    currentActor: () => session?.principal_id ?? cacheActor,
    onStart: () => closeDrawer(true),
    onStateChange(id, state) {
      if (state.actorId !== cacheActor) return;
      if (state.status === "unknown") recoverableApprovals.set(id, state.cardId);
      else if (state.status !== "checking" && recoverableApprovals.get(id) === state.cardId)
        recoverableApprovals.delete(id);
      if (!isSelected(id)) return;
      if (state.status === "committed" && state.receipt) {
        presentConfirmedWrite(id, state.receipt);
        notice.textContent = "";
        showSuccess(state.message);
      } else {
        notice.textContent = state.message;
      }
      if (state.status === "committed" || state.status === "failed") {
        void refreshBoard(id);
      }
      render();
    },
  });
  checkApproval.addEventListener("click", () => {
    if (route.kind !== "board") return;
    const id = recoverableApprovals.get(route.boardId);
    if (id) singleApproval.checkStatus(route.boardId, id);
  });

  const uploads = createAttachmentUploads({
    canDispatch(target) {
      if (
        session?.principal_id !== target.actorId ||
        uncommittedUploadCard === target.cardId ||
        drawerOperations.has(target.cardId) ||
        singleApproval.awaitsConfirmation(target.boardId) ||
        approval.awaitsConfirmation(target.boardId) ||
        ticketOrderInFlight(target.boardId)
      )
        return false;
      const selected = isSelected(target.boardId);
      const carried = activeCreateUploads.has(target.cardId);
      if (!carried && (!selected || drawerKey !== target.cardId)) return false;
      const data = selected ? last(board)?.value : null;
      return (
        (connection === "connected" || carried) &&
        last(workspace)?.value.boards.some(
          ({ id, availability }) => id === target.boardId && availability === "open",
        ) === true &&
        (carried ||
          (data?.view.board.id === target.boardId && uploadsAllowed(data, target.actorId)))
      );
    },
    onChange(target) {
      if (cacheActor !== target.actorId) return;
      if (uploads.inFlightBoard(target.boardId, target.actorId)) {
        uploadRefreshPending.add(target.boardId);
      }
      if (
        uploadRefreshPending.has(target.boardId) &&
        !uploads.inFlightBoard(target.boardId, target.actorId)
      ) {
        uploadRefreshPending.delete(target.boardId);
        if (isSelected(target.boardId)) refreshSelected();
      }
      render();
    },
    onCommitted(target, attachment, recovered) {
      if (cacheActor !== target.actorId || !isSelected(target.boardId)) return;
      const current = (drawerPresentation ?? last(drawerData))?.value;
      if (
        !recovered &&
        attachment.active &&
        drawerKey === target.cardId &&
        current?.card.id === target.cardId &&
        !current.attachments?.some(({ id }) => id === attachment.id) &&
        current.boardRevision + 1 === attachment.revision
      ) {
        drawerPresentation = confirmed({
          ...current,
          boardRevision: attachment.revision,
          card: { ...current.card, attachment_count: current.card.attachment_count + 1 },
          attachments: [...(current.attachments ?? []), attachment],
        });
      }
      uploadRefreshPending.add(target.boardId);
    },
  });
  checkUploads.addEventListener("click", () => {
    const target = recoverableUpload();
    if (target) openDrawer(target.cardId);
  });

  function recoverableUpload(): AttachmentUploadTarget | undefined {
    if (cacheActor === null || route.kind !== "board") return undefined;
    const { boardId } = route;
    return uploads
      .targets(cacheActor)
      .find(
        (target) =>
          target.boardId === boardId &&
          target.cardId !== uncommittedUploadCard &&
          target.cardId !== drawerKey &&
          last(board)?.value.view.board.id === boardId &&
          (last(board)?.value.view.cards.some(({ id }) => id === target.cardId) ||
            uploads.rows(target).some(({ status }) => status === "unknown")),
      );
  }

  function uploadsAllowed(data: BoardData, actorId: Uuid): boolean {
    return data.policy.resource_grants.some(
      ({ user_id, capabilities }) =>
        user_id === actorId && capabilities.includes("manage_attachments"),
    );
  }

  const uploadStates: Record<AttachmentUploadStatus["status"], AttachmentUploadRow["state"]> = {
    ready: "staged",
    "needs-file": "reselect",
    uploading: "uploading",
    committed: "uploaded",
    refused: "failed",
    invalid: "failed",
    unknown: "uncertain",
  };

  function attachmentUploadProps(
    target: AttachmentUploadTarget | undefined,
    enabled: boolean,
    stageOnly = false,
  ): Omit<AttachmentUploadViewProps, "mode" | "pasteTarget"> {
    const active = () => target !== undefined && target.actorId === cacheActor;
    return {
      enabled: enabled && target !== undefined && !uploads.busy(target),
      message: target && !stageOnly ? uploads.progress(target) : undefined,
      rows: target
        ? uploads.rows(target).map(({ status, ...row }) => ({
            ...row,
            state: uploadStates[status],
            canRemove: row.canRemove && (!stageOnly || createPhase === "editing"),
            canRetry: row.canRetry && enabled && !stageOnly,
          }))
        : [],
      onSelect(files) {
        if (!target || !active() || !enabled) return;
        const prior = new Set(uploads.rows(target).map(({ id }) => id));
        void uploads.stage(target, files, stageOnly).then(() => {
          if (!active()) return;
          const added = uploads
            .rows(target)
            .filter(({ id, status }) => status === "ready" && !prior.has(id));
          if (!stageOnly) void uploads.send(target, new Set(added.map(({ id }) => id)));
          else if (createOpen && createUploadTarget === target) {
            const count = added.length;
            if (count > 0)
              showSuccess(`${count} attachment${count === 1 ? "" : "s"} added to draft.`);
          }
        });
      },
      onRemove(id) {
        if (target && active() && (!stageOnly || createPhase === "editing") && isUuid(id))
          uploads.remove(target, id);
      },
      onReselect(id, file) {
        if (target && active() && (!stageOnly || createPhase === "editing") && isUuid(id))
          void uploads.reselect(target, id, file);
      },
      onInspect(id) {
        if (target && active() && !stageOnly && isUuid(id)) void uploads.inspect(target, id);
      },
      onRetry(id) {
        if (target && active() && !stageOnly && isUuid(id)) void uploads.send(target, id);
      },
    };
  }

  function mountCreateUpload(host: HTMLElement, initial: CreateAttachmentProps) {
    const makeProps = ({ form, disabled }: CreateAttachmentProps): AttachmentUploadViewProps => {
      const data = currentBoard();
      const target = createUploadTarget;
      return {
        ...attachmentUploadProps(
          target,
          !disabled &&
            data !== null &&
            target !== undefined &&
            uploadsAllowed(data, target.actorId),
          true,
        ),
        mode: "create",
        pasteTarget: form,
      };
    };
    const mounted = mountAttachmentUploadView(host, makeProps(initial));
    return {
      update(next: CreateAttachmentProps) {
        mounted.update(makeProps(next));
      },
      dispose: mounted.dispose,
    };
  }

  function currentBoard(forApproval = false): BoardData | null {
    const id = route.kind === "board" ? route.boardId : null;
    const present = last(workspace)?.value.boards.some(
      (item) => item.id === id && item.availability === "open",
    );
    return board.status === "ready" &&
      confirmedPresentation === null &&
      (forApproval || id === null || !singleApproval.awaitsConfirmation(id)) &&
      (id === null || cacheActor === null || !uploads.inFlightBoard(id, cacheActor)) &&
      (id === null || !ticketOrderInFlight(id)) &&
      workspace.status === "ready" &&
      session !== null &&
      connection === "connected" &&
      present === true &&
      board.current.value.view.board.id === id
      ? board.current.value
      : null;
  }

  function refreshSelected(): void {
    if (route.kind === "board") void refreshBoard(route.boardId);
  }

  function isSelected(id: Uuid): boolean {
    return route.kind === "board" && route.boardId === id;
  }

  function persistHistory(next = route, mode: "push" | "replace" = "replace", path?: string): void {
    window.clearTimeout(historyTimer);
    const state = next.kind === "board" ? stateFor(next.boardId) : null;
    writeWorkspaceHistory(
      history,
      {
        route: next,
        sidebarScroll,
        boardScroll: state?.scroll ?? noBoardScroll,
        focus: state?.focus ?? null,
      },
      mode,
      path,
    );
  }

  function scheduleHistory(): void {
    window.clearTimeout(historyTimer);
    historyTimer = window.setTimeout(() => persistHistory(), 250);
  }

  function clearPrincipalState(): void {
    const abandonedUpload = createPhase === "editing" ? createUploadTarget : undefined;
    boardReadEpoch += 1;
    boardRead?.abort();
    searchRead?.abort();
    ticketRead?.abort();
    drawerRead?.abort();
    memory.clear();
    minimumBoardRevisions.clear();
    board = { status: "loading", previous: null };
    confirmedPresentation = null;
    confirmedCreation = undefined;
    createOpen = false;
    creating?.dispose();
    creating = undefined;
    draft = undefined;
    createUploadTarget = undefined;
    uncommittedUploadCard = undefined;
    uploadRefreshPending.clear();
    activeCreateUploads.clear();
    createLane = undefined;
    createBoard = undefined;
    createIntent = undefined;
    createPhase = "editing";
    createError = null;
    notice.textContent = "";
    clearTimeout(successNoticeTimer);
    successNotice.textContent = "";
    ordering?.dispose();
    ordering = undefined;
    drawerPresentation = null;
    drawerData = { status: "loading", previous: null };
    drawer?.dispose();
    drawer = undefined;
    drawerKey = null;
    commentDrafts.clear();
    drawerMessages.clear();
    commentMessageCards.clear();
    relationshipOperations.clear();
    committedRelationships.clear();
    committedText.clear();
    drawerOperations.clear();
    recoverableApprovals.clear();
    pendingMoves.clear();
    inspectingMoves.clear();
    liveMoveRevision = undefined;
    lastActor = null;
    lastActorAt = null;
    if (abandonedUpload) uploads.discard(abandonedUpload);
  }

  function setSession(next: SessionDto | null): void {
    session = next;
    if (!next || next.principal_id === cacheActor) return;
    if (cacheActor !== null) clearPrincipalState();
    cache.setPrincipal(next.principal_id);
    cacheActor = next.principal_id;
    for (const input of readPendingComments(next.principal_id)) {
      if (drawerOperations.has(input.cardId)) continue;
      const draft = readCommentDraft(next.principal_id, input.cardId) ?? {
        id: input.draftId,
        text: input.text,
      };
      commentDrafts.set(input.cardId, draft);
      drawerOperations.set(input.cardId, {
        boardId: input.boardId,
        actorId: input.authorId,
        requestId: input.requestId,
        phase: "unknown",
        comment: true,
        inspect: () => inspectComment(input),
        accepted: (value) => acceptDrawerComment(input, value as CommentReceipt),
      });
      drawerMessages.set(input.cardId, "Check the original comment before posting again.");
    }
  }

  function hidesRetained(error: ReadFailure): boolean {
    return error.kind === "http" && [401, 403, 404].includes(error.problem.status);
  }

  function redactBoard(id: Uuid): void {
    cache.evict(id);
    stateFor(id).confirmed = null;
    if (!isSelected(id)) return;
    boardReadEpoch += 1;
    boardRead?.abort();
    confirmedPresentation = null;
    clearTimeout(successNoticeTimer);
    successNotice.textContent = "";
    drawerPresentation = null;
    drawerRead?.abort();
    ticketRead?.abort();
    searchRead?.abort();
    drawerData = { status: "loading", previous: null };
    drawer?.dispose();
    drawer = undefined;
    drawerKey = null;
    if (route.kind === "board") route = { ...route, cardId: null };
  }

  function reconcileRetained(catalog: WorkspaceData): void {
    const allowed = new Set(catalog.boards.map(({ id }) => id));
    cache.retainOnly(allowed);
    if (route.kind === "board" && !allowed.has(route.boardId)) redactBoard(route.boardId);
  }

  function redactWorkspace(failure: ReadFailure): void {
    workspaceReadEpoch += 1;
    identityReadEpoch += 1;
    serviceReadEpoch += 1;
    workspaceRead?.abort();
    workspaceRead = undefined;
    clearPrincipalState();
    cache.setPrincipal(null);
    cacheActor = null;
    session = null;
    workspace = { status: "unavailable", previous: null, failure };
    board = { status: "unavailable", previous: null, failure };
    render();
  }

  function waitsForDrawerReceipt(boardId: Uuid, revision = Infinity): boolean {
    if (cacheActor !== null && uploads.inFlightBoard(boardId, cacheActor)) return true;
    for (const work of drawerOperations.values()) {
      if (
        work.boardId === boardId &&
        work.actorId === cacheActor &&
        work.holdRevision !== undefined &&
        revision > work.holdRevision
      ) {
        return true;
      }
    }
    return false;
  }

  function invalidateLive(event: LiveEvent): void {
    if (event.resource === "board" && isSelected(event.boardId)) {
      const previous = stateFor(event.boardId).confirmed;
      if (event.revision <= (previous?.value.view.board.revision ?? 0)) return;
      minimumBoardRevisions.set(
        event.boardId,
        Math.max(event.revision, minimumBoardRevisions.get(event.boardId) ?? 0),
      );
      boardReadEpoch += 1;
      board = { status: "loading", previous };
      render();
    } else if (event.resource === "workspace") {
      const previous = last(workspace);
      if (event.revision <= (previous?.value.workspace.revision ?? 0)) return;
      minimumWorkspaceRevision = Math.max(minimumWorkspaceRevision, event.revision);
      workspaceReadEpoch += 1;
      workspace = { status: "loading", previous };
      render();
    }
  }

  function stateFor(id: Uuid): BoardMemory {
    const saved = memory.get(id);
    if (saved) return saved;
    const initial: BoardMemory = {
      scroll: noBoardScroll,
      focus: null,
      confirmed: null,
      search: "",
      showCompleted: false,
      matching: null,
      searchConfirmed: true,
    };
    memory.set(id, initial);
    return initial;
  }

  function render(): void {
    let selectedBoard: BoardProps | null = null;
    let searchFound: number | undefined;
    if (route.kind === "board") {
      const id = route.boardId;
      const state = stateFor(id);
      const data = (confirmedPresentation ?? last(board))?.value;
      const presentation =
        confirmedPresentation === null
          ? board
          : board.status === "unavailable"
            ? { ...board, previous: confirmedPresentation }
            : { status: "ready" as const, current: confirmedPresentation };
      const projection =
        data?.view.board.id === id
          ? projectCards(data.view, route.showCompleted, state.matching, Date.now())
          : undefined;
      if (state.searchConfirmed) searchFound = projection?.found;
      selectedBoard = {
        liveMoveRevision,
        boardName: last(workspace)?.value.boards.find((item) => item.id === id)?.name ?? undefined,
        data: presentation,
        session,
        route,
        scroll: state.scroll,
        focus: state.focus,
        projection,
        writable: currentBoard() !== null,
        onLaneAction: laneAction,
        approvalState: (laneId) =>
          approval.busy(laneId) ? "busy" : approval.recoverable(laneId) ? "unknown" : null,
        onMove: (intent) => {
          void moveCard(intent);
        },
        moveEnabled: !moveBusy && !pendingMoves.has(id),
        onNavigate: navigate,
        onScrollChange: (scroll) => {
          if (state.search === "") {
            state.scroll = scroll;
            scheduleHistory();
          }
        },
        onFocusChange: (focus) => {
          state.focus = focus;
          scheduleHistory();
        },
        onRefresh: () => {
          void refreshWorkspace();
        },
      };
    }
    const props: WorkspaceProps = {
      boardPath,
      unavailableBoardKey: missingKey,
      data: workspace,
      service,
      session,
      route,
      selectedBoard,
      connection,
      refreshing:
        liveRefreshSignal?.aborted === false ||
        (board.status === "loading" && last(board) !== null),
      lastActor,
      lastActorAt,
      searchFound,
      onSearch: changeSearch,
      overlayOpen: drawerKey !== null || createOpen || ordering !== undefined,
      scroll: sidebarScroll,
      slots: { mountBoard },
      cache,
      onNavigate: navigate,
      onScrollChange: (scroll) => {
        sidebarScroll = scroll;
        scheduleHistory();
      },
      onRefresh: () => {
        void refreshWorkspace();
      },
    };
    if (mounted) mounted.update(props);
    else mounted = mountWorkspace(host, props);
    const sidebar = host.querySelector(".lsw-workspace-sidebar");
    const header = host.querySelector(".lsw-workspace-header");
    overlay.style.setProperty(
      "--lsw-drawer-sidebar-width",
      `${sidebar?.getBoundingClientRect().width ?? 312}px`,
    );
    overlay.style.setProperty(
      "--lsw-drawer-header-height",
      `${header?.getBoundingClientRect().height ?? 44}px`,
    );
    renderDrawer();
    renderCreate();
    ordering?.update();
    checkMove.hidden = route.kind !== "board" || !pendingMoves.has(route.boardId);
    checkCreate.hidden = createPhase !== "unknown" || createOpen;
    checkApproval.hidden = route.kind !== "board" || !recoverableApprovals.has(route.boardId);
    checkUploads.hidden = recoverableUpload() === undefined;
    renderOrderChecks();
  }

  function renderOrderChecks(): void {
    const data = route.kind === "board" ? stateFor(route.boardId).confirmed?.value : undefined;
    const lanes = data ? pendingTicketOrderLanes(data.view.board.id) : [];
    for (const [id, button] of orderChecks) {
      if (lanes.includes(id)) continue;
      button.remove();
      orderChecks.delete(id);
    }
    if (!data) return;
    for (const id of lanes) {
      if (orderChecks.has(id)) continue;
      const button = document.createElement("button");
      button.type = "button";
      const label = data.view.lanes.find((lane) => lane.id === id)?.label;
      button.textContent = label ? `Check ${label} ticket order` : "Check ticket order status";
      button.addEventListener("click", () => {
        if (route.kind !== "board") return;
        const current = stateFor(route.boardId).confirmed?.value;
        if (current) showOrder(current, id);
      });
      recovery.append(button);
      orderChecks.set(id, button);
    }
  }

  function changeSearch(value: string): void {
    if (route.kind !== "board") return;
    const id = route.boardId;
    const state = stateFor(id);
    const wasFiltered = cardSearchQuery(state.search) !== "";
    state.search = value;
    route = { ...route, search: value };
    scheduleHistory();
    searchRead?.abort();
    const query = cardSearchQuery(value);
    if (query === "") {
      if (wasFiltered) state.scroll = { ...state.scroll, lanes: [] };
      state.matching = null;
      state.searchConfirmed = true;
      if (notice.textContent === "Card search could not be refreshed.") notice.textContent = "";
      render();
      return;
    }
    state.searchConfirmed = false;
    const attempt = new AbortController();
    searchRead = attempt;
    render();
    void readCardMatches(id, query, attempt.signal).then(
      (matches) => {
        if (
          attempt.signal.aborted ||
          route.kind !== "board" ||
          route.boardId !== id ||
          state.search !== value ||
          waitsForDrawerReceipt(id)
        )
          return;
        state.matching = matches;
        state.searchConfirmed = true;
        if (notice.textContent === "Card search could not be refreshed.") notice.textContent = "";
        render();
      },
      () => {
        if (!attempt.signal.aborted && route.kind === "board" && route.boardId === id) {
          notice.textContent = "Card search could not be refreshed.";
        }
      },
    );
  }

  function closeDrawer(animate = false): void {
    if (route.kind !== "board") return;
    route = { ...route, cardId: null };
    persistHistory();
    drawerRead?.abort();
    ticketRead?.abort();
    const opener = drawerKey;
    if (opener !== null) {
      createView += 1;
      confirmedCreation = undefined;
    }
    drawerKey = null;
    drawerPresentation = null;
    if (animate) drawer?.closeAnimated();
    else drawer?.dispose();
    drawer = undefined;
    render();
    if (opener !== null && document.activeElement === document.body) {
      const root = host.querySelector(".lsw-workspace-board-host > div")?.shadowRoot;
      const card = root?.querySelector<HTMLElement>(`[data-card-id="${opener}"]`);
      (card ?? host.querySelector<HTMLElement>(".lsw-workspace-card-query"))?.focus({
        preventScroll: true,
      });
    }
  }

  function openDrawer(id: Uuid): void {
    if (route.kind !== "board") return;
    if (drawerKey !== id) {
      createView += 1;
      confirmedCreation = undefined;
      drawerPresentation = null;
    }
    const boardId = route.boardId;
    route = { ...route, cardId: id };
    persistHistory();
    drawerRead?.abort();
    ticketRead?.abort();
    const attempt = new AbortController();
    drawerRead = attempt;
    const previous = drawerKey === id ? last(drawerData) : null;
    drawerKey = id;
    drawerData = { status: "loading", previous };
    render();
    void loadDrawer(boardId, id, attempt, previous);
  }

  async function loadDrawer(
    boardId: Uuid,
    id: Uuid,
    attempt: AbortController,
    previous: Confirmed<DrawerData> | null,
  ): Promise<void> {
    const { signal } = attempt;
    const epoch = boardReadEpoch;
    try {
      const data = await readDrawer(boardId, id, signal);
      if (signal.aborted || route.kind !== "board" || route.boardId !== boardId) return;
      if (waitsForDrawerReceipt(boardId, data.boardRevision)) return;
      const state = stateFor(boardId);
      let refreshSearch = false;
      if (state.confirmed?.value.boardEtag !== data.boardEtag) {
        const current = await readBoard(boardId, signal);
        if (signal.aborted || route.kind !== "board" || route.boardId !== boardId) return;
        if (
          current.boardEtag !== data.boardEtag ||
          epoch !== boardReadEpoch ||
          board.status !== "ready" ||
          approval.awaitsConfirmation(boardId) ||
          singleApproval.awaitsConfirmation(boardId) ||
          ticketOrderInFlight(boardId) ||
          (state.confirmed?.value.view.board.revision ?? 0) > current.view.board.revision ||
          waitsForDrawerReceipt(boardId, current.view.board.revision)
        ) {
          drawerData = { status: "unavailable", previous, failure: { kind: "invalid_response" } };
          render();
          return;
        }
        refreshSearch = state.search !== "";
        lastActor = null;
        lastActorAt = null;
        state.confirmed = confirmed(current);
        cache.retain(state.confirmed);
        board = { status: "ready", current: state.confirmed };
      }
      drawerData = { status: "ready", current: confirmed(data) };
      if (drawerPresentation && data.boardRevision >= drawerPresentation.value.boardRevision)
        drawerPresentation = null;
      if (refreshSearch) {
        changeSearch(state.search);
        return;
      }
    } catch (error) {
      if (signal.aborted) return;
      const failure = readFailure(error);
      if (hidesRetained(failure)) drawerPresentation = null;
      drawerData = {
        status: "unavailable",
        previous: hidesRetained(failure) ? null : previous,
        failure,
      };
    }
    render();
  }

  function renderDrawer(): void {
    const id = drawerKey;
    if (id === null) return;
    const context = drawerContext();
    const work = drawerOperations.get(id);
    const actor = session?.principal_id ?? cacheActor;
    const uploadTarget =
      route.kind === "board" && actor !== null
        ? { boardId: route.boardId, cardId: id, actorId: actor }
        : undefined;
    const writable =
      context !== null &&
      work === undefined &&
      (uploadTarget === undefined || !uploads.inFlight(uploadTarget)) &&
      !singleApproval.awaitsConfirmation(context.board.view.board.id);
    if (actor !== null && !commentDrafts.has(id)) {
      const saved = readCommentDraft(actor, id);
      if (saved) commentDrafts.set(id, saved);
    }
    const comment = commentDrafts.get(id)?.text ?? "";
    const displayedDrawer = drawerPresentation ?? last(drawerData);
    const displayedBoard = (confirmedPresentation ?? last(board))?.value ?? null;
    const presentation =
      drawerPresentation === null
        ? drawerData
        : drawerData.status === "unavailable"
          ? { ...drawerData, previous: drawerPresentation }
          : { status: "ready" as const, current: drawerPresentation };
    const canComment =
      displayedDrawer !== null &&
      displayedBoard?.policy.resource_grants.some(
        (grant) => grant.user_id === actor && grant.capabilities.includes("comment"),
      ) === true;
    const operationMessage = drawerMessages.get(id) ?? "";
    const commentMessage = work?.comment || commentMessageCards.has(id);
    const props = {
      data: presentation,
      board:
        displayedBoard?.view.board.id === displayedDrawer?.value.card.board_id
          ? displayedBoard
          : null,
      session,
      writable,
      operationMessage: commentMessage ? "" : operationMessage,
      onInspectOperation:
        work?.phase === "unknown" ? () => void inspectDrawerOperation(id) : undefined,
      onPatch: (patch: CardPatch) => void patchDrawerCard(patch),
      onRelationship: (intent: RelationshipIntent) => void changeDrawerRelationship(intent),
      committedRelationship: committedRelationships.get(id),
      committedText: committedText.get(id),
      onAttachment: (action: AttachmentWrite["action"]) => void changeDrawerAttachment(action),
      uploadKey: uploadTarget?.cardId,
      upload: attachmentUploadProps(
        uploadTarget,
        uploadTarget !== undefined &&
          writable &&
          !context.drawer.card.archived &&
          context.drawer.card.completed_at === null &&
          uploadsAllowed(context.board, uploadTarget.actorId),
      ),
      onApprove: () => singleApproval.approve(),
      comment: {
        draft: comment,
        canEdit: canComment || comment.length > 0,
        canSubmit: writable && canComment && comment !== "",
        canInspect: work?.comment === true && work.phase === "unknown",
        status: commentMessage ? operationMessage : "",
      },
      onCommentDraft: (text: string) => {
        if (actor === null || actor !== cacheActor) return;
        const next = { id: crypto.randomUUID(), text };
        commentDrafts.set(id, next);
        writeCommentDraft(actor, id, next);
        renderDrawer();
      },
      onCommentSubmit: () => void submitDrawerComment(),
      onClose: closeDrawer,
      onRefresh: () => openDrawer(id),
      onOpenCard: openDrawer,
      onOpenTicket: (ticket: number) => {
        if (route.kind !== "board") return;
        const boardId = route.boardId;
        ticketRead?.abort();
        const attempt = new AbortController();
        ticketRead = attempt;
        void readDrawerTicket(boardId, ticket, attempt.signal).then(
          (match) => {
            if (
              attempt.signal.aborted ||
              route.kind !== "board" ||
              route.boardId !== boardId ||
              drawerKey !== id
            )
              return;
            if (match) openDrawer(match.id);
            else notice.textContent = "The linked ticket is unavailable.";
          },
          () => {
            if (!attempt.signal.aborted)
              notice.textContent = "The linked card could not be loaded.";
          },
        );
      },
    };
    if (drawer) drawer.update(props);
    else drawer = mountDrawer(overlay, props);
  }

  function drawerContext() {
    const data = currentBoard();
    if (!data || !session || drawerPresentation !== null || drawerData.status !== "ready")
      return null;
    const value = drawerData.current.value;
    return value.card.id === drawerKey &&
      value.card.board_id === data.view.board.id &&
      value.boardEtag === data.boardEtag &&
      value.boardRevision === data.view.board.revision
      ? { board: data, session, drawer: value }
      : null;
  }

  type DrawerContext = NonNullable<ReturnType<typeof drawerContext>>;

  async function runDrawerOperation<T>(
    context: DrawerContext,
    send: (beforeDispatch: (actual: SessionDto) => void) => Promise<MutationResult<T>>,
    inspect: () => Promise<InspectionResult<T>>,
    accepted: (value: T) => void = () => {},
    comment = false,
    refused?: () => void,
  ): Promise<void> {
    const id = context.drawer.card.id;
    if (drawerOperations.has(id) || singleApproval.awaitsConfirmation(context.drawer.card.board_id))
      return;
    const work: DrawerOperation = {
      boardId: context.drawer.card.board_id,
      actorId: context.session.principal_id,
      holdRevision: context.drawer.boardRevision,
      phase: "pending",
      inspect,
      accepted: (value) => accepted(value as T),
      comment,
      refused,
    };
    drawerOperations.set(id, work);
    if (comment) commentMessageCards.add(id);
    else commentMessageCards.delete(id);
    drawerMessages.set(id, comment ? "Posting comment…" : "Saving…");
    render();
    let dispatched = false;
    let result: MutationResult<T> | undefined;
    try {
      result = await send((actual) => {
        const current = drawerContext();
        if (
          !current ||
          current.drawer.card.id !== id ||
          current.drawer.cardEtag !== context.drawer.cardEtag ||
          current.board.boardEtag !== context.board.boardEtag ||
          current.session.principal_id !== work.actorId ||
          actual.principal_id !== work.actorId
        )
          throw new Error("Card context changed");
        dispatched = true;
      });
    } catch {
      // A request that reached the service needs inspection before another write.
    }
    if (drawerOperations.get(id) !== work) return;
    if (result?.outcome === "committed") {
      settleDrawerOperation(id, work, result.value);
    } else if (dispatched && result?.outcome !== "not_applied") {
      work.requestId = result?.requestId;
      work.phase = "unknown";
      await inspectDrawerOperation(id, work);
    } else {
      work.refused?.();
      drawerOperations.delete(id);
      drawerMessages.set(
        id,
        result?.outcome === "not_applied"
          ? result.problem.detail
          : "The card changed. Refresh before saving.",
      );
      if (isSelected(work.boardId)) refreshSelected();
      render();
    }
  }

  function settleDrawerOperation(id: Uuid, work: DrawerOperation, value: unknown): void {
    if (drawerOperations.get(id) !== work) return;
    drawerOperations.delete(id);
    drawerMessages.delete(id);
    commentMessageCards.delete(id);
    work.accepted(value);
    if (isSelected(work.boardId)) refreshSelected();
    render();
  }

  async function inspectDrawerOperation(id: Uuid, expected?: DrawerOperation): Promise<void> {
    const work = drawerOperations.get(id);
    if (
      !work ||
      work.phase !== "unknown" ||
      (expected && expected !== work) ||
      session?.principal_id !== work.actorId
    )
      return;
    work.phase = "checking";
    drawerMessages.set(id, "Checking the original change…");
    render();
    try {
      const result = await work.inspect();
      if (result.outcome === "committed") {
        settleDrawerOperation(id, work, result.value);
        return;
      }
    } catch {
      // Keep the original request available for inspection.
    }
    if (drawerOperations.get(id) !== work) return;
    work.phase = "unknown";
    drawerMessages.set(id, "The original change could not be confirmed.");
    if (isSelected(work.boardId)) refreshSelected();
    render();
  }

  async function patchDrawerCard(patch: CardPatch): Promise<void> {
    const context = drawerContext();
    if (!context) return;
    const prepared = prepareCardPatch(context.board, context.drawer, context.session, patch);
    if (!prepared) {
      drawerMessages.set(
        context.drawer.card.id,
        "The field could not be saved. Check the value and try again.",
      );
      renderDrawer();
      return;
    }
    committedText.delete(context.drawer.card.id);
    await runDrawerOperation(
      context,
      (guard) => requestCardPatch(prepared, undefined, guard),
      () => inspectCardPatch(prepared),
      (card) => {
        if ("title" in patch) committedText.set(card.id, { field: "title", value: patch.title });
        if ("description" in patch)
          committedText.set(card.id, { field: "description", value: patch.description });
        if (!isSelected(card.board_id)) return;
        if ("owner_id" in patch) {
          const from = context.board.users.find(({ id }) => id === prepared.card.owner_id)?.kind;
          const to = context.board.users.find(({ id }) => id === patch.owner_id)?.kind;
          const actor = context.board.view.board.actor_presentation?.human?.label ?? "Terry";
          if (from && to)
            showSuccess(
              `#${String(card.ticket).padStart(4, "0")} ownership change: ${from === "human" ? actor.toLowerCase() : from} → ${to === "human" ? actor.toLowerCase() : to} (by ${actor.toLowerCase()})`,
            );
        }
        const base = (confirmedPresentation ?? last(board))?.value;
        if (
          base?.boardEtag === prepared.boardEtag &&
          card.revision === prepared.boardRevision + 1 &&
          !("priority_id" in patch) &&
          !("archived" in patch)
        ) {
          minimumBoardRevisions.set(
            card.board_id,
            Math.max(card.revision, minimumBoardRevisions.get(card.board_id) ?? 0),
          );
          confirmedPresentation = confirmed({
            ...base,
            view: {
              ...base.view,
              board: {
                ...base.view.board,
                revision: card.revision,
                updated_at: card.updated_at,
                last_card_edit_at: card.updated_at,
              },
              cards: base.view.cards.map((item) => (item.id === card.id ? card : item)),
            },
          });
        }
        if (drawerKey === card.id && (last(drawerData)?.value.boardRevision ?? 0) < card.revision) {
          drawerPresentation = confirmed({ ...context.drawer, card, boardRevision: card.revision });
        }
      },
    );
  }

  async function submitDrawerComment(): Promise<void> {
    const context = drawerContext();
    if (!context) return;
    const { card, cardEtag, boardEtag } = context.drawer;
    const draft = commentDrafts.get(card.id);
    if (!draft?.text) return;
    const input = {
      commentId: crypto.randomUUID(),
      requestId: crypto.randomUUID(),
      boardId: card.board_id,
      cardId: card.id,
      cardEtag,
      boardEtag,
      authorId: context.session.principal_id,
      draftId: draft.id,
      text: draft.text,
    };
    await runDrawerOperation<CommentReceipt>(
      context,
      (guard) =>
        postComment(input, undefined, (actual) => {
          writePendingComment(input);
          try {
            guard(actual);
          } catch (error) {
            writePendingComment(input, true);
            throw error;
          }
        }),
      () => inspectComment(input),
      (value) => acceptDrawerComment(input, value),
      true,
      () => writePendingComment(input, true),
    );
  }

  function acceptDrawerComment(input: CommentPostInput, value: CommentReceipt): void {
    writePendingComment(input, true);
    if (commentDrafts.get(input.cardId)?.id === input.draftId) {
      commentDrafts.delete(input.cardId);
      writeCommentDraft(input.authorId, input.cardId);
    }
    const current = (drawerPresentation ?? last(drawerData))?.value;
    const receipt = value.comment;
    if (
      drawerKey === input.cardId &&
      current?.card.id === input.cardId &&
      !current.comments.some(({ id }) => id === receipt.id)
    ) {
      const comments = [{ ...receipt, ticket: current.card.ticket }, ...current.comments].toSorted(
        (a, b) => b.created_at.localeCompare(a.created_at),
      );
      drawerPresentation = confirmed({ ...current, comments });
    }
  }

  async function changeDrawerRelationship(intent: RelationshipIntent): Promise<void> {
    const context = drawerContext();
    if (!context) return;
    const { card } = context.drawer;
    const linkId = intent.action === "create" ? intent.id : intent.original.id;
    if (relationshipOperations.has(linkId) || drawerOperations.has(card.id)) return;
    relationshipOperations.set(linkId, card.id);
    const requestId = crypto.randomUUID();
    let input: Awaited<ReturnType<typeof prepareRelationshipOperation>>;
    await runDrawerOperation<DrawerData | RelationshipReceipt>(
      context,
      async (guard) => {
        input = await prepareRelationshipOperation(
          context.drawer,
          intent,
          context.session.principal_id,
        );
        return writeRelationshipOperation(input, requestId, undefined, guard);
      },
      async () =>
        input
          ? inspectRelationshipOperation(input, await readDrawer(card.board_id, card.id))
          : { outcome: "unknown" },
      (receipt) => {
        relationshipOperations.delete(linkId);
        committedRelationships.set(card.id, intent);
        if (
          !isSelected(card.board_id) ||
          last(board)?.value.view.board.id !== card.board_id ||
          !last(workspace)?.value.boards.some(({ id }) => id === card.board_id)
        )
          return;
        if (intent.action === "patch") {
          const key = (kind: typeof intent.kind, direction: typeof intent.direction): string =>
            direction === "outgoing" || kind === "relates_to"
              ? kind
              : { blocks: "blocked_by", duplicates: "duplicated_by", references: "referenced_by" }[
                  kind
                ];
          const before = key(intent.original.kind, intent.original.direction);
          const after = key(intent.kind, intent.direction);
          const actor =
            context.board.view.board.actor_presentation?.human?.label ??
            context.board.users.find(({ id }) => id === context.session.principal_id)?.label ??
            "Terry";
          showSuccess(
            `#${String(card.ticket).padStart(4, "0")} relationship to #${String(intent.original.card.ticket).padStart(4, "0")}: ${before} -> ${after} (by ${actor.toLowerCase()})`,
          );
        }
        if (drawerKey !== card.id) return;
        const current = (drawerPresentation ?? last(drawerData))?.value;
        if (current === undefined) return;
        if ("card" in receipt) {
          if (receipt.boardRevision > current.boardRevision)
            drawerPresentation = confirmed(receipt);
        } else if (input && current.boardRevision < receipt.revision) {
          drawerPresentation = confirmed(
            applyRelationshipReceipt(context.drawer, input, receipt, context.board.view.cards),
          );
        }
      },
    );
    if (
      cacheActor === context.session.principal_id &&
      relationshipOperations.get(linkId) === card.id &&
      !drawerOperations.has(card.id)
    ) {
      relationshipOperations.delete(linkId);
    }
  }

  async function changeDrawerAttachment(action: AttachmentWrite["action"]): Promise<void> {
    const context = drawerContext();
    if (!context) return;
    const write = { drawer: context.drawer, actorId: context.session.principal_id, action };
    await runDrawerOperation(
      context,
      (guard) => requestAttachmentWrite(write, undefined, guard),
      () => inspectAttachmentWrite(write),
      () => {
        const { card } = context.drawer;
        if (action.kind === "detach" && isSelected(card.board_id)) {
          const actor = context.board.view.board.actor_presentation?.human?.label ?? "Terry";
          showSuccess(
            `removed attachment ${action.attachment.original_name} from #${String(card.ticket).padStart(4, "0")} (by ${actor.toLowerCase()})`,
          );
        }
        const current = (drawerPresentation ?? last(drawerData))?.value;
        const revision = context.drawer.boardRevision + 1;
        if (
          drawerKey !== context.drawer.card.id ||
          current?.card.id !== drawerKey ||
          current.boardRevision >= revision
        )
          return;
        const detached = action.kind === "detach" ? action.attachment.id : null;
        const cover =
          action.kind === "cover"
            ? (action.attachment?.id ?? null)
            : current.card.cover_attachment_id === detached
              ? null
              : current.card.cover_attachment_id;
        drawerPresentation = confirmed({
          ...current,
          boardRevision: revision,
          card: {
            ...current.card,
            cover_attachment_id: cover,
            attachment_count: current.card.attachment_count - (detached === null ? 0 : 1),
          },
          attachments: current.attachments?.filter((item) => item.id !== detached),
        });
      },
    );
  }

  function closeCreate(): void {
    if (createPhase === "submitting") return;
    createOpen = false;
    creating?.dispose();
    creating = undefined;
    if (createPhase === "unknown") {
      notice.textContent =
        "Creation is not confirmed. Open New card to inspect the original request.";
      render();
      return;
    }
    const target = createUploadTarget;
    createUploadTarget = undefined;
    if (target && uncommittedUploadCard === target.cardId) {
      uncommittedUploadCard = undefined;
      uploads.discard(target);
    }
    draft = undefined;
    createLane = undefined;
    createBoard = undefined;
    createIntent = undefined;
    createError = null;
    render();
  }

  function renderCreate(): void {
    const data = last(board)?.value;
    if (!createOpen || !draft || !createLane || !data || data.view.board.id !== createBoard) return;
    const lane = data.view.lanes.find((item) => item.id === createLane);
    if (!lane) return;
    const actor =
      data.view.board.actor_presentation?.human?.label ??
      data.users.find((user) => user.id === session?.principal_id)?.label ??
      "Terry";
    const props: CreateProps = {
      laneName: lane.label,
      actorName: actor,
      draft,
      phase: createPhase,
      writable:
        currentBoard()?.view.board.id === createBoard &&
        (createUploadTarget === undefined || !uploads.busy(createUploadTarget)),
      error: createError,
      attachment: mountCreateUpload,
      priorities: data.priorities,
      owners: data.users,
      targets: data.view.cards
        .filter((card) => !card.archived)
        .toSorted((a, b) => a.ticket - b.ticket),
      onDraftChange: (next) => {
        draft = next;
        createError = null;
        renderCreate();
      },
      onCancel: closeCreate,
      onSubmit: () => {
        void submitCreate();
      },
      onInspect: () => {
        void inspectCreate();
      },
    };
    if (creating) creating.update(props);
    else creating = mountCreate(overlay, props);
  }

  function settleCreation(receipt: CreateReceipt, originView: number, recovered = false): void {
    const upload = createUploadTarget;
    const fileCount = upload
      ? uploads.rows(upload).filter(({ status }) => status === "ready").length
      : 0;
    if (uncommittedUploadCard === receipt.card.id) uncommittedUploadCard = undefined;
    createPhase = "editing";
    closeCreate();
    const { card } = receipt;
    if (upload?.cardId === card.id && fileCount > 0) {
      activeCreateUploads.add(upload.cardId);
      void uploads.send(upload).finally(() => activeCreateUploads.delete(upload.cardId));
    }
    if (!isSelected(card.board_id)) {
      return;
    }
    notice.textContent = "";
    const reveal = createView === originView;
    confirmedCreation = recovered && reveal ? receipt : undefined;
    const lane = last(board)?.value.view.lanes.find(({ id }) => id === card.lane_id);
    const actor = last(board)?.value.view.board.actor_presentation?.human?.label ?? "Terry";
    const created = `created #${String(card.ticket).padStart(4, "0")} card-${card.id}${lane ? ` in ${lane.key}` : ""} (by ${actor.toLowerCase()})`;
    showSuccess(
      fileCount > 0
        ? `${created}. Uploading ${fileCount} attachment${fileCount === 1 ? "" : "s"}…`
        : created,
    );
    refreshSelected();
    if (!recovered && reveal) openDrawer(receipt.card.id);
  }

  function showSuccess(message: string): void {
    clearTimeout(successNoticeTimer);
    successNotice.textContent = message;
    successNoticeTimer = window.setTimeout(() => {
      successNotice.textContent = "";
    }, 5000);
  }

  function ownsCreateIntent(intent: NonNullable<typeof createIntent>): boolean {
    return createIntent === intent && cacheActor === intent.actorId;
  }

  async function inspectCreate(originView = createView): Promise<void> {
    const intent = createIntent;
    if (!intent || !ownsCreateIntent(intent) || createPhase !== "unknown") return;
    createPhase = "submitting";
    renderCreate();
    try {
      const result = await inspectCardCreation(intent.input);
      if (!ownsCreateIntent(intent)) return;
      if (result.outcome === "committed") {
        settleCreation(result.value, originView, true);
        return;
      }
    } catch {
      // Keep the submitted intent available for the next authoritative inspection.
    }
    if (!ownsCreateIntent(intent)) return;
    createPhase = "unknown";
    createError = "Creation is not yet confirmed. Check the original card before retrying.";
    renderCreate();
    if (!createOpen && isSelected(intent.input.boardId)) notice.textContent = createError;
    render();
  }

  async function submitCreate(): Promise<void> {
    const data = currentBoard();
    const actorId = session?.principal_id;
    if (
      !draft ||
      !createLane ||
      createPhase !== "editing" ||
      !data ||
      !actorId ||
      (createUploadTarget !== undefined && uploads.busy(createUploadTarget)) ||
      createBoard !== data.view.board.id
    ) {
      createError =
        createUploadTarget !== undefined && uploads.busy(createUploadTarget)
          ? "Wait until the attachments are ready."
          : "The board must be current before creating a card.";
      renderCreate();
      return;
    }
    const lane = data.view.lanes.find((item) => item.id === createLane);
    if (
      !lane ||
      !data.priorities.some((priority) => priority.id === draft?.priorityId) ||
      !/[^\p{White_Space}]/u.test(draft.title)
    )
      return;
    const input: CreateInput = {
      ...draft,
      cardId: createUploadTarget?.cardId ?? crypto.randomUUID(),
      requestId: crypto.randomUUID(),
      boardId: data.view.board.id,
      boardEtag: data.boardEtag,
      boardRevision: data.view.board.revision,
      laneId: lane.id,
      completed: lane.role === "completed",
      priorities: data.priorities,
    };
    const intent = { input, actorId, view: createView };
    createIntent = intent;
    createPhase = "submitting";
    createError = null;
    renderCreate();
    let dispatched = false;
    try {
      const result = await createCard(input, undefined, (actual) => {
        const current = currentBoard();
        if (
          !createOpen ||
          !ownsCreateIntent(intent) ||
          actual.principal_id !== actorId ||
          current?.boardEtag !== input.boardEtag ||
          current.view.board.id !== input.boardId
        ) {
          throw new Error("Creation context changed");
        }
        if (createUploadTarget && !uploads.retain(createUploadTarget)) {
          throw new Error("Attachment recovery could not be saved");
        }
        dispatched = true;
      });
      if (!ownsCreateIntent(intent)) return;
      if (result.outcome === "committed") {
        settleCreation(result.value, intent.view);
        return;
      }
      if (result.outcome === "not_applied") {
        if (createUploadTarget) uploads.releaseToDraft(createUploadTarget);
        createPhase = "editing";
        createIntent = undefined;
        createError = result.problem.detail;
        if (isSelected(input.boardId)) refreshSelected();
        renderCreate();
        return;
      }
    } catch {
      // A lost result leaves the original UUID available for inspection.
    }
    if (!ownsCreateIntent(intent)) return;
    if (!dispatched) {
      createPhase = "editing";
      createIntent = undefined;
      createError = "Creation was not sent. Refresh the board and try again.";
      render();
      return;
    }
    createPhase = "unknown";
    await inspectCreate(intent.view);
  }

  function laneAction(action: "create" | "order" | "approve", id: Uuid): void {
    const data = currentBoard();
    if (!data) return;
    if (action === "approve") {
      approval.open(id);
    } else if (action === "order") {
      showOrder(data, id);
    } else if (draft) {
      if (createBoard === data.view.board.id) {
        createOpen = true;
        render();
      } else {
        notice.textContent = "Return to the original board to inspect the pending creation.";
      }
    } else {
      createLane = id;
      createBoard = data.view.board.id;
      if (session !== null) {
        createUploadTarget = {
          boardId: data.view.board.id,
          cardId: crypto.randomUUID(),
          actorId: session.principal_id,
        };
        uncommittedUploadCard = createUploadTarget.cardId;
      }
      draft = {
        title: "",
        description: "",
        ownerId: data.view.board.default_owner_id,
        priorityId: data.policy.default_priority_id,
        relationships: [],
      };
      createOpen = true;
      render();
    }
  }

  function showOrder(data: BoardData, laneId: Uuid): void {
    if (ordering) return;
    ordering = openTicketOrder(overlay, data, laneId, {
      getBoard: currentBoard,
      getSession: () => session,
      canApply: () => currentBoard()?.view.board.id === data.view.board.id,
      onClose: () => {
        ordering?.dispose();
        ordering = undefined;
        render();
      },
      onApplied: (count, receipt) => {
        presentConfirmedWrite(data.view.board.id, receipt);
        showSuccess(`Reordered ${count} ticket ${count === 1 ? "number" : "numbers"}`);
        refreshSelected();
      },
      onRefresh: () => {
        if (isSelected(data.view.board.id)) refreshSelected();
      },
    });
    render();
  }

  async function moveCard(intent: MoveIntent): Promise<void> {
    const data = currentBoard();
    const actor = session?.principal_id;
    if (!data || !actor || moveBusy || pendingMoves.has(data.view.board.id)) return;
    moveBusy = true;
    render();
    try {
      const prepared = await prepareCardMove(data, intent.cardId, intent.laneId, actor);
      if (!prepared || currentBoard()?.boardEtag !== data.boardEtag) {
        if (isSelected(data.view.board.id)) {
          notice.textContent = "The board changed. Refresh before moving this card.";
          refreshSelected();
        }
        return;
      }
      let dispatched = false;
      const result = await requestCardMove(prepared, undefined, (actual) => {
        const current = currentBoard();
        if (
          actual.principal_id !== actor ||
          current?.boardEtag !== prepared.boardEtag ||
          current.view.board.id !== prepared.card.board_id
        ) {
          throw new Error("Move context changed");
        }
        dispatched = true;
      });
      if (actor !== cacheActor) return;
      if (result.outcome === "unknown") {
        if (dispatched) {
          pendingMoves.set(data.view.board.id, { prepared, requestId: result.requestId });
          await inspectMove(data.view.board.id);
        } else if (isSelected(data.view.board.id)) {
          notice.textContent = "The move was not sent. Refresh the board and try again.";
        }
      } else if (isSelected(data.view.board.id)) {
        notice.textContent = result.outcome === "committed" ? "" : result.problem.detail;
        refreshSelected();
      }
    } catch {
      if (actor === cacheActor && isSelected(data.view.board.id))
        notice.textContent = "The card could not be prepared for movement.";
    } finally {
      moveBusy = false;
      render();
    }
  }

  async function inspectMove(id: Uuid): Promise<void> {
    const pending = pendingMoves.get(id);
    if (!pending || inspectingMoves.has(id)) return;
    inspectingMoves.add(id);
    const result = await inspectCardMove(pending.prepared).finally(() =>
      inspectingMoves.delete(id),
    );
    if (pendingMoves.get(id) !== pending) return;
    if (result.outcome === "committed") {
      pendingMoves.delete(id);
      if (isSelected(id)) {
        notice.textContent = "";
        refreshSelected();
      }
    } else if (isSelected(id)) {
      notice.textContent = "The move is not confirmed. Check the original result.";
    }
    render();
  }
  checkMove.addEventListener("click", () => {
    if (route.kind === "board" && !moveBusy) void inspectMove(route.boardId);
  });
  checkCreate.addEventListener("click", () => {
    void inspectCreate();
  });
  function presentConfirmedWrite(
    id: Uuid,
    receipt: ConfirmedApproval | ConfirmedTicketOrder,
  ): void {
    if (!isSelected(id)) return;
    const base = (confirmedPresentation ?? last(board))?.value;
    if (!base || base.view.board.id !== id || base.view.board.revision >= receipt.revision) return;
    minimumBoardRevisions.set(id, Math.max(receipt.revision, minimumBoardRevisions.get(id) ?? 0));
    const next = applyConfirmedBoardWrite(base, receipt);
    if (!next) return;
    confirmedPresentation = confirmed(next);
    lastActor = null;
    lastActorAt = null;
    if ("at" in receipt) {
      liveMoveRevision = receipt.revision;
      lastActor = "human";
      lastActorAt = receipt.at;
    }
  }

  async function refreshBoard(id: Uuid): Promise<void> {
    boardRead?.abort();
    const attempt = new AbortController();
    boardRead = attempt;
    const epoch = ++boardReadEpoch;
    const state = stateFor(id);
    board = { status: "loading", previous: state.confirmed ?? cache.previous(id) };
    render();
    try {
      const value = await readBoard(id, attempt.signal);
      if (attempt.signal.aborted || epoch !== boardReadEpoch || cacheActor === null) return;
      if (
        approval.awaitsConfirmation(id) ||
        singleApproval.awaitsConfirmation(id) ||
        ticketOrderInFlight(id) ||
        waitsForDrawerReceipt(id, value.view.board.revision)
      )
        return;
      if (value.view.board.revision < (minimumBoardRevisions.get(id) ?? 0)) {
        live.retry();
        return;
      }
      if ((state.confirmed?.value.view.board.revision ?? 0) > value.view.board.revision) {
        board = { status: "ready", current: state.confirmed! };
        render();
        return;
      }
      if (
        (confirmedPresentation ?? state.confirmed)?.value.view.board.revision !==
        value.view.board.revision
      ) {
        lastActor = null;
        lastActorAt = null;
      }
      state.confirmed = confirmed(value);
      cache.retain(state.confirmed);
      board = { status: "ready", current: state.confirmed };
      confirmedPresentation = null;
      if (route.kind === "board" && route.boardId === id && state.search !== "") {
        changeSearch(state.search);
      }
      const receipt = confirmedCreation;
      if (
        receipt?.card.board_id === id &&
        value.view.cards.some((card) => card.id === receipt.card.id) &&
        route.kind === "board" &&
        route.boardId === id
      ) {
        confirmedCreation = undefined;
        notice.textContent = "";
        openDrawer(receipt.card.id);
      }
      if (
        drawerKey !== null &&
        route.kind === "board" &&
        route.boardId === id &&
        last(drawerData)?.value.boardRevision !== value.view.board.revision
      )
        openDrawer(drawerKey);
    } catch (error) {
      if (attempt.signal.aborted || epoch !== boardReadEpoch) return;
      const failure = readFailure(error);
      if (hidesRetained(failure)) {
        redactBoard(id);
      }
      board = { status: "unavailable", previous: state.confirmed ?? cache.previous(id), failure };
      if (
        connection === "connected" &&
        ((failure.kind === "http" && failure.problem.status === 503) ||
          (confirmedPresentation !== null && failure.kind !== "http")) &&
        last(workspace)?.value.boards.some((item) => item.id === id && item.availability === "open")
      ) {
        live.retry();
      }
    }
    render();
  }

  function chooseRoute(next: RouteState, reload = false): void {
    const was = route.kind === "board" ? route.boardId : null;
    const nextId = next.kind === "board" ? next.boardId : null;
    if (was !== nextId) {
      createView += 1;
      if (was !== null) stateFor(was).confirmed = null;
      confirmedPresentation = null;
      confirmedCreation = undefined;
      liveMoveRevision = undefined;
      notice.textContent = "";
      clearTimeout(successNoticeTimer);
      successNotice.textContent = "";
      for (const button of orderChecks.values()) button.remove();
      orderChecks.clear();
      lastActor = null;
      lastActorAt = null;
      boardReadEpoch += 1;
      createOpen = false;
      creating?.dispose();
      creating = undefined;
      searchRead?.abort();
      drawerRead?.abort();
      ticketRead?.abort();
      drawer?.dispose();
      drawer = undefined;
      drawerKey = null;
      drawerPresentation = null;
      ordering?.dispose();
      ordering = undefined;
    }
    route = next;
    approval.updateSelection();
    if (route.kind === "board" && workspace.status === "unavailable") {
      boardRead?.abort();
      if (hidesRetained(workspace.failure)) redactBoard(route.boardId);
      board = {
        status: "unavailable",
        previous: hidesRetained(workspace.failure)
          ? null
          : (stateFor(route.boardId).confirmed ?? cache.previous(route.boardId)),
        failure: workspace.failure,
      };
      render();
      return;
    }
    if (route.kind === "board" && (reload || route.boardId !== was)) {
      void refreshBoard(route.boardId);
    } else {
      if (route.kind === "workspace") boardRead?.abort();
      render();
    }
    live.update(nextId);
  }

  function navigate(request: NavigationRequest): void {
    let next = request.route;
    if (next.kind === "board") {
      const state = stateFor(next.boardId);
      if (route.kind !== "board" || route.boardId !== next.boardId) {
        next = { ...next, search: state.search, showCompleted: state.showCompleted };
      } else {
        state.search = next.search;
        state.showCompleted = next.showCompleted;
        if (next.cardId !== null && next.cardId !== drawerKey) {
          openDrawer(next.cardId);
          return;
        }
      }
    }
    const path = next.kind === "board" ? boardPath(next.boardId) : "/";
    if (location.pathname !== path) {
      persistHistory();
      persistHistory(next, request.history, path);
    } else {
      persistHistory(next);
    }
    missingKey = undefined;
    chooseRoute(next);
  }

  function applyLocation(reload = false): void {
    const items = last(workspace)?.value.boards ?? [];
    const key = requestedKey();
    const id = key === null ? undefined : key.toLowerCase();
    const found =
      id === undefined
        ? (items.find((item) => item.availability === "open") ?? items[0])
        : uuid.test(id)
          ? items.find((item) => item.id === id)
          : undefined;
    missingKey = found ? undefined : (key ?? undefined);
    if (!found) {
      chooseRoute(workspaceRoute);
      return;
    }
    if (key === null) history.replaceState(null, "", boardPath(found.id));
    const state = stateFor(found.id);
    const restored = restoreHistoryOnEntry
      ? readWorkspaceHistory(history.state, location.pathname)
      : null;
    const restoreDrawer =
      traversingHistory && restored?.route.kind === "board" ? restored.route.cardId : null;
    traversingHistory = false;
    if (restoreHistoryOnEntry && workspace.status === "ready") restoreHistoryOnEntry = false;
    if (restored?.route.kind === "board" && restored.route.boardId === found.id) {
      sidebarScroll = restored.sidebarScroll;
      state.scroll = restored.boardScroll;
      state.focus = restored.focus;
      state.search = restored.route.search;
      state.showCompleted = restored.route.showCompleted;
    }
    const cardId = route.kind === "board" && route.boardId === found.id ? route.cardId : null;
    chooseRoute(
      {
        ...selected(found.id),
        search: state.search,
        showCompleted: state.showCompleted,
        cardId,
      },
      reload,
    );
    if (restoreDrawer !== null) openDrawer(restoreDrawer);
    else if (restored && drawerKey !== null) closeDrawer();
  }

  async function refreshWorkspace(): Promise<void> {
    workspaceRead?.abort();
    const attempt = new AbortController();
    workspaceRead = attempt;
    const epoch = ++workspaceReadEpoch;
    const serviceEpoch = ++serviceReadEpoch;
    const identityEpoch = ++identityReadEpoch;
    const previous = last(workspace);
    const previousService = last(service);
    workspace = { status: "loading", previous };
    service = { status: "loading", previous: previousService };
    session = null;
    render();
    const [identity, catalog, status] = await Promise.allSettled([
      readSession(attempt.signal),
      readWorkspace(attempt.signal),
      readService(attempt.signal),
    ]);
    if (attempt.signal.aborted) return;
    workspaceRead = undefined;
    const ownsIdentity = identityEpoch === identityReadEpoch;
    const ownsService = serviceEpoch === serviceReadEpoch;
    if (ownsIdentity) setSession(identity.status === "fulfilled" ? identity.value : null);
    if (ownsService) {
      service =
        status.status === "fulfilled"
          ? { status: "ready", current: confirmed(status.value) }
          : {
              status: "unavailable",
              previous: previousService,
              failure: readFailure(status.reason),
            };
    }
    const setupFailed =
      ownsIdentity && identity.status === "rejected"
        ? readFailure(identity.reason)
        : ownsService && status.status === "rejected"
          ? readFailure(status.reason)
          : null;
    if (setupFailed !== null && hidesRetained(setupFailed)) {
      redactWorkspace(setupFailed);
      live.update(null);
      return;
    }
    if (epoch !== workspaceReadEpoch) {
      if (setupFailed !== null) live.retry();
      render();
      return;
    }
    if (
      catalog.status === "fulfilled" &&
      catalog.value.workspace.revision < minimumWorkspaceRevision
    ) {
      live.retry();
      return;
    }
    workspace =
      catalog.status === "fulfilled" && setupFailed === null
        ? { status: "ready", current: confirmed(catalog.value) }
        : {
            status: "unavailable",
            previous: catalog.status === "fulfilled" ? confirmed(catalog.value) : previous,
            failure:
              setupFailed ??
              (catalog.status === "rejected" ? readFailure(catalog.reason) : { kind: "transport" }),
          };
    if (workspace.status === "ready") reconcileRetained(workspace.current.value);
    else if (hidesRetained(workspace.failure)) redactWorkspace(workspace.failure);
    if (workspace.status === "ready") {
      applyLocation(true);
    } else {
      applyLocation();
      live.update(null);
    }
  }

  async function refreshLive(request: LiveRefresh, signal: AbortSignal): Promise<boolean> {
    if (signal.aborted) return false;
    liveRefreshSignal = signal;
    const selectedRequest = route.kind === "board" && request.boardId === route.boardId;
    const boardEpoch = selectedRequest ? ++boardReadEpoch : boardReadEpoch;
    const workspaceEpoch = request.workspace ? ++workspaceReadEpoch : workspaceReadEpoch;
    const readIdentity = request.reason === "snapshot" || (session === null && !workspaceRead);
    const serviceEpoch = request.service ? ++serviceReadEpoch : serviceReadEpoch;
    const identityEpoch = readIdentity ? ++identityReadEpoch : identityReadEpoch;
    if (selectedRequest) {
      board = { status: "loading", previous: last(board) };
    }
    render();
    try {
      const [catalog, status, value, identity] = await Promise.all([
        request.workspace ? readWorkspace(signal) : Promise.resolve(null),
        request.service ? readService(signal) : Promise.resolve(null),
        request.boardId
          ? readBoard(request.boardId, signal).catch((error: unknown) => {
              const failure = readFailure(error);
              if (
                failure.kind === "http" &&
                (failure.problem.status === 403 ||
                  failure.problem.status === 404 ||
                  failure.problem.status === 503)
              ) {
                return failure;
              }
              throw error;
            })
          : Promise.resolve(null),
        readIdentity ? readSession(signal) : Promise.resolve(null),
      ]);
      if (signal.aborted) return false;
      const liveCatalog = workspaceEpoch === workspaceReadEpoch ? catalog : null;
      if (liveCatalog !== null && liveCatalog.workspace.revision < minimumWorkspaceRevision)
        return false;
      if (liveCatalog !== null) {
        workspace = { status: "ready", current: confirmed(liveCatalog) };
        reconcileRetained(liveCatalog);
      }
      if (serviceEpoch === serviceReadEpoch && status !== null)
        service = { status: "ready", current: confirmed(status) };
      if (identityEpoch === identityReadEpoch && identity !== null) {
        const principalChanged = cacheActor !== null && cacheActor !== identity.principal_id;
        setSession(identity);
        if (principalChanged) {
          void refreshWorkspace();
          return true;
        }
      }
      if (
        boardEpoch === boardReadEpoch &&
        cacheActor !== null &&
        route.kind === "board" &&
        request.boardId === route.boardId &&
        value !== null
      ) {
        const state = stateFor(route.boardId);
        if ("view" in value) {
          if (value.view.board.revision < (minimumBoardRevisions.get(route.boardId) ?? 0))
            return false;
          const previousBoard = state.confirmed?.value;
          const previous = (confirmedPresentation ?? state.confirmed)?.value;
          if (
            (!previous || value.view.board.revision >= previous.view.board.revision) &&
            !approval.awaitsConfirmation(route.boardId) &&
            !singleApproval.awaitsConfirmation(route.boardId) &&
            !ticketOrderInFlight(route.boardId) &&
            !waitsForDrawerReceipt(route.boardId, value.view.board.revision)
          ) {
            state.confirmed = confirmed(value);
            cache.retain(state.confirmed);
            board = { status: "ready", current: state.confirmed };
            confirmedPresentation = null;
            if (previous && previous.view.board.revision < value.view.board.revision)
              liveMoveRevision = value.view.board.revision;
            if (
              previousBoard &&
              previousBoard.view.board.revision < value.view.board.revision &&
              !(
                previous?.view.board.revision === value.view.board.revision && lastActor === "human"
              )
            ) {
              lastActor = null;
              lastActorAt = null;
              const writer = last(service)?.value.service_instance_id;
              if (writer) {
                const updated = value.view.board;
                void readLiveActor(previousBoard.view.board, updated, writer, signal).then(
                  (actor) => {
                    if (
                      signal.aborted ||
                      route.kind !== "board" ||
                      route.boardId !== updated.id ||
                      (confirmedPresentation ?? last(board))?.value.view.board.revision !==
                        updated.revision ||
                      last(service)?.value.service_instance_id !== writer
                    )
                      return;
                    lastActor = actor;
                    lastActorAt = actor === null ? null : updated.last_card_edit_at;
                    render();
                  },
                  () => {},
                );
              }
            }
            if (
              drawerKey !== null &&
              last(drawerData)?.value.boardRevision !== value.view.board.revision
            )
              openDrawer(drawerKey);
            if (
              state.search !== "" &&
              previousBoard?.view.board.revision !== value.view.board.revision
            )
              changeSearch(state.search);
          }
        } else {
          if (hidesRetained(value)) {
            redactBoard(route.boardId);
          }
          board = {
            status: "unavailable",
            previous: state.confirmed ?? cache.previous(route.boardId),
            failure: value,
          };
          if (
            value.kind === "http" &&
            value.problem.status === 503 &&
            (catalog ?? last(workspace)?.value)?.boards.some(
              (item) => item.id === request.boardId && item.availability === "open",
            )
          ) {
            render();
            return false;
          }
        }
      }
      if (liveCatalog !== null) {
        if (route.kind === "workspace") {
          applyLocation();
        } else {
          const selected = route.boardId;
          const present = liveCatalog.boards.find((item) => item.id === selected);
          if (present?.availability === "open") {
            live.update(selected);
            if (board.status === "unavailable" && request.boardId === null)
              void refreshBoard(selected);
          } else {
            if (!present) redactBoard(selected);
            boardReadEpoch += 1;
            board = {
              status: "unavailable",
              previous: stateFor(selected).confirmed,
              failure: { kind: "transport" },
            };
            render();
            live.update(null);
          }
        }
      }
      return true;
    } catch (error) {
      const failure = readFailure(error);
      if (signal.aborted) return false;
      if (
        hidesRetained(failure) &&
        workspaceEpoch === workspaceReadEpoch &&
        identityEpoch === identityReadEpoch
      ) {
        redactWorkspace(failure);
      } else if (
        !signal.aborted &&
        selectedRequest &&
        boardEpoch === boardReadEpoch &&
        route.kind === "board"
      ) {
        board = {
          status: "unavailable",
          previous: stateFor(route.boardId).confirmed ?? cache.previous(route.boardId),
          failure,
        };
      }
      return false;
    } finally {
      if (liveRefreshSignal === signal) {
        liveRefreshSignal = null;
        if (!signal.aborted) render();
      }
    }
  }

  addEventListener("popstate", () => {
    window.clearTimeout(historyTimer);
    restoreHistoryOnEntry = true;
    traversingHistory = true;
    applyLocation();
  });
  addEventListener("pagehide", (event) => {
    persistHistory();
    if (!event.persisted) live.dispose();
  });
  addEventListener("pageshow", (event) => {
    if (event.persisted) live.retry();
  });
  void refreshWorkspace();
}

const host = document.getElementById("workspace");
if (host) start(host);
