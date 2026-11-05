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
  return passed + runAttentionEditorTests() + runAttentionRoleChangeTests();
}


// M08 category controls share the ordinary editor and its exact complete draft.
export function runAttentionEditorTests(): number {
  let passed = 0;
  const draft = fixture();
  const waitA = "10000000-0000-4000-8000-000000000020", waitB = "10000000-0000-4000-8000-000000000019";
  const initial = draft.workflow.lanes[0]!.id, doing = draft.workflow.lanes[1]!.id, review = draft.workflow.lanes[2]!.id;
  draft.workflow = { ...draft.workflow, revision: 2,
    lanes: [...draft.workflow.lanes, { id: waitA, label: "Outside", role: "waiting" }, { id: waitB, label: "Input", role: "waiting" }],
    transitions: [...draft.workflow.transitions, ...[waitA, waitB].flatMap(id => [
      { from: initial, to: id, actors: ["human"] as const }, { from: id, to: doing, actors: ["human"] as const },
    ])].sort((a, b) => a.from < b.from ? -1 : a.from > b.from ? 1 : a.to < b.to ? -1 : 1) };
  assert(workflowDraftErrors(draft, true).length === 0, "Attention fixture must be valid");
  const root = document.createElement("div"); document.body.append(root);
  const previews: WorkflowDraft[] = [], submissions: WorkflowDraft[] = [], status: WorkflowDraft[] = [];
  const editor = new WorkflowEditor(root, { existing: true, draft, projects: [], change: () => {}, preview: value => previews.push(value), submit: value => submissions.push(value), status: value => status.push(value), cancel: () => {} });
  const button = (text: string): HTMLButtonElement => { const result = [...root.querySelectorAll("button")].find(row => row.textContent === text); assert(result, "Missing action " + text); return result; };
  const select = (id: string): HTMLSelectElement => { const result = [...root.querySelectorAll<HTMLSelectElement>("select")].find(row => row.getAttribute("aria-label") === `Attention category for lane ${id}`); assert(result, "Missing stable lane category control"); return result; };
  const choose = (id: string, value: string): void => { const input = select(id); input.focus(); input.value = value; input.dispatchEvent(new Event("change", { bubbles: true })); };
  try {
    assert(!editor.isDirty() && editor.snapshot().workflow.attention === undefined, "Opening implicit workflow added explicit policy");
    assert(root.textContent!.includes("Attention source: implicit") && select(review).value === "readyForReview", "Implicit review mapping missing");
    assert(!root.querySelector(`[aria-label="Attention category for lane ${initial}"]`), "Initial lane offered for Attention");
    assert([...select(waitA).options].map(row => row.value).join() === ",blocked,needsTerry" && [...select(review).options].map(row => row.value).join() === ",readyForReview", "Role-specific category choices are wrong"); passed++;
    button("Use explicit Attention assignments").click();
    assert(editor.isDirty() && editor.snapshot().workflow.attention?.readyForReview[0] === review, "Explicit identical mapping lost presence");
    choose(waitA, "blocked"); choose(waitB, "blocked");
    assert(JSON.stringify(editor.snapshot().workflow.attention?.blocked) === JSON.stringify([waitB, waitA]), "Category IDs are not sorted");
    choose(waitA, "needsTerry");
    assert(editor.snapshot().workflow.attention?.blocked.join() === waitB && editor.snapshot().workflow.attention?.needsTerry.join() === waitA, "Waiting categories overlap");
    assert(document.activeElement === select(waitA), "Category change lost keyboard focus");
    const label = root.querySelector<HTMLInputElement>('[aria-label="Lane 1 label"]')!;
    label.value = "Mixed policy"; label.dispatchEvent(new Event("input", { bubbles: true }));
    button("Preview workflow").click();
    assert(previews.length === 1 && previews[0]!.workflow.lanes[0]!.label === "Mixed policy" && previews[0]!.workflow.attention?.needsTerry[0] === waitA && button("Save workflow").disabled, "Mixed complete candidate or authoritative preview gate lost");
    editor.approvePreview(); choose(review, "");
    assert(button("Save workflow").disabled && editor.snapshot().workflow.attention?.readyForReview.length === 0, "Explicit review disable failed to invalidate preview"); passed++;
    const malformed = structuredClone(editor.snapshot());
    malformed.workflow = { ...malformed.workflow, attention: { blocked: [waitA], needsTerry: [waitA], readyForReview: [] } };
    assert(workflowDraftErrors(malformed, true).some(row => row.includes("Attention")), "Overlapping categories accepted");
    malformed.workflow = { ...malformed.workflow, attention: { blocked: [initial], needsTerry: [], readyForReview: [] } };
    assert(workflowDraftErrors(malformed, true).some(row => row.includes("Attention")), "Wrong role accepted");
    malformed.workflow = { ...malformed.workflow, attention: { blocked: [waitA, waitB], needsTerry: [], readyForReview: [] } };
    assert(workflowDraftErrors(malformed, true).some(row => row.includes("Attention")), "Unsorted assignment accepted"); passed++;
    button("Disable all Attention categories").click();
    assert(JSON.stringify(editor.snapshot().workflow.attention) === '{"blocked":[],"needsTerry":[],"readyForReview":[]}', "Disable did not retain explicit empty sets");
    button("Restore implicit Attention assignments").click();
    assert(!Object.hasOwn(editor.snapshot().workflow, "attention") && select(review).value === "readyForReview", "Restore did not omit attention");
    choose(waitA, "blocked"); button("Remove lane 5").click();
    assert(editor.snapshot().workflow.attention?.blocked.length === 0 && !editor.snapshot().workflow.lanes.some(row => row.id === waitA), "Retirement retained a dead category assignment"); passed++;
    button("Preview workflow").click(); editor.approvePreview(); button("Save workflow").click();
    assert(submissions.length === 1, "Complete category candidate was not submitted once");
    const exact = JSON.stringify(submissions[0]);
    editor.setState({ locked: true, canCheckStatus: true });
    choose(waitB, "needsTerry"); button("Restore implicit Attention assignments").click(); button("Check configured status").click();
    assert(JSON.stringify(editor.snapshot()) === exact && JSON.stringify(status[0]) === exact, "UNKNOWN changed exact category candidate"); passed++;
  } finally { editor.destroy(); root.remove(); }
  return passed;
}


