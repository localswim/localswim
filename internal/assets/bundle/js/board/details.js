function node(tag, text = "") {
    const result = document.createElement(tag);
    result.textContent = text;
    result.style.whiteSpace = "pre-wrap";
    result.style.overflowWrap = "anywhere";
    return result;
}
function control(label, run) {
    const result = node("button", label);
    result.type = "button";
    result.addEventListener("click", run);
    return result;
}
function compare(a, b) { return a < b ? -1 : a > b ? 1 : 0; }
function origin(identity) { return identity.boardId + "/" + identity.incarnation; }
function same(a, b) { return a.boardId === b.boardId && a.incarnation === b.incarnation; }
function attribution(id, actors) {
    const actor = actors.find(row => row.actorId === id);
    return actor ? actor.label + " (" + id + ")" : id;
}
export function sortedComments(rows) {
    return [...rows].sort((a, b) => compare(a.createdAt, b.createdAt) || compare(a.commentId, b.commentId));
}
export function priorHistory(rows, identity, cardId, revision) {
    return rows.filter(row => row.cardId === cardId && !(same(row.origin, identity) && row.cardRevision === revision)).sort((a, b) => {
        const currentA = same(a.origin, identity), currentB = same(b.origin, identity);
        return Number(currentB) - Number(currentA) || compare(a.origin.boardId, b.origin.boardId) ||
            compare(a.origin.incarnation, b.origin.incarnation) || b.cardRevision - a.cardRevision;
    });
}
function coverage(host, value) {
    host.append(node("p", "Returned coverage: history floor " + (value.historyFloor ?? "not recorded") +
        "; activity floor " + (value.activityFloor ?? "not recorded") + "; outcome payload floor " + (value.outcomePayloadFloor ?? "not recorded") + ". Returned rows do not prove complete history."));
    for (const gap of value.gaps)
        host.append(node("p", "Coverage gap: " + gap.class + " · " + origin(gap.origin) + " · " +
            (gap.fromExclusive ?? "unbounded start") + " exclusive through " + gap.throughInclusive + " inclusive · " + gap.reason +
            " · recorded " + gap.recordedAt + " at board revision " + gap.boardRevision));
}
// One board-local nonmodal reader. It never reads or writes editor/recovery state.
export class CardDetails {
    client;
    fallback;
    host = node("section");
    selected;
    expected;
    request = 0;
    active = true;
    current;
    history;
    opener;
    heading = node("h2", "Card details");
    status = node("p");
    facts = node("section");
    comments = node("section");
    older = node("section");
    historicalComments = node("section");
    commentPage = 0;
    historyPage = 0;
    historicalCommentPage = 0;
    constructor(client, fallback) {
        this.client = client;
        this.fallback = fallback;
        this.host.hidden = true;
        this.host.setAttribute("aria-label", "Card details");
        this.host.setAttribute("role", "region");
        this.heading.tabIndex = -1;
        this.status.setAttribute("role", "status");
        this.facts.setAttribute("aria-label", "Current card state");
        this.comments.setAttribute("aria-label", "Card comments");
        this.older.setAttribute("aria-label", "Prior card history");
        this.historicalComments.setAttribute("aria-label", "History comments");
        this.host.append(this.heading, control("Close card details", () => this.close()), control("Refresh card details", () => { void this.refresh(); }), this.status, this.facts, this.comments, this.older, this.historicalComments);
    }
    open(cardId, identity, opener) {
        this.request++;
        this.selected = cardId;
        this.expected = identity;
        this.opener = opener;
        this.current = undefined;
        this.history = undefined;
        this.host.hidden = false;
        this.heading.textContent = "Card details · " + cardId;
        this.facts.replaceChildren();
        this.comments.replaceChildren();
        this.older.replaceChildren();
        this.historicalComments.replaceChildren();
        this.heading.focus();
        void this.refresh();
    }
    close() {
        this.request++;
        this.selected = undefined;
        this.host.hidden = true;
        const replacement = [...this.fallback.parentElement.querySelectorAll("button[data-open-card]")]
            .find(row => row.dataset.openCard === this.opener?.dataset.openCard && !row.disabled && row.getClientRects().length > 0);
        (replacement ?? this.fallback).focus();
    }
    suspend() { this.active = false; this.invalidate(); }
    resume() { this.active = true; }
    invalidate() {
        this.request++;
        this.history = undefined;
        this.historicalComments.replaceChildren(node("p", "History comments stale; a deliberate refresh is required."));
        if (this.selected) {
            this.status.textContent = "Card details stale. Deliberately refresh card details to read current facts.";
            this.older.replaceChildren(node("p", "History stale; a deliberate refresh is required."));
        }
    }
    boardRead(identity) {
        if (!this.selected)
            return;
        if (!this.expected || !same(identity, this.expected)) {
            this.close();
            return;
        }
        void this.refresh();
    }
    async refresh() {
        if (!this.active || !this.selected || !this.expected)
            return;
        const generation = ++this.request, cardId = this.selected, identity = this.expected;
        const valid = () => this.active && generation === this.request && this.selected === cardId;
        this.commentPage = 0;
        this.historyPage = 0;
        this.historicalCommentPage = 0;
        this.history = undefined;
        this.status.textContent = "Reading current card and separate history snapshots…";
        this.older.replaceChildren(node("p", "History unavailable while reading."));
        this.historicalComments.replaceChildren(node("p", "History comments unavailable while reading."));
        const currentPromise = this.client.card(identity.boardId, cardId);
        const historyPromise = this.client.history(identity.boardId, cardId);
        const [current, history] = await Promise.allSettled([currentPromise, historyPromise]);
        if (!valid())
            return;
        this.historicalComments.replaceChildren(node("p", "History comments unavailable without matching current and history snapshots."));
        if (current.status !== "fulfilled" || !same(current.value.result.identity, identity) || current.value.result.card.cardId !== cardId) {
            this.status.textContent = this.current ? "Current card unavailable; displayed confirmed facts are stale. Retry deliberately." : "Current card unavailable. Retry deliberately.";
            this.older.replaceChildren(node("p", "History unavailable without a matching current card read."));
            return;
        }
        this.current = current.value;
        this.renderCurrent();
        if (history.status !== "fulfilled") {
            this.status.textContent = "Current card read. History unavailable; retry deliberately.";
            return;
        }
        const h = history.value.result, c = current.value.result;
        if (!same(h.identity, c.identity) || h.cardId !== cardId || h.revision !== c.revision || h.cardRevision !== c.card.revision) {
            this.status.textContent = "Current card read. History stale: separate snapshot identities or revisions disagree. Retry deliberately.";
            this.older.replaceChildren(node("p", "History stale; no rows combined with current state."));
            return;
        }
        this.history = history.value;
        this.renderHistory();
        this.renderHistoricalComments();
        this.status.textContent = "Refreshed returned coverage: " + c.comments.length + " comments and " +
            priorHistory(h.records, h.identity, cardId, h.cardRevision).length + " prior history rows and " + h.comments.length + " history comments. Pages reset to 1. Board revision " + c.revision + ", card revision " + c.card.revision + ".";
    }
    page(host, rows, page, label, change, render) {
        const start = page * 20, stop = Math.min(rows.length, start + 20), list = node("div");
        host.replaceChildren(node("h3", label), node("p", rows.length ? label + " " + (start + 1) + "–" + stop + " of " + rows.length + " returned" : "No " + label.toLowerCase() + " returned."));
        const move = (value, label) => {
            change(value);
            const replacement = [...host.querySelectorAll("button")].find(row => row.textContent === label && !row.disabled);
            (replacement ?? [...host.querySelectorAll("button")].find(row => !row.disabled))?.focus();
        };
        const previous = control("Previous " + label.toLowerCase(), () => move(page - 1, "Previous " + label.toLowerCase()));
        previous.disabled = page === 0;
        const next = control("Next " + label.toLowerCase(), () => move(page + 1, "Next " + label.toLowerCase()));
        next.disabled = stop >= rows.length;
        host.append(previous, next, list);
        for (const row of rows.slice(start, stop))
            list.append(render(row));
    }
    renderCurrent() {
        const c = this.current.result, card = c.card;
        this.heading.textContent = "Card details · #" + card.ticket + " " + card.title;
        this.facts.replaceChildren(node("h3", "Current card state"), node("p", "Card UUID: " + card.cardId), node("p", "Origin: " + origin(c.identity) + " · created in " + origin(card.createdIn)), node("p", "Title: " + card.title), node("p", card.description === "" ? "Description: empty" : "Description:\n" + card.description), node("p", "Lane: " + card.lane + " · Priority: " + card.priority + " · Owner: " + (card.ownerId ? attribution(card.ownerId, c.actors) : "Unassigned")), node("p", "Board revision " + c.revision + " · card revision " + card.revision + " · archived: " + card.archived + " · approval: " + (card.currentApprovalId ?? "none")));
        const links = c.links.filter(row => row.cardId === card.cardId);
        this.facts.append(node("h3", "Existing relationships"));
        if (!links.length)
            this.facts.append(node("p", "No relationships returned."));
        for (const link of links)
            this.facts.append(node("p", link.kind + " → " + origin(link.target) + "/" + link.target.cardId + " · " + (link.active ? "active" : "inactive") + " · " + link.linkId));
        coverage(this.facts, c.coverage);
        this.renderComments();
    }
    renderComments() {
        const c = this.current.result;
        this.page(this.comments, sortedComments(c.comments), this.commentPage, "Comments", page => { this.commentPage = page; this.renderComments(); }, row => {
            const article = node("article");
            article.append(node("p", attribution(row.actorId, c.actors) + " · " + row.createdAt + " · " + row.commentId + " · " + origin(row.createdIn)), node("p", row.text));
            return article;
        });
    }
    renderHistory() {
        const h = this.history.result;
        this.page(this.older, priorHistory(h.records, h.identity, h.cardId, h.cardRevision), this.historyPage, "Prior history", page => { this.historyPage = page; this.renderHistory(); }, row => {
            const article = node("article");
            article.append(node("h4", "Origin " + origin(row.origin) + " · card revision " + row.cardRevision), node("p", attribution(row.actorId, h.actors) + " · " + row.committedAt));
            if (row.state)
                article.append(node("p", "#" + row.state.ticket + " " + row.state.title), node("p", "Description:\n" + row.state.description), node("p", row.state.lane + " · " + row.state.priority + " · Owner: " + (row.state.ownerId ? attribution(row.state.ownerId, h.actors) : "Unassigned")), node("p", "Archived: " + row.state.archived + " · approval: " + (row.state.currentApprovalId ?? "none")));
            else
                article.append(node("p", "Old text unavailable; compact or removed state."));
            if (row.removal)
                article.append(node("p", "State removed: " + row.removal.reason + " · " + row.removal.removedAt + " · board revision " + row.removal.boardRevision));
            const commentReferences = row.references.commentIds.map(id => {
                const comment = h.comments.find(value => value.commentId === id);
                return comment ? id + " (" + origin(comment.createdIn) + ")" : id + " (old text unavailable)";
            });
            article.append(node("p", "Comment references: " + commentReferences.length + (commentReferences.length ? " · " + commentReferences.join(", ") + ". Read History comments for returned facts." : ".")));
            for (const id of row.references.linkIds) {
                const link = h.links.find(value => value.linkId === id);
                article.append(node("p", link ? "Relationship " + id + " · " + link.kind + " → " + origin(link.target) + "/" + link.target.cardId + " · " + (link.active ? "active" : "inactive") : "Relationship " + id + ": unavailable."));
            }
            for (const id of row.references.attachmentIds)
                article.append(node("p", "Attachment reference " + id));
            for (const id of row.references.approvalIds)
                article.append(node("p", "Approval reference " + id));
            return article;
        });
        coverage(this.older, h.coverage);
        for (const row of h.tombstones)
            this.older.append(node("p", "Removed " + row.kind + " " + row.id + " · " + origin(row.createdIn) + " · " + row.reason + " · " + row.removedAt +
                " · actor: " + (row.actorId ? attribution(row.actorId, h.actors) : "unavailable") + " · created: " + (row.createdAt ?? "unavailable") + ". Old text unavailable."));
    }
    renderHistoricalComments() {
        const h = this.history.result;
        this.page(this.historicalComments, sortedComments(h.comments), this.historicalCommentPage, "History comments", page => {
            this.historicalCommentPage = page;
            this.renderHistoricalComments();
        }, row => {
            const article = node("article");
            article.append(node("p", attribution(row.actorId, h.actors) + " · " + row.createdAt + " · " + row.commentId + " · " + origin(row.createdIn)), node("p", row.text));
            return article;
        });
    }
}
