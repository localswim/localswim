import type * as A from "../../generated/archive/contracts.js";
import { resolvedAttention, validateAttentionAssignments } from "../api/workflow.js";

export type WorkflowDraft = { projectKey: string; boardKey: string; label: string; workflow: A.Workflow };
export type WorkflowEditorState = { busy?: boolean; locked?: boolean; message?: string; canCheckStatus?: boolean; canCreate?: boolean };
export type WorkflowEditorOptions = {
  existing?: boolean;
  draft: WorkflowDraft; projects: readonly { key: string; label: string }[];
  change(draft: WorkflowDraft): void; submit(draft: WorkflowDraft): void;
  preview?(draft: WorkflowDraft): void;
  cancel(dirty: boolean): void; status(draft: WorkflowDraft): void;
};
const roles = ["initial", "in_progress", "review", "completed", "waiting"] as const;
const rights = ["approve", "attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"];
const actors = ["automation", "human"] as const;
const copy = <T>(value: T): T => structuredClone(value);
const edgeOrder = (a: A.WorkflowTransition, b: A.WorkflowTransition): number => a.from < b.from ? -1 : a.from > b.from ? 1 : a.to < b.to ? -1 : a.to > b.to ? 1 : 0;
const uuidShape = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
function validID(value: string): boolean { return uuidShape.test(value) && value !== "00000000-0000-0000-0000-000000000000"; }
function fold(value: string): string { return [...value].map(c => {
  // Go's simple folding keeps scalar identity; JavaScript also offers full-case
  // expansions, so retain those scalars and cover their simple-fold partners.
  if (c === "ı") return c;
  if (c === "\u1fd3") return "\u0390";
  if (c === "\u1fe3") return "\u03b0";
  if (c === "\ufb06") return "\ufb05";
  const upper = c.toUpperCase(), lower = [...upper].length === 1 ? upper.toLowerCase() : c.toLowerCase();
  return [...lower].length === 1 ? lower : c;
}).join(""); }
function validLabel(value: string): boolean { return [...value].length >= 1 && [...value].length <= 80 && !/[\u0000-\u001f\u007f\ud800-\udfff]/u.test(value) && !/^\p{White_Space}|\p{White_Space}$/u.test(value); }

export function createWorkflowDraft(projectKey: string, uuid: () => string = () => crypto.randomUUID()): WorkflowDraft {
  const lanes = ["Ready for Work", "In Progress", "Ready for Review", "Completed"].map((label, index) => ({ id: uuid(), label, role: roles[index]! }));
  const transitions = lanes.slice(0, 3).map((lane, index) => ({ from: lane.id, to: lanes[index + 1]!.id, actors: index === 2 ? ["human" as const] : [...actors] })).sort(edgeOrder);
  return { projectKey, boardKey: "", label: "", workflow: { workflowId: uuid(), revision: 1, lanes, transitions, actorRights: { human: [...rights], automation: rights.filter(right => right !== "approve") } } };
}