export function runAttentionRoleChangeTests(): number {
  const draft = fixture(), root = document.createElement("div"); document.body.append(root);
  const originalReview = draft.workflow.lanes[2]!.id;
  const waiting = "10000000-0000-4000-8000-000000000020";
  draft.workflow = { ...draft.workflow, lanes: [...draft.workflow.lanes, { id: waiting, role: "waiting", label: "Waiting" }] };
  const editor = new WorkflowEditor(root, { draft, projects: [{ key: "harbor", label: "Harbor" }], change: () => {}, preview: () => {}, submit: () => {}, status: () => {}, cancel: () => {} });
  const role = (index: number): HTMLSelectElement => root.querySelector<HTMLSelectElement>(`[aria-label="Lane ${index} role"]`)!;
  const category = (id: string): HTMLSelectElement => root.querySelector<HTMLSelectElement>(`[aria-label="Attention category for lane ${id}"]`)!;
  const chooseRole = (index: number, value: string): void => { const input = role(index); input.focus(); input.value = value; input.dispatchEvent(new Event("change", { bubbles: true })); };
  const button = (name: string): HTMLButtonElement => [...root.querySelectorAll("button")].find(row => row.textContent === name)!;
  try {
    chooseRole(3, "waiting"); chooseRole(5, "review");
    assert(document.activeElement === role(5), "Role change did not preserve select focus");
    assert([...category(waiting).options].map(row => row.value).join() === ",readyForReview" && category(waiting).value === "readyForReview", "Role swap did not update implicit review control");
    assert([...category(originalReview).options].map(row => row.value).join() === ",blocked,needsTerry" && category(originalReview).value === "", "Former review lane retained stale category choices");
    assert(editor.snapshot().workflow.attention === undefined && root.textContent!.includes(`Ready for Review mapping: ${waiting}`), "Role swap changed implicit presence or retained old mapping");
    button("Use explicit Attention assignments").click();
    chooseRole(5, "initial");
    const invalid = category(waiting);
    assert(invalid.value === "readyForReview" && invalid.selectedOptions[0]!.disabled && root.textContent!.includes("invalid for the current role"), "Invalid explicit membership was hidden or silently repaired");
    assert(editor.snapshot().workflow.attention?.readyForReview[0] === waiting && workflowDraftErrors(editor.snapshot()).some(row => row.includes("Attention")), "Wrong-role assignment escaped validation");
    invalid.value = ""; invalid.dispatchEvent(new Event("change", { bubbles: true }));
    assert(editor.snapshot().workflow.attention?.readyForReview.length === 0 && !root.querySelector(`[aria-label="Attention category for lane ${waiting}"]`), "Invalid membership could not be deliberately cleared");
    assert(!workflowDraftErrors(editor.snapshot()).some(row => row.includes("Attention")), "Cleared membership retained Attention error");
    chooseRole(3, "review");
    assert(category(originalReview).value === "" && [...category(originalReview).options].map(row => row.value).join() === ",readyForReview", "Explicit disabled review incorrectly restored implicit assignment");
    return 1;
  } finally { editor.destroy(); root.remove(); }
}
