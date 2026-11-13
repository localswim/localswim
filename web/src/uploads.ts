import { ApiError, MAX_ATTACHMENT_BYTES } from "../api.mjs";
import type { AppContext } from "./model.js";
import type { OperationStorage } from "./operations.js";
import {
  createUploadRecovery,
  hasPendingUpload,
  identifyUpload,
  matchUploads,
  MAX_UPLOAD_FILES,
  record,
  revisionOf,
  revisionTag,
  validUploadName,
  type Original,
  type UploadJournal,
} from "./upload-recovery.js";

export { hasPendingUpload, hasPendingBoardUploads } from "./upload-recovery.js";

type UploadContext = Readonly<{
  cardId: string;
  etag: string;
  readable: boolean;
  editable: boolean;
}>;

export type CreatedCardFiles = Readonly<{
  cardId: string;
  etag: string;
  files: readonly File[];
  signal: AbortSignal;
  onTransfer: () => void;
}>;

type UploadOptions = AppContext &
  Readonly<{
    boardId: string;
    pasteTarget: HTMLElement;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    canUpload: () => boolean;
    awaitUploadAdmission?: (signal: AbortSignal, confirmedCardRevision: number) => Promise<boolean>;
    beginWrite: (cardId: string) => (() => void) | null;
    beginInspect: (cardId: string) => (() => void) | null;
    onChange: () => void;
    storage?: OperationStorage;
  }>;

type Batch = {
  results: { name: string; outcome: string; id?: string }[];
  notice: string;
  busy: boolean;
};
type PendingFile = { file: File; result: Batch["results"][number]; original: Original };

const rasterTypes = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);
const waiting = "Waiting.";
const inspectNotice = "Inspect the saved upload before changing this card.";
const resumeNotice =
  "Reselect the remaining files to resume this batch, or discard the remaining uploads.";
const retainedNotice = "Resume with the staged files, or discard the remaining uploads.";
const retainedByDocument = new WeakMap<Document, Map<string, Map<string, File>>>();

export function pastedRasterFiles(event: ClipboardEvent) {
  const clipboard = event.clipboardData;
  if (
    !clipboard ||
    event.defaultPrevented ||
    (clipboard.types.includes("text/plain") &&
      event.target instanceof Element &&
      event.target.closest("input, textarea, [contenteditable]"))
  ) {
    return [];
  }
  return [...clipboard.files].filter((file) => rasterTypes.has(file.type.toLowerCase()));
}

function proveAttachment(
  reply: { data: object; etag: string | null },
  original: Original,
  boardId: string,
  cardId: string,
  expected: string,
  created: boolean,
) {
  const { data, etag } = reply;
  if (
    !record(data) ||
    data.id !== original.id ||
    data.board_id !== boardId ||
    data.card_id !== cardId ||
    data.original_name !== original.name ||
    data.size !== original.size ||
    data.sha2_256 !== original.digest ||
    typeof data.active !== "boolean" ||
    (created && !data.active) ||
    typeof data.revision !== "number" ||
    !Number.isSafeInteger(data.revision) ||
    data.revision <= revisionOf(cardId, expected) ||
    etag !== revisionTag(original.id, data.revision)
  ) {
    throw new ApiError("invalid_response", { outcomeUnknown: true });
  }
  return { revision: data.revision, active: data.active };
}

