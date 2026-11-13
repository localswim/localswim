import { ApiError } from "../api.mjs";
import type { CardMovement } from "./card-movement.js";
import type { AppContext } from "./model.js";

type ActorKind = "human" | "bot";
type ActorPresentation = Partial<Record<ActorKind, { label?: string; accent?: string }>>;
type Label = Readonly<{ id: string; label: string }>;
type Actor = Label & Readonly<{ kind: ActorKind; color: string }>;
type Priority = Label & Readonly<{ rank: number }>;

type BoardLane = Label &
  Readonly<{
    role: string;
    key: string;
    position: number;
    revision: number;
    retired: boolean;
    approval_enabled: boolean;
    bulk_approval_comment?: string;
    reorder_enabled: boolean;
    notes?: string;
  }>;

type BoardCard = Readonly<{
  id: string;
  title: string;
  description: string;
  revision: number;
  ticket: number;
  lane_id: string;
  priority_id: string;
  owner_id: string;
  completed_at: string | null;
  comment_count: number;
  attachment_count: number;
  cover_attachment_id: string | null;
}>;

type UnnumberedCard = Pick<BoardCard, "id" | "title" | "priority_id" | "owner_id"> &
  Readonly<{ ticket: null }>;

type BoardContents = Readonly<{
  board: Readonly<{
    id: string;
    name: string;
    revision: number;
    policy_id: string;
    default_owner_id: string;
    actor_presentation?: ActorPresentation;
    user_css: string;
  }>;
  lanes: readonly BoardLane[];
  cards: readonly BoardCard[];
}>;

export type BoardViewData = BoardContents &
  Readonly<{
    etag: string;
    priorities: readonly Priority[];
    users: readonly Actor[];
  }>;

export type BoardPendingChange = Readonly<{ boardId: string; cardId: string }> &
  Readonly<
    | { kind: "title"; revision: number; title: string }
    | { kind: "move"; revision: number; sourceLaneId: string; laneId: string }
    | { kind: "priority"; revision: number; priorityId: string }
    | {
        kind: "create";
        boardEtag: string;
        laneId: string;
        title: string;
        priorityId: string;
        ownerId: string;
      }
  >;

export type SettleBoardPending = (settlement?: "confirmed" | { priorityId: string }) => void;
export type ProjectBoardPending = (change: BoardPendingChange) => SettleBoardPending;

export type BoardViewOptions = Readonly<{
  onOpenCard?: (card: BoardCard, opener: HTMLButtonElement) => void;
  renderLaneActions?: (lane: BoardLane, container: HTMLElement) => void;
  onViewChange?: () => void;
  onMembershipChange?: () => void;
  mountCover?: (host: HTMLElement, card: BoardCard, signal: AbortSignal) => void;
  movement?: CardMovement;
  pendingChanges?: () => readonly BoardPendingChange[];
}>;

type BoardViewport = Readonly<{
  filter: string;
  horizontalScroll: number;
  laneScroll: Readonly<Record<string, number>>;
}>;

const completionAge = 24 * 60 * 60 * 1000;
const colorHex = /^#[0-9a-f]{6}$/i;
const cardDragType = "application/vnd.localswim.card-move";

function moveSource(card: BoardCard) {
  return { cardId: card.id, laneId: card.lane_id, revision: card.revision };
}

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

