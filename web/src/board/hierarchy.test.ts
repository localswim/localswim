// Synthetic native-DOM supplements; real transaction/HTTP journeys are separate.
import type { BoardRead, CardRead, HistoryRead, MutationRequest, SessionRead } from "../../generated/transport/contracts.js";
import { boardBoundaryVerifier, canonical, prepare, validate } from "../api/boundary.js";
import { validateCardRead, validateHistoryRead } from "../api/history.js";
import { captureHierarchy, validateBoardHierarchy, validParentRef } from "../api/hierarchy.js";
import type { ParentRef } from "../api/hierarchy.js";
import { CardHierarchy } from "./hierarchy.js";
import { mountSyntheticBoard } from "./view.test.js";
import { BoardView } from "./view.js";
import type { EditorReference } from "../session-ui/window-state.js";

const id = (n: number): string => "00000000-0000-4000-8000-" + String(n).padStart(12, "0");
function forest(input: BoardRead, count: number, chain = false): BoardRead {
  const read = structuredClone(input), identity = read.result.identity, template = read.result.cards[0]!;
  Object.assign(read.result, { cards: Array.from({ length: count }, (_, index) => ({ ...template,
    cardId: id(index + 100), ticket: index + 1, title: "Hierarchy " + (index + 1), lane: "Ready for Work", ownerId: null,
    archived: index === 1, commentIds: [], linkIds: [], attachmentIds: [], currentApprovalId: null,
    ...(index ? { parent: { ...identity, cardId: id(chain ? index + 99 : 100) } } : {}) })) });
  return read;
}
const settle = async (test: () => boolean): Promise<void> => {
  for (let index = 0; index < 250; index++) { if (test()) return; await new Promise(resolve => setTimeout(resolve, 10)); }
  throw Error("Hierarchy component did not settle");
};
export async function runHierarchyBoundaryProbe(input: BoardRead): Promise<number> {
  let checks = 0;
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  const refuses = (read: BoardRead): boolean => { try { validateBoardHierarchy(read); return false; } catch { return true; } };
  const read = forest(input, 23), identity = read.result.identity, root = read.result.cards[0]!;
  validateBoardHierarchy(read); checks++;
  const snapshot = captureHierarchy(read, root.cardId);
  check(snapshot.directChildCount === 22 && snapshot.nodes.length === 22 && snapshot.nodes[0]!.card.ticket === 2 && snapshot.nodes[21]!.card.ticket === 23, "Hierarchy counts/order mismatch");
  Object.assign(read.result.cards[1]!, { title: "Later caller mutation" });
  check(snapshot.nodes[0]!.card.title !== "Later caller mutation", "Captured hierarchy retained mutable caller facts");
  const nested = forest(input, 5);
  Object.assign(nested.result.cards[3]!, { parent: { ...identity, cardId: nested.result.cards[1]!.cardId } });
  const nestedSnapshot = captureHierarchy(nested, nested.result.cards[0]!.cardId);
  check(nestedSnapshot.nodes.map(row => row.card.ticket).join() === "2,4,3,5" && nestedSnapshot.nodes[1]!.depth === 2 && nestedSnapshot.nodes[0]!.descendantCount === 1, "Preorder or derived counts mismatch");
  for (const mutate of [
    (r: BoardRead): void => { Object.assign(r.result.cards[0]!, { parent: { ...identity, cardId: r.result.cards[0]!.cardId } }); },
    (r: BoardRead): void => { Object.assign(r.result.cards[0]!, { parent: { ...identity, cardId: r.result.cards[1]!.cardId } }); },
    (r: BoardRead): void => { Object.assign(r.result.cards[1]!, { parent: { ...identity, cardId: id(9000) } }); },
    (r: BoardRead): void => { Object.assign(r.result.cards[1]!, { parent: { ...identity, incarnation: id(9000), cardId: root.cardId } }); },
    (r: BoardRead): void => { Object.assign(r.result.cards[1]!, { parent: { ...identity, boardId: id(9000), cardId: root.cardId } }); },
    (r: BoardRead): void => { Object.assign(r.result.cards[1]!, { parent: null }); },
  ]) { const bad = forest(input, 3); mutate(bad); check(refuses(bad), "Malformed forest accepted"); }
  check(!refuses(forest(input, 65, true)) && refuses(forest(input, 66, true)), "64/65 edge boundary mismatch");
  check(!refuses(forest(input, 1000)) && refuses(forest(input, 1001)), "1000/1001 family boundary mismatch");
  const independent = forest(input, 1001);
  for (const card of independent.result.cards) delete (card as { parent?: unknown }).parent;
  check(!refuses(independent), "Family bound incorrectly became a board bound");
  check(!validParentRef({ ...identity, cardId: root.cardId, ticket: root.ticket }) && !validParentRef({ ...identity, cardId: "00000000-0000-4000-8000-000000000ABC" }), "Inexact parent reference accepted");
  const context = { installationId: id(90), ...identity, actorId: id(91) }, parent = { ...identity, cardId: id(92) };
  for (const command of ["card.create", "card.edit"] as const) {
    const fingerprints = new Set<string>();
    for (const value of [undefined, null, parent]) {
      const request = { opId: id(93), incarnation: identity.incarnation, expectedRevision: 2, expectedCardRevision: command === "card.edit" ? 1 : null,
        target: command === "card.edit" ? { cardId: id(94) } : null,
        input: { ...(command === "card.create" ? { title: "Child", description: "" } : { title: "Legacy edit" }), ...(value === undefined ? {} : { parent: value }) } } as unknown as MutationRequest;
      if (command === "card.edit" && value !== undefined) delete (request.input as unknown as { title?: string }).title;
      const evidence = await prepare(command, request, context, id(95));
      const payload = JSON.parse(evidence.canonicalBytes).payload;
      check(Object.hasOwn(payload, "parent") === (value !== undefined) && (value === undefined || canonical(payload.parent) === canonical(value)) && await boardBoundaryVerifier.prepared(evidence, request), "Parent omission/null/ref recovery boundary mismatch");
      fingerprints.add(evidence.fingerprint);
    }
    check(fingerprints.size === 3, "Parent omission and explicit null collided");
  }
  const mixed = { opId: id(93), incarnation: identity.incarnation, expectedRevision: 2, expectedCardRevision: 1, target: { cardId: id(94) }, input: { parent, title: "Mixed" } } as unknown as MutationRequest;
  let refused = false; try { await prepare("card.edit", mixed, context, id(95)); } catch { refused = true; }
  check(refused, "Mixed parent/content edit admitted");
  return checks;
}

