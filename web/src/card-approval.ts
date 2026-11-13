import { ApiError } from "../api.mjs";
import { newOperationId } from "../operation-id.mjs";
import type { AppContext } from "./model.js";
import { submitOperation, type OperationStorage } from "./operations.js";

type ApprovalContext = Readonly<{
  cardId: string;
  cardEtag: string;
  boardEtag: string;
  actorId: string;
  eligible: boolean;
  writable: boolean;
}>;
type Intent = Pick<ApprovalContext, "cardEtag" | "boardEtag"> & Readonly<{ id: string }>;
type Validators = Pick<Intent, "boardEtag" | "cardEtag">;
type Options = AppContext &
  Readonly<{
    boardId: string;
    storage?: OperationStorage;
    actorId: () => string | undefined;
    isPageActive?: () => boolean;
    context: () => ApprovalContext | null;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    beginWrite: (cardId: string) => (() => void) | null;
    beginInspection: (cardId: string) => (() => void) | null;
    onConfirmed: (cardId: string) => void;
    onRefresh: () => void;
  }>;

const uuidv7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function version(tag: unknown, resourceId: string) {
  const prefix = '"' + resourceId + "-";
  if (typeof tag !== "string" || !tag.startsWith(prefix) || !tag.endsWith('"')) return 0;
  const number = tag.slice(prefix.length, -1);
  const revision = Number(number);
  return Number.isSafeInteger(revision) && revision > 0 && String(revision) === number
    ? revision
    : 0;
}

function parseIntent(raw: string, boardId: string, cardId: string): Intent {
  const value: unknown = JSON.parse(raw);
  if (
    typeof value === "object" &&
    value !== null &&
    "id" in value &&
    "boardEtag" in value &&
    "cardEtag" in value
  ) {
    const { id, boardEtag, cardEtag } = value;
    if (
      typeof id === "string" &&
      uuidv7.test(id) &&
      typeof boardEtag === "string" &&
      version(boardEtag, boardId) &&
      typeof cardEtag === "string" &&
      version(cardEtag, cardId)
    ) {
      return { id, boardEtag, cardEtag };
    }
  }
  throw new TypeError("Invalid approval record.");
}

