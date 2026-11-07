// Production component handlers with controlled asynchronous responses. Real
// selected-board/native journeys are separate from these boundary probes.
import type { Identity } from "../../generated/archive/contracts.js";
import type { NoticePair, NoticeText, NoticeView } from "../../generated/transport/contracts.js";
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
  return checks + await runOriginalCopyProbe();
}

// Controlled component/clipboard proof only: this deliberately replaces the OS
// API and gesture state. Native user-gesture/clipboard evidence is separate.
async function runOriginalCopyProbe(): Promise<number> {
  let checks = 0;
  const check = (condition: boolean, label: string): void => { if (!condition) throw Error(label); checks++; };
  const payload: NoticeText = { version: 1, kind: "complete",
    facts: { label: "Original Ω", from: "2026-11-07T00:00:00Z", through: "2026-11-07T00:00:01Z", tickets: [7, 9] },
    text: "Board: Original Ω\nObserved: 2026-11-07T00:00:00Z to 2026-11-07T00:00:01Z\nTickets: #7, #9" };
  const pair: NoticePair = { ...pending, acceptedAt: "2026-11-07T00:00:02Z", payload };
  let entries = [structuredClone(pair)], reads = 0, holdRead = false, failure: unknown;
  let releaseRead: ((value: NoticeView) => void) | undefined;
  let writeMode = "success", active = true, clipboardAvailable = true;
  const writes: string[] = [];
  let resolveWrite: (() => void) | undefined, rejectWrite: (() => void) | undefined;
  const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, "clipboard");
  const activationDescriptor = Object.getOwnPropertyDescriptor(navigator, "userActivation");
  Object.defineProperty(navigator, "userActivation", { configurable: true, get: () => ({ isActive: active }) });
  Object.defineProperty(navigator, "clipboard", { configurable: true, get: () => clipboardAvailable ? {
    writeText: (text: string): Promise<void> => {
      writes.push(text);
      if (writeMode === "denied") return Promise.reject(new DOMException("Controlled", "NotAllowedError"));
      if (writeMode === "held") return new Promise((resolve, reject) => { resolveWrite = resolve; rejectWrite = () => reject(Error("Controlled")); });
      return Promise.resolve();
    },
  } : undefined });
  const view = (): NoticeView => ({ ...structuredClone(original), entries: structuredClone(entries) });
  const notices = new BoardNotices({ notices: async (_identity, command) => {
    if (command.action !== "inbox") return { ...structuredClone(original), entries: [] };
    reads++;
    if (failure) throw failure;
    if (holdRead) return new Promise(resolve => { releaseRead = resolve; });
    return view();
  } });
  const root = document.createElement("div"); root.append(notices.host); document.body.append(root);
  const button = (label: string): HTMLButtonElement => [...root.querySelectorAll("button")].find(node => node.textContent === label)!;
  const field = (): HTMLInputElement => root.querySelector("input")!;
  const load = async (): Promise<void> => {
    holdRead = false; failure = undefined; entries = [structuredClone(pair)];
    notices.boardRead(identity); button("Notices").click(); button("Read inbox").click(); await tick();
  };
  try {
    await load(); field().value = targetB; field().focus();
    button("Copy original text").click(); await tick();
    check(reads === 2 && writes.length === 1 && writes[0] === payload.text, "Copy requires fresh inbox and writes exact captured UTF-8 text without LF");
    check(root.textContent!.includes("Original notice text copied.") && document.activeElement === field() && field().value === targetB, "Resolved clipboard alone announces success and preserves focus/draft");
    await load(); holdRead = true; writeMode = "held";
    const focusedCopy = button("Copy original text"); focusedCopy.focus(); focusedCopy.click(); await tick();
    check(document.activeElement === focusedCopy && !focusedCopy.disabled && focusedCopy.getAttribute("aria-disabled") === "true", "Focused Copy retains focus while fresh read is pending");
    const pendingReads = reads; focusedCopy.click(); await tick();
    check(reads === pendingReads, "Transient aria-disabled Copy cannot dispatch another read");
    releaseRead!(view()); await tick();
    check(document.activeElement === focusedCopy && !focusedCopy.disabled && focusedCopy.getAttribute("aria-disabled") === "true", "Focused Copy retains focus while clipboard promise is pending");
    resolveWrite!(); await tick();
    check(document.activeElement === focusedCopy && focusedCopy.getAttribute("aria-disabled") === "false" && root.textContent!.includes("Original notice text copied."), "Focused Copy retains focus after resolved clipboard success");
    holdRead = false; writeMode = "success";
    for (const scenario of ["changed", "deleted", "legacy", "read-failed", "refused"]) {
      await load(); const before = writes.length;
      if (scenario === "changed") entries = [{ ...entries[0]!, payload: { version: 1, kind: "fallback", facts: null, text: "Inspect the selected board for changes." } }];
      if (scenario === "deleted") entries = [];
      if (scenario === "legacy") entries = [{ ...entries[0]!, payload: null }];
      if (scenario === "read-failed") failure = Error("Controlled");
      if (scenario === "refused") failure = new NoticeRefusal("refused");
      button("Copy original text").click(); await tick();
      check(writes.length === before && !root.textContent!.includes("Original notice text copied."), "Fresh " + scenario + " cannot use cached text");
      if (scenario === "refused") check(!root.textContent!.includes(payload.text) && !button("Copy original text"), "Known refusal clears Copy evidence");
    }
    await load(); entries = [{ ...pair, payload: null }]; button("Read inbox").click(); await tick();
    check(button("Copy original text").disabled && root.textContent!.includes("legacy notice"), "Legacy original is inspectable and unavailable to Copy");
    entries = [{ ...pair, payload: { version: 1, kind: "fallback", facts: null, text: "Inspect the selected board for changes." } }];
    button("Read inbox").click(); await tick(); button("Copy original text").click(); await tick();
    check(writes.at(-1) === "Inspect the selected board for changes.", "Captured new fallback is copiable exactly");
    for (const scenario of ["unsupported", "expired", "denied"]) {
      await load(); const before = writes.length;
      clipboardAvailable = scenario !== "unsupported"; active = scenario !== "expired"; writeMode = scenario === "denied" ? "denied" : "success";
      button("Copy original text").click(); await tick();
      check(!root.textContent!.includes("Original notice text copied.") && writes.length === before + (scenario === "denied" ? 1 : 0), "Clipboard " + scenario + " remains honest");
      clipboardAvailable = active = true; writeMode = "success";
    }
    for (const phase of ["read", "write"]) {
      for (const transition of ["close", "selection", "window", "authority", "incarnation"]) {
        await load(); const before = writes.length;
        holdRead = phase === "read"; writeMode = phase === "write" ? "held" : "success";
        button("Copy original text").click(); await tick();
        check(!root.textContent!.includes("Original notice text copied."), "Pending " + phase + " cannot announce success");
        if (transition === "close") button("Close notices").click();
        if (transition === "selection") { notices.selectionChanging(true); notices.boardRead({ boardId: id(9), incarnation: id(10) }); notices.selectionChanging(false); notices.selectionChanging(true); notices.boardRead(identity); notices.selectionChanging(false); }
        if (transition === "window") { notices.suspend(); notices.resume(); }
        if (transition === "authority") { notices.reset(); notices.boardRead(identity); }
        if (transition === "incarnation") { notices.boardRead({ ...identity, incarnation: id(11) }); notices.boardRead(identity); }
        button("Notices").click();
        if (phase === "read") releaseRead!(view()); else resolveWrite!();
        await tick();
        check(writes.length === before + (phase === "write" ? 1 : 0) && !root.textContent!.includes("Original notice text copied.") && !root.textContent!.includes(payload.text), "Late " + phase + " after " + transition + " is fenced, without promising OS undo");
        check(field().value === targetB, "Copy transition preserves source-bound draft");
        writeMode = "success";
      }
    }
    await load(); writeMode = "held"; field().focus(); button("Copy original text").click(); await tick(); rejectWrite!(); await tick();
    check(!root.textContent!.includes("Original notice text copied.") && document.activeElement === field() && field().value === targetB, "Rejected held clipboard preserves focus/draft and never announces success");
  } finally {
    notices.suspend(); root.remove();
    if (clipboardDescriptor) Object.defineProperty(navigator, "clipboard", clipboardDescriptor); else Reflect.deleteProperty(navigator, "clipboard");
    if (activationDescriptor) Object.defineProperty(navigator, "userActivation", activationDescriptor); else Reflect.deleteProperty(navigator, "userActivation");
  }
  return checks;
}
