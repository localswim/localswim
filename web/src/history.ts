import { appendCardProse } from "../card-prose.mjs";

export const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const auditId = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const utc = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\+00:00$/;
export const historyPageSize = 20;

export type CardScope = Readonly<{ boardId: string; cardId: string }>;
export type AuthorName = (id: string) => string | undefined;
export type AuthorKind = (id: string) => "human" | "bot" | undefined;
export type TicketLink = (ticket: number, label: string) => HTMLAnchorElement;
type Byline = Readonly<{ actor: string; at: string }>;
type Comment = Byline & Readonly<{ id: string; text: string }>;
type Activity = Byline &
  Readonly<{
    id: string;
    revision: number;
    operation: string;
    field: string | null;
    before: string | null;
    after: string | null;
  }>;
type Page<T> = Readonly<{ items: readonly T[]; next: string | null }>;

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isId(value: unknown, pattern = uuid): value is string {
  return typeof value === "string" && value.length === 36 && pattern.test(value);
}

function timestamp(value: unknown): value is string {
  return typeof value === "string" && value.length === 25 && utc.test(value);
}

export function readComment(value: unknown, scope: CardScope): Comment {
  if (
    !record(value) ||
    value.board_id !== scope.boardId ||
    value.card_id !== scope.cardId ||
    !isId(value.id) ||
    !isId(value.author_id) ||
    !timestamp(value.created_at) ||
    typeof value.text !== "string" ||
    !value.text.length ||
    !value.text.isWellFormed()
  ) {
    throw new TypeError("Invalid focused comment.");
  }
  return { id: value.id, actor: value.author_id, at: value.created_at, text: value.text };
}

function nullableText(value: unknown): value is string | null {
  return value === null || (typeof value === "string" && value.isWellFormed());
}

function readActivity(value: unknown, scope: CardScope): Activity {
  if (
    !record(value) ||
    value.board_id !== scope.boardId ||
    !isId(value.id, auditId) ||
    !isId(value.actor_id) ||
    !isId(value.resource_id) ||
    (value.resource_type === "card"
      ? value.resource_id !== scope.cardId
      : value.resource_type !== "comment" &&
        value.resource_type !== "attachment" &&
        value.resource_type !== "relationship") ||
    !timestamp(value.at) ||
    typeof value.revision !== "number" ||
    !Number.isSafeInteger(value.revision) ||
    value.revision < 1 ||
    typeof value.operation !== "string" ||
    !value.operation ||
    !nullableText(value.field) ||
    !nullableText(value.before) ||
    !nullableText(value.after)
  ) {
    throw new TypeError("Invalid focused activity.");
  }
  return {
    id: value.id,
    actor: value.actor_id,
    at: value.at,
    revision: value.revision,
    operation: value.operation,
    field: value.field,
    before: value.before,
    after: value.after,
  };
}

function readPage<T extends { id: string }>(
  value: unknown,
  key: string,
  limit: number,
  parse: (item: unknown) => T,
): Page<T> {
  if (!record(value)) throw new TypeError("Invalid history page.");
  const items = value[key];
  const next = value.next_cursor;
  if (
    !Array.isArray(items) ||
    items.length > limit ||
    (next !== null && (typeof next !== "string" || !next || next.length > 2048))
  ) {
    throw new TypeError("Invalid history page.");
  }
  const rows = items.map(parse);
  if (new Set(rows.map(({ id }) => id)).size !== rows.length) {
    throw new TypeError("Duplicate history entries.");
  }
  return { items: rows, next };
}

export function readCommentPage(value: unknown, scope: CardScope, limit: number) {
  return readPage(value, "comments", limit, (item) => readComment(item, scope));
}

export function readActivityPage(value: unknown, scope: CardScope) {
  return readPage(value, "activities", historyPageSize, (item) => readActivity(item, scope));
}

function byline(doc: Document, entry: Byline, authorName: AuthorName) {
  const meta = doc.createElement("small");
  const time = doc.createElement("time");
  time.dateTime = entry.at.replace(" ", "T");
  time.textContent = entry.at;
  meta.className = "card-comment-meta";
  meta.append(authorName(entry.actor) ?? entry.actor, " · ", time);
  return meta;
}

export function renderComment(
  doc: Document,
  entry: Comment,
  authorName: AuthorName,
  ticketLink?: TicketLink,
) {
  const row = doc.createElement("li");
  row.className = "card-comment";
  const prose = doc.createElement("p");
  prose.className = "card-comment-prose";
  appendCardProse(prose, entry.text, ticketLink);
  row.append(byline(doc, entry, authorName), prose);
  return row;
}

