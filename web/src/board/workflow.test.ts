import type { Workflow } from "../../generated/archive/contracts.js";
import type { BoardRead, CardSearchRead, SessionRead } from "../../generated/transport/contracts.js";
import { completedLane, laneText, moveLanes, sameWorkflow, workflowEdge, workflowLanes, workflowRight } from "../api/workflow.js";
import { emptySearch } from "../api/search.js";
import type { BoardClient } from "../api/client.js";
import { CardSearch, validPrivateSearch } from "./search.js";
import { dependencyAdvisory } from "./advisory.js";
import { CardDetails } from "./details.js";
import { CardHierarchy } from "./hierarchy.js";
import type { CardRead, HistoryRead } from "../../generated/transport/contracts.js";
import { mountSyntheticBoard } from "./view.test.js";

export const configuredWorkflowFixture = (): Workflow => {
  const ids = [1, 2, 3, 4, 5].map(n => "10000000-0000-4000-8000-" + String(n).padStart(12, "0"));
  return { workflowId: "10000000-0000-4000-8000-000000000090", revision: 1,
    lanes: [
      { id: ids[4]!, label: "Parked", role: "waiting" }, { id: ids[0]!, label: "Intake", role: "initial" },
      { id: ids[1]!, label: "Doing", role: "in_progress" }, { id: ids[2]!, label: "Check", role: "review" }, { id: ids[3]!, label: "Accepted", role: "completed" }],
    transitions: [[0, 1], [0, 4], [1, 2], [1, 4], [2, 3], [4, 1]].map(([from, to]) => ({ from: ids[from!]!, to: ids[to!]!, actors: to === 3 ? ["human"] : ["automation", "human"] })),
    actorRights: { human: ["approve", "attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"], automation: ["attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"] } };
};

// Controlled DOM/private-pipeline evidence only; server qualification is separate.
export async function runConfiguredWorkflowProbe(input: BoardRead, session: SessionRead): Promise<number> {
  let count = 0;
  const check = (ok: unknown, message: string): void => { if (!ok) throw Error(message); count++; };
  const workflow = configuredWorkflowFixture(), lane = (role: string) => workflow.lanes.find(row => row.role === role)!.id;
  const human = { ...session, kind: "human" as const, grant: { ...session.grant, rights: workflow.actorRights.human as SessionRead["grant"]["rights"] } };
  const bot = { ...human, kind: "automation" as const };
  check(workflowLanes(workflow)[0]!.role === "waiting" && laneText(lane("completed"), workflow) === "Accepted (completed)", "Configured order/label/role lost");
  check(completedLane(lane("completed"), workflow) && !completedLane("Completed", workflow) && completedLane("Completed"), "Completed guessed from label/profile");
  check(!workflowRight(bot, "approve", workflow) && !workflowEdge(lane("review"), lane("completed"), bot, "approve", workflow), "Automation approval afforded");
  check(moveLanes(lane("initial"), human, workflow).map(row => row.role).join() === "waiting,in_progress", "Generic edge choices ignored lane order or role");
  check(!moveLanes(lane("review"), human, workflow).length, "Review/completed offered as generic targets");
  check(sameWorkflow(workflow, JSON.parse(JSON.stringify(workflow))) && !sameWorkflow(workflow, { ...workflow, lanes: [...workflow.lanes].reverse() }), "Complete definition matching ignored order");
  const board = structuredClone(input);
  Object.assign(board.result, { workflow });
  const oldRoles = ["Ready for Work", "In Progress", "Ready for Review", "Completed"];
  for (const card of board.result.cards) Object.assign(card, { lane: workflow.lanes.find(row => row.role === ["initial", "in_progress", "review", "completed"][oldRoles.indexOf(card.lane)])?.id ?? lane("initial") });
  const card = board.result.cards[0]!;
  Object.assign(card, { lane: lane("initial"), archived: false, currentApprovalId: null });
  check(dependencyAdvisory(board, card).summary.includes("advisory"), "Configured advisory unavailable");
  const mounted = await mountSyntheticBoard(board, human);
  try {
    const buttons = () => [...document.querySelectorAll<HTMLButtonElement>("button")];
    check(document.body.textContent!.includes("Parked (waiting)") && document.body.textContent!.includes("Accepted (completed)"), "Empty configured lanes not rendered");
    buttons().find(row => row.textContent === "Move card")!.click();
    await new Promise(resolve => setTimeout(resolve, 50));
    const select = document.querySelector<HTMLSelectElement>('[aria-label="laneId"]')!;
    check(select && [...select.options].map(row => row.textContent).join() === "Parked (waiting),Doing (in_progress)", "Move choices do not use complete policy");
    select.value = lane("waiting"); select.dispatchEvent(new Event("input", { bubbles: true }));
    await new Promise(resolve => setTimeout(resolve, 50));
    const save = buttons().find(row => row.textContent === "Move card" && row.closest("form"))!;
    for (let i = 0; save.disabled && i < 100; i++) await new Promise(resolve => setTimeout(resolve, 20));
    check(!save.disabled, "Move private draft did not become ready"); save.click();
    for (let i = 0; !mounted.state.wire.length && i < 100; i++) await new Promise(resolve => setTimeout(resolve, 20));
    const request = JSON.parse(mounted.state.wire[0]!);
    check(Object.keys(request.input).join() === "laneId" && request.input.laneId === lane("waiting"), "Move mixed private input or changed exact UUID");
  } finally { mounted.view.destroy(); mounted.client.close(); }
  const archive = document.createElement("select");
  for (const value of ["active", "archived", "all"]) { const option = document.createElement("option"); option.value = value; archive.append(option); }
  let calls = 0;
  const fake = { search: async (_: string, query: ReturnType<typeof emptySearch>): Promise<CardSearchRead> => { calls++; return { apiVersion: 1, scope: "card-search", result: { identity: board.result.identity, revision: board.result.revision, workflow, query, items: [], nextCursor: null } }; } } as unknown as BoardClient;
  const privateSearch = { draft: { ...emptySearch(), lane: lane("waiting") }, applied: { ...emptySearch(), lane: lane("waiting") } };
  check(validPrivateSearch(privateSearch), "UUID private filter not accepted");
  const search = new CardSearch(fake, board.result.identity.boardId, archive, () => {}, undefined, privateSearch);
  document.body.replaceChildren(search.host); search.boardRead(board.result.identity, board.result.revision, workflow);
  await new Promise(resolve => setTimeout(resolve, 0));
  check(calls === 1 && document.querySelector<HTMLSelectElement>('[aria-label="Lane"]')!.value === lane("waiting"), "Restored UUID filter silently became Any");
  search.suspend();
  const foreign = { draft: { ...emptySearch(), lane: "10000000-0000-4000-8000-000000000099" }, applied: { ...emptySearch(), lane: "10000000-0000-4000-8000-000000000099" } };
  const rejected = new CardSearch(fake, board.result.identity.boardId, archive, () => {}, undefined, foreign);
  document.body.replaceChildren(rejected.host); rejected.boardRead(board.result.identity, board.result.revision, workflow);
  await new Promise(resolve => setTimeout(resolve, 0));
  check(calls === 1 && document.body.textContent!.includes("Invalid filters"), "Foreign UUID dispatched even with empty results"); rejected.suspend();
  count += await runConfiguredReadLabelsProbe(board);
  return count;
}