export async function runHierarchyNavigationProbe(input: BoardRead): Promise<number> {
  let checks = 0;
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  let read = forest(input, 22), reads = 0;
  const root = read.result.cards[0]!, identity = read.result.identity;
  let release: (() => void) | undefined, late = false, navigation = "";
  const hierarchy = new CardHierarchy({ board: async () => { reads++; const result = structuredClone(read); if (late) await new Promise<void>(resolve => { release = resolve; }); return result; } },
    cardId => { navigation = cardId; });
  const unrelated = document.createElement("input"); document.body.replaceChildren(unrelated, hierarchy.host);
  const find = (label: string): HTMLButtonElement => [...hierarchy.host.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
  hierarchy.boardRead(read); hierarchy.open(root.cardId, identity);
  try {
    check(hierarchy.host.textContent!.includes("Children: 21 direct · 21 descendants") && hierarchy.host.textContent!.includes("Children 1–20 of 21") && hierarchy.host.querySelectorAll("article").length === 20 && hierarchy.host.textContent!.includes("Archived"), "First hierarchy page/count/archive mismatch");
    find("Next children").click();
    check(hierarchy.host.textContent!.includes("Children 21–21 of 21 · page 2 of 2 · End") && hierarchy.host.querySelectorAll("article").length === 1 && reads === 0, "Hierarchy pagination used network or missed end");
    find("First children page").click();
    check(hierarchy.host.textContent!.includes("page 1 of 2") && find("First children page").disabled && document.activeElement === find("Read current hierarchy"), "First children page failed reset/focus");
    find("Next children").click();
    read = structuredClone(read); Object.assign(read.result, { revision: read.result.revision + 1 });
    Object.assign(read.result.cards[21]!, { title: "NEW background title" }); hierarchy.boardRead(read);
    check(hierarchy.host.textContent!.includes("Hierarchy stale") && !hierarchy.host.textContent!.includes("NEW background title") && hierarchy.host.textContent!.includes("page 2 of 2"), "Background read spliced the captured page");
    unrelated.focus(); await hierarchy.readCurrent();
    check(hierarchy.host.textContent!.includes("Children 1–20 of 21") && document.activeElement === unrelated && reads === 1, "Explicit hierarchy refresh failed reset or stole focus");
    find("Next children").click();
    check(hierarchy.host.textContent!.includes("NEW background title"), "New revision absent after explicit refresh");
    late = true; const pending = hierarchy.readCurrent(); await settle(() => !!release);
    hierarchy.open(read.result.cards[1]!.cardId, identity); unrelated.focus(); release!(); await pending;
    check(hierarchy.host.textContent!.includes("Children 0–0 of 0 · page 1 of 1 · End") && document.activeElement === unrelated, "Late A hierarchy read painted/focused B");
    const parent = [...hierarchy.host.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent!.startsWith("Open parent"))!; parent.click();
    check(navigation === root.cardId, "Parent navigation lost exact identity");
    hierarchy.open(root.cardId, identity); hierarchy.suspend(); const priorReads = reads; await hierarchy.readCurrent();
    check(reads === priorReads, "Suspended hierarchy performed a read");
    hierarchy.resume(); hierarchy.boardRead({ ...read, result: { ...read.result, identity: { ...identity, incarnation: id(9900) } } });
    check(hierarchy.host.textContent === "", "Hierarchy survived a board-incarnation change");
    const twenty = forest(input, 21); hierarchy.boardRead(twenty); hierarchy.open(twenty.result.cards[0]!.cardId, twenty.result.identity);
    check(hierarchy.host.textContent!.includes("Children 1–20 of 20 · page 1 of 1 · End") && find("Next children").disabled, "Exact 20-row boundary mismatch");
    hierarchy.observeRevision(twenty.result.identity, twenty.result.revision + 1);
    check(hierarchy.host.textContent!.includes("Hierarchy stale") && hierarchy.host.querySelectorAll("article").length === 20, "Newer focused revision failed to mark captured hierarchy stale");
    hierarchy.open(twenty.result.cards[0]!.cardId, twenty.result.identity);
    check(hierarchy.host.textContent!.includes("Hierarchy stale"), "Reopening silently treated an older complete board as current after a focused newer read");
    late = false; read = twenty; await hierarchy.readCurrent();
    check(hierarchy.host.textContent!.includes("Current hierarchy unavailable") && hierarchy.host.textContent!.includes("Hierarchy stale"), "Read below known focused revision incorrectly claimed current hierarchy");
    read = structuredClone(twenty); Object.assign(read.result, { revision: twenty.result.revision + 1 }); await hierarchy.readCurrent();
    check(hierarchy.host.textContent!.includes("Current hierarchy read.") && !hierarchy.host.textContent!.includes("Hierarchy stale"), "Current complete board failed to clear focused-revision staleness");
    // Controlled complete-BoardRead boundary fixture, not 1,000 normal CLI
    // creations or a whole-board network/memory bound claim. Exhaust all 999
    // descendants through the actual local-page controls (49*20 + 19).
    const wide = forest(input, 1000), wideRoot = wide.result.cards[0]!;
    Object.assign(wide.result, { revision: read.result.revision + 1 });
    for (const [index, card] of wide.result.cards.entries()) if (index) Object.assign(card, { ticket: 1001 - index });
    validate("transport:BoardRead", wide as never); validateBoardHierarchy(wide);
    const expected = [...wide.result.cards.slice(1)].sort((a, b) => a.ticket - b.ticket || (a.cardId < b.cardId ? -1 : a.cardId > b.cardId ? 1 : 0)).map(card => card.cardId);
    const seen = new Set<string>(), visited: string[] = [], readsBeforeWidePages = reads;
    hierarchy.boardRead(wide); hierarchy.open(wideRoot.cardId, wide.result.identity);
    check(hierarchy.host.textContent!.includes("Children: 999 direct · 999 descendants"), "Controlled 1000-family direct/descendant counts mismatch");
    for (let page = 0; page < 50; page++) {
      const rows = [...hierarchy.host.querySelectorAll<HTMLElement>("article[data-hierarchy-card]")];
      const ids = rows.map(row => row.dataset.hierarchyCard!);
      const start = page * 20, stop = Math.min(999, start + 20);
      const range = [...hierarchy.host.querySelectorAll("p")].find(row => row.textContent!.startsWith("Children "))?.textContent;
      check(rows.length === (page === 49 ? 19 : 20) && range === "Children " + (start + 1) + "–" + stop + " of 999 · page " + (page + 1) + " of 50" + (page === 49 ? " · End" : ""), "Controlled 1000-family page " + (page + 1) + " range/count/end mismatch");
      check(ids.join() === expected.slice(start, stop).join() && ids.every(value => !seen.has(value)), "Controlled 1000-family page " + (page + 1) + " order/duplicate mismatch");
      check(find("Next children").disabled === (page === 49) && find("Previous children").disabled === (page === 0) && find("First children page").disabled === (page === 0), "Controlled 1000-family page " + (page + 1) + " boundary controls mismatch");
      for (const value of ids) { seen.add(value); visited.push(value); }
      if (page < 49) find("Next children").click();
    }
    check(seen.size === 999 && visited.join() === expected.join() && !seen.has(wideRoot.cardId) && reads === readsBeforeWidePages, "Controlled 1000-family exhaustion omitted/repeated rows, included root or fetched network pages");
    find("First children page").click();
    check(hierarchy.host.textContent!.includes("Children 1–20 of 999 · page 1 of 50") && find("First children page").disabled && [...hierarchy.host.querySelectorAll<HTMLElement>("article[data-hierarchy-card]")].map(row => row.dataset.hierarchyCard).join() === expected.slice(0, 20).join(), "Controlled 1000-family first-page reset mismatch");
    return checks;
  } finally { hierarchy.close(); hierarchy.host.remove(); unrelated.remove(); }
}

export function runHierarchyHistoryProbe(input: HistoryRead): number {
  const history = structuredClone(input), row = history.result.records.find(record => record.state)!;
  const parent = { ...row.origin, cardId: id(9999) };
  Object.assign(row.state!, { parent });
  // An absent standalone registry must not invent target availability or make
  // snapshot-time scope depend on the current incarnation/CreatedIn.
  validate("transport:HistoryRead", history as never); validateHistoryRead(history);
  let checks = 1;
  for (const invalid of [null, { ...parent, incarnation: id(9000) }, { ...parent, boardId: id(9000) }, { ...parent, ticket: 5 }, { ...parent, cardId: row.cardId }]) {
    const bad = structuredClone(history); Object.assign(bad.result.records.find(record => record.state)!.state!, { parent: invalid });
    let refused = false;
    try { validate("transport:HistoryRead", bad as never); validateHistoryRead(bad); } catch { refused = true; }
    if (!refused) throw Error("Historical parent shape/snapshot-origin mismatch accepted"); checks++;
  }
  return checks;
}

export function runHierarchyCurrentReadProbe(input: CardRead): number {
  const current = structuredClone(input), card = current.result.card, parent = { ...current.result.identity, cardId: id(9999) };
  Object.assign(card, { createdIn: { ...current.result.identity, incarnation: id(9998) }, parent });
  validate("transport:CardRead", current as never); validateCardRead(current);
  let checks = 1;
  for (const invalid of [null, { ...parent, incarnation: id(9000) }, { ...parent, boardId: id(9000) }, { ...parent, ticket: 5 }, { ...parent, cardId: card.cardId }]) {
    const bad = structuredClone(current); Object.assign(bad.result.card, { parent: invalid });
    let refused = false;
    try { validate("transport:CardRead", bad as never); validateCardRead(bad); } catch { refused = true; }
    if (!refused) throw Error("Focused current parent shape/scope/self mismatch accepted"); checks++;
  }
  return checks;
}

export async function runHierarchyEditorProbe(input: BoardRead, session: SessionRead): Promise<number> {
  let checks = 0;
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  const data = forest(input, 3), child = data.result.cards[1]!, target = data.result.cards[2]!;
  Object.assign(child, { archived: false }); Object.assign(target, { lane: "Completed", archived: true, createdIn: { ...data.result.identity, incarnation: id(777) } });
  let mounted = await mountSyntheticBoard(data, session);
  let view = mounted.view;
  const find = (label: string): HTMLButtonElement => [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
  const openChild = (): void => { document.querySelector<HTMLButtonElement>('button[data-open-card="' + child.cardId + '"]')!.click(); };
  const choose = (): HTMLSelectElement => document.querySelector('select[name="parent"]')!;
  const discard = async (): Promise<void> => { find("Discard current editor input").click(); find("Confirm discard editor input")?.click(); await view.flushPrivateInput(); };
  try {
    openChild(); find("Set parent").click();
    check(!!choose() && find("Save").disabled && !document.querySelector('input[name="title"]'), "Parent editor mixed content or guessed selection");
    const selected = choose(), option = [...selected.options].find(row => row.value === target.cardId)!;
    check(option.textContent!.includes("Completed") && option.textContent!.includes("Archived"), "Parent selector excluded/lost completed archived target labels");
    selected.value = target.cardId; selected.dispatchEvent(new Event("change", { bubbles: true }));
    await view.flushPrivateInput();
    selected.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    check(!!find("Confirm discard editor input") && selected.value === target.cardId, "Dirty parent cancellation skipped confirmation");
    find("Keep editing").click(); check(document.activeElement === selected && selected.value === target.cardId, "Parent discard refusal lost input/focus");
    selected.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
    selected.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", isComposing: true, bubbles: true, cancelable: true }));
    check(mounted.state.mutations === 0, "Parent IME confirmation submitted");
    selected.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true }));
    selected.dispatchEvent(new KeyboardEvent("keyup", { key: "Enter", bubbles: true }));
    selected.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    check(mounted.state.mutations === 0, "Selecting a parent automatically submitted instead of requiring Save");
    mounted.state.mode = "late";
    find("Save").focus(); find("Save").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    find("Save").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", repeat: true, bubbles: true, cancelable: true }));
    await settle(() => mounted.state.mutations === 1 && mounted.state.releases.length === 1);
    const wire = JSON.parse(mounted.state.wire[0]!);
    check(Object.keys(wire.input).join() === "parent" && wire.input.parent.cardId === target.cardId && wire.input.parent.incarnation === data.result.identity.incarnation && wire.input.parent.incarnation !== target.createdIn.incarnation, "Parent edit lost exclusivity/current identity");
    find("Stop waiting").click(); selected.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    check(mounted.state.mutations === 1 && !!find("Look up original outcome"), "Parent UNKNOWN allowed replay");
    mounted.state.releases.splice(0).forEach(run => run()); await settle(() => document.body.textContent!.includes("Confirmed saved."));
    await view.refresh();
    check(choose() === selected && selected.value === target.cardId, "Board refresh replaced retained parent editor");
    view.discardInput(); openChild(); find("Clear parent").click();
    await view.flushPrivateInput(); find("Save").click(); await settle(() => mounted.state.mutations === 2);
    check(JSON.parse(mounted.state.wire[1]!).input.parent === null && Object.keys(JSON.parse(mounted.state.wire[1]!).input).join() === "parent", "Clear parent lost explicit null or mixed fields");
    await settle(() => document.body.textContent!.includes("Confirmed saved."));
    // Fresh independent view restores the same private draft/original without replay.
    view.destroy(); mounted.client.close(); mounted = await mountSyntheticBoard(data, session); view = mounted.view;
    let reference: EditorReference | undefined;
    const root = document.querySelector("main")!; view.destroy(); view = new BoardView(root, mounted.client, data.result.identity.boardId, value => { reference = value; }); await view.open();
    openChild(); find("Set parent").click(); await view.flushPrivateInput();
    const emptyReference = reference!; view.destroy(); view = new BoardView(root, mounted.client, data.result.identity.boardId, value => { reference = value; }); await view.open(); await view.restore(emptyReference);
    check(!!choose() && choose().value === "" && find("Save").disabled, "Unselected Set parent draft became Clear parent after reload");
    choose().value = target.cardId; choose().dispatchEvent(new Event("change", { bubbles: true })); await view.flushPrivateInput();
    const retained = reference!; view.destroy(); view = new BoardView(root, mounted.client, data.result.identity.boardId); await view.open(); await view.restore(retained);
    check(choose().value === target.cardId && mounted.state.mutations === 0 && mounted.state.lookups === 0, "Parent draft reload lost identity or replayed");
    await discard(); openChild(); find("Create child").click();
    const title = document.querySelector<HTMLInputElement>('input[name="title"]')!; title.value = "Nested child"; title.dispatchEvent(new Event("input", { bubbles: true })); await view.flushPrivateInput();
    find("Save").click(); await settle(() => mounted.state.mutations === 1);
    const created = JSON.parse(mounted.state.wire[0]!);
    check(created.target === null && created.expectedCardRevision === null && created.input.parent.cardId === child.cardId && created.input.title === "Nested child", "Create child did not use ordinary immutable create flow");
    await settle(() => document.body.textContent!.includes("Confirmed saved."));
    view.discardInput(); await view.refresh();
    const archive = document.querySelector<HTMLSelectElement>('[aria-label="Archive status"]')!; archive.value = "all"; archive.dispatchEvent(new Event("change", { bubbles: true }));
    document.querySelector<HTMLButtonElement>('button[data-open-card="' + target.cardId + '"]')!.click();
    check(find("Set parent").disabled && find("Clear parent").disabled && !find("Create child").disabled, "Completed child permission leaked or Completed parent blocked create-child");
    return checks;
  } finally { view.destroy(); mounted.client.close(); }
}

