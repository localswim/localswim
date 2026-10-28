// Synthetic DOM supplements; actual native-browser workspace evidence is separate.
import type { WorkspaceCatalog, WorkspaceCatalogEntry, WorkspaceProjects } from "../../generated/transport/contracts.js";
import { WorkspaceShell, boardHref, countLabel, observationLabel } from "./view.js";

function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
function entry(projectKey: string, boardKey: string, label: string): WorkspaceCatalogEntry {
  return { projectKey, boardKey, label, projectLabel: projectKey === "harbor" ? "Harbor project" : "Orchard project", availability: "ready", freshness: "fresh", ageSeconds: 0, identity: null, memberEpoch: null, boardRevision: 1, lastObservedAt: "2026-10-28T12:00:00Z", activity: { state: "unknown", lastCommittedAt: null, coverage: null }, attention: { readyForReview: { state: "known", value: 0 }, blocked: { state: "not_configured", value: null }, needsTerry: { state: "not_configured", value: null } } };
}
export function runWorkspaceShellProbe(): number {
  const root = document.createElement("div"); document.body.append(root);
  const calls: string[] = [], changes: Array<{ collapsed: boolean; width: number }> = [];
  const pinChanges: string[][] = [];
  const shell = new WorkspaceShell(root, { navigate: path => { calls.push(path); }, retry: () => { calls.push("retry"); }, layoutChanged: value => changes.push(value), pinChanged: pins => pinChanges.push(pins.map(boardHref)), retryPins: () => calls.push("review-pins"), inspectPins: () => calls.push("inspect-pins") });
  const a = entry("harbor", "alpha", "Delivery board"), b = entry("harbor", "beta", "Delivery board"), c = entry("orchard", "garden", "Garden board");
  const catalog = (entries: readonly WorkspaceCatalogEntry[]): WorkspaceCatalog => ({ apiVersion: 1, workspaceId: "synthetic", configRevision: 2, observedAt: "2026-10-28T12:00:00Z", entries });
  const projects: WorkspaceProjects = { apiVersion: 1, workspaceId: "synthetic", configRevision: 2, projects: [{ key: "harbor", label: "Harbor project" }, { key: "orchard", label: "Orchard project" }, { key: "empty", label: "Empty project" }] };
  try {
    shell.update({ state: "ready", catalog: catalog([a, b, c]), projects });
    assert(!shell.chooserRoot.hidden && shell.editorRoot.hidden, "Root selected a board automatically");
    assert(root.querySelectorAll(".workspace-board-list a").length === 3 && root.textContent?.includes("Empty project · No boards registered"), "Flat board list or empty project missing");
    assert(countLabel({ state: "known", value: 0 }) === "0" && countLabel({ state: "unknown", value: null }) === "Unknown" && countLabel({ state: "not_configured", value: null }) === "Not configured" && countLabel({ state: "stale", value: 4 }).includes("out of date"), "Count states collapsed into zero");
    assert(observationLabel({ ...a, ageSeconds: -5 }) === "Observed just now" && observationLabel({ ...a, freshness: "never", ageSeconds: null }) === "Not yet observed", "Observation age is misleading");
    const links = [...root.querySelectorAll<HTMLAnchorElement>(".workspace-board-list a")], first = links[0]!, second = links[1]!;
    assert(first.pathname === boardHref(a) && first.tabIndex === 0 && second.tabIndex === -1, "Board href or roving tab stop invalid");
    first.focus(); first.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true }));
    assert(document.activeElement === second && Number(second.tabIndex) === 0, "Arrow navigation lost stable focus");
    second.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true, cancelable: true }));
    assert(document.activeElement === first, "Home did not focus first board");
    first.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true, cancelable: true }));
    assert(document.activeElement === links[2], "End did not focus last board");
    second.focus(); shell.update({ state: "ready", catalog: catalog([c, b, a]), projects });
    assert(document.activeElement === second && root.querySelectorAll(".workspace-board-list a")[1] === second, "Catalog refresh replaced focused keyed row");
    const editor = document.createElement("textarea"); editor.value = "Private unsaved input"; shell.editorRoot.append(editor); shell.setRoute(boardHref(a)); editor.focus(); editor.setSelectionRange(3, 8);
    shell.update({ state: "ready", catalog: catalog([a, c, b]), projects });
    assert(shell.editorRoot.firstChild === editor && document.activeElement === editor && editor.value === "Private unsaved input" && editor.selectionStart === 3, "Refresh touched active editor/focus");
    assert(first.getAttribute("aria-current") === "page" && shell.chooserRoot.hidden, "Selected board or chooser visibility incorrect");
    const modified = new MouseEvent("click", { button: 0, ctrlKey: true, bubbles: true, cancelable: true }); first.dispatchEvent(modified);
    assert(!modified.defaultPrevented && calls.length === 0, "Modified click intercepted native new-window navigation");
    const ordinary = new MouseEvent("click", { button: 0, bubbles: true, cancelable: true }); first.dispatchEvent(ordinary);
    second.dispatchEvent(new KeyboardEvent("keydown", { key: " ", bubbles: true, cancelable: true }));
    assert(ordinary.defaultPrevented && calls.join(",") === boardHref(a) + "," + boardHref(b), "Board navigation bypassed delegated gate");
    const arrow = new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true }); editor.dispatchEvent(arrow);
    assert(!arrow.defaultPrevented && document.activeElement === editor, "Sidebar keyboard handler hijacked editor input");
    shell.setRouteStatus({ requestedPath: boardHref(b), activePath: boardHref(a), phase: "blocked" });
    assert(first.getAttribute("aria-current") === "page" && root.textContent?.includes("Navigation paused"), "Blocked navigation selected requested board");
    const ref = (item: WorkspaceCatalogEntry) => ({ projectKey: item.projectKey, boardKey: item.boardKey });
    shell.setPins({ state: "ready", pins: [ref(b), ref(a)] });
    const pinned = () => [...root.querySelectorAll<HTMLAnchorElement>(".workspace-pinned-list a[href]")];
    const other = () => [...root.querySelectorAll<HTMLAnchorElement>(".workspace-board-list:not(.workspace-pinned-list) a[href]")];
    assert(pinned()[0] === second && pinned()[1] === first && other().length === 1 && other()[0]!.pathname === boardHref(c), "Saved pin order or flat Other group incorrect");
    const down = second.closest("li")!.querySelector<HTMLButtonElement>(".workspace-pin-down")!;
    down.focus(); down.click();
    assert(pinChanges.at(-1)?.join() === [boardHref(a), boardHref(b)].join() && pinned()[0] === second && document.activeElement === down, "Move action changed projection before persistence or lost button focus");
    const key = new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true }); down.dispatchEvent(key);
    assert(down.type === "button" && !key.defaultPrevented, "Native reorder button keyboard input was hijacked");
    shell.setPins({ state: "saving", pins: [ref(b), ref(a)] });
    assert(down.disabled && root.textContent?.includes("Saving pin change") && document.activeElement === second, "Pending save claims saved state, allows duplicate edit or drops row focus");
    shell.setPins({ state: "failed", pins: [ref(b), ref(a)] });
    assert(pinned()[0] === second && root.textContent?.includes("was not saved"), "Failed save reset or invented pin order");
    shell.setPins({ state: "conflict", pins: [ref(a), ref(b)] });
    assert(pinned()[0] === first && root.textContent?.includes("another window") && document.activeElement === second, "Conflict did not expose current state or preserve same-row focus");
    root.querySelector<HTMLButtonElement>(".workspace-pin-retry")!.click();
    assert(calls.at(-1) === "review-pins" && pinChanges.length === 1, "Conflict retry replayed pin write");
    shell.setPins({ state: "unknown", pins: [ref(a), ref(b)] });
    assert(root.querySelector<HTMLButtonElement>(".workspace-pin-retry")!.hidden && root.textContent?.includes("result of the pin change is unknown"), "Unknown save offered an unsafe write retry");
    root.querySelector<HTMLButtonElement>(".workspace-pin-inspect")!.click();
    assert(calls.at(-1) === "inspect-pins", "Saved pin inspection is not deliberate");
    shell.setPins({ state: "ready", pins: [ref(a), ref(b)] });
    const unpin = first.closest("li")!.querySelector<HTMLButtonElement>(".workspace-pin")!; unpin.focus(); unpin.click();
    assert(pinChanges.at(-1)?.join() === boardHref(b), "Unpin did not emit exact public key references");
    shell.setPins({ state: "ready", pins: [ref(b)] });
    assert(other()[0] === first && document.activeElement === unpin && shell.editorRoot.firstChild === editor && editor.value === "Private unsaved input", "Cross-group pin update replaced focused row or editor");
    editor.focus(); editor.setSelectionRange(4, 7); shell.setPins({ state: "ready", pins: [ref(c), ref(b)] });
    assert(document.activeElement === editor && Number(editor.selectionStart) === 4 && first.getAttribute("aria-current") === "page", "Other-window pins stole editor focus, selection or active route");
    shell.setPins({ state: "ready", pins: [{ projectKey: "registered", boardKey: "absent" }] });
    const unavailable = root.querySelector<HTMLLIElement>(".workspace-pinned-list li")!;
    assert(unavailable.textContent?.includes("Pinned board unavailable") && !unavailable.querySelector("a[href]") && !unavailable.querySelector(".workspace-rfr")!.textContent, "Pin metadata granted navigation or board facts");
    root.querySelector<HTMLButtonElement>(".workspace-toggle")!.click();
    assert(changes[0]?.collapsed && root.querySelector<HTMLElement>(".workspace-sidebar")!.hidden, "Collapse did not persist through callback");
    shell.setCatalog({ phase: "unavailable", catalog: null, projects: null, receivedAt: null, refreshing: false });
    assert(root.querySelectorAll(".workspace-board-list a[href]").length === 0 && root.textContent?.includes("board list is unavailable"), "Authority loss retained catalog disclosure");
    shell.update({ state: "ready", catalog: catalog([]), projects: { ...projects, configRevision: 1 } });
    assert(!root.textContent?.includes("Empty project · No boards registered"), "Mismatched project revision invented empty membership");
    return 32;
  } finally { shell.destroy(); root.remove(); }
}

