import { ApiError } from "../api.mjs";
import type { AppContext } from "./model.js";
import { submitOperation, type OperationStorage } from "./operations.js";

type Attachment = Readonly<{
  id: string;
  board_id: string;
  card_id: string;
  original_name: string;
  size: number;
  sha2_256: string;
  media_type: string;
  created_at: string;
  revision: number;
  active: boolean;
  previewable: boolean;
}>;

type AttachmentAccess = AppContext &
  Readonly<{
    boardId: string;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
  }>;

export type AttachmentContext = Readonly<{
  cardId: string;
  etag: string;
  attachmentCount: number;
  coverAttachmentId: string | null;
  editable: boolean;
}>;

type AttachmentOptions = AttachmentAccess &
  Readonly<{
    getBoardEtag: () => string | null | undefined;
    beginWrite: (cardId: string) => (() => void) | null;
    beginInspect: (cardId: string) => (() => void) | null;
    onChange: () => void;
    storage?: OperationStorage;
  }>;

type CoverOptions = AttachmentAccess & Pick<AttachmentContext, "cardId" | "coverAttachmentId">;

type Mutation = { kind: "cover"; cover: string | null } | { kind: "detach"; id: string };
type SavedMutation =
  | { kind: "cover"; cover: string | null; cardEtag: string }
  | { kind: "detach"; id: string; revision: number; boardEtag: string };

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function tagRevision(tag: unknown, id: string): number | undefined {
  if (typeof tag !== "string" || !tag.startsWith(`"${id}-`) || !tag.endsWith('"')) {
    return undefined;
  }
  const version = tag.slice(id.length + 2, -1);
  const revision = Number(version);
  return Number.isSafeInteger(revision) && revision > 0 && String(revision) === version
    ? revision
    : undefined;
}

function readMutation(raw: string, cardId: string, boardId: string): SavedMutation | undefined {
  let saved: unknown;
  try {
    saved = JSON.parse(raw);
  } catch {
    return undefined;
  }
  if (typeof saved !== "object" || saved === null || !("kind" in saved)) return undefined;
  if (
    saved.kind === "detach" &&
    "id" in saved &&
    typeof saved.id === "string" &&
    uuid.test(saved.id) &&
    "revision" in saved &&
    typeof saved.revision === "number" &&
    Number.isSafeInteger(saved.revision) &&
    saved.revision > 0 &&
    "boardEtag" in saved &&
    typeof saved.boardEtag === "string" &&
    tagRevision(saved.boardEtag, boardId) !== undefined &&
    Object.keys(saved).length === 4
  ) {
    return { kind: "detach", id: saved.id, revision: saved.revision, boardEtag: saved.boardEtag };
  }
  if (
    saved.kind === "cover" &&
    "cover" in saved &&
    (saved.cover === null || (typeof saved.cover === "string" && uuid.test(saved.cover))) &&
    "cardEtag" in saved &&
    typeof saved.cardEtag === "string" &&
    tagRevision(saved.cardEtag, cardId) !== undefined &&
    Object.keys(saved).length === 3
  ) {
    return { kind: "cover", cover: saved.cover, cardEtag: saved.cardEtag };
  }
  return undefined;
}

type Selection = {
  context: AttachmentContext;
  lifetime: AbortController;
  signal: AbortSignal;
  entries: Attachment[] | undefined;
  reading: boolean;
  awaitingRefresh: boolean;
  message: string;
};

function scoped(item: Attachment, boardId: string, cardId: string): boolean {
  return item.board_id === boardId && item.card_id === cardId;
}

function unavailable(error: unknown): boolean {
  return error instanceof ApiError && [401, 403, 404].includes(error.status ?? 0);
}

