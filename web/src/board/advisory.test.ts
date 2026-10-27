import type { BoardRead } from "../../generated/transport/contracts.js";
import type { Approval, Card, Link } from "../../generated/archive/contracts.js";
import { dependencyAdvisory } from "./advisory.js";

// Synthetic derivation assertions, never product qualification or admission proof.
export function runDependencyAdvisoryProbe(boardInput: BoardRead): number {
  const read = structuredClone(boardInput), board = read.result;
  const alpha: Card = { ...board.cards[0]!, cardId: "alpha", ticket: 1, title: "Alpha", priority: "P3", lane: "Ready for Review", archived: false, currentApprovalId: null, revision: 4 };
  const beta: Card = { ...alpha, cardId: "beta", ticket: 2, title: "Beta", priority: "P0", lane: "Ready for Work" };
  const link: Link = { active: true, cardId: beta.cardId, target: { ...board.identity, cardId: alpha.cardId }, kind: "blocking", linkId: "link", createdIn: board.identity, createdAt: "2026-10-01T00:00:00Z" };
  const human = { ...board.actors[0]!, actorId: "human", kind: "human" as const };
  const approval: Approval = { approvalId: "approval", cardId: alpha.cardId, actorId: human.actorId, policyVersion: board.policyVersion, reviewedCardRevision: 4, reviewedBoardRevision: board.revision, createdIn: board.identity, createdAt: "2026-10-01T00:00:00Z" };
  Object.assign(board, { cards: [alpha, beta], links: [link], actors: [human], approvals: [] });
  let checks = 0;
  const check = (condition: boolean, message: string): void => { if (!condition) throw Error(message); checks++; };
  const before = JSON.stringify(read);
  check(dependencyAdvisory(read, alpha).pressure[0] === "Dependency pressure: #2 Beta (P0; Ready for Work)", "Incoming direct P0 pressure missing");
  check(dependencyAdvisory(read, beta).prerequisites[0]!.includes("#1 Alpha (P3; Ready for Review) — awaiting human acceptance (not satisfied)"), "RFR must await acceptance");
  check(JSON.stringify(read) === before && alpha.priority === "P3", "Derivation mutated the read");
  Object.assign(alpha, { lane: "Completed", currentApprovalId: approval.approvalId, revision: 5 });
  Object.assign(board, { approvals: [approval] });
  check(dependencyAdvisory(read, beta).summary.startsWith("Prerequisites satisfied"), "Current human acceptance missing");
  const reject = (change: object, entity: object, label: string): void => {
    const saved = structuredClone(entity); Object.assign(entity, change);
    check(dependencyAdvisory(read, beta).summary.startsWith("Prerequisites not satisfied"), label);
    Object.assign(entity, saved);
  };
  reject({ archived: true }, alpha, "Archived target accepted");
  reject({ currentApprovalId: null }, alpha, "Missing current approval accepted");
  reject({ lane: "Ready for Review" }, alpha, "RFR accepted with old approval");
  reject({ cardId: "other" }, approval, "Wrong-card approval accepted");
  reject({ policyVersion: board.policyVersion + 1 }, approval, "Wrong policy accepted");
  reject({ reviewedCardRevision: 3 }, approval, "Old revision accepted");
  reject({ actorId: "missing" }, approval, "Missing actor accepted");
  reject({ kind: "automation" }, human, "Automation approval accepted");
  for (const target of [{ ...link.target, boardId: "foreign" }, { ...link.target, incarnation: "old" }, { ...link.target, cardId: "missing" }]) {
    const saved = link.target; Object.assign(link, { target });
    check(dependencyAdvisory(read, beta).prerequisites[0]!.includes("unavailable or foreign target (not satisfied)"), "Target substituted by UUID");
    check(dependencyAdvisory(read, alpha).pressure[0] === "Dependency pressure: none.", "Foreign target adds pressure");
    Object.assign(link, { target: saved });
  }
  for (const change of [{ active: false }, { kind: "informational" }]) {
    Object.assign(link, change);
    check(dependencyAdvisory(read, alpha).pressure[0] === "Dependency pressure: none." && dependencyAdvisory(read, beta).prerequisites[0] === "Prerequisites/readiness: none.", "Inactive/informational link contributes");
    Object.assign(link, { active: true, kind: "blocking" });
  }
  Object.assign(board, { links: [] });
  check(dependencyAdvisory(read, alpha).pressure[0] === "Dependency pressure: none." && alpha.priority === "P3", "Unlink changed priority or retained pressure");
  return checks;
}
