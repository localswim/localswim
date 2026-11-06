import type { Attachment } from "../../generated/archive/contracts.js";
import { CoverPreview } from "./cover-preview.js";
import type { CoverState } from "./cover-preview.js";

const wait = (ms = 0): Promise<void> => new Promise(resolve => setTimeout(resolve, ms));

async function until(predicate: () => boolean): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt++) {
    if (predicate()) return;
    await wait(10);
  }
  throw Error("Cover probe did not settle");
}

function file(index: number): Attachment {
  return {
    attachmentId: "00000000-0000-4000-8000-" + String(index).padStart(12, "0"),
    cardId: "00000000-0000-4000-8000-000000000010",
    name: "Synthetic image " + index, mediaLabel: "application/octet-stream",
    createdAt: "2026-11-06T00:00:00Z", byteLength: 1, sha256: "0".repeat(64),
    createdIn: {
      boardId: "00000000-0000-4000-8000-000000000011",
      incarnation: "00000000-0000-4000-8000-000000000012",
    },
  };
}

// Controlled timing/visibility. The raster, bitmap and canvas are real.
export async function runCoverPreviewProbe(): Promise<number> {
  let checks = 0;
  const check = (value: boolean, message: string): void => {
    if (!value) throw Error(message);
    checks++;
  };
  const source = document.createElement("canvas");
  source.width = 2; source.height = 1;
  source.getContext("2d")!.fillRect(0, 0, 2, 1);
  const blob = await new Promise<Blob>((resolve, reject) => {
    source.toBlob(value => value ? resolve(value) : reject(Error("Test raster encoding failed")), "image/png");
  });
  const png = new Uint8Array(await blob.arrayBuffer());
  const originalObserver = window.IntersectionObserver;
  const originalDecode = window.createImageBitmap;
  const observations = new Map<Element, (visibility: boolean[]) => void>();
  window.IntersectionObserver = class {
    constructor(private readonly callback: IntersectionObserverCallback) {}
    observe(target: Element): void {
      observations.set(target, visibility => {
        this.callback(visibility.map((isIntersecting, time) => ({ target, isIntersecting, time })) as IntersectionObserverEntry[],
          this as unknown as IntersectionObserver);
      });
    }
    disconnect(): void {}
  } as unknown as typeof IntersectionObserver;

  const previews: CoverPreview[] = [];
  const canvases: HTMLCanvasElement[] = [];
  const gates = new Map<number, (bytes: Uint8Array) => void>();
  const started: number[] = [];
  function make(priority: "card" | "details", fetchBytes: (attachment: Attachment) => Promise<Uint8Array>) {
    const canvas = document.createElement("canvas");
    const states: CoverState[] = [];
    const preview = new CoverPreview({
      canvas, observe: canvas, priority, fetchBytes,
      isCurrent: () => true, onState: state => states.push(state),
    });
    previews.push(preview); canvases.push(canvas);
    return { preview, canvas, states };
  }
  function queued(index: number, priority: "card" | "details") {
    const item = make(priority, () => {
      started.push(index);
      return new Promise(resolve => gates.set(index, resolve));
    });
    item.preview.start([file(index)], file(index).attachmentId);
    return item;
  }

  let allowDecode = (): void => {};
  try {
    const first = queued(1, "card");
    observations.get(first.canvas)!([true, false]);
    await wait();
    check(started.length === 0, "Latest hidden observation started a transfer");
    observations.get(first.canvas)!([false, true]);
    const second = queued(2, "card");
    const queuedCard = queued(3, "card");
    const cancelled = queued(10, "card");
    observations.get(second.canvas)!([true]);
    observations.get(queuedCard.canvas)!([true]);
    observations.get(cancelled.canvas)!([true]);
    const details = queued(4, "details");
    check(started.join() === "1,2", "Cover fetches exceeded the shared two-job bound");
    cancelled.preview.dispose();
    gates.get(1)!(png);
    await until(() => started.includes(4));
    check(started.join() === "1,2,4", "Details did not get the next slot");
    gates.get(4)!(png);
    await until(() => started.includes(3));
    check(started.join() === "1,2,4,3", "Cancelled pending work fetched or live background work was lost");
    gates.get(2)!(png); gates.get(3)!(png);
    await until(() => second.states.at(-1)?.status === "ready" && details.states.at(-1)?.status === "ready");
    check(details.canvas.width === 2 && details.canvas.height === 1, "Verified raster was not drawn");
    observations.get(first.canvas)!([false]);
    check(first.canvas.width === 0 && !first.canvas.hasAttribute("aria-label"), "Stale canvas semantics survived invalidation");

    let calls = 0;
    const fallback = make("details", async () => ++calls === 1 ? new Uint8Array([0]) : png);
    fallback.preview.start([file(6), file(5)]);
    await until(() => fallback.states.at(-1)?.status === "ready");
    check(calls === 2 && fallback.states.at(-1)?.attachmentId === file(6).attachmentId, "Ordered fallback selected the wrong file");

    let attempts = 0;
    const unavailable = make("details", async () => { attempts++; throw Error("Synthetic transport failure"); });
    unavailable.preview.start([file(7), file(8)]);
    await until(() => unavailable.states.at(-1)?.status === "unavailable");
    check(attempts === 1, "Indeterminate earlier file was silently skipped");

    let decoding = 0, closed = 0;
    const blocked = new Promise<void>(resolve => { allowDecode = resolve; });
    window.createImageBitmap = (async (image: ImageBitmapSource) => {
      const bitmap = await originalDecode(image);
      const close = bitmap.close.bind(bitmap);
      bitmap.close = () => { closed++; close(); };
      decoding++;
      await blocked;
      return bitmap;
    }) as typeof createImageBitmap;
    const late = make("details", async () => png), peer = make("details", async () => png);
    late.preview.start([file(9)]);
    peer.preview.start([file(12)]);
    await until(() => decoding === 2);
    late.preview.dispose();
    peer.preview.dispose();
    const waiting = queued(11, "details");
    await wait(30);
    check(!started.includes(11), "Disposed unabortable decodes gave up their slots too early");
    allowDecode();
    await until(() => closed === 2 && started.includes(11));
    check(late.canvas.width === 0 && !late.states.some(state => state.status === "ready"), "Late bitmap drew after dispose");
    waiting.preview.dispose();
    return checks;
  } finally {
    allowDecode();
    for (const preview of previews) preview.dispose();
    for (const gate of gates.values()) gate(png);
    for (const canvas of canvases) canvas.remove();
    window.IntersectionObserver = originalObserver;
    window.createImageBitmap = originalDecode;
  }
}
