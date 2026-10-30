import type { BoardRead } from "../../generated/transport/contracts.js";
import type { Identity } from "../../generated/archive/contracts.js";
import type { BoardClient } from "../api/client.js";
import { captureHierarchy, sameIdentity } from "../api/hierarchy.js";
import type { HierarchyCard, HierarchySnapshot } from "../api/hierarchy.js";

function node<K extends keyof HTMLElementTagNameMap>(tag: K, text = ""): HTMLElementTagNameMap[K] {
  const value = document.createElement(tag); value.textContent = text; value.style.overflowWrap = "anywhere"; return value;
}
function button(label: string, run: () => void): HTMLButtonElement {
  const value = node("button", label); value.type = "button"; value.addEventListener("click", run); return value;
}
export type HierarchyAction = "create-child" | "set-parent" | "clear-parent";
export class CardHierarchy {
  readonly host = node("section");
  private latest?: BoardRead;
  private knownRevision = 0;
  private snapshot?: HierarchySnapshot;
  private selection?: { identity: Identity; cardId: string };
  private page = 0;
  private stale = false;
  private active = true;
  private generation = 0;
  private message = "";
  constructor(private readonly client: Pick<BoardClient, "board">,
    private readonly navigate: (cardId: string, identity: Identity, opener: HTMLButtonElement) => void,
    private readonly action?: (action: HierarchyAction, card: HierarchyCard, read: BoardRead) => void) {
    this.host.setAttribute("aria-label", "Card hierarchy");
  }
  boardRead(read: BoardRead): void {
    this.knownRevision = this.latest && sameIdentity(this.latest.result.identity, read.result.identity) ? Math.max(this.knownRevision, read.result.revision) : read.result.revision;
    this.latest = structuredClone(read);
    if (this.selection && !sameIdentity(this.selection.identity, read.result.identity)) { this.close(); return; }
    if (this.snapshot && this.snapshot.read.result.revision !== read.result.revision) this.markStale();
  }
  observeRevision(identity: Identity, revision: number): void {
    if (!this.latest || !sameIdentity(this.latest.result.identity, identity)) return;
    this.knownRevision = Math.max(this.knownRevision, revision);
    if (this.snapshot && revision > this.snapshot.read.result.revision) this.markStale();
  }
  open(cardId: string, identity: Identity): void {
    this.generation++; this.selection = { cardId, identity }; this.page = 0; this.snapshot = undefined; this.message = "";
    if (this.latest && sameIdentity(identity, this.latest.result.identity)) {
      try { this.snapshot = captureHierarchy(this.latest, cardId); } catch { /* Complete current target unavailable. */ }
    }
    this.stale = !this.snapshot || this.snapshot.read.result.revision < this.knownRevision; this.render();
  }
  close(): void { this.generation++; this.selection = undefined; this.snapshot = undefined; this.host.replaceChildren(); }
  suspend(): void { this.active = false; this.generation++; this.markStale(); }
  resume(): void { this.active = true; }
  markStale(): void { this.stale = true; this.generation++; if (this.selection) this.render(); }
  async readCurrent(): Promise<void> {
    if (!this.selection || !this.active) return;
    const selection = this.selection, generation = ++this.generation;
    this.message = "Reading current hierarchy…"; this.render();
    try {
      const read = await this.client.board(selection.identity.boardId);
      if (!this.active || generation !== this.generation || this.selection !== selection) return;
      if (!sameIdentity(read.result.identity, selection.identity)) throw Error("Identity changed");
      const snapshot = captureHierarchy(read, selection.cardId);
      if (read.result.revision < this.knownRevision) throw Error("Stale read");
      this.latest = read; this.knownRevision = read.result.revision; this.snapshot = snapshot; this.page = 0; this.stale = false;
      this.message = "Current hierarchy read. Pages reset to 1.";
    } catch {
      if (!this.active || generation !== this.generation || this.selection !== selection) return;
      this.stale = true; this.message = "Current hierarchy unavailable; retained snapshot and private input remain. Retry deliberately.";
    }
    this.render();
    // A read completion updates facts, never focus or editor/recovery state.
  }
  private render(): void {
    const focused = this.host.contains(document.activeElement) ? document.activeElement as HTMLButtonElement : undefined;
    const focusKey = focused?.dataset.focusKey, focusLabel = focused?.textContent;
    const retainFocus = (): void => {
      if (!focused) return;
      const controls = [...this.host.querySelectorAll<HTMLButtonElement>("button")];
      const replacement = controls.find(control => !control.disabled && (focusKey ? control.dataset.focusKey === focusKey : control.textContent === focusLabel));
      (replacement ?? controls[0])?.focus({ preventScroll: true });
    };
    this.host.replaceChildren(node("h3", "Parent and Children"));
    const refresh = button("Read current hierarchy", () => { void this.readCurrent(); });
    const status = node("p", this.message + (this.stale ? " Hierarchy stale; read current hierarchy deliberately." : "")); status.setAttribute("role", "status");
    this.host.append(refresh, status);
    const snapshot = this.snapshot;
    if (!snapshot) { this.host.append(node("p", "Hierarchy unavailable without a complete validated board snapshot.")); retainFocus(); return; }
    const { read, root, parent, directChildCount, nodes } = snapshot, identity = read.result.identity;
    const open = (card: HierarchyCard, label: string): HTMLButtonElement => {
      const control = button(label + " #" + card.ticket + " " + card.title, () => {
        if (this.active) this.navigate(card.cardId, identity, control);
      });
      control.dataset.openCard = card.cardId; control.dataset.focusKey = root.cardId + ":hierarchy:" + card.cardId;
      return control;
    };
    this.host.append(node("p", "Hierarchy board " + identity.boardId + " · incarnation " + identity.incarnation + " · source revision " + read.result.revision));
    if (parent) this.host.append(node("p", "Parent: #" + parent.ticket + " · " + parent.cardId + " · " + parent.lane + " · " + (parent.archived ? "Archived" : "Active")), open(parent, "Open parent"));
    else this.host.append(node("p", "No parent"));
    this.host.append(node("p", "Children: " + directChildCount + " direct · " + nodes.length + " descendants"));
    if (this.action) for (const [action, label] of [["create-child", "Create child"], ["set-parent", "Set parent"], ["clear-parent", "Clear parent"]] as const) {
      const control = button(label, () => { control.focus({ preventScroll: true }); if (this.active && !this.stale) this.action!(action, root, read); });
      control.dataset.focusKey = root.cardId + ":hierarchy:" + action;
      control.disabled = this.stale || action !== "create-child" && root.lane === "Completed";
      this.host.append(control);
    }
    const count = nodes.length, start = this.page * 20, stop = Math.min(count, start + 20), pages = Math.max(1, Math.ceil(count / 20));
    this.host.append(node("p", "Children " + (count ? start + 1 : 0) + "–" + stop + " of " + count + " · page " + (this.page + 1) + " of " + pages + (stop >= count ? " · End" : "")));
    const move = (delta: number): void => {
      if (!this.active) return; this.page += delta; this.render();
      const controls = [...this.host.querySelectorAll<HTMLButtonElement>("button")];
      (controls.find(control => control.textContent === (delta > 0 ? "Next children" : "Previous children") && !control.disabled) ?? controls.find(control => control.textContent === (delta > 0 ? "Previous children" : "Next children") && !control.disabled) ?? controls[0])?.focus();
    };
    const first = button("First children page", () => {
      if (!this.active) return; this.page = 0; this.render();
      [...this.host.querySelectorAll<HTMLButtonElement>("button")].find(control => control.textContent === "Read current hierarchy")?.focus();
    });
    const previous = button("Previous children", () => move(-1)), next = button("Next children", () => move(1));
    first.disabled = this.page === 0; previous.disabled = this.page === 0; next.disabled = stop >= count; this.host.append(first, previous, next);
    for (const row of nodes.slice(start, stop)) {
      const card = row.card, article = node("article"); article.dataset.hierarchyCard = card.cardId;
      const owner = read.result.actors.find(actor => actor.actorId === card.ownerId);
      article.append(node("p", "Depth " + row.depth + " · #" + card.ticket + " · UUID " + card.cardId + " · " + card.title),
        node("p", card.lane + " · " + card.priority + " · Owner: " + (owner ? owner.label + " (" + owner.actorId + ")" : card.ownerId ?? "Unassigned") + " · " + (card.archived ? "Archived" : "Active")),
        node("p", "Direct parent: " + card.parent!.boardId + "/" + card.parent!.incarnation + "/" + card.parent!.cardId + " · " + row.directChildCount + " direct children · " + row.descendantCount + " descendants"), open(card, "Open child"));
      this.host.append(article);
    }
    retainFocus();
  }
}
