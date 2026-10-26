import type { BoardRead } from "../../generated/transport/contracts.js";
import type { ExportRequest } from "../../generated/transport/contracts.js";
import type { RequestAttachmentAdd } from "../../generated/contracts.js";
import { BoardClient } from "../api/client.js";
import type { OriginalOutcome } from "../api/client.js";
import type { Context } from "../recovery/types.js";
import { button, element } from "../recovery-ui/view.js";
import { recoveryCoordinator } from "../recovery-ui/coordination.js";
import type { RecoveryCoordinator } from "../recovery-ui/coordination.js";

export interface ArchiveClient extends BoardClient {
  submitAttachment(intent: Readonly<{ context: Context; draftId: string; request: RequestAttachmentAdd; bytes: ArrayBuffer }>, announce: (outcome: OriginalOutcome) => void): Promise<OriginalOutcome>;
  downloadAttachment(boardId: string, attachmentId: string, facts: Readonly<{ sha256: string; byteLength: number }>, signal: AbortSignal): Promise<ArrayBuffer>;
  exportArchive(boardId: string, request: ExportRequest, signal: AbortSignal): Promise<ArrayBuffer>;
}
type AttachmentEditor = {
  context: Context; base: BoardRead; cardId: string; cardRevision: number;
  draftId: string; draftRevision: number; sequence: number;
  name: HTMLInputElement; media: HTMLInputElement; file: HTMLInputElement;
  raw: ArrayBuffer | undefined; facts: { sha256: string; byteLength: number } | undefined;
  status: HTMLElement; save: HTMLButtonElement; composing: boolean; submitted: boolean; selection: number;
};
const digest = async (bytes: ArrayBuffer): Promise<string> => [...new Uint8Array(await crypto.subtle.digest("SHA-256", bytes))].map(n => n.toString(16).padStart(2, "0")).join("");

