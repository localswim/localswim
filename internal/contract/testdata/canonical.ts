// Test-only independent canonical reference. This is not browser domain code.
import type { RequestCardCreate, RequestCardEdit } from "./contracts";

const shape: RequestCardCreate = {
  opId: "synthetic", incarnation: "synthetic", target: null,
  expectedRevision: 0, input: { title: "A", description: "" },
};
void shape;
// @ts-expect-error Explicit null is not an omitted optional priority.
const nullPriority: RequestCardCreate["input"] = { title: "A", description: "", priority: null };
// @ts-expect-error A client cannot supply authenticated actor authority.
const clientActor: RequestCardCreate = { ...shape, actorId: "client" };
// @ts-expect-error Existing-card mutation requires card revision.
const nullCardRevision: RequestCardEdit = { ...shape, target: { cardId: "C" }, expectedCardRevision: null, input: {} };
// @ts-expect-error Exact optional fields reject explicit undefined.
const undefinedPriority: RequestCardCreate["input"] = { title: "A", description: "", priority: undefined };
void nullPriority; void clientActor; void nullCardRevision; void undefinedPriority;

type Value = null | boolean | string | number | Value[] | { [key: string]: Value };
declare function require(name: "node:fs"): { readFileSync(fd: number, encoding: "utf8"): string };
declare function require(name: "node:crypto"): { createHash(name: "sha256"): { update(bytes: Uint8Array): { digest(encoding: "hex"): string } } };
const fs = require("node:fs");
const crypto = require("node:crypto");

function scalarString(s: string): void {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c >= 0xdc00 && c <= 0xdfff) throw Error("unpaired surrogate");
    if (c >= 0xd800 && c <= 0xdbff) {
      const next = s.charCodeAt(++i);
      if (!(next >= 0xdc00 && next <= 0xdfff)) throw Error("unpaired surrogate");
    }
  }
}

function parse(bytes: Uint8Array): Value {
  const s = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes);
  let p = 0;
  const ws = (): void => { while (/[ \r\n\t]/.test(s[p] ?? "!") && p < s.length) p++; };
  const string = (): string => {
    const start = p++;
    while (p < s.length) {
      const c = s[p++];
      if (c === "\\") { p++; continue; }
      if (c === '"') {
        const v: unknown = JSON.parse(s.slice(start, p));
        if (typeof v !== "string") throw Error("string");
        scalarString(v); return v;
      }
    }
    throw Error("unterminated string");
  };
  const value = (depth: number): Value => {
    ws(); const c = s[p];
    if (c === '"') return string();
    if (c === "{" || c === "[") {
      if (depth >= 32) throw Error("depth");
      p++; ws();
      if (c === "{") {
        const result: { [key: string]: Value } = Object.create(null) as { [key: string]: Value };
        if (s[p] === "}") { p++; return result; }
        for (;;) {
          ws(); if (s[p] !== '"') throw Error("key");
          const key = string(); if (Object.hasOwn(result, key)) throw Error("duplicate key");
          ws(); if (s[p++] !== ":") throw Error("colon");
          result[key] = value(depth + 1); ws();
          const end = s[p++]; if (end === "}") return result;
          if (end !== ",") throw Error("comma");
        }
      }
      const result: Value[] = [];
      if (s[p] === "]") { p++; return result; }
      for (;;) {
        result.push(value(depth + 1)); ws(); const end = s[p++];
        if (end === "]") return result;
        if (end !== ",") throw Error("comma");
      }
    }
    for (const literal of ["null", "true", "false"] as const) {
      if (s.startsWith(literal, p)) { p += literal.length; return literal === "null" ? null : literal === "true"; }
    }
    const n = /^[0-9]+/.exec(s.slice(p));
    if (!n || (n[0].length > 1 && n[0][0] === "0")) throw Error("integer");
    p += n[0].length;
    const number = Number(n[0]); if (!Number.isSafeInteger(number)) throw Error("safe integer");
    return number;
  };
  const result = value(0); ws(); if (p !== s.length) throw Error("trailing JSON"); return result;
}

function canonical(v: Value): string {
  if (v === null || typeof v === "boolean" || typeof v === "number") return String(v);
  if (typeof v === "string") {
    scalarString(v);
    return '"' + v.replace(/[\u0000-\u001f"\\]/g, c => c === '"' || c === "\\" ? "\\" + c : "\\u" + c.charCodeAt(0).toString(16).padStart(4, "0")) + '"';
  }
  if (Array.isArray(v)) return "[" + v.map(canonical).join(",") + "]";
  const keys = Object.keys(v).sort();
  for (const key of keys) if (/[^\x00-\x7f]/.test(key)) throw Error("ASCII schema key");
  return "{" + keys.map(key => canonical(key) + ":" + canonical(v[key]!)).join(",") + "}";
}
function unhex(hex: string): Uint8Array {
  return Uint8Array.from(hex.match(/../g) ?? [], s => Number.parseInt(s, 16));
}
function hex(bytes: Uint8Array): string { return Array.from(bytes, n => n.toString(16).padStart(2, "0")).join(""); }
const vectors = JSON.parse(fs.readFileSync(0, "utf8")) as { inputHex: string; canonicalHex?: string; sha256?: string }[];
let passed = 0;
for (const vector of vectors) {
  let result: Uint8Array;
  try { result = new TextEncoder().encode(canonical(parse(unhex(vector.inputHex)))); }
  catch (error) { if (vector.canonicalHex !== undefined) throw error; passed++; continue; }
  if (vector.canonicalHex === undefined) throw Error("accepted rejected input");
  if (hex(result) !== vector.canonicalHex || crypto.createHash("sha256").update(result).digest("hex") !== vector.sha256) throw Error("canonical byte/hash mismatch");
  passed++;
}
console.log(JSON.stringify({ passed }));
