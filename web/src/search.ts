import { ApiError } from "../api.mjs";
import type { AppContext } from "./model.js";
import { maxDeeperSearchLength, type DeeperSearchState } from "./view-state.js";

type SearchLane = Readonly<{ id: string; label: string }>;
type SearchResult = Readonly<{ id: string; ticket: number; title: string; archived: boolean }>;
type SearchOptions = AppContext &
  Readonly<{
    boardId: string;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    onOpenCard: (reference: { id: string }, opener: HTMLButtonElement) => void;
    onViewChange: (state: DeeperSearchState) => void;
  }>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const maxPageSize = 200;
// Match the service's query whitespace handling, including ASCII information separators.
const searchText = /[^\p{White_Space}\u001c-\u001f]/u;

function result(value: unknown, boardId: string): value is SearchResult {
  return (
    typeof value === "object" &&
    value !== null &&
    "board_id" in value &&
    value.board_id === boardId &&
    "id" in value &&
    typeof value.id === "string" &&
    uuid.test(value.id) &&
    "ticket" in value &&
    typeof value.ticket === "number" &&
    Number.isSafeInteger(value.ticket) &&
    value.ticket > 0 &&
    "title" in value &&
    typeof value.title === "string" &&
    "archived" in value &&
    typeof value.archived === "boolean"
  );
}

function readResults(
  data: object,
  boardId: string,
  limit: number,
  archive: DeeperSearchState["archive"],
) {
  const { cards, next_cursor: next } = data as Record<string, unknown>;
  if (
    !Array.isArray(cards) ||
    cards.length > limit ||
    (next !== null && (typeof next !== "string" || !next))
  ) {
    throw new ApiError("invalid_response");
  }
  const ids = new Set<string>();
  const matches: SearchResult[] = [];
  for (const card of cards as unknown[]) {
    if (
      !result(card, boardId) ||
      ids.has(card.id) ||
      (archive !== "include" && card.archived !== (archive === "only"))
    ) {
      throw new ApiError("invalid_response");
    }
    matches.push(card);
    ids.add(card.id);
  }
  return { matches, next };
}

