import type { MutationRequest, Terminal } from "../../generated/transport/contracts";
import type {
  BoundaryVerifier, Changes, ClearAcknowledgement, Context, Draft, PreparedEvidence,
  ProfileQualification, RecoveryDriver, Snapshot, Submission, TerminalStatus,
} from "./types";
import { RecoveryError } from "./types";

export const DAY = 86_400_000;
export const DRAFT_LIMIT = 20;
export const DRAFT_BYTES = 2 * 1024 * 1024;
export const UNRESOLVED_LIMIT = 100;
export const SUBMISSION_BYTES = 8 * 1024 * 1024;
const encoder = new TextEncoder();
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const hashPattern = /^[0-9a-f]{64}$/;
const forbidden = /^(authorization|bearer|token|tokenhash|credential|credentials|credentialhash|password|secret|session|sessioncode|code|cookie|privatekey)$/i;

function invalid(): never { throw new RecoveryError("invalid"); }
function uuid(value: unknown): asserts value is string {
  if (typeof value !== "string" || !uuidPattern.test(value) || value === "00000000-0000-0000-0000-000000000000") invalid();
}
function plain(value: unknown): asserts value is Record<string, unknown> {
  if (!value || typeof value !== "object" || Object.getPrototypeOf(value) !== Object.prototype) invalid();
}
function exact(value: unknown, keys: readonly string[]): asserts value is Record<string, unknown> {
  plain(value);
  if (Object.getOwnPropertySymbols(value).length) invalid();
  const actual = Object.getOwnPropertyNames(value).sort();
  if (actual.length !== keys.length || actual.some((key, i) => key !== [...keys].sort()[i])) invalid();
  for (const key of actual) {
    const property = Object.getOwnPropertyDescriptor(value, key);
    if (!property || !("value" in property) || !property.enumerable) invalid();
  }
}
function text(value: unknown): asserts value is string {
  if (typeof value !== "string" || !value.isWellFormed()) invalid();
}
function revision(value: unknown): asserts value is number {
  if (!Number.isSafeInteger(value) || (value as number) < 0) invalid();
}
function context(value: unknown): asserts value is Context {
  exact(value, ["installationId", "boardId", "incarnation", "actorId"]);
  for (const field of Object.values(value)) uuid(field);
}
function clone<T>(value: T): T { return JSON.parse(JSON.stringify(value)) as T; }
function freeze<T>(value: T): T {
  if (value && typeof value === "object") {
    for (const child of Object.values(value)) freeze(child);
    Object.freeze(value);
  }
  return value;
}
function equal(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (!a || !b || typeof a !== "object" || typeof b !== "object") return false;
  const x = Object.keys(a).sort(), y = Object.keys(b).sort();
  return x.length === y.length && x.every((k, i) => k === y[i] && equal((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]));
}
function noAuth(value: unknown): void {
  if (!value || typeof value !== "object") return;
  for (const [key, child] of Object.entries(value)) {
    if (forbidden.test(key)) invalid();
    noAuth(child);
  }
}
// UTF-8 bytes of the complete stored JSON records, including keys and metadata.
export function recordBytes(value: unknown): number { return encoder.encode(JSON.stringify(value)).byteLength; }
function key(c: Context, id: string): string {
  return JSON.stringify([c.installationId, c.boardId, c.incarnation, c.actorId, id]);
}
function liveDraft(draft: Draft, now: number): boolean { return draft.editedAt + 7 * DAY > now; }
function statusFor(snapshot: Snapshot, row: Submission): TerminalStatus | undefined {
  return snapshot.statuses.find(status => status.key === row.key);
}
function terminalStatusReservation(row: Submission): number {
  // Existing generated terminal DTOs use a 20-byte ASCII UTC timestamp. Reserve
  // the longest classification and every possible safe-integer clock width.
  // Keep this budget until the whole resolved record expires or is cleared.
  return recordBytes({ key: row.key, outcome: "not_committed", terminalAt: "9999-12-31T23:59:59Z", recordedAt: Number.MAX_SAFE_INTEGER } satisfies TerminalStatus);
}
function liveSubmission(snapshot: Snapshot, row: Submission, now: number): boolean {
  const status = statusFor(snapshot, row);
  return !status || status.recordedAt + 7 * DAY > now;
}

export type SaveDraft = Readonly<{ context: Context; draftId: string; expectedRevision: number; input: string }>;
export type DraftSave = Readonly<{ state: "saved" | "forked"; draft: Draft }>;
export type DispatchReceipt = Readonly<{ dispatchReady: true; submission: Submission }>;
export type Inventory = Readonly<{
  drafts: readonly Omit<Draft, "input">[];
  submissions: readonly { key: string; context: Context; draftId: string; opId: string; insertedAt: number; outcome: "unknown" | "committed" | "not_committed" }[];
}>;

