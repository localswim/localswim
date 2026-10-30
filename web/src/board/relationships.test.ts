// Synthetic DOM and recovery supplements; product transaction proof is separate.
import type { BoardRead, MutationRequest, SessionRead } from "../../generated/transport/contracts.js";
import { prepare, boardBoundaryVerifier, canonical } from "../api/boundary.js";
import { captureRelationships, completeOwnedRelationships, validRelationships, relationshipKinds } from "../api/relationships.js";
import { CardRelationships } from "./relationships.js";
import { BoardView } from "./view.js";
import { mountSyntheticBoard } from "./view.test.js";
const id = (value: number): string => "00000000-0000-4000-8000-" + String(value).padStart(12, "0");
const settle = async (test: () => boolean): Promise<void> => { for (let i = 0; i < 250; i++) { if (test()) return; await new Promise(resolve => setTimeout(resolve, 10)); } throw Error("Relationship component did not settle"); };
export function relationshipFixture(input: BoardRead, count = 24): BoardRead {
  const read = structuredClone(input), template = read.result.cards[0]!, identity = read.result.identity;
  Object.assign(read.result, { cards: Array.from({ length: count + 1 }, (_, index) => ({ ...template, cardId: id(index + 100), ticket: index + 1, title: "Relationship " + index, lane: "Ready for Work", ownerId: null, archived: index === 1, currentApprovalId: null, linkIds: [], commentIds: [], attachmentIds: [] })), links: [], comments: [], approvals: [], attachments: [] });
  for (let index = 1; index <= count; index++) {
    const owner = read.result.cards[index % 3 === 0 ? index : 0]!, peer = read.result.cards[index % 3 === 0 ? 0 : index]!, linkId = id(index + 500);
    const link = { linkId, cardId: owner.cardId, kind: relationshipKinds[(index - 1) % 5]!, target: { ...identity, cardId: peer.cardId }, active: true, createdIn: identity, createdAt: "2026-10-30T00:00:00Z" };
    (read.result.links as unknown as object[]).push(link); (owner.linkIds as string[]).push(linkId);
  }
  return read;
}
export async function runRelationshipBoundaryProbe(input: BoardRead): Promise<number> {
  const read = relationshipFixture(input), identity = read.result.identity, root = read.result.cards[0]!, snapshot = captureRelationships(read, root.cardId);
  let checks = 0; const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  check(snapshot.rows.length === 24 && snapshot.ownedCount === 16 && snapshot.incomingCount === 8, "Complete incident counts mismatch");
  check(snapshot.rows.every((row, index) => row.card?.ticket === index + 2), "Incident numeric ticket order mismatch");
  check(snapshot.rows.filter(row => !row.owned).every(row => row.owner.cardId === row.peer.cardId), "Incoming actual owner mismatch");
  for (const kind of relationshipKinds) { const owned = snapshot.rows.find(row => row.kind === kind && row.owned)!; const reciprocal = captureRelationships(read, owned.peer.cardId).rows.find(row => row.linkId === owned.linkId)!; check(!reciprocal.owned && reciprocal.owner.cardId === root.cardId && reciprocal.direction === (kind === "related" ? "symmetric" : "incoming"), "Reciprocal ownership mismatch"); }
  const foreign = structuredClone(read), foreignOwner = foreign.result.cards[2]!;
  (foreign.result.links as unknown as object[]).push({ linkId: id(9000), cardId: foreignOwner.cardId, kind: "reference", target: { ...identity, incarnation: id(9001), cardId: root.cardId }, active: true, createdIn: identity, createdAt: "2026-10-30T00:00:00Z" });
  check(captureRelationships(foreign, root.cardId).rows.length === 24, "Historical colliding UUID became a current incoming relationship");
  (foreign.result.links as unknown as object[]).push({ linkId: id(9002), cardId: root.cardId, kind: "reference", target: { ...identity, incarnation: id(9001), cardId: foreignOwner.cardId }, active: true, createdIn: identity, createdAt: "2026-10-30T00:00:00Z" });
  const unavailable = captureRelationships(foreign, root.cardId).rows.at(-1)!;
  check(!unavailable.card && unavailable.peer.incarnation === id(9001) && unavailable.owner.cardId === root.cardId, "Historical peer resolved through current label collision");
  Object.assign(read.result.cards[1]!, { title: "Changed caller" }); check(snapshot.rows[0]!.card?.title !== "Changed caller", "Relationship snapshot mutated");
  const context = { installationId: id(90), ...identity, actorId: input.result.actors[0]!.actorId }, base = { opId: id(91), incarnation: identity.incarnation, target: { cardId: root.cardId }, expectedRevision: 5, expectedCardRevision: 4 };
  const specs = completeOwnedRelationships(read, root.cardId).slice(0, 2);
  for (const command of ["card.create", "card.edit"] as const) {
    const wire = { ...base, target: command === "card.create" ? null : base.target, expectedCardRevision: command === "card.create" ? null : 4, input: { ...(command === "card.create" ? { title: "Created", description: "", parent: { ...identity, cardId: id(101) } } : {}), relationships: specs } } as MutationRequest;
    const evidence = await prepare(command, wire, context, id(92)), payload = JSON.parse(evidence.canonicalBytes).payload;
    check(canonical(payload.relationships) === canonical(specs) && await boardBoundaryVerifier.prepared(evidence, wire), "Relationship freeze/readback mismatch");
    const reversed = await prepare(command, { ...wire, input: { ...wire.input, relationships: [...specs].reverse() } } as MutationRequest, context, id(92));
    check(reversed.fingerprint !== evidence.fingerprint, "Array order rewritten");
    for (const relationships of [null, {}, [specs[0], specs[0]], Array.from({ length: 101 }, (_, index) => ({ kind: "reference", target: { ...identity, cardId: id(index + 2000) } }))]) {
      let refused = false; try { await prepare(command, { ...wire, input: { ...wire.input, relationships } } as MutationRequest, context, id(92)); } catch { refused = true; } check(refused, "Invalid relationship batch accepted");
    }
  }
  for (const input of [{ kind: "reference", targetCardId: id(101) }, { kind: "reference", target: { ...identity, cardId: id(101) }, targetCardId: null }, { kind: "blocking", targetCardId: id(101), target: null }, { kind: "reference", target: null }, { relationships: [], title: "Mixed" }]) {
    const command = Object.hasOwn(input, "relationships") ? "card.edit" : "link.add";
    let refused = false; try { await prepare(command, { ...base, input } as MutationRequest, context, id(92)); } catch { refused = true; } check(refused, "Invalid target counterpart or mixed replacement accepted");
  }
  const old = await prepare("link.add", { ...base, input: { kind: "blocking", targetCardId: id(101) } } as MutationRequest, context, id(92));
  check(!Object.hasOwn(JSON.parse(old.canonicalBytes).payload, "target"), "Legacy add gained target counterpart");
  const modern = await prepare("link.add", { ...base, input: { kind: "reference", target: { ...identity, cardId: id(101) } } } as MutationRequest, context, id(92));
  check(!Object.hasOwn(JSON.parse(modern.canonicalBytes).payload, "targetCardId"), "Modern add gained legacy counterpart");
  const legacy = await prepare("card.edit", { ...base, input: { title: "Old" } } as MutationRequest, context, id(92));
  check(!Object.hasOwn(JSON.parse(legacy.canonicalBytes).payload, "relationships"), "Legacy edit gained relationships");
  check(validRelationships(Array.from({ length: 100 }, (_, index) => ({ kind: "reference", target: { ...identity, cardId: id(index + 2000) } }))), "100 legal members refused");
  return checks;
}
export async function runRelationshipNavigationProbe(input: BoardRead): Promise<number> {
  const read = relationshipFixture(input), root = read.result.cards[0]!, identity = read.result.identity;
  let release: (() => void) | undefined, late = false, reads = 0, actions = 0;
  const component = new CardRelationships({ board: async () => { reads++; if (late) await new Promise<void>(resolve => release = resolve); return structuredClone(read); } }, () => {}, () => { actions++; });
  document.body.replaceChildren(component.host); component.boardRead(read); component.open(root.cardId, identity);
  let checks = 0; const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  const button = (label: string): HTMLButtonElement => [...component.host.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
  check(component.host.querySelectorAll("article").length === 20 && component.host.textContent!.includes("Owned: 16 · Incoming: 8"), "First local page/count mismatch");
  button("Next relationships").click(); check(component.host.querySelectorAll("article").length === 4 && reads === 0, "Local page fetched network data");
  check(component.host.querySelectorAll("article").length === 4 && [...component.host.querySelectorAll("article")].filter(row => row.textContent!.startsWith("Incoming")).every(row => ![...row.querySelectorAll("button")].some(button => button.textContent === "Remove owned relationship")), "Incoming remove exposed");
  await component.readCurrent(); check(component.host.querySelectorAll("article").length === 20 && component.host.textContent!.includes("Pages reset to 1"), "Explicit refresh did not reset page");
  button("Add relationship").click(); check(actions === 1, "Deliberate action missing");
  late = true; const pending = component.readCurrent(); await settle(() => !!release); component.open(read.result.cards[1]!.cardId, identity); release!(); await pending;
  check(component.host.querySelectorAll("article").length === 1, "Late prior selection painted current selection");
  const pending2 = component.readCurrent(); await new Promise(resolve => setTimeout(resolve, 0)); component.suspend(); release!(); await pending2;
  check(button("Add relationship").disabled, "Suspended stale component permits edit"); component.close(); return checks;
}
export async function runRelationshipEditorProbe(input: BoardRead, session: SessionRead): Promise<number> {
  const read = relationshipFixture(input, 153), mounted = await mountSyntheticBoard(read, session);
  let checks = 0; const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  const button = (label: string): HTMLButtonElement => [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
  try {
    document.querySelector<HTMLButtonElement>('button[data-open-card="' + read.result.cards[0]!.cardId + '"]')!.click(); await settle(() => !!document.querySelector('[aria-label="Card relationships"] button'));
    button("Replace owned relationships").click();
    const field = document.querySelector<HTMLTextAreaElement>('textarea[name="relationships"]')!;
    check(JSON.parse(field.value).length === 102 && field.hidden && document.querySelectorAll('[aria-label="Selected owned relationships"] article').length === 102, "Replacement dropped hidden owned rows or exposed JSON");
    button("Remove owned set entry 1").click();
    check(JSON.parse(field.value).length === 101 && JSON.parse(field.value)[0].target.cardId === read.result.links.filter(link => link.active && link.cardId === read.result.cards[0]!.cardId)[1]!.target.cardId, "Removing a set entry dropped unrelated hidden members");
    button("Save owned relationships").click(); await new Promise(resolve => setTimeout(resolve, 0));
    check(mounted.state.mutations === 0 && field.value.includes("reference") && document.body.textContent!.includes("at most 100"), "Overbound replacement was truncated/dispatched");
    button("Clear owned relationships").click(); button("Clear owned relationships").focus(); button("Clear owned relationships").dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    check(document.activeElement === button("Keep editing"), "Dirty set cancel lacks safe focus"); button("Keep editing").click();
    check(document.activeElement === button("Clear owned relationships") && field.value === "[]", "Declining dirty cancel lost input");
    mounted.state.mode = "conflict"; await mounted.view.flushPrivateInput(); button("Save owned relationships").click();
    await settle(() => mounted.state.mutations === 1 && document.body.textContent!.includes("Conflict. Input retained;"));
    const wire = JSON.parse(mounted.state.wire[0]!); check(Object.keys(wire.input).join() === "relationships" && wire.input.relationships.length === 0 && wire.target.cardId === read.result.cards[0]!.cardId, "Replacement command targeted incoming owner or included unrelated edits");
    check(field.value === "[]" && mounted.state.lookups === 0, "Conflict input lost or automatic original lookup occurred");
  } finally { mounted.view.destroy(); mounted.client.close(); }
  return checks;
}

export async function runRelationshipRecoveryProbe(input: BoardRead, session: SessionRead): Promise<number> {
  const read = relationshipFixture(input, 153), mounted = await mountSyntheticBoard(read, session);
  let restored: BoardView | undefined;
  const button = (label: string): HTMLButtonElement => [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
  try {
    document.querySelector<HTMLButtonElement>('button[data-open-card="' + read.result.cards[0]!.cardId + '"]')!.click();
    button("Replace owned relationships").click(); button("Remove owned set entry 1").click();
    await mounted.view.flushPrivateInput();
    const field = document.querySelector<HTMLTextAreaElement>('textarea[name="relationships"]')!, before = JSON.stringify(JSON.parse(field.value));
    const drafts = (await mounted.client.recovery.inventory()).drafts, draft = drafts.at(-1)!;
    if (!draft) throw Error("Relationship draft was not stored");
    mounted.view.destroy(); const root = document.createElement("main"); document.body.replaceChildren(root);
    restored = new BoardView(root, mounted.client, read.result.identity.boardId); await restored.open();
    await restored.restore({ context: draft.context, draftId: draft.draftId });
    const after = document.querySelector<HTMLTextAreaElement>('textarea[name="relationships"]');
    if (!after || JSON.stringify(JSON.parse(after.value)) !== before || document.querySelectorAll('[aria-label="Selected owned relationships"] article').length !== 101) throw Error("Overbound complete-set draft lost members/order during reload");
    button("Save owned relationships").click();
    if (mounted.state.mutations !== 0 || mounted.state.lookups !== 0 || !document.body.textContent!.includes("at most 100")) throw Error("Restored overbound input dispatched or looked up an original automatically");
    return 3;
  } finally { restored?.destroy(); mounted.view.destroy(); mounted.client.close(); }
}
