import type { BoardRead, CardSearchRead, SessionRead } from "../../generated/transport/contracts.js";
import { BoardClient } from "../api/client.js";
import { emptySearch, validSearch } from "../api/search.js";
import { CardSearch } from "./search.js";
import type { PrivateSearch } from "./search.js";
import { WindowState } from "../session-ui/window-state.js";

// The private runner activates these controls through Chromium's native keyboard.
export async function mountSearchKeyboardProbe(identity: BoardRead["result"]["identity"]) {
  const archive = document.createElement("select");
  for (const value of ["active", "archived", "all"]) { const option = document.createElement("option"); option.value = value; archive.append(option); }
  let complete: ((value: CardSearchRead) => void) | undefined, fail: ((error: Error) => void) | undefined, sequence = 0;
  const fake = { search: (_: string, query: ReturnType<typeof emptySearch>) => new Promise<CardSearchRead>((resolve, reject) => {
    sequence++; complete = resolve; fail = reject;
    if (sequence === 1) resolve(page(query, false));
  }) } as unknown as BoardClient;
  function page(query = emptySearch(), terminal = false): CardSearchRead {
    const start = sequence * 20;
    return { apiVersion: 1, scope: "card-search", result: { identity, revision: 1, query, nextCursor: terminal ? null : "cursor-" + sequence,
      items: Array.from({ length: terminal ? 1 : 20 }, (_, index) => ({ cardId: "00000000-0000-4000-8000-" + (start + index).toString().padStart(12, "0"), ticket: start + index, revision: 1, title: "Keyboard result", lane: "Ready for Work", priority: "P3", ownerId: null, archived: false })) } };
  }
  const search = new CardSearch(fake, identity.boardId, archive, () => {}), editor = document.createElement("input"); editor.setAttribute("aria-label", "Private editor focus probe");
  document.body.replaceChildren(search.host, editor); search.boardRead(identity, 1);
  [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === "Apply filters")!.click();
  await new Promise(resolve => setTimeout(resolve, 0));
  return { finish: (terminal = false) => complete?.(page(emptySearch(), terminal)), fail: () => fail?.(Error("Controlled failed search")), stop: () => search.suspend() };
}

