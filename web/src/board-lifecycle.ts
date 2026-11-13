import type { AppContext } from "./model.js";
import { submitOperation } from "./operations.js";

type Availability = "open" | "closed";
type Intent = { availability: Availability; etag: string };
type Context = Readonly<{
  canChange: boolean;
  availability: Availability | null;
  etag: string | null;
}>;
type Options = AppContext &
  Readonly<{
    boardId: string;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    context: () => Context;
    onChange: () => void;
    onBusyChange?: () => void;
    beginWrite: () => (() => void) | null;
    beginInspection: () => (() => void) | null;
  }>;

function parseIntent(value: unknown, boardId: string): Intent {
  if (typeof value === "object" && value !== null && "availability" in value && "etag" in value) {
    const { availability, etag } = value;
    const prefix = '"' + boardId + "-";
    if (
      (availability === "open" || availability === "closed") &&
      typeof etag === "string" &&
      etag.startsWith(prefix) &&
      /^[1-9][0-9]*"$/.test(etag.slice(prefix.length))
    ) {
      return { availability, etag };
    }
  }
  throw new TypeError("Invalid board availability intent.");
}

export function mountBoardLifecycle(
  host: HTMLElement,
  {
    api,
    boardId,
    signal,
    readWithSession,
    context,
    onChange,
    onBusyChange,
    beginWrite,
    beginInspection,
  }: Options,
) {
  const doc = host.ownerDocument;
  const journalKey = "localswim:pending-board-availability:" + boardId;
  const change = doc.createElement("button");
  const cancel = doc.createElement("button");
  const inspect = doc.createElement("button");
  const status = doc.createElement("p");
  change.type = cancel.type = inspect.type = "button";
  cancel.textContent = "Cancel";
  inspect.textContent = "Inspect board availability";
  status.setAttribute("role", "status");
  host.replaceChildren(change, cancel, inspect, status);

  let pending: Intent | undefined;
  let raw: string | null = null;
  let inspectedEtag: string | null = null;
  let closeConfirmation: string | null = null;
  let busy = false;
  let journalProblem = false;
  let message = "";

  function refreshJournal(force = false): boolean {
    try {
      const stored = globalThis.sessionStorage.getItem(journalKey);
      if (!force && !journalProblem && stored === raw) return true;
      pending = stored === null ? undefined : parseIntent(JSON.parse(stored) as unknown, boardId);
      raw = stored;
      inspectedEtag = null;
      journalProblem = false;
      closeConfirmation = null;
      message = pending ? "A board availability change needs inspection." : "";
    } catch {
      journalProblem = true;
      message = "The board availability record could not be read.";
    }
    return false;
  }

  function update() {
    // A detached controller can settle its attempt after this controller mounts.
    if (!busy) refreshJournal();
    const { canChange, availability, etag } = context();
    if (closeConfirmation !== null && (closeConfirmation !== etag || availability !== "open")) {
      closeConfirmation = null;
      message = "The board changed. Review the current board before closing.";
    }
    const close = availability !== "closed";
    change.hidden = availability === null || (pending !== undefined && inspectedEtag === null);
    change.disabled =
      busy ||
      journalProblem ||
      !canChange ||
      etag === null ||
      (pending !== undefined && (etag !== inspectedEtag || availability === pending.availability));
    change.textContent = pending
      ? pending.availability === "closed"
        ? "Retry closing board"
        : "Retry reopening board"
      : close
        ? closeConfirmation === null
          ? "Close board"
          : "Confirm close board"
        : "Reopen board";
    cancel.hidden = closeConfirmation === null;
    cancel.disabled = busy;
    inspect.hidden = pending === undefined;
    inspect.disabled = busy || journalProblem;
    if (status.textContent !== message) status.textContent = message;
  }

  function save(intent: Intent | undefined): boolean {
    if (!refreshJournal()) return false;
    try {
      if (intent) {
        const next = JSON.stringify({ ...intent, attempt: crypto.randomUUID() });
        globalThis.sessionStorage.setItem(journalKey, next);
        raw = next;
      } else {
        globalThis.sessionStorage.removeItem(journalKey);
        raw = null;
      }
      pending = intent;
      return true;
    } catch {
      return false;
    }
  }

  function confirmed(availability: Availability) {
    message = save(undefined)
      ? availability === "closed"
        ? "Board closed."
        : "Board reopened."
      : "Board availability was confirmed. Its change record could not be cleared.";
  }

  async function changeAvailability() {
    const { canChange, availability, etag } = context();
    if (
      signal.aborted ||
      busy ||
      journalProblem ||
      !canChange ||
      availability === null ||
      etag === null ||
      (pending && (etag !== inspectedEtag || availability === pending.availability))
    ) {
      return;
    }
    if (!pending && availability === "open" && closeConfirmation !== etag) {
      closeConfirmation = etag;
      message = "Closing pauses changes to this board. You can reopen it here.";
      update();
      return;
    }
    const release = beginWrite();
    if (!release) {
      message = "A board operation is still running. Try again when it finishes.";
      update();
      return;
    }
    const intent: Intent = { availability: availability === "open" ? "closed" : "open", etag };
    closeConfirmation = null;
    busy = true;
    inspectedEtag = null;
    message = availability === "open" ? "Closing board…" : "Reopening board…";
    try {
      onBusyChange?.();
      update();
      const result = await submitOperation(
        () => save(intent),
        () => api.setBoardAvailability(boardId, intent.availability, intent.etag),
      );
      switch (result.kind) {
        case "confirmed":
          confirmed(intent.availability);
          break;
        case "unknown":
          message = "The outcome is unknown. Inspect board availability before retrying.";
          break;
        case "stale":
          message = "The board changed. Inspect its current availability before retrying.";
          break;
        case "refused":
          message = "The availability change was refused. Inspect the board before retrying.";
          break;
        case "journal-failed":
          message = "The board availability record could not be saved.";
          break;
      }
      if (!signal.aborted && result.kind !== "journal-failed") onChange();
    } finally {
      busy = false;
      release();
      onBusyChange?.();
      if (!signal.aborted) update();
    }
  }

  async function inspectAvailability() {
    const intent = pending;
    if (signal.aborted || busy || journalProblem || !intent) return;
    const release = beginInspection();
    if (!release) {
      message = "A board operation is still running. Inspect availability when it finishes.";
      update();
      return;
    }
    busy = true;
    inspectedEtag = null;
    message = "Checking board availability…";
    try {
      onBusyChange?.();
      update();
      if (!refreshJournal()) return;
      const { data, etag } = await readWithSession(
        () => api.getBoardAvailability(boardId, signal),
        signal,
      );
      if (signal.aborted || !refreshJournal()) return;
      if (data.availability === intent.availability) {
        confirmed(data.availability);
      } else if (data.availability === "unhealthy") {
        message = "The board is unhealthy. Inspect it again when its saved state is available.";
      } else {
        inspectedEtag = etag;
        message =
          data.availability === "open"
            ? "The board is open. You can retry closing it."
            : "The board is closed. You can retry reopening it.";
      }
    } catch {
      message = "Could not inspect board availability. Try inspecting it again.";
    } finally {
      try {
        if (!signal.aborted) onChange();
      } finally {
        busy = false;
        release();
        onBusyChange?.();
        if (!signal.aborted) update();
      }
    }
  }

  change.addEventListener("click", () => void changeAvailability().catch(globalThis.reportError), {
    signal,
  });
  cancel.addEventListener(
    "click",
    () => {
      closeConfirmation = null;
      message = "";
      update();
      change.focus();
    },
    { signal },
  );
  inspect.addEventListener(
    "click",
    () => void inspectAvailability().catch(globalThis.reportError),
    {
      signal,
    },
  );
  doc.defaultView?.addEventListener(
    "pageshow",
    (event) => {
      if (!event.persisted || busy) return;
      refreshJournal(true);
      update();
    },
    { signal },
  );
  update();
  return {
    update,
    blocksWrites: () => busy || (pending !== undefined && inspectedEtag === null),
  };
}
