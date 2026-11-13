import { ApiError, type RelationshipKind } from "../api.mjs";
import type { AppContext } from "./model.js";
import { createScopedOperations, submitOperation, type OperationStorage } from "./operations.js";
import {
  isRelationshipKind,
  readCardRelationships,
  readRelationshipTarget,
  relationshipKindSelect,
  relationshipTicket,
  type RelationshipTarget,
} from "./relationships.js";
import {
  alignTextarea,
  applyInput,
  rememberExact,
  textareaText,
  type InputChange,
} from "./text-input.js";

type Choice = Readonly<{ id: string; label: string }>;
type SeedDraft = Readonly<{ ticket: string; kind: RelationshipKind }>;
type Seed = Readonly<{ target_card_id: string; kind: RelationshipKind }>;

export type CreateCardContext = Readonly<{
  etag: string;
  writable: boolean;
  lanes: readonly (Choice & { readonly role?: string })[];
  users: readonly Choice[];
  priorities: readonly (Choice & { readonly rank?: number })[];
  defaultOwnerId: string;
  defaultPriorityId: string;
  relationshipTargets?: readonly RelationshipTarget[];
}>;

type CreateCardOptions = AppContext &
  Readonly<{
    boardId: string;
    context: () => CreateCardContext | null;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    onConfirmed: (cardId: string, ticket: number, showCreatedCard: boolean) => void;
    onStale: () => void;
    focusFallback: () => void;
    isPageActive?: () => boolean;
    beginWrite?: (relatedCardIds?: readonly string[]) => (() => void) | null;
    beginInspection?: () => (() => void) | null;
    storage?: OperationStorage;
  }>;

type Draft = Readonly<{
  title: string;
  description: string;
  laneId: string;
  ownerId: string;
  priorityId: string;
  seeds?: readonly SeedDraft[];
  staleEtag?: string;
  submission?: Readonly<{
    id: string;
    dispatchId?: string;
    etag: string;
    ownerId: string;
    priorityId: string;
    effectiveOwnerId?: string;
    effectivePriorityId?: string;
    savedPriorityId?: string;
    seeds?: readonly Seed[];
  }>;
}>;
type SubmittedDraft = Draft & Required<Pick<Draft, "submission">>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const strongEtag = /^"[A-Za-z0-9_-]{16,128}"(?![\s\S])/;
const titleHasText = /\P{White_Space}/u;
const maxSeeds = 1000;

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function id(value: unknown): value is string {
  return typeof value === "string" && value.length === 36 && uuid.test(value);
}

function optionalChoice(value: unknown): value is string {
  return value === "" || id(value);
}

function etag(value: unknown): value is string {
  return typeof value === "string" && strongEtag.test(value);
}

function boardRevision(boardId: string, tag: string): number | null {
  const revision = Number(tag.slice(boardId.length + 2, -1));
  return Number.isSafeInteger(revision) && revision > 0 && tag === `"${boardId}-${revision}"`
    ? revision
    : null;
}

function parseSeedDraft(value: unknown): SeedDraft[] {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > maxSeeds)
    throw new TypeError("Invalid create relationship draft.");
  return (value as unknown[]).map((row) => {
    if (!record(row) || typeof row.ticket !== "string" || !isRelationshipKind(row.kind))
      throw new TypeError("Invalid create relationship draft row.");
    return { ticket: row.ticket, kind: row.kind };
  });
}

function seedKey(target: string | number, kind: RelationshipKind) {
  return `${target}/${kind}`;
}

function validSeedDraft(seeds: readonly SeedDraft[]) {
  const keys = new Set<string>();
  for (const { ticket, kind } of seeds) {
    const number = relationshipTicket(ticket);
    if (number === null) return false;
    const key = seedKey(number, kind);
    if (keys.has(key)) return false;
    keys.add(key);
  }
  return true;
}

