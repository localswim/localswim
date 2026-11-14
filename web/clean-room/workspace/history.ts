import type { BoardScroll, FocusTarget, RouteState, ScrollPosition } from "../contracts.js";
import { isRecord, isUnknownArray, isUuid } from "../http.js";

export interface WorkspaceHistoryState {
  readonly route: RouteState;
  readonly sidebarScroll: ScrollPosition;
  readonly boardScroll: BoardScroll;
  readonly focus: FocusTarget | null;
}

const historyKey = "localswimWorkspace";

function readRoute(value: unknown, pathname: string): RouteState | null {
  if (!isRecord(value)) return null;
  if (value.kind === "workspace") return pathname === "/" ? { kind: "workspace" } : null;

  const boardId = /^\/boards?\/([^/]+)$/u.exec(pathname)?.[1]?.toLowerCase();
  if (
    value.kind !== "board" ||
    !isUuid(boardId) ||
    value.boardId !== boardId ||
    (value.cardId !== null && !isUuid(value.cardId)) ||
    typeof value.search !== "string" ||
    value.search.length > 1024 ||
    typeof value.showCompleted !== "boolean"
  ) {
    return null;
  }
  return {
    kind: "board",
    boardId,
    cardId: value.cardId,
    search: value.search,
    showCompleted: value.showCompleted,
  };
}

function isPosition(value: unknown): value is ScrollPosition {
  return (
    isRecord(value) &&
    typeof value.left === "number" &&
    Number.isFinite(value.left) &&
    value.left >= 0 &&
    typeof value.top === "number" &&
    Number.isFinite(value.top) &&
    value.top >= 0
  );
}

function isLanePosition(value: unknown): value is BoardScroll["lanes"][number] {
  return isRecord(value) && isUuid(value.laneId) && isPosition(value.position);
}

function isBoardScroll(value: unknown): value is BoardScroll {
  return (
    isRecord(value) &&
    isPosition(value.board) &&
    isUnknownArray(value.lanes) &&
    value.lanes.every(isLanePosition) &&
    new Set(value.lanes.map(({ laneId }) => laneId)).size === value.lanes.length
  );
}

function isFocus(value: unknown): value is FocusTarget {
  return (
    isRecord(value) &&
    typeof value.control === "string" &&
    value.control.length > 0 &&
    value.control.length <= 64 &&
    (value.resourceId === null || isUuid(value.resourceId))
  );
}

export function readWorkspaceHistory(
  state: unknown,
  pathname: string,
): WorkspaceHistoryState | null {
  if (!isRecord(state)) return null;
  const value = state[historyKey];
  if (!isRecord(value)) return null;
  const { sidebarScroll, boardScroll, focus } = value;
  const route = readRoute(value.route, pathname);
  if (
    route === null ||
    !isPosition(sidebarScroll) ||
    !isBoardScroll(boardScroll) ||
    (focus !== null && !isFocus(focus))
  ) {
    return null;
  }
  return { route, sidebarScroll, boardScroll, focus };
}

export function writeWorkspaceHistory(
  history: History,
  state: WorkspaceHistoryState,
  mode: "push" | "replace",
  path?: string,
): boolean {
  try {
    const entry = { [historyKey]: state };
    if (mode === "push") history.pushState(entry, "", path);
    else history.replaceState(entry, "", path);
    return true;
  } catch {
    // A browser can decline a write, for example while the document is unloading.
    return false;
  }
}
