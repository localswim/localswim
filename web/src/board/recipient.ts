import type { Identity } from "../../generated/archive/contracts.js";
import type { RecipientInput, RecipientView } from "../../generated/transport/contracts.js";
import type { BoardClient } from "../api/client.js";
import { RecipientRefusal, sameRecipientInput } from "../api/recipient.js";
import type { RecipientCommand } from "../api/recipient.js";
import type { Context } from "../recovery/types.js";
import { button, element } from "../recovery-ui/view.js";

type Save = { command: Extract<RecipientCommand, { action: "configure" }>; outcome: "pending" | "unknown" | "confirmed" | "refused" };
type Draft = { text: string; original?: Save };
// One ES module instance per window. No browser storage, timer or durable journal.
export class RecipientMemory {
  private readonly contexts = new Map<string, Draft>();
  get(key: string): Draft | undefined {
    let draft = this.contexts.get(key);
    if (!draft && this.contexts.size < 20) { draft = { text: "" }; this.contexts.set(key, draft); }
    return draft;
  }
}
const windowMemory = new RecipientMemory();
const thread = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const scopeKey = (context: Context): string => JSON.stringify([location.origin, context.installationId, context.boardId, context.incarnation, context.actorId]);

export class BoardRecipient {
  readonly host = element("div");
  private readonly panel = element("section");
  private readonly message = element("p");
  private readonly facts = element("div");
  private readonly originalResult = element("p");
  private readonly input = element("input");
  private readonly open = button("Recipient policy", () => {
    if (this.open.disabled || !this.panel.hidden) return;
    this.panel.hidden = false; this.open.setAttribute("aria-expanded", "true");
    this.message.textContent = this.draft ? "Choose Read recipient status before saving. Drafts and original save details stay in this window until reload or close." :
      "This window has reached its 20 recipient contexts. New saves are refused; retained originals are not removed. Status inspection remains available.";
    this.update();
  });
  private readonly read = button("Read recipient status", () => { void this.run({ action: "status" }); });
  private readonly save = button("Save once", () => this.configure());
  private context?: Context;
  private draft?: Draft;
  private displayed?: RecipientView;
  private request?: AbortController;
  private epoch = 0;
  private suspended = false;
  private selecting = false;

  constructor(private readonly client: Pick<BoardClient, "recipient">, private readonly memory = windowMemory) {
    this.panel.hidden = true; this.panel.className = "board-recipient";
    this.panel.setAttribute("aria-label", "Recipient policy");
    this.open.setAttribute("aria-expanded", "false");
    this.message.setAttribute("role", "status");
    this.input.maxLength = 1024; this.input.autocomplete = "off"; this.input.spellcheck = false;
    const label = element("label", "Codex chat UUID"); label.append(this.input);
    this.input.addEventListener("input", () => {
      if (!this.draft || this.draft.original && this.draft.original.outcome !== "refused") { this.input.value = this.draft?.text ?? ""; return; }
      if (new TextEncoder().encode(this.input.value).length > 1024) { this.input.value = this.draft.text; this.message.textContent = "Keep the chat reference within 1,024 UTF-8 bytes."; }
      else this.draft.text = this.input.value;
      this.update();
    });
    this.panel.addEventListener("keydown", event => {
      if (event.key === "Escape") { event.preventDefault(); this.close(); }
    });
    this.panel.append(element("h2", "Recipient policy"),
      element("p", "Save one private Codex chat reference with the conservative metadata policy. Saving does not activate delivery. This reference cannot be replaced here."),
      label, this.read, this.save, this.message, this.originalResult, this.facts,
      button("Close recipient policy", () => this.close()));
    this.host.append(this.open, this.panel); this.update();
  }

