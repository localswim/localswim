import type { Confirmation, MutationRequest, Terminal } from "../../generated/transport/contracts.js";
import type { BoundaryVerifier, PreparedEvidence, Context } from "../recovery/types.js";
import { schemas } from "./schema-data.js";

type ObjectValue = { [key: string]: Value };
export type Value = null | boolean | number | string | Value[] | ObjectValue;
export function invalid(): never { throw new Error("Invalid boundary data"); }
// C01 uses Go strings.TrimSpace (Unicode White_Space), not ECMAScript trim.
// This is a predicate only: original prose/codepoints are never normalized.
function nonblank(s: string): boolean {
  return /[^\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]/u.test(s);
}
export function object(v: Value): ObjectValue {
  if (!v || typeof v !== "object" || Array.isArray(v)) invalid();
  return v;
}
// Parse before JSON.parse can erase duplicate keys or invalid numeric spellings.
export function parse(s: string): Value {
  let i = 0;
  const ws = (): void => { while (i < s.length && /[ \r\n\t]/.test(s[i]!)) i++; };
  function str(): string {
    const start = i++;
    while (i < s.length) {
      const c = s[i++];
      if (c === "\\") { i++; continue; }
      if (c === '"') {
        const v: unknown = JSON.parse(s.slice(start, i));
        if (typeof v !== "string" || !v.isWellFormed()) invalid();
        return v;
      }
    }
    return invalid();
  }
  function val(depth: number): Value {
    if (depth > 32) invalid(); ws(); const c = s[i];
    if (c === '"') return str();
    if (c === "{" || c === "[") {
      i++; ws();
      if (c === "{") {
        const v: ObjectValue = Object.create(null) as ObjectValue;
        if (s[i] === "}") { i++; return v; }
        for (;;) {
          ws(); if (s[i] !== '"') invalid(); const k = str();
          if (Object.hasOwn(v, k)) invalid(); ws(); if (s[i++] !== ":") invalid();
          v[k] = val(depth + 1); ws(); const end = s[i++];
          if (end === "}") return v; if (end !== ",") invalid();
        }
      }
      const v: Value[] = [];
      if (s[i] === "]") { i++; return v; }
      for (;;) { v.push(val(depth + 1)); ws(); const end = s[i++]; if (end === "]") return v; if (end !== ",") invalid(); }
    }
    for (const [literal, v] of [["true", true], ["false", false], ["null", null]] as const) {
      if (s.startsWith(literal, i)) { i += literal.length; return v; }
    }
    const n = /^(0|[1-9][0-9]*)/.exec(s.slice(i)); if (!n) invalid();
    i += n[0].length; const v = Number(n[0]); if (!Number.isSafeInteger(v)) invalid(); return v;
  }
  const value = val(0); ws(); if (i !== s.length) invalid(); return value;
}
export function canonical(v: Value): string {
  if (typeof v === "string") {
    if (!v.isWellFormed()) invalid();
    return '"' + v.replace(/[\u0000-\u001f"\\]/g, c => c === '"' || c === "\\" ? "\\" + c : "\\u" + c.charCodeAt(0).toString(16).padStart(4, "0")) + '"';
  }
  if (v === null || typeof v === "boolean") return String(v);
  if (typeof v === "number") { if (!Number.isSafeInteger(v) || v < 0 || Object.is(v, -0)) invalid(); return String(v); }
  if (Array.isArray(v)) return "[" + v.map(canonical).join(",") + "]";
  return "{" + Object.keys(v).sort().map(k => { if (/[^\x00-\x7f]/.test(k)) invalid(); return canonical(k) + ":" + canonical(v[k]!); }).join(",") + "}";
}
type Schema = { [key: string]: unknown };
function check(v: Value, schema: unknown): boolean {
  const s = schema as Schema;
  if (s.$ref) return check(v, schemas[s.$ref as string]);
  if (s.anyOf && !(s.anyOf as unknown[]).some(x => check(v, x))) return false;
  if (s.oneOf && (s.oneOf as unknown[]).filter(x => check(v, x)).length !== 1) return false;
  if (s.allOf && !(s.allOf as unknown[]).every(x => check(v, x))) return false;
  if (s.not && check(v, s.not)) return false;
  if (Object.hasOwn(s, "const") && canonical(v) !== canonical(s.const as Value)) return false;
  if (s.enum && !(s.enum as Value[]).some(x => canonical(x) === canonical(v))) return false;
  if (s.type === "null") return v === null;
  if (s.type === "boolean") return typeof v === "boolean";
  if (s.type === "integer") return typeof v === "number" && Number.isSafeInteger(v) && v >= (s.minimum as number ?? 0) && v <= (s.maximum as number ?? Number.MAX_SAFE_INTEGER);
  if (s.type === "string") {
    if (typeof v !== "string" || !v.isWellFormed()) return false;
    if (s.pattern && !new RegExp(s.pattern as string).test(v)) return false;
    if (s["x-nonblank"] && !nonblank(v)) return false;
    if (s["x-max-utf8-bytes"] && new TextEncoder().encode(v).length > (s["x-max-utf8-bytes"] as number)) return false;
    if (s.minLength && [...v].length < (s.minLength as number) || s.maxLength && [...v].length > (s.maxLength as number)) return false;
    if (s.format === "date-time") {
      const d = new Date(v); if (!Number.isFinite(d.getTime()) || d.getUTCFullYear() < 1 || d.toISOString().replace(".000Z", "Z") !== v) return false;
    }
    return true;
  }
  if (s.type === "array") {
    if (!Array.isArray(v)) return false;
    if (v.length < (s.minItems as number ?? 0) || v.length > (s.maxItems as number ?? Infinity)) return false;
    if (s.uniqueItems && new Set(v.map(canonical)).size !== v.length) return false;
    if (s["x-order"] === "ASCII ascending" && v.some((x, i) => i > 0 && String(v[i - 1]) >= String(x))) return false;
    return !s.items || v.every(x => check(x, s.items));
  }
  if (s.type === "object") {
    if (!v || typeof v !== "object" || Array.isArray(v)) return false;
    const props = s.properties as Record<string, unknown>;
    if ((s.required as string[] ?? []).some(k => !Object.hasOwn(v, k))) return false;
    return Object.keys(v).every(k => Object.hasOwn(props, k) ? check(v[k]!, props[k]) : s.additionalProperties !== false);
  }
  return true;
}
export function validate(name: string, value: Value): void {
  if (!schemas[name] || !check(value, schemas[name])) invalid();
}
export const commands = {
  "attachment.add": "AttachmentAdd", "history.clear": "HistoryClear", "retention.set": "RetentionSet",
  "card.create": "CardCreate", "card.edit": "CardEdit", "comment.add": "CommentAdd",
  "link.add": "LinkAdd", "link.remove": "LinkRemove", "review-handoff": "ReviewHandoff", "approve": "Approve",
} as const;
export type PreparedCommand = keyof typeof commands;
export type BoardCommand = Exclude<PreparedCommand, "attachment.add" | "history.clear" | "retention.set">;
const recoveryCommand = (command: PreparedCommand): boolean => command === "history.clear" || command === "retention.set";
function request(command: PreparedCommand, value: Value): ObjectValue {
  validate("mutation:Request" + commands[command], value);
  const r = object(value), input = object(r.input!);
  if (Object.hasOwn(input, "archived") && (command !== "card.edit" || typeof input.archived !== "boolean" || Object.keys(input).length !== 1)) invalid();
  if (!Object.hasOwn(r, "expectedCardRevision")) invalid();
  if (recoveryCommand(command) ? r.expectedCardRevision !== null || r.target !== null || r.command !== command : command === "card.create" ? r.expectedCardRevision !== null : typeof r.expectedCardRevision !== "number" || r.expectedCardRevision < 1) invalid();
  const bytes = (x: Value): number => new TextEncoder().encode(String(x)).length;
  if (input.title !== undefined && (typeof input.title !== "string" || !nonblank(input.title) || [...input.title].length > 200)) invalid();
  if (input.description !== undefined && bytes(input.description) > 65536) invalid();
  if (input.text !== undefined && (typeof input.text !== "string" || !nonblank(input.text) || bytes(input.text) > 65536)) invalid();
  return r;
}
function semantic(command: PreparedCommand, r: ObjectValue, c: Context): Value {
  if (r.incarnation !== c.incarnation) invalid();
  const payload = { ...object(r.input!) };
  for (const k of command === "card.create" ? ["priority"] : command === "card.edit" ? ["title", "description", "priority", "lane"] : []) payload[k] ??= null;
  return { fingerprintVersion: 1, boardId: c.boardId, incarnation: c.incarnation, actorId: c.actorId,
    opId: r.opId!, command, target: r.target!, payload: recoveryCommand(command) ? { input: payload, previewHash: r.previewHash! } : payload, expectedRevision: r.expectedRevision!, expectedCardRevision: r.expectedCardRevision! };
}
export async function sha256(s: string): Promise<string> {
  return [...new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(s)))].map(n => n.toString(16).padStart(2, "0")).join("");
}
export async function prepare(command: PreparedCommand, wire: MutationRequest | Confirmation, context: Context, draftId: string): Promise<PreparedEvidence> {
  const requestBytes = JSON.stringify(wire), r = request(command, parse(requestBytes));
  const canonicalBytes = canonical(semantic(command, r, context));
  return Object.freeze({ context: Object.freeze({ ...context }), draftId, command, requestBytes, canonicalBytes,
    fingerprintVersion: 1, fingerprint: await sha256(canonicalBytes), attachment: command === "attachment.add" ? structuredClone(wire.input) as PreparedEvidence["attachment"] : null });
}
// Concrete verifier configured on the same W01 store used for dispatch.
export const boardBoundaryVerifier: BoundaryVerifier = {
  async prepared(e, received) {
    try {
      if (!Object.hasOwn(commands, e.command)) return false;
      const command = e.command as PreparedCommand, r = request(command, parse(e.requestBytes));
      if (command === "attachment.add" ? canonical(e.attachment as unknown as Value) !== canonical(r.input!) : e.attachment !== null) return false;
      return canonical(r) === canonical(parse(JSON.stringify(received))) && e.canonicalBytes === canonical(semantic(command, r, e.context));
    } catch { return false; }
  },
  async terminal(e, terminal: Terminal) {
    try {
      if (!Object.hasOwn(commands, e.command)) return false;
      const value = parse(JSON.stringify(terminal));
      const suffix = terminal.outcome === "committed" ? "Committed" : "Notcommitted";
      validate("transport:Terminal" + commands[e.command as PreparedCommand] + suffix + (terminal.retention === "full" ? "Full" : "Compact"), value);
      // W01 separately checks all original identity, target and precondition fields.
      if (terminal.command !== e.command) return false;
      if (e.command === "attachment.add") {
        if (!e.attachment || terminal.byteFacts.length !== 1 || terminal.byteFacts[0]!.sha256 !== e.attachment.sha256 || terminal.byteFacts[0]!.byteLength !== e.attachment.byteLength) return false;
      } else if (terminal.byteFacts.length !== 0) return false;
      if (terminal.outcome === "not_committed") return terminal.revision === null && terminal.cardRevision === null && terminal.resultRefs.length === 0 && terminal.code !== "OK" && !!terminal.code && (terminal.retention === "compact" || terminal.result.error.code === terminal.code);
      if (terminal.code !== "OK" || terminal.revision !== terminal.expectedRevision + 1) return false;
      if (recoveryCommand(e.command as PreparedCommand)) {
        if (terminal.cardRevision !== null || terminal.resultRefs.length !== 1) return false;
        const ref = terminal.resultRefs[0]!;
        if (ref.kind !== "board" || ref.id !== e.context.boardId || ref.revision !== terminal.revision) return false;
        return terminal.retention === "compact" || ("coverageRevision" in terminal.result && terminal.result.coverageRevision === terminal.revision && terminal.result.retentionVersion >= 1);
      }
      if (terminal.cardRevision === null || terminal.cardRevision < 1) return false;
      if (terminal.target && (terminal.expectedCardRevision === null || terminal.cardRevision !== terminal.expectedCardRevision + 1)) return false;
      const extra: Partial<Record<PreparedCommand, string>> = { "attachment.add": "attachment", "comment.add": "comment", "link.add": "link", "link.remove": "link", "approve": "approval" };
      const kinds = ["card", ...(extra[e.command as PreparedCommand] ? [extra[e.command as PreparedCommand]!] : [])];
      if (terminal.resultRefs.length !== kinds.length || new Set(terminal.resultRefs.map(r => r.kind)).size !== kinds.length) return false;
      for (const ref of terminal.resultRefs) {
        if (!kinds.includes(ref.kind)) return false;
        if (ref.kind === "card") {
          if (ref.revision !== terminal.cardRevision || terminal.target && ref.id !== object(parse(JSON.stringify(terminal.target))).cardId) return false;
        } else if (ref.revision !== null) return false;
      }
      if (terminal.retention === "full") {
        for (const [field, id] of Object.entries(terminal.result)) {
          if (!terminal.resultRefs.some(ref => ref.id === id && ref.kind === field.replace(/Id$/, ""))) return false;
        }
        if (terminal.target && object(parse(JSON.stringify(terminal.result))).cardId !== object(parse(JSON.stringify(terminal.target))).cardId) return false;
      }
      return true;
    } catch { return false; }
  },
};
