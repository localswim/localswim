import type { CardFile, CardFileRead, CardFilesRead } from "../../generated/transport/contracts.js";
import { parse, validate } from "./boundary.js";
import { validateFileRead, validateFilesRead } from "./files.js";

const id = (n: number): string => "00000000-0000-4000-8000-" + String(n).padStart(12, "0");
export const fileSelectionFixture = { identity: { boardId: id(1), incarnation: id(2) }, cardId: id(3) };
export function fileFixture(n = 4): CardFile {
  return { attachment: { attachmentId: id(n), cardId: id(3), createdIn: { boardId: id(1), incarnation: id(20) },
    createdAt: "2026-11-05T00:00:00Z", name: "<script>é\u0000</script>", mediaLabel: "application/x-test", byteLength: 0,
    sha256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" },
    referenceState: "current", downloadState: "current_reference",
    attribution: { state: "known", actorId: id(6), actorKind: "human", actorLabel: "<em>Actor</em>", basis: "operation" } };
}
export function filesFixture(items: readonly CardFile[] = [fileFixture()]): CardFilesRead {
  return { apiVersion: 1, scope: "card-files", result: { ...fileSelectionFixture, revision: 4, cardRevision: 3,
    view: "current", items, nextCursor: null } };
}
export function runFilesBoundaryTests(): number {
  let checks = 0;
  const list = (read: unknown, accepted: boolean): void => {
    let ok = true;
    try {
      const value = parse(JSON.stringify(read)); validate("transport:CardFilesRead", value);
      validateFilesRead(value as unknown as CardFilesRead, fileSelectionFixture, "current", 50);
    } catch { ok = false; }
    if (ok !== accepted) throw Error("File list validation mismatch"); checks++;
  };
  const base = filesFixture(); list(base, true); list(filesFixture([]), true);
  const change = (mutate: (read: CardFilesRead) => void): void => { const read = structuredClone(base); mutate(read); list(read, false); };
  change(read => Object.assign(read, { extra: true }));
  change(read => Object.assign(read.result, { cardId: id(90) }));
  change(read => Object.assign(read.result.identity, { incarnation: id(90) }));
  change(read => Object.assign(read.result, { view: "retained" }));
  change(read => Object.assign(read.result, { items: null }));
  change(read => Object.assign(read.result, { cardRevision: 0 }));
  change(read => Object.assign(read.result, { nextCursor: "a=" }));
  const shortPage = structuredClone(base); Object.assign(shortPage.result, { nextCursor: "a" }); list(shortPage, true);
  const emptyContinuation = filesFixture([]); Object.assign(emptyContinuation.result, { nextCursor: "a" }); list(emptyContinuation, false);
  change(read => Object.assign(read.result.items[0]!.attachment, { cardId: id(90) }));
  change(read => Object.assign(read.result.items[0]!.attachment.createdIn, { boardId: id(90) }));
  change(read => Object.assign(read.result.items[0]!.attachment, { name: "é".repeat(128) }));
  change(read => Object.assign(read.result.items[0]!.attachment, { mediaLabel: "text/é" }));
  change(read => Object.assign(read.result.items[0]!, { downloadState: "not_current" }));
  change(read => Object.assign(read.result.items[0]!.attribution, { actorLabel: null }));
  change(read => Object.assign(read.result.items[0]!.attribution, { state: "unavailable" }));
  list(filesFixture([fileFixture(5), fileFixture(4)]), false);
  list(filesFixture([fileFixture(), fileFixture()]), false);
  const unavailable = fileFixture(); Object.assign(unavailable, { attribution: { state: "unavailable", actorId: null, actorKind: null, actorLabel: null, basis: null } });
  list(filesFixture([unavailable]), true);
  const inspect: CardFileRead = { apiVersion: 1, scope: "card-file", result: { ...fileSelectionFixture, revision: 4, cardRevision: 3, file: fileFixture() } };
  validate("transport:CardFileRead", parse(JSON.stringify(inspect))); validateFileRead(inspect, fileSelectionFixture, id(4)); checks++;
  let refused = false; try { validateFileRead(inspect, fileSelectionFixture, id(5)); } catch { refused = true; }
  if (!refused) throw Error("Inspect selected a foreign attachment"); checks++;
  const excessiveLabel = structuredClone(inspect); Object.assign(excessiveLabel.result.file.attribution, { actorLabel: "é".repeat(16_777_217) });
  refused = false; try { validateFileRead(excessiveLabel, fileSelectionFixture, id(4)); } catch { refused = true; }
  if (!refused) throw Error("Over-budget UTF-8 actor label accepted"); checks++;
  return checks;
}
