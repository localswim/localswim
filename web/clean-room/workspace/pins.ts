import type { BoardSummaryDto, Uuid } from "../api-types.js";
import type { ReadState, WorkspaceData } from "../contracts.js";
import { invalidResponse, mutateJson } from "../http.js";
import { readWorkspacePins, workspacePins, type WorkspacePins } from "./read.js";

interface PinBoard {
  readonly summary: BoardSummaryDto;
  readonly pinned: boolean;
  readonly canToggle: boolean;
  readonly canReorder: boolean;
}

interface PinsOptions {
  readonly search: HTMLInputElement;
  readonly list: HTMLElement;
  readonly status: HTMLElement;
  readonly render: () => void;
}

interface PinChange {
  readonly base: WorkspacePins;
  readonly order: readonly Uuid[];
  readonly message: string;
}

type PinRecovery = PinChange &
  (
    | { readonly outcome: "not_applied" | "superseded" }
    | { readonly outcome: "unknown"; readonly requestId: Uuid }
  );

const maxPins = 1000;

function sameOrder(left: readonly Uuid[], right: readonly Uuid[]): boolean {
  return left.length === right.length && left.every((id, index) => id === right[index]);
}

export function projectPinBoards(
  catalog: readonly BoardSummaryDto[],
  order: readonly Uuid[],
  query: string,
  writable: boolean,
): readonly PinBoard[] {
  const byId = new Map(catalog.map((summary) => [summary.id, summary]));
  const pinned = new Set(order);
  const needle = query.trim().toLowerCase();
  const matches = (board: BoardSummaryDto): boolean =>
    (board.name ?? board.id).toLowerCase().includes(needle);
  const pins = order.flatMap((id) => {
    const summary = byId.get(id);
    return summary && matches(summary) ? [summary] : [];
  });
  const others = catalog.filter((board) => !pinned.has(board.id) && matches(board));
  return [...pins, ...others].map((summary) => ({
    summary,
    pinned: pinned.has(summary.id),
    canToggle: writable && (pinned.has(summary.id) || order.length < maxPins),
    canReorder: writable && pinned.has(summary.id) && pins.length > 1,
  }));
}

export function reorderPins(
  order: readonly Uuid[],
  source: Uuid,
  target: Uuid,
  position: "before" | "after",
): readonly Uuid[] {
  if (source === target || !order.includes(source) || !order.includes(target)) return order;
  const next = order.filter((id) => id !== source);
  next.splice(next.indexOf(target) + (position === "after" ? 1 : 0), 0, source);
  return next;
}

function commitPins(change: PinChange, signal?: AbortSignal) {
  return mutateJson("/workspace", {
    method: "PATCH",
    etag: change.base.workspaceEtag,
    body: { pinned_board_ids: change.order },
    signal,
    validate(data, headers) {
      const next = workspacePins(data, headers);
      if (
        next.workspace.revision !== change.base.workspace.revision + 1 ||
        next.workspaceEtag === change.base.workspaceEtag ||
        !sameOrder(next.workspace.pinned_board_ids, change.order)
      ) {
        return invalidResponse();
      }
      return next;
    },
  });
}

function rowFor(target: EventTarget | null): HTMLElement | null {
  return target instanceof Element ? target.closest<HTMLElement>("[data-pin-board-id]") : null;
}

