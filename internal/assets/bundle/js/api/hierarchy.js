import { invalid } from "./boundary.js";
export const sameIdentity = (a, b) => a.boardId === b.boardId && a.incarnation === b.incarnation;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
export function validParentRef(value, identity) {
    if (!value || typeof value !== "object" || Array.isArray(value))
        return false;
    const ref = value;
    return Object.keys(ref).sort().join() === "boardId,cardId,incarnation" &&
        [ref.boardId, ref.incarnation, ref.cardId].every(id => typeof id === "string" && uuid.test(id) && id !== "00000000-0000-0000-0000-000000000000") &&
        (!identity || sameIdentity(ref, identity));
}
export const hierarchyOrder = (a, b) => a.ticket - b.ticket || (a.cardId < b.cardId ? -1 : a.cardId > b.cardId ? 1 : 0);
// Only a complete BoardRead can establish a forest. Neither a focused CardRead
// nor a search page establishes missing children, roots, depth or family size.
export function validateBoardHierarchy(read) {
    const { identity, cards } = read.result;
    const byId = new Map(), tickets = new Set();
    for (const card of cards) {
        if (byId.has(card.cardId) || tickets.has(card.ticket))
            invalid();
        byId.set(card.cardId, card);
        tickets.add(card.ticket);
    }
    const roots = new Map();
    for (const card of byId.values()) {
        let current = card, depth = 0;
        const seen = new Set([card.cardId]);
        while (Object.hasOwn(current, "parent")) {
            const ref = current.parent;
            if (!validParentRef(ref, identity) || seen.has(ref.cardId))
                invalid();
            const next = byId.get(ref.cardId);
            if (!next || ++depth > 64)
                invalid();
            seen.add(ref.cardId);
            current = next;
        }
        const count = (roots.get(current.cardId) ?? 0) + 1;
        if (count > 1000)
            invalid();
        roots.set(current.cardId, count);
    }
}
export function captureHierarchy(source, cardId) {
    validateBoardHierarchy(source);
    // Detach pages from every later network response and from caller mutation.
    const read = structuredClone(source), cards = read.result.cards;
    const root = cards.find(card => card.cardId === cardId);
    if (!root)
        invalid();
    const children = new Map();
    for (const card of cards)
        if (card.parent) {
            const rows = children.get(card.parent.cardId) ?? [];
            rows.push(card);
            children.set(card.parent.cardId, rows);
        }
    for (const rows of children.values())
        rows.sort(hierarchyOrder);
    const nodes = [];
    const visit = (card, depth) => {
        const index = nodes.length, rows = children.get(card.cardId) ?? [];
        nodes.push({ card, depth, directChildCount: rows.length, descendantCount: 0 });
        let descendants = 0;
        for (const child of rows)
            descendants += 1 + visit(child, depth + 1);
        nodes[index] = { card, depth, directChildCount: rows.length, descendantCount: descendants };
        return descendants;
    };
    const direct = children.get(root.cardId) ?? [];
    for (const child of direct)
        visit(child, 1);
    return { read, root, parent: root.parent ? cards.find(card => card.cardId === root.parent.cardId) : null, directChildCount: direct.length, nodes };
}
