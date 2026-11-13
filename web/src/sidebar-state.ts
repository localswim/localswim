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
  canDiscardJournal: boolean;
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
type PinStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;

type PinOptions = Readonly<{
  signal: AbortSignal;
  storage?: PinStorage;
  read(): Promise<WorkspaceReply>;
  write(ids: string[], etag: string): Promise<WorkspaceReply>;
  refreshCatalog(): Promise<void>;
  onChange(view: SidebarPinsView): void;
}>;

const PIN_JOURNAL_KEY = "localswim:pending-pin-change";
const BOARD_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function isBoardId(value: unknown): value is string {
  return typeof value === "string" && BOARD_ID.test(value);
}

function isPinList(value: unknown): value is string[] {
  return (
    Array.isArray(value) &&
    value.length <= 1000 &&
    value.every(isBoardId) &&
    new Set(value).size === value.length
  );
}

function parsePending(raw: string): PendingChange | undefined {
  try {
    if (raw.length > 80_000) return undefined;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null) return undefined;
    const { action, id, anchor, unavailable, submittedRemovals } = value as Record<string, unknown>;
    if (action === "remove-unavailable" && isPinList(unavailable) && isPinList(submittedRemovals)) {
      if (
        submittedRemovals.length > 0 &&
        submittedRemovals.every((candidate) => unavailable.includes(candidate))
      ) {
        return { change: { action }, unavailable, submittedRemovals, outcome: "unknown" };
      }
      return undefined;
    }
    if (!isBoardId(id)) return undefined;
    if (action === "pin" || action === "unpin") {
      return { change: { action, id }, unavailable: [], submittedRemovals: [], outcome: "unknown" };
    }
    if ((action === "before" || action === "after") && isBoardId(anchor) && id !== anchor) {
      return {
        change: { action, id, anchor },
        unavailable: [],
        submittedRemovals: [],
        outcome: "unknown",
      };
    }
  } catch {
    // Malformed JSON supplies no usable pin intent.
  }
  return undefined;
}

function readPending(
  storage: PinStorage | undefined,
): PendingChange | "unavailable" | "invalid" | undefined {
  try {
    const raw = (storage ?? globalThis.sessionStorage).getItem(PIN_JOURNAL_KEY);
    return raw === null ? undefined : (parsePending(raw) ?? "invalid");
  } catch {
    return "unavailable";
  }
}

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

