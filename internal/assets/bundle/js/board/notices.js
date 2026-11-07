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
const cut = (row) => classes[row.class] + " · lower event " + row.lowerEvent + " · upper event " + (row.upperEvent ?? "unavailable") +
    " · event count " + (row.eventCount ?? "unavailable") + (row.deliveryId === null ? "" : " · delivery " + row.deliveryId);
const receipt = (row) => row.acceptedAt + " · target " + row.targetId + " · " + cut(row);
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
    inbox = element("div");
    lookupResult = element("div");
    target = element("input");
    delivery = element("input");
    controls = {
        configure: button("Configure", () => { void this.control("configure"); }),
        start: button("Start", () => { void this.control("start"); }),
        stop: button("Stop", () => { void this.control("stop"); }),
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
        this.message.setAttribute("role", "status");
        const toolbar = element("div");
        toolbar.className = "notice-controls";
        toolbar.append(this.readStatus, ...Object.values(this.controls), this.readInbox);
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
        this.panel.append(element("h2", "Notices"), this.selection, this.message, toolbar, this.status, this.inbox, fields, button("Close notices", () => this.close()));
        this.host.append(this.open, this.panel);
        this.updateControls();
    }
    boardRead(identity) {
        this.identity = identity;
        this.updateOpener();
    }
    reset() {
        this.close();
        this.identity = undefined;
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
    close() {
        const restoreFocus = this.panel.contains(document.activeElement);
        this.request?.abort();
        this.request = undefined;
        this.displayed = undefined;
        this.panel.hidden = true;
        this.open.setAttribute("aria-expanded", "false");
        this.selection.textContent = this.message.textContent = this.target.value = this.delivery.value = "";
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
        const control = command.action !== "lookup" && "expectedControlGeneration" in command;
        if (command.action !== "inbox") {
            this.displayed = undefined;
            if (command.action !== "recover")
                this.status.replaceChildren();
        }
        if (command.action === "inbox")
            this.inbox.replaceChildren();
        if (command.action === "lookup")
            this.lookupResult.replaceChildren();
        this.message.textContent = command.action === "recover" ? "Recovery in progress… Showing the last read status." : "Notice request in progress…";
        this.updateControls();
        try {
            const result = await this.client.notices(identity, command, request.signal);
            if (this.request !== request)
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
                result.reason ? phase(result) + "." : "Notice request complete.";
            this.message.textContent = control || command.action === "lookup" ? summary + " Read status before another control." : summary;
        }
        catch (error) {
            if (this.request !== request)
                return;
            if (error instanceof NoticeRefusal) {
                this.message.textContent = error.kind === "stale" ? "Control generation is stale. Read status, then choose the control again."
                    : error.kind === "busy" ? "Control refused while the monitor is busy or guarded. Read status."
                        : "Notice request refused.";
            }
            else {
                this.message.textContent = command.action === "recover" ? "Recovery outcome UNKNOWN. Showing the last read status. Read status before choosing another action."
                    : control ? "Control outcome UNKNOWN. Read status before choosing another action."
                        : command.action === "lookup" ? "Receipt outcome UNKNOWN. Lookup response unavailable."
                            : "Notice response unavailable.";
            }
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
        const { checkpoint, targetId, last, pending, state, gapCount } = view;
        this.status.replaceChildren(element("h3", "Monitor status"), element("p", phase(view) + (state === "running" && !last && !pending ? " · awaiting first outcome" : "")), element("p", "Target " + (targetId ?? "unconfigured") + " · checkpoint " + (checkpoint ?? "unavailable") + " · gaps " + gapCount));
        if (last)
            this.status.append(element("p", "Last outcome · " + cut(last)));
        if (pending)
            this.status.append(element("p", "Pending · " + cut(pending)));
    }
    showInbox(view) {
        this.inbox.append(element("h3", "Notice inbox"));
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
