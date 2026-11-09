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
export class ProbeActionFailure extends Error {
    code;
    phase;
    constructor(code, phase = "preflight") {
        super("Identity check " + code);
        this.code = code;
        this.phase = phase;
    }
}
// Require an exposed fixed-length frame. Browsers can normalize a conflicting
// Content-Length on chunked replies before handing the body to the caller.
export function hasProbeResponseFraming(response) {
    return !response.redirected && response.headers.get("Cache-Control") === "no-store" &&
        response.headers.has("Content-Length") && !response.headers.has("Transfer-Encoding") &&
        !response.headers.has("Content-Encoding") &&
        /^application\/json(?:\s*;\s*charset=utf-8)?$/i.test(response.headers.get("Content-Type") ?? "");
}
export function probeControlQuery(expected) {
    context(expected, expected);
    const query = new URLSearchParams({ incarnation: expected.identity.incarnation,
        expectedRuntimeGeneration: expected.runtimeGeneration, expectedConfigurationGeneration: expected.configurationGeneration }).toString();
    if (new TextEncoder().encode(query).length > probeControlQueryMaximum)
        throw new ProbeBoundaryError();
    return query;
}
export function probeRequestBody(expected) {
    operation(expected, expected);
    const body = JSON.stringify({ incarnation: expected.identity.incarnation,
        expectedRuntimeGeneration: expected.runtimeGeneration, expectedConfigurationGeneration: expected.configurationGeneration,
        probeGeneration: expected.probeGeneration });
    parseProbeRequest(body);
    return body;
}
function validIdentity(value) {
    return value !== null && typeof value === "object" && Object.keys(value).sort().join() === "boardId,incarnation" &&
        [value.boardId, value.incarnation].every(id => typeof id === "string" && uuid.test(id) && id !== zero);
}
function context(value, expected) {
    if (!validIdentity(expected.identity) || !uuid4.test(expected.runtimeGeneration) || !uuid4.test(expected.configurationGeneration) ||
        !validIdentity(value.identity) || value.identity.boardId !== expected.identity.boardId ||
        value.identity.incarnation !== expected.identity.incarnation || value.runtimeGeneration !== expected.runtimeGeneration ||
        value.configurationGeneration !== expected.configurationGeneration)
        throw new ProbeBoundaryError();
}
function operation(value, expected) {
    context(value, expected);
    if (!uuid4.test(expected.probeGeneration) || value.probeGeneration !== expected.probeGeneration)
        throw new ProbeBoundaryError();
}
// Byte callers retain strict UTF-8 and BOM refusal; text callers cannot repair
// invalid Unicode. Transport/header/framing validation belongs to its owner.
function decode(input, limit, name) {
    try {
        if (typeof input !== "string" && input.byteLength > limit)
            throw new ProbeBoundaryError();
        const text = typeof input === "string" ? input : new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(input);
        if (!text.isWellFormed() || new TextEncoder().encode(text).byteLength > limit)
            throw new ProbeBoundaryError();
        const value = parse(text);
        validate("transport:" + name, value);
        return value;
    }
    catch {
        throw new ProbeBoundaryError();
    }
}
export function validateProbeResult(value, expected) {
    // Revalidate generated shape even for typed callers; types alone are advisory.
    const checked = decode(JSON.stringify(value), probeResponseMaximum, "ProbeResult");
    operation(checked, expected);
    if (!pairs.has(checked.state + "/" + checked.code) || checked.capability !== null ||
        checked.joined !== (checked.state !== "incomplete") || (checked.toolVersion !== null) !== (checked.state === "supported"))
        throw new ProbeBoundaryError();
}
export function parseProbeResult(input, expected) {
    const value = decode(input, probeResponseMaximum, "ProbeResult");
    validateProbeResult(value, expected);
    return structuredClone(value);
}
export function parseProbeControlResponse(input, expected) {
    const value = decode(input, probeResponseMaximum, "ProbeControlResponse");
    context(value.result, expected);
    if (value.result.joined !== (value.result.phase === "ready"))
        throw new ProbeBoundaryError();
    return structuredClone(value);
}
export function parseProbeStopResponse(input, expected) {
    const value = decode(input, probeResponseMaximum, "ProbeStopResponse");
    operation(value.result, expected);
    if (value.result.joined !== (value.result.phase === "joined"))
        throw new ProbeBoundaryError();
    return structuredClone(value);
}
export function parseProbeOutcomeUnknown(input) {
    return structuredClone(decode(input, probeResponseMaximum, "ProbeOutcomeUnknown"));
}
export function parseProbeRequest(input) {
    return structuredClone(decode(input, probeRequestMaximum, "ProbeRequest"));
}
// Recognition describes only a disclosure/control refusal. In particular a POST
// refusal proves neither non-execution nor native join; POST503 is never known.
export function parseProbeRefusal(input, status, phase) {
    const value = decode(input, probeResponseMaximum, "NonterminalRefusal");
    const body = object(value), error = object(body.error);
    if (body.opId !== undefined || body.conflict !== null || error.field !== null || error.message !== "Request refused" ||
        !(refusalPairs.has(status + "/" + String(error.code)) || phase === "get" && status === 503 && error.code === "UNAVAILABLE"))
        throw new ProbeBoundaryError();
    return structuredClone(value);
}
