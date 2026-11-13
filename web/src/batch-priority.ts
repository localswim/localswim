import { ApiError } from "../api.mjs";
import type { BoardViewData } from "./board-view.js";
import type { AppContext } from "./model.js";
import { submitOperation, type OperationStorage } from "./operations.js";

type Selection = Readonly<{ id: string; ticket: number }>;
type CardPriority = Selection &
  Readonly<{ priority_id: string; completed_at: string | null; revision: number }>;
type Intent = Readonly<{
  etag: string;
  priorityId: string;
  selected: readonly Selection[];
}>;
type Options = AppContext &
  Readonly<{
    boardId: string;
    getContext: () => { board: BoardViewData | null; etag: string | null; writable: boolean };
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    beginWrite: (cardIds: readonly string[]) => (() => void) | null;
    beginInspection: () => (() => void) | null;
    onRefresh: () => void;
    storage?: OperationStorage;
  }>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const maxSelection = 1000;
const pageSize = 200;

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function id(value: unknown): value is string {
  return typeof value === "string" && value.length === 36 && uuid.test(value);
}

function positiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function revision(boardId: string, tag: string): number {
  const value = Number(tag.slice(boardId.length + 2, -1));
  if (!positiveInteger(value) || tag !== `"${boardId}-${value}"`) {
    throw new ApiError("invalid_response");
  }
  return value;
}

function parseTickets(raw: string): number[] | null {
  const values = raw.trim().split(/(?:\s*,\s*|\s+)/);
  if (values.length > maxSelection || values.some((value) => !/^[1-9][0-9]*$/.test(value))) {
    return null;
  }
  const tickets = values.map(Number);
  return tickets.every(positiveInteger) && new Set(tickets).size === tickets.length
    ? tickets
    : null;
}

function selection(value: unknown): value is Selection {
  return record(value) && id(value.id) && positiveInteger(value.ticket);
}

function parseIntent(raw: string, boardId: string): Intent {
  const value: unknown = JSON.parse(raw);
  if (!record(value)) throw new TypeError("Invalid batch priority record.");
  const { etag, priorityId, selected, attempt } = value;
  if (
    typeof etag !== "string" ||
    !id(priorityId) ||
    !id(attempt) ||
    !Array.isArray(selected) ||
    selected.length < 1 ||
    selected.length > maxSelection ||
    !selected.every(selection) ||
    new Set(selected.map((card) => card.id)).size !== selected.length ||
    new Set(selected.map((card) => card.ticket)).size !== selected.length
  ) {
    throw new TypeError("Invalid batch priority record.");
  }
  revision(boardId, etag);
  return { etag, priorityId, selected };
}

function cardPriority(value: unknown, boardId: string): value is CardPriority {
  return (
    record(value) &&
    value.board_id === boardId &&
    id(value.priority_id) &&
    positiveInteger(value.revision) &&
    (value.completed_at === null || typeof value.completed_at === "string") &&
    selection(value)
  );
}

function confirmedRows(
  reply: Awaited<ReturnType<AppContext["api"]["setPriorities"]>>,
  boardId: string,
  intent: Intent,
  inventory: ReadonlyMap<string, CardPriority>,
  labels: ReadonlyMap<string, string>,
): string[] {
  const base = revision(boardId, intent.etag);
  const { data, etag } = reply;
  const { cards, changes, board_revision: saved } = data;
  // The adapter validates identities and response shape. Bind its result to this
  // attempt's exact revision and the selected cards' previous saved values.
  if (
    data.board_id !== boardId ||
    !positiveInteger(saved) ||
    etag !== `"${boardId}-${saved}"` ||
    !Array.isArray(cards) ||
    !Array.isArray(changes) ||
    saved !== base + Number(changes.length > 0) ||
    cards.length !== intent.selected.length
  ) {
    throw new TypeError("Invalid batch priority receipt.");
  }
  const effective = new Map<string, CardPriority>();
  for (const card of cards as unknown[]) {
    if (!cardPriority(card, boardId) || card.completed_at !== null || effective.has(card.id)) {
      throw new TypeError("Invalid effective card priority.");
    }
    effective.set(card.id, card);
  }
  const changed = new Map<string, string>();
  let selectedChanged = false;
  const rows = [`${changes.length} priority changes committed.`];
  const details: string[] = [];
  for (const change of changes as unknown[]) {
    if (!record(change) || !id(change.resource_id) || !id(change.after)) {
      throw new TypeError("Invalid priority change.");
    }
    const { resource_id: cardId, before, after, operation } = change;
    const card = inventory.get(cardId);
    const selected = effective.has(cardId);
    const beforeLabel = card && labels.get(card.priority_id);
    const afterLabel = labels.get(after);
    if (
      card?.completed_at !== null ||
      card.revision > base ||
      changed.has(cardId) ||
      before !== card.priority_id ||
      before === after ||
      beforeLabel === undefined ||
      afterLabel === undefined ||
      operation !== (selected ? "patch" : "promote")
    ) {
      throw new TypeError("Invalid priority change.");
    }
    changed.set(cardId, after);
    selectedChanged ||= selected;
    details.push(
      `#${card.ticket}${selected ? "" : " upstream promotion"} · ${beforeLabel} → ${afterLabel}`,
    );
  }
  if (changes.length > 0 && !selectedChanged) {
    throw new TypeError("Priority promotions require a selected priority change.");
  }
  for (const { id, ticket } of intent.selected) {
    const card = effective.get(id);
    const previous = inventory.get(id);
    const after = changed.get(id);
    const label = card && labels.get(card.priority_id);
    if (
      card?.ticket !== ticket ||
      !previous ||
      previous.revision > base ||
      card.revision !== (after === undefined ? previous.revision : saved) ||
      card.priority_id !== (after ?? previous.priority_id) ||
      label === undefined
    ) {
      throw new TypeError("Invalid effective card priority.");
    }
    rows.push(`#${ticket} effective priority · ${label}`);
  }
  return [...rows, ...details];
}

