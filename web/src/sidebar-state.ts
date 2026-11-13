import { ApiError } from "../api.mjs";
import type { WorkspaceView } from "./workspace-state.js";

export type PinChange =
  | Readonly<{ action: "pin" | "unpin"; id: string }>
  | Readonly<{ action: "before" | "after"; id: string; anchor: string }>
  | Readonly<{ action: "remove-unavailable" }>;

export type SidebarPinsView = Readonly<{
  ids: readonly string[];
  canChange: boolean;
  unavailableCount: number;
  canRemoveUnavailable: boolean;
  busy: boolean;
  canInspect: boolean;
  canRetry: boolean;
  canCancel: boolean;
  message: string;
}>;

type WorkspaceReply = Readonly<{ data: object; etag: string }>;
type SavedPins = Readonly<{ ids: readonly string[]; etag: string }>;
type PendingChange = {
  change: PinChange;
  unavailable: readonly string[];
  outcome: "retryable" | "unknown";
  submittedRemovals: readonly string[];
};

type PinOptions = Readonly<{
  signal: AbortSignal;
  read(): Promise<WorkspaceReply>;
  write(ids: string[], etag: string): Promise<WorkspaceReply>;
  refreshCatalog(): Promise<void>;
  onChange(view: SidebarPinsView): void;
}>;

function sameOrder(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length && left.every((id, index) => id === right[index]);
}

function pinOrder(
  ids: readonly string[],
  pending: PendingChange,
  present: ReadonlySet<string>,
): string[] | undefined {
  const { change } = pending;
  switch (change.action) {
    case "remove-unavailable": {
      const candidates = new Set(pending.unavailable);
      return ids.filter((id) => !candidates.has(id) || present.has(id));
    }
    case "pin":
      if (!present.has(change.id)) return undefined;
      return ids.includes(change.id) ? [...ids] : [...ids, change.id];
    case "unpin":
      return ids.filter((id) => id !== change.id);
    case "before":
    case "after": {
      if (!ids.includes(change.id)) return undefined;
      const rest = ids.filter((id) => id !== change.id);
      const index = rest.indexOf(change.anchor);
      if (index < 0) return undefined;
      rest.splice(index + (change.action === "after" ? 1 : 0), 0, change.id);
      return rest;
    }
  }
}

