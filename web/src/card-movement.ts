import { ApiError } from "../api.mjs";
import type { BoardViewData } from "./board-view.js";
import type { AppContext } from "./model.js";
import { submitOperation, type OperationStorage } from "./operations.js";

type CardVersion = Readonly<{ revision: number; etag: string }>;
type MoveSource = Readonly<{ cardId: string; laneId: string; revision: number }>;
export type CardMoveContext = MoveSource & CardVersion & Readonly<{ archived: boolean }>;
type MoveIntent = Omit<CardMoveContext, "archived"> & Readonly<{ destination: string }>;
type SavedCard = Readonly<{
  id: string;
  board_id: string;
  lane_id: string;
  revision: number;
  archived: boolean;
}>;
type MovementOptions = AppContext &
  Readonly<{
    boardId: string;
    storage?: OperationStorage;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    getBoardData: () => BoardViewData | undefined;
    getPolicy: () => { moveLaneIds: ReadonlyMap<string, ReadonlySet<string>> } | null;
    canWrite: () => boolean;
    beginWrite: (cardId: string) => (() => void) | null;
    onChange: () => void;
    onRefresh: (cardId: string) => void;
  }>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const strongEtag = /^"[A-Za-z0-9_-]{16,128}"(?![\s\S])/;

function isId(value: unknown): value is string {
  return typeof value === "string" && uuid.test(value);
}

function parseMove(raw: string): MoveIntent {
  const value: unknown = JSON.parse(raw);
  if (!value || typeof value !== "object") throw new Error("Invalid pending move.");
  const { cardId, laneId, revision, etag, destination } = value as Record<string, unknown>;
  if (
    !isId(cardId) ||
    !isId(laneId) ||
    !isId(destination) ||
    laneId === destination ||
    typeof revision !== "number" ||
    !Number.isSafeInteger(revision) ||
    revision < 1 ||
    typeof etag !== "string" ||
    !strongEtag.test(etag)
  ) {
    throw new Error("Invalid pending move.");
  }
  return { cardId, laneId, revision, etag, destination };
}

