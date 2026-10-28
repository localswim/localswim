import { button, element } from "../recovery-ui/view.js";
import { SessionWorkspace } from "./workspace.js";
// A reconnect changes only the live connection. Editors and immutable originals
// stay mounted; no board mutation or original lookup runs from this control.
export class LocalSessionView {
    root;
    access;
    workspace;
    unsubscribe;
    destroyed = false;
    busy = false;
    beforeUnload = (event) => { if (!this.canLeave) {
        event.preventDefault();
        event.returnValue = "";
    } };
    constructor(root, access, options = {}) {
        this.root = root;
        this.access = access;
        const content = element("section"), status = element("p"), reconnect = button("Reconnect local service", () => { void connect(); });
        status.setAttribute("role", "status");
        const ready = () => {
            status.textContent = access.recoveryReady
                ? "Connected to local board. Private recovery is ready."
                : "Connected for reading. Private recovery storage is unavailable; saves are refused before dispatch and input is retained.";
        };
        const connect = async () => {
            if (this.destroyed || this.busy)
                return;
            this.busy = true;
            reconnect.disabled = true;
            try {
                await access.reconnect();
                if (!this.destroyed)
                    ready();
            }
            catch {
                if (!this.destroyed)
                    status.textContent = "Local connection unavailable or identity changed. Input and original outcomes retained. Restart the service, then reconnect; no save is resubmitted.";
            }
            finally {
                this.busy = false;
                if (!this.destroyed)
                    reconnect.disabled = false;
            }
        };
        root.replaceChildren(element("h1", "Localswim"), reconnect, status, content);
        this.workspace = new SessionWorkspace(content, access.client, access.initial, { local: true, ...options });
        this.unsubscribe = access.onUnavailable(() => {
            if (!this.destroyed)
                status.textContent = "Local connection unavailable. Input and private originals retained; reconnect to continue. Reconnect never resubmits a save.";
        });
        ready();
        window.addEventListener("beforeunload", this.beforeUnload);
    }
    suspend() { this.workspace.suspend(); }
    resume() { this.workspace.resume(); }
    activate() { this.workspace.activate(); }
    get ready() { return this.workspace.ready; }
    get canLeave() { return this.workspace.canLeave; }
    flushPrivateInput() { return this.workspace.flushPrivateInput(); }
    discardInput() { this.workspace.discardInput(); }
    destroy() {
        if (this.destroyed)
            return;
        this.destroyed = true;
        this.unsubscribe();
        this.workspace.destroy();
        this.root.replaceChildren();
        window.removeEventListener("beforeunload", this.beforeUnload);
    }
}
