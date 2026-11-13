import { ApiError } from "../api.mjs";
import { appendCardProse } from "../card-prose.mjs";
import type { BoardViewData } from "./board-view.js";
import type { AppContext } from "./model.js";

export type CardReference = Readonly<{ id: string } | { ticket: number }>;

type CardViewOptions = AppContext &
  Readonly<{
    boardId: string;
    readWithSession: <T>(read: () => Promise<T>, signal?: AbortSignal) => Promise<T>;
    getBoardData: () => BoardViewData | undefined;
    focusFallback: (reference: CardReference, opener?: HTMLElement) => void;
    onSelectionChange?: (reference: CardReference | null, explicit: boolean) => void;
    onTextContext?: (context: DrawerTextContext | null) => void;
    onTextReadiness?: (confirmed: boolean) => void;
    acceptTextVersion?: (cardId: string, revision: number, etag: string) => boolean;
  }>;

type CardSummary = Readonly<{ id: string; ticket: number; title: string; archived: boolean }>;
type LaneDetails = Readonly<{ id: string; label: string; retired: boolean }>;

type SavedCard = CardSummary &
  Readonly<{
    board_id: string;
    description: string;
    lane_id: string;
    owner_id: string;
    priority_id: string;
    parent_id: string | null;
    child_ids: readonly string[];
    created_at: string;
    updated_at: string;
    completed_at: string | null;
    comment_count: number;
    attachment_count: number;
    revision: number;
    etag: string;
    cover_attachment_id: string | null;
  }>;

export type DrawerTextContext = Readonly<{
  cardId: string;
  title: string;
  description: string;
  revision: number;
  etag: string;
  editable: boolean;
  archived: boolean;
  laneId: string;
  ownerId: string;
  priorityId: string;
  commentCount: number;
  attachmentCount: number;
  coverAttachmentId: string | null;
}>;

type HierarchyEntry = Readonly<{ kind: "Parent" | "Child"; target: CardSummary | null }>;
type Selection = {
  reference: CardReference;
  origin: CardReference;
  opener: HTMLElement | undefined;
  lifetime: AbortController;
  loading: boolean;
  refreshQueued: boolean;
  restored: boolean;
  staleReadRetried: boolean;
  read: AbortController | undefined;
};

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const hierarchyBatchSize = 8;

