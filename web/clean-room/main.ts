import type { ServiceDto, SessionDto, Uuid } from "./api-types.js";
import { readBoard } from "./board/read.js";
import { mountBoard } from "./board/render.js";
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
import { readService, readSession, readWorkspace } from "./workspace/read.js";
import { mountWorkspace } from "./workspace/render.js";

type BoardRoute = Extract<RouteState, { kind: "board" }>;
interface BoardMemory {
  scroll: BoardScroll;
  focus: FocusTarget | null;
  confirmed: Confirmed<BoardData> | null;
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
  const memory = new Map<Uuid, BoardMemory>();

  function stateFor(id: Uuid): BoardMemory {
    const saved = memory.get(id);
    if (saved) return saved;
    const initial = { scroll: noBoardScroll, focus: null, confirmed: null };
    memory.set(id, initial);
    return initial;
  }

  function render(): void {
    let selectedBoard: BoardProps | null = null;
    if (route.kind === "board") {
      const id = route.boardId;
      const state = stateFor(id);
      selectedBoard = {
        boardName: last(workspace)?.value.boards.find((item) => item.id === id)?.name ?? undefined,
        data: board,
        session,
        route,
        scroll: state.scroll,
        focus: state.focus,
        onNavigate: navigate,
        onScrollChange: (scroll) => {
          state.scroll = scroll;
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
  }

  async function refreshBoard(id: Uuid): Promise<void> {
    boardRead?.abort();
    const attempt = new AbortController();
    boardRead = attempt;
    const state = stateFor(id);
    board = { status: "loading", previous: state.confirmed };
    render();
    try {
      const value = await readBoard(id, attempt.signal);
      if (attempt.signal.aborted) return;
      state.confirmed = confirmed(value);
      board = { status: "ready", current: state.confirmed };
    } catch (error) {
      if (attempt.signal.aborted) return;
      board = { status: "unavailable", previous: state.confirmed, failure: readFailure(error) };
    }
    render();
  }

  function chooseRoute(next: RouteState, reload = false): void {
    const was = route.kind === "board" ? route.boardId : null;
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
  }

  function navigate(request: NavigationRequest): void {
    const next = request.route;
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
    chooseRoute(selected(found.id), reload);
  }

  async function refreshWorkspace(): Promise<void> {
    workspaceRead?.abort();
    const attempt = new AbortController();
    workspaceRead = attempt;
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
    session = identity.status === "fulfilled" ? identity.value : null;
    const setupFailed =
      identity.status === "rejected"
        ? readFailure(identity.reason)
        : status.status === "rejected"
          ? readFailure(status.reason)
          : null;
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
    service =
      status.status === "fulfilled"
        ? { status: "ready", current: confirmed(status.value) }
        : { status: "unavailable", previous: previousService, failure: readFailure(status.reason) };
    if (workspace.status === "ready") {
      applyLocation(true);
    } else {
      applyLocation();
    }
  }

  addEventListener("popstate", () => {
    applyLocation();
  });
  void refreshWorkspace();
}

const host = document.getElementById("workspace");
if (host) start(host);
