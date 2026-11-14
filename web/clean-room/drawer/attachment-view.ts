import type { Uuid } from "../api-types.js";
import type { MountComponent } from "../contracts.js";
import { readAttachmentContent, type Attachment } from "./attachment-data.js";

export interface AttachmentViewProps {
  readonly attachments: readonly Attachment[] | null;
  readonly coverId: Uuid | null;
  readonly canManage: boolean;
  readonly pending: boolean;
  readonly message?: string;
  readonly onCover: (id: Uuid | null) => void;
  readonly onDetach: (attachment: Attachment) => void;
}

export function displayedCover(
  attachments: readonly Attachment[],
  coverId: Uuid | null,
): Attachment | undefined {
  const images = attachments.filter(({ active, previewable }) => active && previewable);
  return images.find(({ id }) => id === coverId) ?? images[0];
}

export async function attachmentThumbnail(original: Blob, width: number, height: number) {
  const bitmap = await createImageBitmap(original);
  try {
    const displayScale = Math.min(1, width / bitmap.width, height / bitmap.height);
    const rasterScale = Math.min(
      1,
      (width * devicePixelRatio) / bitmap.width,
      (height * devicePixelRatio) / bitmap.height,
    );
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * rasterScale));
    canvas.height = Math.max(1, Math.round(bitmap.height * rasterScale));
    const context = canvas.getContext("2d");
    if (!context) throw new Error("Preview unavailable");
    context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    const bytes = await new Promise<Blob | null>((resolve) => {
      canvas.toBlob(resolve, "image/webp", 0.88);
    });
    if (!bytes) throw new Error("Preview unavailable");
    return {
      bytes,
      width: Math.max(1, Math.round(bitmap.width * displayScale)),
      height: Math.max(1, Math.round(bitmap.height * displayScale)),
    };
  } finally {
    bitmap.close();
  }
}

function element<K extends keyof HTMLElementTagNameMap>(tag: K, name: string, text?: string) {
  const node = document.createElement(tag);
  node.className = `lsw-attachment-${name}`;
  if (text !== undefined) node.textContent = text;
  return node;
}

function button(name: string, label: string): HTMLButtonElement {
  const control = element("button", name, label);
  control.type = "button";
  return control;
}

