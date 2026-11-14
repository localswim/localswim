import type { CardDto, EntityTag, MutableEntityDto, Timestamp, Uuid } from "../api-types.js";
import {
  entityTag,
  invalidResponse,
  isBoundedText,
  isRecord,
  isTimestamp,
  isUnknownArray,
  isUuid,
  readDeadline,
  readFailure,
  readJson,
} from "../http.js";
import { readAttachments } from "./attachment-data.js";
import type {
  DrawerActivity,
  DrawerCardLink,
  DrawerComment,
  DrawerData,
  DrawerRelationship,
} from "./contracts.js";

const pageSize = 50;
const linkBatchSize = 8;
const maxProse = 4194304;
const firstAuditTime = "0000-01-01 00:00:00+00:00";
const lastAuditTime = "9999-12-31 23:59:59+00:00";
const auditResourceTypes = new Set<string>(["card", "comment", "attachment", "relationship"]);
const coverageCodes = new Set<string>([
  "complete",
  "writer_mismatch",
  "durable_state_unavailable",
  "startup_gap",
  "interval_open",
  "recovery_gap",
]);

interface BoardAnchor {
  readonly id: Uuid;
  readonly etag: EntityTag;
  readonly revision: number;
  readonly userIds: ReadonlySet<Uuid>;
  readonly laneIds: ReadonlySet<Uuid>;
  readonly priorityIds: ReadonlySet<Uuid>;
}

interface RelationshipRow extends MutableEntityDto {
  readonly board_id: Uuid;
  readonly source_card_id: Uuid;
  readonly target_card_id: Uuid;
  readonly kind: DrawerRelationship["kind"];
}

function integer(value: unknown, minimum = 0): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= minimum;
}

function uuid(value: Uuid): Uuid {
  const normalized = value.toLowerCase();
  return isUuid(normalized) ? normalized : invalidResponse();
}

function isUuidV7(value: unknown): value is string {
  return (
    typeof value === "string" &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(value)
  );
}

