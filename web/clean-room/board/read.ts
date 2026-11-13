import type {
  ActorDecorationDto,
  BoardDto,
  BoardViewDto,
  Capability,
  CardDto,
  LaneDto,
  MutableEntityDto,
  PolicyDto,
  PriorityDto,
  UserDto,
  Uuid,
} from "../api-types.js";
import type { BoardData } from "../contracts.js";
import {
  entityTag,
  invalidResponse,
  isBoundedText,
  isRecord,
  isTimestamp,
  isUnknownArray,
  isUuid,
  readFailure,
  readJson,
} from "../http.js";

const pageSize = 200;
const capabilities: ReadonlySet<string> = new Set<Capability>([
  "read",
  "edit_card",
  "comment",
  "archive_card",
  "manage_relationships",
  "manage_attachments",
  "set_priority",
  "approve",
  "manage_board",
  "manage_workflow",
  "manage_permissions",
  "manage_notifications",
  "backup",
  "restore",
  "publish",
  "close_board",
  "create_card",
  "reorder_tickets",
]);

function isArrayOf<T>(value: unknown, check: (entry: unknown) => entry is T): value is T[] {
  return isUnknownArray(value) && value.every(check);
}

function integer(value: unknown, minimum = 0): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= minimum;
}

function isEntity(value: unknown): value is MutableEntityDto & Record<string, unknown> {
  return (
    isRecord(value) &&
    isUuid(value.id) &&
    integer(value.revision, 1) &&
    isTimestamp(value.created_at) &&
    isTimestamp(value.updated_at) &&
    value.updated_at >= value.created_at
  );
}