// Advisory checks only: the strict setup boundary remains authoritative.
export function workflowDraftErrors(draft: WorkflowDraft, existing = false): string[] {
  const errors: string[] = [], w = draft.workflow;
  try { validateAttentionAssignments(w); } catch { errors.push("Attention categories require disjoint active waiting lanes and at most the review lane, with sorted unique IDs."); }
  if (!existing && (draft.boardKey.length > 64 || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(draft.boardKey))) errors.push("Board key must contain 1–64 lowercase letters or digits, separated by single hyphens.");
  if (!existing && !draft.projectKey) errors.push("Select an existing project.");
  if (!existing && (!draft.label.trim() || [...draft.label].length > 200 || /[\ud800-\udfff]/u.test(draft.label))) errors.push("Board label must contain 1–200 valid characters and cannot be only whitespace.");
  if (!validID(w.workflowId) || (!existing ? w.revision !== 1 : !Number.isSafeInteger(w.revision) || w.revision < 2)) errors.push("Workflow identity is invalid.");
  if (w.lanes.length < 4 || w.lanes.length > 16) errors.push("Use 4–16 lanes.");
  const ids = new Set<string>(), labels = new Set<string>();
  w.lanes.forEach((lane, index) => {
    if ((!validID(lane.id) && !(existing && w.profile === "default" && ["Ready for Work", "In Progress", "Ready for Review", "Completed"].includes(lane.id))) || ids.has(lane.id)) errors.push(`Lane ${index + 1}: identity is invalid or duplicated.`);
    ids.add(lane.id);
    if (!validLabel(lane.label) || labels.has(fold(lane.label))) errors.push(`Lane ${index + 1}: use a unique 1–80 character label without surrounding whitespace or controls.`);
    labels.add(fold(lane.label));
    if (!(roles as readonly string[]).includes(lane.role)) errors.push(`Lane ${index + 1}: select a role.`);
  });
  for (const role of roles.slice(0, 4)) if (w.lanes.filter(lane => lane.role === role).length !== 1) errors.push(`Use exactly one ${role} lane.`);
  for (const actor of actors) {
    const set = w.actorRights[actor];
    if (set.some((right, index) => !rights.includes(right) || (actor === "automation" && right === "approve") || (index > 0 && set[index - 1]! >= right))) errors.push(`${actor}: rights must be allowed, unique and sorted.`);
    const required = actor === "human" ? ["read", "read-own-outcome", "create", "edit", "review-handoff", "approve"] : ["read", "read-own-outcome"];
    if (required.some(right => !set.includes(right))) errors.push(`${actor}: required rights are missing.`);
  }
  if (w.transitions.length < 3 || w.transitions.length > 64) errors.push("Use 3–64 transitions.");
  const laneByID = new Map(w.lanes.map(lane => [lane.id, lane]));
  w.transitions.forEach((edge, index) => {
    const from = laneByID.get(edge.from), to = laneByID.get(edge.to);
    if (!from || !to || edge.from === edge.to || from.role === "completed" || (index > 0 && edgeOrder(w.transitions[index - 1]!, edge) >= 0)) errors.push(`Transition ${index + 1}: endpoints must be distinct valid lanes, unique and ordered; Completed cannot have outgoing transitions.`);
    if (edge.actors.length === 0 || edge.actors.some((actor, i) => !(actors as readonly string[]).includes(actor) || (i > 0 && edge.actors[i - 1]! >= actor))) errors.push(`Transition ${index + 1}: select allowed unique actors.`);
    if (to?.role === "review" && (from?.role !== "in_progress" || !edge.actors.includes("human"))) errors.push(`Transition ${index + 1}: only In Progress can enter Review and must allow human.`);
    if (to?.role === "completed" && (from?.role !== "review" || edge.actors.length !== 1 || edge.actors[0] !== "human")) errors.push(`Transition ${index + 1}: only human Review can enter Completed.`);
    const right = to?.role === "completed" ? "approve" : to?.role === "review" ? "review-handoff" : "edit";
    if (edge.actors.some(actor => !w.actorRights[actor].includes(right))) errors.push(`Transition ${index + 1}: actor rights must include ${right}.`);
  });
  for (const role of ["review", "completed"]) if (w.transitions.filter(edge => laneByID.get(edge.to)?.role === role).length !== 1) errors.push(`Use exactly one transition into ${role}.`);
  const reachable = (start: string): Set<string> => { const seen = new Set([start]); for (let i = 0; i < w.lanes.length; i++) for (const edge of w.transitions) if (seen.has(edge.from) && edge.actors.includes("human")) seen.add(edge.to); return seen; };
  const initial = w.lanes.find(lane => lane.role === "initial"), completed = w.lanes.find(lane => lane.role === "completed");
  if (initial && w.lanes.some(lane => !reachable(initial.id).has(lane.id))) errors.push("Every lane must be reachable from Initial by human transitions.");
  if (completed && w.lanes.some(lane => !reachable(lane.id).has(completed.id))) errors.push("Every lane must have a human path to Completed.");
  if (new TextEncoder().encode(JSON.stringify(w)).length > 65536) errors.push("Workflow exceeds 64 KiB.");
  return errors;
}

