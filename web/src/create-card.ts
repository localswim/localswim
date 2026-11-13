import { ApiError } from "../api.mjs";
import type { AppContext } from "./model.js";
import { createScopedOperations, submitOperation, type OperationStorage } from "./operations.js";
import {
  alignTextarea,
  applyInput,
  rememberExact,
  textareaText,
  type InputChange,
} from "./text-input.js";

type Choice = Readonly<{ id: string; label: string }>;

export type CreateCardContext = Readonly<{
  etag: string;
  writable: boolean;
  lanes: readonly Choice[];
  users: readonly Choice[];
  priorities: readonly Choice[];
  defaultOwnerId: string;
  defaultPriorityId: string;
}>;

type CreateCardOptions = AppContext &
  Readonly<{
    boardId: string;
    context: () => CreateCardContext | null;
    readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
    onConfirmed: (cardId: string, ticket: number, showCreatedCard: boolean) => void;
    onStale: () => void;
    focusFallback: () => void;
    beginWrite?: () => (() => void) | null;
    beginInspection?: () => (() => void) | null;
    storage?: OperationStorage;
  }>;

type Draft = Readonly<{
  title: string;
  description: string;
  laneId: string;
  ownerId: string;
  priorityId: string;
  staleEtag?: string;
  submission?: Readonly<{
    id: string;
    etag: string;
    ownerId: string;
    priorityId: string;
  }>;
}>;
type SubmittedDraft = Draft & Required<Pick<Draft, "submission">>;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const strongEtag = /^"[A-Za-z0-9_-]{16,128}"(?![\s\S])/;
const titleHasText = /\P{White_Space}/u;

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
  const draft = { title, description, laneId, ownerId, priorityId, staleEtag };
  if (submission === undefined) return draft;
  if (
    !record(submission) ||
    !id(submission.id) ||
    !etag(submission.etag) ||
    !optionalChoice(submission.ownerId) ||
    !optionalChoice(submission.priorityId) ||
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
      etag: submission.etag,
      ownerId: submission.ownerId,
      priorityId: submission.priorityId,
    },
  };
}

