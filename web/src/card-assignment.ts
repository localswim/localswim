import type { BoardViewData } from "./board-view.js";
import type { AppContext } from "./model.js";
import { submitOperation, type OperationStorage } from "./operations.js";

type Field = "ownerId" | "priorityId";
type Version = Readonly<{ revision: number; etag: string }>;
type SavedAssignment = Version & Readonly<{ cardId: string; ownerId: string; priorityId: string }>;
type AssignmentContext = SavedAssignment &
  Readonly<{ writable: boolean; canAssignOwner?: boolean; canSetPriority?: boolean }>;
type Draft = Readonly<{
  field: Field;
  requested: string;
  base: Version;
  phase: "dirty" | "submitted" | "stale" | "refused";
}>;
type Entry = {
  raw: string | null | undefined;
  draft: Draft | null;
  confirmed?: SavedAssignment;
  inspection?: SavedAssignment;
  result?: Readonly<{ field: Field; requested: string; effective: string }>;
  active?: "submitting" | "inspecting";
  unsaved?: boolean;
  storedSubmission?: boolean;
  storageError?: boolean;
  inspectionFailed?: boolean;
};
type Choice = Readonly<{ id: string; label: string }>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const strongTag = /^"[A-Za-z0-9_-]{16,128}"(?![\s\S])/;
const fields = ["ownerId", "priorityId"] as const;
const labels = { ownerId: "Owner", priorityId: "Priority" } as const;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isId(value: unknown): value is string {
  return typeof value === "string" && uuid.test(value);
}

function isVersion(value: unknown): value is Version {
  return (
    isRecord(value) &&
    typeof value.revision === "number" &&
    Number.isSafeInteger(value.revision) &&
    value.revision > 0 &&
    typeof value.etag === "string" &&
    strongTag.test(value.etag)
  );
}

function sameVersion(first: Version, second: Version): boolean {
  return first.revision === second.revision && first.etag === second.etag;
}

function reachesVersion(saved: Version, floor: Version | undefined): boolean {
  return !floor || saved.revision > floor.revision || sameVersion(saved, floor);
}

function hasGrant(context: AssignmentContext, field: Field): boolean {
  return (field === "ownerId" ? context.canAssignOwner : context.canSetPriority) === true;
}

function parseDraft(raw: string): Draft | null {
  if (raw.length > 512) return null;
  try {
    const value: unknown = JSON.parse(raw);
    if (!isRecord(value)) return null;
    const { field, requested, base, phase } = value;
    if (
      (field !== "ownerId" && field !== "priorityId") ||
      !isId(requested) ||
      !isVersion(base) ||
      (phase !== "dirty" && phase !== "submitted" && phase !== "stale" && phase !== "refused")
    ) {
      return null;
    }
    return { field, requested, base, phase };
  } catch {
    return null;
  }
}

function readReply(
  { data, etag }: Readonly<{ data: object; etag: string | null }>,
  boardId: string,
  cardId: string,
): SavedAssignment {
  if (isRecord(data) && data.board_id === boardId && data.id === cardId) {
    const version = { revision: data.revision, etag };
    if (isVersion(version) && isId(data.owner_id) && isId(data.priority_id)) {
      return { ...version, cardId, ownerId: data.owner_id, priorityId: data.priority_id };
    }
  }
  throw new TypeError("The response did not confirm the card assignment and version.");
}

