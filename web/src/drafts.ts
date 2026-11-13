import type { ProjectBoardPending } from "./board-view.js";
import { createScopedOperations, submitOperation, type OperationStorage } from "./operations.js";

export type CardText = Readonly<{ title: string; description: string }>;
type TextPatch = Partial<CardText>;
export type CardTextScope = Readonly<{ boardId: string; cardId: string }>;
type CardVersion = Readonly<{ revision: number; etag: string }>;
export type ConfirmedCardText = CardText & CardVersion;
export type CardTextContext = Readonly<{ confirmed: ConfirmedCardText | null; writable: boolean }>;

export type CardTextState = Readonly<{
  draft: CardText | null;
  changedFields: readonly (keyof CardText)[];
  phase:
    | "idle"
    | "dirty"
    | "stale"
    | "submitting"
    | "unknown"
    | "inspecting"
    | "refused"
    | "storage-conflict"
    | "storage-error";
  canEdit: boolean;
  canSubmit: boolean;
  canInspect: boolean;
  canDismiss: boolean;
  inspectionFailed: boolean;
}>;

export type CardTextController = Readonly<{
  snapshot(): CardTextState;
  beginEdit(): void;
  cancelEdit(): void;
  edit(text: CardText, field?: keyof CardText): void;
  submit(): Promise<void>;
  inspect(): Promise<void>;
  dismiss(): void;
}>;

type CardReply = Readonly<{ data: object; etag: string | null }>;
type DraftBindings = Readonly<{
  signal: AbortSignal;
  context: () => CardTextContext;
  onChange: () => void;
  onConfirmed: (saved: ConfirmedCardText) => void;
  beginWrite?: () => (() => void) | null;
}>;

type Draft = Readonly<{
  base: CardVersion;
  text: TextPatch;
  phase: "dirty" | "stale" | "refused" | "submitted";
}>;

type Entry = {
  draft: Draft | null;
  raw: string | null | undefined;
  storageProblem: "invalid" | "unavailable" | "conflict" | undefined;
  unsaved: boolean;
  binding: DraftBindings | undefined;
  inspected: CardVersion | undefined;
  inspectionFailed: boolean;
  confirmedWrite: CardVersion | undefined;
};

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const STRONG_ETAG = /^"[A-Za-z0-9_-]{16,128}"(?![\s\S])/;
const STORAGE_LIMIT = 4 * 1024 * 1024;
const DETACHED_WRITE_LIMIT = 64;
// Match Go strings.TrimSpace, including U+0085 and excluding U+FEFF.
const titleHasText = /\P{White_Space}/u;

function version({ etag, revision }: CardVersion): CardVersion {
  return { etag, revision };
}

function sameVersion(first: CardVersion, second: CardVersion): boolean {
  return first.etag === second.etag && first.revision === second.revision;
}

function reachesVersion(saved: CardVersion, floor: CardVersion | undefined): boolean {
  return !floor || saved.revision > floor.revision || sameVersion(saved, floor);
}

function storageKey({ boardId, cardId }: CardTextScope): string {
  return `localswim:v2:card-text:${boardId}:${cardId}`;
}

function sameText(first: TextPatch, second: TextPatch): boolean {
  return first.title === second.title && first.description === second.description;
}

