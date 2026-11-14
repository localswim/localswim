import type { Uuid } from "../api-types.js";
import type { BoardData, Confirmed } from "../contracts.js";

interface CacheEntry {
  readonly read: Confirmed<BoardData>;
  readonly bytes: number;
}

const bytesPerMiB = 1024 * 1024;
const defaultBudgetMiB = 256;

export type RetainedBoardCache = ReturnType<typeof createRetainedBoardCache>;

export function createRetainedBoardCache() {
  const entries = new Map<Uuid, CacheEntry>();
  const encoder = new TextEncoder();
  let principal: Uuid | null = null;
  let budgetMiB = defaultBudgetMiB;
  let bytes = 0;

  function remove(boardId: Uuid): void {
    const entry = entries.get(boardId);
    if (!entry) return;
    entries.delete(boardId);
    bytes -= entry.bytes;
  }

  function enforceBudget(): void {
    for (const boardId of entries.keys()) {
      if (bytes <= budgetMiB * bytesPerMiB) break;
      remove(boardId);
    }
  }

  return {
    setPrincipal(next: Uuid | null): void {
      if (next === principal) return;
      principal = next;
      entries.clear();
      bytes = 0;
    },

    retain(read: Confirmed<BoardData>): void {
      if (principal === null) return;
      const { id } = read.value.view.board;
      remove(id);
      if (budgetMiB === 0) return;
      const entryBytes = encoder.encode(JSON.stringify(read)).byteLength;
      if (entryBytes > budgetMiB * bytesPerMiB) return;
      entries.set(id, { read, bytes: entryBytes });
      bytes += entryBytes;
      enforceBudget();
    },

    previous(boardId: Uuid): Confirmed<BoardData> | null {
      if (principal === null) return null;
      const entry = entries.get(boardId);
      if (!entry) return null;
      entries.delete(boardId);
      entries.set(boardId, entry);
      return entry.read;
    },

    evict: remove,

    retainOnly(allowed: ReadonlySet<Uuid>): void {
      for (const id of entries.keys()) if (!allowed.has(id)) remove(id);
    },

    settings() {
      return { budgetMiB, bytes, count: entries.size };
    },

    setBudget(next: number): void {
      if (!Number.isSafeInteger(next) || next < 0 || next === budgetMiB) {
        return;
      }
      budgetMiB = next;
      enforceBudget();
    },
  };
}

export function mountCacheSettings(toggle: HTMLButtonElement, cache: RetainedBoardCache) {
  const listeners = new AbortController();
  const { signal } = listeners;
  const panel = toggle.ownerDocument.createElement("label");
  panel.id = "workspace-board-cache-settings";
  panel.className = "lsw-workspace-cache-settings";
  panel.hidden = true;
  const budget = toggle.ownerDocument.createElement("input");
  budget.type = "number";
  budget.min = "1";
  budget.step = "1";
  budget.setAttribute("aria-label", "Board cache budget in MiB");
  budget.value = String(cache.settings().budgetMiB);
  panel.append("Board cache (MiB)", budget);
  toggle.after(panel);
  toggle.setAttribute("aria-controls", panel.id);
  toggle.setAttribute("aria-expanded", "false");
  toggle.disabled = false;

  toggle.addEventListener(
    "click",
    () => {
      panel.hidden = !panel.hidden;
      toggle.setAttribute("aria-expanded", String(!panel.hidden));
    },
    { signal },
  );
  budget.addEventListener(
    "change",
    () => {
      if (budget.validity.valid) cache.setBudget(budget.valueAsNumber);
    },
    { signal },
  );

  return {
    dispose(): void {
      listeners.abort();
      panel.remove();
    },
  };
}
