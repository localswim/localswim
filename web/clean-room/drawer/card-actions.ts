import type {
  Capability,
  CardDto,
  EntityTag,
  PriorityDto,
  SessionDto,
  Timestamp,
  Uuid,
} from "../api-types.js";
import type {
  BoardData,
  InspectionResult,
  MountedComponent,
  MutationResult,
} from "../contracts.js";
import { entityTag, invalidResponse, isBoundedText, mutateJson } from "../http.js";
import type { DrawerData } from "./contracts.js";
import { isFocusedCard, readDrawer } from "./read.js";

export type CardPatch =
  | { readonly title: string }
  | { readonly description: string }
  | { readonly owner_id: Uuid }
  | { readonly priority_id: Uuid }
  | { readonly archived: boolean };

export interface PreparedCardPatch {
  readonly card: CardDto;
  readonly cardEtag: EntityTag;
  readonly boardEtag: EntityTag;
  readonly boardRevision: number;
  readonly boardUpdatedAt: Timestamp;
  readonly actorId: Uuid;
  readonly patch: CardPatch;
  readonly priorities: readonly PriorityDto[];
  readonly priorityMayClamp: boolean;
}

function currentBasis(board: BoardData, drawer: DrawerData): boolean {
  return (
    board.view.board.id === drawer.card.board_id &&
    board.view.board.availability === "open" &&
    board.view.board.revision === drawer.boardRevision &&
    board.boardEtag === drawer.boardEtag &&
    drawer.card.revision <= drawer.boardRevision
  );
}

function can(board: BoardData, session: SessionDto, capability: Capability): boolean {
  return (
    board.view.board.user_ids.includes(session.principal_id) &&
    board.policy.resource_grants.some(
      ({ user_id, capabilities }) =>
        user_id === session.principal_id && capabilities.includes(capability),
    )
  );
}

function canRestore(board: BoardData, card: CardDto): boolean {
  return board.view.lanes.some(({ id, retired }) => id === card.lane_id && !retired);
}

function validPatch(
  board: BoardData,
  card: CardDto,
  session: SessionDto,
  patch: CardPatch,
): boolean {
  if ("title" in patch) {
    return (
      patch.title !== card.title &&
      isBoundedText(patch.title, 1, 1024) &&
      /[^\p{White_Space}]/u.test(patch.title) &&
      can(board, session, "edit_card")
    );
  }
  if ("description" in patch) {
    return (
      patch.description !== card.description &&
      isBoundedText(patch.description, 0, 4194304) &&
      can(board, session, "edit_card")
    );
  }
  if ("owner_id" in patch) {
    return (
      patch.owner_id !== card.owner_id &&
      board.users.some(({ id }) => id === patch.owner_id) &&
      can(board, session, "edit_card")
    );
  }
  if ("priority_id" in patch) {
    return (
      patch.priority_id !== card.priority_id &&
      board.priorities.some(({ id }) => id === patch.priority_id) &&
      can(board, session, "set_priority")
    );
  }
  return (
    typeof patch.archived === "boolean" &&
    patch.archived !== card.archived &&
    (patch.archived || canRestore(board, card)) &&
    can(board, session, "archive_card")
  );
}

export function prepareCardPatch(
  board: BoardData,
  drawer: DrawerData,
  session: SessionDto,
  patch: CardPatch,
): PreparedCardPatch | null {
  if (
    Object.keys(patch).length !== 1 ||
    !currentBasis(board, drawer) ||
    !validPatch(board, drawer.card, session, patch)
  ) {
    return null;
  }
  return {
    card: drawer.card,
    cardEtag: drawer.cardEtag,
    boardEtag: drawer.boardEtag,
    boardRevision: drawer.boardRevision,
    boardUpdatedAt: board.view.board.updated_at,
    actorId: session.principal_id,
    patch,
    priorities: board.priorities,
    priorityMayClamp:
      drawer.card.completed_at === null &&
      drawer.relationships.some(
        ({ kind, direction }) => kind === "blocks" && direction === "outgoing",
      ),
  };
}

