import type { BoardRead } from "../../generated/transport/contracts.js";
import type { Card, Identity } from "../../generated/archive/contracts.js";
import { invalid } from "./boundary.js";

export type ParentRef = Identity & { cardId: string };
export type HierarchyCard = Card & { parent?: ParentRef | null };
export const sameIdentity = (a: Identity, b: Identity): boolean => a.boardId === b.boardId && a.incarnation === b.incarnation;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
export function validParentRef(value: unknown, identity?: Identity): value is ParentRef {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const ref = value as ParentRef;
  return Object.keys(ref).sort().join() === "boardId,cardId,incarnation" &&
    [ref.boardId, ref.incarnation, ref.cardId].every(id => typeof id === "string" && uuid.test(id) && id !== "00000000-0000-0000-0000-000000000000") &&
    (!identity || sameIdentity(ref, identity));
}
export const hierarchyOrder = (a: Card, b: Card): number => a.ticket - b.ticket || (a.cardId < b.cardId ? -1 : a.cardId > b.cardId ? 1 : 0);

// Only a complete BoardRead can establish a forest. Neither a focused CardRead
// nor a search page establishes missing children, roots, depth or family size.
export function validateBoardHierarchy(read: BoardRead): void {
  const { identity, cards } = read.result;
  const byId = new Map<string, HierarchyCard>(), tickets = new Set<number>();
  for (const card of cards) {
    if (byId.has(card.cardId) || tickets.has(card.ticket)) invalid();
    byId.set(card.cardId, card); tickets.add(card.ticket);
  }
  const roots = new Map<string, number>();
  for (const card of byId.values()) {
    let current = card, depth = 0;
    const seen = new Set<string>([card.cardId]);
    while (Object.hasOwn(current, "parent")) {
      const ref = current.parent;
      if (!validParentRef(ref, identity) || seen.has(ref.cardId)) invalid();
      const next = byId.get(ref.cardId); if (!next || ++depth > 64) invalid();
      seen.add(ref.cardId); current = next;
    }
    const count = (roots.get(current.cardId) ?? 0) + 1;
    if (count > 1000) invalid(); roots.set(current.cardId, count);
  }
}

export type HierarchyNode = Readonly<{ card: HierarchyCard; depth: number; directChildCount: number; descendantCount: number }>;
export type HierarchySnapshot = Readonly<{ read: BoardRead; root: HierarchyCard; parent: HierarchyCard | null; directChildCount: number; nodes: readonly HierarchyNode[] }>;
export function captureHierarchy(source: BoardRead, cardId: string): HierarchySnapshot {
  validateBoardHierarchy(source);
  // Detach pages from every later network response and from caller mutation.
  const read = structuredClone(source), cards: readonly HierarchyCard[] = read.result.cards;
  const root = cards.find(card => card.cardId === cardId); if (!root) invalid();
  const children = new Map<string, HierarchyCard[]>();
  for (const card of cards) if (card.parent) {
    const rows = children.get(card.parent.cardId) ?? []; rows.push(card); children.set(card.parent.cardId, rows);
  }
  for (const rows of children.values()) rows.sort(hierarchyOrder);
  const nodes: HierarchyNode[] = [];
  const visit = (card: HierarchyCard, depth: number): number => {
    const index = nodes.length, rows = children.get(card.cardId) ?? [];
    nodes.push({ card, depth, directChildCount: rows.length, descendantCount: 0 });
    let descendants = 0;
    for (const child of rows) descendants += 1 + visit(child, depth + 1);
    nodes[index] = { card, depth, directChildCount: rows.length, descendantCount: descendants };
    return descendants;
  };
  const direct = children.get(root.cardId) ?? [];
  for (const child of direct) visit(child, 1);
  return { read, root, parent: root.parent ? cards.find(card => card.cardId === root.parent!.cardId)! : null, directChildCount: direct.length, nodes };
}
