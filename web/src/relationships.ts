import { ApiError, type RelationshipKind } from "../api.mjs";
import type { AppContext } from "./model.js";
import { createScopedOperations, submitOperation } from "./operations.js";

type CardSummary = Readonly<{ id: string; ticket: number; title: string }>;
type Target = CardSummary & Readonly<{ archived: boolean }>;
type Link = Readonly<{
  id: string;
  source_card_id: string;
  target_card_id: string;
  kind: RelationshipKind;
  revision: number;
}>;
type Row = Readonly<{ link: Link; target: Target | null }>;
type Intent = Pick<Link, "id" | "source_card_id" | "target_card_id" | "kind"> &
  Readonly<{ action: "add" | "change" | "remove" }>;
type Journal = Intent & Readonly<{ baseRevision: number }>;

export type RelationshipContext = Readonly<{
  cardId: string;
  archived: boolean;
  boardEtag: string;
  canManage: boolean;
  cards: readonly CardSummary[];
}>;

type Options = AppContext &
  Readonly<{
    boardId: string;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    ticketLink: (ticket: number, label: string, id: string) => HTMLAnchorElement;
    beginWrite: (cardId: string, relatedCardId?: string) => (() => void) | null;
    beginInspection: (sourceCardId: string, targetCardId: string) => (() => void) | null;
    refresh: () => void;
  }>;

const labels = {
  blocks: ["Blocks", "Blocked by"],
  duplicates: ["Duplicates", "Duplicated by"],
  references: ["References", "Referenced by"],
  relates_to: ["Relates to", "Relates to"],
} satisfies Record<RelationshipKind, readonly [string, string]>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function isKind(value: unknown): value is RelationshipKind {
  return typeof value === "string" && Object.hasOwn(labels, value);
}

function isJournal(value: unknown): value is Journal {
  if (value === null || typeof value !== "object") return false;
  const { id, source_card_id, target_card_id, kind, action, baseRevision } =
    value as Partial<Journal>;
  return (
    typeof id === "string" &&
    uuid.test(id) &&
    typeof source_card_id === "string" &&
    uuid.test(source_card_id) &&
    typeof target_card_id === "string" &&
    uuid.test(target_card_id) &&
    source_card_id !== target_card_id &&
    isKind(kind) &&
    (action === "add" || action === "change" || action === "remove") &&
    typeof baseRevision === "number" &&
    Number.isSafeInteger(baseRevision) &&
    baseRevision > 0
  );
}

