import { ApiError } from "../api.mjs";
import type { AppContext } from "./model.js";
import { createScopedOperations, submitOperation, type OperationStorage } from "./operations.js";

export type ArchiveCardContext = Readonly<{
  cardId: string;
  laneId: string;
  archived: boolean;
  etag: string;
  revision: number;
  writable: boolean;
  laneActive: boolean;
}>;

type ArchiveOptions = AppContext &
  Readonly<{
    boardId: string;
    storage?: OperationStorage;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    beginWrite: (cardId: string) => (() => void) | null;
    beginInspect: (cardId: string) => (() => void) | null;
    onRefresh: () => void;
  }>;

type Intent = Readonly<{ archived: boolean; etag: string; laneId: string; revision: number }>;
type CardReply = Readonly<{ data: object; etag: string | null }>;
type Journal = Readonly<{ raw: string; intent: Intent | null }>;
type Feedback = Readonly<{ message: string; waitFor?: string; inspectedJournal?: string }>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function validVersion(cardId: string, etag: unknown, revision: unknown): revision is number {
  return (
    typeof revision === "number" &&
    Number.isSafeInteger(revision) &&
    revision > 0 &&
    etag === `"${cardId}-${revision}"`
  );
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseIntent(cardId: string, raw: string): Intent | null {
  if (raw.length > 256) return null;
  try {
    const value: unknown = JSON.parse(raw);
    if (!record(value)) return null;
    const { archived, etag, laneId, revision } = value;
    if (
      typeof archived === "boolean" &&
      typeof etag === "string" &&
      validVersion(cardId, etag, revision) &&
      typeof laneId === "string" &&
      uuid.test(laneId)
    ) {
      return { archived, etag, laneId, revision };
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
    !validVersion(cardId, etag, card.revision)
  ) {
    throw new ApiError("invalid_response", { outcomeUnknown: true });
  }
  return { archived: card.archived, etag, laneId: card.lane_id, revision: card.revision };
}

function reached(saved: Intent, target: Intent) {
  return (
    saved.archived === target.archived &&
    saved.laneId === target.laneId &&
    saved.revision > target.revision
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
    beginInspect,
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
  const dismiss = doc.createElement("button");
  dismiss.type = "button";
  dismiss.textContent = "Dismiss unresolved change";
  const status = doc.createElement("p");
  status.setAttribute("role", "status");
  controls.append(action, inspection, dismiss, status);
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
      return typeof raw === "string" ? { raw, intent: parseIntent(cardId, raw) } : raw;
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
      validVersion(current.cardId, current.etag, current.revision) &&
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
    const notice = feedback.get(cardId);
    const label = archived ? "Restore card" : "Archive card";
    if (action.textContent !== label) action.textContent = label;
    action.disabled = !canSubmit(context);
    if (
      !pending &&
      (doc.activeElement === inspection || doc.activeElement === dismiss) &&
      !action.disabled
    ) {
      action.focus({ preventScroll: true });
    }
    inspection.hidden = !pending;
    inspection.disabled = Boolean(operation);
    dismiss.hidden = !pending || notice?.inspectedJournal !== pending.raw;
    dismiss.disabled = Boolean(operation);
    const message =
      operation === "submitting"
        ? "Saving card…"
        : operation === "inspecting"
          ? "Reading current card…"
          : (notice?.message ??
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
    const { cardId, etag, laneId, revision } = current;
    const release = beginWrite(cardId);
    if (!release) return;
    const intent: Intent = { archived: !current.archived, etag, laneId, revision };
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
    if (!load(cardId)) return;
    const release = beginInspect(cardId);
    if (!release) {
      feedback.set(cardId, {
        message: "A card operation is still in progress. Inspect again after it finishes.",
      });
      render();
      return;
    }
    try {
      const journal = load(cardId);
      if (!journal) return;
      const request = new AbortController();
      read = request;
      const readSignal = AbortSignal.any([signal, request.signal]);
      await operations.run(
        cardId,
        "inspecting",
        async () => {
          feedback.delete(cardId);
          render();
          try {
            const reply = await readWithSession(() => api.getCard(cardId, readSignal), readSignal);
            readSignal.throwIfAborted();
            const saved = readVersion(boardId, cardId, reply);
            if (saved.revision < Math.max(context?.revision ?? 0, journal.intent?.revision ?? 0)) {
              feedback.set(cardId, {
                message: "The read returned an older card version. Inspect again.",
              });
              return;
            }
            if (load(cardId)?.raw !== journal.raw) {
              feedback.set(cardId, { message: "The pending change was updated. Inspect again." });
              return;
            }
            const matched = journal.intent !== null && reached(saved, journal.intent);
            const state = saved.archived ? "archived" : "active";
            const message = matched
              ? clear(cardId, journal.raw)
                ? `The card is ${state} in its original lane.`
                : "The card was read, but the saved outcome still needs inspection."
              : `The card is currently ${state}. The submitted change could not be confirmed.`;
            feedback.set(cardId, {
              message,
              waitFor: saved.etag === context?.etag ? undefined : context?.etag,
              inspectedJournal: matched ? undefined : journal.raw,
            });
            refresh();
          } catch {
            if (!readSignal.aborted) {
              feedback.set(cardId, {
                message: "The current card could not be read. Inspect again.",
              });
            }
          } finally {
            if (read === request) read = undefined;
          }
        },
        render,
      );
    } finally {
      release();
    }
  }

  function dismissUnresolved() {
    const cardId = context?.cardId;
    if (signal.aborted || !cardId || operations.state(cardId)) return;
    const notice = feedback.get(cardId);
    if (notice?.inspectedJournal === undefined) return;
    const release = beginInspect(cardId);
    if (!release) return;
    try {
      if (clear(cardId, notice.inspectedJournal)) {
        feedback.set(cardId, { message: "Unresolved change dismissed.", waitFor: notice.waitFor });
      } else {
        feedback.set(cardId, {
          message: "The pending change could not be cleared. Inspect again.",
        });
      }
    } finally {
      release();
      render();
    }
  }

  action.addEventListener("click", () => void submit(), { signal });
  inspection.addEventListener("click", () => void inspect(), { signal });
  dismiss.addEventListener("click", dismissUnresolved, { signal });

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