function textPatch(title: string | undefined, description: string | undefined): TextPatch {
  return {
    ...(title === undefined ? {} : { title }),
    ...(description === undefined ? {} : { description }),
  };
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function validVersion(value: unknown): value is CardVersion {
  return (
    record(value) &&
    typeof value.revision === "number" &&
    Number.isSafeInteger(value.revision) &&
    value.revision > 0 &&
    typeof value.etag === "string" &&
    STRONG_ETAG.test(value.etag)
  );
}

function validText(value: unknown): value is CardText {
  return (
    record(value) &&
    typeof value.title === "string" &&
    value.title.isWellFormed() &&
    typeof value.description === "string" &&
    value.description.isWellFormed()
  );
}

function validPatch(value: unknown): value is TextPatch {
  if (!record(value)) return false;
  const fields = Object.entries(value);
  return (
    fields.length > 0 &&
    fields.every(
      ([field, text]) =>
        (field === "title" || field === "description") &&
        typeof text === "string" &&
        text.isWellFormed(),
    )
  );
}

function parseDraft(raw: string): Draft | null {
  if (raw.length > STORAGE_LIMIT) return null;
  try {
    const value: unknown = JSON.parse(raw);
    if (!record(value) || Object.keys(value).length !== 3) return null;
    const { base, text, phase } = value;
    if (
      !validVersion(base) ||
      Object.keys(base).length !== 2 ||
      !validPatch(text) ||
      (phase !== "dirty" && phase !== "stale" && phase !== "refused" && phase !== "submitted") ||
      (phase === "submitted" && text.title !== undefined && !titleHasText.test(text.title))
    )
      return null;
    return { base, text, phase };
  } catch {
    return null;
  }
}

function confirmedReply(scope: CardTextScope, { data, etag }: CardReply): ConfirmedCardText {
  if (record(data) && data.id === scope.cardId && data.board_id === scope.boardId) {
    const next = { etag, revision: data.revision };
    if (validVersion(next) && validText(data) && titleHasText.test(data.title)) {
      return { ...next, title: data.title, description: data.description };
    }
  }
  throw new TypeError("The response did not identify the original card and version.");
}

function matchesSubmission(saved: ConfirmedCardText, draft: Draft): boolean {
  return (
    saved.revision > draft.base.revision &&
    saved.etag !== draft.base.etag &&
    (draft.text.title === undefined || saved.title === draft.text.title) &&
    (draft.text.description === undefined || saved.description === draft.text.description)
  );
}

export function createCardTextDrafts({
  storage,
  api,
  projectPending,
}: Readonly<{
  storage?: OperationStorage;
  projectPending?: ProjectBoardPending;
  api: {
    getCard(cardId: string, signal: AbortSignal): Promise<CardReply>;
    patchCard(cardId: string, etag: string, text: TextPatch): Promise<CardReply>;
  };
}>) {
  const entries = new Map<string, Entry>();
  const operations = createScopedOperations();
  const tabStorage = () => storage ?? globalThis.sessionStorage;

  function markConflict(entry: Entry) {
    entry.storageProblem = "conflict";
    entry.inspected = undefined;
    entry.inspectionFailed = false;
  }

  function load(key: string, entry: Entry, useStored = false) {
    if (entry.storageProblem === "conflict" && !useStored) return false;
    try {
      const raw = tabStorage().getItem(key);
      if (!useStored && raw === entry.raw) {
        if (entry.storageProblem === "unavailable") {
          entry.storageProblem =
            raw !== null && !entry.unsaved && !entry.draft ? "invalid" : undefined;
        }
        return true;
      }
      if (!useStored && entry.raw !== undefined && (entry.draft || entry.unsaved)) {
        markConflict(entry);
        return false;
      }
      entry.inspected = undefined;
      entry.inspectionFailed = false;
      entry.draft = raw === null ? null : parseDraft(raw);
      entry.raw = raw;
      entry.storageProblem = raw !== null && !entry.draft ? "invalid" : undefined;
      entry.unsaved = false;
      return true;
    } catch {
      entry.inspected = undefined;
      if (entry.storageProblem !== "conflict") entry.storageProblem = "unavailable";
      return false;
    }
  }

  function persist(
    key: string,
    entry: Entry,
    next: Draft | null,
    retainOnFailure = false,
  ): boolean {
    try {
      const current = tabStorage().getItem(key);
      if (current !== entry.raw) {
        if (retainOnFailure) {
          entry.draft = next;
          entry.unsaved = true;
        }
        markConflict(entry);
        return false;
      }
      const raw = next === null ? null : JSON.stringify(next);
      if (raw === null) tabStorage().removeItem(key);
      else {
        if (raw.length > STORAGE_LIMIT || !validPatch(next?.text))
          throw new RangeError("Draft storage limit");
        tabStorage().setItem(key, raw);
      }
      entry.raw = raw;
      entry.draft = next;
      entry.storageProblem = undefined;
      entry.unsaved = false;
      return true;
    } catch {
      if (retainOnFailure) {
        entry.draft = next;
        entry.unsaved = true;
      } else {
        entry.storageProblem ??= "unavailable";
      }
      return false;
    }
  }

  function publish(entry: Entry) {
    if (entry.binding && !entry.binding.signal.aborted) entry.binding.onChange();
  }

  function bind(scope: CardTextScope, binding: DraftBindings): CardTextController {
    const { boardId, cardId } = scope;
    if (
      boardId.length !== 36 ||
      cardId.length !== 36 ||
      !UUID.test(boardId) ||
      !UUID.test(cardId)
    ) {
      throw new TypeError("Expected canonical card and board IDs.");
    }
    const key = storageKey(scope);
    const entry: Entry = entries.get(key) ?? {
      draft: null,
      raw: undefined,
      storageProblem: undefined,
      unsaved: false,
      binding: undefined,
      inspected: undefined,
      inspectionFailed: false,
      confirmedWrite: undefined,
    };
    entries.set(key, entry);
    if (!operations.state(key)) load(key, entry);
    entry.binding = binding;
    const current = () => entry.binding === binding && !binding.signal.aborted;
    let editBase: ConfirmedCardText | undefined;

    function release() {
      if (
        entry.binding ||
        operations.state(key) ||
        entry.unsaved ||
        entry.storageProblem === "conflict"
      )
        return;
      entries.delete(key);
      if (!entry.confirmedWrite) return;
      // Keep the most recently detached write versions, without retaining card text.
      entry.draft = null;
      entry.raw = undefined;
      entry.inspected = undefined;
      entry.storageProblem = undefined;
      entry.inspectionFailed = false;
      entries.set(key, entry);
      let count = 0;
      for (const [oldKey, oldEntry] of [...entries].reverse()) {
        if (
          oldEntry.binding ||
          operations.state(oldKey) ||
          !oldEntry.confirmedWrite ||
          oldEntry.unsaved ||
          oldEntry.storageProblem === "conflict"
        )
          continue;
        if (++count > DETACHED_WRITE_LIMIT) entries.delete(oldKey);
      }
    }

    const unbind = () => {
      if (entry.binding === binding) entry.binding = undefined;
      release();
    };
    if (binding.signal.aborted) unbind();
    else binding.signal.addEventListener("abort", unbind, { once: true });

    function context() {
      const { confirmed, writable } = binding.context();
      return {
        confirmed: validVersion(confirmed) && validText(confirmed) ? confirmed : null,
        writable: writable && confirmed !== null && reachesVersion(confirmed, entry.confirmedWrite),
      };
    }

    function canChangeDraft() {
      return (
        current() &&
        !operations.state(key) &&
        entry.draft?.phase !== "submitted" &&
        entry.storageProblem !== "invalid" &&
        entry.storageProblem !== "unavailable" &&
        entry.storageProblem !== "conflict"
      );
    }

    function snapshot(): CardTextState {
      const { confirmed, writable } = context();
      const { draft, inspected, storageProblem, unsaved, inspectionFailed } = entry;
      const active = operations.state(key);
      const unknown = draft?.phase === "submitted";
      const stale =
        draft !== null &&
        (draft.phase === "stale" || !confirmed || !sameVersion(confirmed, draft.base));
      const phase =
        active ??
        (storageProblem === "conflict"
          ? "storage-conflict"
          : unknown
            ? "unknown"
            : storageProblem || unsaved
              ? "storage-error"
              : stale
                ? "stale"
                : draft?.phase === "refused"
                  ? "refused"
                  : draft
                    ? "dirty"
                    : "idle");
      const available = current() && !active;
      const canEdit = canChangeDraft() && writable && confirmed !== null;
      const changedFields: (keyof CardText)[] = [];
      if (draft?.text.title !== undefined) changedFields.push("title");
      if (draft?.text.description !== undefined) changedFields.push("description");
      return {
        draft: draft && {
          title: draft.text.title ?? confirmed?.title ?? "",
          description: draft.text.description ?? confirmed?.description ?? "",
        },
        changedFields,
        phase,
        canEdit,
        canSubmit:
          canEdit &&
          draft !== null &&
          (draft.text.title === undefined || titleHasText.test(draft.text.title)) &&
          (!stale || (inspected !== undefined && sameVersion(confirmed, inspected))),
        canInspect:
          available &&
          storageProblem !== "conflict" &&
          (unknown || stale || unsaved || Boolean(storageProblem) || draft?.phase === "refused"),
        canDismiss:
          available &&
          (draft !== null ||
            storageProblem === "conflict" ||
            (storageProblem === "invalid" && inspected !== undefined)),
        inspectionFailed,
      };
    }

    function beginEdit() {
      if (snapshot().canEdit) editBase ??= context().confirmed ?? undefined;
    }

    function cancelEdit() {
      editBase = undefined;
    }

    function edit(text: CardText, field?: keyof CardText) {
      const { confirmed, writable } = context();
      // A composition that began while writable can finish locally while the
      // board is fenced. Its original version still controls later submission.
      const captured = editBase ?? (writable ? confirmed : null);
      cancelEdit();
      if (!captured || !canChangeDraft()) return;
      const base = captured;
      const previous = entry.draft;
      function editField(key: keyof CardText) {
        if (field && field !== key) return previous?.text[key];
        const value = text[key];
        return value === base[key] ? undefined : value;
      }
      const next = textPatch(editField("title"), editField("description"));
      if (previous && sameText(previous.text, next)) return;
      persist(
        key,
        entry,
        next.title !== undefined || next.description !== undefined
          ? {
              base: previous?.base ?? version(base),
              text: next,
              phase: previous?.phase === "stale" ? "stale" : "dirty",
            }
          : null,
        true,
      );
      entry.inspectionFailed = false;
      publish(entry);
    }

    function isLatest(saved: ConfirmedCardText, receiver: DraftBindings) {
      if (receiver.signal.aborted) return false;
      const previous = receiver.context().confirmed;
      return (
        reachesVersion(saved, previous ?? undefined) && reachesVersion(saved, entry.confirmedWrite)
      );
    }

    function confirmed(saved: ConfirmedCardText, receiver = entry.binding) {
      if (receiver && isLatest(saved, receiver)) receiver.onConfirmed(saved);
    }

    function settled() {
      publish(entry);
      release();
    }

    async function submit() {
      const base = context().confirmed;
      const draft = entry.draft;
      if (!base || !draft || !snapshot().canSubmit) return;
      const finishWrite = binding.beginWrite?.();
      if (finishWrite === null) return;
      let clearProjection: (() => void) | undefined;
      try {
        cancelEdit();
        const text = textPatch(
          draft.text.title === base.title ? undefined : draft.text.title,
          draft.text.description === base.description ? undefined : draft.text.description,
        );
        if (text.title === undefined && text.description === undefined) {
          persist(key, entry, null);
          publish(entry);
          return;
        }
        const submission: Draft = {
          base: version(base),
          text,
          phase: "submitted",
        };
        entry.inspected = undefined;
        entry.inspectionFailed = false;
        await operations.run(
          key,
          "submitting",
          async () => {
            const result = await submitOperation(
              () => persist(key, entry, submission),
              async () => {
                if (text.title !== undefined) {
                  clearProjection = projectPending?.({
                    kind: "title",
                    boardId,
                    cardId,
                    revision: submission.base.revision,
                    title: text.title,
                  });
                }
                publish(entry);
                const saved = confirmedReply(
                  scope,
                  await api.patchCard(cardId, submission.base.etag, text),
                );
                if (!matchesSubmission(saved, submission))
                  throw new TypeError("The response did not confirm the submitted text.");
                return saved;
              },
            );
            switch (result.kind) {
              case "confirmed":
                if (reachesVersion(result.result, entry.confirmedWrite)) {
                  entry.confirmedWrite = version(result.result);
                }
                persist(key, entry, null);
                confirmed(result.result);
                break;
              case "refused":
              case "stale":
                persist(key, entry, { ...submission, phase: result.kind });
                break;
              case "unknown":
              case "journal-failed":
                break;
            }
          },
          settled,
        );
      } finally {
        clearProjection?.();
        finishWrite?.();
      }
    }

    async function inspect() {
      if (!snapshot().canInspect) return;
      cancelEdit();
      await operations.run(
        key,
        "inspecting",
        async () => {
          entry.inspected = undefined;
          entry.inspectionFailed = false;
          if (!load(key, entry)) {
            entry.inspectionFailed = entry.storageProblem !== "conflict";
            return;
          }
          const original = entry.raw;
          publish(entry);
          let saved: ConfirmedCardText;
          try {
            saved = confirmedReply(scope, await api.getCard(cardId, binding.signal));
          } catch {
            if (current()) entry.inspectionFailed = true;
            return;
          }
          if (!current()) return;
          if (!isLatest(saved, binding)) {
            entry.inspectionFailed = true;
            return;
          }
          if (!load(key, entry) || entry.raw !== original) return;
          entry.inspected = version(saved);
          if (entry.draft?.phase === "submitted" && matchesSubmission(saved, entry.draft)) {
            entry.confirmedWrite = version(saved);
            persist(key, entry, null);
          }
          confirmed(saved, binding);
        },
        settled,
      );
    }

    function dismiss() {
      if (!snapshot().canDismiss) return;
      cancelEdit();
      const draft = entry.draft;
      if (entry.storageProblem === "conflict") load(key, entry, true);
      else persist(key, entry, draft?.phase === "submitted" ? { ...draft, phase: "stale" } : null);
      entry.inspected = undefined;
      entry.inspectionFailed = false;
      publish(entry);
    }

    return { snapshot, beginEdit, cancelEdit, edit, submit, inspect, dismiss };
  }

  return {
    bind,
    retainedText(scope: CardTextScope): TextPatch | undefined {
      const key = storageKey(scope);
      const entry = entries.get(key);
      if (entry?.draft) return entry.draft.text;
      try {
        const raw = tabStorage().getItem(key);
        return raw === null ? undefined : (parseDraft(raw)?.text ?? undefined);
      } catch {
        return undefined;
      }
    },
    latestConfirmed(scope: CardTextScope) {
      return entries.get(storageKey(scope))?.confirmedWrite;
    },
    refresh() {
      for (const [key, entry] of entries) {
        if (!operations.state(key)) load(key, entry);
        publish(entry);
      }
    },
  };
}
