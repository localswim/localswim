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
  type AuthorKind,
  type AuthorName,
  type CardScope,
  type TicketLink,
} from "./history.js";
import type { AppContext } from "./model.js";
import { createScopedOperations, submitOperation, type OperationStorage } from "./operations.js";
import {
  alignTextarea,
  applyInput,
  rememberExact,
  textareaText,
  type InputChange,
} from "./text-input.js";

type CommentContext = Readonly<{
  actorId: string;
  boardEtag: string;
  boardRevision: number;
  cardEtag: string;
  revision: number;
  commentCount: number;
  writable: boolean;
}>;

type CommentOptions = Readonly<{
  api: Pick<
    AppContext["api"],
    | "listComments"
    | "getComment"
    | "createComment"
    | "listFocusedCardActivity"
    | "getBoardAvailability"
    | "getCard"
  >;
  scope: CardScope;
  signal: AbortSignal;
  context: () => CommentContext | null;
  authorName: AuthorName;
  authorKind?: AuthorKind;
  ticketLink: TicketLink;
  readWithSession: <T>(read: () => Promise<T>, signal?: AbortSignal) => Promise<T>;
  beginWrite: (cardId: string) => (() => void) | null;
  onConfirmed: (boardId: string, cardId: string, boardEtag: string | null) => void;
  onStale: (boardId: string, cardId: string) => void;
  storage?: OperationStorage;
}>;

type SubmissionBase = Readonly<{
  boardEtag: string;
  boardRevision: number;
  cardEtag: string;
  cardRevision: number;
}>;
type Draft = Readonly<{ text: string; id: string | null; base?: SubmissionBase }>;
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

function versionMatches(etag: unknown, id: string, revision: unknown): boolean {
  return (
    typeof revision === "number" &&
    Number.isSafeInteger(revision) &&
    revision > 0 &&
    etag === `"${id}-${revision}"`
  );
}

function validBase(value: unknown, { boardId, cardId }: CardScope): value is SubmissionBase {
  return (
    typeof value === "object" &&
    value !== null &&
    "boardEtag" in value &&
    "boardRevision" in value &&
    "cardEtag" in value &&
    "cardRevision" in value &&
    versionMatches(value.boardEtag, boardId, value.boardRevision) &&
    versionMatches(value.cardEtag, cardId, value.cardRevision) &&
    typeof value.boardRevision === "number" &&
    typeof value.cardRevision === "number" &&
    value.boardRevision >= value.cardRevision
  );
}

function journalDraft(raw: string, scope: CardScope): Draft {
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
  const base = "base" in value && validBase(value.base, scope) ? value.base : undefined;
  return { text: value.text, id: value.id, base };
}

