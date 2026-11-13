export type CardSelection = Readonly<{ id: string; ticket?: number } | { ticket: number }>;

export type BoardViewState = Readonly<{
  filter: string;
  selection: CardSelection | null;
  horizontalScroll: number;
  laneScroll: Readonly<Record<string, number>>;
  deeperSearch: string;
}>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const storagePrefix = "localswim:ui:board-view:";
const maxDeeperSearchLength = 2048;

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

function decode(value: unknown): BoardViewState {
  const saved = fields(value);
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
  return {
    load(boardId: string): BoardViewState {
      if (uuid.test(boardId)) {
        try {
          const raw = view.sessionStorage.getItem(storagePrefix + boardId.toLowerCase());
          const saved: unknown = raw === null ? null : JSON.parse(raw);
          return decode(saved);
        } catch {
          // Navigation can continue when tab storage or its contents are unavailable.
        }
      }
      return decode(null);
    },
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
