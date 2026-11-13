import {
  ApiError,
  DEFAULT_APPROVAL_COMMENT,
  MAX_BULK_APPROVAL_COMMENT_LENGTH,
  MAX_LANE_APPROVAL_CARDS,
} from "../api.mjs";
import { newOperationId } from "../operation-id.mjs";
import type { AppContext } from "./model.js";
import { submitOperation, type OperationStorage } from "./operations.js";

type CardVersion = Readonly<{ id: string; revision: number }>;
export type LaneApprovalContext = Readonly<{
  laneLabel: string;
  boardEtag: string;
  completedLaneId: string;
  bulkComment: string;
  cards: readonly CardVersion[];
  complete: boolean;
  writable: boolean;
}>;

type Scope = Pick<LaneApprovalContext, "boardEtag" | "completedLaneId" | "bulkComment" | "cards"> &
  Readonly<{ laneId: string }>;
type Intent = Scope & Readonly<{ id: string }>;
type SavedIntent = Readonly<{ raw: string; intent: Intent; cardIds: ReadonlySet<string> }>;
type Options = AppContext &
  Readonly<{
    boardId: string;
    actorId: () => string | undefined;
    isPageActive: () => boolean;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    getContext: (laneId: string) => LaneApprovalContext | null;
    beginWrite: (cardIds: readonly string[]) => (() => void) | null;
    beginInspection: () => (() => void) | null;
    onConfirmed: (cardIds: readonly string[]) => void;
    onRefresh: () => void;
    focusFallback: () => void;
    storage?: OperationStorage;
  }>;

const uuidv4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const uuidv7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function resourceId(value: unknown): value is string {
  return typeof value === "string" && value.length === 36 && uuidv4.test(value);
}

function revisionOf(tag: string, boardId: string) {
  const revision = Number(tag.slice(boardId.length + 2, -1));
  return Number.isSafeInteger(revision) && revision > 0 && tag === `"${boardId}-${revision}"`
    ? revision
    : 0;
}

function cardVersions(value: unknown): value is CardVersion[] {
  if (!Array.isArray(value) || !value.length || value.length > MAX_LANE_APPROVAL_CARDS)
    return false;
  const ids = new Set<string>();
  for (const card of value as unknown[]) {
    if (
      !record(card) ||
      !resourceId(card.id) ||
      typeof card.revision !== "number" ||
      !Number.isSafeInteger(card.revision) ||
      card.revision < 1 ||
      ids.has(card.id)
    ) {
      return false;
    }
    ids.add(card.id);
  }
  return true;
}

function parseIntent(raw: string, boardId: string): Intent {
  const value: unknown = JSON.parse(raw);
  if (record(value)) {
    const { id, laneId, boardEtag, completedLaneId, bulkComment, cards } = value;
    if (
      typeof id === "string" &&
      id.length === 36 &&
      uuidv7.test(id) &&
      resourceId(laneId) &&
      resourceId(completedLaneId) &&
      laneId !== completedLaneId &&
      typeof boardEtag === "string" &&
      revisionOf(boardEtag, boardId) > 0 &&
      typeof bulkComment === "string" &&
      [...bulkComment].length <= MAX_BULK_APPROVAL_COMMENT_LENGTH &&
      cardVersions(cards)
    ) {
      return { id, laneId, boardEtag, completedLaneId, bulkComment, cards };
    }
  }
  throw new TypeError("Invalid lane approval record.");
}

function matchesScope(scope: Scope, current: LaneApprovalContext | null) {
  return (
    current?.complete === true &&
    scope.boardEtag === current.boardEtag &&
    scope.completedLaneId === current.completedLaneId &&
    scope.bulkComment === current.bulkComment &&
    scope.cards.length === current.cards.length &&
    scope.cards.every(
      ({ id, revision }, index) =>
        current.cards[index]?.id === id && current.cards[index].revision === revision,
    )
  );
}

