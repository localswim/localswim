import type { EntityTag, SessionDto, Uuid } from "../api-types.js";
import type { InspectionResult, MutationResult } from "../contracts.js";
import {
  apiPrefix,
  entityTag,
  invalidResponse,
  isBoundedText,
  isRecord,
  isTimestamp,
  isUuid,
  mutateJson,
  readJson,
} from "../http.js";
import type { DrawerComment } from "./contracts.js";

const maxCommentLength = 4_194_304;

export interface CommentPostInput {
  readonly commentId: Uuid;
  readonly requestId: Uuid;
  readonly boardId: Uuid;
  readonly cardId: Uuid;
  readonly cardEtag: EntityTag;
  readonly boardEtag: EntityTag;
  readonly authorId: Uuid;
  readonly text: string;
}

const journalKey = "localswimPendingComments";

export function readPendingComments(actor: Uuid): CommentPostInput[] {
  try {
    const saved: unknown = JSON.parse(sessionStorage.getItem(journalKey) ?? "[]");
    if (!Array.isArray(saved)) return [];
    return saved.filter(
      (item: unknown): item is CommentPostInput =>
        isRecord(item) &&
        item.authorId === actor &&
        isUuid(item.commentId) &&
        isUuid(item.requestId) &&
        isUuid(item.boardId) &&
        isUuid(item.cardId) &&
        isBoundedText(item.text, 1, maxCommentLength) &&
        typeof item.boardEtag === "string" &&
        typeof item.cardEtag === "string",
    );
  } catch {
    return [];
  }
}

export function writePendingComment(input: CommentPostInput, remove = false): void {
  const saved = readPendingComments(input.authorId).filter(
    ({ commentId }) => commentId !== input.commentId,
  );
  if (!remove) saved.push(input);
  try {
    sessionStorage.setItem(journalKey, JSON.stringify(saved));
  } catch (error) {
    if (!remove) throw error;
  }
}

export interface CommentReceipt {
  readonly comment: Omit<DrawerComment, "ticket">;
  readonly commentEtag: EntityTag;
}

export interface CommentPostReceipt extends CommentReceipt {
  readonly boardEtag: EntityTag;
}

function parseComment(data: unknown, headers: Headers, input: CommentPostInput): CommentReceipt {
  if (
    !isRecord(data) ||
    Object.keys(data).length !== 6 ||
    !isUuid(data.id) ||
    data.id !== input.commentId ||
    !isUuid(data.board_id) ||
    data.board_id !== input.boardId ||
    !isUuid(data.card_id) ||
    data.card_id !== input.cardId ||
    !isUuid(data.author_id) ||
    data.author_id !== input.authorId ||
    !isTimestamp(data.created_at) ||
    !isBoundedText(data.text, 1, maxCommentLength) ||
    data.text !== input.text
  ) {
    return invalidResponse();
  }
  const { id, board_id, card_id, author_id, created_at, text } = data;
  return {
    comment: { id, board_id, card_id, author_id, created_at, text },
    commentEtag: entityTag(headers),
  };
}

export function postComment(
  input: CommentPostInput,
  signal?: AbortSignal,
  beforeDispatch?: (session: SessionDto) => void,
): Promise<MutationResult<CommentPostReceipt>> {
  if (
    !isUuid(input.commentId) ||
    !isUuid(input.boardId) ||
    !isUuid(input.cardId) ||
    !isUuid(input.authorId) ||
    !isBoundedText(input.text, 1, maxCommentLength)
  ) {
    return invalidResponse();
  }
  return mutateJson("/comments", {
    method: "POST",
    successStatus: 201,
    requestId: input.requestId,
    boardEtag: input.boardEtag,
    body: {
      id: input.commentId,
      card_id: input.cardId,
      expected_card_etag: input.cardEtag,
      text: input.text,
    },
    signal,
    beforeDispatch(session) {
      if (session.principal_id !== input.authorId) invalidResponse();
      beforeDispatch?.(session);
    },
    validate(data, headers) {
      const receipt = parseComment(data, headers, input);
      const boardEtag = entityTag(headers, "X-Board-ETag");
      if (
        headers.get("Location") !== `${apiPrefix}/comments/${input.commentId}` ||
        boardEtag === input.boardEtag
      ) {
        return invalidResponse();
      }
      return { ...receipt, boardEtag };
    },
  });
}

export async function inspectComment(
  input: CommentPostInput,
  signal?: AbortSignal,
): Promise<InspectionResult<CommentReceipt>> {
  if (!isUuid(input.commentId)) return { outcome: "unknown" };
  try {
    const { data, headers } = await readJson(`/comments/${input.commentId}`, signal);
    return { outcome: "committed", value: parseComment(data, headers, input) };
  } catch {
    // A missing comment can still be in flight or fenced after an uncertain durable write.
    return { outcome: "unknown" };
  }
}

interface CommentComposerElements {
  readonly input: HTMLTextAreaElement;
  readonly submit: HTMLButtonElement;
  readonly status: HTMLElement;
  readonly inspect: HTMLButtonElement;
}

export interface CommentComposerState {
  readonly draft: string;
  readonly canEdit: boolean;
  readonly canSubmit: boolean;
  readonly canInspect: boolean;
  readonly status: string;
}

interface CommentComposerCallbacks {
  readonly onDraft: (text: string) => void;
  readonly onSubmit: () => void;
  readonly onInspect: () => void;
}

export function updateCommentComposer(
  elements: CommentComposerElements,
  state: CommentComposerState,
): void {
  const { input, submit, status, inspect } = elements;
  if (input.value !== state.draft) input.value = state.draft;
  input.disabled = false;
  input.readOnly = !state.canEdit;
  submit.disabled = !state.canSubmit || !isBoundedText(state.draft, 1, maxCommentLength);
  status.hidden = state.status === "";
  if (status.textContent !== state.status) status.textContent = state.status;
  inspect.hidden = !state.canInspect;
  inspect.disabled = !state.canInspect;
}

export function wireCommentComposer(
  elements: CommentComposerElements,
  getState: () => CommentComposerState,
  callbacks: CommentComposerCallbacks,
  signal: AbortSignal,
): void {
  const { input, submit, inspect } = elements;
  const listeners = { signal };
  function submitDraft(): void {
    const state = getState();
    if (state.canSubmit && isBoundedText(state.draft, 1, maxCommentLength)) callbacks.onSubmit();
  }
  input.addEventListener(
    "input",
    () => {
      if (getState().canEdit) callbacks.onDraft(input.value);
    },
    listeners,
  );
  input.addEventListener(
    "keydown",
    (event) => {
      if (event.defaultPrevented || event.isComposing || event.key !== "Enter" || event.shiftKey) {
        return;
      }
      event.preventDefault();
      if (!event.repeat) submitDraft();
    },
    listeners,
  );
  submit.addEventListener("click", submitDraft, listeners);
  inspect.addEventListener(
    "click",
    () => {
      if (getState().canInspect) callbacks.onInspect();
    },
    listeners,
  );
}
