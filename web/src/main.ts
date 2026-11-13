import { ApiError, createBrowserApi, openBrowserSession } from "../api.mjs";
import { parseBoardPage, parseRoute } from "./model.js";
import type { AppContext, BoardLink } from "./model.js";

async function readBoards({ api, signal }: AppContext): Promise<BoardLink[]> {
  const boards: BoardLink[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | null = null;
  do {
    const { data } = await api.listBoards(cursor ?? undefined, signal);
    const page = parseBoardPage(data);
    for (const board of page.boards) boards.push(board);
    cursor = page.nextCursor;
    if (cursor !== null) {
      if (seenCursors.has(cursor)) throw new TypeError("Repeated board catalog cursor.");
      seenCursors.add(cursor);
    }
  } while (cursor !== null);
  return boards;
}

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
  const content = shellElement("#route-content");
  const route = parseRoute(view.location.pathname);
  const lifetime = new AbortController();
  let capability: string | undefined;
  const api = createBrowserApi({ getCapability: () => capability });
  const context: AppContext = { api, signal: lifetime.signal };
  const retry = doc.createElement("button");
  retry.type = "button";
  retry.textContent = "Try again";

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

  function showWorkspace(boards: readonly BoardLink[]) {
    const heading = doc.createElement("h1");
    heading.textContent = "Boards";
    if (boards.length === 0) {
      const empty = doc.createElement("p");
      empty.textContent = "No boards are available.";
      content.replaceChildren(heading, empty);
      return;
    }
    const nav = doc.createElement("nav");
    nav.setAttribute("aria-label", "Boards");
    const list = doc.createElement("ul");
    for (const board of boards) {
      const item = doc.createElement("li");
      const link = doc.createElement("a");
      link.href = `/boards/${board.id}`;
      link.textContent = board.name ?? `Unavailable board (${board.id})`;
      item.append(link);
      list.append(item);
    }
    nav.append(list);
    content.replaceChildren(heading, nav);
  }

  async function load() {
    if (context.signal.aborted) return;
    if (route.kind === "missing") {
      showMissing();
      return;
    }
    retry.disabled = true;
    setPhase("loading", route.kind === "workspace" ? "Loading boards…" : "Loading board…");
    try {
      if (!capability) {
        const token = await openBrowserSession();
        if (context.signal.aborted) return;
        capability = token;
      }
      if (route.kind === "workspace") {
        const boards = await readBoards(context);
        if (context.signal.aborted) return;
        showWorkspace(boards);
        setPhase("ready");
        return;
      }

      const reply = await context.api.getBoardAvailability(route.id, context.signal).catch((error: unknown) => {
        if (error instanceof ApiError && error.status === 404) return null;
        throw error;
      });
      if (context.signal.aborted) return;
      if (reply === null) {
        showMissing();
        return;
      }
      const { name, availability } = reply.data;
      boardName.textContent = name;
      boardName.hidden = false;
      doc.title = `${name} · localswim`;
      content.replaceChildren();
      const message = availability === "closed" ? "This board is closed."
        : availability === "unhealthy" ? "This board is unavailable." : "Board loaded.";
      setPhase("ready", message);
    } catch (error) {
      if (context.signal.aborted) return;
      if (error instanceof ApiError && error.status === 401) capability = undefined;
      setPhase("error", route.kind === "workspace" ? "Could not load boards." : "Could not load this board.");
      content.replaceChildren(retry);
    } finally {
      retry.disabled = false;
    }
  }

  const retryLoad = () => { void load(); };
  retry.addEventListener("click", retryLoad);
  void load();

  return () => {
    lifetime.abort();
    capability = undefined;
    retry.removeEventListener("click", retryLoad);
  };
}

const root = document.querySelector<HTMLElement>("[data-app]");
if (root) mount(root);