export function mountCardSearch(
  host: HTMLElement,
  { api, signal: ownerSignal, boardId, readWithSession, onOpenCard, onViewChange }: SearchOptions,
) {
  const doc = host.ownerDocument;
  const lifetime = new AbortController();
  const signal = AbortSignal.any([ownerSignal, lifetime.signal]);
  const panel = doc.createElement("details");
  panel.className = "board-card-search";
  const heading = doc.createElement("summary");
  heading.textContent = "Find cards";
  const form = doc.createElement("form");
  const query = doc.createElement("input");
  query.type = "search";
  query.maxLength = maxDeeperSearchLength;
  const content = doc.createElement("input");
  content.type = "checkbox";
  const order = doc.createElement("select");
  const lane = doc.createElement("select");
  const limit = doc.createElement("input");
  const limitLabel = doc.createElement("span");
  limitLabel.textContent = "Maximum results";
  limit.type = "number";
  limit.min = "1";
  limit.max = String(maxPageSize);
  limit.required = true;
  limit.value = "20";
  const archive = doc.createElement("select");
  const submit = doc.createElement("button");
  submit.textContent = "Search";
  submit.type = "submit";
  const status = doc.createElement("p");
  status.setAttribute("role", "status");
  status.hidden = true;
  const results = doc.createElement("ul");
  results.hidden = true;
  const loadMore = doc.createElement("button");
  loadMore.type = "button";
  loadMore.textContent = "Load more";
  loadMore.hidden = true;

  function option(select: HTMLSelectElement, value: string, label: string) {
    const node = doc.createElement("option");
    node.value = value;
    node.textContent = label;
    select.append(node);
  }

  function field(label: string | HTMLElement, control: HTMLElement) {
    const wrapper = doc.createElement("label");
    wrapper.append(label, control);
    return wrapper;
  }

  option(order, "triage", "Next work");
  option(order, "ticket", "Ticket number");
  option(archive, "active", "Active cards");
  option(archive, "include", "Active and archived cards");
  option(archive, "only", "Archived cards");
  option(lane, "", "All lanes");
  form.append(
    field("Title or ticket", query),
    field("Search descriptions and comments", content),
    field("Result order", order),
    field("Search lane", lane),
    field("Card visibility", archive),
    field(limitLabel, limit),
    submit,
  );
  panel.append(heading, form, status, results, loadMore);
  host.append(panel);

  let lanes: readonly SearchLane[] = [];
  let revision: number | undefined;
  let request: AbortController | undefined;
  let continuation: { fields: DeeperSearchState; cursor: string } | undefined;
  const seenIds = new Set<string>();
  const seenCursors = new Set<string>();
  let submitted = false;
  let restorePending = false;
  let rememberedOpen = false;

  function setStatus(message: string, stale = false) {
    status.textContent = message;
    status.hidden = message === "";
    panel.dataset.stale = String(stale);
  }

  function readControls(): DeeperSearchState {
    return {
      open: panel.open,
      query: query.value,
      content: content.checked,
      order: order.value === "ticket" ? "ticket" : "triage",
      laneId: lane.value,
      limit: limit.value,
      archive: archive.value === "include" || archive.value === "only" ? archive.value : "active",
      submitted,
    };
  }

  function retain() {
    if (!signal.aborted) onViewChange(readControls());
  }

  function setLanes(selected: string, awaitingBoard = false) {
    lane.replaceChildren();
    option(lane, "", "All lanes");
    for (const { id, label } of lanes) option(lane, id, label);
    if (!selected || lanes.some(({ id }) => id === selected)) {
      lane.value = selected;
    } else if (awaitingBoard) {
      option(lane, selected, "Saved lane");
      lane.value = selected;
    } else {
      lane.value = "";
    }
  }

  function resetSearch() {
    request?.abort();
    request = undefined;
    continuation = undefined;
    seenIds.clear();
    seenCursors.clear();
    if (doc.activeElement === loadMore) submit.focus({ preventScroll: true });
    loadMore.hidden = true;
    loadMore.removeAttribute("aria-disabled");
  }

  function markStale() {
    resetSearch();
    if (submitted || !results.hidden)
      setStatus("Search results are out of date. Search again.", true);
  }

  function updateLimitLabel() {
    limitLabel.textContent = archive.value === "only" ? "Results per page" : "Maximum results";
  }

  function paintResults(matches: SearchResult[], append = false) {
    const focused = doc.activeElement;
    const items = matches.map(({ id, ticket, title, archived }) => {
      const item = doc.createElement("li");
      const opener = doc.createElement("button");
      opener.type = "button";
      opener.dataset.cardId = id;
      opener.textContent = `#${ticket} ${title}${archived ? " (archived)" : ""}`;
      item.append(opener);
      return item;
    });
    if (append) {
      results.append(...items);
      if (focused === loadMore) items[0]?.querySelector("button")?.focus();
      return;
    }
    const focusedId =
      focused && results.contains(focused) ? focused.getAttribute("data-card-id") : null;
    results.replaceChildren(...items);
    results.hidden = matches.length === 0;
    if (focusedId !== null) {
      const replacement = Array.from(results.querySelectorAll("button")).find(
        (button) => button.dataset.cardId === focusedId,
      );
      (replacement ?? submit).focus({ preventScroll: true });
    }
  }

  async function search(fields = readControls(), cursor?: string) {
    const count = Number(fields.limit);
    if (
      !fields.query.isWellFormed() ||
      (searchText.test(fields.query) && Array.from(fields.query).length > 1024)
    ) {
      setStatus("Enter up to 1,024 characters to search.", submitted);
      return;
    }
    if (!Number.isSafeInteger(count) || count < 1 || count > maxPageSize) return;
    if (fields.laneId && !lanes.some(({ id }) => id === fields.laneId)) {
      setStatus("Choose an active lane or All lanes.", submitted);
      return;
    }
    if (cursor === undefined) resetSearch();
    request = new AbortController();
    const current = request;
    const readSignal = AbortSignal.any([signal, current.signal]);
    if (cursor === undefined) {
      submitted = true;
      retain();
      setStatus("Searching cards…", !results.hidden);
    } else {
      loadMore.setAttribute("aria-disabled", "true");
      setStatus("Loading more archived cards…");
    }
    try {
      const { data } = await readWithSession(
        () =>
          api.listCards(
            boardId,
            {
              q: searchText.test(fields.query) ? fields.query : undefined,
              searchContent: fields.content,
              sort: fields.order,
              laneId: fields.laneId || undefined,
              limit: count,
              cursor,
              archived: fields.archive === "active" ? undefined : fields.archive,
            },
            readSignal,
          ),
        readSignal,
      );
      if (readSignal.aborted) return;
      const { matches, next } = readResults(data, boardId, count, fields.archive);
      if (fields.archive === "only" && matches.length > 0) {
        if ((next && seenCursors.has(next)) || matches.some(({ id }) => seenIds.has(id))) {
          throw new ApiError("invalid_response");
        }
        paintResults(matches, cursor !== undefined);
        for (const { id } of matches) seenIds.add(id);
        if (next) seenCursors.add(next);
        continuation = next ? { fields, cursor: next } : undefined;
        loadMore.textContent = "Load more";
        loadMore.hidden = next === null;
        setStatus(`Showing ${next ? "" : "all "}${seenIds.size} matching archived cards.`);
        return;
      }
      if (fields.archive === "only" && (cursor !== undefined || next !== null)) {
        throw new ApiError("invalid_response");
      }
      paintResults(matches);
      setStatus(
        matches.length === 0
          ? "No matching cards."
          : next !== null
            ? `Showing the first ${matches.length} matching cards.`
            : "",
      );
    } catch {
      if (readSignal.aborted) return;
      if (cursor !== undefined) {
        loadMore.textContent = "Retry loading more";
        setStatus("More archived cards could not be read. Retry loading more or search again.");
      } else {
        setStatus("Cards could not be read. Search again.", !results.hidden);
      }
    } finally {
      if (request === current) {
        request = undefined;
        loadMore.removeAttribute("aria-disabled");
      }
    }
  }

  form.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      restorePending = false;
      void search();
    },
    { signal },
  );
  form.addEventListener(
    "input",
    () => {
      restorePending = false;
      markStale();
      updateLimitLabel();
      submitted = false;
      retain();
    },
    { signal },
  );
  panel.addEventListener(
    "toggle",
    () => {
      if (panel.open === rememberedOpen) return;
      rememberedOpen = panel.open;
      retain();
    },
    { signal },
  );
  loadMore.addEventListener(
    "click",
    () => {
      if (!request && continuation) void search(continuation.fields, continuation.cursor);
    },
    { signal },
  );
  results.addEventListener(
    "click",
    (event) => {
      const opener = (event.target as Element | null)?.closest<HTMLButtonElement>("button");
      if (opener?.dataset.cardId) onOpenCard({ id: opener.dataset.cardId }, opener);
    },
    { signal },
  );

  return {
    update(nextLanes: readonly SearchLane[], nextRevision: number) {
      if (revision !== undefined && revision !== nextRevision) markStale();
      revision = nextRevision;
      if (lanes !== nextLanes || (lane.value && !nextLanes.some(({ id }) => id === lane.value))) {
        const selected = lane.value;
        lanes = nextLanes;
        setLanes(selected);
        if (lane.value !== selected) retain();
      }
      if (restorePending) {
        restorePending = false;
        if (!signal.aborted) void search();
      }
    },
    restore(saved: DeeperSearchState | undefined, queryFallback = "") {
      resetSearch();
      paintResults([]);
      submitted = saved?.submitted ?? false;
      restorePending = submitted;
      rememberedOpen = saved?.open ?? queryFallback !== "";
      panel.open = rememberedOpen;
      query.value = saved?.query ?? queryFallback;
      content.checked = saved?.content ?? false;
      order.value = saved?.order ?? "triage";
      setLanes(saved?.laneId ?? "", true);
      limit.value = saved?.limit ?? "20";
      archive.value = saved?.archive ?? "active";
      updateLimitLabel();
      if (submitted) markStale();
      else setStatus("");
    },
    suspend: markStale,
    dispose() {
      lifetime.abort();
      panel.remove();
    },
  };
}
