import type { WorkspaceBoard, WorkspaceView } from "./workspace-state.js";

export type AttentionIntents = Readonly<{
  selectBoard(id: string): void;
  retry(): void;
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
  item.append(link);

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
    update(board: WorkspaceBoard, observedAt: WorkspaceView["observedAt"], selected: boolean) {
      name.textContent = board.name ?? `Unavailable board (${id.slice(0, 8)})`;
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
  const retry = doc.createElement("button");
  retry.className = "workspace-attention-retry";
  retry.type = "button";
  retry.textContent = "Try again";
  retry.hidden = true;
  const requestRetry = () => intents.retry();
  retry.addEventListener("click", requestRetry);

  const list = doc.createElement("ul");
  list.className = "workspace-attention-list";
  list.setAttribute("role", "list");
  panel.append(header, status, retry, list);
  root.append(panel);

  const rows = new Map<string, ReturnType<typeof createBoardRow>>();
  let disposed = false;

  return {
    update(view: WorkspaceView, selectedBoardId: string | null = null) {
      if (disposed) return;
      const focused = doc.activeElement;
      const restorePanelFocus = focused === retry || list.contains(focused);
      const hasBoards = view.boards.length > 0;
      const message = catalogStatus(view.status, hasBoards);
      panel.dataset.status = view.status;
      if (status.textContent !== message) status.textContent = message;
      retry.hidden = view.status !== "error";
      for (const label of headers) label.hidden = !hasBoards;
      list.hidden = !hasBoards;

      const present = new Set(view.boards.map((board) => board.id));
      for (const [boardId, row] of rows) {
        if (present.has(boardId)) continue;
        row.dispose();
        rows.delete(boardId);
      }

      let restoreFocus: HTMLAnchorElement | undefined;
      let next = list.firstElementChild;
      for (const board of view.boards) {
        let row = rows.get(board.id);
        if (!row) {
          row = createBoardRow(doc, board.id, id, intents);
          rows.set(board.id, row);
        }
        row.update(board, view.observedAt, board.id === selectedBoardId);
        if (row.link === focused) restoreFocus = row.link;
        if (row.item !== next) list.insertBefore(row.item, next);
        next = row.item.nextElementSibling;
      }
      if (restorePanelFocus && doc.activeElement !== focused) {
        (restoreFocus ?? heading).focus({ preventScroll: true });
      }
    },
    dispose() {
      disposed = true;
      retry.removeEventListener("click", requestRetry);
      for (const row of rows.values()) row.dispose();
      rows.clear();
      panel.remove();
    },
  };
}