// Controlled actual-DOM and concrete client checks, not ordinary product proof.
export async function runSearchProbe(board: BoardRead, session: SessionRead): Promise<number> {
  let checks = 0;
  const assert = (value: unknown, message: string): void => { if (!value) throw Error(message); checks++; };
  const filters = emptySearch(), identity = board.result.identity;
  const summary = (ticket: number) => ({ cardId: "00000000-0000-4000-8000-" + ticket.toString().padStart(12, "0"), ticket, revision: 1, title: "CAFÉ 船 <script>", priority: "P3" as const, lane: "Ready for Work" as const, ownerId: null, archived: false });
  let wire = "", mode = "ok", calls = 0;
  const response = (query = filters, start = 1, count = 1, cursor: string | null = null): CardSearchRead => ({ apiVersion: 1, scope: "card-search", result: { identity, revision: board.result.revision, query, items: Array.from({ length: count }, (_, index) => summary(start + index)), nextCursor: cursor } });
  const client = await BoardClient.open({ installationId: "00000000-0000-4000-8000-000000000090", profileId: "synthetic-search", credential: () => "synthetic-search-token", databaseName: "search-" + crypto.randomUUID(), transport: "local-cookie", selection: { mode: "standalone", boardId: identity.boardId, incarnation: identity.incarnation, actorId: session.actorId, grantId: session.grant.grantId }, fetch: (async (url) => {
    calls++; wire = String(url); const params = new URL(wire, "http://localhost").searchParams;
    const read = response({ q: params.get("q")!, archive: params.get("archive") as typeof filters.archive, lane: params.get("lane") as typeof filters.lane, owner: params.get("owner"), priority: params.get("priority") as typeof filters.priority });
    if (mode === "echo") Object.assign(read.result.query, { q: "wrong" });
    if (mode === "filters") Object.assign(read.result.query, { lane: "Completed" });
    if (mode === "identity") Object.assign(read.result, { identity: { ...identity, boardId: "00000000-0000-4000-8000-000000000099" } });
    if (mode === "order") Object.assign(read.result, { items: [summary(2), summary(1)] });
    if (mode === "duplicate") Object.assign(read.result, { items: [summary(1), summary(1)] });
    if (mode === "oversized") Object.assign(read.result, { items: Array.from({ length: 21 }, (_, i) => summary(i + 1)) });
    if (mode === "schema") Object.assign(read.result.items[0]!, { description: "forbidden full text" });
    return new Response(JSON.stringify(read), { status: mode === "stale" ? 409 : mode === "failure" ? 503 : 200 });
  }) as typeof fetch });
  try {
    for (const q of ["CAFÉ", "İ", "STRASSE", "café", " 船 🚲%_[\\]<script> ", "船".repeat(341) + "a"]) {
      const read = await client.search(identity.boardId, { ...filters, q }); assert(read.result.query.q === q && new URL(wire, "http://localhost").searchParams.get("q") === q, "Query spelling changed");
    }
    const before = calls;
    for (const q of ["船".repeat(341) + "ab", "\ud800"]) { let failed = false; try { await client.search(identity.boardId, { ...filters, q }); } catch { failed = true; } assert(failed && calls === before, "Invalid UTF-8 query dispatched"); }
    assert(validSearch({ ...filters, owner: "unassigned", lane: "Completed", priority: "P0", archive: "all" }) && !validSearch({ ...filters, owner: "bad" }), "Typed filters invalid");
    await client.search(identity.boardId, { ...filters, lane: "Ready for Work", owner: "unassigned", priority: "P3" }, "opaque");
    assert(wire.includes("owner=unassigned") && wire.includes("cursor=opaque"), "Typed filter/cursor not sent");
    for (mode of ["echo", "filters", "identity", "order", "duplicate", "oversized", "schema", "stale", "failure"]) { let failed = false; try { await client.search(identity.boardId, filters); } catch { failed = true; } assert(failed, "Bad search response admitted: " + mode); }
  } finally { client.close(); }
  const root = document.createElement("main"), archive = document.createElement("select");
  for (const value of ["active", "archived", "all"]) { const option = document.createElement("option"); option.value = value; archive.append(option); }
  const pending: { filters: typeof filters; cursor?: string; resolve: (read: CardSearchRead) => void; reject: (reason: Error) => void }[] = [];
  const fake = { search: (_: string, query: typeof filters, cursor?: string) => new Promise<CardSearchRead>((resolve, reject) => pending.push({ filters: query, cursor, resolve, reject })) } as unknown as BoardClient;
  let saved: PrivateSearch | undefined, opened = "";
  const search = new CardSearch(fake, identity.boardId, archive, id => { opened = id; }, state => { saved = structuredClone(state); });
  root.append(search.host); document.body.replaceChildren(root); search.boardRead(identity, board.result.revision);
  const input = root.querySelector<HTMLInputElement>('[aria-label="Search cards"]')!;
  const button = (text: string): HTMLButtonElement => [...root.querySelectorAll("button")].find(row => row.textContent === text)!;
  const tick = async (): Promise<void> => { await new Promise(resolve => setTimeout(resolve, 0)); };
  const edit = (text: string): void => { input.value = text; input.dispatchEvent(new Event("input")); };
  button("Find cards").click(); assert(document.activeElement === input, "Find did not focus input");
  edit("A"); input.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "Enter", isComposing: true })); assert(pending.length === 0, "IME submitted");
  button("Apply filters").click(); edit("B"); button("Apply filters").click();
  pending[0]!.resolve(response(pending[0]!.filters)); await tick(); assert(!root.textContent!.includes("CAFÉ"), "Late A painted B");
  pending[1]!.resolve(response(pending[1]!.filters, 1, 20, "next")); await tick();
  assert(root.querySelectorAll("article").length === 20 && !button("Next results").disabled && input.value === "B", "First page wrong");
  assert(document.activeElement === input, "Search response stole query focus");
  button("Next results").click(); assert(pending[2]!.cursor === "next", "Next lost cursor");
  pending[2]!.resolve(response(pending[2]!.filters, 21)); await tick();
  assert(root.querySelectorAll("article").length === 1 && button("Next results").disabled, "Final page wrong");
  button("Open card #21 CAFÉ 船 <script>").click(); assert(opened === summary(21).cardId && !root.querySelector("script"), "Summary open/inert text failed");
  button("First page").click(); pending[3]!.reject(Error("Search unavailable")); await tick();
  assert(root.textContent!.includes("previous results are stale") && root.querySelectorAll("article").length === 1 && !button("First page").disabled, "Error erased rows/stuck loading");
  button("Apply filters").click(); search.suspend(); pending[4]!.resolve(response(pending[4]!.filters)); await tick(); search.resume();
  assert(root.querySelectorAll("article")[0]!.textContent!.includes("#21"), "Suspended response painted");
  search.invalidate(); assert(button("Next results").disabled, "Commit stale retained Next");
  const memory = new Map<string, string>(), storage = { getItem: (key: string) => memory.get(key) ?? null, setItem: (key: string, text: string) => memory.set(key, text) } as unknown as Storage;
  const window = new WindowState(storage, "navigate"); window.put("exact-board-a", { search: saved });
  const reload = new WindowState(storage, "reload"), other = new WindowState(storage, "navigate");
  assert(reload.get("exact-board-a").search?.draft.q === "B" && !reload.get("exact-board-b").search && !other.get("exact-board-a").search, "Private window/board/reload filter leak");
  assert(!JSON.stringify(reload.get("exact-board-a")).includes("nextCursor"), "Cursor persisted");
  const restored = new CardSearch(fake, identity.boardId, archive, () => {}, () => { throw Error(); }, saved); root.replaceChildren(restored.host); restored.boardRead(identity, board.result.revision);
  assert(pending[5]!.cursor === undefined && pending[5]!.filters.q === "B", "Reload did not request fresh first page");
  pending[5]!.resolve(response(pending[5]!.filters)); await tick(); assert(root.textContent!.includes("memory only"), "Storage denial claimed saved");
  const restoreInput = root.querySelector<HTMLInputElement>('[aria-label="Search cards"]')!;
  restoreInput.value = "船".repeat(342); restoreInput.dispatchEvent(new Event("input")); button("Apply filters").click();
  assert(pending.length === 6 && root.textContent!.includes("Previous results are stale"), "Oversized DOM query dispatched or hid stale state");
  restoreInput.value = "B"; restoreInput.dispatchEvent(new Event("input")); button("Apply filters").click();
  restored.boardRead({ ...identity, incarnation: "00000000-0000-4000-8000-000000000098" }, board.result.revision);
  pending[6]!.resolve(response(pending[6]!.filters)); await tick(); assert(root.querySelectorAll("article").length === 0 && !button("First page").disabled, "Replaced incarnation admitted late results or stuck loading");
  restored.suspend(); search.suspend();
  const firstPending = new CardSearch(fake, identity.boardId, archive, () => {});
  root.replaceChildren(firstPending.host); firstPending.boardRead(identity, board.result.revision);
  const firstQuery = root.querySelector<HTMLInputElement>('[aria-label="Search cards"]')!;
  firstQuery.value = "retained first query"; firstQuery.dispatchEvent(new Event("input")); button("Apply filters").click();
  firstPending.boardRead(identity, board.result.revision + 1);
  pending[7]!.resolve(response(pending[7]!.filters, 1, 20, "old-cursor")); await tick();
  assert(root.querySelectorAll("article").length === 0 && root.textContent!.includes("older than current board facts") && button("Next results").disabled && !button("First page").disabled && firstQuery.value === "retained first query", "Pending first-page response predating a current board read was accepted or lost input");
  button("First page").click(); const fresh = response(pending[8]!.filters); Object.assign(fresh.result, { revision: board.result.revision + 1 }); pending[8]!.resolve(fresh); await tick();
  assert(root.querySelectorAll("article").length === 1 && root.textContent!.includes("End of results"), "First page did not recover explicitly after old snapshot refusal");
  firstPending.suspend(); root.remove(); return checks;
}
