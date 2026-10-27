import { recoveryCoordinator } from "../recovery-ui/coordination.js";
import { dependencyAdvisory } from "./advisory.js";
function element(tag, text = "") {
    const node = document.createElement(tag);
    node.textContent = text;
    return node;
}
function button(text, run) {
    const node = element("button", text);
    node.type = "button";
    node.addEventListener("click", run);
    return node;
}
// DOM-only view. Every prose-bearing field is rendered as inert text.
export class BoardView {
    root;
    client;
    boardId;
    board;
    session;
    editor;
    stale = true;
    heading = element("h1", "Board unavailable");
    readState = element("p", "Unavailable; no current board loaded.");
    actor = element("p");
    cards = element("section");
    editorHost = element("section");
    current = element("section");
    outcomes = element("section");
    records = new Map();
    outcomeNodes = new Map();
    destroyed = false;
    generation = 0;
    persistence;
    stopPersistence;
    constructor(root, client, boardId) {
        this.root = root;
        this.client = client;
        this.boardId = boardId;
        this.persistence = recoveryCoordinator(client);
        this.stopPersistence = this.persistence.subscribe(() => {
            if (this.editor) {
                this.editor.sequence++;
                this.editor.recovery.textContent = "Private discard pending; visible input retained. Later deliberate edits are saved after the boundary.";
            }
        });
        this.heading.tabIndex = -1;
        this.readState.setAttribute("role", "status");
        this.outcomes.setAttribute("aria-label", "Original operation outcomes");
        this.current.setAttribute("aria-label", "Current confirmed text");
        this.cards.setAttribute("aria-label", "Cards");
        root.replaceChildren(this.heading, this.actor, this.readState, button("Read current board", () => { void this.refresh(); }), button("Create card", () => this.edit("card.create")), this.cards, this.editorHost, this.current, this.outcomes);
    }
    async open() { await this.refresh(); }
    // Owning session adapter invokes on logout/revocation; it preserves private data.
    destroy() { this.destroyed = true; this.stopPersistence(); this.generation++; this.editor = undefined; this.board = undefined; this.session = undefined; this.root.replaceChildren(); }
    async refresh() {
        const generation = ++this.generation;
        try {
            const session = await this.client.session(), board = await this.client.board(this.boardId);
            this.client.context(board, session);
            if (this.destroyed || generation !== this.generation)
                return;
            this.session = session;
            this.board = board;
            this.stale = false;
            this.heading.textContent = "Board " + board.result.identity.boardId;
            this.actor.textContent = "Actor " + session.actorId + " (" + session.kind + ")";
            this.readState.textContent = "Current at board revision " + board.result.revision + "; incarnation " + board.result.identity.incarnation;
            this.renderCards();
        }
        catch {
            if (this.destroyed || generation !== this.generation)
                return;
            this.stale = true;
            this.readState.textContent = this.board ? "Unavailable; displayed board is stale. Editor input and original base retained." : "Unavailable; no current board loaded.";
        }
        this.updateSave();
    }
    renderCards() {
        const focused = document.activeElement?.dataset.focusKey;
        this.cards.replaceChildren();
        for (const card of this.board.result.cards) {
            const article = element("article");
            article.append(element("h2", "#" + card.ticket + " " + card.title), element("p", card.cardId), element("p", card.description), element("p", "Assignee: " + this.assignee(card.ownerId, this.board.result.actors)), element("p", card.priority + " · " + card.lane + " · card revision " + card.revision));
            for (const comment of this.board.result.comments.filter(c => c.cardId === card.cardId))
                article.append(element("p", comment.actorId + ": " + comment.text));
            for (const link of this.board.result.links.filter(l => l.cardId === card.cardId && l.active)) {
                article.append(element("p", link.kind + " → " + link.target.cardId + " · " + link.linkId));
            }
            const advisory = dependencyAdvisory(this.board, card);
            const pressure = element("section"), prerequisites = element("section");
            pressure.setAttribute("aria-label", "Dependency pressure");
            prerequisites.setAttribute("aria-label", "Prerequisites/readiness");
            for (const fact of advisory.pressure)
                pressure.append(element("p", fact));
            for (const fact of advisory.prerequisites)
                prerequisites.append(element("p", fact));
            prerequisites.append(element("p", advisory.summary));
            article.append(pressure, prerequisites);
            for (const [label, command] of [["Edit card", "card.edit"], ["Add comment", "comment.add"], ["Add link", "link.add"], ["Remove link", "link.remove"], ["Start work", "card.edit"], ["Review handoff", "review-handoff"], ["Approve", "approve"]]) {
                const control = button(label, () => this.edit(command, card, label === "Start work"));
                control.dataset.focusKey = card.cardId + ":" + label;
                control.disabled = card.lane === "Completed" || command === "approve" && (this.session.kind !== "human" || !this.session.grant.rights.includes("approve"));
                article.append(control);
            }
            this.cards.append(article);
        }
        // Refresh only restores focus when the old card control was removed.
        if (focused) {
            const replacement = [...this.cards.querySelectorAll("button")].find(n => n.dataset.focusKey === focused);
            (replacement ?? this.heading).focus();
        }
    }
    assignee(id, actors) {
        if (!id)
            return "Unassigned";
        const actor = actors.find(a => a.actorId === id);
        return actor ? actor.label + " (" + id + ")" : id;
    }
    blocked() { return [...this.records.values()].some(r => r.state === "pending" || r.state === "unknown"); }
    updateSave() {
        if (this.editor)
            this.editor.save.disabled = this.stale || this.editor.submitted || this.editor.composing || this.blocked();
    }
    edit(command, card, start = false) {
        if (this.destroyed || !this.board || !this.session || this.stale) {
            this.readState.textContent = "Read a current board before editing.";
            return;
        }
        const base = this.board, context = this.client.context(base, this.session), form = element("form");
        form.addEventListener("submit", event => event.preventDefault());
        const recovery = element("p", "Recovery pending");
        recovery.setAttribute("role", "status");
        const editor = { id: crypto.randomUUID(), revision: 0, command, context, base, card,
            fields: {}, form, recovery, save: button("Save", () => { void this.save(editor); }), composing: false,
            sequence: 0, submitted: false };
        const title = element("h2", command + " · original board revision " + base.result.revision + (card ? ", card revision " + card.revision : ""));
        form.append(title, element("p", "Board " + context.boardId + " · actor " + context.actorId));
        const field = (name, value, choices) => {
            const label = element("label", name + " ");
            const control = choices ? element("select") : name === "description" || name === "text" ? element("textarea") : element("input");
            if (control instanceof HTMLSelectElement)
                for (const choice of choices) {
                    const option = element("option", choice);
                    option.value = choice;
                    control.append(option);
                }
            control.name = name;
            control.setAttribute("aria-label", name === "ownerId" ? "Assignee" : name);
            control.value = value;
            editor.fields[name] = control;
            control.addEventListener("input", () => this.persist(editor));
            control.addEventListener("change", () => this.persist(editor));
            label.append(control);
            form.append(label);
        };
        if (start)
            field("lane", "In Progress", ["In Progress"]);
        else if (command === "card.create" || command === "card.edit") {
            field("title", card?.title ?? "");
            field("description", card?.description ?? "");
            field("priority", card?.priority ?? "P3", ["P0", "P1", "P2", "P3", "P4", "P5"]);
            field("ownerId", card?.ownerId ?? "", ["", ...base.result.actors.map(a => a.actorId)]);
            const assignee = editor.fields["ownerId"];
            assignee.parentElement.firstChild.textContent = "Assignee ";
            for (const option of assignee.options)
                option.textContent = this.assignee(option.value, base.result.actors);
        }
        else if (command === "comment.add")
            field("text", "");
        else if (command === "link.add") {
            field("kind", "blocking", ["blocking", "informational"]);
            field("targetCardId", "");
        }
        else if (command === "link.remove")
            field("linkId", "");
        else if (command === "approve")
            field("policyVersion", String(base.result.policyVersion), [String(base.result.policyVersion)]);
        else
            form.append(element("p", "Explicit Save submits this review handoff."));
        form.addEventListener("compositionstart", () => { editor.composing = true; this.updateSave(); });
        form.addEventListener("compositionend", () => { editor.composing = false; this.persist(editor); this.updateSave(); });
        form.addEventListener("keydown", event => { if (event.isComposing && event.key === "Enter")
            event.preventDefault(); });
        form.append(recovery, editor.save, button("Read current text", () => { void this.currentText(editor); }));
        this.editor = editor;
        this.editorHost.replaceChildren(form);
        this.current.replaceChildren();
        this.persist(editor);
        this.updateSave();
        (Object.values(editor.fields)[0] ?? editor.save).focus();
    }
    input(editor) {
        const input = {};
        for (const [name, field] of Object.entries(editor.fields))
            input[name] = name === "policyVersion" ? Number(field.value) : name === "ownerId" && field.value === "" ? null : field.value;
        return input;
    }
    persist(editor) {
        const sequence = ++editor.sequence, generation = this.persistence.generation;
        // Prose and original non-secret base only; no credential/session DTO enters storage.
        const input = JSON.stringify({ command: editor.command, target: editor.card ? { cardId: editor.card.cardId } : null,
            expectedRevision: editor.base.result.revision, expectedCardRevision: editor.card?.revision ?? null, input: this.input(editor) });
        editor.recovery.textContent = "Recovery pending";
        void this.persistence.persist(async () => {
            try {
                const result = await this.client.recovery.saveDraft({ context: editor.context, draftId: editor.id, expectedRevision: editor.revision, input });
                editor.id = result.draft.draftId;
                editor.revision = result.draft.revision;
                if (!this.destroyed && generation === this.persistence.generation && sequence === editor.sequence)
                    editor.recovery.textContent = result.state === "forked" ? "Recovery saved as a separate draft after a conflict." : "Recovery saved privately.";
            }
            catch {
                if (!this.destroyed && generation === this.persistence.generation && sequence === editor.sequence)
                    editor.recovery.textContent = "Recovery failed; input remains here. Dispatch requires verified recovery storage.";
            }
        });
    }
    async save(editor) {
        if (this.editor !== editor || editor.composing || editor.submitted || this.stale || this.blocked())
            return;
        editor.submitted = true;
        this.updateSave();
        // Capture synchronous immutable input before storage, session or network awaits.
        const request = JSON.parse(JSON.stringify({ opId: crypto.randomUUID(), incarnation: editor.context.incarnation,
            target: editor.card ? { cardId: editor.card.cardId } : null, expectedRevision: editor.base.result.revision,
            expectedCardRevision: editor.card?.revision ?? null, input: this.input(editor) }));
        const announce = (outcome) => {
            this.records.set(outcome.opId, outcome);
            if (this.destroyed)
                return;
            this.showOutcome(outcome, editor.context);
            if (outcome.state === "unsent")
                editor.submitted = false;
            this.reflectCommitted(outcome);
            if (outcome.conflict || outcome.state === "not_committed")
                void this.currentText(editor);
            this.updateSave();
        };
        await this.persistence.admit(async (evidenceReady) => {
            if (this.destroyed)
                return;
            return this.client.submit({ command: editor.command, request, draftId: editor.id, context: editor.context }, outcome => {
                if (outcome.state === "pending" || outcome.state === "unsent")
                    evidenceReady();
                announce(outcome);
            });
        });
    }
    // Compose other views' shared-client outcomes without replacing an editor,
    // refreshing its base or taking focus. This is presentation, not proof.
    observeOutcome(outcome) {
        if (this.destroyed)
            return;
        this.reflectCommitted(outcome);
        this.updateSave();
    }
    reflectCommitted(outcome) {
        if (outcome.state !== "committed")
            return;
        // A receipt proves the original mutation, never the current board text.
        // An authenticated same-incarnation read at/after its revision already
        // covers that commit. Otherwise require another explicit current read.
        const identity = this.board?.result.identity, original = outcome.evidence?.context, terminal = outcome.terminal;
        if (identity && original && terminal?.outcome === "committed" && identity.boardId === original.boardId &&
            identity.incarnation === original.incarnation && this.board.result.revision >= terminal.revision)
            return;
        this.stale = true;
        this.readState.textContent = "Confirmed saved. Displayed board is stale; read the current board before new work.";
    }
    showOutcome(outcome, context) {
        let row = this.outcomeNodes.get(outcome.opId);
        if (!row) {
            row = element("article");
            this.outcomeNodes.set(outcome.opId, row);
            this.outcomes.append(row);
        }
        const status = element("p", outcome.opId + ": " + outcome.message);
        status.setAttribute("role", "status");
        row.replaceChildren(status);
        if (outcome.state === "pending")
            row.append(button("Stop waiting", () => {
                const unknown = { ...outcome, state: "unknown", message: "Stopped waiting. Original outcome unknown; this does not cancel a possible commit." };
                this.records.set(outcome.opId, unknown);
                this.showOutcome(unknown, context);
                this.updateSave();
            }));
        if (outcome.state === "unknown")
            row.append(button("Look up original outcome", () => {
                void this.client.lookup(context, outcome.opId).then(result => {
                    this.records.set(result.opId, result);
                    if (this.destroyed)
                        return;
                    this.reflectCommitted(result);
                    this.showOutcome(result, context);
                    this.updateSave();
                });
            }));
        // Announcements never focus, replace editor input, refresh bases or issue retries.
    }
    async currentText(editor) {
        const host = this.current;
        if (!editor.card) {
            host.replaceChildren(element("p", "Read the current board to compare cards."));
            return;
        }
        try {
            const read = await this.client.card(editor.context.boardId, editor.card.cardId);
            if (this.destroyed || this.editor !== editor)
                return;
            host.replaceChildren(element("h2", "Current confirmed text · board revision " + read.result.revision), element("p", read.result.card.title), element("p", read.result.card.description), element("p", "Assignee: " + this.assignee(read.result.card.ownerId, read.result.actors)));
            for (const comment of read.result.comments)
                host.append(element("p", comment.actorId + ": " + comment.text));
            host.append(element("p", "Your editor retains its original base. Read the current board, then deliberately open a new editor to reconcile."));
        }
        catch {
            if (!this.destroyed && this.editor === editor)
                host.replaceChildren(element("p", "Current text unavailable; editor input retained."));
        }
    }
}
