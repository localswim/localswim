import { ApiError } from "../api.mjs";
import type { BoardViewData } from "./board-view.js";
import type { AppContext } from "./model.js";
import { submitOperation, type OperationStorage } from "./operations.js";

type Card = Pick<BoardViewData["cards"][number], "id" | "ticket" | "title">;
type Assignment = Readonly<{ card_id: string; ticket: number }>;

export type TicketOrderContext = Readonly<{
  laneLabel: string;
  boardEtag: string;
  boardRevision: number;
  cards: readonly Card[];
  complete: boolean;
  writable: boolean;
}>;

type Options = AppContext &
  Readonly<{
    boardId: string;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    getContext: (laneId: string) => TicketOrderContext | null;
    beginWrite: (cardIds: readonly string[]) => (() => void) | null;
    beginInspection?: () => (() => void) | null;
    onRefresh: () => void;
    focusFallback: () => void;
    storage?: OperationStorage;
  }>;

type Intent = Readonly<{
  laneId: string;
  etag: string;
  revision: number;
  current: readonly Assignment[];
  proposed: readonly Assignment[];
}>;
type Preview = Readonly<{ scope: TicketOrderContext; intent: Intent; changed: boolean }>;
type ApiReply = Awaited<ReturnType<AppContext["api"]["previewTicketOrder"]>>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const maximumCards = 1000;

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function id(value: unknown): value is string {
  return typeof value === "string" && value.length === 36 && uuid.test(value);
}

function positiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function assignments(value: unknown): value is Assignment[] {
  if (!Array.isArray(value) || value.length < 2 || value.length > maximumCards) return false;
  const ids = new Set<string>();
  let previous = 0;
  for (const row of value as unknown[]) {
    if (!record(row) || !id(row.card_id) || !positiveInteger(row.ticket)) return false;
    if (row.ticket <= previous || ids.has(row.card_id)) return false;
    previous = row.ticket;
    ids.add(row.card_id);
  }
  return true;
}

function validPermutation(current: readonly Assignment[], proposed: readonly Assignment[]) {
  const ids = new Set(current.map(({ card_id }) => card_id));
  return (
    current.length === proposed.length &&
    proposed.every(
      ({ card_id, ticket }, index) => ids.has(card_id) && current[index]?.ticket === ticket,
    )
  );
}

function sameAssignments(first: readonly Assignment[], second: readonly Assignment[]) {
  return (
    first.length === second.length &&
    first.every(
      ({ card_id, ticket }, index) =>
        second[index]?.card_id === card_id && second[index].ticket === ticket,
    )
  );
}

function matchesCards(cards: readonly Card[], entries: readonly Assignment[]) {
  const tickets = new Map(cards.map(({ id: cardId, ticket }) => [cardId, ticket]));
  return (
    cards.length === entries.length &&
    entries.every(({ card_id, ticket }) => tickets.get(card_id) === ticket)
  );
}

function parseIntent(value: unknown, boardId: string): Intent {
  if (record(value)) {
    const { laneId, etag, revision, current, proposed } = value;
    if (
      id(laneId) &&
      positiveInteger(revision) &&
      etag === `"${boardId}-${revision}"` &&
      assignments(current) &&
      assignments(proposed) &&
      validPermutation(current, proposed) &&
      !sameAssignments(current, proposed)
    )
      return { laneId, etag, revision, current, proposed };
  }
  throw new TypeError("Invalid pending ticket order.");
}

function readPreview(reply: ApiReply, scope: TicketOrderContext, laneId: string): Preview {
  const { current, proposed, changed, board_revision: revision } = reply.data;
  if (
    reply.etag !== scope.boardEtag ||
    revision !== scope.boardRevision ||
    !assignments(current) ||
    !assignments(proposed) ||
    !matchesCards(scope.cards, current) ||
    !validPermutation(current, proposed) ||
    changed !== !sameAssignments(current, proposed)
  )
    throw new ApiError("invalid_response");
  return { scope, intent: { laneId, etag: reply.etag, revision, current, proposed }, changed };
}

function sameScope(scope: TicketOrderContext | null, preview: Preview) {
  return (
    scope?.boardEtag === preview.intent.etag &&
    scope.boardRevision === preview.intent.revision &&
    scope.complete === preview.scope.complete &&
    matchesCards(scope.cards, preview.intent.current)
  );
}

