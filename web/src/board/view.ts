import { WorkflowEditor, type WorkflowDraft } from "../workspace-ui/workflow-editor.js";
import { completedLane, laneText, moveLanes, roleLane, workflowEdge, workflowRight, workflowLanes } from "../api/workflow.js";
import { completeOwnedRelationships, relationshipKinds, forwardLabels, validRelationships } from "../api/relationships.js";
import type { RelationshipRow } from "../api/relationships.js";
import type { RelationshipAction } from "./relationships.js";
import type { BoardRead, CardFileRead, MutationRequest, SessionRead, CatalogCount } from "../../generated/transport/contracts.js";
import type { Card, Identity } from "../../generated/archive/contracts.js";
import type { BoardCommand, Value } from "../api/boundary.js";
import { BoardClient, WorkflowPreviewRefusal } from "../api/client.js";
import type { OriginalOutcome } from "../api/client.js";
import type { Context } from "../recovery/types.js";
import { recoveryCoordinator } from "../recovery-ui/coordination.js";
import type { RecoveryCoordinator } from "../recovery-ui/coordination.js";
import { dependencyAdvisory } from "./advisory.js";
import { canonical, commands, parse, validate } from "../api/boundary.js";
import type { EditorReference } from "../session-ui/window-state.js";
import { CardDetails, presentCover } from "./details.js";
import type { DetailsSelection } from "./details.js";
import type { CoverEdit, FileBase } from "./files.js";
import type { CoverPreview } from "./cover-preview.js";
import { CardSearch } from "./search.js";
import { BoardActivity } from "./activity.js";
import { BoardNotices } from "./notices.js";
import { BoardRecipient } from "./recipient.js";
import type { PrivateSearch } from "./search.js";
import { sameIdentity, validParentRef, validateBoardHierarchy, hierarchyOrder } from "../api/hierarchy.js";
import type { ParentRef, HierarchyCard } from "../api/hierarchy.js";
import type { HierarchyAction } from "./hierarchy.js";

type DetachFile = FileBase & { attachment: CardFileRead["result"]["file"]["attachment"] };

function coverMatches(base: BoardRead, card: Card, cover: CoverEdit): boolean {
  const { identity, revision, attachments } = base.result, file = cover.attachment;
  if (!sameIdentity(identity, cover.identity) || cover.revision !== revision ||
      card.cardId !== cover.cardId || card.revision !== cover.cardRevision) return false;
  if (!file) return card.coverAttachmentId !== undefined;
  const current = attachments.find(row => row.attachmentId === file.attachmentId && row.cardId === card.cardId);
  return !!current && card.attachmentIds.includes(file.attachmentId) &&
    canonical(current as unknown as Value) === canonical(file as unknown as Value);
}

type HierarchyEdit = { action: HierarchyAction; parent?: ParentRef };

function element<K extends keyof HTMLElementTagNameMap>(tag: K, text = ""): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag); node.textContent = text; return node;
}
function button(text: string, run: () => void): HTMLButtonElement {
  const node = element("button", text); node.type = "button"; node.addEventListener("click", run); return node;
}
type Editor = {
  id: string; revision: number; command: BoardCommand; context: Context; base: BoardRead;
  card: Card | undefined; fields: Record<string, HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>;
  form: HTMLFormElement; recovery: HTMLElement; save: HTMLButtonElement; composing: boolean;
  sequence: number; submitted: boolean; persisted: number; start: boolean; genericMove?: boolean; archive?: boolean;
  workflowEditor?: WorkflowEditor; workflowPreview?: string; workflowPreviewBusy?: boolean; workflowPreviewSequence?: number; workflowPreviewFailure?: string;
  hierarchy?: HierarchyEdit;
  detach?: DetachFile;
  cover?: CoverEdit;
  coverDispatching?: boolean;
  relationshipsReplace?: boolean;
  renderRelationships?: () => void;
  hierarchyFollow?: { selection: DetailsSelection; generation: number };
  initial: string; initialFields: Record<string, string>; opener: HTMLElement | null; confirmation: HTMLElement; confirming: boolean; confirmingEnter: boolean;
};

export type ArchiveStatus = "active" | "archived" | "all";
export type BoardViewOptions = Readonly<{ archiveStatus?: ArchiveStatus; archiveCheckpoint?: (status: ArchiveStatus) => void; search?: PrivateSearch; searchCheckpoint?: (state: PrivateSearch) => void }>;
// The complete source graph remains intact for reference and advisory validation.
export function visibleCards(cards: readonly Card[], status: ArchiveStatus): Card[] {
  const rank = ["P0", "P1", "P2", "P3", "P4", "P5"];
  return cards.filter(card => status === "all" || card.archived === (status === "archived"))
    .sort((a, b) => rank.indexOf(a.priority) - rank.indexOf(b.priority) || a.ticket - b.ticket || (a.cardId < b.cardId ? -1 : a.cardId > b.cardId ? 1 : 0));
}

