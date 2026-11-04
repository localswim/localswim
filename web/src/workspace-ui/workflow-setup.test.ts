import type { WorkspaceControlResult, WorkspaceProjects } from "../../generated/transport/contracts.js";
import type { SetupStore, SetupRecord, SetupValue } from "../recovery/workflow-setup.js";
import { WorkflowSetup } from "./workflow-setup.js";
import { setupRequest } from "../api/configured-workflow.test.js";

function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
function memoryStore(value: SetupValue): SetupStore & { peek(): SetupRecord | undefined; fail: boolean } {
  let record: SetupRecord | undefined = { key: "synthetic", revision: 1, value: structuredClone(value) };
  const store = { fail: false, peek: () => structuredClone(record), close() {}, async read() { return structuredClone(record); },
    async write(revision: number, next: SetupValue) { if (store.fail || revision !== (record?.revision ?? 0)) throw Error("storage unavailable"); record = { key: "synthetic", revision: revision + 1, value: structuredClone(next) }; return structuredClone(record); },
    async clear(revision: number) { if (store.fail || revision !== record?.revision) throw Error("storage unavailable"); record = undefined; } };
  return store;
}
export async function runWorkflowSetupTests(): Promise<number> {
  let passed = 0;
  const request = setupRequest(), { apiVersion: _, workspaceId: __, expectedConfigRevision: ___, ...draft } = request;
  const projects: WorkspaceProjects = { apiVersion: 1, workspaceId: request.workspaceId, configRevision: 11, projects: [{ key: draft.projectKey, label: "Harbor" }] };
  const result: WorkspaceControlResult = { apiVersion: 1, workspaceId: request.workspaceId, projectKey: draft.projectKey, boardKey: draft.boardKey, configRevision: 12, status: "registered", identity: { boardId: crypto.randomUUID(), installationId: crypto.randomUUID(), incarnation: crypto.randomUUID() } };
  const root = document.createElement("section"), opener = document.createElement("button"); root.append(opener); document.body.append(root);
  const store = memoryStore({ draft }); let creates = 0, statuses = 0, navigations = 0, submittedRevision = 0;
  const setup = new WorkflowSetup(root, { workspaceId: request.workspaceId, windowId: crypto.randomUUID(), store, current: () => true, projects: () => projects,
    fallback: () => opener, refresh() {}, confirm: () => true,
    navigate: async (_path, identity) => { assert(identity.boardId === result.identity?.boardId, "Open lost expected identity"); navigations++; return true; },
    client: { dispose() {}, async create(sent) { creates++; submittedRevision = sent.expectedConfigRevision; throw Error("lost reply"); },
      async status(sent) { statuses++; assert(JSON.stringify(sent.workflow) === JSON.stringify(request.workflow), "Status changed full submitted definition"); return result; } } });
  try {
    await setup.show(opener); assert(creates === 0 && statuses === 0, "Restore triggered network setup"); passed++;
    root.querySelector<HTMLButtonElement>(".workflow-preview-button")!.click();
    (projects as { configRevision: number }).configRevision = 22;
    await setup.create(draft);
    assert(Number(creates) === 1 && Number(submittedRevision) === 11 && store.peek()?.value.original === "unknown" && Number(navigations) === 0, "Preview revision, UNKNOWN or explicit navigation failed"); passed++;
    await setup.create(draft); assert(Number(creates) === 1, "UNKNOWN replayed create"); passed++;
    await setup.checkStatus(); assert(Number(statuses) === 1 && store.peek()?.value.original === "unknown" && store.peek()?.value.observation?.status === "registered", "Status settled original or was not stored"); passed++;
    await setup.close(); assert(document.activeElement === opener, "Exact opener not restored"); passed++;
    await setup.show(opener); assert(Number(statuses) === 1, "Reopen looked up automatically"); passed++;
    await setup.discard(); assert(!store.peek() && Number(creates) === 1, "Explicit discard mutated server"); passed++;
  } finally { setup.destroy(); root.remove(); }
  const failureRoot = document.createElement("section"), failureOpener = document.createElement("button"); failureRoot.append(failureOpener); document.body.append(failureRoot);
  const failing = memoryStore({ draft }); failing.fail = true; let attempted = false;
  const blocked = new WorkflowSetup(failureRoot, { workspaceId: request.workspaceId, windowId: crypto.randomUUID(), store: failing, current: () => true, projects: () => projects,
    fallback: () => failureOpener, refresh() {}, confirm: () => true, navigate: async () => false,
    client: { dispose() {}, async create() { attempted = true; return result; }, async status() { return result; } } });
  try {
    await blocked.show(failureOpener); failureRoot.querySelector<HTMLButtonElement>(".workflow-preview-button")!.click(); await blocked.create(draft);
    assert(!attempted && !blocked.canLeave, "Failed private save dispatched or allowed destructive navigation"); passed++;
  } finally { blocked.destroy(); failureRoot.remove(); }
  const unavailableRoot = document.createElement("section"), unavailableOpener = document.createElement("button"); unavailableRoot.append(unavailableOpener); document.body.append(unavailableRoot);
  const unavailableStore = memoryStore({ draft }); let unavailableCreate = 0;
  const unavailable = new WorkflowSetup(unavailableRoot, { workspaceId: request.workspaceId, windowId: crypto.randomUUID(), store: unavailableStore,
    current: () => true, windowReady: () => false, projects: () => projects, fallback: () => unavailableOpener, refresh() {}, confirm: () => true, navigate: async () => false,
    client: { dispose() {}, async create() { unavailableCreate++; return result; }, async status() { return result; } } });
  try {
    await unavailable.show(unavailableOpener);
    assert(!unavailableRoot.querySelector<HTMLElement>(".workflow-setup")!.hidden && unavailableRoot.textContent?.includes("Private window storage is unavailable"), "Window storage failure silently hid setup");
    unavailableRoot.querySelector<HTMLButtonElement>(".workflow-preview-button")!.click();
    const create = [...unavailableRoot.querySelectorAll("button")].find(button => button.textContent === "Create board")!;
    assert(create.disabled, "Unverified window enabled Create");
    await unavailable.create(draft); assert(unavailableCreate === 0, "Unverified window dispatched Create"); passed++;
  } finally { unavailable.destroy(); unavailableRoot.remove(); }
  const delayedRoot = document.createElement("section"), oldOpener = document.createElement("button"), newOpener = document.createElement("button"); delayedRoot.append(oldOpener, newOpener); document.body.append(delayedRoot);
  let release: ((value: boolean) => void) | undefined, started: (() => void) | undefined;
  const navigationStarted = new Promise<void>(resolve => { started = resolve; });
  const delayed = new WorkflowSetup(delayedRoot, { workspaceId: request.workspaceId, windowId: crypto.randomUUID(), store: memoryStore({ draft, submitted: request, original: "unknown", observation: result }),
    current: () => true, projects: () => projects, fallback: () => newOpener, refresh() {}, confirm: () => true,
    navigate: async () => { started!(); return new Promise<boolean>(resolve => { release = resolve; }); },
    client: { dispose() {}, async create() { throw Error("unexpected create"); }, async status() { throw Error("unexpected status"); } } });
  try {
    await delayed.show(oldOpener);
    [...delayedRoot.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent === "Open registered board")!.click();
    await navigationStarted; await delayed.show(newOpener);
    const focused = document.activeElement; release!(true);
    await new Promise<void>(resolve => setTimeout(resolve, 0));
    assert(!delayedRoot.querySelector<HTMLElement>(".workflow-setup")!.hidden && document.activeElement === focused && document.activeElement !== oldOpener, "Old Open completion closed or refocused the new setup view"); passed++;
  } finally { delayed.destroy(); delayedRoot.remove(); }
  const savingRoot = document.createElement("section"), savingOpener = document.createElement("button"); savingRoot.append(savingOpener); document.body.append(savingRoot);
  const saved = memoryStore({ draft }); let releaseSave: (() => void) | undefined;
  const waitingStore: SetupStore = { ...saved, async write(revision, value) {
    await new Promise<void>(resolve => { releaseSave = resolve; }); return saved.write(revision, value);
  } };
  const saving = new WorkflowSetup(savingRoot, { workspaceId: request.workspaceId, windowId: crypto.randomUUID(), store: waitingStore,
    current: () => true, projects: () => projects, fallback: () => savingOpener, refresh() {}, confirm: () => true, navigate: async () => false,
    client: { dispose() {}, async create() { throw Error("unexpected create"); }, async status() { throw Error("unexpected status"); } } });
  try {
    await saving.show(savingOpener); savingRoot.querySelector<HTMLButtonElement>(".workflow-preview-button")!.click();
    const create = [...savingRoot.querySelectorAll("button")].find(button => button.textContent === "Create board")!;
    assert(create.disabled && savingRoot.querySelector('[role="status"]')?.textContent === "Saving private setup draft…" && !savingRoot.querySelector("fieldset")!.disabled,
      "Pending private save lacked accessible feedback or disabled editable fields");
    await Promise.resolve(); releaseSave!();
    await new Promise<void>(resolve => setTimeout(resolve, 0));
    assert(!create.disabled && saving.canLeave && !savingRoot.textContent?.includes("Saving private setup draft…"), "Readback completion did not clear saving state"); passed++;
  } finally { saving.destroy(); savingRoot.remove(); }
  return passed;
}
