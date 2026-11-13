import type { ActorKind, BoardDto, UuidV7 } from "../api-types.js";
import {
  isBoundedText,
  isRecord,
  isUnknownArray,
  isUuid,
  readDeadline,
  readJson,
} from "../http.js";

const pageSize = 200;
const pageLimit = 4;
const cardOperations = new Set([
  "Created",
  "Contents modified",
  "Comment added",
  "Ticket numbers reordered",
  "Moved lanes",
  "Reviewed & approved",
  "Archived",
  "Restored",
]);

function isPositiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function isActivityCard(value: unknown): boolean {
  return (
    isRecord(value) &&
    Object.keys(value).length === 2 &&
    isUuid(value.card_id) &&
    isPositiveInteger(value.ticket)
  );
}

// Attribute a confirmed edit using only committed metadata at the exact revision.
// An open wall-clock interval still contains usable committed rows.
export async function readLiveActor(
  previous: BoardDto,
  current: BoardDto,
  writerId: UuidV7,
  signal: AbortSignal,
): Promise<ActorKind | null> {
  const at = current.last_card_edit_at;
  if (
    previous.id !== current.id ||
    previous.revision >= current.revision ||
    at === null ||
    at !== current.updated_at ||
    signal.aborted
  ) {
    return null;
  }

  const deadline = readDeadline(signal);
  const query = new URLSearchParams({
    board_id: current.id,
    from: at,
    to: at,
    view: "monitor",
    writer_id: writerId,
    limit: String(pageSize),
  });
  const cursors = new Set<string>();
  let actor: ActorKind | null = null;
  let revision = 0;

  try {
    for (let page = 0; page < pageLimit; page += 1) {
      const { data } = await readJson(`/activities?${query}`, deadline);
      if (
        deadline.aborted ||
        !isRecord(data) ||
        Object.keys(data).length !== 7 ||
        data.writer_id !== writerId ||
        data.board_revision !== current.revision ||
        !isRecord(data.coverage) ||
        Object.keys(data.coverage).length !== 4 ||
        data.coverage.from !== at ||
        data.coverage.to !== at ||
        !(
          (data.coverage.code === "complete" && data.coverage.complete === true) ||
          (data.coverage.code === "interval_open" && data.coverage.complete === false)
        ) ||
        typeof data.bot_only !== "boolean" ||
        !isUnknownArray(data.activities) ||
        data.activities.length > pageSize
      ) {
        return null;
      }
      for (const row of data.activities) {
        if (
          !isRecord(row) ||
          Object.keys(row).length !== 10 ||
          row.board_id !== current.id ||
          row.at !== at ||
          !isPositiveInteger(row.revision) ||
          row.revision < revision ||
          row.revision > current.revision ||
          typeof row.id !== "string" ||
          !/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(row.id) ||
          (row.actor_kind !== "human" && row.actor_kind !== "bot") ||
          !isUuid(row.actor_id) ||
          typeof row.operation !== "string" ||
          !/^[a-z][a-z0-9_]{0,79}$/u.test(row.operation) ||
          !isUnknownArray(row.cards) ||
          row.cards.length > 2 ||
          !row.cards.every(isActivityCard) ||
          !isUnknownArray(row.tickets) ||
          row.tickets.length > 2 ||
          !row.tickets.every(isPositiveInteger)
        ) {
          return null;
        }
        revision = row.revision;
        if (revision !== current.revision || row.operation_group === null) continue;
        if (
          typeof row.operation_group !== "string" ||
          !cardOperations.has(row.operation_group) ||
          row.cards.length === 0 ||
          row.tickets.length === 0 ||
          (actor !== null && actor !== row.actor_kind)
        ) {
          return null;
        }
        actor = row.actor_kind;
      }
      const cursor = data.next_cursor;
      if (cursor === null && data.pagination_complete === true) return actor;
      if (
        data.pagination_complete !== false ||
        !isBoundedText(cursor, 1, 2_048) ||
        cursors.has(cursor) ||
        data.activities.length === 0
      ) {
        return null;
      }
      cursors.add(cursor);
      query.set("cursor", cursor);
    }
  } catch {
    return null;
  }
  return null;
}