async function* attachmentPages(
  access: AttachmentAccess,
  cardId: string,
  signal: AbortSignal,
): AsyncGenerator<Attachment[]> {
  const { api, boardId, readWithSession } = access;
  const ids = new Set<string>();
  const cursors = new Set<string>();
  let cursor: string | undefined;
  for (;;) {
    const { data } = await readWithSession(
      () => api.listAttachments(cardId, cursor, signal),
      signal,
    );
    signal.throwIfAborted();
    // The API adapter validates metadata and the card identity for every row.
    const { attachments, next_cursor: next } = data as {
      attachments: Attachment[];
      next_cursor: unknown;
    };
    for (const item of attachments) {
      if (!scoped(item, boardId, cardId) || !item.active || ids.has(item.id)) {
        throw new ApiError("invalid_response");
      }
      ids.add(item.id);
    }
    if (
      next !== null &&
      (typeof next !== "string" ||
        !next ||
        next.length > 2048 ||
        cursors.has(next) ||
        attachments.length === 0)
    ) {
      throw new ApiError("invalid_response");
    }
    yield attachments;
    if (next === null) return;
    cursors.add(next);
    cursor = next;
  }
}

async function readContent(
  { api, readWithSession }: AttachmentAccess,
  entry: Attachment,
  disposition: "attachment" | "inline",
  signal: AbortSignal,
): Promise<Blob> {
  signal.throwIfAborted();
  // The adapter authenticates the read and verifies the original size and SHA-256.
  const blob = await readWithSession(
    () => api.getAttachmentContent(entry.id, disposition, entry.size, entry.sha2_256, signal),
    signal,
  );
  signal.throwIfAborted();
  const media = entry.media_type.split(";", 1)[0]?.trim().toLowerCase();
  if (disposition === "inline" && (!entry.previewable || blob.type !== media)) {
    throw new ApiError("invalid_response");
  }
  return blob;
}

async function thumbnail(
  root: HTMLElement,
  original: Blob,
  maxHeight: number,
  signal: AbortSignal,
) {
  const bitmap = await createImageBitmap(original);
  try {
    signal.throwIfAborted();
    const doc = root.ownerDocument;
    const maxWidth = Math.min(320, root.clientWidth || root.parentElement?.clientWidth || 320);
    const dpr = Math.max(1, doc.defaultView?.devicePixelRatio || 1);
    const fit = Math.min(maxWidth / bitmap.width, maxHeight / bitmap.height);
    const scale = Math.min(1, fit * dpr);
    const canvas = doc.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    const context = canvas.getContext("2d");
    if (!context) throw new Error("Image canvas unavailable");
    context.imageSmoothingQuality = "high";
    context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob(
        (image) => {
          if (image) resolve(image);
          else reject(new Error("Thumbnail unavailable"));
        },
        "image/webp",
        0.85,
      );
    });
    signal.throwIfAborted();
    const cssScale = Math.min(fit, 1);
    return {
      blob,
      width: Math.max(1, Math.round(bitmap.width * cssScale)),
      height: Math.max(1, Math.round(bitmap.height * cssScale)),
    };
  } finally {
    bitmap.close();
  }
}

function objectUrl(blob: Blob, signal: AbortSignal) {
  signal.throwIfAborted();
  const url = URL.createObjectURL(blob);
  let released = false;
  function release() {
    if (released) return;
    released = true;
    signal.removeEventListener("abort", release);
    URL.revokeObjectURL(url);
  }
  signal.addEventListener("abort", release, { once: true });
  return { url, release };
}

async function showThumbnail(
  root: HTMLElement,
  image: HTMLImageElement,
  blob: Blob,
  maxHeight: number,
  signal: AbortSignal,
) {
  const derivative = await thumbnail(root, blob, maxHeight, signal);
  const { url, release } = objectUrl(derivative.blob, signal);
  image.width = derivative.width;
  image.height = derivative.height;
  image.src = url;
  try {
    await image.decode();
    signal.throwIfAborted();
  } catch (error) {
    release();
    throw error;
  }
}

