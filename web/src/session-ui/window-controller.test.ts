// Deterministic synthetic supplements. These do not qualify a product profile.
import type { BoardRead, SessionRead, WorkspaceResolve } from "../../generated/transport/contracts.js";
import { mountSyntheticBoard } from "../board/view.test.js";
import { WindowBoardController } from "./window-controller.js";
import { WindowState } from "./window-state.js";
import type { EditorReference } from "./window-state.js";
import { BoardView } from "../board/view.js";
import { RecoveryView } from "../recovery-ui/view.js";
import { ArchiveView } from "../archive-ui/view.js";

class MemoryStorage implements Storage {
  private readonly entries = new Map<string, string>();
  fail = false;
  get length(): number { return this.entries.size; }
  clear(): void { this.entries.clear(); }
  getItem(key: string): string | null { return this.entries.get(key) ?? null; }
  key(index: number): string | null { return [...this.entries.keys()][index] ?? null; }
  removeItem(key: string): void { this.entries.delete(key); }
  setItem(key: string, value: string): void { if (this.fail) throw Error("Synthetic storage refusal"); this.entries.set(key, value); }
}

export function runWindowIdentityProbe(): number {
  const storage = new MemoryStorage(), first = new WindowState(storage, "navigate");
  if ("filter" in JSON.parse(storage.getItem("localswim.window.v1")!)) throw Error("Empty filter changed default privacy shape");
  first.setSidebarFilter("harbor");
  const reload = new WindowState(storage, "reload");
  if (reload.sidebarFilter !== "harbor") throw Error("Reload lost sidebar filter");
  if (first.id !== reload.id) throw Error("Reload replaced window identity");
  const cloned = new MemoryStorage();
  for (let i = 0; i < storage.length; i++) { const key = storage.key(i)!; cloned.setItem(key, storage.getItem(key)!); }
  const newWindow = new WindowState(cloned, "navigate");
  if (newWindow.sidebarFilter !== "") throw Error("New window adopted opener filter");
  first.setSidebarFilter("x".repeat(300));
  if (first.sidebarFilter.length !== 200 || new WindowState(storage, "back_forward").sidebarFilter.length !== 200) throw Error("Filter bound was not retained");
  first.setSidebarFilter("");
  if ("filter" in JSON.parse(storage.getItem("localswim.window.v1")!)) throw Error("Cleared filter retained privacy field");
  if (newWindow.id === first.id) throw Error("New window adopted opener identity");
  storage.fail = true;
  const refusing = new WindowState(storage, "reload");
  let refused = false;
  try { refusing.put("synthetic", {}); } catch { refused = true; }
  if (!refused) throw Error("Unavailable window storage claimed a save");
  refusing.setSidebarFilter("memory only");
  if (refusing.sidebarFilter !== "memory only") throw Error("Denied storage disabled filter");
  return 9;
}

