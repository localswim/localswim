import type { EntityTag, ProblemDto, Uuid } from "../api-types.js";
import { readBoardSummary } from "../board/read.js";
import {
  apiPrefix,
  entityTag,
  invalidResponse,
  isBoundedText,
  isRecord,
  isUnknownArray,
  isUuid,
  mutateForm,
  readDeadline,
  readJson,
  readSession,
} from "../http.js";
import { isAttachment, type Attachment } from "./attachment-data.js";
import { readFocusedCard } from "./read.js";

const maxAttachmentBytes = 10 * 1024 * 1024;

export interface AttachmentUploadTarget {
  readonly boardId: Uuid;
  readonly cardId: Uuid;
  readonly actorId: Uuid;
}

type JournalState = "ready" | "refused" | "unknown" | "committed";

interface FileIdentity {
  readonly id: Uuid;
  readonly requestId: Uuid;
  readonly name: string;
  readonly size: number;
  readonly digest: string;
}

type JournalFile = FileIdentity &
  (
    | { readonly state: "ready" | "refused" | "committed" }
    | { readonly state: "unknown"; readonly revision: number }
  );

interface PendingFile {
  saved: JournalFile;
  file?: File;
  message?: string;
}

interface InvalidFile {
  readonly id: Uuid;
  readonly name: string;
  readonly size: number;
  readonly message: string;
}

interface UploadBatch {
  readonly target: AttachmentUploadTarget;
  readonly files: Map<Uuid, PendingFile | InvalidFile>;
  busy: boolean;
  transient?: boolean;
  progress?: { readonly index: number; readonly total: number };
  inFlight?: Uuid;
  discarded?: boolean;
}

interface ActorUploads {
  readonly batches: Map<string, UploadBatch>;
  readonly readable: boolean;
}

interface UploadOptions {
  readonly canDispatch: (target: AttachmentUploadTarget) => boolean;
  readonly onChange: (target: AttachmentUploadTarget) => void;
  readonly onCommitted: (
    target: AttachmentUploadTarget,
    attachment: Attachment,
    recovered: boolean,
  ) => void;
  readonly storage?: Pick<Storage, "getItem" | "setItem" | "removeItem">;
}

export interface AttachmentUploadStatus {
  readonly id: Uuid;
  readonly name: string;
  readonly size: number;
  readonly status: JournalState | "needs-file" | "uploading" | "invalid";
  readonly message?: string;
  readonly canRemove: boolean;
  readonly canInspect: boolean;
  readonly canRetry: boolean;
  readonly canReselect: boolean;
}

function validName(name: unknown): name is string {
  return isBoundedText(name, 1, 1024);
}