export function mountUploads(
  host: HTMLElement,
  {
    api,
    signal: appSignal,
    boardId,
    pasteTarget,
    readWithSession,
    canUpload,
    awaitUploadAdmission,
    beginWrite,
    beginInspect,
    onChange,
    storage,
  }: UploadOptions,
) {
  const doc = host.ownerDocument;
  const lifetime = new AbortController();
  const signal = AbortSignal.any([appSignal, lifetime.signal]);
  const recovery = createUploadRecovery(boardId, storage);
  const retained = retainedByDocument.get(doc) ?? new Map<string, Map<string, File>>();
  retainedByDocument.set(doc, retained);
  const batches = new Map<string, Batch>();
  let current: UploadContext | null = null;
  let selection = new AbortController();
  let pickerFiles: File[] = [];
  const label = doc.createElement("label");
  const picker = doc.createElement("input");
  const upload = doc.createElement("button");
  const inspect = doc.createElement("button");
  const resume = doc.createElement("button");
  const discard = doc.createElement("button");
  const status = doc.createElement("p");
  const outcomes = doc.createElement("ul");
  label.textContent = "Files to attach ";
  picker.type = "file";
  picker.multiple = true;
  label.append(picker);
  upload.type = "button";
  upload.textContent = "Upload attachments";
  inspect.type = "button";
  inspect.textContent = "Inspect saved upload";
  resume.type = "button";
  resume.textContent = "Resume remaining uploads";
  discard.type = "button";
  discard.textContent = "Discard remaining uploads";
  status.setAttribute("role", "status");
  host.replaceChildren(label, upload, inspect, status, outcomes, resume, discard);

  function blocksCard(cardId: string): boolean {
    return hasPendingUpload(boardId, cardId, storage);
  }

  function writable(context: UploadContext): boolean {
    return !signal.aborted && context.readable && context.editable && canUpload();
  }

  function retainedKey(cardId: string) {
    return `${boardId}/${cardId}`;
  }

  function retainedFiles(cardId: string, remaining: readonly Original[]) {
    const batch = retained.get(retainedKey(cardId));
    if (!batch) return [];
    const files: File[] = [];
    for (const { id } of remaining) {
      const file = batch.get(id);
      if (!file) return [];
      files.push(file);
    }
    return files;
  }

  function retain(cardId: string, files: readonly PendingFile[]) {
    const key = retainedKey(cardId);
    retained.delete(key);
    let count = files.length;
    for (const batch of retained.values()) count += batch.size;
    // Evict whole batches of in-memory bytes. Durable journals still support
    // exact reselection, and one evicted file cannot strand other cached files.
    for (const [oldest, batch] of retained) {
      if (count <= MAX_UPLOAD_FILES) break;
      retained.delete(oldest);
      count -= batch.size;
    }
    retained.set(key, new Map(files.map(({ original, file }) => [original.id, file])));
  }

  function forgetRetained(cardId: string, id: string) {
    const key = retainedKey(cardId);
    const batch = retained.get(key);
    if (!batch) return;
    batch.delete(id);
    if (!batch.size) retained.delete(key);
  }

  function render() {
    if (signal.aborted) return;
    const readable = current?.readable === true;
    const batch = current && readable ? batches.get(current.cardId) : undefined;
    const busy = batch?.busy === true;
    const saved = current && readable ? recovery.read(current.cardId) : null;
    const fenced = saved !== null && saved.journal?.expected !== null;
    const resumable = saved !== null && !fenced;
    const cached =
      current && saved?.journal && resumable
        ? retainedFiles(current.cardId, saved.journal.remaining).length > 0
        : false;
    const mayWrite = current !== null && writable(current);
    host.hidden = !readable || (!mayWrite && !saved && !batch);
    label.hidden = !mayWrite || fenced;
    upload.hidden = !mayWrite || saved !== null;
    picker.disabled = busy || label.hidden;
    upload.disabled = picker.disabled || pickerFiles.length === 0;
    inspect.hidden = !fenced;
    inspect.disabled = busy;
    resume.hidden = !resumable;
    resume.disabled = picker.disabled || (pickerFiles.length === 0 && !cached);
    discard.hidden = !resumable;
    discard.disabled = busy;
    status.textContent = [
      batch?.notice,
      fenced && !batch?.notice ? inspectNotice : "",
      resumable ? (cached ? retainedNotice : resumeNotice) : "",
    ]
      .filter(Boolean)
      .join(" ");
    const results = batch?.results ?? [];
    const listed = new Set(results.map(({ id }) => id));
    const savedResults = (saved?.journal?.remaining ?? []).flatMap(({ id, name }, index) =>
      listed.has(id)
        ? []
        : [{ name, outcome: fenced && index === 0 ? "Outcome uncertain." : "Remaining." }],
    );
    outcomes.replaceChildren(
      ...[...results, ...savedResults].map(({ name, outcome }) => {
        const row = doc.createElement("li");
        row.textContent = `${name} — ${outcome}`;
        return row;
      }),
    );
  }

  async function readCardTag(
    cardId: string,
    revision: number,
    readSignal: AbortSignal,
    requireActive = false,
  ) {
    const reply = await readWithSession(() => api.getCard(cardId, readSignal), readSignal);
    readSignal.throwIfAborted();
    const { data, etag } = reply;
    // An upload advances its attachment and card to the same revision.
    // A newer card revision belongs to another write, so stop this batch.
    if (
      !record(data) ||
      data.id !== cardId ||
      data.board_id !== boardId ||
      data.revision !== revision ||
      (requireActive && data.archived !== false) ||
      etag !== revisionTag(cardId, revision)
    ) {
      throw new TypeError("The saved card changed during upload.");
    }
    return etag;
  }

  async function submit(
    context: UploadContext,
    files: readonly File[],
    saved?: { raw: string; journal: UploadJournal },
    onCreatedTransfer?: () => void,
    batchSignal = signal,
  ) {
    const { cardId } = context;
    if (!writable(context) || batches.get(cardId)?.busy) return;
    const batch: Batch = (saved && batches.get(cardId)) ?? { results: [], notice: "", busy: false };
    batches.set(cardId, batch);
    if (files.length > MAX_UPLOAD_FILES) {
      batch.notice = `Select at most ${MAX_UPLOAD_FILES} files per batch.`;
      render();
      return;
    }
    const release = beginWrite(cardId);
    if (!release) {
      batch.notice = "The card is busy or unavailable. Try again when it is ready.";
      render();
      return;
    }
    const resumeSignal = AbortSignal.any([signal, selection.signal]);
    let refresh = false;
    let raw = saved?.raw ?? null;
    let keepAttempt = false;
    batch.busy = true;
    batch.notice = saved ? "Checking the selected files…" : "Preparing files…";
    render();
    try {
      let expected = context.etag;
      let revision = revisionOf(cardId, expected);
      const pending: PendingFile[] = [];
      if (saved) {
        const matched = await matchUploads(files, saved.journal.remaining, resumeSignal);
        resumeSignal.throwIfAborted();
        if (!matched) {
          batch.notice =
            "Reselect all remaining files with their recorded names, sizes and contents.";
          return;
        }
        for (const [index, original] of saved.journal.remaining.entries()) {
          const file = matched[index];
          if (!file) throw new TypeError("Missing matched upload.");
          const previous = batch.results.find(({ id }) => id === original.id);
          const result = previous ?? { id: original.id, name: original.name, outcome: waiting };
          result.outcome = waiting;
          if (!previous) batch.results.push(result);
          pending.push({ file, result, original });
        }
      } else {
        for (const file of files) {
          batchSignal.throwIfAborted();
          const result: Batch["results"][number] = { name: file.name, outcome: waiting };
          batch.results.push(result);
          if (file.size > MAX_ATTACHMENT_BYTES) {
            result.outcome = "Refused. Files are limited to 10 MiB.";
          } else if (!validUploadName(file.name)) {
            result.outcome = "Refused. File names must contain 1 to 1024 characters.";
          } else {
            const original = await identifyUpload(file);
            result.id = original.id;
            pending.push({ file, result, original });
          }
        }
      }
      let remaining = pending.map(({ original }) => original);
      batchSignal.throwIfAborted();
      if (!saved && remaining.length) {
        raw = recovery.save(cardId, null, { remaining, expected: null });
        if (onCreatedTransfer) retain(cardId, pending);
        onCreatedTransfer?.();
      }
      batch.notice = "";
      for (const [index, { file, result, original }] of pending.entries()) {
        batchSignal.throwIfAborted();
        const firstResume = saved && index === 0;
        const readSignal = firstResume ? resumeSignal : batchSignal;
        if (firstResume || index > 0) {
          expected = await readCardTag(cardId, revision, readSignal, !!onCreatedTransfer);
          if (awaitUploadAdmission && !(await awaitUploadAdmission(readSignal, revision))) {
            batch.notice = "Upload stopped because write access could not be confirmed.";
            break;
          }
        }
        if (readSignal.aborted || !canUpload()) {
          batch.notice = "Upload stopped because write access could not be confirmed.";
          break;
        }
        raw = recovery.save(cardId, raw, { remaining, expected });
        keepAttempt = true;
        result.outcome = "Uploading…";
        batch.notice = `Uploading file ${index + 1} of ${pending.length}…`;
        render();
        try {
          const reply = await api.uploadAttachment(cardId, expected, file, original.id);
          revision = proveAttachment(reply, original, boardId, cardId, expected, true).revision;
          result.outcome = "Uploaded.";
          refresh = true;
        } catch (error) {
          if (!(error instanceof ApiError) || error.outcomeUnknown || error.status === 409) {
            result.outcome = "Outcome uncertain.";
            batch.notice = inspectNotice;
            break;
          }
          result.outcome = error.status === 412 ? "Refused. The card changed." : "Upload refused.";
          if (saved || onCreatedTransfer || ![400, 413, 415, 422].includes(error.status ?? 0)) {
            raw = recovery.save(cardId, raw, { remaining, expected: null });
            keepAttempt = false;
            batch.notice = "Review the saved card before resuming the remaining uploads.";
            refresh = true;
            break;
          }
        }
        remaining = remaining.slice(1);
        raw = recovery.save(cardId, raw, { remaining, expected: null });
        forgetRetained(cardId, original.id);
        keepAttempt = false;
        batch.notice = "";
      }
    } catch {
      batch.notice = keepAttempt
        ? inspectNotice
        : "The selected files, saved card, or upload recovery details could not be verified.";
    } finally {
      for (const result of batch.results) {
        if (result.outcome === waiting) result.outcome = "Upload stopped before this file.";
      }
      batch.busy = false;
      release();
      if (refresh) onChange();
      render();
    }
  }

  async function submitCreated(created: CreatedCardFiles) {
    if (
      !awaitUploadAdmission ||
      !created.files.length ||
      created.files.length > MAX_UPLOAD_FILES ||
      created.files.some((file) => file.size > MAX_ATTACHMENT_BYTES || !validUploadName(file.name))
    ) {
      return false;
    }
    const { cardId, etag, files } = created;
    const batchSignal = AbortSignal.any([signal, created.signal]);
    let transferred = false;
    try {
      const revision = revisionOf(cardId, etag);
      await readCardTag(cardId, revision, batchSignal, true);
      if (
        !(await awaitUploadAdmission(batchSignal, revision)) ||
        batchSignal.aborted ||
        !canUpload()
      )
        return false;
      if (recovery.read(cardId) !== null) return false;
      await submit(
        { cardId, etag, readable: true, editable: true },
        files,
        undefined,
        () => {
          transferred = true;
          created.onTransfer();
        },
        batchSignal,
      );
    } catch {
      return transferred;
    }
    return transferred;
  }

  async function inspectOriginal(context: UploadContext) {
    const { cardId } = context;
    if (signal.aborted || !context.readable || !blocksCard(cardId) || batches.get(cardId)?.busy) {
      return;
    }
    const release = beginInspect(cardId);
    if (!release) return;
    const batch = batches.get(cardId) ?? { results: [], notice: "", busy: false };
    batches.set(cardId, batch);
    batch.busy = true;
    batch.notice = "Inspecting saved upload…";
    const readSignal = AbortSignal.any([signal, selection.signal]);
    render();
    try {
      const saved = recovery.read(cardId);
      if (!saved?.journal) throw new TypeError("Invalid saved upload.");
      const {
        expected,
        remaining: [original, ...rest],
      } = saved.journal;
      if (!original || expected === null) return;
      const reply = await readWithSession(
        () => api.getAttachment(original.id, readSignal),
        readSignal,
      );
      readSignal.throwIfAborted();
      const proof = proveAttachment(reply, original, boardId, cardId, expected, false);
      recovery.save(cardId, saved.raw, { remaining: rest, expected: null });
      forgetRetained(cardId, original.id);
      const outcome = proof.active
        ? "Uploaded. Confirmed by inspection."
        : "Uploaded, then detached.";
      const result = batch.results.find(({ id }) => id === original.id);
      if (result) result.outcome = outcome;
      else batch.results.push({ id: original.id, name: original.name, outcome });
      batch.notice = "Saved upload verified.";
      onChange();
    } catch (error) {
      // A missing original can race a write still in flight. Keep the fence.
      batch.notice =
        error instanceof ApiError && error.status === 404
          ? "The original upload was not found. Its outcome is still uncertain."
          : "The original upload could not be verified. Inspect it again.";
    } finally {
      batch.busy = false;
      release();
      render();
    }
  }

  function run(action: Promise<void>) {
    void action.catch((error: unknown) => globalThis.reportError(error));
  }

  function takeFiles() {
    const files = pickerFiles;
    pickerFiles = [];
    picker.value = "";
    return files;
  }

  picker.addEventListener(
    "change",
    () => {
      pickerFiles = [...(picker.files ?? [])];
      render();
    },
    { signal },
  );
  upload.addEventListener(
    "click",
    () => {
      if (!current || !pickerFiles.length || recovery.read(current.cardId) !== null) return;
      if (writable(current) && !batches.get(current.cardId)?.busy)
        run(submit(current, takeFiles()));
    },
    { signal },
  );
  inspect.addEventListener(
    "click",
    () => {
      if (current) run(inspectOriginal(current));
    },
    { signal },
  );
  resume.addEventListener(
    "click",
    () => {
      if (!current || !writable(current) || batches.get(current.cardId)?.busy) return;
      const saved = recovery.read(current.cardId);
      if (saved?.journal?.expected === null) {
        const key = retainedKey(current.cardId);
        const batch = retained.get(key);
        if (batch) {
          retained.delete(key);
          retained.set(key, batch);
        }
        const files = pickerFiles.length
          ? takeFiles()
          : retainedFiles(current.cardId, saved.journal.remaining);
        if (files.length) run(submit(current, files, { raw: saved.raw, journal: saved.journal }));
      }
    },
    { signal },
  );
  discard.addEventListener(
    "click",
    () => {
      if (signal.aborted || !current?.readable) return;
      const { cardId } = current;
      const saved = recovery.read(cardId);
      if (saved?.journal?.expected !== null || batches.get(cardId)?.busy) return;
      const release = beginInspect(cardId);
      if (!release) return;
      const batch = batches.get(cardId) ?? { results: [], notice: "", busy: false };
      batches.set(cardId, batch);
      try {
        recovery.save(cardId, saved.raw, null);
        takeFiles();
        const ids = new Set(saved.journal.remaining.map(({ id }) => id));
        retained.delete(retainedKey(cardId));
        batch.results = batch.results.filter(({ id }) => id === undefined || !ids.has(id));
        batch.notice = "Remaining uploads discarded.";
      } catch {
        batch.notice = "Could not discard the remaining uploads. Try again.";
      } finally {
        release();
        render();
      }
    },
    { signal },
  );
  pasteTarget.addEventListener(
    "paste",
    (event) => {
      if (
        !current ||
        !writable(current) ||
        recovery.read(current.cardId) !== null ||
        batches.get(current.cardId)?.busy
      )
        return;
      const files = pastedRasterFiles(event);
      if (!files.length) return;
      event.preventDefault();
      run(submit(current, files));
    },
    { signal },
  );
  render();

  return {
    show(context: UploadContext | null) {
      // A refresh can suspend readability while admission is being reconfirmed.
      // The selected card still owns its files until the drawer leaves that card.
      if (current?.cardId !== context?.cardId) {
        selection.abort();
        selection = new AbortController();
        takeFiles();
      }
      current = context;
      render();
    },
    blocksCard,
    submitCreated,
    dispose() {
      lifetime.abort();
    },
  };
}
