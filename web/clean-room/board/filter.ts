import type { BoardViewDto, CardDto, Uuid } from "../api-types.js";
import {
  invalidResponse,
  isBoundedText,
  isRecord,
  isUnknownArray,
  isUuid,
  readDeadline,
  readFailure,
  readJson,
} from "../http.js";

const pageSize = 200;
const completedWindowMs = 24 * 60 * 60 * 1000;

export interface BoardCardProjection {
  readonly cardsByLane: ReadonlyMap<Uuid, readonly CardDto[]>;
  readonly found: number;
  readonly olderCompleted: number;
}

export function projectCards(
  view: BoardViewDto,
  showCompleted: boolean,
  matching: ReadonlySet<Uuid> | null,
  now: number,
): BoardCardProjection {
  const cutoff = now - completedWindowMs;
  const visibleLanes = new Set(view.lanes.filter((lane) => !lane.retired).map(({ id }) => id));
  const cards: CardDto[] = [];
  let olderCompleted = 0;
  for (const card of view.cards) {
    if (card.archived || !visibleLanes.has(card.lane_id)) continue;
    if (card.completed_at !== null && Date.parse(card.completed_at.replace(" ", "T")) < cutoff) {
      olderCompleted += 1;
      if (!showCompleted) continue;
    }
    if (matching !== null && !matching.has(card.id)) continue;
    cards.push(card);
  }
  return {
    cardsByLane: Map.groupBy(cards, (card) => card.lane_id),
    found: cards.length,
    olderCompleted,
  };
}

export function cardSearchQuery(value: string): string {
  return value.trim();
}

async function readMatchPages(
  boardId: Uuid,
  search: string,
  signal: AbortSignal,
): Promise<ReadonlySet<Uuid>> {
  const query = new URLSearchParams({
    board_id: boardId,
    q: search,
    search_content: "true",
    limit: String(pageSize),
  });
  const matches = new Set<Uuid>();
  const cursors = new Set<string>();
  for (;;) {
    const { data } = await readJson(`/cards?${query}`, signal);
    if (
      !isRecord(data) ||
      Object.keys(data).length !== 2 ||
      !isUnknownArray(data.cards) ||
      data.cards.length > pageSize
    ) {
      return invalidResponse();
    }
    for (const card of data.cards) {
      if (
        !isRecord(card) ||
        !isUuid(card.id) ||
        card.board_id !== boardId ||
        card.archived !== false ||
        matches.has(card.id)
      ) {
        return invalidResponse();
      }
      matches.add(card.id);
    }
    const cursor = data.next_cursor;
    if (cursor === null) return matches;
    if (data.cards.length !== pageSize || !isBoundedText(cursor, 1, 2048) || cursors.has(cursor)) {
      return invalidResponse();
    }
    cursors.add(cursor);
    query.set("cursor", cursor);
  }
}

export async function readCardMatches(
  boardId: Uuid,
  value: string,
  signal?: AbortSignal,
): Promise<ReadonlySet<Uuid>> {
  const search = cardSearchQuery(value);
  if (!isUuid(boardId) || !isBoundedText(search, 1, 1024)) return invalidResponse();
  const deadline = readDeadline(signal);
  try {
    return await readMatchPages(boardId, search, deadline);
  } catch (error) {
    const failure = readFailure(error);
    if (
      failure.kind !== "http" ||
      failure.problem.code !== "invalid_request" ||
      !failure.problem.errors.some((issue) => issue.field === "cursor")
    ) {
      throw error;
    }
    return readMatchPages(boardId, search, deadline);
  }
}
