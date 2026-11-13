import type { CardText, CardTextController, CardTextState } from "./drafts.js";

type Field = keyof CardText;
type Surfaces = Readonly<Record<Field, HTMLElement>>;
type InputChange = Readonly<{ start: number; end: number; inserted: string | null }>;

const fieldLabels = { title: "Title", description: "Description" } as const;
const fields = ["title", "description"] as const;
const titleHasText = /\P{White_Space}/u;
const recentValueCount = 8;
const recentValueMaxLength = 32_768;

const phaseMessages = {
  idle: "",
  dirty: "Unsaved changes.",
  stale: "The saved card changed. Inspect the saved text before retrying.",
  submitting: "Saving changes…",
  unknown: "The save outcome is unknown. Inspect the saved text or dismiss the uncertain save.",
  inspecting: "Checking the saved text…",
  refused: "The save was refused. Your draft is still available.",
  "storage-error": "The draft could not be stored. Keep this page open to retain any changes.",
  "storage-conflict": "Another page changed this tab’s draft. Use the tab’s draft to continue.",
} satisfies Record<CardTextState["phase"], string>;

function textareaText(text: string): string {
  return text.replace(/\r\n?/g, "\n");
}

function rememberExact(recent: Map<string, string | null>, value: string) {
  // A CRLF uses two raw code units for one textarea code unit.
  if (value.length > 2 * recentValueMaxLength) return;
  const normalized = textareaText(value);
  if (normalized.length > recentValueMaxLength) return;
  const previous = recent.get(normalized);
  recent.delete(normalized);
  recent.set(normalized, previous === undefined || previous === value ? value : null);
  if (recent.size > recentValueCount) {
    const oldest = recent.keys().next();
    if (!oldest.done) recent.delete(oldest.value);
  }
}

function rawOffset(text: string, offset: number): number {
  // Each CRLF before this boundary adds one code unit to the textarea offset.
  for (const { index } of text.matchAll(/\r\n/g)) {
    if (index >= offset) break;
    offset++;
  }
  return offset;
}

function applyInput(previous: string, value: string, change?: InputChange): string {
  const normalized = textareaText(previous);
  let start = 0;
  let end = normalized.length;
  let inserted = value;
  const after = change ? normalized.slice(change.end) : "";
  if (
    change &&
    change.start >= 0 &&
    change.start <= change.end &&
    change.end <= normalized.length &&
    value.startsWith(normalized.slice(0, change.start)) &&
    value.endsWith(after) &&
    value.length >= change.start + after.length
  ) {
    start = change.start;
    end = change.end;
    inserted = value.slice(start, value.length - after.length);
    if (change.inserted !== null && textareaText(change.inserted) === inserted) {
      inserted = change.inserted;
    } else if (value === normalized) {
      return previous;
    }
  } else {
    if (value === normalized) return previous;
    while (start < normalized.length && normalized[start] === value[start]) start++;
    let valueEnd = value.length;
    while (end > start && valueEnd > start && normalized[end - 1] === value[valueEnd - 1]) {
      end--;
      valueEnd--;
    }
    inserted = value.slice(start, valueEnd);
  }

  // Only the changed span comes from the textarea. Its value normalizes existing CR/CRLF.
  const prefix = previous.slice(0, rawOffset(previous, start));
  const suffix = previous.slice(rawOffset(previous, end));
  // A join between a bare CR and LF would otherwise collapse two visible line breaks.
  if (inserted.endsWith("\r") && suffix.startsWith("\n")) inserted += "\r";
  if (prefix.endsWith("\r") && (inserted || suffix).startsWith("\n")) inserted = "\r" + inserted;
  return prefix + inserted + suffix;
}

