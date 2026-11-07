// Production component handlers with controlled asynchronous responses. Real
// selected-board/native journeys are separate from these boundary probes.
import type { Identity } from "../../generated/archive/contracts.js";
import type { NoticeView } from "../../generated/transport/contracts.js";
import { BoardClient } from "../api/client.js";
import { NoticeRefusal } from "../api/notices.js";
import type { NoticeCommand } from "../api/notices.js";
import { BoardNotices } from "./notices.js";

const id = (n: number): string => "00000000-0000-4000-8000-" + String(n).padStart(12, "0");
const identity: Identity = { boardId: id(1), incarnation: id(2) };
const targetA = id(3), targetB = id(4), deliveryId = "a".repeat(64);
const pending = { targetId: targetA, deliveryId, class: "human" as const, lowerEvent: 0, upperEvent: 1, eventCount: 1 };
const original: NoticeView = { identity, controlGeneration: id(5), configurationRevision: 1, targetId: targetA,
  state: "guarded", reason: "unknown", checkpoint: 0, gapCount: 0, last: null, pending,
  controlOperation: null, entries: [], lookup: null };
const tick = async (): Promise<void> => { await new Promise(resolve => setTimeout(resolve, 0)); };
export async function runNoticesProbe(): Promise<number> {
  let checks = 0;
  const check = (condition: boolean, label: string): void => { if (!condition) throw Error(label); checks++; };
  const calls: NoticeCommand[] = [];
  let value = structuredClone(original), failure: unknown, held: ((view: NoticeView) => void) | undefined;
  let hold = false;
  const notices = new BoardNotices({ notices: async (_identity, command) => {
    calls.push(command);
    if (hold) return new Promise(resolve => { held = resolve; });
    if (failure) throw failure;
    return structuredClone(value);
  } });
  const root = document.createElement("div"); root.append(notices.host); document.body.append(root);
  const button = (label: string): HTMLButtonElement => [...root.querySelectorAll("button")].find(node => node.textContent === label)!;
  const field = (label: string): HTMLInputElement => [...root.querySelectorAll("label")].find(node => node.firstChild?.textContent === label)!.querySelector("input")!;
  const set = (label: string, text: string): void => { const node = field(label); node.value = text; node.dispatchEvent(new Event("input")); };
  const open = (): void => { button("Notices").click(); };
  const read = async (): Promise<void> => { button("Read status").click(); await tick(); };
  try {
    notices.boardRead(identity); open(); await read(); set("Future target ID", "invalid");
    button("Retarget").click(); await tick();
    check(calls.length === 1, "Invalid future target must never dispatch");
    set("Future target ID", targetB);
    const future = field("Future target ID"); future.focus();
    value = { ...original, targetId: targetB, configurationRevision: 2, controlGeneration: id(6) };
    button("Retarget").click(); button("Reload").click(); await tick();
    const retarget = calls.at(-1)!;
    check(calls.length === 2 && retarget.action === "retarget" && retarget.targetId === targetB && retarget.expectedConfigurationRevision === 1 && retarget.expectedControlGeneration === original.controlGeneration, "Retarget must use displayed exact versions once");
    check(document.activeElement === future && future.value === targetB, "Target draft and keyboard focus must survive control");
    check(root.textContent!.includes("Future target " + targetB + " · configuration revision 2") && root.textContent!.includes("Original pending · target " + targetA) && root.textContent!.includes(deliveryId), "Future configuration must not relabel original pending");
    check(button("Reload").getAttribute("aria-disabled") === "true", "Control response must require deliberate new status");
    await read();
    failure = Error("controlled lost result"); button("Reload").click(); await tick();
    check(root.textContent!.includes("Control outcome UNKNOWN") && root.textContent!.includes("may have taken effect") && root.textContent!.includes(deliveryId), "Lost response must retain original evidence and report unknown");
    failure = undefined; await read();
    check(root.textContent!.includes("Original reload response · Control outcome UNKNOWN") && calls.filter(c => c.action === "reload").length === 1, "Later status must retain original unknown and never replay");
    for (const kind of ["stale", "busy", "refused"] as const) {
      failure = new NoticeRefusal(kind); button("Retarget").click(); await tick();
      check(button("Retarget").getAttribute("aria-disabled") === "true" && future.value === targetB, "Refusal must retain draft and require status");
      failure = undefined; await read();
    }
    value = { ...value, state: "partial-drain", controlOperation: "reload" }; button("Reload").click(); await tick();
    check(root.textContent!.includes("Partial drain") && root.textContent!.includes("incomplete; read status for eventual state"), "Partial response must explain eventual status");
    value = { ...value, state: "stopped", reason: null, controlOperation: null }; await read();
    set("Target ID", targetA); set("Delivery ID", deliveryId);
    value = { ...value, lookup: { state: "accepted", receipt: { ...pending, acceptedAt: "2026-11-07T00:00:00Z" } } };
    button("Look up receipt").click(); await tick();
    check(root.textContent!.includes("accepted 2026-11-07T00:00:00Z") && root.textContent!.includes("target " + targetA), "Exact old-target receipt must show original accepted time");
    value = { ...value, lookup: null };
    for (const action of ["Read status", "Reload", "Read inbox", "Look up receipt"]) {
      for (const transition of ["close", "selection", "window", "authority", "incarnation"]) {
        hold = false; await read(); hold = true;
        button(action).click(); await tick(); const release = held!;
        check(typeof release === "function", "Controlled production handler must be in flight");
        if (transition === "close") { button("Close notices").click(); open(); }
        if (transition === "selection") { notices.selectionChanging(true); notices.boardRead({ boardId: id(9), incarnation: id(10) }); notices.selectionChanging(false); notices.selectionChanging(true); notices.boardRead(identity); notices.selectionChanging(false); open(); }
        if (transition === "window") { notices.suspend(); notices.resume(); open(); }
        if (transition === "authority") { notices.reset(); notices.boardRead(identity); open(); }
        if (transition === "incarnation") { notices.boardRead({ ...identity, incarnation: id(11) }); notices.boardRead(identity); open(); }
        release({ ...value, targetId: id(99), configurationRevision: 99,
          entries: action === "Read inbox" ? [{ ...pending, targetId: id(99), acceptedAt: "2099-01-01T00:00:00Z" }] : [],
          lookup: action === "Look up receipt" ? { state: "accepted", receipt: { ...pending, targetId: id(99), acceptedAt: "2099-01-01T00:00:00Z" } } : null }); await tick();
        check(!root.textContent!.includes(id(99)) && !root.textContent!.includes("revision 99") && !root.textContent!.includes("2099-01-01") && root.textContent!.includes("Choose Read status before using a control.") && button("Reload").getAttribute("aria-disabled") === "true", "Late " + action + " must be fenced after " + transition);
        check(field("Future target ID").value === targetB && field("Target ID").value === targetA, "Context transitions must retain source-bound drafts");
      }
    }
    hold = false; await read(); button("Close notices").focus(); button("Close notices").click();
    check(document.activeElement === button("Notices"), "Close must return keyboard focus");
    open(); field("Future target ID").focus();
    field("Future target ID").dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    check(document.activeElement === button("Notices") && button("Notices").getAttribute("aria-expanded") === "false", "Escape must return focus and close without clearing target draft");
  } finally { notices.suspend(); root.remove(); }

  let credential = "connection-A", wire: unknown = { apiVersion: 1, scope: "notices", result: original }, status = 200;
  let captured: RequestInit | undefined, fetchCount = 0;
  const client = await BoardClient.open({ installationId: id(20), profileId: id(21), transport: "local-cookie", credential: () => credential,
    selection: { mode: "standalone", ...identity, actorId: id(22), grantId: id(23) },
    fetch: async (_url, init) => { captured = init; fetchCount++; return new Response(JSON.stringify(wire), { status, headers: { "Cache-Control": "no-store", "Content-Type": "application/json" } }); } });
  const command = { action: "retarget" as const, targetId: targetB, expectedControlGeneration: original.controlGeneration, expectedConfigurationRevision: 1 };
  try {
    await client.notices(identity, command, new AbortController().signal);
    check(fetchCount === 1 && captured?.method === "POST" && captured.credentials === "same-origin" && captured.redirect === "error" && captured.cache === "no-store" && captured.body === JSON.stringify({ incarnation: identity.incarnation, targetId: targetB, expectedControlGeneration: original.controlGeneration, expectedConfigurationRevision: 1 }), "Production API must serialize strict retarget once with selected transport");
    status = 503; wire = { apiVersion: 1, outcome: "unknown", error: { code: "OUTCOME_UNKNOWN", message: "Notice request outcome is unknown. Read notices status before taking further action." } };
    let unknown: unknown; try { await client.notices(identity, command, new AbortController().signal); } catch (error) { unknown = error; }
    check(unknown instanceof Error && !(unknown instanceof NoticeRefusal) && fetchCount === 2, "Exact post-admission 503 must be unknown with no retry");
    wire = { apiVersion: 1, outcome: "refused", conflict: null, error: { code: "UNAVAILABLE", message: "Unavailable", field: null } };
    let refusal: unknown; try { await client.notices(identity, command, new AbortController().signal); } catch (error) { refusal = error; }
    check(refusal instanceof NoticeRefusal && fetchCount === 3, "Exact validated pre-admission 503 must remain refusal");
    wire = { apiVersion: 1, error: { code: "UNAVAILABLE", message: "Unavailable" } };
    let malformed: unknown; try { await client.notices(identity, command, new AbortController().signal); } catch (error) { malformed = error; }
    check(malformed instanceof Error && !(malformed instanceof NoticeRefusal) && fetchCount === 4, "Malformed refusal cannot prove no effect and must not retry");
    credential = "connection-B";
  } finally { client.close(); }
  return checks;
}
