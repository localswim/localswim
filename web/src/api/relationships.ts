import type { BoardRead } from "../../generated/transport/contracts.js";
import type { Card, Identity } from "../../generated/archive/contracts.js";
import { validParentRef, sameIdentity } from "./hierarchy.js";
import { invalid } from "./boundary.js";
export const relationshipKinds = ["blocking", "informational", "related", "reference", "duplicate"] as const;
export type RelationshipKind = typeof relationshipKinds[number];
export type CardRef = Identity & { cardId: string };
export type RelationshipSpec = { kind: RelationshipKind; target: CardRef };
export const forwardLabels: Record<RelationshipKind, string> = { blocking: "Depends on", informational: "Informs", related: "Related to", reference: "References", duplicate: "Duplicates" };
const inverseLabels: Record<RelationshipKind, string> = { blocking: "Blocks", informational: "Informed by", related: "Related to", reference: "Referenced by", duplicate: "Duplicated by" };
export function validRelationships(value: unknown): value is RelationshipSpec[] {
  if (!Array.isArray(value) || value.length > 100) return false;
  const seen = new Set<string>();
  return value.every(row => {
    if (!row || typeof row !== "object" || Object.keys(row).sort().join() !== "kind,target" || !relationshipKinds.includes(row.kind) || !validParentRef(row.target)) return false;
    const key = row.kind + ":" + row.target.boardId + ":" + row.target.incarnation + ":" + row.target.cardId;
    if (seen.has(key)) return false; seen.add(key); return true;
  });
}
export type RelationshipRow = Readonly<{ linkId: string; owner: CardRef; peer: CardRef; kind: RelationshipKind; direction: "outgoing" | "incoming" | "symmetric"; label: string; owned: boolean; card?: Card }>;
export type RelationshipSnapshot = Readonly<{ read: BoardRead; root: Card; ownedCount: number; incomingCount: number; rows: readonly RelationshipRow[] }>;
const compare = (a: string, b: string): number => a < b ? -1 : a > b ? 1 : 0;
export function captureRelationships(source: BoardRead, cardId: string): RelationshipSnapshot {
  const read = structuredClone(source), { identity, cards, links } = read.result;
  const root = cards.find(card => card.cardId === cardId); if (!root) invalid();
  const rows: RelationshipRow[] = [];
  for (const link of links) {
    if (!link.active) continue;
    const owned = link.cardId === cardId, incoming = sameIdentity(identity, link.target) && link.target.cardId === cardId;
    if (!owned && !incoming) continue;
    const kind = link.kind as RelationshipKind; if (!relationshipKinds.includes(kind)) invalid();
    const owner = { ...identity, cardId: link.cardId }, peer = owned ? { ...link.target } : owner;
    const card = sameIdentity(identity, peer) ? cards.find(card => card.cardId === peer.cardId) : undefined;
    rows.push({ linkId: link.linkId, owner, peer, kind, owned, direction: kind === "related" ? "symmetric" : owned ? "outgoing" : "incoming", label: owned ? forwardLabels[kind] : inverseLabels[kind], ...(card ? { card } : {}) });
  }
  rows.sort((a, b) => a.card && b.card ? a.card.ticket - b.card.ticket || compare(a.peer.cardId, b.peer.cardId) || compare(a.kind, b.kind) || compare(a.owner.cardId, b.owner.cardId) || compare(a.linkId, b.linkId) : a.card ? -1 : b.card ? 1 : compare(a.peer.boardId, b.peer.boardId) || compare(a.peer.incarnation, b.peer.incarnation) || compare(a.peer.cardId, b.peer.cardId) || compare(a.kind, b.kind) || compare(a.owner.cardId, b.owner.cardId) || compare(a.linkId, b.linkId));
  return { read, root, rows, ownedCount: rows.filter(row => row.owned).length, incomingCount: rows.filter(row => !row.owned).length };
}
export function completeOwnedRelationships(read: BoardRead, cardId: string): RelationshipSpec[] {
  return read.result.links.filter(row => row.active && row.cardId === cardId).map(row => ({ kind: row.kind as RelationshipKind, target: { ...row.target } }));
}
