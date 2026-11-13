import type { AttentionDto, BoardSummaryDto, Uuid } from "../api-types.js";
import type {
  BoardProps,
  MountComponent,
  MountedComponent,
  ReadState,
  WorkspaceProps,
} from "../contracts.js";

function element<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className: string,
  content?: string,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  node.className = className;
  if (content !== undefined) {
    node.textContent = content;
  }
  return node;
}

function displayValue<T>(read: ReadState<T>): T | undefined {
  return read.status === "ready" ? read.current.value : read.previous?.value;
}

const attentionTypes = [
  { key: "blocked", text: "Blocked" },
  { key: "needs_input", text: "Terry" },
  { key: "ready_for_review", text: "Review" },
] as const satisfies readonly { key: keyof AttentionDto; text: string }[];

const localTime = new Intl.DateTimeFormat("en-US", {
  hour: "numeric",
  minute: "2-digit",
  second: "2-digit",
  hour12: true,
});
const relativeTime = new Intl.RelativeTimeFormat("en-US");

function cardAge(timestamp: string | null): string {
  if (timestamp === null) {
    return "";
  }
  const elapsed = Date.now() - Date.parse(timestamp.replace(" ", "T"));
  if (!Number.isFinite(elapsed)) {
    return "";
  }
  const seconds = Math.max(0, Math.floor(elapsed / 1000));
  if (seconds < 60) {
    return relativeTime.format(-seconds, "second");
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return relativeTime.format(-minutes, "minute");
  }
  const hours = Math.floor(minutes / 60);
  return hours < 24
    ? relativeTime.format(-hours, "hour")
    : relativeTime.format(-Math.floor(hours / 24), "day");
}

function searchInput(className: string, placeholder: string): HTMLInputElement {
  const input = element("input", className);
  input.type = "search";
  input.placeholder = placeholder;
  input.setAttribute("aria-label", placeholder);
  input.disabled = true;
  return input;
}

function makeHeader() {
  const header = element("header", "lsw-workspace-header");
  const indicator = element("span", "lsw-workspace-indicator");
  indicator.setAttribute("aria-hidden", "true");
  const brand = element("div", "lsw-workspace-brand");
  const brandMark = element("span", "lsw-workspace-mark");
  brandMark.setAttribute("aria-hidden", "true");
  brandMark.append(element("i", ""), element("i", ""), element("i", ""));
  brand.append(brandMark, document.createTextNode("localswim"));
  const title = element("div", "lsw-workspace-title");

  const search = element("div", "lsw-workspace-card-search");
  const searchField = searchInput("lsw-workspace-card-query", "Find in cards");
  const shortcut = element("kbd", "lsw-workspace-search-shortcut", "/");
  shortcut.setAttribute("aria-hidden", "true");
  search.append(searchField, shortcut);

  const build = element("span", "lsw-workspace-build");
  const latest = element("span", "lsw-workspace-latest");
  const clock = element("time", "lsw-workspace-clock");
  const status = element("span", "lsw-workspace-connection");
  status.setAttribute("role", "status");
  header.append(indicator, brand, title, search, build, latest, clock, status);
  return { header, title, searchField, build, latest, clock, status };
}

function makeSidebar() {
  const sidebar = element("aside", "lsw-workspace-sidebar");
  const heading = element("div", "lsw-workspace-sidebar-heading");
  const title = element("h2", "lsw-workspace-sidebar-title", "Boards");
  const toggle = element("button", "lsw-workspace-sidebar-toggle", "☰");
  toggle.type = "button";
  toggle.disabled = true;
  toggle.setAttribute("aria-label", "Collapse sidebar");
  heading.append(title, toggle);
  const search = searchInput("lsw-workspace-board-query", "Find a board");
  const legend = element("div", "lsw-workspace-legend");
  for (const { key, text } of attentionTypes) {
    legend.append(element("span", `lsw-workspace-attention-${key}`, text));
  }
  const list = element("nav", "lsw-workspace-board-list");
  list.setAttribute("aria-label", "Boards");
  const footer = element("div", "lsw-workspace-sidebar-footer");
  const empty = element("div", "lsw-workspace-empty");
  const external = element("a", "lsw-workspace-external", "Open board in new window ↗");
  external.target = "_blank";
  external.rel = "noopener";
  const cache = element("button", "lsw-workspace-cache", "Cache settings");
  cache.type = "button";
  cache.disabled = true;
  cache.setAttribute("aria-expanded", "false");
  footer.append(empty, external, cache);
  const grip = element("span", "lsw-workspace-sidebar-grip");
  grip.setAttribute("aria-hidden", "true");
  sidebar.append(heading, search, legend, list, footer, grip);
  return { sidebar, list, empty, external };
}

