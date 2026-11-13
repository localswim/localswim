import { ApiError } from "../api.mjs";
import { mountAttention } from "./attention.js";
import { getBoardView, mountBoardView } from "./board-view.js";
import { parseRoute } from "./model.js";
import { createWorkspaceState } from "./workspace-state.js";

export function mount(root: HTMLElement): () => void {
  const doc = root.ownerDocument;
  const view = doc.defaultView;
  if (!view) throw new Error("The application shell requires a window.");

  function shellElement(selector: string): HTMLElement {
    const element = root.querySelector<HTMLElement>(selector);
    if (!element) throw new Error(`Missing application shell element ${selector}.`);
    return element;
  }

  const status = shellElement("#status");
  const boardName = shellElement("#board-name");
  const boardRegion = shellElement("#board-region");
  const content = shellElement("#route-content");
  const route = parseRoute(view.location.pathname);
  const lifetime = new AbortController();
  const { signal } = lifetime;
  const selectedBoardId = route.kind === "board" ? route.id : undefined;
  let title = "localswim";
  let totalAttention = 0;
  let board: ReturnType<typeof mountBoardView> | undefined;
  const retry = doc.createElement("button");
  retry.type = "button";
  retry.textContent = "Try again";
  root.dataset.route = route.kind;
  boardRegion.hidden = route.kind === "workspace";

  const attention = mountAttention(shellElement("#attention"), {
    selectBoard: (id) => view.location.assign(`/boards/${id}`),
    retry: () => {
      void workspace.refresh();
    },
  });
  const workspace = createWorkspaceState({
    signal,
    selectedBoardId,
    onChange(catalog) {
      attention.update(catalog, selectedBoardId);
      totalAttention = catalog.totalAttention;
      updateTitle();
      if (route.kind === "workspace") {
        setPhase(catalog.status);
      }
    },
  });
  const context = { api: workspace.api, signal };

  function updateTitle() {
    doc.title = totalAttention > 0 ? `(${totalAttention}) ${title}` : title;
  }

  function setPhase(phase: "loading" | "ready" | "missing" | "error", message = "") {
    root.dataset.phase = phase;
    status.dataset.phase = phase;
    status.textContent = message;
  }

  function showMissing() {
    const back = doc.createElement("a");
    back.href = "/";
    back.textContent = "Back to boards";
    content.replaceChildren(back);
    setPhase("missing", "Board not found.");
  }

  async function loadBoard() {
    if (signal.aborted || retry.disabled || route.kind === "workspace") return;
    if (route.kind === "missing") {
      showMissing();
      return;
    }
    retry.disabled = true;
    setPhase("loading", "Loading board…");
    try {
      const reply = await workspace
        .readWithSession(() => context.api.getBoardAvailability(route.id, signal), signal)
        .catch((error: unknown) => {
          if (error instanceof ApiError && error.status === 404) return null;
          throw error;
        });
      if (signal.aborted) return;
      if (reply === null) {
        showMissing();
        return;
      }
      const { availability } = reply.data;
      let name = reply.data.name;
      if (availability === "open") {
        const confirmed = await workspace.readWithSession(
          () => getBoardView(context, route.id),
          signal,
        );
        if (signal.aborted) return;
        name = confirmed.board.name;
        const host = doc.createElement("div");
        content.replaceChildren(host);
        board = mountBoardView(host, confirmed);
      } else {
        content.replaceChildren();
      }
      boardName.textContent = name;
      boardName.hidden = false;
      title = `${name} · localswim`;
      updateTitle();
      const message =
        availability === "closed"
          ? "This board is closed."
          : availability === "unhealthy"
            ? "This board is unavailable."
            : "";
      setPhase("ready", message);
    } catch {
      if (signal.aborted) return;
      setPhase("error", "Could not load this board.");
      content.replaceChildren(retry);
    } finally {
      retry.disabled = false;
    }
  }

  const retryLoad = () => {
    void loadBoard();
  };
  retry.addEventListener("click", retryLoad);
  void loadBoard();

  function dispose() {
    if (signal.aborted) return;
    lifetime.abort();
    board?.dispose();
    attention.dispose();
    workspace.dispose();
    retry.removeEventListener("click", retryLoad);
    view?.removeEventListener("pagehide", disposeOnExit);
  }

  function disposeOnExit(event: PageTransitionEvent) {
    if (!event.persisted) dispose();
  }

  view.addEventListener("pagehide", disposeOnExit);
  return dispose;
}

const root = document.querySelector<HTMLElement>("[data-app]");
if (root) mount(root);
