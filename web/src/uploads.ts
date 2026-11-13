import { ApiError, MAX_ATTACHMENT_BYTES } from "../api.mjs";
import type { AppContext } from "./model.js";
import type { OperationStorage } from "./operations.js";

type UploadContext = Readonly<{
  cardId: string;
  etag: string;
  readable: boolean;
  editable: boolean;
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

type Original = Readonly<{ id: string; name: string; size: number; digest: string }>;
type Journal = Readonly<{ remaining: readonly Original[]; expected: string | null }>;
type Batch = {
  results: { name: string; outcome: string; id?: string }[];
  notice: string;
  busy: boolean;
};

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const sha256 = /^[0-9a-f]{64}$/;
const rasterTypes = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);
const waiting = "Waiting.";

function key(boardId: string, cardId: string): string {
  return `localswim:v2:uploads:${boardId}:${cardId}`;
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function revisionTag(id: string, revision: number): string {
  return `"${id}-${revision}"`;
}

function revisionOf(id: string, etag: string): number {
  const revision = Number(etag.slice(id.length + 2, -1));
  if (!Number.isSafeInteger(revision) || revision < 1 || revisionTag(id, revision) !== etag) {
    throw new TypeError("Invalid upload validator.");
  }
  return revision;
}

function parseJournal(raw: string, cardId: string): Journal {
  const value: unknown = JSON.parse(raw);
  if (
    !record(value) ||
    !Array.isArray(value.remaining) ||
    value.remaining.length === 0 ||
    (value.expected !== null && typeof value.expected !== "string")
  ) {
    throw new TypeError("Invalid upload journal.");
  }
  if (value.expected !== null) revisionOf(cardId, value.expected);
  const ids = new Set<string>();
  const remaining = (value.remaining as unknown[]).map((item) => {
    if (!record(item)) throw new TypeError("Invalid upload original.");
    const { id, name, size, digest } = item;
    if (
      typeof id !== "string" ||
      !uuid.test(id) ||
      ids.has(id) ||
      typeof name !== "string" ||
      typeof size !== "number" ||
      !Number.isSafeInteger(size) ||
      size < 0 ||
      size > MAX_ATTACHMENT_BYTES ||
      typeof digest !== "string" ||
      !sha256.test(digest)
    ) {
      throw new TypeError("Invalid upload original.");
    }
    ids.add(id);
    return { id, name, size, digest };
  });
  return { remaining, expected: value.expected };
}

export function hasPendingUpload(
  boardId: string,
  cardId: string,
  storage?: Pick<OperationStorage, "getItem">,
): boolean {
  let raw: string | null;
  try {
    raw = (storage ?? globalThis.sessionStorage).getItem(key(boardId, cardId));
  } catch {
    return false;
  }
  if (raw === null) return false;
  try {
    return parseJournal(raw, cardId).expected !== null;
  } catch {
    return true;
  }
}

export function hasPendingBoardUploads(
  boardId: string,
  storage?: Pick<Storage, "getItem" | "key" | "length">,
): boolean {
  const prefix = key(boardId, "");
  try {
    const store = storage ?? globalThis.sessionStorage;
    const count = store.length;
    for (let index = 0; index < count; index++) {
      const entry = store.key(index);
      if (!entry?.startsWith(prefix)) continue;
      const cardId = entry.slice(prefix.length);
      if (cardId.length !== 36 || !uuid.test(cardId) || hasPendingUpload(boardId, cardId, store)) {
        return true;
      }
    }
  } catch {
    return false;
  }
  return false;
}

async function fingerprint(file: File): Promise<Original> {
  const id = globalThis.crypto.randomUUID();
  const hash = await globalThis.crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  const digest = Array.from(new Uint8Array(hash), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
  return { id, name: file.name, size: file.size, digest };
}

function multipartName(name: string): string {
  // FormData escapes quote, CR, and LF. The server's MIME parser also
  // removes quoted-pair backslashes before RFC 2045 special characters.
  return name
    .replaceAll('"', "%22")
    .replaceAll("\r", "%0D")
    .replaceAll("\n", "%0A")
    .replace(/\\([()[\]<>@,;:\\/?=])/g, "$1");
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
    data.original_name !== multipartName(original.name) ||
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
  const journal = () => storage ?? globalThis.sessionStorage;
  const batches = new Map<string, Batch>();
  let current: UploadContext | null = null;
  let selection = new AbortController();
  let pickerFiles: File[] = [];
  const label = doc.createElement("label");
  const picker = doc.createElement("input");
  const upload = doc.createElement("button");
  const inspect = doc.createElement("button");
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
  status.setAttribute("role", "status");
  host.replaceChildren(label, upload, inspect, status, outcomes);

  function blocksCard(cardId: string): boolean {
    return hasPendingUpload(boardId, cardId, storage);
  }

  function eligible(context: UploadContext): boolean {
    return !signal.aborted && context.editable && canUpload() && !blocksCard(context.cardId);
  }

  function render() {
    if (signal.aborted) return;
    const readable = current?.readable === true;
    const busy = current ? batches.get(current.cardId)?.busy === true : false;
    const fenced = current ? blocksCard(current.cardId) : false;
    const writable = current !== null && eligible(current);
    const batch = current && readable ? batches.get(current.cardId) : undefined;
    host.hidden = !readable || (!writable && !fenced && !batch);
    label.hidden = upload.hidden = !writable;
    picker.disabled = busy || !writable;
    upload.disabled = picker.disabled || pickerFiles.length === 0;
    inspect.hidden = !readable || !fenced;
    inspect.disabled = busy;
    status.textContent =
      batch?.notice || (fenced ? "Inspect the saved upload before changing this card." : "");
    outcomes.replaceChildren(
      ...(batch?.results ?? []).map(({ name, outcome }) => {
        const row = doc.createElement("li");
        row.textContent = `${name} — ${outcome}`;
        return row;
      }),
    );
  }

  function save(cardId: string, previous: string | null, next: Journal | null): string | null {
    const store = journal();
    const itemKey = key(boardId, cardId);
    if (store.getItem(itemKey) !== previous) throw new TypeError("Upload journal changed.");
    if (!next?.remaining.length) {
      store.removeItem(itemKey);
      return null;
    }
    const raw = JSON.stringify(next);
    store.setItem(itemKey, raw);
    return raw;
  }

  async function readCardTag(cardId: string, revision: number): Promise<string> {
    const reply = await readWithSession(() => api.getCard(cardId, signal), signal);
    signal.throwIfAborted();
    const { data, etag } = reply;
    // An upload advances its attachment and card to the same revision.
    // A newer card revision belongs to another write, so stop this batch.
    if (
      !record(data) ||
      data.id !== cardId ||
      data.board_id !== boardId ||
      data.revision !== revision ||
      etag !== revisionTag(cardId, revision)
    ) {
      throw new TypeError("The saved card changed during upload.");
    }
    return etag;
  }

  async function submit(context: UploadContext, files: File[]) {
    const { cardId } = context;
    if (!eligible(context) || batches.get(cardId)?.busy) return;
    const results: Batch["results"] = files.map((file) => ({
      name: file.name,
      outcome: file.size > MAX_ATTACHMENT_BYTES ? "Refused. Files are limited to 10 MiB." : waiting,
    }));
    const batch: Batch = { results, notice: "", busy: false };
    batches.set(cardId, batch);
    const accepted = files.flatMap((file, index) => {
      const result = results[index];
      return result?.outcome === waiting ? [{ file, result }] : [];
    });
    if (!accepted.length) {
      render();
      return;
    }
    const release = beginWrite(cardId);
    if (!release) {
      for (const { result } of accepted) result.outcome = "Upload did not start.";
      batch.notice = "The card is busy or unavailable. Try again when it is ready.";
      render();
      return;
    }
    let refresh = false;
    let raw: string | null = null;
    let keepAttempt = false;
    batch.busy = true;
    batch.notice = "Preparing files…";
    render();
    try {
      let expected = context.etag;
      let revision = revisionOf(cardId, expected);
      const pending = [];
      for (const entry of accepted) {
        pending.push({ ...entry, original: await fingerprint(entry.file) });
      }
      signal.throwIfAborted();
      const previous = journal().getItem(key(boardId, cardId));
      if (previous !== null && parseJournal(previous, cardId).expected !== null) {
        batch.notice = "Inspect the saved upload before changing this card.";
        return;
      }
      let remaining = pending.map(({ original }) => original);
      raw = save(cardId, previous, { remaining, expected: null });
      for (const [index, { file, result, original }] of pending.entries()) {
        signal.throwIfAborted();
        if (index > 0) {
          expected = await readCardTag(cardId, revision);
          if (awaitUploadAdmission && !(await awaitUploadAdmission(signal, revision))) {
            batch.notice = "Upload stopped because write access could not be confirmed.";
            break;
          }
        }
        if (signal.aborted || !canUpload()) {
          batch.notice = "Upload stopped because write access could not be confirmed.";
          break;
        }
        raw = save(cardId, raw, { remaining, expected });
        keepAttempt = true;
        result.id = original.id;
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
            batch.notice = "Inspect the saved upload before changing this card.";
            break;
          }
          result.outcome = error.status === 412 ? "Refused. The card changed." : "Upload refused.";
          if (![400, 413, 415, 422].includes(error.status ?? 0)) {
            batch.notice = "Review the saved card before selecting files again.";
            refresh = true;
            keepAttempt = false;
            break;
          }
        }
        remaining = remaining.slice(1);
        raw = save(cardId, raw, { remaining, expected: null });
        keepAttempt = false;
        batch.notice = "";
      }
    } catch {
      batch.notice = keepAttempt
        ? "Inspect the saved upload before changing this card."
        : "The selected files, saved card, or upload recovery details could not be verified.";
    } finally {
      // A known refusal is settled, while an ambiguous dispatch must retain its original ID.
      if (raw !== null && !keepAttempt) {
        try {
          save(cardId, raw, null);
        } catch {
          batch.notice = "Could not clear the saved upload details. Inspect the saved upload.";
        }
      }
      for (const result of results) {
        if (result.outcome === waiting) result.outcome = "Upload stopped before this file.";
      }
      batch.busy = false;
      release();
      if (refresh) onChange();
      render();
    }
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
      const raw = journal().getItem(key(boardId, cardId));
      if (raw === null) return;
      const {
        expected,
        remaining: [original],
      } = parseJournal(raw, cardId);
      if (!original || expected === null) return;
      const reply = await readWithSession(
        () => api.getAttachment(original.id, readSignal),
        readSignal,
      );
      readSignal.throwIfAborted();
      const proof = proveAttachment(reply, original, boardId, cardId, expected, false);
      save(cardId, raw, null);
      const outcome = proof.active
        ? "Uploaded. Confirmed by inspection."
        : "Uploaded, then detached.";
      const result = batch.results.find(({ id }) => id === original.id);
      if (result) result.outcome = outcome;
      else batch.results.push({ name: original.name, outcome });
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
      if (!current || !pickerFiles.length || !eligible(current)) return;
      const files = pickerFiles;
      pickerFiles = [];
      picker.value = "";
      run(submit(current, files));
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
  pasteTarget.addEventListener(
    "paste",
    (event) => {
      const clipboard = event.clipboardData;
      if (!current || !eligible(current) || !clipboard || event.defaultPrevented) return;
      if (
        event.target instanceof Element &&
        event.target.closest("input, textarea, [contenteditable]") &&
        clipboard.types.includes("text/plain")
      ) {
        return;
      }
      const files = [...clipboard.files].filter((file) => rasterTypes.has(file.type.toLowerCase()));
      if (!files.length || batches.get(current.cardId)?.busy) return;
      event.preventDefault();
      run(submit(current, files));
    },
    { signal },
  );
  render();

  return {
    show(context: UploadContext | null) {
      if (current?.cardId !== context?.cardId || (current?.readable && !context?.readable)) {
        selection.abort();
        selection = new AbortController();
        pickerFiles = [];
        picker.value = "";
      }
      current = context;
      render();
    },
    blocksCard,
    dispose() {
      lifetime.abort();
    },
  };
}