function uploadForm(
  target: AttachmentUploadTarget,
  original: FileIdentity,
  cardEtag: EntityTag,
  bytes: ArrayBuffer,
): Blob {
  const boundary = `localswim-${crypto.randomUUID()}`;
  const parts = [
    ["id", original.id],
    ["card_id", target.cardId],
    ["expected_card_etag", cardEtag],
  ].map(
    ([name, value]) =>
      `--${boundary}\r\nContent-Disposition: form-data; name="${name}"\r\n\r\n${value}\r\n`,
  );
  // The native parser accepts RFC 2231 parameters. Keep raw filename bytes out
  // of the header and retain them exactly after UTF-8 percent decoding.
  const filename = encodeURIComponent(original.name).replace(
    /[!'()*]/gu,
    (character) => `%${character.charCodeAt(0).toString(16).toUpperCase()}`,
  );
  return new Blob(
    [
      ...parts,
      `--${boundary}\r\nContent-Disposition: form-data; name="file"; filename*=UTF-8''${filename}\r\nContent-Type: application/octet-stream\r\n\r\n`,
      bytes,
      `\r\n--${boundary}--\r\n`,
    ],
    { type: `multipart/form-data; boundary=${boundary}` },
  );
}

async function fileDigest(bytes: ArrayBuffer): Promise<string> {
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
  return Array.from(digest, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

function batchKey(boardId: Uuid, cardId: Uuid): string {
  return `${boardId}:${cardId}`;
}

function storageKey(actorId: Uuid): string {
  return `localswimAttachmentUploads:${actorId}`;
}

function emptyBatch(target: AttachmentUploadTarget): UploadBatch {
  return { target, files: new Map(), busy: false };
}

function parseSavedFile(value: unknown): JournalFile {
  if (
    !isRecord(value) ||
    !isUuid(value.id) ||
    !isUuid(value.requestId) ||
    !validName(value.name) ||
    typeof value.size !== "number" ||
    !Number.isSafeInteger(value.size) ||
    value.size < 0 ||
    value.size > maxAttachmentBytes ||
    typeof value.digest !== "string" ||
    !/^[0-9a-f]{64}$/u.test(value.digest)
  ) {
    return invalidResponse();
  }
  const { id, requestId, name, size, digest, state } = value;
  const identity = { id, requestId, name, size, digest };
  if (state === "ready" || state === "refused" || state === "committed") {
    if (Object.keys(value).length !== 6) return invalidResponse();
    return { ...identity, state };
  }
  if (
    state !== "unknown" ||
    Object.keys(value).length !== 7 ||
    typeof value.revision !== "number" ||
    !Number.isSafeInteger(value.revision) ||
    value.revision < 1
  ) {
    return invalidResponse();
  }
  return { ...identity, state, revision: value.revision };
}

function loadBatches(saved: string | null, actorId: Uuid): Map<string, UploadBatch> {
  const batches = new Map<string, UploadBatch>();
  const data: unknown = JSON.parse(saved ?? "[]");
  if (!isUnknownArray(data)) return invalidResponse();
  const ids = new Set<Uuid>();
  const requests = new Set<Uuid>();
  for (const batch of data) {
    if (
      !isRecord(batch) ||
      Object.keys(batch).length !== 3 ||
      !isUuid(batch.boardId) ||
      !isUuid(batch.cardId) ||
      !isUnknownArray(batch.files)
    ) {
      return invalidResponse();
    }
    const key = batchKey(batch.boardId, batch.cardId);
    if (batches.has(key)) return invalidResponse();
    const restored = emptyBatch({ actorId, boardId: batch.boardId, cardId: batch.cardId });
    for (const file of batch.files) {
      const metadata = parseSavedFile(file);
      if (ids.has(metadata.id) || requests.has(metadata.requestId)) return invalidResponse();
      ids.add(metadata.id);
      requests.add(metadata.requestId);
      restored.files.set(metadata.id, { saved: metadata });
    }
    if (restored.files.size > 0) batches.set(key, restored);
  }
  return batches;
}

async function readPreconditions(target: AttachmentUploadTarget, signal: AbortSignal) {
  const board = await readBoardSummary(target.boardId, signal);
  const { card, cardEtag } = await readFocusedCard(target.boardId, target.cardId, signal);
  const latest = await readBoardSummary(target.boardId, signal);
  if (
    board.etag !== latest.etag ||
    board.board.revision !== latest.board.revision ||
    card.revision > latest.board.revision ||
    card.archived ||
    card.completed_at !== null ||
    latest.board.availability !== "open" ||
    !latest.board.user_ids.includes(target.actorId)
  ) {
    return invalidResponse();
  }
  return { boardEtag: latest.etag, cardEtag, revision: latest.board.revision + 1 };
}

function exactAttachment(
  value: unknown,
  headers: Headers,
  target: AttachmentUploadTarget,
  file: FileIdentity,
): Attachment {
  entityTag(headers);
  if (
    !isAttachment(value) ||
    value.id !== file.id ||
    value.board_id !== target.boardId ||
    value.card_id !== target.cardId ||
    value.added_by !== target.actorId ||
    value.original_name !== file.name ||
    value.size !== file.size ||
    value.sha2_256 !== file.digest
  ) {
    return invalidResponse();
  }
  return value;
}

function uploadReceipt(
  value: unknown,
  headers: Headers,
  target: AttachmentUploadTarget,
  file: FileIdentity,
  revision: number,
  previousBoardEtag: EntityTag,
): Attachment {
  const attachment = exactAttachment(value, headers, target, file);
  if (
    !attachment.active ||
    attachment.revision !== revision ||
    attachment.created_at !== attachment.updated_at ||
    headers.get("Location") !== `${apiPrefix}/attachments/${file.id}` ||
    entityTag(headers, "X-Board-ETag") === previousBoardEtag
  ) {
    return invalidResponse();
  }
  return attachment;
}

function withState(file: JournalFile, state: "ready" | "refused" | "committed"): JournalFile {
  const { id, requestId, name, size, digest } = file;
  return { id, requestId, name, size, digest, state };
}

function refusalMessage(code: ProblemDto["code"]): string {
  switch (code) {
    case "unauthorized":
    case "forbidden":
      return "You can’t upload attachments to this card right now.";
    case "precondition_failed":
      return "The board changed. Retry this file after the board refreshes.";
    case "payload_too_large":
      return "The file exceeds the 10 MiB limit.";
    case "unavailable":
      return "The service is temporarily unavailable. Retry when it’s ready.";
    case "invalid_request":
    case "not_found":
    case "conflict":
    case "precondition_required":
    case "unsupported_media_type":
    case "method_not_allowed":
    case "not_acceptable":
    case "internal_error":
    case "outcome_unknown":
      return "The upload was refused. Retry this file or remove it.";
  }
}

export function createAttachmentUploads(options: UploadOptions) {
  const actors = new Map<Uuid, ActorUploads>();
  const storage = () => options.storage ?? sessionStorage;

  function actor(actorId: Uuid): ActorUploads {
    const cached = actors.get(actorId);
    if (cached !== undefined) return cached;
    let uploads: ActorUploads;
    try {
      uploads = {
        batches: loadBatches(storage().getItem(storageKey(actorId)), actorId),
        readable: true,
      };
    } catch {
      uploads = { batches: new Map(), readable: false };
    }
    actors.set(actorId, uploads);
    return uploads;
  }

  function batchFor(target: AttachmentUploadTarget): UploadBatch {
    const { batches } = actor(target.actorId);
    const key = batchKey(target.boardId, target.cardId);
    const found = batches.get(key);
    if (found !== undefined) return found;
    const batch = emptyBatch(target);
    batches.set(key, batch);
    return batch;
  }

  function save(actorId: Uuid): boolean {
    const { batches, readable } = actor(actorId);
    if (!readable) return false;
    try {
      const journal = [];
      for (const { target, files, transient } of batches.values()) {
        if (transient) continue;
        const saved = Array.from(files.values()).flatMap((file) =>
          "saved" in file ? [file.saved] : [],
        );
        if (!saved.some(({ state }) => state !== "committed")) continue;
        journal.push({ boardId: target.boardId, cardId: target.cardId, files: saved });
      }
      if (journal.length > 0) storage().setItem(storageKey(actorId), JSON.stringify(journal));
      else storage().removeItem(storageKey(actorId));
      return true;
    } catch {
      return false;
    }
  }

  function blocked(batch: UploadBatch): boolean {
    return Array.from(batch.files.values()).some(
      (item) => "saved" in item && item.saved.state === "unknown",
    );
  }

  async function run(
    target: AttachmentUploadTarget,
    action: (batch: UploadBatch) => Promise<void>,
  ) {
    const batch = batchFor(target);
    if (batch.busy) return;
    batch.busy = true;
    try {
      options.onChange(target);
      await action(batch);
    } finally {
      batch.busy = false;
      options.onChange(target);
    }
  }

  function committed(
    batch: UploadBatch,
    item: PendingFile,
    attachment: Attachment,
    recovered = false,
  ): void {
    item.saved = withState(item.saved, "committed");
    item.file = undefined;
    item.message = undefined;
    // If storage fails after a dispatch, its last record still requires original-ID inspection.
    save(batch.target.actorId);
    options.onCommitted(batch.target, attachment, recovered);
  }

  async function sendFile(batch: UploadBatch, item: PendingFile, file: File): Promise<boolean> {
    const { target } = batch;
    const original = item.saved;
    let bytes: ArrayBuffer;
    try {
      bytes = await file.arrayBuffer();
      if (bytes.byteLength !== original.size || (await fileDigest(bytes)) !== original.digest) {
        return invalidResponse();
      }
    } catch {
      item.file = undefined;
      item.message = "The original file could not be read unchanged. Reselect it.";
      return false;
    }
    if (batch.discarded) return false;
    const signal = readDeadline();
    const preconditions = await readPreconditions(target, signal).catch(() => undefined);
    if (preconditions === undefined) {
      if (batch.discarded) return false;
      item.saved = withState(original, "refused");
      item.message = "The current card could not be confirmed. Try again.";
      save(target.actorId);
      return false;
    }
    const { boardEtag, cardEtag, revision } = preconditions;
    const form = uploadForm(target, original, cardEtag, bytes);
    let failedToSave = false;
    const result = await mutateForm("/attachments", form, {
      boardEtag,
      requestId: original.requestId,
      successStatus: 201,
      signal,
      beforeDispatch(session) {
        if (
          batch.discarded ||
          session.principal_id !== target.actorId ||
          !options.canDispatch(target)
        )
          return invalidResponse();
        item.saved = { ...original, state: "unknown", revision };
        if (!save(target.actorId)) {
          item.saved = original;
          failedToSave = true;
          return invalidResponse();
        }
        item.message = undefined;
        batch.inFlight = original.id;
        options.onChange(target);
      },
      validate: (value, headers) =>
        uploadReceipt(value, headers, target, original, revision, boardEtag),
    });
    const dispatched = batch.inFlight === original.id;
    batch.inFlight = undefined;
    if (!dispatched) {
      item.message = failedToSave
        ? "Recovery information could not be saved. The file has not been sent."
        : "Upload is unavailable for this card right now. Try again.";
      return false;
    }
    if (result.outcome === "committed") {
      committed(batch, item, result.value);
      return true;
    }
    if (result.outcome === "not_applied") {
      item.saved = withState(original, "refused");
      item.message = refusalMessage(result.problem.code);
      save(target.actorId);
    }
    return false;
  }

  return {
    targets(actorId: Uuid): readonly AttachmentUploadTarget[] {
      return Array.from(actor(actorId).batches.values())
        .filter(({ files }) =>
          Array.from(files.values()).some(
            (file) => !("saved" in file) || file.saved.state !== "committed",
          ),
        )
        .map(({ target }) => target);
    },
    busy(target: AttachmentUploadTarget): boolean {
      return batchFor(target).busy;
    },
    inFlight(target: AttachmentUploadTarget): boolean {
      return batchFor(target).inFlight !== undefined;
    },
    progress(target: AttachmentUploadTarget): string | undefined {
      const batch = batchFor(target);
      const progress = batch.progress;
      if (!batch.inFlight || !progress) return undefined;
      return `Uploading attachment ${progress.index} of ${progress.total}…`;
    },
    inFlightBoard(boardId: Uuid, actorId: Uuid): boolean {
      return Array.from(actor(actorId).batches.values()).some(
        (batch) => batch.target.boardId === boardId && batch.inFlight !== undefined,
      );
    },
    rows(target: AttachmentUploadTarget): readonly AttachmentUploadStatus[] {
      const batch = batchFor(target);
      const waiting = blocked(batch);
      return Array.from(batch.files.values(), (item): AttachmentUploadStatus => {
        if (!("saved" in item)) {
          return {
            ...item,
            status: "invalid",
            canRemove: !batch.busy,
            canRetry: false,
            canInspect: false,
            canReselect: false,
          };
        }
        const { id, name, size, state } = item.saved;
        let status: AttachmentUploadStatus["status"] = state;
        if (state === "ready" || state === "refused") {
          if (!item.file) status = "needs-file";
        } else if (state === "unknown" && batch.inFlight === id) {
          status = "uploading";
        }
        return {
          id,
          name,
          size,
          status,
          message: item.message,
          canRemove: !batch.busy && state !== "unknown",
          canInspect: !batch.busy && state === "unknown",
          canRetry: !batch.busy && !waiting && (status === "ready" || status === "refused"),
          canReselect: !batch.busy && status === "needs-file",
        };
      });
    },
    stage(
      target: AttachmentUploadTarget,
      files: readonly File[],
      transient = false,
    ): Promise<void> {
      return run(target, async (batch) => {
        if (transient) batch.transient = true;
        for (const file of files) {
          const id = crypto.randomUUID();
          const { name, size } = file;
          let message: string | undefined;
          if (!validName(name)) message = "This filename could not be uploaded unchanged.";
          else if (size > maxAttachmentBytes) message = "The file exceeds the 10 MiB limit.";
          else {
            try {
              const digest = await fileDigest(await file.arrayBuffer());
              if (batch.discarded) return;
              batch.files.set(id, {
                saved: {
                  id,
                  requestId: crypto.randomUUID(),
                  name,
                  size,
                  digest,
                  state: "ready",
                },
                file,
              });
              if (batch.transient || save(target.actorId)) continue;
              message = "Recovery information could not be saved. Select this file again.";
            } catch {
              message = "The file could not be read. Select it again.";
            }
          }
          if (batch.discarded) return;
          batch.files.set(id, { id, name, size, message });
        }
      });
    },
    retain(target: AttachmentUploadTarget): boolean {
      const batch = batchFor(target);
      if (!batch.transient) return true;
      batch.transient = false;
      if (save(target.actorId)) return true;
      batch.transient = true;
      return false;
    },
    releaseToDraft(target: AttachmentUploadTarget): void {
      const batch = batchFor(target);
      if (batch.transient) return;
      batch.transient = true;
      if (!save(target.actorId)) batch.transient = false;
    },
    send(target: AttachmentUploadTarget, selection?: Uuid | ReadonlySet<Uuid>): Promise<void> {
      return run(target, async (batch) => {
        if (!options.canDispatch(target) || blocked(batch)) return;
        const selected = Array.from(batch.files).flatMap(([current, item]) => {
          const matches =
            selection === undefined ||
            (typeof selection === "string" ? selection === current : selection.has(current));
          if (!("saved" in item) || !item.file || !matches) return [];
          const { state } = item.saved;
          return state === "ready" || (state === "refused" && current === selection)
            ? [{ item, file: item.file }]
            : [];
        });
        try {
          for (const [index, { item, file }] of selected.entries()) {
            batch.progress = { index: index + 1, total: selected.length };
            if (!(await sendFile(batch, item, file))) break;
            options.onChange(target);
          }
        } finally {
          batch.progress = undefined;
        }
      });
    },
    inspect(target: AttachmentUploadTarget, id?: Uuid): Promise<void> {
      return run(target, async (batch) => {
        for (const [current, item] of batch.files) {
          if (!("saved" in item) || item.saved.state !== "unknown") continue;
          if (id !== undefined && current !== id) continue;
          const file = item.saved;
          try {
            const signal = readDeadline();
            const session = await readSession(signal);
            if (session.principal_id !== target.actorId) return;
            const { data, headers } = await readJson(
              `/attachments/${file.id}`,
              signal,
              target.actorId,
            );
            const attachment = exactAttachment(data, headers, target, file);
            if (
              attachment.active
                ? attachment.revision < file.revision ||
                  attachment.created_at !== attachment.updated_at
                : attachment.revision <= file.revision
            ) {
              invalidResponse();
            }
            committed(batch, item, attachment, true);
          } catch {
            // A 404 or unusable receipt cannot settle a previously dispatched upload.
            item.message = "The original upload is still unconfirmed. Check its result again.";
          }
        }
      });
    },
    reselect(target: AttachmentUploadTarget, id: Uuid, file: File): Promise<void> {
      return run(target, async ({ files }) => {
        const item = files.get(id);
        if (item === undefined || !("saved" in item) || item.file) return;
        const { name, size, digest, state } = item.saved;
        if (state !== "ready" && state !== "refused") return;
        try {
          if (
            name === file.name &&
            size === file.size &&
            digest === (await fileDigest(await file.arrayBuffer()))
          ) {
            item.file = file;
            item.message = undefined;
            return;
          }
        } catch {
          item.message = "The selected file could not be read.";
          return;
        }
        item.message = "Select the original file with the same name and contents.";
      });
    },
    remove(target: AttachmentUploadTarget, id: Uuid): void {
      const batch = batchFor(target);
      const item = batch.files.get(id);
      if (batch.busy || item === undefined || ("saved" in item && item.saved.state === "unknown"))
        return;
      batch.files.delete(id);
      if (!batch.transient && "saved" in item && !save(target.actorId)) {
        item.message = "The staged file could not be removed. Try again.";
        batch.files.set(id, item);
      }
      options.onChange(target);
    },
    discard(target: AttachmentUploadTarget): void {
      const batch = batchFor(target);
      if (blocked(batch)) return;
      const { batches } = actor(target.actorId);
      const key = batchKey(target.boardId, target.cardId);
      batches.delete(key);
      if (
        !batch.transient &&
        !save(target.actorId) &&
        Array.from(batch.files.values()).some((row) => "saved" in row)
      ) {
        batches.set(key, batch);
      } else {
        batch.discarded = true;
      }
      options.onChange(target);
    },
  };
}