function isUniqueIdList(value: unknown): value is readonly Uuid[] {
  return isUnknownArray(value) && value.every(isUuid) && new Set(value).size === value.length;
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

export function isFocusedCard(value: unknown): value is CardDto {
  return (
    isEntity(value) &&
    Object.keys(value).length === 21 &&
    isUuid(value.board_id) &&
    integer(value.ticket, 1) &&
    isBoundedText(value.title, 1, 1024) &&
    /[^\p{White_Space}]/u.test(value.title) &&
    isBoundedText(value.description, 0, maxProse) &&
    isUuid(value.lane_id) &&
    isUuid(value.owner_id) &&
    isUuid(value.priority_id) &&
    (value.parent_id === null || (isUuid(value.parent_id) && value.parent_id !== value.id)) &&
    isUniqueIdList(value.child_ids) &&
    !value.child_ids.includes(value.id) &&
    (value.parent_id === null || !value.child_ids.includes(value.parent_id)) &&
    typeof value.archived === "boolean" &&
    (value.cover_attachment_id === null || isUuid(value.cover_attachment_id)) &&
    integer(value.comment_count) &&
    integer(value.attachment_count) &&
    integer(value.blocking_depth) &&
    integer(value.blocked_card_count) &&
    Number.isSafeInteger(value.triage_score) &&
    (value.completed_at === null || isTimestamp(value.completed_at))
  );
}

function isRelationshipKind(value: unknown): value is RelationshipRow["kind"] {
  return (
    value === "blocks" || value === "duplicates" || value === "references" || value === "relates_to"
  );
}

function isRelationship(value: unknown): value is RelationshipRow {
  return (
    isEntity(value) &&
    Object.keys(value).length === 8 &&
    isUuid(value.board_id) &&
    isUuid(value.source_card_id) &&
    isUuid(value.target_card_id) &&
    value.source_card_id !== value.target_card_id &&
    isRelationshipKind(value.kind)
  );
}

function isComment(value: unknown): value is DrawerComment {
  return (
    isRecord(value) &&
    Object.keys(value).length === 7 &&
    isUuid(value.id) &&
    isUuid(value.board_id) &&
    isUuid(value.card_id) &&
    isUuid(value.author_id) &&
    isTimestamp(value.created_at) &&
    isBoundedText(value.text, 1, maxProse) &&
    integer(value.ticket, 1)
  );
}

function nullableText(value: unknown, max: number): boolean {
  return value === null || isBoundedText(value, 0, max);
}

function isActivity(value: unknown): value is DrawerActivity {
  return (
    isRecord(value) &&
    Object.keys(value).length === (value.bulk_approval_comment === undefined ? 11 : 12) &&
    isUuidV7(value.id) &&
    isUuid(value.board_id) &&
    integer(value.revision, 1) &&
    isTimestamp(value.at) &&
    isUuid(value.actor_id) &&
    typeof value.operation === "string" &&
    /^[a-z][a-z0-9_]{0,79}$/u.test(value.operation) &&
    typeof value.resource_type === "string" &&
    auditResourceTypes.has(value.resource_type) &&
    isUuid(value.resource_id) &&
    nullableText(value.field, 80) &&
    nullableText(value.before, maxProse) &&
    nullableText(value.after, maxProse) &&
    (value.bulk_approval_comment === undefined ||
      (value.operation === "approve" &&
        value.resource_type === "card" &&
        value.field === "lane_id" &&
        isBoundedText(value.bulk_approval_comment, 1, 1024)))
  );
}

async function readBoardAnchor(id: Uuid, signal: AbortSignal): Promise<BoardAnchor> {
  const { data, headers } = await readJson(`/boards/${id}`, signal);
  if (
    !isEntity(data) ||
    data.id !== id ||
    data.availability !== "open" ||
    !isUniqueIdList(data.user_ids) ||
    !isUniqueIdList(data.lane_ids) ||
    !isUniqueIdList(data.priority_ids)
  ) {
    return invalidResponse();
  }
  return {
    id,
    etag: entityTag(headers),
    revision: data.revision,
    userIds: new Set(data.user_ids),
    laneIds: new Set(data.lane_ids),
    priorityIds: new Set(data.priority_ids),
  };
}

export async function readFocusedCard(
  boardId: Uuid,
  cardId: Uuid,
  signal?: AbortSignal,
): Promise<{ readonly card: CardDto; readonly cardEtag: EntityTag }> {
  const board = uuid(boardId);
  const id = uuid(cardId);
  const { data, headers } = await readJson(`/cards/${id}`, signal);
  if (!isFocusedCard(data) || data.id !== id || data.board_id !== board) return invalidResponse();
  return { card: data, cardEtag: entityTag(headers) };
}

function cardLink(card: CardDto): DrawerCardLink {
  const { id, ticket, title, archived } = card;
  return { id, ticket, title, archived };
}

export async function readDrawerTicket(
  boardId: Uuid,
  ticket: number,
  signal?: AbortSignal,
): Promise<DrawerCardLink | null> {
  const id = uuid(boardId);
  if (!integer(ticket, 1)) return invalidResponse();
  const query = new URLSearchParams({
    board_id: id,
    ticket: String(ticket),
    archived: "include",
    limit: "1",
  });
  const { data } = await readJson(`/cards?${query}`, signal);
  if (
    !isRecord(data) ||
    Object.keys(data).length !== 2 ||
    data.next_cursor !== null ||
    !isUnknownArray(data.cards) ||
    data.cards.length > 1
  )
    return invalidResponse();
  if (data.cards.length === 0) return null;
  const card = data.cards[0];
  if (!isFocusedCard(card) || card.board_id !== id || card.ticket !== ticket)
    return invalidResponse();
  return cardLink(card);
}

async function readPages<T extends { readonly id: Uuid }>(
  resource: "comments" | "relationships" | "activities",
  query: URLSearchParams,
  isEntry: (value: unknown) => value is T,
  signal: AbortSignal,
  validatePage?: (value: Record<string, unknown>) => void,
): Promise<readonly T[]> {
  const cursors = new Set<string>();
  const ids = new Set<Uuid>();
  const rows: T[] = [];
  query.set("limit", String(pageSize));
  for (;;) {
    const { data } = await readJson(`/${resource}?${query}`, signal);
    if (!isRecord(data)) return invalidResponse();
    const values = data[resource];
    const cursor = data.next_cursor;
    if (
      Object.keys(data).length !== (resource === "activities" ? 7 : 2) ||
      !isUnknownArray(values) ||
      values.length > pageSize ||
      (cursor !== null &&
        (!isBoundedText(cursor, 1, 2048) || cursors.has(cursor) || values.length !== pageSize))
    ) {
      return invalidResponse();
    }
    validatePage?.(data);
    for (const value of values) {
      if (!isEntry(value) || ids.has(value.id)) return invalidResponse();
      ids.add(value.id);
      rows.push(value);
    }
    if (cursor === null) return rows;
    cursors.add(cursor);
    query.set("cursor", cursor);
  }
}

async function readActivity(
  anchor: BoardAnchor,
  cardId: Uuid,
  signal: AbortSignal,
): Promise<readonly DrawerActivity[]> {
  // Read the full supported timestamp range. The reported writer coverage can be partial
  // while the full view still returns the board's currently admitted audit.
  const query = new URLSearchParams({
    board_id: anchor.id,
    card_id: cardId,
    view: "full",
    from: firstAuditTime,
    to: lastAuditTime,
  });
  let writer: string | undefined;
  return readPages("activities", query, isActivity, signal, (data) => {
    const { coverage, writer_id: writerId } = data;
    if (
      !isUuidV7(writerId) ||
      (writer !== undefined && writer !== writerId) ||
      data.board_revision !== anchor.revision ||
      data.pagination_complete !== (data.next_cursor === null) ||
      data.bot_only !== false ||
      !isRecord(coverage) ||
      coverage.from !== firstAuditTime ||
      coverage.to !== lastAuditTime ||
      typeof coverage.code !== "string" ||
      !coverageCodes.has(coverage.code) ||
      coverage.code === "durable_state_unavailable" ||
      coverage.code === "writer_mismatch" ||
      coverage.complete !== (coverage.code === "complete")
    )
      invalidResponse();
    writer = writerId;
  });
}

function otherCardId(row: RelationshipRow, cardId: Uuid): Uuid {
  if (row.source_card_id === cardId) return row.target_card_id;
  if (row.target_card_id === cardId) return row.source_card_id;
  return invalidResponse();
}

async function readLinkedCards(
  card: CardDto,
  relationships: readonly RelationshipRow[],
  signal: AbortSignal,
): Promise<ReadonlyMap<Uuid, CardDto>> {
  const ids = new Set(card.child_ids);
  if (card.parent_id !== null) ids.add(card.parent_id);
  for (const row of relationships) {
    if (row.board_id !== card.board_id) return invalidResponse();
    ids.add(otherCardId(row, card.id));
  }
  const targets = [...ids];
  const linked = new Map<Uuid, CardDto>();
  for (let start = 0; start < targets.length; start += linkBatchSize) {
    const batch = targets.slice(start, start + linkBatchSize);
    const values = await Promise.all(batch.map((id) => readFocusedCard(card.board_id, id, signal)));
    for (const value of values) linked.set(value.card.id, value.card);
  }
  return linked;
}

function linkedCard(cards: ReadonlyMap<Uuid, CardDto>, id: Uuid): CardDto {
  return cards.get(id) ?? invalidResponse();
}

function validateCards(
  anchor: BoardAnchor,
  card: CardDto,
  linked: ReadonlyMap<Uuid, CardDto>,
): void {
  const tickets = new Set<number>();
  for (const item of [card, ...linked.values()]) {
    if (
      item.revision > anchor.revision ||
      tickets.has(item.ticket) ||
      !anchor.userIds.has(item.owner_id) ||
      !anchor.laneIds.has(item.lane_id) ||
      !anchor.priorityIds.has(item.priority_id)
    )
      invalidResponse();
    tickets.add(item.ticket);
  }
  if (
    (card.parent_id !== null && !linkedCard(linked, card.parent_id).child_ids.includes(card.id)) ||
    card.child_ids.some((id) => linkedCard(linked, id).parent_id !== card.id)
  )
    invalidResponse();
}

function validateComments(
  anchor: BoardAnchor,
  card: CardDto,
  comments: readonly DrawerComment[],
): void {
  if (comments.length !== card.comment_count) invalidResponse();
  let previousAt: Timestamp | undefined;
  for (const comment of comments) {
    if (
      comment.board_id !== anchor.id ||
      comment.card_id !== card.id ||
      comment.ticket !== card.ticket ||
      !anchor.userIds.has(comment.author_id) ||
      (previousAt !== undefined && comment.created_at > previousAt)
    )
      invalidResponse();
    previousAt = comment.created_at;
  }
}

function auditObject(text: string | null): Record<string, unknown> {
  try {
    if (text === null) return invalidResponse();
    const data: unknown = JSON.parse(text);
    if (isRecord(data)) return data;
  } catch {
    return invalidResponse();
  }
  return invalidResponse();
}

function validateActivity(
  anchor: BoardAnchor,
  card: CardDto,
  comments: readonly DrawerComment[],
  activity: readonly DrawerActivity[],
): void {
  const commentIds = new Set(comments.map((comment) => comment.id));
  const attachments = new Set<Uuid>();
  let previousRevision = 0;
  for (const event of activity) {
    if (
      event.board_id !== anchor.id ||
      event.revision > anchor.revision ||
      event.revision < previousRevision ||
      !anchor.userIds.has(event.actor_id)
    )
      invalidResponse();
    previousRevision = event.revision;
    if (event.resource_type === "card" && event.resource_id !== card.id) invalidResponse();
    if (event.resource_type === "comment" && !commentIds.has(event.resource_id)) invalidResponse();
    if (event.resource_type === "relationship") {
      const endpoints = auditObject(event.after ?? event.before);
      if (
        !isUuid(endpoints.source_card_id) ||
        !isUuid(endpoints.target_card_id) ||
        endpoints.source_card_id === endpoints.target_card_id ||
        (endpoints.source_card_id !== card.id && endpoints.target_card_id !== card.id) ||
        !isRelationshipKind(endpoints.kind)
      )
        invalidResponse();
    }
    if (event.resource_type === "attachment" && event.operation === "attach") {
      const attachment = auditObject(event.after);
      if (
        attachment.id !== event.resource_id ||
        attachment.board_id !== anchor.id ||
        attachment.card_id !== card.id
      ) {
        invalidResponse();
      }
      attachments.add(event.resource_id);
    }
    if (event.resource_type === "attachment" && !attachments.has(event.resource_id))
      invalidResponse();
  }
}

function relationshipLinks(
  anchor: BoardAnchor,
  card: CardDto,
  linked: ReadonlyMap<Uuid, CardDto>,
  rows: readonly RelationshipRow[],
): readonly DrawerRelationship[] {
  const keys = new Set<string>();
  return rows.map((row) => {
    const other = otherCardId(row, card.id);
    const direction = row.source_card_id === card.id ? "outgoing" : "incoming";
    const key = `${row.kind}:${row.kind === "relates_to" ? "" : direction}:${other}`;
    if (row.revision > anchor.revision || keys.has(key)) return invalidResponse();
    keys.add(key);
    return { id: row.id, kind: row.kind, direction, card: cardLink(linkedCard(linked, other)) };
  });
}

function changed(before: BoardAnchor, after: BoardAnchor): boolean {
  return before.revision !== after.revision || before.etag !== after.etag;
}

async function readDrawerAttempt(
  anchor: BoardAnchor,
  cardId: Uuid,
  signal: AbortSignal,
): Promise<{ readonly drawer: DrawerData } | { readonly nextAnchor: BoardAnchor }> {
  const { card, cardEtag } = await readFocusedCard(anchor.id, cardId, signal);
  const query = { board_id: anchor.id, card_id: card.id };
  const [comments, rows, activity, attachments] = await Promise.all([
    readPages(
      "comments",
      new URLSearchParams({ ...query, archived: "include" }),
      isComment,
      signal,
    ),
    readPages("relationships", new URLSearchParams(query), isRelationship, signal),
    readActivity(anchor, cardId, signal),
    card.attachment_count === 0
      ? Promise.resolve([])
      : readAttachments(card, anchor.revision, signal),
  ]);
  const linked = await readLinkedCards(card, rows, signal);
  const latest = await readBoardAnchor(anchor.id, signal);
  if (changed(anchor, latest)) return { nextAnchor: latest };
  validateCards(anchor, card, linked);
  validateComments(anchor, card, comments);
  validateActivity(anchor, card, comments, activity);
  if (attachments.some((attachment) => !anchor.userIds.has(attachment.added_by)))
    return invalidResponse();
  return {
    drawer: {
      card,
      cardEtag,
      boardRevision: anchor.revision,
      boardEtag: anchor.etag,
      parent: card.parent_id === null ? null : cardLink(linkedCard(linked, card.parent_id)),
      children: card.child_ids.map((id) => cardLink(linkedCard(linked, id))),
      relationships: relationshipLinks(anchor, card, linked, rows),
      comments,
      activity,
      attachments,
    },
  };
}

export async function readDrawer(
  boardId: Uuid,
  cardId: Uuid,
  signal?: AbortSignal,
): Promise<DrawerData> {
  const board = uuid(boardId);
  const id = uuid(cardId);
  const deadline = readDeadline(signal);
  let anchor = await readBoardAnchor(board, deadline);
  for (let attempt = 0; attempt < 2; attempt += 1) {
    const controller = new AbortController();
    const attemptSignal = AbortSignal.any([controller.signal, deadline]);
    try {
      const result = await readDrawerAttempt(anchor, id, attemptSignal);
      if ("drawer" in result) return result.drawer;
      anchor = result.nextAnchor;
    } catch (error) {
      controller.abort();
      const failure = readFailure(error);
      const staleCandidate =
        failure.kind === "invalid_response" ||
        (failure.kind === "http" &&
          (failure.problem.code === "not_found" ||
            (failure.problem.code === "invalid_request" &&
              failure.problem.errors.some((issue) => issue.field === "cursor"))));
      if (attempt !== 0 || !staleCandidate) throw error;
      const latest = await readBoardAnchor(board, deadline);
      if (!changed(anchor, latest)) throw error;
      anchor = latest;
    } finally {
      controller.abort();
    }
  }
  return invalidResponse();
}