function matchesPriority(prepared: PreparedCardPatch, actualId: unknown): boolean {
  const { patch } = prepared;
  if (!("priority_id" in patch)) return actualId === prepared.card.priority_id;
  if (actualId === patch.priority_id) return true;
  if (!prepared.priorityMayClamp) return false;
  const requested = prepared.priorities.find(({ id }) => id === patch.priority_id);
  const actual = prepared.priorities.find(({ id }) => id === actualId);
  return requested !== undefined && actual !== undefined && actual.rank < requested.rank;
}

function parsePatchedCard(prepared: PreparedCardPatch, value: unknown, headers: Headers): CardDto {
  const { card, patch } = prepared;
  if (
    !isFocusedCard(value) ||
    value.id !== card.id ||
    value.board_id !== card.board_id ||
    value.ticket !== card.ticket ||
    value.created_at !== card.created_at ||
    value.lane_id !== card.lane_id ||
    value.completed_at !== card.completed_at ||
    value.title !== ("title" in patch ? patch.title : card.title) ||
    value.description !== ("description" in patch ? patch.description : card.description) ||
    value.owner_id !== ("owner_id" in patch ? patch.owner_id : card.owner_id) ||
    value.archived !== ("archived" in patch ? patch.archived : card.archived) ||
    value.parent_id !== card.parent_id ||
    value.child_ids.length !== card.child_ids.length ||
    value.child_ids.some((id, index) => id !== card.child_ids[index]) ||
    value.comment_count !== card.comment_count ||
    value.attachment_count !== card.attachment_count ||
    value.cover_attachment_id !== card.cover_attachment_id ||
    !matchesPriority(prepared, value.priority_id)
  ) {
    return invalidResponse();
  }
  // A lower priority can clamp to the existing effective priority without an edit.
  const unchanged = "priority_id" in patch && value.priority_id === card.priority_id;
  const sameCardTag = entityTag(headers) === prepared.cardEtag;
  const sameBoardTag = entityTag(headers, "X-Board-ETag") === prepared.boardEtag;
  if (unchanged) {
    if (
      !sameCardTag ||
      !sameBoardTag ||
      value.revision !== card.revision ||
      value.updated_at !== card.updated_at ||
      value.blocking_depth !== card.blocking_depth ||
      value.blocked_card_count !== card.blocked_card_count ||
      value.triage_score !== card.triage_score
    )
      return invalidResponse();
  } else if (
    sameCardTag ||
    sameBoardTag ||
    value.revision !== prepared.boardRevision + 1 ||
    value.updated_at < prepared.boardUpdatedAt
  ) {
    return invalidResponse();
  }
  return value;
}

export function requestCardPatch(
  prepared: PreparedCardPatch,
  signal?: AbortSignal,
  beforeDispatch?: (session: SessionDto) => void,
): Promise<MutationResult<CardDto>> {
  return mutateJson(`/cards/${prepared.card.id}`, {
    method: "PATCH",
    etag: prepared.cardEtag,
    boardEtag: prepared.boardEtag,
    body: prepared.patch,
    signal,
    beforeDispatch,
    validate: (value, headers) => parsePatchedCard(prepared, value, headers),
  });
}

function auditValues({ card, patch }: PreparedCardPatch): {
  readonly field: keyof Pick<
    CardDto,
    "title" | "description" | "owner_id" | "priority_id" | "archived"
  >;
  readonly before: string;
  readonly requested: string;
} {
  if ("title" in patch) return { field: "title", before: card.title, requested: patch.title };
  if ("description" in patch)
    return { field: "description", before: card.description, requested: patch.description };
  if ("owner_id" in patch)
    return { field: "owner_id", before: card.owner_id, requested: patch.owner_id };
  if ("priority_id" in patch)
    return { field: "priority_id", before: card.priority_id, requested: patch.priority_id };
  return { field: "archived", before: String(card.archived), requested: String(patch.archived) };
}

export async function inspectCardPatch(
  prepared: PreparedCardPatch,
  signal?: AbortSignal,
): Promise<InspectionResult<CardDto>> {
  const { card } = prepared;
  const { field, before, requested } = auditValues(prepared);
  try {
    const current = await readDrawer(card.board_id, card.id, signal);
    const revision = prepared.boardRevision + 1;
    // Only the original board revision could have accepted the conditional edit.
    // Priority audit records the effective value, so a different requested value cannot be inferred.
    if (
      current.card.revision >= revision &&
      current.activity.some(
        (event) =>
          event.revision === revision &&
          event.at >= prepared.boardUpdatedAt &&
          event.at <= current.card.updated_at &&
          event.actor_id === prepared.actorId &&
          event.operation === "patch" &&
          event.resource_type === "card" &&
          event.resource_id === card.id &&
          event.field === field &&
          event.before === before &&
          event.after === requested,
      )
    ) {
      return { outcome: "committed", value: current.card };
    }
  } catch {
    // A failed read cannot resolve a dispatched edit.
  }
  return { outcome: "unknown" };
}

