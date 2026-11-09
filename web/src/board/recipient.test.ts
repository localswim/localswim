// Actual production DOM handlers, controlled asynchronous client responses.
// The primary runs this probe in qualified Chrome; it is not ordinary HTTP proof.
import type { InstallationInspectionResult, RecipientView } from "../../generated/transport/contracts.js";
import type { Identity } from "../../generated/archive/contracts.js";
import { RecipientRefusal, RecipientUnknown } from "../api/recipient.js";
import type { RecipientCommand } from "../api/recipient.js";
import { BoardRecipient, RecipientMemory } from "./recipient.js";

const unexpectedProbe = {
  checkInstalledVersion: async (): Promise<never> => { throw Error("Unexpected identity check"); },
  stopInstalledVersion: async (): Promise<never> => { throw Error("Unexpected identity stop"); },
};
import { InstallationInspectionRefusal } from "../api/installation.js";
const id = (n: number): string => "00000000-0000-4000-8000-" + String(n).padStart(12, "0");
const context = { installationId: id(1), boardId: id(2), incarnation: id(3), actorId: id(4) };
const empty: RecipientView = { identity: { boardId: context.boardId, incarnation: context.incarnation }, runtimeGeneration: id(5),
  state: "unconfigured", reason: null, configurationRevision: 0, configurationGeneration: null, requestId: null, input: null };
