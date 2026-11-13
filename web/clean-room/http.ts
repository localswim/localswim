import type { EntityTag, ProblemDto, SessionDto } from "./api-types.js";
import type { ReadFailure } from "./contracts.js";

const apiVersion = "2026-11-10";
const apiPrefix = `/api/${apiVersion}`;
const readTimeoutMs = 15_000;
const maxJsonBytes = 96 * 1024 * 1024;
const problemStatuses = {
  invalid_request: 400,
  unauthorized: 401,
  forbidden: 403,
  not_found: 404,
  conflict: 409,
  precondition_failed: 412,
  precondition_required: 428,
  payload_too_large: 413,
  unsupported_media_type: 415,
  method_not_allowed: 405,
  not_acceptable: 406,
  internal_error: 500,
  unavailable: 503,
  outcome_unknown: 503,
} satisfies Record<ProblemDto["code"], number>;

interface JsonRead {
  readonly data: unknown;
  readonly headers: Headers;
}

interface BrowserCapability {
  readonly token: string;
  readonly session: SessionDto;
}

class ReadError extends Error {
  readonly failure: ReadFailure;

  constructor(failure: ReadFailure) {
    super(failure.kind);
    this.failure = failure;
  }
}

let browserCapability: Promise<BrowserCapability> | undefined;

export function readFailure(error: unknown): ReadFailure {
  return error instanceof ReadError ? error.failure : { kind: "transport" };
}

export function invalidResponse(): never {
  throw new ReadError({ kind: "invalid_response" });
}

export function readDeadline(signal?: AbortSignal): AbortSignal {
  const timeout = AbortSignal.timeout(readTimeoutMs);
  return signal ? AbortSignal.any([signal, timeout]) : timeout;
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function isUnknownArray(value: unknown): value is readonly unknown[] {
  return Array.isArray(value);
}

export function isUuid(value: unknown): value is string {
  return (
    typeof value === "string" &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(value)
  );
}

export function isTimestamp(value: unknown): value is string {
  if (
    typeof value !== "string" ||
    !/^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01]) ([01]\d|2[0-3]):[0-5]\d:[0-5]\d\+00:00$/u.test(
      value,
    )
  ) {
    return false;
  }
  const iso = `${value.slice(0, 10)}T${value.slice(11, 19)}.000Z`;
  const parsed = new Date(iso);
  return Number.isFinite(parsed.valueOf()) && parsed.toISOString() === iso;
}

export function isBoundedText(value: unknown, minimum: number, maximum: number): value is string {
  if (
    typeof value !== "string" ||
    value.length < minimum ||
    value.length > maximum * 2 ||
    !value.isWellFormed()
  ) {
    return false;
  }
  if (value.length <= maximum && value.length >= minimum * 2) {
    return true;
  }
  let length = 0;
  for (const _character of value) {
    length += 1;
  }
  return length >= minimum && length <= maximum;
}

export function entityTag(headers: Headers, name = "ETag"): EntityTag {
  const value = headers.get(name);
  if (value === null || !/^"[A-Za-z0-9_-]{16,128}"$/u.test(value)) {
    return invalidResponse();
  }
  return value;
}

function isProblemCode(value: unknown): value is ProblemDto["code"] {
  return typeof value === "string" && Object.hasOwn(problemStatuses, value);
}

function isFieldIssue(value: unknown): value is ProblemDto["errors"][number] {
  if (!isRecord(value) || Object.keys(value).length !== 2 || !isBoundedText(value.field, 1, 128)) {
    return false;
  }
  switch (value.code) {
    case "required":
    case "invalid_type":
    case "invalid_value":
    case "duplicate":
    case "unknown_reference":
    case "forbidden_field":
    case "incompatible_history":
    case "integrity_failure":
      return true;
    default:
      return false;
  }
}

function isProblem(data: unknown, status: number, method: "GET" | "POST"): data is ProblemDto {
  return (
    isRecord(data) &&
    Object.keys(data).length === 8 &&
    data.type === "about:blank" &&
    isBoundedText(data.title, 1, 120) &&
    data.status === status &&
    isProblemCode(data.code) &&
    problemStatuses[data.code] === status &&
    isBoundedText(data.detail, 0, 512) &&
    isUuid(data.request_id) &&
    data.outcome === (method === "GET" ? "not_applicable" : "not_applied") &&
    isUnknownArray(data.errors) &&
    data.errors.length <= 64 &&
    data.errors.every(isFieldIssue)
  );
}

function decodeJsonChunk(decoder: TextDecoder, bytes?: Uint8Array): string {
  try {
    return decoder.decode(bytes, { stream: bytes !== undefined });
  } catch {
    return invalidResponse();
  }
}

async function readJsonBody(response: Response, signal: AbortSignal): Promise<unknown> {
  if (response.body === null) {
    return invalidResponse();
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true });
  const parts: string[] = [];
  let size = 0;
  try {
    let chunk = await reader.read();
    while (!chunk.done) {
      size += chunk.value.byteLength;
      if (size > maxJsonBytes) {
        return invalidResponse();
      }
      const text = decodeJsonChunk(decoder, chunk.value);
      if (text !== "") {
        parts.push(text);
      }
      chunk = await reader.read();
    }
    parts.push(decodeJsonChunk(decoder));
  } catch (error) {
    // An invalid body is never used, even if canceling its stream also fails.
    void reader.cancel().catch(() => {});
    if (error instanceof ReadError) {
      throw error;
    }
    return abortRead();
  } finally {
    reader.releaseLock();
  }
  if (signal.aborted) {
    return abortRead();
  }
  try {
    const value: unknown = JSON.parse(parts.join(""));
    return value;
  } catch {
    return invalidResponse();
  }
}

