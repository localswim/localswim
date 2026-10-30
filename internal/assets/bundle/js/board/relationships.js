import { captureRelationships } from "../api/relationships.js";
import { sameIdentity } from "../api/hierarchy.js";
function node(tag, text = "") { const n = document.createElement(tag); n.textContent = text; return n; }
function button(text, run) { const n = node("button", text); n.type = "button"; n.addEventListener("click", run); return n; }
const ref = (value) => value.boardId + "/" + value.incarnation + "/" + value.cardId;
export class CardRelationships {
    client;
    navigate;
    action;
    host = node("section");
    latest;
    snapshot;
    selection;
    generation = 0;
    active = true;
    stale = true;
    knownRevision = 0;
    page = 0;
    message = "";
    constructor(client, navigate, action) {
        this.client = client;
        this.navigate = navigate;
        this.action = action;
        this.host.setAttribute("aria-label", "Card relationships");
    }
    boardRead(read) {
        this.knownRevision = this.latest && sameIdentity(this.latest.result.identity, read.result.identity) ? Math.max(this.knownRevision, read.result.revision) : read.result.revision;
        this.latest = structuredClone(read);
        if (this.selection && !sameIdentity(this.selection.identity, read.result.identity)) {
            this.close();
            return;
        }
        if (this.snapshot && this.snapshot.read.result.revision !== read.result.revision)
            this.markStale();
    }
    observeRevision(identity, revision) { if (this.latest && sameIdentity(identity, this.latest.result.identity)) {
        this.knownRevision = Math.max(this.knownRevision, revision);
        if (this.snapshot && revision > this.snapshot.read.result.revision)
            this.markStale();
    } }
    open(cardId, identity) {
        this.generation++;
        this.selection = { cardId, identity: { ...identity } };
        this.page = 0;
        this.snapshot = undefined;
        this.message = "";
        if (this.latest && sameIdentity(identity, this.latest.result.identity)) {
            try {
                this.snapshot = captureRelationships(this.latest, cardId);
            }
            catch { }
        }
        this.stale = !this.snapshot || this.snapshot.read.result.revision < this.knownRevision;
        this.render();
    }
    close() { this.generation++; this.selection = undefined; this.snapshot = undefined; this.host.replaceChildren(); }
    suspend() { this.active = false; this.markStale(); }
    resume() { this.active = true; }
    markStale() { this.stale = true; this.generation++; if (this.selection)
        this.render(); }
    async readCurrent() {
        if (!this.active || !this.selection)
            return;
        const selection = this.selection, generation = ++this.generation;
        this.message = "Reading current relationships…";
        this.render();
        try {
            const read = await this.client.board(selection.identity.boardId);
            if (!this.active || generation !== this.generation || selection !== this.selection)
                return;
            if (!sameIdentity(selection.identity, read.result.identity) || read.result.revision < this.knownRevision)
                throw Error("Stale read");
            this.snapshot = captureRelationships(read, selection.cardId);
            this.latest = read;
            this.knownRevision = read.result.revision;
            this.stale = false;
            this.page = 0;
            this.message = "Current relationships read. Pages reset to 1.";
        }
        catch {
            if (!this.active || generation !== this.generation || selection !== this.selection)
                return;
            this.stale = true;
            this.message = "Current relationships unavailable; retained snapshot and private input remain. Retry deliberately.";
        }
        this.render();
    }
    render() {
        const focused = this.host.contains(document.activeElement) ? document.activeElement : undefined, key = focused?.dataset.focusKey, label = focused?.textContent;
        this.host.replaceChildren(node("h3", "Relationships"), button("Read current relationships", () => { void this.readCurrent(); }));
        const status = node("p", this.message + (this.stale ? " Relationships stale; read current relationships deliberately." : ""));
        status.setAttribute("role", "status");
        this.host.append(status);
        const snapshot = this.snapshot;
        if (snapshot) {
            const { read, root, rows, ownedCount, incomingCount } = snapshot, identity = read.result.identity;
            this.host.append(node("p", rows.length + " relationships · Owned: " + ownedCount + " · Incoming: " + incomingCount + " · complete board revision " + read.result.revision));
            const act = (action, text, row) => {
                const n = button(text, () => { n.focus({ preventScroll: true }); if (this.active && !this.stale)
                    this.action?.(action, root, read, row); });
                n.disabled = this.stale || root.lane === "Completed";
                n.dataset.focusKey = root.cardId + ":relationships:" + action + ":" + (row?.linkId ?? "");
                return n;
            };
            if (this.action)
                this.host.append(act("add", "Add relationship"), act("replace", "Replace owned relationships"));
            this.host.append(node("p", "Incoming relationships belong to their actual owner. Open owner deliberately to change them."));
            const start = this.page * 20, stop = Math.min(rows.length, start + 20);
            this.host.append(node("p", "Relationships " + (rows.length ? start + 1 : 0) + "–" + stop + " of " + rows.length + " · page " + (this.page + 1) + " of " + Math.max(1, Math.ceil(rows.length / 20))));
            const move = (delta) => { if (!this.active)
                return; this.page += delta; this.render(); [...this.host.querySelectorAll("button")].find(n => n.textContent === (delta > 0 ? "Previous relationships" : "Next relationships") && !n.disabled)?.focus(); };
            const prev = button("Previous relationships", () => move(-1)), next = button("Next relationships", () => move(1));
            prev.disabled = this.page === 0;
            next.disabled = stop >= rows.length;
            this.host.append(prev, next);
            const open = (cardId, text, key) => { const n = button(text, () => { if (this.active)
                this.navigate(cardId, identity, n); }); n.dataset.openCard = cardId; n.dataset.focusKey = root.cardId + ":relationships:" + key; return n; };
            for (const row of rows.slice(start, stop)) {
                const article = node("article");
                article.dataset.relationship = row.linkId;
                article.append(node("h4", (row.owned ? "Owned · " : "Incoming · ") + row.label), node("p", row.kind + " · " + row.direction + " · " + row.linkId), node("p", "Peer: " + ref(row.peer) + (row.card ? " · #" + row.card.ticket + " " + row.card.title + " · " + row.card.lane + " · " + (row.card.archived ? "Archived" : "Active") : " · Unavailable")), node("p", "Actual owner: " + ref(row.owner)));
                if (row.card)
                    article.append(open(row.card.cardId, "Open relationship peer", row.linkId + ":peer"));
                if (!row.owned && read.result.cards.some(card => card.cardId === row.owner.cardId))
                    article.append(open(row.owner.cardId, "Open relationship owner", row.linkId + ":owner"));
                if (row.owned && this.action)
                    article.append(act("remove", "Remove owned relationship", row));
                this.host.append(article);
            }
        }
        else
            this.host.append(node("p", "Relationships unavailable without a complete board snapshot."));
        if (focused && this.active) {
            const controls = [...this.host.querySelectorAll("button")];
            (controls.find(n => !n.disabled && (key ? n.dataset.focusKey === key : n.textContent === label)) ?? controls[0])?.focus({ preventScroll: true });
        }
    }
}
