export type CardSelection = Readonly<{ id: string; ticket?: number } | { ticket: number }>;

export type DeeperSearchState = Readonly<{
  open: boolean;
  query: string;
  content: boolean;
  order: "triage" | "ticket";
  laneId: string;
  limit: string;
  archive: "active" | "include" | "only";
  submitted: boolean;
}>;

export type BoardViewState = Readonly<{
  filter: string;
  selection: CardSelection | null;
  horizontalScroll: number;
  laneScroll: Readonly<Record<string, number>>;
  deeperSearch: string;
  deeperSearchState?: DeeperSearchState;
}>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const storagePrefix = "localswim:ui:board-view:";
export const maxDeeperSearchLength = 2048;

function fields(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function ticketNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0 ? value : undefined;
}

function scrollOffset(value: unknown): number | undefined {
  return typeof value === "number" && Math.abs(value) <= Number.MAX_SAFE_INTEGER
    ? value
    : undefined;
}

function selection(value: unknown): CardSelection | null {
  const { id, ticket } = fields(value);
  const number = ticketNumber(ticket);
  if (id !== undefined) {
    if (typeof id !== "string" || !uuid.test(id)) return null;
    const canonicalId = id.toLowerCase();
    return number === undefined ? { id: canonicalId } : { id: canonicalId, ticket: number };
  }
  return number === undefined ? null : { ticket: number };
}

function deeperSearchState(value: unknown): DeeperSearchState | undefined {
  const { open, query, content, order, laneId, limit, archive, submitted } = fields(value);
  if (
    typeof open !== "boolean" ||
    typeof query !== "string" ||
    query.length > maxDeeperSearchLength ||
    typeof content !== "boolean" ||
    (order !== "triage" && order !== "ticket") ||
    typeof laneId !== "string" ||
    (laneId !== "" && !uuid.test(laneId)) ||
    typeof limit !== "string" ||
    limit.length > 32 ||
    (archive !== "active" && archive !== "include" && archive !== "only") ||
    typeof submitted !== "boolean"
  ) {
    return undefined;
  }
  return { open, query, content, order, laneId: laneId.toLowerCase(), limit, archive, submitted };
}

function decode(value: unknown): BoardViewState {
  const saved = fields(value);
  const search = deeperSearchState(saved.deeperSearchState);
  const laneScroll: Record<string, number> = {};
  for (const [id, value] of Object.entries(fields(saved.laneScroll))) {
    const offset = scrollOffset(value);
    if (uuid.test(id) && offset !== undefined) laneScroll[id.toLowerCase()] = offset;
  }
  return {
    filter: typeof saved.filter === "string" ? saved.filter : "",
    selection: selection(saved.selection),
    horizontalScroll: scrollOffset(saved.horizontalScroll) ?? 0,
    laneScroll,
    deeperSearch:
      typeof saved.deeperSearch === "string" && saved.deeperSearch.length <= maxDeeperSearchLength
        ? saved.deeperSearch
        : "",
    ...(search ? { deeperSearchState: search } : {}),
  };
}

export function linkedCard(search: string): CardSelection | null {
  const params = new URLSearchParams(search);
  const id = params.get("card");
  if (id !== null && uuid.test(id)) return { id: id.toLowerCase() };
  const ticket = params.get("ticket");
  const number =
    ticket !== null && /^[1-9]\d*$/.test(ticket) ? ticketNumber(Number(ticket)) : undefined;
  return number === undefined ? null : { ticket: number };
}

export function retainActiveLaneScroll(
  laneScroll: BoardViewState["laneScroll"],
  activeLaneIds: Iterable<string>,
): BoardViewState["laneScroll"] {
  const active: Record<string, number> = {};
  for (const id of activeLaneIds) {
    const offset = laneScroll[id];
    if (offset !== undefined) active[id] = offset;
  }
  return active;
}

export function createViewState(view: Pick<Window, "sessionStorage">) {
  function read(boardId: string): BoardViewState | null {
    if (!uuid.test(boardId)) return null;
    try {
      const raw = view.sessionStorage.getItem(storagePrefix + boardId.toLowerCase());
      if (raw === null) return null;
      const saved: unknown = JSON.parse(raw);
      return decode(saved);
    } catch {
      return null;
    }
  }

  return {
    read,
    load: (boardId: string): BoardViewState => read(boardId) ?? decode(null),
    save(boardId: string, state: BoardViewState): void {
      if (!uuid.test(boardId)) return;
      try {
        view.sessionStorage.setItem(
          storagePrefix + boardId.toLowerCase(),
          JSON.stringify(decode(state)),
        );
      } catch {
        // Keep the live view usable when tab storage is disabled or full.
      }
    },
  };
}
