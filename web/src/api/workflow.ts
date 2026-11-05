import type { Workflow, WorkflowLane, WorkflowPolicy, Card, Actor, Approval } from "../../generated/archive/contracts.js";
import type { SessionRead, WorkflowPreview } from "../../generated/transport/contracts.js";

// Presentation/advisory only. Callers supply the complete definition from the
// same validated read; these helpers never authorize or manufacture policy.
const defaults: readonly WorkflowLane[] = [
  { id: "Ready for Work", label: "Ready for Work", role: "initial" },
  { id: "In Progress", label: "In Progress", role: "in_progress" },
  { id: "Ready for Review", label: "Ready for Review", role: "review" },
  { id: "Completed", label: "Completed", role: "completed" },
];
export const workflowLanes = (workflow?: Workflow): readonly WorkflowLane[] => workflow?.lanes ?? defaults;
export const laneRole = (id: string, workflow?: Workflow): string | undefined => workflowLanes(workflow).find(lane => lane.id === id)?.role;
export const laneText = (id: string, workflow?: Workflow): string => {
  const lane = workflowLanes(workflow).find(row => row.id === id);
  return lane ? lane.label + (workflow ? " (" + lane.role + ")" : "") : "Unavailable lane";
};
export const roleLane = (role: string, workflow?: Workflow): string | undefined => workflowLanes(workflow).find(lane => lane.role === role)?.id;
export const completedLane = (id: string, workflow?: Workflow): boolean => laneRole(id, workflow) === "completed";
export function workflowRight(session: SessionRead, right: SessionRead["grant"]["rights"][number], workflow?: Workflow): boolean {
  return session.grant.rights.includes(right) && (!workflow || workflow.actorRights[session.kind].includes(right));
}
export function workflowEdge(from: string, to: string, session: SessionRead, right: "edit" | "review-handoff" | "approve", workflow?: Workflow): boolean {
  if (!workflowRight(session, right, workflow)) return false;
  if (!workflow) return (from === "Ready for Work" && to === "In Progress" || from === "In Progress" && to === "Ready for Review" || from === "Ready for Review" && to === "Completed") && (right !== "approve" || session.kind === "human");
  return workflow.transitions.some(edge => edge.from === from && edge.to === to && edge.actors.includes(session.kind)) && (right !== "approve" || session.kind === "human");
}
export function moveLanes(from: string, session: SessionRead, workflow: Workflow): readonly WorkflowLane[] {
  return workflow.lanes.filter(lane => ["initial", "in_progress", "waiting"].includes(lane.role) && workflowEdge(from, lane.id, session, "edit", workflow));
}

export function sameWorkflow(a?: Workflow, b?: Workflow): boolean {
  const project = (w: Workflow) => [w.workflowId, w.revision, w.profile, w.lanes.map(lane => [lane.id, lane.label, lane.role]), w.transitions.map(edge => [edge.from, edge.to, edge.actors]), w.actorRights.human, w.actorRights.automation];
  return a === undefined || b === undefined ? a === b : JSON.stringify(project(a)) === JSON.stringify(project(b));
}

