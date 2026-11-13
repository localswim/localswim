import { MAX_ATTACHMENT_BYTES } from "../api.mjs";
import type { OperationStorage } from "./operations.js";

export type Original = Readonly<{ id: string; name: string; size: number; digest: string }>;
export type UploadJournal = Readonly<{ remaining: readonly Original[]; expected: string | null }>;

export const MAX_UPLOAD_FILES = 64;
const maxJournalLength = 512 * 1024;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const sha256 = /^[0-9a-f]{64}$/;

export function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function revisionTag(id: string, revision: number): string {
  return `"${id}-${revision}"`;
}

export function revisionOf(id: string, etag: string): number {
  const revision = Number(etag.slice(id.length + 2, -1));
  if (!Number.isSafeInteger(revision) || revision < 1 || revisionTag(id, revision) !== etag) {
    throw new TypeError("Invalid upload validator.");
  }
  return revision;
}

export function validUploadName(name: string): boolean {
  return name.length > 0 && name.length <= 2048 && name.isWellFormed() && [...name].length <= 1024;
}

function key(boardId: string, cardId: string): string {
  return `localswim:v2:uploads:${boardId}:${cardId}`;
}

function parseJournal(raw: string, cardId: string): UploadJournal {
  if (raw.length > maxJournalLength) throw new TypeError("Upload journal is too large.");
  const value: unknown = JSON.parse(raw);
  if (
    !record(value) ||
    !Array.isArray(value.remaining) ||
    value.remaining.length === 0 ||
    value.remaining.length > MAX_UPLOAD_FILES ||
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
      !validUploadName(name) ||
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

function savedUpload(boardId: string, cardId: string, storage?: Pick<OperationStorage, "getItem">) {
  let raw: string | null;
  try {
    raw = (storage ?? globalThis.sessionStorage).getItem(key(boardId, cardId));
  } catch {
    return null;
  }
  if (raw === null) return null;
  try {
    return { raw, journal: parseJournal(raw, cardId) };
  } catch {
    return { raw, journal: null };
  }
}

export function hasPendingUpload(
  boardId: string,
  cardId: string,
  storage?: Pick<OperationStorage, "getItem">,
): boolean {
  const saved = savedUpload(boardId, cardId, storage);
  return saved !== null && saved.journal?.expected !== null;
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
      if (!uuid.test(cardId) || hasPendingUpload(boardId, cardId, store)) return true;
    }
  } catch {
    return false;
  }
  return false;
}

export function createUploadRecovery(boardId: string, storage?: OperationStorage) {
  return {
    read(cardId: string) {
      return savedUpload(boardId, cardId, storage);
    },
    save(cardId: string, previous: string | null, next: UploadJournal | null): string | null {
      const store = storage ?? globalThis.sessionStorage;
      const itemKey = key(boardId, cardId);
      if (store.getItem(itemKey) !== previous) throw new TypeError("Upload journal changed.");
      if (!next?.remaining.length) {
        store.removeItem(itemKey);
        return null;
      }
      const raw = JSON.stringify(next);
      store.setItem(itemKey, raw);
      return raw;
    },
  };
}

async function fingerprint(file: File): Promise<Omit<Original, "id">> {
  const hash = await globalThis.crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  const digest = Array.from(new Uint8Array(hash), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
  return { name: file.name, size: file.size, digest };
}

export async function identifyUpload(file: File): Promise<Original> {
  return { id: globalThis.crypto.randomUUID(), ...(await fingerprint(file)) };
}

export async function matchUploads(
  files: readonly File[],
  originals: readonly Original[],
  signal: AbortSignal,
): Promise<File[] | null> {
  if (
    files.length !== originals.length ||
    files.some((file) => file.size > MAX_ATTACHMENT_BYTES || !validUploadName(file.name))
  ) {
    return null;
  }
  const identity = ({ name, size, digest }: Omit<Original, "id">) =>
    JSON.stringify([name, size, digest]);
  const selected = new Map<string, File[]>();
  for (const file of files) {
    signal.throwIfAborted();
    const key = identity(await fingerprint(file));
    const matches = selected.get(key);
    if (matches) matches.push(file);
    else selected.set(key, [file]);
  }
  const ordered: File[] = [];
  for (const original of originals) {
    const file = selected.get(identity(original))?.pop();
    if (!file) return null;
    ordered.push(file);
  }
  return ordered;
}
