import { ApiError } from "../api.mjs";
import type { AppContext } from "./model.js";
import { createScopedOperations, submitOperation, type OperationStorage } from "./operations.js";

export type ArchiveCardContext = Readonly<{
  cardId: string;
  laneId: string;
  archived: boolean;
  etag: string;
  writable: boolean;
  laneActive: boolean;
}>;

type ArchiveOptions = AppContext &
  Readonly<{
    boardId: string;
    storage?: OperationStorage;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    beginWrite: (cardId: string) => (() => void) | null;
    onRefresh: () => void;
  }>;

type Intent = Readonly<{ archived: boolean; etag: string; laneId: string }>;
type CardReply = Readonly<{ data: object; etag: string | null }>;
type Journal = Readonly<{ raw: string; intent: Intent | null }>;
type Feedback = Readonly<{ message: string; waitFor?: string }>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const strongTag = /^"[A-Za-z0-9_-]{16,128}"(?![\s\S])/;

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseIntent(raw: string): Intent | null {
  if (raw.length > 256) return null;
  try {
    const value: unknown = JSON.parse(raw);
    if (!record(value)) return null;
    const { archived, etag, laneId } = value;
    if (
      typeof archived === "boolean" &&
      typeof etag === "string" &&
      strongTag.test(etag) &&
      typeof laneId === "string" &&
      uuid.test(laneId)
    ) {
      return { archived, etag, laneId };
    }
    return null;
  } catch {
    return null;
  }
}

function readVersion(boardId: string, cardId: string, { data, etag }: CardReply): Intent {
  const card = data as Record<string, unknown>;
  if (
    card.board_id !== boardId ||
    card.id !== cardId ||
    typeof card.archived !== "boolean" ||
    typeof card.lane_id !== "string" ||
    !uuid.test(card.lane_id) ||
    etag === null ||
    !strongTag.test(etag)
  ) {
    throw new ApiError("invalid_response", { outcomeUnknown: true });
  }
  return { archived: card.archived, etag, laneId: card.lane_id };
}

function reached(saved: Intent, target: Intent) {
  return (
    saved.archived === target.archived &&
    saved.laneId === target.laneId &&
    saved.etag !== target.etag
  );
}