async function requestJson(
  path: string,
  method: "GET" | "POST",
  signal: AbortSignal,
  token?: string,
): Promise<JsonRead> {
  const headers = new Headers({ Accept: "application/json, application/problem+json" });
  if (token !== undefined) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  let response: Response;
  try {
    response = await fetch(apiPrefix + path, {
      method,
      headers,
      signal,
      mode: "same-origin",
      credentials: "omit",
      redirect: "error",
      cache: "no-store",
    });
  } catch {
    throw new ReadError({ kind: "transport" });
  }
  const mediaType = response.headers.get("Content-Type")?.split(";", 1)[0]?.trim().toLowerCase();
  const success = response.status === 200;
  if (mediaType !== (success ? "application/json" : "application/problem+json")) {
    void response.body?.cancel().catch(() => {});
    return invalidResponse();
  }
  const data = await readJsonBody(response, signal);
  if (success) {
    return { data, headers: response.headers };
  }
  if (
    !isProblem(data, response.status, method) ||
    data.request_id !== response.headers.get("X-Request-ID")
  ) {
    return invalidResponse();
  }
  throw new ReadError({ kind: "http", problem: data });
}

function humanSession(data: Record<string, unknown>): SessionDto {
  if (
    !isUuid(data.principal_id) ||
    data.kind !== "human" ||
    data.api_version !== apiVersion ||
    typeof data.workspace_admin !== "boolean"
  ) {
    return invalidResponse();
  }
  return {
    principal_id: data.principal_id,
    kind: data.kind,
    api_version: data.api_version,
    workspace_admin: data.workspace_admin,
  };
}

function isCapabilityToken(value: unknown): value is string {
  if (typeof value !== "string" || !/^[A-Za-z0-9_-]{43,128}$/u.test(value)) {
    return false;
  }
  const standard = value.replaceAll("-", "+").replaceAll("_", "/");
  try {
    const bytes = atob(standard);
    return bytes.length >= 32 && btoa(bytes).replace(/=+$/u, "") === standard;
  } catch {
    return false;
  }
}

async function bootstrapBrowser(): Promise<BrowserCapability> {
  // The capability lives only in this module. A caller can leave while another still needs bootstrap.
  const { data } = await requestJson("/browser-session", "POST", readDeadline());
  if (
    !isRecord(data) ||
    Object.keys(data).length !== 6 ||
    data.expires_on_service_replacement !== true ||
    !isCapabilityToken(data.access_token)
  ) {
    return invalidResponse();
  }
  return { token: data.access_token, session: humanSession(data) };
}

function abortRead(): never {
  throw new ReadError({ kind: "transport" });
}

async function awaitCapability(
  pending: Promise<BrowserCapability>,
  signal: AbortSignal,
): Promise<BrowserCapability> {
  let onAbort: (() => void) | undefined;
  const aborted = new Promise<never>((_resolve, reject) => {
    onAbort = () => reject(new ReadError({ kind: "transport" }));
    signal.addEventListener("abort", onAbort, { once: true });
    if (signal.aborted) {
      onAbort();
    }
  });
  try {
    return await Promise.race([pending, aborted]);
  } finally {
    if (onAbort) {
      signal.removeEventListener("abort", onAbort);
    }
  }
}

async function authenticatedRead(
  path: string,
  signal: AbortSignal,
  renew = true,
): Promise<JsonRead & { readonly session: SessionDto }> {
  if (signal.aborted) {
    return abortRead();
  }
  const pending = (browserCapability ??= bootstrapBrowser().catch((error: unknown) => {
    browserCapability = undefined;
    throw error;
  }));
  const { token, session } = await awaitCapability(pending, signal);
  if (signal.aborted) {
    return abortRead();
  }
  try {
    const result = await requestJson(path, "GET", signal, token);
    return { ...result, session };
  } catch (error) {
    const failure = readFailure(error);
    if (failure.kind === "http" && failure.problem.code === "unauthorized") {
      if (browserCapability === pending) {
        browserCapability = undefined;
      }
      if (renew) {
        return authenticatedRead(path, signal, false);
      }
    }
    throw error;
  }
}

export async function readJson(path: string, signal?: AbortSignal): Promise<JsonRead> {
  if (!/^\/[a-z][a-z0-9-]*(\/[a-zA-Z0-9-]+)*(\?[^#]*)?$/u.test(path)) {
    return invalidResponse();
  }
  const { data, headers } = await authenticatedRead(path, readDeadline(signal));
  return { data, headers };
}

export async function readSession(signal?: AbortSignal): Promise<SessionDto> {
  const { data, session } = await authenticatedRead("/session", readDeadline(signal));
  if (!isRecord(data) || Object.keys(data).length !== 4) {
    return invalidResponse();
  }
  const confirmed = humanSession(data);
  if (
    confirmed.principal_id !== session.principal_id ||
    confirmed.workspace_admin !== session.workspace_admin
  ) {
    return invalidResponse();
  }
  return confirmed;
}
