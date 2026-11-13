import type { WorkspaceBoard, WorkspaceView } from "./workspace-state.js";
import { mountSidebarGeometry, type PinChange, type SidebarPinsView } from "./sidebar-state.js";

export type AttentionIntents = Readonly<{
  selectBoard(id: string): void;
  retry(): void;
  changePins?(change: PinChange): void;
  inspectPins?(): void;
  retryPins?(): void;
  cancelPins?(): void;
}>;

const columns = [
  { key: "blocked", short: "Blocked", label: "Blocked" },
  { key: "needsInput", short: "Terry", label: "Needs Terry" },
  { key: "readyForReview", short: "Review", label: "Ready for Review" },
] as const;

const availabilityLabels: Record<WorkspaceBoard["availability"], string> = {
  open: "",
  closed: "Board closed",
  unhealthy: "Board unhealthy",
  unknown: "Board availability unknown",
};

let nextAttentionID = 0;

function button(doc: Document, label: string, activate?: () => void) {
  const control = doc.createElement("button");
  control.type = "button";
  control.textContent = label;
  if (activate) control.addEventListener("click", activate);
  return control;
}

function recency(
  board: WorkspaceBoard,
  observedAt: WorkspaceView["observedAt"],
  reference: "ago" | "before the last catalog refresh",
): string {
  if (board.lastCardEditAt !== null && observedAt !== null) {
    const minutes = Math.max(0, Math.floor((observedAt - board.lastCardEditAt) / 60_000));
    const elapsed =
      minutes === 0 ? "less than a minute" : `${minutes} ${minutes === 1 ? "minute" : "minutes"}`;
    return `Last card edit ${elapsed} ${reference}`;
  }
  if (board.lastCardEditAt === null && board.attention !== null) return "No recorded card edits";
  return "Card edit history unavailable";
}

function catalogStatus(status: WorkspaceView["status"], hasBoards: boolean): string {
  switch (status) {
    case "loading":
      return "Loading boards…";
    case "ready":
      return hasBoards ? "" : "No boards are available.";
    case "error":
      return hasBoards
        ? "Could not refresh boards. Showing the last loaded boards."
        : "Could not load boards.";
  }
}

