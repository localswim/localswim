import type { MountComponent } from "../contracts.js";

export interface AttachmentUploadRow {
  readonly id: string;
  readonly name: string;
  readonly size: number;
  readonly state: "staged" | "uploading" | "uploaded" | "failed" | "uncertain" | "reselect";
  readonly message?: string;
  readonly canRemove?: boolean;
  readonly canInspect?: boolean;
  readonly canRetry?: boolean;
  readonly canReselect?: boolean;
}

export interface AttachmentUploadViewProps {
  readonly mode: "drawer" | "create";
  readonly enabled: boolean;
  readonly pasteTarget: HTMLElement;
  readonly rows: readonly AttachmentUploadRow[];
  readonly message?: string;
  readonly onSelect: (files: readonly File[]) => void;
  readonly onRemove: (id: string) => void;
  readonly onReselect: (id: string, file: File) => void;
  readonly onInspect: (id: string) => void;
  readonly onRetry: (id: string) => void;
}

const statusText: Record<AttachmentUploadRow["state"], string> = {
  staged: "Ready to upload",
  uploading: "Uploading…",
  uploaded: "Uploaded",
  failed: "Upload failed",
  uncertain: "Upload result unknown",
  reselect: "Reselect the original file to resume",
};

function element<K extends keyof HTMLElementTagNameMap>(tag: K, className = "", text?: string) {
  const node = document.createElement(tag);
  node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function button(className: string, text: string): HTMLButtonElement {
  const node = element("button", className, text);
  node.type = "button";
  return node;
}

function picker(multiple: boolean): HTMLInputElement {
  const node = document.createElement("input");
  node.type = "file";
  node.multiple = multiple;
  return node;
}

function pastedImages(clipboard: DataTransfer): File[] {
  const files = Array.from(clipboard.files).filter(({ type }) => type.startsWith("image/"));
  if (files.length > 0) return files;
  for (const item of clipboard.items) {
    if (item.kind !== "file" || !item.type.startsWith("image/")) continue;
    const file = item.getAsFile();
    if (file !== null) files.push(file);
  }
  return files;
}

function nameScreenClip(file: File): File {
  if (file.name !== "clipboard.png" || file.type !== "image/png") return file;
  return new File([file], `screen-clip-${crypto.randomUUID()}.png`, {
    type: file.type,
    lastModified: file.lastModified,
  });
}

function mountRow(
  id: string,
  create: boolean,
  getProps: () => AttachmentUploadViewProps,
  chooseOriginal: (id: string) => void,
) {
  const lifetime = new AbortController();
  const listeners = { signal: lifetime.signal };
  const node = element("li", "lsw-upload-row");
  const name = element("span", "lsw-upload-name");
  const size = element("span", "lsw-upload-size");
  const status = element("span", "lsw-upload-status");
  status.setAttribute("role", "status");
  const actions = element("div", "lsw-upload-actions");
  const actionClass = create ? "lsw-create-action lsw-upload-action" : "lsw-upload-action";
  const remove = button(actionClass, "Remove");
  const reselect = button(actionClass, "Reselect file");
  const inspect = button(actionClass, "Check result");
  const retry = button(actionClass, "Retry");
  remove.addEventListener("click", () => getProps().onRemove(id), listeners);
  reselect.addEventListener("click", () => chooseOriginal(id), listeners);
  inspect.addEventListener("click", () => getProps().onInspect(id), listeners);
  retry.addEventListener("click", () => getProps().onRetry(id), listeners);
  actions.append(reselect, inspect, retry, remove);
  if (create) node.append(name, " — ", status, actions);
  else node.append(name, size, status, actions);
  return {
    node,
    update(row: AttachmentUploadRow) {
      name.textContent = row.name;
      size.textContent = `${(row.size / 1024).toFixed(1)} KiB`;
      const text =
        row.message ??
        (create && row.state === "staged"
          ? "Added to draft; not yet uploaded"
          : statusText[row.state]);
      if (status.textContent !== text) status.textContent = text;
      status.dataset.uploadState = row.state;
      remove.hidden = row.canRemove !== true;
      reselect.hidden = row.canReselect !== true;
      inspect.hidden = row.canInspect !== true;
      retry.hidden = row.canRetry !== true;
      actions.hidden = remove.hidden && reselect.hidden && inspect.hidden && retry.hidden;
      for (const action of [remove, reselect, inspect, retry]) {
        action.setAttribute("aria-label", `${action.textContent} ${row.name}`);
      }
    },
    dispose() {
      lifetime.abort();
      node.remove();
    },
  };
}

export const mountAttachmentUploadView: MountComponent<AttachmentUploadViewProps> = (
  host,
  initial,
) => {
  let props = initial;
  const create = initial.mode === "create";
  const lifetime = new AbortController();
  const listeners = { signal: lifetime.signal };
  const group = element("div", create ? "lsw-create-field lsw-create-attachments" : "");
  const message = element("p", "lsw-upload-message");
  message.setAttribute("role", "status");
  const list = element("ul", "lsw-upload-list");
  const files = picker(create);
  const open = button("lsw-drawer-upload", "Upload attachment");
  const originals = picker(false);
  originals.hidden = true;
  let reselectId: string | undefined;
  if (create) {
    const label = element("label", "lsw-create-label", "Attachments");
    label.htmlFor = files.id = "lsw-create-files";
    const hint = element(
      "p",
      "lsw-create-hint",
      "Choose files or paste a screen clip (Ctrl+V) anywhere in this form. Files upload after the card is created, up to 10 MiB each. Keep this page open until uploads finish.",
    );
    hint.id = "lsw-create-attachment-hint";
    files.setAttribute("aria-describedby", hint.id);
    group.append(label, files, hint, message);
  } else {
    const hint = element(
      "p",
      "lsw-drawer-hint",
      "Upload a file or paste a screen clip (Ctrl+V) anywhere in this card. Maximum 10 MiB per file.",
    );
    hint.id = "lsw-drawer-attachment-hint";
    open.setAttribute("aria-describedby", hint.id);
    files.hidden = true;
    open.addEventListener("click", () => files.click(), listeners);
    group.append(open, files, message, hint);
  }
  group.append(originals, list);
  host.append(group);

  files.addEventListener(
    "change",
    () => {
      const selected = Array.from(files.files ?? []);
      files.value = "";
      if (props.enabled && selected.length > 0) props.onSelect(selected);
    },
    listeners,
  );
  originals.addEventListener(
    "change",
    () => {
      const selected = originals.files?.[0];
      const id = reselectId;
      originals.value = "";
      reselectId = undefined;
      if (selected !== undefined && id !== undefined) props.onReselect(id, selected);
    },
    listeners,
  );
  originals.addEventListener("cancel", () => (reselectId = undefined), listeners);

  function onPaste(event: ClipboardEvent): void {
    const clipboard = event.clipboardData;
    if (!props.enabled || event.defaultPrevented || clipboard === null) return;
    const images = pastedImages(clipboard);
    if (images.length === 0 || clipboard.getData("text/plain") !== "") return;
    event.preventDefault();
    props.onSelect(images.map(nameScreenClip));
  }

  initial.pasteTarget.addEventListener("paste", onPaste, listeners);
  const rows = new Map<string, ReturnType<typeof mountRow>>();

  function update(next: AttachmentUploadViewProps): void {
    const focused = document.activeElement;
    if (next.pasteTarget !== props.pasteTarget) {
      props.pasteTarget.removeEventListener("paste", onPaste);
      next.pasteTarget.addEventListener("paste", onPaste, listeners);
    }
    props = next;
    files.disabled = open.disabled = !props.enabled;
    const uploading = props.rows.findIndex(({ state }) => state === "uploading");
    const text =
      props.message ??
      (!create && uploading !== -1
        ? `Uploading attachment ${uploading + 1} of ${props.rows.length}...`
        : "");
    if (message.textContent !== text) message.textContent = text;
    message.hidden = text === "";
    const visibleRows = create
      ? props.rows
      : props.rows.filter(
          ({ state, canInspect, canRetry, canRemove }) =>
            state === "failed" ||
            state === "uncertain" ||
            state === "reselect" ||
            (state === "staged" &&
              (canInspect === true || canRetry === true || canRemove === true)),
        );
    list.hidden = visibleRows.length === 0;

    const ids = new Set(visibleRows.map(({ id }) => id));
    for (const [id, row] of rows) {
      if (ids.has(id)) continue;
      const hadFocus = row.node.contains(document.activeElement);
      row.dispose();
      rows.delete(id);
      if (hadFocus) (create ? files : open).focus();
    }
    let position = list.firstElementChild;
    for (const value of visibleRows) {
      let row = rows.get(value.id);
      if (row === undefined) {
        row = mountRow(
          value.id,
          create,
          () => props,
          (id) => {
            reselectId = id;
            originals.click();
          },
        );
        rows.set(value.id, row);
      }
      row.update(value);
      if (row.node !== position) list.insertBefore(row.node, position);
      position = row.node.nextElementSibling;
    }
    if (
      focused instanceof HTMLElement &&
      group.contains(focused) &&
      (focused.matches(":disabled") || focused.getClientRects().length === 0)
    ) {
      const replacement = create ? files : open;
      if (!replacement.disabled) replacement.focus();
      else {
        message.tabIndex = -1;
        if (!message.hidden) message.focus();
      }
    }
  }

  update(initial);
  return {
    update,
    dispose() {
      lifetime.abort();
      for (const row of rows.values()) row.dispose();
      rows.clear();
      group.remove();
    },
  };
};