export function createSidebarPins({
  signal,
  storage,
  read,
  write,
  refreshCatalog,
  onChange,
}: PinOptions) {
  const noPins: readonly string[] = Object.freeze([]);
  let saved: SavedPins | undefined;
  let present = new Set<string>();
  let catalogComplete = false;
  let pending: PendingChange | undefined;
  let journalProblem: "unavailable" | "invalid" | undefined;
  let journaled = false;
  let canDiscardJournal = false;
  let busy = false;
  let reordering = false;
  let refreshRequested = false;
  let readFailed = false;
  let message = "";
  let page = new AbortController();
  let activeSignal = AbortSignal.any([signal, page.signal]);

  function loadJournal() {
    const stored = readPending(storage);
    pending = typeof stored === "object" ? stored : undefined;
    journaled = pending !== undefined;
    journalProblem = typeof stored === "string" ? stored : undefined;
    canDiscardJournal = false;
    message = pending ? "A pin save needs inspection. Inspect the saved pin order." : "";
  }

  loadJournal();

  function unavailablePins(ids: readonly string[]): string[] {
    return catalogComplete ? ids.filter((id) => !present.has(id)) : [];
  }

  function publish() {
    if (activeSignal.aborted) return;
    const ids = saved?.ids ?? noPins;
    const unavailableCount = unavailablePins(ids).length;
    const idle =
      !busy && !pending && !journalProblem && saved !== undefined && catalogComplete && !readFailed;
    onChange({
      ids,
      canChange: idle && unavailableCount === 0,
      unavailableCount,
      canRemoveUnavailable: idle && unavailableCount > 0,
      busy,
      canInspect:
        !busy &&
        Boolean(journalProblem || pending?.outcome === "unknown" || (readFailed && !pending)),
      canRetry: !busy && pending?.outcome === "retryable",
      canCancel: !busy && pending !== undefined && pending.outcome !== "unknown",
      canDiscardJournal: !busy && canDiscardJournal,
      message:
        (journalProblem === "unavailable"
          ? "Could not read pending pins from tab storage. Inspect saved pins to try again."
          : "") ||
        (journalProblem === "invalid"
          ? canDiscardJournal
            ? "Saved pins were refreshed. The unreadable pending record can be discarded."
            : "The pending pin record is unreadable. Inspect saved pins before discarding it."
          : "") ||
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

  async function readSaved(operationSignal: AbortSignal): Promise<SavedPins> {
    try {
      const reply = await read();
      operationSignal.throwIfAborted();
      return accept(reply);
    } catch (error) {
      if (!operationSignal.aborted) readFailed = true;
      throw error;
    }
  }

  async function run(operation: (operationSignal: AbortSignal) => Promise<void>) {
    const operationSignal = activeSignal;
    busy = true;
    publish();
    try {
      await operation(operationSignal);
    } finally {
      if (!operationSignal.aborted) {
        busy = false;
        publish();
        if (refreshRequested && !reordering) {
          refreshRequested = false;
          await refresh();
        }
      }
    }
  }

  function refresh(): Promise<void> {
    if (activeSignal.aborted) return Promise.resolve();
    if (busy || reordering) {
      refreshRequested = true;
      return Promise.resolve();
    }
    return run(async (operationSignal) => {
      try {
        await readSaved(operationSignal);
      } catch {
        // Keep the last confirmed order visible and expose the read failure through publish.
      }
    });
  }

  async function freshBase(edit: PendingChange, operationSignal: AbortSignal): Promise<SavedPins> {
    const latest = await readSaved(operationSignal);
    if (edit.change.action === "remove-unavailable") await refreshCatalog();
    operationSignal.throwIfAborted();
    return latest;
  }

  function recordDispatch(edit: PendingChange): boolean {
    // Navigation can interrupt response handling after the service accepts the write.
    try {
      (storage ?? globalThis.sessionStorage).setItem(
        PIN_JOURNAL_KEY,
        JSON.stringify({
          ...edit.change,
          unavailable: edit.unavailable,
          submittedRemovals: edit.submittedRemovals,
        }),
      );
      journaled = true;
      return true;
    } catch {
      message = "Tab storage is unavailable. The pin change is pending.";
      return false;
    }
  }

  function finish(messageOnSuccess: string) {
    if (journaled) {
      try {
        (storage ?? globalThis.sessionStorage).removeItem(PIN_JOURNAL_KEY);
      } catch {
        message =
          "Could not clear the pending pin change from tab storage. Inspect the saved order.";
        if (pending) pending.outcome = "unknown";
        return;
      }
    }
    pending = undefined;
    journaled = false;
    message = messageOnSuccess;
  }

  async function save(edit: PendingChange, operationSignal: AbortSignal, initial?: SavedPins) {
    let latest: SavedPins;
    try {
      latest = initial ?? (await freshBase(edit, operationSignal));
    } catch {
      if (!operationSignal.aborted) {
        message = "Could not refresh the saved pin order. The pin change is pending.";
      }
      return;
    }
    for (let attempt = 0; attempt < 2; attempt++) {
      if (operationSignal.aborted) return;
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
        finish("Pin order is current.");
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
      if (!recordDispatch(edit)) return;
      try {
        const reply = await write(requested, latest.etag);
        operationSignal.throwIfAborted();
        accept(reply, requested);
        finish("Pin order saved.");
        return;
      } catch (error) {
        if (operationSignal.aborted) return;
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
          latest = await freshBase(edit, operationSignal);
        } catch {
          if (!operationSignal.aborted) {
            message = "Could not read the concurrent pin order. The pin change is pending.";
          }
          return;
        }
      }
    }
    message = "The shared pin order changed again. Retry to review and apply the pending change.";
  }

  function change(request: PinChange): Promise<void> {
    if (
      activeSignal.aborted ||
      busy ||
      pending ||
      journalProblem ||
      !saved ||
      !catalogComplete ||
      readFailed
    ) {
      return Promise.resolve();
    }
    if (reordering && request.action !== "before" && request.action !== "after") {
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
    return run((operationSignal) => save(edit, operationSignal, initial));
  }

  function inspect(): Promise<void> {
    if (activeSignal.aborted || busy) return Promise.resolve();
    if (!journalProblem && pending?.outcome !== "unknown") {
      return pending ? Promise.resolve() : refresh();
    }
    message = "Reading saved pin order…";
    return run(async (operationSignal) => {
      try {
        if (journalProblem) loadJournal();
        const edit = pending;
        const { ids } = await readSaved(operationSignal);
        if (!edit) {
          canDiscardJournal = journalProblem === "invalid";
          return;
        }
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
          finish("The saved pin order confirms the change.");
        } else {
          edit.outcome = "retryable";
          message = "The saved order could not confirm the change. Review it before retrying.";
        }
      } catch {
        if (!operationSignal.aborted) {
          message = "Could not inspect the saved pin order. The save outcome is unknown.";
        }
      }
    });
  }

  function retry(): Promise<void> {
    const edit = pending;
    if (activeSignal.aborted || busy || edit?.outcome !== "retryable") return Promise.resolve();
    message = "Saving pin order…";
    return run((operationSignal) => save(edit, operationSignal));
  }

  function cancel() {
    if (activeSignal.aborted || busy) return;
    if (canDiscardJournal) {
      loadJournal();
      if (journalProblem === "invalid") {
        try {
          (storage ?? globalThis.sessionStorage).removeItem(PIN_JOURNAL_KEY);
          journalProblem = undefined;
          message = "Unreadable pending pin record discarded.";
        } catch {
          journalProblem = "unavailable";
        }
      }
      publish();
      return;
    }
    if (pending?.outcome !== "retryable") return;
    finish("");
    publish();
  }

  function suspend() {
    // A page restored from the back-forward cache may share the journal with a newer page.
    page.abort();
  }

  function resume(): Promise<void> {
    if (!page.signal.aborted || signal.aborted) return Promise.resolve();
    page = new AbortController();
    activeSignal = AbortSignal.any([signal, page.signal]);
    loadJournal();
    refreshRequested = false;
    busy = false;
    reordering = false;
    return refresh();
  }

  function beginReorder(): boolean {
    if (activeSignal.aborted || busy || reordering || pending || journalProblem) return false;
    reordering = true;
    return true;
  }

  function endReorder(): void {
    if (!reordering) return;
    reordering = false;
    if (!busy && refreshRequested && !activeSignal.aborted) {
      refreshRequested = false;
      void refresh().catch((error: unknown) => globalThis.reportError(error));
    }
  }

  return {
    refresh,
    updateCatalog,
    change,
    inspect,
    retry,
    cancel,
    suspend,
    resume,
    beginReorder,
    endReorder,
  };
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
      view.matchMedia("(width > 750px)").matches && host.closest('[data-route="board"]') !== null;
    minWidth = Math.min(224, available);
    const computedLimit = view.getComputedStyle(host).maxInlineSize;
    const drawerLimit = computedLimit.endsWith("px") ? Number.parseFloat(computedLimit) : Infinity;
    const maxAvailable = Math.min(available - (sideBySide ? 280 : 0), drawerLimit);
    maxWidth = Math.max(minWidth, Math.floor(maxAvailable));
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
    measure();
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
  const drawerRoot = widthOwner.querySelector("#card-drawer-root");
  const visibility = new MutationObserver((records) => {
    if (
      records.some(
        ({ target }) =>
          target === drawerRoot ||
          (target instanceof HTMLElement && target.matches(".card-drawer")),
      )
    ) {
      measure();
    }
  });
  if (drawerRoot) {
    visibility.observe(drawerRoot, { attributeFilter: ["hidden"], childList: true, subtree: true });
  }

  return () => {
    listeners.abort();
    observer.disconnect();
    visibility.disconnect();
    clearResize();
    widthOwner.style.removeProperty("--attention-width");
    delete host.dataset.attentionCollapsed;
  };
}