export async function runHierarchyRecoveryProbe(input: BoardRead, session: SessionRead): Promise<number> {
  let checks = 0;
  for (const action of ["Set parent", "Clear parent", "Create child"]) {
    const data = forest(input, 3), child = data.result.cards[1]!, target = data.result.cards[2]!;
    Object.assign(child, { archived: false });
    const mounted = await mountSyntheticBoard(data, session); mounted.view.destroy();
    const host = document.querySelector("main")!;
    let reference: EditorReference | undefined;
    let view = new BoardView(host, mounted.client, data.result.identity.boardId, value => { reference = value; });
    const find = (label: string): HTMLButtonElement => [...host.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
    try {
      await view.open(); document.querySelector<HTMLButtonElement>('button[data-open-card="' + child.cardId + '"]')!.click(); find(action).click();
      if (action === "Set parent") { const select = host.querySelector<HTMLSelectElement>('select[name="parent"]')!; select.value = target.cardId; select.dispatchEvent(new Event("change", { bubbles: true })); }
      if (action === "Create child") { const title = host.querySelector<HTMLInputElement>('input[name="title"]')!; title.value = "UNKNOWN child"; title.dispatchEvent(new Event("input", { bubbles: true })); }
      await view.flushPrivateInput(); const retained = reference!;
      mounted.state.mode = "late"; find("Save").click(); await settle(() => mounted.state.releases.length === 1);
      const original = mounted.state.wire[0]!;
      view.destroy(); view = new BoardView(host, mounted.client, data.result.identity.boardId); await view.open(); await view.restore(retained);
      if (!find("Save").disabled || !find("Look up original outcome") || mounted.state.mutations !== 1 || mounted.state.lookups !== 0) throw Error(action + " UNKNOWN reload lost original guard or replayed"); checks++;
      const draft = await mounted.client.recovery.loadDraft(retained.context, retained.draftId);
      const payload = JSON.parse(draft!.input);
      if (action === "Clear parent" && (!Object.hasOwn(payload.input, "parent") || payload.input.parent !== null)) throw Error("Explicit null lost from private clear-parent draft");
      if (action !== "Clear parent" && payload.input.parent.cardId !== (action === "Set parent" ? target.cardId : child.cardId)) throw Error("Parent exact identity lost on private reload"); checks++;
      await view.refresh(); document.querySelector<HTMLButtonElement>('button[data-open-card="' + child.cardId + '"]')!.click(); find("Read current hierarchy").click(); await settle(() => host.textContent!.includes("Current hierarchy read."));
      find("Save").click();
      if (mounted.state.mutations !== 1 || mounted.state.lookups !== 0 || mounted.state.wire[0] !== original || !find("Save").disabled) throw Error("Hierarchy navigation/refresh replayed UNKNOWN original"); checks++;
      mounted.state.releases.splice(0).forEach(release => release());
      await new Promise(resolve => setTimeout(resolve, 20));
    } finally { view.destroy(); mounted.client.close(); }
  }
  const mounted = await mountSyntheticBoard(forest(input, 3), session, false);
  const child = mounted.board.result.cards[2]!;
  const find = (label: string): HTMLButtonElement => [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
  try {
    document.querySelector<HTMLButtonElement>('button[data-open-card="' + child.cardId + '"]')!.click(); find("Clear parent").click(); await mounted.view.flushPrivateInput(); find("Save").click();
    await settle(() => document.body.textContent!.includes("Not dispatched."));
    if (mounted.state.mutations !== 0 || !document.querySelector("form") || !document.body.textContent!.includes("Clear parent for")) throw Error("Unqualified private storage dispatched/lost clear-parent"); checks++;
    await mounted.view.refresh(); if (mounted.state.mutations !== 0 || !document.body.textContent!.includes("Clear parent for")) throw Error("Private refusal refresh changed retained parent intent"); checks++;
  } finally { mounted.view.destroy(); mounted.client.close(); }
  return checks;
}

export async function runHierarchyDirtySwitchProbe(input: BoardRead, session: SessionRead): Promise<number> {
  let checks = 0;
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  for (const action of ["Create child", "Set parent", "Clear parent"] as const) {
    const data = forest(input, 3), child = data.result.cards[1]!, target = data.result.cards[2]!;
    Object.assign(child, { archived: false });
    const mounted = await mountSyntheticBoard(data, session);
    const find = (label: string): HTMLButtonElement => [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
    try {
      const baselineForms = document.querySelectorAll("form").length;
      find("Edit card").click();
      const original = document.querySelector<HTMLInputElement>('input[name="title"]')!;
      original.value = "Keep my unfinished content"; original.dispatchEvent(new Event("input", { bubbles: true })); await mounted.view.flushPrivateInput();
      document.querySelector<HTMLButtonElement>('button[data-open-card="' + child.cardId + '"]')!.click();
      find(action).click();
      check(!!find("Confirm discard editor input") && original.isConnected && original.value === "Keep my unfinished content" && mounted.state.mutations === 0, action + " replaced dirty content without confirmation");
      find("Keep editing").click();
      check(document.activeElement === original && original.value === "Keep my unfinished content" && !find("Confirm discard editor input") && mounted.state.mutations === 0, action + " Keep editing lost input/focus or dispatched");
      find(action).click(); find("Confirm discard editor input").click();
      const title = document.querySelector<HTMLInputElement>('input[name="title"]'), parent = document.querySelector<HTMLSelectElement>('select[name="parent"]');
      check(!original.isConnected && !find("Confirm discard editor input") && mounted.state.mutations === 0, action + " discard did not transition without dispatch");
      check(action === "Create child" ? !!title && title.value === "" && document.activeElement === title && document.body.textContent!.includes("Create child of exact parent " + data.result.identity.boardId + "/" + data.result.identity.incarnation + "/" + child.cardId)
        : action === "Set parent" ? !!parent && parent.value === "" && document.activeElement === parent && !title && find("Save").disabled
          : !title && !parent && document.activeElement === find("Save") && document.body.textContent!.includes("Clear parent for #" + child.ticket), action + " continuation lost hierarchy context/initial focus");
      await mounted.view.flushPrivateInput(); find("Discard current editor input").click();
      check(document.querySelectorAll("form").length === baselineForms && document.activeElement === find(action) && mounted.state.mutations === 0, action + " clean cancellation failed opener focus or dispatched");
      find(action).click();
      if (action === "Create child") { const field = document.querySelector<HTMLInputElement>('input[name="title"]')!; field.value = "Confirmed nested child"; field.dispatchEvent(new Event("input", { bubbles: true })); }
      if (action === "Set parent") { const field = document.querySelector<HTMLSelectElement>('select[name="parent"]')!; field.value = target.cardId; field.dispatchEvent(new Event("change", { bubbles: true })); }
      await mounted.view.flushPrivateInput(); find("Save").click(); await settle(() => mounted.state.mutations === 1);
      const request = JSON.parse(mounted.state.wire[0]!);
      check(action === "Create child" ? request.target === null && request.input.parent.cardId === child.cardId && request.input.title === "Confirmed nested child"
        : request.target.cardId === child.cardId && Object.keys(request.input).join() === "parent" && (action === "Clear parent" ? request.input.parent === null : request.input.parent.cardId === target.cardId), action + " transition submitted changed authority or lost exclusive parent payload");
      await settle(() => document.body.textContent!.includes("Confirmed saved."));
    } finally { mounted.view.destroy(); mounted.client.close(); }
  }
  return checks;
}

export async function runHierarchyReceiptFollowProbe(input: BoardRead, session: SessionRead): Promise<number> {
  let checks = 0;
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  for (const action of ["Set parent", "Clear parent", "Create child"] as const) {
    const data = forest(input, 3), child = data.result.cards[1]!, target = data.result.cards[2]!;
    Object.assign(child, { archived: false });
    const mounted = await mountSyntheticBoard(data, session);
    const find = (label: string): HTMLButtonElement => [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
    const announcement = (): string => document.querySelector('[aria-label="Current parent announcement"]')!.textContent!;
    let boardReads = 0; const boardRead = mounted.client.board.bind(mounted.client);
    mounted.client.board = async boardId => { boardReads++; return boardRead(boardId); };
    try {
      document.querySelector<HTMLButtonElement>('button[data-open-card="' + child.cardId + '"]')!.click();
      await settle(() => document.querySelector('[aria-label="Current card state"]')!.textContent!.includes("Card UUID: " + child.cardId));
      find(action).click();
      if (action === "Set parent") { const field = document.querySelector<HTMLSelectElement>('select[name="parent"]')!; field.value = target.cardId; field.dispatchEvent(new Event("change", { bubbles: true })); }
      if (action === "Create child") { const field = document.querySelector<HTMLInputElement>('input[name="title"]')!; field.value = "Follow authoritative child"; field.dispatchEvent(new Event("input", { bubbles: true })); }
      await mounted.view.flushPrivateInput(); const editor = document.querySelector("form > h2")!.parentElement!, save = find("Save");
      mounted.state.mode = "late"; save.focus(); save.click(); await settle(() => mounted.state.releases.length === 1);
      const request = JSON.parse(mounted.state.wire[0]!), selectedId = action === "Create child" ? mounted.board.result.cards.at(-1)!.cardId : child.cardId;
      const retainedFocus = document.querySelector<HTMLElement>('select[name="parent"],input[name="title"]') ?? document.querySelector<HTMLElement>('[aria-label="Search cards"]')!;
      retainedFocus.focus();
      check(announcement() === "" && mounted.state.mutations === 1, action + " announced parent optimistically before the receipt/read");
      mounted.state.releases.splice(0).forEach(release => release());
      await settle(() => announcement().includes("Current parent for child " + selectedId + ":"));
      const parent = action === "Set parent" ? target.cardId : action === "Create child" ? child.cardId : null;
      check(announcement().includes(parent ? data.result.identity.boardId + "/" + data.result.identity.incarnation + "/" + parent : "No parent") && document.querySelector('[aria-label="Current card state"]')!.textContent!.includes("Card UUID: " + selectedId), action + " failed selected child/current parent announcement");
      check(editor.isConnected && find("Save") === save && save.disabled && document.activeElement === retainedFocus && boardReads === 0 && mounted.state.lookups === 0, action + " receipt changed editor/focus, fetched graph or looked up/replayed");
      check(document.querySelector('[aria-label="Card hierarchy"]')!.textContent!.includes("Hierarchy stale") && request.expectedRevision === data.result.revision && request.expectedCardRevision === (action === "Create child" ? null : child.revision), action + " receipt spliced hierarchy or rebased original");
      const selectedCard = mounted.board.result.cards.find(card => card.cardId === selectedId)!;
      Object.assign(selectedCard, { parent: { ...data.result.identity, cardId: data.result.cards[0]!.cardId }, revision: selectedCard.revision + 1 });
      Object.assign(mounted.board.result, { revision: mounted.board.result.revision + 1 });
      find("Refresh card details").click();
      check(announcement() === "", action + " ordinary refresh retained an older current-parent announcement");
      await settle(() => document.querySelector('[aria-label="Current card state"]')!.textContent!.includes("Board revision " + mounted.board.result.revision));
      check(announcement() === "" && document.querySelector('[aria-label="Current card state"]')!.textContent!.includes("Current parent reference: " + data.result.identity.boardId + "/" + data.result.identity.incarnation + "/" + data.result.cards[0]!.cardId), action + " ordinary refresh combined an old parent announcement with newer parent facts");
    } finally { mounted.view.destroy(); mounted.client.close(); }
  }
  for (const scenario of ["navigate-before-receipt", "close-before-receipt", "suspend-resume-before-receipt", "reload-before-receipt", "refresh-details-before-receipt", "refresh-board-before-receipt", "navigate-during-follow-read", "failed-read", "board-revision-floor", "card-revision-floor", "wrong-incarnation"] as const) {
    const data = forest(input, 3), child = data.result.cards[1]!, target = data.result.cards[2]!;
    Object.assign(child, { archived: false });
    const mounted = await mountSyntheticBoard(data, session);
    let activeView = mounted.view;
    const find = (label: string): HTMLButtonElement => [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
    const announcement = (): string => document.querySelector('[aria-label="Current parent announcement"]')!.textContent!;
    const stateText = (): string => document.querySelector('[aria-label="Current card state"]')!.textContent!;
    const open = (cardId: string): void => { document.querySelector<HTMLButtonElement>('button[data-open-card="' + cardId + '"]')!.click(); };
    let createdId = "", releaseRead: (() => void) | undefined, followReads = 0;
    const cardRead = mounted.client.card.bind(mounted.client);
    mounted.client.card = async (boardId, cardId) => {
      const result = await cardRead(boardId, cardId);
      if (createdId && cardId === createdId) {
        followReads++;
        if (scenario === "failed-read") throw Error("Controlled follow read unavailable");
        if (scenario === "navigate-during-follow-read") await new Promise<void>(resolve => { releaseRead = resolve; });
        if (scenario === "board-revision-floor") Object.assign(result.result, { revision: data.result.revision });
        if (scenario === "card-revision-floor") Object.assign(result.result.card, { revision: 0 });
        if (scenario === "wrong-incarnation") Object.assign(result.result, { identity: { ...result.result.identity, incarnation: id(9997) } });
      }
      return result;
    };
    try {
      open(child.cardId); await settle(() => stateText().includes("Card UUID: " + child.cardId));
      find("Create child").click(); const title = document.querySelector<HTMLInputElement>('input[name="title"]')!;
      title.value = "Fenced new child"; title.dispatchEvent(new Event("input", { bubbles: true })); await mounted.view.flushPrivateInput();
      mounted.state.mode = "late"; find("Save").focus(); find("Save").click(); await settle(() => mounted.state.releases.length === 1); createdId = mounted.board.result.cards.at(-1)!.cardId;
      if (scenario === "navigate-before-receipt") { open(target.cardId); await settle(() => stateText().includes("Card UUID: " + target.cardId)); }
      if (scenario === "close-before-receipt") find("Close card details").click();
      if (scenario === "suspend-resume-before-receipt") { mounted.view.suspend(); mounted.view.resume(); }
      if (scenario === "reload-before-receipt") {
        const context = mounted.client.context(mounted.board, mounted.auth), operation = JSON.parse(mounted.state.wire[0]!).opId;
        const submission = await mounted.client.recovery.loadSubmission(context, operation), host = document.querySelector("main")!;
        mounted.view.destroy(); activeView = new BoardView(host, mounted.client, data.result.identity.boardId); await activeView.open(); await activeView.restore({ context, draftId: submission!.evidence.draftId });
        open(target.cardId); await settle(() => stateText().includes("Card UUID: " + target.cardId));
      }
      if (scenario === "refresh-details-before-receipt") { find("Refresh card details").click(); await settle(() => stateText().includes("Board revision " + mounted.board.result.revision)); }
      if (scenario === "refresh-board-before-receipt") { await mounted.view.refresh(); await settle(() => stateText().includes("Board revision " + mounted.board.result.revision)); }
      mounted.state.releases.splice(0).forEach(release => release());
      if (scenario === "reload-before-receipt") await new Promise(resolve => setTimeout(resolve, 20));
      else await settle(() => document.body.textContent!.includes("Confirmed saved."));
      if (scenario === "navigate-during-follow-read") {
        await settle(() => !!releaseRead); open(target.cardId); await settle(() => stateText().includes("Card UUID: " + target.cardId));
        const focus = document.activeElement; releaseRead!(); await new Promise(resolve => setTimeout(resolve, 20));
        check(stateText().includes("Card UUID: " + target.cardId) && document.activeElement === focus && announcement() === "", "Late created-child read painted/focused the newly selected card");
      } else if (scenario.endsWith("floor") || scenario === "wrong-incarnation" || scenario === "failed-read") {
        await settle(() => document.querySelector('[aria-label="Card details"]')!.textContent!.includes("Current card unavailable"));
        check(announcement() === "" && !stateText().includes("Card UUID: " + createdId) && followReads === 1, scenario + " accepted a response below receipt floor or wrong identity");
      } else {
        await new Promise(resolve => setTimeout(resolve, 20));
        check(followReads === 0 && announcement() === "" && (scenario === "close-before-receipt" ? (document.querySelector('[aria-label="Card details"]') as HTMLElement).hidden === true : stateText().includes("Card UUID: " + (scenario === "navigate-before-receipt" || scenario === "reload-before-receipt" ? target.cardId : child.cardId))), scenario + " receipt switched selection after navigation/generation changed");
      }
      const retainedTitle = scenario === "reload-before-receipt" ? document.querySelector<HTMLInputElement>('input[name="title"]')! : title;
      check(retainedTitle.isConnected && retainedTitle.value === "Fenced new child" && find("Save").disabled && mounted.state.mutations === 1 && mounted.state.lookups === 0, scenario + " lost private original or replayed mutation");
    } finally { releaseRead?.(); activeView.destroy(); mounted.client.close(); }
  }
  return checks;
}
