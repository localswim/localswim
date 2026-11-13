import type { ServiceDto, SessionDto, Uuid } from "./api-types.js";
import { readBoard } from "./board/read.js";
import { mountBoard } from "./board/render.js";
import { createLaneApproval } from "./board/approve-lane.js";
import { cardSearchQuery, projectCards, readCardMatches } from "./board/filter.js";
import {
  inspectCardMove,
  prepareCardMove,
  requestCardMove,
  type MoveIntent,
  type PreparedCardMove,
} from "./board/move.js";
import { openTicketOrder, pendingTicketOrderLanes } from "./board/ticket-order.js";
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
import { readFailure } from "./http.js";
import type { CreateDraft, CreateInput, CreateProps, CreateReceipt } from "./create/contracts.js";
import { mountCreate } from "./create/render.js";
import { createCard, inspectCardCreation } from "./create/write.js";
import type { DrawerData } from "./drawer/contracts.js";
import { readDrawer, readDrawerTicket } from "./drawer/read.js";
import { mountDrawer } from "./drawer/render.js";
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
  let lastActor: ActorKind | null = null;
  let lastActorAt: string | null = null;
  let boardReadEpoch = 0;
  let workspaceReadEpoch = 0;
  let serviceReadEpoch = 0;
  let identityReadEpoch = 0;
  let minimumWorkspaceRevision = 0;
  const minimumBoardRevisions = new Map<Uuid, number>();
  let searchRead: AbortController | undefined;
  let drawerRead: AbortController | undefined;
  let drawerData: ReadState<DrawerData> = { status: "loading", previous: null };
  let drawer: ReturnType<typeof mountDrawer> | undefined;
  let drawerKey: Uuid | null = null;
  let ticketRead: AbortController | undefined;
  let creating: ReturnType<typeof mountCreate> | undefined;
  let draft: CreateDraft | undefined;
  let createLane: Uuid | undefined;
  let createBoard: Uuid | undefined;
  let createPhase: CreateProps["phase"] = "editing";
  let createError: string | null = null;
  let createIntent: CreateInput | undefined;
  let createOpen = false;
  let confirmedCreation: CreateReceipt | undefined;
  let ordering: ReturnType<typeof openTicketOrder> | undefined;
  let moveBusy = false;
  const pendingMoves = new Map<Uuid, { prepared: PreparedCardMove; requestId: Uuid }>();
  const inspectingMoves = new Set<Uuid>();
  const memory = new Map<Uuid, BoardMemory>();
  const overlay = document.createElement("div");
  overlay.className = "lsw-interaction-overlay";
  const notice = document.createElement("div");
  notice.className = "lsw-operation-notice";
  notice.setAttribute("role", "status");
  const orderNotice = document.createElement("p");
  orderNotice.className = "lsw-ticket-order-status";
  orderNotice.setAttribute("role", "status");
  let orderNoticeTimer = 0;
  const checkMove = document.createElement("button");
  checkMove.type = "button";
  checkMove.textContent = "Check move status";
  checkMove.hidden = true;
  const checkCreate = document.createElement("button");
  checkCreate.type = "button";
  checkCreate.textContent = "Check creation status";
  checkCreate.hidden = true;
  const orderChecks = new Map<Uuid, HTMLButtonElement>();
  const recovery = document.createElement("div");
  recovery.className = "lsw-operation-recovery";
  recovery.append(checkMove, checkCreate);
  overlay.append(notice, orderNotice, recovery);
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
    current: () => {
      const data = currentBoard();
      return data !== null && session !== null ? { data, session } : null;
    },
    refresh: refreshSelected,
    onStateChange: render,
  });

  function currentBoard(): BoardData | null {
    const id = route.kind === "board" ? route.boardId : null;
    const present = last(workspace)?.value.boards.some(
      (item) => item.id === id && item.availability === "open",
    );
    return board.status === "ready" &&
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
      const data = last(board)?.value;
      const projection =
        data?.view.board.id === id
          ? projectCards(data.view, route.showCompleted, state.matching, Date.now())
          : undefined;
      if (state.searchConfirmed) searchFound = projection?.found;
      selectedBoard = {
        boardName: last(workspace)?.value.boards.find((item) => item.id === id)?.name ?? undefined,
        data: board,
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
          if (state.search === "") state.scroll = scroll;
        },
        onFocusChange: (focus) => {
          state.focus = focus;
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
      lastActor,
      lastActorAt,
      searchFound,
      onSearch: changeSearch,
      overlayOpen: drawerKey !== null || createOpen || ordering !== undefined,
      scroll: sidebarScroll,
      slots: { mountBoard },
      onNavigate: navigate,
      onScrollChange: (scroll) => {
        sidebarScroll = scroll;
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
          state.search !== value
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

  function closeDrawer(): void {
    if (route.kind !== "board") return;
    route = { ...route, cardId: null };
    drawerRead?.abort();
    ticketRead?.abort();
    const opener = drawerKey;
    drawerKey = null;
    drawer?.dispose();
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
    const boardId = route.boardId;
    route = { ...route, cardId: id };
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
      const state = stateFor(boardId);
      let refreshSearch = false;
      if (state.confirmed?.value.boardEtag !== data.boardEtag) {
        const current = await readBoard(boardId, signal);
        if (signal.aborted || route.kind !== "board" || route.boardId !== boardId) return;
        if (
          current.boardEtag !== data.boardEtag ||
          epoch !== boardReadEpoch ||
          board.status !== "ready" ||
          (state.confirmed?.value.view.board.revision ?? 0) > current.view.board.revision
        ) {
          drawerData = { status: "unavailable", previous, failure: { kind: "invalid_response" } };
          render();
          return;
        }
        refreshSearch = state.search !== "";
        state.confirmed = confirmed(current);
        board = { status: "ready", current: state.confirmed };
      }
      drawerData = { status: "ready", current: confirmed(data) };
      if (refreshSearch) {
        changeSearch(state.search);
        return;
      }
    } catch (error) {
      if (signal.aborted) return;
      drawerData = { status: "unavailable", previous, failure: readFailure(error) };
    }
    render();
  }

  function renderDrawer(): void {
    const id = drawerKey;
    if (id === null) return;
    const props = {
      data: drawerData,
      board:
        last(board)?.value.boardEtag === last(drawerData)?.value.boardEtag
          ? (last(board)?.value ?? null)
          : null,
      session,
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
      writable: currentBoard()?.view.board.id === createBoard,
      error: createError,
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

  function settleCreation(receipt: CreateReceipt, recovered = false): void {
    createPhase = "editing";
    closeCreate();
    notice.textContent = "";
    confirmedCreation = recovered ? receipt : undefined;
    refreshSelected();
    if (recovered) {
      notice.textContent = `Created #${String(receipt.card.ticket).padStart(4, "0")}. The board is refreshing.`;
    } else if (route.kind === "board" && route.boardId === receipt.card.board_id) {
      openDrawer(receipt.card.id);
    }
  }

  async function inspectCreate(): Promise<void> {
    const intent = createIntent;
    if (!intent || createPhase !== "unknown") return;
    createPhase = "submitting";
    renderCreate();
    try {
      const result = await inspectCardCreation(intent);
      if (result.outcome === "committed") {
        settleCreation(result.value, true);
        return;
      }
    } catch {
      // Keep the submitted intent available for the next authoritative inspection.
    }
    createPhase = "unknown";
    createError = "Creation is not yet confirmed. Check the original card before retrying.";
    renderCreate();
    if (!createOpen) notice.textContent = createError;
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
      createBoard !== data.view.board.id
    ) {
      createError = "The board must be current before creating a card.";
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
    const intent: CreateInput = {
      ...draft,
      cardId: crypto.randomUUID(),
      requestId: crypto.randomUUID(),
      boardId: data.view.board.id,
      boardEtag: data.boardEtag,
      boardRevision: data.view.board.revision,
      laneId: lane.id,
      completed: lane.role === "completed",
      priorities: data.priorities,
    };
    createIntent = intent;
    createPhase = "submitting";
    createError = null;
    renderCreate();
    let dispatched = false;
    try {
      const result = await createCard(intent, undefined, (actual) => {
        const current = currentBoard();
        if (
          !createOpen ||
          actual.principal_id !== actorId ||
          current?.boardEtag !== intent.boardEtag ||
          current.view.board.id !== intent.boardId
        ) {
          throw new Error("Creation context changed");
        }
        dispatched = true;
      });
      if (result.outcome === "committed") {
        settleCreation(result.value);
        return;
      }
      if (result.outcome === "not_applied") {
        createPhase = "editing";
        createIntent = undefined;
        createError = result.problem.detail;
        refreshSelected();
        renderCreate();
        return;
      }
    } catch {
      // A lost result leaves the original UUID available for inspection.
    }
    if (!dispatched) {
      createPhase = "editing";
      createIntent = undefined;
      createError = "Creation was not sent. Refresh the board and try again.";
      render();
      return;
    }
    createPhase = "unknown";
    await inspectCreate();
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
      onApplied: (count) => {
        clearTimeout(orderNoticeTimer);
        orderNotice.textContent = `Reordered ${count} ticket ${count === 1 ? "number" : "numbers"}`;
        orderNoticeTimer = window.setTimeout(() => {
          orderNotice.textContent = "";
        }, 5000);
        refreshSelected();
      },
      onRefresh: refreshSelected,
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
      if (isSelected(data.view.board.id))
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
  async function refreshBoard(id: Uuid): Promise<void> {
    boardRead?.abort();
    const attempt = new AbortController();
    boardRead = attempt;
    const epoch = ++boardReadEpoch;
    const state = stateFor(id);
    board = { status: "loading", previous: state.confirmed };
    render();
    try {
      const value = await readBoard(id, attempt.signal);
      if (attempt.signal.aborted || epoch !== boardReadEpoch) return;
      if (value.view.board.revision < (minimumBoardRevisions.get(id) ?? 0)) {
        live.retry();
        return;
      }
      if ((state.confirmed?.value.view.board.revision ?? 0) > value.view.board.revision) {
        board = { status: "ready", current: state.confirmed! };
        render();
        return;
      }
      state.confirmed = confirmed(value);
      board = { status: "ready", current: state.confirmed };
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
      board = { status: "unavailable", previous: state.confirmed, failure };
      if (
        connection === "connected" &&
        failure.kind === "http" &&
        failure.problem.status === 503 &&
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
      notice.textContent = "";
      clearTimeout(orderNoticeTimer);
      orderNotice.textContent = "";
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
      ordering?.dispose();
      ordering = undefined;
    }
    route = next;
    if (route.kind === "board" && workspace.status === "unavailable") {
      boardRead?.abort();
      board = {
        status: "unavailable",
        previous: stateFor(route.boardId).confirmed,
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
      if (request.history === "push") history.pushState(null, "", path);
      else history.replaceState(null, "", path);
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
    if (ownsIdentity) session = identity.status === "fulfilled" ? identity.value : null;
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
    if (workspace.status === "ready") {
      applyLocation(true);
    } else {
      applyLocation();
      live.update(null);
    }
  }

  async function refreshLive(request: LiveRefresh, signal: AbortSignal): Promise<boolean> {
    if (signal.aborted) return false;
    const selectedRequest = route.kind === "board" && request.boardId === route.boardId;
    const boardEpoch = selectedRequest ? ++boardReadEpoch : boardReadEpoch;
    const workspaceEpoch = request.workspace ? ++workspaceReadEpoch : workspaceReadEpoch;
    const readIdentity = request.reason === "snapshot" || (session === null && !workspaceRead);
    const serviceEpoch = request.service ? ++serviceReadEpoch : serviceReadEpoch;
    const identityEpoch = readIdentity ? ++identityReadEpoch : identityReadEpoch;
    if (selectedRequest) {
      board = { status: "loading", previous: last(board) };
      render();
    }
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
      if (liveCatalog !== null) workspace = { status: "ready", current: confirmed(liveCatalog) };
      if (serviceEpoch === serviceReadEpoch && status !== null)
        service = { status: "ready", current: confirmed(status) };
      if (identityEpoch === identityReadEpoch && identity !== null) session = identity;
      if (
        boardEpoch === boardReadEpoch &&
        route.kind === "board" &&
        request.boardId === route.boardId &&
        value !== null
      ) {
        const state = stateFor(route.boardId);
        if ("view" in value) {
          if (value.view.board.revision < (minimumBoardRevisions.get(route.boardId) ?? 0))
            return false;
          const previous = state.confirmed?.value;
          if (!previous || value.view.board.revision >= previous.view.board.revision) {
            state.confirmed = confirmed(value);
            board = { status: "ready", current: state.confirmed };
            if (previous && previous.view.board.revision < value.view.board.revision) {
              lastActor = null;
              lastActorAt = null;
              const writer = last(service)?.value.service_instance_id;
              if (writer) {
                const updated = value.view.board;
                void readLiveActor(previous.view.board, updated, writer, signal).then(
                  (actor) => {
                    if (
                      signal.aborted ||
                      route.kind !== "board" ||
                      route.boardId !== updated.id ||
                      last(board)?.value.view.board.revision !== updated.revision
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
            if (state.search !== "" && previous?.view.board.revision !== value.view.board.revision)
              changeSearch(state.search);
          }
        } else {
          board = { status: "unavailable", previous: state.confirmed, failure: value };
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
            boardReadEpoch += 1;
            board = {
              status: "unavailable",
              previous: stateFor(selected).confirmed,
              failure: { kind: "transport" },
            };
            live.update(null);
          }
        }
      }
      render();
      return true;
    } catch {
      return false;
    }
  }

  addEventListener("popstate", () => {
    applyLocation();
  });
  addEventListener("pagehide", (event) => {
    if (!event.persisted) live.dispose();
  });
  addEventListener("pageshow", (event) => {
    if (event.persisted) live.retry();
  });
  void refreshWorkspace();
}

const host = document.getElementById("workspace");
if (host) start(host);