export function mountRelationships(
  root: HTMLElement,
  {
    api,
    signal: ownerSignal,
    boardId,
    readWithSession,
    ticketLink,
    beginWrite,
    beginInspection,
    refresh,
  }: Options,
) {
  const doc = root.ownerDocument;
  const lifetime = new AbortController();
  const signal = AbortSignal.any([ownerSignal, lifetime.signal]);
  const operations = createScopedOperations();
  const journalKey = `localswim.relationships.v1.${boardId}`;
  let context: RelationshipContext | null = null;
  let ready = false;
  let rows: readonly Row[] = [];
  let loadedTag: string | null = null;
  let loadFailed = false;
  let read: AbortController | undefined;
  let lookup: AbortController | undefined;
  let pending: Journal | null = null;
  let storageFailed = false;
  let notice = "";

  const element = <K extends keyof HTMLElementTagNameMap>(tag: K, className = "", text = "") => {
    const node = doc.createElement(tag);
    node.className = className;
    node.textContent = text;
    return node;
  };
  const button = (text: string) => {
    const node = element("button", "", text);
    node.type = "button";
    return node;
  };
  const option = (value: string, text: string) => {
    const node = element("option", "", text);
    node.value = value;
    return node;
  };
  const kindSelect = (current: RelationshipKind, direction: 0 | 1 = 0) => {
    const node = element("select");
    for (const kind of Object.keys(labels)) {
      if (isKind(kind)) node.append(option(kind, labels[kind][direction]));
    }
    node.value = current;
    return node;
  };
  const status = element("p", "card-editor-status");
  status.setAttribute("role", "status");
  const heading = element("h3", "card-drawer-heading", "Relationships");
  heading.tabIndex = -1;
  const retry = button("Retry relationships");
  const inspect = button("Inspect saved relationship");
  const list = element("ul", "card-drawer-links");
  const form = element("form");
  const controls = element("div", "card-editor-actions");
  const direction = element("select");
  direction.setAttribute("aria-label", "Relationship direction");
  direction.append(option("outgoing", "From this card"), option("incoming", "To this card"));
  const kind = kindSelect("relates_to");
  kind.setAttribute("aria-label", "Relationship kind");
  const target = element("input");
  target.type = "text";
  target.inputMode = "numeric";
  target.required = true;
  target.pattern = "#?[0-9]+";
  target.placeholder = "Ticket number";
  target.setAttribute("aria-label", "Related ticket number");
  const tickets = element("datalist");
  tickets.id = "card-relationship-tickets";
  target.setAttribute("list", tickets.id);
  const add = button("Add relationship");
  add.type = "submit";
  controls.append(direction, kind, target, add);
  form.append(controls, tickets);
  root.append(heading, status, retry, inspect, list, form);

  function updateDirectionLabels() {
    const inbound = direction.value === "incoming";
    for (const choice of kind.options) {
      if (isKind(choice.value)) choice.textContent = labels[choice.value][inbound ? 1 : 0];
    }
  }

  function journalStorage() {
    const storage = doc.defaultView?.sessionStorage;
    if (!storage) throw new TypeError("Session storage unavailable.");
    return storage;
  }

  function readJournal() {
    try {
      const raw = journalStorage().getItem(journalKey);
      const parsed: unknown = raw === null ? null : JSON.parse(raw);
      if (parsed !== null && !isJournal(parsed))
        throw new TypeError("Invalid relationship journal.");
      pending = parsed;
      return raw;
    } catch {
      storageFailed = true;
      return undefined;
    }
  }

  function writeJournal(intent: Journal) {
    if (readJournal() !== null) return null;
    try {
      const raw = JSON.stringify(intent);
      journalStorage().setItem(journalKey, raw);
      pending = intent;
      return raw;
    } catch {
      storageFailed = true;
      return null;
    }
  }

  function clearJournal(original: string) {
    if (readJournal() !== original) return false;
    try {
      journalStorage().removeItem(journalKey);
      pending = null;
      return true;
    } catch {
      storageFailed = true;
      return false;
    }
  }

  function canWrite(): boolean {
    return Boolean(
      !signal.aborted &&
      context?.canManage &&
      ready &&
      !read &&
      !loadFailed &&
      loadedTag === context.boardEtag &&
      !pending &&
      !storageFailed &&
      !operations.state(boardId),
    );
  }

  function renderControls() {
    root.hidden = context === null;
    if (!context) return;
    const busy = operations.state(boardId);
    const writable = canWrite();
    form.hidden = !context.canManage;
    for (const control of [direction, kind, target, add]) control.disabled = !writable || !!lookup;
    for (const control of list.querySelectorAll<HTMLButtonElement | HTMLSelectElement>(
      "button, select",
    )) {
      control.hidden = !context.canManage;
      control.disabled = !writable;
    }
    retry.hidden = !loadFailed;
    inspect.hidden = pending === null;
    inspect.disabled = busy !== undefined;
    if (busy === "inspecting") status.textContent = "Inspecting saved relationship…";
    else if (busy === "submitting") status.textContent = "Saving relationship…";
    else if (storageFailed)
      status.textContent = "Relationship recovery storage is unavailable. Editing is disabled.";
    else if (notice) status.textContent = notice;
    else if (pending)
      status.textContent =
        "The relationship write outcome is unknown. Inspect the saved relationship.";
    else if (lookup) status.textContent = "Looking up ticket…";
    else if (read && !loadedTag) status.textContent = "Loading relationships…";
    else if (loadFailed) status.textContent = "Could not load current relationships.";
    else status.textContent = "";
  }

  function targetId(link: Pick<Link, "source_card_id" | "target_card_id">, cardId: string) {
    return link.source_card_id === cardId ? link.target_card_id : link.source_card_id;
  }

  function renderRows(cardId: string) {
    const focused = list.contains(doc.activeElement) ? doc.activeElement : null;
    const focusKey = focused?.getAttribute("data-relation-focus");
    const choices = new Map(
      [...list.querySelectorAll("select")].map((select) => [select.dataset.relationFocus, select]),
    );
    const nodes = rows.map(({ link, target: related }) => {
      const row = element("li");
      const direction = link.source_card_id === cardId ? 0 : 1;
      const label = labels[link.kind][direction];
      row.append(element("span", "card-drawer-relation-kind", label), " ");
      const name = related
        ? ticketLink(related.ticket, `#${related.ticket} ${related.title}`, related.id)
        : element("span", "card-drawer-empty", "Card unavailable");
      if (related?.archived) name.append(element("span", "card-drawer-archive", " (Archived)"));
      name.dataset.relationFocus = `${link.id}-link`;
      row.append(name);
      const actions = element("div", "card-editor-actions");
      const replacement = kindSelect(link.kind, direction);
      replacement.dataset.relationFocus = `${link.id}-kind`;
      replacement.dataset.revision = String(link.revision);
      const previous = choices.get(replacement.dataset.relationFocus);
      if (previous && previous.dataset.revision === replacement.dataset.revision) {
        replacement.value = previous.value;
      }
      const description = related
        ? `#${related.ticket} ${label.toLowerCase()}`
        : label.toLowerCase();
      replacement.setAttribute("aria-label", `New relationship kind for ${description}`);
      const change = button("Change kind");
      change.dataset.relationFocus = `${link.id}-change`;
      change.setAttribute("aria-label", `Change kind for ${description}`);
      change.addEventListener("click", () => {
        if (!isKind(replacement.value) || replacement.value === link.kind) return;
        void submit(cardId, { ...link, kind: replacement.value, action: "change" }, link.revision);
      });
      const remove = button("Remove");
      remove.dataset.relationFocus = `${link.id}-remove`;
      remove.setAttribute("aria-label", `Remove relationship for ${description}`);
      remove.addEventListener("click", () => {
        void submit(cardId, { ...link, action: "remove" }, link.revision);
      });
      actions.append(replacement, change, remove);
      row.append(actions);
      return row;
    });
    if (!nodes.length && loadedTag !== null) {
      nodes.push(element("li", "card-drawer-empty", "No relationships."));
    }
    list.replaceChildren(...nodes);
    renderControls();
    if (focusKey) {
      const replacement = [...list.querySelectorAll<HTMLElement>("[data-relation-focus]")].find(
        (node) => node.dataset.relationFocus === focusKey && !node.matches("[disabled], [hidden]"),
      );
      (replacement ?? heading).focus({ preventScroll: true });
    }
  }

  async function readLinks(cardId: string, requestSignal: AbortSignal): Promise<Link[]> {
    const links = new Map<string, Link>();
    const cursors = new Set<string>();
    let cursor: string | undefined;
    do {
      const reply = await readWithSession(
        () => api.listRelationships(boardId, cardId, cursor),
        requestSignal,
      );
      requestSignal.throwIfAborted();
      // The adapter verifies identity, membership, revision and kind for each entry.
      const page = reply.data as { relationships: readonly Link[]; next_cursor: unknown };
      for (const link of page.relationships) {
        if (links.has(link.id) || link.source_card_id === link.target_card_id)
          throw new ApiError("invalid_response");
        links.set(link.id, link);
      }
      const next = page.next_cursor;
      if (next === null) break;
      if (typeof next !== "string" || next.length === 0 || next.length > 2048 || cursors.has(next))
        throw new ApiError("invalid_response");
      cursors.add(next);
      cursor = next;
    } while (cursor !== undefined);
    return [...links.values()];
  }

  async function readTarget(id: string, requestSignal: AbortSignal): Promise<Target | null> {
    try {
      const { data } = await readWithSession(() => api.getCard(id, requestSignal), requestSignal);
      requestSignal.throwIfAborted();
      const saved = data as Target & { board_id: string };
      return saved.board_id === boardId ? saved : null;
    } catch (error) {
      if (error instanceof ApiError && (error.status === 403 || error.status === 404)) return null;
      throw error;
    }
  }

  async function checkBoard(etag: string, requestSignal: AbortSignal) {
    const { etag: current } = await readWithSession(
      () => api.getBoardIfChanged(boardId, etag, requestSignal),
      requestSignal,
    );
    requestSignal.throwIfAborted();
    if (current !== etag) throw new ApiError("http_error", { status: 412 });
  }

  async function load(current: RelationshipContext) {
    read?.abort();
    const attempt = new AbortController();
    read = attempt;
    const requestSignal = AbortSignal.any([signal, attempt.signal]);
    loadFailed = false;
    renderControls();
    try {
      const links = await readLinks(current.cardId, requestSignal);
      const targets = new Map<string, Target | null>();
      const known = new Map(current.cards.map((card) => [card.id, card]));
      const ids = [...new Set(links.map((link) => targetId(link, current.cardId)))];
      for (let offset = 0; offset < ids.length; offset += 8) {
        await Promise.all(
          ids.slice(offset, offset + 8).map(async (id) => {
            const active = known.get(id);
            targets.set(
              id,
              active ? { ...active, archived: false } : await readTarget(id, requestSignal),
            );
          }),
        );
      }
      await checkBoard(current.boardEtag, requestSignal);
      rows = links.map((link) => ({
        link,
        target: targets.get(targetId(link, current.cardId)) ?? null,
      }));
      loadedTag = current.boardEtag;
      renderRows(current.cardId);
    } catch {
      if (!requestSignal.aborted) loadFailed = true;
    } finally {
      if (read === attempt) {
        read = undefined;
        renderControls();
      }
    }
  }

  async function findTarget(
    current: RelationshipContext,
    ticket: number,
    requestSignal: AbortSignal,
  ) {
    const known = current.cards.find((card) => card.ticket === ticket);
    if (known) return known.id;
    const { data } = await readWithSession(
      () => api.listCards(boardId, { ticket, archived: "include", limit: 2 }, requestSignal),
      requestSignal,
    );
    requestSignal.throwIfAborted();
    const page = data as { cards?: unknown; next_cursor?: unknown };
    if (!Array.isArray(page.cards) || page.next_cursor !== null)
      throw new ApiError("invalid_response");
    if (page.cards.length === 0) return null;
    const [match] = page.cards as readonly unknown[];
    if (
      page.cards.length !== 1 ||
      !match ||
      typeof match !== "object" ||
      !("id" in match) ||
      typeof match.id !== "string" ||
      !uuid.test(match.id) ||
      !("board_id" in match) ||
      match.board_id !== boardId ||
      !("ticket" in match) ||
      match.ticket !== ticket
    ) {
      throw new ApiError("invalid_response");
    }
    return match.id;
  }

  function sameLink(link: Link | undefined, intent: Intent) {
    return (
      link?.id === intent.id &&
      link.source_card_id === intent.source_card_id &&
      link.target_card_id === intent.target_card_id &&
      link.kind === intent.kind
    );
  }

  async function submit(
    cardId: string,
    intent: Intent,
    revision: number,
    expectedEtag = context?.boardEtag,
  ) {
    if (!context || context.cardId !== cardId || context.boardEtag !== expectedEtag || !canWrite())
      return;
    const etag = context.boardEtag;
    const baseRevision = Number(etag.slice(boardId.length + 2, -1));
    if (
      !Number.isSafeInteger(baseRevision) ||
      baseRevision < 1 ||
      etag !== `"${boardId}-${baseRevision}"`
    )
      return;
    const release = beginWrite(cardId, targetId(intent, cardId));
    if (!release) return;
    notice = "";
    try {
      await operations.run(
        boardId,
        "submitting",
        async () => {
          renderControls();
          let journal: string | null = null;
          const result = await submitOperation(
            () => {
              journal = writeJournal({ ...intent, baseRevision });
              return journal !== null;
            },
            async () => {
              if (intent.action === "remove") {
                await api.removeRelationship(intent.id, revision, etag);
                return;
              }
              const reply =
                intent.action === "add"
                  ? await api.createRelationship(
                      intent.id,
                      boardId,
                      etag,
                      intent.source_card_id,
                      intent.target_card_id,
                      intent.kind,
                    )
                  : await api.replaceRelationship(intent.id, revision, etag, intent.kind);
              if (!sameLink(reply.data as Link, intent))
                throw new ApiError("invalid_response", { outcomeUnknown: true });
            },
          );
          switch (result.kind) {
            case "confirmed":
              if (journal) clearJournal(journal);
              notice = intent.action === "remove" ? "Relationship removed." : "Relationship saved.";
              if (intent.action === "add" && context?.cardId === cardId) target.value = "";
              break;
            case "stale":
              if (journal) clearJournal(journal);
              notice = "The board or relationship changed. Review the latest relationships.";
              break;
            case "refused":
              if (journal) clearJournal(journal);
              notice = "The relationship change was refused.";
              break;
            case "unknown":
            case "journal-failed":
              break;
          }
          if (result.kind !== "journal-failed") refreshAfterWrite();
        },
        renderControls,
      );
    } finally {
      release();
    }
  }

  function refreshAfterWrite() {
    read?.abort();
    read = undefined;
    loadFailed = false;
    loadedTag = null;
    renderControls();
    if (!signal.aborted) refresh();
  }

  async function addRelationship() {
    const current = context;
    if (!current || !canWrite() || lookup || !isKind(kind.value)) return;
    const ticket = Number(target.value.replace(/^#/, ""));
    if (!Number.isSafeInteger(ticket) || ticket < 1) {
      notice = "Enter a valid ticket number.";
      renderControls();
      return;
    }
    const selectedKind = kind.value;
    const inbound = direction.value === "incoming";
    const attempt = new AbortController();
    const requestSignal = AbortSignal.any([signal, attempt.signal]);
    let id: string | null;
    lookup = attempt;
    notice = "";
    renderControls();
    try {
      id = await findTarget(current, ticket, requestSignal);
    } catch {
      if (!requestSignal.aborted) notice = "Could not look up that ticket.";
      return;
    } finally {
      if (lookup === attempt) {
        lookup = undefined;
        renderControls();
      }
    }
    if (
      requestSignal.aborted ||
      context?.cardId !== current.cardId ||
      context.boardEtag !== current.boardEtag
    )
      return;
    if (!id || id === current.cardId) {
      notice = "Choose a different ticket on this board.";
      renderControls();
      return;
    }
    await submit(
      current.cardId,
      {
        action: "add",
        id: globalThis.crypto.randomUUID(),
        source_card_id: inbound ? id : current.cardId,
        target_card_id: inbound ? current.cardId : id,
        kind: selectedKind,
      },
      0,
      current.boardEtag,
    );
  }

  async function inspectRelationship() {
    if (signal.aborted || operations.state(boardId)) return;
    const original = readJournal();
    const intent = pending;
    if (!original || !intent) {
      notice = "";
      renderControls();
      return;
    }
    const release = beginInspection(intent.source_card_id, intent.target_card_id);
    if (!release) {
      notice =
        "A card operation is still running. Inspect the saved relationship again when it finishes.";
      renderControls();
      return;
    }
    try {
      await operations.run(
        boardId,
        "inspecting",
        async () => {
          notice = "";
          renderControls();
          try {
            const { etag, data } = await readWithSession(
              () => api.getBoardAvailability(boardId, signal),
              signal,
            );
            const links = await readLinks(intent.source_card_id, signal);
            await checkBoard(etag, signal);
            const found = links.find(({ id }) => id === intent.id);
            const matches = intent.action === "remove" ? !found : sameLink(found, intent);
            // An outstanding request still has a usable board precondition at its base revision.
            if (data.revision <= intent.baseRevision) {
              notice = "The relationship change is not visible yet. The outcome is still unknown.";
              return;
            }
            if (!clearJournal(original)) return;
            notice = matches
              ? intent.action === "remove"
                ? "The inspected relationship is absent."
                : "The inspected relationship matches the submitted change."
              : "The inspected relationship does not match the submitted change.";
            refreshAfterWrite();
          } catch {
            if (!signal.aborted)
              notice = "Could not inspect the saved relationship. Try inspection again.";
          }
        },
        renderControls,
      );
    } finally {
      release();
    }
  }

  retry.addEventListener(
    "click",
    () => {
      if (!context) return;
      if (!signal.aborted) refresh();
      if (!read) void load(context);
    },
    { signal },
  );
  direction.addEventListener("change", updateDirectionLabels, { signal });
  inspect.addEventListener("click", () => void inspectRelationship(), { signal });
  form.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      void addRelationship();
    },
    { signal },
  );

  readJournal();
  renderControls();
  return {
    update(next: RelationshipContext | null, writable: boolean) {
      if (signal.aborted) return;
      if (!operations.state(boardId)) readJournal();
      const previous = context;
      context = next;
      ready = writable;
      if (next?.cardId !== previous?.cardId) {
        read?.abort();
        read = undefined;
        lookup?.abort();
        lookup = undefined;
        loadedTag = null;
        loadFailed = false;
        rows = [];
        notice = "";
        target.value = "";
        kind.value = "relates_to";
        direction.value = "outgoing";
        updateDirectionLabels();
        list.replaceChildren();
      }
      if (next && (next.cardId !== previous?.cardId || next.cards !== previous.cards)) {
        tickets.replaceChildren(
          ...next.cards
            .filter(({ id }) => id !== next.cardId)
            .map(({ ticket, title }) => option(String(ticket), title)),
        );
      }
      if (
        next &&
        (next.cardId !== previous?.cardId ||
          next.boardEtag !== previous.boardEtag ||
          (writable && !read && !loadFailed && loadedTag !== next.boardEtag))
      ) {
        void load(next);
      } else {
        renderControls();
      }
    },
    dispose() {
      lifetime.abort();
      root.replaceChildren();
    },
  };
}
