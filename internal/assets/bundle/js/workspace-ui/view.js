function node(tag, className = "", text = "") {
    const result = document.createElement(tag);
    result.className = className;
    result.textContent = text;
    return result;
}
export function boardHref(entry) {
    return "/board/" + encodeURIComponent(entry.projectKey) + "/" + encodeURIComponent(entry.boardKey);
}
export function countLabel(count) {
    switch (count.state) {
        case "known": return String(count.value);
        case "stale": return String(count.value) + " · out of date";
        case "not_configured": return "Not configured";
        case "unknown": return "Unknown";
    }
}
export function observationLabel(entry) {
    if (entry.freshness === "never" || entry.ageSeconds === null)
        return "Not yet observed";
    const seconds = Math.max(0, Math.floor(entry.ageSeconds));
    const age = seconds < 60 ? "just now" : seconds < 3600 ? Math.floor(seconds / 60) + " min ago" : seconds < 86400 ? Math.floor(seconds / 3600) + " hr ago" : Math.floor(seconds / 86400) + " days ago";
    return (entry.freshness === "stale" ? "Out of date · observed " : "Observed ") + age;
}
const availability = { ready: "Available", pending: "Connecting", missing: "Board missing", invalid: "Board needs attention", busy: "In use by another service", unavailable: "Unavailable", identity_conflict: "Board identity changed" };
// Only this view's metadata nodes change during refresh. The controller owns the
// editor subtree for its entire lifetime, including detached private views.
export class WorkspaceShell {
    root;
    options;
    editorRoot = node("section", "workspace-editor");
    chooserRoot = node("section", "workspace-chooser");
    shell = node("div", "workspace-shell");
    sidebar = node("aside", "workspace-sidebar");
    list = node("ul", "workspace-board-list");
    pinnedList = node("ul", "workspace-board-list workspace-pinned-list");
    pinStatus = node("p", "workspace-pin-status");
    pinActions = node("div", "workspace-pin-actions");
    rows = new Map();
    choices = new Map();
    choiceList = node("ul", "workspace-choice-list");
    status = node("p", "workspace-catalog-status", "Loading boards…");
    routeStatus = node("p", "workspace-route-status");
    empty = node("p", "workspace-empty");
    projects = node("div", "workspace-empty-projects");
    toggle = node("button", "workspace-toggle");
    width = node("input", "workspace-width");
    layout;
    selected = "/";
    roving;
    snapshot;
    pins = { pins: [], state: "loading" };
    constructor(root, options) {
        this.root = root;
        this.options = options;
        this.layout = { collapsed: options.layout?.collapsed ?? false, width: Math.max(220, Math.min(420, options.layout?.width ?? 288)) };
        const header = node("header", "workspace-header"), brand = node("a", "workspace-brand", "Localswim");
        brand.href = "/";
        this.bindNavigation(brand);
        this.sidebar.id = "workspace-sidebar";
        this.toggle.setAttribute("aria-controls", this.sidebar.id);
        this.toggle.type = "button";
        this.toggle.addEventListener("click", () => { this.layout = { ...this.layout, collapsed: !this.layout.collapsed }; this.applyLayout(); this.options.layoutChanged?.(this.layout); });
        header.append(brand, this.toggle);
        const body = node("div", "workspace-body"), nav = node("nav", "workspace-nav");
        nav.setAttribute("aria-label", "Workspace boards");
        const title = node("h2", "workspace-group-title", "Other"), pinnedTitle = node("h2", "workspace-group-title", "Pinned");
        pinnedTitle.id = "workspace-pinned-title";
        title.id = "workspace-other-title";
        this.pinnedList.setAttribute("aria-labelledby", pinnedTitle.id);
        this.list.setAttribute("aria-labelledby", title.id);
        this.pinStatus.setAttribute("role", "status");
        this.pinStatus.setAttribute("aria-live", "polite");
        const retryPins = node("button", "workspace-pin-retry", "Retry pin change"), inspectPins = node("button", "workspace-pin-inspect", "Check saved pins");
        retryPins.type = inspectPins.type = "button";
        retryPins.addEventListener("click", () => options.retryPins?.());
        inspectPins.addEventListener("click", () => options.inspectPins?.());
        this.pinActions.append(retryPins, inspectPins);
        this.status.setAttribute("role", "status");
        this.status.setAttribute("aria-live", "polite");
        const retry = node("button", "workspace-refresh", "Refresh boards");
        retry.type = "button";
        retry.addEventListener("click", () => options.retry());
        const widthLabel = node("label", "workspace-width-label", "Sidebar width");
        this.width.type = "range";
        this.width.min = "220";
        this.width.max = "420";
        this.width.step = "10";
        this.width.setAttribute("aria-label", "Sidebar width");
        this.width.addEventListener("input", () => { this.layout = { ...this.layout, width: Number(this.width.value) }; this.applyLayout(); this.options.layoutChanged?.(this.layout); });
        widthLabel.append(this.width);
        nav.append(pinnedTitle, this.pinnedList, title, this.list);
        this.sidebar.append(nav, this.pinStatus, this.pinActions, this.status, retry, widthLabel);
        this.list.addEventListener("keydown", this.sidebarKeys);
        this.pinnedList.addEventListener("keydown", this.sidebarKeys);
        const content = node("div", "workspace-content");
        this.routeStatus.setAttribute("role", "status");
        this.routeStatus.hidden = true;
        this.chooserRoot.append(node("p", "workspace-eyebrow", "Your workspace"), node("h1", "", "Choose a board"), node("p", "workspace-intro", "Open a board to view its cards and continue your work."), this.empty, this.choiceList, this.projects);
        content.append(this.routeStatus, this.chooserRoot, this.editorRoot);
        body.append(this.sidebar, content);
        this.shell.append(header, body);
        root.replaceChildren(this.shell);
        this.applyLayout();
        this.setRoute("/");
        this.setPins(this.pins);
    }
    applyLayout() {
        this.shell.classList.toggle("workspace-collapsed", this.layout.collapsed);
        this.shell.style.setProperty("--sidebar-width", this.layout.width + "px");
        this.sidebar.hidden = this.layout.collapsed;
        this.toggle.textContent = this.layout.collapsed ? "Show boards" : "Hide boards";
        this.toggle.setAttribute("aria-expanded", String(!this.layout.collapsed));
        this.width.value = String(this.layout.width);
    }
    bindNavigation(link) {
        link.addEventListener("click", event => {
            if (!link.hasAttribute("href") || event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey)
                return;
            event.preventDefault();
            void this.options.navigate(link.pathname);
        });
        link.addEventListener("keydown", event => { if (link.hasAttribute("href") && event.key === " " && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
            event.preventDefault();
            void this.options.navigate(link.pathname);
        } });
    }
    makeRow(entry, sidebar) {
        const row = { root: node("li", "workspace-board-row"), link: node("a", "workspace-board-link"), project: node("span", "workspace-project-label"), board: node("span", "workspace-board-label"), count: node("span", "workspace-rfr"), status: node("span", "workspace-availability"), age: node("span", "workspace-observation") };
        row.link.href = boardHref(entry);
        this.bindNavigation(row.link);
        row.link.append(row.project, row.board, row.count, row.status, row.age);
        row.root.append(row.link);
        if (sidebar)
            row.link.addEventListener("focus", () => { this.roving = row.link.pathname; this.syncSelection(); });
        if (sidebar && this.options.pinChanged) {
            row.controls = node("div", "workspace-row-controls");
            row.pin = node("button", "workspace-pin");
            row.up = node("button", "workspace-pin-up", "Move up");
            row.down = node("button", "workspace-pin-down", "Move down");
            for (const button of [row.pin, row.up, row.down])
                button.type = "button";
            const ref = { projectKey: entry.projectKey, boardKey: entry.boardKey }, key = boardHref(ref);
            row.pin.addEventListener("click", () => { if (this.pins.state !== "ready")
                return; const exists = this.pins.pins.some(pin => boardHref(pin) === key); this.options.pinChanged?.(exists ? this.pins.pins.filter(pin => boardHref(pin) !== key) : [...this.pins.pins, ref]); });
            const move = (offset) => { if (this.pins.state !== "ready")
                return; const order = [...this.pins.pins], index = order.findIndex(pin => boardHref(pin) === key), next = index + offset; if (index < 0 || next < 0 || next >= order.length)
                return; [order[index], order[next]] = [order[next], order[index]]; this.options.pinChanged?.(order); };
            row.up.addEventListener("click", () => move(-1));
            row.down.addEventListener("click", () => move(1));
            row.controls.append(row.pin, row.up, row.down);
            row.root.append(row.controls);
        }
        return row;
    }
    updateRows(entries, map, list, sidebar, failed) {
        const keys = new Set(entries.map(boardHref));
        for (const [key, row] of map)
            if (!keys.has(key) && (!sidebar || !this.pins.pins.some(pin => boardHref(pin) === key))) {
                row.root.remove();
                map.delete(key);
            }
        entries.forEach((entry, index) => {
            const key = boardHref(entry);
            let row = map.get(key);
            if (!row) {
                row = this.makeRow(entry, sidebar);
                map.set(key, row);
            }
            row.link.href = key;
            row.project.textContent = entry.projectLabel;
            row.board.textContent = entry.label;
            row.count.textContent = "Ready for Review: " + (failed && entry.attention.readyForReview.state === "known" ? String(entry.attention.readyForReview.value) + " · out of date" : countLabel(entry.attention.readyForReview));
            row.status.textContent = failed ? "Connection unavailable · last observation" : availability[entry.availability];
            row.age.textContent = (failed ? "Out of date · " : "") + observationLabel(entry);
            row.root.dataset.availability = failed ? "unavailable" : entry.availability;
            const pinIndex = sidebar ? this.pins.pins.findIndex(pin => boardHref(pin) === key) : -1;
            const target = pinIndex >= 0 ? this.pinnedList : list, position = pinIndex >= 0 ? pinIndex : sidebar ? entries.slice(0, index).filter(item => !this.pins.pins.some(pin => boardHref(pin) === boardHref(item))).length : index;
            if (target.children[position] !== row.root)
                target.insertBefore(row.root, target.children[position] ?? null);
            if (row.pin) {
                row.pin.textContent = pinIndex >= 0 ? "Unpin" : "Pin";
                row.pin.setAttribute("aria-label", (pinIndex >= 0 ? "Unpin " : "Pin ") + entry.projectLabel + " / " + entry.label);
                row.pin.disabled = this.pins.state !== "ready";
            }
            if (row.up && row.down) {
                row.up.hidden = row.down.hidden = pinIndex < 0;
                row.up.disabled = this.pins.state !== "ready" || pinIndex <= 0;
                row.down.disabled = this.pins.state !== "ready" || pinIndex === this.pins.pins.length - 1;
                row.up.setAttribute("aria-label", "Move " + entry.label + " up in Pinned");
                row.down.setAttribute("aria-label", "Move " + entry.label + " down in Pinned");
            }
        });
    }
    sidebarFocus() {
        const element = document.activeElement;
        if (!(element instanceof HTMLElement) || !this.sidebar.contains(element))
            return;
        return { element, row: [...this.rows.values()].find(row => row.root.contains(element)) };
    }
    restoreSidebarFocus(focus) {
        if (!focus)
            return;
        const usable = (element) => !!element?.isConnected && !element.closest("[hidden]") && !(element instanceof HTMLButtonElement && element.disabled) && element.getClientRects().length > 0;
        let target = usable(focus.element) ? focus.element : undefined;
        if (!target && focus.row?.root.isConnected) {
            const row = focus.row;
            target = [row.link.hasAttribute("href") ? row.link : undefined, row.pin, row.up, row.down].find(usable);
            if (!target && usable(row.root)) {
                row.root.tabIndex = -1;
                target = row.root;
            }
        }
        if (target && document.activeElement !== target)
            target.focus({ preventScroll: true });
    }
    update(snapshot) {
        const focused = this.sidebarFocus();
        const scrollTop = this.sidebar.scrollTop, scrollLeft = this.sidebar.scrollLeft;
        this.snapshot = snapshot;
        const entries = snapshot.catalog?.entries ?? [];
        this.updateRows(entries, this.rows, this.list, true, snapshot.state === "unavailable");
        for (const pin of this.pins.pins)
            if (!entries.some(entry => boardHref(entry) === boardHref(pin))) {
                const key = boardHref(pin);
                let row = this.rows.get(key);
                if (!row) {
                    row = this.makeRow(pin, true);
                    this.rows.set(key, row);
                }
                row.link.removeAttribute("href");
                row.link.tabIndex = -1;
                row.project.textContent = pin.projectKey + " / " + pin.boardKey;
                row.board.textContent = "Pinned board unavailable";
                row.count.textContent = "";
                row.age.textContent = "";
                row.status.textContent = "Current board information is unavailable";
                row.root.dataset.availability = "unavailable";
                const index = this.pins.pins.indexOf(pin);
                if (row.pin && row.up && row.down) {
                    row.pin.textContent = "Unpin";
                    row.pin.setAttribute("aria-label", "Unpin " + pin.projectKey + " / " + pin.boardKey);
                    row.pin.disabled = this.pins.state !== "ready";
                    row.up.hidden = row.down.hidden = false;
                    row.up.disabled = this.pins.state !== "ready" || index === 0;
                    row.down.disabled = this.pins.state !== "ready" || index === this.pins.pins.length - 1;
                    row.up.setAttribute("aria-label", "Move unavailable pinned board up");
                    row.down.setAttribute("aria-label", "Move unavailable pinned board down");
                }
                this.pinnedList.append(row.root);
            }
        // Apply the saved order after catalog iteration; Other keeps server order.
        this.pins.pins.forEach((pin, index) => { const row = this.rows.get(boardHref(pin)); if (row && this.pinnedList.children[index] !== row.root)
            this.pinnedList.insertBefore(row.root, this.pinnedList.children[index] ?? null); });
        this.updateRows(entries, this.choices, this.choiceList, false, snapshot.state === "unavailable");
        this.status.textContent = snapshot.state === "unavailable" ? "Board list could not be refreshed. Retry when the local service is available." : snapshot.state === "loading" ? "Loading boards…" : "Board list connected · refreshes every 5 seconds while this window is visible";
        this.empty.textContent = entries.length ? "" : snapshot.state === "loading" ? "Loading your boards…" : snapshot.state === "unavailable" ? "The board list is unavailable. Refresh to try again." : "No boards are registered in this workspace yet.";
        this.empty.hidden = entries.length > 0;
        const emptyProjects = snapshot.catalog && snapshot.projects?.configRevision === snapshot.catalog.configRevision ? snapshot.projects.projects.filter(project => !entries.some(entry => entry.projectKey === project.key)) : [];
        this.projects.replaceChildren(...emptyProjects.map(project => node("p", "workspace-empty-project", project.label + " · No boards registered")));
        this.syncSelection();
        this.restoreSidebarFocus(focused);
        this.sidebar.scrollTop = scrollTop;
        this.sidebar.scrollLeft = scrollLeft;
    }
    setPins(snapshot) {
        const focused = this.sidebarFocus();
        const scrollTop = this.sidebar.scrollTop, scrollLeft = this.sidebar.scrollLeft;
        this.pins = snapshot;
        const messages = { loading: "Loading saved pins…", ready: "Pins are saved across your workspace windows.", saving: "Saving pin change…", failed: "Pin change was not saved. Your last saved pins are shown.", conflict: "Pins changed in another window. Current saved pins are shown; review them before retrying.", unknown: "The result of the pin change is unknown. Check saved pins before trying again." };
        this.pinStatus.textContent = snapshot.message ?? messages[snapshot.state];
        this.pinActions.hidden = snapshot.state === "loading" || snapshot.state === "ready" || snapshot.state === "saving";
        const retry = this.pinActions.querySelector(".workspace-pin-retry");
        retry.hidden = snapshot.state === "unknown";
        retry.textContent = "Review current pins";
        if (this.snapshot)
            this.update(this.snapshot);
        this.restoreSidebarFocus(focused);
        this.sidebar.scrollTop = scrollTop;
        this.sidebar.scrollLeft = scrollLeft;
    }
    setCatalog(snapshot) {
        this.update({ ...(snapshot.catalog ? { catalog: snapshot.catalog } : {}), ...(snapshot.projects ? { projects: snapshot.projects } : {}), state: snapshot.phase === "stale" ? "ready" : snapshot.phase, refreshing: snapshot.refreshing });
        if (snapshot.phase === "stale")
            this.status.textContent = "Board list is out of date. The last observation is shown; refresh to try again.";
    }
    setRoute(path) {
        this.selected = path;
        this.chooserRoot.hidden = path !== "/";
        this.editorRoot.hidden = path === "/";
        this.syncSelection();
    }
    setStatus(message) { this.routeStatus.textContent = message; this.routeStatus.hidden = !message; }
    setRouteStatus(status) {
        this.setRoute(status.phase === "blocked" ? status.activePath ?? this.selected : status.requestedPath);
        this.setStatus(status.message ?? (status.phase === "loading" ? "Opening board…" : status.phase === "blocked" ? "Navigation paused. Save or discard pending private input to continue." : ""));
    }
    syncSelection() {
        const first = this.snapshot?.catalog?.entries[0];
        if (!this.roving || !this.rows.get(this.roving)?.link.hasAttribute("href"))
            this.roving = this.rows.get(this.selected)?.link.hasAttribute("href") ? this.selected : first ? boardHref(first) : undefined;
        for (const [key, row] of this.rows) {
            row.link.tabIndex = row.link.hasAttribute("href") && key === this.roving ? 0 : -1;
            if (key === this.selected)
                row.link.setAttribute("aria-current", "page");
            else
                row.link.removeAttribute("aria-current");
        }
        for (const [key, row] of this.choices) {
            if (key === this.selected)
                row.link.setAttribute("aria-current", "page");
            else
                row.link.removeAttribute("aria-current");
        }
    }
    sidebarKeys = (event) => {
        if (event.ctrlKey || event.metaKey || event.altKey || event.shiftKey || !(event.target instanceof HTMLAnchorElement))
            return;
        const links = [...this.pinnedList.querySelectorAll("a[href]"), ...this.list.querySelectorAll("a[href]")], index = links.indexOf(event.target);
        if (index < 0)
            return;
        const next = event.key === "ArrowDown" ? (index + 1) % links.length : event.key === "ArrowUp" ? (index + links.length - 1) % links.length : event.key === "Home" ? 0 : event.key === "End" ? links.length - 1 : -1;
        if (next >= 0) {
            event.preventDefault();
            links[next].focus();
        }
    };
    destroy() { this.list.removeEventListener("keydown", this.sidebarKeys); this.pinnedList.removeEventListener("keydown", this.sidebarKeys); this.shell.remove(); }
}
