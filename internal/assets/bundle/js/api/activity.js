import { canonical } from "./boundary.js";
import { sameIdentity } from "./hierarchy.js";
export const activityReadMaximum = 32_768;
export class ActivityCursorStale extends Error {
}
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
const compare = (a, b) => a < b ? -1 : a > b ? 1 : 0;
const compareOrigin = (a, b) => compare(a.boardId, b.boardId) || compare(a.incarnation, b.incarnation);
const compareEvent = (a, b) => compare(a.at, b.at) || compareOrigin(a.origin, b.origin) || a.eventId - b.eventId;
const compareGap = (a, b) => compareOrigin(a.origin, b.origin) || compare(a.fromExclusive ?? "", b.fromExclusive ?? "") ||
    compare(a.throughInclusive, b.throughInclusive) || compare(a.reason, b.reason);
function validCursor(cursor) {
    if (cursor.length > 2048 || !/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(cursor))
        return false;
    return cursor.split(".").every(part => {
        const remainder = part.length % 4, last = alphabet.indexOf(part.at(-1));
        return remainder === 0 || remainder === 2 && last % 16 === 0 || remainder === 3 && last % 4 === 0;
    });
}
// The generated schema checks exact fields, limits and primitive values first.
// Here we compare the validated facts that depend on the requested inspection.
export function validateActivityPage(page, identity, previous) {
    const { from, through, events, nextCursor, localEventUpper, coverage } = page;
    const span = Date.parse(through) - Date.parse(from);
    if (!sameIdentity(page.identity, identity) || span !== 604_800_000 ||
        nextCursor !== null && (events.length !== 20 || !validCursor(nextCursor)))
        throw Error("Activity page mismatch");
    const { activityFloor, gaps, status } = coverage;
    const expectedStatus = activityFloor !== null && activityFloor >= through ? "expired" :
        gaps.length || activityFloor !== null && activityFloor > from ? "gapped" : "complete";
    if (status !== expectedStatus || gaps.some((gap, index) => gap.throughInclusive <= from ||
        gap.fromExclusive !== null && (gap.fromExclusive >= through || gap.fromExclusive >= gap.throughInclusive) ||
        index > 0 && compareGap(gaps[index - 1], gap) >= 0))
        throw Error("Activity coverage mismatch");
    const last = previous?.events.at(-1);
    if (previous && (from !== previous.from || through !== previous.through ||
        page.revision !== previous.revision || localEventUpper !== previous.localEventUpper ||
        canonical(coverage) !== canonical(previous.coverage) ||
        !last || !events.length || nextCursor !== null && nextCursor === previous.nextCursor ||
        compareEvent(last, events[0]) <= 0))
        throw Error("Activity continuation mismatch");
    const keys = new Set();
    for (const [index, event] of events.entries()) {
        const key = event.origin.boardId + "/" + event.origin.incarnation + "/" + event.eventId;
        if (event.at <= from || event.at > through || sameIdentity(event.origin, identity) && event.eventId > localEventUpper ||
            event.actorId === null && event.actorKind !== "unknown" || keys.has(key) ||
            index > 0 && compareEvent(events[index - 1], event) <= 0)
            throw Error("Activity event mismatch");
        keys.add(key);
    }
}
