import { ApiError } from "../api.mjs";
import type { AppContext } from "./model.js";

type ActorKind = "human" | "bot";
type ActorPresentation = Partial<Record<ActorKind, { label?: string; accent?: string }>>;
type Label = Readonly<{ id: string; label: string }>;
type Actor = Label & Readonly<{ kind: ActorKind; color: string }>;

type BoardLane = Label &
  Readonly<{
    role: string;
    notes?: string;
  }>;

type BoardCard = Readonly<{
  id: string;
  title: string;
  ticket: number;
  lane_id: string;
  priority_id: string;
  owner_id: string;
  completed_at: string | null;
  comment_count: number;
  attachment_count: number;
}>;

type BoardContents = Readonly<{
  board: Readonly<{
    id: string;
    name: string;
    revision: number;
    actor_presentation?: ActorPresentation;
    user_css: string;
  }>;
  lanes: readonly BoardLane[];
  cards: readonly BoardCard[];
}>;

export type BoardViewData = BoardContents &
  Readonly<{
    etag: string;
    priorities: readonly Label[];
    users: readonly Actor[];
  }>;

export type BoardViewOptions = Readonly<{
  onOpenCard?: (card: BoardCard, opener: HTMLButtonElement) => void;
  renderLaneActions?: (lane: BoardLane, container: HTMLElement) => void;
  onViewChange?: () => void;
}>;

type BoardViewport = Readonly<{
  filter: string;
  horizontalScroll: number;
  laneScroll: Readonly<Record<string, number>>;
}>;

const completionAge = 24 * 60 * 60 * 1000;
const colorHex = /^#[0-9a-f]{6}$/i;

function isLabel(value: unknown): value is Label {
  return (
    typeof value === "object" &&
    value !== null &&
    "id" in value &&
    typeof value.id === "string" &&
    value.id.length > 0 &&
    "label" in value &&
    typeof value.label === "string" &&
    value.label.length > 0
  );
}

function isActor(value: unknown): value is Actor {
  return (
    isLabel(value) &&
    "kind" in value &&
    (value.kind === "human" || value.kind === "bot") &&
    "color" in value &&
    typeof value.color === "string"
  );
}

async function readCatalog<T extends Label>(
  request: (cursor?: string) => Promise<{ data: object }>,
  collection: "priorities" | "users",
  validate: (entry: unknown) => entry is T,
): Promise<T[]> {
  const entries: T[] = [];
  const ids = new Set<string>();
  const cursors = new Set<string>();
  let cursor: string | undefined;
  do {
    const { data } = await request(cursor);
    const rows: unknown = (data as Record<string, unknown>)[collection];
    const next: unknown = (data as Record<string, unknown>).next_cursor;
    if (
      !Array.isArray(rows) ||
      (next !== null && (typeof next !== "string" || next.length === 0 || cursors.has(next)))
    ) {
      throw new ApiError("invalid_response");
    }
    for (const row of rows as unknown[]) {
      if (!validate(row) || ids.has(row.id)) throw new ApiError("invalid_response");
      ids.add(row.id);
      entries.push(row);
    }
    cursor = next ?? undefined;
    if (cursor !== undefined) cursors.add(cursor);
  } while (cursor !== undefined);
  return entries;
}

