import type { AttentionDto, BoardSummaryDto, Uuid } from "../api-types.js";
import { mountPins } from "./pins.js";
import { mountSidebar } from "./sidebar.js";
import { mountWorkspaceStatus, workspaceTabTitle } from "../live/status.js";
import { mountCacheSettings } from "./retained-cache.js";
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
  const clear = element("button", "lsw-workspace-card-clear", "×");
  clear.type = "button";
  clear.setAttribute("aria-label", "Clear search");
  const found = element("span", "lsw-workspace-card-found");
  found.setAttribute("role", "status");
  search.append(searchField, shortcut, clear, found);

  const build = element("span", "lsw-workspace-build");
  const latest = element("span", "lsw-workspace-latest");
  const clock = element("time", "lsw-workspace-clock");
  const status = element("span", "lsw-workspace-connection");
  status.setAttribute("role", "status");
  header.append(indicator, brand, title, search, build, latest, clock, status);
  return { header, title, searchField, shortcut, clear, found, build, latest, clock, status };
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
  const pinStatus = element("div", "lsw-workspace-pin-status");
  sidebar.insertBefore(pinStatus, footer);
  return { sidebar, list, empty, external, title, toggle, grip, search, pinStatus, cache };
}

interface BoardRow {
  readonly container: HTMLDivElement;
  readonly select: HTMLButtonElement;
  readonly title: HTMLSpanElement;
  readonly counts: readonly HTMLSpanElement[];
  readonly pin: HTMLButtonElement;
  readonly reorder: HTMLButtonElement;
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
  pin.dataset.pinAction = id;
  const mark = element("span", "lsw-workspace-pin-mark");
  mark.setAttribute("aria-hidden", "true");
  pin.append(mark);
  container.dataset.pinBoardId = id;
  const reorder = element("button", "lsw-workspace-pin-reorder");
  reorder.type = "button";
  reorder.dataset.pinReorder = id;
  container.append(select, reorder, pin);
  return { container, select, title, counts, pin, reorder };
}

function updateRow(row: BoardRow, board: BoardSummaryDto, active: boolean, pinned: boolean): void {
  const name = board.name ?? board.id;
  row.title.textContent = name;
  const attention = board.attention;
  const countText =
    attention === null
      ? "Attention unavailable"
      : `Blocked: ${attention.blocked} Needs Terry: ${attention.needs_input} Ready for Review: ${attention.ready_for_review}`;
  row.select.setAttribute("aria-label", `${name} ${countText}`);
  const edit =
    board.last_card_edit_at === null
      ? "unknown"
      : `${Math.max(0, Math.floor((Date.now() - Date.parse(board.last_card_edit_at.replace(" ", "T"))) / 60_000))} minutes ago`;
  row.select.title = `${name} · ${board.id} · Last card edit: ${edit}`;
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
    badge.hidden = value === 0;
    badge.textContent = value === undefined ? "—" : value > 99 ? "99+" : String(value);
    badge.setAttribute("aria-label", `${text} ${value ?? "unavailable"}`);
  }
}

function defaultBoardPath(id: Uuid): string {
  return `/board/${encodeURIComponent(id)}`;
}