export function createSidebarPins({ signal, read, write, refreshCatalog, onChange }: PinOptions) {
  const noPins: readonly string[] = Object.freeze([]);
  let saved: SavedPins | undefined;
  let present = new Set<string>();
  let catalogComplete = false;
  let pending: PendingChange | undefined;
  let busy = false;
  let refreshRequested = false;
  let readFailed = false;
  let message = "";

  function unavailablePins(ids: readonly string[]): string[] {
    return catalogComplete ? ids.filter((id) => !present.has(id)) : [];
  }

  function publish() {
    if (signal.aborted) return;
    const ids = saved?.ids ?? noPins;
    const unavailableCount = unavailablePins(ids).length;
    const idle = !busy && !pending && saved !== undefined && catalogComplete && !readFailed;
    onChange({
      ids,
      canChange: idle && unavailableCount === 0,
      unavailableCount,
      canRemoveUnavailable: idle && unavailableCount > 0,
      busy,
      canInspect: !busy && (pending?.outcome === "unknown" || (!pending && readFailed)),
      canRetry: !busy && pending?.outcome === "retryable",
      canCancel: !busy && pending !== undefined && pending.outcome !== "unknown",
      message:
        (busy || pending ? message : "") ||
        (readFailed ? "Could not read the saved pin order." : "") ||
        (unavailableCount
          ? `${unavailableCount} saved ${unavailableCount === 1 ? "pin is" : "pins are"} unavailable.`
          : message),
    });
  }

  function updateCatalog(catalog: WorkspaceView) {
    catalogComplete = catalog.status === "ready";
    present = new Set(catalog.boards.map(({ id }) => id));
    publish();
  }

  function accept({ data, etag }: WorkspaceReply, requested?: readonly string[]): SavedPins {
    // The shared API validates the workspace body, including canonical unique IDs and its ETag.
    const { pinned_board_ids: ids } = data as { pinned_board_ids: string[] };
    if (requested && !sameOrder(ids, requested)) {
      throw new ApiError("invalid_response", { outcomeUnknown: true });
    }
    saved = { ids: Object.freeze([...ids]), etag };
    readFailed = false;
    return saved;
  }

  async function readSaved(): Promise<SavedPins> {
    try {
      const reply = await read();
      signal.throwIfAborted();
      return accept(reply);
    } catch (error) {
      readFailed = true;
      throw error;
    }
  }

  async function run(operation: () => Promise<void>) {
    busy = true;
    publish();
    try {
      await operation();
    } finally {
      busy = false;
      publish();
      if (refreshRequested && !signal.aborted) {
        refreshRequested = false;
        await refresh();
      }
    }
  }

  function refresh(): Promise<void> {
    if (signal.aborted) return Promise.resolve();
    if (busy) {
      refreshRequested = true;
      return Promise.resolve();
    }
    return run(async () => {
      try {
        await readSaved();
      } catch {
        // Keep the last confirmed order visible and expose the read failure through publish.
      }
    });
  }

  async function freshBase(edit: PendingChange): Promise<SavedPins> {
    const latest = await readSaved();
    if (edit.change.action === "remove-unavailable") await refreshCatalog();
    signal.throwIfAborted();
    return latest;
  }

  async function save(edit: PendingChange, initial?: SavedPins) {
    let latest: SavedPins;
    try {
      latest = initial ?? (await freshBase(edit));
    } catch {
      message = "Could not refresh the saved pin order. The pin change is pending.";
      return;
    }
    for (let attempt = 0; attempt < 2; attempt++) {
      if (signal.aborted) return;
      if (!catalogComplete) {
        message = "The pin change requires a complete board catalog. Refresh boards and retry.";
        return;
      }
      const requested = pinOrder(latest.ids, edit, present);
      if (!requested) {
        message = "The board or move destination is unavailable. The pin change is pending.";
        return;
      }
      if (sameOrder(latest.ids, requested)) {
        pending = undefined;
        message = "Pin order is current.";
        return;
      }
      if (unavailablePins(requested).length > 0) {
        message = "The saved order contains other unavailable pins. Cancel and review them.";
        return;
      }
      if (edit.change.action === "remove-unavailable") {
        const kept = new Set(requested);
        edit.submittedRemovals = latest.ids.filter((id) => !kept.has(id));
      }
      try {
        const reply = await write(requested, latest.etag);
        signal.throwIfAborted();
        accept(reply, requested);
        pending = undefined;
        message = "Pin order saved.";
        return;
      } catch (error) {
        if (signal.aborted) return;
        if (!(error instanceof ApiError) || error.outcomeUnknown) {
          edit.outcome = "unknown";
          message = "The save outcome is unknown. Inspect the saved pin order.";
          return;
        }
        if (error.status !== 412) {
          message = "The service refused the save. The pin change is pending.";
          return;
        }
      }
      if (attempt === 0) {
        try {
          latest = await freshBase(edit);
        } catch {
          message = "Could not read the concurrent pin order. The pin change is pending.";
          return;
        }
      }
    }
    message = "The shared pin order changed again. Retry to review and apply the pending change.";
  }

  function change(request: PinChange): Promise<void> {
    if (signal.aborted || busy || pending || !saved || !catalogComplete || readFailed) {
      return Promise.resolve();
    }
    const unavailable = unavailablePins(saved.ids);
    const removing = request.action === "remove-unavailable";
    if (removing !== unavailable.length > 0) return Promise.resolve();
    const edit: PendingChange = {
      change: request,
      unavailable,
      outcome: "retryable",
      submittedRemovals: noPins,
    };
    pending = edit;
    message = "Saving pin order…";
    const initial = removing ? undefined : saved;
    return run(() => save(edit, initial));
  }

  function inspect(): Promise<void> {
    if (signal.aborted || busy) return Promise.resolve();
    const edit = pending;
    if (edit?.outcome !== "unknown") return edit ? Promise.resolve() : refresh();
    message = "Reading saved pin order…";
    return run(async () => {
      try {
        const { ids } = await readSaved();
        let confirmed: boolean;
        switch (edit.change.action) {
          case "pin":
          case "unpin":
            confirmed = ids.includes(edit.change.id) === (edit.change.action === "pin");
            break;
          case "remove-unavailable":
            confirmed = edit.submittedRemovals.every((id) => !ids.includes(id));
            break;
          case "before":
          case "after": {
            const requested = pinOrder(ids, edit, present);
            confirmed = requested !== undefined && sameOrder(ids, requested);
            break;
          }
        }
        if (confirmed) {
          pending = undefined;
          message = "The saved pin order confirms the change.";
        } else {
          edit.outcome = "retryable";
          message = "The saved order could not confirm the change. Review it before retrying.";
        }
      } catch {
        message = "Could not inspect the saved pin order. The save outcome is unknown.";
      }
    });
  }

  function retry(): Promise<void> {
    const edit = pending;
    if (signal.aborted || busy || edit?.outcome !== "retryable") return Promise.resolve();
    message = "Saving pin order…";
    return run(() => save(edit));
  }

  function cancel() {
    if (signal.aborted || busy || pending?.outcome !== "retryable") return;
    pending = undefined;
    message = "";
    publish();
  }

  return { refresh, updateCatalog, change, inspect, retry, cancel };
}

