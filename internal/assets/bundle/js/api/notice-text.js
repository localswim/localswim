import { validate } from "./boundary.js";
const fallbackText = "Inspect the selected board for changes.";
const bytes = (text) => new TextEncoder().encode(text).length;
function fixedWhitespace(point) {
    return point >= 0x09 && point <= 0x0d || point === 0x20 || point === 0x85 || point === 0xa0 ||
        point === 0x1680 || point >= 0x2000 && point <= 0x200a || point === 0x2028 || point === 0x2029 ||
        point === 0x202f || point === 0x205f || point === 0x3000;
}
function validLabel(label) {
    if (!label.isWellFormed() || bytes(label) === 0 || bytes(label) > 96)
        return false;
    let nonblank = false;
    for (const scalar of label) {
        const point = scalar.codePointAt(0);
        if (point <= 0x1f || point >= 0x7f && point <= 0x9f || point === 0x2028 || point === 0x2029 ||
            point === 0x061c || point === 0x200e || point === 0x200f || point >= 0x202a && point <= 0x202e ||
            point >= 0x2066 && point <= 0x2069 || point === 0xfeff)
            return false;
        if (!fixedWhitespace(point))
            nonblank = true;
    }
    return nonblank;
}
function validTime(value) {
    if (!/^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$/.test(value))
        return false;
    const date = new Date(value);
    return Number.isFinite(date.getTime()) && date.getUTCFullYear() >= 1 &&
        date.toISOString().replace(".000Z", "Z") === value;
}
function validFacts(facts) {
    return validLabel(facts.label) && validTime(facts.from) && validTime(facts.through) && facts.from <= facts.through &&
        facts.tickets.length <= 32 && facts.tickets.every((ticket, index, tickets) => Number.isSafeInteger(ticket) && ticket >= 1 && (index === 0 || ticket > tickets[index - 1]));
}
// Reconstruct only to validate captured canonical bytes, never to replace a
// missing or legacy original at read/copy time.
function canonicalText(facts) {
    return "Board: " + facts.label + "\nObserved: " + facts.from + " to " + facts.through + "\nTickets: " +
        (facts.tickets.length === 0 ? "none" : facts.tickets.map(ticket => "#" + String(ticket)).join(", "));
}
// Production notice validation uses the exact generated shape plus the same
// fixed scalar/template rules as Go. Invalid payloads are refused, never repaired.
export function validNoticeText(value) {
    try {
        validate("transport:NoticeText", value);
    }
    catch {
        return false;
    }
    const payload = value;
    if (payload.version !== 1 || bytes(payload.text) > 512)
        return false;
    if (payload.kind === "fallback")
        return payload.facts === null && payload.text === fallbackText;
    return payload.kind === "complete" && payload.facts !== null && validFacts(payload.facts) &&
        payload.text === canonicalText(payload.facts);
}