export function mountTicketOrder(host: HTMLElement, options: Options) {
  const {
    api,
    boardId,
    readWithSession,
    getContext,
    beginWrite,
    beginInspection,
    onRefresh,
    focusFallback,
  } = options;
  const lifetime = new AbortController();
  const signal = AbortSignal.any([options.signal, lifetime.signal]);
  const journal = `localswim:pending-ticket-order:${boardId}`;
  const storage = () => options.storage ?? globalThis.sessionStorage;
  const doc = host.ownerDocument;
  const dialog = doc.createElement("dialog");
  dialog.className = "ticket-order";
  const heading = doc.createElement("h2");
  heading.id = "ticket-order-heading";
  dialog.setAttribute("aria-labelledby", heading.id);
  const status = doc.createElement("p");
  status.className = "ticket-order-status";
  status.tabIndex = -1;
  status.setAttribute("role", "status");
  const list = doc.createElement("ol");
  list.className = "ticket-order-assignments";
  const actions = doc.createElement("div");
  actions.className = "ticket-order-actions";
  const previewAction = button("Preview ticket order");
  const applyAction = button("Apply ticket order");
  const inspectAction = button("Inspect saved ticket order");
  const dismissAction = button("Dismiss inspected attempt");
  const closeAction = button("Close");
  const recovery = button("Inspect pending ticket order");
  actions.append(closeAction, previewAction, applyAction, inspectAction, dismissAction);
  dialog.append(heading, status, list, actions);
  host.append(dialog, recovery);

  let laneId: string | undefined;
  let opener: HTMLElement | undefined;
  let preview: Preview | undefined;
  let rows: readonly string[] = [];
  let message = "";
  let request: AbortController | undefined;
  let applying = false;
  let journalProblem = false;
  let raw: string | null = null;
  let pending: Intent | undefined;
  let inspected: string | undefined;

  function button(text: string) {
    const element = doc.createElement("button");
    element.type = "button";
    element.textContent = text;
    return element;
  }

  function readJournal() {
    try {
      const next = storage().getItem(journal);
      if (next !== raw) {
        pending = next === null ? undefined : parseIntent(JSON.parse(next) as unknown, boardId);
        raw = next;
        inspected = undefined;
        preview = undefined;
        rows = [];
        message = "";
      }
      journalProblem = false;
    } catch {
      journalProblem = true;
    }
  }

  function writeJournal(expected: string | null, value: Intent | null) {
    try {
      const store = storage();
      if (store.getItem(journal) !== expected) return false;
      const next = value === null ? null : JSON.stringify(value);
      if (next === null) store.removeItem(journal);
      else store.setItem(journal, next);
      raw = next;
      pending = value ?? undefined;
      inspected = undefined;
      return true;
    } catch {
      return false;
    }
  }

  function context() {
    return laneId ? getContext(laneId) : null;
  }

  function canPreview(scope: TicketOrderContext | null): scope is TicketOrderContext {
    return (
      dialog.open &&
      !request &&
      !applying &&
      !pending &&
      scope !== null &&
      scope.cards.length >= 2 &&
      scope.cards.length <= maximumCards
    );
  }

  function canApply(scope: TicketOrderContext | null, proposal: Preview | undefined) {
    return (
      canPreview(scope) &&
      !journalProblem &&
      scope.complete &&
      scope.writable &&
      proposal?.changed === true &&
      sameScope(scope, proposal)
    );
  }

  let paintedRows: readonly string[] | undefined;
  function update() {
    if (signal.aborted) return;
    if (!applying && !request) readJournal();
    recovery.hidden = !pending && !journalProblem;
    if (!dialog.open) return;
    const scope = context();
    if (preview && !sameScope(scope, preview)) {
      preview = undefined;
      rows = [];
      message = "The lane view changed. Preview the current cards again.";
    }
    heading.textContent = scope ? `Ticket order in ${scope.laneLabel}` : "Ticket order";
    if (paintedRows !== rows) {
      list.replaceChildren(
        ...rows.map((text) => {
          const item = doc.createElement("li");
          item.textContent = text;
          return item;
        }),
      );
      paintedRows = rows;
    }
    list.hidden = rows.length === 0;
    previewAction.hidden = Boolean(pending);
    previewAction.disabled = !canPreview(scope);
    applyAction.hidden = preview?.changed !== true || scope?.complete !== true || !scope.writable;
    applyAction.disabled = !canApply(scope, preview);
    inspectAction.hidden = !pending || applying;
    inspectAction.disabled = Boolean(request || journalProblem);
    dismissAction.hidden = !pending || inspected === undefined || inspected !== raw;
    dismissAction.disabled = Boolean(request || applying || journalProblem);
    let guidance = "";
    if (journalProblem) guidance = "Could not access the pending ticket-order record in this tab.";
    else if (pending)
      guidance = "Inspect the saved assignments before applying another ticket order.";
    else if (!scope) guidance = "The lane’s current cards are unavailable.";
    else if (scope.cards.length < 2)
      guidance = "Show at least two active cards to preview ticket order.";
    else if (scope.cards.length > maximumCards)
      guidance = "Show at most 1,000 active cards to preview ticket order.";
    else if (!scope.complete) guidance = "Show the full unfiltered lane to apply a ticket order.";
    else if (!scope.writable && preview?.changed)
      guidance = "Applying ticket order is currently unavailable.";
    const next = [message, guidance].filter(Boolean).join(" ");
    if (status.textContent !== next) status.textContent = next;
    if (
      [previewAction, applyAction, inspectAction, dismissAction].some(
        (action) => action === doc.activeElement && (action.hidden || action.disabled),
      )
    )
      status.focus({ preventScroll: true });
  }

  function open(nextLaneId: string | undefined, source: HTMLElement) {
    if (signal.aborted || dialog.open) return;
    readJournal();
    laneId = pending?.laneId ?? nextLaneId;
    opener = source;
    preview = undefined;
    rows = [];
    message = "";
    dialog.showModal();
    update();
    if (!inspectAction.hidden && !inspectAction.disabled) inspectAction.focus();
    else if (!previewAction.disabled) previewAction.focus();
    else closeAction.focus();
  }

  function close(restoreFocus: boolean) {
    if (!dialog.open) return;
    request?.abort();
    request = undefined;
    dialog.close();
    if (restoreFocus) {
      if (opener?.isConnected && !opener.hidden) opener.focus({ preventScroll: true });
      else focusFallback();
    }
    opener = undefined;
    preview = undefined;
  }

  async function loadPreview() {
    const scope = context();
    const capturedLane = laneId;
    if (signal.aborted || !capturedLane || !canPreview(scope)) return;
    const read = new AbortController();
    request = read;
    const readSignal = AbortSignal.any([signal, read.signal]);
    const captured = { ...scope, cards: scope.cards.map((card) => ({ ...card })) };
    preview = undefined;
    rows = [];
    message = "Preparing ticket-order preview…";
    update();
    try {
      const reply = await readWithSession(() => {
        readSignal.throwIfAborted();
        return api.previewTicketOrder(
          boardId,
          capturedLane,
          captured.cards.map(({ id: cardId }) => cardId),
        );
      }, readSignal);
      readSignal.throwIfAborted();
      if (reply.etag !== captured.boardEtag) {
        message = "The board changed. Preview again after it refreshes.";
        onRefresh();
        return;
      }
      const next = readPreview(reply, captured, capturedLane);
      if (!sameScope(context(), next)) {
        message = "The lane view changed. Preview the current cards again.";
        return;
      }
      const cards = new Map(captured.cards.map((card) => [card.id, card]));
      rows = next.intent.proposed.map(({ card_id, ticket }) => {
        const card = cards.get(card_id);
        if (!card) throw new ApiError("invalid_response");
        return `#${card.ticket} → #${ticket} ${card.title}`;
      });
      preview = next;
      message = next.changed
        ? `Preview at board revision ${next.intent.revision}.`
        : "The tickets are already in dependency order.";
    } catch (error) {
      if (!readSignal.aborted) {
        message =
          error instanceof ApiError && error.status === 400
            ? "The service refused the preview. Check the selected cards for a blocking cycle."
            : "Could not preview ticket order.";
      }
    } finally {
      if (request === read) request = undefined;
      update();
    }
  }

  async function inspect(intent: Intent, expected: string, confirmedRevision?: number) {
    const read = new AbortController();
    request = read;
    const readSignal = AbortSignal.any([signal, read.signal]);
    inspected = undefined;
    message = "Reading saved ticket assignments…";
    update();
    try {
      const reply = await readWithSession(() => api.getBoardLanes(boardId, readSignal), readSignal);
      readSignal.throwIfAborted();
      // The shared adapter validates the board identity, revision, and active card records.
      const saved = reply.data as Pick<BoardViewData, "board" | "cards">;
      const revision = saved.board.revision;
      if (revision < (confirmedRevision ?? intent.revision)) {
        throw new ApiError("invalid_response");
      }
      const byId = new Map(saved.cards.map((card) => [card.id, card]));
      const matches =
        revision > intent.revision &&
        intent.proposed.every(({ card_id, ticket }) => byId.get(card_id)?.ticket === ticket);
      rows = intent.proposed.map(({ card_id, ticket }) => {
        const card = byId.get(card_id);
        return card
          ? `Requested #${ticket}, saved #${card.ticket} ${card.title}`
          : `Requested #${ticket}, saved card unavailable`;
      });
      if (matches && writeJournal(expected, null)) {
        message = "Saved ticket assignments match the requested order.";
      } else if (matches) {
        message = "Saved assignments match. Could not clear the pending ticket-order record.";
      } else {
        inspected = expected;
        message =
          confirmedRevision === undefined
            ? "The saved assignments differ from the requested order. The Apply outcome remains unknown."
            : "The saved assignments differ from the applied order.";
      }
      onRefresh();
    } catch {
      if (!readSignal.aborted) message = "Could not read the saved assignments. Inspect again.";
    } finally {
      if (request === read) request = undefined;
      update();
    }
  }

  async function apply() {
    if (signal.aborted) return;
    update();
    const proposal = preview;
    if (!proposal || !canApply(context(), proposal)) return;
    const { intent } = proposal;
    const release = beginWrite(intent.proposed.map(({ card_id }) => card_id));
    if (!release) {
      message = "Wait for the current board or card operation to finish.";
      update();
      return;
    }
    applying = true;
    preview = undefined;
    message = "Applying ticket order…";
    try {
      const outcome = await submitOperation(
        () => writeJournal(null, intent),
        async () => {
          update();
          const reply = await api.applyTicketOrder(boardId, intent.laneId, intent.etag, [
            ...intent.proposed,
          ]);
          const { data } = reply;
          // A changed order commits once against the captured board revision.
          if (
            data.changed !== true ||
            data.board_revision !== intent.revision + 1 ||
            !assignments(data.current) ||
            !sameAssignments(data.current, intent.current) ||
            !assignments(data.proposed) ||
            !sameAssignments(data.proposed, intent.proposed)
          )
            throw new ApiError("invalid_response", { outcomeUnknown: true });
          return data.board_revision;
        },
      );
      if (outcome.kind === "journal-failed") {
        message = "Could not save the ticket-order intent in this tab.";
        return;
      }
      const expected = raw;
      if (expected === null) throw new Error("The ticket-order intent is unavailable.");
      if (outcome.kind === "confirmed" && !signal.aborted) {
        await inspect(intent, expected, outcome.result);
      } else if (outcome.kind === "stale" || outcome.kind === "refused") {
        const cleared = writeJournal(expected, null);
        message =
          outcome.kind === "stale"
            ? "The board changed before Apply. Preview again after it refreshes."
            : "The service refused the ticket order. Preview again to review the current cards.";
        if (!cleared) message += " Could not clear the pending ticket-order record.";
        rows = [];
        onRefresh();
      } else {
        message = "The Apply outcome is unknown.";
        onRefresh();
      }
    } finally {
      applying = false;
      release();
      update();
    }
  }

  async function inspectPending() {
    if (signal.aborted || request || applying) return;
    update();
    if (!pending || raw === null || journalProblem) return;
    const release = beginInspection ? beginInspection() : beginWrite([]);
    if (!release) return;
    try {
      await inspect(pending, raw);
    } finally {
      release();
    }
  }

  function dismiss() {
    if (signal.aborted || request || applying) return;
    update();
    if (!pending || raw === null || inspected !== raw || journalProblem) return;
    message = writeJournal(raw, null)
      ? "Inspected ticket-order attempt dismissed."
      : "Could not clear the pending ticket-order record.";
    onRefresh();
    update();
  }

  function run(action: () => Promise<void>) {
    void action().catch(() => {
      message = "Could not complete the ticket-order action.";
      update();
    });
  }

  previewAction.addEventListener("click", () => run(loadPreview), { signal });
  applyAction.addEventListener("click", () => run(apply), { signal });
  inspectAction.addEventListener("click", () => run(inspectPending), { signal });
  dismissAction.addEventListener("click", dismiss, { signal });
  recovery.addEventListener("click", () => open(undefined, recovery), { signal });
  closeAction.addEventListener("click", () => close(true), { signal });
  dialog.addEventListener(
    "cancel",
    (event) => {
      event.preventDefault();
      close(true);
    },
    { signal },
  );
  update();
  return {
    open: (nextLaneId: string, source: HTMLElement) => open(nextLaneId, source),
    update,
    suspend: () => close(false),
    dispose() {
      close(false);
      lifetime.abort();
      dialog.remove();
      recovery.remove();
    },
  };
}
