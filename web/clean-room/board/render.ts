import type { CardDto, LaneDto, PriorityDto, Uuid } from "../api-types.js";
import { bindBoardMoves } from "./move.js";
import { projectCards } from "./filter.js";
import { laneApprovalAvailability } from "./approve-lane.js";
import type {
  BoardData,
  BoardProps,
  BoardScroll,
  FocusTarget,
  MountComponent,
  ScrollPosition,
} from "../contracts.js";

interface CardElements {
  readonly button: HTMLButtonElement;
  readonly priority: HTMLSpanElement;
  readonly ticket: HTMLSpanElement;
  readonly title: HTMLSpanElement;
  readonly comments: HTMLSpanElement;
  readonly commentCount: HTMLSpanElement;
}

interface LaneElements {
  readonly section: HTMLElement;
  readonly title: HTMLHeadingElement;
  readonly count: HTMLSpanElement;
  readonly create: HTMLButtonElement;
  readonly reorder: HTMLDivElement;
  readonly approve: HTMLDivElement;
  readonly caption: HTMLParagraphElement;
  readonly list: HTMLDivElement;
  readonly cards: Map<Uuid, CardElements>;
  readonly older: HTMLInputElement;
  readonly olderLabel: HTMLLabelElement;
  readonly olderText: HTMLSpanElement;
}

function element<Tag extends keyof HTMLElementTagNameMap>(tag: Tag, className: string) {
  const node = document.createElement(tag);
  node.className = className;
  return node;
}

function button(className: string, label: string): HTMLButtonElement {
  const control = element("button", className);
  control.type = "button";
  control.setAttribute("aria-label", label);
  return control;
}

function setText(node: HTMLElement, value: string): void {
  if (node.textContent !== value) node.textContent = value;
}

function place(parent: HTMLElement, child: HTMLElement, index: number): void {
  const current = parent.children.item(index);
  if (current !== child) parent.insertBefore(child, current);
}

function retainedData(props: BoardProps): BoardData | null {
  return props.data.status === "ready"
    ? props.data.current.value
    : (props.data.previous?.value ?? null);
}

function permissions(data: BoardData, laneId: Uuid): string {
  const incoming = new Set<Uuid>();
  const outgoing = new Set<Uuid>();
  for (const grant of data.policy.move_grants) {
    if (grant.to_lane_id === laneId) incoming.add(grant.user_id);
    if (grant.from_lane_id === laneId) outgoing.add(grant.user_id);
  }
  const users = data.users.toSorted((left, right) => left.kind.localeCompare(right.kind));
  function labels(ids: ReadonlySet<Uuid>): string {
    return (
      users
        .filter((user) => ids.has(user.id))
        .map((user) => data.view.board.actor_presentation?.[user.kind]?.label ?? user.label)
        .join(" + ") || "—"
    );
  }
  return `IN: ${labels(incoming)} · OUT: ${labels(outgoing)}`;
}

function updateCard(nodes: CardElements, card: CardDto, priority: PriorityDto | undefined): void {
  const ticket = String(card.ticket).padStart(4, "0");
  const priorityKey = priority?.key.toUpperCase() ?? "";
  setText(nodes.ticket, ticket);
  setText(nodes.title, card.title);
  setText(nodes.priority, priorityKey);
  nodes.priority.dataset.priority = priorityKey;
  nodes.priority.title = priority?.label ?? "";
  nodes.priority.hidden = priority === undefined;
  nodes.priority.style.backgroundColor = priority?.color ?? "";
  nodes.button.setAttribute("aria-label", `Open card #${ticket}: ${card.title}`);
  nodes.comments.hidden = card.comment_count === 0;
  nodes.comments.title = `${card.comment_count} ${card.comment_count === 1 ? "comment" : "comments"}`;
  setText(nodes.commentCount, String(card.comment_count));
}

function position(node: HTMLElement): ScrollPosition {
  return { left: node.scrollLeft, top: node.scrollTop };
}

