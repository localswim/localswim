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
  const shell = new WorkspaceShell(root, { navigate: path => { calls.push(path); }, retry: () => { calls.push("retry"); }, layoutChanged: value => changes.push(value) });
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
    root.querySelector<HTMLButtonElement>(".workspace-toggle")!.click();
    assert(changes[0]?.collapsed && root.querySelector<HTMLElement>(".workspace-sidebar")!.hidden, "Collapse did not persist through callback");
    shell.setCatalog({ phase: "unavailable", catalog: null, projects: null, receivedAt: null, refreshing: false });
    assert(root.querySelectorAll(".workspace-board-list a").length === 0 && root.textContent?.includes("board list is unavailable"), "Authority loss retained catalog disclosure");
    shell.update({ state: "ready", catalog: catalog([]), projects: { ...projects, configRevision: 1 } });
    assert(!root.textContent?.includes("Empty project · No boards registered"), "Mismatched project revision invented empty membership");
    return 18;
  } finally { shell.destroy(); root.remove(); }
}
