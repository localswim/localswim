// Synthetic actual-DOM component evidence; never a product qualification claim.
import type { BoardRead, CardRead, HistoryRead } from "../../generated/transport/contracts.js";
import type { Snapshot } from "../../generated/archive/contracts.js";
import { CardDetails, priorHistory, sortedComments } from "./details.js";

export async function runCardDetailsProbe(boardInput: BoardRead): Promise<number> {
  const board = structuredClone(boardInput).result, card = { ...board.cards[0]!, revision: 26, description: "<script>inert</script>\nSecond line" };
  const identity = board.identity, actor = board.actors[0]!;
  const id = (value: number): string => "00000000-0000-4000-8000-" + String(value).padStart(12, "0");
  const comments = Array.from({ length: 25 }, (_, index) => ({ actorId: actor.actorId, cardId: card.cardId, commentId: id(index + 1),
    createdAt: "2026-10-29T00:00:00Z", createdIn: identity, text: "Comment " + (index + 1) + "\n<em>inert</em>" })).reverse();
  const records: Snapshot[] = Array.from({ length: 26 }, (_, index) => ({ actorId: actor.actorId, cardId: card.cardId,
    cardRevision: index + 1, committedAt: "2026-10-29T00:00:00Z", origin: identity,
    state: { ...card, title: "Prior " + (index + 1) }, removal: null,
    references: { commentIds: comments.filter(row => row.commentId <= id(index)).map(row => row.commentId), linkIds: [], attachmentIds: [], approvalIds: [] } }));
  const current: CardRead = { apiVersion: 1, scope: "card", result: { ...board, card, comments, historyReferences: [] } };
  const priorOnlyComment = { ...comments[0]!, commentId: id(26), text: "Retained history-only comment" };
  const history: HistoryRead = { apiVersion: 1, scope: "history", result: { ...board, cardId: card.cardId, cardRevision: 26, comments: [...comments, priorOnlyComment], records, tombstones: [] } };
  let mode = "normal", reads = 0;
  const pending: Array<() => void> = [];
  const client = {
    card: async (_boardId: string, cardId: string): Promise<CardRead> => {
      reads++; const value = structuredClone(current); Object.assign(value.result.card, { cardId });
      if (mode === "late") await new Promise<void>(resolve => pending.push(resolve));
      if (mode === "unavailable") throw Error("Synthetic read unavailable"); return value;
    },
    history: async (_boardId: string, cardId: string): Promise<HistoryRead> => {
      const value = structuredClone(history); Object.assign(value.result, { cardId });
      if (mode === "mismatch") Object.assign(value.result, { revision: value.result.revision + 1 });
      if (mode === "wrong-origin") Object.assign(value.result.identity, { incarnation: id(400) });
      if (mode === "empty") Object.assign(value.result, { records: [], comments: [] });
      if (mode === "late") await new Promise<void>(resolve => pending.push(resolve));
      if (mode === "history-unavailable") throw Error("Synthetic history unavailable"); return value;
    },
  };
  const root = document.createElement("main"), fallback = document.createElement("button"), opener = document.createElement("button"), editor = document.createElement("textarea");
  fallback.textContent = "Read current board"; opener.dataset.openCard = card.cardId; opener.textContent = "Open card"; editor.value = "Private draft";
  const details = new CardDetails(client, fallback); root.append(fallback, opener, details.host, editor); document.body.replaceChildren(root);
  const flush = async (): Promise<void> => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); };
  const button = (label: string): HTMLButtonElement => [...details.host.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
  const commentsRegion = (): HTMLElement => details.host.querySelector('[aria-label="Card comments"]')!;
  const historyRegion = (): HTMLElement => details.host.querySelector('[aria-label="Prior card history"]')!;
  const historyComments = (): HTMLElement => details.host.querySelector('[aria-label="History comments"]')!;
  let checks = 0;
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  try {
    details.open(card.cardId, identity, opener); await flush();
    check(details.host.getAttribute("role") === "region" && !details.host.hasAttribute("aria-modal"), "Details must be nonmodal");
    check(details.host.textContent!.includes(card.description) && !details.host.querySelector("script,em"), "Prose was altered or executed");
    check(commentsRegion().querySelectorAll("article").length === 20 && commentsRegion().textContent!.includes("1–20 of 25 returned"), "First comment page mismatch");
    check(historyRegion().querySelectorAll("article").length === 20 && historyRegion().querySelector("h4")!.textContent!.endsWith("revision 25"), "Prior history order/current exclusion mismatch");
    check(!historyRegion().textContent!.includes("<em>inert</em>") && historyComments().querySelectorAll("article").length === 20 &&
      [...historyComments().querySelectorAll("article")].every(row => (row.textContent!.match(/<em>inert<\/em>/g) ?? []).length === 1), "Cumulative snapshot references expanded duplicate comment bodies");
    button("Next history comments").click();
    check(historyComments().querySelectorAll("article").length === 6 && historyComments().textContent!.includes("21–26 of 26 returned") &&
      historyComments().textContent!.includes(priorOnlyComment.text), "Last retained historical comment was unreachable");
    button("Next comments").click();
    check(commentsRegion().querySelectorAll("article").length === 5 && commentsRegion().textContent!.includes("21–25 of 25 returned") && !button("Previous comments").disabled, "Comment continuation mismatch");
    check(document.activeElement === button("Previous comments"), "Paging lost keyboard focus");
    button("Next prior history").click();
    check(historyRegion().querySelectorAll("article").length === 5 && historyRegion().textContent!.includes("21–25 of 25 returned"), "History continuation mismatch");
    editor.focus(); await details.refresh();
    check(document.activeElement === editor && editor.value === "Private draft" && commentsRegion().textContent!.includes("1–20 of 25") &&
      historyComments().textContent!.includes("1–20 of 26"), "Refresh stole input/focus or kept old page");
    check(sortedComments(comments)[0]!.commentId === id(1), "Equal-second comment ID ordering mismatch");
    const foreign = { ...records[0]!, origin: { boardId: identity.boardId, incarnation: id(300) }, cardRevision: 1000 };
    const sorted = priorHistory([foreign, ...records], identity, card.cardId, 26);
    check(sorted[0]!.cardRevision === 25 && sorted.at(-1) === foreign, "Origins were compared as one revision timeline");
    mode = "mismatch"; await details.refresh();
    check(details.host.textContent!.includes("History stale") && !historyRegion().querySelector("article"), "Mismatched snapshots combined");
    check(!historyComments().querySelector("article"), "Stale history retained comment facts");
    mode = "wrong-origin"; await details.refresh();
    check(details.host.textContent!.includes("History stale") && !historyRegion().querySelector("article"), "Incarnations combined");
    mode = "history-unavailable"; await details.refresh();
    check(details.host.textContent!.includes("History unavailable") && !historyRegion().textContent!.includes("No prior history"), "Unavailable history looked empty");
    mode = "empty"; await details.refresh();
    check(historyRegion().textContent!.includes("No prior history returned"), "Empty history missing");
    mode = "normal";
    const gap = { class: "history" as const, origin: identity, reason: "cleared" as const, boardRevision: 9,
      fromExclusive: null, throughInclusive: "2026-10-28T00:00:00Z", recordedAt: "2026-10-29T00:00:00Z" };
    Object.assign(history.result, { records: [{ ...records[0]!, state: null, removal: { reason: "cleared", boardRevision: 9, removedAt: gap.recordedAt }, references: { commentIds: [id(800)], linkIds: [], attachmentIds: [], approvalIds: [] } }],
      coverage: { ...history.result.coverage, historyFloor: gap.throughInclusive, gaps: [gap] },
      tombstones: [{ kind: "comment", id: id(800), cardId: card.cardId, createdIn: identity, actorId: null, createdAt: null, reason: "cleared", removedAt: gap.recordedAt }] });
    await details.refresh();
    check(historyRegion().textContent!.includes("Coverage gap: history") && historyRegion().textContent!.includes("Old text unavailable; compact or removed state.") &&
      historyRegion().textContent!.includes("Removed comment " + id(800)) && !historyRegion().textContent!.includes(card.description), "Missing/removed old text was hidden or reconstructed");
    mode = "unavailable"; await details.refresh();
    check(details.host.textContent!.includes("confirmed facts are stale"), "Unavailable current read lacked stale disclosure");
    mode = "normal"; details.open(card.cardId, identity, opener); await flush();
    mode = "late"; details.open(id(902), identity, opener);
    check(details.host.querySelector("h2")!.textContent === "Card details · " + id(902) && !historyComments().querySelector("article"), "New loading selection retained old title or historical comments");
    pending.splice(0).forEach(release => release()); await flush();
    mode = "unavailable"; details.open(id(903), identity, opener); await flush();
    check(details.host.querySelector("h2")!.textContent === "Card details · " + id(903), "Failed new selection retained old title");
    mode = "late"; details.open(card.cardId, identity, opener);
    mode = "normal"; details.open(id(901), identity, opener); await flush(); pending.splice(0).forEach(release => release()); await flush();
    check(details.host.textContent!.includes("Card UUID: " + id(901)) && !details.host.textContent!.includes("Card UUID: " + card.cardId), "Late old selection painted newer selection");
    mode = "late"; details.open(card.cardId, identity, opener); const before = reads; details.suspend(); pending.splice(0).forEach(release => release()); await flush();
    check(details.host.textContent!.includes("Card details stale") && !details.host.querySelector('[aria-label="Current card state"] article'), "Suspended response painted");
    details.resume(); mode = "normal"; details.open(id(900), identity, opener); await flush();
    check(details.host.textContent!.includes("Card UUID: " + id(900)) && !details.host.textContent!.includes("Card UUID: " + card.cardId), "New selection contains old card");
    check(reads === before + 1, "Unexpected automatic retry");
    details.close(); check(document.activeElement === opener, "Close did not restore visible opener");
    details.open(card.cardId, identity, opener); await flush(); opener.remove(); details.close();
    check(document.activeElement === fallback, "Missing opener did not use workspace fallback");
    details.open(card.cardId, identity, opener); await flush();
    const beforeArchiveRead = reads;
    details.refreshAfterArchive({ ...identity, incarnation: id(700) }, card.cardId, 1, 1);
    details.refreshAfterArchive(identity, id(701), 1, 1);
    check(reads === beforeArchiveRead, "Archive receipt from another context refreshed selected details");
    details.refreshAfterArchive(identity, card.cardId, current.result.revision + 1, current.result.card.revision + 1); await flush();
    check(details.host.textContent!.includes("confirmed facts are stale") && details.host.textContent!.includes("archived: false"), "Pre-commit focused read was presented as current archive state");
    Object.assign(current.result, { revision: current.result.revision + 1 });
    Object.assign(current.result.card, { revision: current.result.card.revision + 1, archived: true });
    editor.focus();
    details.refreshAfterArchive(identity, card.cardId, current.result.revision, current.result.card.revision); await flush();
    check(details.host.textContent!.includes("archived: true") && document.activeElement === editor && editor.value === "Private draft", "Fresh archive read failed to update status or stole private input focus");
    mode = "late"; details.refreshAfterArchive(identity, card.cardId, current.result.revision, current.result.card.revision);
    mode = "normal"; Object.assign(current.result.card, { archived: false }); details.open(id(904), identity, opener); await flush();
    pending.splice(0).forEach(release => release()); await flush();
    check(details.host.textContent!.includes("Card UUID: " + id(904)) && details.host.textContent!.includes("archived: false"), "Late post-archive read painted another selection");
    details.suspend(); const suspendedReads = reads;
    details.refreshAfterArchive(identity, id(904), 1, 1);
    check(reads === suspendedReads, "Suspended archive receipt triggered a focused read");
    return checks;
  } finally { details.suspend(); root.remove(); }
}
