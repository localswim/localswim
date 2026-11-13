import { ApiError } from "../api.mjs";
import {
  historyPageSize,
  mountHistory,
  readActivityPage,
  readComment,
  readCommentPage,
  renderActivity,
  renderComment,
  uuid,
  type AuthorName,
  type CardScope,
  type TicketLink,
} from "./history.js";
import type { AppContext } from "./model.js";
import { createScopedOperations, submitOperation, type OperationStorage } from "./operations.js";
import { applyInput, rememberExact, textareaText, type InputChange } from "./text-input.js";

type CommentContext = Readonly<{
  actorId: string;
  boardEtag: string;
  cardEtag: string;
  revision: number;
  commentCount: number;
  writable: boolean;
}>;

type CommentOptions = Readonly<{
  api: Pick<
    AppContext["api"],
    "listComments" | "getComment" | "createComment" | "listFocusedCardActivity"
  >;
  scope: CardScope;
  signal: AbortSignal;
  context: () => CommentContext | null;
  authorName: AuthorName;
  ticketLink: TicketLink;
  readWithSession: <T>(read: () => Promise<T>, signal?: AbortSignal) => Promise<T>;
  beginWrite: (cardId: string) => (() => void) | null;
  onConfirmed: (boardId: string, cardId: string, boardEtag: string | null) => void;
  onStale: (boardId: string, cardId: string) => void;
  storage?: OperationStorage;
}>;

type Draft = Readonly<{ text: string; id: string | null }>;
type Journal = {
  key: string;
  raw: string | null | undefined;
  draft: Draft;
  unsaved: boolean;
  error: boolean;
};

const emptyDraft: Draft = { text: "", id: null };
const previewSize = 2;
const strongTag = /^"[A-Za-z0-9_-]{16,128}"(?![\s\S])/;
const operations = createScopedOperations();
const listeners = new Map<string, () => void>();

function journalKey(actor: string, { boardId, cardId }: CardScope) {
  return `localswim:v2:comment:${actor}:${boardId}:${cardId}`;
}

function journalDraft(raw: string): Draft {
  const value: unknown = JSON.parse(raw);
  if (
    typeof value !== "object" ||
    value === null ||
    !("text" in value) ||
    typeof value.text !== "string" ||
    !value.text.length ||
    !value.text.isWellFormed() ||
    !("id" in value) ||
    (value.id !== null &&
      (typeof value.id !== "string" || value.id.length !== 36 || !uuid.test(value.id)))
  ) {
    throw new TypeError("Invalid comment draft.");
  }
  return { text: value.text, id: value.id };
}