export function mountAttachmentCover(root: HTMLElement, options: CoverOptions) {
  const { api, boardId, cardId, coverAttachmentId, readWithSession } = options;
  const lifetime = new AbortController();
  const signal = AbortSignal.any([options.signal, lifetime.signal]);
  const image = root.ownerDocument.createElement("img");
  image.className = "board-view-card-cover";
  image.alt = "";
  image.draggable = false;
  let observer: IntersectionObserver | undefined;

  function dispose() {
    lifetime.abort();
    observer?.disconnect();
    image.removeAttribute("src");
    image.remove();
  }

  async function select(): Promise<Attachment | undefined> {
    if (coverAttachmentId) {
      try {
        const { data } = await readWithSession(
          () => api.getAttachment(coverAttachmentId, signal),
          signal,
        );
        const item = data as Attachment;
        if (!scoped(item, boardId, cardId)) throw new ApiError("invalid_response");
        if (item.active && item.previewable) return item;
      } catch (error) {
        if (!(error instanceof ApiError) || error.status !== 404) throw error;
      }
    }
    for await (const page of attachmentPages(options, cardId, signal)) {
      const first = page.find((item) => item.previewable);
      if (first) return first;
    }
    return undefined;
  }

  async function load() {
    try {
      const entry = await select();
      if (!entry) {
        dispose();
        return;
      }
      const blob = await readContent(options, entry, "inline", signal);
      await showThumbnail(root, image, blob, 96, signal);
      if (!signal.aborted) root.append(image);
    } catch {
      dispose();
    }
  }

  if (signal.aborted) return dispose;
  options.signal.addEventListener("abort", dispose, { signal });
  const Observe = root.ownerDocument.defaultView?.IntersectionObserver;
  if (Observe) {
    observer = new Observe((entries) => {
      if (signal.aborted || !entries.some((entry) => entry.isIntersecting)) return;
      observer?.disconnect();
      void load();
    });
    observer.observe(root.parentElement ?? root);
  } else {
    void load();
  }
  return dispose;
}

