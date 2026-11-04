import type { ConfiguredWorkspaceBoardRequest, WorkspaceProjects, WorkspaceControlResult, WorkspaceIdentity } from "../../generated/transport/contracts.js";
import { ConfiguredWorkflowRefusal, type ConfiguredWorkflowClient } from "../api/configured-workflow.js";
import { parse, validate } from "../api/boundary.js";
import type { SetupRecord, SetupStore, SetupValue } from "../recovery/workflow-setup.js";
import { WorkflowEditor, createWorkflowDraft, type WorkflowDraft } from "./workflow-editor.js";

export type WorkflowSetupOptions = Readonly<{
  workspaceId: string; windowId: string; client: ConfiguredWorkflowClient; store: SetupStore;
  current(): boolean; windowReady?(): boolean; projects(): WorkspaceProjects | undefined;
  navigate(path: string, expectedIdentity: WorkspaceIdentity): Promise<unknown>; refresh(): void; fallback(): HTMLElement;
  confirm(message: string): boolean;
}>;
const unknownMessage = "Creation outcome is UNKNOWN. Create will not be sent again. Check status deliberately; a current status does not settle the original outcome.";

// Workspace setup has two boundaries (initialize, register), not a card command.
// The immutable submitted request survives uncertain responses without replay.
export class WorkflowSetup {
  private record?: SetupRecord;
  private value?: SetupValue;
  private editor?: WorkflowEditor;
  private opener?: HTMLElement;
  private readonly panel = document.createElement("section");
  private readonly editorRoot = document.createElement("div");
  private readonly openButton = document.createElement("button");
  private readonly notice = document.createElement("p");
  private readonly closeButton = document.createElement("button");
  private readonly retrySave = document.createElement("button");
  private queue: Promise<void> = Promise.resolve();
  private saving = 0;
  private busy = false;
  private opening = false;
  private failed = false;
  private alive = true;
  private previewRevision?: number;
  private viewGeneration = 0;
  readonly ready: Promise<void>;
  constructor(root: HTMLElement, private readonly options: WorkflowSetupOptions) {
    this.panel.className = "workflow-setup"; this.panel.hidden = true;
    this.panel.setAttribute("aria-label", "New configured board");
    this.notice.setAttribute("role", "status"); this.openButton.type = this.closeButton.type = "button";
    this.openButton.textContent = "Open registered board"; this.openButton.hidden = true;
    this.closeButton.textContent = "Keep draft and close";
    this.retrySave.type = "button"; this.retrySave.textContent = "Retry private save";
    this.retrySave.addEventListener("click", () => { void this.retryPrivateSave(); });
    this.openButton.addEventListener("click", () => { void this.openRegistered(); });
    this.closeButton.addEventListener("click", () => { void this.close(); });
    this.panel.append(this.editorRoot, this.notice, this.openButton, this.retrySave, this.closeButton); root.append(this.panel);
    this.ready = options.store.read().then(record => { if (!this.live()) return; this.record = record; this.value = record?.value; }, () => { this.failed = true; });
    window.addEventListener("beforeunload", this.beforeUnload);
  }
  private live(): boolean { return this.alive && this.options.current(); }
  private windowReady(): boolean { try { return this.options.windowReady?.() ?? true; } catch { return false; } }
  get canLeave(): boolean { return !this.busy && !this.saving && (!this.value || (!this.failed && this.windowReady())); }
  private readonly beforeUnload = (event: BeforeUnloadEvent): void => { if (!this.canLeave) { event.preventDefault(); event.returnValue = ""; } };
  async show(opener: HTMLElement): Promise<void> {
    const generation = ++this.viewGeneration;
    await this.ready; if (!this.live() || this.busy || generation !== this.viewGeneration) return;
    const projects = this.options.projects();
    if (!projects || projects.workspaceId !== this.options.workspaceId || !projects.projects.length) return;
    this.opener = opener;
    if (!this.value) this.value = { draft: createWorkflowDraft(projects.projects[0]!.key) };
    this.editor?.destroy();
    this.editor = new WorkflowEditor(this.editorRoot, { draft: structuredClone(this.value.draft), projects: projects.projects,
      change: draft => { if (!this.live() || this.value?.submitted || this.busy) return; this.previewRevision = undefined; this.value = { draft }; this.persist(this.value); },
      preview: () => { const metadata = this.options.projects(); this.previewRevision = metadata?.workspaceId === this.options.workspaceId ? metadata.configRevision : undefined; },
      submit: draft => { void this.create(draft); }, cancel: () => { void this.discard(); }, status: () => { void this.checkStatus(); } });
    this.panel.hidden = false;
    this.render();
    this.editorRoot.querySelector<HTMLElement>("input,select,button")?.focus();
  }
  private render(message?: string): void {
    if (!this.live()) return;
    const original = this.value?.original;
    const observed = this.value?.observation ?? this.value?.result;
    const description = original === "registered" ? "Board registered. Open it deliberately when ready." : original === "refused" ? "Creation was refused. Your input is retained. Discard this attempt explicitly before preparing a new request." : this.value?.submitted ? unknownMessage : "Draft is private to this workspace and window.";
    const windowReady = this.windowReady();
    this.editor?.setState({ busy: this.busy, locked: !!this.value?.submitted, canCreate: windowReady && !this.failed && this.saving === 0, canCheckStatus: windowReady && !!this.value?.submitted && !this.busy,
      message: !windowReady ? "Private window storage is unavailable or unverified. Setup remains visible, but Create is disabled. Input cannot be safely saved until private window storage is available." : this.failed ? "Private setup storage is unavailable or unverified. Input is retained; creation and navigation are blocked." : message ?? (!this.value?.submitted && this.saving > 0 ? "Saving private setup draft…" : description) });
    this.notice.textContent = observed ? "Current observation: " + observed.status + (observed.status === "initialized-unregistered" ? ". Use the native CLI: inspect the initialized board, then explicitly register it with localswim workspace board register. Browser recovery does not register or recreate it." : "") : "";
    this.openButton.hidden = observed?.status !== "registered" || !observed.identity;
    this.openButton.disabled = !this.canLeave || !windowReady || this.opening;
    this.closeButton.disabled = !this.canLeave;
    this.retrySave.hidden = !this.failed; this.retrySave.disabled = this.busy || this.saving > 0;
  }
  private async retryPrivateSave(): Promise<void> {
    if (!this.live() || this.busy || this.saving || !this.value) return;
    try {
      const stored = await this.options.store.read(); if (!this.live()) return;
      if (stored && JSON.stringify(stored.value) === JSON.stringify(this.value)) { this.record = stored; this.failed = false; }
      else if ((stored?.revision ?? 0) === (this.record?.revision ?? 0)) await this.persist(this.value);
      // A different revision/value could belong to another context. Never overwrite it.
    } catch { this.failed = true; }
    this.render();
  }
  private persist(value: SetupValue): Promise<void> {
    const snapshot = structuredClone(value); this.saving++;
    this.render();
    const work = this.queue.then(async () => {
      if (!this.live() || !this.windowReady()) throw Error("Setup context or private window changed");
      const record = await this.options.store.write(this.record?.revision ?? 0, snapshot);
      if (!this.live()) return;
      this.record = record; this.failed = false;
    });
    this.queue = work.then(() => {}, () => { if (this.live()) this.failed = true; }).finally(() => { this.saving--; this.render(); });
    return this.queue;
  }
  async create(draft: WorkflowDraft): Promise<void> {
    if (!this.live() || this.busy || this.value?.submitted || this.previewRevision === undefined || !this.windowReady()) { this.render(); return; }
    const expectedConfigRevision = this.previewRevision;
    const request: ConfiguredWorkspaceBoardRequest = { apiVersion: 1, workspaceId: this.options.workspaceId, expectedConfigRevision,
      projectKey: draft.projectKey, boardKey: draft.boardKey, label: draft.label, workflow: structuredClone(draft.workflow) };
    try { validate("transport:ConfiguredWorkspaceBoardRequest", parse(JSON.stringify(request))); } catch { this.render("Review the bounded configuration fields before creating."); return; }
    this.busy = true; this.render();
    await this.queue;
    if (!this.live() || !this.windowReady() || this.failed) { this.busy = false; this.render(); return; }
    this.value = { draft: { projectKey: request.projectKey, boardKey: request.boardKey, label: request.label, workflow: request.workflow }, submitted: request, original: "pending" };
    await this.persist(this.value);
    if (!this.live() || !this.windowReady() || this.failed) { this.busy = false; this.render(); return; }
    try {
      const result = await this.options.client.create(request);
      if (!this.live()) return;
      this.value = { ...this.value, original: result.status === "registered" ? "registered" : "unknown", result, ...(result.identity ? { knownIdentity: result.identity } : {}) };
    } catch (error) {
      if (!this.live()) return;
      this.value = { ...this.value, original: error instanceof ConfiguredWorkflowRefusal ? "refused" : "unknown" };
    }
    if (this.live()) { await this.persist(this.value); this.busy = false; this.render(); this.options.refresh(); }
  }
  async checkStatus(): Promise<void> {
    const value = this.value, request = value?.submitted;
    if (!this.live() || !request || this.busy || this.failed || !this.windowReady()) { this.render(); return; }
    const identity = value.knownIdentity ?? value.observation?.identity ?? value.result?.identity;
    let message: string | undefined;
    this.busy = true; this.render();
    try {
      const observation = await this.options.client.status({ apiVersion: 1, workspaceId: request.workspaceId, projectKey: request.projectKey, boardKey: request.boardKey,
        workflow: structuredClone(request.workflow), ...(identity ? { expectedIdentity: identity } : {}) });
      if (!this.live()) return;
      this.value = { ...value, observation, ...(observation.identity ? { knownIdentity: observation.identity } : {}) }; await this.persist(this.value);
    } catch { message = "Status unavailable. The original outcome and draft remain unchanged."; }
    finally { if (this.live()) { this.busy = false; this.render(message); } }
  }
  private async openRegistered(): Promise<void> {
    const generation = this.viewGeneration;
    await this.queue;
    const result = this.value?.observation ?? this.value?.result;
    if (!this.live() || !this.windowReady() || !this.canLeave || this.opening || generation !== this.viewGeneration || result?.status !== "registered" || !result.identity) return;
    const path = "/board/" + encodeURIComponent(result.projectKey) + "/" + encodeURIComponent(result.boardKey!);
    const identity = structuredClone(result.identity);
    // Current resolution/navigation remains authoritative. No new create or status.
    this.opening = true; this.render();
    try {
      const opened = await this.options.navigate(path, identity);
      if (this.live() && generation === this.viewGeneration && opened !== false) await this.close();
    } finally { this.opening = false; if (this.live()) this.render(); }
  }
  async close(): Promise<void> {
    const generation = this.viewGeneration;
    await this.queue; if (!this.live() || !this.canLeave || generation !== this.viewGeneration) return;
    this.panel.hidden = true;
    const target = this.opener?.isConnected && !this.opener.closest("[hidden]") ? this.opener : this.options.fallback();
    if (target.isConnected && !target.closest("[hidden]") && !(target instanceof HTMLButtonElement && target.disabled)) target.focus({ preventScroll: true });
  }
  async discard(): Promise<void> {
    if (!this.live() || this.busy) return;
    const text = this.value?.submitted ? "Discard this saved setup attempt? Its original outcome may become unknowable. This does not delete or change any server board." : "Discard this private workflow draft?";
    if (!this.options.confirm(text)) return;
    await this.queue; if (!this.live()) return;
    try { await this.options.store.clear(this.record?.revision ?? 0); if (!this.live()) return; this.record = undefined; this.value = undefined; this.failed = false; this.previewRevision = undefined; await this.close(); }
    catch { this.failed = true; this.render(); }
  }
  destroy(): void { this.alive = false; window.removeEventListener("beforeunload", this.beforeUnload); this.editor?.destroy(); this.panel.remove(); this.options.client.dispose(); this.options.store.close(); }
}
