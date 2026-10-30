export const emptySearch = () => ({ q: "", lane: null, owner: null, priority: null, archive: "active" });
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
export function validSearch(value) {
    if (!value || typeof value !== "object")
        return false;
    const row = value;
    return Object.keys(row).sort().join() === "archive,lane,owner,priority,q" && typeof row.q === "string" && row.q.isWellFormed() && new TextEncoder().encode(row.q).length <= 1024 &&
        (row.lane === null || ["Ready for Work", "In Progress", "Ready for Review", "Completed"].includes(row.lane)) &&
        (row.owner === null || row.owner === "unassigned" || typeof row.owner === "string" && uuid.test(row.owner) && row.owner !== "00000000-0000-0000-0000-000000000000") &&
        (row.priority === null || /^P[0-5]$/.test(row.priority)) && ["active", "archived", "all"].includes(row.archive);
}
export const sameSearch = (a, b) => a.q === b.q && a.lane === b.lane && a.owner === b.owner && a.priority === b.priority && a.archive === b.archive;
export const compareSummary = (a, b) => a.priority.charCodeAt(1) - b.priority.charCodeAt(1) || a.ticket - b.ticket || (a.cardId < b.cardId ? -1 : a.cardId > b.cardId ? 1 : 0);
export function validateSearchRead(read, filters) {
    const result = read.result;
    if (!sameSearch(result.query, filters) || result.items.length > 20 || result.nextCursor !== null && (!/^[\x21-\x7e]{1,2048}$/.test(result.nextCursor) || result.items.length !== 20))
        throw Error("Search response mismatch");
    const ids = new Set(), tickets = new Set();
    for (const [index, row] of result.items.entries()) {
        if (ids.has(row.cardId) || tickets.has(row.ticket) || index && compareSummary(result.items[index - 1], row) >= 0 ||
            filters.lane !== null && row.lane !== filters.lane || filters.priority !== null && row.priority !== filters.priority ||
            filters.owner !== null && row.ownerId !== (filters.owner === "unassigned" ? null : filters.owner) || filters.archive !== "all" && row.archived !== (filters.archive === "archived"))
            throw Error("Search row mismatch");
        ids.add(row.cardId);
        tickets.add(row.ticket);
    }
}