export function mountBatchPriority(
  host: HTMLElement,
  {
    api,
    signal,
    boardId,
    getContext,
    readWithSession,
    beginWrite,
    beginInspection,
    onRefresh,
    storage,
  }: Options,
) {
  const doc = host.ownerDocument;
  const journalKey = `localswim:v2:batch-priority:${boardId}`;
  const tab = () => storage ?? doc.defaultView?.sessionStorage;
  const form = doc.createElement("form");
  const tickets = doc.createElement("input");
  tickets.required = true;
  tickets.placeholder = "1, 2, 3";
  const priority = doc.createElement("select");
  priority.required = true;
  const submit = doc.createElement("button");
  submit.type = "submit";
  submit.textContent = "Set batch priority";
  const inspect = doc.createElement("button");
  inspect.type = "button";
  inspect.textContent = "Inspect saved priorities";
  const clear = doc.createElement("button");
  clear.type = "button";
  const status = doc.createElement("p");
  status.setAttribute("role", "status");
  const result = doc.createElement("ul");
  const ticketLabel = doc.createElement("label");
  ticketLabel.append("Ticket numbers", tickets);
  const priorityLabel = doc.createElement("label");
  priorityLabel.append("Priority", priority);
  form.append(ticketLabel, priorityLabel, submit, inspect, clear);
  host.replaceChildren(form, status, result);

  let draftTag: string | null = null;
  let raw: string | null = null;
  let pending: Intent | null = null;
  let journalProblem = false;
  let busy = false;
  let inspectedTag: string | null = null;
  let minRevision = 0;
  let catalog = "";
  let message = "";

  function loadJournal() {
    try {
      const stored = tab()?.getItem(journalKey) ?? null;
      if (stored !== raw || journalProblem) {
        const intent = stored === null ? null : parseIntent(stored, boardId);
        if (intent || pending) {
          tickets.value = intent?.selected.map(({ ticket }) => ticket).join(", ") ?? "";
          priority.value = intent?.priorityId ?? "";
          draftTag = intent?.etag ?? null;
        }
        raw = stored;
        pending = intent;
        inspectedTag = null;
        message = pending ? "The batch outcome is unknown. Inspect the saved priorities." : "";
        showRows([]);
      }
      journalProblem = false;
    } catch {
      journalProblem = true;
    }
  }

  function writeJournal(intent: Intent | null): boolean {
    try {
      const store = tab();
      if (!store || store.getItem(journalKey) !== raw || journalProblem) return false;
      const next =
        intent === null ? null : JSON.stringify({ ...intent, attempt: crypto.randomUUID() });
      if (next === null) store.removeItem(journalKey);
      else store.setItem(journalKey, next);
      raw = next;
      pending = intent;
      return true;
    } catch {
      return false;
    }
  }

  function showRows(rows: readonly string[]) {
    result.replaceChildren(
      ...rows.map((text) => {
        const item = doc.createElement("li");
        item.textContent = text;
        return item;
      }),
    );
    result.hidden = rows.length === 0;
  }

  function contextTag() {
    const { board, etag, writable } = getContext();
    return writable &&
      board?.board.id === boardId &&
      etag === board.etag &&
      board.board.revision >= minRevision
      ? etag
      : null;
  }

  function update() {
    if (signal.aborted) return;
    if (!busy) loadJournal();
    const { board, etag } = getContext();
    const tag = contextTag();
    const priorities = board?.priorities ?? [];
    const signature = JSON.stringify(priorities);
    if (catalog !== signature) {
      const previous = priority.value;
      const empty = doc.createElement("option");
      empty.value = "";
      empty.textContent = "Choose a priority";
      priority.replaceChildren(
        empty,
        ...priorities.map(({ id, label }) => {
          const option = doc.createElement("option");
          option.value = id;
          option.textContent = label;
          return option;
        }),
      );
      priority.value = pending?.priorityId ?? previous;
      catalog = signature;
    }
    const stale = draftTag !== null && etag !== null && draftTag !== etag;
    const locked = busy || pending !== null || stale || journalProblem;
    tickets.disabled = priority.disabled = locked || tag === null;
    submit.disabled = tickets.disabled || !tickets.value.trim() || !priority.value;
    inspect.hidden = pending === null;
    inspect.disabled = busy || journalProblem;
    clear.hidden = pending ? inspectedTag === null : draftTag === null;
    clear.disabled = busy || journalProblem || (pending !== null && inspectedTag === null);
    clear.textContent = pending ? "Dismiss inspected batch" : "Clear selection";
    const text = journalProblem
      ? "The saved batch record could not be read."
      : stale && !pending && !busy
        ? "The board changed. Clear the selection and choose tickets again."
        : message;
    if (status.textContent !== text) status.textContent = text;
  }

  async function checkBoard(tag: string) {
    const { etag } = await readWithSession(
      () => api.getBoardIfChanged(boardId, tag, signal),
      signal,
    );
    signal.throwIfAborted();
    if (etag !== tag) throw new ApiError("http_error", { status: 412 });
  }

  async function readInventory(tag: string) {
    const cards = new Map<string, CardPriority>();
    const tickets = new Set<number>();
    const cursors = new Set<string>();
    let cursor: string | undefined;
    try {
      do {
        const { data } = await readWithSession(
          () =>
            api.listCards(
              boardId,
              { cursor, archived: "include", limit: pageSize, sort: "ticket" },
              signal,
            ),
          signal,
        );
        signal.throwIfAborted();
        const { cards: rows, next_cursor: next } = data as Record<string, unknown>;
        if (
          !Array.isArray(rows) ||
          rows.length > pageSize ||
          (next !== null &&
            (typeof next !== "string" ||
              !next ||
              next.length > 2048 ||
              !rows.length ||
              cursors.has(next)))
        ) {
          throw new ApiError("invalid_response");
        }
        for (const card of rows as unknown[]) {
          if (!cardPriority(card, boardId) || cards.has(card.id) || tickets.has(card.ticket)) {
            throw new ApiError("invalid_response");
          }
          cards.set(card.id, card);
          tickets.add(card.ticket);
        }
        cursor = next ?? undefined;
        if (cursor !== undefined) cursors.add(cursor);
      } while (cursor !== undefined);
    } catch (error) {
      if (!signal.aborted) await checkBoard(tag);
      throw error;
    }
    await checkBoard(tag);
    return cards;
  }

  async function save() {
    update();
    const tag = contextTag();
    const board = getContext().board;
    if (signal.aborted || submit.disabled || !tag || tag !== draftTag || !board) return;
    const values = parseTickets(tickets.value);
    const requested = priority.value;
    if (!values || !board.priorities.some(({ id }) => id === requested)) {
      message = "Enter distinct positive ticket numbers and choose a board priority.";
      update();
      return;
    }
    const labels = new Map(board.priorities.map(({ id, label }) => [id, label]));
    busy = true;
    showRows([]);
    message = "Checking selected tickets…";
    update();
    let release: (() => void) | null = null;
    try {
      const inventory = await readInventory(tag);
      const byTicket = new Map([...inventory.values()].map((card) => [card.ticket, card]));
      const selected: Selection[] = [];
      for (const ticket of values) {
        const card = byTicket.get(ticket);
        if (!card || card.completed_at !== null) {
          message = card
            ? `Ticket #${ticket} is Completed.`
            : `Ticket #${ticket} is unavailable on this board.`;
          return;
        }
        selected.push({ id: card.id, ticket });
      }
      if (contextTag() !== tag) {
        message = "The board must be confirmed at the original revision before saving.";
        return;
      }
      const ids = selected.map(({ id }) => id);
      release = beginWrite(ids);
      if (!release) {
        message = "A selected card or the board has another operation in progress.";
        return;
      }
      const intent: Intent = { etag: tag, priorityId: requested, selected };
      message = "Saving batch priorities…";
      update();
      const outcome = await submitOperation(
        () => writeJournal(intent),
        async () => {
          const reply = await api.setPriorities(boardId, tag, ids, requested);
          return {
            rows: confirmedRows(reply, boardId, intent, inventory, labels),
            etag: reply.etag,
          };
        },
      );
      switch (outcome.kind) {
        case "confirmed":
          minRevision = revision(boardId, outcome.result.etag ?? "");
          // A remounted view can inspect the original selection from the journal.
          if (signal.aborted) break;
          if (writeJournal(null)) {
            reset();
            showRows(outcome.result.rows);
            message = "Batch priorities saved.";
          } else {
            message =
              "Batch priorities were confirmed. Inspect the saved record before starting another batch.";
          }
          break;
        case "stale":
        case "refused":
          message = writeJournal(null)
            ? outcome.kind === "stale"
              ? "The board changed. Clear the selection and choose tickets again."
              : "The service refused the batch. Review the selection and board access."
            : "The service refused the batch. Inspect the saved record before starting another batch.";
          if (outcome.kind === "stale") minRevision = revision(boardId, tag) + 1;
          break;
        case "unknown":
          message = "The batch outcome is unknown. Inspect the saved priorities.";
          break;
        case "journal-failed":
          message = "Could not save the batch record in this tab.";
          break;
      }
      if (outcome.kind !== "journal-failed") onRefresh();
    } catch (error) {
      if (!signal.aborted) {
        const stale = error instanceof ApiError && error.status === 412;
        message = stale
          ? "The board changed. Clear the selection and choose tickets again."
          : "Could not verify the selected tickets. Check the board and try again.";
        if (stale) {
          minRevision = revision(boardId, tag) + 1;
          onRefresh();
        }
      }
    } finally {
      busy = false;
      release?.();
      update();
    }
  }

  async function inspectSaved() {
    update();
    const intent = pending;
    if (signal.aborted || inspect.disabled || !intent) return;
    const release = beginInspection();
    if (!release) {
      message = "A board operation is still in progress. Inspect when it finishes.";
      update();
      return;
    }
    const owned = raw;
    busy = true;
    inspectedTag = null;
    showRows([]);
    message = "Reading saved priorities…";
    update();
    try {
      const { etag } = await readWithSession(
        () => api.getBoardAvailability(boardId, signal),
        signal,
      );
      if (!etag) throw new ApiError("invalid_response");
      const inventory = await readInventory(etag);
      const current = getContext();
      const saved = revision(boardId, etag);
      if (
        saved < Math.max(revision(boardId, intent.etag), current.board?.board.revision ?? 0) ||
        tab()?.getItem(journalKey) !== owned
      ) {
        throw new ApiError("invalid_response");
      }
      const label = (id: string) =>
        current.board?.priorities.find((entry) => entry.id === id)?.label ?? id;
      showRows([
        `Requested priority · ${label(intent.priorityId)}`,
        ...intent.selected.map(({ id, ticket }) => {
          const card = inventory.get(id);
          if (!card) return `Original #${ticket} · unavailable on this board`;
          const original = card.ticket === ticket ? "" : ` (originally #${ticket})`;
          const completed = card.completed_at === null ? "" : " · Completed";
          return `#${card.ticket}${original} saved priority · ${label(card.priority_id)}${completed}`;
        }),
      ]);
      inspectedTag = etag;
      minRevision = saved;
      message = "Saved priorities inspected. The earlier batch outcome remains unknown.";
      onRefresh();
    } catch {
      if (!signal.aborted)
        message = "Could not inspect saved priorities. Inspect again to retry the read.";
    } finally {
      busy = false;
      release();
      update();
    }
  }

  function reset() {
    draftTag = inspectedTag = null;
    tickets.value = priority.value = "";
  }

  function edit() {
    if (busy || pending || journalProblem) return;
    draftTag ??= contextTag();
    if (!tickets.value.trim() && !priority.value) draftTag = null;
    message = "";
    showRows([]);
    update();
  }

  tickets.addEventListener("input", edit, { signal });
  priority.addEventListener("change", edit, { signal });
  form.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      void save().catch(globalThis.reportError);
    },
    { signal },
  );
  inspect.addEventListener("click", () => void inspectSaved().catch(globalThis.reportError), {
    signal,
  });
  clear.addEventListener(
    "click",
    () => {
      update();
      if (signal.aborted || clear.disabled || clear.hidden) return;
      if (pending && !writeJournal(null)) {
        message = "The inspected batch record could not be cleared.";
      } else {
        reset();
        message = "";
        showRows([]);
      }
      update();
    },
    { signal },
  );
  showRows([]);
  update();
  return { update };
}