export class RecoveryStore {
  private readonly qualification: ProfileQualification | undefined;
  constructor(
    private readonly driver: RecoveryDriver,
    private readonly installationId: string,
    private readonly profileId: string,
    private readonly verifier?: BoundaryVerifier,
    qualification?: ProfileQualification,
    private readonly now: () => number = Date.now,
    private readonly newDraftId: () => string = () => crypto.randomUUID(),
  ) {
    uuid(installationId);
    text(profileId);
    if (!profileId) invalid();
    this.qualification = qualification ? freeze(clone(qualification)) : undefined;
  }

  private checkContext(c: Context): void {
    context(c);
    if (c.installationId !== this.installationId) throw new RecoveryError("mismatch");
  }

  private time(): number {
    const now = this.now();
    if (!Number.isSafeInteger(now) || now < 0) invalid();
    return now;
  }

  private async read(): Promise<Snapshot> {
    try { return await this.driver.read(); }
    catch (error) { throw error instanceof RecoveryError ? error : new RecoveryError("unavailable"); }
  }

  private async commit<T>(change: (snapshot: Snapshot) => Changes<T>): Promise<T> {
    try { return await this.driver.commit(change); }
    catch (error) { throw error instanceof RecoveryError ? error : new RecoveryError("unavailable"); }
  }

  private prune(snapshot: Snapshot, now: number): Changes<undefined> {
    const drafts = snapshot.drafts.filter(d => d.context.installationId === this.installationId && !liveDraft(d, now));
    const submissions = snapshot.submissions.filter(s => s.evidence.context.installationId === this.installationId && !liveSubmission(snapshot, s, now));
    return { value: undefined, draftDeletes: drafts.map(d => d.key), submissionDeletes: submissions.map(s => s.key), statusDeletes: submissions.map(s => s.key) };
  }

  async saveDraft(input: SaveDraft): Promise<DraftSave> {
    exact(input, ["context", "draftId", "expectedRevision", "input"]);
    this.checkContext(input.context); uuid(input.draftId); revision(input.expectedRevision); text(input.input);
    const candidate = freeze(clone(input)), now = this.time();
    const result = await this.commit(snapshot => {
      const existing = snapshot.drafts.find(d => d.key === key(candidate.context, candidate.draftId) && liveDraft(d, now));
      const collision = (existing?.revision ?? 0) !== candidate.expectedRevision;
      const id = collision ? this.newDraftId() : candidate.draftId;
      uuid(id);
      const recordKey = key(candidate.context, id);
      if (collision && (id === candidate.draftId || snapshot.drafts.some(d => d.key === recordKey))) throw new RecoveryError("collision");
      if (!collision && existing?.revision === Number.MAX_SAFE_INTEGER) invalid();
      const draft: Draft = {
        key: recordKey, context: clone(candidate.context), draftId: id,
        revision: collision ? 1 : (existing?.revision ?? 0) + 1,
        editedAt: now, input: candidate.input,
        forkedFrom: collision ? candidate.draftId : (existing?.forkedFrom ?? null),
      };
      const retained = snapshot.drafts.filter(d => d.context.installationId === this.installationId && liveDraft(d, now) && d.key !== draft.key);
      if (retained.length + 1 > DRAFT_LIMIT || retained.reduce((n, d) => n + recordBytes(d), recordBytes(draft)) > DRAFT_BYTES) throw new RecoveryError("capacity");
      return { ...this.prune(snapshot, now), value: { state: collision ? "forked" as const : "saved" as const, draft }, draftPuts: [draft] };
    });
    const actual = (await this.read()).drafts.find(d => d.key === result.draft.key);
    // A newer tab may have saved before this readback. Never announce our older
    // bytes as saved in that case; the caller retains input and can reconcile.
    if (!equal(actual, result.draft)) throw new RecoveryError("readback");
    return freeze(clone(result));
  }

  async loadDraft(c: Context, draftId: string): Promise<Draft | undefined> {
    this.checkContext(c); uuid(draftId);
    const now = this.time();
    const draft = (await this.read()).drafts.find(d => d.key === key(c, draftId) && liveDraft(d, now));
    return draft ? freeze(clone(draft)) : undefined;
  }