export interface CardActionHosts {
  readonly priority: HTMLElement;
  readonly owner: HTMLElement;
}

export interface CardActionProps {
  readonly board: BoardData | null;
  readonly drawer: DrawerData | null;
  readonly session: SessionDto | null;
  readonly writable: boolean;
  readonly onPatch: (patch: CardPatch) => void;
}

export function mountCardActions(
  hosts: CardActionHosts,
  initial: CardActionProps,
): MountedComponent<CardActionProps> {
  let props = initial;
  let shownPriorities: BoardData["priorities"] | undefined;
  const lifetime = new AbortController();
  const listeners = { signal: lifetime.signal };
  const priority = document.createElement("select");
  priority.className = "lsw-drawer-priority";
  const owner = document.createElement("button");
  owner.type = "button";
  owner.className = "lsw-drawer-owner-badge";
  hosts.priority.classList.add("lsw-drawer-priority-control");
  hosts.priority.replaceChildren(priority);
  hosts.owner.replaceChildren(owner);

  function dispatch(patch: CardPatch): void {
    const { board, drawer, session, writable } = props;
    if (
      writable &&
      board !== null &&
      drawer !== null &&
      session !== null &&
      prepareCardPatch(board, drawer, session, patch) !== null
    ) {
      props.onPatch(patch);
    }
  }

  function nextOwner(): Uuid | undefined {
    const { board, drawer } = props;
    if (board === null || drawer === null) return undefined;
    const ids = board.view.board.user_ids;
    const current = ids.indexOf(drawer.card.owner_id);
    if (current === -1 || ids.length < 2) return undefined;
    return ids[(current + 1) % ids.length];
  }

  priority.addEventListener(
    "change",
    () => {
      const selected = priority.value;
      priority.value = props.drawer?.card.priority_id ?? "";
      dispatch({ priority_id: selected });
    },
    listeners,
  );
  owner.addEventListener(
    "click",
    () => {
      const id = nextOwner();
      if (id !== undefined) dispatch({ owner_id: id });
    },
    listeners,
  );

  function update(next: CardActionProps): void {
    props = next;
    const { board, drawer, session, writable } = next;
    const card = drawer?.card;
    const selected = board?.priorities.find(({ id }) => id === card?.priority_id);
    const user = board?.users.find(({ id }) => id === card?.owner_id);
    const decoration =
      user === undefined ? undefined : board?.view.board.actor_presentation?.[user.kind];
    const ready =
      writable &&
      board !== null &&
      drawer !== null &&
      session !== null &&
      currentBasis(board, drawer);

    if (shownPriorities !== board?.priorities) {
      shownPriorities = board?.priorities;
      const options = (shownPriorities ?? [])
        .toSorted((left, right) => left.rank - right.rank)
        .map(({ id, key, label }) => new Option(`${key.toUpperCase()} · ${label}`, id));
      priority.replaceChildren(...options);
    }
    hosts.priority.hidden = selected === undefined;
    priority.value = selected?.id ?? "";
    priority.dataset.priority = selected?.key.toUpperCase() ?? "";
    priority.style.backgroundColor = selected?.color ?? "";
    priority.setAttribute("aria-label", selected?.label ?? "Priority");
    priority.disabled =
      !ready || !can(board, session, "set_priority") || board.priorities.length < 2;
    owner.textContent = decoration?.label ?? user?.label ?? "Unknown user";
    owner.style.color = decoration?.accent ?? user?.color ?? "";
    owner.disabled = !ready || !can(board, session, "edit_card") || nextOwner() === undefined;
  }

  update(initial);
  return {
    update,
    dispose() {
      lifetime.abort();
      priority.remove();
      owner.remove();
      hosts.priority.classList.remove("lsw-drawer-priority-control");
    },
  };
}