export function mountCardApproval(
  host: HTMLElement,
  {
    api,
    boardId,
    signal,
    storage,
    actorId,
    isPageActive = () => true,
    context,
    readWithSession,
    beginWrite,
    beginInspection,
    onConfirmed,
    onRefresh,
  }: Options,
) {
  const doc = host.ownerDocument;
  const approve = doc.createElement("button");
  const inspect = doc.createElement("button");
  const status = doc.createElement("p");
  approve.type = inspect.type = "button";
  approve.textContent = "Reviewed & approved";
  inspect.textContent = "Inspect approval";
  status.setAttribute("role", "status");
  host.replaceChildren(approve, inspect, status);

  const journal = () => storage ?? globalThis.sessionStorage;
  const keyFor = (actor: string, cardId: string) =>
    `localswim:v2:card-approval:${actor}:${boardId}:${cardId}`;
  const notice = new Map<string, string>();
  const expired = new Map<string, Validators>();
  let active: { key: string; phase: "submitting" | "inspecting" } | undefined;

  function load(key: string, cardId: string) {
    try {
      const raw = journal().getItem(key);
      if (raw !== null) return { raw, intent: parseIntent(raw, boardId, cardId) };
      return { raw: null, intent: null };
    } catch {
      return null;
    }
  }

  function selection() {
    const current = context();
    if (!current || actorId() !== current.actorId) return null;
    const key = keyFor(current.actorId, current.cardId);
    return { current, key, saved: load(key, current.cardId) };
  }

  function writable(current: ApprovalContext, key: string) {
    const previous = expired.get(key);
    const boardRevision = version(current.boardEtag, boardId);
    const cardRevision = version(current.cardEtag, current.cardId);
    return (
      current.eligible &&
      current.writable &&
      boardRevision > 0 &&
      cardRevision > 0 &&
      (!previous ||
        boardRevision > version(previous.boardEtag, boardId) ||
        cardRevision > version(previous.cardEtag, current.cardId))
    );
  }

  function update() {
    const selected = selection();
    const busy = selected && active?.key === selected.key ? active.phase : null;
    const pending = selected?.saved?.intent;
    const message = selected ? notice.get(selected.key) : undefined;
    host.hidden = !selected || !(selected.current.eligible || pending || busy || message);
    approve.hidden = !selected?.current.eligible || Boolean(pending);
    approve.disabled =
      signal.aborted ||
      !isPageActive() ||
      Boolean(active) ||
      selected?.saved?.raw !== null ||
      !writable(selected.current, selected.key);
    inspect.hidden = !pending;
    inspect.disabled = signal.aborted || !isPageActive() || Boolean(active);
    const text =
      busy === "submitting"
        ? "Approving card…"
        : busy === "inspecting"
          ? "Checking the original approval receipt…"
          : selected?.saved === null
            ? "The approval record could not be read from tab storage."
            : message ||
              (pending ? "Inspect the original approval receipt before continuing." : "");
    if (status.textContent !== text) status.textContent = text;
  }

  function owns(key: string, expected: string) {
    try {
      return journal().getItem(key) === expected;
    } catch {
      return false;
    }
  }

  function clear(key: string, expected: string) {
    if (!owns(key, expected)) return false;
    try {
      journal().removeItem(key);
      return true;
    } catch {
      return false;
    }
  }

  function canSettle(actor: string) {
    return !signal.aborted && isPageActive() && actorId() === actor;
  }

  function confirmed(key: string, cardId: string, intent: Intent, raw: string) {
    if (!owns(key, raw)) return;
    expired.set(key, intent);
    notice.set(
      key,
      clear(key, raw)
        ? "Card approved and moved to Completed."
        : "Approval was confirmed. Its record could not be cleared from tab storage.",
    );
    onConfirmed(cardId);
  }

  async function submit() {
    const selected = selection();
    if (
      signal.aborted ||
      !isPageActive() ||
      active ||
      selected?.saved?.raw !== null ||
      !writable(selected.current, selected.key)
    ) {
      return;
    }
    const {
      key,
      current: { actorId: actor, cardId, boardEtag, cardEtag },
    } = selected;
    let intent: Intent;
    try {
      intent = { id: newOperationId(), boardEtag, cardEtag };
    } catch {
      notice.set(key, "The approval receipt ID could not be created.");
      update();
      return;
    }
    const raw = JSON.stringify(intent);
    const release = beginWrite(cardId);
    if (!release) {
      notice.set(key, "Wait for the current card change to finish before approving.");
      update();
      return;
    }
    active = { key, phase: "submitting" };
    notice.delete(key);
    try {
      const result = await submitOperation(
        () => {
          try {
            if (journal().getItem(key) !== null) return false;
            journal().setItem(key, raw);
            update();
            return true;
          } catch {
            return false;
          }
        },
        () => api.approveCard(intent.id, boardId, intent.boardEtag, cardId, intent.cardEtag),
      );
      if (!canSettle(actor)) return;
      switch (result.kind) {
        case "confirmed":
          confirmed(key, cardId, intent, raw);
          break;
        case "stale":
          if (owns(key, raw))
            notice.set(key, "The board or card changed. Inspect the original approval receipt.");
          onRefresh();
          break;
        case "unknown":
          if (owns(key, raw))
            notice.set(
              key,
              "The approval outcome is unknown. Inspect the original approval receipt.",
            );
          onRefresh();
          break;
        case "refused":
          if (owns(key, raw)) {
            notice.set(
              key,
              clear(key, raw)
                ? "Approval was refused."
                : "Approval was refused. Its record could not be cleared from tab storage.",
            );
          }
          onRefresh();
          break;
        case "journal-failed":
          notice.set(key, "The approval record could not be saved in tab storage.");
          break;
      }
    } finally {
      active = undefined;
      release();
      if (!signal.aborted) update();
    }
  }

  async function findReceipt(id: string, cardId: string) {
    try {
      await readWithSession(() => api.inspectApproval(id, boardId, cardId), signal);
      return true;
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) return false;
      throw error;
    }
  }

  function readBoard() {
    return readWithSession(() => api.getBoardAvailability(boardId, signal), signal);
  }

  async function cardValidatorExpired(cardId: string, originalEtag: string) {
    const { data, etag } = await readWithSession(() => api.getCard(cardId, signal), signal);
    const revision = version(etag, cardId);
    return (
      "board_id" in data &&
      data.board_id === boardId &&
      "id" in data &&
      data.id === cardId &&
      "revision" in data &&
      data.revision === revision &&
      revision > version(originalEtag, cardId)
    );
  }

  async function inspectOriginal() {
    const selected = selection();
    if (signal.aborted || !isPageActive() || active || !selected?.saved?.intent) return;
    const {
      key,
      current: { cardId, actorId: actor },
      saved: { intent, raw },
    } = selected;
    const release = beginInspection(cardId);
    if (!release) {
      notice.set(key, "Wait for the current card change to finish before inspecting.");
      update();
      return;
    }
    active = { key, phase: "inspecting" };
    const current = () => canSettle(actor) && owns(key, raw);
    try {
      update();
      if (await findReceipt(intent.id, cardId)) {
        if (current()) confirmed(key, cardId, intent, raw);
        return;
      }
      if (!current()) return;
      const board = await readBoard();
      if (!current()) return;
      const originalRevision = version(intent.boardEtag, boardId);
      const validatorExpired =
        board.data.availability !== "unhealthy" &&
        (board.data.revision > originalRevision ||
          (board.etag === intent.boardEtag &&
            (await cardValidatorExpired(cardId, intent.cardEtag))));
      if (!current()) return;
      if (validatorExpired) {
        // The original POST cannot commit past a stale validator. Check the same
        // receipt again, with readable board observations on both sides of the miss.
        if (await findReceipt(intent.id, cardId)) {
          if (current()) confirmed(key, cardId, intent, raw);
          return;
        }
        if (!current()) return;
        const after = await readBoard();
        if (!current()) return;
        if (after.etag === board.etag && after.data.availability !== "unhealthy") {
          expired.set(key, intent);
          notice.set(
            key,
            clear(key, raw)
              ? "The board or card changed and the original approval was not found."
              : "The original approval was not found. Its record could not be cleared from tab storage.",
          );
          onRefresh();
          return;
        }
      }
      notice.set(
        key,
        "The original approval has not been found. Inspect again to check its outcome.",
      );
    } catch {
      if (current())
        notice.set(key, "The original approval could not be verified. Try inspecting again.");
    } finally {
      active = undefined;
      release();
      if (!signal.aborted) update();
    }
  }

  approve.addEventListener("click", () => void submit(), { signal });
  inspect.addEventListener("click", () => void inspectOriginal(), { signal });
  update();
  return {
    update,
    blocksCard(cardId: string) {
      const actor = actorId();
      if (!actor) return false;
      const key = keyFor(actor, cardId);
      if (active?.key === key) return true;
      return load(key, cardId)?.raw !== null;
    },
  };
}