export function createCardMovement({
  api,
  boardId,
  signal,
  storage,
  readWithSession,
  getBoardData,
  getPolicy,
  canWrite,
  beginWrite,
  onChange,
  onRefresh,
}: MovementOptions) {
  const key = `localswim:v2:card-move:${boardId}`;
  const journal = () => storage ?? globalThis.sessionStorage;
  const confirmed = new Map<string, CardVersion>();
  let pending: MoveIntent | undefined;
  let busy = false;
  let storageError = false;
  let message = "";
  let messageCard: string | undefined;

  try {
    const raw = journal().getItem(key);
    if (raw !== null) {
      pending = parseMove(raw);
      messageCard = pending.cardId;
      message = "A card move needs inspection.";
    }
  } catch {
    storageError = true;
    message = "Could not read the pending card move from tab storage.";
  }

  function announce(cardId: string, text: string) {
    messageCard = cardId;
    message = text;
    if (!signal.aborted) onChange();
  }

  function laneLabel(id: string) {
    return getBoardData()?.lanes.find((lane) => lane.id === id)?.label ?? "unavailable lane";
  }

  function destinations(source: MoveSource) {
    const granted = getPolicy()?.moveLaneIds.get(source.laneId);
    return getBoardData()?.lanes.filter(({ id }) => id !== source.laneId && granted?.has(id)) ?? [];
  }

  function available(source: MoveSource): boolean {
    const floor = confirmed.get(source.cardId);
    return (
      !signal.aborted &&
      !storageError &&
      !busy &&
      !pending &&
      (!floor || source.revision >= floor.revision) &&
      canWrite()
    );
  }

  function decodeCard(
    cardId: string,
    reply: { data: object; etag: string | null },
  ): CardMoveContext {
    // The API adapter validates the card shape. Check the requested board and HTTP validator.
    const card = reply.data as SavedCard;
    if (card.id !== cardId || card.board_id !== boardId || !reply.etag) {
      throw new ApiError("invalid_response", { outcomeUnknown: true });
    }
    return {
      cardId,
      revision: card.revision,
      etag: reply.etag,
      archived: card.archived,
      laneId: card.lane_id,
    };
  }

  async function readCard(cardId: string, requestSignal: AbortSignal) {
    const reply = await readWithSession(() => api.getCard(cardId, requestSignal), requestSignal);
    requestSignal.throwIfAborted();
    return decodeCard(cardId, reply);
  }

  async function prepareDrag(source: MoveSource, dragSignal: AbortSignal) {
    const requestSignal = AbortSignal.any([signal, dragSignal]);
    try {
      const card = await readCard(source.cardId, requestSignal);
      return card.revision === source.revision && card.laneId === source.laneId && !card.archived
        ? card
        : null;
    } catch {
      return null;
    }
  }

  function remember(card: CardMoveContext) {
    const floor = confirmed.get(card.cardId);
    if (
      floor &&
      (card.revision < floor.revision ||
        (card.revision === floor.revision && card.etag !== floor.etag))
    ) {
      throw new ApiError("invalid_response");
    }
    confirmed.set(card.cardId, { revision: card.revision, etag: card.etag });
  }

  function clearPending(intent: MoveIntent) {
    try {
      const raw = journal().getItem(key);
      if (raw !== null) {
        const stored = parseMove(raw);
        if (JSON.stringify(stored) !== JSON.stringify(intent)) {
          pending = stored;
          return false;
        }
      }
      journal().removeItem(key);
      pending = undefined;
      return true;
    } catch {
      return false;
    }
  }

  async function inspectPending(intent: MoveIntent, reason: string) {
    const { cardId } = intent;
    announce(cardId, `${reason} Checking the saved card…`);
    try {
      const saved = await readCard(cardId, signal);
      if (
        saved.revision < intent.revision ||
        (saved.revision === intent.revision &&
          (saved.etag !== intent.etag || saved.laneId !== intent.laneId))
      ) {
        throw new ApiError("invalid_response");
      }
      remember(saved);
      const current = saved.archived
        ? "The saved card is archived."
        : `The saved card is in ${laneLabel(saved.laneId)}.`;
      const cleaned = clearPending(intent);
      announce(
        cardId,
        cleaned ? `${reason} ${current}` : `${current} The pending move could not be cleared.`,
      );
    } catch (error) {
      if (
        !signal.aborted &&
        error instanceof ApiError &&
        error.status === 404 &&
        clearPending(intent)
      ) {
        announce(cardId, `${reason} The saved card is unavailable.`);
      } else {
        announce(cardId, `${reason} Could not inspect the saved card.`);
      }
    } finally {
      if (!signal.aborted) onRefresh(cardId);
    }
  }

  async function move(card: CardMoveContext, destination: string) {
    if (
      card.archived ||
      !available(card) ||
      !destinations(card).some(({ id }) => id === destination)
    ) {
      return;
    }
    const { cardId, laneId, revision, etag } = card;
    const release = beginWrite(cardId);
    if (!release) {
      announce(
        cardId,
        "Finish inspecting or saving the other card change before moving this card.",
      );
      return;
    }
    const intent = { cardId, laneId, revision, etag, destination };
    busy = true;
    try {
      const outcome = await submitOperation(
        () => {
          try {
            const stored = journal().getItem(key);
            if (stored !== null) {
              pending = parseMove(stored);
              return false;
            }
            journal().setItem(key, JSON.stringify(intent));
            pending = intent;
            return true;
          } catch {
            return false;
          }
        },
        async () => {
          announce(cardId, `Moving card to ${laneLabel(destination)}…`);
          const reply = await api.patchCard(cardId, etag, { lane_id: destination });
          const saved = decodeCard(cardId, reply);
          if (
            saved.laneId !== destination ||
            saved.archived ||
            saved.revision <= revision ||
            saved.etag === etag
          ) {
            throw new ApiError("invalid_response", { outcomeUnknown: true });
          }
          return saved;
        },
      );
      if (signal.aborted) return;
      switch (outcome.kind) {
        case "journal-failed":
          if (pending) announce(pending.cardId, "A card move needs inspection.");
          else announce(cardId, "Could not record this card move in tab storage.");
          break;
        case "confirmed":
          remember(outcome.result);
          announce(
            cardId,
            clearPending(intent)
              ? `Card moved to ${laneLabel(destination)}.`
              : "The move was saved, but the pending move could not be cleared. Inspect the saved card.",
          );
          onRefresh(cardId);
          break;
        case "stale":
          await inspectPending(intent, "The card changed before the move.");
          break;
        case "refused":
          await inspectPending(intent, "The service refused the move.");
          break;
        case "unknown":
          await inspectPending(intent, "The move result is unknown.");
          break;
      }
    } finally {
      busy = false;
      release();
      if (!signal.aborted) onChange();
    }
  }

  return {
    destinations,
    available,
    prepareDrag,
    move,
    async drop(
      source: MoveSource,
      destination: string,
      original: ReturnType<typeof prepareDrag>,
      dragSignal: AbortSignal,
    ) {
      const card = await original;
      if (dragSignal.aborted || signal.aborted) return;
      if (!card) {
        announce(source.cardId, "Could not confirm the original card. Refreshing the saved card…");
        onRefresh(source.cardId);
        return;
      }
      if (!available(card) || !destinations(card).some(({ id }) => id === destination)) {
        announce(source.cardId, "The move is no longer available. Check the current board.");
        return;
      }
      await move(card, destination);
    },
    status(cardId?: string) {
      const visible = cardId === undefined || messageCard === cardId || pending?.cardId === cardId;
      return {
        message: visible ? message : "",
        canInspect:
          !busy && Boolean(pending) && (cardId === undefined || pending?.cardId === cardId),
        pending: visible ? pending : undefined,
      };
    },
    async inspect() {
      if (signal.aborted || busy || !pending) return;
      const release = beginWrite(pending.cardId);
      if (!release) {
        announce(
          pending.cardId,
          "A card change is still in progress. Inspect the saved card again after it settles.",
        );
        return;
      }
      busy = true;
      try {
        await inspectPending(pending, "Inspecting the pending move.");
      } finally {
        busy = false;
        release();
        if (!signal.aborted) onChange();
      }
    },
    latestConfirmed: (cardId: string) => confirmed.get(cardId),
    blocksCard: (cardId: string) => pending?.cardId === cardId,
  };
}

