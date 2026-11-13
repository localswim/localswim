import { ApiError } from "../api.mjs";
import type { AppContext } from "./model.js";
import { parseRoute } from "./model.js";
import { submitOperation } from "./operations.js";

type CreateIntent = { id: string; name: string };
type Options = AppContext &
  Readonly<{
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    canCreate: () => boolean;
    onCreated: (id: string) => void;
  }>;

const journalKey = "localswim:pending-board-create";

function validName(name: unknown): name is string {
  return typeof name === "string" && name.length > 0 && [...name].length <= 256;
}

function parseIntent(value: unknown): CreateIntent {
  if (typeof value === "object" && value !== null && "id" in value && "name" in value) {
    const { id, name } = value;
    if (typeof id === "string" && validName(name)) {
      const route = parseRoute("/boards/" + id);
      if (route.kind === "board" && route.id === id) return { id, name };
    }
  }
  throw new TypeError("Invalid board creation intent.");
}

export function mountWorkspaceCreate(
  host: HTMLElement,
  { api, signal, readWithSession, canCreate, onCreated }: Options,
) {
  const doc = host.ownerDocument;
  const form = doc.createElement("form");
  const label = doc.createElement("label");
  const name = doc.createElement("input");
  const create = doc.createElement("button");
  const inspect = doc.createElement("button");
  const status = doc.createElement("p");
  label.textContent = "Board name";
  name.required = true;
  label.append(name);
  create.type = "submit";
  inspect.type = "button";
  inspect.textContent = "Inspect board creation";
  status.setAttribute("role", "status");
  form.append(label, create, inspect, status);
  host.replaceChildren(form);

  let pending: CreateIntent | undefined;
  let retryable = false;
  let busy = false;
  let pageActive = true;
  let journalProblem = false;
  let message = "";

  function loadJournal() {
    try {
      const stored = globalThis.sessionStorage.getItem(journalKey);
      pending = stored === null ? undefined : parseIntent(JSON.parse(stored) as unknown);
      retryable = false;
      journalProblem = false;
      message = "";
      if (pending) {
        name.value = pending.name;
        message = "A board creation needs inspection.";
      }
    } catch {
      journalProblem = true;
      message = "The board creation record could not be read.";
    }
  }

  function update() {
    const permitted = canCreate();
    host.hidden = !permitted && !pending && !journalProblem;
    name.readOnly = busy || pending !== undefined;
    create.hidden = pending !== undefined && !retryable;
    create.disabled = !permitted || busy || journalProblem;
    create.textContent = pending ? "Retry board creation" : "Create board";
    inspect.hidden = pending === undefined;
    inspect.disabled = busy || journalProblem;
    if (status.textContent !== message) status.textContent = message;
  }

  function save(intent: CreateIntent | undefined): boolean {
    try {
      if (intent) {
        globalThis.sessionStorage.setItem(journalKey, JSON.stringify(intent));
      } else {
        globalThis.sessionStorage.removeItem(journalKey);
      }
      pending = intent;
      return true;
    } catch {
      return false;
    }
  }

  function created(intent: CreateIntent) {
    if (!pageActive || signal.aborted) return;
    if (!save(undefined)) {
      message = "The board exists. Its creation record could not be cleared.";
      return;
    }
    name.value = "";
    onCreated(intent.id);
  }

  async function submit() {
    if (
      signal.aborted ||
      !pageActive ||
      busy ||
      journalProblem ||
      !canCreate() ||
      (pending && !retryable)
    ) {
      return;
    }
    const value = name.value;
    if (!validName(value)) {
      name.setCustomValidity("Enter a board name of 1 to 256 characters.");
      name.reportValidity();
      return;
    }
    const retrying = pending !== undefined;
    const intent = pending ?? { id: crypto.randomUUID(), name: value };
    busy = true;
    retryable = false;
    message = "Creating board…";
    update();
    try {
      const result = await submitOperation(
        () => save(intent),
        async () => {
          try {
            const { data, etag } = await api.createBoard(intent);
            if (
              !("id" in data) ||
              data.id !== intent.id ||
              !("name" in data) ||
              data.name !== intent.name ||
              !("availability" in data) ||
              data.availability !== "open" ||
              !("revision" in data) ||
              data.revision !== 1 ||
              etag !== '"' + intent.id + '-1"'
            ) {
              throw new ApiError("invalid_response", { outcomeUnknown: true });
            }
          } catch (error) {
            // The same UUID may already exist if the original request committed during a retry.
            if (error instanceof ApiError && error.status === 409) {
              throw new ApiError("inspect_board_creation", { outcomeUnknown: true });
            }
            throw error;
          }
        },
      );
      switch (result.kind) {
        case "confirmed":
          created(intent);
          break;
        case "unknown":
          message = "The creation outcome is unknown. Inspect the original board.";
          break;
        case "stale":
        case "refused":
          message =
            !retrying && save(undefined)
              ? "Board creation was refused. You can try again."
              : "Board creation was refused. The saved attempt still needs inspection.";
          break;
        case "journal-failed":
          message = "The board creation record could not be saved.";
          break;
      }
    } finally {
      busy = false;
      if (!signal.aborted) update();
    }
  }

  async function inspectCreation() {
    const intent = pending;
    if (signal.aborted || !pageActive || busy || journalProblem || !intent) return;
    busy = true;
    retryable = false;
    message = "Checking the original board…";
    update();
    try {
      await readWithSession(() => api.getBoardAvailability(intent.id, signal), signal);
      created(intent);
    } catch (error) {
      retryable = error instanceof ApiError && error.status === 404;
      message = retryable
        ? "The board was not found. Retrying uses the original board ID and name."
        : "The original board could not be inspected. Try inspecting it again.";
    } finally {
      busy = false;
      if (!signal.aborted) update();
    }
  }

  form.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      void submit().catch(globalThis.reportError);
    },
    { signal },
  );
  name.addEventListener("input", () => name.setCustomValidity(""), { signal });
  inspect.addEventListener("click", () => void inspectCreation().catch(globalThis.reportError), {
    signal,
  });
  doc.defaultView?.addEventListener("pagehide", () => (pageActive = false), { signal });
  doc.defaultView?.addEventListener(
    "pageshow",
    (event) => {
      pageActive = true;
      if (!event.persisted || busy) return;
      loadJournal();
      update();
    },
    { signal },
  );
  loadJournal();
  update();
  return { update };
}