// Browser downloads offer bytes. They cannot attest to a local save or restore.
export class ArchiveView {
  private readonly heading = element("h2", "Attachments and export");
  private readonly status = element("p", "Attachment/export controls unavailable; read a current board first.");
  private readonly cards = element("select");
  private readonly editorHost = element("section");
  private readonly attachments = element("section");
  private readonly outcomes = element("section");
  private readonly exportButton: HTMLButtonElement;
  private readonly attachButton: HTMLButtonElement;
  private base: BoardRead | undefined;
  private context: Context | undefined;
  private mayAttach = false;
  private mayExport = false;
  private editor: AttachmentEditor | undefined;
  private generation = 0;
  private destroyed = false;
  private unresolved = false;
  private exportPending = false;
  private readonly downloads = new Set<AbortController>();
  private readonly persistence: RecoveryCoordinator;
  private readonly stopPersistence: () => void;
  constructor(private readonly root: HTMLElement, readonly client: ArchiveClient, readonly boardId: string, private readonly observe: (outcome: OriginalOutcome) => void = () => {}) {
    this.persistence = recoveryCoordinator(client);
    this.stopPersistence = this.persistence.subscribe(() => {
      if (this.editor) { this.editor.sequence++; this.editor.status.textContent = "Private discard pending; file selection and visible input retained. Later deliberate edits are saved after the boundary."; }
    });
    this.heading.tabIndex = -1; this.status.setAttribute("role", "status"); this.cards.setAttribute("aria-label", "Attachment target card"); this.outcomes.setAttribute("aria-label", "Original attachment outcomes");
    this.attachButton = button("Open attachment editor", () => this.edit());
    this.exportButton = button("Offer current board export", () => { void this.export(); });
    root.replaceChildren(this.heading, button("Read current attachment board", () => { void this.refresh(); }), this.status,
      this.cards, this.attachButton, this.exportButton, this.editorHost, this.attachments, this.outcomes); this.update();
  }
  destroy(): void { this.destroyed = true; this.stopPersistence(); this.generation++; for (const controller of this.downloads) controller.abort(); this.downloads.clear(); this.editor = undefined; this.context = undefined; this.base = undefined; this.root.replaceChildren(); }
  private update(): void {
    this.attachButton.disabled = !this.context || !this.mayAttach || this.unresolved || this.destroyed;
    this.exportButton.disabled = !this.context || !this.mayExport || this.destroyed || this.exportPending;
    if (this.editor) this.editor.save.disabled = !this.editor.raw || !this.editor.facts || this.editor.composing || this.editor.submitted || this.unresolved || this.destroyed;
  }
  async refresh(): Promise<void> {
    const generation = ++this.generation;
    try {
      const session = await this.client.session(), base = await this.client.board(this.boardId), context = this.client.context(base, session);
      if (!session.grant.rights.includes("read")) throw Error("Current read unavailable");
      if (this.destroyed || generation !== this.generation) return;
      this.base = base; this.context = context; this.mayAttach = session.grant.rights.includes("attach"); this.mayExport = session.grant.rights.includes("export");
      this.status.textContent = "Current board " + context.boardId + " · incarnation " + context.incarnation + " · actor " + context.actorId + " · revision " + base.result.revision;
      const selected = this.cards.value; this.cards.replaceChildren();
      for (const card of base.result.cards.filter(c => c.lane !== "Completed")) { const option = element("option", "#" + card.ticket + " " + card.title); option.value = card.cardId; this.cards.append(option); }
      if ([...this.cards.options].some(o => o.value === selected)) this.cards.value = selected;
      this.renderAttachments(base);
    } catch { if (!this.destroyed && generation === this.generation) { this.context = undefined; this.mayAttach = false; this.mayExport = false; this.status.textContent = this.base ? "Attachment board unavailable; displayed metadata is stale. Existing editor retains its original base." : "Attachment board unavailable."; } }
    this.update();
  }
  private edit(): void {
    if (!this.context || !this.base || !this.mayAttach || this.unresolved || this.destroyed) return;
    const card = this.base.result.cards.find(c => c.cardId === this.cards.value); if (!card || card.lane === "Completed") return;
    const form = element("form"), name = element("input"), media = element("input"), file = element("input"), status = element("p", "Select one file; private recovery pending.");
    file.type = "file"; media.value = "application/octet-stream"; status.setAttribute("role", "status");
    const editor: AttachmentEditor = { context: structuredClone(this.context), base: structuredClone(this.base), cardId: card.cardId, cardRevision: card.revision,
      draftId: crypto.randomUUID(), draftRevision: 0, sequence: 0, name, media, file,
      raw: undefined, facts: undefined, status, save: button("Attach once", () => { void this.save(editor); }), composing: false, submitted: false, selection: 0 };
    form.addEventListener("submit", event => event.preventDefault());
    form.append(element("h3", "Attachment · original card " + card.cardId + " revision " + card.revision + " · original board revision " + this.base.result.revision));
    for (const [labelText, input] of [["Attachment display name", name], ["Attachment media label", media], ["Attachment file", file]] as const) { const label = element("label", labelText + " "); label.append(input); form.append(label); }
    name.addEventListener("input", () => this.persist(editor)); media.addEventListener("input", () => this.persist(editor));
    file.addEventListener("change", () => { void this.capture(editor); });
    form.addEventListener("compositionstart", () => { editor.composing = true; this.update(); });
    form.addEventListener("compositionend", () => { editor.composing = false; this.persist(editor); this.update(); });
    form.addEventListener("keydown", event => { if (event.isComposing && event.key === "Enter") event.preventDefault(); });
    form.append(status, editor.save, element("p", "After a lost response, use the original operation lookup. Reload never uploads bytes or creates a replacement operation."));
    this.editor = editor; this.editorHost.replaceChildren(form); this.persist(editor); this.update(); name.focus();
  }
  private async capture(editor: AttachmentEditor): Promise<void> {
    const selection = ++editor.selection, file = editor.file.files?.[0], persistenceGeneration = this.persistence.generation; editor.raw = undefined; editor.facts = undefined; this.update();
    if (file && !editor.name.value) editor.name.value = file.name;
    this.persist(editor);
    if (!file) return;
    if (file.size > 10 * 1024 * 1024) { editor.sequence++; editor.status.textContent = "File exceeds 10 MiB; not dispatched."; return; }
    try {
      // One raw buffer is retained privately and never mutated or persisted.
      const raw = await file.arrayBuffer(), sha256 = await digest(raw);
      if (this.destroyed || this.editor !== editor || selection !== editor.selection) return;
      if (raw.byteLength !== file.size || raw.byteLength > 10 * 1024 * 1024) throw Error("Incomplete file");
      editor.raw = raw; editor.facts = { sha256, byteLength: raw.byteLength };
      if (persistenceGeneration === this.persistence.generation) this.persist(editor);
      else editor.status.textContent = "Earlier private file metadata was discarded. Visible selection retained; further deliberate edits or Attach once create fresh evidence.";
      this.update();
    } catch { if (!this.destroyed && this.editor === editor && selection === editor.selection) { editor.sequence++; editor.status.textContent = "Complete file bytes unavailable; not dispatched."; } }
  }
  private persist(editor: AttachmentEditor): void {
    const sequence = ++editor.sequence, generation = this.persistence.generation;
    const input = JSON.stringify({ command: "attachment.add", incarnation: editor.context.incarnation, target: { cardId: editor.cardId }, expectedRevision: editor.base.result.revision, expectedCardRevision: editor.cardRevision,
      input: { name: editor.name.value, mediaLabel: editor.media.value, ...(editor.facts ?? {}) } });
    editor.status.textContent = "Recovery pending";
    void this.persistence.persist(async () => {
      try {
        const saved = await this.client.recovery.saveDraft({ context: editor.context, draftId: editor.draftId, expectedRevision: editor.draftRevision, input }); editor.draftId = saved.draft.draftId; editor.draftRevision = saved.draft.revision;
        if (!this.destroyed && generation === this.persistence.generation && this.editor === editor && sequence === editor.sequence) editor.status.textContent = "Recovery saved privately. File bytes are not stored in recovery.";
      } catch { if (!this.destroyed && generation === this.persistence.generation && this.editor === editor && sequence === editor.sequence) editor.status.textContent = "Recovery unavailable; input retained. Dispatch requires verified original evidence."; }
    });
  }
  private async save(editor: AttachmentEditor): Promise<void> {
    if (this.destroyed || this.editor !== editor || editor.submitted || editor.composing || !editor.raw || !editor.facts || this.unresolved) return;
    editor.submitted = true; this.unresolved = true; this.update();
    const bytes = editor.raw;
    const request: RequestAttachmentAdd = { opId: crypto.randomUUID(), incarnation: editor.context.incarnation, target: { cardId: editor.cardId }, expectedRevision: editor.base.result.revision, expectedCardRevision: editor.cardRevision,
      input: { name: editor.name.value, mediaLabel: editor.media.value, ...editor.facts } };
    const row = element("article"), state = element("p"); state.setAttribute("role", "status"); state.tabIndex = -1; row.append(state); this.outcomes.append(row);
    let settled = false, waiting: HTMLButtonElement | undefined, lookupNode: HTMLButtonElement | undefined;
    const announce = (outcome: OriginalOutcome): void => {
      if (this.destroyed) return;
      if (settled && outcome.state !== "committed" && outcome.state !== "not_committed") return;
      if (outcome.state === "committed" || outcome.state === "not_committed") settled = true;
      this.observe(outcome);
      state.textContent = request.opId + ": " + outcome.message;
      this.unresolved = outcome.state === "pending" || outcome.state === "unknown";
      const restoreFocus = document.activeElement === waiting || document.activeElement === lookupNode;
      waiting?.remove(); waiting = undefined;
      lookupNode?.remove(); lookupNode = undefined;
      if (outcome.state === "pending") {
        waiting = button("Stop waiting for attachment", () => announce({ ...outcome, state: "unknown", message: "Stopped waiting; possible attachment commit remains unknown. Original lookup only." })); row.append(waiting);
      }
      if (outcome.state === "committed") { this.context = undefined; this.status.textContent = "Confirmed attachment saved. Displayed metadata is stale; read the current board."; }
      if (outcome.state === "unknown") {
        const lookup = button("Look up original attachment operation", () => { lookup.disabled = true; void this.client.lookup(editor.context, request.opId).then(found => { lookup.remove(); announce(found); }); }); lookupNode = lookup; row.append(lookup);
      }
      this.update();
      if (restoreFocus) (lookupNode ?? state).focus();
    };
    try { await this.persistence.admit(async evidenceReady => {
      if (this.destroyed) return;
      return this.client.submitAttachment({ context: editor.context, draftId: editor.draftId, request, bytes }, outcome => {
        if (outcome.state === "pending" || outcome.state === "unsent") evidenceReady();
        announce(outcome);
      });
    }); }
    catch { announce({ opId: request.opId, state: "unknown", message: "Original attachment outcome unavailable; original lookup only." }); }
  }
  private offer(bytes: ArrayBuffer, type: string, name: string): void {
    const url = URL.createObjectURL(new Blob([bytes], { type })), link = element("a"); link.href = url; link.download = name; link.hidden = true; this.root.append(link);
    try { link.click(); } finally { link.remove(); setTimeout(() => URL.revokeObjectURL(url), 0); }
  }
  private renderAttachments(base: BoardRead): void {
    const focused = (document.activeElement as HTMLElement | null)?.dataset.attachmentFocus; this.attachments.replaceChildren();
    for (const attachment of base.result.attachments) {
      const state = element("p"), row = element("article", attachment.name + " · " + attachment.mediaLabel + " · " + attachment.byteLength + " bytes · SHA-256 " + attachment.sha256); state.setAttribute("role", "status");
      const offer = button("Offer attachment download", () => {
        const generation = this.generation, controller = new AbortController(); this.downloads.add(controller); offer.disabled = true;
        const cancel = button("Cancel attachment download", () => { controller.abort(); state.textContent = "Download cancelled; local save unverified."; cancel.remove(); }); row.append(cancel);
        void this.client.downloadAttachment(this.boardId, attachment.attachmentId, { sha256: attachment.sha256, byteLength: attachment.byteLength }, controller.signal).then(bytes => {
          if (this.destroyed || controller.signal.aborted || generation !== this.generation) return;
          this.offer(bytes, "application/octet-stream", "attachment-" + attachment.attachmentId + ".bin"); state.textContent = "Attachment download offered after complete byte verification. Local save unverified.";
        }, () => { if (!this.destroyed && !controller.signal.aborted) state.textContent = "Attachment bytes unavailable or incomplete; no verified download offered."; }).finally(() => { this.downloads.delete(controller); cancel.remove(); offer.disabled = false; });
      }); offer.dataset.attachmentFocus = attachment.attachmentId; row.append(offer, state); this.attachments.append(row);
    }
    if (focused) ([...this.attachments.querySelectorAll<HTMLButtonElement>("button")].find(n => n.dataset.attachmentFocus === focused) ?? this.heading).focus();
  }
  private async export(): Promise<void> {
    if (!this.context || !this.base || !this.mayExport || this.destroyed || this.exportPending) return;
    const context = structuredClone(this.context), revision = this.base.result.revision, controller = new AbortController(), generation = this.generation;
    this.downloads.add(controller); this.exportPending = true; this.update();
    const state = element("p", "Receiving export; no local save confirmed."); state.setAttribute("role", "status");
    const cancel = button("Cancel export download", () => { controller.abort(); state.textContent = "Export cancelled; local save unverified."; cancel.remove(); }); this.outcomes.append(state, cancel);
    try {
      const bytes = await this.client.exportArchive(context.boardId, { incarnation: context.incarnation, expectedRevision: revision, input: {} }, controller.signal);
      if (this.destroyed || controller.signal.aborted || generation !== this.generation) return;
      this.offer(bytes, "application/zip", "board-" + context.boardId + "-revision-" + revision + ".zip");
      state.textContent = JSON.stringify({ apiVersion: 1, scope: "export", outcome: "offered", result: { sourceIdentity: { boardId: context.boardId, incarnation: context.incarnation }, sourceRevision: revision, destination: null } }) + " · Local save unverified.";
    } catch { if (!this.destroyed && !controller.signal.aborted) state.textContent = "Export unavailable or incomplete; no complete download offered and no local save confirmed."; }
    finally { this.downloads.delete(controller); cancel.remove(); this.exportPending = false; this.update(); }
  }
}