export function mountPins({ search, list, status, render }: PinsOptions) {
  const listeners = new AbortController();
  const { signal } = listeners;
  let displayed: readonly PinBoard[] = [];
  let confirmed: WorkspacePins | null = null;
  let ready = false;
  let saving = false;
  let needsCheck = false;
  let recovery: PinRecovery | null = null;
  let dragged: Uuid | null = null;
  let dropRow: HTMLElement | null = null;

  const feedback = document.createElement("span");
  feedback.setAttribute("role", "status");
  const announcement = document.createElement("span");
  announcement.setAttribute("role", "status");
  announcement.className = "lsw-workspace-pin-announcement";
  const inspect = document.createElement("button");
  inspect.type = "button";
  inspect.textContent = "Check again";
  inspect.hidden = true;
  status.after(announcement);
  status.append(feedback, inspect);
  status.hidden = true;

  function show(message: string, visible = true): void {
    feedback.textContent = message;
    feedback.classList.toggle("lsw-workspace-pin-announcement", !visible);
    inspect.hidden = !needsCheck;
    status.hidden = message.length === 0 && !needsCheck;
  }

  function review(change: PinRecovery, message: string): void {
    show(`${message} Requested change: ${change.message}`);
  }

  function receive(next: WorkspacePins): void {
    if (!confirmed || next.workspace.revision >= confirmed.workspace.revision) confirmed = next;
    if (
      recovery?.outcome !== "unknown" ||
      confirmed.workspace.revision <= recovery.base.workspace.revision
    ) {
      return;
    }
    needsCheck = false;
    if (sameOrder(confirmed.workspace.pinned_board_ids, recovery.order)) {
      recovery = null;
      show("Current pinned order matches the requested order.");
    } else {
      recovery = { ...recovery, outcome: "superseded" };
      review(recovery, "Board pins changed. Review the current order and choose again.");
    }
  }

  async function inspectOrder(attempt: PinRecovery): Promise<void> {
    try {
      const next = await readWorkspacePins(signal);
      if (signal.aborted) return;
      receive(next);
      // A live update can resolve this attempt before or during the focused read.
      if (recovery !== attempt) return;
      needsCheck = attempt.outcome === "unknown";
      if (needsCheck) review(attempt, "Board pin changes could not be confirmed.");
      else if (confirmed && sameOrder(confirmed.workspace.pinned_board_ids, attempt.order)) {
        review(attempt, "Current pinned order matches the requested order.");
      } else
        review(attempt, "Could not save board pins. Review the current order and choose again.");
    } catch {
      if (signal.aborted || recovery !== attempt) return;
      needsCheck = true;
      review(attempt, "Could not check the saved board pins.");
    }
  }

  async function save(
    order: readonly Uuid[],
    control: HTMLElement,
    message: string,
  ): Promise<void> {
    if (
      !ready ||
      saving ||
      needsCheck ||
      confirmed === null ||
      sameOrder(order, confirmed.workspace.pinned_board_ids)
    ) {
      return;
    }
    const change: PinChange = { base: confirmed, order, message };
    saving = true;
    recovery = null;
    announcement.textContent = "";
    show("Saving pinned boards…", false);
    function renderWithFocus(): void {
      const active = document.activeElement;
      render();
      if (active === control && control.isConnected) control.focus({ preventScroll: true });
    }
    try {
      renderWithFocus();
      const result = await commitPins(change, signal);
      if (signal.aborted) return;
      switch (result.outcome) {
        case "committed":
          receive(result.value);
          if (confirmed && !sameOrder(confirmed.workspace.pinned_board_ids, order)) {
            recovery = { ...change, outcome: "superseded" };
            review(recovery, "Board pins changed. Review the current order and choose again.");
          } else {
            show("Pinned boards saved.", false);
            announcement.textContent = message;
          }
          break;
        case "not_applied":
        case "unknown": {
          const attempt: PinRecovery = { ...change, ...result };
          recovery = attempt;
          renderWithFocus();
          if (recovery === attempt) await inspectOrder(attempt);
          break;
        }
      }
    } finally {
      if (!signal.aborted) {
        saving = false;
        renderWithFocus();
      }
    }
  }

  function unexpectedFailure(): void {
    if (signal.aborted) return;
    saving = false;
    inspect.removeAttribute("aria-disabled");
    needsCheck = true;
    show("Could not check the saved board pins.");
    render();
  }

  function reorder(
    source: Uuid,
    target: Uuid,
    position: "before" | "after",
    control: HTMLElement,
  ): void {
    const order = confirmed?.workspace.pinned_board_ids;
    const board = displayed.find(({ summary }) => summary.id === source)?.summary;
    if (!order || !board) return;
    const next = reorderPins(order, source, target, position);
    const name = board.name ?? board.id;
    const message = `${name} moved to position ${next.indexOf(source) + 1}.`;
    void save(next, control, message).catch(unexpectedFailure);
  }

  function clearDrop(): void {
    dropRow?.removeAttribute("data-pin-drop");
    dropRow = null;
  }

  function endDrag(): void {
    dragged = null;
    clearDrop();
  }

  search.addEventListener("input", render, { signal });
  inspect.addEventListener(
    "click",
    () => {
      if (saving || !needsCheck || recovery === null) return;
      saving = true;
      inspect.setAttribute("aria-disabled", "true");
      void inspectOrder(recovery)
        .then(() => {
          if (signal.aborted) return;
          saving = false;
          inspect.removeAttribute("aria-disabled");
          inspect.hidden = !needsCheck;
          render();
        })
        .catch(unexpectedFailure);
    },
    { signal },
  );

  list.addEventListener(
    "keydown",
    (event) => {
      if (!(event.target instanceof HTMLButtonElement)) return;
      const control = event.target;
      const id = control.dataset.pinReorder;
      if (!id || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
      const pinned = displayed.filter((board) => board.canReorder);
      const index = pinned.findIndex((board) => board.summary.id === id);
      const up = event.key === "ArrowUp" || event.key === "Home";
      const down = event.key === "ArrowDown" || event.key === "End";
      if (index < 0 || (!up && !down)) return;
      event.preventDefault();
      const targetIndex =
        event.key === "Home" ? 0 : event.key === "End" ? pinned.length - 1 : index + (up ? -1 : 1);
      const target = pinned[targetIndex];
      if (target) {
        reorder(id, target.summary.id, up ? "before" : "after", control);
      }
    },
    { signal },
  );

  function canDrag(id: Uuid | undefined): boolean {
    return displayed.some((board) => board.summary.id === id && board.canReorder);
  }

  function dragTarget(event: DragEvent): HTMLElement | null {
    const row = rowFor(event.target);
    const id = row?.dataset.pinBoardId;
    return dragged !== null && dragged !== id && canDrag(dragged) && canDrag(id) ? row : null;
  }

  function dropPosition(row: HTMLElement, pointerY: number): "before" | "after" {
    const bounds = row.getBoundingClientRect();
    return pointerY < bounds.top + bounds.height / 2 ? "before" : "after";
  }

  list.addEventListener(
    "dragstart",
    (event) => {
      const id = event.target instanceof HTMLElement ? event.target.dataset.pinReorder : undefined;
      if (!id || !canDrag(id) || !event.dataTransfer) {
        event.preventDefault();
        return;
      }
      dragged = id;
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData("text/plain", id);
    },
    { signal },
  );
  list.addEventListener(
    "dragover",
    (event) => {
      const row = dragTarget(event);
      if (dropRow !== row) clearDrop();
      if (!row || !event.dataTransfer) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = "move";
      dropRow = row;
      row.dataset.pinDrop = dropPosition(row, event.clientY);
    },
    { signal },
  );
  list.addEventListener(
    "dragleave",
    (event) => {
      if (!(event.relatedTarget instanceof Node) || !list.contains(event.relatedTarget)) {
        clearDrop();
      }
    },
    { signal },
  );
  list.addEventListener(
    "drop",
    (event) => {
      const row = dragTarget(event);
      const target = row?.dataset.pinBoardId;
      const source = dragged;
      endDrag();
      if (!row || !target || !source) return;
      event.preventDefault();
      const control = document.activeElement;
      reorder(
        source,
        target,
        dropPosition(row, event.clientY),
        control instanceof HTMLElement ? control : row,
      );
    },
    { signal },
  );
  list.addEventListener("dragend", endDrag, { signal });

  list.addEventListener(
    "click",
    (event) => {
      const control =
        event.target instanceof Element
          ? event.target.closest<HTMLButtonElement>("button[data-pin-action]")
          : null;
      const id = control?.dataset.pinAction;
      const order = confirmed?.workspace.pinned_board_ids;
      const board = displayed.find(({ summary }) => summary.id === id);
      if (!control || !id || !order || !board?.canToggle) return;
      const next = order.includes(id) ? order.filter((item) => item !== id) : [...order, id];
      const name = board.summary.name ?? id;
      const message = board.pinned
        ? `${name} unpinned.`
        : `${name} pinned at position ${next.length}.`;
      void save(next, control, message).catch(unexpectedFailure);
    },
    { signal },
  );

  return {
    update(data: ReadState<WorkspaceData>, canWrite: boolean): readonly PinBoard[] {
      const value = (data.status === "ready" ? data.current : data.previous)?.value;
      if (value) receive(value);
      ready = data.status === "ready" && canWrite;
      search.disabled = value === undefined;
      displayed = projectPinBoards(
        value?.boards ?? [],
        confirmed?.workspace.pinned_board_ids ?? [],
        search.value,
        ready && !saving && !needsCheck,
      );
      if (dragged !== null && !canDrag(dragged)) endDrag();
      return displayed;
    },
    dispose(): void {
      listeners.abort();
      endDrag();
      announcement.remove();
      feedback.remove();
      inspect.remove();
    },
  };
}
