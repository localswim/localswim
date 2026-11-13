import type { CardDto, EntityTag, SessionDto, Uuid } from "../api-types.js";
import type { BoardData } from "../contracts.js";
import {
  entityTag,
  invalidResponse,
  isRecord,
  isUnknownArray,
  isUuid,
  mutateJson,
  readFailure,
  readJson,
} from "../http.js";
import { readBoard, readBoardSummary } from "./read.js";

interface Assignment {
  readonly card_id: Uuid;
  readonly ticket: number;
}

interface TicketOrder {
  readonly revision: number;
  readonly etag: EntityTag;
  readonly current: readonly Assignment[];
  readonly proposed: readonly Assignment[];
  readonly changed: boolean;
}

const pendingOrders = new Map<string, TicketOrder>();

export function pendingTicketOrderLanes(boardId: Uuid): readonly Uuid[] {
  const prefix = `${boardId}:`;
  return Array.from(pendingOrders.keys())
    .filter((key) => key.startsWith(prefix))
    .map((key) => key.slice(prefix.length));
}

function positiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function isAssignment(value: unknown): value is Assignment {
  return (
    isRecord(value) &&
    Object.keys(value).length === 2 &&
    isUuid(value.card_id) &&
    positiveInteger(value.ticket)
  );
}

function assignmentsEqual(left: readonly Assignment[], right: readonly Assignment[]): boolean {
  return (
    left.length === right.length &&
    left.every((entry, index) => {
      const other = right[index];
      return other?.card_id === entry.card_id && other.ticket === entry.ticket;
    })
  );
}

function parseOrder(value: unknown, headers: Headers, boardId: Uuid, laneId: Uuid): TicketOrder {
  if (
    !isRecord(value) ||
    Object.keys(value).length !== 7 ||
    value.board_id !== boardId ||
    value.lane_id !== laneId ||
    !positiveInteger(value.board_revision) ||
    !isUnknownArray(value.selected_card_ids) ||
    !isUnknownArray(value.current) ||
    !value.current.every(isAssignment) ||
    !isUnknownArray(value.proposed) ||
    !value.proposed.every(isAssignment) ||
    typeof value.changed !== "boolean"
  ) {
    return invalidResponse();
  }
  const { current, proposed, selected_card_ids: selected } = value;
  const members = new Set(selected);
  if (
    selected.length !== current.length ||
    members.size !== current.length ||
    current.some(
      (entry, index) =>
        entry.card_id !== selected[index] || entry.ticket <= (current[index - 1]?.ticket ?? 0),
    ) ||
    proposed.length !== current.length ||
    proposed.some(
      (entry, index) => !members.delete(entry.card_id) || entry.ticket !== current[index]?.ticket,
    ) ||
    value.changed === assignmentsEqual(current, proposed)
  ) {
    return invalidResponse();
  }
  return {
    revision: value.board_revision,
    etag: entityTag(headers, "X-Board-ETag"),
    current,
    proposed,
    changed: value.changed,
  };
}

function validateApplied(
  value: unknown,
  headers: Headers,
  boardId: Uuid,
  laneId: Uuid,
  original: TicketOrder,
): TicketOrder {
  const applied = parseOrder(value, headers, boardId, laneId);
  if (
    applied.revision !== original.revision + 1 ||
    applied.etag === original.etag ||
    !applied.changed ||
    !assignmentsEqual(applied.current, original.current) ||
    !assignmentsEqual(applied.proposed, original.proposed)
  ) {
    return invalidResponse();
  }
  return applied;
}

async function inspectOrder(
  path: string,
  boardId: Uuid,
  laneId: Uuid,
  original: TicketOrder,
  signal: AbortSignal,
): Promise<"committed" | "superseded" | "unknown"> {
  try {
    const response = await readJson(path, signal);
    const observed = parseOrder(response.data, response.headers, boardId, laneId);
    if (observed.changed) return invalidResponse();
    if (
      observed.revision === original.revision + 1 &&
      observed.etag !== original.etag &&
      assignmentsEqual(observed.current, original.proposed)
    ) {
      return "committed";
    }
    if (observed.revision > original.revision && observed.etag !== original.etag) {
      return "superseded";
    }
  } catch {
    // A retired lane can no longer report its order. The board can still establish
    // whether the original validator has been superseded.
    try {
      const { board, etag } = await readBoardSummary(boardId, signal);
      if (board.revision > original.revision && etag !== original.etag) {
        return "superseded";
      }
    } catch {
      // A failed read provides no evidence about the write.
    }
  }
  return "unknown";
}

