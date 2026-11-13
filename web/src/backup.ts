import { ApiError } from "../api.mjs";
import { newOperationId } from "../operation-id.mjs";
import type { AppContext } from "./model.js";
import { submitOperation } from "./operations.js";
import type { createWorkspaceState } from "./workspace-state.js";

export type CapturedBackup = Readonly<{
  id: string;
  board_id: string;
  board_revision: number;
  size: number;
  sha2_256: string;
}>;

type CaptureIntent = Readonly<{ id: string; revision: number }>;
type BackupState = {
  pending?: CaptureIntent;
  backup?: CapturedBackup;
  busy: boolean;
  downloading: boolean;
  journalProblem: boolean;
  message: string;
  observers: Set<() => void>;
};
type Options = AppContext &
  Readonly<{
    boardId: string;
    readWithSession: ReturnType<typeof createWorkspaceState>["readWithSession"];
    canRead: () => boolean;
    captureContext: () => { etag: string | null; revision: number | null; allowed: boolean };
  }>;

const operationId = /^[\da-f]{8}-[\da-f]{4}-7[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/;
const digest = /^[\da-f]{64}$/;
const states = new WeakMap<Document, Map<string, BackupState>>();

export function formatOperationTime(id: string): string {
  const timestamp = Number.parseInt(id.slice(0, 8) + id.slice(9, 13), 16);
  return new Date(timestamp)
    .toISOString()
    .replace("T", " ")
    .replace(/\.\d{3}Z$/, "+00:00");
}

function readIntent(value: unknown): CaptureIntent {
  if (
    typeof value !== "object" ||
    value === null ||
    !("id" in value) ||
    typeof value.id !== "string" ||
    !operationId.test(value.id) ||
    !("revision" in value) ||
    typeof value.revision !== "number" ||
    !Number.isSafeInteger(value.revision) ||
    value.revision <= 0
  ) {
    throw new TypeError("Invalid backup capture record.");
  }
  return { id: value.id, revision: value.revision };
}

function captureMetadata(value: unknown, boardId: string, intent: CaptureIntent): CapturedBackup {
  if (
    typeof value !== "object" ||
    value === null ||
    !("id" in value) ||
    value.id !== intent.id ||
    !("board_id" in value) ||
    value.board_id !== boardId ||
    !("board_revision" in value) ||
    value.board_revision !== intent.revision ||
    !("size" in value) ||
    typeof value.size !== "number" ||
    !Number.isSafeInteger(value.size) ||
    value.size <= 0 ||
    !("sha2_256" in value) ||
    typeof value.sha2_256 !== "string" ||
    !digest.test(value.sha2_256)
  ) {
    throw new ApiError("invalid_response", { outcomeUnknown: true });
  }
  return Object.freeze({
    id: intent.id,
    board_id: boardId,
    board_revision: intent.revision,
    size: value.size,
    sha2_256: value.sha2_256,
  });
}

function boardState(doc: Document, boardId: string, key: string): BackupState {
  const boards = states.get(doc) ?? new Map<string, BackupState>();
  states.set(doc, boards);
  const existing = boards.get(boardId);
  if (existing) return existing;
  const state: BackupState = {
    busy: false,
    downloading: false,
    journalProblem: false,
    message: "",
    observers: new Set(),
  };
  try {
    const storage = doc.defaultView?.sessionStorage;
    if (!storage) throw new Error("Tab storage is unavailable.");
    const raw = storage.getItem(key);
    if (raw !== null) {
      state.pending = readIntent(JSON.parse(raw) as unknown);
      state.message = "Backup capture could not be confirmed. Refresh backup status to inspect it.";
    }
  } catch {
    state.journalProblem = true;
    state.message = "The tab’s backup capture record could not be read.";
  }
  boards.set(boardId, state);
  return state;
}

export function mountBackup(
  host: HTMLElement,
  { api, boardId, signal, readWithSession, canRead, captureContext }: Options,
) {
  const doc = host.ownerDocument;
  const key = `localswim:pending-backup-capture:${boardId}`;
  const state = boardState(doc, boardId, key);
  const heading = doc.createElement("h2");
  const create = doc.createElement("button");
  const inspect = doc.createElement("button");
  const download = doc.createElement("button");
  const summary = doc.createElement("p");
  const status = doc.createElement("p");
  heading.textContent = "Board backup";
  create.type = inspect.type = download.type = "button";
  create.textContent = "Create backup";
  inspect.textContent = "Refresh backup status";
  download.textContent = "Download backup";
  status.setAttribute("role", "status");
  summary.setAttribute("aria-live", "polite");
  host.replaceChildren(heading, create, inspect, download, summary, status);

  function capturedBackup(): CapturedBackup | undefined {
    return !signal.aborted && canRead() && !state.busy && !state.pending ? state.backup : undefined;
  }

  function update() {
    if (signal.aborted) return;
    const readable = canRead();
    const capture = captureContext();
    const backup = capturedBackup();
    host.hidden = !readable || (!capture.allowed && !state.pending && !backup);
    create.disabled =
      !readable ||
      !capture.allowed ||
      capture.etag === null ||
      capture.revision === null ||
      state.journalProblem ||
      state.busy ||
      state.downloading ||
      state.pending !== undefined;
    inspect.hidden = state.pending === undefined;
    inspect.disabled = !readable || state.busy;
    download.hidden = backup === undefined;
    download.disabled = state.busy || state.downloading;
    const description = backup
      ? `Captured revision ${backup.board_revision} at ${formatOperationTime(backup.id)}.`
      : "";
    if (summary.textContent !== description) summary.textContent = description;
    if (status.textContent !== state.message) status.textContent = state.message;
  }

  function notify() {
    for (const observer of state.observers) observer();
  }

  function save(intent?: CaptureIntent): boolean {
    try {
      const storage = doc.defaultView?.sessionStorage;
      if (!storage) return false;
      if (intent) storage.setItem(key, JSON.stringify(intent));
      else storage.removeItem(key);
      state.pending = intent;
      return true;
    } catch {
      return false;
    }
  }

  function settle(message: string) {
    state.message = save(undefined)
      ? message
      : "The capture was resolved. Refresh backup status to clear its tab record.";
  }

  async function createBackup() {
    const { etag, revision, allowed } = captureContext();
    if (
      signal.aborted ||
      !canRead() ||
      !allowed ||
      etag === null ||
      revision === null ||
      !Number.isSafeInteger(revision) ||
      revision <= 0 ||
      etag !== `"${boardId}-${revision}"` ||
      state.journalProblem ||
      state.pending ||
      state.busy ||
      state.downloading
    )
      return;

    const intent = { id: newOperationId(), revision };
    state.busy = true;
    state.message = "Capturing backup…";
    try {
      notify();
      const result = await submitOperation(
        () => save(intent),
        async () => {
          const { data } = await api.createBackup(intent.id, boardId, etag);
          return captureMetadata(data, boardId, intent);
        },
      );
      switch (result.kind) {
        case "confirmed":
          state.backup = result.result;
          settle("");
          break;
        case "unknown":
          state.message =
            "Backup capture could not be confirmed. Refresh backup status to inspect it.";
          break;
        case "stale":
          settle("The board changed before capture. Refresh the board before creating a backup.");
          break;
        case "refused":
          settle("The service refused the backup capture.");
          break;
        case "journal-failed":
          state.message = "The tab’s backup capture record could not be saved.";
          break;
      }
    } finally {
      state.busy = false;
      notify();
    }
  }

  async function inspectBackup() {
    const intent = state.pending;
    if (signal.aborted || !canRead() || !intent || state.busy) return;
    state.busy = true;
    state.message = "Checking backup status…";
    try {
      notify();
      const { data } = await readWithSession(() => api.getBackup(intent.id, boardId), signal);
      if (!signal.aborted && canRead()) {
        state.backup = captureMetadata(data, boardId, intent);
        settle("");
      } else {
        state.message = "Refresh backup status to inspect the original capture.";
      }
    } catch {
      // A missing backup may still be in flight. Keep the original capture ID.
      state.message = "Backup capture could not be confirmed. Refresh backup status to inspect it.";
    } finally {
      state.busy = false;
      notify();
    }
  }

  async function downloadBackup() {
    const backup = capturedBackup();
    if (!backup || state.downloading || state.busy) return;
    state.downloading = true;
    state.message = "Verifying backup download…";
    try {
      notify();
      // The API verifies the ZIP media type, exact size, and SHA-256 against this capture.
      const blob = await readWithSession(
        () => api.getBackupContent(backup.id, backup.size, backup.sha2_256),
        signal,
      );
      if (!signal.aborted && canRead()) {
        const anchor = doc.createElement("a");
        const url = URL.createObjectURL(blob);
        anchor.href = url;
        anchor.download = `localswim-${boardId}-r${backup.board_revision}.zip`;
        try {
          anchor.click();
        } finally {
          setTimeout(() => URL.revokeObjectURL(url), 1000);
        }
      }
      state.message = "";
    } catch {
      state.message = signal.aborted ? "" : "Backup download could not be verified. Try again.";
    } finally {
      state.downloading = false;
      notify();
    }
  }

  create.addEventListener("click", () => void createBackup(), { signal });
  inspect.addEventListener("click", () => void inspectBackup(), { signal });
  download.addEventListener("click", () => void downloadBackup(), { signal });
  if (!signal.aborted) state.observers.add(update);
  signal.addEventListener("abort", () => state.observers.delete(update), { once: true });
  update();
  return { update, capturedBackup };
}