  boardRead(context: Context): void {
    if (this.context && scopeKey(this.context) !== scopeKey(context)) this.reset();
    this.context = { ...context }; this.draft = this.memory.get(scopeKey(context));
    this.input.value = this.draft?.text ?? "";
    this.update();
  }
  reset(): void { this.close(false); this.context = undefined; this.draft = undefined; this.input.value = ""; this.update(); }
  selectionChanging(pending: boolean): void { this.selecting = pending; if (pending) this.close(false); this.update(); }
  suspend(): void { this.suspended = true; this.close(false); this.update(); }
  resume(): void { this.suspended = false; this.update(); }
  private close(restoreFocus = true): void {
    const ownedFocus = this.panel.contains(document.activeElement);
    this.epoch++; this.request?.abort(); this.request = undefined; this.displayed = undefined;
    this.panel.hidden = true; this.facts.replaceChildren(); this.originalResult.textContent = "";
    this.open.setAttribute("aria-expanded", "false"); this.update();
    if (restoreFocus && ownedFocus && !this.open.disabled) this.open.focus({ preventScroll: true });
  }
  private update(): void {
    const available = !!this.context && !this.suspended && !this.selecting;
    this.open.disabled = !available;
    const busy = !!this.request, original = this.draft?.original;
    const locked = !!original && original.outcome !== "refused";
    this.input.readOnly = busy || locked;
    this.read.setAttribute("aria-disabled", String(!available || busy || this.panel.hidden));
    this.save.setAttribute("aria-disabled", String(!available || busy || this.panel.hidden || !this.draft || locked ||
      this.displayed?.state !== "unconfigured" || !thread.test(this.input.value)));
  }
  private configure(): void {
    if (this.save.getAttribute("aria-disabled") === "true" || !this.draft || !this.displayed) return;
    const input: RecipientInput = { version: 1, recipient: { kind: "codex-thread", threadId: this.draft.text }, policy: "conservative-metadata-v1" };
    const command = { action: "configure" as const, expectedRuntimeGeneration: this.displayed.runtimeGeneration,
      expectedConfigurationRevision: 0 as const, requestId: crypto.randomUUID(), input };
    // Freeze the one original before dispatch. Navigation never creates another.
    this.draft.original = { command: structuredClone(command), outcome: "pending" };
    void this.run(command);
  }
  private async run(command: RecipientCommand): Promise<void> {
    const context = this.context, draft = this.draft;
    if (!context || this.suspended || this.selecting || this.panel.hidden || this.request ||
        command.action === "status" && this.read.getAttribute("aria-disabled") === "true") return;
    const key = scopeKey(context), request = new AbortController(), epoch = ++this.epoch;
    const original = command.action === "configure" ? draft?.original : undefined;
    const identity: Identity = { boardId: context.boardId, incarnation: context.incarnation };
    this.request = request; this.message.textContent = command.action === "status" ? "Reading recipient policy…" : "Saving recipient policy once…"; this.update();
    const current = (): boolean => this.request === request && this.epoch === epoch && !!this.context && scopeKey(this.context) === key &&
      !this.suspended && !this.selecting && !this.panel.hidden && !request.signal.aborted;
    try {
      const view = await this.client.recipient(identity, command, request.signal);
      if (!current()) return;
      this.displayed = structuredClone(view);
      const retained = draft?.original;
      if (retained && view.state === "configured" && view.requestId === retained.command.requestId && view.input && sameRecipientInput(view.input, retained.command.input)) retained.outcome = "confirmed";
      this.paint(view);
      this.message.textContent = view.state === "configured" ? "Saved; delivery is not activated. Further saves are refused, including the same reference." :
        view.state === "unconfigured" ? "No recipient is currently saved. This observation does not prove that an earlier uncertain save had no effect." :
        "Recipient policy unavailable. Retain the original save details and inspect status deliberately.";
    } catch (error) {
      // The immutable original remains UNKNOWN even if this view went away.
      if (original && original.outcome === "pending") original.outcome = error instanceof RecipientRefusal ? "refused" : "unknown";
      if (!current()) return;
      this.displayed = undefined; this.facts.replaceChildren();
      if (error instanceof RecipientRefusal) this.message.textContent = "Recipient request refused (" + error.code + "). Draft retained; read status before another action.";
      else this.message.textContent = command.action === "configure" ? "Save outcome UNKNOWN. It may have taken effect. Inspect status; do not submit again." : "Recipient status unavailable. Draft and original save details retained.";
    } finally {
      // An ignored/late positive response cannot settle a retained original.
      if (original?.outcome === "pending") original.outcome = "unknown";
      if (current()) {
        const retained = draft?.original;
        this.originalResult.textContent = retained ? retained.outcome === "confirmed" ? "Original save confirmed by matching saved details." :
          retained.outcome === "refused" ? "Original save refused before saving." : "Original save remains UNKNOWN. Only exact matching saved details can confirm it; no new save is allowed." :
          "Current facts only. A save from a previous reload or closed window cannot be correlated here.";
      }
      if (this.request === request) { this.request = undefined; this.update(); }
    }
  }
  private paint(view: RecipientView): void {
    this.facts.replaceChildren(element("p", "Board " + view.identity.boardId + " · incarnation " + view.identity.incarnation));
    if (view.state === "configured" && view.input) this.facts.append(
      element("p", "Codex chat: " + view.input.recipient.threadId), element("p", "Policy: " + view.input.policy),
      element("p", "Configuration revision: " + view.configurationRevision),
      element("p", "Saved generation: " + view.configurationGeneration));
  }
}
