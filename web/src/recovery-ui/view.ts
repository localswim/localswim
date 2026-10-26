import { BoardClient } from "../api/client";
import type { OriginalOutcome } from "../api/client";
import type { Context } from "../recovery/types";
import type { Inventory } from "../recovery/store";
import { recoveryCoordinator } from "./coordination";
import type { RecoveryCoordinator } from "./coordination";

export function element<K extends keyof HTMLElementTagNameMap>(tag: K, text = ""): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag); node.textContent = text; return node;
}
export function button(text: string, run: () => void): HTMLButtonElement {
  const node = element("button", text); node.type = "button"; node.addEventListener("click", run); return node;
}
export function check(text: string): { label: HTMLLabelElement; input: HTMLInputElement } {
  const label = element("label", text + " "), input = element("input"); input.type = "checkbox"; label.append(input); return { label, input };
}
type Selection = Readonly<{ kind: "draft" | "submission"; context: Context; id: string }> | "all";

// Private recovery is advisory. Loading never submits or applies a recovered draft.
export class RecoveryView {
  private readonly heading = element("h2", "Private browser recovery");
  private readonly status = element("p", "Private recovery inventory unavailable.");
  private readonly rows = element("section");
  private readonly detail = element("section");
  private readonly clearing = element("section");
  private generation = 0;
  private selection = 0;
  private detailRequest = 0;
  private detailEdits = 0;
  private destroyed = false;
  private readonly persistence: RecoveryCoordinator;
  private readonly stopPersistence: () => void;
  private readonly discards = new Set<AbortController>();
  constructor(private readonly root: HTMLElement, readonly client: BoardClient, private readonly observe: (outcome: OriginalOutcome) => void = () => {}) {
    this.persistence = recoveryCoordinator(client);
    this.stopPersistence = this.persistence.subscribe(() => {
      const restoreFocus = this.detail.contains(document.activeElement) || this.rows.contains(document.activeElement);
      this.generation++; this.selection++; this.detailRequest++; this.detail.replaceChildren(); this.rows.replaceChildren();
      this.status.textContent = "Private discard pending; earlier workspace writes are draining. Later deliberate edits are retained after the boundary.";
      if (restoreFocus) this.heading.focus();
    });
    this.heading.tabIndex = -1; this.status.setAttribute("role", "status"); this.rows.setAttribute("aria-label", "Private recovery metadata");
    this.detail.setAttribute("aria-label", "Original recovery detail");
    root.replaceChildren(this.heading, element("p", "Recovery belongs to this browser profile. It is not server history or proof of a saved operation."),
      button("Reload private inventory", () => { void this.open(); }),
      button("Clear all private recovery", () => this.clear("all")), this.status, this.rows, this.detail, this.clearing);
  }
  destroy(): void { this.destroyed = true; this.stopPersistence(); for (const controller of this.discards) controller.abort(); this.discards.clear(); this.generation++; this.selection++; this.detailRequest++; this.root.replaceChildren(); }
  async open(): Promise<void> {
    const generation = ++this.generation, persistenceGeneration = this.persistence.generation;
    try {
      const inventory = await this.persistence.read(() => this.client.recovery.inventory());
      if (this.destroyed || generation !== this.generation || persistenceGeneration !== this.persistence.generation) return;
      this.render(inventory); this.status.textContent = "Private inventory loaded. Original outcomes require deliberate reauthentication and original lookup.";
    } catch { if (!this.destroyed && generation === this.generation) this.status.textContent = "Private inventory unavailable; retained input must not be resubmitted to investigate an unknown outcome."; }
  }
  private render(inventory: Inventory): void {
    const focus = (document.activeElement as HTMLElement | null)?.dataset.recoveryFocus;
    this.rows.replaceChildren();
    for (const row of inventory.drafts) {
      const article = element("article", "Draft " + row.draftId + " · board " + row.context.boardId + " · incarnation " + row.context.incarnation + " · actor " + row.context.actorId + " · private revision " + row.revision);
      const show = button("View retained draft", () => { void this.draft(row.context, row.draftId); }); show.dataset.recoveryFocus = row.key;
      article.append(show, button("Clear this private draft", () => this.clear({ kind: "draft", context: row.context, id: row.draftId })));
      this.rows.append(article);
    }
    for (const row of inventory.submissions) {
      const article = element("article", "Original operation " + row.opId + " · board " + row.context.boardId + " · incarnation " + row.context.incarnation + " · actor " + row.context.actorId + " · " + row.outcome);
      const state = element("p"); state.setAttribute("role", "status");
      const lookup = button("Look up this original operation", () => {
        lookup.disabled = true;
        void this.client.lookup(row.context, row.opId).then(outcome => {
          if (this.destroyed) return;
          this.observe(outcome); this.status.textContent = outcome.message;
          state.textContent = outcome.message; lookup.disabled = false;
        }, () => { if (!this.destroyed) { state.textContent = "Original outcome unavailable; do not resubmit."; lookup.disabled = false; } });
      });
      lookup.dataset.recoveryFocus = row.key;
      article.append(lookup, button("Clear this private operation", () => this.clear({ kind: "submission", context: row.context, id: row.opId })), state); this.rows.append(article);
    }
    if (!inventory.drafts.length && !inventory.submissions.length) this.rows.append(element("p", "No retained private records."));
    if (focus) ([...this.rows.querySelectorAll<HTMLButtonElement>("button")].find(n => n.dataset.recoveryFocus === focus) ?? this.heading).focus();
  }
  private async draft(context: Context, id: string): Promise<void> {
    const request = ++this.detailRequest, edits = this.detailEdits, persistenceGeneration = this.persistence.generation;
    try {
      const draft = await this.persistence.read(() => this.client.recovery.loadDraft(context, id));
      if (this.destroyed || request !== this.detailRequest || edits !== this.detailEdits || persistenceGeneration !== this.persistence.generation) return;
      const selection = ++this.selection;
      if (!draft) { this.detail.replaceChildren(element("p", "Retained draft unavailable or expired.")); return; }
      const input = element("textarea"); input.value = draft.input; input.setAttribute("aria-label", "Retained private draft text");
      const status = element("p", "Private draft loaded; no server submission."); status.setAttribute("role", "status");
      let editingId = draft.draftId, revision = draft.revision, sequence = 0;
      input.addEventListener("input", () => {
        if (this.destroyed || selection !== this.selection) return;
        this.detailEdits++;
        const text = input.value, edited = ++sequence, generation = this.persistence.generation; status.textContent = "Recovery pending";
        void this.persistence.persist(async () => {
          try {
            const saved = await this.client.recovery.saveDraft({ context, draftId: editingId, expectedRevision: revision, input: text });
            editingId = saved.draft.draftId; revision = saved.draft.revision;
            if (!this.destroyed && generation === this.persistence.generation && selection === this.selection && edited === sequence) status.textContent = saved.state === "forked" ? "Recovery saved as a separate draft after a conflict." : "Recovery saved privately.";
          } catch { if (!this.destroyed && generation === this.persistence.generation && selection === this.selection && edited === sequence) status.textContent = "Recovery failed; newer input remains in this editor."; }
        });
      });
      this.detail.replaceChildren(element("h3", "Retained draft · private revision " + draft.revision), input,
        status, element("p", "Original board/incarnation/actor retained. Editing this private copy does not replace another open editor, adopt a current base, or submit."));
    } catch { if (!this.destroyed && request === this.detailRequest && edits === this.detailEdits) this.detail.replaceChildren(element("p", "Retained draft unavailable.")); }
  }
  private clear(selection: Selection): void {
    this.selection++; this.detailRequest++; this.detail.replaceChildren();
    const prose = check("Discard selected private prose and evidence"), unknown = check("Unresolved outcomes may become unknowable after this discard");
    const state = element("p"); state.setAttribute("role", "status");
    let attempted = false;
    let controller: AbortController | undefined;
    const confirm = button("Confirm private discard", () => {
      if (!prose.input.checked || !unknown.input.checked || attempted) return;
      attempted = true;
      confirm.disabled = true;
      controller = new AbortController(); this.discards.add(controller);
      const current = controller;
      state.textContent = "Waiting for earlier workspace persistence before private discard. Later deliberate edits will be retained.";
      void this.persistence.clear(() => this.client.recovery.clear(selection, { discardPrivateProse: true, unresolvedMayBecomeUnknowable: true }), current.signal).then(async () => {
        if (this.destroyed || current.signal.aborted) return;
        state.textContent = "Selected private recovery discarded. Later deliberate edits are retained. Server history and outcomes were not cleared.";
        await this.open();
      }, () => { if (!this.destroyed && !current.signal.aborted) state.textContent = "Private discard unavailable or unverified. Reload the inventory before another deliberate action."; }).finally(() => { this.discards.delete(current); });
    });
    confirm.disabled = true;
    const update = (): void => { confirm.disabled = attempted || !prose.input.checked || !unknown.input.checked; };
    prose.input.addEventListener("change", update); unknown.input.addEventListener("change", update);
    this.clearing.replaceChildren(element("h3", selection === "all" ? "Discard all private recovery" : "Discard one private record"),
      element("p", "This permanently removes selected private recovery. It does not cancel a possible server commit. Server clear and retention require their separate preview/confirm controls."),
      element("p", "Discard affects records currently stored in this browser profile. Earlier writes registered in this workspace finish first. Later deliberate edits and other open tabs may store private recovery again."),
      prose.label, unknown.label, confirm, button("Cancel private discard", () => {
        if (!attempted) { this.clearing.replaceChildren(); return; }
        controller?.abort(); state.textContent = "Stopped waiting for private discard. Storage already admitted may finish; local result is unverified. Reload the inventory deliberately.";
      }), state);
    prose.input.focus();
  }
}