function parseSubmittedSeeds(value: unknown, seeds: readonly SeedDraft[], cardId: string): Seed[] {
  if (!Array.isArray(value) || value.length !== seeds.length || !validSeedDraft(seeds)) {
    throw new TypeError("Invalid submitted relationships.");
  }
  const keys = new Set<string>();
  const targets = new Map<number, string>();
  return (value as unknown[]).map((seed, index) => {
    const draftSeed = seeds[index];
    if (
      !draftSeed ||
      !record(seed) ||
      !id(seed.target_card_id) ||
      seed.target_card_id === cardId ||
      seed.kind !== draftSeed.kind
    ) {
      throw new TypeError("Invalid submitted relationship row.");
    }
    const ticket = Number(draftSeed.ticket.replace(/^#/, ""));
    const key = seedKey(seed.target_card_id, draftSeed.kind);
    const previous = targets.get(ticket);
    if (keys.has(key) || (previous !== undefined && previous !== seed.target_card_id))
      throw new TypeError("Conflicting submitted relationship rows.");
    keys.add(key);
    targets.set(ticket, seed.target_card_id);
    return { target_card_id: seed.target_card_id, kind: draftSeed.kind };
  });
}

function parseDraft(raw: string): Draft {
  const value: unknown = JSON.parse(raw);
  if (!record(value)) throw new TypeError("Invalid create draft.");
  const { title, description, laneId, ownerId, priorityId, staleEtag, submission } = value;
  if (
    typeof title !== "string" ||
    typeof description !== "string" ||
    !id(laneId) ||
    !optionalChoice(ownerId) ||
    !optionalChoice(priorityId) ||
    (staleEtag !== undefined && !etag(staleEtag))
  ) {
    throw new TypeError("Invalid create draft fields.");
  }
  const seeds = parseSeedDraft(value.seeds);
  const draft = { title, description, laneId, ownerId, priorityId, staleEtag, seeds };
  if (submission === undefined) return draft;
  if (
    !record(submission) ||
    !id(submission.id) ||
    (submission.dispatchId !== undefined && !id(submission.dispatchId)) ||
    !etag(submission.etag) ||
    !optionalChoice(submission.ownerId) ||
    !optionalChoice(submission.priorityId) ||
    (submission.effectiveOwnerId !== undefined && !id(submission.effectiveOwnerId)) ||
    (submission.effectivePriorityId !== undefined && !id(submission.effectivePriorityId)) ||
    (submission.savedPriorityId !== undefined &&
      (!id(submission.savedPriorityId) || !seeds.some(({ kind }) => kind === "blocks"))) ||
    (ownerId !== "" &&
      submission.effectiveOwnerId !== undefined &&
      submission.effectiveOwnerId !== ownerId) ||
    (priorityId !== "" &&
      submission.effectivePriorityId !== undefined &&
      submission.effectivePriorityId !== priorityId) ||
    (submission.ownerId !== "" && submission.ownerId !== ownerId) ||
    (submission.priorityId !== "" && submission.priorityId !== priorityId) ||
    !titleHasText.test(title) ||
    !title.isWellFormed() ||
    !description.isWellFormed()
  ) {
    throw new TypeError("Invalid create submission.");
  }
  return {
    ...draft,
    submission: {
      id: submission.id,
      dispatchId: submission.dispatchId,
      etag: submission.etag,
      ownerId: submission.ownerId,
      priorityId: submission.priorityId,
      effectiveOwnerId: submission.effectiveOwnerId,
      effectivePriorityId: submission.effectivePriorityId,
      savedPriorityId: submission.savedPriorityId,
      seeds: parseSubmittedSeeds(
        submission.seeds === undefined ? [] : submission.seeds,
        seeds,
        submission.id,
      ),
    },
  };
}

function identifyCard(boardId: string, draft: Draft, data: object) {
  if (!draft.submission || !record(data)) return null;
  const { id } = draft.submission;
  const ownerId = draft.submission.effectiveOwnerId ?? draft.submission.ownerId;
  const priorityId =
    draft.submission.savedPriorityId ??
    draft.submission.effectivePriorityId ??
    draft.submission.priorityId;
  if (
    data.id !== id ||
    data.board_id !== boardId ||
    typeof data.ticket !== "number" ||
    !Number.isSafeInteger(data.ticket) ||
    data.ticket <= 0
  )
    return null;
  return {
    id,
    ticket: data.ticket,
    matchesDraft:
      data.lane_id === draft.laneId &&
      data.title === draft.title &&
      data.description === draft.description &&
      (ownerId === "" || data.owner_id === ownerId) &&
      (priorityId === "" || data.priority_id === priorityId),
  };
}

function seedTargetIds(seeds: readonly Seed[] | undefined) {
  return seeds?.length
    ? [...new Set(seeds.map(({ target_card_id }) => target_card_id))]
    : undefined;
}

export function mountCreateCard(host: HTMLElement, options: CreateCardOptions) {
  const { api, boardId, context } = options;
  const doc = host.ownerDocument;
  const lifetime = new AbortController();
  const signal = AbortSignal.any([lifetime.signal, options.signal]);
  const operations = createScopedOperations();
  const key = `localswim:v2:create-card:${boardId}`;
  const storage = () => options.storage ?? doc.defaultView?.sessionStorage;
  const pageActive = () => !signal.aborted && (options.isPageActive?.() ?? true);
  let draft: Draft | null = null;
  let initialDraft: Draft | null = null;
  let raw: string | null = null;
  let storedDraftInvalid = false;
  let hasUnstoredChanges = false;
  let opener: HTMLButtonElement | undefined;
  let dialogRead: AbortController | undefined;
  let absence: Readonly<{ id: string; kind: "retry" | "dismiss" }> | undefined;
  let message = "";
  let composing = false;
  let superseded = false;
  const resetTextInputs: (() => void)[] = [];
  function loadStoredDraft() {
    if (operations.state(key) || hasUnstoredChanges) return;
    try {
      const stored = storage()?.getItem(key);
      if (stored === undefined || stored === raw) return;
      raw = stored;
      draft = null;
      absence = undefined;
      draft = stored === null ? null : parseDraft(stored);
      storedDraftInvalid = false;
      message = "";
    } catch {
      storedDraftInvalid = true;
    }
  }
  loadStoredDraft();

  function ownsJournal(expected: string | null) {
    try {
      return storage()?.getItem(key) === expected;
    } catch {
      return false;
    }
  }

  function persist(next: Draft | null): boolean {
    try {
      const tab = storage();
      if (!tab || tab.getItem(key) !== raw) return false;
      if (raw !== null && storedDraftInvalid) return false;
      const encoded = next === null ? null : JSON.stringify(next);
      if (encoded === null) tab.removeItem(key);
      else tab.setItem(key, encoded);
      raw = encoded;
      storedDraftInvalid = false;
      hasUnstoredChanges = false;
      return true;
    } catch {
      return false;
    }
  }

  const dialog = doc.createElement("dialog");
  dialog.className = "create-card";
  const heading = doc.createElement("h2");
  heading.id = "create-card-heading";
  heading.textContent = "Add card";
  dialog.setAttribute("aria-labelledby", heading.id);
  const form = doc.createElement("form");
  const title = doc.createElement("textarea");
  title.rows = 1;
  title.required = true;
  const description = doc.createElement("textarea");
  description.rows = 6;
  const lane = doc.createElement("select");
  const owner = doc.createElement("select");
  const priority = doc.createElement("select");
  const status = doc.createElement("p");
  status.className = "create-card-status";
  status.setAttribute("role", "status");
  const actions = doc.createElement("div");
  actions.className = "create-card-actions";
  const add = button("Add card");
  add.className = "create-card-submit";
  const inspect = button("Inspect creation");
  const retry = button("Retry original creation");
  const dismiss = button("Dismiss unresolved creation");
  const cancel = button("Cancel");
  const recovery = button("Inspect pending creation");
  recovery.className = "create-card-recovery";
  const recoveryStatus = doc.createElement("p");
  recoveryStatus.className = "create-card-status";
  recoveryStatus.setAttribute("role", "status");
  actions.append(cancel, dismiss, inspect, retry, add);
  const choices = doc.createElement("div");
  choices.className = "create-card-choices";
  choices.append(field("Destination", lane), field("Owner", owner), field("Priority", priority));
  const relationships = doc.createElement("fieldset");
  relationships.className = "create-card-relationships";
  const relationshipLegend = doc.createElement("legend");
  relationshipLegend.textContent = "Relationships";
  const seedRows = doc.createElement("div");
  seedRows.className = "create-card-relationship-rows";
  const addSeed = button("Add relationship");
  const tickets = doc.createElement("datalist");
  tickets.id = "create-card-relationship-tickets";
  relationships.append(relationshipLegend, seedRows, addSeed, tickets);
  const seedControls: ReturnType<typeof createSeedRow>[] = [];
  let previousTargets: CreateCardContext["relationshipTargets"];
  form.append(
    field("Title", title),
    field("Description", description),
    choices,
    relationships,
    status,
    actions,
  );
  dialog.append(heading, form);
  host.append(dialog, recovery, recoveryStatus);

  function button(label: string) {
    const element = doc.createElement("button");
    element.type = "button";
    element.textContent = label;
    return element;
  }

  function field(label: string, input: HTMLElement) {
    const element = doc.createElement("label");
    element.append(label, input);
    return element;
  }

  function createSeedRow(index: number) {
    const row = doc.createElement("fieldset");
    row.className = "create-card-relationship-row";
    row.setAttribute("aria-label", `Relationship ${index + 1}`);
    const target = doc.createElement("input");
    target.type = "text";
    target.inputMode = "numeric";
    target.setAttribute("list", tickets.id);
    const kind = relationshipKindSelect(doc, "relates_to");
    const remove = button("Remove");
    remove.setAttribute("aria-label", `Remove relationship ${index + 1}`);
    row.append(field("Target ticket", target), field("Relationship kind", kind), remove);
    target.addEventListener("input", () => editSeed(index, { ticket: target.value }), { signal });
    kind.addEventListener(
      "change",
      () => {
        if (isRelationshipKind(kind.value)) editSeed(index, { kind: kind.value });
      },
      { signal },
    );
    remove.addEventListener(
      "click",
      () => {
        edit({ seeds: draft?.seeds?.filter((_, i) => i !== index) });
        const next = seedControls[index] ?? seedControls[index - 1];
        if (next) (next.target.disabled ? next.remove : next.target).focus();
        else (addSeed.hidden ? title : addSeed).focus();
      },
      { signal },
    );
    return { row, target, kind, remove };
  }

  function editSeed(index: number, fields: Partial<SeedDraft>) {
    if (context()?.relationshipTargets === undefined) return;
    edit({ seeds: draft?.seeds?.map((seed, i) => (i === index ? { ...seed, ...fields } : seed)) });
  }

  addSeed.addEventListener(
    "click",
    () => {
      if (!draft || context()?.relationshipTargets === undefined) return;
      const seeds = draft.seeds ?? [];
      if (seeds.length >= maxSeeds) return;
      edit({ seeds: [...seeds, { ticket: "", kind: "relates_to" }] });
      seedControls.at(-1)?.target.focus();
    },
    { signal },
  );

  function renderSeeds(
    seeds: readonly SeedDraft[],
    current: CreateCardContext | null,
    locked: boolean,
  ) {
    const targets = current?.relationshipTargets;
    relationships.hidden = targets === undefined && seeds.length === 0;
    addSeed.hidden = targets === undefined || locked;
    addSeed.disabled = seeds.length >= maxSeeds;
    if (targets !== previousTargets) {
      tickets.replaceChildren(
        ...(targets ?? []).map(({ ticket, title }) => {
          const option = doc.createElement("option");
          option.value = String(ticket);
          option.textContent = title;
          return option;
        }),
      );
      previousTargets = targets;
    }
    while (seedControls.length > seeds.length) seedControls.pop()?.row.remove();
    seeds.forEach(({ ticket, kind }, index) => {
      let controls = seedControls[index];
      if (!controls) {
        controls = createSeedRow(index);
        seedControls.push(controls);
        seedRows.append(controls.row);
      }
      if (controls.target.value !== ticket) controls.target.value = ticket;
      controls.kind.value = kind;
      controls.target.disabled = controls.kind.disabled = locked || targets === undefined;
      controls.remove.disabled = locked;
    });
  }

  function setChoices(
    select: HTMLSelectElement,
    values: readonly Choice[],
    selected: string,
    defaultId?: string,
  ) {
    const defaultChoice = values.find(({ id }) => id === defaultId);
    const next: (Choice & { disabled?: boolean })[] = [...values];
    if (defaultId !== undefined) {
      next.unshift({
        id: "",
        label: defaultChoice ? `Board default (${defaultChoice.label})` : "Board default",
      });
    }
    if (selected && !values.some(({ id }) => id === selected)) {
      next.unshift({ id: selected, label: "Unavailable", disabled: true });
    }
    if (
      select.options.length !== next.length ||
      next.some(
        ({ id, label, disabled }, i) =>
          select.options[i]?.value !== id ||
          select.options[i]?.textContent !== label ||
          select.options[i]?.disabled !== Boolean(disabled),
      )
    ) {
      select.replaceChildren(
        ...next.map(({ id, label, disabled }) => {
          const option = doc.createElement("option");
          option.value = id;
          option.textContent = label;
          option.disabled = Boolean(disabled);
          return option;
        }),
      );
    }
    if (select.value !== selected) select.value = selected;
  }

  function seedIssue(next: Draft, current: CreateCardContext | null) {
    const seeds = next.seeds ?? [];
    if (!seeds.length) return "";
    if (current?.relationshipTargets === undefined)
      return "Remove the relationship rows to create a card without relationship permission.";
    if (!validSeedDraft(seeds))
      return "Enter a ticket for each relationship and remove repeated relationships.";
    return "";
  }

  function canCreate(next: Draft, current: CreateCardContext | null): current is CreateCardContext {
    return Boolean(
      current?.writable &&
      etag(current.etag) &&
      current.etag !== next.staleEtag &&
      current.lanes.some(({ id }) => id === next.laneId) &&
      current.users.some(({ id }) => id === (next.ownerId || current.defaultOwnerId)) &&
      current.priorities.some(({ id }) => id === (next.priorityId || current.defaultPriorityId)) &&
      !seedIssue(next, current),
    );
  }

  function update() {
    if (signal.aborted) return;
    const previous = raw;
    loadStoredDraft();
    if (dialog.open && raw !== previous) {
      close();
      return;
    }
    recovery.hidden = dialog.open || !draft?.submission;
    if (!dialog.open || !draft) return;
    const current = context();
    const active = operations.state(key);
    const locked = Boolean(active || draft.submission);
    title.readOnly = description.readOnly = locked;
    setChoices(lane, current?.lanes ?? [], draft.laneId);
    setChoices(owner, current?.users ?? [], draft.ownerId, current?.defaultOwnerId ?? "");
    setChoices(
      priority,
      current?.priorities ?? [],
      draft.priorityId,
      current?.defaultPriorityId ?? "",
    );
    lane.disabled = owner.disabled = priority.disabled = locked;
    renderSeeds(draft.seeds ?? [], current, locked);
    add.hidden = Boolean(draft.submission);
    add.disabled = composing || !canCreate(draft, current);
    cancel.disabled = composing;
    inspect.hidden = !draft.submission;
    inspect.disabled = Boolean(active);
    const checked =
      draft.submission && draft.submission.id === absence?.id ? absence.kind : undefined;
    retry.hidden = checked !== "retry";
    retry.disabled =
      Boolean(active) || draft.submission?.etag !== current?.etag || !canCreate(draft, current);
    dismiss.hidden = checked !== "dismiss";
    dismiss.disabled = Boolean(active);
    cancel.textContent = draft.submission ? "Close" : "Cancel";
    const nextStatus =
      active === "submitting"
        ? "Adding card…"
        : active === "inspecting"
          ? "Checking the original card…"
          : message ||
            (draft.submission
              ? "The creation outcome is unknown. Inspect the original card to continue."
              : canCreate(draft, current)
                ? ""
                : seedIssue(draft, current) ||
                  "Creation is unavailable for the current selection.");
    if (status.textContent !== nextStatus) status.textContent = nextStatus;
  }

  function edit(
    fields: Partial<
      Pick<Draft, "title" | "description" | "laneId" | "ownerId" | "priorityId" | "seeds">
    >,
  ) {
    if (!dialog.open || !draft || draft.submission || operations.state(key)) return;
    draft = { ...draft, ...fields };
    hasUnstoredChanges = !persist(draft);
    message = hasUnstoredChanges
      ? "The draft could not be stored. Keep this page open to retain it."
      : "";
    update();
  }

  function close() {
    if (!dialog.open) return;
    dialogRead?.abort();
    dialog.close();
    if (draft === initialDraft) draft = null;
    initialDraft = null;
    update();
    if (pageActive()) {
      if (opener?.isConnected && !opener.disabled && !opener.hidden)
        opener.focus({ preventScroll: true });
      else options.focusFallback();
    }
    opener = undefined;
  }

  function finishOperation(release: (() => void) | undefined) {
    release?.();
    if (superseded) {
      superseded = false;
      if (!signal.aborted) close();
    }
    update();
  }

  function resolveCreation(
    card: NonNullable<ReturnType<typeof identifyCard>>,
    matchesRelationships = true,
  ) {
    const { id, ticket, matchesDraft } = card;
    const wasOpen = dialog.open;
    const difference =
      `Found the original card #${ticket}. ` +
      (!matchesDraft ? "Its saved details differ from the submitted draft. " : "") +
      (!matchesRelationships
        ? "Its saved relationships differ from the submitted relationships."
        : "");
    const matches = matchesDraft && matchesRelationships;
    absence = undefined;
    const expected = raw;
    const cleared = persist(null);
    if (cleared) {
      draft = null;
      if (!signal.aborted) {
        close();
        if (!matches) recoveryStatus.textContent = difference.trimEnd();
      }
    } else {
      if (!ownsJournal(expected)) {
        superseded = true;
        return;
      }
      message = matches
        ? `Found the original card #${ticket}. The stored draft could not be cleared.`
        : `${difference.trimEnd()} The stored draft could not be cleared.`;
    }
    if (pageActive()) options.onConfirmed(id, ticket, wasOpen && cleared);
  }

  function createOriginal({ title, description, laneId, submission }: SubmittedDraft) {
    return api.createCard({
      id: submission.id,
      board_id: boardId,
      expected_board_etag: submission.etag,
      title,
      description,
      lane_id: laneId,
      ...(submission.ownerId ? { owner_id: submission.ownerId } : {}),
      ...(submission.priorityId ? { priority_id: submission.priorityId } : {}),
      ...(submission.seeds?.length ? { relationships: [...submission.seeds] } : {}),
    });
  }

  async function submitOriginal(submitted: SubmittedDraft, journal: () => boolean) {
    let dispatched: string | null = null;
    const result = await submitOperation(
      () => {
        if (!journal()) return false;
        dispatched = raw;
        return true;
      },
      () => createOriginal(submitted),
    );
    if (dispatched !== null && !ownsJournal(dispatched)) {
      superseded = true;
      return "superseded";
    }
    if (result.kind === "confirmed") {
      const card = identifyCard(boardId, submitted, result.result.data);
      if (card?.matchesDraft) {
        resolveCreation(card);
        return "created";
      }
      return "unknown";
    }
    return result.kind;
  }

  async function resolveSeeds(
    seeds: readonly SeedDraft[],
    current: CreateCardContext,
    lookupSignal: AbortSignal,
  ) {
    const available = current.relationshipTargets ?? [];
    const targets = new Map(available.map((target) => [target.ticket, target]));
    const resolved: Seed[] = [];
    const blockers: RelationshipTarget[] = [];
    for (const row of seeds) {
      const ticket = relationshipTicket(row.ticket);
      if (ticket === null) return null;
      let target = targets.get(ticket);
      if (!target) {
        const found = await readRelationshipTarget(
          api,
          options.readWithSession,
          boardId,
          ticket,
          lookupSignal,
        );
        if (!found) return null;
        target = found;
        targets.set(ticket, target);
      }
      resolved.push({ target_card_id: target.id, kind: row.kind });
      if (row.kind === "blocks" && target.completed_at === null) blockers.push(target);
    }
    return { seeds: resolved, blockers };
  }

  // A new card has no incoming links. Each unfinished target already has its
  // effective priority, so only its existing rank can promote the new blocker.
  function savedPriority(
    previous: Draft,
    current: CreateCardContext,
    blockers: readonly RelationshipTarget[],
  ) {
    const selected = previous.priorityId || current.defaultPriorityId;
    if (
      !blockers.length ||
      current.lanes.find(({ id }) => id === previous.laneId)?.role === "completed"
    )
      return undefined;
    const ranks = new Map(current.priorities.map(({ id, rank }) => [id, rank]));
    let strongest = selected;
    for (const { priority_id } of blockers) {
      const rank = ranks.get(priority_id);
      const previousRank = ranks.get(strongest);
      if (rank === undefined || previousRank === undefined) throw new ApiError("invalid_response");
      if (rank < previousRank) strongest = priority_id;
    }
    return strongest === selected ? undefined : strongest;
  }

  async function submit() {
    const current = context();
    if (
      !dialog.open ||
      !dialogRead ||
      !draft ||
      draft.submission ||
      composing ||
      operations.state(key) ||
      !canCreate(draft, current)
    )
      return;
    const titleValid = titleHasText.test(draft.title) && draft.title.isWellFormed();
    if (!titleValid || !draft.description.isWellFormed()) {
      message = titleValid ? "Enter a valid description." : "Enter a title containing text.";
      (titleValid ? description : title).focus();
      update();
      return;
    }
    const previous = draft;
    const focused = doc.activeElement;
    const lookupSignal = AbortSignal.any([signal, dialogRead.signal]);
    let release: (() => void) | null | undefined;
    await operations.run(
      key,
      "submitting",
      async () => {
        const seedDraft = previous.seeds ?? [];
        let relationships;
        let savedPriorityId;
        if (seedDraft.length) update();
        try {
          relationships = seedDraft.length
            ? await resolveSeeds(seedDraft, current, lookupSignal)
            : undefined;
          if (relationships)
            savedPriorityId = savedPriority(previous, current, relationships.blockers);
        } catch {
          if (!lookupSignal.aborted)
            message = "Could not check the relationship targets. Your draft is still available.";
          return;
        }
        if (lookupSignal.aborted || !dialog.open || !pageActive()) return;
        if (seedDraft.length && !relationships) {
          message = "Choose a valid ticket on this board for every relationship.";
          return;
        }
        const latest = context();
        if (!canCreate(previous, latest) || latest.etag !== current.etag) {
          message = "The board changed. Review the draft before trying again.";
          return;
        }
        const submission = {
          id: crypto.randomUUID(),
          dispatchId: crypto.randomUUID(),
          etag: current.etag,
          ownerId: previous.ownerId === current.defaultOwnerId ? "" : previous.ownerId,
          priorityId: previous.priorityId === current.defaultPriorityId ? "" : previous.priorityId,
          effectiveOwnerId: previous.ownerId || current.defaultOwnerId,
          effectivePriorityId: previous.priorityId || current.defaultPriorityId,
          savedPriorityId,
          seeds: relationships?.seeds,
        };
        if (submission.seeds?.some(({ target_card_id }) => target_card_id === submission.id)) {
          message = "Choose another target. The card cannot be related to itself.";
          return;
        }
        release = options.beginWrite?.(seedTargetIds(submission.seeds));
        if (release === null) {
          message =
            "Another board operation is active. Try adding the card again when it finishes.";
          return;
        }
        const submitted = { ...previous, submission };
        const result = await submitOriginal(submitted, () => {
          if (!persist(submitted)) return false;
          draft = submitted;
          update();
          return true;
        });
        if (result === "created" || result === "superseded") return;
        if (result === "journal-failed") {
          message = "The draft could not be stored. Creation requires a stored draft.";
        } else if (result === "stale" || result === "refused") {
          const retained = {
            ...previous,
            staleEtag: result === "stale" ? submission.etag : previous.staleEtag,
          };
          const dispatched = raw;
          if (persist(retained)) {
            draft = retained;
            message =
              result === "stale"
                ? "The board changed. Review the draft before trying again."
                : "Creation was refused. Your draft is still available.";
          } else if (!ownsJournal(dispatched)) {
            superseded = true;
            return;
          } else {
            message = "Creation was refused. The stored submission could not be cleared.";
          }
          if (result === "stale" && pageActive()) options.onStale();
        } else {
          message = "The creation outcome is unknown. Inspect the original card to continue.";
        }
      },
      () => {
        finishOperation(release ?? undefined);
        if (
          pageActive() &&
          dialog.open &&
          !inspect.hidden &&
          (doc.activeElement === focused || doc.activeElement === doc.body)
        ) {
          inspect.focus();
        }
      },
    );
  }

  async function inspectCreation() {
    if (!dialog.open || !draft?.submission || operations.state(key)) return;
    const release = options.beginInspection?.();
    if (release === null) {
      message = "Another board operation is active. Inspect again when it finishes.";
      update();
      return;
    }
    const original = draft.submission.id;
    loadStoredDraft();
    if (draft?.submission?.id !== original) {
      release?.();
      close();
      return;
    }
    const submitted = draft;
    const { id, etag: expected } = draft.submission;
    const inspected = raw;
    function stillOwnsInspection() {
      if (signal.aborted) return false;
      if (ownsJournal(inspected)) return true;
      superseded = true;
      return false;
    }
    absence = undefined;
    await operations.run(
      key,
      "inspecting",
      async () => {
        update();
        try {
          const readCard = () =>
            options
              .readWithSession(() => api.getCard(id, signal), signal)
              .catch((error: unknown) => {
                if (error instanceof ApiError && error.status === 404) return null;
                throw error;
              });
          const readBoard = () =>
            options.readWithSession(() => api.getBoardAvailability(boardId, signal), signal);
          const found = await readCard();
          if (!stillOwnsInspection()) return;
          if (found) {
            await recoverCard(submitted, found.data, stillOwnsInspection);
            return;
          }

          const baseRevision = boardRevision(boardId, expected);
          const before = await readBoard();
          if (!stillOwnsInspection()) return;
          if (
            baseRevision === null ||
            before.data.availability !== "open" ||
            before.data.revision < baseRevision
          ) {
            message =
              "The original card is not visible yet. The creation outcome is still unknown.";
            return;
          }

          // Read absence between two views of the same readable board version.
          // A newer version expires the original POST's precondition.
          const absentAtVersion = await readCard();
          if (!stillOwnsInspection()) return;
          if (absentAtVersion) {
            await recoverCard(submitted, absentAtVersion.data, stillOwnsInspection);
            return;
          }
          const after = await readBoard();
          if (!stillOwnsInspection()) return;
          if (after.data.availability !== "open" || after.etag !== before.etag) {
            message =
              "The board changed during inspection. Inspect again to check the original card.";
            return;
          }
          const kind = before.etag === expected ? "retry" : "dismiss";
          absence = { id, kind };
          message =
            kind === "retry"
              ? "The original card is not visible yet. You can inspect again or retry the same creation."
              : "The original card was not found. You can inspect again or dismiss the unresolved creation and keep the draft.";
        } catch {
          if (!stillOwnsInspection()) return;
          message = "The original card could not be checked. Inspect again to retry the read.";
        }
      },
      () => finishOperation(release ?? undefined),
    );
  }

  async function recoverCard(submitted: Draft, data: object, stillOwnsInspection: () => boolean) {
    const card = identifyCard(boardId, submitted, data);
    // A focused read identifies the original even after another actor changes its fields.
    if (!card) {
      message = "The read did not identify the original card. Inspect again to retry the read.";
      return;
    }
    const seeds = submitted.submission?.seeds ?? [];
    if (!seeds.length) {
      resolveCreation(card);
      return;
    }
    const readBoard = () =>
      options.readWithSession(() => api.getBoardAvailability(boardId, signal), signal);
    const before = await readBoard();
    if (!stillOwnsInspection()) return;
    const { data: current } = await options.readWithSession(
      () => api.getCard(card.id, signal),
      signal,
    );
    if (!stillOwnsInspection()) return;
    const links = await readCardRelationships(
      api,
      options.readWithSession,
      boardId,
      card.id,
      signal,
    );
    if (!stillOwnsInspection()) return;
    const after = await readBoard();
    if (!stillOwnsInspection()) return;
    const checked = identifyCard(boardId, submitted, current);
    const revision = record(current) ? current.revision : undefined;
    const base = boardRevision(boardId, submitted.submission?.etag ?? "");
    if (
      !checked ||
      typeof revision !== "number" ||
      base === null ||
      revision <= base ||
      revision > after.data.revision ||
      links.some((link) => link.revision > revision) ||
      before.etag !== after.etag ||
      after.data.availability !== "open"
    ) {
      message =
        "The board changed during inspection. Inspect again to check the original card and relationships.";
      return;
    }
    const expected = new Set(
      seeds.map(({ target_card_id, kind }) => seedKey(target_card_id, kind)),
    );
    const matches =
      links.length === expected.size &&
      links.every(({ source_card_id, target_card_id, kind }) => {
        if (source_card_id === card.id) return expected.delete(seedKey(target_card_id, kind));
        return kind === "relates_to" && expected.delete(seedKey(source_card_id, kind));
      });
    resolveCreation(checked, matches);
  }

  async function retryCreation() {
    const current = context();
    if (
      !dialog.open ||
      !draft?.submission ||
      operations.state(key) ||
      absence?.kind !== "retry" ||
      draft.submission.id !== absence.id ||
      draft.submission.etag !== current?.etag ||
      !canCreate(draft, current)
    )
      return;
    const original = draft.submission.id;
    loadStoredDraft();
    const submission = draft?.submission;
    if (!draft || !submission || submission.id !== original || absence?.id !== original) {
      close();
      return;
    }
    const submitted = { ...draft, submission: { ...submission, dispatchId: crypto.randomUUID() } };
    const release = options.beginWrite?.(seedTargetIds(submission.seeds));
    if (release === null) {
      message = "Another board operation is active. Try again when it finishes.";
      update();
      return;
    }
    absence = undefined;
    await operations.run(
      key,
      "submitting",
      async () => {
        update();
        const result = await submitOriginal(submitted, () => {
          if (!persist(submitted)) return false;
          draft = submitted;
          return true;
        });
        if (result === "created" || result === "superseded") return;
        message = "The original creation remains unresolved. Inspect the original card again.";
        if (result === "stale" && pageActive()) options.onStale();
      },
      () => {
        finishOperation(release ?? undefined);
        if (pageActive() && dialog.open && !inspect.hidden && doc.activeElement === doc.body) {
          inspect.focus();
        }
      },
    );
  }

  function reportActionError() {
    message = "The action could not be completed. Your draft is still available.";
    update();
  }

  function dismissCreation() {
    if (
      !dialog.open ||
      !draft?.submission ||
      operations.state(key) ||
      absence?.kind !== "dismiss" ||
      draft.submission.id !== absence.id
    )
      return;
    const retained = { ...draft, submission: undefined };
    const inspected = raw;
    if (persist(retained)) {
      draft = retained;
      absence = undefined;
      message = "Unresolved creation dismissed. Your draft is still available.";
      title.focus();
    } else if (!ownsJournal(inspected)) {
      close();
      return;
    } else {
      message = "The stored submission could not be cleared. Your draft is still available.";
    }
    update();
  }

  form.addEventListener("submit", (event) => event.preventDefault(), { signal });
  add.addEventListener("click", () => void submit().catch(reportActionError), { signal });
  inspect.addEventListener("click", () => void inspectCreation().catch(reportActionError), {
    signal,
  });
  retry.addEventListener("click", () => void retryCreation().catch(reportActionError), { signal });
  dismiss.addEventListener("click", dismissCreation, { signal });
  recovery.addEventListener(
    "click",
    () => {
      if (signal.aborted || dialog.open) return;
      loadStoredDraft();
      if (draft?.submission) showDialog(recovery, draft);
      else update();
    },
    { signal },
  );
  cancel.addEventListener("click", close, { signal });
  dialog.addEventListener(
    "cancel",
    (event) => {
      event.preventDefault();
      if (!composing) close();
    },
    { signal },
  );
  title.addEventListener(
    "keydown",
    (event) => {
      if (event.key === "Enter" && !event.isComposing && !composing && event.keyCode !== 229) {
        event.preventDefault();
      }
    },
    { signal },
  );

  for (const [control, field] of [
    [title, "title"],
    [description, "description"],
  ] as const) {
    const recent = new Map<string, string | null>();
    let change: InputChange | undefined;
    let pasted: string | undefined;
    let composingChange: InputChange | undefined;
    resetTextInputs.push(() => {
      recent.clear();
      change = undefined;
      pasted = undefined;
      composingChange = undefined;
    });
    function storeText(value: string, history = false) {
      const previous = draft?.[field] ?? "";
      alignTextarea(control, textareaText(value));
      if (previous === value) return;
      if (!history) {
        rememberExact(recent, previous);
        rememberExact(recent, value);
      }
      edit({ [field]: value });
    }
    control.addEventListener(
      "paste",
      (event) => {
        pasted = event.clipboardData?.getData("text/plain");
      },
      { signal },
    );
    control.addEventListener(
      "beforeinput",
      (event) => {
        change = {
          start: control.selectionStart,
          end: control.selectionEnd,
          inserted: event.inputType === "insertFromPaste" ? (pasted ?? event.data) : event.data,
        };
        pasted = undefined;
      },
      { signal },
    );
    control.addEventListener(
      "compositionstart",
      () => {
        composingChange = {
          start: control.selectionStart,
          end: control.selectionEnd,
          inserted: null,
        };
        composing = true;
        update();
      },
      { signal },
    );
    control.addEventListener(
      "compositionend",
      () => {
        composing = false;
        change = undefined;
        storeText(applyInput(draft?.[field] ?? "", control.value, composingChange));
        composingChange = undefined;
        update();
      },
      { signal },
    );
    control.addEventListener(
      "input",
      (event) => {
        if (composing || (event instanceof InputEvent && event.isComposing)) return;
        const previous = draft?.[field] ?? "";
        const inputType = event instanceof InputEvent ? event.inputType : "";
        if ((!change || change.start === change.end) && inputType.startsWith("delete")) {
          const start = control.selectionStart;
          change = {
            start,
            end: start + textareaText(previous).length - control.value.length,
            inserted: null,
          };
        }
        const history = inputType === "historyUndo" || inputType === "historyRedo";
        storeText(
          history
            ? (recent.get(control.value) ?? control.value)
            : applyInput(previous, control.value, change),
          history,
        );
        change = undefined;
      },
      { signal },
    );
  }
  for (const [control, field] of [
    [lane, "laneId"],
    [owner, "ownerId"],
    [priority, "priorityId"],
  ] as const) {
    control.addEventListener("change", () => edit({ [field]: control.value }), { signal });
  }

  function showDialog(trigger: HTMLButtonElement, initial: Draft) {
    recoveryStatus.textContent = "";
    title.value = initial.title;
    description.value = initial.description;
    for (const reset of resetTextInputs) reset();
    composing = false;
    opener = trigger;
    dialogRead = new AbortController();
    dialog.showModal();
    update();
    (initial.submission ? inspect : title).focus();
  }

  update();
  return {
    open(laneId: string, trigger: HTMLButtonElement) {
      const current = context();
      if (
        signal.aborted ||
        dialog.open ||
        !current?.writable ||
        !current.lanes.some(({ id }) => id === laneId)
      )
        return;
      loadStoredDraft();
      if (!draft) {
        draft = { title: "", description: "", laneId, ownerId: "", priorityId: "" };
        initialDraft = draft;
        message = "";
      }
      showDialog(trigger, draft);
    },
    update,
    hasUncertainSubmission: () => Boolean(draft?.submission),
    dispose() {
      lifetime.abort();
      dialog.remove();
      recovery.remove();
      recoveryStatus.remove();
    },
  };
}
