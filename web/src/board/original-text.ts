import type { NoticePair } from "../../generated/transport/contracts.js";
import { validNoticeText } from "../api/notice-text.js";

// Own the captured evidence: later renders/caller changes cannot alter a gesture.
export function originalSnapshot(pair: NoticePair): NoticePair {
  const snapshot = structuredClone(pair);
  if (snapshot.payload) {
    if (snapshot.payload.facts) {
      Object.freeze(snapshot.payload.facts.tickets);
      Object.freeze(snapshot.payload.facts);
    }
    Object.freeze(snapshot.payload);
  }
  return Object.freeze(snapshot);
}

export function sameOriginal(selected: NoticePair, fresh: NoticePair): boolean {
  const a = selected.payload, b = fresh.payload;
  return selected.targetId === fresh.targetId && selected.deliveryId === fresh.deliveryId &&
    validNoticeText(a) && validNoticeText(b) && a.version === b.version && a.kind === b.kind &&
    a.text === b.text && (a.facts === null ? b.facts === null : b.facts !== null &&
      a.facts.label === b.facts.label && a.facts.from === b.facts.from && a.facts.through === b.facts.through &&
      a.facts.tickets.length === b.facts.tickets.length && a.facts.tickets.every((ticket, index) => ticket === b.facts!.tickets[index]));
}

export type OriginalCopyResult = "copied" | "stale" | "unavailable" | "unsupported" | "expired" | "failed";

// A real resolved write is the only success. Once invoked, OS work cannot be
// cancelled or restored; a late resolution is fenced only from success UI.
export async function copyOriginal(pair: NoticePair, current: () => boolean): Promise<OriginalCopyResult> {
  if (!validNoticeText(pair.payload)) return "unavailable";
  if (!current()) return "stale";
  if (!navigator.clipboard || typeof navigator.clipboard.writeText !== "function") return "unsupported";
  if (navigator.userActivation && !navigator.userActivation.isActive) return "expired";
  if (!current()) return "stale";
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    const write = navigator.clipboard.writeText(pair.payload.text);
    const completed = await Promise.race([
      write.then(() => true),
      new Promise<false>(resolve => { timer = setTimeout(() => resolve(false), 10_000); }),
    ]);
    return !current() ? "stale" : completed ? "copied" : "failed";
  } catch { return current() ? "failed" : "stale"; }
  finally { if (timer !== undefined) clearTimeout(timer); }
}