// Run with the real app stylesheet and allow Chromium's rendering/focus update.
export async function runWorkspacePinFocusProbe(): Promise<number> {
  const root = document.createElement("div"); document.body.append(root);
  const navigation: string[] = [];
  const shell = new WorkspaceShell(root, { navigate: path => { navigation.push(path); }, retry: () => {}, pinChanged: () => {} });
  const entries = Array.from({ length: 18 }, (_, index) => entry("harbor", "board-" + index, "Board " + index));
  const [a, b] = entries as [WorkspaceCatalogEntry, WorkspaceCatalogEntry, ...WorkspaceCatalogEntry[]];
  const ref = (value: WorkspaceCatalogEntry) => ({ projectKey: value.projectKey, boardKey: value.boardKey });
  const paint = async (): Promise<void> => { await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))); };
  try {
    shell.update({ state: "ready", catalog: { apiVersion: 1, workspaceId: "synthetic", configRevision: 2, observedAt: "2026-10-28T12:00:00Z", entries } });
    shell.setPins({ state: "ready", pins: [ref(a), ref(b)] });
    const sidebar = root.querySelector<HTMLElement>(".workspace-sidebar")!;
    sidebar.style.height = "240px"; sidebar.style.overflow = "auto";
    const link = (value: WorkspaceCatalogEntry) => [...root.querySelectorAll<HTMLAnchorElement>(".workspace-board-list a[href]")].find(candidate => candidate.pathname === boardHref(value))!;
    const aLink = link(a), bLink = link(b), up = bLink.closest("li")!.querySelector<HTMLButtonElement>(".workspace-pin-up")!;
    up.focus(); sidebar.scrollTop = 60; const before = sidebar.scrollTop;
    shell.setPins({ state: "ready", pins: [ref(a)] }); await paint();
    assert(up.hidden && document.activeElement === bLink && sidebar.scrollTop === before, "Remote unpin lost same-row focus after CSS rendering or moved sidebar scroll");
    const pin = aLink.closest("li")!.querySelector<HTMLButtonElement>(".workspace-pin")!;
    pin.focus(); sidebar.scrollTop = 40; const pendingScroll = sidebar.scrollTop;
    shell.setPins({ state: "saving", pins: [ref(a)] }); await paint();
    assert(pin.disabled && document.activeElement === aLink && sidebar.scrollTop === pendingScroll, "Pending save dropped disabled-control focus after rendering or moved sidebar scroll");
    shell.setPins({ state: "ready", pins: [ref(a), ref(b)] });
    const unpin = bLink.closest("li")!.querySelector<HTMLButtonElement>(".workspace-pin")!;
    unpin.focus(); shell.setPins({ state: "ready", pins: [ref(b), ref(a)] }); await paint();
    assert(document.activeElement === unpin, "Saved order update replaced a usable focused control");
    const editor = document.createElement("textarea"); shell.editorRoot.append(editor); shell.setRoute(boardHref(a)); editor.focus(); editor.value = "Private input"; editor.setSelectionRange(2, 5);
    shell.setPins({ state: "ready", pins: [ref(a)] }); await paint();
    assert(document.activeElement === editor && editor.selectionStart === 2 && navigation.length === 0, "Pin focus fallback stole editor focus or navigated");
    return 4;
  } finally { shell.destroy(); root.remove(); }
}
