import { createWorkflowDraft, WorkflowEditor, workflowDraftErrors, type WorkflowDraft } from "./workflow-editor.js";

function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
function fixture(): WorkflowDraft { let id = 0; const draft = createWorkflowDraft("harbor", () => `10000000-0000-4000-8000-${String(++id).padStart(12, "0")}`); draft.boardKey = "new-board"; draft.label = "New board"; return draft; }
// Deterministic advisory-validator and DOM behavior tests, not native setup proof.
export function runWorkflowEditorTests(): number {
  let passed = 0;
  const baseline = fixture();
  assert(workflowDraftErrors(baseline).length === 0, "Initial configured draft must be valid");
  assert(new Set([baseline.workflow.workflowId, ...baseline.workflow.lanes.map(lane => lane.id)]).size === 5, "Draft generates each immutable ID once"); passed++;
  const change = (edit: (d: WorkflowDraft) => void): WorkflowDraft => { const d = structuredClone(baseline); edit(d); return d; };
  for (const invalid of [
    change(d => { d.workflow = { ...d.workflow, lanes: d.workflow.lanes.slice(0, 3) }; }),
    change(d => { d.workflow = { ...d.workflow, lanes: d.workflow.lanes.map((l, i) => i === 1 ? { ...l, label: "ready for work" } : l) }; }),
    change(d => { d.workflow = { ...d.workflow, lanes: d.workflow.lanes.map((l, i) => i === 1 ? { ...l, label: " Bad " } : l) }; }),
    change(d => { d.workflow = { ...d.workflow, lanes: d.workflow.lanes.map((l, i) => i === 1 ? { ...l, role: "completed" } : l) }; }),
    change(d => { d.workflow = { ...d.workflow, transitions: [...d.workflow.transitions, d.workflow.transitions[0]!] }; }),
    change(d => { d.workflow = { ...d.workflow, transitions: d.workflow.transitions.map((e, i) => i === 2 ? { ...e, actors: ["automation", "human"] } : e) }; }),
    change(d => { d.workflow = { ...d.workflow, actorRights: { ...d.workflow.actorRights, automation: ["read"] } }; }),
    change(d => { d.workflow = { ...d.workflow, transitions: d.workflow.transitions.map((e, i) => i === 0 ? { ...e, actors: ["automation"] } : e) }; }),
  ]) assert(workflowDraftErrors(invalid).length > 0, "Malformed workflow was accepted by advisory validation"); passed++;
  const unicode = change(d => { d.workflow = { ...d.workflow, lanes: d.workflow.lanes.map((l, i) => ({ ...l, label: i === 0 ? "Σ" : i === 1 ? "ς" : l.label })) }; });
  assert(workflowDraftErrors(unicode).some(error => error.includes("unique")), "Unicode simple-fold duplicate escaped validation");
  const labels = (first: string, second: string): WorkflowDraft => change(d => { d.workflow = { ...d.workflow, lanes: d.workflow.lanes.map((lane, i) => ({ ...lane, label: i === 0 ? first : i === 1 ? second : lane.label })) }; });
  assert(workflowDraftErrors(labels("İ", "i\u0307")).length === 0, "Full-case expansion incorrectly merged distinct simple-fold labels");
  assert(workflowDraftErrors(labels("ı", "I")).length === 0, "Dotless I incorrectly merged with ASCII I");
  for (const [first, second] of [["Σ", "ς"], ["K", "K"], ["\u0390", "\u1fd3"], ["\u03b0", "\u1fe3"], ["\ufb05", "\ufb06"]]) assert(workflowDraftErrors(labels(first!, second!)).some(error => error.includes("unique")), "Known Unicode simple-fold pair escaped validation");
  const astral = change(d => { d.workflow = { ...d.workflow, lanes: d.workflow.lanes.map((l, i) => i === 0 ? { ...l, label: "🏊".repeat(80) } : l) }; });
  assert(workflowDraftErrors(astral).length === 0, "Valid Unicode scalar labels were counted as UTF-16 units"); passed++;
  const root = document.createElement("div"); document.body.append(root);
  const submissions: WorkflowDraft[] = [], changes: WorkflowDraft[] = [], cancels: boolean[] = [], inspections: WorkflowDraft[] = [], previews: WorkflowDraft[] = [];
  const editor = new WorkflowEditor(root, { draft: baseline, projects: [{ key: "harbor", label: "Harbor" }], change: d => changes.push(d), submit: d => submissions.push(d), cancel: dirty => cancels.push(dirty), status: d => inspections.push(d), preview: d => previews.push(d) });
  assert(root.querySelector('select[aria-label="Transition 1 from"]') && root.querySelector('select[aria-label="Lane 1 role"]'), "Select controls lack exact accessible names");
  const button = (text: string): HTMLButtonElement => { const b = [...root.querySelectorAll("button")].find(b => b.textContent === text); assert(b, "Missing button: " + text); return b; };
  const input = (text: string): HTMLInputElement => { const label = [...root.querySelectorAll("label")].find(l => l.firstChild?.textContent === text); const field = label?.querySelector("input"); assert(field, "Missing input: " + text); return field; };
  const type = (label: string, value: string): void => { const field = input(label); field.value = value; field.dispatchEvent(new Event("input", { bubbles: true })); };
  assert(button("Create board").disabled && !editor.isDirty(), "Creation requires explicit preview");
  type("Board label", "<img src=x onerror=alert(1)>");
  input("Board label").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
  assert(submissions.length === 0 && changes.length === 1 && editor.isDirty(), "Input must persist without implicit Enter creation");
  button("Preview workflow").click();
  assert(previews.length === 1 && !button("Create board").disabled && !root.querySelector("img"), "Preview must be explicit and safely render input");
  assert(root.querySelector(".workflow-preview")?.textContent?.includes("in_progress") && root.querySelector(".workflow-preview")?.textContent?.includes("automation rights"), "Complete preview omits roles or rights"); passed++;
  editor.setState({ canCreate: false, message: "Private storage unavailable" });
  assert(button("Create board").disabled && !root.querySelector("fieldset")!.disabled, "Unavailable private storage must block Create without locking draft");
  button("Create board").click(); assert(submissions.length === 0, "Unavailable private storage allowed Create");
  editor.setState({});
  button("Create board").click(); button("Create board").click();
  assert(Number(submissions.length) === 1 && button("Create board").disabled, "Double create dispatched twice");
  assert(JSON.stringify(submissions[0]!.workflow) === JSON.stringify(baseline.workflow), "Preview/submission changed immutable identities or definition");
  editor.setState({ locked: true, canCheckStatus: true, message: "UNKNOWN: check configured status deliberately." });
  button("Check configured status").click();
  assert(inspections.length === 1 && JSON.stringify(inspections[0]!.workflow) === JSON.stringify(submissions[0]!.workflow), "Status changed the exact submitted workflow");
  type("Board label", "Must not change");
  assert(editor.snapshot().label === submissions[0]!.label, "Locked UNKNOWN draft changed"); passed++;
  editor.setState({}); type("Lane 1 label", ""); button("Preview workflow").click();
  assert(input("Lane 1 label").value === "" && button("Create board").disabled && root.querySelector("[role=alert]")?.textContent?.includes("Lane 1"), "Invalid input was erased or not identified");
  assert(document.activeElement === root.querySelector("[role=alert]"), "Invalid preview must focus validation summary");
  type("Lane 1 label", "Queue"); button("Preview workflow").click(); type("Board label", "Changed after preview");
  assert(button("Create board").disabled, "Changing draft must invalidate preview"); passed++;
  const originalIDs = editor.snapshot().workflow.lanes.map(l => l.id);
  button("Move down lane 1").click();
  assert(editor.snapshot().workflow.lanes[1]!.id === originalIDs[0] && editor.snapshot().workflow.workflowId === baseline.workflow.workflowId, "Reorder regenerated immutable IDs");
  button("Add waiting lane").click(); const addedID = editor.snapshot().workflow.lanes.at(-1)!.id;
  type("Lane 5 label", "Paused"); assert(editor.snapshot().workflow.lanes.at(-1)!.id === addedID, "Editing regenerated new lane ID"); passed++;
  const field = input("Board label"); field.dispatchEvent(new Event("compositionstart", { bubbles: true })); field.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  assert(cancels.length === 0, "IME escape dismissed editor"); field.dispatchEvent(new Event("compositionend", { bubbles: true })); field.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  assert(Number(cancels.length) === 1 && cancels[0], "Escape did not delegate dirty dismissal");
  const snapshot = editor.snapshot(); snapshot.label = "External edit"; assert(editor.snapshot().label !== snapshot.label, "Snapshot leaks mutable draft");
  editor.destroy(); root.remove(); passed++;
  const existing = fixture();
  existing.workflow = { ...existing.workflow, revision: 2, profile: "default", lanes: existing.workflow.lanes.map(lane => ({ ...lane, id: lane.label })), transitions: [
    { from: "In Progress", to: "Ready for Review", actors: ["automation", "human"] },
    { from: "Ready for Review", to: "Completed", actors: ["human"] },
    { from: "Ready for Work", to: "In Progress", actors: ["automation", "human"] },
  ] };
  assert(workflowDraftErrors(existing, true).length === 0, "Existing default draft must retain literal lane identities");
  const editRoot = document.createElement("div"); document.body.append(editRoot);
  let saveCount = 0, previewCount = 0;
  const edit = new WorkflowEditor(editRoot, { existing: true, draft: existing, projects: [], change: () => {}, submit: () => { saveCount++; }, preview: () => { previewCount++; }, cancel: () => {}, status: () => {} });
  const editButton = (text: string): HTMLButtonElement => { const result = [...editRoot.querySelectorAll("button")].find(node => node.textContent === text); assert(result, "Missing existing-workflow action"); return result; };
  assert(!!(editRoot.querySelector(".workflow-actions")!.compareDocumentPosition(editRoot.querySelector("fieldset")!) & Node.DOCUMENT_POSITION_FOLLOWING), "Existing workflow actions must precede the long editor");
  assert(!editRoot.querySelector('[aria-label="Board key"]') && !editRoot.querySelector('[aria-label="Lane 1 role"]'), "Existing identities must not offer replacement controls");
  assert(editButton("Remove lane 1").disabled && editRoot.textContent?.includes("identity Ready for Work"), "Fixed lane identity or role is not preserved visibly");
  editButton("Preview workflow").click();
  assert(previewCount === 1 && editButton("Save workflow").disabled, "Local preview must wait for authoritative preview");
  edit.approvePreview(); edit.setState({ canCreate: false });
  assert(editButton("Save workflow").disabled, "Recovery failure must block workflow save");
  edit.setState({}); editButton("Save workflow").click(); editButton("Save workflow").click();
  assert(saveCount === 1, "Workflow save must dispatch once");
  edit.setState({ locked: true });
  const fixedLabel = editRoot.querySelector<HTMLInputElement>('[aria-label="Lane 1 label"]')!;
  fixedLabel.value = "Changed"; fixedLabel.dispatchEvent(new Event("input", { bubbles: true }));
  assert(edit.snapshot().workflow.lanes[0]!.label === "Ready for Work", "UNKNOWN must lock exact workflow draft");
  edit.destroy(); editRoot.remove(); passed++;
  return passed;
}
