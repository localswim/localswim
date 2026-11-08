import type { Identity } from "../../generated/archive/contracts.js";
import type { InstallationInspectionResponse, InstallationInspectionResult } from "../../generated/transport/contracts.js";
import { object, parse, validate } from "./boundary.js";

export const installationInspectionMaximum = 2048;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const uuid4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const zero = "00000000-0000-0000-0000-000000000000";
const pairs = new Set([
  "candidate/CANDIDATE_PRESENT", "missing/TOOL_MISSING",
  "unqualified/PLATFORM_UNQUALIFIED", "unqualified/SOURCE_UNQUALIFIED",
  "unqualified/PATH_UNQUALIFIED", "unqualified/PUBLISHER_UNQUALIFIED",
  "unqualified/FILE_CHANGED", "unqualified/INSPECTION_UNKNOWN", "unqualified/INSPECTION_TIMED_OUT",
]);
const refusals = new Set([
  "400/INVALID_INPUT", "401/UNAUTHENTICATED", "403/FORBIDDEN",
  "404/NOT_FOUND", "405/INVALID_INPUT", "409/CONTROL_STALE", "409/CONTROL_BUSY",
  "409/IDENTITY_CHANGED", "503/UNAVAILABLE",
]);

export class InstallationInspectionUnavailable extends Error {
  constructor() { super("Installation inspection unavailable"); }
}
export class InstallationInspectionRefusal extends Error {
  constructor(readonly code: string) { super("Installation inspection refused"); }
}

// The owning BoardClient supplies its ordinary same-origin authenticated raw
// transport. assertCurrent binds the caller's selected context and view epoch;
// equal board IDs alone cannot admit a late A-to-B-to-A result.
export type InstallationInspectionTransport = Readonly<{
  raw(path: string, init: RequestInit): Promise<{ response: Response; credential: string }>;
  sameConnection(credential: string): void;
  assertCurrent(): void;
}>;

function validIdentity(identity: Identity): boolean {
  return identity !== null && typeof identity === "object" &&
    Object.keys(identity).sort().join() === "boardId,incarnation" &&
    [identity.boardId, identity.incarnation].every(id => typeof id === "string" && uuid.test(id) && id !== zero);
}

export function installationInspectionPath(identity: Identity, expectedRuntimeGeneration: string): string {
  if (!validIdentity(identity) || typeof expectedRuntimeGeneration !== "string" || !uuid4.test(expectedRuntimeGeneration)) throw new InstallationInspectionUnavailable();
  const query = new URLSearchParams({ incarnation: identity.incarnation, expectedRuntimeGeneration }).toString();
  if (new TextEncoder().encode(query).length > 256) throw new InstallationInspectionUnavailable();
  return "/api/v1/boards/" + identity.boardId + "/recipient/tool/inspect?" + query;
}

export function validateInstallationInspection(result: InstallationInspectionResult, identity: Identity, expectedRuntimeGeneration: string): void {
  if (!validIdentity(identity) || typeof expectedRuntimeGeneration !== "string" || !uuid4.test(expectedRuntimeGeneration) ||
      !result || typeof result !== "object" || Object.keys(result).sort().join() !== "code,identity,runtimeGeneration,state" || !validIdentity(result.identity) ||
      result.identity.boardId !== identity.boardId || result.identity.incarnation !== identity.incarnation ||
      result.runtimeGeneration !== expectedRuntimeGeneration || !uuid4.test(result.runtimeGeneration) ||
      !pairs.has(result.state + "/" + result.code)) throw new InstallationInspectionUnavailable();
}

export function parseInstallationInspection(text: string, identity: Identity, expectedRuntimeGeneration: string): InstallationInspectionResult {
  if (new TextEncoder().encode(text).length > installationInspectionMaximum) throw new InstallationInspectionUnavailable();
  const value = parse(text);
  validate("transport:InstallationInspectionResponse", value);
  const result = (value as unknown as InstallationInspectionResponse).result;
  validateInstallationInspection(result, identity, expectedRuntimeGeneration);
  return structuredClone(result);
}

export async function inspectInstallation(transport: InstallationInspectionTransport, identity: Identity,
  expectedRuntimeGeneration: string, signal: AbortSignal): Promise<InstallationInspectionResult> {
  identity = structuredClone(identity);
  const path = installationInspectionPath(identity, expectedRuntimeGeneration);
  const requestSignal = AbortSignal.any([signal, AbortSignal.timeout(20_000)]);
  let response: Response | undefined;
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  let refusal: InstallationInspectionRefusal | undefined;
  const current = (credential?: string): void => {
    requestSignal.throwIfAborted();
    transport.assertCurrent();
    if (credential !== undefined) transport.sameConnection(credential);
  };
  try {
    current();
    const received = await transport.raw(path, { method: "GET", signal: requestSignal });
    response = received.response;
    current(received.credential);
    if (response.redirected || response.headers.get("Cache-Control") !== "no-store" ||
        response.headers.has("Content-Encoding") ||
        !/^application\/json(?:\s*;\s*charset=utf-8)?$/i.test(response.headers.get("Content-Type") ?? "")) throw new InstallationInspectionUnavailable();
    const declared = response.headers.get("Content-Length");
    if (declared !== null && (!/^(0|[1-9][0-9]*)$/.test(declared) || Number(declared) > installationInspectionMaximum)) throw new InstallationInspectionUnavailable();
    reader = response.body?.getReader();
    if (!reader) throw new InstallationInspectionUnavailable();
    const chunks: Uint8Array[] = [];
    let size = 0;
    for (;;) {
      const chunk = await reader.read();
      current(received.credential);
      if (chunk.done) break;
      size += chunk.value.byteLength;
      if (size > installationInspectionMaximum) throw new InstallationInspectionUnavailable();
      chunks.push(chunk.value);
    }
    if (declared !== null && Number(declared) !== size) throw new InstallationInspectionUnavailable();
    const bytes = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
    const text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes);
    if (response.status !== 200) {
      const value = parse(text);
      validate("transport:NonterminalRefusal", value);
      const body = object(value), error = object(body.error!);
      current(received.credential);
      if (body.opId === undefined && body.conflict === null && error.field === null && error.message === "Request refused" &&
          refusals.has(response.status + "/" + String(error.code))) {
        refusal = new InstallationInspectionRefusal(String(error.code));
        throw refusal;
      }
      throw new InstallationInspectionUnavailable();
    }
    const result = parseInstallationInspection(text, identity, expectedRuntimeGeneration);
    current(received.credential);
    return result;
  } catch (error) {
    if (reader) await reader.cancel().catch(() => {});
    else if (response) await response.body?.cancel().catch(() => {});
    if (refusal !== undefined && error === refusal) throw refusal;
    throw new InstallationInspectionUnavailable();
  } finally {
    reader?.releaseLock();
  }
}