function identifyCard(boardId: string, draft: Draft, data: object) {
  if (!draft.submission || !record(data)) return null;
  const { id, ownerId, priorityId } = draft.submission;
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

export function mountCreateCard(host: HTMLElement, options: CreateCardOptions) {
  const { api, boardId, context } = options;
  const doc = host.ownerDocument;
  const lifetime = new AbortController();
  const signal = AbortSignal.any([lifetime.signal, options.signal]);
  const operations = createScopedOperations();
  const key = `localswim:v2:create-card:${boardId}`;
  const storage = () => options.storage ?? doc.defaultView?.sessionStorage;
  let draft: Draft | null = null;
  let initialDraft: Draft | null = null;
  let raw: string | null = null;
  let storedDraftInvalid = false;
  let hasUnstoredChanges = false;
  let opener: HTMLButtonElement | undefined;
  let absence: Readonly<{ id: string; kind: "retry" | "dismiss" }> | undefined;
  let message = "";
  let composing = false;
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
  form.append(field("Title", title), field("Description", description), choices, status, actions);
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

  function canCreate(next: Draft, current: CreateCardContext | null): current is CreateCardContext {
    return Boolean(
      current?.writable &&
      etag(current.etag) &&
      current.etag !== next.staleEtag &&
      current.lanes.some(({ id }) => id === next.laneId) &&
      current.users.some(({ id }) => id === (next.ownerId || current.defaultOwnerId)) &&
      current.priorities.some(({ id }) => id === (next.priorityId || current.defaultPriorityId)),
    );
  }

  function update() {
    if (signal.aborted) return;
    if (!dialog.open) loadStoredDraft();
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
                : "Creation is unavailable for the current selection.");
    if (status.textContent !== nextStatus) status.textContent = nextStatus;
  }

  function edit(
    fields: Partial<Pick<Draft, "title" | "description" | "laneId" | "ownerId" | "priorityId">>,
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
    dialog.close();
    if (draft === initialDraft) draft = null;
    initialDraft = null;
    update();
    if (opener?.isConnected && !opener.disabled && !opener.hidden)
      opener.focus({ preventScroll: true });
    else options.focusFallback();
    opener = undefined;
  }

  function resolveCreation(card: NonNullable<ReturnType<typeof identifyCard>>) {
    const { id, ticket, matchesDraft } = card;
    const wasOpen = dialog.open;
    const difference = `Found the original card #${ticket}. Its saved details differ from the submitted draft.`;
    absence = undefined;
    const cleared = persist(null);
    if (cleared) {
      draft = null;
      if (!signal.aborted) {
        close();
        if (!matchesDraft) recoveryStatus.textContent = difference;
      }
    } else {
      message = matchesDraft
        ? `Found the original card #${ticket}. The stored draft could not be cleared.`
        : `${difference} The stored draft could not be cleared.`;
    }
    if (!signal.aborted) options.onConfirmed(id, ticket, wasOpen && cleared);
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
    });
  }

  async function submitOriginal(submitted: SubmittedDraft, journal: () => boolean) {
    const result = await submitOperation(journal, () => createOriginal(submitted));
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

  async function submit() {
    const current = context();
    if (
      !dialog.open ||
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
    const release = options.beginWrite?.();
    if (release === null) {
      message = "Another board operation is active. Try adding the card again when it finishes.";
      update();
      return;
    }
    await operations.run(
      key,
      "submitting",
      async () => {
        const submission = {
          id: crypto.randomUUID(),
          etag: current.etag,
          ownerId: previous.ownerId === current.defaultOwnerId ? "" : previous.ownerId,
          priorityId: previous.priorityId === current.defaultPriorityId ? "" : previous.priorityId,
        };
        const submitted = { ...previous, submission };
        const result = await submitOriginal(submitted, () => {
          if (!persist(submitted)) return false;
          draft = submitted;
          update();
          return true;
        });
        if (result === "created") return;
        if (result === "journal-failed") {
          message = "The draft could not be stored. Creation requires a stored draft.";
        } else if (result === "stale" || result === "refused") {
          const retained = {
            ...previous,
            staleEtag: result === "stale" ? submission.etag : previous.staleEtag,
          };
          if (persist(retained)) {
            draft = retained;
            message =
              result === "stale"
                ? "The board changed. Review the draft before trying again."
                : "Creation was refused. Your draft is still available.";
          } else {
            message = "Creation was refused. The stored submission could not be cleared.";
          }
          if (result === "stale" && !signal.aborted) options.onStale();
        } else {
          message = "The creation outcome is unknown. Inspect the original card to continue.";
        }
      },
      () => {
        release?.();
        update();
        if (
          !signal.aborted &&
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
          if (signal.aborted) return;
          if (found) {
            recoverCard(submitted, found.data);
            return;
          }

          const baseRevision = boardRevision(boardId, expected);
          const before = await readBoard();
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
          if (signal.aborted) return;
          if (absentAtVersion) {
            recoverCard(submitted, absentAtVersion.data);
            return;
          }
          const after = await readBoard();
          if (signal.aborted) return;
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
          if (signal.aborted) return;
          message = "The original card could not be checked. Inspect again to retry the read.";
        }
      },
      () => {
        release?.();
        update();
      },
    );
  }

  function recoverCard(submitted: Draft, data: object) {
    const card = identifyCard(boardId, submitted, data);
    // A focused read identifies the original even after another actor changes its fields.
    if (card) resolveCreation(card);
    else message = "The read did not identify the original card. Inspect again to retry the read.";
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
    const submitted = { ...draft, submission };
    const release = options.beginWrite?.();
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
        const result = await submitOriginal(submitted, () => persist(submitted));
        if (result === "created") return;
        message = "The original creation remains unresolved. Inspect the original card again.";
        if (result === "stale" && !signal.aborted) options.onStale();
      },
      () => {
        release?.();
        update();
        if (!signal.aborted && dialog.open && !inspect.hidden && doc.activeElement === doc.body) {
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
    if (persist(retained)) {
      draft = retained;
      absence = undefined;
      message = "Unresolved creation dismissed. Your draft is still available.";
      title.focus();
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