export function mountSidebarGeometry(
  host: HTMLElement,
  panel: HTMLElement,
  toggle: HTMLButtonElement,
  separator: HTMLElement,
) {
  const doc = host.ownerDocument;
  const view = doc.defaultView;
  const layout = host.parentElement;
  if (!view || !layout) return () => {};
  const widthOwner = layout.parentElement ?? layout;

  const key = "localswim:attention-sidebar";
  let preferredWidth = 336;
  let collapsed = false;
  try {
    const stored: unknown = JSON.parse(view.sessionStorage.getItem(key) ?? "null");
    if (typeof stored === "object" && stored !== null) {
      const { width, collapsed: storedCollapsed } = stored as Record<string, unknown>;
      if (typeof width === "number" && Number.isSafeInteger(width) && width > 0) {
        preferredWidth = width;
      }
      collapsed = storedCollapsed === true;
    }
  } catch {
    // The controls also work with tab storage disabled.
  }

  let minWidth = 0;
  let maxWidth = preferredWidth;
  let drag: { pointer: number; startX: number; startWidth: number; width: number } | undefined;
  const listeners = new AbortController();
  const { signal } = listeners;

  function persist() {
    try {
      view?.sessionStorage.setItem(key, JSON.stringify({ width: preferredWidth, collapsed }));
    } catch {
      // Retain geometry in this page when storage is disabled.
    }
  }

  function clamp(width: number): number {
    return Math.max(minWidth, Math.min(maxWidth, Math.round(width)));
  }

  function paintWidth() {
    const width = clamp(drag?.width ?? preferredWidth);
    widthOwner.style.setProperty("--attention-width", `${width}px`);
    separator.setAttribute("aria-valuemin", String(minWidth));
    separator.setAttribute("aria-valuemax", String(maxWidth));
    separator.setAttribute("aria-valuenow", String(width));
    separator.setAttribute("aria-valuetext", `${width} pixels`);
  }

  function measure() {
    if (!layout || !view) return;
    const available = layout.clientWidth;
    const sideBySide =
      view.matchMedia("(min-width: 751px)").matches &&
      host.closest('[data-route="board"]') !== null;
    minWidth = Math.min(224, available);
    maxWidth = Math.max(minWidth, available - (sideBySide ? 280 : 0));
    paintWidth();
  }

  function paintVisibility() {
    host.dataset.attentionCollapsed = String(collapsed);
    panel.hidden = collapsed;
    separator.hidden = collapsed;
    toggle.textContent = collapsed ? "Show Attention" : "Hide Attention";
    toggle.setAttribute("aria-expanded", String(!collapsed));
  }

  function clearResize() {
    const pointer = drag?.pointer;
    drag = undefined;
    if (pointer !== undefined && separator.hasPointerCapture(pointer)) {
      separator.releasePointerCapture(pointer);
    }
    paintWidth();
  }

  function toggleVisibility() {
    clearResize();
    collapsed = !collapsed;
    if (collapsed && (panel.contains(doc.activeElement) || doc.activeElement === separator)) {
      toggle.focus({ preventScroll: true });
    }
    paintVisibility();
    persist();
  }

  function commitWidth(width: number) {
    const next = clamp(width);
    if (next === clamp(preferredWidth)) return;
    preferredWidth = next;
    paintWidth();
    persist();
  }

  const pointerWidth = (event: PointerEvent, start: NonNullable<typeof drag>) =>
    clamp(start.startWidth + event.clientX - start.startX);

  toggle.addEventListener("click", toggleVisibility, { signal });
  separator.addEventListener(
    "keydown",
    (event) => {
      if (drag || event.isComposing) return;
      const width = clamp(preferredWidth);
      const steps: Partial<Record<string, number>> = {
        ArrowLeft: width - 16,
        ArrowRight: width + 16,
        Home: minWidth,
        End: maxWidth,
      };
      if (event.key === "Enter") {
        event.preventDefault();
        toggleVisibility();
      } else if (Object.hasOwn(steps, event.key)) {
        const next = steps[event.key];
        if (next === undefined) return;
        event.preventDefault();
        commitWidth(next);
      }
    },
    { signal },
  );
  separator.addEventListener(
    "pointerdown",
    (event) => {
      if (drag || !event.isPrimary || event.button !== 0) return;
      try {
        separator.setPointerCapture(event.pointerId);
      } catch {
        return;
      }
      const width = clamp(preferredWidth);
      drag = { pointer: event.pointerId, startX: event.clientX, startWidth: width, width };
      event.preventDefault();
      separator.focus({ preventScroll: true });
    },
    { signal },
  );
  separator.addEventListener(
    "pointermove",
    (event) => {
      if (drag?.pointer !== event.pointerId) return;
      drag.width = pointerWidth(event, drag);
      paintWidth();
    },
    { signal },
  );
  separator.addEventListener(
    "pointerup",
    (event) => {
      if (drag?.pointer !== event.pointerId) return;
      const width = pointerWidth(event, drag);
      clearResize();
      commitWidth(width);
    },
    { signal },
  );
  function cancelResize(event: PointerEvent) {
    if (drag?.pointer === event.pointerId) clearResize();
  }
  separator.addEventListener("pointercancel", cancelResize, { signal });
  separator.addEventListener("lostpointercapture", cancelResize, { signal });
  paintVisibility();
  measure();
  const observer = new ResizeObserver(measure);
  observer.observe(layout);

  return () => {
    listeners.abort();
    observer.disconnect();
    clearResize();
    widthOwner.style.removeProperty("--attention-width");
    delete host.dataset.attentionCollapsed;
  };
}