export async function runPrivateReloadProbe(boardInput: BoardRead, sessionInput: SessionRead): Promise<number> {
  const fixture = await mountSyntheticBoard(boardInput, sessionInput); fixture.view.destroy();
  const boardRoot = document.createElement("section"), recoveryRoot = document.createElement("section"); document.body.replaceChildren(boardRoot, recoveryRoot);
  let editor: EditorReference | undefined, recovery: EditorReference | undefined;
  let board = new BoardView(boardRoot, fixture.client, fixture.board.result.identity.boardId, ref => { editor = ref; });
  let view = new RecoveryView(recoveryRoot, fixture.client, () => {}, ref => { recovery = ref; });
  try {
    await board.open();
    [...boardRoot.querySelectorAll<HTMLButtonElement>("button")].find(n => n.textContent === "Start work")!.click();
    await board.flushPrivateInput();
    if (!editor) throw Error("Start work editor reference missing");
    board.destroy(); board = new BoardView(boardRoot, fixture.client, fixture.board.result.identity.boardId, ref => { editor = ref; });
    await board.open(); await board.restore(editor);
    if (boardRoot.querySelector<HTMLSelectElement>('select[name="lane"]')?.value !== "In Progress" || boardRoot.querySelector('input[name="title"]')) throw Error("Reload changed Start work editor shape");
    await board.flushPrivateInput(); await view.open();
    [...recoveryRoot.querySelectorAll<HTMLButtonElement>("button")].find(n => n.textContent === "View retained draft")!.click();
    for (let i = 0; i < 250 && !recoveryRoot.querySelector("textarea"); i++) await new Promise(resolve => setTimeout(resolve, 20));
    const input = recoveryRoot.querySelector<HTMLTextAreaElement>("textarea")!;
    input.value = "Own retained recovery input"; input.dispatchEvent(new Event("input", { bubbles: true })); await view.flushPrivateInput();
    if (!recovery || recovery.draftId === editor.draftId) throw Error("Private selection adopted shared writable draft");
    view.destroy(); view = new RecoveryView(recoveryRoot, fixture.client, () => {}, ref => { recovery = ref; });
    await view.open(); await view.restore(recovery);
    if (recoveryRoot.querySelector<HTMLTextAreaElement>("textarea")?.value !== "Own retained recovery input") throw Error("Reload lost own private draft selection/input");
    if (fixture.state.mutations || fixture.state.lookups) throw Error("Private reload dispatched or looked up an original");
    return 4;
  } finally { board.destroy(); view.destroy(); fixture.client.close(); }
}

export async function runAttachmentLifetimeProbe(boardInput: BoardRead, sessionInput: SessionRead): Promise<number> {
  const fixture = await mountSyntheticBoard(boardInput, sessionInput); fixture.view.destroy();
  const root = document.createElement("main"); document.body.replaceChildren(root);
  const view = new ArchiveView(root, fixture.client, fixture.board.result.identity.boardId);
  const button = (text: string): HTMLButtonElement => [...root.querySelectorAll("button")].find(n => n.textContent === text)!;
  try {
    await view.refresh(); button("Open attachment editor").click(); await view.flushPrivateInput();
    if (view.canLeave) throw Error("Visible attachment editor without own form hydration allowed destructive navigation");
    const name = root.querySelector<HTMLInputElement>('form input:not([type="file"])')!;
    const saveDraft = fixture.client.recovery.saveDraft.bind(fixture.client.recovery);
    fixture.client.recovery.saveDraft = async () => { throw Error("Synthetic recovery refusal"); };
    name.value = "Retain attachment metadata"; name.dispatchEvent(new Event("input", { bubbles: true })); await view.flushPrivateInput();
    if (view.canLeave || !name.isConnected || name.value !== "Retain attachment metadata") throw Error("Attachment metadata refusal lost input or allowed navigation");
    button("Open attachment editor").click();
    if (root.querySelector('form input:not([type="file"])') !== name) throw Error("Attachment edit replaced failed input");
    view.discardInput(); if (!view.canLeave) throw Error("Attachment explicit discard did not release guard");
    fixture.client.recovery.saveDraft = saveDraft;
    button("Open attachment editor").click();
    const file = root.querySelector<HTMLInputElement>('input[type="file"]')!, transfer = new DataTransfer();
    transfer.items.add(new File(["synthetic only"], "synthetic.txt", { type: "text/plain" })); file.files = transfer.files; file.dispatchEvent(new Event("change", { bubbles: true }));
    await view.flushPrivateInput();
    if (view.canLeave) throw Error("Memory-only unsubmitted file selection allowed destructive navigation");
    view.suspend(); root.remove(); document.body.append(root); view.resume();
    if (file !== root.querySelector('input[type="file"]') || file.files?.[0]?.name !== "synthetic.txt") throw Error("Attachment suspension replaced file input or File selection");
    view.discardInput(); if (!view.canLeave || fixture.state.mutations) throw Error("Attachment discard failed or dispatched a mutation");
    return 7;
  } finally { view.destroy(); fixture.client.close(); }
}

