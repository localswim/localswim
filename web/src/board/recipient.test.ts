// Actual production DOM handlers, controlled asynchronous client responses.
// The primary runs this probe in qualified Chrome; it is not ordinary HTTP proof.
import type { RecipientView } from "../../generated/transport/contracts.js";
import type { Identity } from "../../generated/archive/contracts.js";
import { RecipientRefusal, RecipientUnknown } from "../api/recipient.js";
import type { RecipientCommand } from "../api/recipient.js";
import { BoardRecipient, RecipientMemory } from "./recipient.js";
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
  const client = { recipient: async (identity: Identity, command: RecipientCommand): Promise<RecipientView> => {
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
