import { createScopedOperations, submitOperation, type OperationStorage } from "./operations.js";

export type CardText = Readonly<{ title: string; description: string }>;
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
  edit(text: CardText): void;
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
}>;

type Draft = Readonly<{
  base: CardVersion;
  text: CardText;
  titleChanged: boolean;
  descriptionChanged: boolean;
  phase: "dirty" | "stale" | "refused" | "submitted";
}>;

type Entry = {
  draft: Draft | null;
  raw: string | null | undefined;
  storageProblem: "invalid" | "unavailable" | "unsaved" | undefined;
  binding: DraftBindings | undefined;
  inspected: CardVersion | undefined;
  inspectionFailed: boolean;
};

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const STRONG_ETAG = /^"[A-Za-z0-9_-]{16,128}"(?![\s\S])/;
const STORAGE_LIMIT = 4 * 1024 * 1024;
// Match Go strings.TrimSpace, including U+0085 and excluding U+FEFF.
const titleHasText = /\P{White_Space}/u;

function version({ etag, revision }: CardVersion): CardVersion {
  return { etag, revision };
}

function sameVersion(first: CardVersion, second: CardVersion): boolean {
  return first.etag === second.etag && first.revision === second.revision;
}

function sameText(first: CardText, second: CardText): boolean {
  return first.title === second.title && first.description === second.description;
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

function parseDraft(raw: string): Draft | null {
  if (raw.length > STORAGE_LIMIT) return null;
  try {
    const value: unknown = JSON.parse(raw);
    if (!record(value) || Object.keys(value).length !== 5) return null;
    const { base, text, titleChanged, descriptionChanged, phase } = value;
    if (
      !validVersion(base) ||
      Object.keys(base).length !== 2 ||
      !validText(text) ||
      Object.keys(text).length !== 2 ||
      typeof titleChanged !== "boolean" ||
      typeof descriptionChanged !== "boolean" ||
      (!titleChanged && !descriptionChanged) ||
      (phase !== "dirty" && phase !== "stale" && phase !== "refused" && phase !== "submitted") ||
      (phase === "submitted" && !titleHasText.test(text.title))
    )
      return null;
    return { base, text, titleChanged, descriptionChanged, phase };
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
    sameText(saved, draft.text)
  );
}

export function createCardTextDrafts({
  storage,
  api,
}: Readonly<{
  storage?: OperationStorage;
  api: {
    getCard(cardId: string, signal: AbortSignal): Promise<CardReply>;
    patchCard(cardId: string, etag: string, text: CardText): Promise<CardReply>;
  };
}>) {
  const entries = new Map<string, Entry>();
  const operations = createScopedOperations();
  const tabStorage = () => storage ?? globalThis.sessionStorage;

  function load(key: string, entry: Entry) {
    try {
      const raw = tabStorage().getItem(key);
      if (raw === entry.raw) {
        if (entry.storageProblem === "unavailable") {
          entry.storageProblem = raw !== null && !entry.draft ? "invalid" : undefined;
        }
        return;
      }
      entry.inspected = undefined;
      entry.inspectionFailed = false;
      entry.draft = raw === null ? null : parseDraft(raw);
      entry.raw = raw;
      entry.storageProblem = raw !== null && !entry.draft ? "invalid" : undefined;
    } catch {
      entry.inspected = undefined;
      entry.storageProblem = "unavailable";
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
        load(key, entry);
        return false;
      }
      const raw = next === null ? null : JSON.stringify(next);
      if (raw === null) tabStorage().removeItem(key);
      else {
        if (raw.length > STORAGE_LIMIT || !validText(next?.text))
          throw new RangeError("Draft storage limit");
        tabStorage().setItem(key, raw);
      }
      entry.raw = raw;
      entry.draft = next;
      entry.storageProblem = undefined;
      return true;
    } catch {
      if (retainOnFailure) {
        entry.draft = next;
        entry.storageProblem = "unsaved";
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
    const key = `localswim:v2:card-text:${boardId}:${cardId}`;
    const entry: Entry = entries.get(key) ?? {
      draft: null,
      raw: undefined,
      storageProblem: undefined,
      binding: undefined,
      inspected: undefined,
      inspectionFailed: false,
    };
    entries.set(key, entry);
    if (!operations.state(key)) load(key, entry);
    entry.binding = binding;
    const current = () => entry.binding === binding && !binding.signal.aborted;
    let editBase: ConfirmedCardText | undefined;

    function release() {
      if (!entry.binding && !operations.state(key) && entry.storageProblem !== "unsaved")
        entries.delete(key);
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
        writable,
      };
    }

    function snapshot(): CardTextState {
      const { confirmed, writable } = context();
      const { draft, inspected, storageProblem, inspectionFailed } = entry;
      const active = operations.state(key);
      const unknown = draft?.phase === "submitted";
      const stale =
        draft !== null &&
        (draft.phase === "stale" || !confirmed || !sameVersion(confirmed, draft.base));
      const phase =
        active ??
        (unknown
          ? "unknown"
          : storageProblem
            ? "storage-error"
            : stale
              ? "stale"
              : draft?.phase === "refused"
                ? "refused"
                : draft
                  ? "dirty"
                  : "idle");
      const available = current() && !active;
      const storageBlocked = storageProblem === "invalid" || storageProblem === "unavailable";
      const canEdit = available && !unknown && !storageBlocked && writable && confirmed !== null;
      const changedFields: (keyof CardText)[] = [];
      if (draft?.titleChanged) changedFields.push("title");
      if (draft?.descriptionChanged) changedFields.push("description");
      return {
        draft: draft?.text ?? null,
        changedFields,
        phase,
        canEdit,
        canSubmit:
          canEdit &&
          draft !== null &&
          titleHasText.test(draft.text.title) &&
          (!stale || (inspected !== undefined && sameVersion(confirmed, inspected))),
        canInspect:
          available && (unknown || stale || Boolean(storageProblem) || draft?.phase === "refused"),
        canDismiss:
          available &&
          (draft !== null || (storageProblem === "invalid" && inspected !== undefined)),
        inspectionFailed,
      };
    }

    function beginEdit() {
      if (snapshot().canEdit) editBase ??= context().confirmed ?? undefined;
    }

    function cancelEdit() {
      editBase = undefined;
    }

    function edit(text: CardText) {
      const base = editBase ?? context().confirmed;
      cancelEdit();
      if (!base || !snapshot().canEdit) return;
      const previous = entry.draft;
      const canCompare = previous === null || sameVersion(previous.base, base);
      const titleChanged = canCompare
        ? text.title !== base.title
        : previous.titleChanged || text.title !== previous.text.title;
      const descriptionChanged = canCompare
        ? text.description !== base.description
        : previous.descriptionChanged || text.description !== previous.text.description;
      if (previous && sameText(previous.text, text)) return;
      persist(
        key,
        entry,
        titleChanged || descriptionChanged
          ? {
              base: previous?.base ?? version(base),
              text: { title: text.title, description: text.description },
              titleChanged,
              descriptionChanged,
              phase: previous?.phase === "stale" ? "stale" : "dirty",
            }
          : null,
        true,
      );
      entry.inspectionFailed = false;
      publish(entry);
    }

    function confirmed(saved: ConfirmedCardText) {
      if (!current()) return;
      const previous = context().confirmed;
      if (!previous || saved.revision >= previous.revision) binding.onConfirmed(saved);
    }

    function settled() {
      publish(entry);
      release();
    }

    async function submit() {
      const base = context().confirmed;
      const draft = entry.draft;
      if (!base || !draft || !snapshot().canSubmit) return;
      cancelEdit();
      const text = {
        title: draft.titleChanged ? draft.text.title : base.title,
        description: draft.descriptionChanged ? draft.text.description : base.description,
      };
      const titleChanged = text.title !== base.title;
      const descriptionChanged = text.description !== base.description;
      if (!titleChanged && !descriptionChanged) {
        persist(key, entry, null);
        publish(entry);
        return;
      }
      const submission: Draft = {
        base: version(base),
        text,
        titleChanged,
        descriptionChanged,
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
          load(key, entry);
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
          load(key, entry);
          if (entry.raw !== original) return;
          entry.inspected = version(saved);
          if (entry.draft?.phase === "submitted" && matchesSubmission(saved, entry.draft)) {
            persist(key, entry, null);
          }
          confirmed(saved);
        },
        settled,
      );
    }

    function dismiss() {
      if (!snapshot().canDismiss) return;
      cancelEdit();
      const draft = entry.draft;
      persist(key, entry, draft?.phase === "submitted" ? { ...draft, phase: "stale" } : null);
      entry.inspected = undefined;
      entry.inspectionFailed = false;
      publish(entry);
    }

    return { snapshot, beginEdit, cancelEdit, edit, submit, inspect, dismiss };
  }

  return {
    bind,
    refresh() {
      for (const [key, entry] of entries) {
        if (!operations.state(key)) load(key, entry);
        publish(entry);
      }
    },
  };
}