function laneCards(data: BoardData, laneId: Uuid): readonly CardDto[] {
  return data.view.cards
    .filter((card) => card.lane_id === laneId && !card.archived)
    .toSorted((left, right) => left.ticket - right.ticket);
}

interface TicketOrderCallbacks {
  readonly canApply: () => boolean;
  readonly getBoard: () => BoardData | null;
  readonly getSession: () => SessionDto | null;
  readonly onClose: () => void;
  readonly onApplied: (changedCount: number) => void;
  readonly onRefresh: () => void;
}

function control(label: string): HTMLButtonElement {
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = label;
  return button;
}

const pageSize = 50;
const stalePreview = "The board changed. Refresh the preview before applying.";

function renderOrder(
  list: HTMLTableSectionElement,
  proposed: readonly Assignment[],
  cards: readonly CardDto[],
): void {
  const byId = new Map(cards.map((card) => [card.id, card]));
  list.replaceChildren();
  for (const assignment of proposed) {
    const card = byId.get(assignment.card_id);
    if (card === undefined) return invalidResponse();
    const row = list.insertRow();
    row.insertCell().textContent = `#${String(card.ticket).padStart(4, "0")}`;
    row.insertCell().textContent = `#${String(assignment.ticket).padStart(4, "0")}`;
    row.insertCell().textContent = card.title;
  }
}

