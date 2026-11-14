import type { CardDto, EntityTag, MutableEntityDto, SessionDto, Uuid } from "../api-types.js";
import type { InspectionResult, MutationResult } from "../contracts.js";
import {
  entityTag,
  invalidResponse,
  isBoundedText,
  isRecord,
  isTimestamp,
  isUnknownArray,
  isUuid,
  mutateJson,
  readBinary,
  readDeadline,
  readJson,
} from "../http.js";
import type { DrawerActivity, DrawerData } from "./contracts.js";

const maxAttachmentBytes = 10 * 1024 * 1024;
const pageSize = 200;
const rasterTypes = new Set(["image/gif", "image/jpeg", "image/png", "image/webp"]);

export interface Attachment extends MutableEntityDto {
  readonly board_id: Uuid;
  readonly card_id: Uuid;
  readonly original_name: string;
  readonly size: number;
  readonly sha2_256: string;
  readonly media_type: string;
  readonly previewable: boolean;
  readonly active: boolean;
  readonly added_by: Uuid;
}

export function isAttachment(value: unknown): value is Attachment {
  return (
    isRecord(value) &&
    Object.keys(value).length === 13 &&
    isUuid(value.id) &&
    typeof value.revision === "number" &&
    Number.isSafeInteger(value.revision) &&
    value.revision > 0 &&
    isTimestamp(value.created_at) &&
    isTimestamp(value.updated_at) &&
    value.updated_at >= value.created_at &&
    isUuid(value.board_id) &&
    isUuid(value.card_id) &&
    isBoundedText(value.original_name, 1, 1024) &&
    typeof value.size === "number" &&
    Number.isSafeInteger(value.size) &&
    value.size >= 0 &&
    value.size <= maxAttachmentBytes &&
    typeof value.sha2_256 === "string" &&
    /^[0-9a-f]{64}$/u.test(value.sha2_256) &&
    isBoundedText(value.media_type, 1, 128) &&
    /^[\w!#$%&'*+.^`|~-]+\/[\w!#$%&'*+.^`|~-]+(?:;[ -~]+)?$/u.test(value.media_type) &&
    value.previewable === rasterTypes.has(value.media_type) &&
    typeof value.active === "boolean" &&
    isUuid(value.added_by)
  );
}

function belongsTo(attachment: Attachment, card: CardDto): boolean {
  return attachment.board_id === card.board_id && attachment.card_id === card.id;
}

export async function readAttachments(
  card: CardDto,
  boardRevision: number,
  signal?: AbortSignal,
): Promise<readonly Attachment[]> {
  const deadline = readDeadline(signal);
  const query = new URLSearchParams({ card_id: card.id, limit: String(pageSize) });
  const rows: Attachment[] = [];
  const ids = new Set<Uuid>();
  const cursors = new Set<string>();
  const pageCount = Math.max(1, Math.ceil(card.attachment_count / pageSize));
  for (let page = 0; page < pageCount; page += 1) {
    const { data, headers } = await readJson(`/attachments?${query}`, deadline);
    entityTag(headers);
    if (
      !isRecord(data) ||
      Object.keys(data).length !== 2 ||
      !isUnknownArray(data.attachments) ||
      data.attachments.length !== Math.min(pageSize, card.attachment_count - rows.length)
    ) {
      return invalidResponse();
    }
    for (const item of data.attachments) {
      if (
        !isAttachment(item) ||
        !belongsTo(item, card) ||
        !item.active ||
        item.revision > boardRevision ||
        item.revision > card.revision ||
        ids.has(item.id)
      ) {
        return invalidResponse();
      }
      ids.add(item.id);
      rows.push(item);
    }
    const cursor = data.next_cursor;
    if (page === pageCount - 1) {
      if (
        cursor !== null ||
        (card.cover_attachment_id !== null &&
          !rows.some((item) => item.id === card.cover_attachment_id && item.previewable))
      ) {
        return invalidResponse();
      }
      return rows;
    }
    if (!isBoundedText(cursor, 1, 2048) || cursors.has(cursor)) return invalidResponse();
    cursors.add(cursor);
    query.set("cursor", cursor);
  }
  return invalidResponse();
}

async function readAttachment(
  card: CardDto,
  id: Uuid,
  signal?: AbortSignal,
): Promise<{ readonly attachment: Attachment; readonly etag: EntityTag }> {
  if (!isUuid(id)) return invalidResponse();
  const { data, headers } = await readJson(`/attachments/${id}`, signal);
  if (!isAttachment(data) || data.id !== id || !belongsTo(data, card)) return invalidResponse();
  return { attachment: data, etag: entityTag(headers) };
}

async function readContentBytes(
  body: ReadableStream<Uint8Array> | null,
  size: number,
): Promise<Uint8Array<ArrayBuffer>> {
  if (body === null && size !== 0) return invalidResponse();
  const bytes = new Uint8Array(size);
  if (body === null) return bytes;
  const reader = body.getReader();
  let offset = 0;
  try {
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      if (chunk.value.byteLength > size - offset) return invalidResponse();
      bytes.set(chunk.value, offset);
      offset += chunk.value.byteLength;
    }
    if (offset !== size) return invalidResponse();
    return bytes;
  } finally {
    reader.releaseLock();
  }
}

