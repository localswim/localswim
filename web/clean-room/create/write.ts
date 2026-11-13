import type { CardDto, SessionDto, Uuid } from "../api-types.js";
import { isCard } from "../board/read.js";
import type { InspectionResult, MutationResult } from "../contracts.js";
import {
  apiPrefix,
  entityTag,
  invalidResponse,
  isBoundedText,
  isRecord,
  isUnknownArray,
  isUuid,
  mutateJson,
  readDeadline,
  readJson,
} from "../http.js";
import type { CreateInput, CreateReceipt } from "./contracts.js";

const pageSize = 200;

function relationshipSeeds(cardId: Uuid, relationships: CreateInput["relationships"]) {
  return relationships.map(({ kind, targetCardId, direction }) => ({
    kind,
    source_card_id: direction === "incoming" ? targetCardId : cardId,
    target_card_id: direction === "incoming" ? cardId : targetCardId,
  }));
}

function hasValidPriority(card: CardDto, input: CreateInput): boolean {
  const requested = input.priorities.find((priority) => priority.id === input.priorityId);
  const effective = input.priorities.find((priority) => priority.id === card.priority_id);
  return (
    requested !== undefined &&
    effective !== undefined &&
    (effective.id === requested.id ||
      (effective.rank < requested.rank &&
        !input.completed &&
        input.relationships.some(
          ({ kind, direction }) => kind === "blocks" && direction === "outgoing",
        )))
  );
}

function parseCreatedCard(data: unknown, headers: Headers, input: CreateInput): CreateReceipt {
  if (
    !isCard(data) ||
    data.id !== input.cardId ||
    data.board_id !== input.boardId ||
    data.revision !== input.boardRevision + 1 ||
    data.title !== input.title ||
    data.description !== input.description ||
    data.lane_id !== input.laneId ||
    data.owner_id !== input.ownerId ||
    !hasValidPriority(data, input) ||
    data.parent_id !== null ||
    data.child_ids.length !== 0 ||
    data.cover_attachment_id !== null ||
    data.comment_count !== 0 ||
    data.attachment_count !== 0 ||
    data.updated_at !== data.created_at ||
    data.completed_at !== (input.completed ? data.created_at : null)
  ) {
    return invalidResponse();
  }
  return { card: data, cardEtag: entityTag(headers) };
}

export function createCard(
  input: CreateInput,
  signal?: AbortSignal,
  beforeDispatch?: (session: SessionDto) => void,
): Promise<MutationResult<CreateReceipt>> {
  return mutateJson("/cards", {
    method: "POST",
    successStatus: 201,
    requestId: input.requestId,
    body: {
      id: input.cardId,
      board_id: input.boardId,
      expected_board_etag: input.boardEtag,
      title: input.title,
      description: input.description,
      lane_id: input.laneId,
      owner_id: input.ownerId,
      priority_id: input.priorityId,
      relationships: relationshipSeeds(input.cardId, input.relationships),
    },
    validate(data, headers) {
      const receipt = parseCreatedCard(data, headers, input);
      const boardTag = entityTag(headers, "X-Board-ETag");
      if (
        headers.get("Location") !== `${apiPrefix}/cards/${input.cardId}` ||
        boardTag === input.boardEtag
      ) {
        return invalidResponse();
      }
      return receipt;
    },
    ...(signal === undefined ? {} : { signal }),
    ...(beforeDispatch === undefined ? {} : { beforeDispatch }),
  });
}

function seedKey(kind: unknown, source: unknown, target: unknown): string {
  if (typeof kind !== "string" || !isUuid(source) || !isUuid(target)) {
    return invalidResponse();
  }
  return `${kind}:${source}:${target}`;
}

function checkRelationship(
  row: unknown,
  card: CardDto,
  ids: Set<string>,
  expected: Set<string>,
): void {
  if (
    !isRecord(row) ||
    !isUuid(row.id) ||
    ids.has(row.id) ||
    row.board_id !== card.board_id ||
    row.revision !== card.revision ||
    row.created_at !== card.created_at ||
    row.updated_at !== card.created_at ||
    !expected.delete(seedKey(row.kind, row.source_card_id, row.target_card_id))
  ) {
    return invalidResponse();
  }
  ids.add(row.id);
}

async function checkRelationships(
  card: CardDto,
  relationships: CreateInput["relationships"],
  signal: AbortSignal,
): Promise<void> {
  const expected = new Set(
    relationshipSeeds(card.id, relationships).map(({ kind, source_card_id, target_card_id }) =>
      seedKey(kind, source_card_id, target_card_id),
    ),
  );
  if (expected.size !== relationships.length) return invalidResponse();
  const query = new URLSearchParams({
    board_id: card.board_id,
    card_id: card.id,
    limit: String(pageSize),
  });
  const ids = new Set<string>();
  const cursors = new Set<string>();
  const pageCount = Math.max(1, Math.ceil(expected.size / pageSize));
  for (let page = 0; page < pageCount; page += 1) {
    const { data } = await readJson(`/relationships?${query}`, signal);
    if (
      !isRecord(data) ||
      !isUnknownArray(data.relationships) ||
      data.relationships.length !== Math.min(pageSize, expected.size)
    ) {
      return invalidResponse();
    }
    for (const row of data.relationships) {
      checkRelationship(row, card, ids, expected);
    }
    const cursor = data.next_cursor;
    if (expected.size === 0) {
      if (cursor !== null) return invalidResponse();
      return;
    }
    if (!isBoundedText(cursor, 1, 2048) || cursors.has(cursor)) return invalidResponse();
    cursors.add(cursor);
    query.set("cursor", cursor);
  }
  return invalidResponse();
}

async function checkPriorityPromotion(
  card: CardDto,
  requestedPriority: Uuid,
  signal: AbortSignal,
): Promise<void> {
  if (card.priority_id === requestedPriority) return;
  const query = new URLSearchParams({
    board_id: card.board_id,
    card_id: card.id,
    operation: "promote",
    view: "full",
    from: card.created_at,
    to: card.created_at,
    limit: String(pageSize),
  });
  const { data } = await readJson(`/activities?${query}`, signal);
  if (
    !isRecord(data) ||
    !isUnknownArray(data.activities) ||
    data.activities.length > pageSize ||
    data.next_cursor !== null ||
    data.pagination_complete !== true
  ) {
    return invalidResponse();
  }
  const matches = data.activities.filter(
    (event) =>
      isRecord(event) &&
      typeof event.id === "string" &&
      /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(event.id) &&
      isUuid(event.actor_id) &&
      event.board_id === card.board_id &&
      event.resource_type === "card" &&
      event.resource_id === card.id &&
      event.operation === "promote" &&
      event.revision === card.revision &&
      event.at === card.created_at &&
      event.field === "priority_id" &&
      event.before === requestedPriority &&
      event.after === card.priority_id,
  );
  if (matches.length !== 1) return invalidResponse();
}

export async function inspectCardCreation(
  input: CreateInput,
  signal?: AbortSignal,
): Promise<InspectionResult<CreateReceipt>> {
  const deadline = readDeadline(signal);
  try {
    const { data, headers } = await readJson(`/cards/${input.cardId}`, deadline);
    const receipt = parseCreatedCard(data, headers, input);
    await checkRelationships(receipt.card, input.relationships, deadline);
    await checkPriorityPromotion(receipt.card, input.priorityId, deadline);
    return { outcome: "committed", value: receipt };
  } catch {
    // A missing card can still be in flight or fenced after an uncertain durable write.
    return { outcome: "unknown" };
  }
}