function isPriority(value: unknown): value is Priority {
  return (
    isLabel(value) &&
    "rank" in value &&
    typeof value.rank === "number" &&
    Number.isSafeInteger(value.rank)
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
        typeof contents.board.policy_id !== "string" ||
        typeof contents.board.default_owner_id !== "string" ||
        typeof contents.board.user_css !== "string"
      ) {
        throw new ApiError("invalid_response");
      }
      const [priorities, users] = await Promise.all([
        readCatalog(
          (cursor) => api.listPriorities(boardId, cursor, signal),
          "priorities",
          isPriority,
        ),
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
  const NodeType = view.Node;
  const ElementType = view.HTMLElement;

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
  const moveFeedback = element("div", "board-view-movement");
  const moveStatus = element("p", "board-view-move-status");
  moveStatus.setAttribute("role", "status");
  const inspectMove = element("button", "", "Inspect saved card");
  inspectMove.type = "button";
  moveFeedback.append(moveStatus, inspectMove);
  const strip = element("div", "board-view-lanes lanes");
  board.append(filterBar, strip);
  if (options.movement) filterBar.after(moveFeedback);
  shadow.append(stylesheet, board);

  type CardFace<T extends BoardCard | UnnumberedCard = BoardCard | UnnumberedCard> = {
    card: T;
    item: HTMLLIElement;
    opener: HTMLButtonElement;
    title: HTMLSpanElement;
    priority: HTMLSpanElement;
    pending: HTMLSpanElement;
    description: readonly string[];
    painted?: BoardPendingChange | null;
  };
  type LaneFace = {
    lane: BoardLane;
    section: HTMLElement;
    count: HTMLSpanElement;
    countDescription: HTMLSpanElement;
    scroll: HTMLDivElement;
    reveal: HTMLButtonElement;
    list: HTMLUListElement;
    members: CardFace<BoardCard>[];
    displayed: CardFace[];
    pendingMembership: boolean;
  };

  let currentBoard = initialData.board.id;
  let confirmedData = initialData;
  let lanes: LaneFace[] = [];
  let facesById = new Map<string, CardFace>();
  let priorities = new Map<string, Priority>();
  let users = new Map<string, Actor>();
  let projectedChanges: readonly BoardPendingChange[] | undefined;
  let showOlderCompleted = false;
  const { onOpenCard, renderLaneActions, onViewChange, onMembershipChange, movement } = options;
  let coverLifetime = new AbortController();
  const coverMounts: Array<() => void> = [];
  const lifetime = new AbortController();
  const listenerOptions = { signal: lifetime.signal };
  type Drag = {
    opener: HTMLButtonElement;
    source: ReturnType<typeof moveSource>;
    payload: string;
    request: AbortController;
    original: ReturnType<CardMovement["prepareDrag"]>;
  };
  let drag: Drag | undefined;
  let dropping: AbortController | undefined;
  let dropTarget: HTMLElement | undefined;
  let deferredData: BoardViewData | undefined;

  function markDropTarget(target?: HTMLElement) {
    if (dropTarget === target) return;
    dropTarget?.removeAttribute("data-drop-target");
    dropTarget = target;
    if (target) target.dataset.dropTarget = "true";
  }

  function finishDrag(): Drag | undefined {
    const current = drag;
    drag = undefined;
    current?.opener.removeAttribute("data-dragging");
    markDropTarget();
    const deferred = deferredData;
    deferredData = undefined;
    if (deferred) update(deferred);
    else if (current) refreshPending();
    return current;
  }

  function cancelMovement() {
    dropping?.abort();
    finishDrag()?.request.abort();
  }

  function refreshMovement() {
    if (!movement) return;
    const feedback = movement.status();
    moveFeedback.hidden = !feedback.message;
    if (moveStatus.textContent !== feedback.message) moveStatus.textContent = feedback.message;
    if (inspectMove === shadow.activeElement && !feedback.canInspect) {
      moveStatus.tabIndex = -1;
      moveStatus.focus({ preventScroll: true });
    }
    inspectMove.hidden = !feedback.canInspect;
    const enabled = new Map<string, boolean>();
    for (const { members } of lanes) {
      for (const { card, opener } of members) {
        if (card.ticket === null) continue;
        const source = moveSource(card);
        let granted = enabled.get(source.laneId);
        if (granted === undefined) {
          granted = movement.destinations(source).length > 0;
          enabled.set(source.laneId, granted);
        }
        if (opener !== drag?.opener) {
          opener.draggable =
            !opener.disabled && !opener.dataset.pending && granted && movement.available(source);
        }
      }
    }
    if (drag && dropTarget && !canDrop(drag, dropTarget.dataset.laneId ?? "")) {
      markDropTarget();
    }
  }

  function canDrop(current: Drag, laneId: string) {
    return (
      current.opener.isConnected &&
      movement?.available(current.source) === true &&
      movement.destinations(current.source).some(({ id }) => id === laneId)
    );
  }

  inspectMove.addEventListener("click", () => void movement?.inspect(), listenerOptions);

  function cardFace<T extends BoardCard | UnnumberedCard>(card: T): CardFace<T> {
    const item = doc.createElement("li");
    const opener = element("button", "board-view-card card");
    opener.type = "button";
    opener.dataset.cardId = card.id;
    const ticket = card.ticket === null ? "" : `#${card.ticket}`;
    if (card.ticket !== null && onOpenCard) {
      opener.addEventListener("click", () => onOpenCard(card, opener));
    } else {
      opener.disabled = true;
      opener.setAttribute("aria-disabled", "true");
    }

    const meta = element("span", "board-view-card-meta");
    if (ticket) meta.append(element("span", "board-view-card-ticket", ticket));
    const priority = element("span", "board-view-card-priority card-priority");
    const pending = element("span", "board-view-pending-label");
    meta.append(priority, pending);
    const description: string[] = [];
    const owner = users.get(card.owner_id);
    if (owner) {
      const custom = confirmedData.board.actor_presentation?.[owner.kind];
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
    const title = element("span", "board-view-card-title");
    opener.append(title, meta);
    if (card.ticket !== null && (card.comment_count > 0 || card.attachment_count > 0)) {
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
    if (card.ticket !== null && movement) {
      opener.addEventListener("dragstart", (event) => {
        const transfer = event.dataTransfer;
        const source = moveSource(card);
        if (
          drag ||
          dropping ||
          !transfer ||
          !opener.draggable ||
          !opener.isConnected ||
          !movement.available(source) ||
          movement.destinations(source).length === 0
        ) {
          event.preventDefault();
          return;
        }
        const request = new AbortController();
        const payload = JSON.stringify({ boardId: currentBoard, ...source });
        transfer.setData(cardDragType, payload);
        transfer.effectAllowed = "move";
        opener.dataset.dragging = "true";
        drag = {
          opener,
          source,
          payload,
          request,
          original: movement.prepareDrag(source, request.signal),
        };
      });
      opener.addEventListener("dragend", () => {
        if (drag?.opener === opener) finishDrag()?.request.abort();
      });
    }
    item.append(opener);
    if (card.ticket !== null && options.mountCover && card.attachment_count > 0) {
      const cover = element("span", "board-view-cover-slot");
      opener.prepend(cover);
      const mount = options.mountCover;
      coverMounts.push(() => mount(cover, card, coverLifetime.signal));
    }
    return { card, item, opener, title, priority, pending, description };
  }

  function paintFace(face: CardFace, change?: BoardPendingChange) {
    if (face.painted === (change ?? null)) return;
    face.painted = change ?? null;
    const { card, opener, title, priority, pending, description } = face;
    const text = change?.kind === "title" ? change.title : card.title;
    const priorityId = change?.kind === "priority" ? change.priorityId : card.priority_id;
    const label = priorities.get(priorityId)?.label;
    title.textContent = text;
    priority.textContent = label ?? "";
    priority.hidden = label === undefined;
    pending.hidden = change === undefined;
    pending.textContent = change?.kind === "create" ? "Pending creation" : change ? "Pending" : "";
    opener.classList.toggle("board-view-card-pending", change !== undefined);
    if (change) opener.dataset.pending = change.kind;
    else delete opener.dataset.pending;
    opener.setAttribute(
      "aria-label",
      `${card.ticket === null ? "" : `#${card.ticket} `}${text}${change ? `, ${pending.textContent}` : ""}`,
    );
    const details = label ? [`Priority ${label}`, ...description] : description;
    if (details.length) opener.setAttribute("aria-description", details.join(", "));
    else opener.removeAttribute("aria-description");
  }

  function paintPending(changes: readonly BoardPendingChange[]) {
    const byLane = new Map(lanes.map((lane) => [lane.lane.id, lane]));
    const byCard = new Map(
      changes
        .filter(({ boardId }) => boardId === currentBoard)
        .map((change) => [change.cardId, change]),
    );
    const placements: Array<{ face: CardFace; lane: LaneFace; priorityId: string }> = [];
    for (const lane of lanes) {
      lane.displayed = [];
      lane.pendingMembership = false;
    }
    for (const lane of lanes) {
      for (const face of lane.members) {
        const { card } = face;
        const change = byCard.get(card.id);
        const applicable =
          change?.kind !== "create" && change?.revision === card.revision ? change : undefined;
        const target =
          applicable?.kind === "move" && applicable.sourceLaneId === lane.lane.id
            ? byLane.get(applicable.laneId)
            : undefined;
        if (target && target !== lane) {
          lane.pendingMembership = target.pendingMembership = true;
          placements.push({ face, lane: target, priorityId: card.priority_id });
          paintFace(face, applicable);
        } else if (applicable?.kind === "priority" && priorities.has(applicable.priorityId)) {
          placements.push({ face, lane, priorityId: applicable.priorityId });
          paintFace(face, applicable);
        } else {
          lane.displayed.push(face);
          paintFace(face, applicable?.kind === "title" ? applicable : undefined);
        }
      }
    }
    for (const change of byCard.values()) {
      if (
        change.kind !== "create" ||
        change.boardEtag !== confirmedData.etag ||
        facesById.has(change.cardId)
      )
        continue;
      const lane = byLane.get(change.laneId);
      if (!lane || !priorities.has(change.priorityId) || !users.has(change.ownerId)) continue;
      const face = cardFace({
        id: change.cardId,
        ticket: null,
        title: change.title,
        priority_id: change.priorityId,
        owner_id: change.ownerId,
      });
      lane.pendingMembership = true;
      placements.push({ face, lane, priorityId: change.priorityId });
      paintFace(face, change);
    }
    // The server resolves relationship ordering. Keep the order of its other faces
    // and position projected faces by priority and ticket until the next snapshot.
    const pendingRanks = new Map(placements.map(({ face, priorityId }) => [face, priorityId]));
    const rank = (id: string) => priorities.get(id)?.rank ?? Number.POSITIVE_INFINITY;
    for (const { face, lane, priorityId } of placements) {
      const projectedRank = rank(priorityId);
      const ticket = face.card.ticket ?? Number.POSITIVE_INFINITY;
      const index = lane.displayed.findIndex((other) => {
        const otherRank = rank(pendingRanks.get(other) ?? other.card.priority_id);
        return (
          otherRank > projectedRank ||
          (otherRank === projectedRank && (other.card.ticket ?? Number.POSITIVE_INFINITY) > ticket)
        );
      });
      lane.displayed.splice(index === -1 ? lane.displayed.length : index, 0, face);
    }
    projectedChanges = changes;
  }

  function refreshPending() {
    if (drag || lifetime.signal.aborted) return;
    const changes = options.pendingChanges?.() ?? [];
    if (
      projectedChanges?.length === changes.length &&
      projectedChanges.every((change, index) => change === changes[index])
    )
      return;
    const focused = shadow.activeElement;
    const focusLane = focused?.closest<HTMLElement>("[data-lane-id]")?.dataset.laneId;
    const scrollLeft = strip.scrollLeft;
    const scrollTops = lanes.map(({ scroll }) => scroll.scrollTop);
    paintPending(changes);
    paintFilter();
    strip.scrollLeft = scrollLeft;
    lanes.forEach(({ scroll }, index) => {
      scroll.scrollTop = scrollTops[index] ?? 0;
    });
    if (focused instanceof ElementType && focusLane && shadow.activeElement !== focused) {
      const lane = lanes.find(({ lane }) => lane.id === focusLane);
      const target =
        focused.isConnected && !focused.closest("[hidden]") && !focused.matches(":disabled")
          ? focused
          : (lane?.scroll ?? filter);
      target.focus({ preventScroll: true });
    }
    refreshMovement();
    onMembershipChange?.();
  }

  function paintFilter(revealingLane?: LaneFace) {
    const query = filter.value.trim().toLowerCase();
    const cutoff = Date.now() - completionAge;
    let total = 0;
    let matching = 0;
    let revealTarget: CardFace | undefined;
    for (const laneFace of lanes) {
      const { lane, count, countDescription, reveal, list, displayed } = laneFace;
      const matches = query
        ? displayed.filter(
            ({ card, title }) =>
              title.textContent.toLowerCase().includes(query) ||
              (card.ticket !== null && `#${card.ticket}`.includes(query)),
          )
        : displayed;
      total += displayed.length;
      matching += matches.length;
      const laneSummary = query
        ? `${matches.length} of ${displayed.length} cards`
        : countLabel(displayed.length, "card");
      count.textContent = query
        ? `${matches.length}/${displayed.length}`
        : String(displayed.length);
      count.title = laneSummary;
      countDescription.textContent = laneSummary;

      if (revealingLane && revealingLane !== laneFace) continue;
      let hiddenCount = 0;
      const visible: HTMLLIElement[] = [];
      for (const face of matches) {
        const { card, item } = face;
        const older =
          lane.role === "completed" &&
          card.ticket !== null &&
          olderThanCutoff(card.completed_at, cutoff);
        if (older && !showOlderCompleted) {
          hiddenCount++;
          continue;
        }
        if (older && revealingLane === laneFace) revealTarget ??= face;
        visible.push(item);
      }
      reveal.hidden = hiddenCount === 0;
      reveal.textContent = `Show ${countLabel(hiddenCount, "older card")}`;
      const visibleSet = new Set<Element>(visible);
      for (const item of Array.from(list.children)) {
        if (!visibleSet.has(item)) item.remove();
      }
      for (const [index, item] of visible.entries()) {
        const current = list.children.item(index);
        if (item !== current) list.insertBefore(item, current);
      }
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
    if (movement) {
      section.addEventListener("dragover", (event) => {
        const transfer = event.dataTransfer;
        if (!drag || !transfer?.types.includes(cardDragType) || !canDrop(drag, lane.id)) {
          markDropTarget();
          return;
        }
        event.preventDefault();
        transfer.dropEffect = "move";
        markDropTarget(section);
      });
      section.addEventListener("dragleave", (event) => {
        if (
          dropTarget === section &&
          !(event.relatedTarget instanceof NodeType && section.contains(event.relatedTarget))
        ) {
          markDropTarget();
        }
      });
      section.addEventListener("drop", (event) => {
        const current = drag;
        const transfer = event.dataTransfer;
        if (!current || !transfer?.types.includes(cardDragType)) return;
        event.preventDefault();
        const allowed =
          transfer.getData(cardDragType) === current.payload && canDrop(current, lane.id);
        finishDrag();
        if (!allowed) {
          current.request.abort();
          return;
        }
        dropping = current.request;
        void movement
          .drop(current.source, lane.id, current.original, current.request.signal)
          .finally(() => {
            if (dropping === current.request) dropping = undefined;
            current.request.abort();
          });
      });
    }
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
      displayed: [],
      pendingMembership: false,
    };
    reveal.addEventListener("click", () => {
      cancelMovement();
      showOlderCompleted = true;
      paintFilter(face);
      onViewChange?.();
      onMembershipChange?.();
    });
    return face;
  }

  function update(data: BoardViewData) {
    const sameBoard = data.board.id === currentBoard;
    if (drag && sameBoard) {
      deferredData = data;
      return;
    }
    if (!sameBoard) cancelMovement();
    confirmedData = data;
    coverLifetime.abort();
    coverLifetime = new AbortController();
    coverMounts.length = 0;
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
    priorities = new Map(data.priorities.map((priority) => [priority.id, priority]));
    users = new Map(data.users.map((user) => [user.id, user]));
    lanes = data.lanes.map(laneFace);
    const lanesById = new Map(lanes.map((face) => [face.lane.id, face]));
    facesById = new Map();
    for (const card of data.cards) {
      const lane = lanesById.get(card.lane_id);
      if (!lane) continue;
      const face = cardFace(card);
      lane.members.push(face);
      facesById.set(card.id, face);
    }
    boardStyle.replaceSync(data.board.user_css);
    paintPending(options.pendingChanges?.() ?? []);
    paintFilter();
    strip.replaceChildren(...lanes.map(({ section }) => section));
    for (const mount of coverMounts) mount();
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
    refreshMovement();
    onViewChange?.();
    onMembershipChange?.();
  }

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
      cancelMovement();
      paintFilter();
      onViewChange?.();
      onMembershipChange?.();
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
      cancelMovement();
      filter.value = "";
      paintFilter();
      onViewChange?.();
      onMembershipChange?.();
    },
    listenerOptions,
  );
  doc.addEventListener(
    "keydown",
    (event) => {
      if (drag && event.key === "Escape" && !event.defaultPrevented && !event.isComposing) {
        event.preventDefault();
        cancelMovement();
        return;
      }
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
    refreshPending,
    refreshMovement,
    cancelMovement,
    revokeCovers() {
      coverLifetime.abort();
    },
    laneMembership(laneId: string) {
      const lane = lanes.find(({ lane }) => lane.id === laneId);
      if (!lane) return undefined;
      const query = filter.value.trim().toLowerCase();
      const visible: BoardCard[] = [];
      for (const { card, item, opener } of lane.members) {
        if (
          item.parentElement === lane.list &&
          (!query ||
            card.title.toLowerCase().includes(query) ||
            `#${card.ticket}`.includes(query)) &&
          opener.checkVisibility({ opacityProperty: true, visibilityProperty: true })
        ) {
          visible.push(card);
        }
      }
      return {
        cards: visible,
        revision: confirmedData.board.revision,
        complete: !query && !lane.pendingMembership && visible.length === lane.members.length,
      };
    },
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
      cancelMovement();
      cancelViewportRestore();
      restore = new AbortController();
      const restoreSignal = AbortSignal.any([lifetime.signal, restore.signal]);
      pendingScroll = { horizontalScroll, laneScroll };
      filter.value = query;
      paintFilter();
      onViewChange?.();
      onMembershipChange?.();
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
      cancelMovement();
      lifetime.abort();
      coverLifetime.abort();
      cancelViewportRestore();
      boundary.remove();
    },
  };
}
