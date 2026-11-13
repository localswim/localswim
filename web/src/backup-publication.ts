import { ApiError, type BackupPublication } from "../api.mjs";
import { newOperationId } from "../operation-id.mjs";
import { formatOperationTime, type CapturedBackup } from "./backup.js";
import type { AppContext } from "./model.js";
import { submitOperation, type OperationStorage } from "./operations.js";
import type { createWorkspaceState } from "./workspace-state.js";

type Options = AppContext &
  Readonly<{
    boardId: string;
    readWithSession: ReturnType<typeof createWorkspaceState>["readWithSession"];
    canRead: () => boolean;
    canPublish: () => boolean;
    capturedBackup: () => CapturedBackup | undefined;
  }>;

type Intent = Readonly<{ id: string; boardId: string; backupId: string; revision: number }>;
type PublicationState = {
  pending?: Intent;
  busy: "submitting" | "inspecting" | null;
  journalError: boolean;
  message: string;
  observers: Set<() => void>;
};

const operationId = /^[\da-f]{8}-[\da-f]{4}-7[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/;
const states = new WeakMap<Document, Map<string, PublicationState>>();
const uncertain =
  "Publication status could not be confirmed. Refresh publication status to inspect it.";

function readIntent(value: unknown, boardId: string): Intent {
  if (typeof value !== "object" || value === null) {
    throw new TypeError("Invalid backup publication record.");
  }
  const { id, boardId: savedBoard, backupId, revision } = value as Record<string, unknown>;
  if (
    typeof id !== "string" ||
    !operationId.test(id) ||
    savedBoard !== boardId ||
    typeof backupId !== "string" ||
    !operationId.test(backupId) ||
    typeof revision !== "number" ||
    !Number.isSafeInteger(revision) ||
    revision < 1
  ) {
    throw new TypeError("Invalid backup publication record.");
  }
  return { id, boardId, backupId, revision };
}

function sameIntent(first: Intent | undefined, second: Intent | undefined): boolean {
  return (
    first?.id === second?.id &&
    first?.boardId === second?.boardId &&
    first?.backupId === second?.backupId &&
    first?.revision === second?.revision
  );
}

function boardState(doc: Document, boardId: string): PublicationState {
  const boards = states.get(doc) ?? new Map<string, PublicationState>();
  states.set(doc, boards);
  const existing = boards.get(boardId);
  if (existing) return existing;
  const state: PublicationState = {
    busy: null,
    journalError: false,
    message: "",
    observers: new Set(),
  };
  boards.set(boardId, state);
  return state;
}

function failureText(code: string): string {
  switch (code) {
    case "backup_integrity":
      return "The captured backup could not be verified.";
    case "remote_unavailable":
    case "publication_destination_unavailable":
      return "The configured Git destination is unavailable.";
    case "publication_target_ambiguous":
      return "Git configuration does not identify a single destination.";
    case "publication_destination_changed":
      return "The configured Git destination changed during publication recovery.";
    case "repository_busy":
      return "The Git repository is busy.";
    case "publication_index_busy":
      return "The Git index is locked.";
    case "publication_index_overlap":
      return "Staged changes overlap the captured backup.";
    case "publication_index_uncertain":
      return "The local Git branch and index require inspection.";
    case "git_push_failed":
      return "Git push failed. The local branch may have advanced.";
    case "git_push_timeout":
      return "Git push timed out. The remote outcome is unknown.";
    default:
      return "The service reported a publication failure.";
  }
}

export function mountBackupPublication(
  host: HTMLElement,
  { api, signal, boardId, readWithSession, canRead, canPublish, capturedBackup }: Options,
) {
  const doc = host.ownerDocument;
  const key = `localswim:pending-backup-publication:${boardId}`;
  const state = boardState(doc, boardId);
  const publish = doc.createElement("button");
  const inspect = doc.createElement("button");
  const scope = doc.createElement("p");
  const status = doc.createElement("p");
  publish.type = inspect.type = "button";
  inspect.textContent = "Refresh publication status";
  status.setAttribute("role", "status");
  host.replaceChildren(publish, inspect, scope, status);

  function storage(): OperationStorage {
    const tab = doc.defaultView?.sessionStorage;
    if (!tab) throw new Error("Tab storage is unavailable.");
    return tab;
  }

  function refreshJournal(): boolean {
    try {
      const raw = storage().getItem(key);
      const pending = raw === null ? undefined : readIntent(JSON.parse(raw) as unknown, boardId);
      if (!sameIntent(pending, state.pending)) {
        state.pending = pending;
        state.message = pending ? uncertain : "";
      }
      state.journalError = false;
      return true;
    } catch {
      state.journalError = true;
      return false;
    }
  }

  function ownsIntent(intent: Intent): boolean {
    return refreshJournal() && sameIntent(intent, state.pending);
  }

  function selectedBackup() {
    const backup = capturedBackup();
    return backup?.board_id === boardId ? backup : undefined;
  }

  function update() {
    if (signal.aborted) return;
    if (!state.busy) refreshJournal();
    const readable = canRead();
    const backup = readable ? selectedBackup() : undefined;
    const pending = state.pending;
    publish.hidden = !backup || !canPublish() || pending !== undefined;
    publish.disabled = !readable || state.busy !== null || state.journalError;
    const label = backup ? `Publish captured revision ${backup.board_revision}` : "";
    if (publish.textContent !== label) publish.textContent = label;
    inspect.hidden = pending === undefined;
    inspect.disabled = !readable || state.busy !== null || state.journalError;
    const description = pending
      ? `Publication for captured revision ${pending.revision}, requested at ${formatOperationTime(pending.id)}.`
      : publish.hidden
        ? ""
        : "Uses the configured Git destination.";
    if (scope.textContent !== description) scope.textContent = description;
    const message = state.journalError
      ? "The tab’s publication record could not be read."
      : state.busy === "submitting"
        ? "Submitting backup publication…"
        : state.busy === "inspecting"
          ? "Checking publication status…"
          : state.message;
    if (status.textContent !== message) status.textContent = message;
    host.hidden = !readable || (publish.hidden && inspect.hidden && !message);
  }

  function notify() {
    for (const observer of state.observers) observer();
  }

  function save(intent: Intent): boolean {
    if (!refreshJournal() || state.pending) return false;
    try {
      storage().setItem(key, JSON.stringify(intent));
      state.pending = intent;
      state.message = uncertain;
      return true;
    } catch {
      state.message = "The tab’s publication record could not be saved.";
      return false;
    }
  }

  function settle(intent: Intent, message: string) {
    if (!ownsIntent(intent)) return;
    try {
      storage().removeItem(key);
      state.pending = undefined;
      state.message = message;
    } catch {
      state.message = `${message} Refresh publication status to clear its tab record.`;
    }
  }

  function accept(intent: Intent, receipt: BackupPublication) {
    switch (receipt.state) {
      case "queued":
      case "running":
        if (ownsIntent(intent)) {
          state.message =
            receipt.state === "queued" ? "Publication queued." : "Publication running.";
        }
        break;
      case "succeeded":
        settle(intent, `Published captured revision ${intent.revision} at ${receipt.finished_at}.`);
        break;
      case "failed":
        settle(
          intent,
          `Publication failed for captured revision ${intent.revision} at ${receipt.finished_at}. ${failureText(receipt.error_code)}`,
        );
        break;
    }
  }

  async function submit() {
    if (signal.aborted || !canRead() || !canPublish() || state.busy) return;
    const backup = selectedBackup();
    if (!backup) return;
    const intent = {
      id: newOperationId(),
      boardId,
      backupId: backup.id,
      revision: backup.board_revision,
    };
    state.busy = "submitting";
    try {
      const outcome = await submitOperation(
        () => {
          const saved = save(intent);
          notify();
          return saved;
        },
        async () => {
          try {
            return (await api.publishBackup(intent.id, intent.boardId, intent.backupId)).data;
          } catch (error) {
            // A duplicate ID may already have an accepted receipt.
            if (error instanceof ApiError && error.status === 409) {
              throw new ApiError("publication_exists", { outcomeUnknown: true });
            }
            throw error;
          }
        },
      );
      switch (outcome.kind) {
        case "confirmed":
          accept(intent, outcome.result);
          break;
        case "stale":
        case "refused":
          settle(
            intent,
            `The service refused publication of captured revision ${intent.revision}.`,
          );
          break;
        case "unknown":
        case "journal-failed":
          break;
      }
    } finally {
      state.busy = null;
      notify();
    }
  }

  async function refresh() {
    if (signal.aborted || !canRead() || state.busy || !refreshJournal()) return;
    const intent = state.pending;
    if (!intent) return;
    state.busy = "inspecting";
    try {
      notify();
      const receipt = await readWithSession(() => {
        if (!canRead() || !ownsIntent(intent)) return Promise.resolve(undefined);
        return api.getPublication(intent.id, intent.boardId, intent.backupId, signal);
      }, signal);
      if (receipt && !signal.aborted && canRead()) accept(intent, receipt.data);
      else if (ownsIntent(intent)) state.message = uncertain;
    } catch {
      // A missing receipt may precede acceptance. Keep the original publication ID.
      if (ownsIntent(intent)) state.message = uncertain;
    } finally {
      state.busy = null;
      notify();
    }
  }

  publish.addEventListener("click", () => void submit(), { signal });
  inspect.addEventListener("click", () => void refresh(), { signal });
  if (!signal.aborted) state.observers.add(update);
  signal.addEventListener("abort", () => state.observers.delete(update), { once: true });
  update();
  return { update };
}
