// Controlled actual-DOM regressions. The ordinary keyboard journey uses the built service separately.
import type { BoardRead, SessionRead } from "../../generated/transport/contracts.js";
import { mountSyntheticBoard } from "./view.test.js";

export async function runKeyboardFocusProbe(board: BoardRead, session: SessionRead): Promise<number> {
  const mounted = await mountSyntheticBoard(board, session); let checks = 0;
  const check = (ok: boolean, message: string): void => { if (!ok) throw Error(message); checks++; };
  const button = (name: string): HTMLButtonElement => [...document.querySelectorAll<HTMLButtonElement>("button")].find(row => row.textContent === name)!;
  try {
    const opener = button("Edit card"); opener.focus(); opener.click();
    const prose = document.querySelector<HTMLTextAreaElement>('textarea[name="description"]')!;
    prose.value = "Private multiline input\nSelection stays here"; prose.dispatchEvent(new Event("input", { bubbles: true }));
    prose.focus(); prose.setSelectionRange(4, 12, "backward");
    prose.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    check(document.activeElement === button("Keep editing"), "Discard confirmation did not offer safe focused decline");
    button("Keep editing").click();
    check(document.activeElement === prose && prose.selectionStart === 4 && prose.selectionEnd === 12 && prose.selectionDirection === "backward" && prose.value.includes("Selection stays here"), "Declining discard lost the actual multiline focus or selection");
    await mounted.view.refresh();
    check(document.activeElement === prose && prose.selectionStart === 4 && prose.selectionEnd === 12, "Refresh replaced editor focus or selection");
    prose.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    button("Confirm discard editor input").click();
    check(document.activeElement === button("Edit card") && document.activeElement !== opener, "Discard did not find refreshed opener by stable identity");
    const refreshed = button("Edit card"); refreshed.focus(); refreshed.click();
    // A previously usable opener may become unavailable before clean Escape.
    refreshed.disabled = true;
    document.querySelector<HTMLInputElement>('input[name="title"]')!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    check(document.activeElement === button("Read current board"), "Disabled opener did not return to the usable workspace fallback");
    const hidden = button("Rename card"); hidden.focus(); hidden.click(); hidden.hidden = true;
    document.querySelector<HTMLInputElement>('input[name="title"]')!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    check(document.activeElement === button("Read current board"), "Hidden keyed opener did not use the visible workspace fallback");
    hidden.hidden = false;
    const keyed = button("Rename card"); keyed.focus();
    const focus = HTMLElement.prototype.focus; let prevented = false;
    HTMLElement.prototype.focus = function(options?: FocusOptions): void { if (this.dataset.focusKey === keyed.dataset.focusKey) prevented = options?.preventScroll === true; focus.call(this, options); };
    try { Object.assign(mounted.board.result, { cards: [...mounted.board.result.cards].reverse() }); await mounted.view.refresh(); }
    finally { HTMLElement.prototype.focus = focus; }
    check((document.activeElement as HTMLElement).dataset.focusKey === keyed.dataset.focusKey && prevented, "Card reorder did not restore stable control without scrolling");
    check(mounted.state.mutations === 0, "Focus and discard journey dispatched a mutation");
    return checks;
  } finally { mounted.view.destroy(); mounted.client.close(); }
}
