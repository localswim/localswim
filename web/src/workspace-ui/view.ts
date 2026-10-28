import type { CatalogCount, WorkspaceCatalog, WorkspaceCatalogEntry, WorkspaceProjects, WorkspacePinReference } from "../../generated/transport/contracts.js";

export type ShellPinSnapshot = Readonly<{ pins: readonly WorkspacePinReference[]; state: "loading" | "ready" | "saving" | "failed" | "conflict" | "unknown"; message?: string }>;

export type ShellSnapshot = Readonly<{ catalog?: WorkspaceCatalog; projects?: WorkspaceProjects; state: "loading" | "ready" | "stale" | "unavailable"; refreshing?: boolean }>;
export type ShellCatalogSnapshot = Readonly<{ phase: "loading" | "ready" | "stale" | "unavailable"; catalog: WorkspaceCatalog | null; projects: WorkspaceProjects | null; receivedAt: number | null; refreshing: boolean }>;
export type ShellLayout = Readonly<{ collapsed: boolean; width: number }>;
export type RouteStatus = Readonly<{ requestedPath: string; activePath?: string; phase: "loading" | "ready" | "unavailable" | "blocked"; message?: string }>;
export type WorkspaceShellOptions = Readonly<{ navigate(path: string): void | Promise<unknown>; retry(): void; filter?: string; filterChanged?(filter: string): void; layout?: ShellLayout; layoutChanged?(layout: ShellLayout): void; pinChanged?(pins: ShellPinSnapshot["pins"]): void; retryPins?(): void; inspectPins?(): void }>;

function node<K extends keyof HTMLElementTagNameMap>(tag: K, className = "", text = ""): HTMLElementTagNameMap[K] {
  const result = document.createElement(tag); result.className = className; result.textContent = text; return result;
}
export function boardHref(entry: Pick<WorkspaceCatalogEntry, "projectKey" | "boardKey">): string {
  return "/board/" + encodeURIComponent(entry.projectKey) + "/" + encodeURIComponent(entry.boardKey);
}
export function countLabel(count: CatalogCount): string {
  switch (count.state) {
    case "known": return String(count.value);
    case "stale": return String(count.value) + " · out of date";
    case "not_configured": return "Not configured";
    case "unknown": return "Unknown";
  }
}
export function observationLabel(entry: WorkspaceCatalogEntry): string {
  if (entry.freshness === "never" || entry.ageSeconds === null) return "Not yet observed";
  const seconds = Math.max(0, Math.floor(entry.ageSeconds));
  const age = seconds < 60 ? "just now" : seconds < 3600 ? Math.floor(seconds / 60) + " min ago" : seconds < 86400 ? Math.floor(seconds / 3600) + " hr ago" : Math.floor(seconds / 86400) + " days ago";
  return (entry.freshness === "stale" ? "Out of date · observed " : "Observed ") + age;
}
const availability: Record<WorkspaceCatalogEntry["availability"], string> = { ready: "Available", pending: "Connecting", missing: "Board missing", invalid: "Board needs attention", busy: "In use by another service", unavailable: "Unavailable", identity_conflict: "Board identity changed" };
type Row = { root: HTMLLIElement; link: HTMLAnchorElement; project: HTMLElement; board: HTMLElement; count: HTMLElement; status: HTMLElement; age: HTMLElement; controls?: HTMLElement; pin?: HTMLButtonElement; up?: HTMLButtonElement; down?: HTMLButtonElement };
type SidebarFocus = { element: HTMLElement; row?: Row };

