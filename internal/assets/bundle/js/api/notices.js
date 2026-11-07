import { sameIdentity } from "./hierarchy.js";
export const noticeReadMaximum = 131_072;
export class NoticeRefusal extends Error {
    kind;
    constructor(kind) {
        super("Notice request refused");
        this.kind = kind;
    }
}
function validRange(row) {
    const { class: kind, lowerEvent: lower, upperEvent: upper, eventCount: count } = row;
    if (upper !== null && upper < lower)
        return false;
    switch (kind) {
        case "unavailable": return count === null;
        case "expired":
        case "gapped": return upper !== null && count === null;
        case "observed-empty": return upper === lower && count === 0;
        default: return upper !== null && count !== null && count === upper - lower &&
            count >= (kind === "mixed" ? 2 : 1) && count <= 1000;
    }
}
function validPair(row, target) {
    return target !== null && row.class !== "suppressed" && validRange(row);
}
// The generated exact schema validates shape and scalar limits. These checks
// bind evidence to the requested action and enforce relationships among fields.
export function validateNotices(view, identity, command) {
    const { targetId, checkpoint, entries, last, pending, lookup } = view;
    if (!sameIdentity(view.identity, identity) || (targetId === null) !== (checkpoint === null) ||
        (targetId === null) !== (view.configurationRevision === 0) ||
        view.state === "unconfigured" && targetId !== null ||
        targetId === null && (view.gapCount !== 0 || last !== null || pending !== null || entries.length !== 0) ||
        last && !validCut(last) || pending && (pending.class === "suppressed" || !validCut(pending)) ||
        command.action !== "inbox" && entries.length !== 0)
        throw Error("Notice view mismatch");
    const seen = new Set();
    for (const [index, entry] of entries.entries()) {
        const previous = entries[index - 1];
        if (!validPair(entry, targetId) || seen.has(entry.deliveryId) || previous &&
            (entry.acceptedAt > previous.acceptedAt ||
                entry.acceptedAt === previous.acceptedAt && entry.deliveryId < previous.deliveryId))
            throw Error("Notice inbox mismatch");
        seen.add(entry.deliveryId);
    }
    if (command.action === "lookup") {
        if (!lookup || (lookup.state === "accepted") !== (lookup.receipt !== null) ||
            lookup.receipt && (!validPair(lookup.receipt, targetId) || lookup.receipt.targetId !== command.targetId ||
                lookup.receipt.deliveryId !== command.deliveryId))
            throw Error("Notice lookup mismatch");
    }
    else if (lookup !== null)
        throw Error("Unexpected notice lookup");
}
function validCut(cut) {
    return (cut.class === "suppressed") === (cut.deliveryId === null) && validRange(cut);
}
