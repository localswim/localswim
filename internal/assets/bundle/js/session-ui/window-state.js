const storageKey = "localswim.window.v1", uuid = /^[0-9a-f-]{36}$/;
// This bounded index contains references, never draft prose, credentials or sessions.
// A newly navigated browsing context gets a fresh identity even when its opener's
// sessionStorage was cloned. Reload/back-forward retain this window's references.
export class WindowState {
    id;
    boards = {};
    storage;
    layout = { collapsed: false, width: 280 };
    constructor(storage, navigationType = performance.getEntriesByType("navigation")[0] && performance.getEntriesByType("navigation")[0].type) {
        try {
            this.storage = storage ?? globalThis.sessionStorage;
        }
        catch {
            this.storage = undefined;
        }
        let retained;
        try {
            const text = this.storage?.getItem(storageKey);
            if (text && text.length <= 65536 && (navigationType === "reload" || navigationType === "back_forward"))
                retained = JSON.parse(text);
        }
        catch { /* Memory remains usable when window-local storage refuses. */ }
        if (retained?.layout && typeof retained.layout.collapsed === "boolean" && typeof retained.layout.width === "number" && Number.isFinite(retained.layout.width)) {
            this.layout = { collapsed: retained.layout.collapsed, width: Math.min(420, Math.max(220, Math.round(retained.layout.width))) };
        }
        this.id = typeof retained?.id === "string" && uuid.test(retained.id) ? retained.id : crypto.randomUUID();
        if (retained?.boards && typeof retained.boards === "object" && !Array.isArray(retained.boards)) {
            for (const [key, value] of Object.entries(retained.boards).slice(0, 256)) {
                if (key.length <= 256 && value && typeof value === "object") {
                    const state = value;
                    const validReference = (reference) => !reference || !!reference.context && uuid.test(reference.draftId) && [reference.context.installationId, reference.context.boardId, reference.context.incarnation, reference.context.actorId].every(v => typeof v === "string" && uuid.test(v));
                    if (!validReference(state.editor) || !validReference(state.recovery))
                        continue;
                    this.boards[key] = { ...(state.editor ? { editor: state.editor } : {}), ...(state.recovery ? { recovery: state.recovery } : {}),
                        scrollX: Number.isFinite(state.scrollX) ? state.scrollX : 0, scrollY: Number.isFinite(state.scrollY) ? state.scrollY : 0,
                        ...(Array.isArray(state.focusPath) && state.focusPath.length <= 32 && state.focusPath.every(n => Number.isInteger(n) && n >= 0 && n <= 10000) ? { focusPath: state.focusPath } : {}),
                        ...(Array.isArray(state.selection) && state.selection.length === 2 && state.selection.every(n => n === null || Number.isInteger(n) && n >= 0 && n <= 1000000) ? { selection: state.selection } : {}) };
                }
            }
        }
        try {
            this.storage?.setItem(storageKey, JSON.stringify({ id: this.id, boards: this.boards, layout: this.layout }));
        }
        catch { /* Memory identity remains valid for this lifetime. */ }
    }
    get sidebarLayout() { return { ...this.layout }; }
    setSidebarLayout(layout) {
        if (typeof layout.collapsed !== "boolean" || !Number.isFinite(layout.width))
            return;
        this.layout = { collapsed: layout.collapsed, width: Math.min(420, Math.max(220, Math.round(layout.width))) };
        // Layout is advisory UI state. A storage failure never disables the shell or
        // claims that private editor references have been durably saved.
        try {
            this.persist();
        }
        catch { /* Keep the layout for this window lifetime. */ }
    }
    persist() {
        const text = JSON.stringify({ id: this.id, boards: this.boards, layout: this.layout });
        if (!this.storage || text.length > 65536 || Object.keys(this.boards).length > 256)
            throw Error("Window recovery index unavailable");
        this.storage.setItem(storageKey, text);
        if (this.storage.getItem(storageKey) !== text)
            throw Error("Window recovery index unverified");
    }
    get(key) { return structuredClone(this.boards[key] ?? {}); }
    put(key, state) {
        this.boards[key] = structuredClone(state);
        this.persist();
    }
}