export type CardMovement = ReturnType<typeof createCardMovement>;

export function mountCardMovement(
  root: HTMLElement,
  controller: CardMovement,
  getCard: () => CardMoveContext | null,
) {
  const doc = root.ownerDocument;
  const lifetime = new AbortController();
  const { signal } = lifetime;
  const form = doc.createElement("form");
  form.className = "card-movement-form";
  const label = doc.createElement("label");
  label.textContent = "Move to lane";
  const select = doc.createElement("select");
  label.append(select);
  const submit = doc.createElement("button");
  submit.type = "submit";
  submit.textContent = "Move card";
  form.append(label, submit);
  const status = doc.createElement("p");
  status.className = "card-movement-status";
  status.setAttribute("role", "status");
  const inspect = doc.createElement("button");
  inspect.type = "button";
  inspect.textContent = "Inspect saved card";
  root.append(form, status, inspect);

  let cardId: string | undefined;
  let original: CardMoveContext | null = null;
  let destination = "";
  let optionSignature = "";

  function update() {
    const card = getCard();
    if (card?.cardId !== cardId) {
      cardId = card?.cardId;
      original = null;
      destination = "";
    }
    const destinations = card && !card.archived ? controller.destinations(card) : [];
    const signature = JSON.stringify(destinations.map(({ id, label }) => [id, label]));
    if (optionSignature !== signature) {
      optionSignature = signature;
      const placeholder = doc.createElement("option");
      placeholder.value = "";
      placeholder.textContent = "Choose a lane";
      const options = destinations.map(({ id, label }) => {
        const option = doc.createElement("option");
        option.value = id;
        option.textContent = label;
        return option;
      });
      select.replaceChildren(placeholder, ...options);
    }
    if (!destinations.some(({ id }) => id === destination)) {
      original = null;
      destination = "";
    }
    select.value = destination;
    form.hidden = destinations.length === 0;
    select.disabled = !card || !controller.available(card);
    submit.disabled = select.disabled || !original || !controller.available(original);
    const feedback = card ? controller.status(card.cardId) : undefined;
    const message = feedback?.message ?? "";
    if (status.textContent !== message) status.textContent = message;
    status.hidden = !message;
    inspect.hidden = !feedback?.canInspect;
  }

  select.addEventListener(
    "change",
    () => {
      destination = select.value;
      original = getCard();
      update();
    },
    { signal },
  );
  form.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      if (submit.disabled || !original) return;
      const requested = destination;
      const card = original;
      original = null;
      destination = "";
      update();
      void controller.move(card, requested);
    },
    { signal },
  );
  inspect.addEventListener("click", () => void controller.inspect(), { signal });
  update();
  return {
    update,
    dispose() {
      lifetime.abort();
      form.remove();
      status.remove();
      inspect.remove();
    },
  };
}