export function readAttachmentContent(
  attachment: Attachment,
  disposition: "inline" | "attachment",
  signal?: AbortSignal,
): Promise<Blob> {
  if (!isAttachment(attachment) || !attachment.active) return invalidResponse();
  if (disposition === "inline" && !attachment.previewable) return invalidResponse();
  const media = attachment.previewable ? attachment.media_type : "application/octet-stream";
  return readBinary(
    `/attachments/${attachment.id}/content?disposition=${disposition}`,
    media,
    async (response, deadline) => {
      entityTag(response.headers);
      if (
        response.headers.get("Content-Type") !== media ||
        response.headers.get("Content-Length") !== String(attachment.size) ||
        !response.headers.get("Content-Disposition")?.startsWith(`${disposition};`)
      ) {
        return invalidResponse();
      }
      const bytes = await readContentBytes(response.body, attachment.size);
      const hash = await crypto.subtle.digest("SHA-256", bytes);
      const digest = Array.from(new Uint8Array(hash), (byte) =>
        byte.toString(16).padStart(2, "0"),
      ).join("");
      deadline.throwIfAborted();
      if (digest !== attachment.sha2_256) return invalidResponse();
      return new Blob([bytes], { type: media });
    },
    signal,
  );
}

export interface AttachmentWrite {
  readonly drawer: DrawerData;
  readonly actorId: Uuid;
  readonly action:
    | { readonly kind: "cover"; readonly attachment: Attachment | null }
    | { readonly kind: "detach"; readonly attachment: Attachment };
}

interface AttachmentWriteReceipt {
  readonly boardEtag: EntityTag;
}

function sameAttachment(left: Attachment, right: Attachment): boolean {
  return (
    left.id === right.id &&
    left.board_id === right.board_id &&
    left.card_id === right.card_id &&
    left.original_name === right.original_name &&
    left.size === right.size &&
    left.sha2_256 === right.sha2_256 &&
    left.media_type === right.media_type &&
    left.previewable === right.previewable &&
    left.created_at === right.created_at &&
    left.added_by === right.added_by
  );
}

function readReceipt(headers: Headers, baseTag: EntityTag): AttachmentWriteReceipt {
  const boardEtag = entityTag(headers, "X-Board-ETag");
  if (boardEtag === baseTag) return invalidResponse();
  return { boardEtag };
}