// Only this view's metadata nodes change during refresh. The controller owns the
// editor subtree for its entire lifetime, including detached private views.
export class WorkspaceShell {
  readonly editorRoot = node("section", "workspace-editor");
  readonly chooserRoot = node("section", "workspace-chooser");
  private readonly shell = node("div", "workspace-shell");
  private readonly sidebar = node("aside", "workspace-sidebar");
  private readonly list = node("ul", "workspace-board-list");
  private readonly pinnedList = node("ul", "workspace-board-list workspace-pinned-list");
  private readonly pinStatus = node("p", "workspace-pin-status");
  private readonly pinActions = node("div", "workspace-pin-actions");
  private readonly rows = new Map<string, Row>();
  private readonly choices = new Map<string, Row>();
  private readonly choiceList = node("ul", "workspace-choice-list");
  private readonly status = node("p", "workspace-catalog-status", "Loading boards…");
  private readonly routeStatus = node("p", "workspace-route-status");
  private readonly empty = node("p", "workspace-empty");
  private readonly projects = node("div", "workspace-empty-projects");
  private readonly toggle = node("button", "workspace-toggle");
  private readonly width = node("input", "workspace-width");
  private readonly filter = node("input", "workspace-filter");
  private readonly filterStatus = node("p", "workspace-filter-status");
  private layout: ShellLayout;
  private selected = "/";
  private roving?: string;
  private snapshot?: ShellSnapshot;
  private pins: ShellPinSnapshot = { pins: [], state: "loading" };
  constructor(private readonly root: HTMLElement, private readonly options: WorkspaceShellOptions) {
    this.layout = { collapsed: options.layout?.collapsed ?? false, width: Math.max(220, Math.min(420, options.layout?.width ?? 288)) };
    const header = node("header", "workspace-header"), brand = node("a", "workspace-brand", "Localswim");
    brand.href = "/"; this.bindNavigation(brand);
    this.sidebar.id = "workspace-sidebar"; this.toggle.setAttribute("aria-controls", this.sidebar.id);
    this.toggle.type = "button"; this.toggle.addEventListener("click", () => { this.layout = { ...this.layout, collapsed: !this.layout.collapsed }; this.applyLayout(); this.options.layoutChanged?.(this.layout); });
    header.append(brand, this.toggle);
    const body = node("div", "workspace-body"), nav = node("nav", "workspace-nav"); nav.setAttribute("aria-label", "Workspace boards");
    const title = node("h2", "workspace-group-title", "Other"), pinnedTitle = node("h2", "workspace-group-title", "Pinned");
    pinnedTitle.id = "workspace-pinned-title"; title.id = "workspace-other-title";
    this.pinnedList.setAttribute("aria-labelledby", pinnedTitle.id); this.list.setAttribute("aria-labelledby", title.id);
    this.pinStatus.setAttribute("role", "status"); this.pinStatus.setAttribute("aria-live", "polite");
    const retryPins = node("button", "workspace-pin-retry", "Retry pin change"), inspectPins = node("button", "workspace-pin-inspect", "Check saved pins");
    retryPins.type = inspectPins.type = "button"; retryPins.addEventListener("click", () => options.retryPins?.()); inspectPins.addEventListener("click", () => options.inspectPins?.()); this.pinActions.append(retryPins, inspectPins);
    this.status.setAttribute("role", "status"); this.status.setAttribute("aria-live", "polite");
    const retry = node("button", "workspace-refresh", "Refresh boards"); retry.type = "button"; retry.addEventListener("click", () => options.retry());
    const widthLabel = node("label", "workspace-width-label", "Sidebar width");
    this.width.type = "range"; this.width.min = "220"; this.width.max = "420"; this.width.step = "10"; this.width.setAttribute("aria-label", "Sidebar width");
    this.width.addEventListener("input", () => { this.layout = { ...this.layout, width: Number(this.width.value) }; this.applyLayout(); this.options.layoutChanged?.(this.layout); });
    const filterLabel = node("label", "workspace-filter-label", "Filter boards");
    this.filter.type = "search"; this.filter.maxLength = 200; this.filter.value = (options.filter ?? "").slice(0, 200);
    this.filter.id = "workspace-filter"; filterLabel.htmlFor = this.filter.id;
    this.filterStatus.setAttribute("role", "status"); this.filterStatus.hidden = true;
    this.filter.addEventListener("input", () => { this.filter.value = this.filter.value.slice(0, 200); this.applyFilter(); this.options.filterChanged?.(this.filter.value); });
    widthLabel.append(this.width); nav.append(pinnedTitle, this.pinnedList, title, this.list); this.sidebar.append(filterLabel, this.filter, this.filterStatus, nav, this.pinStatus, this.pinActions, this.status, retry, widthLabel);
    this.list.addEventListener("keydown", this.sidebarKeys);
    this.pinnedList.addEventListener("keydown", this.sidebarKeys);
    const content = node("div", "workspace-content");
    this.routeStatus.setAttribute("role", "status"); this.routeStatus.hidden = true;
    this.chooserRoot.append(node("p", "workspace-eyebrow", "Your workspace"), node("h1", "", "Choose a board"), node("p", "workspace-intro", "Open a board to view its cards and continue your work."), this.empty, this.choiceList, this.projects);
    content.append(this.routeStatus, this.chooserRoot, this.editorRoot); body.append(this.sidebar, content); this.shell.append(header, body); root.replaceChildren(this.shell);
    this.applyLayout(); this.setRoute("/"); this.setPins(this.pins);
  }
  private applyLayout(): void {
    this.shell.classList.toggle("workspace-collapsed", this.layout.collapsed); this.shell.style.setProperty("--sidebar-width", this.layout.width + "px");
    this.sidebar.hidden = this.layout.collapsed; this.toggle.textContent = this.layout.collapsed ? "Show boards" : "Hide boards"; this.toggle.setAttribute("aria-expanded", String(!this.layout.collapsed)); this.width.value = String(this.layout.width);
  }
  private bindNavigation(link: HTMLAnchorElement): void {
    link.addEventListener("click", event => {
      if (!link.hasAttribute("href") || event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
      event.preventDefault(); void this.options.navigate(link.pathname);
    });
    link.addEventListener("keydown", event => { if (link.hasAttribute("href") && event.key === " " && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) { event.preventDefault(); void this.options.navigate(link.pathname); } });
  }
  private makeRow(entry: Pick<WorkspaceCatalogEntry, "projectKey" | "boardKey">, sidebar: boolean): Row {
    const row: Row = { root: node("li", "workspace-board-row"), link: node("a", "workspace-board-link"), project: node("span", "workspace-project-label"), board: node("span", "workspace-board-label"), count: node("span", "workspace-rfr"), status: node("span", "workspace-availability"), age: node("span", "workspace-observation") };
    row.link.href = boardHref(entry); this.bindNavigation(row.link);
    row.link.append(row.project, row.board, row.count, row.status, row.age); row.root.append(row.link);
    if (sidebar) row.link.addEventListener("focus", () => { this.roving = row.link.pathname; this.syncSelection(); });
    if (sidebar && this.options.pinChanged) {
      row.controls = node("div", "workspace-row-controls"); row.pin = node("button", "workspace-pin"); row.up = node("button", "workspace-pin-up", "Move up"); row.down = node("button", "workspace-pin-down", "Move down");
      for (const button of [row.pin, row.up, row.down]) button.type = "button";
      const ref = { projectKey: entry.projectKey, boardKey: entry.boardKey }, key = boardHref(ref);
      row.pin.addEventListener("click", () => { if (this.pins.state !== "ready") return; const exists = this.pins.pins.some(pin => boardHref(pin) === key); this.options.pinChanged?.(exists ? this.pins.pins.filter(pin => boardHref(pin) !== key) : [...this.pins.pins, ref]); });
      const move = (offset: number): void => { if (this.pins.state !== "ready") return; const order = [...this.pins.pins], index = order.findIndex(pin => boardHref(pin) === key), next = index + offset; if (index < 0 || next < 0 || next >= order.length) return; [order[index], order[next]] = [order[next]!, order[index]!]; this.options.pinChanged?.(order); };
      row.up.addEventListener("click", () => move(-1)); row.down.addEventListener("click", () => move(1));
      row.controls.append(row.pin, row.up, row.down); row.root.append(row.controls);
    }
    return row;
  }
  private updateRows(entries: readonly WorkspaceCatalogEntry[], map: Map<string, Row>, list: HTMLElement, sidebar: boolean, failed: boolean): void {
    const keys = new Set(entries.map(boardHref));
    for (const [key, row] of map) if (!keys.has(key) && (!sidebar || !this.pins.pins.some(pin => boardHref(pin) === key))) { row.root.remove(); map.delete(key); }
    entries.forEach((entry, index) => {
      const key = boardHref(entry); let row = map.get(key);
      if (!row) { row = this.makeRow(entry, sidebar); map.set(key, row); }
      row.link.href = key;
      row.root.dataset.filter = [entry.projectLabel, entry.label, entry.projectKey, entry.boardKey].join("\n").toLowerCase();
      row.project.textContent = entry.projectLabel; row.board.textContent = entry.label;
      row.count.textContent = "Ready for Review: " + (failed && entry.attention.readyForReview.state === "known" ? String(entry.attention.readyForReview.value) + " · out of date" : countLabel(entry.attention.readyForReview));
      row.status.textContent = failed ? "Current availability unknown · last observation: " + availability[entry.availability] : availability[entry.availability];
      row.age.textContent = (failed && entry.freshness !== "stale" ? "Out of date · " : "") + observationLabel(entry);
      row.root.dataset.availability = failed ? "unavailable" : entry.availability;
      const pinIndex = sidebar ? this.pins.pins.findIndex(pin => boardHref(pin) === key) : -1;
      const target = pinIndex >= 0 ? this.pinnedList : list, position = pinIndex >= 0 ? pinIndex : sidebar ? entries.slice(0, index).filter(item => !this.pins.pins.some(pin => boardHref(pin) === boardHref(item))).length : index;
      if (target.children[position] !== row.root) target.insertBefore(row.root, target.children[position] ?? null);
      if (row.pin) { row.pin.textContent = pinIndex >= 0 ? "Unpin" : "Pin"; row.pin.setAttribute("aria-label", (pinIndex >= 0 ? "Unpin " : "Pin ") + entry.projectLabel + " / " + entry.label); row.pin.disabled = this.pins.state !== "ready"; }
      if (row.up && row.down) { row.up.hidden = row.down.hidden = pinIndex < 0; row.up.disabled = this.pins.state !== "ready" || pinIndex <= 0; row.down.disabled = this.pins.state !== "ready" || pinIndex === this.pins.pins.length - 1; row.up.setAttribute("aria-label", "Move " + entry.label + " up in Pinned"); row.down.setAttribute("aria-label", "Move " + entry.label + " down in Pinned"); }
    });
  }
  private sidebarFocus(): SidebarFocus | undefined {
    const element = document.activeElement;
    if (!(element instanceof HTMLElement) || !this.sidebar.contains(element)) return;
    return { element, row: [...this.rows.values()].find(row => row.root.contains(element)) };
  }
  private restoreSidebarFocus(focus: SidebarFocus | undefined): void {
    if (!focus) return;
    const usable = (element: HTMLElement | undefined): element is HTMLElement => !!element?.isConnected && !element.closest("[hidden]") && !(element instanceof HTMLButtonElement && element.disabled) && element.getClientRects().length > 0;
    let target = usable(focus.element) ? focus.element : undefined;
    if (!target && focus.row?.root.isConnected) {
      const row = focus.row;
      target = [row.link.hasAttribute("href") ? row.link : undefined, row.pin, row.up, row.down].find(usable);
      if (!target && usable(row.root)) { row.root.tabIndex = -1; target = row.root; }
    }
    // Moving keyed rows can blur them before filtering runs. Use the focus
    // captured before the update when that row is now hidden or removed.
    if (!target && focus.row) target = [...this.visibleLinks(), this.filter].find(usable);
    if (target && document.activeElement !== target) target.focus({ preventScroll: true });
  }
  update(snapshot: ShellSnapshot): void {
    const focused = this.sidebarFocus();
    const scrollTop = this.sidebar.scrollTop, scrollLeft = this.sidebar.scrollLeft;
    this.snapshot = snapshot;
    const entries = snapshot.catalog?.entries ?? [];
    this.updateRows(entries, this.rows, this.list, true, snapshot.state === "unavailable" || snapshot.state === "stale");
    for (const pin of this.pins.pins) if (!entries.some(entry => boardHref(entry) === boardHref(pin))) {
      const key = boardHref(pin); let row = this.rows.get(key);
      if (!row) { row = this.makeRow(pin, true); this.rows.set(key, row); }
      row.root.dataset.filter = [pin.projectKey, pin.boardKey].join("\n").toLowerCase();
      row.link.removeAttribute("href"); row.link.tabIndex = -1;
      row.project.textContent = pin.projectKey + " / " + pin.boardKey; row.board.textContent = "Pinned board unavailable";
      row.count.textContent = ""; row.age.textContent = ""; row.status.textContent = "Current board information is unavailable"; row.root.dataset.availability = "unavailable";
      const index = this.pins.pins.indexOf(pin);
      if (row.pin && row.up && row.down) { row.pin.textContent = "Unpin"; row.pin.setAttribute("aria-label", "Unpin " + pin.projectKey + " / " + pin.boardKey); row.pin.disabled = this.pins.state !== "ready"; row.up.hidden = row.down.hidden = false; row.up.disabled = this.pins.state !== "ready" || index === 0; row.down.disabled = this.pins.state !== "ready" || index === this.pins.pins.length - 1; row.up.setAttribute("aria-label", "Move unavailable pinned board up"); row.down.setAttribute("aria-label", "Move unavailable pinned board down"); }
      this.pinnedList.append(row.root);
    }
    // Apply the saved order after catalog iteration; Other keeps server order.
    this.pins.pins.forEach((pin, index) => { const row = this.rows.get(boardHref(pin)); if (row && this.pinnedList.children[index] !== row.root) this.pinnedList.insertBefore(row.root, this.pinnedList.children[index] ?? null); });
    this.updateRows(entries, this.choices, this.choiceList, false, snapshot.state === "unavailable" || snapshot.state === "stale");
    this.status.textContent = snapshot.state === "stale" ? "Board list is out of date. The last observation is shown; refresh to try again." : snapshot.state === "unavailable" ? "Board list could not be refreshed. Retry when the local service is available." : snapshot.state === "loading" ? "Loading boards…" : "Board list connected · refreshes every 5 seconds while this window is visible";
    this.empty.textContent = entries.length ? "" : snapshot.state === "loading" ? "Loading your boards…" : snapshot.state === "unavailable" ? "The board list is unavailable. Refresh to try again." : snapshot.state === "stale" ? "No boards were registered at the last observation. The board list is out of date." : "No boards are registered in this workspace yet.";
    this.empty.hidden = entries.length > 0;
    const emptyProjects = snapshot.catalog && snapshot.projects?.configRevision === snapshot.catalog.configRevision ? snapshot.projects.projects.filter(project => !entries.some(entry => entry.projectKey === project.key)) : [];
    this.projects.replaceChildren(...emptyProjects.map(project => node("p", "workspace-empty-project", project.label + (snapshot.state === "stale" || snapshot.state === "unavailable" ? " · No boards registered at the last observation" : " · No boards registered"))));
    this.applyFilter();
    this.restoreSidebarFocus(focused);
    this.sidebar.scrollTop = scrollTop; this.sidebar.scrollLeft = scrollLeft;
  }
  setPins(snapshot: ShellPinSnapshot): void {
    const focused = this.sidebarFocus();
    const scrollTop = this.sidebar.scrollTop, scrollLeft = this.sidebar.scrollLeft;
    this.pins = snapshot;
    const messages = { loading: "Loading saved pins…", ready: "Pins are saved across your workspace windows.", saving: "Saving pin change…", failed: "Pin change was not saved. Your last saved pins are shown.", conflict: "Pins changed in another window. Current saved pins are shown; review them before retrying.", unknown: "The result of the pin change is unknown. Check saved pins before trying again." };
    this.pinStatus.textContent = snapshot.message ?? messages[snapshot.state];
    this.pinActions.hidden = snapshot.state === "loading" || snapshot.state === "ready" || snapshot.state === "saving";
    const retry = this.pinActions.querySelector<HTMLButtonElement>(".workspace-pin-retry")!; retry.hidden = snapshot.state === "unknown"; retry.textContent = "Review current pins";
    if (this.snapshot) this.update(this.snapshot);
    this.restoreSidebarFocus(focused);
    this.sidebar.scrollTop = scrollTop; this.sidebar.scrollLeft = scrollLeft;
  }
  setCatalog(snapshot: ShellCatalogSnapshot): void {
    this.update({ ...(snapshot.catalog ? { catalog: snapshot.catalog } : {}), ...(snapshot.projects ? { projects: snapshot.projects } : {}), state: snapshot.phase, refreshing: snapshot.refreshing });
  }
  setRoute(path: string): void {
    this.selected = path; this.chooserRoot.hidden = path !== "/"; this.editorRoot.hidden = path === "/"; this.syncSelection();
  }
  setStatus(message: string): void { this.routeStatus.textContent = message; this.routeStatus.hidden = !message; }
  setRouteStatus(status: RouteStatus): void {
    this.setRoute(status.phase === "blocked" ? status.activePath ?? this.selected : status.requestedPath);
    this.setStatus(status.message ?? (status.phase === "loading" ? "Opening board…" : status.phase === "blocked" ? "Navigation paused. Save or discard pending private input to continue." : ""));
  }
  // Filter changes only row visibility. Catalog facts, pin order and the active
  // editor/route remain intact, including when the selected board is hidden.
  private applyFilter(): void {
    const focused = this.sidebarFocus();
    const query = this.filter.value.trim().toLowerCase();
    for (const row of this.rows.values()) row.root.hidden = !!query && !(row.root.dataset.filter ?? "").split("\n").some(value => value.includes(query));
    const visible = [...this.rows.values()].filter(row => !row.root.hidden).length;
    this.filterStatus.textContent = query && !visible ? "No boards match this filter. Clear Filter boards to show all boards." : "";
    this.filterStatus.hidden = !query || visible > 0;
    this.syncSelection();
    if (focused?.row?.root.hidden) (this.visibleLinks()[0] ?? this.filter).focus({ preventScroll: true });
  }
  private visibleLinks(): HTMLAnchorElement[] {
    return [...this.pinnedList.querySelectorAll<HTMLAnchorElement>("a[href]"), ...this.list.querySelectorAll<HTMLAnchorElement>("a[href]")].filter(link => !link.closest<HTMLLIElement>(".workspace-board-row")?.hidden);
  }
  private syncSelection(): void {
    const links = this.visibleLinks(), eligible = (key: string | undefined): boolean => !!key && links.includes(this.rows.get(key)?.link as HTMLAnchorElement);
    if (!eligible(this.roving)) this.roving = eligible(this.selected) ? this.selected : links[0]?.pathname;
    for (const [key, row] of this.rows) { row.link.tabIndex = !row.root.hidden && row.link.hasAttribute("href") && key === this.roving ? 0 : -1; if (key === this.selected) row.link.setAttribute("aria-current", "page"); else row.link.removeAttribute("aria-current"); }
    for (const [key, row] of this.choices) { if (key === this.selected) row.link.setAttribute("aria-current", "page"); else row.link.removeAttribute("aria-current"); }
  }
  private readonly sidebarKeys = (event: KeyboardEvent): void => {
    if (event.ctrlKey || event.metaKey || event.altKey || event.shiftKey || !(event.target instanceof HTMLAnchorElement)) return;
    const links = this.visibleLinks(), index = links.indexOf(event.target);
    if (index < 0) return;
    const next = event.key === "ArrowDown" ? (index + 1) % links.length : event.key === "ArrowUp" ? (index + links.length - 1) % links.length : event.key === "Home" ? 0 : event.key === "End" ? links.length - 1 : -1;
    if (next >= 0) { event.preventDefault(); links[next]!.focus(); }
  };
  destroy(): void { this.list.removeEventListener("keydown", this.sidebarKeys); this.pinnedList.removeEventListener("keydown", this.sidebarKeys); this.shell.remove(); }
}