export function mountCardAssignment(
  root: HTMLElement,
  {
    boardId,
    api,
    signal,
    storage,
    readWithSession,
    getBoardData,
    getContext,
    beginWrite,
    beginInspection,
    onConfirmed,
    onChange,
  }: AppContext &
    Readonly<{
      boardId: string;
      storage?: OperationStorage;
      readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
      getBoardData: () => BoardViewData | null | undefined;
      getContext: () => AssignmentContext | null;
      beginWrite: (cardId: string) => (() => void) | null;
      beginInspection: (cardId: string) => (() => void) | null;
      onConfirmed: (saved: SavedAssignment) => void;
      onChange?: () => void;
    }>,
) {
  const doc = root.ownerDocument;
  const entries = new Map<string, Entry>();
  const tabStorage = () => storage ?? globalThis.sessionStorage;
  let focusedCard: string | undefined;
  let inspectionRequest: AbortController | undefined;

  function button(label: string) {
    const control = doc.createElement("button");
    control.type = "button";
    control.textContent = label;
    return control;
  }

  function field(label: string, action: string) {
    const row = doc.createElement("div");
    row.className = "card-editor-actions";
    const caption = doc.createElement("label");
    caption.textContent = label;
    const select = doc.createElement("select");
    select.required = true;
    caption.append(" ", select);
    const save = button(action);
    row.append(caption, save);
    return { row, select, save, choices: "" };
  }

  const controls = {
    ownerId: field("Owner", "Assign owner"),
    priorityId: field("Priority", "Set priority"),
  };
  const status = doc.createElement("p");
  status.className = "card-editor-status";
  status.setAttribute("role", "status");
  const actions = doc.createElement("div");
  actions.className = "card-editor-actions";
  const inspect = button("Inspect saved assignment");
  const discard = button("Discard assignment draft");
  actions.append(inspect, discard);
  root.append(controls.ownerId.row, controls.priorityId.row, status, actions);

  function key(cardId: string): string {
    return `localswim:v2:card-assignment:${boardId}:${cardId}`;
  }

  function entryFor(cardId: string): Entry {
    let entry = entries.get(cardId);
    if (!entry) {
      entry = { draft: null, raw: undefined };
      entries.set(cardId, entry);
    }
    if (entry.active) return entry;
    try {
      const raw = tabStorage().getItem(key(cardId));
      if (raw !== entry.raw) {
        const stored = raw === null ? null : parseDraft(raw);
        entry.storedSubmission = stored?.phase === "submitted";
        if (entry.unsaved) {
          entry.storageError = true;
          return entry;
        }
        entry.raw = raw;
        entry.draft = stored;
        entry.inspection = undefined;
        entry.inspectionFailed = false;
      }
      entry.storageError = Boolean(entry.unsaved || (raw !== null && entry.draft === null));
    } catch {
      entry.storageError = true;
    }
    return entry;
  }

  function persist(cardId: string, entry: Entry, draft: Draft | null): boolean {
    try {
      const store = tabStorage();
      const storageKey = key(cardId);
      const raw = store.getItem(storageKey);
      if (entry.raw !== undefined && raw !== entry.raw)
        throw new Error("Assignment draft changed.");
      if (entry.raw === undefined && raw !== null) throw new Error("Assignment draft unavailable.");
      const next = draft === null ? null : JSON.stringify(draft);
      if (next === null) store.removeItem(storageKey);
      else store.setItem(storageKey, next);
      entry.raw = next;
      entry.draft = draft;
      entry.storedSubmission = draft?.phase === "submitted";
      entry.unsaved = false;
      entry.storageError = false;
      return true;
    } catch {
      entry.storageError = true;
      return false;
    }
  }

  function options(): Record<Field, readonly Choice[]> {
    const board = getBoardData();
    return {
      ownerId:
        board?.users.map(({ id, label, kind }) => ({
          id,
          label: board.board.actor_presentation?.[kind]?.label ?? label,
        })) ?? [],
      priorityId: board?.priorities ?? [],
    };
  }

  function isStale(draft: Draft, saved: Version) {
    return draft.phase === "stale" || !sameVersion(draft.base, saved);
  }

  function latestAssignment(context: AssignmentContext, entry: Entry) {
    return entry.confirmed && reachesVersion(entry.confirmed, context) ? entry.confirmed : context;
  }

  function canDiscard(entry: Entry, saved: Version): boolean {
    if (entry.active) return false;
    const draft = entry.draft;
    if (entry.unsaved) return true;
    if (!draft) return entry.storageError === true;
    return (
      (draft.phase !== "submitted" && !isStale(draft, saved)) ||
      (entry.inspection !== undefined && sameVersion(entry.inspection, saved))
    );
  }

  function canSave(context: AssignmentContext, entry: Entry, choices: readonly Choice[]) {
    const { draft, active } = entry;
    return (
      context.writable &&
      !active &&
      !entry.storedSubmission &&
      reachesVersion(context, entry.confirmed) &&
      draft !== null &&
      hasGrant(context, draft.field) &&
      (draft.phase === "dirty" || draft.phase === "refused") &&
      !isStale(draft, context) &&
      context[draft.field] !== draft.requested &&
      choices.some(({ id }) => id === draft.requested)
    );
  }

  function update() {
    if (signal.aborted) return;
    const context = getContext();
    if (focusedCard !== context?.cardId) {
      inspectionRequest?.abort();
      focusedCard = context?.cardId;
    }
    root.hidden = context === null;
    if (!context) return;
    const entry = entryFor(context.cardId);
    const { draft, active, confirmed, inspection, result } = entry;
    const saved = latestAssignment(context, entry);
    const choices = options();
    const stale = draft !== null && isStale(draft, saved);
    const uncertain = draft?.phase === "submitted";
    const canInspect = !active && (uncertain || stale);
    const canEdit =
      context.writable && reachesVersion(context, confirmed) && !active && !stale && !uncertain;
    const inspectedCurrent = inspection !== undefined && sameVersion(inspection, saved);
    const label = (name: Field, value: string) =>
      choices[name].find(({ id }) => id === value)?.label ?? "Unavailable";

    for (const name of fields) {
      const control = controls[name];
      const catalog = choices[name];
      const signature = JSON.stringify(catalog);
      if (control.choices !== signature) {
        control.choices = signature;
        control.select.replaceChildren(
          ...catalog.map(({ id, label }) => {
            const option = doc.createElement("option");
            option.value = id;
            option.textContent = label;
            return option;
          }),
        );
      }
      const selected =
        draft?.field === name && catalog.some(({ id }) => id === draft.requested)
          ? draft.requested
          : saved[name];
      if (control.select.value !== selected) control.select.value = selected;
      const granted = hasGrant(context, name);
      control.row.hidden =
        (!granted && draft?.field !== name) || !catalog.some(({ id }) => id === selected);
      control.select.disabled = !canEdit || !granted || (draft !== null && draft.field !== name);
      control.save.hidden = !granted;
      control.save.setAttribute(
        "aria-disabled",
        String(draft?.field !== name || !canSave(context, entry, catalog)),
      );
    }

    inspect.hidden = !canInspect && active !== "inspecting";
    inspect.setAttribute("aria-disabled", String(!canInspect));
    discard.hidden = !draft && !entry.storageError;
    discard.setAttribute("aria-disabled", String(!canDiscard(entry, saved)));
    actions.hidden = inspect.hidden && discard.hidden;

    let message = "";
    if (active === "submitting") message = "Saving assignment…";
    else if (active === "inspecting") message = "Inspecting saved assignment…";
    else if (entry.inspectionFailed)
      message = "Could not inspect the assignment. Inspect again to retry.";
    else if (entry.storageError) message = "Could not save the assignment draft in this tab.";
    else if (draft && (uncertain || stale)) {
      if (inspection && inspectedCurrent) {
        message = `Requested ${labels[draft.field].toLowerCase()}: ${label(draft.field, draft.requested)}. Saved: ${label(draft.field, inspection[draft.field])}.`;
        if (uncertain) message += " The earlier save outcome remains uncertain.";
      } else {
        message = uncertain
          ? "Assignment save outcome is unknown. Inspect the saved assignment before editing again."
          : "The card changed. Inspect the saved assignment before editing again.";
      }
    } else if (draft && !hasGrant(context, draft.field)) {
      message = "You can review or discard this assignment draft.";
    } else if (draft && !choices[draft.field].some(({ id }) => id === draft.requested)) {
      message = "That selection is no longer available. Choose another or discard the draft.";
    } else if (draft?.phase === "refused") message = "The service refused the assignment change.";
    else if (result) {
      const effective = choices[result.field].find(({ id }) => id === result.effective)?.label;
      if (saved[result.field] === result.effective && effective !== undefined) {
        message =
          result.field === "ownerId"
            ? `Owner assigned to ${effective}.`
            : result.requested === result.effective
              ? `Effective priority: ${effective}.`
              : `Dependency floor kept the effective priority at ${effective}.`;
      }
    }
    if (status.textContent !== message) status.textContent = message;
  }

  function publish() {
    onChange?.();
    update();
  }

  function edit(name: Field) {
    const context = getContext();
    if (!context?.writable || !hasGrant(context, name) || controls[name].select.disabled) return;
    const entry = entryFor(context.cardId);
    const requested = controls[name].select.value;
    if (!options()[name].some(({ id }) => id === requested)) return;
    const draft: Draft | null =
      requested === context[name]
        ? null
        : {
            field: name,
            requested,
            base: { revision: context.revision, etag: context.etag },
            phase: "dirty",
          };
    entry.result = undefined;
    if (!persist(context.cardId, entry, draft)) {
      entry.draft = draft;
      entry.unsaved = true;
    }
    publish();
  }

  async function save(name: Field) {
    const context = getContext();
    if (!context) return;
    const { cardId } = context;
    const entry = entryFor(cardId);
    const draft = entry.draft;
    if (draft?.field !== name || !canSave(context, entry, options()[name])) return;
    const release = beginWrite(cardId);
    if (!release) return;
    entry.active = "submitting";
    entry.inspection = undefined;
    entry.inspectionFailed = false;
    const submission: Draft = { ...draft, phase: "submitted" };
    const change =
      name === "ownerId" ? { owner_id: draft.requested } : { priority_id: draft.requested };
    try {
      const outcome = await submitOperation(
        () => persist(cardId, entry, submission),
        async () => {
          publish();
          const saved = readReply(
            await api.patchCard(cardId, context.etag, change),
            boardId,
            cardId,
          );
          const versionAdvanced = saved.revision > context.revision && saved.etag !== context.etag;
          const floorNoOp =
            name === "priorityId" &&
            sameVersion(saved, context) &&
            saved.priorityId === context.priorityId;
          const matchesOwner =
            saved.ownerId === (name === "ownerId" ? draft.requested : context.ownerId);
          if (
            (!versionAdvanced && !floorNoOp) ||
            !matchesOwner ||
            (name === "ownerId" && saved.priorityId !== context.priorityId)
          ) {
            throw new TypeError("The response did not confirm the requested assignment.");
          }
          return saved;
        },
      );
      switch (outcome.kind) {
        case "confirmed": {
          const saved = outcome.result;
          entry.confirmed = saved;
          entry.result = { field: name, requested: draft.requested, effective: saved[name] };
          persist(cardId, entry, null);
          onConfirmed(saved);
          break;
        }
        case "stale":
        case "refused":
          persist(cardId, entry, { ...draft, phase: outcome.kind });
          break;
        case "unknown":
        case "journal-failed":
          break;
      }
    } finally {
      entry.active = undefined;
      release();
      publish();
    }
  }

  async function inspectSaved() {
    const context = getContext();
    if (!context) return;
    const { cardId } = context;
    const entry = entryFor(cardId);
    const draft = entry.draft;
    const latest = latestAssignment(context, entry);
    if (entry.active || !draft || (draft.phase !== "submitted" && !isStale(draft, latest))) return;
    const release = beginInspection(cardId);
    if (!release) {
      update();
      return;
    }
    const request = new AbortController();
    inspectionRequest = request;
    const readSignal = AbortSignal.any([signal, request.signal]);
    entry.active = "inspecting";
    entry.inspectionFailed = false;
    try {
      publish();
      const reply = await readWithSession(() => api.getCard(cardId, readSignal), readSignal);
      readSignal.throwIfAborted();
      const saved = readReply(reply, boardId, cardId);
      const current = getContext();
      if (current?.cardId !== cardId) return;
      if (
        !reachesVersion(saved, context) ||
        !reachesVersion(saved, current) ||
        !reachesVersion(saved, entry.confirmed) ||
        (draft.phase === "stale" && saved.revision <= draft.base.revision)
      ) {
        throw new TypeError("The read did not reach the current assignment version.");
      }
      entry.confirmed = saved;
      entry.inspection = saved;
      onConfirmed(saved);
    } catch {
      if (!readSignal.aborted) entry.inspectionFailed = true;
    } finally {
      if (inspectionRequest === request) inspectionRequest = undefined;
      entry.active = undefined;
      release();
      publish();
    }
  }

  function discardDraft() {
    const context = getContext();
    if (!context) return;
    const entry = entryFor(context.cardId);
    if (!canDiscard(entry, latestAssignment(context, entry))) {
      update();
      return;
    }
    if (entry.unsaved) {
      try {
        tabStorage().getItem(key(context.cardId));
      } catch {
        entry.storageError = true;
        publish();
        return;
      }
      entry.unsaved = false;
      entry.raw = undefined;
      entryFor(context.cardId);
      publish();
      return;
    }
    if (persist(context.cardId, entry, null)) {
      entry.inspection = undefined;
      entry.inspectionFailed = false;
    }
    publish();
  }

  function run(action: () => Promise<void>) {
    void action().catch(() => {
      if (!signal.aborted) status.textContent = "Could not complete the assignment action.";
    });
  }

  for (const name of fields) {
    controls[name].select.addEventListener("change", () => edit(name), { signal });
    controls[name].save.addEventListener("click", () => run(() => save(name)), { signal });
  }
  inspect.addEventListener("click", () => run(inspectSaved), { signal });
  discard.addEventListener("click", discardDraft, { signal });
  update();
  return {
    update,
    hasUncertainSubmission(cardId = focusedCard) {
      if (cardId === undefined) return false;
      const entry = entryFor(cardId);
      return entry.storedSubmission === true || entry.draft?.phase === "submitted";
    },
  };
}