export async function requestAttachmentWrite(
  write: AttachmentWrite,
  signal?: AbortSignal,
  beforeDispatch?: (session: SessionDto) => void,
): Promise<MutationResult<AttachmentWriteReceipt>> {
  const { drawer, action } = write;
  const { card, cardEtag, boardEtag, boardRevision } = drawer;
  if (
    !isUuid(write.actorId) ||
    (action.attachment !== null &&
      (!isAttachment(action.attachment) ||
        !action.attachment.active ||
        !belongsTo(action.attachment, card) ||
        action.attachment.revision > card.revision))
  ) {
    return invalidResponse();
  }
  const deadline = readDeadline(signal);
  const options = {
    boardEtag,
    signal: deadline,
    beforeDispatch(session: SessionDto) {
      if (session.principal_id !== write.actorId) invalidResponse();
      beforeDispatch?.(session);
    },
  };
  if (action.kind === "detach") {
    const selected = action.attachment;
    const { attachment, etag } = await readAttachment(card, selected.id, deadline);
    if (
      !attachment.active ||
      attachment.revision !== selected.revision ||
      attachment.updated_at !== selected.updated_at ||
      !sameAttachment(attachment, selected)
    ) {
      return invalidResponse();
    }
    return mutateJson(`/attachments/${selected.id}`, {
      ...options,
      method: "DELETE",
      etag,
      body: undefined,
      successStatus: 204,
      validate: (_data, headers) => readReceipt(headers, boardEtag),
    });
  }
  const nextCover = action.attachment?.id ?? null;
  if (nextCover === card.cover_attachment_id || action.attachment?.previewable === false)
    return invalidResponse();
  const { isFocusedCard } = await import("./read.js");
  return mutateJson(`/cards/${card.id}`, {
    ...options,
    method: "PATCH",
    etag: cardEtag,
    body: { cover_attachment_id: nextCover },
    validate(value, headers) {
      if (
        !isFocusedCard(value) ||
        value.id !== card.id ||
        value.board_id !== card.board_id ||
        value.revision !== boardRevision + 1 ||
        value.cover_attachment_id !== nextCover ||
        value.attachment_count !== card.attachment_count ||
        entityTag(headers) === cardEtag
      ) {
        return invalidResponse();
      }
      return readReceipt(headers, boardEtag);
    },
  });
}

function isCoverChange(event: DrawerActivity, card: CardDto, nextCover: Uuid | null): boolean {
  return (
    event.operation === "patch" &&
    event.resource_type === "card" &&
    event.resource_id === card.id &&
    event.field === "cover_attachment_id" &&
    event.before === card.cover_attachment_id &&
    event.after === nextCover
  );
}

export async function inspectAttachmentWrite(
  write: AttachmentWrite,
  signal?: AbortSignal,
): Promise<InspectionResult<AttachmentWriteReceipt>> {
  const { drawer, actorId, action } = write;
  const { card, boardRevision, boardEtag } = drawer;
  const deadline = readDeadline(signal);
  try {
    const { readDrawer } = await import("./read.js");
    const latest = await readDrawer(card.board_id, card.id, deadline);
    const revision = boardRevision + 1;
    const original = latest.activity.filter((event) => event.revision === revision);
    if (latest.boardEtag === boardEtag || original.some((event) => event.actor_id !== actorId)) {
      return { outcome: "unknown" };
    }
    if (action.kind === "cover") {
      // Detaching the selected cover emits the same cover change alongside a detach event.
      const event = original[0];
      if (
        original.length === 1 &&
        event !== undefined &&
        isCoverChange(event, card, action.attachment?.id ?? null)
      ) {
        return { outcome: "committed", value: { boardEtag: latest.boardEtag } };
      }
    } else {
      const id = action.attachment.id;
      const clearedCover = card.cover_attachment_id === id;
      const detached = original.find(
        (event) =>
          event.operation === "detach" &&
          event.resource_type === "attachment" &&
          event.resource_id === id &&
          event.field === null &&
          event.before === null &&
          event.after === null,
      );
      if (
        detached !== undefined &&
        original.length === (clearedCover ? 2 : 1) &&
        (!clearedCover || original.some((event) => isCoverChange(event, card, null)))
      ) {
        const { attachment } = await readAttachment(card, id, deadline);
        if (
          !attachment.active &&
          attachment.revision === revision &&
          attachment.updated_at === detached.at &&
          sameAttachment(attachment, action.attachment)
        ) {
          return { outcome: "committed", value: { boardEtag: latest.boardEtag } };
        }
      }
    }
  } catch {
    // A failed read cannot determine the outcome of the original request.
  }
  return { outcome: "unknown" };
}