const tick = async (): Promise<void> => { await new Promise(resolve => setTimeout(resolve, 0)); };
export async function runRecipientProbe(): Promise<number> {
  let checks = 0;
  const check = (condition: boolean, label: string): void => { if (!condition) throw Error(label); checks++; };
  const calls: RecipientCommand[] = [];
  let result = structuredClone(empty), failure: unknown, hold = false;
  let release: ((value: RecipientView) => void) | undefined;
  const client = { ...unexpectedProbe, installationInspection: async (): Promise<never> => { throw Error("Unexpected installation inspection"); }, recipient: async (identity: Identity, command: RecipientCommand): Promise<RecipientView> => {
    calls.push(structuredClone(command));
    if (hold) return new Promise(resolve => { release = resolve; });
    if (failure) throw failure;
    if (command.action === "configure") return { ...empty, identity, state: "configured", configurationRevision: 1,
      configurationGeneration: id(6), requestId: command.requestId, input: structuredClone(command.input) };
    return structuredClone(result);
  } };
  const root = document.createElement("div"), panel = new BoardRecipient(client, new RecipientMemory());
  root.append(panel.host); document.body.append(root);
  const button = (label: string): HTMLButtonElement => [...root.querySelectorAll("button")].find(node => node.textContent === label)!;
  const input = (): HTMLInputElement => root.querySelector("input")!;
  const set = (text: string): void => { input().value = text; input().dispatchEvent(new Event("input")); };
  const open = (): void => button("Recipient policy").click();
  const read = async (): Promise<void> => { button("Read recipient status").click(); await tick(); };
  const save = async (): Promise<void> => { button("Save once").click(); await tick(); };
  try {
    panel.boardRead(context); open();
    check(calls.length === 0, "Opening must not read or save automatically");
    set(id(7)); await save(); check(calls.length === 0, "Save needs deliberate status");
    await read(); set("é".repeat(513)); check(input().value === id(7), "Byte-over-limit input preserves prior bounded draft");
    set("00000000-0000-7000-8000-000000000007"); check(button("Save once").getAttribute("aria-disabled") === "false", "UUIDv7 chat reference accepted without normalization");
    set("invalid"); await save(); check(calls.length === 1, "Invalid reference refuses without POST");
    set(id(7)); input().focus(); await save();
    check(calls.filter(c => c.action === "configure").length === 1, "Exactly one explicit save");
    check(document.activeElement === input() && input().value === id(7), "Save preserves reference and focus");
    check(root.textContent!.includes("Saved; delivery is not activated") && root.textContent!.includes(id(6)) && root.textContent!.includes("conservative-metadata-v1"), "Exact saved facts and no-activation wording");
    await save(); check(calls.filter(c => c.action === "configure").length === 1, "Equivalent fresh save refused locally");

    // Separate identity for uncertain original: no nonce replacement or replay.
    const other = { ...context, boardId: id(8), incarnation: id(9) };
    panel.boardRead(other); open(); result = { ...empty, identity: { boardId: other.boardId, incarnation: other.incarnation } }; await read();
    set(id(10)); failure = new RecipientUnknown(); input().focus(); await save();
    const original = calls.at(-1)!;
    if (original.action !== "configure") throw Error("Original missing");
    check(root.textContent!.includes("Save outcome UNKNOWN") && input().value === id(10) && document.activeElement === input(), "UNKNOWN retains draft/focus");
    await save(); check(calls.filter(c => c.action === "configure").length === 2, "UNKNOWN cannot create new request");
    failure = undefined; result = { ...result, state: "configured", configurationRevision: 1, configurationGeneration: id(11), requestId: id(12), input: original.input }; await read();
    check(root.textContent!.includes("Original save remains UNKNOWN"), "Other request with same input does not settle original");
    result = { ...result, requestId: original.requestId, runtimeGeneration: id(13) }; await read();
    check(root.textContent!.includes("Original save confirmed"), "Exact fresh status after genuine runtime change may settle original");

    for (const [index, code] of ["FORBIDDEN", "CONTROL_STALE", "CONTROL_BUSY"].entries()) {
      const selected = { ...context, boardId: id(20 + index), incarnation: id(30 + index) };
      panel.boardRead(selected); open(); result = { ...empty, identity: { boardId: selected.boardId, incarnation: selected.incarnation } }; await read();
      set(id(40)); input().focus(); failure = new RecipientRefusal(code); await save();
      const before = calls.filter(c => c.action === "configure").length;
      check(root.textContent!.includes(code) && root.textContent!.includes("Original save refused") && input().value === id(40) && document.activeElement === input(), "Pre-admission save refusal retains focus/input " + code);
      await save(); check(calls.filter(c => c.action === "configure").length === before, "Refused save requires deliberate fresh status " + code);
      failure = undefined;
    }
    panel.boardRead(other); open();
    result = { ...empty, identity: { boardId: other.boardId, incarnation: other.incarnation }, state: "configured", configurationRevision: 1,
      configurationGeneration: id(11), requestId: original.requestId, input: original.input }; await read();

    // Stale A-B-A, close, authority and window replies cannot repaint private facts.
    for (const transition of ["selection", "close", "authority", "window"]) {
      hold = true; button("Read recipient status").click(); await tick(); const finish = release!;
      if (transition === "selection") { panel.selectionChanging(true); panel.boardRead(context); panel.selectionChanging(false); panel.selectionChanging(true); panel.boardRead(other); panel.selectionChanging(false); }
      if (transition === "close") button("Close recipient policy").click();
      if (transition === "authority") { panel.reset(); panel.boardRead(other); }
      if (transition === "window") { panel.suspend(); panel.resume(); }
      open(); finish({ ...result, configurationGeneration: id(99) }); await tick();
      check(!root.textContent!.includes(id(99)) && !root.textContent!.includes("Codex chat:"), "Late private response fenced after " + transition);
      check(input().value === id(10), "Draft retained after " + transition);
      hold = false;
    }
    for (const code of ["FORBIDDEN", "CONTROL_STALE", "CONTROL_BUSY"]) {
      failure = new RecipientRefusal(code); input().focus(); await read();
      check(!root.textContent!.includes("Codex chat:") && root.textContent!.includes(code) && input().value === id(10), "Refusal redacts facts and retains draft " + code);
    }
    failure = undefined; await read(); button("Close recipient policy").focus(); button("Close recipient policy").click();
    check(document.activeElement === button("Recipient policy"), "Explicit Close returns focus");
    open(); input().focus(); input().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })); await tick();
    check(calls.filter(c => c.action === "configure").length === 5, "Enter never saves");
    input().dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    check(document.activeElement === button("Recipient policy"), "Escape closes and returns focus");

    const bounded = new RecipientMemory();
    for (let i = 0; i < 20; i++) check(bounded.get(String(i)) !== undefined, "Bounded retained context " + i);
    check(bounded.get("21") === undefined && bounded.get("0") !== undefined, "Context capacity refuses without eviction");
  } finally { panel.suspend(); root.remove(); }
  return checks;
}

