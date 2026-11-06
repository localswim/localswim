import { invalid } from "./boundary.js";
export const fileReadMaximum = 134_217_728;
const actorLabelMaximum = 33_554_432;
export class FilesReadRefusal extends Error {
    code;
    constructor(code) {
        super(code === "REVISION_CONFLICT" ? "Files changed. Refresh files from the first page." : "Files unavailable. Retry deliberately.");
        this.code = code;
    }
}
export function compareFiles(a, b) {
    const x = a.attachment, y = b.attachment;
    return x.createdAt < y.createdAt ? -1 : x.createdAt > y.createdAt ? 1 : x.attachmentId < y.attachmentId ? -1 : x.attachmentId > y.attachmentId ? 1 : 0;
}
function context(result, selected) {
    if (result.identity.boardId !== selected.identity.boardId || result.identity.incarnation !== selected.identity.incarnation || result.cardId !== selected.cardId)
        invalid();
}
function file(row, selected) {
    if (row.attachment.cardId !== selected.cardId || row.attachment.createdIn.boardId !== selected.identity.boardId ||
        new TextEncoder().encode(row.attachment.name).length > 255 || !/^[\x20-\x7e]{0,127}$/.test(row.attachment.mediaLabel) ||
        row.downloadState !== (row.referenceState === "current" ? "current_reference" : "not_current"))
        invalid();
    const actor = row.attribution;
    if (actor.actorLabel !== null && new TextEncoder().encode(actor.actorLabel).length > actorLabelMaximum)
        invalid();
    if (actor.state === "known" ? actor.actorId === null || actor.actorKind === null || actor.actorLabel === null || actor.basis === null :
        actor.actorId !== null || actor.actorKind !== null || actor.actorLabel !== null || actor.basis !== null)
        invalid();
}
export function validateFilesRead(read, selected, view, limit) {
    const result = read.result;
    context(result, selected);
    if (result.view !== view || result.items.length > limit || result.nextCursor !== null &&
        (!/^[A-Za-z0-9_-]{1,1024}$/.test(result.nextCursor) || result.items.length === 0))
        invalid();
    const ids = new Set();
    for (const [index, row] of result.items.entries()) {
        file(row, selected);
        if (ids.has(row.attachment.attachmentId) || row.referenceState !== view || index > 0 && compareFiles(result.items[index - 1], row) >= 0)
            invalid();
        ids.add(row.attachment.attachmentId);
    }
}
export function validateFileRead(read, selected, attachmentId) {
    context(read.result, selected);
    file(read.result.file, selected);
    if (read.result.file.attachment.attachmentId !== attachmentId)
        invalid();
}
