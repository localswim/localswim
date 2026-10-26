import type { Confirmation, Preview, PreviewRequest } from "../../generated/archive/contracts.js";
import type { RequestAttachmentAdd } from "../../generated/contracts.js";
import type { ExportRequest } from "../../generated/transport/contracts.js";
import type { BoardRead, CardRead, MutationRequest, SessionRead, Terminal } from "../../generated/transport/contracts.js";
import { openRecoveryDatabase } from "../recovery/indexeddb.js";
import { RecoveryStore } from "../recovery/store.js";
import type { Context, PreparedEvidence, ProfileQualification, RecoveryDriver } from "../recovery/types.js";
import { RecoveryError } from "../recovery/types.js";
import { boardBoundaryVerifier, canonical, object, parse, prepare, sha256, validate } from "./boundary.js";
import type { BoardCommand, PreparedCommand, Value } from "./boundary.js";

export type OriginalOutcome = Readonly<{
  opId: string;
  state: "pending" | "unknown" | "committed" | "not_committed" | "unsent";
  message: string;
  evidence?: PreparedEvidence;
  terminal?: Terminal;
  conflict?: boolean;
}>;
export type ClientOptions = Readonly<{
  installationId: string;
  profileId: string;
  // Trusted composition supplies V02's actual profile proof. Absent means refusal.
  qualification?: ProfileQualification;
  credential: () => string | undefined;
  authenticationUnavailable?: (credential: string) => void;
  fetch?: typeof fetch;
  databaseName?: string;
}>;
export type Intent = Readonly<{ command: BoardCommand; request: MutationRequest; draftId: string; context: Context }>;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
export class BoardClient {
  readonly recovery: RecoveryStore;
  private readonly recoveryDomain: string;
  // Opaque local coordination identity, never a credential or public wire field.
  // Capture the actual store scope before callers can mutate their options.
  get recoveryDomainKey(): string { return this.recoveryDomain; }
  private readonly fetcher: typeof fetch;
  private readonly previews = new Map<string, Preview>();
  private readonly inFlight = new Set<string>();
  static async open(options: ClientOptions): Promise<BoardClient> {
    const selected: ClientOptions = Object.freeze({ ...options,
      ...(options.qualification ? { qualification: Object.freeze({ ...options.qualification }) } : {}) });
    let driver: RecoveryDriver & { close(): void };
    try { driver = await openRecoveryDatabase(globalThis.indexedDB, selected.databaseName); }
    catch {
      // Recovery denial refuses mutations; independently authenticated reads work.
      driver = { read: async () => { throw new RecoveryError("unavailable"); },
        commit: async () => { throw new RecoveryError("unavailable"); }, close: () => {} };
    }
    return new BoardClient(selected, driver);
  }
  private constructor(private readonly options: ClientOptions, private readonly driver: RecoveryDriver & { close(): void }) {
    this.recoveryDomain = JSON.stringify([location.origin, options.databaseName ?? "localswim-private-recovery-v1", options.installationId]);
    this.fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.recovery = new RecoveryStore(driver, options.installationId,
      options.profileId, boardBoundaryVerifier, options.qualification);
  }
  close(): void { this.driver.close(); }
  private path(boardId: string): string {
    if (!uuid.test(boardId) || boardId === "00000000-0000-0000-0000-000000000000") throw Error("Invalid board identity");
    return "/api/v1/boards/" + boardId;
  }
  private async raw(path: string, init: RequestInit = {}): Promise<Response> {
    if (location.protocol !== "https:") throw Error("HTTPS required");
    const credential = this.options.credential(); if (!credential) throw Error("Authentication unavailable");
    const response = await this.fetcher(path, { ...init, credentials: "omit", cache: "no-store", redirect: "error",
      headers: { "Authorization": "Bearer " + credential, ...(typeof init.body === "string" ? { "Content-Type": "application/json" } : {}) } });
    if (response.status === 401 || response.status === 403) this.options.authenticationUnavailable?.(credential);
    return response;
  }
  private async call(path: string, init: RequestInit = {}): Promise<{ response: Response; value: Value }> {
    const response = await this.raw(path, init);
    const value = parse(new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(await response.arrayBuffer()));
    return { response, value };
  }
  async session(): Promise<SessionRead> {
    const { response, value } = await this.call("/api/v1/session");
    if (!response.ok) throw Error("Authentication unavailable");
    validate("transport:SessionRead", value);
    const session = value as unknown as SessionRead;
    if (session.kind === "automation" && session.grant.rights.some(r => ["approve", "history-admin", "grant-admin"].includes(r))) throw Error("Invalid session rights");
    return session;
  }
  async board(boardId: string): Promise<BoardRead> {
    const { response, value } = await this.call(this.path(boardId));
    if (!response.ok) throw Error("Board unavailable");
    validate("transport:BoardRead", value);
    const board = value as unknown as BoardRead;
    if (board.result.identity.boardId !== boardId) throw Error("Board identity mismatch");
    return board;
  }
  async card(boardId: string, cardId: string): Promise<CardRead> {
    if (!uuid.test(cardId)) throw Error("Invalid card identity");
    const { response, value } = await this.call(this.path(boardId) + "/cards/" + cardId);
    if (!response.ok) throw Error("Current text unavailable");
    validate("transport:CardRead", value);
    const card = value as unknown as CardRead;
    if (card.result.identity.boardId !== boardId || card.result.card.cardId !== cardId) throw Error("Card identity mismatch");
    return card;
  }
  context(board: BoardRead, session: SessionRead): Context {
    const identity = board.result.identity;
    if (session.grant.boardId !== identity.boardId || session.grant.incarnation !== identity.incarnation) throw Error("Session scope mismatch");
    return Object.freeze({ installationId: this.options.installationId, boardId: identity.boardId,
      incarnation: identity.incarnation, actorId: session.actorId });
  }
  private async classify(evidence: PreparedEvidence, value: Value, allowTerminal = true): Promise<OriginalOutcome> {
    const original = object(parse(evidence.requestBytes)), opId = String(original.opId);
    const unknown = (): OriginalOutcome => ({ opId, state: "unknown", evidence, message: "Original outcome unknown. Investigate using the original operation; do not resubmit." });
    try {
      const body = object(value);
      if (body.outcome !== "committed" && body.outcome !== "not_committed") {
        if (body.outcome === "refused") {
          validate("transport:NonterminalRefusal", body);
          if (body.conflict !== null) return { ...unknown(), conflict: true, message: "Conflict. Input retained; current text available for deliberate reconciliation. Original outcome remains unknown without terminal proof." };
        }
        return unknown();
      }
      if (!allowTerminal) return unknown();
      const terminal = value as unknown as Terminal;
      const status = await this.recovery.appendTerminal(evidence.context, opId, terminal);
      return { opId, state: status.outcome, evidence, terminal, message: status.outcome === "committed" ? "Confirmed saved." : "Definitive noncommit. Reconcile current text before a deliberate new submission." };
    } catch { return unknown(); }
  }
  // Deliberate initial dispatch only. Loading original evidence cannot call this path.
  async submit(intent: Intent, announce: (outcome: OriginalOutcome) => void): Promise<OriginalOutcome> {
    return this.dispatch(intent, announce);
  }
  async submitRecovery(intent: Readonly<{ context: Context; draftId: string; request: Confirmation }>, announce: (outcome: OriginalOutcome) => void): Promise<OriginalOutcome> {
    return this.dispatch({ ...structuredClone(intent), command: intent.request.command }, announce);
  }
  async submitAttachment(intent: Readonly<{ context: Context; draftId: string; request: RequestAttachmentAdd; bytes: ArrayBuffer }>, announce: (outcome: OriginalOutcome) => void): Promise<OriginalOutcome> {
    // Copy raw bytes synchronously before the first hash/auth/storage await.
    const bytes = intent.bytes.slice(0);
    return this.dispatch({ ...structuredClone(intent), command: "attachment.add" }, announce, bytes);
  }
  private async dispatch(intent: Readonly<{ command: PreparedCommand; request: MutationRequest | Confirmation; draftId: string; context: Context }>, announce: (outcome: OriginalOutcome) => void, attachmentBytes?: ArrayBuffer): Promise<OriginalOutcome> {
    // Detach from caller mutation before the first authentication/storage await.
    intent = structuredClone(intent);
    const opId = intent.request.opId;
    if (this.inFlight.has(opId)) throw Error("Operation already submitted");
    this.inFlight.add(opId);
    let evidence: PreparedEvidence;
    let dispatchCredential: string | undefined;
    try {
      const credential = this.options.credential(); dispatchCredential = credential;
      const session = await this.session();
      if (session.actorId !== intent.context.actorId || session.grant.boardId !== intent.context.boardId || session.grant.incarnation !== intent.context.incarnation) throw Error("Original actor unavailable");
      if (intent.command === "attachment.add") {
        const facts = intent.request.input as RequestAttachmentAdd["input"];
        if (!attachmentBytes || attachmentBytes.byteLength > 10485760 || attachmentBytes.byteLength !== facts.byteLength || await digestBytes(attachmentBytes) !== facts.sha256) throw Error("Attachment bytes mismatch");
      }
      if (intent.command === "history.clear" || intent.command === "retention.set") {
        const request = intent.request as Confirmation, preview = this.previews.get(request.previewHash);
        if (!preview || session.kind !== "human" || !session.grant.rights.includes("history-admin") || preview.actorId !== session.actorId || preview.boardId !== intent.context.boardId || preview.incarnation !== request.incarnation || preview.command !== intent.command || preview.expectedRevision !== request.expectedRevision || canonical(preview.input as unknown as Value) !== canonical(request.input as unknown as Value) || Date.now() >= Date.parse(preview.expiresAt)) throw Error("Preview unavailable");
        const board = await this.board(intent.context.boardId);
        if (board.result.identity.incarnation !== request.incarnation || board.result.revision !== request.expectedRevision) throw Error("Current revision changed");
      }
      if (!credential || this.options.credential() !== credential) throw Error("Authentication changed");
      evidence = await prepare(intent.command, intent.request, intent.context, intent.draftId);
      if (intent.command === "attachment.add" && new TextEncoder().encode(evidence.requestBytes).length > 16384) throw Error("Attachment metadata too large");
      await this.recovery.freezeSubmission(evidence);
      if (this.options.credential() !== credential) throw Error("Authentication changed");
    } catch {
      const outcome: OriginalOutcome = { opId, state: "unsent", message: "Not dispatched. Input validation, recovery or authentication refused/unavailable/unqualified; input retained. Deliberate action required." };
      announce(outcome); return outcome;
    }
    announce({ opId, state: "pending", evidence, message: "Submitted; awaiting original outcome." });
    const r = object(parse(evidence.requestBytes));
    const target = r.target === null ? null : object(r.target!);
    const base = this.path(evidence.context.boardId) + "/cards";
    const suffix: Record<PreparedCommand, string> = { "attachment.add": "/attachments", "history.clear": "", "retention.set": "", "card.create": "", "card.edit": "", "comment.add": "/comments", "link.add": "/link-add", "link.remove": "/link-remove", "review-handoff": "/review-handoff", "approve": "/approve" };
    const path = intent.command === "history.clear" || intent.command === "retention.set" ? this.path(evidence.context.boardId) + "/recovery/confirm" : base + (target ? "/" + String(target.cardId) : "") + suffix[intent.command];
    let result: OriginalOutcome;
    try {
      if (!dispatchCredential || this.options.credential() !== dispatchCredential) throw Error("Authentication changed");
      let body: BodyInit = evidence.requestBytes;
      if (attachmentBytes) {
        const multipart = new FormData();
        multipart.append("metadata", new Blob([evidence.requestBytes], { type: "application/json" }));
        multipart.append("file", new Blob([attachmentBytes], { type: "application/octet-stream" }));
        body = multipart;
      }
      const { response, value } = await this.call(path, { method: intent.command === "card.edit" ? "PATCH" : "POST", body });
      result = await this.classify(evidence, value, response.status === 200 || response.status === 201);
    } catch { result = { opId, state: "unknown", evidence, message: "Original outcome unknown after possible transmission. Input and original evidence retained." }; }
    announce(result); return result;
  }
  async preview(boardId: string, request: PreviewRequest): Promise<Preview> {
    request = structuredClone(request);
    validate("archive:PreviewRequest", parse(JSON.stringify(request)));
    const credential = this.options.credential(), session = await this.session();
    if (session.kind !== "human" || !session.grant.rights.includes("history-admin") || session.grant.boardId !== boardId || session.grant.incarnation !== request.incarnation) throw Error("History administration unavailable");
    const { response, value } = await this.call(this.path(boardId) + "/recovery/preview", { method: "POST", body: JSON.stringify(request) });
    if (response.status !== 200 || !credential || this.options.credential() !== credential) throw Error("Preview unavailable");
    validate("archive:Preview", value);
    const preview = value as unknown as Preview;
    const inventory = { ...object(value) }; delete inventory.previewHash;
    if (preview.boardId !== boardId || preview.actorId !== session.actorId || preview.incarnation !== request.incarnation || preview.command !== request.command || preview.expectedRevision !== request.expectedRevision || canonical(preview.input as unknown as Value) !== canonical(request.input as unknown as Value) || Date.parse(preview.expiresAt) - Date.parse(preview.previewedAt) !== 300000 || await sha256(canonical(inventory)) !== preview.previewHash) throw Error("Preview mismatch");
    this.previews.clear(); this.previews.set(preview.previewHash, structuredClone(preview));
    return structuredClone(preview);
  }
  private async binary(path: string, type: string, maximum: number, init: RequestInit): Promise<ArrayBuffer> {
    const credential = this.options.credential();
    const response = await this.raw(path, init);
    const length = response.headers.get("Content-Length");
    if (response.status !== 200 || response.headers.get("Content-Type") !== type || response.headers.has("Content-Encoding") || !length || !/^(0|[1-9][0-9]*)$/.test(length) || Number(length) > maximum || !/^attachment(?:;|$)/.test(response.headers.get("Content-Disposition") ?? "")) { await response.body?.cancel(); throw Error("Download unavailable"); }
    const expected = Number(length), reader = response.body?.getReader();
    if (!reader) throw Error("Incomplete download");
    const chunks: Uint8Array[] = []; let size = 0;
    try {
      for (;;) {
        const { done, value } = await reader.read(); if (done) break;
        size += value.byteLength;
        if (size > expected || size > maximum) throw Error("Download too large");
        chunks.push(value);
      }
      if (size !== expected || init.signal?.aborted || !credential || this.options.credential() !== credential) throw Error("Incomplete download");
    } catch (error) { await reader.cancel().catch(() => {}); throw error; }
    const bytes = new Uint8Array(size); let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
    return bytes.buffer;
  }
  async downloadAttachment(boardId: string, id: string, facts: Readonly<{ sha256: string; byteLength: number }>, signal: AbortSignal): Promise<ArrayBuffer> {
    facts = structuredClone(facts);
    const credential = this.options.credential();
    if (!uuid.test(id) || id === "00000000-0000-0000-0000-000000000000") throw Error("Invalid attachment identity");
    // Read current authenticated metadata; a stale editor's claim cannot select bytes.
    const board = await this.board(boardId), attachment = board.result.attachments.find(a => a.attachmentId === id);
    if (!credential || this.options.credential() !== credential || !attachment || attachment.sha256 !== facts.sha256 || attachment.byteLength !== facts.byteLength) throw Error("Attachment metadata changed");
    const bytes = await this.binary(this.path(boardId) + "/attachments/" + id, "application/octet-stream", 10485760, { signal });
    if (bytes.byteLength !== facts.byteLength || await digestBytes(bytes) !== facts.sha256 || signal.aborted || this.options.credential() !== credential) throw Error("Attachment bytes mismatch");
    return bytes;
  }
  async exportArchive(boardId: string, request: ExportRequest, signal: AbortSignal): Promise<ArrayBuffer> {
    request = structuredClone(request);
    const credential = this.options.credential();
    validate("transport:ExportRequest", parse(JSON.stringify(request)));
    const board = await this.board(boardId);
    if (!credential || this.options.credential() !== credential || board.result.identity.incarnation !== request.incarnation || board.result.revision !== request.expectedRevision) throw Error("Current revision changed");
    return this.binary(this.path(boardId) + "/exports", "application/zip", 150994944, { method: "POST", body: JSON.stringify(request), signal });
  }
  async lookup(context: Context, opId: string): Promise<OriginalOutcome> {
    const credential = this.options.credential();
    const unknown: OriginalOutcome = { opId, state: "unknown", message: "Original outcome unavailable; do not resubmit." };
    try {
      const submission = await this.recovery.loadSubmission(context, opId); if (!submission) return unknown;
      const e = submission.evidence, session = await this.session();
      if (session.actorId !== context.actorId || session.grant.boardId !== context.boardId || session.grant.incarnation !== context.incarnation) return unknown;
      if (!credential || this.options.credential() !== credential) return unknown;
      const query = new URLSearchParams({ incarnation: context.incarnation, fingerprintVersion: "1", fingerprint: e.fingerprint });
      const { response, value } = await this.call(this.path(context.boardId) + "/operations/" + opId + "?" + query);
      if (response.status !== 200) return unknown;
      return await this.classify(e, value);
    } catch { return unknown; }
  }
}

async function digestBytes(bytes: ArrayBuffer): Promise<string> {
  return [...new Uint8Array(await crypto.subtle.digest("SHA-256", bytes))].map(n => n.toString(16).padStart(2, "0")).join("");
}