function saveFile(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function createRow(attachment: Attachment, getProps: () => AttachmentViewProps) {
  const lifetime = new AbortController();
  const { signal } = lifetime;
  const node = element("li", "row");
  const preview = element("a", "preview");
  preview.hidden = true;
  preview.target = "_blank";
  preview.rel = "noopener noreferrer";
  const image = element("img", "image");
  image.alt = `Preview ${attachment.original_name}`;
  preview.append(image);
  const download = button("download", attachment.original_name);
  const size = element("p", "size", `${(attachment.size / 1024).toFixed(1)} KiB`);
  const actions = element("div", "actions");
  const cover = button("action", "Set cover");
  const detach = button("action", "Remove");
  detach.setAttribute("aria-label", `Remove ${attachment.original_name}`);
  if (attachment.previewable) actions.append(cover);
  actions.append(detach);
  const downloadError = element("p", "error", "Download unavailable. Try again.");
  downloadError.setAttribute("role", "status");
  downloadError.hidden = true;
  node.append(preview, download, size, downloadError, actions);

  let url: string | undefined;
  let thumbnailUrl: string | undefined;
  function releasePreview(): void {
    if (url !== undefined) URL.revokeObjectURL(url);
    url = undefined;
    if (thumbnailUrl !== undefined) URL.revokeObjectURL(thumbnailUrl);
    thumbnailUrl = undefined;
    preview.removeAttribute("href");
    image.removeAttribute("src");
    preview.hidden = true;
  }

  async function loadPreview(): Promise<void> {
    try {
      const blob = await readAttachmentContent(attachment, "inline", signal);
      if (signal.aborted) return;
      url = URL.createObjectURL(blob);
      preview.href = url;
      const thumbnail = await attachmentThumbnail(blob, 400, 200);
      if (signal.aborted) return;
      thumbnailUrl = URL.createObjectURL(thumbnail.bytes);
      image.width = thumbnail.width;
      image.height = thumbnail.height;
      image.src = thumbnailUrl;
    } catch {
      if (!signal.aborted && url !== undefined) showEmptyPreview();
    }
  }

  function showEmptyPreview(): void {
    if (thumbnailUrl !== undefined) URL.revokeObjectURL(thumbnailUrl);
    thumbnailUrl = undefined;
    image.remove();
    preview.setAttribute("aria-label", `Preview ${attachment.original_name}`);
    preview.classList.add("lsw-attachment-preview-empty");
    preview.hidden = false;
  }

  let downloading = false;
  async function downloadFile(): Promise<void> {
    if (downloading) return;
    downloading = true;
    download.setAttribute("aria-disabled", "true");
    downloadError.hidden = true;
    try {
      const blob = await readAttachmentContent(attachment, "attachment", signal);
      if (!signal.aborted) saveFile(blob, attachment.original_name);
    } catch {
      if (!signal.aborted) downloadError.hidden = false;
    } finally {
      downloading = false;
      download.removeAttribute("aria-disabled");
    }
  }

  image.addEventListener("load", () => (preview.hidden = false), { signal });
  image.addEventListener("error", showEmptyPreview, { signal });
  download.addEventListener("click", () => void downloadFile(), { signal });
  cover.addEventListener(
    "click",
    () => {
      const { coverId, onCover } = getProps();
      onCover(coverId === attachment.id ? null : attachment.id);
    },
    { signal },
  );
  detach.addEventListener("click", () => getProps().onDetach(attachment), { signal });

  const observer = attachment.previewable
    ? new IntersectionObserver((entries) => {
        if (signal.aborted || !entries.some((entry) => entry.isIntersecting)) return;
        observer?.disconnect();
        void loadPreview();
      })
    : undefined;
  observer?.observe(node);

  return {
    attachment,
    node,
    update(props: AttachmentViewProps) {
      actions.hidden = !props.canManage;
      cover.disabled = detach.disabled = props.pending;
      cover.textContent = props.coverId === attachment.id ? "Clear cover" : "Set cover";
    },
    dispose() {
      observer?.disconnect();
      lifetime.abort();
      releasePreview();
      node.remove();
    },
  };
}

export const mountAttachmentView: MountComponent<AttachmentViewProps> = (host, initial) => {
  let props = initial;
  const section = element("div", "view");
  const status = element("p", "error");
  status.setAttribute("role", "status");
  const empty = document.createElement("p");
  empty.className = "lsw-drawer-empty";
  empty.textContent = "No attachments.";
  const list = element("ul", "list");
  section.append(status, empty, list);
  host.append(section);
  const rows = new Map<Uuid, ReturnType<typeof createRow>>();

  function update(next: AttachmentViewProps): void {
    props = next;
    status.hidden = next.message === undefined || next.message === "";
    status.textContent = next.message ?? "";
    const attachments = next.attachments?.filter(({ active }) => active);
    empty.hidden = attachments?.length !== 0;
    const ids = new Set(attachments?.map(({ id }) => id));
    for (const [id, row] of rows) {
      if (ids.has(id)) continue;
      row.dispose();
      rows.delete(id);
    }
    let position = list.firstElementChild;
    for (const attachment of attachments ?? []) {
      let row = rows.get(attachment.id);
      if (row?.attachment.revision !== attachment.revision) {
        if (row !== undefined && row.node === position) position = row.node.nextElementSibling;
        row?.dispose();
        row = createRow(attachment, () => props);
        rows.set(attachment.id, row);
      }
      row.update(next);
      if (row.node !== position) list.insertBefore(row.node, position);
      position = row.node.nextElementSibling;
    }
  }

  update(initial);
  return {
    update,
    dispose() {
      for (const row of rows.values()) row.dispose();
      rows.clear();
      section.remove();
    },
  };
};