function node<K extends keyof HTMLElementTagNameMap>(tag: K, text = ""): HTMLElementTagNameMap[K] { const element = document.createElement(tag); element.textContent = text; return element; }
export class WorkflowEditor {
  private draft: WorkflowDraft;
  private baseline: string;
  private state: WorkflowEditorState = {};
  private previewed = false;
  private sent = false;
  private disposed = false;
  private composing = false;
  private compositionEnd = false;
  private readonly form: HTMLFormElement | HTMLDivElement;
  private readonly fields = node("fieldset");
  private readonly preview = node("section");
  private readonly feedback = node("div");
  private readonly message = node("p");
  private readonly create = node("button", "Create board");
  private readonly check = node("button", "Check configured status");
  constructor(private readonly root: HTMLElement, private readonly options: WorkflowEditorOptions) {
    this.form = options.existing ? node("div") : node("form");
    this.draft = copy(options.draft); this.baseline = JSON.stringify(this.draft);
    if (options.existing) this.create.textContent = "Save workflow";
    this.form.className = options.existing ? "workflow-editor workflow-editor-existing" : "workflow-editor"; if (this.form instanceof HTMLFormElement) this.form.noValidate = true;
    this.form.addEventListener("submit", event => event.preventDefault());
    this.form.addEventListener("compositionstart", () => { this.composing = true; });
    this.form.addEventListener("compositionend", () => { this.composing = false; this.compositionEnd = true; setTimeout(() => { this.compositionEnd = false; }, 0); });
    this.form.addEventListener("keydown", raw => {
      const event = raw as KeyboardEvent;
      if (this.disposed) return;
      if (event.key === "Enter" && (this.composing || this.compositionEnd || event.isComposing || event.keyCode === 229)) { event.preventDefault(); return; }
      if (event.key === "Escape" && !this.composing && !event.isComposing) { event.preventDefault(); this.options.cancel(this.isDirty()); }
      if (event.key === "Enter" && event.target instanceof HTMLInputElement && !this.composing && !event.isComposing && event.keyCode !== 229) event.preventDefault();
    });
    this.feedback.setAttribute("role", "alert"); this.feedback.tabIndex = -1; this.message.setAttribute("role", "status");
    this.preview.className = "workflow-preview"; this.preview.setAttribute("aria-label", "Complete workflow preview");
    this.create.type = this.check.type = "button";
    this.create.addEventListener("click", () => { if (this.disposed || this.composing || this.compositionEnd || !this.previewed || this.sent || this.state.busy || this.state.locked || this.state.canCreate === false || workflowDraftErrors(this.draft, !!this.options.existing).length) return; this.sent = true; this.applyState(); this.options.submit(this.snapshot()); });
    this.check.addEventListener("click", () => { if (!this.disposed && !this.state.busy && this.state.canCheckStatus) this.options.status(this.snapshot()); });
    const actions = node("div"); actions.className = "workflow-actions";
    actions.append(this.button("Preview workflow", () => this.showPreview(), "workflow-preview-button"), this.create, this.check, this.button("Cancel", () => options.cancel(this.isDirty())));
    this.form.append(node("h1", options.existing ? "Edit existing workflow" : "Create a configured board"), node("p", options.existing ? "Edit labels, order, ordinary transitions, ceilings and Attention categories. Lane identities and roles remain fixed. Cards and human approvals are preserved. Preview is read-only; Save is deliberate." : "Choose an immutable workflow for a new board in an existing project. Roles define behavior; labels are display names. Default board creation remains available through the CLI."), this.fields, this.feedback, this.preview, this.message, actions);
    if (options.existing) this.fields.before(actions);
    root.replaceChildren(this.form); this.renderFields(); this.applyState();
  }
  approvePreview(): void { this.previewed = true; this.applyState(); }
  snapshot(): WorkflowDraft { return copy(this.draft); }
  isDirty(): boolean { return JSON.stringify(this.draft) !== this.baseline; }
  setState(state: WorkflowEditorState): void { this.state = { ...state }; if (!state.busy && !state.locked) this.sent = false; this.applyState(); }
  destroy(): void { this.disposed = true; this.root.replaceChildren(); }
  private applyState(): void {
    this.fields.disabled = !!(this.state.busy || this.state.locked || this.sent);
    this.create.disabled = !this.previewed || this.state.canCreate === false || !!(this.state.busy || this.state.locked || this.sent);
    this.check.hidden = !this.state.canCheckStatus; this.check.disabled = !!this.state.busy;
    this.message.textContent = this.state.message ?? ""; this.form.setAttribute("aria-busy", String(!!this.state.busy));
  }
  private changed(render = false): void {
    const activeElement = this.fields.contains(document.activeElement) ? document.activeElement : null;
    const activeName = activeElement?.getAttribute("aria-label");
    const active = activeElement?.textContent;
    this.previewed = false; this.preview.replaceChildren(); this.feedback.replaceChildren(); if (render) this.renderFields(); this.applyState(); this.options.change(this.snapshot());
    if (render && active) {
      const buttons = [...this.fields.querySelectorAll("button")].filter(button => !button.disabled);
      const named = activeName ? [...this.fields.querySelectorAll<HTMLElement>("[aria-label]")].find(field => field.getAttribute("aria-label") === activeName) : undefined;
      (named ?? buttons.find(button => button.textContent === active) ?? buttons.at(-1))?.focus();
    }
  }
  private button(text: string, action: () => void, className = ""): HTMLButtonElement { const b = node("button", text); b.type = "button"; b.className = className; b.addEventListener("click", () => { if (!this.disposed && !b.disabled && !(this.fields.disabled && this.fields.contains(b))) action(); }); return b; }
  private field(text: string, value: string, change: (value: string) => void, choices?: readonly { value: string; label: string }[], render = false): HTMLLabelElement {
    const label = node("label", text), input = choices ? node("select") : node("input");
    input.setAttribute("aria-label", text);
    if (input instanceof HTMLSelectElement) for (const choice of choices!) { const option = node("option", choice.label); option.value = choice.value; input.append(option); }
    else { input.type = "text"; input.autocomplete = "off"; }
    input.value = value; input.addEventListener(choices ? "change" : "input", () => { if (this.disposed || this.fields.disabled) return; change(input.value); this.changed(render); }); label.append(input); return label;
  }
  private renderFields(): void {
    this.fields.replaceChildren(node("legend", "Board and workflow"));
    if (!this.options.existing) this.fields.append(this.field("Project", this.draft.projectKey, value => { this.draft.projectKey = value; }, this.options.projects.map(p => ({ value: p.key, label: p.label }))), this.field("Board key", this.draft.boardKey, value => { this.draft.boardKey = value; }), this.field("Board label", this.draft.label, value => { this.draft.label = value; }));
    const lanes = node("section"); lanes.append(node("h2", "Ordered lanes (4–16)"));
    this.draft.workflow.lanes.forEach((lane, index) => {
      const row = node("div"); row.className = "workflow-row";
      row.append(this.field(`Lane ${index + 1} label`, lane.label, value => this.replaceLane(index, { ...this.draft.workflow.lanes[index]!, label: value })));
      if (this.options.existing) row.append(node("p", `Lane ${index + 1} identity ${lane.id} · role ${lane.role}`));
      else row.append(this.field(`Lane ${index + 1} role`, lane.role, value => this.replaceLane(index, { ...this.draft.workflow.lanes[index]!, role: value }), roles.map(value => ({ value, label: value })), true));
      for (const [text, offset] of [["Move up", -1], ["Move down", 1]] as const) { const b = this.button(`${text} lane ${index + 1}`, () => { const next = [...this.draft.workflow.lanes]; [next[index], next[index + offset]] = [next[index + offset]!, next[index]!]; this.draft.workflow = { ...this.draft.workflow, lanes: next }; this.changed(true); }); b.disabled = index + offset < 0 || index + offset >= this.draft.workflow.lanes.length; row.append(b); }
      const remove = this.button(`Remove lane ${index + 1}`, () => { const attention = this.draft.workflow.attention;
        this.draft.workflow = { ...this.draft.workflow, lanes: this.draft.workflow.lanes.filter((_, i) => i !== index), transitions: this.draft.workflow.transitions.filter(edge => edge.from !== lane.id && edge.to !== lane.id),
          ...(attention ? { attention: { blocked: attention.blocked.filter(id => id !== lane.id), needsTerry: attention.needsTerry.filter(id => id !== lane.id), readyForReview: attention.readyForReview.filter(id => id !== lane.id) } } : {}) }; this.changed(true); }); remove.disabled = this.draft.workflow.lanes.length <= 4 || !!this.options.existing && lane.role !== "waiting"; row.append(remove); lanes.append(row);
    });
    const addLane = this.button("Add waiting lane", () => { this.draft.workflow = { ...this.draft.workflow, lanes: [...this.draft.workflow.lanes, { id: crypto.randomUUID(), label: "", role: "waiting" }] }; this.changed(true); }); addLane.disabled = this.draft.workflow.lanes.length >= 16; lanes.append(addLane); this.fields.append(lanes);
    this.renderAttention();
    const edges = node("section"); edges.append(node("h2", "Directed transitions (3–64)"));
    const choices = this.draft.workflow.lanes.map((lane, index) => ({ value: lane.id, label: `${index + 1}. ${lane.label || "Unnamed"} (${lane.role})` }));
    this.draft.workflow.transitions.forEach((edge, index) => {
      const row = node("div"); row.className = "workflow-row";
      const update = (next: A.WorkflowTransition): void => { this.draft.workflow = { ...this.draft.workflow, transitions: this.draft.workflow.transitions.map((e, i) => i === index ? next : e) }; };
      row.append(this.field(`Transition ${index + 1} from`, edge.from, value => update({ ...this.draft.workflow.transitions[index]!, from: value }), choices), this.field(`Transition ${index + 1} to`, edge.to, value => update({ ...this.draft.workflow.transitions[index]!, to: value }), choices));
      for (const actor of actors) { const label = node("label", `Transition ${index + 1} ${actor}`), input = node("input"); input.type = "checkbox"; input.checked = edge.actors.includes(actor); input.addEventListener("change", () => { if (this.disposed || this.fields.disabled) return; const current = this.draft.workflow.transitions[index]!; update({ ...current, actors: input.checked ? [...current.actors, actor].sort() : current.actors.filter(a => a !== actor) }); this.changed(); }); label.append(input); row.append(label); }
      row.append(this.button(`Remove transition ${index + 1}`, () => { this.draft.workflow = { ...this.draft.workflow, transitions: this.draft.workflow.transitions.filter((_, i) => i !== index) }; this.changed(true); })); edges.append(row);
    });
    const addEdge = this.button("Add transition", () => { this.draft.workflow = { ...this.draft.workflow, transitions: [...this.draft.workflow.transitions, { from: this.draft.workflow.lanes[0]!.id, to: this.draft.workflow.lanes[1]!.id, actors: ["human"] }] }; this.changed(true); }); addEdge.disabled = this.draft.workflow.transitions.length >= 64; edges.append(addEdge); this.fields.append(edges);
    const ceilings = node("section"); ceilings.append(node("h2", "Ordinary-right ceilings"));
    for (const actor of actors) for (const right of rights.filter(r => actor === "human" || r !== "approve")) { const label = node("label", `${actor}: ${right}`), input = node("input"); input.type = "checkbox"; input.checked = this.draft.workflow.actorRights[actor].includes(right); input.addEventListener("change", () => { if (this.disposed || this.fields.disabled) return; const current = this.draft.workflow.actorRights[actor]; this.draft.workflow = { ...this.draft.workflow, actorRights: { ...this.draft.workflow.actorRights, [actor]: input.checked ? [...current, right].sort() : current.filter(r => r !== right) } }; this.changed(); }); label.append(input); ceilings.append(label); } this.fields.append(ceilings);
  }
  private renderAttention(): void {
    const section = node("section"), workflow = this.draft.workflow;
    const assignments = resolvedAttention(workflow), explicit = workflow.attention !== undefined;
    section.setAttribute("aria-label", "Attention categories");
    section.append(node("h2", "Attention categories"), node("p", `Attention source: ${explicit ? "explicit" : "implicit"}. Policy ${workflow.revision}.`),
      node("p", "Blocked waits on an external condition. Needs Terry requires a specific input or action. Ready for Review awaits human review or approval."),
      node("p", "An empty category is Not configured. A configured category with no matching unarchived cards has count 0. Category choices preserve cards and approval rules."));
    section.append(this.button("Use explicit Attention assignments", () => {
      this.draft.workflow = { ...this.draft.workflow, attention: resolvedAttention(this.draft.workflow) }; this.changed(true);
    }), this.button("Restore implicit Attention assignments", () => {
      const { attention: _attention, ...rest } = this.draft.workflow; this.draft.workflow = rest; this.changed(true);
    }), this.button("Disable all Attention categories", () => {
      this.draft.workflow = { ...this.draft.workflow, attention: { blocked: [], needsTerry: [], readyForReview: [] } }; this.changed(true);
    }));
    const categoryNames = { blocked: "Blocked", needsTerry: "Needs Terry", readyForReview: "Ready for Review" };
    const categoryKeys = ["blocked", "needsTerry", "readyForReview"] as const;
    for (const lane of workflow.lanes.filter(lane => lane.role === "waiting" || lane.role === "review" || categoryKeys.some(key => assignments[key].includes(lane.id)))) {
      const label = node("label", `${lane.label || "Unnamed lane"} · ID ${lane.id} · role ${lane.role}`), select = node("select");
      select.setAttribute("aria-label", `Attention category for lane ${lane.id}`);
      const choices = [["", "Not configured"], ...(lane.role === "review" ? [["readyForReview", "Ready for Review"]] : lane.role === "waiting" ? [["blocked", "Blocked"], ["needsTerry", "Needs Terry"]] : [])];
      for (const [value, text] of choices) { const option = node("option", text); option.value = value!; select.append(option); }
      const assigned = categoryKeys.find(key => assignments[key].includes(lane.id));
      if (assigned && !choices.some(([value]) => value === assigned)) {
        const invalid = node("option", `Invalid assignment: ${categoryNames[assigned]}`); invalid.value = assigned; invalid.disabled = true; select.append(invalid);
        label.append(node("span", " This assignment is invalid for the current role. Clear it or restore the lane role before previewing."));
      }
      select.value = assigned ?? "";
      select.addEventListener("change", () => {
        if (this.disposed || this.fields.disabled) return;
        const current = resolvedAttention(this.draft.workflow);
        const next = { blocked: [...current.blocked], needsTerry: [...current.needsTerry], readyForReview: [...current.readyForReview] };
        for (const key of ["blocked", "needsTerry", "readyForReview"] as const) next[key] = next[key].filter(id => id !== lane.id);
        if (select.value === "blocked" || select.value === "needsTerry" || select.value === "readyForReview") next[select.value].push(lane.id);
        for (const key of ["blocked", "needsTerry", "readyForReview"] as const) next[key].sort();
        this.draft.workflow = { ...this.draft.workflow, attention: next }; this.changed(true);
      });
      label.append(select); section.append(label);
    }
    for (const key of ["blocked", "needsTerry", "readyForReview"] as const) section.append(node("p", `${categoryNames[key]} mapping: ${assignments[key].join(", ") || "Not configured"}`));
    this.fields.append(section);
  }
  private replaceLane(index: number, lane: A.WorkflowLane): void { this.draft.workflow = { ...this.draft.workflow, lanes: this.draft.workflow.lanes.map((old, i) => i === index ? lane : old) }; }
  private showPreview(): void {
    if (this.disposed || this.composing || this.compositionEnd || this.state.busy || this.state.locked || this.sent) return;
    // Sorting here is explicit draft construction, never normalization of submitted/status evidence.
    this.draft.workflow = { ...this.draft.workflow, transitions: [...this.draft.workflow.transitions].sort(edgeOrder) }; this.renderFields(); this.options.change(this.snapshot());
    const errors = workflowDraftErrors(this.draft, !!this.options.existing); this.feedback.replaceChildren();
    for (const error of errors) this.feedback.append(node("p", error));
    if (errors.length) this.feedback.focus();
    this.previewed = errors.length === 0; this.preview.replaceChildren();
    if (this.previewed) {
      if (this.options.existing) { this.previewed = false; this.options.preview?.(this.snapshot()); this.applyState(); return; }
      this.options.preview?.(this.snapshot());
      this.preview.append(node("h2", "Review before Create"), node("p", "This complete definition is immutable after creation. Lane order is the order shown. Actor-right ceilings do not grant credentials or authority."), node("p", `Project: ${this.draft.projectKey} · Board: ${this.draft.label} (${this.draft.boardKey})`));
      const lanes = node("ol"); for (const lane of this.draft.workflow.lanes) lanes.append(node("li", `${lane.label} — ${lane.role} · ${lane.id}`)); this.preview.append(node("h3", "Ordered lanes"), lanes);
      const edges = node("ul"), label = (id: string): string => { const lane = this.draft.workflow.lanes.find(l => l.id === id)!; return `${lane.label} (${lane.role})`; };
      for (const edge of this.draft.workflow.transitions) edges.append(node("li", `${label(edge.from)} → ${label(edge.to)} · ${edge.actors.join(", ")}`)); this.preview.append(node("h3", "Allowed transitions"), edges);
      for (const actor of actors) this.preview.append(node("p", `${actor} rights: ${this.draft.workflow.actorRights[actor].join(", ")}`));
      this.preview.append(node("h3", "Attention categories"), node("p", `Attention source: ${this.draft.workflow.attention === undefined ? "implicit" : "explicit"}.`));
      const assignments = resolvedAttention(this.draft.workflow);
      for (const [key, name] of [["blocked", "Blocked"], ["needsTerry", "Needs Terry"], ["readyForReview", "Ready for Review"]] as const) this.preview.append(node("p", `${name}: ${assignments[key].map(id => `${label(id)} · ID ${id}`).join(", ") || "Not configured"}`));
      const details = node("details"); details.append(node("summary", "Complete definition and immutable IDs"), node("pre", JSON.stringify(this.draft, null, 2))); this.preview.append(details);
    }
    this.applyState();
  }
}