export async function runPendingReloadProbe(boardInput: BoardRead, sessionInput: SessionRead): Promise<number> {
  const fixture = await mountSyntheticBoard(boardInput, sessionInput); fixture.view.destroy();
  const root = document.createElement("main"); document.body.replaceChildren(root);
  let reference: EditorReference | undefined;
  let view = new BoardView(root, fixture.client, fixture.board.result.identity.boardId, value => { reference = value; });
  const button = (text: string): HTMLButtonElement => [...root.querySelectorAll("button")].find(n => n.textContent === text)!;
  try {
    await view.open(); button("Create card").click();
    const title = root.querySelector<HTMLInputElement>('input[name="title"]')!;
    title.value = "Retained unknown operation"; title.dispatchEvent(new Event("input", { bubbles: true })); await view.flushPrivateInput();
    fixture.state.mode = "late"; button("Save").click();
    for (let i = 0; i < 250 && !fixture.state.releases.length; i++) await new Promise(resolve => setTimeout(resolve, 20));
    if (!reference || fixture.state.releases.length !== 1) throw Error("Pending reload fixture did not dispatch once");
    view.destroy(); view = new BoardView(root, fixture.client, fixture.board.result.identity.boardId);
    await view.open(); await view.restore(reference);
    if (!button("Save").disabled) throw Error("Reload enabled replacement submission for unresolved original");
    if (!root.textContent!.includes("Outcome unknown") || !button("Look up original outcome")) throw Error("Reload lost original identity lookup control");
    if (fixture.state.lookups || fixture.state.mutations !== 1) throw Error("Reload looked up or replayed an original");
    fixture.state.releases[0]!();
    return 3;
  } finally { view.destroy(); fixture.client.close(); }
}

export async function runPrivateWindowProbe(boardA: BoardRead, sessionA: SessionRead, boardB: BoardRead, sessionB: SessionRead): Promise<number> {
  if (boardA.result.identity.boardId === boardB.result.identity.boardId) throw Error("Probe requires distinct boards");
  const databaseName = "m01-private-synthetic-" + crypto.randomUUID();
  const a = await mountSyntheticBoard(boardA, sessionA, true, databaseName); a.view.destroy();
  const b = await mountSyntheticBoard(boardB, sessionB, true, databaseName); b.view.destroy();
  const storage = new MemoryStorage(), host = document.createElement("main"); document.body.replaceChildren(host);
  const access = (fixture: typeof a) => ({ client: fixture.client, initial: fixture.auth, recoveryReady: true, clear(): void {},
    onUnavailable(): () => void { return () => {}; }, reconnect: () => fixture.client.session() });
  const connection = {
    async resolve(projectKey: string, boardKey: string): Promise<WorkspaceResolve> {
      const fixture = boardKey === "a" ? a : b;
      return { apiVersion: 1, workspaceId: "00000000-0000-4000-8000-000000000091", configRevision: 1,
        member: { projectKey, boardKey, label: boardKey, availability: "ready", memberEpoch: "00000000-0000-4000-8000-000000000092",
          identity: { installationId: "00000000-0000-4000-8000-000000000090", ...fixture.board.result.identity } } };
    },
    async openBoard(selected: WorkspaceResolve) { return access(selected.member.boardKey === "a" ? a : b); },
  };
  const controller = new WindowBoardController(host, connection, new WindowState(storage, "navigate"));
  let commits = 0; controller.onCommitted(() => { commits++; });
  const settle = async (test: () => boolean): Promise<void> => {
    for (let i = 0; i < 250; i++) { if (test()) return; await new Promise(resolve => setTimeout(resolve, 20)); }
    throw Error("Private view probe did not settle");
  };
  const button = (text: string): HTMLButtonElement => [...host.querySelectorAll("button")].find(n => n.textContent === text)!;
  const edit = async (text: string): Promise<HTMLInputElement> => {
    await settle(() => host.textContent!.includes("Current at board revision")); button("Create card").click();
    const input = host.querySelector<HTMLInputElement>('input[name="title"]')!;
    input.value = text; input.dispatchEvent(new Event("input", { bubbles: true })); input.focus(); input.setSelectionRange(2, 5);
    await controller.flushPrivateInput(); return input;
  };
  let checks = runWindowIdentityProbe();
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  try {
    await controller.navigate("/board/test/a", false);
    const original = await edit("A retained private title");
    await controller.navigate("/board/test/b", false);
    check(!original.isConnected && !host.textContent!.includes("A retained private title"), "A input painted B");
    await controller.navigate("/board/test/a", false);
    check(original === host.querySelector('input[name="title"]') && original.value === "A retained private title", "Switch remounted or lost editor input");
    check(document.activeElement === original && original.selectionStart === 2 && original.selectionEnd === 5, "Switch lost focus or text selection");
    a.state.mode = "late"; button("Save").click();
    await settle(() => a.state.releases.length === 1);
    check(commits === 0, "Pending optimistic save triggered committed refresh");
    await controller.navigate("/board/test/b", false);
    a.state.releases[0]!();
    await settle(() => a.state.terminals.size === 1);
    await settle(() => commits === 1);
    check(commits === 1, "Validated detached A commit did not trigger exactly one workspace refresh");
    await new Promise(resolve => setTimeout(resolve, 50));
    check(!host.textContent!.includes("Confirmed saved.") && !host.querySelector('input[name="title"]'), "Late A completion painted B");
    await controller.navigate("/board/test/a", false);
    check(original === host.querySelector('input[name="title"]') && original.value === "A retained private title", "Late completion changed original editor");
    check(a.state.mutations === 1 && a.state.lookups === 0 && b.state.mutations === 0, "Navigation replayed or looked up an original");
    await controller.navigate("/board/test/b", false);
    storage.fail = true;
    const memory = await edit("Retain when window storage refuses");
    check(!controller.canLeave, "Failed private persistence permitted destructive navigation");
    check(!await controller.navigate("/board/test/a", false) && memory.isConnected && memory.value === "Retain when window storage refuses", "Failed persistence lost memory input");
    controller.discardActiveInput();
    check(await controller.navigate("/board/test/a", false), "Explicit discard did not release navigation guard");
    return checks;
  } finally { controller.destroy(); }
}


