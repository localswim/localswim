import { ApiError, type Publishing } from "../api.mjs";
import type { AppContext } from "./model.js";
import { submitOperation, type OperationStorage } from "./operations.js";

type Scope = { kind: "workspace" } | { kind: "board"; boardId: string };
type Reply = { data: Publishing; etag: string };
type Draft = { enabled: boolean; inspect: boolean };
type Options = AppContext & {
  scope: Scope;
  readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
  canPublish: () => boolean;
  beginWrite?: () => (() => void) | null;
  onSaved?: () => void;
  storage?: OperationStorage;
};

function statusText(status: Publishing): string {
  const parts = [status.enabled ? "Automatic publishing is on." : "Automatic publishing is off."];
  if (status.pending) parts.push("Publication pending.");
  if (status.error_code !== null) {
    switch (status.error_code) {
      case "git_push_failed":
        parts.push("Git push failed.");
        break;
      case "git_push_timeout":
        parts.push("Git push timed out.");
        break;
      case "repository_busy":
        parts.push("The repository is busy.");
        break;
      default:
        parts.push("Publishing failed.");
    }
  }
  if (status.last_success_at !== null) {
    parts.push(`Last successful publication at ${status.last_success_at}.`);
  }
  return parts.join(" ");
}

export function mountPublishingSettings(
  host: HTMLElement,
  {
    api,
    signal: ownerSignal,
    scope,
    readWithSession,
    canPublish,
    beginWrite,
    onSaved,
    storage,
  }: Options,
) {
  const doc = host.ownerDocument;
  const lifetime = new AbortController();
  let page = new AbortController();
  let signal = AbortSignal.any([ownerSignal, lifetime.signal, page.signal]);
  const key = `localswim:publishing:${scope.kind === "workspace" ? "workspace" : scope.boardId}`;
  const scopeName = scope.kind === "workspace" ? "Workspace" : "Board";
  let saved: Reply | undefined;
  let draft: Draft | undefined;
  let journal: string | null | undefined;
  let journalConflict = false;
  let storageMessage = "";
  let message = "";
  let busy = false;
  let readFailed = false;

  const panel = doc.createElement("section");
  panel.className = "publishing-settings";
  const heading = doc.createElement("h3");
  heading.textContent = `${scopeName} publishing`;
  const status = doc.createElement("p");
  status.setAttribute("role", "status");
  const feedback = doc.createElement("p");
  feedback.setAttribute("role", "status");
  const form = doc.createElement("form");
  const label = doc.createElement("label");
  label.className = "check-option";
  const checkbox = doc.createElement("input");
  checkbox.type = "checkbox";
  label.append(checkbox, ` Enable ${scope.kind} publishing`);

  function button(text: string) {
    const node = doc.createElement("button");
    node.type = "button";
    node.textContent = text;
    return node;
  }

  const save = button(`Save ${scope.kind} publishing`);
  save.type = "submit";
  const discard = button("Discard change");
  const inspect = button("Inspect saved settings");
  const refreshButton = button(`Refresh ${scope.kind} publishing status`);
  form.append(label, save, discard);
  panel.append(heading, status, form, inspect, refreshButton, feedback);
  host.append(panel);

  function tabStorage(): OperationStorage {
    return storage ?? globalThis.sessionStorage;
  }

  function restoreJournal() {
    try {
      journal = tabStorage().getItem(key);
      journalConflict = false;
      storageMessage = "";
      switch (journal) {
        case null:
          draft = undefined;
          break;
        case "draft:on":
        case "draft:off":
        case "inspect:on":
        case "inspect:off":
          draft = { enabled: journal.endsWith(":on"), inspect: journal.startsWith("inspect:") };
          break;
        default:
          journal = undefined;
          storageMessage = "The pending publishing settings in tab storage are unreadable.";
      }
    } catch {
      journal = undefined;
      storageMessage = "Tab storage is unavailable. Keep this page open to retain your change.";
    }
  }

  function remember(next: Draft | undefined): boolean {
    try {
      const tab = tabStorage();
      const existing = tab.getItem(key);
      if (existing !== journal && (existing !== null || journal !== undefined)) {
        journalConflict = true;
        storageMessage =
          "The tab’s pending publishing settings changed. Refresh status to reload them.";
        return false;
      }
      const value = next
        ? `${next.inspect ? "inspect" : "draft"}:${next.enabled ? "on" : "off"}`
        : null;
      if (value === null) tab.removeItem(key);
      else tab.setItem(key, value);
      journal = value;
      journalConflict = false;
      storageMessage = "";
      return true;
    } catch {
      storageMessage = "Tab storage is unavailable. Keep this page open to retain your change.";
      return false;
    }
  }

  function updatePermission() {
    if (signal.aborted) return;
    const allowed = canPublish();
    const needsInspection = draft?.inspect === true;
    form.hidden = !allowed;
    checkbox.checked = draft?.enabled ?? saved?.data.enabled ?? false;
    checkbox.disabled = !allowed || !saved || busy || needsInspection;
    save.disabled = checkbox.disabled || !draft || readFailed;
    discard.hidden = !draft || needsInspection;
    discard.disabled = busy;
    inspect.hidden = !needsInspection;
    inspect.disabled = busy;
    refreshButton.disabled = busy;
    status.textContent = saved
      ? statusText(saved.data)
      : busy
        ? "Checking publishing status…"
        : "Publishing status is unavailable.";
    feedback.textContent =
      storageMessage ||
      (readFailed ? "Publishing status could not be read. Try refreshing again." : "") ||
      message ||
      (needsInspection ? "Inspect the saved settings before retrying the change." : "");
  }

  async function read(operationSignal: AbortSignal): Promise<Reply> {
    if (scope.kind === "board") {
      const { boardId } = scope;
      return readWithSession(() => api.getBoardPublishing(boardId), operationSignal);
    }
    const { data, etag } = await readWithSession(api.getWorkspace, operationSignal);
    return { data: data.publishing, etag };
  }

  async function write(enabled: boolean, etag: string): Promise<Reply> {
    const reply =
      scope.kind === "board"
        ? await api.configureBoardPublishing(scope.boardId, enabled, etag)
        : await api.configureWorkspacePublishing(enabled, etag);
    const data = "publishing" in reply.data ? reply.data.publishing : reply.data;
    if (data.enabled !== enabled) throw new ApiError("invalid_response", { outcomeUnknown: true });
    return { data, etag: reply.etag };
  }

  function finish(next: Draft | undefined, text: string) {
    if (remember(next)) draft = next;
    message = text;
  }

  async function run(action: (operationSignal: AbortSignal) => Promise<boolean>) {
    if (busy || signal.aborted) return;
    const operationSignal = signal;
    busy = true;
    updatePermission();
    try {
      const confirmed = await action(operationSignal);
      if (confirmed && !operationSignal.aborted) onSaved?.();
    } catch (error) {
      if (!operationSignal.aborted) globalThis.reportError(error);
    } finally {
      if (!operationSignal.aborted) {
        busy = false;
        updatePermission();
      }
    }
  }

  function refresh(inspecting = false): Promise<void> {
    return run(async (operationSignal) => {
      if (journalConflict) {
        restoreJournal();
        message = "";
      }
      try {
        const reply = await read(operationSignal);
        if (operationSignal.aborted) return false;
        saved = reply;
        readFailed = false;
        if (inspecting && draft?.inspect) {
          if (draft.enabled === reply.data.enabled) {
            finish(undefined, "The saved settings confirm the publishing change.");
            return true;
          }
          finish(
            { enabled: draft.enabled, inspect: false },
            "Review the current setting before saving the pending change.",
          );
        }
      } catch {
        if (!operationSignal.aborted) readFailed = true;
      }
      return false;
    });
  }

  async function submit(operationSignal: AbortSignal): Promise<boolean> {
    if (!saved || !draft || draft.inspect || readFailed || !canPublish()) return false;
    const release = beginWrite?.();
    if (release === null) {
      message = "Another settings change is in progress. Try saving again when it finishes.";
      return false;
    }
    const { enabled } = draft;
    const expected = saved.etag;
    try {
      const pending: Draft = { enabled, inspect: true };
      const outcome = await submitOperation(
        () => {
          if (!remember(pending)) return false;
          draft = pending;
          message = "Saving publishing settings…";
          updatePermission();
          return true;
        },
        () => write(enabled, expected),
      );
      if (operationSignal.aborted) return false;
      switch (outcome.kind) {
        case "confirmed":
          saved = outcome.result;
          finish(undefined, "Publishing settings saved.");
          return true;
        case "stale":
          message = "The saved settings changed. Inspect the current setting before retrying.";
          break;
        case "refused":
          finish(
            { enabled, inspect: false },
            "The service refused the save. Your change is pending.",
          );
          break;
        case "unknown":
          message = "The save outcome is unknown. Inspect the saved settings before retrying.";
          break;
        case "journal-failed":
          break;
      }
      return false;
    } finally {
      release?.();
    }
  }

  checkbox.addEventListener(
    "change",
    () => {
      if (checkbox.disabled || !canPublish()) return;
      draft =
        checkbox.checked === saved?.data.enabled
          ? undefined
          : { enabled: checkbox.checked, inspect: false };
      remember(draft);
      message = draft ? "Unsaved publishing change." : "";
      updatePermission();
    },
    { signal: lifetime.signal },
  );
  form.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      void run(submit);
    },
    { signal: lifetime.signal },
  );
  discard.addEventListener(
    "click",
    () => {
      if (busy || draft?.inspect || !canPublish()) return;
      finish(undefined, "");
      updatePermission();
    },
    { signal: lifetime.signal },
  );
  inspect.addEventListener("click", () => void refresh(true), { signal: lifetime.signal });
  refreshButton.addEventListener("click", () => void refresh(), { signal: lifetime.signal });

  restoreJournal();
  updatePermission();

  return {
    refresh: () => refresh(),
    updatePermission,
    suspend: () => page.abort(),
    resume() {
      if (!page.signal.aborted || ownerSignal.aborted || lifetime.signal.aborted)
        return Promise.resolve();
      page = new AbortController();
      signal = AbortSignal.any([ownerSignal, lifetime.signal, page.signal]);
      busy = false;
      message = "";
      restoreJournal();
      return refresh();
    },
    dispose() {
      lifetime.abort();
      panel.remove();
    },
  };
}
