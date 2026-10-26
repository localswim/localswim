import type { BoardRead, SessionRead } from "../../generated/transport/contracts.js";
import type { Confirmation, Preview, PreviewRequest } from "../../generated/archive/contracts.js";
import { BoardClient } from "../api/client.js";
import type { OriginalOutcome } from "../api/client.js";
import type { Context } from "../recovery/types.js";
import { canonical, parse } from "../api/boundary.js";
import { recoveryCoordinator } from "./coordination.js";
import type { RecoveryCoordinator } from "./coordination.js";
import { button, check, element } from "./view.js";

// Composition supplies the concrete W02 provider; there is no UI credential,
// alternate verifier, or permissive mutation default.
export interface RecoveryAdminClient extends BoardClient {
  preview(boardId: string, request: PreviewRequest): Promise<Preview>;
  submitRecovery(intent: Readonly<{ context: Context; draftId: string; request: Confirmation }>, announce: (outcome: OriginalOutcome) => void): Promise<OriginalOutcome>;
}
export class RecoveryAdminView {
  private readonly status = element("p", "Recovery administration unavailable; read current authority and board first.");
  private readonly form = element("form");
  private readonly previewHost = element("section");
  private readonly outcome = element("p");
  private readonly command = element("select");
  private readonly through = element("input");
  private readonly days: HTMLInputElement[] = [];
  private readonly previewButton: HTMLButtonElement;
  private base: BoardRead | undefined;
  private auth: SessionRead | undefined;
  private context: Context | undefined;
  private draft: { id: string; revision: number } = { id: crypto.randomUUID(), revision: 0 };
  private sequence = 0;
  private generation = 0;
  private composing = false;
  private destroyed = false;
  private busy = false;
  private unresolved = false;
  private initializedDays = false;
  private readonly persistence: RecoveryCoordinator;
  constructor(private readonly root: HTMLElement, readonly client: RecoveryAdminClient, readonly boardId: string, private readonly observe: (outcome: OriginalOutcome) => void = () => {}) {
    this.persistence = recoveryCoordinator(client);
    this.status.setAttribute("role", "status"); this.outcome.setAttribute("role", "status"); this.outcome.tabIndex = -1;
    for (const name of ["history.clear", "retention.set"]) { const option = element("option", name); option.value = name; this.command.append(option); }
    const field = (name: string, input: HTMLInputElement | HTMLSelectElement): void => { const label = element("label", name + " "); label.append(input); this.form.append(label); };
    field("Recovery command", this.command); this.through.placeholder = "YYYY-MM-DDTHH:mm:ssZ"; field("Clear through UTC time", this.through);
    for (const name of ["History days", "Activity days", "Outcome payload days"]) { const input = element("input"); input.type = "number"; input.min = "1"; input.max = "3650"; input.step = "1"; field(name, input); this.days.push(input); }
    this.previewButton = button("Preview server recovery losses", () => { void this.preview(); });
    this.form.append(this.previewButton);
    this.form.addEventListener("submit", event => event.preventDefault());
    this.form.addEventListener("input", () => this.edited()); this.form.addEventListener("change", () => this.edited());
    this.form.addEventListener("compositionstart", () => { this.composing = true; this.update(); });
    this.form.addEventListener("compositionend", () => { this.composing = false; this.edited(); });
    this.form.addEventListener("keydown", event => { if (event.isComposing && event.key === "Enter") event.preventDefault(); });
    root.replaceChildren(element("h2", "Server history and retention"), element("p", "Human read and history-admin rights are required. Clearing affects server history and full outcomes, separate from private browser recovery. Current cards and approvals are preserved by the server contract."),
      button("Read current recovery authority", () => { void this.refresh(); }), this.status, this.form, this.previewHost, this.outcome); this.update();
  }
  destroy(): void { this.destroyed = true; this.generation++; this.root.replaceChildren(); }
  private update(): void { this.previewButton.disabled = this.destroyed || !this.context || this.busy || this.composing || this.unresolved; }
  async refresh(): Promise<void> {
    if (this.busy || this.unresolved) return;
    const generation = ++this.generation, sequence = this.sequence, persistenceGeneration = this.persistence.generation; this.previewHost.replaceChildren(); this.context = undefined; this.update();
    try {
      const auth = await this.client.session(), base = await this.client.board(this.boardId), context = this.client.context(base, auth);
      if (auth.kind !== "human" || !auth.grant.rights.includes("read") || !auth.grant.rights.includes("history-admin")) throw Error("Unavailable authority");
      if (this.destroyed || generation !== this.generation) return;
      this.auth = auth; this.base = base; this.context = context; this.draft = { id: crypto.randomUUID(), revision: 0 };
      const policy = base.result.retention;
      if (!this.initializedDays && sequence === this.sequence) [policy.historyDays, policy.activityDays, policy.outcomeDays].forEach((value, index) => { this.days[index]!.value = String(value); });
      this.initializedDays = true;
      this.status.textContent = "Current board " + context.boardId + " · incarnation " + context.incarnation + " · actor " + context.actorId + " · revision " + base.result.revision + " · retention version " + base.result.retentionVersion;
      if (persistenceGeneration === this.persistence.generation) this.edited();
    } catch { if (!this.destroyed && generation === this.generation) { this.base = undefined; this.auth = undefined; this.status.textContent = "Current recovery authority unavailable. Human read and history-admin rights are required; no previous preview can be confirmed."; } }
    this.update();
  }
  private input(): PreviewRequest["input"] {
    return this.command.value === "history.clear" ? { throughTime: this.through.value } : { historyDays: Number(this.days[0]!.value), activityDays: Number(this.days[1]!.value), outcomeDays: Number(this.days[2]!.value) };
  }
  private edited(): void {
    this.sequence++; this.previewHost.replaceChildren(); this.update();
    if (!this.context || !this.base) return;
    const context = this.context, draft = this.draft, generation = this.persistence.generation;
    const input = JSON.stringify({ command: this.command.value, fields: { throughTime: this.through.value, historyDays: this.days[0]!.value, activityDays: this.days[1]!.value, outcomeDays: this.days[2]!.value }, expectedRevision: this.base.result.revision, incarnation: context.incarnation });
    void this.persistence.persist(async () => {
      try { const saved = await this.client.recovery.saveDraft({ context, draftId: draft.id, expectedRevision: draft.revision, input }); draft.id = saved.draft.draftId; draft.revision = saved.draft.revision; }
      catch { if (!this.destroyed && generation === this.persistence.generation) this.status.textContent = "Private recovery unavailable; input remains here. Confirm still requires durable verified original evidence."; }
    });
  }
  private async preview(): Promise<void> {
    if (!this.context || !this.base || !this.auth || this.busy || this.composing || this.unresolved) return;
    const context = this.context, base = this.base, sequence = this.sequence, generation = this.generation, draft = this.draft;
    const request: PreviewRequest = structuredClone({ command: this.command.value as PreviewRequest["command"], input: this.input(), expectedRevision: base.result.revision, incarnation: context.incarnation });
    this.busy = true; this.update();
    try {
      const result = await this.client.preview(this.boardId, request);
      if (this.destroyed || generation !== this.generation || sequence !== this.sequence) return;
      // Provider verifies schema/hash; UI binds the exact original displayed base.
      if (result.actorId !== context.actorId || result.boardId !== context.boardId || result.incarnation !== context.incarnation || result.expectedRevision !== base.result.revision || result.retentionVersion !== base.result.retentionVersion || result.command !== request.command || canonical(parse(JSON.stringify(result.input))) !== canonical(parse(JSON.stringify(request.input)))) throw Error("Preview mismatch");
      const preview = structuredClone(result);
      const warn = check("I approve exactly these server recovery losses");
      let attempted = false;
      const confirm = button("Confirm exact server recovery preview", () => {
        if (attempted || !warn.input.checked || this.composing || this.destroyed || sequence !== this.sequence || generation !== this.generation) return;
        attempted = true; confirm.disabled = true; this.busy = true; this.unresolved = true; this.update();
        const confirmation: Confirmation = { opId: crypto.randomUUID(), incarnation: preview.incarnation, target: null, expectedRevision: preview.expectedRevision, expectedCardRevision: null, command: preview.command, input: structuredClone(preview.input), previewHash: preview.previewHash };
        let settled = false, waiting: HTMLButtonElement | undefined, lookupNode: HTMLButtonElement | undefined;
        const announce = (outcome: OriginalOutcome): void => {
          if (this.destroyed) return;
          if (settled && outcome.state !== "committed" && outcome.state !== "not_committed") return;
          if (outcome.state === "committed" || outcome.state === "not_committed") settled = true;
          this.observe(outcome);
          this.outcome.textContent = outcome.message;
          this.unresolved = outcome.state === "pending" || outcome.state === "unknown";
          this.busy = outcome.state === "pending";
          const restoreFocus = document.activeElement === waiting || document.activeElement === lookupNode;
          waiting?.remove(); waiting = undefined;
          lookupNode?.remove(); lookupNode = undefined;
          if (outcome.state === "pending") {
            waiting = button("Stop waiting for recovery confirmation", () => announce({ ...outcome, state: "unknown", message: "Stopped waiting; possible recovery confirmation remains unknown. Original lookup only." })); this.previewHost.append(waiting);
          }
          if (outcome.state !== "pending") { this.context = undefined; this.status.textContent = "Previous preview is spent. Read current recovery authority and request a new preview only after the original outcome is resolved."; }
          if (outcome.state === "unknown") {
            const lookup = button("Look up original recovery confirmation", () => { lookup.disabled = true; void this.client.lookup(context, confirmation.opId).then(found => { lookup.remove(); announce(found); }); });
            lookupNode = lookup; this.previewHost.append(lookup);
          }
          this.update();
          if (restoreFocus) (lookupNode ?? this.outcome).focus();
        };
        void this.persistence.admit(async evidenceReady => {
          if (this.destroyed || generation !== this.generation) return;
          return this.client.submitRecovery({ context, draftId: draft.id, request: confirmation }, outcome => {
            if (outcome.state === "pending" || outcome.state === "unsent") evidenceReady();
            announce(outcome);
          });
        }).catch(() => announce({ opId: confirmation.opId, state: "unknown", message: "Original recovery confirmation unavailable; use only its original lookup." }));
      });
      confirm.disabled = true; warn.input.addEventListener("change", () => { confirm.disabled = attempted || !warn.input.checked; });
      this.previewHost.replaceChildren(element("h3", "Immutable server preview"),
        element("p", "Preview hash " + preview.previewHash + " · previewed " + preview.previewedAt + " · expires " + preview.expiresAt + " · original revision " + preview.expectedRevision),
        element("pre", JSON.stringify(preview.inventory, null, 2)), warn.label, confirm);
      this.status.textContent = "Preview only. Confirm requires a separate deliberate action; expiry and current authority are rechecked by the server.";
    } catch { if (!this.destroyed && generation === this.generation && sequence === this.sequence) this.status.textContent = "Preview unavailable or mismatched. No confirmation occurred; request a new preview deliberately."; }
    finally { this.busy = false; this.update(); }
  }
}