export async function getBoardView(context: AppContext, boardId: string): Promise<BoardViewData> {
  const { api } = context;
  const group = new AbortController();
  const signal = AbortSignal.any([context.signal, group.signal]);
  try {
    for (let attempt = 0; attempt < 2; attempt++) {
      signal.throwIfAborted();
      const { data, etag } = await api.getBoardLanes(boardId, signal);
      // The adapter validates membership, ordering of lanes, card fields, and actor presentation.
      const contents = data as BoardContents;
      if (
        etag === null ||
        typeof contents.board.name !== "string" ||
        typeof contents.board.user_css !== "string"
      ) {
        throw new ApiError("invalid_response");
      }
      const [priorities, users] = await Promise.all([
        readCatalog((cursor) => api.listPriorities(boardId, cursor, signal), "priorities", isLabel),
        readCatalog((cursor) => api.listUsers(boardId, cursor, signal), "users", isActor),
      ]);
      const check = await api.getBoardIfChanged(boardId, etag, signal);
      signal.throwIfAborted();
      if (check.etag !== etag) continue;

      const priorityIds = new Set(priorities.map(({ id }) => id));
      const userIds = new Set(users.map(({ id }) => id));
      if (
        contents.cards.some(
          ({ priority_id, owner_id }) => !priorityIds.has(priority_id) || !userIds.has(owner_id),
        )
      ) {
        throw new ApiError("invalid_response");
      }
      return { ...contents, etag, priorities, users };
    }
    throw new ApiError("invalid_response");
  } finally {
    group.abort();
  }
}

function countLabel(count: number, singular: string): string {
  return `${count} ${singular}${count === 1 ? "" : "s"}`;
}

function olderThanCutoff(completedAt: string | null, cutoff: number): boolean {
  if (completedAt === null) return false;
  const date = new Date(completedAt.replace(" ", "T"));
  return (
    date.getTime() < cutoff &&
    date.toISOString().replace("T", " ").replace(".000Z", "+00:00") === completedAt
  );
}