export function mountCardComments(
  root: HTMLElement,
  {
    api,
    scope,
    signal,
    context,
    authorName,
    ticketLink,
    readWithSession,
    beginWrite,
    onConfirmed,
    onStale,
    storage,
  }: CommentOptions,
) {
  const { boardId, cardId } = scope;
  const filter = { board_id: boardId, card_id: cardId };
  const tabStorage = () => storage ?? globalThis.sessionStorage;
  const doc = root.ownerDocument;
  const region = doc.createElement("section");
  region.className = "card-comments";
  const heading = doc.createElement("h3");
  heading.textContent = "Comments";
  const preview = doc.createElement("ol");
  preview.className = "card-comment-list";
  const previewStatus = doc.createElement("p");
  previewStatus.className = "card-comment-status";
  previewStatus.setAttribute("role", "status");
  const retryPreview = doc.createElement("button");
  retryPreview.type = "button";
  retryPreview.textContent = "Reload recent comments";
  retryPreview.hidden = true;
  const form = doc.createElement("form");
  form.className = "card-comment-form";
  const label = doc.createElement("label");
  label.textContent = "Add a comment";
  const input = doc.createElement("textarea");
  input.className = "card-comment-input";
  input.rows = 3;
  label.append(input);
  const hint = doc.createElement("small");
  hint.id = `card-comment-hint-${cardId}`;
  hint.textContent = "Enter posts. Shift+Enter adds a new line.";
  input.setAttribute("aria-describedby", hint.id);
  const actions = doc.createElement("div");
  actions.className = "card-comment-actions";
  const post = doc.createElement("button");
  post.type = "submit";
  post.textContent = "Post comment";
  const inspect = doc.createElement("button");
  inspect.type = "button";
  inspect.textContent = "Inspect original comment";
  const status = doc.createElement("p");
  status.className = "card-comment-status";
  status.setAttribute("role", "status");
  actions.append(post, inspect);
  form.append(label, hint, actions, status);
  region.append(heading, preview, previewStatus, retryPreview, form);
  root.append(region);

  const readComments = async (
    limit: number,
    cursor: string | undefined,
    readSignal: AbortSignal,
  ) => {
    const reply = await readWithSession(
      () => api.listComments(filter, limit, cursor, readSignal),
      readSignal,
    );
    return readCommentPage(reply.data, scope, limit);
  };
  const commentHistory = mountHistory(
    region,
    signal,
    "All comments",
    (cursor, readSignal) => readComments(historyPageSize, cursor, readSignal),
    (entry) => renderComment(doc, entry, authorName, ticketLink),
  );
  const activityHistory = mountHistory(
    region,
    signal,
    "Card history",
    async (cursor, readSignal) => {
      const reply = await readWithSession(
        () => api.listFocusedCardActivity(boardId, cardId, cursor, readSignal),
        readSignal,
      );
      return readActivityPage(reply.data, scope);
    },
    (entry) => renderActivity(doc, entry, authorName),
  );

  let journal: Journal | undefined;
  let revision: number | undefined;
  let commentCount: number | undefined;
  let previewRead: AbortController | undefined;
  let notice = "";
  let change: InputChange | undefined;
  let pasted: string | undefined;
  let composition: { journal: Journal; previous: string; change?: InputChange } | undefined;
  let compositionEnter = false;
  const recent = new Map<string, string | null>();

  function load(entry: Journal) {
    if (entry.unsaved) return;
    try {
      const raw = tabStorage().getItem(entry.key);
      if (raw !== entry.raw) {
        entry.draft = raw === null ? emptyDraft : journalDraft(raw);
        entry.raw = raw;
      }
      entry.error = false;
    } catch {
      entry.error = true;
    }
  }

  function persist(entry: Journal, draft: Draft, keepLocally = false): boolean {
    try {
      const store = tabStorage();
      if (store.getItem(entry.key) !== entry.raw) throw new TypeError("Comment draft changed.");
      const raw = draft.text ? JSON.stringify(draft) : null;
      if (raw === null) store.removeItem(entry.key);
      else store.setItem(entry.key, raw);
      entry.raw = raw;
      entry.draft = draft;
      entry.unsaved = false;
      entry.error = false;
      return true;
    } catch {
      if (keepLocally) {
        entry.draft = draft;
        entry.unsaved = true;
      }
      entry.error = true;
      return false;
    }
  }

  function detachJournal() {
    if (journal && listeners.get(journal.key) === update) listeners.delete(journal.key);
  }

  function focusInputIfHidden() {
    if (doc.activeElement === inspect && inspect.hidden) input.focus({ preventScroll: true });
  }

  function canPost(current: CommentContext | null, entry: Journal | undefined) {
    return Boolean(
      current?.writable &&
      strongTag.test(current.cardEtag) &&
      strongTag.test(current.boardEtag) &&
      entry &&
      entry.key === journalKey(current.actorId, scope) &&
      !entry.error &&
      !operations.state(entry.key) &&
      !entry.draft.id &&
      entry.draft.text &&
      entry.draft.text.isWellFormed() &&
      !composition,
    );
  }

  function update() {
    if (signal.aborted) return;
    const current = context();
    if (current && current.actorId.length === 36 && uuid.test(current.actorId)) {
      const key = journalKey(current.actorId, scope);
      if (journal?.key !== key) {
        detachJournal();
        journal = { key, raw: undefined, draft: emptyDraft, error: false, unsaved: false };
        notice = "";
        composition = undefined;
        recent.clear();
        listeners.set(key, update);
      }
    }
    const entry = journal;
    if (entry && !composition) load(entry);
    const active = entry && operations.state(entry.key);
    const pending = entry?.draft.id;
    const text = entry?.draft.text ?? "";
    if (!composition && input.value !== textareaText(text)) input.value = textareaText(text);
    input.readOnly = !composition && (!entry || Boolean(pending || active));
    post.hidden = Boolean(pending);
    post.setAttribute("aria-disabled", String(!canPost(current, entry)));
    inspect.hidden = !pending;
    inspect.setAttribute("aria-disabled", String(Boolean(active)));
    focusInputIfHidden();
    const message =
      active === "submitting"
        ? "Posting comment…"
        : active === "inspecting"
          ? "Checking the original comment…"
          : entry?.error
            ? "The comment draft could not be stored. Keep this page open to retain it."
            : notice ||
              (pending
                ? "The post outcome is unknown. Inspect the original comment before continuing."
                : "");
    if (status.textContent !== message) status.textContent = message;
    if (current && current.revision !== revision) {
      revision = current.revision;
      activityHistory.reset();
    }
    if (current && current.commentCount !== commentCount) {
      commentCount = current.commentCount;
      refreshComments();
    }
  }

  async function refreshPreview() {
    previewRead?.abort();
    const attempt = new AbortController();
    previewRead = attempt;
    const readSignal = AbortSignal.any([signal, attempt.signal]);
    retryPreview.hidden = true;
    previewStatus.textContent = "Loading recent comments…";
    try {
      const page = await readComments(previewSize, undefined, readSignal);
      if (readSignal.aborted) return;
      const focused = preview.contains(doc.activeElement);
      preview.replaceChildren(
        ...page.items.map((item) => renderComment(doc, item, authorName, ticketLink)),
      );
      if (focused) input.focus({ preventScroll: true });
      previewStatus.textContent = page.items.length ? "" : "No comments.";
    } catch {
      if (readSignal.aborted) return;
      previewStatus.textContent = "Recent comments could not be loaded.";
      retryPreview.hidden = false;
    }
  }

  function refreshComments() {
    if (signal.aborted) return;
    void refreshPreview();
    commentHistory.reset();
  }

  function publish(entry: Journal) {
    listeners.get(entry.key)?.();
  }

  function prove(reply: { data: object }, actor: string, draft: Draft) {
    const comment = readComment(reply.data, scope);
    if (comment.id !== draft.id || comment.actor !== actor || comment.text !== draft.text) {
      throw new TypeError("The response did not confirm the original comment.");
    }
  }

  function confirmed(entry: Journal, boardEtag: string | null) {
    persist(entry, emptyDraft);
    notice = "Comment posted.";
    refreshComments();
    activityHistory.reset();
    onConfirmed(boardId, cardId, boardEtag);
  }

  async function submit() {
    update();
    const current = context();
    const entry = journal;
    if (signal.aborted || !current || !entry || !canPost(current, entry)) return;
    const draft = { text: entry.draft.text, id: crypto.randomUUID() };
    const release = beginWrite(cardId);
    if (!release) return;
    try {
      await operations.run(
        entry.key,
        "submitting",
        async () => {
          notice = "";
          const result = await submitOperation(
            () => persist(entry, draft),
            async () => {
              publish(entry);
              const reply = await api.createComment(
                cardId,
                current.cardEtag,
                draft.text,
                draft.id,
                current.boardEtag,
              );
              prove(reply, current.actorId, draft);
              const tag = "boardEtag" in reply ? reply.boardEtag : null;
              if (typeof tag !== "string" || !strongTag.test(tag))
                throw new TypeError("The response did not identify the committed board version.");
              return tag;
            },
          );
          switch (result.kind) {
            case "confirmed":
              confirmed(entry, result.result);
              break;
            case "stale":
              persist(entry, { text: draft.text, id: null });
              notice = "The card changed. Your comment draft is still available.";
              onStale(boardId, cardId);
              break;
            case "refused":
              persist(entry, { text: draft.text, id: null });
              notice = "The post was refused. Your comment draft is still available.";
              break;
            case "unknown":
              onStale(boardId, cardId);
              break;
            case "journal-failed":
              break;
          }
        },
        () => publish(entry),
      );
    } finally {
      release();
    }
  }

  async function inspectOriginal() {
    update();
    const entry = journal;
    const actor = context()?.actorId;
    const draft = entry?.draft;
    if (
      signal.aborted ||
      !entry ||
      !actor ||
      entry.key !== journalKey(actor, scope) ||
      !draft?.id
    ) {
      return;
    }
    const id = draft.id;
    await operations.run(
      entry.key,
      "inspecting",
      async () => {
        publish(entry);
        try {
          const reply = await readWithSession(() => api.getComment(id, signal), signal);
          if (signal.aborted || context()?.actorId !== actor) return;
          prove(reply, actor, draft);
          confirmed(entry, null);
        } catch (error) {
          if (signal.aborted) return;
          notice =
            error instanceof ApiError && error.status === 404
              ? "The original comment is not visible yet. Inspect it again to check the outcome."
              : "The original comment could not be verified. Inspect it again to check the outcome.";
        }
      },
      () => publish(entry),
    );
  }

  function edit(entry: Journal, value: string, preserveHistory = false) {
    if (entry !== journal || entry.draft.id || operations.state(entry.key)) return;
    if (value !== entry.draft.text) {
      if (!preserveHistory) {
        rememberExact(recent, entry.draft.text);
        rememberExact(recent, value);
      }
      notice = "";
      persist(entry, { text: value, id: null }, true);
    }
    update();
  }

  function run(action: () => Promise<void>) {
    void action().catch(() => {
      notice = "The comment action could not be completed.";
      update();
    });
  }

  retryPreview.addEventListener("click", () => run(refreshPreview), { signal });
  form.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      if (!compositionEnter) run(submit);
    },
    { signal },
  );
  inspect.addEventListener("click", () => run(inspectOriginal), { signal });
  input.addEventListener(
    "paste",
    (event) => {
      pasted = event.clipboardData?.getData("text/plain");
    },
    { signal },
  );
  input.addEventListener(
    "beforeinput",
    (event) => {
      change = {
        start: input.selectionStart,
        end: input.selectionEnd,
        inserted: event.inputType === "insertFromPaste" ? (pasted ?? event.data) : event.data,
      };
      pasted = undefined;
    },
    { signal },
  );
  input.addEventListener(
    "compositionstart",
    () => {
      if (!journal || input.readOnly) return;
      composition = {
        journal,
        previous: journal.draft.text,
        change: { start: input.selectionStart, end: input.selectionEnd, inserted: null },
      };
      update();
    },
    { signal },
  );
  input.addEventListener(
    "compositionend",
    () => {
      const previous = composition;
      composition = undefined;
      change = undefined;
      if (previous) {
        edit(previous.journal, applyInput(previous.previous, input.value, previous.change));
      }
    },
    { signal },
  );
  input.addEventListener(
    "input",
    (event) => {
      if (composition || (event instanceof InputEvent && event.isComposing)) return;
      const entry = journal;
      if (!entry || input.readOnly) return;
      const previous = entry.draft.text;
      if (
        (!change || change.start === change.end) &&
        event instanceof InputEvent &&
        event.inputType.startsWith("delete")
      ) {
        const start = input.selectionStart;
        change = {
          start,
          end: start + textareaText(previous).length - input.value.length,
          inserted: null,
        };
      }
      const isHistory =
        event instanceof InputEvent &&
        (event.inputType === "historyUndo" || event.inputType === "historyRedo");
      const value = isHistory
        ? (recent.get(input.value) ?? input.value)
        : applyInput(previous, input.value, change);
      change = undefined;
      edit(entry, value, isHistory);
    },
    { signal },
  );
  input.addEventListener(
    "keydown",
    (event) => {
      if (event.isComposing || composition || event.keyCode === 229) {
        if (event.key === "Escape") event.stopPropagation();
        if (event.key === "Enter") {
          compositionEnter = true;
          setTimeout(() => {
            compositionEnter = false;
          }, 0);
        }
        return;
      }
      if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        if (!event.repeat && !compositionEnter) run(submit);
      }
    },
    { signal },
  );
  signal.addEventListener("abort", detachJournal, { once: true });
  update();
  return { update };
}
