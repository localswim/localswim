import { invalid } from "./boundary.js";
import { validParentRef } from "./hierarchy.js";
const kinds = ["card", "comment", "link", "attachment", "approval"];
const originKey = (origin) => origin.boardId + "/" + origin.incarnation;
const numberKey = (n) => String(n).padStart(16, "0");
function ordered(rows, key) {
    let prior;
    for (const row of rows) {
        const current = key(row);
        if (prior !== undefined && current <= prior)
            invalid();
        prior = current;
    }
}
// Call after the generated HistoryRead schema check. These are the accepted
// Go D03/C03 cross-row invariants, not new transport or authority semantics.
// Incarnations may be inherited within the selected board. Revisions are keyed
// by full origin/card identity; transport ordering is distinct from display order.
export function validateHistoryRead(history) {
    const view = history.result, boardId = view.identity.boardId;
    if (view.revision === 0)
        invalid();
    const full = new Map();
    const dead = new Map();
    const needed = new Map();
    for (const kind of kinds) {
        full.set(kind, new Map());
        dead.set(kind, new Map());
        needed.set(kind, new Set());
    }
    const actors = new Set(), neededActors = new Set();
    const provenance = (origin) => { if (origin.boardId !== boardId)
        invalid(); };
    const actor = (id) => {
        if (!actors.has(id))
            invalid();
        neededActors.add(id);
    };
    const add = (kind, id, cardId) => {
        const collection = full.get(kind);
        if (collection.has(id))
            invalid();
        collection.set(id, cardId);
    };
    const resolve = (kind, id, cardId, removed) => {
        const owner = full.get(kind).get(id) ?? (removed ? dead.get(kind).get(id) : undefined);
        if (owner === undefined || owner !== cardId)
            invalid();
        needed.get(kind).add(id);
    };
    ordered(view.actors, row => row.actorId);
    for (const row of view.actors) {
        provenance(row.createdIn);
        if (actors.has(row.actorId))
            invalid();
        actors.add(row.actorId);
    }
    ordered(view.comments, row => row.commentId);
    for (const row of view.comments) {
        provenance(row.createdIn);
        add("comment", row.commentId, row.cardId);
        actor(row.actorId);
    }
    ordered(view.links, row => row.linkId);
    for (const row of view.links) {
        provenance(row.createdIn);
        add("link", row.linkId, row.cardId);
    }
    const lengths = new Map();
    ordered(view.attachments, row => row.attachmentId);
    for (const row of view.attachments) {
        provenance(row.createdIn);
        add("attachment", row.attachmentId, row.cardId);
        if (row.byteLength > 10485760 || lengths.has(row.sha256) && lengths.get(row.sha256) !== row.byteLength)
            invalid();
        lengths.set(row.sha256, row.byteLength);
    }
    ordered(view.approvals, row => row.approvalId);
    for (const row of view.approvals) {
        provenance(row.createdIn);
        add("approval", row.approvalId, row.cardId);
        actor(row.actorId);
    }
    ordered(view.tombstones, row => row.kind + "/" + row.id);
    for (const row of view.tombstones) {
        provenance(row.createdIn);
        if ((row.kind === "comment") !== (row.actorId !== null) ||
            (row.kind === "card") !== (row.createdAt === null) ||
            row.kind === "card" && row.id !== row.cardId)
            invalid();
        if (full.get(row.kind).has(row.id) || dead.get(row.kind).has(row.id))
            invalid();
        dead.get(row.kind).set(row.id, row.cardId);
        if (row.actorId !== null)
            actor(row.actorId);
    }
    ordered(view.records, row => originKey(row.origin) + "/" + row.cardId + "/" + numberKey(row.cardRevision));
    for (const row of view.records) {
        provenance(row.origin);
        if (row.cardId !== view.cardId || row.cardRevision >= view.cardRevision || (row.state === null) === (row.removal === null))
            invalid();
        actor(row.actorId);
        if (row.state?.ownerId != null)
            actor(row.state.ownerId);
        // The standalone history projection has no source-card registry. Validate
        // exact snapshot-time scope, without claiming current target availability.
        if (row.state && Object.hasOwn(row.state, "parent")) {
            const parent = row.state.parent;
            if (!validParentRef(parent, row.origin) || parent.cardId === row.cardId)
                invalid();
        }
        const refs = row.references;
        for (const [kind, ids] of [["comment", refs.commentIds], ["link", refs.linkIds],
            ["attachment", refs.attachmentIds], ["approval", refs.approvalIds]]) {
            ordered(ids, id => id);
            for (const id of ids)
                resolve(kind, id, row.cardId, row.state === null);
        }
        if (row.state?.currentApprovalId != null) {
            resolve("approval", row.state.currentApprovalId, row.cardId, false);
            if (!refs.approvalIds.includes(row.state.currentApprovalId))
                invalid();
        }
    }
    // Exact projection: every returned immutable fact (including tombstones) and
    // actor must be needed by these snapshots or their returned referenced facts.
    for (const kind of kinds) {
        for (const id of full.get(kind).keys())
            if (!needed.get(kind).has(id))
                invalid();
        for (const id of dead.get(kind).keys())
            if (!needed.get(kind).has(id))
                invalid();
    }
    if (neededActors.size !== actors.size)
        invalid();
    validateCoverage(view.coverage, boardId);
}
function validateCoverage(coverage, boardId) {
    ordered(coverage.gaps, gap => originKey(gap.origin) + "/" + gap.class + "/" +
        (gap.fromExclusive ?? "") + "/" + gap.throughInclusive + "/" + gap.reason + "/" +
        numberKey(gap.boardRevision) + "/" + gap.recordedAt);
    for (const gap of coverage.gaps) {
        if (gap.origin.boardId !== boardId)
            invalid();
        if (gap.fromExclusive !== null && gap.fromExclusive > gap.throughInclusive ||
            gap.reason === "cleared" && (gap.class === "activity" || gap.fromExclusive !== null))
            invalid();
    }
}
// Mirrors Go current(..., exactActors=true) and the CardView semantic checks.
// A focused card read cannot resolve target cards, so link targets may remain
// external/inherited references exactly as on the accepted Go boundary.
export function validateCardRead(card) {
    const view = card.result, current = view.card, boardId = view.identity.boardId;
    if (view.revision === 0 || current.createdIn.boardId !== boardId)
        invalid();
    if (Object.hasOwn(current, "parent")) {
        const parent = current.parent;
        if (!validParentRef(parent, view.identity) || parent.cardId === current.cardId)
            invalid();
    }
    const actors = new Map(), neededActors = new Set();
    const actor = (id) => {
        if (!actors.has(id))
            invalid();
        neededActors.add(id);
    };
    ordered(view.actors, row => row.actorId);
    for (const row of view.actors) {
        if (row.createdIn.boardId !== boardId || actors.has(row.actorId))
            invalid();
        actors.set(row.actorId, row.kind);
    }
    if (current.ownerId != null)
        actor(current.ownerId);
    const required = new Map(), found = new Set();
    for (const [kind, ids] of [["comment", current.commentIds], ["link", current.linkIds],
        ["attachment", current.attachmentIds]]) {
        ordered(ids, id => id);
        for (const id of ids) {
            const key = kind + "/" + id;
            if (required.has(key))
                invalid();
            required.set(key, current.cardId);
        }
    }
    const row = (kind, id, cardId, origin) => {
        const key = kind + "/" + id;
        if (origin.boardId !== boardId || required.get(key) !== cardId || found.has(key))
            invalid();
        found.add(key);
    };
    ordered(view.comments, item => item.commentId);
    for (const item of view.comments) {
        row("comment", item.commentId, item.cardId, item.createdIn);
        actor(item.actorId);
    }
    ordered(view.links, item => item.linkId);
    for (const item of view.links)
        row("link", item.linkId, item.cardId, item.createdIn);
    ordered(view.attachments, item => item.attachmentId);
    const lengths = new Map();
    for (const item of view.attachments) {
        row("attachment", item.attachmentId, item.cardId, item.createdIn);
        if (item.byteLength > 10485760 || lengths.has(item.sha256) && lengths.get(item.sha256) !== item.byteLength)
            invalid();
        lengths.set(item.sha256, item.byteLength);
    }
    if (found.size !== required.size)
        invalid();
    ordered(view.approvals, item => item.approvalId);
    const approvals = new Map();
    for (const item of view.approvals) {
        if (item.cardId !== current.cardId || item.createdIn.boardId !== boardId ||
            item.reviewedCardRevision > current.revision || actors.get(item.actorId) !== "human")
            invalid();
        actor(item.actorId);
        approvals.set(item.approvalId, item.cardId);
    }
    if (current.currentApprovalId !== null && approvals.get(current.currentApprovalId) !== current.cardId)
        invalid();
    if (actors.size !== neededActors.size)
        invalid();
    ordered(view.historyReferences, item => originKey(item.origin) + "/" + item.cardId + "/" + numberKey(item.cardRevision));
    for (const item of view.historyReferences) {
        if (item.origin.boardId !== boardId || item.cardId !== current.cardId || item.cardRevision >= current.revision)
            invalid();
    }
    validateCoverage(view.coverage, boardId);
}
