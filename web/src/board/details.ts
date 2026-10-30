import { CardRelationships } from "./relationships.js";
import type { RelationshipActionHandler } from "./relationships.js";
import type { BoardRead, CardRead, HistoryRead } from "../../generated/transport/contracts.js";
import type { Actor, Comment, Coverage, Identity, Snapshot } from "../../generated/archive/contracts.js";
import type { BoardClient } from "../api/client.js";
import { CardHierarchy } from "./hierarchy.js";
import type { HierarchyAction } from "./hierarchy.js";
import type { HierarchyCard, ParentRef } from "../api/hierarchy.js";

function node<K extends keyof HTMLElementTagNameMap>(tag: K, text = ""): HTMLElementTagNameMap[K] {
  const result = document.createElement(tag); result.textContent = text;
  result.style.whiteSpace = "pre-wrap"; result.style.overflowWrap = "anywhere";
  return result;
}
function control(label: string, run: () => void): HTMLButtonElement {
  const result = node("button", label); result.type = "button"; result.addEventListener("click", run); return result;
}
function compare(a: string, b: string): number { return a < b ? -1 : a > b ? 1 : 0; }
function origin(identity: Identity): string { return identity.boardId + "/" + identity.incarnation; }
function same(a: Identity, b: Identity): boolean { return a.boardId === b.boardId && a.incarnation === b.incarnation; }
function attribution(id: string, actors: readonly Actor[]): string {
  const actor = actors.find(row => row.actorId === id); return actor ? actor.label + " (" + id + ")" : id;
}
export function sortedComments(rows: readonly Comment[]): Comment[] {
  return [...rows].sort((a, b) => compare(a.createdAt, b.createdAt) || compare(a.commentId, b.commentId));
}
export function priorHistory(rows: readonly Snapshot[], identity: Identity, cardId: string, revision: number): Snapshot[] {
  return rows.filter(row => row.cardId === cardId && !(same(row.origin, identity) && row.cardRevision === revision)).sort((a, b) => {
    const currentA = same(a.origin, identity), currentB = same(b.origin, identity);
    return Number(currentB) - Number(currentA) || compare(a.origin.boardId, b.origin.boardId) ||
      compare(a.origin.incarnation, b.origin.incarnation) || b.cardRevision - a.cardRevision;
  });
}
function coverage(host: HTMLElement, value: Coverage): void {
  host.append(node("p", "Returned coverage: history floor " + (value.historyFloor ?? "not recorded") +
    "; activity floor " + (value.activityFloor ?? "not recorded") + "; outcome payload floor " + (value.outcomePayloadFloor ?? "not recorded") + ". Returned rows do not prove complete history."));
  for (const gap of value.gaps) host.append(node("p", "Coverage gap: " + gap.class + " · " + origin(gap.origin) + " · " +
    (gap.fromExclusive ?? "unbounded start") + " exclusive through " + gap.throughInclusive + " inclusive · " + gap.reason +
    " · recorded " + gap.recordedAt + " at board revision " + gap.boardRevision));
}
export type DetailsSelection = Readonly<{ cardId: string; identity: Identity; request: number }>;

