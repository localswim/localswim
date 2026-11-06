// Synthetic DOM/recovery checks. Real service and native CLI evidence is separate.
import type { BoardRead, SessionRead } from "../../generated/transport/contracts.js";
import { mountSyntheticBoard } from "./view.test.js";
import { configuredWorkflowFixture } from "./workflow.test.js";
import { fileFixture } from "../api/files.test.js";

async function runDetachMutationProbe(input: BoardRead, session: SessionRead): Promise<number> {
  const board = structuredClone(input), card = board.result.cards[0]!, attachment = fileFixture().attachment;
  Object.assign(attachment, { cardId: card.cardId, createdIn: board.result.identity });
  Object.assign(card, { attachmentIds: [attachment.attachmentId], lane: "Ready for Work" });
  Object.assign(board.result, { attachments: [attachment] });
  const mounted = await mountSyntheticBoard(board, session);
  let checks = 0;
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  const button = (label: string): HTMLButtonElement => [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
  const settle = async (ready: () => boolean): Promise<void> => { for (let i = 0; i < 250; i++) { if (ready()) return; await new Promise(resolve => setTimeout(resolve, 10)); } throw Error("Detach probe did not settle"); };
  try {
    document.querySelector<HTMLButtonElement>('button[data-open-card="' + card.cardId + '"]')!.click();
    await settle(() => !!button("Inspect file " + attachment.attachmentId));
    button("Inspect file " + attachment.attachmentId).click(); await settle(() => !!button("Detach file"));
    button("Detach file").click(); await mounted.view.flushPrivateInput();
    const confirmation = document.querySelector('[aria-label="Detach file confirmation"]')!;
    check(!!confirmation && confirmation.textContent!.includes(card.cardId) && confirmation.textContent!.includes(attachment.attachmentId) && confirmation.textContent!.includes(attachment.name) && confirmation.textContent!.includes(board.result.identity.incarnation), "Exact detach confirmation missing");
    check(confirmation.textContent!.includes("Ordinary download becomes unavailable after detach."), "Detach confirmation omitted ordinary download consequence");
    check(mounted.state.mutations === 0, "Opening detach confirmation submitted");
    button("Cancel detach file").click(); check(!document.querySelector('[aria-label="Detach file confirmation"]') && mounted.state.mutations === 0, "Cancel submitted or retained confirmation");
    button("Detach file").click(); await mounted.view.flushPrivateInput();
    mounted.state.mode = "late"; button("Confirm detach file").click();
    await settle(() => mounted.state.mutations === 1 && mounted.state.releases.length === 1);
    const wire = JSON.parse(mounted.state.wire[0]!);
    check(wire.target.cardId === card.cardId && wire.expectedRevision === board.result.revision && wire.expectedCardRevision === card.revision && wire.input.attachmentId === attachment.attachmentId && Object.keys(wire.input).length === 1, "Detach changed captured preconditions/input");
    button("Stop waiting").click(); button("Confirm detach file").click();
    check(mounted.state.mutations === 1 && !!button("Look up original outcome"), "Unknown detach replayed or lost original lookup");
    mounted.state.releases.splice(0).forEach(release => release());
    await settle(() => document.body.textContent!.includes("Confirmed saved"));
    check(mounted.state.mutations === 1 && !document.querySelector('[aria-label="Card files"] article'), "Commit replayed or kept current file rows fresh");
    return checks;
  } finally { mounted.view.destroy(); mounted.client.close(); }
}

export async function runDetachEligibilityProbe(input: BoardRead, session: SessionRead): Promise<number> {
  let checks = 0;
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  const settle = async (scenario: string, phase: string, ready: () => boolean): Promise<void> => { for (let i = 0; i < 250; i++) { if (ready()) return; await new Promise(resolve => setTimeout(resolve, 10)); } throw Error("Detach eligibility " + scenario + " " + phase + " did not settle"); };
  for (const scenario of ["read-only", "attach-ceiling", "completed-default", "completed-renamed", "archived-unfinished"]) {
    const board = structuredClone(input), card = board.result.cards[0]!, attachment = fileFixture().attachment;
    Object.assign(attachment, { cardId: card.cardId, createdIn: board.result.identity });
    Object.assign(board.result, { attachments: [attachment] });
    Object.assign(card, { attachmentIds: [attachment.attachmentId], lane: "Ready for Work", archived: false });
    if (scenario === "attach-ceiling" || scenario === "completed-renamed") {
      const workflow = configuredWorkflowFixture();
      if (scenario === "attach-ceiling") Object.assign(workflow.actorRights, { [session.kind]: workflow.actorRights[session.kind].filter(right => right !== "attach") });
      Object.assign(board.result, { workflow });
      for (const row of board.result.cards) Object.assign(row, { lane: workflow.lanes.find(lane => lane.role === "initial")!.id });
    }
    const mounted = await mountSyntheticBoard(board, session);
    const button = (label: string): HTMLButtonElement => [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
    try {
      button("Rename card").click();
      const dirty = document.querySelector<HTMLInputElement>('input[name="title"]')!;
      dirty.value = "Retain unrelated private input"; dirty.dispatchEvent(new Event("input", { bubbles: true }));
      await mounted.view.flushPrivateInput();
      const currentCard = mounted.board.result.cards[0]!;
      if (scenario === "read-only") Object.assign(mounted.auth.grant, { rights: ["read", "read-own-outcome"] });
      if (scenario === "completed-renamed") {
        const workflow = mounted.board.result.workflow!, approvalId = crypto.randomUUID();
        const actor = mounted.board.result.actors.find(row => row.kind === "human")!;
        const reviewedCardRevision = currentCard.revision, reviewedBoardRevision = mounted.board.result.revision;
        Object.assign(currentCard, { lane: workflow.lanes.find(lane => lane.role === "completed")!.id, revision: reviewedCardRevision + 1, currentApprovalId: approvalId });
        Object.assign(mounted.board.result, { revision: reviewedBoardRevision + 1, approvals: [{ approvalId, actorId: actor.actorId, cardId: currentCard.cardId, createdAt: "2026-11-05T00:00:00Z", createdIn: mounted.board.result.identity, policyVersion: 1, reviewedCardRevision, reviewedBoardRevision }] });
      }
      if (scenario === "completed-default") Object.assign(currentCard, { lane: "Completed" });
      if (scenario === "archived-unfinished") Object.assign(currentCard, { archived: true });
      await mounted.view.refresh();
      if (scenario === "archived-unfinished") {
        const archive = document.querySelector<HTMLSelectElement>('[aria-label="Archive status"]')!;
        archive.value = "archived"; archive.dispatchEvent(new Event("change", { bubbles: true }));
      }
      document.querySelector<HTMLButtonElement>('button[data-open-card="' + card.cardId + '"]')!.click();
      await settle(scenario, "file-list", () => !!button("Inspect file " + attachment.attachmentId));
      button("Inspect file " + attachment.attachmentId).click(); await settle(scenario, "file-inspect", () => !!button("Detach file"));
      const detach = button("Detach file");
      check(detach.disabled === (scenario !== "archived-unfinished"), scenario + " detach eligibility mismatch");
      if (scenario !== "archived-unfinished") {
        // A synthetic click also verifies the callback guard behind disabled UI.
        detach.dispatchEvent(new MouseEvent("click", { bubbles: true }));
        check(!document.querySelector('[aria-label="Detach file confirmation"]') && !button("Confirm discard editor input") && mounted.state.mutations === 0, scenario + " opened destructive replacement or dispatched");
      }
      check(document.querySelector('input[name="title"]') === dirty && dirty.value === "Retain unrelated private input", scenario + " replaced private editor input");
    } finally { mounted.view.destroy(); mounted.client.close(); }
  }
  return checks;
}

export async function runDetachEditorProbe(input: BoardRead, session: SessionRead): Promise<number> {
  return await runDetachMutationProbe(input, session) + await runDetachEligibilityProbe(input, session);
}