function isActorDecoration(value: unknown): value is ActorDecorationDto {
  return (
    isRecord(value) &&
    (value.label === undefined || isBoundedText(value.label, 1, 256)) &&
    (value.accent === undefined ||
      (typeof value.accent === "string" && /^#[\da-f]{6}$/i.test(value.accent)))
  );
}

function isBoard(value: unknown): value is BoardDto {
  if (!isEntity(value)) return false;
  const { attention, publishing, actor_presentation: presentation } = value;
  return (
    isBoundedText(value.name, 1, 256) &&
    isUuid(value.policy_id) &&
    isArrayOf(value.user_ids, isUuid) &&
    isUuid(value.default_owner_id) &&
    isArrayOf(value.lane_ids, isUuid) &&
    isArrayOf(value.priority_ids, isUuid) &&
    (value.availability === "open" ||
      value.availability === "closed" ||
      value.availability === "unhealthy") &&
    isRecord(attention) &&
    integer(attention.blocked) &&
    integer(attention.needs_input) &&
    integer(attention.ready_for_review) &&
    (value.last_card_edit_at === null || isTimestamp(value.last_card_edit_at)) &&
    isRecord(publishing) &&
    typeof publishing.enabled === "boolean" &&
    (publishing.repository === null || isBoundedText(publishing.repository, 0, 2048)) &&
    (publishing.last_success_at === null || isTimestamp(publishing.last_success_at)) &&
    typeof publishing.pending === "boolean" &&
    (publishing.error_code === null || isBoundedText(publishing.error_code, 0, 80)) &&
    isBoundedText(value.user_css, 0, 1048576) &&
    (presentation === undefined ||
      (isRecord(presentation) &&
        (presentation.human === undefined || isActorDecoration(presentation.human)) &&
        (presentation.bot === undefined || isActorDecoration(presentation.bot))))
  );
}

function isLane(value: unknown): value is LaneDto {
  return (
    isEntity(value) &&
    isUuid(value.board_id) &&
    isBoundedText(value.key, 1, 128) &&
    isBoundedText(value.label, 1, 256) &&
    integer(value.position) &&
    (value.role === "ordinary" ||
      value.role === "completed" ||
      value.role === "blocked" ||
      value.role === "needs_input" ||
      value.role === "ready_for_review") &&
    value.retired === false &&
    typeof value.approval_enabled === "boolean" &&
    typeof value.reorder_enabled === "boolean" &&
    (value.notes === undefined || isBoundedText(value.notes, 0, 4096)) &&
    (value.bulk_approval_comment === undefined ||
      isBoundedText(value.bulk_approval_comment, 1, 1024))
  );
}

export function isCard(value: unknown): value is CardDto {
  return (
    isEntity(value) &&
    isUuid(value.board_id) &&
    integer(value.ticket, 1) &&
    isBoundedText(value.title, 1, 1024) &&
    /[^\p{White_Space}]/u.test(value.title) &&
    isBoundedText(value.description, 0, 4194304) &&
    isUuid(value.lane_id) &&
    isUuid(value.owner_id) &&
    isUuid(value.priority_id) &&
    (value.parent_id === null || isUuid(value.parent_id)) &&
    isArrayOf(value.child_ids, isUuid) &&
    value.archived === false &&
    (value.cover_attachment_id === null || isUuid(value.cover_attachment_id)) &&
    integer(value.comment_count) &&
    integer(value.attachment_count) &&
    integer(value.blocking_depth) &&
    integer(value.blocked_card_count) &&
    Number.isInteger(value.triage_score) &&
    (value.completed_at === null || isTimestamp(value.completed_at))
  );
}

function isUser(value: unknown): value is UserDto {
  return (
    isRecord(value) &&
    isUuid(value.id) &&
    value.revision === 1 &&
    isTimestamp(value.created_at) &&
    isBoundedText(value.label, 1, 256) &&
    (value.kind === "human" || value.kind === "bot") &&
    isBoundedText(value.color, 0, 64)
  );
}

function isPriority(value: unknown): value is PriorityDto {
  return (
    isEntity(value) &&
    isUuid(value.board_id) &&
    isBoundedText(value.key, 1, 128) &&
    isBoundedText(value.label, 1, 256) &&
    typeof value.rank === "number" &&
    Number.isInteger(value.rank) &&
    value.rank >= 0 &&
    isBoundedText(value.color, 0, 64)
  );
}

function isPolicy(value: unknown): value is PolicyDto {
  return (
    isEntity(value) &&
    isUuid(value.board_id) &&
    isUuid(value.default_priority_id) &&
    isUnknownArray(value.create_grants) &&
    value.create_grants.every(
      (grant: unknown) => isRecord(grant) && isUuid(grant.user_id) && isUuid(grant.lane_id),
    ) &&
    isUnknownArray(value.move_grants) &&
    value.move_grants.every(
      (grant: unknown) =>
        isRecord(grant) &&
        isUuid(grant.user_id) &&
        isUuid(grant.from_lane_id) &&
        isUuid(grant.to_lane_id) &&
        grant.from_lane_id !== grant.to_lane_id,
    ) &&
    isUnknownArray(value.resource_grants) &&
    value.resource_grants.every(
      (grant: unknown) =>
        isRecord(grant) &&
        isUuid(grant.user_id) &&
        isUnknownArray(grant.capabilities) &&
        grant.capabilities.length <= capabilities.size &&
        grant.capabilities.every(
          (item: unknown) => typeof item === "string" && capabilities.has(item),
        ) &&
        new Set(grant.capabilities).size === grant.capabilities.length,
    )
  );
}

function parseView(value: unknown): BoardViewDto {
  if (
    !isRecord(value) ||
    !isBoard(value.board) ||
    !isArrayOf(value.lanes, isLane) ||
    !isArrayOf(value.cards, isCard)
  ) {
    return invalidResponse();
  }
  return { board: value.board, lanes: value.lanes, cards: value.cards };
}

async function readCollection<T extends { readonly id: Uuid }>(
  resource: "users" | "priorities",
  boardId: Uuid,
  ids: readonly Uuid[],
  isEntry: (entry: unknown) => entry is T,
  signal?: AbortSignal,
): Promise<readonly T[] | null> {
  const query = new URLSearchParams({ board_id: boardId, limit: String(pageSize) });
  const pending = new Set(ids);
  const cursors = new Set<string>();
  const entries: T[] = [];
  const pageCount = Math.max(1, Math.ceil(ids.length / pageSize));
  for (let page = 0; page < pageCount; page += 1) {
    const { data } = await readJson(`/${resource}?${query}`, signal);
    if (!isRecord(data)) return invalidResponse();
    const values = data[resource];
    const cursor = data.next_cursor;
    if (
      !isArrayOf(values, isEntry) ||
      values.length > pageSize ||
      new Set(values.map((item) => item.id)).size !== values.length ||
      (cursor !== null && (!isBoundedText(cursor, 1, 2048) || cursors.has(cursor)))
    ) {
      return invalidResponse();
    }
    // A valid page may belong to a newer board revision. Stop paging against the old registry.
    if (values.length !== Math.min(pageSize, pending.size)) return null;
    for (const item of values) {
      if (!pending.delete(item.id)) return null;
      entries.push(item);
    }
    if (pending.size === 0) return cursor === null ? entries : null;
    if (cursor === null) return null;
    cursors.add(cursor);
    query.set("cursor", cursor);
  }
  return invalidResponse();
}

function validateReferences({ view, users, priorities, policy }: BoardData): void {
  const { board, lanes, cards } = view;
  const userIds = new Set(board.user_ids);
  const priorityIds = new Set(board.priority_ids);
  // Board references can include retired lanes, which are absent from the lane view.
  const laneIds = new Set(board.lane_ids);
  const lanesById = new Map(lanes.map((lane) => [lane.id, lane]));
  const cardsById = new Map(cards.map((card) => [card.id, card]));
  const entityIds = new Set([board.id, policy.id, ...userIds, ...priorityIds, ...laneIds]);
  if (
    entityIds.size !==
      2 + board.user_ids.length + board.priority_ids.length + board.lane_ids.length ||
    !userIds.has(board.default_owner_id) ||
    lanesById.size !== lanes.length ||
    lanes.filter((lane) => lane.role === "completed").length !== 1 ||
    new Set(lanes.map((lane) => lane.position)).size !== lanes.length ||
    cardsById.size !== cards.length ||
    new Set(cards.map((card) => card.ticket)).size !== cards.length ||
    policy.revision > board.revision ||
    !priorityIds.has(policy.default_priority_id) ||
    priorities.some((priority) => priority.revision > board.revision) ||
    lanes.some(
      (lane) =>
        lane.board_id !== board.id || lane.revision > board.revision || !laneIds.has(lane.id),
    )
  ) {
    invalidResponse();
  }
  for (const card of cards) {
    const lane = lanesById.get(card.lane_id);
    if (
      entityIds.has(card.id) ||
      card.board_id !== board.id ||
      card.revision > board.revision ||
      lane === undefined ||
      (lane.role === "completed") !== (card.completed_at !== null) ||
      !userIds.has(card.owner_id) ||
      !priorityIds.has(card.priority_id)
    ) {
      invalidResponse();
    }
    // The other end of a hierarchy link can be an archived card outside this view.
    const parent = card.parent_id === null ? undefined : cardsById.get(card.parent_id);
    if (
      (card.parent_id !== null && (entityIds.has(card.parent_id) || card.parent_id === card.id)) ||
      (parent !== undefined && !parent.child_ids.includes(card.id)) ||
      new Set(card.child_ids).size !== card.child_ids.length ||
      card.child_ids.some((id) => {
        const child = cardsById.get(id);
        return (
          id === card.id ||
          entityIds.has(id) ||
          (child !== undefined && child.parent_id !== card.id)
        );
      })
    ) {
      invalidResponse();
    }
  }
  const humanIds = new Set(users.filter((user) => user.kind === "human").map((user) => user.id));
  if (
    policy.create_grants.some(
      (grant) => !userIds.has(grant.user_id) || !laneIds.has(grant.lane_id),
    ) ||
    policy.move_grants.some(
      (grant) =>
        !userIds.has(grant.user_id) ||
        !laneIds.has(grant.from_lane_id) ||
        !laneIds.has(grant.to_lane_id),
    ) ||
    policy.resource_grants.some(
      (grant) =>
        !userIds.has(grant.user_id) ||
        (grant.capabilities.includes("approve") && !humanIds.has(grant.user_id)),
    ) ||
    new Set(policy.create_grants.map((grant) => `${grant.user_id}:${grant.lane_id}`)).size !==
      policy.create_grants.length ||
    new Set(
      policy.move_grants.map(
        (grant) => `${grant.user_id}:${grant.from_lane_id}:${grant.to_lane_id}`,
      ),
    ).size !== policy.move_grants.length ||
    new Set(policy.resource_grants.map((grant) => grant.user_id)).size !==
      policy.resource_grants.length
  ) {
    invalidResponse();
  }
}

async function readPolicy(
  board: BoardDto,
  signal?: AbortSignal,
): Promise<{ readonly policy: PolicyDto } | { readonly error: unknown }> {
  try {
    const { data } = await readJson(`/policies/${board.policy_id}`, signal);
    if (!isPolicy(data) || data.id !== board.policy_id || data.board_id !== board.id) {
      return invalidResponse();
    }
    return { policy: data };
  } catch (error) {
    const failure = readFailure(error);
    if (failure.kind === "http" && failure.problem.code === "not_found") {
      // A restore can replace the policy UUID while this read is in progress.
      return { error };
    }
    throw error;
  }
}

export async function readBoardSummary(boardId: Uuid, signal?: AbortSignal) {
  const { data, headers } = await readJson(`/boards/${boardId}`, signal);
  if (!isBoard(data) || data.id !== boardId) return invalidResponse();
  return { board: data, etag: entityTag(headers) };
}

async function readBoardAttempt(boardId: Uuid, signal?: AbortSignal): Promise<BoardData | null> {
  const path = `/boards/${boardId}`;
  const response = await readJson(`${path}?view=lanes`, signal);
  const view = parseView(response.data);
  if (view.board.id !== boardId || view.board.availability !== "open") return invalidResponse();
  const boardEtag = entityTag(response.headers);
  const { board } = view;
  const [users, priorities, policyResponse] = await Promise.all([
    readCollection("users", boardId, board.user_ids, isUser, signal),
    readCollection(
      "priorities",
      boardId,
      board.priority_ids,
      (entry): entry is PriorityDto => isPriority(entry) && entry.board_id === boardId,
      signal,
    ),
    readPolicy(board, signal),
  ]);
  // The resource reads have independent validators. Recheck the board revision after all of them.
  const latest = await readBoardSummary(boardId, signal);
  if (latest.etag !== boardEtag || latest.board.revision !== board.revision) return null;
  if ("error" in policyResponse) throw policyResponse.error;
  if (latest.board.availability !== board.availability || users === null || priorities === null) {
    return invalidResponse();
  }
  const result = { view, boardEtag, users, priorities, policy: policyResponse.policy };
  validateReferences(result);
  return result;
}

export async function readBoard(boardId: Uuid, signal?: AbortSignal): Promise<BoardData> {
  const id = boardId.toLowerCase();
  if (!isUuid(id)) return invalidResponse();
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try {
      const result = await readBoardAttempt(id, signal);
      if (result !== null) return result;
    } catch (error) {
      const failure = readFailure(error);
      if (
        attempt !== 0 ||
        failure.kind !== "http" ||
        failure.problem.code !== "invalid_request" ||
        !failure.problem.errors.some((issue) => issue.field === "cursor")
      ) {
        throw error;
      }
    }
  }
  return invalidResponse();
}