function restorePosition(node: HTMLElement, requested: ScrollPosition): void {
  if (node.scrollLeft !== requested.left) node.scrollLeft = requested.left;
  if (node.scrollTop !== requested.top) node.scrollTop = requested.top;
}

function sameFocus(left: FocusTarget | null, right: FocusTarget | null): boolean {
  return left?.control === right?.control && left?.resourceId === right?.resourceId;
}

export const mountBoard: MountComponent<BoardProps> = (host, initialProps) => {
  let props = initialProps;
  let lastFocus = initialProps.focus;
  let pendingFocus = initialProps.focus;
  let configuredCss = "";
  let stylesLoaded = false;
  const lanes = new Map<Uuid, LaneElements>();
  const focusTargets = new WeakMap<HTMLElement, FocusTarget>();
  const lifetime = new AbortController();
  const listeners = { signal: lifetime.signal };

  const surface = document.createElement("div");
  surface.style.cssText = "height: 100%; min-width: 0; min-height: 0";
  const shadow = surface.attachShadow({ mode: "open" });
  const baseline = document.createElement("link");
  baseline.rel = "stylesheet";
  baseline.href = "/assets/board.css";
  const boardStyles = new CSSStyleSheet();

  const root = element("section", "lsw-board");
  const loading = element("p", "lsw-board-loading");
  loading.setAttribute("role", "status");
  const unavailable = element("section", "lsw-board-unavailable");
  const unavailableTitle = element("h2", "lsw-board-unavailable-title");
  const unavailableText = element("p", "lsw-board-unavailable-text");
  const retry = button("lsw-board-retry", "Retry");
  setText(unavailableTitle, "Board unavailable");
  setText(retry, "Retry");
  unavailable.append(unavailableTitle, unavailableText, retry);
  const viewport = element("div", "lsw-board-viewport");
  const track = element("div", "lsw-board-lanes");
  viewport.append(track);
  root.append(loading, unavailable, viewport);
  shadow.append(baseline, root);
  const movement = bindBoardMoves(root, {
    data: null,
    actorId: null,
    enabled: false,
    onMove: () => {},
  });

  function trackFocus(control: HTMLElement, action: string, resourceId: Uuid | null): void {
    focusTargets.set(control, { control: action, resourceId });
  }

  function laneAction(action: string, kind: "reorder" | "approve", laneId: Uuid): HTMLDivElement {
    const container = element("div", "lsw-board-lane-action");
    const control = button(`lsw-board-lane-${kind}`, action);
    control.title = action;
    control.setAttribute("aria-disabled", "true");
    const icon = element("span", `lsw-board-icon-${kind}`);
    icon.setAttribute("aria-hidden", "true");
    control.append(icon);
    container.append(control);
    trackFocus(control, kind === "reorder" ? "reorder-tickets" : "approve-lane", laneId);
    return container;
  }

  function createLane(lane: LaneDto): LaneElements {
    const section = element("section", "lsw-board-lane lane");
    section.dataset.laneId = lane.id;
    const header = element("header", "lsw-board-lane-header");
    const heading = element("div", "lsw-board-lane-heading");
    const title = element("h2", "lsw-board-lane-title");
    title.id = `lsw-board-lane-${lane.id}`;
    section.setAttribute("aria-labelledby", title.id);
    const count = element("span", "lsw-board-lane-count");
    const create = button("lsw-board-lane-create", "Add card");
    create.setAttribute("aria-disabled", "true");
    setText(create, "+");
    trackFocus(create, "create-card", lane.id);
    const reorder = laneAction("Reorder tickets by dependencies", "reorder", lane.id);
    const approve = laneAction("Approve lane", "approve", lane.id);
    heading.append(title, count, create, reorder, approve);
    header.append(heading);
    const caption = element("p", "lsw-board-lane-permissions");
    const list = element("div", "lsw-board-card-list");
    const older = element("input", "lsw-board-completed-checkbox");
    older.type = "checkbox";
    const olderLabel = element("label", "lsw-board-completed-toggle");
    const olderText = element("span", "");
    olderLabel.title =
      "Cards finished more than 24 hours ago are hidden. Tick to bring them back for this visit.";
    olderLabel.hidden = true;
    olderLabel.append(older, olderText);
    older.addEventListener(
      "change",
      () => {
        props.onNavigate({
          route: { ...props.route, showCompleted: older.checked },
          history: "replace",
        });
      },
      listeners,
    );
    const permissionsBlock = element("div", "lsw-board-lane-context");
    permissionsBlock.append(caption, olderLabel);
    create.addEventListener(
      "click",
      () => {
        if (props.writable && create.getAttribute("aria-disabled") !== "true")
          props.onLaneAction?.("create", lane.id);
      },
      listeners,
    );
    reorder.addEventListener(
      "click",
      () => {
        if (
          props.writable &&
          reorder.querySelector("button")?.getAttribute("aria-disabled") !== "true"
        )
          props.onLaneAction?.("order", lane.id);
      },
      listeners,
    );
    approve.addEventListener(
      "click",
      () => {
        if (
          props.writable &&
          approve.querySelector("button")?.getAttribute("aria-disabled") !== "true"
        )
          props.onLaneAction?.("approve", lane.id);
      },
      listeners,
    );
    section.append(header, permissionsBlock, list);
    return {
      section,
      title,
      count,
      create,
      reorder,
      approve,
      caption,
      list,
      older,
      olderLabel,
      olderText,
      cards: new Map(),
    };
  }

  function createCard(cardId: Uuid): CardElements {
    const open = button("lsw-board-card card", "Open card");
    open.dataset.cardId = cardId;
    const meta = element("span", "lsw-board-card-meta");
    const priority = element("span", "lsw-board-card-priority");
    const ticket = element("span", "lsw-board-card-ticket");
    const title = element("span", "lsw-board-card-title");
    const comments = element("span", "lsw-board-card-comments");
    const icon = element("span", "lsw-board-icon-comment");
    icon.setAttribute("aria-hidden", "true");
    const commentCount = element("span", "lsw-board-card-comment-count");
    comments.append(icon, commentCount);
    meta.append(priority, ticket);
    open.append(meta, title, comments);
    trackFocus(open, "open-card", cardId);
    return { button: open, priority, ticket, title, comments, commentCount };
  }

  function renderCards(
    lane: LaneElements,
    cards: readonly CardDto[],
    priorities: ReadonlyMap<Uuid, PriorityDto>,
  ): void {
    const visible = new Set(cards.map((card) => card.id));
    for (const [id, nodes] of lane.cards) {
      if (visible.has(id)) continue;
      nodes.button.remove();
      lane.cards.delete(id);
    }
    for (const [index, card] of cards.entries()) {
      const nodes = lane.cards.get(card.id) ?? createCard(card.id);
      lane.cards.set(card.id, nodes);
      updateCard(nodes, card, priorities.get(card.priority_id));
      place(lane.list, nodes.button, index);
    }
  }

  function renderLanes(data: BoardData): void {
    const session = props.session;
    const capabilities = data.policy.resource_grants.find(
      (grant) => grant.user_id === session?.principal_id,
    )?.capabilities;
    const createLanes = new Set(
      data.policy.create_grants
        .filter((grant) => grant.user_id === session?.principal_id)
        .map((grant) => grant.lane_id),
    );
    const canCreate = capabilities?.includes("create_card") ?? false;
    const canReorder = capabilities?.includes("reorder_tickets") ?? false;
    const priorities = new Map(data.priorities.map((priority) => [priority.id, priority]));
    const projection =
      props.projection ?? projectCards(data.view, props.route.showCompleted, null, Date.now());
    const cardsByLane = projection.cardsByLane;
    const visible = data.view.lanes
      .filter((lane) => !lane.retired)
      .toSorted((left, right) => left.position - right.position);
    const visibleIds = new Set(visible.map((lane) => lane.id));
    for (const [id, nodes] of lanes) {
      if (visibleIds.has(id)) continue;
      nodes.section.remove();
      lanes.delete(id);
    }
    for (const [index, lane] of visible.entries()) {
      const nodes = lanes.get(lane.id) ?? createLane(lane);
      const cards = cardsByLane.get(lane.id) ?? [];
      lanes.set(lane.id, nodes);
      nodes.section.dataset.role = lane.role;
      setText(nodes.title, lane.label);
      nodes.title.title = lane.notes ?? "";
      setText(nodes.count, String(cards.length));
      nodes.count.setAttribute(
        "aria-label",
        `${cards.length} ${cards.length === 1 ? "card" : "cards"}`,
      );
      nodes.create.hidden = !canCreate || !createLanes.has(lane.id);
      nodes.create.setAttribute("aria-disabled", String(!props.writable));
      nodes.create.title = `Add card to ${lane.label}`;
      nodes.create.setAttribute("aria-label", nodes.create.title);
      nodes.reorder.hidden = !canReorder || !lane.reorder_enabled;
      const reorderButton = nodes.reorder.querySelector("button");
      reorderButton?.setAttribute(
        "aria-disabled",
        String(!props.writable || cards.length < 2 || props.route.search.trim() !== ""),
      );
      const approval = props.approvalState?.(lane.id);
      const availability = laneApprovalAvailability(data, session, lane.id);
      nodes.approve.hidden = !availability.visible && approval !== "unknown";
      const filteredApproval = cards.length !== availability.count;
      const approveButton = nodes.approve.querySelector("button");
      const approvalLabel =
        approval === "unknown"
          ? "Check approval status"
          : availability.count === 0
            ? "Disabled; no cards in swimlane"
            : filteredApproval
              ? "Clear the card search to approve the full lane"
              : `Approve all ${availability.count} cards in ${lane.label} and move to Completed`;
      if (approveButton) {
        approveButton.title = approvalLabel;
        approveButton.setAttribute("aria-label", approvalLabel);
        approveButton.setAttribute(
          "aria-disabled",
          String(
            !props.writable ||
              approval === "busy" ||
              (approval !== "unknown" && (!availability.enabled || filteredApproval)),
          ),
        );
      }
      nodes.olderLabel.hidden = lane.role !== "completed" || projection.olderCompleted === 0;
      nodes.older.checked = props.route.showCompleted;
      setText(nodes.olderText, `Unhide old (${projection.olderCompleted})`);
      setText(nodes.caption, permissions(data, lane.id));
      renderCards(nodes, cards, priorities);
      place(track, nodes.section, index);
    }
  }

  function reportScroll(): void {
    if (viewport.hidden || !stylesLoaded) return;
    const scroll: BoardScroll = {
      board: position(viewport),
      lanes: Array.from(lanes, ([laneId, nodes]) => ({ laneId, position: position(nodes.list) })),
    };
    props.onScrollChange(scroll);
  }

  function restoreScroll(scroll: BoardScroll): void {
    restorePosition(viewport, scroll.board);
    const requestedLanes = new Map(scroll.lanes.map((lane) => [lane.laneId, lane.position]));
    for (const [laneId, nodes] of lanes) {
      restorePosition(nodes.list, requestedLanes.get(laneId) ?? { left: 0, top: 0 });
    }
  }

  function restoreFocus(): void {
    const wanted = pendingFocus;
    if (wanted === null || !stylesLoaded) return;
    const documentFocus = document.activeElement;
    if (documentFocus !== null && documentFocus !== document.body && documentFocus !== surface) {
      pendingFocus = null;
      return;
    }
    for (const control of root.querySelectorAll("button")) {
      const target = focusTargets.get(control);
      if (
        target !== undefined &&
        sameFocus(target, wanted) &&
        control.getClientRects().length > 0
      ) {
        if (shadow.activeElement !== control) control.focus({ preventScroll: true });
        if (shadow.activeElement === control) pendingFocus = null;
        return;
      }
    }
  }

  function finishStyles(): void {
    stylesLoaded = true;
    applyBoardStyles();
    viewport.hidden = retainedData(props) === null;
    loading.hidden = props.data.status !== "loading" || !viewport.hidden;
    if (!viewport.hidden) restoreScroll(props.scroll);
    restoreFocus();
  }

  baseline.addEventListener("load", finishStyles, { ...listeners, once: true });
  baseline.addEventListener("error", finishStyles, { ...listeners, once: true });
  trackFocus(retry, "retry-board", null);
  retry.addEventListener("click", () => props.onRefresh(), listeners);
  viewport.addEventListener("scroll", reportScroll, { ...listeners, capture: true, passive: true });
  document.addEventListener(
    "focusin",
    (event) => {
      if (
        event.target instanceof Node &&
        event.target !== document.body &&
        !event.composedPath().includes(root)
      ) {
        pendingFocus = null;
      }
    },
    listeners,
  );
  root.addEventListener(
    "focusin",
    (event) => {
      if (!(event.target instanceof HTMLElement)) return;
      const target = focusTargets.get(event.target);
      if (target === undefined) return;
      pendingFocus = null;
      if (sameFocus(target, lastFocus)) return;
      lastFocus = target;
      props.onFocusChange(target);
    },
    listeners,
  );
  viewport.addEventListener(
    "click",
    (event) => {
      if (!(event.target instanceof Element)) return;
      const control = event.target.closest("button");
      const cardId = control?.dataset.cardId;
      if (cardId === undefined) return;
      props.onNavigate({ route: { ...props.route, cardId }, history: "replace" });
    },
    listeners,
  );

  function applyBoardStyles(): void {
    const css = retainedData(props)?.view.board.user_css ?? "";
    if (css === configuredCss) return;
    configuredCss = css;
    boardStyles.replaceSync(css);
    shadow.adoptedStyleSheets = css === "" ? [] : [boardStyles];
  }

  function update(next: BoardProps): void {
    const active = shadow.activeElement;
    const ownedFocus = active instanceof HTMLElement ? focusTargets.get(active) : undefined;
    const focusChanged = !sameFocus(next.focus, lastFocus);
    if (focusChanged) {
      lastFocus = next.focus;
      pendingFocus = next.focus;
    }
    const previousScroll = props.scroll;
    const previouslyVisible = !viewport.hidden;
    props = next;
    const data = retainedData(next);
    if (stylesLoaded) applyBoardStyles();
    viewport.hidden = data === null || !stylesLoaded;
    unavailable.hidden = next.data.status !== "unavailable";
    loading.hidden = !unavailable.hidden || !viewport.hidden;
    unavailable.classList.toggle("lsw-board-unavailable-retained", data !== null);
    root.dataset.readState = next.data.status;
    if (!loading.hidden) setText(loading, `Loading ${next.boardName ?? "board"}...`);
    if (!unavailable.hidden) {
      setText(
        unavailableText,
        data === null
          ? "This board could not be loaded. Try again."
          : "Showing the last confirmed board.",
      );
    }
    if (data !== null) {
      renderLanes(data);
    }
    movement.update({
      data,
      actorId: next.session?.principal_id ?? null,
      enabled: next.writable === true && next.moveEnabled === true,
      onMove: (intent) => props.onMove?.(intent),
    });
    if (stylesLoaded && data !== null && (!previouslyVisible || next.scroll !== previousScroll))
      restoreScroll(next.scroll);
    if (
      !focusChanged &&
      active instanceof HTMLElement &&
      ownedFocus !== undefined &&
      (shadow.activeElement !== active || active.getClientRects().length === 0)
    ) {
      pendingFocus = ownedFocus;
    }
    restoreFocus();
  }

  viewport.hidden = true;
  host.append(surface);
  update(initialProps);
  return {
    update,
    dispose() {
      lifetime.abort();
      movement.dispose();
      shadow.adoptedStyleSheets = [];
      surface.remove();
    },
  };
};