export function mountCardEditor(
  root: HTMLElement,
  controller: CardTextController,
  getConfirmed: () => CardText | null | undefined,
  surfaces: Surfaces,
) {
  const doc = root.ownerDocument;
  const lifetime = new AbortController();
  const { signal } = lifetime;
  const open = new Set<Field>();
  let composition:
    { field: Field; text: CardText; previous: string; change?: InputChange } | undefined;
  let lastField: Field = "title";
  let actionError = false;

  function button(label: string) {
    const node = doc.createElement("button");
    node.type = "button";
    node.textContent = label;
    return node;
  }

  const form = doc.createElement("form");
  const status = doc.createElement("p");
  status.className = "card-editor-status";
  status.setAttribute("role", "status");
  const saved = doc.createElement("div");
  saved.className = "card-editor-saved";
  saved.tabIndex = 0;
  saved.setAttribute("role", "region");
  saved.setAttribute("aria-label", "Saved text for comparison");
  const actions = doc.createElement("div");
  actions.className = "card-editor-actions";
  const save = button("Save changes");
  save.type = "submit";
  const inspect = button("Inspect saved");
  const dismiss = button("Dismiss draft");
  actions.append(save, inspect, dismiss);
  form.append(status, actions, saved);
  root.append(form);

  function makeField(field: Field) {
    const surface = surfaces[field];
    const label = fieldLabels[field];
    const edit = button(`Edit ${label.toLowerCase()}`);
    edit.className = "card-editor-open";
    const input = doc.createElement("textarea");
    input.className = `card-editor-input card-editor-${field}`;
    input.rows = field === "title" ? 2 : 6;
    input.setAttribute("aria-label", label);
    if (field === "description") {
      surface.before(edit);
      surface.after(input);
    } else {
      surface.after(edit, input);
    }
    const savedRow = doc.createElement("div");
    const savedLabel = doc.createElement("strong");
    savedLabel.textContent = `Saved ${label.toLowerCase()}`;
    const savedText = doc.createElement("p");
    savedRow.append(savedLabel, savedText);
    saved.append(savedRow);
    return { surface, edit, input, savedRow, savedText, recent: new Map<string, string | null>() };
  }

  const controls = { title: makeField("title"), description: makeField("description") };

  function update() {
    if (signal.aborted) return;
    const state = controller.snapshot();
    const confirmed = getConfirmed();
    const text = state.draft ?? confirmed;
    const compare = Boolean(confirmed && (state.phase === "stale" || state.phase === "unknown"));
    let showField = false;
    let showSaved = false;
    for (const field of fields) {
      const { surface, edit, input, savedRow, savedText, recent } = controls[field];
      const changed = state.changedFields.includes(field);
      const editing = Boolean(text && (open.has(field) || changed || composition?.field === field));
      surface.hidden = editing;
      edit.hidden = editing || !state.canEdit;
      input.hidden = !editing;
      input.readOnly = composition ? composition.field !== field : !state.canEdit;
      if (editing && text && composition?.field !== field) {
        const visible = textareaText((!changed && confirmed ? confirmed : text)[field]);
        if (input.value !== visible) {
          recent.clear();
          input.value = visible;
        }
      }
      const showSavedField = compare && changed;
      savedRow.hidden = !showSavedField;
      if (showSavedField && confirmed && savedText.textContent !== confirmed[field]) {
        savedText.textContent = confirmed[field];
      }
      showField ||= editing;
      showSaved ||= showSavedField;
    }
    saved.hidden = !showSaved;
    const saveLabel = state.phase === "stale" ? "Retry save" : "Save changes";
    if (save.textContent !== saveLabel) save.textContent = saveLabel;
    save.hidden = !showField || state.phase === "unknown" || state.phase === "inspecting";
    save.setAttribute("aria-disabled", String(!state.canSubmit || Boolean(composition)));
    inspect.hidden = !state.canInspect;
    dismiss.hidden = !state.canDismiss;
    inspect.setAttribute("aria-disabled", String(Boolean(composition)));
    dismiss.setAttribute("aria-disabled", String(Boolean(composition)));
    const dismissLabel =
      state.phase === "storage-conflict"
        ? "Use tab’s draft"
        : state.phase === "unknown"
          ? "Dismiss uncertain save"
          : "Dismiss draft";
    if (dismiss.textContent !== dismissLabel) dismiss.textContent = dismissLabel;
    actions.hidden = save.hidden && inspect.hidden && dismiss.hidden;

    let message = phaseMessages[state.phase];
    const blankTitle = Boolean(state.draft && !titleHasText.test(state.draft.title));
    controls.title.input.setAttribute("aria-invalid", String(blankTitle));
    if (state.phase === "dirty" && blankTitle) message = "Enter a title containing text.";
    if (state.phase === "stale" && state.canSubmit) message = "Review the saved text, then retry.";
    if (state.inspectionFailed) message = "Could not check the saved text. Inspect again to retry.";
    if (actionError) message = "Could not complete the action. Your draft may still be available.";
    if (status.textContent !== message) status.textContent = message;
  }

  function focusField(field: Field) {
    const { surface, edit, input } = controls[field];
    (input.hidden ? (edit.hidden ? surface : edit) : input).focus({ preventScroll: true });
  }

  function updateAfterAction(focused: Element | null) {
    update();
    const active = doc.activeElement;
    if (active !== focused && active !== doc.body) return;
    if (
      [save, inspect, dismiss, controls.title.input, controls.description.input].some(
        (node) => node === focused && node.hidden,
      )
    ) {
      focusField(lastField);
    }
  }

  async function run(action: "submit" | "inspect") {
    const state = controller.snapshot();
    if (composition || !(action === "submit" ? state.canSubmit : state.canInspect)) return;
    const focused = doc.activeElement;
    actionError = false;
    try {
      const result = controller[action]();
      updateAfterAction(focused);
      await result;
      if (controller.snapshot().phase === "idle") open.clear();
    } catch {
      actionError = true;
    }
    if (!signal.aborted) updateAfterAction(focused);
  }

  form.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      void run("submit");
    },
    { signal },
  );
  inspect.addEventListener("click", () => void run("inspect"), { signal });
  dismiss.addEventListener(
    "click",
    () => {
      if (composition || !controller.snapshot().canDismiss) return;
      actionError = false;
      for (const field of fields) controls[field].recent.clear();
      controller.dismiss();
      if (controller.snapshot().phase === "idle") open.clear();
      updateAfterAction(dismiss);
    },
    { signal },
  );

  function editField(
    field: Field,
    text: CardText,
    previous: string,
    value: string,
    history = false,
  ) {
    if (value !== previous) {
      actionError = false;
      if (!history) {
        const { recent } = controls[field];
        rememberExact(recent, previous);
        rememberExact(recent, value);
      }
      controller.edit({ ...text, [field]: value }, field);
    } else {
      controller.cancelEdit();
    }
    update();
  }

  for (const field of fields) {
    const { edit, input, recent } = controls[field];
    let change: InputChange | undefined;
    let pasted: string | undefined;
    edit.addEventListener(
      "click",
      () => {
        if (!controller.snapshot().canEdit) return;
        lastField = field;
        open.add(field);
        update();
        focusField(field);
      },
      { signal },
    );
    input.addEventListener(
      "focus",
      () => {
        lastField = field;
      },
      { signal },
    );
    input.addEventListener(
      "paste",
      (event) => {
        pasted = event.clipboardData?.getData("text/plain");
      },
      { signal },
    );
    input.addEventListener(
      "beforeinput",
      (event) => {
        change = {
          start: input.selectionStart,
          end: input.selectionEnd,
          inserted: event.inputType === "insertFromPaste" ? (pasted ?? event.data) : event.data,
        };
        pasted = undefined;
      },
      { signal },
    );
    function startComposition() {
      const state = controller.snapshot();
      const confirmed = getConfirmed();
      const text = state.draft ?? confirmed;
      if (composition || !state.canEdit || !text) return;
      controller.beginEdit();
      const previous = (!state.changedFields.includes(field) && confirmed ? confirmed : text)[
        field
      ];
      composition = { field, text, previous, change };
      update();
    }
    input.addEventListener(
      "compositionstart",
      () => {
        change = { start: input.selectionStart, end: input.selectionEnd, inserted: null };
        startComposition();
      },
      { signal },
    );
    input.addEventListener(
      "input",
      (event) => {
        if (event instanceof InputEvent && event.isComposing) startComposition();
        if (composition?.field === field) return;
        const state = controller.snapshot();
        const confirmed = getConfirmed();
        const text = state.draft ?? confirmed;
        if (state.canEdit && text) {
          const previous = (!state.changedFields.includes(field) && confirmed ? confirmed : text)[
            field
          ];
          if (
            (!change || change.start === change.end) &&
            event instanceof InputEvent &&
            event.inputType.startsWith("delete")
          ) {
            // Word and grapheme deletions can exceed a collapsed beforeinput selection.
            const start = input.selectionStart;
            const end = start + textareaText(previous).length - input.value.length;
            change = { start, end, inserted: null };
          }
          const isHistory =
            event instanceof InputEvent &&
            (event.inputType === "historyUndo" || event.inputType === "historyRedo");
          let exact: string | undefined;
          if (isHistory) {
            // Native undo/redo supplies only normalized text. Reuse exact recent
            // text when that view identifies a single line-ending variant.
            const known = recent.get(input.value);
            const saved = confirmed?.[field];
            if (saved !== undefined && input.value === textareaText(saved)) {
              if (known === undefined || known === saved) exact = saved;
            } else if (known !== null) {
              exact = known;
            }
          }
          const value = isHistory
            ? (exact ?? input.value)
            : applyInput(previous, input.value, change);
          editField(field, text, previous, value, isHistory);
        }
        change = undefined;
      },
      { signal },
    );
    input.addEventListener(
      "compositionend",
      () => {
        if (composition?.field !== field) return;
        const previous = composition;
        composition = undefined;
        change = undefined;
        editField(
          field,
          previous.text,
          previous.previous,
          applyInput(previous.previous, input.value, previous.change),
        );
      },
      { signal },
    );
    input.addEventListener(
      "keydown",
      (event) => {
        if (event.isComposing || composition) {
          if (event.key === "Escape") event.stopPropagation();
          return;
        }
        if (
          event.key === "Enter" &&
          ((field === "title" && !event.shiftKey) || event.ctrlKey || event.metaKey)
        ) {
          event.preventDefault();
          void run("submit");
        } else if (event.key === "Escape" && !controller.snapshot().changedFields.includes(field)) {
          event.preventDefault();
          open.delete(field);
          update();
          focusField(field);
        }
      },
      { signal },
    );
  }

  update();
  return {
    update,
    dispose() {
      lifetime.abort();
      if (composition) controller.cancelEdit();
      for (const { surface, edit, input } of Object.values(controls)) {
        surface.hidden = false;
        edit.remove();
        input.remove();
      }
      form.remove();
    },
  };
}
