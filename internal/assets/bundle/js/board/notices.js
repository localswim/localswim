import { NoticeRefusal } from "../api/notices.js";
import { button, element } from "../recovery-ui/view.js";
const phases = {
    unconfigured: "Unconfigured", stopped: "Stopped", running: "Running", draining: "Draining",
    "partial-drain": "Partial drain", guarded: "Guarded", failed: "Failed",
};
function phase(view) {
    const suffix = view.reason ? " · " + view.reason : "";
    if (view.state !== "guarded")
        return phases[view.state] + suffix;
    if (view.reason === "unavailable")
        return "Guarded · unavailable";
    return "Guarded · outcome UNKNOWN" + (view.reason === "unknown" ? "" : suffix);
}
const classes = {
    human: "Human", mixed: "Mixed", "unknown-actor": "Unknown actor", "other-actor": "Other actor",
    "observed-empty": "Observed empty", expired: "Expired", gapped: "Gapped",
    unavailable: "Unavailable", suppressed: "Suppressed",
};
const cut = (row) => "target " + row.targetId + " · " + classes[row.class] + " · lower event " + row.lowerEvent + " · upper event " + (row.upperEvent ?? "unavailable") +
    " · event count " + (row.eventCount ?? "unavailable") + (row.deliveryId === null ? "" : " · delivery " + row.deliveryId);