export function mountLaneApproval(host: HTMLElement, options: Options) {
  const {
    api,
    boardId,
    actorId,
    isPageActive,
    readWithSession,
    getContext,
    beginWrite,
    beginInspection,
    onConfirmed,
    onRefresh,
    focusFallback,
  } = options;
  const lifetime = new AbortController();
  const signal = AbortSignal.any([options.signal, lifetime.signal]);
  const journal = () => options.storage ?? globalThis.sessionStorage;
  const keyFor = (actor: string) => `localswim:v2:lane-approval:${actor}:${boardId}`;
  const doc = host.ownerDocument;
  const dialog = doc.createElement("dialog");
  dialog.className = "lane-approval";
  const heading = doc.createElement("h2");
  heading.id = "lane-approval-heading";
  dialog.setAttribute("aria-labelledby", heading.id);
  const status = doc.createElement("p");
  status.tabIndex = -1;
  status.setAttribute("role", "status");
  const summary = doc.createElement("p");
  const comment = doc.createElement("p");
  comment.className = "lane-approval-comment";
  const actions = doc.createElement("div");
  actions.className = "lane-approval-actions";
  const closeAction = button("Close");
  const approveAction = button("Approve all cards");
  const inspectAction = button("Inspect original approval");
  const recovery = button("Inspect pending lane approval");
  recovery.className = "lane-approval-recovery";
  actions.append(closeAction, approveAction, inspectAction);
  dialog.append(heading, status, summary, comment, actions);
  host.append(dialog, recovery);

  let opener: HTMLElement | undefined;
  let captured: { actor: string; scope: Scope; laneLabel: string } | undefined;
  let active: { actor: string; phase: "submitting" | "inspecting" } | undefined;
  let notice: { actor: string; text: string } | undefined;
  let lastRead: SavedIntent | null = null;
  const expired = new Map<string, number>();

  function button(label: string) {
    const element = doc.createElement("button");
    element.type = "button";
    element.textContent = label;
    return element;
  }

  function readJournal(actor: string): SavedIntent | null | undefined {
    try {
      const raw = journal().getItem(keyFor(actor));
      if (raw === null) {
        lastRead = null;
        return null;
      }
      if (lastRead?.raw !== raw) {
        const intent = parseIntent(raw, boardId);
        lastRead = { raw, intent, cardIds: new Set(intent.cards.map(({ id }) => id)) };
      }
      return lastRead;
    } catch {
      return undefined;
    }
  }

  function writeJournal(actor: string, expected: string | null, next: string | null) {
    try {
      const store = journal();
      const key = keyFor(actor);
      if (store.getItem(key) !== expected) return false;
      if (next === null) store.removeItem(key);
      else store.setItem(key, next);
      return true;
    } catch {
      return false;
    }
  }

  function canAct() {
    return !signal.aborted && isPageActive() && !active;
  }

  function canSettle(actor: string) {
    return !signal.aborted && isPageActive() && actorId() === actor;
  }

  function setNotice(actor: string, text: string) {
    notice = { actor, text };
  }

  function update() {
    if (signal.aborted) return;
    const actor = actorId();
    const saved = actor ? readJournal(actor) : null;
    recovery.hidden = !actor || saved === null;
    recovery.disabled = !canAct();
    if (!dialog.open) return;
    if (captured && captured.actor !== actor) captured = undefined;
    const scope = captured?.scope;
    const current = scope ? getContext(scope.laneId) : null;
    if (scope && !saved && !matchesScope(scope, current)) {
      captured = undefined;
      if (actor)
        setNotice(actor, "The lane view changed. Reopen approval to review the current cards.");
    }
    approveAction.hidden = !captured || saved !== null;
    approveAction.disabled = !canAct() || current?.writable !== true;
    inspectAction.hidden = !saved;
    inspectAction.disabled = !canAct();
    const shown = saved?.intent ?? captured?.scope;
    heading.textContent = captured ? `Approve all cards in ${captured.laneLabel}` : "Lane approval";
    if (shown) {
      const count = shown.cards.length;
      const cards = `${count} ${count === 1 ? "card" : "cards"}`;
      summary.textContent = saved
        ? `Original approval for ${cards} with this comment on each card.`
        : `${cards} will move to Completed with this comment on each card.`;
      comment.textContent = shown.bulkComment || DEFAULT_APPROVAL_COMMENT;
    } else {
      summary.textContent = "";
      comment.textContent = "";
    }
    const busy = actor && active?.actor === actor ? active.phase : undefined;
    const message =
      busy === "submitting"
        ? "Approving lane…"
        : busy === "inspecting"
          ? "Checking the original approval receipt…"
          : saved === undefined
            ? "The lane approval record could not be read from tab storage."
            : (notice?.actor === actor ? notice?.text : "") ||
              (saved ? "Inspect the original approval receipt before approving again." : "");
    if (status.textContent !== message) status.textContent = message;
    if (
      [approveAction, inspectAction].some(
        (action) => action === doc.activeElement && (action.hidden || action.disabled),
      )
    ) {
      status.focus({ preventScroll: true });
    }
  }

  function open(laneId: string | undefined, source: HTMLElement) {
    const actor = actorId();
    if (!actor || !canAct() || dialog.open) return;
    const saved = readJournal(actor);
    captured = undefined;
    notice = undefined;
    const current = laneId ? getContext(laneId) : null;
    if (saved === null && laneId && current) {
      const { boardEtag, completedLaneId, bulkComment, cards } = current;
      if (
        !current.complete ||
        !current.writable ||
        !resourceId(laneId) ||
        !resourceId(completedLaneId) ||
        laneId === completedLaneId ||
        revisionOf(boardEtag, boardId) <= (expired.get(actor) ?? 0) ||
        [...bulkComment].length > MAX_BULK_APPROVAL_COMMENT_LENGTH ||
        !cardVersions(cards)
      ) {
        return;
      }
      captured = {
        actor,
        laneLabel: current.laneLabel,
        scope: {
          laneId,
          boardEtag,
          completedLaneId,
          bulkComment,
          cards: cards.map(({ id, revision }) => ({ id, revision })),
        },
      };
    } else if (saved === null) {
      return;
    }
    opener = source;
    dialog.showModal();
    update();
    if (!inspectAction.hidden && !inspectAction.disabled) inspectAction.focus();
    else closeAction.focus();
  }

  function close(restoreFocus: boolean) {
    if (!dialog.open) return;
    dialog.close();
    captured = undefined;
    if (restoreFocus) {
      if (opener?.isConnected && !opener.hidden && !opener.matches(":disabled")) {
        opener.focus({ preventScroll: true });
      } else {
        focusFallback();
      }
    }
    opener = undefined;
  }

  function markExpired(actor: string, intent: Intent) {
    expired.set(actor, revisionOf(intent.boardEtag, boardId));
  }

  function confirmed(actor: string, intent: Intent, raw: string) {
    if (readJournal(actor)?.raw !== raw) return;
    markExpired(actor, intent);
    setNotice(
      actor,
      writeJournal(actor, raw, null)
        ? "Lane approved and moved to Completed."
        : "Approval was confirmed. Its record could not be cleared from tab storage.",
    );
    onConfirmed(intent.cards.map(({ id }) => id));
  }

  async function submit() {
    if (!canAct() || !dialog.open) return;
    update();
    const selection = captured;
    if (!selection || actorId() !== selection.actor || readJournal(selection.actor) !== null)
      return;
    const { actor, scope } = selection;
    const current = getContext(scope.laneId);
    if (current?.writable !== true || !matchesScope(scope, current)) return;
    const intent = { ...scope, id: newOperationId() };
    const raw = JSON.stringify(intent);
    const release = beginWrite(intent.cards.map(({ id }) => id));
    if (!release) {
      setNotice(actor, "Wait for the current board or card change to finish before approving.");
      update();
      return;
    }
    active = { actor, phase: "submitting" };
    captured = undefined;
    notice = undefined;
    try {
      const result = await submitOperation(
        () => writeJournal(actor, null, raw),
        () => {
          update();
          return api.approveLane(
            intent.id,
            boardId,
            intent.boardEtag,
            intent.laneId,
            intent.completedLaneId,
            [...intent.cards],
            intent.bulkComment,
            actor,
          );
        },
      );
      if (result.kind === "refused") {
        // This attempt cannot commit. Clear its record even if its route or
        // principal has changed, since the board revision may stay unchanged.
        if (readJournal(actor)?.raw !== raw) return;
        const cleared = writeJournal(actor, raw, null);
        if (!canSettle(actor)) return;
        setNotice(
          actor,
          cleared
            ? "Lane approval was refused. Reopen approval to review the current cards."
            : "Approval was refused. Its record could not be cleared from tab storage.",
        );
        onRefresh();
        return;
      }
      if (!canSettle(actor)) return;
      if (result.kind === "journal-failed") {
        if (!readJournal(actor))
          setNotice(actor, "The lane approval record could not be saved in tab storage.");
        return;
      }
      if (readJournal(actor)?.raw !== raw) return;
      switch (result.kind) {
        case "confirmed":
          confirmed(actor, intent, raw);
          break;
        case "stale":
          setNotice(actor, "The board or lane changed. Inspect the original approval receipt.");
          onRefresh();
          break;
        case "unknown":
          setNotice(
            actor,
            "The approval outcome is unknown. Inspect the original approval receipt.",
          );
          onRefresh();
          break;
      }
    } finally {
      active = undefined;
      release();
      update();
    }
  }

  async function findReceipt(actor: string, intent: Intent) {
    try {
      await readWithSession(
        () =>
          api.inspectLaneApproval(
            intent.id,
            boardId,
            intent.boardEtag,
            intent.laneId,
            intent.completedLaneId,
            intent.cards.map(({ id }) => id),
            intent.bulkComment,
            { actorId: actor, signal },
          ),
        signal,
      );
      return true;
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) return false;
      throw error;
    }
  }

  async function inspect() {
    const actor = actorId();
    if (!actor || !dialog.open || !canAct()) return;
    const saved = readJournal(actor);
    if (!saved) return;
    const release = beginInspection();
    if (!release) {
      setNotice(actor, "Wait for the current board change to finish before inspecting.");
      update();
      return;
    }
    active = { actor, phase: "inspecting" };
    const { intent, raw } = saved;
    const current = () => canSettle(actor) && readJournal(actor)?.raw === raw;
    const readBoard = () =>
      readWithSession(() => api.getBoardAvailability(boardId, signal), signal);
    try {
      update();
      if (await findReceipt(actor, intent)) {
        if (current()) confirmed(actor, intent, raw);
        return;
      }
      if (!current()) return;
      const board = await readBoard();
      if (!current()) return;
      if (
        board.data.availability !== "unhealthy" &&
        board.data.revision > revisionOf(intent.boardEtag, boardId)
      ) {
        // An advanced validator fences the old POST. Check for a receipt that
        // committed before that fence, and confirm a stable readable board.
        if (await findReceipt(actor, intent)) {
          if (current()) confirmed(actor, intent, raw);
          return;
        }
        if (!current()) return;
        const after = await readBoard();
        if (!current()) return;
        if (after.etag === board.etag && after.data.availability !== "unhealthy") {
          markExpired(actor, intent);
          setNotice(
            actor,
            writeJournal(actor, raw, null)
              ? "The board changed and the original approval was not found."
              : "The original approval was not found. Its record could not be cleared from tab storage.",
          );
          onRefresh();
          return;
        }
      }
      setNotice(
        actor,
        "The original approval has not been found. Inspect again to check its outcome.",
      );
    } catch {
      if (current())
        setNotice(actor, "The original approval could not be verified. Try inspecting again.");
    } finally {
      active = undefined;
      release();
      update();
    }
  }

  function run(action: () => Promise<void>) {
    void action().catch(() => {
      const actor = actorId();
      if (!actor || signal.aborted) return;
      setNotice(actor, "Could not complete the lane approval action.");
      update();
    });
  }

  approveAction.addEventListener("click", () => run(submit), { signal });
  inspectAction.addEventListener("click", () => run(inspect), { signal });
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
    open: (laneId: string, source: HTMLElement) => open(laneId, source),
    update,
    suspend: () => close(false),
    dispose() {
      close(false);
      lifetime.abort();
      dialog.remove();
      recovery.remove();
    },
    blocksCard(cardId: string) {
      const actor = actorId();
      if (!actor) return false;
      return readJournal(actor)?.cardIds.has(cardId) === true;
    },
  };
}