// DOM-only view. Every prose-bearing field is rendered as inert text.
export class BoardView {
  private board: BoardRead | undefined;
  private session: SessionRead | undefined;
  private editor: Editor | undefined;
  private stale = true;
  private readonly heading = element("h1", "Board unavailable");
  private readonly readState = element("p", "Unavailable; no current board loaded.");
  private readonly actor = element("p");
  private readonly cards = element("section");
  private readonly create = button("Create card", () => this.edit("card.create"));
  private readonly workflowEdit = button("Edit workflow", () => { this.workflowEdit.focus(); this.edit("workflow.edit"); });
  private readonly editorHost = element("section");
  private readonly current = element("section");
  private readonly outcomes = element("section");
  private readonly records = new Map<string, OriginalOutcome>();
  private readonly restoredCommits = new Set<string>();
  private readonly outcomeNodes = new Map<string, HTMLElement>();
  private destroyed = false;
  private generation = 0;
  private suspended = false;
  private refreshing = false;
  private readonly persistence: RecoveryCoordinator;
  private readonly stopPersistence: () => void;
  private readonly details: CardDetails;
  private readonly stopRevision: () => void;
  private readonly search: CardSearch;
  private readonly activity: BoardActivity;
  private readonly notices: BoardNotices;
  private readonly recipient: BoardRecipient;
  private readonly archiveStatus = element("select");
  private readonly cardCovers: CoverPreview[] = [];
  private observed?: { identity: Identity; revision: number };
  constructor(private readonly root: HTMLElement, readonly client: BoardClient, readonly boardId: string,
    private readonly checkpoint: (reference?: EditorReference) => void = () => {}, private readonly options: BoardViewOptions = {}) {
    this.persistence = recoveryCoordinator(client);
    this.stopPersistence = this.persistence.subscribe(() => {
      if (this.editor) { this.editor.sequence++; this.editor.recovery.textContent = "Private discard pending; visible input retained. Later deliberate edits are saved after the boundary."; }
    });
    this.heading.tabIndex = -1;
    this.readState.setAttribute("role", "status");
    this.outcomes.setAttribute("aria-label", "Original operation outcomes");
    this.current.setAttribute("aria-label", "Current confirmed text");
    this.cards.setAttribute("aria-label", "Cards");
    const read = button("Read current board", () => { void this.refresh(); });
    this.archiveStatus.setAttribute("aria-label", "Archive status");
    for (const [value, label] of [["active", "Active"], ["archived", "Archived"], ["all", "All"]]) {
      const option = element("option", label); option.value = value!; this.archiveStatus.append(option);
    }
    this.archiveStatus.value = options.archiveStatus ?? "active";
    this.archiveStatus.addEventListener("change", () => {
      if (this.destroyed || this.suspended) return;
      try { options.archiveCheckpoint?.(this.archiveStatus.value as ArchiveStatus); }
      catch { this.readState.textContent = "Archive status retained for this window lifetime; window storage unavailable."; }
      if (this.board) this.renderCards();
    });
    this.search = new CardSearch(client, boardId, this.archiveStatus, (cardId, identity, opener) => {
      if (!this.destroyed && !this.suspended) this.details.open(cardId, identity, opener);
    }, options.searchCheckpoint, options.search);
    this.activity = new BoardActivity(client);
    this.notices = new BoardNotices(client);
    this.recipient = new BoardRecipient(client);
    this.details = new CardDetails(client, this.search.find, focused => {
      const current = this.board, card = current?.result.cards.find(row => row.cardId === focused.result.card.cardId);
      if (!current || !card || current.result.identity.boardId !== focused.result.identity.boardId ||
          current.result.identity.incarnation !== focused.result.identity.incarnation || current.result.revision !== focused.result.revision || card.revision !== focused.result.card.revision) {
        this.readState.textContent = "Read current board before archiving this focused card; its independently read facts differ from the board base."; return;
      }
      this.edit("card.edit", card, false, undefined, !card.archived);
    }, root, { client, action: (action, card, read) => this.hierarchyEdit(action, card, read), fallback: read, relationshipAction: (action, card, read, row) => this.relationshipEdit(action, card, read, row) }, client, read => this.detachFile(read), read => this.canDetachFile(read.result, read.result.file.attachment.attachmentId),
      { coverBytes: (selection, attachment, signal) => client.coverBytes(selection, attachment, signal),
        edit: cover => this.editCover(cover), allowed: cover => this.canEditCover(cover), refresh: () => this.refresh(),
        observeRevision: (identity, revision) => this.observeRevision(identity, revision) });
    this.stopRevision = client.onReadRevision?.((identity, revision) => this.details.observeRevision(identity, revision)) ?? (() => {});
    // Append new controls after existing children so persisted focus paths keep their indices.
    root.replaceChildren(this.heading, this.actor, this.readState, read,
      this.create, this.workflowEdit, this.search.host, this.cards, this.details.host, this.editorHost, this.current, this.outcomes, this.activity.host, this.notices.host, this.recipient.host);
  }
  async open(): Promise<void> { await this.refresh(); }
  suspend(): void { this.suspended = true; this.refreshing = false; this.generation++; this.clearCardCovers(); this.details.suspend(); this.search.suspend(); this.activity.reset(); this.notices.suspend(); this.recipient.suspend(); }
  noticesSelectionChanging(pending: boolean): void { this.notices.selectionChanging(pending); this.recipient.selectionChanging(pending); }
  resume(): void {
    this.suspended = false;
    this.details.resume();
    this.search.resume();
    this.notices.resume();
    this.recipient.resume();
    for (const outcome of this.records.values()) {
      const context = outcome.evidence?.context ?? this.editor?.context;
      if (context) this.showOutcome(outcome, context);
      this.reflectCommitted(outcome);
    }
    if (this.board && !this.stale) this.renderCards();
    this.updateSave();
  }
  get canLeave(): boolean { return !this.editor || this.editor.persisted === this.editor.sequence; }
  async flushPrivateInput(): Promise<boolean> { await this.persistence.settled(); return this.canLeave; }
  discardInput(): void {
    this.editor?.workflowEditor?.destroy(); this.editor = undefined; this.editorHost.replaceChildren(); this.current.replaceChildren();
    try { this.checkpoint(); } catch { this.readState.textContent = "Current editor input discarded in memory. Window storage is unavailable; existing private records may remain in recovery."; }
  }
  async restore(reference: EditorReference): Promise<void> {
    const generation = this.generation, persistenceGeneration = this.persistence.generation;
    try {
      if (reference.context.boardId !== this.boardId) return;
      const draft = await this.persistence.read(() => this.client.recovery.loadDraft(reference.context, reference.draftId));
      if (!draft || this.destroyed || this.editor || generation !== this.generation) return;
      const payload = parse(draft.input) as unknown as { command: BoardCommand; base: BoardRead; card?: Card; input: Record<string, Value>; start?: boolean; genericMove?: boolean; hierarchy?: HierarchyEdit; detach?: DetachFile; cover?: CoverEdit; target?: { cardId: string }; expectedRevision?: number; expectedCardRevision?: number };
      if (!payload || typeof payload !== "object" || typeof payload.command !== "string" || !Object.hasOwn(commands, payload.command) || ["attachment.add", "history.clear", "retention.set"].includes(payload.command) || !payload.base || !payload.input) return;
      validate("transport:BoardRead", payload.base as unknown as import("../api/boundary.js").Value);
      validateBoardHierarchy(payload.base);
      const identity = payload.base.result.identity;
      if (identity.boardId !== reference.context.boardId || identity.incarnation !== reference.context.incarnation || this.session?.actorId !== reference.context.actorId) return;
      const context = this.client.context(payload.base, this.session);
      if (Object.entries(context).some(([key, value]) => value !== reference.context[key as keyof Context])) return;
      const card = payload.card ? payload.base.result.cards.find(c => c.cardId === payload.card!.cardId) : undefined;
      if (payload.card && !card) return;
      const archive = Object.hasOwn(payload.input, "archived") ? payload.input.archived : undefined;
      if (archive !== undefined && (typeof archive !== "boolean" || payload.command !== "card.edit" || !card || Object.keys(payload.input).length !== 1 || payload.start)) return;
      if (Object.hasOwn(payload.input, "laneId") && (payload.command !== "card.edit" || !card || !payload.start || !payload.base.result.workflow || Object.keys(payload.input).length !== 1 || typeof payload.input.laneId !== "string" || !workflowLanes(payload.base.result.workflow).some(lane => lane.id === payload.input.laneId && ["initial", "in_progress", "waiting"].includes(lane.role)))) return;
      let hierarchy: HierarchyEdit | undefined;
      if (payload.hierarchy && (!Object.hasOwn(payload.input, "parent") || !["create-child", "set-parent", "clear-parent"].includes(payload.hierarchy.action))) return;
      if (Object.hasOwn(payload.input, "parent")) {
        const parent = payload.input.parent;
        if (payload.start || archive !== undefined || parent !== null && !validParentRef(parent, identity)) return;
        if (payload.command === "card.edit") {
          if (!card || Object.keys(payload.input).length !== 1 || completedLane(card.lane, payload.base.result.workflow)) return;
          if (payload.hierarchy?.action === "create-child" || payload.hierarchy?.action === "clear-parent" && parent !== null) return;
          hierarchy = parent === null && payload.hierarchy?.action !== "set-parent" ? { action: "clear-parent" } : { action: "set-parent" };
        } else if (payload.command === "card.create" && parent !== null) hierarchy = { action: "create-child", parent: parent as ParentRef };
        else return;
        if (parent !== null && !payload.base.result.cards.some(row => row.cardId === (parent as ParentRef).cardId)) return;
      }
      if (Object.hasOwn(payload.input, "relationships") && (payload.command !== "card.create" && payload.command !== "card.edit" || payload.command === "card.edit" && (!card || Object.keys(payload.input).length !== 1))) return;
      if (payload.command === "attachment.detach") {
        const file = payload.detach;
        if (!card || !file || file.identity.boardId !== identity.boardId || file.identity.incarnation !== identity.incarnation || file.cardId !== card.cardId || file.revision !== payload.base.result.revision || file.cardRevision !== card.revision || payload.input.attachmentId !== file.attachment.attachmentId || Object.keys(payload.input).length !== 1 || !card.attachmentIds.includes(file.attachment.attachmentId)) return;
        validate("archive:Attachment", file.attachment as unknown as Value);
        if (file.attachment.cardId !== card.cardId) return;
      } else if (payload.detach) return;
      if (Object.hasOwn(payload.input, "coverAttachmentId") || Object.hasOwn(payload, "cover")) {
        const cover = payload.cover;
        if (payload.command !== "card.edit" || !card || !cover || payload.start || payload.genericMove || hierarchy || archive !== undefined ||
            Object.keys(payload.input).length !== 1 || payload.target?.cardId !== card.cardId ||
            payload.expectedRevision !== payload.base.result.revision || payload.expectedCardRevision !== card.revision ||
            payload.input.coverAttachmentId !== (cover.attachment === null ? null : cover.attachment?.attachmentId) ||
            completedLane(card.lane, payload.base.result.workflow)) return;
        validate("archive:Identity", cover.identity as unknown as Value);
        if (cover.attachment !== null) validate("archive:Attachment", cover.attachment as unknown as Value);
        if (!coverMatches(payload.base, card, cover)) return;
      }
      const inventory = await this.persistence.read(() => this.client.recovery.inventory());
      const rows = inventory.submissions.filter(row => row.draftId === reference.draftId && Object.entries(reference.context).every(([key, value]) => value === row.context[key as keyof Context]));
      const originals = await Promise.all(rows.map(async row => ({ row, submission: await this.client.recovery.loadSubmission(row.context, row.opId) })));
      if (this.destroyed || this.editor || generation !== this.generation || persistenceGeneration !== this.persistence.generation) return;
      for (const { row, submission } of originals) {
        if (row.outcome === "committed") this.restoredCommits.add(row.opId);
        const outcome: OriginalOutcome = { opId: row.opId, state: row.outcome, ...(submission ? { evidence: submission.evidence } : {}),
          message: row.outcome === "unknown" ? "Original operation retained after reload. Outcome unknown; deliberate original lookup only. Do not resubmit." : "Retained original outcome: " + row.outcome + ". No operation was replayed." };
        this.records.set(row.opId, outcome); this.showOutcome(outcome, row.context);
      }
      // Recovery reads may finish after deliberate query/editor navigation. Keep
      // the user's currently focused control when mounting the recovered editor.
      const retainedFocus = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : undefined;
      const retainedSelection = retainedFocus instanceof HTMLInputElement || retainedFocus instanceof HTMLTextAreaElement
        ? [retainedFocus.selectionStart, retainedFocus.selectionEnd, retainedFocus.selectionDirection ?? undefined] as const : undefined;
      this.edit(payload.command, card, payload.start === true, { base: payload.base, reference, revision: draft.revision, input: payload.input }, archive as boolean | undefined, hierarchy, payload.command === "card.edit" && Object.hasOwn(payload.input, "relationships"), undefined, payload.genericMove === true, payload.detach, payload.cover);
      const restored = this.editor as Editor | undefined;
      if (restored) restored.submitted = rows.some(row => row.outcome !== "not_committed");
      if (rows.some(row => row.outcome === "committed")) this.readState.textContent = "Retained original was committed. This editor remains submitted; read current text and deliberately open new work.";
      this.updateSave();
      if (retainedFocus?.isConnected) {
        retainedFocus.focus({ preventScroll: true });
        if (retainedSelection && (retainedFocus instanceof HTMLInputElement || retainedFocus instanceof HTMLTextAreaElement)) {
          try { retainedFocus.setSelectionRange(...retainedSelection); } catch { /* Native non-text input has no selection. */ }
        }
      }
    } catch { this.readState.textContent = "Own window draft unavailable. Private records retained; no input was submitted."; }
  }
  // Owning session adapter invokes on logout/revocation; it preserves private data.
  destroy(): void { this.destroyed = true; this.stopRevision(); this.suspend(); this.stopPersistence(); this.editor?.workflowEditor?.destroy(); this.editor = undefined; this.board = undefined; this.session = undefined; this.root.replaceChildren(); }
  connectionUnavailable(): void {
    if (this.destroyed) return;
    this.generation++;
    this.refreshing = false;
    this.stale = true;
    this.clearCardCovers();
    this.details.invalidate();
    this.search.invalidate();
    this.activity.reset();
    this.notices.reset();
    this.recipient.reset();
    this.create.disabled = this.workflowEdit.disabled = true;
    this.readState.textContent = "Connection unavailable. Read the current board to resume. Private input and original outcomes are retained.";
    this.updateSave();
  }
  async refresh(): Promise<void> {
    if (this.destroyed || this.suspended) return;
    const generation = ++this.generation;
    this.refreshing = true;
    this.activity.reset();
    this.notices.reset();
    this.recipient.reset();
    this.clearCardCovers();
    this.details.invalidate();
    this.updateSave();
    try {
      const session = await this.client.session(), board = await this.client.board(this.boardId);
      this.client.context(board, session);
      if (this.destroyed || generation !== this.generation) return;
      this.refreshing = false;
      this.session = session; this.board = board; this.stale = false;
      this.workflowEdit.disabled = !workflowRight(session, "edit", board.result.workflow);
      this.create.disabled = !!board.result.workflow && !workflowRight(session, "create", board.result.workflow);
      this.heading.textContent = "Board " + board.result.identity.boardId;
      this.actor.textContent = "Actor " + session.actorId + " (" + session.kind + ")";
      this.readState.textContent = "Current at board revision " + board.result.revision + "; incarnation " + board.result.identity.incarnation;
      this.renderCards();
      this.details.completeBoardRead(board);
      this.details.boardRead(board.result.identity);
      this.search.boardRead(board.result.identity, board.result.revision, board.result.workflow);
      this.activity.boardRead(board.result.identity);
      this.bindNotices();
    } catch {
      if (this.destroyed || generation !== this.generation) return;
      this.refreshing = false;
      this.stale = true;
      this.search.invalidate();
      this.activity.reset();
      this.readState.textContent = this.board ? "Unavailable; displayed board is stale. Editor input and original base retained." : "Unavailable; no current board loaded.";
    }
    this.updateSave();
  }
  private bindNotices(): void {
    if (this.board && this.session?.kind === "human" && workflowRight(this.session, "read", this.board.result.workflow)) {
      this.notices.boardRead(this.board.result.identity);
      this.recipient.boardRead(this.client.context(this.board, this.session));
    }
  }
  private observeRevision(identity: Identity, revision: number): void {
    if (!this.observed || !sameIdentity(this.observed.identity, identity) || revision > this.observed.revision) this.observed = { identity, revision };
    if (!this.board || !sameIdentity(this.board.result.identity, identity) || this.board.result.revision >= revision) return;
    this.clearCardCovers();
    this.updateSave();
  }
  private currentCoverBoard(base: BoardRead): boolean {
    return !this.refreshing && !this.stale && (!this.observed || !sameIdentity(this.observed.identity, base.result.identity) || base.result.revision >= this.observed.revision);
  }
  private clearCardCovers(): void {
    for (const preview of this.cardCovers) preview.dispose();
    this.cardCovers.length = 0;
    for (const preview of this.cards.querySelectorAll('[aria-label="Card cover preview"]')) preview.remove();
  }
  private renderCards(): void {
    this.clearCardCovers();
    const focused = this.cards.contains(document.activeElement) ? (document.activeElement as HTMLElement | null)?.dataset.focusKey : undefined;
    this.cards.replaceChildren();
    const visible = visibleCards(this.board!.result.cards, this.archiveStatus.value as ArchiveStatus);
    this.cards.append(element("p", visible.length + " cards visible · Archive status: " + this.archiveStatus.selectedOptions[0]!.textContent));
    const base = this.board!, workflow = base.result.workflow, generation = this.generation;
    const filesByCard = new Map<string, typeof base.result.attachments[number][]>();
    for (const file of base.result.attachments) {
      const group = filesByCard.get(file.cardId);
      if (group) group.push(file); else filesByCard.set(file.cardId, [file]);
    }
    for (const lane of workflow ? workflowLanes(workflow) : [undefined]) {
      if (lane) this.cards.append(element("h2", laneText(lane.id, workflow)));
      for (const card of lane ? visible.filter(row => row.lane === lane.id) : visible) {
      const article = element("article");
      article.append(element("h2", "#" + card.ticket + " " + card.title), element("p", card.cardId),
        element("p", card.description), element("p", "Assignee: " + this.assignee(card.ownerId, this.board!.result.actors)), element("p", card.priority + " · " + laneText(card.lane, this.board!.result.workflow) + " · card revision " + card.revision + " · " + (card.archived ? "Archived" : "Active")));
      if (this.currentCoverBoard(base)) {
        const preview = element("section"); article.append(preview);
        this.cardCovers.push(presentCover(preview, this.client, base.result.identity, card, filesByCard.get(card.cardId) ?? [],
          () => !this.destroyed && !this.suspended && this.board === base && this.currentCoverBoard(base) && this.generation === generation && article.isConnected, article));
      }
      const commentCount = this.board!.result.comments.filter(c => c.cardId === card.cardId).length;
      article.append(element("p", commentCount + " comments returned. Open card to read paged comments and history."));
      for (const link of this.board!.result.links.filter(l => l.cardId === card.cardId && l.active)) {
        article.append(element("p", link.kind + " → " + link.target.cardId + " · " + link.linkId));
        const identity = this.board!.result.identity, target = this.board!.result.cards.find(row => row.cardId === link.target.cardId);
        if (target && link.target.boardId === identity.boardId && link.target.incarnation === identity.incarnation) {
          const openTarget = button("Open linked card #" + target.ticket + " " + target.title, () => {
            if (!this.destroyed && !this.suspended && this.board) this.details.open(target.cardId, this.board.result.identity, openTarget);
          });
          openTarget.dataset.openCard = target.cardId; openTarget.dataset.focusKey = card.cardId + ":link:" + link.linkId;
          article.append(openTarget);
        }
      }
      const archive = button(card.archived ? "Unarchive card" : "Archive card", () => {
        archive.focus({ preventScroll: true }); this.edit("card.edit", card, false, undefined, !card.archived);
      });
      archive.dataset.focusKey = card.cardId + ":archive"; archive.disabled = completedLane(card.lane, this.board!.result.workflow) || !!this.board!.result.workflow && !workflowRight(this.session!, "edit", this.board!.result.workflow); article.append(archive);
      const advisory = dependencyAdvisory(this.board!, card);
      const pressure = element("section"), prerequisites = element("section");
      pressure.setAttribute("aria-label", "Dependency pressure");
      prerequisites.setAttribute("aria-label", "Prerequisites/readiness");
      for (const fact of advisory.pressure) pressure.append(element("p", fact));
      for (const fact of advisory.prerequisites) prerequisites.append(element("p", fact));
      prerequisites.append(element("p", advisory.summary));
      article.append(pressure, prerequisites);
      for (const [label, command] of [["Edit card", "card.edit"], ["Rename card", "card.edit"], ["Add comment", "comment.add"], ["Add link", "link.add"], ["Remove link", "link.remove"], ["Start work", "card.edit"], ["Review handoff", "review-handoff"], ["Approve", "approve"]] as const) {
        const control = button(label, () => this.edit(command, card, label === "Start work"));
        control.dataset.focusKey = card.cardId + ":" + label;
        const workflow = this.board!.result.workflow, session = this.session!;
        control.disabled = completedLane(card.lane, workflow) || command === "approve" && (session.kind !== "human" || !session.grant.rights.includes("approve"));
        if (workflow) {
          const role = label === "Start work" ? "in_progress" : command === "review-handoff" ? "review" : command === "approve" ? "completed" : undefined;
          const right = command === "review-handoff" || command === "approve" ? command : command === "comment.add" ? "comment" : "edit";
          control.disabled ||= !workflowRight(session, right, workflow) || !!role && (card.archived || !workflowEdge(card.lane, roleLane(role, workflow)!, session, right === "comment" ? "edit" : right, workflow));
        }
        article.append(control);
      }
      if (this.board!.result.workflow) {
        const move = button("Move card", () => this.edit("card.edit", card, true, undefined, undefined, undefined, false, undefined, true));
        move.dataset.focusKey = card.cardId + ":move";
        move.disabled = card.archived || completedLane(card.lane, this.board!.result.workflow) || !moveLanes(card.lane, this.session!, this.board!.result.workflow).length;
        article.append(move);
      }
      const open = button("Open card #" + card.ticket + " " + card.title, () => {
        if (!this.destroyed && !this.suspended && this.board) this.details.open(card.cardId, this.board.result.identity, open);
      });
      open.dataset.openCard = card.cardId; open.dataset.focusKey = card.cardId + ":open"; article.append(open);
      this.cards.append(article);
    }
    }
    // Refresh only restores focus when the old card control was removed.
    if (focused) {
      const replacement = [...this.cards.querySelectorAll<HTMLButtonElement>("button")].find(n => n.dataset.focusKey === focused);
      (replacement ?? this.heading).focus({ preventScroll: true });
    }
  }
  private assignee(id: string | null | undefined, actors: BoardRead["result"]["actors"]): string {
    if (!id) return "Unassigned";
    const actor = actors.find(a => a.actorId === id);
    return actor ? actor.label + " (" + id + ")" : id;
  }
  private blocked(): boolean { return [...this.records.values()].some(r => r.state === "pending" || r.state === "unknown"); }
  private updateSave(): void {
    if (this.editor?.workflowEditor) {
      const editor = this.editor;
      editor.workflowEditor!.setState({ busy: editor.workflowPreviewBusy, locked: editor.submitted || editor.confirming,
        canCreate: !this.stale && !this.blocked() && editor.persisted === editor.sequence && editor.workflowPreview === JSON.stringify(this.input(editor)),
        message: editor.recovery.textContent ?? "" });
    }
    if (this.editor) this.editor.save.disabled = !!this.editor.detach && !this.canDetachFile(this.editor.detach, this.editor.detach.attachment.attachmentId) || !!this.editor.cover && !this.canEditCover(this.editor.cover) || this.stale || this.editor.submitted || this.editor.composing || this.editor.confirming || this.blocked() || this.editor.hierarchy?.action === "set-parent" && !this.editor.fields.parent?.value;
  }
  private hierarchyEdit(action: HierarchyAction, card: HierarchyCard, read: BoardRead): void {
    const current = this.board;
    if (!current || !sameIdentity(current.result.identity, read.result.identity) || current.result.revision !== read.result.revision) {
      this.readState.textContent = "Read current board before changing hierarchy; the captured hierarchy base differs."; return;
    }
    const target = current.result.cards.find(row => row.cardId === card.cardId);
    if (!target || target.revision !== card.revision || action !== "create-child" && completedLane(target.lane, current.result.workflow)) return;
    this.edit(action === "create-child" ? "card.create" : "card.edit", action === "create-child" ? undefined : target, false, undefined, undefined,
      { action, ...(action === "create-child" ? { parent: { ...current.result.identity, cardId: target.cardId } } : {}) });
  }
  private relationshipEdit(action: RelationshipAction, card: Card, read: BoardRead, row?: RelationshipRow): void {
    const current = this.board;
    if (!current || this.stale || !sameIdentity(current.result.identity, read.result.identity) || current.result.revision !== read.result.revision) { this.readState.textContent = "Read current board before changing relationships; the captured relationship base differs."; return; }
    const target = current.result.cards.find(value => value.cardId === card.cardId);
    if (!target || target.revision !== card.revision || completedLane(target.lane, current.result.workflow) || action === "remove" && (!row?.owned || row.owner.cardId !== target.cardId)) return;
    this.edit(action === "replace" ? "card.edit" : action === "add" ? "link.add" : "link.remove", target, false, undefined, undefined, undefined, action === "replace", row?.linkId);
  }
  private canDetachFile(selected: Pick<DetachFile, "identity" | "cardId" | "revision" | "cardRevision">, attachmentId: string): boolean {
    const base = this.board, session = this.session, card = base?.result.cards.find(row => row.cardId === selected.cardId);
    return !this.destroyed && !this.suspended && !this.stale && !!base && !!session && !!card &&
      sameIdentity(base.result.identity, selected.identity) && base.result.revision === selected.revision && card.revision === selected.cardRevision &&
      card.attachmentIds.includes(attachmentId) && !completedLane(card.lane, base.result.workflow) && workflowRight(session, "attach", base.result.workflow);
  }
  private detachFile(read: CardFileRead): void {
    const selected = read.result;
    if (selected.file.referenceState !== "current" || !this.canDetachFile(selected, selected.file.attachment.attachmentId)) return;
    const card = this.board!.result.cards.find(row => row.cardId === selected.cardId)!;
    this.edit("attachment.detach", card, false, undefined, undefined, undefined, false, undefined, false, structuredClone({ identity: selected.identity, cardId: selected.cardId, revision: selected.revision, cardRevision: selected.cardRevision, attachment: selected.file.attachment }));
  }
  private canEditCover(cover: CoverEdit): boolean {
    const base = this.board, session = this.session, card = base?.result.cards.find(row => row.cardId === cover.cardId);
    return !this.destroyed && !this.suspended && !!base && this.currentCoverBoard(base) && !!session && !!card &&
      coverMatches(base, card, cover) && !completedLane(card.lane, base.result.workflow) && workflowRight(session, "attach", base.result.workflow);
  }
  private editCover(cover: CoverEdit): void {
    if (!this.canEditCover(cover)) return;
    const card = this.board!.result.cards.find(row => row.cardId === cover.cardId)!;
    this.edit("card.edit", card, false, undefined, undefined, undefined, false, undefined, false, undefined, structuredClone(cover));
  }
  private edit(command: BoardCommand, card?: Card, start = false, restore?: { base: BoardRead; reference: EditorReference; revision: number; input: Record<string, Value> }, archive?: boolean, hierarchy?: HierarchyEdit, relationshipsReplace = false, removeLinkId?: string, genericMove = false, detach?: DetachFile, cover?: CoverEdit): void {
    if (this.destroyed || !this.board || !this.session || this.stale) { this.readState.textContent = "Read a current board before editing."; return; }
    if (!this.canLeave) { this.readState.textContent = "Private recovery is pending or failed. Retain this input until saved or explicitly discard the editor."; return; }
    if (command === "attachment.detach" && !restore && (!detach || !this.canDetachFile(detach, detach.attachment.attachmentId))) return;
    if (cover && !restore && !this.canEditCover(cover)) return;
    if (this.editor?.cover && this.editor.submitted && (this.editor.coverDispatching || this.blocked())) {
      this.readState.textContent = "The original cover operation is pending or unknown. Retain this editor until its original outcome is known.";
      return;
    }
    if (this.editor && !this.editor.submitted && this.dirty(this.editor)) {
      if (archive !== undefined || hierarchy !== undefined || relationshipsReplace || removeLinkId !== undefined || detach !== undefined || cover !== undefined) {
        const opener = document.activeElement as HTMLElement | null;
        this.cancelEditor(this.editor, () => {
          const replacement = [...this.root.querySelectorAll<HTMLButtonElement>("button")].find(row => row.dataset.focusKey === opener?.dataset.focusKey);
          if (cover && (!opener?.isConnected || this.details.host.hidden || !this.details.host.contains(opener))) return;
          (replacement ?? (opener?.isConnected ? opener : undefined))?.focus({ preventScroll: true }); this.edit(command, card, start, restore, archive, hierarchy, relationshipsReplace, removeLinkId, genericMove, detach, cover);
        });
        return;
      }
      this.readState.textContent = "Current editor has unsaved intent. Finish it or explicitly discard it before opening another editor.";
      return;
    }
    if (!restore && (this.board.result.workflow || command === "attachment.detach" || cover)) {
      const right = command === "attachment.detach" || cover ? "attach" : command === "card.create" ? "create" : command === "comment.add" ? "comment" : command === "review-handoff" || command === "approve" ? command : "edit";
      if (!workflowRight(this.session, right, this.board.result.workflow)) { this.readState.textContent = "Current workflow and grant do not offer this action. The server remains authoritative."; return; }
    }
    if (command === "attachment.detach" && (!card || completedLane(card.lane, (restore?.base ?? this.board).result.workflow))) return;
    const base = restore?.base ?? this.board, context = this.client.context(base, this.session), form = element("form");
    form.addEventListener("submit", event => event.preventDefault());
    const recovery = element("p", "Recovery pending"); recovery.setAttribute("role", "status");
    const editor: Editor = { id: restore?.reference.draftId ?? crypto.randomUUID(), revision: restore?.revision ?? 0, command, context, base, card,
      ...(detach ? { detach } : {}), ...(cover ? { cover } : {}), fields: {}, form, recovery, save: button("Save", () => { void this.save(editor); }), composing: false,
      sequence: 0, submitted: false, persisted: -1, start, ...(genericMove ? { genericMove: true } : {}), ...(archive === undefined ? {} : { archive }), ...(hierarchy ? { hierarchy } : {}), ...(relationshipsReplace ? { relationshipsReplace: true } : {}), initial: "", initialFields: {}, opener: document.activeElement as HTMLElement | null,
      confirmation: element("section"), confirming: false, confirmingEnter: false };
    const title = element("h2", command + " · original board revision " + base.result.revision + (card ? ", card revision " + card.revision : ""));
    form.append(title, element("p", "Board " + context.boardId + " · actor " + context.actorId));
    const field = (name: string, value: string, choices?: string[]): void => {
      const label = element("label", name + " ");
      const control = choices ? element("select") : name === "description" || name === "text" || name === "relationships" ? element("textarea") : element("input");
      if (control instanceof HTMLSelectElement) for (const choice of choices!) { const option = element("option", choice); option.value = choice; control.append(option); }
      control.name = name; control.setAttribute("aria-label", name === "ownerId" ? "Assignee" : name === "parent" ? "Parent card" : name); control.value = value; editor.fields[name] = control;
      control.addEventListener("input", () => { this.persist(editor); this.updateSave(); });
      control.addEventListener("change", () => { this.persist(editor); this.updateSave(); });
      label.append(control); form.append(label);
    };
    const relationshipSet = (initial: string): void => {
      field("relationships", initial);
      const storage = editor.fields.relationships!; storage.hidden = true; storage.parentElement!.hidden = true;
      form.append(element("p", "Complete owned relationship set, including every relationship beyond the visible page. Incoming relationships are retained. Maximum 100 submitted entries; larger legacy sets remain visible and must be reduced deliberately."));
      const selected = element("section"); selected.setAttribute("aria-label", "Selected owned relationships");
      const update = (value: Value[]): void => { storage.value = JSON.stringify(value); editor.renderRelationships!(); this.persist(editor); this.updateSave(); };
      editor.renderRelationships = (): void => {
        selected.replaceChildren();
        let value: Value;
        try { value = storage.value.trim() ? parse(storage.value) : []; } catch { selected.append(element("p", "Retained owned relationship input is unavailable. Clear the set deliberately to replace it.")); return; }
        if (!Array.isArray(value)) { selected.append(element("p", "Retained owned relationship input is invalid. Clear the set deliberately to replace it.")); return; }
        selected.append(element("p", value.length + " selected owned relationships" + (value.length > 100 ? ". More than 100 entries: saving is refused until you deliberately remove entries or clear this set." : ".")));
        for (const [index, spec] of value.entries()) {
          const entry = element("article"), row = spec as unknown as { kind?: string; target?: ParentRef };
          const target = row?.target, peer = target && sameIdentity(base.result.identity, target) ? base.result.cards.find(card => card.cardId === target.cardId) : undefined;
          const label = (row && row.kind && Object.hasOwn(forwardLabels, row.kind) ? forwardLabels[row.kind as keyof typeof forwardLabels] : "Unavailable relationship") + " · " + (peer ? "#" + peer.ticket + " " + peer.title + " · " + laneText(peer.lane, base.result.workflow) + " · " + (peer.archived ? "Archived" : "Active") + " · " : "Unavailable · ") + (target ? target.boardId + "/" + target.incarnation + "/" + target.cardId : "Unknown exact target");
          entry.append(element("p", label), button("Remove owned set entry " + (index + 1), () => { if (this.editor !== editor || editor.submitted || editor.confirming) return; const next = [...value as Value[]]; next.splice(index, 1); update(next); (selected.querySelector<HTMLButtonElement>("button") ?? clear).focus(); }));
          selected.append(entry);
        }
      };
      const clear = button("Clear owned relationships", () => { if (this.editor !== editor || editor.submitted || editor.confirming) return; update([]); clear.focus(); });
      form.append(selected, clear); editor.renderRelationships();
      const kind = element("select"); kind.setAttribute("aria-label", "Relationship kind");
      for (const value of relationshipKinds) { const option = element("option", forwardLabels[value]); option.value = value; kind.append(option); }
      const peer = element("select"); peer.setAttribute("aria-label", "Relationship target"); peer.append(element("option", "Choose an exact relationship peer")); peer.options[0]!.value = "";
      for (const card of [...base.result.cards].filter(value => value.cardId !== editor.card?.cardId).sort(hierarchyOrder)) { const option = element("option", "#" + card.ticket + " " + card.title + " · " + card.cardId + " · " + laneText(card.lane, base.result.workflow) + " · " + (card.archived ? "Archived" : "Active")); option.value = card.cardId; peer.append(option); }
      form.append(kind, peer, button("Add selected relationship to owned set", () => {
        if (this.editor !== editor || editor.submitted || editor.confirming || !peer.value) return;
        try {
          const value = editor.fields.relationships!.value.trim() ? parse(editor.fields.relationships!.value) : [];
          if (!Array.isArray(value)) throw Error("Array required");
          value.push({ kind: kind.value, target: { ...base.result.identity, cardId: peer.value } });
          update(value);
        } catch { editor.recovery.textContent = "Retained owned set is invalid. Input retained; clear the set deliberately before adding a selected peer."; }
      }));
    };
    if (command === "workflow.edit") {
      const current = base.result.workflow;
      const retainedWorkflow = restore?.input.workflow as unknown as import("../../generated/archive/contracts.js").Workflow | undefined;
      const lanes = structuredClone(workflowLanes(current)) as import("../../generated/archive/contracts.js").WorkflowLane[];
      const ordinary = ["attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"];
      const workflow = current ? { ...structuredClone(current), revision: base.result.policyVersion + 1 } : {
        workflowId: retainedWorkflow?.workflowId ?? crypto.randomUUID(), profile: "default" as const, revision: base.result.policyVersion + 1, lanes,
        transitions: [{ from: "In Progress", to: "Ready for Review", actors: ["automation", "human"] }, { from: "Ready for Review", to: "Completed", actors: ["human"] }, { from: "Ready for Work", to: "In Progress", actors: ["automation", "human"] }],
        actorRights: { human: ["approve", ...ordinary], automation: ordinary } };
      editor.initial = JSON.stringify({ expectedPolicyVersion: base.result.policyVersion, workflow });
      field("workflow", JSON.stringify(retainedWorkflow ?? workflow));
      editor.fields.workflow!.hidden = true; editor.fields.workflow!.parentElement!.hidden = true;
      const host = element("section"); form.append(host);
      const draft: WorkflowDraft = { projectKey: "", boardKey: "", label: "", workflow: JSON.parse(editor.fields.workflow!.value) };
      editor.workflowEditor = new WorkflowEditor(host, { existing: true, draft, projects: [],
        change: next => { if (this.editor !== editor || editor.submitted || this.suspended) return; editor.workflowPreview = undefined; editor.workflowPreviewFailure = undefined; editor.workflowPreviewSequence = (editor.workflowPreviewSequence ?? 0) + 1; editor.fields.workflow!.value = JSON.stringify(next.workflow); this.persist(editor); this.updateSave(); },
        preview: () => { void this.previewWorkflow(editor); }, submit: () => { void this.save(editor); },
        cancel: () => this.cancelEditor(editor), status: () => {} });
      editor.save.hidden = true;
    } else if (relationshipsReplace) {
      title.textContent = "Replace owned relationships · original board revision " + base.result.revision + ", card revision " + card!.revision;
      editor.save.textContent = "Save owned relationships";
      relationshipSet(JSON.stringify(completeOwnedRelationships(base, card!.cardId), null, 2));
    } else if (hierarchy?.action === "set-parent") {
      const candidates = [...base.result.cards].filter(row => row.cardId !== card?.cardId).sort(hierarchyOrder);
      field("parent", "", ["", ...candidates.map(row => row.cardId)]);
      const selector = editor.fields.parent as HTMLSelectElement;
      selector.options[0]!.textContent = "Choose an exact parent card";
      for (const option of [...selector.options].slice(1)) {
        const target = candidates.find(row => row.cardId === option.value)!;
        option.textContent = "#" + target.ticket + " " + target.title + " · " + target.cardId + " · " + laneText(target.lane, base.result.workflow) + " · " + (target.archived ? "Archived" : "Active");
      }
      form.append(element("p", "Set parent only. Save submits the explicitly selected exact identity."));
    } else if (hierarchy?.action === "clear-parent") {
      form.append(element("p", "Clear parent for #" + card!.ticket + "? Save submits one explicit parent edit."));
    } else if (archive !== undefined) {
      editor.save.textContent = archive ? "Confirm archive card" : "Confirm unarchive card";
      form.append(element("p", (archive ? "Archive" : "Unarchive") + " card #" + card!.ticket + "? Recorded lane " + laneText(card!.lane, base.result.workflow) + " and retained card facts remain. This submits one explicit edit."));
    } else if (cover) {
      editor.initial = JSON.stringify({ coverAttachmentId: card!.coverAttachmentId ?? null });
      editor.save.textContent = cover.attachment ? card!.coverAttachmentId ? "Confirm change card cover" : "Confirm set card cover" : "Confirm clear card cover";
      const confirmation = element("section"); confirmation.setAttribute("aria-label", "Card cover confirmation");
      confirmation.style.overflowWrap = "anywhere";
      confirmation.append(element("p", cover.attachment ? "Select this exact file as the explicit card cover." : "Clear the explicit card cover and restore automatic fallback."),
        element("p", cover.attachment ? "File " + cover.attachment.name + " · " + cover.attachment.attachmentId : "Explicit cover " + card!.coverAttachmentId),
        element("p", "Card #" + card!.ticket + " " + card!.title + " · " + cover.cardId),
        element("p", "Board " + cover.identity.boardId + " · incarnation " + cover.identity.incarnation),
        element("p", "Captured board revision " + cover.revision + " · card revision " + cover.cardRevision));
      form.append(confirmation);
    } else if (start) {
      if (base.result.workflow) {
        const choices = [...moveLanes(card!.lane, this.session, base.result.workflow).filter(lane => genericMove || lane.role === "in_progress")];
        if (restore && typeof restore.input.laneId === "string" && !choices.some(lane => lane.id === restore.input.laneId)) {
          const retained = workflowLanes(base.result.workflow).find(lane => lane.id === restore.input.laneId);
          if (retained) choices.push(retained);
        }
        field("laneId", roleLane("in_progress", base.result.workflow) ?? "", choices.map(lane => lane.id));
        const select = editor.fields.laneId as HTMLSelectElement;
        for (const option of select.options) option.textContent = laneText(option.value, base.result.workflow);
        if (!select.value && choices.length) select.value = choices[0]!.id;
        editor.save.textContent = "Move card";
        form.append(element("p", "Move only. The server rechecks the edge, rights and prerequisites when entering in_progress."));
      } else field("lane", "In Progress", ["In Progress"]);
    }
    else if (command === "card.create" || command === "card.edit") {
      field("title", card?.title ?? ""); field("description", card?.description ?? ""); field("priority", card?.priority ?? "P3", ["P0", "P1", "P2", "P3", "P4", "P5"]);
      field("ownerId", card?.ownerId ?? "", ["", ...base.result.actors.map(a => a.actorId)]);
      const assignee = editor.fields["ownerId"] as HTMLSelectElement;
      assignee.parentElement!.firstChild!.textContent = "Assignee ";
      for (const option of assignee.options) option.textContent = this.assignee(option.value, base.result.actors);
      if (command === "card.create") relationshipSet("");
      if (hierarchy?.action === "create-child") form.append(element("p", "Create child of exact parent " + hierarchy.parent!.boardId + "/" + hierarchy.parent!.incarnation + "/" + hierarchy.parent!.cardId));
    } else if (command === "comment.add") field("text", "");
    else if (command === "link.add") {
      field("kind", "blocking", [...relationshipKinds]);
      const kinds = editor.fields.kind as HTMLSelectElement;
      for (const option of kinds.options) option.textContent = forwardLabels[option.value as keyof typeof forwardLabels];
      field("targetCardId", "", ["", ...base.result.cards.filter(value => value.cardId !== card?.cardId).sort(hierarchyOrder).map(value => value.cardId)]);
      const targets = editor.fields.targetCardId as HTMLSelectElement; targets.setAttribute("aria-label", "Relationship target"); targets.options[0]!.textContent = "Choose an exact relationship peer";
      for (const option of [...targets.options].slice(1)) { const peer = base.result.cards.find(value => value.cardId === option.value)!; option.textContent = "#" + peer.ticket + " " + peer.title + " · " + peer.cardId + " · " + laneText(peer.lane, base.result.workflow) + " · " + (peer.archived ? "Archived" : "Active"); }
    }
    else if (command === "link.remove") {
      const links = base.result.links.filter(link => link.active && link.cardId === card?.cardId);
      field("linkId", removeLinkId ?? "", ["", ...links.map(link => link.linkId)]);
      for (const option of [...(editor.fields.linkId as HTMLSelectElement).options].slice(1)) { const link = links.find(link => link.linkId === option.value)!; option.textContent = link.kind + " · " + link.target.boardId + "/" + link.target.incarnation + "/" + link.target.cardId + " · " + link.linkId; }
    }
    else if (command === "attachment.detach" && detach) {
      editor.save.textContent = "Confirm detach file";
      const confirmation = element("section"); confirmation.setAttribute("aria-label", "Detach file confirmation");
      confirmation.style.overflowWrap = "anywhere"; confirmation.style.whiteSpace = "pre-wrap"; confirmation.style.minWidth = "0";
      confirmation.append(element("p", "Detach this exact file from the current card? Ordinary download becomes unavailable after detach. Retained metadata, bytes and prior evidence remain available under their retention rules."),
        element("p", "File " + detach.attachment.name + " · " + detach.attachment.attachmentId),
        element("p", "Card #" + card!.ticket + " " + card!.title + " · " + card!.cardId),
        element("p", "Board " + base.result.identity.boardId + " · incarnation " + base.result.identity.incarnation),
        element("p", "Captured board revision " + detach.revision + " · card revision " + detach.cardRevision));
      form.append(confirmation);
    }
    else if (command === "approve") field("policyVersion", String(base.result.policyVersion), [String(base.result.policyVersion)]);
    else form.append(element("p", "Explicit Save submits this review handoff."));
    form.addEventListener("compositionstart", () => { editor.composing = true; this.updateSave(); });
    form.addEventListener("compositionend", () => {
      editor.composing = false; editor.confirmingEnter = true;
      // Some engines deliver confirmation Enter after compositionend. Fence
      // that event turn without swallowing a later deliberate Enter after a
      // mouse-selected candidate (which has no corresponding Enter keyup).
      setTimeout(() => { editor.confirmingEnter = false; }, 0);
      this.persist(editor); this.updateSave();
    });
    form.addEventListener("keyup", event => { if (event.key === "Enter") editor.confirmingEnter = false; });
    form.addEventListener("keydown", event => {
      if (command === "workflow.edit") return;
      if (event.isComposing || editor.composing || event.keyCode === 229) {
        if (event.key === "Enter") event.preventDefault();
        return;
      }
      if (event.key === "Enter" && editor.confirmingEnter) { event.preventDefault(); return; }
      if (event.key === "Escape") {
        event.preventDefault(); this.cancelEditor(editor); return;
      }
      if (event.key === "Enter" && (event.target instanceof HTMLInputElement || event.target === editor.save)) {
        event.preventDefault();
        if (!editor.confirmingEnter && !editor.confirming && !event.repeat) void this.save(editor);
      }
    });
    editor.initialFields = Object.fromEntries(Object.entries(editor.fields).map(([name, control]) => [name, control.value]));
    if (!editor.initial) editor.initial = JSON.stringify(this.input(editor));
    form.append(recovery, editor.save, button("Read current text", () => { void this.currentText(editor); }), button(command === "attachment.detach" ? "Cancel detach file" : cover ? "Cancel card cover edit" : archive === undefined ? "Discard current editor input" : archive ? "Cancel archive" : "Cancel unarchive", () => this.cancelEditor(editor)), editor.confirmation);
    this.editor?.workflowEditor?.destroy(); this.editor = editor; this.editorHost.replaceChildren(form); this.current.replaceChildren();
    if (restore) for (const [name, control] of Object.entries(editor.fields)) {
      if (name !== "workflow" && (Object.hasOwn(restore.input, name) || name === "targetCardId" && Object.hasOwn(restore.input, "target"))) control.value = name === "relationships" ? typeof restore.input[name] === "string" ? restore.input[name] as string : JSON.stringify(restore.input[name], null, 2) : name === "targetCardId" && validParentRef(restore.input.target) ? (restore.input.target as unknown as ParentRef).cardId : name === "parent" && validParentRef(restore.input[name]) ? (restore.input[name] as unknown as ParentRef).cardId : String(restore.input[name] ?? "");
    }
    editor.renderRelationships?.();
    if (restore) { editor.persisted = editor.sequence; editor.recovery.textContent = "Own window draft restored privately; no operation submitted."; }
    else this.persist(editor);
    this.updateSave();
    (editor.workflowEditor ? editor.form.querySelector<HTMLInputElement>('input[aria-label="Lane 1 label"]')! : Object.values(editor.fields).find(field => !field.hidden) ?? editor.form.querySelector<HTMLSelectElement>('[aria-label="Relationship kind"]') ?? editor.save).focus();
  }
  private dirty(editor: Editor): boolean { return JSON.stringify(this.input(editor)) !== editor.initial; }
  private cancelEditor(editor: Editor, next?: () => void): void {
    if (this.editor !== editor || editor.submitted) return;
    const previousFocus = editor.form.contains(document.activeElement) ? document.activeElement as HTMLElement : undefined;
    const selection = previousFocus instanceof HTMLInputElement || previousFocus instanceof HTMLTextAreaElement
      ? [previousFocus.selectionStart, previousFocus.selectionEnd, previousFocus.selectionDirection ?? undefined] as const : undefined;
    const close = (): void => {
      if (this.editor !== editor || editor.submitted) return;
      const opener = editor.opener;
      this.discardInput();
      if (next) { next(); return; }
      const replacement = opener?.dataset.focusKey ? [...this.root.querySelectorAll<HTMLButtonElement>("button")]
        .find(row => row.dataset.focusKey === opener.dataset.focusKey && !row.disabled && row.getClientRects().length > 0) : undefined;
      (replacement ?? (opener?.isConnected && !(opener instanceof HTMLButtonElement && opener.disabled) && opener.getClientRects().length > 0
        ? opener : this.root.querySelector<HTMLButtonElement>("button")))?.focus();
    };
    if (!this.dirty(editor)) { close(); return; }
    if (editor.confirming) return;
    editor.confirming = true;
    this.updateSave();
    editor.confirmation.setAttribute("aria-label", "Discard editor confirmation");
    const keep = button("Keep editing", () => {
      editor.confirming = false; editor.confirmation.replaceChildren();
      this.updateSave();
      const retainedFocus = previousFocus?.isConnected && !editor.confirmation.contains(previousFocus) ? previousFocus : Object.values(editor.fields)[0] ?? editor.save;
      retainedFocus.focus({ preventScroll: true });
      if (selection && (retainedFocus instanceof HTMLInputElement || retainedFocus instanceof HTMLTextAreaElement)) {
        try { retainedFocus.setSelectionRange(...selection); } catch { /* Some native input types have no text selection. */ }
      }
    });
    editor.confirmation.replaceChildren(element("p", "Discard this editor's changed input? Retained original operations are unaffected."),
      button("Confirm discard editor input", close), keep);
    keep.focus();
  }
  private input(editor: Editor): Record<string, Value> {
    if (editor.command === "workflow.edit") return { expectedPolicyVersion: editor.base.result.policyVersion, workflow: parse(editor.fields.workflow!.value) };
    if (editor.detach) return { attachmentId: editor.detach.attachment.attachmentId };
    if (editor.cover) return { coverAttachmentId: editor.cover.attachment?.attachmentId ?? null };
    if (editor.archive !== undefined) return { archived: editor.archive };
    if (editor.hierarchy?.action === "clear-parent") return { parent: null };
    if (editor.hierarchy?.action === "set-parent") return { parent: editor.fields.parent?.value ? { ...editor.base.result.identity, cardId: editor.fields.parent.value } : null };
    if (editor.relationshipsReplace) {
      const text = editor.fields.relationships!.value;
      try { return { relationships: parse(text) }; } catch { return { relationships: text }; }
    }
    const input: Record<string, Value> = {};
    for (const [name, field] of Object.entries(editor.fields)) if (name !== "relationships") input[name] = name === "policyVersion" ? Number(field.value) : name === "ownerId" && field.value === "" ? null : field.value;
    if (editor.fields.relationships?.value.trim()) { try { input.relationships = parse(editor.fields.relationships.value); } catch { input.relationships = editor.fields.relationships.value; } }
    if (editor.command === "link.add") { input.target = { ...editor.base.result.identity, cardId: editor.fields.targetCardId!.value }; delete input.targetCardId; }
    if (editor.hierarchy?.action === "create-child") input.parent = { ...editor.hierarchy.parent! };
    // Native text controls normalize existing line endings. An untouched field
    // must retain its exact confirmed bytes, including when another field changes.
    if (editor.command === "card.edit" && editor.card) for (const name of ["title", "description"] as const) {
      if (editor.fields[name] && editor.fields[name].value === editor.initialFields[name]) input[name] = editor.card[name];
    }
    return input;
  }
  private persist(editor: Editor): void {
    const sequence = ++editor.sequence, generation = this.persistence.generation;
    // Prose and original non-secret base only; no credential/session DTO enters storage.
    const input = JSON.stringify({ command: editor.command, target: editor.card ? { cardId: editor.card.cardId } : null,
      expectedRevision: editor.base.result.revision, expectedCardRevision: editor.card?.revision ?? null, input: this.input(editor), base: editor.base, start: editor.start, ...(editor.genericMove ? { genericMove: true } : {}),
      ...(editor.hierarchy ? { hierarchy: editor.hierarchy } : {}), ...(editor.detach ? { detach: editor.detach } : {}), ...(editor.cover ? { cover: editor.cover } : {}), ...(editor.card ? { card: editor.card } : {}) });
    editor.recovery.textContent = editor.workflowPreviewFailure ?? "Recovery pending";
    void this.persistence.persist(async () => {
      try {
        const result = await this.client.recovery.saveDraft({ context: editor.context, draftId: editor.id, expectedRevision: editor.revision, input });
        editor.id = result.draft.draftId; editor.revision = result.draft.revision;
        if (this.editor === editor) this.checkpoint({ context: editor.context, draftId: editor.id });
        if (generation === this.persistence.generation && sequence === editor.sequence) editor.persisted = sequence;
        if (!this.destroyed && generation === this.persistence.generation && sequence === editor.sequence) editor.recovery.textContent = editor.workflowPreviewFailure ?? (result.state === "forked" ? "Recovery saved as a separate draft after a conflict." : "Recovery saved privately.");
      } catch { if (!this.destroyed && generation === this.persistence.generation && sequence === editor.sequence) editor.recovery.textContent = (editor.workflowPreviewFailure ? editor.workflowPreviewFailure + " " : "") + "Recovery failed; input remains here. Dispatch requires verified recovery storage."; }
      this.updateSave();
    });
  }
  private async previewWorkflow(editor: Editor): Promise<void> {
    if (this.destroyed || this.suspended || this.editor !== editor || editor.submitted || editor.workflowPreviewBusy || this.stale) return;
    const generation = this.generation, sequence = editor.workflowPreviewSequence ?? 0, input = this.input(editor);
    editor.workflowPreviewBusy = true; editor.workflowPreview = undefined; editor.workflowPreviewFailure = undefined; editor.recovery.textContent = "Checking workflow preview."; this.updateSave();
    try {
      const preview = await this.client.previewWorkflow({ apiVersion: 1, boardId: editor.context.boardId, incarnation: editor.context.incarnation,
        expectedRevision: editor.base.result.revision, expectedPolicyVersion: editor.base.result.policyVersion,
        workflow: input.workflow as unknown as import("../../generated/archive/contracts.js").Workflow });
      if (this.destroyed || this.suspended || this.editor !== editor || generation !== this.generation || sequence !== (editor.workflowPreviewSequence ?? 0)) return;
      editor.workflowPreview = JSON.stringify(input);
      const target = editor.form.querySelector('[aria-label="Complete workflow preview"]')!;
      target.replaceChildren(element("h2", "Review before Save"),
        element("p", preview.wouldChange ? `Policy ${preview.policyVersion} → ${preview.nextPolicyVersion} at board revision ${preview.revision}.` : `No workflow changes at policy ${preview.policyVersion}. Save will return NO_CHANGE.`),
        element("p", "Read-only preview. Cards stay in their current lanes. Existing human approvals retain their issuing policy. Ordinary authority is rechecked when saving."));
      const section = (heading: string, lines: string[]): void => {
        const list = element("ul");
        for (const line of lines) list.append(element("li", line));
        target.append(element("h3", heading), list);
      };
      const before = workflowLanes(preview.currentWorkflow), after = preview.candidate.lanes;
      const describe = (id: string): string => {
        const lane = after.find(row => row.id === id) ?? before.find(row => row.id === id);
        return lane ? `${lane.label} (${lane.role})` : id;
      };
      const changes = preview.changes;
      section("Attention categories", [
        `Source ${changes.attention.beforeSource} → ${changes.attention.afterSource}. ${changes.attention.changed ? "Attention policy changes." : "Attention policy stays the same."}`,
        "Counts use current committed unarchived cards. Candidate counts are preview estimates.",
        ...(["blocked", "needsTerry", "readyForReview"] as const).map(key => {
          const name = { blocked: "Blocked", needsTerry: "Needs Terry", readyForReview: "Ready for Review" }[key];
          const mapping = (ids: readonly string[], lanes: readonly { id: string; label: string; role: string }[]): string => ids.map(id => {
            const lane = lanes.find(row => row.id === id)!;
            return `${lane.label} (${lane.role}) · ID ${id}`;
          }).join(", ") || "Not configured";
          const count = (value: CatalogCount): string => value.state === "known" ? String(value.value) : value.state === "not_configured" ? "Not configured" : value.state === "stale" ? `Stale ${value.value}` : "Unavailable";
          return `${name} · Current ${count(preview.currentAttention[key])}: ${mapping(changes.attention.before[key], before)} → Candidate ${count(preview.candidateAttention[key])}: ${mapping(changes.attention.after[key], after)}`;
        }),
      ]);
      section("Lane changes", [
        ...changes.renamed.map(row => `${row.before} → ${row.after} · ${describe(row.laneId)} · ID ${row.laneId}`),
        ...changes.added.map(row => `Add ${row.label} (${row.role}) · ID ${row.id}`),
        ...changes.retired.map(row => `Retire ${row.label} (${row.role}) · ID ${row.id}`),
        changes.orderChanged ? "Lane order changes. New order appears below." : "Lane order stays the same.",
      ]);
      if (changes.orderChanged) section("New lane order", after.map((lane, index) => `${index + 1}. ${lane.label} (${lane.role}) · ID ${lane.id}`));
      const priorEdges = preview.currentWorkflow?.transitions ?? [
        { from: "In Progress", to: "Ready for Review", actors: ["automation", "human"] },
        { from: "Ready for Review", to: "Completed", actors: ["human"] },
        { from: "Ready for Work", to: "In Progress", actors: ["automation", "human"] },
      ];
      const edgeKey = (edge: { from: string; to: string; actors: readonly string[] }): string => JSON.stringify([edge.from, edge.to, edge.actors]);
      const priorKeys = new Set(priorEdges.map(edgeKey)), nextKeys = new Set(preview.candidate.transitions.map(edgeKey));
      const edgeLine = (edge: { from: string; to: string; actors: readonly string[] }): string => `${describe(edge.from)} → ${describe(edge.to)} · ${edge.actors.join(", ")}`;
      section("Transition changes", changes.transitionsChanged ? [
        ...priorEdges.filter(edge => !nextKeys.has(edgeKey(edge))).map(edge => "Remove " + edgeLine(edge)),
        ...preview.candidate.transitions.filter(edge => !priorKeys.has(edgeKey(edge))).map(edge => "Add " + edgeLine(edge)),
      ] : ["No transition changes."]);
      section("Ordinary-right ceiling changes", (["human", "automation"] as const).map(actor => {
        const rights = changes.rights[actor];
        return `${actor} · add ${rights.added.join(", ") || "none"} · remove ${rights.removed.join(", ") || "none"}`;
      }));
      target.append(element("p", "Effective rights remain the intersection of these ceilings and independently valid grants. Removing rights can prevent existing card actions."));
      section("Current lane occupancy", preview.occupancy.map(row => `${describe(row.laneId)} · ${row.currentCards} current cards, including ${row.archivedCards} archived. ID ${row.laneId}`));
      target.append(element("h3", "Approvals and capacity"),
        element("p", `${preview.approval.preservedCurrentCount} current human approvals preserved under their retained issuing policy.`),
        element("p", `Policy records ${preview.capacity.policyRecords} → ${preview.capacity.policyRecordsAfter} of ${preview.capacity.maxPolicyRecords}. Remaining after save ${preview.capacity.maxPolicyRecords - preview.capacity.policyRecordsAfter}.`),
        element("p", `Canonical policy history after save ${preview.capacity.canonicalPolicyBytesAfter} of ${preview.capacity.maxCanonicalPolicyBytes} bytes. Remaining ${preview.capacity.maxCanonicalPolicyBytes - preview.capacity.canonicalPolicyBytesAfter} bytes.`));
      const complete = element("details");
      complete.append(element("summary", "Complete validated preview and definitions"), element("pre", JSON.stringify(preview, null, 2)));
      target.append(complete);
      editor.workflowEditor!.approvePreview();
      editor.recovery.textContent = "Preview ready. Review changes, occupancy, rights, approval preservation and capacity before Save workflow.";
    } catch (error) {
      if (!this.destroyed && !this.suspended && this.editor === editor && generation === this.generation && sequence === (editor.workflowPreviewSequence ?? 0)) {
        editor.workflowPreviewFailure = (error instanceof WorkflowPreviewRefusal ? "Workflow preview refused (" + error.code + ")." : "Workflow preview refused or unavailable.") + " Input and original revision retained. Read the current board and deliberately open a new editor to refresh context.";
        editor.recovery.textContent = editor.workflowPreviewFailure;
      }
    }
    finally { editor.workflowPreviewBusy = false; if (!this.destroyed && !this.suspended && this.editor === editor) this.updateSave(); }
  }
  private async save(editor: Editor): Promise<void> {
    if (this.destroyed || this.suspended || this.editor !== editor || editor.command === "workflow.edit" && editor.workflowPreview !== JSON.stringify(this.input(editor)) || editor.composing || editor.confirmingEnter || editor.confirming || editor.submitted || this.stale || this.blocked() || editor.hierarchy?.action === "set-parent" && !editor.fields.parent?.value) return;
    if (editor.detach && !this.canDetachFile(editor.detach, editor.detach.attachment.attachmentId)) return;
    if (editor.cover && !this.canEditCover(editor.cover)) return;
    const submittedInput = this.input(editor);
    if (editor.command === "workflow.edit" && editor.persisted !== editor.sequence) return;
    if (Object.hasOwn(submittedInput, "relationships") && !validRelationships(submittedInput.relationships)) { editor.recovery.textContent = "Relationships refused: select at most 100 unique relationships to exact target cards. Visible input retained; no operation submitted."; return; }
    if (editor.command === "link.add" && !editor.fields.targetCardId?.value || editor.command === "link.remove" && !editor.fields.linkId?.value) { editor.recovery.textContent = "Choose an exact relationship before saving. Input retained."; return; }
    const selection = editor.hierarchy ? this.details.captureSelection() : undefined;
    const sourceId = editor.hierarchy?.action === "create-child" ? editor.hierarchy.parent?.cardId : editor.card?.cardId;
    editor.hierarchyFollow = selection && selection.cardId === sourceId && selection.identity.boardId === editor.context.boardId && selection.identity.incarnation === editor.context.incarnation
      ? { selection, generation: this.generation } : undefined;
    editor.submitted = true;
    if (editor.cover) editor.coverDispatching = true;
    this.updateSave();
    // Capture synchronous immutable input before storage, session or network awaits.
    const request = JSON.parse(JSON.stringify({ opId: crypto.randomUUID(), incarnation: editor.context.incarnation,
      target: editor.card ? { cardId: editor.card.cardId } : null, expectedRevision: editor.base.result.revision,
      expectedCardRevision: editor.card?.revision ?? null, input: this.input(editor) })) as MutationRequest;
    const announce = (outcome: OriginalOutcome): void => {
      editor.coverDispatching = false;
      this.records.set(outcome.opId, outcome);
      if (outcome.state === "unsent" || outcome.state === "not_committed") { editor.submitted = false; if (editor.command === "workflow.edit") editor.workflowPreview = undefined; }
      if (this.destroyed || this.suspended) return;
      this.showOutcome(outcome, editor.context);
      this.reflectCommitted(outcome);
      if (outcome.conflict || outcome.state === "not_committed") void this.currentText(editor);
      this.updateSave();
    };
    await this.persistence.admit(async evidenceReady => {
      if (this.destroyed) return;
      if (editor.cover && (this.editor !== editor || !this.canEditCover(editor.cover))) {
        editor.submitted = false;
        editor.coverDispatching = false;
        editor.recovery.textContent = "Current cover context changed. Private input retained; no operation submitted.";
        this.updateSave(); return;
      }
      return this.client.submit({ command: editor.command, request, draftId: editor.id, context: editor.context }, outcome => {
        if (outcome.state === "pending" || outcome.state === "unsent") evidenceReady();
        announce(outcome);
      });
    });
  }
  // Compose other views' shared-client outcomes without replacing an editor,
  // refreshing its base or taking focus. This is presentation, not proof.
  observeOutcome(outcome: OriginalOutcome): void {
    if (this.destroyed) return;
    const original = outcome.evidence?.context;
    if (!original || !this.board || !this.session) return;
    const context = this.client.context(this.board, this.session);
    if (Object.entries(context).some(([key, value]) => value !== original[key as keyof Context])) return;
    this.reflectCommitted(outcome);
    this.updateSave();
  }
  private reflectCommitted(outcome: OriginalOutcome): void {
    if (outcome.state !== "committed") return;
    // A receipt proves the original mutation, never the current board text.
    // An authenticated same-incarnation read at/after its revision already
    // covers that commit. Otherwise require another explicit current read.
    const identity = this.board?.result.identity, original = outcome.evidence?.context, terminal = outcome.terminal;
    if (identity && original && terminal?.outcome === "committed" && identity.boardId === original.boardId &&
        identity.incarnation === original.incarnation && this.board!.result.revision >= terminal.revision) return;
    // A restored terminal classification has no retained receipt/revision. It
    // keeps the original editor submitted and the existing board-stale policy,
    // but is not a new mutation relative to this reload's independent searches.
    // Fresh receipts and live outcomes retain normal search invalidation.
    if (terminal || !this.restoredCommits.has(outcome.opId)) this.search.invalidate();
    if (terminal?.command === "workflow.edit" || outcome.evidence?.command === "workflow.edit") { this.notices.reset(); this.recipient.reset(); }
    this.stale = true;
    this.clearCardCovers();
    let hierarchyRefreshed = false;
    const editor = this.editor, follow = editor?.hierarchyFollow;
    if (identity && original && terminal?.outcome === "committed" && terminal.cardRevision !== null && editor?.hierarchy && follow && follow.generation === this.generation &&
        outcome.evidence?.draftId === editor.id && identity.boardId === original.boardId && identity.incarnation === original.incarnation) {
      try {
        const request = parse(outcome.evidence.requestBytes) as unknown as { input?: Record<string, unknown> };
        if (request.input && Object.hasOwn(request.input, "parent")) {
          const created = editor.hierarchy.action === "create-child";
          const cardId = created ? terminal.resultRefs.find(ref => ref.kind === "card" && ref.revision === terminal.cardRevision)?.id : terminal.target?.cardId;
          if (cardId && terminal.command === (created ? "card.create" : "card.edit")) hierarchyRefreshed = this.details.refreshAfterHierarchy(follow.selection, cardId, terminal.revision, terminal.cardRevision, created);
        }
      } catch { /* Invalid evidence never supplies navigation or optimistic facts. */ }
    }
    if (!hierarchyRefreshed) this.details.invalidate();
    this.readState.textContent = "Confirmed saved. Displayed board is stale; read the current board before new work.";
    if (identity && original && terminal?.outcome === "committed" && terminal.command === "card.edit" && terminal.target && terminal.cardRevision !== null &&
        identity.boardId === original.boardId && identity.incarnation === original.incarnation && outcome.evidence?.command === "card.edit") {
      try {
        const request = parse(outcome.evidence.requestBytes) as unknown as { input?: Record<string, unknown> };
        if (request.input && Object.keys(request.input).length === 1 && typeof request.input.archived === "boolean") {
          this.details.refreshAfterArchive(identity, terminal.target.cardId, terminal.revision, terminal.cardRevision);
        }
      } catch { /* A receipt never supplies optimistic card facts or permits replay. */ }
    }
  }
  private showOutcome(outcome: OriginalOutcome, context: Context): void {
    let row = this.outcomeNodes.get(outcome.opId);
    if (!row) { row = element("article"); this.outcomeNodes.set(outcome.opId, row); this.outcomes.append(row); }
    const status = element("p", outcome.opId + ": " + outcome.message); status.setAttribute("role", "status");
    row.replaceChildren(status);
    if (outcome.state === "pending") row.append(button("Stop waiting", () => {
      const unknown: OriginalOutcome = { ...outcome, state: "unknown", message: "Stopped waiting. Original outcome unknown; this does not cancel a possible commit." };
      this.records.set(outcome.opId, unknown); this.showOutcome(unknown, context); this.updateSave();
    }));
    if (outcome.state === "unknown") row.append(button("Look up original outcome", () => {
      void this.client.lookup(context, outcome.opId).then(result => {
        this.records.set(result.opId, result);
        if (result.state === "not_committed" && this.editor && result.evidence?.draftId === this.editor.id) this.editor.submitted = false;
        if (this.destroyed || this.suspended) return;
        this.reflectCommitted(result);
        this.showOutcome(result, context); this.updateSave();
      });
    }));
    // Announcements never focus, replace editor input, refresh bases or issue retries.
  }
  private async currentText(editor: Editor): Promise<void> {
    const generation = this.generation;
    const host = this.current;
    if (!editor.card) { host.replaceChildren(element("p", "Read the current board to compare cards.")); return; }
    try {
      const read = await this.client.card(editor.context.boardId, editor.card.cardId);
      if (this.destroyed || this.editor !== editor || generation !== this.generation) return;
      if (editor.cover && (!sameIdentity(read.result.identity, editor.context) || read.result.card.cardId !== editor.card.cardId)) throw Error("Current card identity changed");
      if (sameIdentity(read.result.identity, editor.context) && read.result.card.cardId === editor.card.cardId) {
        this.details.observeRevision(read.result.identity, read.result.revision);
      }
      host.replaceChildren(element("h2", "Current confirmed text · board revision " + read.result.revision),
        element("p", read.result.card.title), element("p", read.result.card.description),
        element("p", "Assignee: " + this.assignee(read.result.card.ownerId, read.result.actors)));
      if (editor.cover) host.append(element("p", "Current explicit cover · " + (read.result.card.coverAttachmentId ?? "Unset")));
      for (const comment of read.result.comments) host.append(element("p", comment.actorId + ": " + comment.text));
      host.append(element("p", "Your editor retains its original base. Read the current board, then deliberately open a new editor to reconcile."));
    } catch { if (!this.destroyed && this.editor === editor && generation === this.generation) host.replaceChildren(element("p", "Current text unavailable; editor input retained.")); }
  }
}