export function mountCardArchive(
  host: HTMLElement,
  {
    api,
    signal: ownerSignal,
    boardId,
    storage,
    readWithSession,
    beginWrite,
    onRefresh,
  }: ArchiveOptions,
) {
  const doc = host.ownerDocument;
  const lifetime = new AbortController();
  const signal = AbortSignal.any([ownerSignal, lifetime.signal]);
  const controls = doc.createElement("div");
  controls.className = "card-archive-actions";
  controls.hidden = true;
  const action = doc.createElement("button");
  action.type = "button";
  const inspection = doc.createElement("button");
  inspection.type = "button";
  inspection.textContent = "Inspect card";
  const status = doc.createElement("p");
  status.setAttribute("role", "status");
  controls.append(action, inspection, status);
  host.append(controls);

  const operations = createScopedOperations();
  const feedback = new Map<string, Feedback>();
  let context: ArchiveCardContext | null = null;
  let read: AbortController | undefined;
  const tabStorage = () => storage ?? doc.defaultView?.sessionStorage;
  const key = (cardId: string) => `localswim:v2:card-archive:${boardId}:${cardId}`;

  function load(cardId: string): Journal | null | undefined {
    try {
      const raw = tabStorage()?.getItem(key(cardId));
      return typeof raw === "string" ? { raw, intent: parseIntent(raw) } : raw;
    } catch {
      return undefined;
    }
  }

  function store(cardId: string, raw: string): boolean {
    try {
      const store = tabStorage();
      if (!store || store.getItem(key(cardId)) !== null) return false;
      store.setItem(key(cardId), raw);
      return true;
    } catch {
      return false;
    }
  }

  function clear(cardId: string, raw: string): boolean {
    try {
      const store = tabStorage();
      if (!store || store.getItem(key(cardId)) !== raw) return false;
      store.removeItem(key(cardId));
      return true;
    } catch {
      return false;
    }
  }

  function canSubmit(current: ArchiveCardContext) {
    return (
      current.writable &&
      (!current.archived || current.laneActive) &&
      !operations.state(current.cardId) &&
      load(current.cardId) === null &&
      feedback.get(current.cardId)?.waitFor !== current.etag
    );
  }

  function render() {
    if (signal.aborted) return;
    controls.hidden = context === null;
    if (!context) return;
    const { cardId, archived, laneActive } = context;
    const pending = load(cardId);
    const operation = operations.state(cardId);
    const label = archived ? "Restore card" : "Archive card";
    if (action.textContent !== label) action.textContent = label;
    action.disabled = !canSubmit(context);
    if (!pending && doc.activeElement === inspection && !action.disabled) {
      action.focus({ preventScroll: true });
    }
    inspection.hidden = !pending;
    inspection.disabled = Boolean(operation);
    const message =
      operation === "submitting"
        ? "Saving card…"
        : operation === "inspecting"
          ? "Reading current card…"
          : (feedback.get(cardId)?.message ??
            (pending
              ? "Archive or restore outcome is unknown. Inspect the current card."
              : pending === undefined
                ? "Card changes need available tab storage."
                : archived && !laneActive
                  ? "Restoring requires the card’s original lane to be active."
                  : ""));
    if (status.textContent !== message) status.textContent = message;
    status.hidden = message === "";
  }

  function refresh() {
    if (!signal.aborted) onRefresh();
  }

  async function submit() {
    const current = context;
    if (signal.aborted || !current || !canSubmit(current)) return;
    const { cardId, etag, laneId } = current;
    const release = beginWrite(cardId);
    if (!release) return;
    const intent: Intent = { archived: !current.archived, etag, laneId };
    const raw = JSON.stringify(intent);
    feedback.delete(cardId);
    try {
      await operations.run(
        cardId,
        "submitting",
        async () => {
          render();
          const outcome = await submitOperation(
            () => store(cardId, raw),
            async () => {
              const reply = await api.patchCard(cardId, etag, { archived: intent.archived });
              if (!reached(readVersion(boardId, cardId, reply), intent)) {
                throw new ApiError("invalid_response", { outcomeUnknown: true });
              }
            },
          );
          switch (outcome.kind) {
            case "confirmed":
              const cleared = clear(cardId, raw);
              feedback.set(cardId, {
                message: !cleared
                  ? "The change was saved. Inspect the current card to clear this tab’s pending record."
                  : intent.archived
                    ? "Card archived."
                    : "Card restored.",
                waitFor: etag,
              });
              refresh();
              break;
            case "unknown":
              feedback.set(cardId, {
                message: "Archive or restore outcome is unknown. Inspect the current card.",
              });
              refresh();
              break;
            case "stale":
              clear(cardId, raw);
              feedback.set(cardId, {
                message: "The card changed. Reading the current card…",
                waitFor: etag,
              });
              refresh();
              break;
            case "refused":
              clear(cardId, raw);
              feedback.set(cardId, { message: "The service refused the card change." });
              refresh();
              break;
            case "journal-failed":
              feedback.set(cardId, { message: "Card change could not be recorded in this tab." });
              break;
          }
        },
        render,
      );
    } finally {
      release();
    }
  }

  async function inspect() {
    const cardId = context?.cardId;
    if (signal.aborted || !cardId || operations.state(cardId)) return;
    const journal = load(cardId);
    if (!journal) return;
    const request = new AbortController();
    read = request;
    const readSignal = AbortSignal.any([signal, request.signal]);
    await operations.run(
      cardId,
      "inspecting",
      async () => {
        render();
        try {
          const reply = await readWithSession(() => api.getCard(cardId, readSignal), readSignal);
          readSignal.throwIfAborted();
          const saved = readVersion(boardId, cardId, reply);
          const matched = journal.intent !== null && reached(saved, journal.intent);
          const message = !clear(cardId, journal.raw)
            ? "The card was read, but the saved outcome still needs inspection."
            : matched
              ? `The card is ${saved.archived ? "archived" : "active"} in its original lane.`
              : `The card is currently ${saved.archived ? "archived" : "active"}. The submitted change could not be confirmed.`;
          feedback.set(cardId, {
            message,
            waitFor: saved.etag === context?.etag ? undefined : context?.etag,
          });
          refresh();
        } catch {
          if (!readSignal.aborted) {
            feedback.set(cardId, { message: "The current card could not be read. Inspect again." });
          }
        } finally {
          if (read === request) read = undefined;
        }
      },
      render,
    );
  }

  action.addEventListener("click", () => void submit(), { signal });
  inspection.addEventListener("click", () => void inspect(), { signal });

  return {
    update(next: ArchiveCardContext | null) {
      if (next?.cardId !== context?.cardId) read?.abort();
      context = next;
      render();
    },
    dispose() {
      lifetime.abort();
      controls.remove();
    },
  };
}