// Exercise actual row text, not just board headers, using each read's policy.
export async function runConfiguredReadLabelsProbe(input: BoardRead): Promise<number> {
  const read = structuredClone(input), workflow = configuredWorkflowFixture();
  const waiting = workflow.lanes.find(row => row.role === "waiting")!.id;
  const doing = workflow.lanes.find(row => row.role === "in_progress")!.id;
  const identity = read.result.identity, template = read.result.cards[0]!;
  const root = { ...template, cardId: "10000000-0000-4000-8000-000000000061", ticket: 61, lane: doing, revision: 3, currentApprovalId: null, commentIds: [], linkIds: [], attachmentIds: [] };
  delete (root as { parent?: unknown }).parent;
  const child = { ...root, cardId: "10000000-0000-4000-8000-000000000062", ticket: 62, lane: waiting, parent: { ...identity, cardId: root.cardId } };
  const grandchild = { ...child, cardId: "10000000-0000-4000-8000-000000000063", ticket: 63, parent: { ...identity, cardId: child.cardId } };
  Object.assign(read.result, { workflow, cards: [root, child, grandchild], comments: [], links: [], attachments: [], approvals: [] });
  const hierarchy = new CardHierarchy({ board: async () => read }, () => {});
  document.body.replaceChildren(hierarchy.host); hierarchy.boardRead(read); hierarchy.open(root.cardId, identity);
  let checks = 0;
  const check = (ok: unknown, message: string): void => { if (!ok) throw Error(message); checks++; };
  const rows = [...hierarchy.host.querySelectorAll("article")];
  check(rows.length === 2 && rows.every(row => row.textContent!.includes("Parked (waiting)") && !row.textContent!.includes(waiting)), "Child/descendant rows leaked raw lane UUID or omitted role");
  hierarchy.suspend();
  const foreign = { ...identity, incarnation: "10000000-0000-4000-8000-000000000064" };
  const historicalParent = { ...foreign, cardId: grandchild.cardId };
  const card: CardRead = { apiVersion: 1, scope: "card", result: { ...read.result, card: root, historyReferences: [] } };
  const history: HistoryRead = { apiVersion: 1, scope: "history", result: { ...read.result, cardId: root.cardId, cardRevision: root.revision,
    records: [{ actorId: read.result.actors[0]!.actorId, cardId: root.cardId, cardRevision: 1, committedAt: "2026-11-04T00:00:00Z", origin: foreign,
      state: { ...root, lane: waiting, parent: historicalParent }, removal: null,
      references: { commentIds: [], linkIds: [], attachmentIds: [], approvalIds: [] } }], tombstones: [] } };
  const fallback = document.createElement("button"), details = new CardDetails({ card: async () => card, history: async () => history }, fallback);
  document.body.replaceChildren(fallback, details.host); details.open(root.cardId, identity, fallback);
  for (let i = 0; i < 50 && !details.host.querySelector('[aria-label="Prior card history"] article'); i++) await new Promise(resolve => setTimeout(resolve, 10));
  const historicalRow = details.host.querySelector('[aria-label="Prior card history"] article');
  check(historicalRow?.textContent!.includes("Parked (waiting)"), "Historical row omitted retained-policy label or role");
  check(historicalRow?.textContent!.includes("Recorded workflow policy 1 · lane ID " + waiting), "Historical row omitted explicit policy/identity provenance");
  check(historicalRow?.textContent!.includes(foreign.incarnation) && historicalRow.textContent!.includes("unresolved/unavailable in this history view"), "Historical origin/parent was resolved through current identity");
  details.suspend();
  return checks;
}