// Controlled production DOM checks only. Actual selected-service metadata
// observations belong to the separately executed parent browser journey.
export async function runInstallationProbe(): Promise<number> {
  let checks = 0, statusCalls = 0, inspectionCalls = 0, saveCalls = 0;
  const check = (ok: boolean, label: string): void => { if (!ok) throw Error(label); checks++; };
  let status = structuredClone(empty), outcome: InstallationInspectionResult = {
    identity: { boardId: context.boardId, incarnation: context.incarnation }, runtimeGeneration: id(5), state: "candidate", code: "CANDIDATE_PRESENT" };
  let fail: unknown, heldStatus = false, heldInspection = false;
  let releaseStatus: ((value: RecipientView) => void) | undefined;
  let releaseInspection: ((value: InstallationInspectionResult) => void) | undefined;
  let savedCommand: Extract<RecipientCommand, { action: "configure" }> | undefined;
  const client = {
    recipient: async (_identity: Identity, command: RecipientCommand): Promise<RecipientView> => {
      if (command.action === "configure") { saveCalls++; savedCommand = command; throw new RecipientUnknown(); }
      statusCalls++;
      if (heldStatus) return new Promise(resolve => { releaseStatus = resolve; });
      return structuredClone(status);
    },
    ...unexpectedProbe,
    installationInspection: async (_identity: Identity, generation: string, _signal: AbortSignal, guard: () => void): Promise<InstallationInspectionResult> => {
      inspectionCalls++; check(generation === status.runtimeGeneration, "Inspection uses same-action status generation"); guard();
      if (heldInspection) return new Promise(resolve => { releaseInspection = resolve; });
      if (fail) throw fail;
      return structuredClone(outcome);
    },
  };
  const root = document.createElement("div"), view = new BoardRecipient(client, new RecipientMemory());
  root.append(view.host); document.body.append(root);
  const button = (name: string): HTMLButtonElement => [...root.querySelectorAll("button")].find(node => node.textContent === name)!;
  const input = (): HTMLInputElement => root.querySelector("input")!;
  const open = (): void => button("Recipient policy").click();
  const inspect = async (): Promise<void> => { button("Inspect installation").click(); await tick(); };
  const choose = (selected = context): void => {
    view.boardRead(selected); status = { ...empty, identity: { boardId: selected.boardId, incarnation: selected.incarnation } };
    outcome = { ...outcome, identity: status.identity, runtimeGeneration: status.runtimeGeneration }; open();
  };
  try {
    choose();
    check(statusCalls === 0 && inspectionCalls === 0, "Mount and open do not inspect or read automatically");
    check(button("Inspect installation").type === "button", "Inspection cannot implicitly submit a form");
    input().value = id(7); input().dispatchEvent(new Event("input")); input().focus();
    await inspect();
    check(statusCalls === 1 && inspectionCalls === 1 && saveCalls === 0, "One deliberate status and one inspect GET; no save");
    check(root.textContent!.includes("Candidate present; no command was run"), "Candidate wording makes no invocation claim");
    check(input().value === id(7) && document.activeElement === input(), "Inspection preserves recipient input and focus");
    outcome = { ...outcome, state: "missing", code: "TOOL_MISSING" }; await inspect();
    check(root.textContent!.includes("No candidate in the supported installation location") && !root.textContent!.includes("not installed"), "Missing is scoped to supported location");
    for (const code of ["PATH_UNQUALIFIED", "INSPECTION_UNKNOWN", "INSPECTION_TIMED_OUT"] as const) {
      outcome = { ...outcome, state: "unqualified", code }; await inspect();
      check(root.textContent!.includes("Qualification not established"), "Unqualified is not unsafe or ready: " + code);
      check(root.textContent!.includes(code === "PATH_UNQUALIFIED" ? "This installation was not qualified by the supported inspection policy" : "Inspection could not complete"), "Exact policy/incomplete wording " + code);
    }
    heldInspection = true; void inspect(); await tick(); const before = inspectionCalls;
    button("Inspect installation").click(); await tick(); check(inspectionCalls === before, "Busy duplicate click has no second request");
    releaseInspection!(structuredClone(outcome)); await tick(); heldInspection = false;

    for (const transition of ["selection", "close", "authority", "suspend"]) {
      choose(); heldInspection = true; void inspect(); await tick(); const release = releaseInspection!;
      if (transition === "selection") { view.selectionChanging(true); choose({ ...context, boardId: id(20), incarnation: id(21) }); view.selectionChanging(false); view.selectionChanging(true); choose(); view.selectionChanging(false); }
      if (transition === "close") button("Close recipient policy").click();
      if (transition === "authority") view.reset();
      if (transition === "suspend") { view.suspend(); view.resume(); }
      open(); release({ ...outcome, state: "candidate", code: "CANDIDATE_PRESENT" }); await tick();
      check(!root.textContent!.includes("Candidate present"), "Late result discarded after " + transition);
      heldInspection = false;
    }
    choose(); heldStatus = true; const count = inspectionCalls; void inspect(); await tick();
    view.suspend(); view.resume(); open(); releaseStatus!(status); await tick(); heldStatus = false;
    check(inspectionCalls === count, "Late status cannot start inspection after suspend");

    choose({ ...context, boardId: id(30), incarnation: id(31) });
    button("Read recipient status").click(); await tick(); input().value = id(32); input().dispatchEvent(new Event("input"));
    button("Save once").click(); await tick();
    check(saveCalls === 1 && root.textContent!.includes("Save outcome UNKNOWN"), "Controlled original remains pending for inspection test");
    status = { ...status, state: "configured", configurationRevision: 1, configurationGeneration: id(33),
      requestId: savedCommand!.requestId, input: savedCommand!.input };
    outcome = { ...outcome, identity: status.identity, state: "candidate", code: "CANDIDATE_PRESENT" };
    await inspect();
    check(!root.textContent!.includes("Original save confirmed") && root.textContent!.includes("Save outcome UNKNOWN") && saveCalls === 1,
      "Inspection status cannot settle/replay a recipient original");
    check(input().value === id(32), "Unknown original input remains exact");
    button("Read recipient status").click(); await tick();
    check(root.textContent!.includes("Codex chat:"), "Configured facts available before denied inspection");
    fail = new InstallationInspectionRefusal("FORBIDDEN"); await inspect();
    check(!root.textContent!.includes("Codex chat:") && !root.textContent!.includes("Candidate present"), "Authority refusal clears displayed private facts and observation");
    check(input().value === id(32), "Authority loss retains bounded input");
    fail = undefined;
    const separate = new BoardRecipient(client, new RecipientMemory()); root.append(separate.host); separate.boardRead(context);
    check(!separate.host.textContent!.includes("Candidate present"), "Another window view has no inspection observation"); separate.suspend(); separate.host.remove();
    button("Close recipient policy").focus(); button("Close recipient policy").click();
    check(document.activeElement === button("Recipient policy"), "Close restores explicit opener focus");
  } finally { view.suspend(); root.remove(); }
  return checks;
}
