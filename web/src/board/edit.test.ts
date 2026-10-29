// Synthetic actual-DOM editor evidence; authoritative mutation behavior is tested in Go.
import type { BoardRead, SessionRead } from "../../generated/transport/contracts.js";
import { mountSyntheticBoard } from "./view.test.js";

export async function runCardEditProbe(boardInput: BoardRead, sessionInput: SessionRead): Promise<number> {
  let mounted = await mountSyntheticBoard(boardInput, sessionInput), checks = 0;
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  const button = (label: string): HTMLButtonElement => [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === label)!;
  const title = (): HTMLInputElement => document.querySelector('input[name="title"]')!;
  const press = (target: HTMLElement, key: string, extra: KeyboardEventInit = {}): void => {
    target.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...extra }));
  };
  const change = (target: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string): void => {
    target.value = value; target.dispatchEvent(new Event("input", { bubbles: true }));
  };
  const settle = async (test: () => boolean): Promise<void> => {
    for (let i = 0; i < 250; i++) { if (test()) return; await new Promise(resolve => setTimeout(resolve, 20)); }
    throw Error("Editor probe did not settle");
  };
  const remount = async (): Promise<void> => {
    mounted.view.destroy(); mounted.client.close(); mounted = await mountSyntheticBoard(boardInput, sessionInput);
  };
  try {
    button("Rename card").focus(); button("Rename card").click();
    check(document.activeElement === title() && title().value === boardInput.result.cards[0]!.title, "Rename did not focus the shared title editor");
    press(title(), "Escape");
    check(!title() && document.activeElement === button("Rename card"), "Clean Escape did not return focus");

    button("Rename card").click(); const retained = title(); change(retained, "Changed 雪 e\u0301 🏊");
    await mounted.view.flushPrivateInput(); button("Add comment").click();
    check(title() === retained && !document.querySelector('textarea[name="text"]'), "Opening another editor replaced dirty intent");
    press(retained, "Escape");
    check(!!button("Confirm discard editor input") && button("Save").disabled && mounted.state.mutations === 0, "Dirty Escape silently discarded or submitted");
    button("Keep editing").click();
    check(title() === retained && retained.value === "Changed 雪 e\u0301 🏊" && document.activeElement === retained, "Declining discard lost input or focus");
    press(retained, "Escape"); button("Confirm discard editor input").click();
    check(!title() && mounted.state.mutations === 0, "Confirmed discard submitted or retained current editor");

    button("Rename card").click();
    const edited = title(), description = document.querySelector<HTMLTextAreaElement>('textarea[name="description"]')!;
    change(edited, " Renamed 雪 e\u0301 🏊 "); change(description, "Line one\nLine two <script>inert</script>");
    press(description, "Enter");
    check(mounted.state.mutations === 0 && description.value.includes("\n"), "Multiline Enter submitted");
    edited.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
    press(edited, "Enter", { isComposing: true });
    edited.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true }));
    press(edited, "Enter");
    check(mounted.state.mutations === 0, "IME confirmation submitted");
    edited.dispatchEvent(new KeyboardEvent("keyup", { key: "Enter", bubbles: true }));
    mounted.state.mode = "late"; press(edited, "Enter"); press(edited, "Enter", { repeat: true });
    await settle(() => mounted.state.mutations === 1 && mounted.state.releases.length === 1);
    const request = JSON.parse(mounted.state.wire[0]!);
    check(request.input.title === " Renamed 雪 e\u0301 🏊 " && request.input.description === description.value &&
      request.target.cardId === boardInput.result.cards[0]!.cardId && request.expectedCardRevision === boardInput.result.cards[0]!.revision &&
      request.expectedRevision === boardInput.result.revision, "Rename changed Unicode, UUID, prose or original base");
    press(edited, "Escape");
    check(title() === edited && !button("Confirm discard editor input"), "Escape discarded submitted original/editor");
    button("Stop waiting").click(); press(edited, "Enter");
    check(mounted.state.mutations === 1 && title() === edited && !!button("Look up original outcome"), "UNKNOWN allowed replacement submission or lost original");
    mounted.state.releases.splice(0).forEach(release => release());
    await settle(() => document.body.textContent!.includes("Confirmed saved."));
    await mounted.view.refresh();
    check(mounted.board.result.cards[0]!.ticket === boardInput.result.cards[0]!.ticket && mounted.board.result.cards[0]!.cardId === request.target.cardId &&
      title() === edited && edited.value === request.input.title, "Read changed stable identity or replaced editor");

    await remount(); mounted.state.mode = "conflict"; button("Rename card").click();
    const conflicted = title(); change(conflicted, "Stale intent"); press(conflicted, "Enter");
    await settle(() => document.body.textContent!.includes("Conflict. Input retained;") && document.body.textContent!.includes("Your editor retains its original base."));
    check(title() === conflicted && conflicted.value === "Stale intent", "Conflict/current text destroyed input");
    await mounted.view.refresh(); press(conflicted, "Enter");
    check(mounted.state.mutations === 1 && title() === conflicted, "Refresh replayed/rebased UNKNOWN intent");

    await remount(); mounted.state.mode = "nochange"; button("Rename card").click(); press(title(), "Enter");
    await settle(() => document.body.textContent!.includes("Definitive noncommit.") && !button("Save").disabled);
    const unchanged = JSON.parse(mounted.state.wire[0]!);
    check(unchanged.input.description === boardInput.result.cards[0]!.description && unchanged.input.title === boardInput.result.cards[0]!.title &&
      mounted.board.result.revision === boardInput.result.revision && mounted.board.result.cards[0]!.revision === boardInput.result.cards[0]!.revision,
      "Unchanged editor normalized source text or bumped fake revision");
    check(!!title() && !button("Look up original outcome"), "Definitive no-change did not retain editable input");

    await remount(); mounted.state.mode = "late"; button("Rename card").click(); change(title(), "Only title changes"); press(title(), "Enter");
    await settle(() => mounted.state.releases.length === 1);
    check(JSON.parse(mounted.state.wire[0]!).input.description === boardInput.result.cards[0]!.description, "Rename normalized untouched description");
    mounted.state.releases.splice(0).forEach(release => release());
    await settle(() => document.body.textContent!.includes("Confirmed saved."));

    await remount(); mounted.state.mode = "http403"; button("Rename card").click(); press(title(), "Enter");
    await settle(() => !!button("Look up original outcome"));
    check(mounted.state.mutations === 1 && !!title() && button("Save").disabled, "Refusal without terminal proof lost original guard");

    await remount();
    mounted.client.recovery.saveDraft = async () => { throw Error("Synthetic private storage denial"); };
    button("Rename card").click(); const denied = title(); change(denied, "Retain storage-denied input");
    check(!await mounted.view.flushPrivateInput() && !mounted.view.canLeave, "Denied storage released navigation guard");
    button("Add comment").click(); press(denied, "Escape"); button("Keep editing").click();
    check(title() === denied && denied.value === "Retain storage-denied input" && !mounted.view.canLeave && mounted.state.mutations === 0,
      "Denied storage/switch/declined discard lost private input");
    return checks;
  } finally { mounted.state.releases.splice(0).forEach(release => release()); mounted.view.destroy(); mounted.client.close(); }
}
