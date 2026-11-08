import type { Identity } from "../../generated/archive/contracts.js";
import type { ProbeResult, ProbeControlResponse, ProbeStopResponse, ProbeOutcomeUnknown, ProbeRequest, NonterminalRefusal } from "../../generated/transport/contracts.js";
import { object, parse, validate } from "./boundary.js";

export const probeRequestMaximum = 512;
export const probeControlQueryMaximum = 384;
export const probeResponseMaximum = 2048;
const uuid4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const zero = "00000000-0000-0000-0000-000000000000";
const pairs = new Set(["supported/OK", "unavailable/TOOL_MISSING", "unavailable/PLATFORM_UNQUALIFIED",
  "unsupported/TOOL_UNSUPPORTED", "unsupported/PARSE_UNSUPPORTED", "unknown/EXIT_FAILED", "unknown/PARSE_ERROR",
  "unknown/OUTPUT_LIMIT", "unknown/TIMED_OUT", "unknown/CANCELLED", "incomplete/DRAIN_INCOMPLETE"]);
const refusalPairs = new Set(["400/INVALID_INPUT", "401/UNAUTHENTICATED", "403/FORBIDDEN", "404/NOT_FOUND",
  "405/INVALID_INPUT", "409/CONTROL_STALE", "409/CONTROL_BUSY", "409/IDENTITY_CHANGED"]);

export class ProbeBoundaryError extends Error {
  constructor() { super("Invalid identity check response"); }
}
export type ProbeContext = Readonly<{ identity: Identity; runtimeGeneration: string; configurationGeneration: string }>;
export type ProbeOperationContext = ProbeContext & Readonly<{ probeGeneration: string }>;

function validIdentity(value: Identity): boolean {
  return value !== null && typeof value === "object" && Object.keys(value).sort().join() === "boardId,incarnation" &&
    [value.boardId, value.incarnation].every(id => typeof id === "string" && uuid.test(id) && id !== zero);
}
function context(value: ProbeContext, expected: ProbeContext): void {
  if (!validIdentity(expected.identity) || !uuid4.test(expected.runtimeGeneration) || !uuid4.test(expected.configurationGeneration) ||
      !validIdentity(value.identity) || value.identity.boardId !== expected.identity.boardId ||
      value.identity.incarnation !== expected.identity.incarnation || value.runtimeGeneration !== expected.runtimeGeneration ||
      value.configurationGeneration !== expected.configurationGeneration) throw new ProbeBoundaryError();
}
function operation(value: ProbeOperationContext, expected: ProbeOperationContext): void {
  context(value, expected);
  if (!uuid4.test(expected.probeGeneration) || value.probeGeneration !== expected.probeGeneration) throw new ProbeBoundaryError();
}
// Byte callers retain strict UTF-8 and BOM refusal; text callers cannot repair
// invalid Unicode. Transport/header/framing validation belongs to its owner.
function decode(input: string | Uint8Array, limit: number, name: string): unknown {
  try {
    if (typeof input !== "string" && input.byteLength > limit) throw new ProbeBoundaryError();
    const text = typeof input === "string" ? input : new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(input);
    if (!text.isWellFormed() || new TextEncoder().encode(text).byteLength > limit) throw new ProbeBoundaryError();
    const value = parse(text);
    validate("transport:" + name, value);
    return value;
  } catch { throw new ProbeBoundaryError(); }
}
export function validateProbeResult(value: ProbeResult, expected: ProbeOperationContext): void {
  // Revalidate generated shape even for typed callers; types alone are advisory.
  const checked = decode(JSON.stringify(value), probeResponseMaximum, "ProbeResult") as ProbeResult;
  operation(checked, expected);
  if (!pairs.has(checked.state + "/" + checked.code) || checked.capability !== null ||
      checked.joined !== (checked.state !== "incomplete") || (checked.toolVersion !== null) !== (checked.state === "supported")) throw new ProbeBoundaryError();
}
export function parseProbeResult(input: string | Uint8Array, expected: ProbeOperationContext): ProbeResult {
  const value = decode(input, probeResponseMaximum, "ProbeResult") as ProbeResult;
  validateProbeResult(value, expected);
  return structuredClone(value);
}
export function parseProbeControlResponse(input: string | Uint8Array, expected: ProbeContext): ProbeControlResponse {
  const value = decode(input, probeResponseMaximum, "ProbeControlResponse") as ProbeControlResponse;
  context(value.result, expected);
  if (value.result.joined !== (value.result.phase === "ready")) throw new ProbeBoundaryError();
  return structuredClone(value);
}
export function parseProbeStopResponse(input: string | Uint8Array, expected: ProbeOperationContext): ProbeStopResponse {
  const value = decode(input, probeResponseMaximum, "ProbeStopResponse") as ProbeStopResponse;
  operation(value.result, expected);
  if (value.result.joined !== (value.result.phase === "joined")) throw new ProbeBoundaryError();
  return structuredClone(value);
}
export function parseProbeOutcomeUnknown(input: string | Uint8Array): ProbeOutcomeUnknown {
  return structuredClone(decode(input, probeResponseMaximum, "ProbeOutcomeUnknown") as ProbeOutcomeUnknown);
}
export function parseProbeRequest(input: string | Uint8Array): ProbeRequest {
  return structuredClone(decode(input, probeRequestMaximum, "ProbeRequest") as ProbeRequest);
}
// Recognition describes only a disclosure/control refusal. In particular a POST
// refusal proves neither non-execution nor native join; POST503 is never known.
export function parseProbeRefusal(input: string | Uint8Array, status: number, phase: "get" | "post"): NonterminalRefusal {
  const value = decode(input, probeResponseMaximum, "NonterminalRefusal") as NonterminalRefusal;
  const body = object(value as unknown as ReturnType<typeof parse>), error = object(body.error!);
  if (body.opId !== undefined || body.conflict !== null || error.field !== null || error.message !== "Request refused" ||
      !(refusalPairs.has(status + "/" + String(error.code)) || phase === "get" && status === 503 && error.code === "UNAVAILABLE")) throw new ProbeBoundaryError();
  return structuredClone(value);
}
