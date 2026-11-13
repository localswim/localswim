import { ApiError } from "../api.mjs";

export type OperationStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;

export type WriteOutcome<T> =
  { kind: "confirmed"; result: T } | { kind: "stale" | "refused" | "unknown" | "journal-failed" };

// The caller owns the journal and its reconciliation policy. A write may only
// run once and only after the original intent is recoverable from storage.
export async function submitOperation<T>(
  journal: () => boolean,
  write: () => Promise<T>,
): Promise<WriteOutcome<T>> {
  if (!journal()) return { kind: "journal-failed" };
  try {
    return { kind: "confirmed", result: await write() };
  } catch (error) {
    if (!(error instanceof ApiError) || error.outcomeUnknown) return { kind: "unknown" };
    return { kind: error.status === 412 ? "stale" : "refused" };
  }
}

export function createScopedOperations() {
  type ActiveOperation = "submitting" | "inspecting";
  const active = new Map<string, ActiveOperation>();

  return {
    state: (key: string) => active.get(key),
    async run(
      key: string,
      operation: ActiveOperation,
      action: () => Promise<void>,
      settled: () => void,
    ): Promise<void> {
      if (active.has(key)) return;
      active.set(key, operation);
      try {
        await action();
      } finally {
        active.delete(key);
        settled();
      }
    },
  };
}