export const mountWorkspace: MountComponent<WorkspaceProps> = (host, initial) => {
  let props = initial;
  const listeners = new AbortController();
  const { signal } = listeners;
  const root = element("div", "lsw-workspace");
  const header = makeHeader();
  const sidebar = makeSidebar();
  const cacheSettings = initial.cache && mountCacheSettings(sidebar.cache, initial.cache);
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
  host.append(root);
  const statusPresenter = mountWorkspaceStatus(root, header);
  const sidebarControls = mountSidebar({
    root,
    body,
    sidebar: sidebar.sidebar,
    title: sidebar.title,
    toggle: sidebar.toggle,
    grip: sidebar.grip,
  });
  sidebar.search.disabled = false;
  const pins = mountPins({
    search: sidebar.search,
    list: sidebar.list,
    status: sidebar.pinStatus,
    render,
  });
  const pinnedHeading = element("h2", "lsw-workspace-pin-group", "Pinned");
  const otherHeading = element("h2", "lsw-workspace-pin-group", "Other boards");
  otherHeading.dataset.pinGroup = "other";

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
    const title = workspaceTabTitle(
      props.data.status,
      boardProps?.data.status,
      selectedName ?? "Workspace",
      selectedView?.attention.needs_input ?? selected?.attention?.needs_input ?? 0,
    );
    const query = props.route.kind === "board" ? props.route.search : "";
    if (header.searchField.value !== query) header.searchField.value = query;
    header.searchField.disabled = props.route.kind !== "board";
    header.shortcut.hidden = query !== "";
    header.clear.hidden = query === "";
    header.found.hidden = query.trim() === "" || props.searchFound === undefined;
    header.found.textContent = header.found.hidden
      ? ""
      : props.searchFound === 0
        ? "no match"
        : `${props.searchFound} found`;
    const service = displayValue(props.service);
    const connection =
      props.connection ??
      (props.service.status === "ready" && service?.state === "running"
        ? "connected"
        : "disconnected");
    const connected = connection === "connected";
    const pathForBoard = props.boardPath ?? defaultBoardPath;
    const ids = new Set<Uuid>();
    lastCardEdit = selectedView?.last_card_edit_at ?? null;
    for (const summary of workspace?.boards ?? []) {
      if (
        selectedId !== null &&
        summary.id !== selectedId &&
        summary.last_card_edit_at !== null &&
        (lastCardEdit === null || summary.last_card_edit_at > lastCardEdit)
      ) {
        lastCardEdit = summary.last_card_edit_at;
      }
    }
    const visible = pins.update(props.data, connected && props.session !== null);
    const hasPinned = pins.hasPins();
    const grouped = visible.some((item) => item.pinned);
    const ordered: HTMLElement[] = [];
    if (grouped) ordered.push(pinnedHeading);
    let otherShown = false;
    for (const item of visible) {
      const { summary, pinned, canToggle, canReorder } = item;
      ids.add(summary.id);
      if (hasPinned && !pinned && !otherShown) {
        ordered.push(otherHeading);
        otherShown = true;
      }
      let row = rows.get(summary.id);
      if (row === undefined) {
        row = makeRow(summary.id);
        rows.set(summary.id, row);
      }
      const displaySummary =
        summary.id === selectedId && selectedView
          ? {
              ...summary,
              attention: selectedView.attention,
              last_card_edit_at: selectedView.last_card_edit_at,
            }
          : summary;
      updateRow(row, displaySummary, summary.id === selectedId, pinned);
      row.pin.setAttribute("aria-disabled", String(!canToggle));
      row.reorder.hidden = !pinned;
      row.reorder.setAttribute("aria-disabled", String(!canReorder));
      row.reorder.draggable = canReorder;
      row.reorder.setAttribute(
        "aria-label",
        `Reorder ${summary.name ?? summary.id}. Use Up and Down arrows, Home or End.`,
      );
      ordered.push(row.container);
    }
    const focused = document.activeElement;
    const retainedFocus =
      focused instanceof HTMLElement && sidebar.list.contains(focused) ? focused : null;
    const nodes = new Set(ordered);
    for (const child of [...sidebar.list.children])
      if (!nodes.has(child as HTMLElement)) child.remove();
    ordered.forEach((item, index) => {
      const before = sidebar.list.children.item(index);
      if (before !== item) sidebar.list.insertBefore(item, before);
    });
    if (retainedFocus?.isConnected && document.activeElement !== retainedFocus) {
      retainedFocus.focus({ preventScroll: true });
    }
    statusPresenter.update({
      connection,
      refreshing: props.refreshing ?? false,
      buildId: service?.build_id ?? null,
      lastCardEdit,
      title,
    });
    const actor =
      selectedView?.last_card_edit_at === lastCardEdit &&
      props.lastActorAt === lastCardEdit &&
      !workspace?.boards.some(
        (item) => item.id !== selectedId && item.last_card_edit_at === lastCardEdit,
      )
        ? props.lastActor
        : null;
    header.latest.title =
      actor === "bot"
        ? "Last card edit by Bot"
        : actor === "human"
          ? "Last card edit by Human"
          : "";
    for (const [id, row] of rows) {
      if (!ids.has(id)) {
        row.container.remove();
        rows.delete(id);
      }
    }
    sidebar.empty.textContent =
      props.data.status === "ready" && workspace?.boards.length === 0
        ? "No boards found in this folder."
        : "";
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

  header.searchField.maxLength = 1024;
  header.searchField.setAttribute(
    "aria-label",
    "Search titles, descriptions and comments. Press / to focus, Escape to clear.",
  );
  header.searchField.addEventListener("input", () => props.onSearch?.(header.searchField.value), {
    signal,
  });
  header.clear.addEventListener(
    "click",
    () => {
      props.onSearch?.("");
      header.searchField.focus();
    },
    { signal },
  );
  document.addEventListener(
    "keydown",
    (event) => {
      if (event.defaultPrevented || props.overlayOpen || props.route.kind !== "board") return;
      if (event.key === "Escape" && document.activeElement === header.searchField) {
        event.preventDefault();
        props.onSearch?.("");
      } else if (
        event.key === "/" &&
        !event.ctrlKey &&
        !event.altKey &&
        !event.metaKey &&
        !(
          event.target instanceof HTMLElement &&
          (event.target.isContentEditable || event.target.matches("input, textarea, select"))
        )
      ) {
        event.preventDefault();
        header.searchField.focus();
      }
    },
    { signal },
  );

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
      cacheSettings?.dispose();
      listeners.abort();
      sidebarControls.dispose();
      pins.dispose();
      statusPresenter.dispose();
      board?.component.dispose();
      root.remove();
    },
  };
};