export function openTicketOrder(
  host: HTMLElement,
  data: BoardData,
  laneId: Uuid,
  callbacks: TicketOrderCallbacks,
): { update(): void; dispose(): void } {
  const boardId = data.view.board.id;
  const path = `/ticket-orders/${laneId}`;
  const key = `${boardId}:${laneId}`;
  const lifetime = new AbortController();
  const { signal } = lifetime;
  let plan: TicketOrder | null = null;
  let cards: readonly CardDto[] = [];
  let page = 0;
  let busy = false;
  let pending = pendingOrders.get(key) ?? null;
  if (pending === null && !mayReorder(data, callbacks.getSession())) {
    throw new Error("Ticket ordering is unavailable for this lane.");
  }

  const dialog = document.createElement("dialog");
  dialog.className = "lsw-ticket-order";
  dialog.setAttribute("aria-labelledby", "lsw-ticket-order-title");
  const title = document.createElement("h2");
  title.id = "lsw-ticket-order-title";
  title.textContent = "Reorder by dependencies";
  const summary = document.createElement("p");
  const explanation = document.createElement("p");
  explanation.textContent =
    "Cards and their history stay together. Existing number references in text are not rewritten.";
  const status = document.createElement("p");
  status.setAttribute("role", "status");
  const list = document.createElement("div");
  list.className = "lsw-ticket-order-preview";
  const table = document.createElement("table");
  const header = table.createTHead().insertRow();
  for (const label of ["Current", "New", "Card"]) {
    const column = document.createElement("th");
    column.scope = "col";
    column.textContent = label;
    header.append(column);
  }
  const rows = table.createTBody();
  list.append(table);
  const pager = document.createElement("div");
  pager.className = "lsw-ticket-order-pager";
  const previous = control("Previous");
  const next = control("Next");
  const range = document.createElement("span");
  range.setAttribute("role", "status");
  pager.append(previous, range, next);
  const footer = document.createElement("footer");
  const refresh = control("Refresh preview");
  refresh.autofocus = true;
  const cancel = control("Close");
  const check = control("Check result");
  const apply = control("Apply");
  footer.append(refresh, cancel, check, apply);
  dialog.append(title, summary, explanation, list, pager, status, footer);

  function matchingBoard(order: TicketOrder, board: BoardData | null): board is BoardData {
    return (
      board !== null &&
      board.view.board.id === boardId &&
      board.view.board.revision === order.revision &&
      board.boardEtag === order.etag &&
      board.view.lanes.some((item) => item.id === laneId && item.reorder_enabled && !item.retired)
    );
  }

  function mayReorder(board: BoardData | null, session: SessionDto | null): boolean {
    return (
      callbacks.canApply() &&
      session?.kind === "human" &&
      board?.view.board.id === boardId &&
      board.view.board.availability === "open" &&
      board.view.lanes.some(
        (item) => item.id === laneId && item.reorder_enabled && !item.retired,
      ) &&
      board.policy.resource_grants.some(
        (grant) =>
          grant.user_id === session.principal_id && grant.capabilities.includes("reorder_tickets"),
      )
    );
  }

  function showPage(): void {
    const order = plan;
    if (order === null) return;
    const count = order.proposed.length;
    const start = page * pageSize;
    const end = Math.min(start + pageSize, count);
    renderOrder(rows, order.proposed.slice(start, end), cards);
    range.textContent = `${count === 0 ? 0 : start + 1}–${end} of ${count}`;
    previous.disabled = page === 0;
    next.disabled = end >= count;
  }

  function update(): void {
    const liveBoard = callbacks.getBoard();
    const canWrite = mayReorder(liveBoard, callbacks.getSession());
    const ready = canWrite && plan !== null && matchingBoard(plan, liveBoard);
    if (
      !busy &&
      pending === null &&
      status.textContent === "" &&
      plan !== null &&
      liveBoard !== null &&
      !matchingBoard(plan, liveBoard)
    ) {
      status.textContent = stalePreview;
    } else if (ready && status.textContent === stalePreview) {
      status.textContent = "";
    }
    cancel.disabled = busy && pending !== null;
    refresh.hidden = !canWrite;
    refresh.setAttribute("aria-disabled", String(busy || pending !== null || !canWrite));
    check.hidden = pending === null;
    check.disabled = busy;
    apply.disabled =
      busy || pending !== null || plan?.changed !== true || cards.length > 1000 || !ready;
    apply.hidden = !canWrite;
    status.hidden = status.textContent === "";
    list.hidden = plan === null;
    pager.hidden = plan === null;
    summary.hidden = plan === null;
  }

  function finish(order: TicketOrder): void {
    const changedCount = order.proposed.filter(
      (entry, index) => entry.card_id !== order.current[index]?.card_id,
    ).length;
    dispose();
    callbacks.onApplied(changedCount);
    callbacks.onClose();
  }

  function dispose(): void {
    lifetime.abort();
    dialog.close();
    dialog.remove();
  }

  function close(): void {
    dispose();
    callbacks.onClose();
  }

  function clearPending(original: TicketOrder): void {
    if (pendingOrders.get(key) === original) pendingOrders.delete(key);
    pending = null;
  }

  async function preview(): Promise<void> {
    busy = true;
    plan = null;
    status.textContent = "Loading preview…";
    rows.replaceChildren();
    update();
    try {
      const response = await readJson(`${path}?preview=true`, signal);
      if (signal.aborted) return;
      const order = parseOrder(response.data, response.headers, boardId, laneId);
      let board = callbacks.getBoard();
      if (!matchingBoard(order, board)) {
        board = await readBoard(boardId, signal);
        if (signal.aborted) return;
        callbacks.onRefresh();
      }
      if (!matchingBoard(order, board)) {
        status.textContent = stalePreview;
        return;
      }
      cards = laneCards(board, laneId);
      if (
        !assignmentsEqual(
          order.current,
          cards.map((card) => ({ card_id: card.id, ticket: card.ticket })),
        )
      ) {
        return invalidResponse();
      }
      plan = order;
      page = 0;
      const changes = order.proposed.filter(
        (entry, index) => entry.card_id !== order.current[index]?.card_id,
      ).length;
      summary.textContent = `${cards.length} ${cards.length === 1 ? "card" : "cards"}; ${changes} number ${changes === 1 ? "change" : "changes"}. Prerequisites come first.`;
      status.textContent =
        cards.length > 1000 ? "A preview can apply to at most 1,000 tickets." : "";
      showPage();
    } catch (error) {
      if (signal.aborted) return;
      const failure = readFailure(error);
      status.textContent =
        failure.kind === "http" &&
        failure.problem.code === "invalid_request" &&
        failure.problem.errors.some((issue) => issue.field === "card_id")
          ? "These tickets contain a blocking cycle."
          : "The ticket order preview could not be loaded.";
    } finally {
      busy = false;
      if (!signal.aborted) {
        update();
        if (document.activeElement === refresh) refresh.scrollIntoView({ block: "nearest" });
      }
    }
  }

  async function inspect(original: TicketOrder): Promise<void> {
    busy = true;
    status.textContent = "Checking the board for the new ticket order…";
    update();
    const result = await inspectOrder(path, boardId, laneId, original, signal);
    if (signal.aborted) return;
    busy = false;
    if (result === "committed") {
      clearPending(original);
      finish(original);
      return;
    }
    if (result === "superseded") {
      clearPending(original);
      plan = null;
      status.textContent = "The board advanced before the result could be confirmed.";
      callbacks.onRefresh();
    } else {
      status.textContent =
        "The result could not be confirmed. Check the board before applying again.";
    }
    update();
  }

  async function applyOrder(): Promise<void> {
    const original = plan;
    const actor = callbacks.getSession();
    const board = callbacks.getBoard();
    if (
      busy ||
      pending !== null ||
      original?.changed !== true ||
      cards.length > 1000 ||
      actor === null ||
      !mayReorder(board, actor) ||
      !matchingBoard(original, board)
    )
      return;
    busy = true;
    status.textContent = "Applying ticket order…";
    update();
    let dispatched = false;
    const result = await mutateJson(path, {
      method: "PUT",
      body: {
        board_id: boardId,
        expected_board_etag: original.etag,
        card_ids: original.proposed.map((entry) => entry.card_id),
      },
      validate: (value, headers) => validateApplied(value, headers, boardId, laneId, original),
      beforeDispatch(actual) {
        const current = callbacks.getBoard();
        const session = callbacks.getSession();
        if (
          signal.aborted ||
          actual.principal_id !== actor.principal_id ||
          session?.principal_id !== actor.principal_id ||
          !mayReorder(current, actual) ||
          !matchingBoard(original, current) ||
          pendingOrders.has(key)
        )
          return invalidResponse();
        dispatched = true;
        pending = original;
        pendingOrders.set(key, original);
      },
      signal,
    });
    if (dispatched && result.outcome !== "unknown") clearPending(original);
    if (signal.aborted) return;
    busy = false;
    if (!dispatched) {
      status.textContent = "The board could not be verified. Refresh the preview before applying.";
      plan = null;
      callbacks.onRefresh();
      update();
      return;
    }
    if (result.outcome === "committed") {
      finish(original);
      return;
    }
    if (result.outcome === "unknown") {
      await inspect(original);
      return;
    }
    plan = null;
    if (result.problem.code === "precondition_failed") {
      status.textContent = stalePreview;
      callbacks.onRefresh();
    } else {
      status.textContent = "The ticket order could not be applied.";
    }
    update();
  }

  cancel.addEventListener(
    "click",
    () => {
      if (!busy || pending === null) close();
    },
    { signal },
  );
  refresh.addEventListener(
    "click",
    () => {
      if (!busy && pending === null && mayReorder(callbacks.getBoard(), callbacks.getSession())) {
        void preview();
      }
    },
    { signal },
  );
  previous.addEventListener(
    "click",
    () => {
      page -= 1;
      showPage();
    },
    { signal },
  );
  next.addEventListener(
    "click",
    () => {
      page += 1;
      showPage();
    },
    { signal },
  );
  dialog.addEventListener(
    "cancel",
    (event) => {
      event.preventDefault();
      if (!busy || pending === null) close();
    },
    { signal },
  );
  dialog.addEventListener("close", close, { signal });
  check.addEventListener(
    "click",
    () => {
      if (!busy && pending !== null) void inspect(pending);
    },
    { signal },
  );
  apply.addEventListener("click", () => void applyOrder(), { signal });
  host.append(dialog);
  dialog.showModal();
  if (pending === null) void preview();
  else void inspect(pending);
  return { update, dispose };
}
