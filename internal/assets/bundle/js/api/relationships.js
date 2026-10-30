import { validParentRef, sameIdentity } from "./hierarchy.js";
import { invalid } from "./boundary.js";
export const relationshipKinds = ["blocking", "informational", "related", "reference", "duplicate"];
export const forwardLabels = { blocking: "Depends on", informational: "Informs", related: "Related to", reference: "References", duplicate: "Duplicates" };
const inverseLabels = { blocking: "Blocks", informational: "Informed by", related: "Related to", reference: "Referenced by", duplicate: "Duplicated by" };
export function validRelationships(value) {
    if (!Array.isArray(value) || value.length > 100)
        return false;
    const seen = new Set();
    return value.every(row => {
        if (!row || typeof row !== "object" || Object.keys(row).sort().join() !== "kind,target" || !relationshipKinds.includes(row.kind) || !validParentRef(row.target))
            return false;
        const key = row.kind + ":" + row.target.boardId + ":" + row.target.incarnation + ":" + row.target.cardId;
        if (seen.has(key))
            return false;
        seen.add(key);
        return true;
    });
}
const compare = (a, b) => a < b ? -1 : a > b ? 1 : 0;
export function captureRelationships(source, cardId) {
    const read = structuredClone(source), { identity, cards, links } = read.result;
    const root = cards.find(card => card.cardId === cardId);
    if (!root)
        invalid();
    const rows = [];
    for (const link of links) {
        if (!link.active)
            continue;
        const owned = link.cardId === cardId, incoming = sameIdentity(identity, link.target) && link.target.cardId === cardId;
        if (!owned && !incoming)
            continue;
        const kind = link.kind;
        if (!relationshipKinds.includes(kind))
            invalid();
        const owner = { ...identity, cardId: link.cardId }, peer = owned ? { ...link.target } : owner;
        const card = sameIdentity(identity, peer) ? cards.find(card => card.cardId === peer.cardId) : undefined;
        rows.push({ linkId: link.linkId, owner, peer, kind, owned, direction: kind === "related" ? "symmetric" : owned ? "outgoing" : "incoming", label: owned ? forwardLabels[kind] : inverseLabels[kind], ...(card ? { card } : {}) });
    }
    rows.sort((a, b) => a.card && b.card ? a.card.ticket - b.card.ticket || compare(a.peer.cardId, b.peer.cardId) || compare(a.kind, b.kind) || compare(a.owner.cardId, b.owner.cardId) || compare(a.linkId, b.linkId) : a.card ? -1 : b.card ? 1 : compare(a.peer.boardId, b.peer.boardId) || compare(a.peer.incarnation, b.peer.incarnation) || compare(a.peer.cardId, b.peer.cardId) || compare(a.kind, b.kind) || compare(a.owner.cardId, b.owner.cardId) || compare(a.linkId, b.linkId));
    return { read, root, rows, ownedCount: rows.filter(row => row.owned).length, incomingCount: rows.filter(row => !row.owned).length };
}
export function completeOwnedRelationships(read, cardId) {
    return read.result.links.filter(row => row.active && row.cardId === cardId).map(row => ({ kind: row.kind, target: { ...row.target } }));
}
