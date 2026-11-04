// Presentation/advisory only. Callers supply the complete definition from the
// same validated read; these helpers never authorize or manufacture policy.
const defaults = [
    { id: "Ready for Work", label: "Ready for Work", role: "initial" },
    { id: "In Progress", label: "In Progress", role: "in_progress" },
    { id: "Ready for Review", label: "Ready for Review", role: "review" },
    { id: "Completed", label: "Completed", role: "completed" },
];
export const workflowLanes = (workflow) => workflow?.lanes ?? defaults;
export const laneRole = (id, workflow) => workflowLanes(workflow).find(lane => lane.id === id)?.role;
export const laneText = (id, workflow) => {
    const lane = workflowLanes(workflow).find(row => row.id === id);
    return lane ? lane.label + (workflow ? " (" + lane.role + ")" : "") : "Unavailable lane";
};
export const roleLane = (role, workflow) => workflowLanes(workflow).find(lane => lane.role === role)?.id;
export const completedLane = (id, workflow) => laneRole(id, workflow) === "completed";
export function workflowRight(session, right, workflow) {
    return session.grant.rights.includes(right) && (!workflow || workflow.actorRights[session.kind].includes(right));
}
export function workflowEdge(from, to, session, right, workflow) {
    if (!workflowRight(session, right, workflow))
        return false;
    if (!workflow)
        return (from === "Ready for Work" && to === "In Progress" || from === "In Progress" && to === "Ready for Review" || from === "Ready for Review" && to === "Completed") && (right !== "approve" || session.kind === "human");
    return workflow.transitions.some(edge => edge.from === from && edge.to === to && edge.actors.includes(session.kind)) && (right !== "approve" || session.kind === "human");
}
export function moveLanes(from, session, workflow) {
    return workflow.lanes.filter(lane => ["initial", "in_progress", "waiting"].includes(lane.role) && workflowEdge(from, lane.id, session, "edit", workflow));
}
export function sameWorkflow(a, b) {
    const project = (w) => [w.workflowId, w.revision, w.lanes.map(lane => [lane.id, lane.label, lane.role]), w.transitions.map(edge => [edge.from, edge.to, edge.actors]), w.actorRights.human, w.actorRights.automation];
    return a === undefined || b === undefined ? a === b : JSON.stringify(project(a)) === JSON.stringify(project(b));
}