function downloadName(name: string): string {
  const safe = name.replace(/[<>:"/\\|?*\p{Cc}\p{Cf}]/gu, "_").replace(/^[ .]+|[ .]+$/g, "");
  if (!safe) return "attachment";
  return /^(?:con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³])(?:\s*\.|$)/i.test(safe) ? "_" + safe : safe;
}

export function mountAttachments(root: HTMLElement, options: AttachmentOptions) {
  const { api, boardId, readWithSession, getBoardEtag, beginWrite, beginInspect, onChange } =
    options;
  const doc = root.ownerDocument;
  const lifetime = new AbortController();
  const signal = AbortSignal.any([options.signal, lifetime.signal]);
  const section = doc.createElement("section");
  section.className = "card-attachments";
  section.setAttribute("aria-label", "Attachments");
  const heading = doc.createElement("h3");
  heading.className = "card-drawer-heading";
  heading.textContent = "Attachments";
  const status = doc.createElement("p");
  status.setAttribute("role", "status");
  const retry = doc.createElement("button");
  retry.type = "button";
  retry.textContent = "Retry attachments";
  const inspect = doc.createElement("button");
  inspect.type = "button";
  inspect.textContent = "Inspect attachments";
  const list = doc.createElement("ul");
  list.className = "card-attachment-list";
  section.append(heading, status, retry, inspect, list);
  section.hidden = true;
  root.append(section);
  let selection: Selection | undefined;
  let rows: AbortController | undefined;
  const writing = new Set<string>();

  function storage() {
    return options.storage ?? doc.defaultView?.sessionStorage;
  }

  function operationKey(cardId: string) {
    return `localswim:v2:attachments:${boardId}:${cardId}`;
  }

  function pending(cardId: string) {
    try {
      return storage()?.getItem(operationKey(cardId)) ?? null;
    } catch {
      return null;
    }
  }

  function clearPending(cardId: string, raw: string) {
    try {
      const store = storage();
      const key = operationKey(cardId);
      if (!store || store.getItem(key) !== raw) return false;
      store.removeItem(key);
      return store.getItem(key) === null;
    } catch {
      return false;
    }
  }

  function journal(cardId: string, raw: string) {
    try {
      const store = storage();
      const key = operationKey(cardId);
      if (!store || store.getItem(key) !== null) return false;
      store.setItem(key, raw);
      return store.getItem(key) === raw;
    } catch {
      return false;
    }
  }

  function active(current: Selection) {
    return selection === current && !current.signal.aborted;
  }

  function writable(current: Selection) {
    return (
      active(current) &&
      current.context.editable &&
      !writing.has(current.context.cardId) &&
      !current.awaitingRefresh &&
      !current.reading &&
      pending(current.context.cardId) === null
    );
  }

  function update(current: Selection) {
    if (!active(current)) return;
    const uncertain =
      pending(current.context.cardId) !== null && !writing.has(current.context.cardId);
    const message =
      current.message || (uncertain ? "Inspect the saved attachments before changing them." : "");
    status.textContent = message;
    status.hidden = !message;
    inspect.hidden = !uncertain;
    inspect.disabled = current.reading;
    retry.hidden = current.reading || current.entries !== undefined || uncertain;
    const editable = writable(current);
    for (const button of list.querySelectorAll<HTMLButtonElement>(
      "button[data-attachment-write]",
    )) {
      button.hidden = !current.context.editable;
      button.disabled = !editable;
    }
  }

  function clearRows() {
    rows?.abort();
    rows = undefined;
    list.replaceChildren();
  }

  function revoke(current: Selection) {
    if (!active(current)) return;
    clearRows();
    current.entries = undefined;
    current.message = "Attachments are unavailable.";
    onChange();
  }

  function render(current: Selection) {
    const focus = doc.activeElement?.getAttribute("data-attachment-focus");
    clearRows();
    if (current.entries?.length === 0) {
      const empty = doc.createElement("li");
      empty.textContent = "No attachments.";
      list.append(empty);
    }
    rows = new AbortController();
    const rowSignal = AbortSignal.any([current.signal, rows.signal]);
    for (const entry of current.entries ?? []) list.append(renderEntry(current, entry, rowSignal));
    update(current);
    if (focus) {
      const replacement = [...list.querySelectorAll("button")].find(
        (control) => control.dataset.attachmentFocus === focus,
      );
      if (replacement && !replacement.hidden && !replacement.disabled) replacement.focus();
      else if (!inspect.hidden) inspect.focus();
      else {
        heading.tabIndex = -1;
        heading.focus({ preventScroll: true });
      }
    }
  }

  function renderEntry(current: Selection, entry: Attachment, rowSignal: AbortSignal) {
    const row = doc.createElement("li");
    row.className = "card-attachment-row";
    const file = doc.createElement("p");
    file.textContent = entry.original_name;
    const size = doc.createElement("span");
    size.textContent = ` · ${entry.size} bytes · ${entry.created_at}`;
    file.append(size);
    const actions = doc.createElement("div");
    actions.className = "card-attachment-actions";
    const note = doc.createElement("p");
    note.setAttribute("role", "status");
    row.append(file, actions, note);

    function button(label: string, action: () => void, mutation = false) {
      const control = doc.createElement("button");
      control.type = "button";
      control.textContent = label;
      control.setAttribute("aria-label", `${label} ${entry.original_name}`);
      control.dataset.attachmentFocus = `${entry.id}:${label}`;
      if (mutation) control.dataset.attachmentWrite = "";
      control.addEventListener("click", action, { signal: rowSignal });
      actions.append(control);
      return control;
    }

    async function useFile(
      control: HTMLButtonElement,
      disposition: "attachment" | "inline",
      use: (blob: Blob) => void | Promise<void>,
    ) {
      if (control.disabled || rowSignal.aborted) return;
      control.disabled = true;
      note.textContent = "";
      try {
        const blob = await readContent(options, entry, disposition, rowSignal);
        await use(blob);
      } catch (error) {
        if (rowSignal.aborted) return;
        if (unavailable(error)) revoke(current);
        else
          note.textContent =
            disposition === "inline"
              ? "The image preview could not be displayed."
              : "The attachment could not be downloaded.";
      } finally {
        control.disabled = false;
        update(current);
      }
    }

    const download = button("Download", () => {
      void useFile(download, "attachment", (blob) => {
        const { url, release } = objectUrl(blob, rowSignal);
        const anchor = doc.createElement("a");
        anchor.href = url;
        anchor.download = downloadName(entry.original_name);
        row.append(anchor);
        try {
          anchor.click();
        } finally {
          anchor.remove();
          setTimeout(release, 1000);
        }
      });
    });
    if (entry.previewable) {
      const image = doc.createElement("img");
      image.className = "card-attachment-preview";
      image.alt = entry.original_name;
      image.hidden = true;
      let previewScope: AbortController | undefined;
      const preview = button("Preview", () => {
        if (previewScope) {
          previewScope.abort();
          previewScope = undefined;
          image.hidden = true;
          image.removeAttribute("src");
          preview.textContent = "Preview";
          preview.setAttribute("aria-label", `Preview ${entry.original_name}`);
          preview.setAttribute("aria-expanded", "false");
          return;
        }
        void useFile(preview, "inline", async (blob) => {
          const attempt = new AbortController();
          const previewSignal = AbortSignal.any([rowSignal, attempt.signal]);
          try {
            await showThumbnail(row, image, blob, 200, previewSignal);
            previewSignal.throwIfAborted();
            previewScope = attempt;
            image.hidden = false;
            preview.textContent = "Hide preview";
            preview.setAttribute("aria-label", `Hide preview ${entry.original_name}`);
            preview.setAttribute("aria-expanded", "true");
          } catch (error) {
            attempt.abort();
            image.removeAttribute("src");
            throw error;
          }
        });
      });
      preview.setAttribute("aria-expanded", "false");
      const selected = current.context.coverAttachmentId === entry.id;
      button(
        selected ? "Clear cover" : "Set cover",
        () => {
          void mutate(current, entry, { kind: "cover", cover: selected ? null : entry.id });
        },
        true,
      );
      row.append(image);
    }
    button(
      "Detach",
      () => {
        void mutate(current, entry, { kind: "detach", id: entry.id });
      },
      true,
    );
    return row;
  }

  async function readEntries(current: Selection) {
    const entries: Attachment[] = [];
    for await (const page of attachmentPages(options, current.context.cardId, current.signal)) {
      entries.push(...page);
    }
    return entries;
  }

  async function load(current: Selection, inspectSaved = false) {
    const { cardId } = current.context;
    if (!active(current) || current.reading || writing.has(cardId)) return;
    const finish = inspectSaved ? beginInspect(cardId) : undefined;
    if (finish === null) {
      current.message = "A card change is still in progress. Inspect again after it settles.";
      update(current);
      return;
    }
    current.reading = true;
    current.message = inspectSaved ? "Inspecting attachments…" : "Reading attachments…";
    update(current);
    const raw = inspectSaved ? pending(cardId) : null;
    try {
      let savedCover: unknown;
      let advanced = false;
      const intent = raw ? readMutation(raw, cardId, boardId) : undefined;
      if (inspectSaved) {
        if (!intent || JSON.stringify(intent) !== raw) {
          current.message = "The saved attachment change could not be validated.";
          return;
        }
        if (intent.kind === "detach") {
          const board = await readWithSession(
            () => api.getBoardAvailability(boardId, current.signal),
            current.signal,
          );
          advanced = board.data.revision > (tagRevision(intent.boardEtag, boardId) ?? Infinity);
        }
        const { data, etag } = await readWithSession(
          () => api.getCard(cardId, current.signal),
          current.signal,
        );
        if (
          !("board_id" in data) ||
          data.board_id !== boardId ||
          !("cover_attachment_id" in data)
        ) {
          throw new ApiError("invalid_response");
        }
        savedCover = data.cover_attachment_id;
        if (intent.kind === "cover") {
          const revision = tagRevision(etag, cardId);
          if (revision === undefined || !("revision" in data) || data.revision !== revision) {
            throw new ApiError("invalid_response");
          }
          advanced = revision > (tagRevision(intent.cardEtag, cardId) ?? Infinity);
        }
      }
      const entries = await readEntries(current);
      if (!active(current)) return;
      current.entries = entries;
      current.message = "";
      if (inspectSaved) {
        const matches =
          intent?.kind === "detach"
            ? entries.every(({ id }) => id !== intent.id) && savedCover !== intent.id
            : intent?.kind === "cover" && savedCover === intent.cover;
        // A read at the original validator can race a write whose response was lost.
        current.message = !advanced
          ? "The original version is still current. The attachment change could still arrive."
          : raw && clearPending(cardId, raw)
            ? matches
              ? "Saved attachments reflect the requested change."
              : "Saved attachments refreshed. Review them before making another change."
            : "The previous attachment change still needs inspection.";
        current.awaitingRefresh = true;
        onChange();
      }
      render(current);
    } catch (error) {
      if (!active(current)) return;
      if (unavailable(error)) revoke(current);
      else {
        clearRows();
        current.entries = undefined;
        current.message = inspectSaved
          ? "Could not inspect saved attachments."
          : "Could not read attachments.";
      }
    } finally {
      current.reading = false;
      finish?.();
      update(current);
    }
  }

  async function mutate(current: Selection, entry: Attachment, intent: Mutation) {
    if (!writable(current)) return;
    const { cardId, etag } = current.context;
    const boardEtag = intent.kind === "detach" ? getBoardEtag() : undefined;
    if (intent.kind === "detach" && !boardEtag) {
      current.message = "Refresh the board before detaching this attachment.";
      update(current);
      return;
    }
    const finish = beginWrite(cardId);
    if (!finish) {
      current.message = "Another card change is in progress.";
      update(current);
      return;
    }
    writing.add(cardId);
    current.message = "Saving attachment change…";
    update(current);
    const raw = JSON.stringify(
      intent.kind === "detach"
        ? { ...intent, revision: entry.revision, boardEtag }
        : { ...intent, cardEtag: etag },
    );
    try {
      const result = await submitOperation(
        () => journal(cardId, raw),
        async () => {
          if (intent.kind === "detach" && boardEtag) {
            await api.detachAttachment(entry.id, entry.revision, boardEtag);
          } else if (intent.kind === "cover") {
            const { data } = await api.patchCard(cardId, etag, {
              cover_attachment_id: intent.cover,
            });
            if (
              !("board_id" in data) ||
              data.board_id !== boardId ||
              !("cover_attachment_id" in data) ||
              data.cover_attachment_id !== intent.cover
            ) {
              throw new ApiError("invalid_response", { outcomeUnknown: true });
            }
          }
        },
      );
      if (result.kind === "confirmed" || result.kind === "stale" || result.kind === "refused") {
        clearPending(cardId, raw);
      }
      const selected = selection;
      if (!selected || !active(selected) || selected.context.cardId !== cardId) {
        if (result.kind !== "journal-failed" && result.kind !== "unknown") onChange();
        return;
      }
      switch (result.kind) {
        case "confirmed":
          selected.message =
            intent.kind === "detach"
              ? "Attachment detached."
              : intent.cover
                ? "Cover set."
                : "Cover cleared.";
          if (intent.kind === "detach") {
            selected.entries = selected.entries?.filter(({ id }) => id !== entry.id);
            render(selected);
          }
          break;
        case "stale":
          selected.message = "The card or board changed. Review the refreshed attachments.";
          break;
        case "refused":
          selected.message = "The attachment change was refused.";
          break;
        case "unknown":
          selected.message =
            "The attachment change could not be confirmed. Inspect the saved attachments.";
          if (intent.kind === "detach") {
            selected.entries = selected.entries?.filter(({ id }) => id !== entry.id);
            render(selected);
          }
          break;
        case "journal-failed":
          selected.message = "The tab could not save attachment recovery details.";
          return;
      }
      selected.awaitingRefresh = result.kind !== "refused";
      if (result.kind !== "unknown") onChange();
    } finally {
      writing.delete(cardId);
      finish();
      if (selection) update(selection);
    }
  }

  function show(context: AttachmentContext | null) {
    if (signal.aborted) return;
    const previous = selection;
    if (
      previous &&
      context &&
      previous.context.cardId === context.cardId &&
      previous.context.etag === context.etag &&
      previous.context.attachmentCount === context.attachmentCount &&
      previous.context.coverAttachmentId === context.coverAttachmentId
    ) {
      previous.context = context;
      previous.awaitingRefresh = false;
      update(previous);
      return;
    }
    previous?.lifetime.abort();
    clearRows();
    selection = undefined;
    section.hidden = !context;
    if (!context) return;
    const selectedLifetime = new AbortController();
    const current: Selection = {
      context,
      lifetime: selectedLifetime,
      signal: AbortSignal.any([signal, selectedLifetime.signal]),
      entries: undefined,
      reading: false,
      awaitingRefresh: false,
      message: "",
    };
    selection = current;
    if (context.attachmentCount === 0) {
      current.entries = [];
      render(current);
    } else void load(current);
  }

  retry.addEventListener(
    "click",
    () => {
      if (selection) void load(selection);
    },
    { signal },
  );
  inspect.addEventListener(
    "click",
    () => {
      if (selection) void load(selection, true);
    },
    { signal },
  );
  function dispose() {
    lifetime.abort();
    selection?.lifetime.abort();
    selection = undefined;
    clearRows();
    section.remove();
  }
  if (signal.aborted) dispose();
  else options.signal.addEventListener("abort", dispose, { signal });
  return { show, dispose };
}
