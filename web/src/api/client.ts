import type { BoardRead, CardRead, MutationRequest, SessionRead, Terminal } from "../../generated/transport/contracts";
import { openRecoveryDatabase } from "../recovery/indexeddb";
import { RecoveryStore } from "../recovery/store";
import type { Context, PreparedEvidence, ProfileQualification, RecoveryDriver } from "../recovery/types";
import { RecoveryError } from "../recovery/types";
import { boardBoundaryVerifier, object, parse, prepare, validate } from "./boundary";
import type { BoardCommand, Value } from "./boundary";

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
  fetch?: typeof fetch;
  databaseName?: string;
}>;
export type Intent = Readonly<{ command: BoardCommand; request: MutationRequest; draftId: string; context: Context }>;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
export class BoardClient {
  readonly recovery: RecoveryStore;
  private readonly fetcher: typeof fetch;
  private readonly inFlight = new Set<string>();
  static async open(options: ClientOptions): Promise<BoardClient> {
    let driver: RecoveryDriver & { close(): void };
    try { driver = await openRecoveryDatabase(globalThis.indexedDB, options.databaseName); }
    catch {
      // Recovery denial refuses mutations; independently authenticated reads work.
      driver = { read: async () => { throw new RecoveryError("unavailable"); },
        commit: async () => { throw new RecoveryError("unavailable"); }, close: () => {} };
    }
    return new BoardClient(options, driver);
  }
  private constructor(private readonly options: ClientOptions, private readonly driver: RecoveryDriver & { close(): void }) {
    this.fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.recovery = new RecoveryStore(driver, options.installationId,
      options.profileId, boardBoundaryVerifier, options.qualification);
  }
  close(): void { this.driver.close(); }
  private path(boardId: string): string {
    if (!uuid.test(boardId) || boardId === "00000000-0000-0000-0000-000000000000") throw Error("Invalid board identity");
    return "/api/v1/boards/" + boardId;
  }
  private async call(path: string, init: RequestInit = {}): Promise<{ response: Response; value: Value }> {
    if (location.protocol !== "https:") throw Error("HTTPS required");
    const credential = this.options.credential(); if (!credential) throw Error("Authentication unavailable");
    const response = await this.fetcher(path, { ...init, credentials: "omit", cache: "no-store", redirect: "error",
      headers: { "Authorization": "Bearer " + credential, ...(init.body ? { "Content-Type": "application/json" } : {}) } });
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
  private async classify(evidence: PreparedEvidence, value: Value): Promise<OriginalOutcome> {
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
      const terminal = value as unknown as Terminal;
      const status = await this.recovery.appendTerminal(evidence.context, opId, terminal);
      return { opId, state: status.outcome, evidence, terminal, message: status.outcome === "committed" ? "Confirmed saved." : "Definitive noncommit. Reconcile current text before a deliberate new submission." };
    } catch { return unknown(); }
  }
  // Deliberate initial dispatch only. Loading original evidence cannot call this path.
  async submit(intent: Intent, announce: (outcome: OriginalOutcome) => void): Promise<OriginalOutcome> {
    // Detach from caller mutation before the first authentication/storage await.
    intent = structuredClone(intent);
    const opId = intent.request.opId;
    if (this.inFlight.has(opId)) throw Error("Operation already submitted");
    this.inFlight.add(opId);
    let evidence: PreparedEvidence;
    try {
      const session = await this.session();
      if (session.actorId !== intent.context.actorId || session.grant.boardId !== intent.context.boardId || session.grant.incarnation !== intent.context.incarnation) throw Error("Original actor unavailable");
      evidence = await prepare(intent.command, intent.request, intent.context, intent.draftId);
      await this.recovery.freezeSubmission(evidence);
    } catch {
      const outcome: OriginalOutcome = { opId, state: "unsent", message: "Not dispatched. Input validation, recovery or authentication refused/unavailable/unqualified; input retained. Deliberate action required." };
      announce(outcome); return outcome;
    }
    announce({ opId, state: "pending", evidence, message: "Submitted; awaiting original outcome." });
    const r = object(parse(evidence.requestBytes));
    const target = r.target === null ? null : object(r.target!);
    const base = this.path(evidence.context.boardId) + "/cards";
    const suffix: Record<BoardCommand, string> = { "card.create": "", "card.edit": "", "comment.add": "/comments", "link.add": "/link-add", "link.remove": "/link-remove", "review-handoff": "/review-handoff", "approve": "/approve" };
    const path = base + (target ? "/" + String(target.cardId) : "") + suffix[intent.command];
    let result: OriginalOutcome;
    try {
      const { value } = await this.call(path, { method: intent.command === "card.edit" ? "PATCH" : "POST", body: evidence.requestBytes });
      result = await this.classify(evidence, value);
    } catch { result = { opId, state: "unknown", evidence, message: "Original outcome unknown after possible transmission. Input and original evidence retained." }; }
    announce(result); return result;
  }
  async lookup(context: Context, opId: string): Promise<OriginalOutcome> {
    const unknown: OriginalOutcome = { opId, state: "unknown", message: "Original outcome unavailable; do not resubmit." };
    try {
      const submission = await this.recovery.loadSubmission(context, opId); if (!submission) return unknown;
      const e = submission.evidence, session = await this.session();
      if (session.actorId !== context.actorId || session.grant.boardId !== context.boardId || session.grant.incarnation !== context.incarnation) return unknown;
      const query = new URLSearchParams({ incarnation: context.incarnation, fingerprintVersion: "1", fingerprint: e.fingerprint });
      const { value } = await this.call(this.path(context.boardId) + "/operations/" + opId + "?" + query);
      return await this.classify(e, value);
    } catch { return unknown; }
  }
}
