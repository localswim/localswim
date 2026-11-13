import type { CardDto, EntityTag, LaneDto, SessionDto, Timestamp, Uuid } from "../api-types.js";
import type { BoardData, InspectionResult, MutationResult } from "../contracts.js";
import { isFocusedCard, readFocusedCard } from "../drawer/read.js";
import {
  entityTag,
  invalidResponse,
  isRecord,
  isTimestamp,
  isUnknownArray,
  mutateJson,
  readJson,
} from "../http.js";

export interface MoveIntent {
  readonly cardId: Uuid;
  readonly laneId: Uuid;
}

export interface PreparedCardMove {
  readonly card: CardDto;
  readonly cardEtag: EntityTag;
  readonly boardEtag: EntityTag;
  readonly boardRevision: number;
  readonly boardUpdatedAt: Timestamp;
  readonly actorId: Uuid;
  readonly destination: LaneDto;
}

export function moveDestinations(
  data: BoardData,
  card: CardDto,
  actorId: Uuid | null,
): readonly LaneDto[] {
  if (actorId === null || card.archived || data.view.board.availability !== "open") return [];
  const granted = new Set(
    data.policy.move_grants
      .filter(
        (grant) =>
          grant.user_id === actorId &&
          grant.from_lane_id === card.lane_id &&
          grant.to_lane_id !== card.lane_id,
      )
      .map((grant) => grant.to_lane_id),
  );
  return data.view.lanes
    .filter((lane) => !lane.retired && granted.has(lane.id))
    .toSorted((left, right) => left.position - right.position);
}

export async function prepareCardMove(
  data: BoardData,
  cardId: Uuid,
  laneId: Uuid,
  actorId: Uuid,
  signal?: AbortSignal,
): Promise<PreparedCardMove | null> {
  const { board, cards } = data.view;
  const card = cards.find((item) => item.id === cardId);
  if (card === undefined) return null;
  const destination = moveDestinations(data, card, actorId).find((lane) => lane.id === laneId);
  if (destination === undefined) return null;
  const focused = await readFocusedCard(board.id, cardId, signal);
  if (
    focused.card.revision !== card.revision ||
    focused.card.lane_id !== card.lane_id ||
    focused.card.archived
  ) {
    return null;
  }
  return {
    ...focused,
    boardEtag: data.boardEtag,
    boardRevision: board.revision,
    boardUpdatedAt: board.updated_at,
    actorId,
    destination,
  };
}

export function requestCardMove(
  move: PreparedCardMove,
  signal?: AbortSignal,
  beforeDispatch?: (session: SessionDto) => void,
): Promise<MutationResult<CardDto>> {
  const { card, destination } = move;
  return mutateJson(`/cards/${card.id}`, {
    method: "PATCH",
    etag: move.cardEtag,
    boardEtag: move.boardEtag,
    body: { lane_id: destination.id },
    signal,
    beforeDispatch,
    validate: (value, headers) => {
      if (
        !isFocusedCard(value) ||
        value.id !== card.id ||
        value.board_id !== card.board_id ||
        value.revision !== move.boardRevision + 1 ||
        value.lane_id !== destination.id ||
        value.owner_id !== card.owner_id ||
        value.archived ||
        (value.completed_at !== null) !== (destination.role === "completed") ||
        entityTag(headers) === move.cardEtag ||
        entityTag(headers, "X-Board-ETag") === move.boardEtag
      ) {
        return invalidResponse();
      }
      return value;
    },
  });
}

function isOriginalMove(event: unknown, move: PreparedCardMove, through: Timestamp): boolean {
  const { card, destination } = move;
  const operation =
    card.completed_at !== null && destination.role !== "completed" ? "reopen" : "patch";
  return (
    isRecord(event) &&
    typeof event.id === "string" &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(event.id) &&
    event.board_id === card.board_id &&
    event.revision === move.boardRevision + 1 &&
    isTimestamp(event.at) &&
    event.at >= move.boardUpdatedAt &&
    event.at <= through &&
    event.actor_id === move.actorId &&
    event.operation === operation &&
    event.resource_type === "card" &&
    event.resource_id === card.id &&
    event.field === "lane_id" &&
    event.before === card.lane_id &&
    event.after === destination.id
  );
}

export async function inspectCardMove(
  move: PreparedCardMove,
  signal?: AbortSignal,
): Promise<InspectionResult<CardDto>> {
  const { card } = move;
  try {
    const { card: current } = await readFocusedCard(card.board_id, card.id, signal);
    if (current.revision <= move.boardRevision) return { outcome: "unknown" };
    // The board guard pins the move to one revision. Later card edits may change its lane again.
    const query = new URLSearchParams({
      board_id: card.board_id,
      card_id: card.id,
      actor_id: move.actorId,
      view: "full",
      from: move.boardUpdatedAt,
      to: current.updated_at,
      limit: "200",
    });
    const { data } = await readJson(`/activities?${query}`, signal);
    if (
      isRecord(data) &&
      typeof data.board_revision === "number" &&
      Number.isSafeInteger(data.board_revision) &&
      data.board_revision >= current.revision &&
      isUnknownArray(data.activities) &&
      data.activities.length <= 200 &&
      data.activities.some((event) => isOriginalMove(event, move, current.updated_at))
    ) {
      return { outcome: "committed", value: current };
    }
  } catch {
    // The original request can still have committed when readback fails.
  }
  return { outcome: "unknown" };
}