function createBoardRow(doc: Document, id: string, prefix: string, intents: AttentionIntents) {
  const item = doc.createElement("li");
  const link = doc.createElement("a");
  link.className = "workspace-attention-row";
  link.dataset.boardId = id;
  link.href = `/boards/${encodeURIComponent(id)}`;

  const identity = doc.createElement("span");
  identity.className = "workspace-attention-identity";
  const name = doc.createElement("span");
  name.id = `${prefix}-${id}-name`;
  const availability = doc.createElement("span");
  availability.className = "workspace-attention-availability";
  identity.append(name, availability);

  const slots = columns.map(({ key, label }) => {
    const cell = doc.createElement("span");
    cell.className = "workspace-attention-cell";
    cell.dataset.attention = key;
    cell.setAttribute("aria-hidden", "true");
    const count = doc.createElement("span");
    cell.append(count);
    return { cell, count, key, label };
  });

  const description = doc.createElement("span");
  description.id = `${prefix}-${id}-summary`;
  description.hidden = true;
  link.setAttribute("aria-labelledby", name.id);
  link.setAttribute("aria-describedby", description.id);
  link.append(identity, ...slots.map(({ cell }) => cell), description);
  item.className = "workspace-attention-entry";
  const actions = doc.createElement("div");
  actions.className = "workspace-attention-actions";
  let pinned = false;
  const toggle = button(doc, "Pin", () => {
    if (toggle.getAttribute("aria-disabled") === "true") return;
    intents.changePins?.({ id, action: pinned ? "unpin" : "pin" });
  });
  const grip = button(doc, "⠿");
  grip.className = "workspace-attention-grip";
  grip.setAttribute("aria-keyshortcuts", "ArrowUp ArrowDown");
  actions.append(toggle, grip);
  item.append(link, actions);

  function select(event: MouseEvent) {
    if (
      event.defaultPrevented ||
      event.button !== 0 ||
      event.altKey ||
      event.ctrlKey ||
      event.metaKey ||
      event.shiftKey
    ) {
      return;
    }
    event.preventDefault();
    intents.selectBoard(id);
  }

  let updateInteractiveRecency: (() => void) | undefined;
  const showCurrentRecency = () => updateInteractiveRecency?.();
  link.addEventListener("click", select);
  link.addEventListener("pointerenter", showCurrentRecency);
  link.addEventListener("focus", showCurrentRecency);

  return {
    item,
    link,
    toggle,
    grip,
    update(
      board: WorkspaceBoard,
      observedAt: WorkspaceView["observedAt"],
      selected: boolean,
      isPinned: boolean,
      canChange: boolean,
    ) {
      const boardName = board.name ?? `Unavailable board (${id.slice(0, 8)})`;
      name.textContent = boardName;
      pinned = isPinned;
      actions.hidden = !intents.changePins;
      const action = pinned ? "Unpin" : "Pin";
      toggle.textContent = action;
      toggle.setAttribute("aria-label", `${action} ${boardName}`);
      toggle.setAttribute("aria-disabled", String(!canChange));
      grip.hidden = !pinned;
      grip.title = `Drag or use the Up and Down arrows to reorder ${boardName}`;
      grip.setAttribute("aria-label", `Reorder ${boardName}`);
      grip.setAttribute("aria-disabled", String(!canChange));
      const status = availabilityLabels[board.availability];
      availability.textContent = status;
      availability.hidden = status === "";
      if (selected) link.setAttribute("aria-current", "page");
      else link.removeAttribute("aria-current");

      const counts = slots.map(({ cell, count, key, label }) => {
        const value = board.attention?.[key];
        cell.dataset.countState =
          value === undefined ? "unavailable" : value > 0 ? "active" : "zero";
        count.textContent = value === undefined ? "—" : String(value);
        return `${label} ${value ?? "unavailable"}`;
      });
      const countSummary = counts.join(", ");
      function setRecency(lastEdit: string) {
        const details = [status, lastEdit].filter(Boolean);
        link.title = details.join(". ");
        description.textContent = [countSummary, ...details].join(". ");
      }
      setRecency(recency(board, observedAt, "before the last catalog refresh"));
      updateInteractiveRecency = () => setRecency(recency(board, Date.now(), "ago"));
    },
    dispose() {
      link.removeEventListener("click", select);
      link.removeEventListener("pointerenter", showCurrentRecency);
      link.removeEventListener("focus", showCurrentRecency);
      item.remove();
    },
  };
}

