import { ApiError } from "../api.mjs";
import type { AppContext } from "./model.js";

type Context = Readonly<{
  name: string | null;
  revision: number | null;
  allowed: boolean;
  readable: boolean;
}>;
type Options = AppContext &
  Readonly<{
    boardId: string;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    getContext: () => Context;
    beginWrite: () => (() => void) | null;
    refresh: (boardId: string, revision?: number) => void;
    onStateChange?: () => void;
    onReconciled?: (revision: number) => void;
  }>;

type Intent = Readonly<{ attempt: string; revision: number }>;
type Journal = Intent &
  (
    | { phase: "submitted" | "damaged" | "unavailable" }
    | { phase: "restored" | "inspected"; observedRevision: number }
  );
type RestoreState = {
  busy: boolean;
  raw?: string | null;
  journal?: Journal;
  journalProblem: boolean;
  message: string;
  observers: Set<() => void>;
};

const states = new WeakMap<Document, Map<string, RestoreState>>();
const attemptId = /^[\da-f]{8}-[\da-f]{4}-4[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/;
const snapshotTag = /^"[\da-f]{64}"$/;
const unknown = "Restore outcome is unknown. Inspect the original board before making changes.";

function storageKey(boardId: string) {
  return `localswim:pending-board-restore:${boardId}`;
}

function storage(doc: Document): Storage {
  const current = doc.defaultView?.sessionStorage;
  if (!current) throw new Error("Tab storage is unavailable.");
  return current;
}

function isRevision(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function parseJournal(value: unknown): Journal {
  if (
    typeof value === "object" &&
    value !== null &&
    "attempt" in value &&
    typeof value.attempt === "string" &&
    attemptId.test(value.attempt) &&
    "revision" in value &&
    isRevision(value.revision) &&
    "phase" in value
  ) {
    const { attempt, revision, phase } = value;
    if (phase === "submitted" || phase === "damaged" || phase === "unavailable") {
      return { attempt, revision, phase };
    }
    if (
      (phase === "restored" || phase === "inspected") &&
      "observedRevision" in value &&
      isRevision(value.observedRevision) &&
      value.observedRevision >= revision &&
      (phase !== "restored" || value.observedRevision > revision)
    ) {
      return { attempt, revision, phase, observedRevision: value.observedRevision };
    }
  }
  throw new TypeError("Invalid board restore record.");
}

function pendingMessage(journal: Journal): string {
  switch (journal.phase) {
    case "submitted":
      return unknown;
    case "damaged":
      return "The native backup ZIP failed validation. Inspect the board before choosing another backup.";
    case "unavailable":
      return "The restore was refused because saved state is unavailable. Inspect the board.";
    case "restored":
      return `Native backup restored at revision ${journal.observedRevision}. Refreshing the board…`;
    case "inspected":
      return `The live and saved board agree at revision ${journal.observedRevision}. Refreshing the board…`;
  }
}

function boardState(doc: Document, boardId: string): RestoreState {
  const boards = states.get(doc) ?? new Map<string, RestoreState>();
  states.set(doc, boards);
  const previous = boards.get(boardId);
  if (previous) return previous;
  const next: RestoreState = {
    busy: false,
    journalProblem: false,
    message: "",
    observers: new Set(),
  };
  boards.set(boardId, next);
  return next;
}

export function hasPendingBoardRestore(doc: Document, boardId: string): boolean {
  const state = states.get(doc)?.get(boardId);
  if (state?.busy) return true;
  try {
    return storage(doc).getItem(storageKey(boardId)) !== null;
  } catch {
    return state?.raw != null;
  }
}

function snapshotRevision(value: unknown, boardId: string): number {
  if (
    typeof value !== "object" ||
    value === null ||
    !("format" in value) ||
    value.format !== "localswim-snapshot" ||
    !("format_version" in value) ||
    value.format_version !== 2 ||
    !("board" in value) ||
    typeof value.board !== "object" ||
    value.board === null ||
    !("id" in value.board) ||
    value.board.id !== boardId ||
    !("revision" in value.board) ||
    !isRevision(value.board.revision)
  ) {
    throw new ApiError("invalid_response");
  }
  return value.board.revision;
}

function durableRevision(value: unknown, boardId: string): number | null {
  if (
    typeof value !== "object" ||
    value === null ||
    !("board_id" in value) ||
    value.board_id !== boardId ||
    !("valid" in value) ||
    !("revision" in value) ||
    !("issues" in value) ||
    !Array.isArray(value.issues)
  ) {
    throw new ApiError("invalid_response");
  }
  if (value.valid === true && value.issues.length === 0 && isRevision(value.revision)) {
    return value.revision;
  }
  if (value.valid === false && value.revision === null && value.issues.length > 0) return null;
  throw new ApiError("invalid_response");
}

export function mountRestore(
  host: HTMLElement,
  {
    api,
    boardId,
    signal,
    readWithSession,
    getContext,
    beginWrite,
    refresh,
    onStateChange,
    onReconciled,
  }: Options,
) {
  const doc = host.ownerDocument;
  const state = boardState(doc, boardId);
  const key = storageKey(boardId);
  const heading = doc.createElement("h2");
  const label = doc.createElement("label");
  const picker = doc.createElement("input");
  const review = doc.createElement("button");
  const confirmation = doc.createElement("p");
  const confirm = doc.createElement("button");
  const cancel = doc.createElement("button");
  const inspect = doc.createElement("button");
  const status = doc.createElement("p");
  heading.textContent = "Restore board backup";
  label.textContent = "Native backup ZIP";
  picker.type = "file";
  picker.accept = ".zip,application/zip";
  label.append(picker);
  review.type = confirm.type = cancel.type = inspect.type = "button";
  review.textContent = "Review restore";
  confirm.textContent = "Restore backup";
  cancel.textContent = "Cancel";
  inspect.textContent = "Inspect board restore";
  status.setAttribute("role", "status");
  host.replaceChildren(heading, label, review, confirmation, confirm, cancel, inspect, status);

  let selection: { file: File; name: string; revision: number } | undefined;

  function readJournal(): boolean {
    try {
      const raw = storage(doc).getItem(key);
      if (!state.journalProblem && raw === state.raw) return true;
      const journal = raw === null ? undefined : parseJournal(JSON.parse(raw) as unknown);
      state.raw = raw;
      state.journal = journal;
      state.journalProblem = false;
      state.message = journal ? pendingMessage(journal) : "";
      return true;
    } catch {
      state.journalProblem = true;
      state.message = "The tab’s restore record could not be read.";
      return false;
    }
  }

  function writeJournal(expected: string | null, next?: Journal): boolean {
    try {
      const tab = storage(doc);
      if (tab.getItem(key) !== expected) return false;
      const raw = next ? JSON.stringify(next) : null;
      if (raw === null) tab.removeItem(key);
      else tab.setItem(key, raw);
      state.raw = raw;
      state.journal = next;
      return true;
    } catch {
      return false;
    }
  }

  function eligible(context: Context): context is Context & { name: string; revision: number } {
    return (
      context.readable && context.allowed && context.name !== null && isRevision(context.revision)
    );
  }

  function selectedAt(context: Context, chosen: NonNullable<typeof selection>) {
    return (
      eligible(context) && context.name === chosen.name && context.revision === chosen.revision
    );
  }

  function update() {
    if (signal.aborted) return;
    if (!state.busy) readJournal();
    const context = getContext();
    const journal = state.journal;
    if (
      !state.busy &&
      !state.journalProblem &&
      journal &&
      "observedRevision" in journal &&
      state.raw &&
      context.readable &&
      context.revision !== null &&
      context.revision >= journal.observedRevision
    ) {
      const saved = writeJournal(state.raw);
      state.message = saved
        ? journal.phase === "restored"
          ? `Native backup restored at revision ${journal.observedRevision}.`
          : `The live and saved board agree at revision ${journal.observedRevision}.`
        : "The tab’s restore record could not be cleared.";
      if (saved && journal.observedRevision > journal.revision) {
        onReconciled?.(journal.observedRevision);
      }
    }
    if (selection && !selectedAt(context, selection)) {
      selection = undefined;
      state.message = "The board changed. Review its current revision before restoring.";
    }
    const pending = state.journal !== undefined;
    const ready = eligible(context) && !state.busy && !state.journalProblem && !pending;
    host.hidden = !context.readable || (!context.allowed && !pending && !state.message);
    label.hidden = review.hidden = pending;
    picker.disabled = !ready || selection !== undefined;
    review.disabled = !ready || !picker.files?.[0] || selection !== undefined;
    confirmation.hidden = confirm.hidden = cancel.hidden = selection === undefined;
    confirm.disabled = !ready || selection === undefined;
    inspect.hidden = !pending;
    inspect.disabled = state.busy || state.journalProblem;
    const prompt = selection
      ? `Replace ${selection.name} at revision ${selection.revision} with ${selection.file.name}? Current board data will be replaced. Local drafts are kept.`
      : "";
    if (confirmation.textContent !== prompt) confirmation.textContent = prompt;
    if (status.textContent !== state.message) status.textContent = state.message;
  }

  function notify() {
    for (const observer of state.observers) observer();
  }

  function observe() {
    update();
    onStateChange?.();
  }

  function reviewRestore() {
    const file = picker.files?.[0];
    const context = getContext();
    if (
      signal.aborted ||
      !file ||
      !eligible(context) ||
      state.busy ||
      !readJournal() ||
      state.journal
    )
      return;
    selection = { file, name: context.name, revision: context.revision };
    state.message = "";
    update();
    confirm.focus();
  }

  async function submitRestore() {
    const chosen = selection;
    if (
      signal.aborted ||
      !chosen ||
      !selectedAt(getContext(), chosen) ||
      state.busy ||
      !readJournal() ||
      state.raw !== null
    ) {
      return;
    }
    const release = beginWrite();
    if (!release) {
      state.message = "A board operation is still running. Try again when it finishes.";
      update();
      return;
    }
    state.busy = true;
    selection = undefined;
    picker.value = "";
    state.message = "Checking the board revision…";
    let submitted: { raw: string; intent: Intent } | undefined;
    try {
      notify();
      const snapshot = await readWithSession(() => api.getSnapshot(boardId, signal), signal);
      if (signal.aborted) return;
      if (
        !selectedAt(getContext(), chosen) ||
        snapshotRevision(snapshot.data, boardId) !== chosen.revision
      ) {
        state.message = "The board changed. Review its current revision before restoring.";
        refresh(boardId);
        return;
      }
      if (!snapshot.etag || !snapshotTag.test(snapshot.etag))
        throw new ApiError("invalid_response");
      const intent = {
        attempt: crypto.randomUUID(),
        revision: chosen.revision,
        phase: "submitted",
      } as const;
      if (!writeJournal(null, intent) || !state.raw) {
        state.message = "The tab’s restore record could not be saved.";
        return;
      }
      submitted = { raw: state.raw, intent };
      state.message = "Restoring native backup…";
      notify();
      // Keep the request tied to the confirmed board even if its view is replaced.
      const reply = await api.restoreSnapshot(
        boardId,
        snapshot.etag,
        new Blob([chosen.file], { type: "application/zip" }),
      );
      const revision = reply.data.revision;
      if (revision <= chosen.revision)
        throw new ApiError("invalid_response", { outcomeUnknown: true });
      const saved = writeJournal(submitted.raw, {
        ...intent,
        phase: "restored",
        observedRevision: revision,
      });
      state.message = saved
        ? `Native backup restored at revision ${revision}. Refreshing the board…`
        : unknown;
      refresh(boardId, revision);
    } catch (error) {
      if (!submitted) {
        if (signal.aborted) return;
        state.message =
          "The current saved snapshot could not be verified. Inspect the board before restoring.";
      } else if (!(error instanceof ApiError) || error.outcomeUnknown) {
        state.message = unknown;
      } else if (error.status === 400 || error.status === 503) {
        const refused: Journal = {
          ...submitted.intent,
          phase: error.status === 400 ? "damaged" : "unavailable",
        };
        state.message = writeJournal(submitted.raw, refused) ? pendingMessage(refused) : unknown;
      } else if (!writeJournal(submitted.raw)) {
        state.message = "The restore was refused. The tab’s restore record could not be cleared.";
      } else {
        state.message =
          error.status === 412
            ? "The board changed. Review its current revision before restoring."
            : error.status === 413
              ? "The native backup ZIP exceeds the service size limit."
              : "The service refused the restore. Inspect the current board before choosing another backup.";
      }
      refresh(boardId);
    } finally {
      state.busy = false;
      release();
      notify();
    }
  }

  async function inspectRestore() {
    if (signal.aborted || !getContext().readable || state.busy || !readJournal()) return;
    const journal = state.journal;
    const original = state.raw;
    if (!journal || !original) return;
    if ("observedRevision" in journal) {
      if (journal.phase === "restored") refresh(boardId, journal.observedRevision);
      else refresh(boardId);
      notify();
      return;
    }
    state.busy = true;
    state.message = "Inspecting live and saved board state…";
    try {
      notify();
      const { data: report } = await readWithSession(
        () => api.getVerification(boardId, signal),
        signal,
      );
      const revision = durableRevision(report, boardId);
      const { data: live } = await readWithSession(
        () => api.getBoardAvailability(boardId, signal),
        signal,
      );
      if (signal.aborted || storage(doc).getItem(key) !== original) return;
      if (revision === null || live.availability === "unhealthy") {
        state.message =
          "The saved board is unavailable or failed verification. Inspect it again after recovery.";
      } else if (live.revision !== revision || revision < journal.revision) {
        state.message =
          "The live and saved board revisions could not be reconciled. Inspect the board again.";
      } else if (journal.phase === "submitted" && revision === journal.revision) {
        state.message = `The board is still at revision ${revision}. The restore outcome remains unknown.`;
      } else if (
        writeJournal(original, { ...journal, phase: "inspected", observedRevision: revision })
      ) {
        state.message = `The live and saved board agree at revision ${revision}. Refreshing the board…`;
      } else {
        state.message = "The tab’s restore record changed or could not be saved.";
      }
    } catch {
      if (!signal.aborted)
        state.message = "Board restore could not be inspected. Try inspecting it again.";
    } finally {
      try {
        if (!signal.aborted) refresh(boardId);
      } finally {
        state.busy = false;
        notify();
      }
    }
  }

  picker.addEventListener("change", update, { signal });
  review.addEventListener("click", reviewRestore, { signal });
  confirm.addEventListener("click", () => void submitRestore().catch(globalThis.reportError), {
    signal,
  });
  cancel.addEventListener(
    "click",
    () => {
      selection = undefined;
      update();
      review.focus();
    },
    { signal },
  );
  inspect.addEventListener("click", () => void inspectRestore().catch(globalThis.reportError), {
    signal,
  });
  doc.defaultView?.addEventListener(
    "pageshow",
    (event) => {
      if (event.persisted) observe();
    },
    { signal },
  );
  if (!signal.aborted) state.observers.add(observe);
  signal.addEventListener("abort", () => state.observers.delete(observe), { once: true });
  update();
  return { update };
}