export function mountBoardView(
  root: HTMLElement,
  initialData: BoardViewData,
  options: BoardViewOptions = {},
) {
  const doc = root.ownerDocument;
  const view = doc.defaultView;
  if (!view) throw new Error("The board view requires a window.");

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

  const boundary = element("div", "board-view-boundary");
  const host = element("div", "board-view-host");
  boundary.append(host);
  const shadow = host.attachShadow({ mode: "open" });
  const boardStyle = new view.CSSStyleSheet();
  shadow.adoptedStyleSheets = [boardStyle];
  const stylesheet = doc.createElement("link");
  stylesheet.rel = "stylesheet";
  stylesheet.href = "/assets/app.css";

  const board = element("div", "board-view");
  const filterBar = element("div", "board-view-filter board-filter");
  const filterLabel = doc.createElement("label");
  const filter = doc.createElement("input");
  filter.id = "board-filter";
  filter.type = "search";
  filter.setAttribute("aria-keyshortcuts", "/");
  filterLabel.append(
    element("span", "board-view-filter-label", "Filter by title or ticket"),
    filter,
  );
  const filterCount = element("span", "board-view-filter-count");
  filterCount.setAttribute("role", "status");
  filterBar.append(filterLabel, filterCount);
  const strip = element("div", "board-view-lanes lanes");
  board.append(filterBar, strip);
  shadow.append(stylesheet, board);

  type CardFace = { card: BoardCard; item: HTMLLIElement; opener: HTMLButtonElement };
  type LaneFace = {
    lane: BoardLane;
    section: HTMLElement;
    count: HTMLSpanElement;
    countDescription: HTMLSpanElement;
    scroll: HTMLDivElement;
    reveal: HTMLButtonElement;
    list: HTMLUListElement;
    members: CardFace[];
  };

  let currentBoard = initialData.board.id;
  let lanes: LaneFace[] = [];
  let showOlderCompleted = false;
  const { onOpenCard, renderLaneActions, onViewChange } = options;

  function cardFace(
    card: BoardCard,
    priority: Label | undefined,
    owner: Actor | undefined,
    presentation: ActorPresentation | undefined,
  ): CardFace {
    const item = doc.createElement("li");
    const opener = element("button", "board-view-card card");
    opener.type = "button";
    opener.dataset.cardId = card.id;
    const ticket = `#${card.ticket}`;
    opener.setAttribute("aria-label", `${ticket} ${card.title}`);
    if (onOpenCard) {
      opener.addEventListener("click", () => onOpenCard(card, opener));
    } else {
      opener.disabled = true;
      opener.setAttribute("aria-disabled", "true");
    }

    const meta = element("span", "board-view-card-meta");
    meta.append(element("span", "board-view-card-ticket", ticket));
    const description: string[] = [];
    if (priority) {
      meta.append(element("span", "board-view-card-priority card-priority", priority.label));
      description.push(`Priority ${priority.label}`);
    }
    if (owner) {
      const custom = presentation?.[owner.kind];
      const name = custom?.label ?? owner.label;
      const actor = element("span", "board-view-card-actor actor-label", name);
      actor.dataset.actorKind = owner.kind;
      const accent = custom?.accent ?? owner.color;
      if (colorHex.test(accent)) {
        actor.classList.add("actor-accent");
        actor.style.setProperty("--board-view-actor-accent", accent);
      }
      meta.append(actor);
      description.push(`Owner ${name}`);
    }
    opener.append(element("span", "board-view-card-title", card.title), meta);
    if (card.comment_count > 0 || card.attachment_count > 0) {
      const counts = element("span", "board-view-card-counts card-counts");
      counts.setAttribute("aria-hidden", "true");
      if (card.comment_count > 0) {
        counts.append(
          element(
            "span",
            "board-view-card-comments card-comment-count",
            `💬 ${card.comment_count}`,
          ),
        );
        description.push(countLabel(card.comment_count, "comment"));
      }
      if (card.attachment_count > 0) {
        counts.append(
          element(
            "span",
            "board-view-card-attachments card-attachment-count",
            `📎 ${card.attachment_count}`,
          ),
        );
        description.push(countLabel(card.attachment_count, "attachment"));
      }
      opener.append(counts);
    }
    if (description.length > 0) opener.setAttribute("aria-description", description.join(", "));
    item.append(opener);
    return { card, item, opener };
  }

  function paintFilter(revealingLane?: LaneFace) {
    const query = filter.value.trim().toLowerCase();
    const cutoff = Date.now() - completionAge;
    let total = 0;
    let matching = 0;
    let revealTarget: CardFace | undefined;
    for (const laneFace of lanes) {
      const { lane, count, countDescription, reveal, list, members } = laneFace;
      const matches = query
        ? members.filter(
            ({ card }) =>
              card.title.toLowerCase().includes(query) || `#${card.ticket}`.includes(query),
          )
        : members;
      total += members.length;
      matching += matches.length;
      const laneSummary = query
        ? `${matches.length} of ${members.length} cards`
        : countLabel(members.length, "card");
      count.textContent = query ? `${matches.length}/${members.length}` : String(members.length);
      count.title = laneSummary;
      countDescription.textContent = laneSummary;

      if (revealingLane && revealingLane !== laneFace) continue;
      let hiddenCount = 0;
      const visible = doc.createDocumentFragment();
      for (const face of matches) {
        const { card, item } = face;
        const older = lane.role === "completed" && olderThanCutoff(card.completed_at, cutoff);
        if (older && !showOlderCompleted) {
          hiddenCount++;
          continue;
        }
        if (older && revealingLane === laneFace) revealTarget ??= face;
        visible.append(item);
      }
      reveal.hidden = hiddenCount === 0;
      reveal.textContent = `Show ${countLabel(hiddenCount, "older card")}`;
      list.replaceChildren(visible);
    }
    const summary = query ? `${matching} of ${total} cards match` : countLabel(total, "card");
    if (filterCount.textContent !== summary) filterCount.textContent = summary;
    if (revealingLane) {
      let target: HTMLElement = revealTarget?.opener ?? revealingLane.scroll;
      if (revealTarget?.opener.disabled) {
        revealTarget.item.tabIndex = -1;
        target = revealTarget.item;
      }
      target.focus({ preventScroll: true });
      target.scrollIntoView({ block: "nearest", inline: "nearest" });
    }
  }

  function laneFace(lane: BoardLane): LaneFace {
    const section = element("section", "board-view-lane lane");
    section.dataset.laneId = lane.id;
    section.dataset.laneRole = lane.role;
    const heading = element("div", "board-view-lane-heading lane-heading");
    const title = doc.createElement("h2");
    title.id = `board-lane-${lane.id}`;
    title.title = lane.label;
    title.textContent = lane.label;
    const count = element("span", "board-view-lane-count lane-total");
    const countDescription = doc.createElement("span");
    countDescription.hidden = true;
    countDescription.id = `board-lane-count-${lane.id}`;
    heading.append(title, count, countDescription);
    section.append(heading);
    const scroll = element("div", "board-view-lane-scroll");
    scroll.tabIndex = 0;
    scroll.setAttribute("role", "group");
    scroll.setAttribute("aria-labelledby", title.id);
    const note = element("div", "board-view-lane-note lane-note");
    const descriptions = [countDescription.id];
    if (lane.notes) {
      const toggle = element("button", "board-view-lane-note-toggle");
      toggle.type = "button";
      toggle.title = lane.notes;
      toggle.setAttribute("aria-label", `Lane note for ${lane.label}`);
      toggle.setAttribute("aria-expanded", "false");
      toggle.append(element("span", "board-view-lane-note-preview", lane.notes));

      const full = element("p", "board-view-lane-note-full body-text", lane.notes);
      full.id = `board-lane-note-${lane.id}`;
      full.hidden = true;
      toggle.setAttribute("aria-controls", full.id);
      toggle.addEventListener("click", () => {
        const expanded = full.hidden;
        full.hidden = !expanded;
        toggle.setAttribute("aria-expanded", String(expanded));
        if (expanded) scroll.scrollTop = 0;
      });
      note.append(toggle);
      scroll.append(full);
      descriptions.push(full.id);
    }
    scroll.setAttribute("aria-describedby", descriptions.join(" "));
    if (renderLaneActions) {
      const actions = element("div", "board-view-lane-actions");
      renderLaneActions(lane, actions);
      scroll.append(actions);
    }
    const reveal = element("button", "board-view-reveal show-older-completed");
    reveal.type = "button";
    const list = element("ul", "board-view-cards");
    list.id = `board-lane-cards-${lane.id}`;
    reveal.setAttribute("aria-controls", list.id);
    scroll.append(reveal, list);
    section.append(note, scroll);
    const face: LaneFace = {
      lane,
      section,
      count,
      countDescription,
      scroll,
      reveal,
      list,
      members: [],
    };
    reveal.addEventListener("click", () => {
      showOlderCompleted = true;
      paintFilter(face);
    });
    return face;
  }

  function update(data: BoardViewData) {
    const sameBoard = data.board.id === currentBoard;
    const focused = shadow.activeElement;
    const focusLane = focused?.closest<HTMLElement>("[data-lane-id]")?.dataset.laneId;
    const focusCard = focused?.getAttribute("data-card-id");
    const scrollLeft = sameBoard ? strip.scrollLeft : 0;
    const scrollTops = new Map(lanes.map(({ lane, scroll }) => [lane.id, scroll.scrollTop]));
    if (!sameBoard) {
      currentBoard = data.board.id;
      showOlderCompleted = false;
      filter.value = "";
      scrollTops.clear();
    }
    const priorities = new Map(data.priorities.map((priority) => [priority.id, priority]));
    const users = new Map(data.users.map((user) => [user.id, user]));
    lanes = data.lanes.map(laneFace);
    const lanesById = new Map(lanes.map((face) => [face.lane.id, face]));
    for (const card of data.cards) {
      lanesById
        .get(card.lane_id)
        ?.members.push(
          cardFace(
            card,
            priorities.get(card.priority_id),
            users.get(card.owner_id),
            data.board.actor_presentation,
          ),
        );
    }
    boardStyle.replaceSync(data.board.user_css);
    paintFilter();
    strip.replaceChildren(...lanes.map(({ section }) => section));
    strip.scrollLeft = scrollLeft;
    for (const { lane, scroll } of lanes) scroll.scrollTop = scrollTops.get(lane.id) ?? 0;
    if (focusLane) {
      const card = sameBoard
        ? lanes.flatMap(({ members }) => members).find(({ card }) => card.id === focusCard)
        : undefined;
      const previousLane = sameBoard ? lanesById.get(focusLane) : undefined;
      const target = card?.opener.isConnected
        ? card.opener
        : focused?.classList.contains("board-view-reveal") && previousLane?.reveal.hidden === false
          ? previousLane.reveal
          : (previousLane?.scroll ?? filter);
      target.focus({ preventScroll: true });
    }
    onViewChange?.();
  }

  const lifetime = new AbortController();
  const listenerOptions = { signal: lifetime.signal };
  let restore = new AbortController();
  let restoreFrame = 0;
  let pendingScroll: Pick<BoardViewport, "horizontalScroll" | "laneScroll"> | undefined;
  function cancelViewportRestore() {
    restore.abort();
    view?.cancelAnimationFrame(restoreFrame);
    pendingScroll = undefined;
  }
  for (const event of ["pointerdown", "keydown", "wheel"]) {
    shadow.addEventListener(event, cancelViewportRestore, listenerOptions);
  }
  filter.addEventListener(
    "input",
    () => {
      paintFilter();
      onViewChange?.();
    },
    listenerOptions,
  );
  strip.addEventListener("scroll", () => onViewChange?.(), {
    ...listenerOptions,
    capture: true,
  });
  filter.addEventListener(
    "keydown",
    (event) => {
      if (event.defaultPrevented || event.isComposing || event.key !== "Escape" || !filter.value) {
        return;
      }
      event.preventDefault();
      filter.value = "";
      paintFilter();
      onViewChange?.();
    },
    listenerOptions,
  );
  doc.addEventListener(
    "keydown",
    (event) => {
      if (
        !host.isConnected ||
        event.defaultPrevented ||
        event.isComposing ||
        event.repeat ||
        event.key !== "/" ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey ||
        event
          .composedPath()
          .some(
            (node) =>
              node instanceof view.HTMLElement &&
              (node.isContentEditable || node.matches("input, textarea, select, [role=textbox]")),
          )
      ) {
        return;
      }
      event.preventDefault();
      filter.focus();
      filter.select();
    },
    listenerOptions,
  );

  update(initialData);
  root.replaceChildren(boundary);
  return {
    update,
    captureViewport(): BoardViewport {
      return {
        filter: filter.value,
        horizontalScroll: pendingScroll?.horizontalScroll ?? strip.scrollLeft,
        laneScroll:
          pendingScroll?.laneScroll ??
          Object.fromEntries(lanes.map(({ lane, scroll }) => [lane.id, scroll.scrollTop])),
      };
    },
    restoreViewport({ filter: query, horizontalScroll, laneScroll }: BoardViewport) {
      cancelViewportRestore();
      restore = new AbortController();
      const restoreSignal = AbortSignal.any([lifetime.signal, restore.signal]);
      pendingScroll = { horizontalScroll, laneScroll };
      filter.value = query;
      paintFilter();
      const restoreScroll = () => {
        if (restoreSignal.aborted) return;
        strip.scrollLeft = horizontalScroll;
        for (const { lane, scroll } of lanes) scroll.scrollTop = laneScroll[lane.id] ?? 0;
        pendingScroll = undefined;
        onViewChange?.();
      };
      if (stylesheet.sheet) {
        restoreScroll();
        return;
      }
      stylesheet.addEventListener(
        "load",
        () => {
          restoreFrame = view.requestAnimationFrame(restoreScroll);
        },
        { signal: restoreSignal, once: true },
      );
    },
    focusCard(id: string): boolean {
      const card = lanes.flatMap(({ members }) => members).find(({ card }) => card.id === id);
      if (!card || card.opener.disabled || !card.opener.isConnected) return false;
      card.opener.focus({ preventScroll: true });
      card.opener.scrollIntoView({ block: "nearest", inline: "nearest" });
      return true;
    },
    focusFilter() {
      filter.focus();
    },
    dispose() {
      lifetime.abort();
      cancelViewportRestore();
      boundary.remove();
    },
  };
}