  // A deliberate user action only. Source remains in its original context.
  async copyDraft(source: Context, draftId: string, destination: Context, newId: string): Promise<DraftSave> {
    this.checkContext(source); this.checkContext(destination); uuid(draftId); uuid(newId);
    if (newId === draftId) throw new RecoveryError("mismatch");
    const old = await this.loadDraft(source, draftId);
    if (!old) throw new RecoveryError("unavailable");
    return this.saveDraft({ context: destination, draftId: newId, expectedRevision: 0, input: old.input });
  }

  private async prepare(input: PreparedEvidence): Promise<{ evidence: PreparedEvidence; request: MutationRequest }> {
    exact(input, ["context", "draftId", "command", "requestBytes", "canonicalBytes", "fingerprintVersion", "fingerprint", "attachment"]);
    this.checkContext(input.context); uuid(input.draftId); text(input.command); text(input.requestBytes); text(input.canonicalBytes);
    text(input.fingerprint);
    if (input.fingerprintVersion !== 1 || !hashPattern.test(input.fingerprint)) invalid();
    if (input.attachment !== null) exact(input.attachment, ["name", "mediaLabel", "sha256", "byteLength"]);
    const evidence = freeze(clone(input));
    let request: MutationRequest;
    try { request = JSON.parse(evidence.requestBytes) as MutationRequest; }
    catch { return invalid(); }
    plain(request); noAuth(request);
    uuid(request.opId);
    if (request.incarnation !== evidence.context.incarnation) throw new RecoveryError("mismatch");
    if ("boardId" in request && request.boardId !== evidence.context.boardId) throw new RecoveryError("mismatch");
    revision(request.expectedRevision);
    if (request.expectedCardRevision !== null) revision(request.expectedCardRevision);
    if (evidence.command === "attachment.add") {
      exact(evidence.attachment, ["name", "mediaLabel", "sha256", "byteLength"]);
      if (!equal(evidence.attachment, request.input)) throw new RecoveryError("mismatch");
    } else if (evidence.attachment !== null) invalid();
    if (!this.verifier) throw new RecoveryError("boundary-unavailable");
    try {
      if (!await this.verifier.prepared(evidence, freeze(request))) invalid();
      const hash = [...new Uint8Array(await crypto.subtle.digest("SHA-256", encoder.encode(evidence.canonicalBytes)))].map(v => v.toString(16).padStart(2, "0")).join("");
      if (hash !== evidence.fingerprint) throw new RecoveryError("mismatch");
    } catch (error) { throw error instanceof RecoveryError ? error : new RecoveryError("boundary-unavailable"); }
    return { evidence, request };
  }

  async freezeSubmission(input: PreparedEvidence): Promise<DispatchReceipt> {
    const q = this.qualification;
    if (!q || q.installationId !== this.installationId || q.profileId !== this.profileId || q.strictDurabilityQualified !== true || !q.evidenceReference) throw new RecoveryError("unqualified");
    const { evidence, request } = await this.prepare(input), now = this.time();
    const row: Submission = { key: key(evidence.context, request.opId), evidence, opId: request.opId, insertedAt: now };
    await this.commit(snapshot => {
      // Even identical duplicate calls are not another permission to dispatch.
      if (snapshot.submissions.some(s => s.key === row.key)) throw new RecoveryError("collision");
      const retained = snapshot.submissions.filter(s => s.evidence.context.installationId === this.installationId && liveSubmission(snapshot, s, now));
      const reservedBytes = [...retained, row].reduce((n, r) => n + recordBytes(r) + terminalStatusReservation(r), 0);
      if (retained.filter(s => !statusFor(snapshot, s)).length + 1 > UNRESOLVED_LIMIT || reservedBytes > SUBMISSION_BYTES) throw new RecoveryError("capacity");
      return { ...this.prune(snapshot, now), value: undefined, submissionAdds: [row] };
    });
    const actual = (await this.read()).submissions.find(s => s.key === row.key);
    if (!equal(actual, row)) throw new RecoveryError("readback");
    return freeze({ dispatchReady: true, submission: clone(row) });
  }

  async loadSubmission(c: Context, opId: string): Promise<Submission | undefined> {
    this.checkContext(c); uuid(opId);
    const snapshot = await this.read(), now = this.time();
    const row = snapshot.submissions.find(s => s.key === key(c, opId) && liveSubmission(snapshot, s, now));
    return row ? freeze(clone(row)) : undefined;
  }