const receipt = (row) => "accepted " + row.acceptedAt + " · " + cut(row);
const targetPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const deliveryPattern = /^[0-9a-f]{64}$/;
export class BoardNotices {
    client;
    host = element("div");
    panel = element("section");
    open = button("Notices", () => {
        if (this.open.disabled || !this.identity || !this.panel.hidden)
            return;
        this.panel.hidden = false;
        this.open.setAttribute("aria-expanded", "true");
        this.selection.textContent = "Board " + this.identity.boardId + " · incarnation " + this.identity.incarnation;
        this.message.textContent = "Choose Read status before using a control.";
    });
    selection = element("p");
    message = element("p");
    status = element("div");
    originalOutcome = element("p");
    inbox = element("div");
    lookupResult = element("div");
    target = element("input");
    delivery = element("input");
    futureTarget = element("input");
    retarget = button("Retarget", () => { void this.retargetFuture(); });
    epoch = 0;
    drafts = new Map();
    controls = {
        configure: button("Configure", () => { void this.control("configure"); }),
        start: button("Start", () => { void this.control("start"); }),
        stop: button("Stop", () => { void this.control("stop"); }),
        reload: button("Reload", () => { void this.control("reload"); }),
        recover: button("Recover", () => { void this.control("recover"); }),
    };
    readStatus = button("Read status", () => { void this.run({ action: "status" }); });
    readInbox = button("Read inbox", () => { void this.run({ action: "inbox" }); });
    lookup = button("Look up receipt", () => { void this.lookupReceipt(); });
    identity;
    selectionPending = false;
    suspended = false;
    request;
    displayed;
    constructor(client) {
        this.client = client;
        this.open.disabled = true;
        this.open.setAttribute("aria-expanded", "false");
        this.panel.hidden = true;
        this.panel.className = "board-notices";
        this.panel.setAttribute("aria-label", "Notices");
        this.panel.addEventListener("keydown", event => {
            if (event.key === "Escape") {
                event.preventDefault();
                this.close();
            }
        });
        this.message.setAttribute("role", "status");
        const toolbar = element("div");
        toolbar.className = "notice-controls";
        toolbar.append(this.readStatus, ...Object.values(this.controls), this.readInbox);
        const future = element("fieldset");
        future.className = "notice-lookup";
        const futureLabel = element("label", "Future target ID");
        this.futureTarget.maxLength = 36;
        this.futureTarget.autocomplete = "off";
        this.futureTarget.spellcheck = false;
        this.futureTarget.addEventListener("input", () => this.updateControls());
        futureLabel.append(this.futureTarget);
        future.append(element("legend", "Change future destination"), element("p", "A target is a partition of this board’s local inbox. Retarget changes future observations; original pending work keeps its target. External delivery is not configured here."), futureLabel, this.retarget);
        const fields = element("fieldset");
        fields.className = "notice-lookup";
        const targetLabel = element("label", "Target ID"), deliveryLabel = element("label", "Delivery ID");
        this.target.maxLength = 36;
        this.delivery.maxLength = 64;
        for (const field of [this.target, this.delivery]) {
            field.autocomplete = "off";
            field.spellcheck = false;
            field.addEventListener("input", () => this.updateControls());
        }
        targetLabel.append(this.target);
        deliveryLabel.append(this.delivery);
        fields.append(element("legend", "Exact receipt lookup"), targetLabel, deliveryLabel, this.lookup, this.lookupResult);
        this.panel.append(element("h2", "Notices"), this.selection, this.message, this.originalOutcome, toolbar, this.status, this.inbox, future, fields, button("Close notices", () => this.close()));
        this.host.append(this.open, this.panel);
        this.updateControls();
    }
    boardRead(identity) {
        if (this.identity && (this.identity.boardId !== identity.boardId || this.identity.incarnation !== identity.incarnation))
            this.reset();
        this.identity = { ...identity };
        const saved = this.drafts.get(this.draftKey(identity));
        if (saved)
            [this.futureTarget.value, this.target.value, this.delivery.value] = saved;
        this.updateOpener();
    }
    reset() {
        this.saveDraft();
        this.close();
        this.identity = undefined;
        this.futureTarget.value = this.target.value = this.delivery.value = "";
        this.open.disabled = true;
    }
    selectionChanging(pending) {
        this.selectionPending = pending;
        if (pending)
            this.close();
        this.updateOpener();
    }
    suspend() { this.suspended = true; this.close(); this.updateOpener(); }
    resume() { this.suspended = false; this.updateOpener(); }
    updateOpener() { this.open.disabled = this.selectionPending || this.suspended || !this.identity; }
    draftKey(identity) { return identity.boardId + "/" + identity.incarnation; }
    saveDraft() {
        if (this.identity)
            this.drafts.set(this.draftKey(this.identity), [this.futureTarget.value, this.target.value, this.delivery.value]);
    }
    close() {
        this.epoch++;
        this.saveDraft();
        const restoreFocus = this.panel.contains(document.activeElement);
        this.request?.abort();
        this.request = undefined;
        this.displayed = undefined;
        this.panel.hidden = true;
        this.open.setAttribute("aria-expanded", "false");
        this.selection.textContent = this.message.textContent = this.originalOutcome.textContent = "";
        this.status.replaceChildren();
        this.inbox.replaceChildren();
        this.lookupResult.replaceChildren();
        this.updateControls();
        if (restoreFocus)
            this.open.focus({ preventScroll: true });
    }
    updateControls() {
        const busy = this.request !== undefined;
        for (const [action, control] of Object.entries(this.controls)) {
            control.setAttribute("aria-disabled", String(busy || !this.displayed || action === "recover" && !this.canRecover()));
        }
        this.readStatus.setAttribute("aria-disabled", String(busy));
        this.readInbox.setAttribute("aria-disabled", String(busy));
        this.retarget.setAttribute("aria-disabled", String(busy || !this.displayed?.targetId || !targetPattern.test(this.futureTarget.value)));
        this.lookup.setAttribute("aria-disabled", String(busy || !this.displayed || !targetPattern.test(this.target.value) || !deliveryPattern.test(this.delivery.value)));
    }
    canRecover() {
        return !!this.displayed?.targetId && (this.displayed.state === "guarded" || this.displayed.state === "failed");
    }
    async control(action) {
        if (!this.displayed || action === "recover" && !this.canRecover())
            return;
        await this.run({ action, expectedControlGeneration: this.displayed.controlGeneration, expectedConfigurationRevision: this.displayed.configurationRevision });
    }
    async retargetFuture() {
        if (!this.displayed?.targetId || !targetPattern.test(this.futureTarget.value))
            return;
        await this.run({ action: "retarget", targetId: this.futureTarget.value, expectedControlGeneration: this.displayed.controlGeneration, expectedConfigurationRevision: this.displayed.configurationRevision });
    }
    async lookupReceipt() {
        const targetId = this.target.value, deliveryId = this.delivery.value;
        if (!this.displayed || !targetPattern.test(targetId) || !deliveryPattern.test(deliveryId))
            return;
        await this.run({ action: "lookup", targetId, deliveryId, expectedControlGeneration: this.displayed.controlGeneration, expectedConfigurationRevision: this.displayed.configurationRevision });
    }
    async run(command) {
        const identity = this.identity;
        if (!identity || this.selectionPending || this.suspended || this.panel.hidden || this.request)
            return;
        const request = new AbortController();
        this.request = request;
        const epoch = ++this.epoch;
        const current = () => this.request === request && this.epoch === epoch && this.identity?.boardId === identity.boardId && this.identity.incarnation === identity.incarnation && !this.selectionPending && !this.suspended && !this.panel.hidden;
        const control = command.action !== "lookup" && "expectedControlGeneration" in command;
        if (command.action !== "inbox")
            this.displayed = undefined;
        this.message.textContent = "Notice request in progress… Showing the last read evidence.";
        this.updateControls();
        try {
            const result = await this.client.notices(identity, command, request.signal);
            if (!current())
                return;
            if (command.action === "status")
                this.displayed = result;
            if (command.action === "status" || control)
                this.showStatus(result);
            if (command.action === "inbox")
                this.showInbox(result);
            if (command.action === "lookup") {
                const match = result.lookup?.receipt;
                if (match)
                    this.showStatus(result);
                this.lookupResult.textContent = match ? "Accepted receipt · " + receipt(match) : "Receipt outcome UNKNOWN.";
            }
            const summary = command.action === "recover" ? this.recoverySummary(result) :
                control || result.reason ? phase(result) + "." : "Notice request complete.";
            this.message.textContent = control || command.action === "lookup" ? summary + " Read status before another control." : summary;
            if (control)
                this.originalOutcome.textContent = "Original " + command.action + " response · " + phase(result) + ".";
        }
        catch (error) {
            if (!current())
                return;
            if (error instanceof NoticeRefusal) {
                this.message.textContent = error.kind === "stale" ? "Control generation or configuration revision is stale. Read status, then choose the control again."
                    : error.kind === "busy" ? "Control refused while the monitor is busy or guarded. Read status."
                        : "Notice request refused.";
            }
            else {
                this.message.textContent = control ? "Control outcome UNKNOWN. The request may have taken effect. Last read evidence retained. Read status before choosing another action."
                    : command.action === "lookup" ? "Receipt outcome UNKNOWN. The request may have taken effect. Last read evidence retained. Read status before another lookup."
                        : "Notice response unavailable.";
            }
            if (control || command.action === "lookup")
                this.originalOutcome.textContent = "Original " + command.action + " response · " + this.message.textContent;
        }
        finally {
            if (this.request === request) {
                this.request = undefined;
                this.updateControls();
            }
        }
    }
    recoverySummary(view) {
        if (view.state === "stopped")
            return "Recovery settled. Monitor stopped.";
        return "Recovery status · " + phase(view) + ".";
    }
    showStatus(view) {
        const { checkpoint, targetId, last, pending, state, gapCount, configurationRevision, controlGeneration, controlOperation } = view;
        this.status.replaceChildren(element("h3", "Monitor status"), element("p", phase(view) + (state === "running" && !last && !pending ? " · awaiting first outcome" : "")), element("p", "Future target " + (targetId ?? "unconfigured") + " · configuration revision " + configurationRevision), element("p", "Control generation " + controlGeneration + (controlOperation ? " · " + controlOperation + " incomplete; read status for eventual state" : "")), element("p", "Checkpoint " + (checkpoint ?? "unavailable") + " · gaps " + gapCount));
        if (last)
            this.status.append(element("p", "Last outcome · " + cut(last)));
        if (pending)
            this.status.append(element("p", "Original pending · " + cut(pending)));
    }
    showInbox(view) {
        this.inbox.replaceChildren(element("h3", "Local notice inbox"));
        if (!view.entries.length) {
            this.inbox.append(element("p", view.reason ? "Inbox unavailable." : "No retained notices."));
            return;
        }
        const list = element("ol");
        for (const entry of view.entries)
            list.append(element("li", receipt(entry)));
        this.inbox.append(list);
    }
}
