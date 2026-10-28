import { LocalSessionView } from "./local-view.js";
import { WindowState } from "./window-state.js";
import { button, element } from "../recovery-ui/view.js";
const slug = "[a-z0-9]+(?:-[a-z0-9]+)*";
export function boardRoute(path) {
    const match = new RegExp("^/board/(" + slug + ")/(" + slug + ")$").exec(path);
    return match && match[1].length <= 64 && match[2].length <= 64 ? [match[1], match[2]] : undefined;
}
// Owns actual DOM trees and their closures, rather than reconstructing editors
// from a current board read. Detached A callbacks can update only A's own tree.
export class WindowBoardController {
    root;
    connection;
    state;
    views = new Map();
    active;
    generation = 0;
    destroyed = false;
    index = 0;
    reverting = false;
    historyGeneration = 0;
    windowId;
    constructor(root, connection, state = new WindowState()) {
        this.root = root;
        this.connection = connection;
        this.state = state;
        this.windowId = state.id;
        if (history.state?.localswimWindow === this.windowId && Number.isInteger(history.state.index))
            this.index = history.state.index;
        history.replaceState({ localswimWindow: this.windowId, index: this.index }, "", location.href);
        window.addEventListener("beforeunload", this.beforeUnload);
        window.addEventListener("popstate", this.popState);
    }
    get canLeave() { return [...this.views.values()].every(v => v.view.canLeave); }
    async flushPrivateInput() { await Promise.all([...this.views.values()].map(v => v.view.flushPrivateInput())); return this.canLeave; }
    discardActiveInput() { this.active?.view.discardInput(); }
    activate() { if (this.active)
        this.attach(this.active); }
    discardUnpersistedInput() { for (const view of this.views.values())
        if (!view.view.canLeave)
            view.view.discardInput(); }
    blocked() {
        if (!this.root.querySelector('[data-private-navigation="blocked"]')) {
            const notice = element("section");
            notice.dataset.privateNavigation = "blocked";
            notice.setAttribute("role", "alert");
            notice.append(element("p", "Private input in a retained view is pending or could not be saved. Navigation is paused until it is saved or explicitly discarded."), button("Discard unpersisted input in retained views", () => { this.discardUnpersistedInput(); notice.remove(); }));
            this.root.append(notice);
        }
        return false;
    }
    async reconnect() { this.generation++; await this.connection.reconnect?.(); }
    beforeUnload = (event) => {
        if (this.active)
            this.capture(this.active);
        if (!this.canLeave) {
            event.preventDefault();
            event.returnValue = "";
        }
    };
    popState = (event) => {
        if (this.reverting) {
            this.reverting = false;
            return;
        }
        const target = event.state;
        if (!target || target.localswimWindow !== this.windowId || !Number.isInteger(target.index))
            return;
        const previous = this.index;
        const generation = ++this.historyGeneration;
        void this.navigate(location.pathname, false).then(ok => {
            if (generation !== this.historyGeneration)
                return;
            if (ok)
                this.index = target.index;
            else {
                this.reverting = true;
                history.go(previous - target.index);
            }
        });
    };
    async navigate(path, push = true) {
        const route = boardRoute(path);
        if (path !== "/" && !route)
            throw Error("Invalid board route");
        const allowed = route ? await this.select(route[0], route[1]) : await this.showPendingShell();
        if (allowed) {
            if (push && location.pathname !== path)
                history.pushState({ localswimWindow: this.windowId, index: ++this.index }, "", path);
        }
        return allowed;
    }
    bounded(work, deadline, late = () => { }) {
        return new Promise((resolve, reject) => {
            let expired = false;
            const timer = setTimeout(() => { expired = true; reject(Error("Selected connection timed out")); }, Math.max(0, deadline - Date.now()));
            void work.then(value => { clearTimeout(timer); if (expired)
                late(value);
            else
                resolve(value); }, error => { clearTimeout(timer); if (!expired)
                reject(error); });
        });
    }
    capture(active) {
        if (active.root.contains(document.activeElement))
            active.focus = document.activeElement;
        const input = active.focus;
        if (input instanceof HTMLInputElement || input instanceof HTMLTextAreaElement)
            active.selection = [input.selectionStart, input.selectionEnd];
        active.scroll = [window.scrollX, window.scrollY];
        active.offsets = [...active.root.querySelectorAll("*")].filter(n => n.scrollTop || n.scrollLeft).map(n => [n, n.scrollLeft, n.scrollTop]);
        const focusPath = [];
        for (let node = active.focus; node && node !== active.root; node = node.parentElement ?? undefined) {
            if (!node.parentElement)
                break;
            focusPath.unshift([...node.parentElement.children].indexOf(node));
        }
        try {
            this.state.put(active.key, { ...this.state.get(active.key), scrollX: active.scroll[0], scrollY: active.scroll[1],
                ...(active.focus ? { focusPath } : { focusPath: undefined }), selection: active.selection ? [...active.selection] : undefined });
        }
        catch { /* Recovery save itself reports index failure. */ }
    }
    suspend() {
        const active = this.active;
        if (!active)
            return;
        this.capture(active);
        active.view.suspend();
        active.root.remove();
        this.active = undefined;
    }
    attach(view) {
        this.root.replaceChildren(view.root);
        this.active = view;
        view.view.resume();
        if (view.focus && !view.focus.isConnected) {
            let replacement = view.root;
            for (const index of this.state.get(view.key).focusPath ?? [])
                replacement = replacement?.children[index];
            if (replacement instanceof HTMLElement)
                view.focus = replacement;
        }
        if (view.focus?.isConnected) {
            view.focus.focus({ preventScroll: true });
            if (view.selection && (view.focus instanceof HTMLInputElement || view.focus instanceof HTMLTextAreaElement)) {
                try {
                    view.focus.setSelectionRange(...view.selection);
                }
                catch { /* Select controls have no text selection. */ }
            }
        }
        else if (view.root.isConnected)
            view.view.activate();
        for (const [node, x, y] of view.offsets) {
            node.scrollLeft = x;
            node.scrollTop = y;
        }
        window.scrollTo(...view.scroll);
    }
    async select(projectKey, boardKey) {
        if (this.destroyed || !boardRoute("/board/" + projectKey + "/" + boardKey))
            return false;
        const generation = ++this.generation;
        if (!await this.flushPrivateInput())
            return this.blocked();
        if (generation !== this.generation || this.destroyed)
            return false;
        let access;
        const deadline = Date.now() + 20_000;
        try {
            const selected = await this.bounded(this.connection.resolve(projectKey, boardKey), deadline);
            if (generation !== this.generation || this.destroyed)
                return false;
            if (selected.member.availability !== "ready")
                throw Error("Selected board unavailable");
            const identity = selected.member.identity;
            const key = [identity.installationId, identity.boardId, identity.incarnation].join(":");
            let retained = this.views.get(key);
            if (!retained) {
                access = await this.bounded(this.connection.openBoard(selected), deadline, late => { late.clear(); late.client.close(); });
                if (generation !== this.generation || this.destroyed) {
                    access.clear();
                    access.client.close();
                    return false;
                }
                const host = element("section"), previous = this.state.get(key);
                const view = new LocalSessionView(host, access, { ...(previous.editor ? { editor: previous.editor } : {}),
                    ...(previous.recovery ? { recovery: previous.recovery } : {}),
                    checkpoint: reference => this.state.put(key, { ...this.state.get(key), editor: reference }),
                    recoveryCheckpoint: reference => this.state.put(key, { ...this.state.get(key), recovery: reference }) });
                retained = { root: host, view, access, key, readiness: Promise.resolve(), scroll: [previous.scrollX ?? 0, previous.scrollY ?? 0], offsets: [] };
                this.views.set(key, retained);
                access = undefined; // The retained view now owns cleanup, including timeout.
                const candidate = retained;
                host.addEventListener("focusin", event => { if (event.target instanceof HTMLElement)
                    candidate.focus = event.target; });
                candidate.readiness = this.bounded(view.ready, deadline).catch(error => {
                    // Initialization lifetime belongs to the view, not whichever navigation
                    // first requested it. All concurrent selectors see this same failure.
                    if (this.views.get(key) === candidate) {
                        this.views.delete(key);
                        view.destroy();
                        candidate.access.clear();
                        candidate.access.client.close();
                    }
                    throw error;
                });
                void candidate.readiness.catch(() => { });
            }
            await this.bounded(retained.readiness, deadline);
            if (!retained.focus) {
                const previous = this.state.get(key);
                let focus = retained.root;
                for (const index of previous.focusPath ?? [])
                    focus = focus?.children[index];
                if (previous.focusPath && focus instanceof HTMLElement) {
                    retained.focus = focus;
                    retained.selection = previous.selection;
                }
            }
            if (!await this.flushPrivateInput())
                return this.blocked();
            if (generation !== this.generation || this.destroyed)
                return false;
            this.root.querySelector('[data-private-navigation="blocked"]')?.remove();
            if (this.active !== retained) {
                this.suspend();
                this.attach(retained);
            }
            return true;
        }
        catch {
            if (access && ![...this.views.values()].some(v => v.access === access)) {
                access.clear();
                access.client.close();
            }
            if (generation !== this.generation || this.destroyed)
                return false;
            if (!await this.flushPrivateInput())
                return this.blocked();
            this.suspend();
            this.root.replaceChildren(element("h1", "Localswim"), element("p", "Selected board unavailable. Retained private views and original outcomes remain with their source board. Retry deliberately; no board action was submitted."), button("Retry selected board", () => { void this.reconnect().then(() => this.select(projectKey, boardKey)).catch(() => { }); }));
            return true;
        }
    }
    async showPendingShell() {
        const generation = ++this.generation;
        if (!await this.flushPrivateInput())
            return this.blocked();
        if (generation !== this.generation || this.destroyed)
            return false;
        this.suspend();
        this.root.replaceChildren(element("h1", "Localswim workspace"), element("p", "Workspace connected. Board chooser and sidebar are pending M01.07. Open an explicit registered board route to use its private view."));
        return true;
    }
    destroy() {
        this.destroyed = true;
        this.generation++;
        window.removeEventListener("beforeunload", this.beforeUnload);
        window.removeEventListener("popstate", this.popState);
        for (const retained of this.views.values()) {
            retained.view.destroy();
            retained.access.clear();
            retained.access.client.close();
        }
        this.views.clear();
        this.active = undefined;
        this.root.replaceChildren();
    }
}