export function mountAttention(root: HTMLElement, intents: AttentionIntents) {
  const doc = root.ownerDocument;
  const id = `workspace-attention-${++nextAttentionID}`;
  const panel = doc.createElement("nav");
  panel.className = "workspace-attention";
  panel.id = id;
  const toggle = button(doc, "Hide Attention");
  toggle.className = "workspace-attention-toggle";
  toggle.setAttribute("aria-controls", panel.id);
  const separator = doc.createElement("div");
  separator.className = "workspace-attention-resize";
  separator.setAttribute("role", "separator");
  separator.setAttribute("aria-label", "Attention width");
  separator.setAttribute("aria-orientation", "vertical");
  separator.setAttribute("aria-controls", panel.id);
  separator.tabIndex = 0;

  const heading = doc.createElement("h2");
  heading.id = `${id}-heading`;
  heading.className = "workspace-attention-heading";
  heading.textContent = "Attention";
  heading.tabIndex = -1;
  panel.setAttribute("aria-labelledby", heading.id);

  const headers = columns.map(({ key, short, label }) => {
    const header = doc.createElement("span");
    header.className = "workspace-attention-label";
    header.dataset.attention = key;
    header.textContent = short;
    header.title = label;
    header.setAttribute("aria-hidden", "true");
    return header;
  });
  const header = doc.createElement("div");
  header.className = "workspace-attention-header";
  header.append(heading, ...headers);

  const status = doc.createElement("p");
  status.className = "workspace-attention-status";
  status.setAttribute("role", "status");
  const retry = button(doc, "Try again", () => intents.retry());
  retry.className = "workspace-attention-retry";
  retry.hidden = true;

  const feedback = doc.createElement("div");
  feedback.className = "workspace-attention-feedback";
  const pinStatus = doc.createElement("p");
  pinStatus.className = "workspace-attention-status";
  pinStatus.setAttribute("role", "status");
  const inspect = button(doc, "Inspect saved pins", () => intents.inspectPins?.());
  const retryPins = button(doc, "Retry pin change", () => intents.retryPins?.());
  const cancel = button(doc, "Cancel pending change", () => intents.cancelPins?.());
  const remove = button(doc, "Remove unavailable pins", () =>
    intents.changePins?.({ action: "remove-unavailable" }),
  );
  feedback.append(pinStatus, inspect, retryPins, cancel, remove);

  function boardGroup(label: string) {
    const section = doc.createElement("section");
    section.className = "workspace-attention-group";
    section.hidden = true;
    const groupHeading = doc.createElement("h3");
    groupHeading.textContent = label;
    const list = doc.createElement("ul");
    list.className = "workspace-attention-list";
    list.setAttribute("role", "list");
    section.append(groupHeading, list);
    return { section, list, heading: groupHeading };
  }

  const pinned = boardGroup("Pinned boards");
  const other = boardGroup("Other boards");
  feedback.hidden = true;
  panel.append(header, status, retry, feedback, pinned.section, other.section);
  root.append(toggle, separator, panel);
  const unmountGeometry = mountSidebarGeometry(root, panel, toggle, separator);

  const rows = new Map<string, ReturnType<typeof createBoardRow>>();
  type BoardRow = NonNullable<ReturnType<typeof rows.get>>;
  let view: WorkspaceView | undefined;
  let selected: string | null = null;
  let pins: SidebarPinsView | undefined;
  let drag: { row: BoardRow; pointer: number; target?: BoardRow } | undefined;
  let visiblePins: readonly string[] = [];
  let disposed = false;

  function move(row: BoardRow, target: string) {
    const boardId = row.link.dataset.boardId;
    if (!pins?.canChange || !boardId || boardId === target || !visiblePins.includes(target)) return;
    const action = visiblePins.indexOf(target) < visiblePins.indexOf(boardId) ? "before" : "after";
    intents.changePins?.({ id: boardId, anchor: target, action });
  }

  function clearDrag() {
    const current = drag;
    drag = undefined;
    current?.target?.item.removeAttribute("data-pin-drop");
    if (current?.row.grip.hasPointerCapture(current.pointer)) {
      current.row.grip.releasePointerCapture(current.pointer);
    }
  }

  function wireReorder(row: BoardRow, boardId: string) {
    const { grip } = row;
    grip.addEventListener("keydown", (event) => {
      if (event.isComposing || drag || !pins?.canChange) return;
      if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
      event.preventDefault();
      const index = visiblePins.indexOf(boardId);
      const target = visiblePins[index + (event.key === "ArrowUp" ? -1 : 1)];
      if (index >= 0 && target) move(row, target);
    });
    grip.addEventListener("pointerdown", (event) => {
      if (drag || !pins?.canChange || !event.isPrimary || event.button !== 0) return;
      try {
        grip.setPointerCapture(event.pointerId);
      } catch {
        return;
      }
      drag = { row, pointer: event.pointerId };
      event.preventDefault();
      grip.focus({ preventScroll: true });
    });
    function updateTarget(event: PointerEvent) {
      if (drag?.pointer !== event.pointerId) return;
      drag.target?.item.removeAttribute("data-pin-drop");
      const hit = doc.elementFromPoint(event.clientX, event.clientY);
      const entry = hit?.closest(".workspace-attention-entry");
      const targetId =
        entry?.parentElement === pinned.list
          ? entry.querySelector<HTMLElement>("[data-board-id]")?.dataset.boardId
          : undefined;
      const target =
        targetId && targetId !== boardId && visiblePins.includes(targetId)
          ? rows.get(targetId)
          : undefined;
      drag.target = target;
      target?.item.setAttribute("data-pin-drop", "true");
    }
    grip.addEventListener("pointermove", updateTarget);
    grip.addEventListener("pointerup", (event) => {
      if (drag?.pointer !== event.pointerId) return;
      updateTarget(event);
      const target = drag.target?.link.dataset.boardId;
      clearDrag();
      if (target) move(row, target);
      paint();
    });
    function cancelDrag(event: PointerEvent) {
      if (drag?.pointer !== event.pointerId) return;
      clearDrag();
      paint();
    }
    grip.addEventListener("pointercancel", cancelDrag);
    grip.addEventListener("lostpointercapture", cancelDrag);
  }

  function paint() {
    if (disposed || drag || !view) return;
    const focused = doc.activeElement;
    const hadFocus = panel.contains(focused);
    const hasBoards = view.boards.length > 0;
    const message = catalogStatus(view.status, hasBoards);
    panel.dataset.status = view.status;
    if (status.textContent !== message) status.textContent = message;
    retry.hidden = view.status !== "error";
    for (const label of headers) label.hidden = !hasBoards;

    const pinMessage = pins?.message ?? "";
    if (pinStatus.textContent !== pinMessage) pinStatus.textContent = pinMessage;
    feedback.hidden = pinMessage === "";
    const pinControls = [
      [inspect, pins?.canInspect],
      [retryPins, pins?.canRetry],
      [cancel, pins?.canCancel],
      [remove, pins?.canRemoveUnavailable],
    ] as const;
    for (const [control, enabled] of pinControls) {
      control.hidden = !enabled && (!pins?.busy || control !== focused);
      control.setAttribute("aria-disabled", String(!enabled));
    }

    const catalog = new Map(view.boards.map((board) => [board.id, board]));
    for (const [boardId, row] of rows) {
      if (catalog.has(boardId)) continue;
      row.dispose();
      rows.delete(boardId);
    }
    visiblePins = pins?.ids.filter((boardId) => catalog.has(boardId)) ?? [];
    const pinnedIds = new Set(visiblePins);
    const observedAt = view.observedAt;
    let fallbackLink: HTMLAnchorElement | undefined;

    function paintGroup(group: typeof pinned, ids: readonly string[], isPinned: boolean) {
      const { section, list } = group;
      section.hidden = ids.length === 0;
      let next = list.firstElementChild;
      for (const boardId of ids) {
        const board = catalog.get(boardId);
        if (!board) continue;
        let row = rows.get(boardId);
        if (!row) {
          row = createBoardRow(doc, boardId, id, intents);
          wireReorder(row, boardId);
          rows.set(boardId, row);
        }
        if (row.item.contains(focused)) fallbackLink = row.link;
        row.update(board, observedAt, boardId === selected, isPinned, pins?.canChange ?? false);
        if (row.item !== next) list.insertBefore(row.item, next);
        next = row.item.nextElementSibling;
      }
    }
    paintGroup(pinned, visiblePins, true);
    paintGroup(
      other,
      view.boards.filter((board) => !pinnedIds.has(board.id)).map((board) => board.id),
      false,
    );
    other.heading.hidden = visiblePins.length === 0;
    if (
      hadFocus &&
      (!panel.contains(focused) || focused?.closest("[hidden]") || doc.activeElement !== focused)
    ) {
      const restore =
        focused instanceof HTMLElement && panel.contains(focused) && !focused.closest("[hidden]")
          ? focused
          : (fallbackLink ?? pinControls.find(([control]) => !control.hidden)?.[0] ?? heading);
      restore.focus({ preventScroll: true });
    }
  }

  return {
    update(catalog: WorkspaceView, selectedBoardId: string | null = null) {
      view = catalog;
      selected = selectedBoardId;
      paint();
    },
    updatePins(state: SidebarPinsView) {
      pins = state;
      paint();
    },
    dispose() {
      disposed = true;
      clearDrag();
      unmountGeometry();
      for (const row of rows.values()) row.dispose();
      rows.clear();
      panel.remove();
      separator.remove();
      toggle.remove();
    },
  };
}
