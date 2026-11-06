import { compareFiles, FilesReadRefusal } from "../api/files.js";
import { canonical } from "../api/boundary.js";
import { CoverPreview } from "./cover-preview.js";
function node(tag, text = "") {
    const result = document.createElement(tag);
    result.textContent = text;
    result.style.whiteSpace = "pre-wrap";
    result.style.overflowWrap = "anywhere";
    result.style.minWidth = "0";
    return result;
}
function button(text, run) {
    const result = node("button", text);
    result.type = "button";
    result.addEventListener("click", run);
    return result;
}
export async function coverBytes(client, selection, attachment, signal) {
    return new Uint8Array(await client.coverBytes(selection, attachment, signal));
}
const key = (value) => value.identity.boardId + "/" + value.identity.incarnation + "/" + value.cardId;
// Shares CardDetails' exact selection, with no editor or private-draft state.
export class CardFiles {
    client;
    detach;
    canDetach;
    cover;
    observeRevision;
    host = node("section");
    status = node("p");
    rows = node("div");
    detail = node("section");
    current = button("Current files", () => this.changeView("current"));
    retained = button("Retained files", () => this.changeView("retained"));
    refresh = button("Refresh files", () => { void this.load(); });
    next = button("Next files", () => { void this.load(true); });
    selection;
    view = "current";
    page;
    generation = 0;
    inspection = 0;
    active = true;
    downloading;
    coverPreview;
    coverAction;
    coverStatus;
    coverBase;
    constructor(client, detach, canDetach, cover, observeRevision) {
        this.client = client;
        this.detach = detach;
        this.canDetach = canDetach;
        this.cover = cover;
        this.observeRevision = observeRevision;
        this.host.setAttribute("aria-label", "Card files");
        this.host.style.maxWidth = "100%";
        this.status.setAttribute("role", "status");
        this.detail.setAttribute("aria-label", "File details");
        this.host.append(node("h3", "Files"), this.current, this.retained, this.refresh, this.next, this.status, this.rows, this.detail);
        this.update();
    }
    open(selected, coverBase) {
        if (!this.selection || key(this.selection) !== key(selected))
            this.view = "current";
        this.selection = structuredClone(selected);
        this.coverBase = coverBase;
        this.active = true;
        void this.load();
    }
    close() { this.selection = undefined; this.invalidate(); this.rows.replaceChildren(); this.update(); }
    suspend() { this.active = false; this.invalidate(); }
    resume() { this.active = true; this.update(); }
    observeBoardRevision(identity, revision) {
        const base = this.coverBase;
        if (!base || base.identity.boardId !== identity.boardId || base.identity.incarnation !== identity.incarnation || base.revision >= revision)
            return;
        this.disposeCover(true);
        this.coverBase = undefined;
    }
    invalidate() {
        const restoreFocus = this.rows.contains(document.activeElement) || this.detail.contains(document.activeElement);
        this.generation++;
        this.inspection++;
        this.downloading?.abort();
        this.downloading = undefined;
        this.disposeCover();
        this.coverBase = undefined;
        this.page = undefined;
        this.rows.replaceChildren();
        this.detail.replaceChildren();
        this.status.textContent = "Files stale. Refresh files from the first page.";
        this.update();
        if (restoreFocus && !this.refresh.disabled)
            this.refresh.focus();
    }
    update() {
        this.current.setAttribute("aria-pressed", String(this.view === "current"));
        this.retained.setAttribute("aria-pressed", String(this.view === "retained"));
        this.current.disabled = this.retained.disabled = this.refresh.disabled = !this.active || !this.selection;
        this.next.disabled = !this.active || !this.page?.result.nextCursor;
    }
    changeView(view) { this.view = view; void this.load(); }
    async load(more = false) {
        if (!this.active || !this.selection || more && !this.page?.result.nextCursor)
            return;
        const selected = this.selection, view = this.view, previous = more ? this.page : undefined;
        const restorePagingFocus = document.activeElement === this.next;
        const restoreRemovedFocus = this.rows.contains(document.activeElement) || this.detail.contains(document.activeElement);
        const generation = ++this.generation;
        this.inspection++;
        this.downloading?.abort();
        this.downloading = undefined;
        this.disposeCover();
        this.page = undefined;
        this.rows.replaceChildren();
        this.detail.replaceChildren();
        this.update();
        if (restoreRemovedFocus)
            this.refresh.focus();
        this.status.textContent = "Reading " + view + " files…";
        const valid = () => this.active && generation === this.generation && !!this.selection && key(this.selection) === key(selected);
        try {
            const read = await this.client.cardFiles(selected, view, previous?.result.nextCursor ?? undefined);
            if (!valid())
                return;
            const result = read.result;
            if (result.identity.boardId !== selected.identity.boardId || result.identity.incarnation !== selected.identity.incarnation || result.cardId !== selected.cardId || result.view !== view)
                throw Error("Files context mismatch");
            this.observeRevision?.(result.identity, result.revision);
            if (previous && (result.revision !== previous.result.revision || result.cardRevision !== previous.result.cardRevision ||
                result.items.length > 0 && compareFiles(previous.result.items.at(-1), result.items[0]) >= 0))
                throw new FilesReadRefusal("REVISION_CONFLICT");
            this.page = read;
            this.status.textContent = (result.items.length ? "Showing " + result.items.length + " " + view + " files." : "No " + view + " files returned.") +
                " Board revision " + result.revision + " · card revision " + result.cardRevision + ".";
            for (const row of result.items) {
                const article = node("article"), attachment = row.attachment;
                article.append(node("p", attachment.name), node("p", attachment.attachmentId + " · " + attachment.byteLength + " bytes · " + row.referenceState), button("Inspect file " + attachment.attachmentId, () => { void this.inspect(row); }));
                this.rows.append(article);
            }
        }
        catch (error) {
            if (!valid())
                return;
            this.page = undefined;
            this.rows.replaceChildren();
            this.status.textContent = error instanceof FilesReadRefusal && error.code === "REVISION_CONFLICT" ?
                "Files changed. Partial pages discarded. Refresh files from the first page." : "Files unavailable. Refresh files to retry from the first page.";
        }
        this.update();
        if (restorePagingFocus && (document.activeElement === document.body || document.activeElement === this.next))
            (this.next.disabled ? this.refresh : this.next).focus();
    }
    async inspect(row) {
        if (!this.active || !this.selection || !this.page)
            return;
        const selected = this.selection, page = this.page, generation = this.generation, inspection = ++this.inspection;
        this.downloading?.abort();
        this.downloading = undefined;
        this.disposeCover();
        this.detail.replaceChildren(node("p", "Reading file details…"));
        const valid = () => this.active && generation === this.generation && inspection === this.inspection;
        try {
            const read = await this.client.cardFile(selected, row.attachment.attachmentId);
            if (!valid())
                return;
            if (read.result.identity.boardId !== selected.identity.boardId || read.result.identity.incarnation !== selected.identity.incarnation ||
                read.result.cardId !== selected.cardId || read.result.file.attachment.attachmentId !== row.attachment.attachmentId)
                throw Error("File context mismatch");
            this.observeRevision?.(read.result.identity, read.result.revision);
            if (read.result.revision !== page.result.revision || read.result.cardRevision !== page.result.cardRevision ||
                canonical(read.result.file) !== canonical(row)) {
                this.invalidate();
                return;
            }
            const file = read.result.file, a = file.attachment, attribution = file.attribution;
            this.detail.replaceChildren(node("h4", "File details"), node("p", "Name: " + a.name), node("p", "Media label: " + a.mediaLabel), node("p", "Attachment UUID: " + a.attachmentId), node("p", "Card UUID: " + a.cardId), node("p", "Selected board: " + selected.identity.boardId + " · incarnation " + selected.identity.incarnation), node("p", "Created in: " + a.createdIn.boardId + "/" + a.createdIn.incarnation + " · " + a.createdAt), node("p", "Length: " + a.byteLength + " bytes · SHA-256 " + a.sha256), node("p", attribution.state === "known" ? "Added by: " + attribution.actorLabel + " · " + attribution.actorId + " · " + attribution.actorKind + " · evidence " + attribution.basis : "Added by: attribution unavailable"), node("p", "Reference: " + file.referenceState + " · board revision " + read.result.revision + " · card revision " + read.result.cardRevision));
            if (file.downloadState === "current_reference") {
                const state = node("p");
                state.setAttribute("role", "status");
                const offer = button("Offer file download", () => { void this.download(file, selected, offer, state, valid); });
                this.detail.append(offer, state);
                if (this.detach && this.view === "current" && file.referenceState === "current") {
                    const detach = button("Detach file", () => { if (valid() && (!this.canDetach || this.canDetach(read)))
                        this.detach(structuredClone(read)); });
                    detach.disabled = !!this.canDetach && !this.canDetach(read);
                    this.detail.append(detach);
                }
                if (this.cover && this.view === "current" && file.referenceState === "current")
                    this.inspectCover(read, valid);
            }
            else
                this.detail.append(node("p", "Download unavailable because this file is no longer attached."));
        }
        catch {
            if (valid())
                this.detail.replaceChildren(node("p", "File details unavailable. Refresh files to read current metadata."));
        }
    }
    disposeCover(stale = false) {
        const restoreFocus = stale && this.coverAction === document.activeElement;
        this.coverPreview?.dispose();
        this.coverPreview = undefined;
        if (this.coverAction)
            this.coverAction.disabled = true;
        if (stale && this.coverStatus)
            this.coverStatus.textContent = "Cover information is stale. Refresh card cover before choosing a cover.";
        this.coverAction = undefined;
        this.coverStatus = undefined;
        if (restoreFocus && !this.refresh.disabled)
            this.refresh.focus();
    }
    inspectCover(read, valid) {
        const base = this.coverBase, actions = this.cover;
        const { identity, cardId, revision, cardRevision, file } = read.result, attachment = file.attachment;
        if (!base || base.cardId !== cardId || base.identity.boardId !== identity.boardId ||
            base.identity.incarnation !== identity.incarnation || base.revision !== revision || base.cardRevision !== cardRevision)
            return;
        if (base.explicit === attachment.attachmentId) {
            this.coverStatus = node("p", "This file is the explicit card cover.");
            this.detail.append(this.coverStatus);
            return;
        }
        const candidate = { identity, cardId, revision, cardRevision, attachment };
        const state = node("p");
        state.setAttribute("role", "status");
        this.coverStatus = state;
        let eligible = false;
        const set = button(base.explicit ? "Change card cover" : "Set card cover", () => {
            if (eligible && valid() && actions.allowed(candidate)) {
                set.focus();
                actions.edit(candidate);
            }
        });
        set.dataset.focusKey = cardId + ":cover:" + attachment.attachmentId;
        this.coverAction = set;
        set.disabled = true;
        this.detail.append(state, set);
        if (!actions.allowed(candidate)) {
            state.textContent = "Cover selection unavailable for this card and current access.";
            return;
        }
        const canvas = node("canvas");
        canvas.hidden = true; // Eligibility uses the same verified decode path as displayed covers.
        const preview = new CoverPreview({
            canvas, priority: "details", isCurrent: valid,
            fetchBytes: (a, signal) => coverBytes(actions, { identity, cardId }, a, signal),
            onState: result => {
                eligible = result.status === "ready";
                set.disabled = !eligible || !actions.allowed(candidate);
                state.textContent = eligible ? "Verified image eligible for card cover." : result.status === "loading" ?
                    "Checking card cover eligibility…" : result.status === "ineligible" ? "This file cannot be displayed as a card cover." :
                    "Card cover eligibility unavailable. Inspect this file again to retry.";
            },
        });
        this.coverPreview = preview;
        preview.start([attachment], attachment.attachmentId);
    }
    async download(file, selected, offer, state, valid) {
        if (!valid() || this.downloading)
            return;
        const controller = new AbortController();
        this.downloading = controller;
        offer.disabled = true;
        const cancel = button("Cancel file download", () => { controller.abort(); state.textContent = "File download cancelled. No verified offer made."; });
        this.detail.append(cancel);
        state.textContent = "Verifying complete file bytes…";
        try {
            const a = file.attachment;
            const bytes = await this.client.downloadAttachment(selected.identity.boardId, a.attachmentId, { sha256: a.sha256, byteLength: a.byteLength, incarnation: selected.identity.incarnation, cardId: selected.cardId }, controller.signal);
            if (!valid() || controller.signal.aborted)
                return;
            const url = URL.createObjectURL(new Blob([bytes], { type: "application/octet-stream" })), link = node("a");
            link.href = url;
            link.download = "attachment-" + a.attachmentId + ".bin";
            link.hidden = true;
            this.detail.append(link);
            try {
                link.click();
            }
            finally {
                link.remove();
                setTimeout(() => URL.revokeObjectURL(url), 0);
            }
            state.textContent = "File download offered after complete byte verification. Local save unverified.";
        }
        catch {
            if (valid() && !controller.signal.aborted)
                state.textContent = "File bytes unavailable or incomplete. No verified offer made.";
        }
        finally {
            if (this.downloading === controller)
                this.downloading = undefined;
            const restoreFocus = document.activeElement === cancel;
            cancel.remove();
            if (valid()) {
                offer.disabled = false;
                if (restoreFocus)
                    offer.focus();
            }
        }
    }
}
