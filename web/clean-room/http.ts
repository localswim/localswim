import type { EntityTag, ProblemDto, SessionDto, Uuid } from "./api-types.js";
import type { MutationResult, ReadFailure } from "./contracts.js";

const apiVersion = "2026-11-10";
export const apiPrefix = `/api/${apiVersion}`;
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

function resourcePath(path: string): boolean {
  return /^\/[a-z][a-z0-9-]*(\/[a-zA-Z0-9-]+)*(\?[^#]*)?$/u.test(path);
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
  const response = await requestRead(path, method, "application/json", signal, token);
  const mediaType = response.headers.get("Content-Type")?.split(";", 1)[0]?.trim().toLowerCase();
  if (response.status !== 200) return rejectRead(response, method, signal);
  if (mediaType !== "application/json") {
    void response.body?.cancel().catch(() => {});
    return invalidResponse();
  }
  return { data: await readJsonBody(response, signal), headers: response.headers };
}

async function requestRead(
  path: string,
  method: "GET" | "POST",
  accept: string,
  signal: AbortSignal,
  token?: string,
): Promise<Response> {
  const headers = new Headers({ Accept: `${accept}, application/problem+json` });
  if (token !== undefined) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  try {
    return await fetch(apiPrefix + path, {
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
}

async function rejectRead(
  response: Response,
  method: "GET" | "POST",
  signal: AbortSignal,
): Promise<never> {
  const mediaType = response.headers.get("Content-Type")?.split(";", 1)[0]?.trim().toLowerCase();
  if (mediaType !== "application/problem+json") {
    void response.body?.cancel().catch(() => {});
    return invalidResponse();
  }
  const data = await readJsonBody(response, signal);
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
  return withReadCapability(
    signal,
    async ({ token, session }) => ({
      ...(await requestJson(path, "GET", signal, token)),
      session,
    }),
    renew,
  );
}

async function withReadCapability<T>(
  signal: AbortSignal,
  read: (capability: BrowserCapability) => Promise<T>,
  renew = true,
): Promise<T> {
  if (signal.aborted) {
    return abortRead();
  }
  const pending = (browserCapability ??= bootstrapBrowser().catch((error: unknown) => {
    browserCapability = undefined;
    throw error;
  }));
  const capability = await awaitCapability(pending, signal);
  if (signal.aborted) {
    return abortRead();
  }
  try {
    return await read(capability);
  } catch (error) {
    const failure = readFailure(error);
    if (failure.kind === "http" && failure.problem.code === "unauthorized") {
      if (browserCapability === pending) {
        browserCapability = undefined;
      }
      if (renew) {
        return withReadCapability(signal, read, false);
      }
    }
    throw error;
  }
}

export async function readJson(
  path: string,
  signal?: AbortSignal,
  actorId?: Uuid,
): Promise<JsonRead> {
  if (!resourcePath(path)) {
    return invalidResponse();
  }
  const { data, headers, session } = await authenticatedRead(path, readDeadline(signal));
  if (actorId !== undefined && session.principal_id !== actorId) return invalidResponse();
  return { data, headers };
}

export async function readBinary<T>(
  path: string,
  accept: string,
  validate: (response: Response, signal: AbortSignal) => Promise<T>,
  signal?: AbortSignal,
): Promise<T> {
  if (!resourcePath(path)) return invalidResponse();
  const deadline = readDeadline(signal);
  return withReadCapability(deadline, async ({ token }) => {
    const response = await requestRead(path, "GET", accept, deadline, token);
    if (response.status !== 200) return rejectRead(response, "GET", deadline);
    try {
      return await validate(response, deadline);
    } finally {
      void response.body?.cancel().catch(() => {});
    }
  });
}

export interface MutationOptions<T> {
  readonly method: "POST" | "PATCH" | "PUT" | "DELETE";
  readonly etag?: EntityTag;
  readonly boardEtag?: EntityTag;
  readonly body?: unknown;
  readonly validate: (data: unknown, headers: Headers) => T;
  readonly successStatus?: 200 | 201 | 204;
  readonly requestId?: Uuid;
  readonly signal?: AbortSignal;
  readonly beforeDispatch?: (session: SessionDto) => void;
}

export function mutateJson<T>(
  path: string,
  options: MutationOptions<T>,
): Promise<MutationResult<T>> {
  return mutate(path, options);
}

export function mutateForm<T>(
  path: string,
  form: Blob,
  options: Omit<MutationOptions<T>, "body" | "method">,
): Promise<MutationResult<T>> {
  return mutate(path, { ...options, method: "POST" }, form);
}

async function mutate<T>(
  path: string,
  options: MutationOptions<T>,
  form?: Blob,
): Promise<MutationResult<T>> {
  const requestId = options.requestId ?? crypto.randomUUID();
  if (!resourcePath(path) || path === "/browser-session" || !isUuid(requestId)) {
    return invalidResponse();
  }
  const signal = readDeadline(options.signal);
  const unknown = (problem: ProblemDto | null): MutationResult<T> => ({
    outcome: "unknown",
    requestId,
    problem,
  });
  try {
    const pending = (browserCapability ??= bootstrapBrowser().catch((error: unknown) => {
      browserCapability = undefined;
      throw error;
    }));
    const { token, session } = await awaitCapability(pending, signal);
    if (signal.aborted) return unknown(null);
    const headers = new Headers({
      Accept: "application/json, application/problem+json",
      Authorization: `Bearer ${token}`,
      "X-Request-ID": requestId,
    });
    if (form !== undefined) headers.set("Content-Type", form.type);
    else if (options.body !== undefined) headers.set("Content-Type", "application/json");
    if (options.etag !== undefined) headers.set("If-Match", options.etag);
    if (options.boardEtag !== undefined) headers.set("X-Expected-Board-ETag", options.boardEtag);
    options.beforeDispatch?.(session);
    const response = await fetch(apiPrefix + path, {
      method: options.method,
      headers,
      body: form ?? JSON.stringify(options.body),
      signal,
      mode: "same-origin",
      credentials: "omit",
      redirect: "error",
      cache: "no-store",
    });
    const success = response.status === (options.successStatus ?? 200);
    if (
      success &&
      response.status === 204 &&
      response.headers.get("X-Request-ID") === requestId &&
      response.body === null
    )
      return { outcome: "committed", value: options.validate(undefined, response.headers) };
    const media = response.headers.get("Content-Type")?.split(";", 1)[0]?.trim().toLowerCase();
    if (
      response.headers.get("X-Request-ID") !== requestId ||
      media !== (success ? "application/json" : "application/problem+json")
    ) {
      void response.body?.cancel().catch(() => {});
      return unknown(null);
    }
    const data = await readJsonBody(response, signal);
    if (success) return { outcome: "committed", value: options.validate(data, response.headers) };
    if (isProblem(data, response.status, "POST") && data.request_id === requestId) {
      if (data.code === "unauthorized" && browserCapability === pending) {
        browserCapability = undefined;
      }
      if (data.outcome === "not_applied") {
        return { outcome: "not_applied", problem: { ...data, outcome: data.outcome } };
      }
    }
    if (
      isRecord(data) &&
      data.outcome === "unknown" &&
      data.code === "outcome_unknown" &&
      data.request_id === requestId &&
      isProblem({ ...data, outcome: "not_applied" }, response.status, "POST")
    ) {
      const valid = { ...data, outcome: "not_applied" };
      if (isProblem(valid, response.status, "POST"))
        return unknown({ ...valid, outcome: "unknown" });
    }
    return unknown(null);
  } catch {
    // A dispatched write has one attempt. Its caller owns authoritative inspection.
    return unknown(null);
  }
}

export async function openEventStream(
  boardId: Uuid | null,
  signal: AbortSignal,
): Promise<Response> {
  if (boardId !== null && !isUuid(boardId)) return invalidResponse();
  const path = boardId === null ? "/events" : `/events?board_id=${boardId}`;
  for (let attempt = 0; attempt < 2; attempt += 1) {
    if (signal.aborted) return abortRead();
    const pending = (browserCapability ??= bootstrapBrowser().catch((error: unknown) => {
      browserCapability = undefined;
      throw error;
    }));
    const { token } = await awaitCapability(pending, readDeadline(signal));
    const response = await fetch(apiPrefix + path, {
      method: "GET",
      headers: { Accept: "text/event-stream", Authorization: `Bearer ${token}` },
      signal,
      mode: "same-origin",
      credentials: "omit",
      redirect: "error",
      cache: "no-store",
    });
    const media = response.headers.get("Content-Type")?.split(";", 1)[0]?.trim().toLowerCase();
    if (response.status === 200 && media === "text/event-stream" && response.body) return response;
    if (media === "application/problem+json") {
      const data = await readJsonBody(response, readDeadline(signal));
      if (
        isProblem(data, response.status, "GET") &&
        data.request_id === response.headers.get("X-Request-ID")
      ) {
        if (data.code === "unauthorized") {
          if (browserCapability === pending) browserCapability = undefined;
          if (attempt === 0) continue;
        }
        throw new ReadError({ kind: "http", problem: data });
      }
    }
    void response.body?.cancel().catch(() => {});
    return invalidResponse();
  }
  return abortRead();
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