const fieldLabels = new Map([
  ["priority_id", "Priority"],
  ["lane_id", "Lane"],
  ["owner_id", "Owner"],
  ["parent_id", "Parent card"],
  ["description", "Description"],
  ["title", "Title"],
  ["ticket", "Ticket"],
  ["archived", "Archived"],
]);

export function renderActivity(doc: Document, entry: Activity, authorName: AuthorName) {
  const row = doc.createElement("li");
  row.className = "card-comment";
  const meta = byline(doc, entry, authorName);
  meta.append(` · Revision ${entry.revision}`);
  const heading = doc.createElement("p");
  heading.textContent = entry.field
    ? (fieldLabels.get(entry.field) ?? entry.field.replaceAll("_", " "))
    : entry.operation.replaceAll("_", " ");
  row.append(meta, heading);
  if (entry.before !== null || entry.after !== null) {
    const values = doc.createElement("dl");
    for (const [label, value] of [
      ["Previous", entry.before],
      ["Saved", entry.after],
    ] as const) {
      const key = doc.createElement("dt");
      key.textContent = label;
      const text = doc.createElement("dd");
      text.className = "card-comment-prose";
      text.textContent = value ?? "—";
      values.append(key, text);
    }
    row.append(values);
  }
  return row;
}

export function mountHistory<T extends { id: string }>(
  root: HTMLElement,
  signal: AbortSignal,
  label: string,
  load: (cursor: string | undefined, signal: AbortSignal) => Promise<Page<T>>,
  render: (item: T) => HTMLElement,
  renderKey: (item: T) => string,
) {
  const doc = root.ownerDocument;
  const details = doc.createElement("details");
  details.className = "card-comment-history";
  const summary = doc.createElement("summary");
  summary.textContent = label;
  const list = doc.createElement("ol");
  list.className = "card-comment-list";
  const status = doc.createElement("p");
  status.className = "card-comment-status";
  status.setAttribute("role", "status");
  const more = doc.createElement("button");
  more.type = "button";
  more.hidden = true;
  details.append(summary, list, status, more);
  root.append(details);

  let read: AbortController | undefined;
  let next: string | null | undefined;
  const seenIds = new Set<string>();
  const seenCursors = new Set<string>();
  const rendered: { item: T; node: HTMLElement; key: string }[] = [];

  async function fetchPage() {
    if (signal.aborted || !details.open || read || next === null) return;
    const attempt = new AbortController();
    read = attempt;
    const pageSignal = AbortSignal.any([signal, attempt.signal]);
    status.textContent = "Loading…";
    more.setAttribute("aria-disabled", "true");
    try {
      const page = await load(next, pageSignal);
      if (pageSignal.aborted) return;
      if (
        (page.next && seenCursors.has(page.next)) ||
        page.items.some(({ id }) => seenIds.has(id))
      ) {
        throw new TypeError("Overlapping history page.");
      }
      const rows = page.items.map((item) => ({ item, node: render(item), key: renderKey(item) }));
      list.append(...rows.map(({ node }) => node));
      rendered.push(...rows);
      for (const { id } of page.items) seenIds.add(id);
      if (page.next) seenCursors.add(page.next);
      next = page.next;
      more.textContent = "Load more";
      more.hidden = next === null;
      if (more.hidden && doc.activeElement === more) summary.focus({ preventScroll: true });
      status.textContent = seenIds.size ? "" : "No entries.";
    } catch {
      if (pageSignal.aborted) return;
      status.textContent = "History could not be loaded.";
      more.textContent = "Reload history";
      more.hidden = false;
    } finally {
      if (read === attempt) {
        read = undefined;
        more.removeAttribute("aria-disabled");
      }
    }
  }

  function reset() {
    read?.abort();
    read = undefined;
    next = undefined;
    seenIds.clear();
    seenCursors.clear();
    rendered.length = 0;
    list.replaceChildren();
    status.textContent = "";
    if (doc.activeElement === more) summary.focus({ preventScroll: true });
    more.hidden = true;
    more.removeAttribute("aria-disabled");
    if (details.open) void fetchPage();
  }

  details.addEventListener("toggle", reset, { signal });
  more.addEventListener(
    "click",
    () => {
      if (read) return;
      if (more.textContent === "Reload history") reset();
      else void fetchPage();
    },
    { signal },
  );
  return {
    reset,
    refreshRows() {
      for (const row of rendered) {
        const key = renderKey(row.item);
        if (key === row.key) continue;
        const focused = row.node.contains(doc.activeElement);
        const node = render(row.item);
        row.node.replaceWith(node);
        row.node = node;
        row.key = key;
        if (focused) summary.focus({ preventScroll: true });
      }
    },
  };
}
