import type { MountComponent } from "../contracts.js";

export interface TextEditorProps {
  readonly field: "title" | "description";
  readonly value: string;
  readonly pending: boolean;
  readonly error: string | null;
  readonly onSave: (value: string) => void;
  readonly onCancel: () => void;
}

// Text controls project stored line endings differently. Map a visible edit
// back to the draft so unaffected line endings keep their original spelling.
function applyControlEdit(
  draft: string,
  previous: string,
  next: string,
  multiline: boolean,
  selection: { readonly start: number; readonly end: number } | null,
): string {
  if (previous === draft) return next;
  if (selection?.start === 0 && selection.end === previous.length) return next;
  if (previous === next) return draft;
  let left = 0;
  const sharedLength = Math.min(previous.length, next.length);
  while (left < sharedLength && previous[left] === next[left]) left++;
  let right = 0;
  while (
    right < sharedLength - left &&
    previous[previous.length - 1 - right] === next[next.length - 1 - right]
  ) {
    right++;
  }
  // Replacing selected text can produce the same prefix or suffix by chance.
  // Use the actual selection when the resulting value still fits its bounds.
  if (selection !== null) {
    const prefix = previous.slice(0, selection.start);
    const suffix = previous.slice(selection.end);
    if (
      next.length >= prefix.length + suffix.length &&
      next.startsWith(prefix) &&
      next.endsWith(suffix)
    ) {
      left = prefix.length;
      right = suffix.length;
    }
  }

  const offsets = [0];
  const endings = /\r\n?|\n/g;
  let raw = 0;
  for (const match of draft.matchAll(endings)) {
    while (raw < match.index) offsets.push(++raw);
    raw = match.index + match[0].length;
    if (multiline) offsets.push(raw);
  }
  while (raw < draft.length) offsets.push(++raw);
  const start = offsets[left] ?? 0;
  const end = offsets[previous.length - right] ?? draft.length;
  const hidden = multiline ? "" : (draft.slice(start, end).match(/^[\r\n]+/)?.[0] ?? "");
  const prefix = draft.slice(0, start) + hidden;
  const middle = next.slice(left, next.length - right);
  const suffix = draft.slice(end);
  // A new adjacent CR and LF would otherwise collapse two visible line breaks.
  const join = (before: string, after: string) =>
    (multiline && before.endsWith("\r") && after.startsWith("\n")
      ? before.slice(0, -1) + "\n"
      : before) + after;
  return join(join(prefix, middle), suffix);
}

export const mountTextEditor: MountComponent<TextEditorProps> = (host, initial) => {
  let props = initial;
  let draft = initial.value;
  const title = initial.field === "title";
  const lifetime = new AbortController();
  const listeners = { signal: lifetime.signal };
  const editor = document.createElement("div");
  editor.className = `lsw-drawer-edit lsw-drawer-edit-${initial.field}`;
  const field = title ? document.createElement("input") : document.createElement("textarea");
  field.className = "lsw-drawer-edit-field";
  field.setAttribute("aria-label", title ? "Card title" : "Card description");
  field.value = draft;
  let visible = field.value;
  let selection: { readonly start: number; readonly end: number } | null = null;

  const feedback = document.createElement("p");
  feedback.className = "lsw-drawer-edit-error";
  feedback.id = `lsw-drawer-edit-${initial.field}-error`;
  feedback.setAttribute("role", "alert");
  let validation = "";

  const actions = document.createElement("div");
  actions.className = "lsw-drawer-edit-actions";
  const save = document.createElement("button");
  save.type = "button";
  save.className = "lsw-drawer-edit-save";
  save.textContent = "Save description";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "lsw-drawer-text-action";
  cancel.textContent = "Cancel";
  actions.append(save, cancel);
  editor.append(field);
  if (!title) editor.append(actions);
  editor.append(feedback);

  function showError(): void {
    const error = validation || props.error || "";
    feedback.hidden = error === "";
    if (feedback.textContent !== error) feedback.textContent = error;
    if (error === "") field.removeAttribute("aria-describedby");
    else field.setAttribute("aria-describedby", feedback.id);
    if (validation === "") field.removeAttribute("aria-invalid");
    else field.setAttribute("aria-invalid", "true");
  }

  function capture(useSelection: boolean): void {
    draft = applyControlEdit(draft, visible, field.value, !title, useSelection ? selection : null);
    visible = field.value;
    selection = null;
  }

  function submit(): void {
    if (props.pending) return;
    capture(false);
    if (title && !/[^\p{White_Space}]/u.test(draft)) {
      validation = "Title is required.";
      showError();
      field.focus({ preventScroll: true });
      return;
    }
    if (draft === initial.value && props.error === null) props.onCancel();
    else props.onSave(draft);
  }

  function dismiss(): void {
    if (!props.pending) props.onCancel();
  }

  field.addEventListener(
    "beforeinput",
    () => {
      const { selectionStart, selectionEnd } = field;
      if (selectionStart !== null && selectionEnd !== null) {
        selection = { start: selectionStart, end: selectionEnd };
      }
    },
    listeners,
  );
  field.addEventListener(
    "input",
    () => {
      capture(true);
      if (validation === "") return;
      validation = "";
      showError();
    },
    listeners,
  );
  editor.addEventListener(
    "keydown",
    (event) => {
      if (event.isComposing || event.defaultPrevented) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        dismiss();
      } else if (title && event.key === "Enter") {
        event.preventDefault();
        submit();
      }
    },
    listeners,
  );
  save.addEventListener("click", submit, listeners);
  cancel.addEventListener("click", dismiss, listeners);

  function update(next: TextEditorProps): void {
    props = next;
    if (next.pending && (save.matches(":focus") || cancel.matches(":focus"))) {
      field.focus({ preventScroll: true });
    }
    field.readOnly = next.pending;
    save.disabled = cancel.disabled = next.pending;
    editor.setAttribute("aria-busy", String(next.pending));
    showError();
  }

  host.append(editor);
  update(initial);
  field.focus({ preventScroll: true });
  if (title) field.select();
  else field.setSelectionRange(visible.length, visible.length);
  return {
    update,
    dispose() {
      lifetime.abort();
      editor.remove();
    },
  };
};
