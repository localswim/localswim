import { compareSummary, emptySearch, sameSearch, validSearch } from "../api/search.js";
import { button, element } from "../recovery-ui/view.js";
export function validPrivateSearch(value) {
    const state = value;
    return !!state && validSearch(state.draft) && (state.applied === undefined || validSearch(state.applied));
}
// Search is an independent read surface: summaries never become editor bases.
export class CardSearch {
    client;
    boardId;
    archive;
    open;
    checkpoint;
    host = element("section");
    find;
    query = element("input");
    lane = element("select");
    owner = element("input");
    priority = element("select");
    status = element("p", "Apply filters to search committed current cards.");
    storage = element("p");
    rows = element("section");
    next;
    first;
    identity;
    revisionFloor = 0;
    result;
    applied;
    generation = 0;
    active = true;
    busy = false;
    stale = true;
    composing = false;
    confirmingEnter = false;
    restored = false;
    constructor(client, boardId, archive, open, checkpoint, initial) {
        this.client = client;
        this.boardId = boardId;
        this.archive = archive;
        this.open = open;
        this.checkpoint = checkpoint;
        this.find = button("Find cards", () => this.query.focus());
        this.next = button("Next results", () => { void this.read(true, true, this.next); });
        this.first = button("First page", () => { void this.read(false, false, this.first); });
        this.status.setAttribute("role", "status");
        this.rows.setAttribute("aria-label", "Search results");
        this.query.setAttribute("aria-label", "Search cards");
        this.owner.setAttribute("aria-label", "Owner");
        this.owner.placeholder = "Any owner; unassigned; actor UUID";
        const choices = (control, label, values) => {
            control.setAttribute("aria-label", label);
            for (const value of ["", ...values]) {
                const option = element("option", value || "Any");
                option.value = value;
                control.append(option);
            }
        };
        choices(this.lane, "Lane", ["Ready for Work", "In Progress", "Ready for Review", "Completed"]);
        choices(this.priority, "Explicit priority", ["P0", "P1", "P2", "P3", "P4", "P5"]);
        const form = element("form");
        form.addEventListener("submit", event => event.preventDefault());
        for (const [labelText, control] of [["Search cards", this.query], ["Lane", this.lane], ["Owner", this.owner], ["Explicit priority", this.priority], ["Archive status", this.archive]]) {
            const label = element("label", labelText + " ");
            label.append(control);
            form.append(label);
            control.addEventListener("input", () => this.changed());
            control.addEventListener("change", () => this.changed());
        }
        form.addEventListener("compositionstart", () => { this.composing = true; });
        form.addEventListener("compositionend", () => { this.composing = false; this.confirmingEnter = true; setTimeout(() => { this.confirmingEnter = false; }, 0); });
        form.addEventListener("keydown", event => {
            if (event.key === "Enter" && event.target instanceof HTMLInputElement) {
                event.preventDefault();
                if (!event.repeat && !event.isComposing && !this.composing && !this.confirmingEnter && event.keyCode !== 229)
                    void this.read(false);
            }
        });
        form.append(button("Apply filters", () => { if (!this.composing)
            void this.read(false); }), button("Clear filters", () => {
            this.setDraft(emptySearch());
            this.archive.dispatchEvent(new Event("change"));
            this.changed();
            void this.read(false);
        }));
        this.host.append(this.find, form, this.status, this.storage, this.first, this.next, this.rows);
        if (validPrivateSearch(initial)) {
            this.setDraft(initial.draft);
            this.applied = initial.applied;
            this.restored = !!initial.applied;
        }
        this.update();
    }
    draft() { return { q: this.query.value, lane: this.lane.value || null, owner: this.owner.value || null, priority: this.priority.value || null, archive: this.archive.value }; }
    setDraft(value) { this.query.value = value.q; this.lane.value = value.lane ?? ""; this.owner.value = value.owner ?? ""; this.priority.value = value.priority ?? ""; this.archive.value = value.archive; }
    persist() {
        if (!this.checkpoint) {
            this.storage.textContent = "Search filters are held in memory for this window lifetime.";
            return;
        }
        try {
            if (!validSearch(this.draft()))
                throw Error();
            this.checkpoint({ draft: this.draft(), ...(this.applied ? { applied: this.applied } : {}) });
            this.storage.textContent = "Search filters saved privately for this window and board; pages are not saved.";
        }
        catch {
            this.storage.textContent = "Search filter storage unavailable or limit exceeded; visible input retained in memory only.";
        }
    }
    changed() {
        this.generation++;
        this.busy = false;
        this.stale = true;
        this.status.textContent = this.result ? "Filters changed; previous results are stale. Apply filters to search." : "Filters changed; apply filters to search.";
        this.persist();
        this.update();
    }
    boardRead(identity, revision) {
        if (this.identity && (this.identity.boardId !== identity.boardId || this.identity.incarnation !== identity.incarnation)) {
            this.generation++;
            this.result = undefined;
            this.rows.replaceChildren();
            this.busy = false;
            this.stale = true;
            this.revisionFloor = 0;
        }
        this.identity = structuredClone(identity);
        this.revisionFloor = Math.max(this.revisionFloor, revision);
        if (this.result && this.result.result.revision !== revision)
            this.invalidate();
        if (this.restored) {
            this.restored = false;
            void this.read(false, false);
        }
        this.update();
    }
    invalidate() { this.generation++; this.busy = false; this.stale = true; this.status.textContent = this.result ? "Displayed search results are stale; choose First page or Apply filters." : "Search unavailable until a deliberate fresh read."; this.update(); }
    suspend() { this.active = false; this.invalidate(); }
    resume() { this.active = true; this.update(); }
    update() { this.next.disabled = !this.active || this.busy || this.stale || !this.result?.result.nextCursor || !this.applied || !sameSearch(this.draft(), this.applied); this.first.disabled = !this.active || !this.identity || this.busy; }
    async read(continuation, apply = true, invoked) {
        if (!this.active || !this.identity || continuation && this.next.disabled)
            return;
        const filters = structuredClone(apply ? this.draft() : this.applied ?? this.draft()), previous = this.result;
        if (!validSearch(filters)) {
            this.generation++;
            this.busy = false;
            this.stale = true;
            this.status.textContent = "Invalid filters. Query must be valid Unicode at most 1024 UTF-8 bytes; use exact lane, actor UUID/unassigned and P0–P5 values." + (previous ? " Previous results are stale." : " No confirmed results.");
            this.update();
            return;
        }
        const generation = ++this.generation, identity = structuredClone(this.identity), cursor = continuation ? previous?.result.nextCursor ?? undefined : undefined;
        // Native disabled buttons can shed keyboard focus to BODY during loading.
        // Restore only our own invocation, and permanently relinquish this request's
        // focus claim as soon as the user focuses any other control, including an editor.
        let ownsFocus = !!invoked && document.activeElement === invoked;
        const movedFocus = (event) => { if (event.target !== invoked && event.target !== document.body)
            ownsFocus = false; };
        document.addEventListener("focusin", movedFocus, true);
        this.busy = true;
        this.stale = true;
        this.status.textContent = previous ? "Loading; previous results are stale." : "Loading search results.";
        this.update();
        try {
            const read = await this.client.search(this.boardId, filters, cursor);
            if (!this.active || generation !== this.generation)
                return;
            if (read.result.revision < this.revisionFloor)
                throw Error("Search snapshot is older than current board facts; choose First page");
            if (read.result.identity.incarnation !== identity.incarnation || read.result.identity.boardId !== identity.boardId || continuation && (!previous || read.result.revision !== previous.result.revision || read.result.items.some(row => previous.result.items.some(old => old.cardId === row.cardId || old.ticket === row.ticket)) || read.result.items.length && compareSummary(previous.result.items.at(-1), read.result.items[0]) >= 0))
                throw Error("Search page mismatch; choose First page");
            this.result = read;
            this.applied = filters;
            this.stale = false;
            this.revisionFloor = Math.max(this.revisionFloor, read.result.revision);
            this.status.textContent = read.result.items.length + " results returned on this page · " + (read.result.nextCursor ? "More results available." : "End of results.") + " Board revision " + read.result.revision + (sameSearch(this.draft(), filters) ? "" : " · Draft filters differ; Apply filters to use them.");
            this.rows.replaceChildren();
            for (const card of read.result.items) {
                const article = element("article"), opener = button("Open card #" + card.ticket + " " + card.title, () => { if (this.active)
                    this.open(card.cardId, read.result.identity, opener); });
                opener.dataset.openCard = card.cardId;
                opener.dataset.focusKey = "search:" + card.cardId;
                article.append(element("h2", "#" + card.ticket + " " + card.title), element("p", card.cardId + " · " + card.priority + " · " + card.lane + " · Owner: " + (card.ownerId ?? "Unassigned") + " · " + (card.archived ? "Archived" : "Active")), opener);
                this.rows.append(article);
            }
            this.persist();
        }
        catch (error) {
            if (this.active && generation === this.generation) {
                this.stale = true;
                this.status.textContent = (error instanceof Error ? error.message : "Search unavailable") + (previous ? "; previous results are stale." : "; no confirmed results.");
            }
        }
        finally {
            document.removeEventListener("focusin", movedFocus, true);
            if (generation === this.generation) {
                this.busy = false;
                this.update();
                if (this.active && ownsFocus && invoked?.isConnected &&
                    (document.activeElement === invoked || document.activeElement === document.body || document.activeElement === null)) {
                    const target = !invoked.disabled ? invoked : !this.first.disabled ? this.first : this.find;
                    if (target.isConnected && target.getClientRects().length > 0)
                        target.focus({ preventScroll: true });
                }
            }
        }
    }
}
