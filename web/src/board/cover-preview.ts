import type { Attachment } from "../../generated/archive/contracts.js";
import { inspectRaster } from "../api/raster-cover.js";

export type CoverState = Readonly<{ status: "loading" | "ready" | "empty" | "unavailable" | "ineligible"; attachmentId?: string; mode: "explicit" | "fallback" }>;
export type CoverPreviewOptions = Readonly<{
  canvas: HTMLCanvasElement;
  fetchBytes: (attachment: Attachment, signal: AbortSignal) => Promise<Uint8Array>;
  onState: (state: CoverState) => void;
  isCurrent: () => boolean;
  priority: "details" | "card";
  observe?: Element;
}>;

type Job = { owner: CoverPreview; run: () => Promise<void>; valid: () => boolean };
// This module is shared by all previews in the window, including eligibility checks.
const pending: Record<"details" | "card", Job[]> = { details: [], card: [] };
let running = 0;
function drain(): void {
  while (running < 2) {
    const job = pending.details.shift() ?? pending.card.shift();
    if (!job) return;
    if (!job.valid()) continue;
    running++;
    void job.run().finally(() => { running--; drain(); });
  }
}
function schedule(priority: "details" | "card", job: Job): void { pending[priority].push(job); drain(); }
function removePending(owner: CoverPreview): void {
  for (const queue of [pending.details, pending.card]) {
    for (let i = queue.length - 1; i >= 0; i--) if (queue[i]!.owner === owner) queue.splice(i, 1);
  }
}

export class CoverPreview {
  private generation = 0;
  private controller: AbortController | undefined;
  private observer: IntersectionObserver | undefined;
  private visible: boolean;
  private attachments: readonly Attachment[] = [];
  private explicitId: string | undefined;
  private disposed = false;
  constructor(private readonly options: CoverPreviewOptions) {
    this.visible = options.priority === "details";
    options.canvas.setAttribute("aria-hidden", "true");
    if (options.priority === "card" && options.observe) {
      this.observer = new IntersectionObserver(entries => {
        const entry = entries.reduce<IntersectionObserverEntry | undefined>((latest, item) =>
          item.target === options.observe && (!latest || item.time >= latest.time) ? item : latest, undefined);
        if (!entry || this.disposed || entry.isIntersecting === this.visible) return;
        this.visible = entry.isIntersecting;
        this.invalidate();
        if (this.visible) this.begin();
      });
      this.observer.observe(options.observe);
    }
  }
  start(attachments: readonly Attachment[], explicitId?: string): void {
    this.invalidate();
    this.attachments = attachments.map(attachment => {
      const copy = structuredClone(attachment);
      Object.freeze(copy.createdIn);
      return Object.freeze(copy);
    }).sort((a, b) => a.createdAt < b.createdAt ? -1 : a.createdAt > b.createdAt ? 1 : a.attachmentId < b.attachmentId ? -1 : a.attachmentId > b.attachmentId ? 1 : 0);
    this.explicitId = explicitId;
    this.begin();
  }
  dispose(): void { this.disposed = true; this.observer?.disconnect(); this.invalidate(); this.attachments = []; }
  private invalidate(): void {
    this.generation++; this.controller?.abort(); this.controller = undefined;
    removePending(this);
    const canvas = this.options.canvas; canvas.width = 0; canvas.height = 0;
    canvas.removeAttribute("role"); canvas.removeAttribute("aria-label"); canvas.setAttribute("aria-hidden", "true");
  }
  private begin(): void {
    if (this.disposed || !this.visible || !this.options.isCurrent()) return;
    const generation = this.generation, mode = this.explicitId ? "explicit" : "fallback";
    const candidates = this.explicitId ? this.attachments.filter(a => a.attachmentId === this.explicitId) : this.attachments;
    const valid = (): boolean => !this.disposed && this.visible && this.generation === generation && this.options.isCurrent();
    const publish = (status: CoverState["status"], attachmentId?: string): void => { if (valid()) this.options.onState({ status, mode, ...(attachmentId ? { attachmentId } : {}) }); };
    if (!candidates.length) { publish(this.explicitId ? "unavailable" : "empty", this.explicitId); return; }
    publish("loading", this.explicitId);
    const scan = (index: number): void => {
      if (!valid()) return;
      if (index >= candidates.length) { publish(mode === "explicit" ? "ineligible" : "empty", this.explicitId); return; }
      const attachment = candidates[index]!;
      schedule(this.options.priority, { owner: this, valid, run: async () => {
        if (!valid()) return;
        const controller = new AbortController(); this.controller = controller;
        let timedOut = false;
        const deadline = setTimeout(() => { timedOut = true; controller.abort(); publish("unavailable", attachment.attachmentId); }, 10_000);
        let bitmap: ImageBitmap | undefined;
        try {
          const bytes = await this.options.fetchBytes(attachment, controller.signal);
          if (!valid() || timedOut) return;
          const admitted = inspectRaster(bytes);
          if (!admitted) { next(); return; }
          try { bitmap = await createImageBitmap(new Blob([new Uint8Array(bytes)], { type: admitted.mime })); }
          catch { if (valid() && !timedOut) next(); return; }
          if (!valid() || timedOut) return;
          if (bitmap.width <= 0 || bitmap.height <= 0 || bitmap.width > 4096 || bitmap.height > 4096 || bitmap.width * bitmap.height > 4_194_304) { next(); return; }
          const scale = Math.min(1, 512 / bitmap.width, 512 / bitmap.height);
          const canvas = this.options.canvas;
          canvas.width = Math.max(1, Math.round(bitmap.width * scale)); canvas.height = Math.max(1, Math.round(bitmap.height * scale));
          const context = canvas.getContext("2d");
          if (!context) { canvas.width = 0; canvas.height = 0; publish("unavailable", attachment.attachmentId); return; }
          if (!valid() || timedOut) { canvas.width = 0; canvas.height = 0; return; }
          context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
          canvas.setAttribute("role", "img"); canvas.removeAttribute("aria-hidden");
          canvas.setAttribute("aria-label", (mode === "explicit" ? "Selected cover: " : "Fallback cover: ") + attachment.name);
          publish("ready", attachment.attachmentId);
        } catch { if (valid() && !timedOut) publish("unavailable", attachment.attachmentId); }
        finally { clearTimeout(deadline); bitmap?.close(); if (this.controller === controller) this.controller = undefined; }
        function next(): void {
          if (!valid()) return;
          setTimeout(() => scan(index + 1), 0);
        }
      } });
    };
    scan(0);
  }
}
