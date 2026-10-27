import type { BoardRead } from "../../generated/transport/contracts.js";
import type { Card } from "../../generated/archive/contracts.js";

export type DependencyAdvisory = {
  pressure: string[];
  prerequisites: string[];
  summary: string;
};

function cardFact(card: Card): string {
  return "#" + card.ticket + " " + card.title + " (" + card.priority + "; " + card.lane + (card.archived ? "; archived" : "") + ")";
}

// A current, complete validated board read is required. This derives presentation
// only: current start admission and all authority remain on the server.
export function dependencyAdvisory(read: BoardRead, card: Card): DependencyAdvisory {
  const board = read.result;
  const currentTarget = (target: { boardId: string; incarnation: string }): boolean =>
    target.boardId === board.identity.boardId && target.incarnation === board.identity.incarnation;
  const links = board.links.filter(link => link.active && link.kind === "blocking");
  const pressure = links.filter(link => currentTarget(link.target) && link.target.cardId === card.cardId)
    .flatMap(link => {
      const dependent = board.cards.find(candidate => candidate.cardId === link.cardId);
      return dependent ? ["Dependency pressure: " + cardFact(dependent)] : [];
    });
  let satisfied = true;
  const prerequisites = links.filter(link => link.cardId === card.cardId).map(link => {
    const target = currentTarget(link.target) ? board.cards.find(candidate => candidate.cardId === link.target.cardId) : undefined;
    if (!target) {
      satisfied = false;
      return "Prerequisites/readiness: " + link.target.boardId + "/" + link.target.incarnation + "/" + link.target.cardId + " — unavailable or foreign target (not satisfied)";
    }
    const approval = board.approvals.find(candidate => candidate.approvalId === target.currentApprovalId && candidate.cardId === target.cardId);
    const human = approval && board.actors.find(actor => actor.actorId === approval.actorId && actor.kind === "human");
    const accepted = !target.archived && target.lane === "Completed" && approval !== undefined && human !== undefined &&
      approval.policyVersion === board.policyVersion && approval.reviewedCardRevision === target.revision - 1;
    satisfied = satisfied && accepted;
    return "Prerequisites/readiness: " + cardFact(target) + (accepted ? " — satisfied" : " — awaiting human acceptance (not satisfied)");
  });
  return {
    pressure: pressure.length ? pressure : ["Dependency pressure: none."],
    prerequisites: prerequisites.length ? prerequisites : ["Prerequisites/readiness: none."],
    summary: (prerequisites.length ? satisfied ? "Prerequisites satisfied" : "Prerequisites not satisfied" : "No prerequisites") +
      " (advisory; server rechecks at deliberate start).",
  };
}