const cardSelector = ".lsw-board-card[data-card-id]";
const laneSelector = ".lsw-board-lane[data-lane-id]";

export interface BoardMoveProps {
  readonly data: BoardData | null;
  readonly actorId: Uuid | null;
  readonly enabled: boolean;
  readonly onMove: (intent: MoveIntent) => void;
}

export function bindBoardMoves(board: HTMLElement, initial: BoardMoveProps) {
  let props = initial;
  let drag: {
    readonly source: HTMLElement;
    readonly cardId: Uuid;
    readonly lanes: Set<Uuid>;
  } | null = null;
  let highlighted: HTMLElement | null = null;
  const lifetime = new AbortController();
  const listeners = { signal: lifetime.signal };

  function findCard(id: Uuid): CardDto | undefined {
    return props.data?.view.cards.find((card) => card.id === id);
  }

  function destinations(card: CardDto | undefined): readonly LaneDto[] {
    if (!props.enabled || props.data === null || card === undefined) return [];
    return moveDestinations(props.data, card, props.actorId);
  }

  function highlight(lane: HTMLElement | null): void {
    if (highlighted === lane) return;
    highlighted?.classList.remove("lsw-move-target");
    highlighted = lane;
    highlighted?.classList.add("lsw-move-target");
  }

  function reset(): void {
    highlight(null);
    drag?.source.classList.remove("lsw-move-source");
    drag = null;
  }

  function validTarget(event: DragEvent): HTMLElement | null {
    if (drag === null || !(event.target instanceof Element)) return null;
    const lane = event.target.closest<HTMLElement>(laneSelector);
    const id = lane?.dataset.laneId;
    return lane && id && drag.lanes.has(id) && board.contains(lane) ? lane : null;
  }

  board.addEventListener(
    "dragstart",
    (event) => {
      if (!(event.target instanceof Element)) return;
      const source = event.target.closest<HTMLElement>(cardSelector);
      const cardId = source?.dataset.cardId;
      if (source === null || cardId === undefined) return;
      const lanes = destinations(findCard(cardId));
      if (lanes.length === 0 || event.dataTransfer === null) {
        event.preventDefault();
        return;
      }
      reset();
      event.dataTransfer.setData("application/x-localswim-card", cardId);
      event.dataTransfer.effectAllowed = "move";
      source.classList.add("lsw-move-source");
      drag = { source, cardId, lanes: new Set(lanes.map((lane) => lane.id)) };
    },
    listeners,
  );
  board.addEventListener(
    "dragover",
    (event) => {
      if (drag === null) return;
      const lane = validTarget(event);
      highlight(lane);
      if (lane === null) return;
      event.preventDefault();
      if (event.dataTransfer !== null) event.dataTransfer.dropEffect = "move";
    },
    listeners,
  );
  board.addEventListener(
    "dragleave",
    (event) => {
      if (
        highlighted !== null &&
        event.target instanceof Node &&
        highlighted.contains(event.target) &&
        (!(event.relatedTarget instanceof Node) || !highlighted.contains(event.relatedTarget))
      ) {
        highlight(null);
      }
    },
    listeners,
  );
  board.addEventListener(
    "drop",
    (event) => {
      const cardId = drag?.cardId;
      const laneId = validTarget(event)?.dataset.laneId;
      reset();
      if (cardId === undefined || laneId === undefined) return;
      event.preventDefault();
      props.onMove({ cardId, laneId });
    },
    listeners,
  );
  board.addEventListener("dragend", reset, listeners);

  function update(next: BoardMoveProps): void {
    if (
      props.data !== next.data ||
      props.actorId !== next.actorId ||
      !next.enabled ||
      (drag !== null && !board.contains(drag.source))
    ) {
      reset();
    } else if (highlighted !== null && !board.contains(highlighted)) {
      highlight(null);
    }
    props = next;
    const cards = new Map(next.data?.view.cards.map((card) => [card.id, card]));
    const movableLanes = new Map<Uuid, boolean>();
    for (const node of board.querySelectorAll<HTMLElement>(cardSelector)) {
      const card = cards.get(node.dataset.cardId ?? "");
      if (card === undefined || card.archived) {
        node.draggable = false;
        continue;
      }
      const movable = movableLanes.get(card.lane_id) ?? destinations(card).length > 0;
      movableLanes.set(card.lane_id, movable);
      node.draggable = movable;
    }
  }

  update(initial);
  return {
    update,
    dispose() {
      lifetime.abort();
      reset();
    },
  };
}
