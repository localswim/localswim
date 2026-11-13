import type { BoardViewData } from "./board-view.js";

export const maxRecentBoardBudgetMiB = 32;
const defaultBudgetMiB = 4;
const bytesPerMiB = 1024 * 1024;
const budgetKey = "localswim:recent-board-budget-mib";

export type RetainedBoard = Readonly<{ data: BoardViewData; confirmedAt: number }>;

type Entry = Readonly<{ retained: RetainedBoard; bytes: number }>;

function validBudget(mib: number): boolean {
  return Number.isInteger(mib) && mib >= 0 && mib <= maxRecentBoardBudgetMiB;
}

export function createRecentBoards(storage?: Pick<Storage, "getItem" | "setItem">) {
  const tabStorage = () => storage ?? globalThis.sessionStorage;
  let budgetMiB = defaultBudgetMiB;
  try {
    const raw = tabStorage().getItem(budgetKey);
    if (raw !== null && /^(0|[1-9]\d?)$/.test(raw) && validBudget(Number(raw))) {
      budgetMiB = Number(raw);
    }
  } catch {
    // Keep the budget usable in this document when tab storage is unavailable.
  }

  const entries = new Map<string, Entry>();
  const encoder = new TextEncoder();
  let byteSize = 0;

  function remove(boardId: string) {
    const entry = entries.get(boardId);
    if (entry === undefined) return;
    entries.delete(boardId);
    byteSize -= entry.bytes;
  }

  function trim() {
    const budget = budgetMiB * bytesPerMiB;
    for (const boardId of entries.keys()) {
      if (byteSize <= budget) break;
      remove(boardId);
    }
  }

  return {
    get budgetMiB() {
      return budgetMiB;
    },
    get byteSize() {
      return byteSize;
    },
    get size() {
      return entries.size;
    },
    get(boardId: string): RetainedBoard | undefined {
      const id = boardId.toLowerCase();
      const entry = entries.get(id);
      if (entry !== undefined) {
        entries.delete(id);
        entries.set(id, entry);
      }
      return entry?.retained;
    },
    remember(data: BoardViewData, confirmedAt = Date.now()) {
      if (budgetMiB === 0) return;
      const id = data.board.id.toLowerCase();
      const previous = entries.get(id);
      remove(id);
      const budget = budgetMiB * bytesPerMiB;
      let bytes: number;
      if (previous?.retained.data === data) {
        bytes = previous.bytes;
      } else {
        const serialized = JSON.stringify(data);
        if (serialized.length > budget) return;
        bytes = encoder.encode(serialized).byteLength;
        if (bytes > budget) return;
      }
      entries.set(id, { retained: { data, confirmedAt }, bytes });
      byteSize += bytes;
      trim();
    },
    delete(boardId: string) {
      remove(boardId.toLowerCase());
    },
    setBudgetMiB(mib: number): boolean {
      if (!validBudget(mib)) throw new RangeError("Invalid recent board cache budget.");
      budgetMiB = mib;
      trim();
      try {
        tabStorage().setItem(budgetKey, String(mib));
        return true;
      } catch {
        return false;
      }
    },
  };
}

export type RecentBoards = ReturnType<typeof createRecentBoards>;