interface BoardRow {
  readonly container: HTMLDivElement;
  readonly select: HTMLButtonElement;
  readonly title: HTMLSpanElement;
  readonly counts: readonly HTMLSpanElement[];
  readonly pin: HTMLButtonElement;
}

function makeRow(id: Uuid): BoardRow {
  const container = element("div", "lsw-workspace-board-row");
  const select = element("button", "lsw-workspace-board-select");
  select.type = "button";
  select.dataset.boardId = id;
  const title = element("span", "lsw-workspace-board-title");
  const attention = element("span", "lsw-workspace-board-attention");
  const counts = attentionTypes.map(({ key }) =>
    element("span", `lsw-workspace-count lsw-workspace-attention-${key}`),
  );
  attention.append(...counts);
  select.append(title, attention);
  const pin = element("button", "lsw-workspace-pin");
  pin.type = "button";
  pin.disabled = true;
  pin.append(element("span", "lsw-workspace-pin-mark"));
  container.append(select, pin);
  return { container, select, title, counts, pin };
}

function updateRow(row: BoardRow, board: BoardSummaryDto, active: boolean, pinned: boolean): void {
  const name = board.name ?? board.id;
  row.title.textContent = name;
  row.select.setAttribute("aria-label", name);
  if (active) {
    row.select.setAttribute("aria-current", "page");
  } else {
    row.select.removeAttribute("aria-current");
  }
  row.container.classList.toggle("lsw-workspace-board-current", active);
  row.pin.setAttribute("aria-label", `${pinned ? "Unpin" : "Pin"} ${name}`);
  row.pin.setAttribute("aria-pressed", String(pinned));
  for (const [index, { key, text }] of attentionTypes.entries()) {
    const badge = row.counts[index];
    if (badge === undefined) {
      continue;
    }
    const value = board.attention?.[key];
    badge.hidden = !value;
    badge.textContent = value === undefined ? "" : String(value);
    badge.setAttribute("aria-label", `${text} ${value ?? 0}`);
  }
}

function defaultBoardPath(id: Uuid): string {
  return `/board/${encodeURIComponent(id)}`;
}

function pageTitle(
  workspaceStatus: ReadState<unknown>["status"],
  boardProps: BoardProps | null,
  boardName: string,
  needsTerry: number,
): string {
  if (boardProps?.data.status === "loading") {
    return `${boardName} · Loading`;
  }
  if (boardProps?.data.status === "ready") {
    return needsTerry > 0 ? `(${needsTerry}) ${boardName}` : boardName;
  }
  return workspaceStatus === "loading" ? "localswim" : "Board unavailable · localswim";
}