// Historical state pins resolve solely within the coherent selected-board read.
export function workflowAtVersion(current: Workflow | undefined, policies: readonly WorkflowPolicy[] | undefined, version: number | null = 1): Workflow | undefined {
  if (version === null || !Number.isSafeInteger(version) || version < 1) throw Error("Invalid workflow policy pin");
  if (policies === undefined) {
    if (version !== 1 || current && current.revision !== 1) throw Error("Workflow policy unavailable");
    return current;
  }
  const policy = policies[version - 1];
  if (!policy || policy.policyVersion !== version) throw Error("Workflow policy unavailable");
  return policy.workflow;
}
export function validateWorkflowPolicies(current: Workflow | undefined, policies: readonly WorkflowPolicy[] | undefined, policyVersion = current?.revision ?? 1): void {
  if (!Number.isSafeInteger(policyVersion) || policyVersion < 1 || policyVersion !== (current?.revision ?? 1)) throw Error("Current policy version mismatch");
  if (policies === undefined) {
    if (current && (current.revision !== 1 || current.profile !== undefined)) throw Error("Workflow history unavailable");
    return;
  }
  if (policies.length < 2 || policies.length > 128 || !current || current.revision !== policies.length || new TextEncoder().encode(JSON.stringify(policies)).length > 8388608) throw Error("Invalid workflow history");
  const initial = policies[0]!.workflow, profile = initial ? undefined : "default";
  const known = new Map(workflowLanes(initial).map(lane => [lane.id, lane.role]));
  let active = new Set(known.keys());
  for (let index = 0; index < policies.length; index++) {
    const policy = policies[index]!;
    if (policy.policyVersion !== index + 1 || index > 0 && !policy.workflow) throw Error("Incomplete workflow history");
    const workflow = policy.workflow;
    if (workflow && (workflow.revision !== policy.policyVersion || workflow.profile !== profile || workflow.workflowId !== current.workflowId)) throw Error("Workflow identity changed");
    if (index === 0) continue;
    const next = new Set<string>();
    for (const lane of workflow!.lanes) {
      if (known.has(lane.id) && (!active.has(lane.id) || known.get(lane.id) !== lane.role)) throw Error("Workflow lane identity changed");
      known.set(lane.id, lane.role); next.add(lane.id);
    }
    for (const id of active) if (!next.has(id) && known.get(id) !== "waiting") throw Error("Required workflow lane removed");
    active = next;
  }
  if (!sameWorkflow(current, policies.at(-1)!.workflow)) throw Error("Current workflow mismatch");
}

// This validates supplied approval evidence for display and prerequisite advice.
// Ordinary admission still rechecks the authoritative writer snapshot.
export function currentApprovalValid(card: Pick<Card, "cardId" | "revision" | "lane" | "currentApprovalId" | "workflowPolicyVersion">, current: Workflow | undefined, policies: readonly WorkflowPolicy[] | undefined, actors: readonly Actor[], approvals: readonly Approval[]): boolean {
  if (laneRole(card.lane, current) !== "completed" || !card.currentApprovalId || card.revision < 2) return false;
  const approval = approvals.find(row => row.approvalId === card.currentApprovalId);
  if (!approval || approval.policyVersion > (card.workflowPolicyVersion ?? 1) || approval.cardId !== card.cardId || approval.reviewedCardRevision !== card.revision - 1 || !actors.some(actor => actor.actorId === approval.actorId && actor.kind === "human")) return false;
  try {
    const issuing = workflowAtVersion(current, policies, approval.policyVersion);
    return issuing === undefined || issuing.actorRights.human.includes("approve") && issuing.transitions.some(edge => laneRole(edge.from, issuing) === "review" && laneRole(edge.to, issuing) === "completed" && edge.actors.length === 1 && edge.actors[0] === "human");
  } catch { return false; }
}

export function validateCurrentWorkflowCards(cards: readonly Card[], current: Workflow | undefined, policies: readonly WorkflowPolicy[] | undefined, actors: readonly Actor[], approvals: readonly Approval[]): void {
  for (const card of cards) {
    if (!laneRole(card.lane, workflowAtVersion(current, policies, card.workflowPolicyVersion))) throw Error("Card policy unavailable");
    if (current && (laneRole(card.lane, current) === "completed" ? !currentApprovalValid(card, current, policies, actors, approvals) : card.currentApprovalId !== null)) throw Error("Current workflow approval unavailable");
  }
}


