import type { BoardClient } from "../api/client.js";
import { WorkflowPreviewRefusal } from "../api/client.js";
import type { BoardRead, SessionRead, WorkflowPreview, WorkflowPreviewRequest } from "../../generated/transport/contracts.js";
import { resolvedAttention, workflowLanes } from "../api/workflow.js";
import { BoardView } from "./view.js";

// Controlled BoardView entry-point race. The private store completion is delayed
// until after an authoritative preview refusal, using a synthetic client only.
export async function runWorkflowPreviewPersistenceRace(): Promise<number> {
  const identity = { boardId: "10000000-0000-4000-8000-000000000001", incarnation: "10000000-0000-4000-8000-000000000002" };
  const actorId = "10000000-0000-4000-8000-000000000003";
  const context = { ...identity, actorId, installationId: "10000000-0000-4000-8000-000000000004" };
  const board = { apiVersion: 1, scope: "board", result: { identity, revision: 1, policyVersion: 1, cards: [], actors: [], comments: [], links: [], attachments: [], approvals: [] } } as unknown as BoardRead;
  const session = { actorId, kind: "human", grant: { ...identity, rights: ["read", "edit", "create"] } } as unknown as SessionRead;
  let delayed = false, refuse = true, release: (() => void) | undefined;
  const client = {
    recoveryDomainKey: "workflow-preview-race-" + crypto.randomUUID(),
    session: async () => session, board: async () => board, context: () => context,
    recovery: { saveDraft: async (request: { draftId: string; expectedRevision: number }) => {
      if (delayed) await new Promise<void>(resolve => { release = resolve; });
      return { state: "saved", draft: { draftId: request.draftId, revision: request.expectedRevision + 1 } };
    } },
    previewWorkflow: async (request: WorkflowPreviewRequest): Promise<WorkflowPreview> => {
      if (refuse) throw new WorkflowPreviewRefusal("REVISION_CONFLICT");
      const before = workflowLanes(), after = request.workflow.lanes;
      return { apiVersion: 1, ...identity, revision: 1, policyVersion: 1, candidate: request.workflow, wouldChange: true, nextPolicyVersion: 2,
        changes: { renamed: before.filter(lane => after.find(row => row.id === lane.id)!.label !== lane.label).map(lane => ({ laneId: lane.id, before: lane.label, after: after.find(row => row.id === lane.id)!.label })), orderChanged: false, added: [], retired: [], transitionsChanged: false,
          rights: { human: { added: [], removed: [] }, automation: { added: [], removed: [] } }, attention: { beforeSource: "implicit", afterSource: "explicit", before: resolvedAttention(), after: resolvedAttention(request.workflow), changed: true } },
        occupancy: before.map(lane => ({ laneId: lane.id, currentCards: 0, archivedCards: 0 })),
        currentAttention: { blocked: { state: "not_configured", value: null }, needsTerry: { state: "not_configured", value: null }, readyForReview: { state: "known", value: 0 } },
        candidateAttention: { blocked: { state: "not_configured", value: null }, needsTerry: { state: "not_configured", value: null }, readyForReview: { state: "known", value: 0 } },
        approval: { rule: "retained-issuing-policy", preservedCurrentCount: 0 }, capacity: { policyRecords: 1, policyRecordsAfter: 2, canonicalPolicyBytesAfter: 1000, maxPolicyRecords: 128, maxCanonicalPolicyBytes: 8388608 } };
    },
  } as unknown as BoardClient;
  const root = document.createElement("main"); document.body.append(root);
  const view = new BoardView(root, client, identity.boardId);
  let checks = 0;
  const assert = (value: unknown, message: string): void => { if (!value) throw Error(message); checks++; };
  const button = (name: string): HTMLButtonElement => [...root.querySelectorAll("button")].find(row => row.textContent === name)!;
  const messages = (): string => [...root.querySelectorAll('[role="status"]')].map(row => row.textContent).join("\n");
  const settle = async (test: () => boolean): Promise<void> => {
    for (let count = 0; count < 100; count++) { if (test()) return; await new Promise(resolve => setTimeout(resolve, 10)); }
    throw Error("Preview persistence race did not reach its controlled state");
  };
  try {
    await view.open(); button("Edit workflow").click(); await view.flushPrivateInput();
    button("Use explicit Attention assignments").click(); await view.flushPrivateInput();
    delayed = true; button("Preview workflow").click();
    await settle(() => !!release && messages().includes("REVISION_CONFLICT"));
    assert(button("Save workflow").disabled, "Refused preview enabled Save");
    release!(); await view.flushPrivateInput();
    assert(messages().includes("REVISION_CONFLICT") && messages().includes("Input and original revision retained"), "Private persistence completion erased authoritative preview refusal");
    assert(root.querySelector<HTMLSelectElement>('[aria-label="Attention category for lane Ready for Review"]')!.value === "readyForReview", "Refusal changed retained category input");
    delayed = false;
    const label = root.querySelector<HTMLInputElement>('[aria-label="Lane 1 label"]')!;
    label.value = "Changed after refusal"; label.dispatchEvent(new Event("input", { bubbles: true }));
    await view.flushPrivateInput();
    assert(!messages().includes("REVISION_CONFLICT"), "Later input retained obsolete preview refusal");
    refuse = false; button("Preview workflow").click(); await view.flushPrivateInput();
    await settle(() => !button("Save workflow").disabled);
    assert(!messages().includes("REVISION_CONFLICT") && root.textContent!.includes("Review before Save"), "New valid preview retained obsolete refusal");
    return checks;
  } finally { release?.(); view.destroy(); root.remove(); }
}
