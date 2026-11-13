import {
  readComment,
  renderComment,
  uuid,
  type AuthorKind,
  type AuthorName,
  type TicketLink,
} from "./history.js";
import type { AppContext } from "./model.js";

type ReadableBoard = Readonly<{
  revision: number;
  authorName: AuthorName;
  authorKind: AuthorKind;
}>;
type NewestCommentsOptions = Readonly<{
  api: Pick<AppContext["api"], "listComments">;
  boardId: string;
  signal: AbortSignal;
  readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
  context: () => ReadableBoard | null;
  ticketLink: TicketLink;
  onOpenCard: (cardId: string, opener: HTMLAnchorElement) => void;
}>;

const previewSize = 2;

function readBoardComments(value: unknown, boardId: string) {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new TypeError("Invalid board comment page.");
  }
  const { comments, next_cursor: next } = value as Record<string, unknown>;
  if (
    !Array.isArray(comments) ||
    comments.length > previewSize ||
    (next !== null && (typeof next !== "string" || !next || next.length > 2048))
  ) {
    throw new TypeError("Invalid board comment page.");
  }
  const seen = new Set<string>();
  return (comments as unknown[]).map((item) => {
    if (
      typeof item !== "object" ||
      item === null ||
      !("card_id" in item) ||
      typeof item.card_id !== "string" ||
      item.card_id.length !== 36 ||
      !uuid.test(item.card_id) ||
      !("ticket" in item) ||
      typeof item.ticket !== "number" ||
      !Number.isSafeInteger(item.ticket) ||
      item.ticket < 1
    ) {
      throw new TypeError("Invalid board comment source.");
    }
    const comment = readComment(item, { boardId, cardId: item.card_id });
    if (seen.has(comment.id)) throw new TypeError("Duplicate board comment.");
    seen.add(comment.id);
    return { comment, cardId: item.card_id, ticket: item.ticket };
  });
}

export function mountNewestComments(
  host: HTMLElement,
  { api, boardId, signal, readWithSession, context, ticketLink, onOpenCard }: NewestCommentsOptions,
) {
  const doc = host.ownerDocument;
  const panel = doc.createElement("details");
  panel.className = "newest-comments";
  const heading = doc.createElement("summary");
  heading.textContent = "Newest comments";
  const status = doc.createElement("p");
  status.className = "card-comment-status";
  status.setAttribute("role", "status");
  const results = doc.createElement("ol");
  results.className = "card-comment-list";
  results.hidden = true;
  const retry = doc.createElement("button");
  retry.type = "button";
  retry.textContent = "Reload newest comments";
  retry.hidden = true;
  panel.append(heading, status, results, retry);
  host.append(panel);

  let requestedRevision: number | undefined;
  let request: AbortController | undefined;

  function setStatus(message: string, canRetry = false) {
    if (status.textContent !== message) status.textContent = message;
    status.hidden = !message;
    if (!canRetry && doc.activeElement === retry) heading.focus({ preventScroll: true });
    retry.hidden = !canRetry;
  }

  function clear() {
    request?.abort();
    request = undefined;
    requestedRevision = undefined;
    if (results.contains(doc.activeElement)) heading.focus({ preventScroll: true });
    results.replaceChildren();
    results.hidden = true;
  }

  async function load(board: ReadableBoard) {
    clear();
    const attempt = new AbortController();
    request = attempt;
    requestedRevision = board.revision;
    const readSignal = AbortSignal.any([signal, attempt.signal]);
    const isCurrent = () =>
      !readSignal.aborted && panel.open && context()?.revision === board.revision;
    setStatus("Loading newest comments…");
    try {
      const reply = await readWithSession(
        () => api.listComments({ board_id: boardId }, previewSize, undefined, readSignal),
        readSignal,
      );
      if (!isCurrent()) return;
      const rows = readBoardComments(reply.data, boardId).map(({ comment, cardId, ticket }) => {
        const row = renderComment(
          doc,
          comment,
          board.authorName,
          board.authorKind(comment.actor) === "human" ? ticketLink : undefined,
        );
        const link = doc.createElement("a");
        link.className = "newest-comment-source";
        link.href = `/boards/${boardId}?card=${cardId}`;
        link.textContent = `#${ticket}`;
        link.addEventListener(
          "click",
          (event) => {
            if (!isCurrent()) {
              event.preventDefault();
              return;
            }
            if (
              event.defaultPrevented ||
              event.button !== 0 ||
              event.altKey ||
              event.ctrlKey ||
              event.metaKey ||
              event.shiftKey
            ) {
              return;
            }
            event.preventDefault();
            onOpenCard(cardId, link);
          },
          { signal: readSignal },
        );
        row.prepend(link);
        return row;
      });
      results.replaceChildren(...rows);
      results.hidden = rows.length === 0;
      setStatus(rows.length ? "" : "No comments.");
    } catch {
      if (isCurrent()) setStatus("Newest comments could not be loaded.", true);
    }
  }

  function suspend() {
    clear();
    setStatus(panel.open ? "Newest comments are unavailable." : "");
  }

  function update() {
    if (signal.aborted || !panel.open) {
      suspend();
      return;
    }
    const board = context();
    if (board === null) {
      suspend();
    } else if (board.revision !== requestedRevision) {
      void load(board);
    }
  }

  function refresh() {
    requestedRevision = undefined;
    update();
  }

  panel.addEventListener("toggle", update, { signal });
  retry.addEventListener("click", refresh, { signal });
  signal.addEventListener("abort", suspend, { once: true });
  return { update, suspend, refresh };
}