// Route state and browser history are exercised with actual retained DOM/IDB.
export async function runWorkspaceNavigationProbe(boardA: BoardRead, sessionA: SessionRead, boardB: BoardRead, sessionB: SessionRead): Promise<number> {
  const a = await mountSyntheticBoard(boardA, sessionA), b = await mountSyntheticBoard(boardB, sessionB); a.view.destroy(); b.view.destroy();
  const host = document.createElement("main"); document.body.replaceChildren(host);
  const storage = new MemoryStorage();
  const state = new WindowState(storage, "navigate");
  state.setSidebarLayout({ collapsed: true, width: 900 });
  let checks = 0;
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  check(state.sidebarLayout.collapsed && state.sidebarLayout.width === 420, "Layout bound was not enforced");
  const reloaded = new WindowState(storage, "reload");
  check(reloaded.id === state.id && reloaded.sidebarLayout.collapsed && reloaded.sidebarLayout.width === 420, "Reload lost window layout/identity");
  check(!new WindowState(storage, "navigate").sidebarLayout.collapsed, "New window adopted opener layout");
  storage.fail = true; state.setSidebarLayout({ collapsed: false, width: 1 });
  check(!state.sidebarLayout.collapsed && state.sidebarLayout.width === 220, "Denied layout persistence disabled memory UI"); storage.fail = false;
  let release: (() => void) | undefined, waiting = false;
  const connection = {
    async resolve(projectKey: string, boardKey: string): Promise<WorkspaceResolve> {
      if (boardKey === "slow") { waiting = true; await new Promise<void>(resolve => { release = resolve; }); throw Error("Late unavailable response"); }
      if (boardKey === "unknown") throw Error("Unregistered selected board");
      const fixture = boardKey === "a" ? a : b;
      return { apiVersion: 1, workspaceId: "00000000-0000-4000-8000-000000000091", configRevision: 1, member: {
        projectKey, boardKey, label: boardKey, availability: "ready", memberEpoch: "00000000-0000-4000-8000-000000000092",
        identity: { installationId: "00000000-0000-4000-8000-000000000090", ...fixture.board.result.identity } } };
    },
    async openBoard(selected: WorkspaceResolve) {
      const fixture = selected.member.boardKey === "a" ? a : b;
      return { client: fixture.client, initial: fixture.auth, recoveryReady: true, clear(): void {}, onUnavailable(): () => void { return () => {}; }, reconnect: () => fixture.client.session() };
    },
  };
  const oldState = history.state, oldURL = location.href;
  const traverse = async (direction: "back" | "forward"): Promise<void> => {
    const event = new Promise<void>(resolve => window.addEventListener("popstate", () => resolve(), { once: true })); history[direction](); await event;
  };
  let controller = new WindowBoardController(host, connection, state);
  const settle = async (test: () => boolean): Promise<void> => { for (let i = 0; i < 200; i++) { if (test()) return; await new Promise(resolve => setTimeout(resolve, 5)); } throw Error("Navigation did not settle"); };
  try {
    check(await controller.navigate("/"), "Root chooser navigation refused");
    check(host.childElementCount === 0 && controller.status.activePath === "/", "Root selected a default board");
    await controller.navigate("/board/test/a");
    const aRoot = host.firstElementChild;
    const pending = controller.navigate("/board/test/slow"); await settle(() => waiting);
    check(controller.status.phase === "loading" && controller.status.requestedPath === "/board/test/slow" && controller.status.activePath === "/board/test/a" && host.firstElementChild === aRoot, "Loading replaced active view or hid requested route");
    await controller.navigate("/board/test/b"); const bRoot = host.firstElementChild; release!();
    check(!await pending && controller.status.activePath === "/board/test/b" && host.firstElementChild === bRoot && location.pathname === "/board/test/b", "Late old navigation repainted or changed history");
    await controller.navigate("/board/test/unknown");
    check(controller.status.phase === "unavailable" && controller.status.activePath === "/board/test/unknown" && host.textContent!.includes("Selected board unavailable"), "Unknown board fell back to a healthy board");
    await controller.navigate("/board/test/a");
    check(host.firstElementChild === aRoot, "Unavailable route destroyed retained A view");
    await controller.navigate("/bad/route");
    check(controller.status.phase === "unavailable" && controller.status.activePath === "/bad/route", "Malformed route selected a board or threw outside shell");
    await controller.navigate("/board/test/a"); await controller.navigate("/board/test/b");
    const originalFlush = controller.flushPrivateInput.bind(controller); controller.flushPrivateInput = async () => false;
    check(!await controller.navigate("/") && controller.status.phase === "blocked" && controller.status.activePath === "/board/test/b" && host.firstElementChild === bRoot && location.pathname === "/board/test/b", "Blocked root navigation detached private view");
    await traverse("back"); await settle(() => controller.status.phase === "blocked" && location.pathname === "/board/test/b");
    check(host.firstElementChild === bRoot, "Denied Back lost retained view");
    controller.flushPrivateInput = originalFlush;
    await traverse("back"); await settle(() => controller.status.activePath === "/board/test/a");
    check(host.firstElementChild === aRoot, "Back did not restore actual A DOM");
    await traverse("forward"); await settle(() => controller.status.activePath === "/board/test/b");
    check(host.firstElementChild === bRoot, "Forward did not restore actual B DOM");
    // Recreate only the controller lifetime to model a same-window reload. The
    // original history index must survive even while private persistence refuses.
    controller.destroy(); controller = new WindowBoardController(host, connection, new WindowState(storage, "reload"));
    await controller.navigate(location.pathname, false); controller.flushPrivateInput = async () => false;
    await traverse("back"); await settle(() => controller.status.phase === "blocked" && location.pathname === "/board/test/b");
    check(controller.windowId === state.id && controller.status.activePath === "/board/test/b", "Reload history bypassed private navigation guard");
    check(a.state.mutations === 0 && b.state.mutations === 0 && a.state.lookups === 0 && b.state.lookups === 0, "Route navigation submitted or looked up an original");
    return checks;
  } finally { release?.(); controller.destroy(); history.replaceState(oldState, "", oldURL); }
}
