import { boardBoundaryVerifier, canonical, parse, prepare, validate } from "./boundary.js";
import type { BoardCommand } from "./boundary.js";
import type { MutationRequest } from "../../generated/transport/contracts.js";
import type { Context } from "../recovery/types.js";

export async function runBoundaryVectors(vectors: readonly { command: BoardCommand; wire: string; semantic: string }[]): Promise<number> {
  let passed = 0;
  for (const vector of vectors) {
    const semantic = JSON.parse(vector.semantic) as Context & { actorId: string };
    const context: Context = { installationId: "00000000-0000-4000-8000-000000000090", boardId: semantic.boardId, incarnation: semantic.incarnation, actorId: semantic.actorId };
    const request = parse(vector.wire) as unknown as MutationRequest;
    const e = await prepare(vector.command, request, context, "00000000-0000-4000-8000-000000000091");
    if (e.canonicalBytes !== vector.semantic || !await boardBoundaryVerifier.prepared(e, request)) throw Error("Accepted canonical vector mismatch");
    if (await boardBoundaryVerifier.prepared({ ...e, requestBytes: e.requestBytes.replace('"opId":', '"opId":"00000000-0000-4000-8000-000000000099","opId":') }, request)) throw Error("Duplicate key accepted");
    if (await boardBoundaryVerifier.prepared({ ...e, canonicalBytes: e.canonicalBytes + " " }, request)) throw Error("Noncanonical evidence accepted");
    passed++;
  }
  for (const text of ['{"a":1,"a":1}', '"\\ud800"', '-0', '1.0', '1e2', '01', '\ufeff{}', '{"a":9007199254740992}']) {
    let refused = false; try { parse(text); } catch { refused = true; }
    if (!refused) throw Error("Invalid JSON accepted"); passed++;
  }
  const value = parse('{"a":"雪\\r\\n\\t\\u0000\\\"\\\\"}');
  if (canonical(value) !== '{"a":"雪\\u000d\\u000a\\u0009\\u0000\\\"\\\\"}') throw Error("Control canonicalization mismatch");
  let refused = false; try { validate("transport:SessionRead", parse('{"apiVersion":1}')); } catch { refused = true; }
  if (!refused) throw Error("Incomplete session accepted");
  return passed + 2;
}

// Accepted C01 Go White_Space corpus and neighboring non-space codepoints.
export async function runWhitespaceParity(): Promise<number> {
  const whitespace = [9, 10, 11, 12, 13, 32, 0x85, 0xa0, 0x1680,
    ...Array.from({ length: 11 }, (_, i) => 0x2000 + i), 0x2028, 0x2029, 0x202f, 0x205f, 0x3000];
  const nonWhitespace = [0, 8, 14, 0x1c, 0x1f, 0x21, 0x84, 0x86, 0xa1, 0x167f, 0x1681,
    0x180e, 0x1fff, 0x200b, 0x2027, 0x202a, 0x202e, 0x2030, 0x205e, 0x2060, 0x2fff, 0x3001, 0xfeff];
  const context: Context = { installationId: "00000000-0000-4000-8000-000000000090",
    boardId: "00000000-0000-4000-8000-000000000001", incarnation: "00000000-0000-4000-8000-000000000002",
    actorId: "00000000-0000-4000-8000-00000000000a" };
  let passed = 0;
  for (const command of ["card.create", "card.edit", "comment.add"] as const) {
    for (const [points, accepted] of [[whitespace, false], [nonWhitespace, true]] as const) {
      for (const point of points) {
        const text = String.fromCodePoint(point);
        const wire = { opId: crypto.randomUUID(), incarnation: context.incarnation, expectedRevision: 5,
          expectedCardRevision: command === "card.create" ? null : 4,
          target: command === "card.create" ? null : { cardId: "00000000-0000-4000-8000-00000000000b" },
          input: command === "comment.add" ? { text } : command === "card.create" ? { title: text, description: "" } : { title: text } } as MutationRequest;
        let evidence;
        try { evidence = await prepare(command, wire, context, crypto.randomUUID()); } catch { /* refusal asserted below */ }
        if (!!evidence !== accepted) throw Error("Go whitespace parity mismatch");
        if (evidence) {
          const payload = JSON.parse(evidence.canonicalBytes).payload as Record<string, string>;
          if ((command === "comment.add" ? payload.text : payload.title) !== text) throw Error("Input codepoint changed");
        }
        passed++;
      }
    }
    const mixed = whitespace.map(point => String.fromCodePoint(point)).join("") + "\ufeff";
    const wire = { opId: crypto.randomUUID(), incarnation: context.incarnation, expectedRevision: 5,
      expectedCardRevision: command === "card.create" ? null : 4,
      target: command === "card.create" ? null : { cardId: "00000000-0000-4000-8000-00000000000b" },
      input: command === "comment.add" ? { text: mixed } : command === "card.create" ? { title: mixed, description: "" } : { title: mixed } } as MutationRequest;
    const evidence = await prepare(command, wire, context, crypto.randomUUID());
    if (JSON.parse(evidence.canonicalBytes).payload[command === "comment.add" ? "text" : "title"] !== mixed) throw Error("Whitespace normalized");
    passed++;
  }
  return passed;
}