  async appendTerminal(c: Context, opId: string, input: Terminal): Promise<TerminalStatus> {
    this.checkContext(c); uuid(opId);
    const original = await this.loadSubmission(c, opId);
    if (!original) throw new RecoveryError("unavailable");
    // Unknown, missing, denial and HTTP errors are never terminal proof.
    if (!input || (input.outcome !== "committed" && input.outcome !== "not_committed")) throw new RecoveryError("mismatch");
    const terminal = freeze(clone(input));
    const request = JSON.parse(original.evidence.requestBytes) as MutationRequest;
    if (terminal.apiVersion !== 1 || terminal.boardId !== c.boardId || terminal.incarnation !== c.incarnation || terminal.actorId !== c.actorId || terminal.opId !== opId || terminal.fingerprintVersion !== original.evidence.fingerprintVersion || terminal.fingerprint !== original.evidence.fingerprint || terminal.command !== original.evidence.command || terminal.expectedRevision !== request.expectedRevision || !equal(terminal.expectedCardRevision, request.expectedCardRevision) || !equal(terminal.target, request.target)) throw new RecoveryError("mismatch");
    text(terminal.terminalAt);
    if (!this.verifier) throw new RecoveryError("boundary-unavailable");
    try { if (!await this.verifier.terminal(original.evidence, terminal)) invalid(); }
    catch (error) { throw error instanceof RecoveryError ? error : new RecoveryError("boundary-unavailable"); }
    const now = this.time();
    const status: TerminalStatus = { key: original.key, outcome: terminal.outcome, terminalAt: terminal.terminalAt, recordedAt: now };
    // Defensive storage bound on the trusted verifier's output, not a new
    // terminal schema. Every valid generated terminal variant fits its reserve.
    if (recordBytes(status) > terminalStatusReservation(original)) invalid();
    const result = await this.commit(snapshot => {
      if (!equal(snapshot.submissions.find(s => s.key === original.key), original)) throw new RecoveryError("mismatch");
      const prior = statusFor(snapshot, original);
      if (prior) {
        if (prior.outcome !== status.outcome || prior.terminalAt !== status.terminalAt) throw new RecoveryError("mismatch");
        return { value: prior };
      }
      return { ...this.prune(snapshot, now), value: status, statusAdds: [status] };
    });
    if (!equal((await this.read()).statuses.find(s => s.key === result.key), result)) throw new RecoveryError("readback");
    return freeze(clone(result));
  }

  async inventory(): Promise<Inventory> {
    const snapshot = await this.read(), now = this.time();
    return freeze({
      drafts: snapshot.drafts.filter(d => d.context.installationId === this.installationId && liveDraft(d, now)).map(({ input: _input, ...metadata }) => metadata),
      submissions: snapshot.submissions.filter(s => s.evidence.context.installationId === this.installationId && liveSubmission(snapshot, s, now)).map(s => ({ key: s.key, context: clone(s.evidence.context), draftId: s.evidence.draftId, opId: s.opId, insertedAt: s.insertedAt, outcome: statusFor(snapshot, s)?.outcome ?? "unknown" })),
    });
  }

  async clear(selection: Readonly<{ kind: "draft" | "submission"; context: Context; id: string }> | "all", acknowledgement: ClearAcknowledgement): Promise<void> {
    if (!acknowledgement || acknowledgement.discardPrivateProse !== true) throw new RecoveryError("warning-required");
    if (selection !== "all") {
      exact(selection, ["kind", "context", "id"]); this.checkContext(selection.context); uuid(selection.id);
      if (selection.kind !== "draft" && selection.kind !== "submission") invalid();
    }
    const selected = selection === "all" ? "all" : freeze(clone(selection));
    const removed = await this.commit(snapshot => {
      const drafts = snapshot.drafts.filter(d => d.context.installationId === this.installationId && (selected === "all" || selected.kind === "draft" && d.key === key(selected.context, selected.id)));
      const submissions = snapshot.submissions.filter(s => s.evidence.context.installationId === this.installationId && (selected === "all" || selected.kind === "submission" && s.key === key(selected.context, selected.id)));
      if (submissions.some(s => !statusFor(snapshot, s)) && acknowledgement.unresolvedMayBecomeUnknowable !== true) throw new RecoveryError("warning-required");
      const draftDeletes = drafts.map(d => d.key), submissionDeletes = submissions.map(s => s.key);
      return { value: { draftDeletes, submissionDeletes }, draftDeletes, submissionDeletes, statusDeletes: submissionDeletes };
    });
    const after = await this.read();
    if (after.drafts.some(d => removed.draftDeletes.includes(d.key)) || after.submissions.some(s => removed.submissionDeletes.includes(s.key)) || after.statuses.some(s => removed.submissionDeletes.includes(s.key))) throw new RecoveryError("readback");
  }
}