export const mountWorkspace: MountComponent<WorkspaceProps> = (host, initial) => {
  let props = initial;
  const listeners = new AbortController();
  const { signal } = listeners;
  const root = element("div", "lsw-workspace");
  const header = makeHeader();
  const sidebar = makeSidebar();
  const body = element("div", "lsw-workspace-body");
  const main = element("main", "lsw-workspace-main");
  const boardHost = element("div", "lsw-workspace-board-host");
  const message = element("section", "lsw-workspace-message");
  const messageTitle = element("h1", "");
  messageTitle.setAttribute("role", "status");
  const messageDetail = element("p", "");
  const retry = element("button", "", "Retry");
  retry.type = "button";
  retry.addEventListener("click", () => props.onRefresh(), { signal });
  message.append(messageTitle, messageDetail, retry);
  main.append(boardHost, message);
  body.append(sidebar.sidebar, main);
  root.append(header.header, body);
  const previousTitle = document.title;
  host.append(root);

  let board: { id: Uuid; component: MountedComponent<BoardProps> } | null = null;
  let lastCardEdit: string | null = null;
  const rows = new Map<Uuid, BoardRow>();

  function navigate(id: Uuid): void {
    if (props.route.kind === "board" && props.route.boardId === id) {
      return;
    }
    props.onNavigate({
      route: { kind: "board", boardId: id, cardId: null, search: "", showCompleted: false },
      history: "push",
    });
  }

  function updateClock(): void {
    header.clock.textContent = localTime.format().replace(/\s/g, "").toLowerCase();
    const age = cardAge(lastCardEdit);
    header.latest.textContent = age ? `· Last card update: ${age}` : "";
  }

  function render(): void {
    const workspace = displayValue(props.data);
    const selectedId = props.route.kind === "board" ? props.route.boardId : null;
    const boardProps =
      props.selectedBoard?.route.boardId === selectedId ? props.selectedBoard : null;
    const selected = workspace?.boards.find(({ id }) => id === selectedId);
    const selectedView =
      boardProps === null ? undefined : displayValue(boardProps.data)?.view.board;
    const selectedName = selectedView?.name ?? selected?.name ?? props.unavailableBoardKey;
    header.title.textContent = selectedName ?? "Workspace";
    document.title = pageTitle(
      props.data.status,
      boardProps,
      selectedName ?? "Workspace",
      selectedView?.attention.needs_input ?? selected?.attention?.needs_input ?? 0,
    );
    header.searchField.value = props.route.kind === "board" ? props.route.search : "";
    const service = displayValue(props.service);
    header.build.textContent = service === undefined ? "" : `Build ${service.build_id.slice(0, 7)}`;
    const connected = props.service.status === "ready" && service?.state === "running";
    root.classList.toggle("lsw-workspace-connected", connected);
    header.status.textContent = connected ? "CONNECTED" : "DISCONNECTED";
    const pathForBoard = props.boardPath ?? defaultBoardPath;
    const pinned = new Set(workspace?.workspace.pinned_board_ids);
    const ids = new Set<Uuid>();
    lastCardEdit = selectedView?.last_card_edit_at ?? null;
    let nextRow = sidebar.list.firstElementChild;
    for (const summary of workspace?.boards ?? []) {
      ids.add(summary.id);
      if (
        selectedId !== null &&
        summary.last_card_edit_at !== null &&
        (lastCardEdit === null || summary.last_card_edit_at > lastCardEdit)
      ) {
        lastCardEdit = summary.last_card_edit_at;
      }
      let row = rows.get(summary.id);
      if (row === undefined) {
        row = makeRow(summary.id);
        rows.set(summary.id, row);
      }
      updateRow(row, summary, summary.id === selectedId, pinned.has(summary.id));
      if (nextRow !== row.container) {
        sidebar.list.insertBefore(row.container, nextRow);
      }
      nextRow = row.container.nextElementSibling;
    }
    updateClock();
    for (const [id, row] of rows) {
      if (!ids.has(id)) {
        row.container.remove();
        rows.delete(id);
      }
    }
    sidebar.empty.textContent =
      props.data.status === "ready" && ids.size === 0 ? "No boards found in this folder." : "";
    if (selectedId === null) {
      if (props.unavailableBoardKey === undefined) sidebar.external.removeAttribute("href");
      else sidebar.external.href = location.pathname;
    } else {
      sidebar.external.href = pathForBoard(selectedId);
    }

    if (board !== null && board.id !== boardProps?.route.boardId) {
      board.component.dispose();
      board = null;
    }
    message.hidden = boardProps !== null;
    boardHost.hidden = boardProps === null;
    if (boardProps !== null) {
      if (board === null) {
        board = {
          id: boardProps.route.boardId,
          component: props.slots.mountBoard(boardHost, boardProps),
        };
      } else {
        board.component.update(boardProps);
      }
      return;
    }

    const loading = props.data.status === "loading";
    message.classList.toggle("lsw-workspace-message-loading", loading);
    messageTitle.textContent = loading
      ? `Loading ${selectedName ?? "workspace"}...`
      : "Board unavailable";
    messageDetail.hidden = loading;
    retry.hidden = loading;
    if (props.data.status === "unavailable") {
      messageDetail.textContent = "The workspace could not be loaded. Retry to reconnect.";
    } else if (props.unavailableBoardKey !== undefined) {
      messageDetail.textContent =
        "This bookmark does not identify a board in this workspace. Choose a board from the sidebar.";
    } else {
      messageDetail.textContent =
        "No supported boards were found. Add a board to the workspace folder, then retry.";
    }
  }

  function restoreScroll(): void {
    sidebar.list.scrollTo(props.scroll.left, props.scroll.top);
  }

  sidebar.list.addEventListener(
    "click",
    (event) => {
      const id =
        event.target instanceof Element
          ? event.target.closest<HTMLButtonElement>("button[data-board-id]")?.dataset.boardId
          : undefined;
      if (id !== undefined) {
        navigate(id);
      }
    },
    { signal },
  );
  sidebar.list.addEventListener(
    "scroll",
    () => props.onScrollChange({ left: sidebar.list.scrollLeft, top: sidebar.list.scrollTop }),
    { signal },
  );
  render();
  restoreScroll();
  const clockTimer = window.setInterval(updateClock, 1000);
  return {
    update(next) {
      const restore =
        props.scroll.left !== next.scroll.left ||
        props.scroll.top !== next.scroll.top ||
        (displayValue(props.data) === undefined && displayValue(next.data) !== undefined);
      props = next;
      render();
      if (restore) {
        restoreScroll();
      }
    },
    dispose() {
      listeners.abort();
      window.clearInterval(clockTimer);
      board?.component.dispose();
      root.remove();
      document.title = previousTitle;
    },
  };
};