// A preview is an observation of one exact revision. These cross-field checks
// reject contradictory server data before a caller can present it as a plan.
export function validateWorkflowPreview(preview: WorkflowPreview): void {
  const current = preview.currentWorkflow, candidate = preview.candidate;
  const version = current?.revision ?? 1;
  if (preview.policyVersion !== version || candidate.revision !== version + 1 || current && (candidate.workflowId !== current.workflowId || candidate.profile !== current.profile) || !current && candidate.profile !== "default") throw Error("Workflow preview version mismatch");
  const currentLanes = workflowLanes(current), before = new Map(currentLanes.map(lane => [lane.id, lane])), after = new Map(candidate.lanes.map(lane => [lane.id, lane]));
  const edgeFacts = (workflow: Workflow | undefined): unknown => workflow ? workflow.transitions.map(edge => [edge.from, edge.to, edge.actors]) : [["In Progress", "Ready for Review", ["automation", "human"]], ["Ready for Review", "Completed", ["human"]], ["Ready for Work", "In Progress", ["automation", "human"]]];
  const ordinary = ["attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"];
  const rights = current?.actorRights ?? { human: ["approve", ...ordinary], automation: ordinary };
  const same = (a: unknown, b: unknown): boolean => JSON.stringify(a) === JSON.stringify(b);
  const laneFacts = (lanes: readonly WorkflowLane[]): unknown => lanes.map(lane => [lane.id, lane.label, lane.role]);
  const renamed = currentLanes.filter(lane => after.has(lane.id) && after.get(lane.id)!.label !== lane.label).sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0).map(lane => [lane.id, lane.label, after.get(lane.id)!.label]);
  const added = candidate.lanes.filter(lane => !before.has(lane.id)), retired = currentLanes.filter(lane => !after.has(lane.id));
  const orderChanged = !same(currentLanes.map(lane => lane.id), candidate.lanes.map(lane => lane.id));
  const transitionsChanged = !same(edgeFacts(current), edgeFacts(candidate));
  const rightChanges = (kind: "human" | "automation"): { added: string[]; removed: string[] } => ({ added: candidate.actorRights[kind].filter(right => !rights[kind].includes(right)), removed: rights[kind].filter(right => !candidate.actorRights[kind].includes(right)) });
  const human = rightChanges("human"), automation = rightChanges("automation"), changes = preview.changes;
  if (!same(changes.renamed.map(row => [row.laneId, row.before, row.after]), renamed) || changes.orderChanged !== orderChanged || !same(laneFacts(changes.added), laneFacts(added)) || !same(laneFacts(changes.retired), laneFacts(retired)) || changes.transitionsChanged !== transitionsChanged || !same(changes.rights.human.added, human.added) || !same(changes.rights.human.removed, human.removed) || !same(changes.rights.automation.added, automation.added) || !same(changes.rights.automation.removed, automation.removed)) throw Error("Workflow preview changes mismatch");
  const changed = !same(laneFacts(currentLanes), laneFacts(candidate.lanes)) || transitionsChanged || human.added.length > 0 || human.removed.length > 0 || automation.added.length > 0 || automation.removed.length > 0;
  if (preview.wouldChange !== changed || preview.nextPolicyVersion !== version + (changed ? 1 : 0)) throw Error("Workflow preview progression mismatch");
  if (preview.occupancy.length !== currentLanes.length) throw Error("Workflow preview occupancy incomplete");
  for (const [index, row] of preview.occupancy.entries()) if (!before.has(row.laneId) || row.archivedCards > row.currentCards || index > 0 && preview.occupancy[index - 1]!.laneId >= row.laneId) throw Error("Workflow preview occupancy mismatch");
  const capacity = preview.capacity;
  if (capacity.policyRecords !== version || capacity.policyRecordsAfter !== version + (changed ? 1 : 0) || capacity.policyRecordsAfter > 128 || capacity.canonicalPolicyBytesAfter < 1 || capacity.canonicalPolicyBytesAfter > 8388608 || capacity.maxPolicyRecords !== 128 || capacity.maxCanonicalPolicyBytes !== 8388608 || preview.approval.rule !== "retained-issuing-policy") throw Error("Workflow preview capacity mismatch");
}
