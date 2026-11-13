import { ApiError, type Publishing } from "../api.mjs";
import type { AppContext } from "./model.js";
import { submitOperation, type OperationStorage } from "./operations.js";

type Scope = { kind: "workspace" } | { kind: "board"; boardId: string };
type Reply = { data: Publishing; etag: string };
type Draft = { enabled: boolean; inspect: boolean; etag?: string };
const INSPECTION_JOURNAL =
  /^inspect:(on|off):("[A-Za-z0-9_-]{16,128}")(?:\/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})?$/u;

type Options = AppContext & {
  scope: Scope;
  readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
  canPublish: () => boolean;
  writeUnavailable?: () => string | undefined;
  beginWrite?: () => (() => void) | null;
  beginInspection?: () => (() => void) | null;
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
    writeUnavailable,
    beginWrite,
    beginInspection,
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
  let retryEtag: string | undefined;
  let journal: string | null | undefined;
  let journalConflict = false;
  let storageMessage = "";
  let message = "";
  let busy = false;
  let refreshQueued = false;
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
    retryEtag = undefined;
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
        default: {
          const inspection = INSPECTION_JOURNAL.exec(journal);
          if (inspection) {
            draft = { enabled: inspection[1] === "on", inspect: true, etag: inspection[2] };
          } else {
            journal = undefined;
            storageMessage = "The pending publishing settings in tab storage are unreadable.";
          }
        }
      }
    } catch {
      journal = undefined;
      storageMessage = "Tab storage is unavailable. Keep this page open to retain your change.";
    }
  }

  function journalMatches(): boolean {
    try {
      const existing = tabStorage().getItem(key);
      if (existing === journal || (existing === null && journal === undefined)) return true;
      journalConflict = true;
      retryEtag = undefined;
      storageMessage =
        "The tab’s pending publishing settings changed. Refresh status to reload them.";
    } catch {
      storageMessage = "Tab storage is unavailable. Keep this page open to retain your change.";
    }
    return false;
  }

  function remember(next: Draft | undefined, attempt?: string): boolean {
    if (!journalMatches()) return false;
    try {
      const tab = tabStorage();
      const value = next
        ? `${next.inspect ? "inspect" : "draft"}:${next.enabled ? "on" : "off"}${next.etag ? `:${next.etag}` : ""}${attempt ? `/${attempt}` : ""}`
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
    const unavailable = allowed ? writeUnavailable?.() : undefined;
    form.hidden = !allowed;
    checkbox.checked = draft?.enabled ?? saved?.data.enabled ?? false;
    checkbox.disabled = !allowed || !saved || busy || journalConflict || needsInspection;
    save.textContent = retryEtag
      ? `Retry ${scope.kind} publishing save`
      : `Save ${scope.kind} publishing`;
    save.disabled =
      !allowed ||
      !saved ||
      busy ||
      journalConflict ||
      !draft ||
      readFailed ||
      !!unavailable ||
      (needsInspection && retryEtag === undefined);
    discard.hidden = !draft || needsInspection;
    discard.disabled = busy || journalConflict;
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
      (draft && (!needsInspection || retryEtag) ? unavailable : undefined) ||
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
        if (refreshQueued) {
          refreshQueued = false;
          void refresh();
        }
      }
    }
  }

  function refresh(inspecting = false): Promise<void> {
    if (busy && !inspecting) {
      refreshQueued = true;
      return Promise.resolve();
    }
    return run(async (operationSignal) => {
      const release = inspecting ? beginInspection?.() : undefined;
      if (release === null) {
        message = "A settings save is still in progress. Inspect again after it settles.";
        return false;
      }
      if (journalConflict) {
        restoreJournal();
        message = "";
      }
      try {
        const reply = await read(operationSignal);
        if (operationSignal.aborted) return false;
        saved = reply;
        readFailed = false;
        if (retryEtag !== undefined && retryEtag !== reply.etag) {
          retryEtag = undefined;
          message = "The saved settings changed. Inspect the current setting before retrying.";
        }
        if (inspecting && draft?.inspect) {
          if (!journalMatches()) return false;
          if (draft.enabled === reply.data.enabled) {
            finish(undefined, "The saved settings confirm the publishing change.");
            return true;
          }
          if (draft.etag === reply.etag) {
            // A late original write and an explicit retry must compete for the same validator.
            retryEtag = reply.etag;
            message = "The saved setting is unchanged. You can retry the pending change.";
          } else {
            finish(
              { enabled: draft.enabled, inspect: false },
              "Review the current setting before saving the pending change.",
            );
          }
        }
      } catch {
        if (!operationSignal.aborted) {
          retryEtag = undefined;
          readFailed = true;
        }
      } finally {
        release?.();
      }
      return false;
    });
  }

  async function submit(operationSignal: AbortSignal): Promise<boolean> {
    if (!saved || !draft || readFailed || journalConflict || !canPublish() || writeUnavailable?.())
      return false;
    const retrying = draft.inspect;
    const expected = retrying ? retryEtag : saved.etag;
    if (expected === undefined || expected !== saved.etag) return false;
    const release = beginWrite?.();
    if (release === null) {
      message = "Another settings change is in progress. Try saving again when it finishes.";
      return false;
    }
    const { enabled } = draft;
    try {
      const pending: Draft = { enabled, inspect: true, etag: expected };
      retryEtag = undefined;
      const outcome = await submitOperation(
        () => {
          // The token owns the tab record. Retries keep the original wire ETag.
          if (!remember(pending, crypto.randomUUID())) return false;
          draft = pending;
          message = "Saving publishing settings…";
          updatePermission();
          return true;
        },
        () => write(enabled, expected),
      );
      if (operationSignal.aborted || outcome.kind === "journal-failed" || !journalMatches())
        return false;
      switch (outcome.kind) {
        case "confirmed":
          saved = outcome.result;
          finish(undefined, "Publishing settings saved.");
          return true;
        case "stale":
          message = "The saved settings changed. Inspect the current setting before retrying.";
          break;
        case "refused":
          if (retrying) {
            message = "The service refused the retry. Inspect the saved settings again.";
          } else {
            finish(
              { enabled, inspect: false },
              "The service refused the save. Your change is pending.",
            );
          }
          break;
        case "unknown":
          message = "The save outcome is unknown. Inspect the saved settings before retrying.";
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
