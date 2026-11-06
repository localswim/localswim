import type { CardFileRead, CardFilesRead } from "../../generated/transport/contracts.js";
import { fileFixture, fileSelectionFixture, filesFixture } from "../api/files.test.js";
import { FilesReadRefusal } from "../api/files.js";
import { CardFiles } from "./files.js";

// Actual DOM probe with controlled responses, separate from real-service evidence.
export async function runCardFilesProbe(): Promise<number> {
  let checks = 0, mode = "normal", downloads = 0, offers = 0, revoked = 0;
  const pending: Array<() => void> = [];
  const root = document.createElement("main"), editor = document.createElement("textarea"); editor.value = "Private editor input";
  const originalCreate = URL.createObjectURL, originalRevoke = URL.revokeObjectURL, originalClick = HTMLAnchorElement.prototype.click;
  URL.createObjectURL = () => { offers++; return "blob:synthetic-file-test"; };
  URL.revokeObjectURL = () => { revoked++; };
  HTMLAnchorElement.prototype.click = function (): void { if (this.download !== "attachment-" + fileFixture().attachment.attachmentId + ".bin") throw Error("Unsafe download name"); };
  const panel = new CardFiles({
    cardFiles: async (selected, view, cursor): Promise<CardFilesRead> => {
      if (mode === "stale" && cursor) throw new FilesReadRefusal("REVISION_CONFLICT");
      const file = fileFixture(cursor ? 5 : 4);
      if (view === "retained") Object.assign(file, { referenceState: "retained", downloadState: "not_current", attribution: { state: "unavailable", actorId: null, actorKind: null, actorLabel: null, basis: null } });
      const read = filesFixture([file]); Object.assign(read.result, { ...selected, view, nextCursor: mode === "paging" || mode === "stale" ? cursor ? null : "cursor" : null });
      if (mode === "wrong") Object.assign(read.result, { cardId: "foreign" });
      if (mode === "late") await new Promise<void>(resolve => pending.push(resolve));
      return read;
    },
    cardFile: async (selected, attachmentId): Promise<CardFileRead> => {
      const file = fileFixture(); Object.assign(file.attachment, { attachmentId });
      if (mode === "retained") Object.assign(file, { referenceState: "retained", downloadState: "not_current", attribution: { state: "unavailable", actorId: null, actorKind: null, actorLabel: null, basis: null } });
      const read: CardFileRead = { apiVersion: 1, scope: "card-file", result: { ...selected, revision: mode === "inspect-stale" ? 5 : 4, cardRevision: 3, file } };
      if (mode === "late-inspect") await new Promise<void>(resolve => pending.push(resolve));
      return read;
    },
    downloadAttachment: async (_board, _id, facts, signal): Promise<ArrayBuffer> => {
      downloads++;
      if (facts.incarnation !== fileSelectionFixture.identity.incarnation || facts.cardId !== fileSelectionFixture.cardId) throw Error("Download identity missing");
      if (mode === "late-download") await new Promise<void>(resolve => pending.push(resolve));
      if (mode === "broken" || signal.aborted) throw Error("No complete bytes"); return new ArrayBuffer(0);
    },
  });
  root.append(panel.host, editor); document.body.replaceChildren(root);
  const flush = async (): Promise<void> => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); };
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  const button = (label: string): HTMLButtonElement => [...panel.host.querySelectorAll<HTMLButtonElement>("button")].find(value => value.textContent === label)!;
  const inspect = (): void => button("Inspect file " + fileFixture().attachment.attachmentId).click();
  try {
    panel.open(fileSelectionFixture); await flush();
    check(panel.host.textContent!.includes(fileFixture().attachment.name) && !panel.host.querySelector("script,em"), "Filename was executed or lost");
    check(button("Current files").getAttribute("aria-pressed") === "true" && button("Next files").disabled, "Current view defaults or paging mismatch");
    editor.focus(); inspect(); await flush();
    check(document.activeElement === editor && editor.value === "Private editor input", "Inspection changed editor/focus");
    check(panel.host.textContent!.includes("Added by: <em>Actor</em>") && panel.host.textContent!.includes("Created in:") && !panel.host.querySelector("em"), "Attribution/provenance was hidden or executed");
    button("Offer file download").click(); await flush(); await new Promise(resolve => setTimeout(resolve, 1));
    check(offers === 1 && revoked === 1 && panel.host.textContent!.includes("offered after complete byte verification"), "Verified offer or URL cleanup mismatch");
    mode = "broken"; button("Offer file download").click(); await flush();
    check(offers === 1 && panel.host.textContent!.includes("No verified offer made"), "Failure offered a download");
    mode = "late-download"; button("Offer file download").click(); button("Cancel file download").focus(); button("Cancel file download").click();
    pending.splice(0).forEach(resolve => resolve()); await flush();
    check(document.activeElement === button("Offer file download") && offers === 1, "Cancelled transfer lost local focus or offered bytes");
    button("Offer file download").click(); button("Cancel file download").focus(); pending.splice(0).forEach(resolve => resolve()); await flush();
    check(document.activeElement === button("Offer file download") && offers === 2, "Completed transfer lost focused Cancel fallback");
    button("Offer file download").click(); editor.focus(); pending.splice(0).forEach(resolve => resolve()); await flush();
    check(document.activeElement === editor && offers === 3, "Transfer completion stole unrelated editor focus");
    mode = "normal"; button("Offer file download").focus(); panel.open(fileSelectionFixture); await flush();
    check(document.activeElement === button("Refresh files"), "Same-card refresh lost local details focus");
    editor.focus(); panel.open(fileSelectionFixture); await flush();
    check(document.activeElement === editor, "Same-card refresh stole unrelated editor focus");
    mode = "retained"; button("Retained files").click(); await flush(); inspect(); await flush();
    check(!button("Offer file download") && panel.host.textContent!.includes("no longer attached") && panel.host.textContent!.includes("attribution unavailable"), "Retained file enabled download or fabricated attribution");
    mode = "paging"; button("Current files").click(); await flush(); button("Next files").focus(); button("Next files").click(); await flush();
    check(document.activeElement === button("Refresh files") && panel.host.textContent!.includes(fileFixture(5).attachment.attachmentId) && !button("Inspect file " + fileFixture().attachment.attachmentId), "Paging merged rows or lost focus");
    mode = "stale"; button("Refresh files").click(); await flush(); button("Next files").click(); await flush();
    check(!panel.host.querySelector("article") && panel.host.textContent!.includes("Partial pages discarded"), "Stale pagination retained mixed facts");
    mode = "wrong"; button("Refresh files").click(); await flush();
    check(!panel.host.querySelector("article") && panel.host.textContent!.includes("Files unavailable"), "Foreign list selected");
    mode = "normal"; button("Refresh files").click(); await flush(); mode = "inspect-stale";
    button("Inspect file " + fileFixture().attachment.attachmentId).focus(); inspect(); await flush();
    check(!button("Offer file download") && panel.host.textContent!.includes("Files stale") && document.activeElement === button("Refresh files"), "Stale inspect lost local focus or offered bytes");
    mode = "normal"; button("Refresh files").click(); await flush(); mode = "inspect-stale"; editor.focus(); inspect(); await flush();
    check(document.activeElement === editor, "Stale inspection stole unrelated editor focus");
    mode = "late"; panel.open(fileSelectionFixture); panel.close(); pending.splice(0).forEach(resolve => resolve()); await flush();
    check(!panel.host.querySelector("article"), "Closed selection accepted late list");
    mode = "normal"; panel.open(fileSelectionFixture); await flush(); mode = "late-inspect"; inspect(); panel.suspend(); pending.splice(0).forEach(resolve => resolve()); await flush();
    check(!button("Offer file download"), "Suspended selection accepted late inspect");
    panel.resume(); mode = "normal"; panel.open(fileSelectionFixture); await flush(); inspect(); await flush();
    const priorOffers = offers, priorDownloads = downloads;
    mode = "late-download"; button("Offer file download").click(); panel.close(); pending.splice(0).forEach(resolve => resolve()); await flush();
    check(offers === priorOffers && downloads === priorDownloads + 1, "Closed selection offered late bytes");
    return checks;
  } finally {
    panel.close(); URL.createObjectURL = originalCreate; URL.revokeObjectURL = originalRevoke; HTMLAnchorElement.prototype.click = originalClick; root.remove();
  }
}