// One board-local nonmodal reader. It never reads or writes editor/recovery state.
export class CardDetails {
  readonly host = node("section");
  private selected: string | undefined;
  private expected: Identity | undefined;
  private request = 0;
  private active = true;
  private current: CardRead | undefined;
  private history: HistoryRead | undefined;
  private opener: HTMLButtonElement | undefined;
  private readonly heading = node("h2", "Card details");
  private readonly status = node("p");
  private readonly parentAnnouncement = node("p");
  private readonly facts = node("section");
  private readonly comments = node("section");
  private readonly older = node("section");
  private readonly historicalComments = node("section");
  private commentPage = 0;
  private historyPage = 0;
  private historicalCommentPage = 0;
  private readonly hierarchy?: CardHierarchy;
  private readonly relationships?: CardRelationships;
  private readonly hierarchyFallback?: HTMLButtonElement;
  private hierarchyOrigin = false;
  constructor(private readonly client: Pick<BoardClient, "card" | "history">, private readonly fallback: HTMLButtonElement,
    private readonly archiveAction?: (read: CardRead) => void, private readonly focusRoot?: HTMLElement,
    hierarchy?: { client: Pick<BoardClient, "board">; action?: (action: HierarchyAction, card: HierarchyCard, read: BoardRead) => void; fallback?: HTMLButtonElement; relationshipAction?: RelationshipActionHandler }) {
    this.host.hidden = true; this.host.setAttribute("aria-label", "Card details"); this.host.setAttribute("role", "region");
    this.heading.tabIndex = -1; this.status.setAttribute("role", "status");
    this.parentAnnouncement.setAttribute("role", "status"); this.parentAnnouncement.setAttribute("aria-label", "Current parent announcement");
    this.facts.setAttribute("aria-label", "Current card state"); this.comments.setAttribute("aria-label", "Card comments");
    this.older.setAttribute("aria-label", "Prior card history");
    this.historicalComments.setAttribute("aria-label", "History comments");
    this.host.append(this.heading, control("Close card details", () => this.close()),
      control("Refresh card details", () => { void this.refresh(); }), this.status, this.parentAnnouncement, this.facts, this.comments, this.older, this.historicalComments);
    if (hierarchy) {
      this.hierarchyFallback = hierarchy.fallback;
      this.hierarchy = new CardHierarchy(hierarchy.client, (cardId, identity, opener) => this.open(cardId, identity, opener, true), hierarchy.action);
      this.host.insertBefore(this.hierarchy.host, this.comments);
      this.relationships = new CardRelationships(hierarchy.client, (cardId, identity, opener) => this.open(cardId, identity, opener, true), hierarchy.relationshipAction);
      this.host.insertBefore(this.relationships.host, this.comments);
    }
  }
  open(cardId: string, identity: Identity, opener: HTMLButtonElement, hierarchyOrigin = false): void {
    this.request++; this.selected = cardId; this.expected = identity; this.opener = opener;
    this.hierarchyOrigin = hierarchyOrigin;
    this.current = undefined; this.history = undefined; this.host.hidden = false;
    this.parentAnnouncement.textContent = "";
    this.heading.textContent = "Card details · " + cardId;
    this.facts.replaceChildren(); this.comments.replaceChildren(); this.older.replaceChildren(); this.historicalComments.replaceChildren();
    this.hierarchy?.open(cardId, identity); this.relationships?.open(cardId, identity);
    this.heading.focus(); void this.refresh();
  }
  close(): void {
    this.request++; this.selected = undefined; this.host.hidden = true;
    this.hierarchy?.close(); this.relationships?.close();
    const scope = this.focusRoot ?? this.fallback.parentElement;
    const replacement = scope?.isConnected ? [...scope.querySelectorAll<HTMLButtonElement>("button[data-open-card]")]
      .find(row => row.dataset.focusKey === this.opener?.dataset.focusKey && row.dataset.openCard === this.opener?.dataset.openCard && !row.disabled && row.getClientRects().length > 0)
      : undefined;
    const target = replacement ?? (this.hierarchyOrigin ? this.hierarchyFallback ?? this.fallback : this.fallback);
    if (target.isConnected && !target.disabled && target.getClientRects().length > 0) target.focus();
  }
  suspend(): void { this.active = false; this.invalidate(); this.hierarchy?.suspend(); this.relationships?.suspend(); }
  resume(): void { this.active = true; this.hierarchy?.resume(); this.relationships?.resume(); }
  invalidate(): void {
    this.request++; this.history = undefined;
    this.parentAnnouncement.textContent = "";
    this.hierarchy?.markStale(); this.relationships?.markStale();
    this.historicalComments.replaceChildren(node("p", "History comments stale; a deliberate refresh is required."));
    if (this.selected) {
      this.status.textContent = "Card details stale. Deliberately refresh card details to read current facts.";
      this.older.replaceChildren(node("p", "History stale; a deliberate refresh is required."));
    }
  }
  boardRead(identity: Identity): void {
    if (!this.selected) return;
    if (!this.expected || !same(identity, this.expected)) { this.close(); return; }
    void this.refresh();
  }
  completeBoardRead(read: BoardRead): void { this.hierarchy?.boardRead(read); this.relationships?.boardRead(read); }
  captureSelection(): DetailsSelection | undefined {
    return this.active && this.selected && this.expected ? { cardId: this.selected, identity: { ...this.expected }, request: this.request } : undefined;
  }
  refreshAfterHierarchy(selection: DetailsSelection, cardId: string, boardRevision: number, cardRevision: number, created = false): boolean {
    if (!this.active || this.host.hidden || selection.request !== this.request || this.selected !== selection.cardId ||
        !this.expected || !same(selection.identity, this.expected) || !created && cardId !== selection.cardId) return false;
    this.hierarchy?.markStale(); this.relationships?.markStale(); this.parentAnnouncement.textContent = "";
    if (created) {
      // Follow only this still-selected create-child source. A receipt supplies
      // identity, never text; no focus or editor/base/recovery state changes.
      this.selected = cardId; this.current = undefined; this.history = undefined;
      this.heading.textContent = "Card details · " + cardId;
      this.facts.replaceChildren(); this.comments.replaceChildren(); this.older.replaceChildren(); this.historicalComments.replaceChildren();
      this.hierarchy?.open(cardId, this.expected); this.relationships?.open(cardId, this.expected);
    }
    void this.refresh({ boardRevision, cardRevision, announceParent: true }); return true;
  }
  refreshAfterArchive(identity: Identity, cardId: string, boardRevision: number, cardRevision: number): void {
    if (!this.active || this.selected !== cardId || !this.expected || !same(identity, this.expected)) return;
    // The terminal receipt triggers a new authoritative read; it supplies no
    // current card facts. A response older than that receipt is still stale.
    void this.refresh({ boardRevision, cardRevision });
  }
  async refresh(minimum?: Readonly<{ boardRevision: number; cardRevision: number; announceParent?: boolean }>): Promise<void> {
    if (!this.active || !this.selected || !this.expected) return;
    const generation = ++this.request, cardId = this.selected, identity = this.expected;
    const valid = (): boolean => this.active && generation === this.request && this.selected === cardId;
    this.commentPage = 0; this.historyPage = 0; this.historicalCommentPage = 0; this.history = undefined;
    this.parentAnnouncement.textContent = "";
    this.status.textContent = "Reading current card and separate history snapshots…";
    this.older.replaceChildren(node("p", "History unavailable while reading."));
    this.historicalComments.replaceChildren(node("p", "History comments unavailable while reading."));
    const currentPromise = this.client.card(identity.boardId, cardId);
    const historyPromise = this.client.history(identity.boardId, cardId);
    const [current, history] = await Promise.allSettled([currentPromise, historyPromise]);
    if (!valid()) return;
    this.historicalComments.replaceChildren(node("p", "History comments unavailable without matching current and history snapshots."));
    if (current.status !== "fulfilled" || !same(current.value.result.identity, identity) || current.value.result.card.cardId !== cardId ||
        minimum && (current.value.result.revision < minimum.boardRevision || current.value.result.card.revision < minimum.cardRevision)) {
      this.status.textContent = this.current ? "Current card unavailable; displayed confirmed facts are stale. Retry deliberately." : "Current card unavailable. Retry deliberately.";
      this.older.replaceChildren(node("p", "History unavailable without a matching current card read.")); return;
    }
    this.current = current.value;
    this.hierarchy?.observeRevision(current.value.result.identity, current.value.result.revision); this.relationships?.observeRevision(current.value.result.identity, current.value.result.revision);
    this.renderCurrent();
    if (minimum?.announceParent) {
      const c = current.value.result, parent = (c.card as { parent?: ParentRef }).parent;
      this.parentAnnouncement.textContent = "Current parent for child " + c.card.cardId + ": " + (parent ? origin(parent) + "/" + parent.cardId : "No parent") + " · board revision " + c.revision + ", card revision " + c.card.revision + ".";
    }
    if (history.status !== "fulfilled") {
      this.status.textContent = "Current card read. History unavailable; retry deliberately."; return;
    }
    const h = history.value.result, c = current.value.result;
    if (!same(h.identity, c.identity) || h.cardId !== cardId || h.revision !== c.revision || h.cardRevision !== c.card.revision) {
      this.status.textContent = "Current card read. History stale: separate snapshot identities or revisions disagree. Retry deliberately.";
      this.older.replaceChildren(node("p", "History stale; no rows combined with current state.")); return;
    }
    this.history = history.value; this.renderHistory(); this.renderHistoricalComments();
    this.status.textContent = "Refreshed returned coverage: " + c.comments.length + " comments and " +
      priorHistory(h.records, h.identity, cardId, h.cardRevision).length + " prior history rows and " + h.comments.length + " history comments. Pages reset to 1. Board revision " + c.revision + ", card revision " + c.card.revision + ".";
  }
  private page<T>(host: HTMLElement, rows: readonly T[], page: number, label: string, change: (page: number) => void, render: (row: T) => HTMLElement): void {
    const start = page * 20, stop = Math.min(rows.length, start + 20), list = node("div");
    host.replaceChildren(node("h3", label), node("p", rows.length ? label + " " + (start + 1) + "–" + stop + " of " + rows.length + " returned" : "No " + label.toLowerCase() + " returned."));
    const move = (value: number, label: string): void => {
      change(value);
      const replacement = [...host.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label && !row.disabled);
      (replacement ?? [...host.querySelectorAll<HTMLButtonElement>("button")].find(row => !row.disabled))?.focus();
    };
    const previous = control("Previous " + label.toLowerCase(), () => move(page - 1, "Previous " + label.toLowerCase())); previous.disabled = page === 0;
    const next = control("Next " + label.toLowerCase(), () => move(page + 1, "Next " + label.toLowerCase())); next.disabled = stop >= rows.length;
    host.append(previous, next, list);
    for (const row of rows.slice(start, stop)) list.append(render(row));
  }
  private renderCurrent(): void {
    const focusedArchive = this.facts.contains(document.activeElement) && (document.activeElement as HTMLElement)?.dataset.focusKey;
    const c = this.current!.result, card = c.card;
    this.heading.textContent = "Card details · #" + card.ticket + " " + card.title;
    this.facts.replaceChildren(node("h3", "Current card state"), node("p", "Card UUID: " + card.cardId),
      node("p", "Origin: " + origin(c.identity) + " · created in " + origin(card.createdIn)),
      node("p", "Title: " + card.title), node("p", card.description === "" ? "Description: empty" : "Description:\n" + card.description),
      node("p", "Lane: " + card.lane + " · Priority: " + card.priority + " · Owner: " + (card.ownerId ? attribution(card.ownerId, c.actors) : "Unassigned")),
      node("p", "Board revision " + c.revision + " · card revision " + card.revision + " · archived: " + card.archived + " · approval: " + (card.currentApprovalId ?? "none")));
    const parent = (card as { parent?: ParentRef }).parent;
    this.facts.append(node("p", parent ? "Current parent reference: " + origin(parent) + "/" + parent.cardId : "Current parent reference: No parent"));
    if (this.archiveAction) {
      const archive = control(card.archived ? "Unarchive card" : "Archive card", () => {
        archive.focus({ preventScroll: true }); this.archiveAction!(this.current!);
      });
      archive.dataset.focusKey = card.cardId + ":details:archive";
      archive.disabled = card.lane === "Completed"; this.facts.append(archive);
      if (focusedArchive === archive.dataset.focusKey && !archive.disabled) archive.focus({ preventScroll: true });
    }
    const links = c.links.filter(row => row.cardId === card.cardId);
    if (!this.relationships) {
    this.facts.append(node("h3", "Existing relationships"));
    if (!links.length) this.facts.append(node("p", "No relationships returned."));
    for (const link of links) this.facts.append(node("p", link.kind + " → " + origin(link.target) + "/" + link.target.cardId + " · " + (link.active ? "active" : "inactive") + " · " + link.linkId));
    }
    coverage(this.facts, c.coverage); this.renderComments();
  }
  private renderComments(): void {
    const c = this.current!.result;
    this.page(this.comments, sortedComments(c.comments), this.commentPage, "Comments", page => { this.commentPage = page; this.renderComments(); }, row => {
      const article = node("article"); article.append(node("p", attribution(row.actorId, c.actors) + " · " + row.createdAt + " · " + row.commentId + " · " + origin(row.createdIn)), node("p", row.text)); return article;
    });
  }
  private renderHistory(): void {
    const h = this.history!.result;
    this.page(this.older, priorHistory(h.records, h.identity, h.cardId, h.cardRevision), this.historyPage, "Prior history", page => { this.historyPage = page; this.renderHistory(); }, row => {
      const article = node("article");
      article.append(node("h4", "Origin " + origin(row.origin) + " · card revision " + row.cardRevision),
        node("p", attribution(row.actorId, h.actors) + " · " + row.committedAt));
      if (row.state) article.append(node("p", "#" + row.state.ticket + " " + row.state.title), node("p", "Description:\n" + row.state.description),
        node("p", row.state.lane + " · " + row.state.priority + " · Owner: " + (row.state.ownerId ? attribution(row.state.ownerId, h.actors) : "Unassigned")),
        node("p", "Archived: " + row.state.archived + " · approval: " + (row.state.currentApprovalId ?? "none")));
      else article.append(node("p", "Old text unavailable; compact or removed state."));
      if (row.state) {
        const parent = (row.state as { parent?: ParentRef }).parent;
        article.append(node("p", parent ? "Historical parent: " + origin(parent) + "/" + parent.cardId + " · unresolved/unavailable in this history view" : "Historical parent: No parent"));
      }
      if (row.removal) article.append(node("p", "State removed: " + row.removal.reason + " · " + row.removal.removedAt + " · board revision " + row.removal.boardRevision));
      const commentReferences = row.references.commentIds.map(id => {
        const comment = h.comments.find(value => value.commentId === id);
        return comment ? id + " (" + origin(comment.createdIn) + ")" : id + " (old text unavailable)";
      });
      article.append(node("p", "Comment references: " + commentReferences.length + (commentReferences.length ? " · " + commentReferences.join(", ") + ". Read History comments for returned facts." : ".")));
      for (const id of row.references.linkIds) {
        const link = h.links.find(value => value.linkId === id);
        article.append(node("p", link ? "Relationship " + id + " · " + link.kind + " → " + origin(link.target) + "/" + link.target.cardId + " · " + (link.active ? "active" : "inactive") : "Relationship " + id + ": unavailable."));
      }
      for (const id of row.references.attachmentIds) article.append(node("p", "Attachment reference " + id));
      for (const id of row.references.approvalIds) article.append(node("p", "Approval reference " + id));
      return article;
    });
    coverage(this.older, h.coverage);
    for (const row of h.tombstones) this.older.append(node("p", "Removed " + row.kind + " " + row.id + " · " + origin(row.createdIn) + " · " + row.reason + " · " + row.removedAt +
      " · actor: " + (row.actorId ? attribution(row.actorId, h.actors) : "unavailable") + " · created: " + (row.createdAt ?? "unavailable") + ". Old text unavailable."));
  }
  private renderHistoricalComments(): void {
    const h = this.history!.result;
    this.page(this.historicalComments, sortedComments(h.comments), this.historicalCommentPage, "History comments", page => {
      this.historicalCommentPage = page; this.renderHistoricalComments();
    }, row => {
      const article = node("article");
      article.append(node("p", attribution(row.actorId, h.actors) + " · " + row.createdAt + " · " + row.commentId + " · " + origin(row.createdIn)), node("p", row.text));
      return article;
    });
  }
}
