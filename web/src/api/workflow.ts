import type { Workflow, WorkflowLane } from "../../generated/archive/contracts.js";
import type { SessionRead } from "../../generated/transport/contracts.js";

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
  const project = (w: Workflow) => [w.workflowId, w.revision, w.lanes.map(lane => [lane.id, lane.label, lane.role]), w.transitions.map(edge => [edge.from, edge.to, edge.actors]), w.actorRights.human, w.actorRights.automation];
  return a === undefined || b === undefined ? a === b : JSON.stringify(project(a)) === JSON.stringify(project(b));
}