export function mountCardComments(
  root: HTMLElement,
  {
    api,
    scope,
    signal,
    context,
    authorName,
    authorKind,
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
  const retry = doc.createElement("button");
  retry.type = "button";
  retry.textContent = "Retry original post";
  retry.hidden = true;
  const dismiss = doc.createElement("button");
  dismiss.type = "button";
  dismiss.textContent = "Dismiss unresolved post";
  dismiss.hidden = true;
  const status = doc.createElement("p");
  status.className = "card-comment-status";
  status.setAttribute("role", "status");
  actions.append(post, inspect, retry, dismiss);
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
  type Comment = ReturnType<typeof readComment>;
  const renderEntry = (entry: Comment) =>
    renderComment(
      doc,
      entry,
      authorName,
      authorKind?.(entry.actor) === "human" ? ticketLink : undefined,
    );
  const attributionKey = (actor: string) => authorName(actor) ?? actor;
  const commentKey = (entry: Comment) =>
    JSON.stringify([attributionKey(entry.actor), authorKind?.(entry.actor)]);
  const commentHistory = mountHistory(
    region,
    signal,
    "All comments",
    (cursor, readSignal) => readComments(historyPageSize, cursor, readSignal),
    renderEntry,
    commentKey,
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
    (entry) => attributionKey(entry.actor),
  );

  let journal: Journal | undefined;
  let revision: number | undefined;
  let commentCount: number | undefined;
  let previewRead: AbortController | undefined;
  let previewItems: readonly Comment[] = [];
  let previewKey = "";
  let notice = "";
  let inspection: { raw: string; kind: "retry" | "dismiss" } | undefined;
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
        entry.draft = raw === null ? emptyDraft : journalDraft(raw, scope);
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
    if ([inspect, retry, dismiss].some((node) => doc.activeElement === node && node.hidden)) {
      (inspect.hidden ? input : inspect).focus({ preventScroll: true });
    }
  }

  function canWrite(current: CommentContext | null, entry: Journal | undefined) {
    return Boolean(
      current?.writable &&
      versionMatches(current.cardEtag, cardId, current.revision) &&
      versionMatches(current.boardEtag, boardId, current.boardRevision) &&
      current.boardRevision >= current.revision &&
      entry &&
      entry.key === journalKey(current.actorId, scope) &&
      !entry.error &&
      !operations.state(entry.key) &&
      entry.draft.text &&
      entry.draft.text.isWellFormed() &&
      !composition,
    );
  }

  function retryMatches(current: CommentContext | null, entry: Journal | undefined) {
    const base = entry?.draft.base;
    return Boolean(
      current &&
      base &&
      entry?.draft.id &&
      inspection?.kind === "retry" &&
      inspection.raw === entry.raw &&
      base.boardEtag === current.boardEtag &&
      base.boardRevision === current.boardRevision &&
      base.cardEtag === current.cardEtag &&
      base.cardRevision === current.revision,
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
        inspection = undefined;
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
    if (!composition) alignTextarea(input, textareaText(text));
    input.readOnly = !composition && (!entry || Boolean(pending || active));
    post.hidden = Boolean(pending);
    post.setAttribute("aria-disabled", String(!canWrite(current, entry) || Boolean(pending)));
    inspect.hidden = !pending;
    inspect.setAttribute("aria-disabled", String(Boolean(active)));
    retry.hidden = !retryMatches(current, entry);
    retry.setAttribute("aria-disabled", String(!canWrite(current, entry)));
    dismiss.hidden = !pending || inspection?.kind !== "dismiss" || inspection.raw !== entry?.raw;
    dismiss.setAttribute("aria-disabled", String(Boolean(active || entry?.error)));
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
    renderPreview();
    commentHistory.refreshRows();
    activityHistory.refreshRows();
  }

  function renderPreview() {
    const key = JSON.stringify(previewItems.map((entry) => [entry.id, commentKey(entry)]));
    if (key === previewKey) return;
    const focused = preview.contains(doc.activeElement);
    preview.replaceChildren(...previewItems.map(renderEntry));
    previewKey = key;
    if (focused) input.focus({ preventScroll: true });
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
      previewItems = page.items;
      previewKey = "";
      renderPreview();
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
    inspection = undefined;
    notice = "Comment posted.";
    refreshComments();
    activityHistory.reset();
    onConfirmed(boardId, cardId, boardEtag);
  }

  async function submit(retrying = false) {
    update();
    const current = context();
    const entry = journal;
    if (signal.aborted || !current || !entry) return;
    if (!canWrite(current, entry) || (retrying ? !retryMatches(current, entry) : entry.draft.id))
      return;
    const base = retrying
      ? entry.draft.base
      : {
          boardEtag: current.boardEtag,
          boardRevision: current.boardRevision,
          cardEtag: current.cardEtag,
          cardRevision: current.revision,
        };
    const id = retrying ? entry.draft.id : crypto.randomUUID();
    if (!base || !id) return;
    const draft = { text: entry.draft.text, id, base };
    const release = beginWrite(cardId);
    if (!release) return;
    try {
      await operations.run(
        entry.key,
        "submitting",
        async () => {
          notice = "";
          inspection = undefined;
          const result = await submitOperation(
            () => persist(entry, draft),
            async () => {
              publish(entry);
              const reply = await api.createComment(
                cardId,
                base.cardEtag,
                draft.text,
                draft.id,
                base.boardEtag,
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
            case "refused":
              if (retrying) {
                notice =
                  "The retry did not confirm the original post. Inspect it again to check its outcome.";
                onStale(boardId, cardId);
                break;
              }
              persist(entry, { text: draft.text, id: null });
              notice =
                result.kind === "stale"
                  ? "The card changed. Your comment draft is still available."
                  : "The post was refused. Your comment draft is still available.";
              if (result.kind === "stale") onStale(boardId, cardId);
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

  async function findOriginal(id: string) {
    try {
      return await readWithSession(() => api.getComment(id, signal), signal);
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) return null;
      throw error;
    }
  }

  async function readBoardVersion() {
    const { data, etag } = await readWithSession(
      () => api.getBoardAvailability(boardId, signal),
      signal,
    );
    if (data.id !== boardId || !versionMatches(etag, boardId, data.revision)) {
      throw new TypeError("Invalid board version for comment inspection.");
    }
    return { etag, revision: data.revision };
  }

  async function inspectOriginal() {
    update();
    const entry = journal;
    const actor = context()?.actorId;
    const draft = entry?.draft;
    const raw = entry?.raw;
    if (
      signal.aborted ||
      !entry ||
      !actor ||
      entry.key !== journalKey(actor, scope) ||
      !draft?.id ||
      typeof raw !== "string"
    ) {
      return;
    }
    const id = draft.id;
    await operations.run(
      entry.key,
      "inspecting",
      async () => {
        inspection = undefined;
        publish(entry);
        const currentActor = () => !signal.aborted && context()?.actorId === actor;
        try {
          let reply = await findOriginal(id);
          if (!currentActor()) return;
          if (!reply && draft.base) {
            const board = await readBoardVersion();
            if (!currentActor()) return;
            if (board.etag === draft.base.boardEtag) {
              const card = await readWithSession(() => api.getCard(cardId, signal), signal);
              if (!currentActor()) return;
              if (
                card.etag === draft.base.cardEtag &&
                "id" in card.data &&
                card.data.id === cardId &&
                "board_id" in card.data &&
                card.data.board_id === boardId
              ) {
                inspection = { raw, kind: "retry" };
              }
            } else if (board.revision > draft.base.boardRevision) {
              // The writer checks this board validator atomically with comment creation.
              // Read the original ID again after its original validator is fenced.
              reply = await findOriginal(id);
              if (!currentActor()) return;
              if (!reply) {
                // A membership change can also hide the original ID. Verify the
                // board remained readable at the same version around the 404.
                const after = await readBoardVersion();
                if (!currentActor()) return;
                if (after.etag === board.etag) inspection = { raw, kind: "dismiss" };
              }
            }
          }
          if (reply) {
            prove(reply, actor, draft);
            confirmed(entry, null);
          } else {
            notice =
              inspection?.kind === "dismiss"
                ? "The board changed and the original comment was not found. Dismiss the unresolved post to recover your draft."
                : retryMatches(context(), entry)
                  ? "The original comment was not found. You can retry the original post."
                  : "The original comment has not been found. Its outcome is still unknown. Inspect again to check.";
          }
        } catch {
          if (!currentActor()) return;
          notice =
            "The original comment could not be verified. Inspect it again to check the outcome.";
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
  retry.addEventListener("click", () => run(() => submit(true)), { signal });
  dismiss.addEventListener(
    "click",
    () => {
      update();
      const entry = journal;
      const actor = context()?.actorId;
      if (
        !actor ||
        !entry?.draft.id ||
        entry.key !== journalKey(actor, scope) ||
        inspection?.kind !== "dismiss" ||
        entry.raw !== inspection.raw ||
        operations.state(entry.key) ||
        entry.error
      ) {
        return;
      }
      if (persist(entry, { text: entry.draft.text, id: null })) {
        inspection = undefined;
        notice = "Unresolved post dismissed. Your exact draft is still available.";
      }
      update();
    },
    { signal },
  );
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