export function mountCardView(
  root: HTMLElement,
  {
    api,
    signal: ownerSignal,
    boardId,
    readWithSession,
    getBoardData,
    focusFallback,
    onSelectionChange,
    onTextContext,
    onTextReadiness,
    acceptTextVersion,
  }: CardViewOptions,
) {
  const doc = root.ownerDocument;
  const lifetime = new AbortController();
  const signal = AbortSignal.any([ownerSignal, lifetime.signal]);

  function element<K extends keyof HTMLElementTagNameMap>(
    tag: K,
    className: string,
    text?: string,
  ): HTMLElementTagNameMap[K] {
    const node = doc.createElement(tag);
    node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  const panel = element("aside", "card-drawer");
  panel.id = "card-drawer";
  panel.setAttribute("aria-labelledby", "card-drawer-ticket card-drawer-title");
  panel.hidden = true;

  const bar = element("div", "card-drawer-bar");
  const ticketLabel = element("span", "card-drawer-ticket");
  ticketLabel.id = "card-drawer-ticket";
  const retryButton = element("button", "", "Retry card read");
  retryButton.type = "button";
  retryButton.hidden = true;
  const closeButton = element("button", "card-drawer-close", "×");
  closeButton.type = "button";
  closeButton.setAttribute("aria-label", "Close card");
  bar.append(ticketLabel, retryButton, closeButton);

  const body = element("div", "card-drawer-body");
  body.tabIndex = 0;
  body.setAttribute("role", "group");
  body.setAttribute("aria-label", "Card details");
  const title = element("h2", "card-drawer-title");
  title.id = "card-drawer-title";
  title.tabIndex = -1;
  title.setAttribute("aria-live", "polite");
  const status = element("p", "card-drawer-status");
  status.setAttribute("role", "status");
  const details = element("div", "card-drawer-details");
  const metadata = element("dl", "card-drawer-metadata");
  const description = element("p", "card-drawer-prose");
  const hierarchy = element("ul", "card-drawer-links");
  const editor = element("div", "card-drawer-editor");
  const tools = element("div", "card-drawer-tools");
  details.append(
    metadata,
    element("h3", "card-drawer-heading", "Description"),
    description,
    editor,
    tools,
    element("h3", "card-drawer-heading", "Parent and children"),
    hierarchy,
  );
  body.append(title, status, details);
  panel.append(bar, body);
  root.append(panel);

  let selection: Selection | undefined;
  let displayedProse: string | undefined;
  let displayedHierarchy: string | undefined;

  function setStatus(message: string, retry = false) {
    if (status.textContent !== message) status.textContent = message;
    if (!retry && doc.activeElement === retryButton) closeButton.focus({ preventScroll: true });
    retryButton.hidden = !retry;
  }

  async function getCard(id: string, requestSignal: AbortSignal): Promise<SavedCard> {
    const reply = await readWithSession(() => api.getCard(id, requestSignal), requestSignal);
    requestSignal.throwIfAborted();
    // The API adapter validates the card shape and requested UUID. Confirm board ownership here.
    const card = reply.data as Omit<SavedCard, "etag">;
    if (card.board_id !== boardId) throw new ApiError("http_error", { status: 404 });
    if (!reply.etag) throw new ApiError("invalid_response");
    return { ...card, etag: reply.etag };
  }

  async function readCard(
    reference: CardReference,
    requestSignal: AbortSignal,
  ): Promise<SavedCard> {
    if ("id" in reference) return getCard(reference.id, requestSignal);

    const { ticket } = reference;
    const { data } = await readWithSession(
      () => api.listCards(boardId, { ticket, archived: "include", limit: 2 }, requestSignal),
      requestSignal,
    );
    requestSignal.throwIfAborted();
    const { cards, next_cursor: nextCursor } = data as Record<string, unknown>;
    if (!Array.isArray(cards) || nextCursor !== null) throw new ApiError("invalid_response");
    if (cards.length === 0) throw new ApiError("http_error", { status: 404 });
    const [match] = cards as unknown[];
    if (
      cards.length !== 1 ||
      typeof match !== "object" ||
      match === null ||
      !("id" in match) ||
      typeof match.id !== "string" ||
      !uuid.test(match.id) ||
      !("board_id" in match) ||
      match.board_id !== boardId ||
      !("ticket" in match) ||
      match.ticket !== ticket
    ) {
      throw new ApiError("invalid_response");
    }
    const card = await getCard(match.id, requestSignal);
    if (card.ticket !== ticket) throw new ApiError("http_error", { status: 404 });
    return card;
  }

  async function readHierarchy(
    card: SavedCard,
    data: BoardViewData | undefined,
    requestSignal: AbortSignal,
  ): Promise<HierarchyEntry[]> {
    const links = card.child_ids.map((id) => ({ kind: "Child" as const, id }));
    const references = card.parent_id
      ? [{ kind: "Parent" as const, id: card.parent_id }, ...links]
      : links;
    if (references.length === 0) return [];
    const active = new Map(data?.cards.map((entry) => [entry.id, entry]));
    const result: HierarchyEntry[] = [];
    for (let offset = 0; offset < references.length; offset += hierarchyBatchSize) {
      requestSignal.throwIfAborted();
      const batch = references.slice(offset, offset + hierarchyBatchSize);
      const loaded = await Promise.all(
        batch.map(async ({ kind, id }) => {
          const visible = active.get(id);
          if (visible) {
            return {
              kind,
              target: { id, ticket: visible.ticket, title: visible.title, archived: false },
            };
          }
          const saved = await getCard(id, requestSignal).catch((error: unknown) => {
            if (cardUnavailable(error)) return null;
            throw error;
          });
          const target = saved && {
            id,
            ticket: saved.ticket,
            title: saved.title,
            archived: saved.archived,
          };
          return { kind, target };
        }),
      );
      result.push(...loaded);
    }
    requestSignal.throwIfAborted();
    return result;
  }

  function cardUnavailable(error: unknown): boolean {
    return error instanceof ApiError && (error.status === 403 || error.status === 404);
  }

  async function readLaneLabel(
    laneId: string,
    data: BoardViewData | undefined,
    requestSignal: AbortSignal,
  ): Promise<string> {
    const active = data?.lanes.find(({ id }) => id === laneId);
    if (active) return active.label;

    const seen = new Set<string>();
    let cursor: string | undefined;
    do {
      const reply = await readWithSession(
        () => api.listLanes(boardId, cursor, requestSignal),
        requestSignal,
      );
      requestSignal.throwIfAborted();
      // The adapter validates each lane, including its board identity and retired flag.
      const { lanes, next_cursor: next } = reply.data as {
        lanes: readonly LaneDetails[];
        next_cursor: unknown;
      };
      const match = lanes.find(({ id }) => id === laneId);
      if (match) return match.retired ? `${match.label} (Retired)` : match.label;
      if (next === null) break;
      if (typeof next !== "string" || next.length === 0 || next.length > 2048 || seen.has(next)) {
        throw new ApiError("invalid_response");
      }
      seen.add(next);
      cursor = next;
    } while (cursor !== undefined);
    throw new ApiError("invalid_response");
  }

  function ticketLink(ticket: number, label: string, id?: string): HTMLAnchorElement {
    const link = element("a", "card-drawer-link", label);
    link.href = `/boards/${boardId}?ticket=${ticket}`;
    link.dataset.target = id ?? String(ticket);
    link.addEventListener("click", (event) => {
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
      open(id === undefined ? { ticket } : { id });
    });
    return link;
  }

  function updateLinkedContent(container: HTMLElement, nodes: readonly Node[]) {
    const focused = doc.activeElement;
    const key = focused?.getAttribute("data-target");
    const matchingLinks = () =>
      [...container.querySelectorAll("a")].filter((link) => link.dataset.target === key);
    const occurrence =
      key && container.contains(focused)
        ? matchingLinks().findIndex((link) => link === focused)
        : -1;
    container.replaceChildren(...nodes);
    if (occurrence >= 0) {
      const replacement = matchingLinks()[occurrence];
      (replacement ?? body).focus({ preventScroll: true });
    }
  }

  function showConfirmedText(text: { title: string; description: string }) {
    if (title.textContent !== text.title) title.textContent = text.title;
    if (displayedProse === text.description) return;
    displayedProse = text.description;
    const prose = doc.createElement("p");
    if (text.description === "") prose.textContent = "No description.";
    else appendCardProse(prose, text.description, ticketLink);
    description.classList.toggle("card-drawer-empty", text.description === "");
    updateLinkedContent(description, [...prose.childNodes]);
  }

  function renderCard(card: SavedCard, laneLabel: string, data: BoardViewData | undefined) {
    const owner = data?.users.find(({ id }) => id === card.owner_id);
    const presentation = owner ? data?.board.actor_presentation?.[owner.kind] : undefined;
    const fields = new Map<string, string>([
      ["Lane", laneLabel],
      [
        "Priority",
        data?.priorities.find(({ id }) => id === card.priority_id)?.label ?? "Unavailable",
      ],
      ["Owner", presentation?.label ?? owner?.label ?? "Unavailable"],
      ["Created", card.created_at],
      ["Updated", card.updated_at],
      ["Comments", String(card.comment_count)],
      ["Attachments", String(card.attachment_count)],
    ]);
    if (card.completed_at) fields.set("Completed", card.completed_at);
    if (card.archived) fields.set("State", "Archived");

    ticketLabel.textContent = `#${card.ticket}`;
    const rows = [...fields].map(([label, value]) => {
      const row = element("div", "card-drawer-field");
      row.append(element("dt", "", label), element("dd", "", value));
      return row;
    });
    metadata.replaceChildren(...rows);

    showConfirmedText(card);
    details.hidden = false;
  }

  function renderHierarchy(entries: readonly HierarchyEntry[]) {
    const signature = JSON.stringify(entries);
    if (signature === displayedHierarchy) return;
    displayedHierarchy = signature;
    const rows = entries.map(({ kind, target }) => {
      const row = element("li", "card-drawer-relation");
      row.append(element("span", "card-drawer-relation-kind", kind));
      if (target) {
        const link = ticketLink(target.ticket, `#${target.ticket} ${target.title}`, target.id);
        if (target.archived) link.append(element("span", "card-drawer-archive", " (Archived)"));
        row.append(link);
      } else {
        row.append(element("span", "card-drawer-empty", "Card unavailable"));
      }
      return row;
    });
    if (rows.length === 0) rows.push(element("li", "card-drawer-empty", "No parent or children."));
    updateLinkedContent(hierarchy, rows);
  }

  async function load(current: Selection): Promise<void> {
    if (current.loading) {
      current.refreshQueued = true;
      return;
    }
    current.loading = true;
    onTextReadiness?.(false);
    retryButton.setAttribute("aria-disabled", "true");
    const attempt = new AbortController();
    current.read = attempt;
    const selectionSignal = AbortSignal.any([signal, current.lifetime.signal]);
    const requestSignal = AbortSignal.any([selectionSignal, attempt.signal]);
    let cardConfirmed = false;
    try {
      const card = await readCard(current.reference, requestSignal);
      requestSignal.throwIfAborted();
      cardConfirmed = true;
      const resolveOrigin = current.origin === current.reference;
      current.reference = { id: card.id };
      if (resolveOrigin) current.origin = current.reference;
      onSelectionChange?.(current.reference, false);
      const data = getBoardData();
      const [laneLabel, relations] = await Promise.all([
        readLaneLabel(card.lane_id, data, requestSignal),
        readHierarchy(card, data, requestSignal),
      ]);
      requestSignal.throwIfAborted();
      if (acceptTextVersion?.(card.id, card.revision, card.etag) === false) {
        if (!current.staleReadRetried) {
          current.staleReadRetried = true;
          current.refreshQueued = true;
        }
        throw new ApiError("invalid_response");
      }
      current.staleReadRetried = false;
      renderCard(card, laneLabel, data);
      renderHierarchy(relations);
      onTextContext?.({
        cardId: card.id,
        title: card.title,
        description: card.description,
        revision: card.revision,
        etag: card.etag,
        editable: !card.archived,
        archived: card.archived,
        laneId: card.lane_id,
        ownerId: card.owner_id,
        priorityId: card.priority_id,
        commentCount: card.comment_count,
        attachmentCount: card.attachment_count,
        coverAttachmentId: card.cover_attachment_id,
      });
      onTextReadiness?.(true);
      setStatus("");
    } catch (error) {
      if (requestSignal.aborted) return;
      const missing = !cardConfirmed && cardUnavailable(error);
      if (missing && current.restored) {
        close(false, true);
        return;
      }
      if (missing) onSelectionChange?.(null, true);
      if (missing || details.hidden) {
        if (details.contains(doc.activeElement)) body.focus({ preventScroll: true });
        title.textContent = "Card unavailable";
        details.hidden = true;
        onTextContext?.(null);
      }
      setStatus(
        missing
          ? "Card not found on this board."
          : details.hidden
            ? "Could not load this card."
            : "Could not refresh this card.",
        !missing,
      );
    } finally {
      attempt.abort();
      current.loading = false;
      current.read = undefined;
      if (!selectionSignal.aborted) {
        retryButton.removeAttribute("aria-disabled");
        if (current.refreshQueued) {
          current.refreshQueued = false;
          refresh();
        }
      }
    }
  }

  function refresh() {
    if (selection && !signal.aborted) void load(selection);
  }

  function open(reference: CardReference, opener?: HTMLElement, focus = true) {
    if (signal.aborted) return;
    const previous = selection;
    previous?.lifetime.abort();
    selection = {
      reference,
      origin: opener ? reference : (previous?.origin ?? reference),
      opener: opener ?? previous?.opener,
      lifetime: new AbortController(),
      loading: false,
      refreshQueued: false,
      restored: !focus,
      staleReadRetried: false,
      read: undefined,
    };
    onSelectionChange?.(reference, focus);
    onTextReadiness?.(false);
    onTextContext?.(null);
    panel.hidden = false;
    ticketLabel.textContent = "ticket" in reference ? `#${reference.ticket}` : "";
    title.textContent = "Loading card…";
    setStatus("");
    details.hidden = true;
    hierarchy.replaceChildren();
    displayedHierarchy = undefined;
    body.scrollTop = 0;
    if (focus) title.focus({ preventScroll: true });
    refresh();
  }

  function close(focus = true, explicit = focus) {
    if (!selection) return;
    const { origin, opener, lifetime: currentLifetime } = selection;
    selection = undefined;
    currentLifetime.abort();
    panel.hidden = true;
    onSelectionChange?.(null, explicit);
    onTextReadiness?.(false);
    onTextContext?.(null);
    if (!focus) return;
    if (opener?.isConnected) {
      opener.focus();
      const scope = opener.getRootNode();
      if ("activeElement" in scope && scope.activeElement === opener) return;
    }
    focusFallback(origin, opener);
  }

  closeButton.addEventListener("click", () => close(), { signal });
  retryButton.addEventListener(
    "click",
    () => {
      if (!selection || selection.loading) return;
      selection.staleReadRetried = false;
      setStatus("Retrying card read…", true);
      refresh();
    },
    { signal },
  );
  doc.addEventListener(
    "keydown",
    (event) => {
      if (!selection || event.defaultPrevented || event.isComposing || event.key !== "Escape")
        return;
      event.preventDefault();
      close();
    },
    { signal },
  );
  function dispose() {
    lifetime.abort();
    selection?.lifetime.abort();
    selection = undefined;
    panel.remove();
  }

  if (signal.aborted) dispose();
  else ownerSignal.addEventListener("abort", dispose, { signal });
  return {
    open,
    close,
    refresh,
    refreshRestored() {
      if (!selection) return;
      selection.restored = true;
      selection.read?.abort();
      refresh();
    },
    suspendRead() {
      selection?.read?.abort();
      if (selection) selection.refreshQueued = false;
    },
    dispose,
    editor,
    tools,
    title,
    description,
    showConfirmedText,
    ticketLink,
    get selected() {
      return selection?.reference ?? null;
    },
  };
}
