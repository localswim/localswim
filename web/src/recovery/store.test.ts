// Synthetic fixtures only. This suite is callable in an isolated browser page.
// Its narrow verifier is a TEST DOUBLE, not a shipped canonical/schema provider.
import type { MutationRequest, Terminal } from "../../generated/transport/contracts";
import type { BoundaryVerifier, Changes, Context, PreparedEvidence, ProfileQualification, RecoveryDriver, Snapshot } from "./types";
import { RecoveryError } from "./types";
import { IndexedDBDriver, openRecoveryDatabase } from "./indexeddb";
import { DAY, DRAFT_BYTES, DRAFT_LIMIT, RecoveryStore, SUBMISSION_BYTES, UNRESOLVED_LIMIT, recordBytes } from "./store";

const id = (n: number): string => `10000000-0000-0000-0000-${n.toString(16).padStart(12, "0")}`;
const base: Context = { installationId: id(1), boardId: id(2), incarnation: id(3), actorId: id(4) };
const qualified: ProfileQualification = { installationId: id(1), profileId: "isolated-test-profile", strictDurabilityQualified: true, evidenceReference: "synthetic-test-only-no-product-qualification" };
const copy = <T>(value: T): T => structuredClone(value);
function assert(condition: unknown, message = "assertion"): asserts condition { if (!condition) throw new Error(message); }
async function rejects(action: () => Promise<unknown>, code: RecoveryError["code"]): Promise<void> {
  try { await action(); } catch (error) { assert(error instanceof RecoveryError && error.code === code, `expected ${code}`); return; }
  throw new Error(`expected rejection ${code}`);
}
async function hash(value: string): Promise<string> {
  return [...new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value)))].map(n => n.toString(16).padStart(2, "0")).join("");
}

// Sample-specific validation to exercise the mandatory seam. Canonical input is
// deliberately a test sentinel, not a second implementation of Go canonical JSON.
const verifier: BoundaryVerifier = {
  async prepared(evidence, request) {
    return evidence.command === "card.create" && evidence.canonicalBytes === `synthetic canonical ${request.opId}` &&
      Object.keys(request).sort().join() === "expectedCardRevision,expectedRevision,incarnation,input,opId,target" &&
      request.target === null && request.expectedCardRevision === null &&
      Object.keys(request.input).sort().join() === "description,title";
  },
  async terminal(_evidence, terminal) {
    return terminal.retention === "full" && terminal.terminalAt === "2026-10-26T00:00:00Z" &&
      ((terminal.outcome === "committed" && terminal.code === "OK") || (terminal.outcome === "not_committed" && terminal.code === "CONFLICT"));
  },
};
async function evidence(op = 100, c = base, description = "Synthetic submitted prose"): Promise<PreparedEvidence> {
  const request: MutationRequest = { opId: id(op), incarnation: c.incarnation, target: null, expectedRevision: 0, expectedCardRevision: null, input: { title: "Synthetic title", description } };
  const canonicalBytes = `synthetic canonical ${request.opId}`;
  return { context: copy(c), draftId: id(10), command: "card.create", requestBytes: JSON.stringify(request), canonicalBytes, fingerprintVersion: 1, fingerprint: await hash(canonicalBytes), attachment: null };
}
// Independent budget fixture includes the existing status format's worst case.
async function sizedEvidence(op: number, c: Context, now: number, budget: number): Promise<PreparedEvidence> {
  const empty = await evidence(op, c, "");
  const key = JSON.stringify([c.installationId, c.boardId, c.incarnation, c.actorId, id(op)]);
  const record = { key, evidence: empty, opId: id(op), insertedAt: now };
  const status = { key, outcome: "not_committed", terminalAt: "9999-12-31T23:59:59Z", recordedAt: Number.MAX_SAFE_INTEGER };
  const available = budget - recordBytes(record) - recordBytes(status);
  assert(available >= 0);
  return evidence(op, c, "\u{1f30a}".repeat(Math.floor(available / 4)) + "x".repeat(available % 4));
}
function terminal(e: PreparedEvidence, outcome: "committed" | "not_committed" = "committed"): Terminal {
  const request = JSON.parse(e.requestBytes) as MutationRequest;
  const common = { apiVersion: 1 as const, boardId: e.context.boardId, incarnation: e.context.incarnation, actorId: e.context.actorId, opId: request.opId, fingerprintVersion: 1 as const, fingerprint: e.fingerprint, command: "card.create" as const, expectedRevision: 0, expectedCardRevision: null, target: null, terminalAt: "2026-10-26T00:00:00Z", retention: "full" as const, removal: null, byteFacts: [], resultRefs: [] };
  return outcome === "committed"
    ? { ...common, outcome, code: "OK", result: { cardId: id(20) }, revision: 1, cardRevision: 1 }
    : { ...common, resultRefs: [], outcome, code: "CONFLICT", result: { conflict: null, error: { code: "CONFLICT", field: null, message: "Synthetic conflict" } }, revision: null, cardRevision: null };
}

class MemoryDriver implements RecoveryDriver {
  snapshot: Snapshot = { drafts: [], submissions: [], statuses: [] };
  failRead = false;
  failCommit = false;
  tamperRead = false;
  hold: Promise<void> | undefined;
  async read(): Promise<Snapshot> {
    if (this.failRead) throw new Error("synthetic read denial");
    const result = copy(this.snapshot);
    return this.tamperRead ? { ...result, submissions: [] } : result;
  }
  async commit<T>(change: (snapshot: Snapshot) => Changes<T>): Promise<T> {
    const changes = change(copy(this.snapshot));
    if (this.hold) await this.hold;
    if (this.failCommit) throw new Error("synthetic transaction abort");
    const drafts = this.snapshot.drafts.filter(r => !changes.draftDeletes?.includes(r.key));
    for (const row of changes.draftPuts ?? []) {
      const prior = drafts.findIndex(d => d.key === row.key);
      if (prior >= 0) drafts.splice(prior, 1);
      drafts.push(copy(row));
    }
    this.snapshot = {
      drafts,
      submissions: [...this.snapshot.submissions.filter(r => !changes.submissionDeletes?.includes(r.key)), ...copy(changes.submissionAdds ?? [])],
      statuses: [...this.snapshot.statuses.filter(r => !changes.statusDeletes?.includes(r.key)), ...copy(changes.statusAdds ?? [])],
    };
    return changes.value;
  }
}

export async function runRecoveryTests(): Promise<{ browserCases: number; faultCases: number; names: string[] }> {
  const names: string[] = [];
  let browserCases = 0, faultCases = 0, draftNumber = 1000, now = Date.UTC(2026, 9, 26);
  const store = (driver: RecoveryDriver, q: ProfileQualification | undefined = qualified, v: BoundaryVerifier | undefined = verifier) => new RecoveryStore(driver, id(1), "isolated-test-profile", v, q, () => now, () => id(draftNumber++));
  const test = async (name: string, kind: "browser" | "fault", fn: () => Promise<void>) => {
    await fn(); names.push(name); if (kind === "browser") browserCases++; else faultCases++;
  };
  const databaseName = `localswim-w01-isolated-${crypto.randomUUID()}`;
  let driver = await openRecoveryDatabase(indexedDB, databaseName);
  let recovery = store(driver);
  try {
    await test("draft revision CAS race preserves both inputs", "browser", async () => {
      await recovery.saveDraft({ context: base, draftId: id(10), expectedRevision: 0, input: "Synthetic base" });
      const results = await Promise.allSettled([
        recovery.saveDraft({ context: base, draftId: id(10), expectedRevision: 1, input: "Synthetic tab A" }),
        recovery.saveDraft({ context: base, draftId: id(10), expectedRevision: 1, input: "Synthetic tab B" }),
      ]);
      assert(results.every(r => r.status === "fulfilled"));
      assert(results.filter(r => r.status === "fulfilled" && r.value.state === "forked").length === 1);
      const all = await driver.read();
      assert(all.drafts.some(d => d.input === "Synthetic tab A") && all.drafts.some(d => d.input === "Synthetic tab B"));
    });
    await test("complete submitted bytes independent of editable draft and reload", "browser", async () => {
      const prepared = await evidence();
      const receipt = await recovery.freezeSubmission(prepared);
      assert(receipt.dispatchReady && Object.isFrozen(receipt.submission.evidence.context));
      await recovery.saveDraft({ context: base, draftId: id(10), expectedRevision: 2, input: "Synthetic later edit" });
      driver.close(); driver = await openRecoveryDatabase(indexedDB, databaseName); recovery = store(driver);
      assert((await recovery.loadSubmission(base, id(100)))?.evidence.requestBytes === prepared.requestBytes);
      assert((await recovery.inventory()).submissions[0].outcome === "unknown");
      await rejects(() => recovery.freezeSubmission(prepared), "collision");
      assert((await recovery.loadDraft(base, id(10)))?.input === "Synthetic later edit");
    });
    await test("identity mismatch and nonterminal responses preserve uncertainty", "browser", async () => {
      const prepared = await evidence();
      for (const field of ["boardId", "incarnation", "actorId", "opId", "fingerprint", "command", "expectedRevision", "expectedCardRevision", "target"] as const) {
        const wrong = { ...terminal(prepared), [field]: field === "expectedRevision" ? 9 : field === "expectedCardRevision" ? 2 : field === "target" ? { cardId: id(900) } : id(900) } as unknown as Terminal;
        await rejects(() => recovery.appendTerminal(base, id(100), wrong), "mismatch");
      }
      for (const outcome of ["unknown", "refused", undefined]) await rejects(() => recovery.appendTerminal(base, id(100), { outcome, status: 403 } as unknown as Terminal), "mismatch");
      assert((await recovery.inventory()).submissions[0].outcome === "unknown");
    });
    await test("matching late terminal appends separately and never rewrites draft", "browser", async () => {
      const prepared = await evidence(), original = (await recovery.loadSubmission(base, id(100)))!;
      const status = await recovery.appendTerminal(base, id(100), terminal(prepared));
      assert(status.outcome === "committed");
      assert(JSON.stringify(await recovery.loadSubmission(base, id(100))) === JSON.stringify(original));
      assert((await recovery.loadDraft(base, id(10)))?.input === "Synthetic later edit");
      await recovery.appendTerminal(base, id(100), terminal(prepared));
      assert((await driver.read()).statuses.length === 1);
      await rejects(() => recovery.appendTerminal(base, id(100), terminal(prepared, "not_committed")), "mismatch");
    });
    await test("new incarnation requires deliberate copy with a new draft identity", "browser", async () => {
      const destination = { ...base, incarnation: id(33) };
      assert(!await recovery.loadSubmission(destination, id(100)));
      await rejects(() => recovery.copyDraft(base, id(10), destination, id(10)), "mismatch");
      await recovery.copyDraft(base, id(10), destination, id(77));
      assert((await recovery.loadDraft(destination, id(77)))?.input === "Synthetic later edit");
      assert((await recovery.loadSubmission(base, id(100)))?.evidence.context.incarnation === base.incarnation);
    });
    await test("secret fields and arbitrary response bodies are not retained", "browser", async () => {
      await rejects(() => recovery.saveDraft({ context: base, draftId: id(88), expectedRevision: 0, input: "Synthetic", token: "SYNTHETIC-NOT-A-REAL-TOKEN" } as never), "invalid");
      const prepared = await evidence(102);
      for (const field of ["authorization", "bearer", "token", "tokenHash", "credential", "sessionCode", "privateKey"]) {
        const bad = { ...prepared, requestBytes: JSON.stringify({ ...JSON.parse(prepared.requestBytes), [field]: "SYNTHETIC-NOT-A-REAL-SECRET" }) };
        await rejects(() => recovery.freezeSubmission(bad), "invalid");
      }
      assert(!JSON.stringify(await driver.read()).includes("SYNTHETIC-NOT-A-REAL"));
      assert(!JSON.stringify(await recovery.inventory()).includes("Synthetic later edit"));
    });
    await test("warned clear one/all and unresolved warning are separate", "browser", async () => {
      const prepared = await evidence(103); await recovery.freezeSubmission(prepared);
      await rejects(() => recovery.clear("all", { discardPrivateProse: true }), "warning-required");
      assert((await recovery.inventory()).drafts.length > 0);
      await recovery.clear({ kind: "submission", context: base, id: id(100) }, { discardPrivateProse: true });
      assert(!await recovery.loadSubmission(base, id(100)) && !!await recovery.loadSubmission(base, id(103)));
      await recovery.clear("all", { discardPrivateProse: true, unresolvedMayBecomeUnknowable: true });
      assert((await recovery.inventory()).drafts.length === 0 && (await recovery.inventory()).submissions.length === 0);
    });
    await test("draft count cap spans boards and never evicts live input", "browser", async () => {
      for (let n = 0; n < DRAFT_LIMIT; n++) await recovery.saveDraft({ context: { ...base, boardId: id(200 + n) }, draftId: id(300 + n), expectedRevision: 0, input: `Synthetic ${n}` });
      await rejects(() => recovery.saveDraft({ context: base, draftId: id(500), expectedRevision: 0, input: "Synthetic retained by caller" }), "capacity");
      assert((await recovery.inventory()).drafts.length === DRAFT_LIMIT);
    });
    await test("inclusive seven-day draft expiry permits replacement without deleting new input", "browser", async () => {
      now += 7 * DAY - 1;
      assert((await recovery.inventory()).drafts.length === DRAFT_LIMIT);
      now++;
      assert((await recovery.inventory()).drafts.length === 0);
      await recovery.saveDraft({ context: { ...base, boardId: id(200) }, draftId: id(300), expectedRevision: 0, input: "Synthetic replacement" });
      assert((await recovery.inventory()).drafts.length === 1);
      assert((await recovery.loadDraft({ ...base, boardId: id(200) }, id(300)))?.input === "Synthetic replacement");
    });
    await test("UTF-8 draft byte cap uses complete record bytes", "browser", async () => {
      await recovery.clear("all", { discardPrivateProse: true });
      const blank = (await recovery.saveDraft({ context: base, draftId: id(600), expectedRevision: 0, input: "" })).draft;
      const count = Math.floor((DRAFT_BYTES - recordBytes({ ...blank, revision: 2 })) / 4);
      const input = "\u{1f30a}".repeat(count);
      await recovery.saveDraft({ context: base, draftId: id(600), expectedRevision: 1, input });
      assert(recordBytes((await recovery.loadDraft(base, id(600)))!) <= DRAFT_BYTES);
      await rejects(() => recovery.saveDraft({ context: base, draftId: id(600), expectedRevision: 2, input: input + "\u{1f30a}" }), "capacity");
      assert((await recovery.loadDraft(base, id(600)))?.input === input);
      await recovery.clear("all", { discardPrivateProse: true });
    });
    await test("100 unresolved across boards persist indefinitely and cap new evidence", "browser", async () => {
      for (let n = 0; n < UNRESOLVED_LIMIT; n++) await recovery.freezeSubmission(await evidence(700 + n, { ...base, boardId: id(900 + n) }));
      await rejects(async () => recovery.freezeSubmission(await evidence(850)), "capacity");
      now += 400 * DAY;
      assert((await recovery.inventory()).submissions.length === UNRESOLVED_LIMIT);
      await rejects(() => recovery.clear("all", { discardPrivateProse: true }), "warning-required");
      await recovery.clear("all", { discardPrivateProse: true, unresolvedMayBecomeUnknowable: true });
    });
    await test("exact 8 MiB reserved boundary refuses one byte excess and still resolves", "browser", async () => {
      const prepared = await sizedEvidence(860, base, now, SUBMISSION_BYTES);
      const tooLarge = { ...prepared, requestBytes: prepared.requestBytes.replace('"description":"', '"description":"x') };
      await rejects(() => recovery.freezeSubmission(tooLarge), "capacity");
      assert((await recovery.inventory()).submissions.length === 0);
      const receipt = await recovery.freezeSubmission(prepared);
      const maxStatus = { key: receipt.submission.key, outcome: "not_committed", terminalAt: "9999-12-31T23:59:59Z", recordedAt: Number.MAX_SAFE_INTEGER };
      assert(recordBytes(receipt.submission) + recordBytes(maxStatus) === SUBMISSION_BYTES);
      await rejects(async () => recovery.freezeSubmission(await evidence(861)), "capacity");
      await recovery.appendTerminal(base, id(860), terminal(prepared, "not_committed"));
      const actual = await driver.read();
      assert(actual.statuses.length === 1 && actual.statuses[0].outcome === "not_committed");
      assert(JSON.stringify(actual.submissions[0]) === JSON.stringify(receipt.submission));
      assert([...actual.submissions, ...actual.statuses].reduce((n, row) => n + recordBytes(row), 0) <= SUBMISSION_BYTES);
      await rejects(async () => recovery.freezeSubmission(await evidence(861)), "capacity");
      assert((await recovery.inventory()).submissions.length === 1);
      await recovery.clear({ kind: "submission", context: base, id: id(860) }, { discardPrivateProse: true });
      await recovery.freezeSubmission(await evidence(861));
      await recovery.clear("all", { discardPrivateProse: true, unresolvedMayBecomeUnknowable: true });
    });
    await test("cross-board cap race reserves terminal space for every accepted record", "browser", async () => {
      const a = await evidence(862, { ...base, boardId: id(886) });
      const b = await evidence(863, { ...base, boardId: id(887) });
      const aKey = JSON.stringify([base.installationId, a.context.boardId, base.incarnation, base.actorId, id(862)]);
      const aCost = recordBytes({ key: aKey, evidence: a, opId: id(862), insertedAt: now }) + recordBytes({ key: aKey, outcome: "not_committed", terminalAt: "9999-12-31T23:59:59Z", recordedAt: Number.MAX_SAFE_INTEGER });
      const first = await sizedEvidence(864, base, now, SUBMISSION_BYTES - aCost);
      const initial = await recovery.freezeSubmission(first);
      const race = await Promise.allSettled([
        recovery.freezeSubmission(a), recovery.freezeSubmission(b),
        recovery.appendTerminal(base, id(864), terminal(first)),
      ]);
      assert(race[2].status === "fulfilled");
      const admissions = race.slice(0, 2);
      assert(admissions.filter(r => r.status === "fulfilled").length === 1);
      assert(admissions.some(r => r.status === "rejected" && r.reason instanceof RecoveryError && r.reason.code === "capacity"));
      const accepted = admissions[0].status === "fulfilled" ? a : b;
      const acceptedId = (JSON.parse(accepted.requestBytes) as MutationRequest).opId;
      await recovery.appendTerminal(accepted.context, acceptedId, terminal(accepted, "not_committed"));
      const actual = await driver.read();
      assert(actual.submissions.length === 2 && actual.statuses.length === 2);
      assert(JSON.stringify(actual.submissions.find(s => s.opId === id(864))) === JSON.stringify(initial.submission));
      assert([...actual.submissions, ...actual.statuses].reduce((n, row) => n + recordBytes(row), 0) <= SUBMISSION_BYTES);
      await rejects(async () => recovery.freezeSubmission(await evidence(865)), "capacity");
      await recovery.clear("all", { discardPrivateProse: true });
      await recovery.freezeSubmission(await evidence(865));
      await recovery.clear("all", { discardPrivateProse: true, unresolvedMayBecomeUnknowable: true });
    });
    await test("resolved retention is seven days since status recording, inclusive", "browser", async () => {
      const prepared = await sizedEvidence(870, base, now, SUBMISSION_BYTES); await recovery.freezeSubmission(prepared);
      now += 100 * DAY; await recovery.appendTerminal(base, id(870), terminal(prepared, "not_committed"));
      now += 7 * DAY - 1; assert(!!await recovery.loadSubmission(base, id(870)));
      await rejects(async () => recovery.freezeSubmission(await evidence(871)), "capacity");
      now++; assert(!await recovery.loadSubmission(base, id(870)));
      await recovery.freezeSubmission(await evidence(871));
      assert(!(await driver.read()).submissions.some(s => s.opId === id(870)));
      await recovery.clear("all", { discardPrivateProse: true, unresolvedMayBecomeUnknowable: true });
    });
    await test("installation scope and clear preserve other installation data", "browser", async () => {
      const other = new RecoveryStore(driver, id(88), "isolated-test-profile", verifier, undefined, () => now);
      const c = { ...base, installationId: id(88) };
      await other.saveDraft({ context: c, draftId: id(20), expectedRevision: 0, input: "Synthetic other installation" });
      await rejects(() => recovery.loadDraft(c, id(20)), "mismatch");
      await recovery.clear("all", { discardPrivateProse: true });
      assert((await other.loadDraft(c, id(20)))?.input === "Synthetic other installation");
    });
    await test("actual IDB request success then abort never claims submission or terminal persistence", "browser", async () => {
      // Exercise the production adapter with a factory/transaction wrapper that
      // aborts its genuine IDB tx after the submission add request succeeds.
      const abortName = `${databaseName}-abort`;
      const raw = await new Promise<IDBDatabase>((resolve, reject) => {
        const req = indexedDB.open(abortName, 1);
        req.onupgradeneeded = () => { for (const name of ["drafts", "submissions", "statuses"]) req.result.createObjectStore(name, { keyPath: "key" }); };
        req.onsuccess = () => resolve(req.result); req.onerror = () => reject(req.error);
      });
      let successObserved = false;
      let abortStore: string | null = "submissions";
      const wrapped = new Proxy(raw, { get(target, property) {
        if (property !== "transaction") { const value = Reflect.get(target, property, target); return typeof value === "function" ? value.bind(target) : value; }
        return (...args: Parameters<IDBDatabase["transaction"]>) => {
          const tx = target.transaction(...args);
          return new Proxy(tx, { get(t, p) {
            if (p !== "objectStore") { const value = Reflect.get(t, p, t); return typeof value === "function" ? value.bind(t) : value; }
            return (name: string) => {
              const object = t.objectStore(name);
              if (name !== abortStore || t.mode !== "readwrite") return object;
              return new Proxy(object, { get(o, q) {
                if (q !== "add") { const value = Reflect.get(o, q, o); return typeof value === "function" ? value.bind(o) : value; }
                return (row: unknown) => { const req = o.add(row); req.addEventListener("success", () => { successObserved = true; t.abort(); }); return req; };
              } });
            };
          }, set(t, p, v) { return Reflect.set(t, p, v, t); } });
        };
      }, set(t, p, v) { return Reflect.set(t, p, v, t); } });
      const abortDriver = new IndexedDBDriver(wrapped);
      await rejects(async () => store(abortDriver).freezeSubmission(await evidence(880)), "unavailable");
      assert(successObserved && (await abortDriver.read()).submissions.length === 0);
      abortStore = null;
      const prepared = await evidence(880), original = await store(abortDriver).freezeSubmission(prepared);
      abortStore = "statuses"; successObserved = false;
      await rejects(() => store(abortDriver).appendTerminal(base, id(880), terminal(prepared)), "unavailable");
      const after = await abortDriver.read();
      assert(successObserved && after.statuses.length === 0);
      assert(JSON.stringify(after.submissions[0]) === JSON.stringify(original.submission));
      abortDriver.close(); indexedDB.deleteDatabase(abortName);
    });
  } finally { driver.close(); indexedDB.deleteDatabase(databaseName); }

  await test("unqualified and missing boundary provider refuse before commit", "fault", async () => {
    const memory = new MemoryDriver();
    const unqualified = new RecoveryStore(memory, id(1), "isolated-test-profile", verifier);
    await rejects(async () => unqualified.freezeSubmission(await evidence()), "unqualified");
    const noProvider = new RecoveryStore(memory, id(1), "isolated-test-profile", undefined, qualified);
    await rejects(async () => noProvider.freezeSubmission(await evidence()), "boundary-unavailable");
    await rejects(async () => store(memory, { ...qualified, profileId: "wrong-profile" }).freezeSubmission(await evidence()), "unqualified");
    assert(memory.snapshot.submissions.length === 0);
  });
  await test("transaction failure preserves caller input and has no receipt", "fault", async () => {
    const memory = new MemoryDriver(); memory.failCommit = true;
    const prepared = await evidence(), original = prepared.requestBytes;
    await rejects(() => store(memory).freezeSubmission(prepared), "unavailable");
    assert(prepared.requestBytes === original && memory.snapshot.submissions.length === 0);
  });
  await test("quota error refuses persistence without discarding input", "fault", async () => {
    const quota: RecoveryDriver = {
      async read() { return { drafts: [], submissions: [], statuses: [] }; },
      async commit() { throw new DOMException("Synthetic quota", "QuotaExceededError"); },
    };
    const input = { context: base, draftId: id(10), expectedRevision: 0, input: "Synthetic input retained by caller" };
    await rejects(() => store(quota).saveDraft(input), "unavailable");
    assert(input.input === "Synthetic input retained by caller");
    await rejects(async () => store(quota).freezeSubmission(await evidence()), "unavailable");
  });
  await test("late transaction completion cannot produce an early receipt", "fault", async () => {
    const memory = new MemoryDriver(); let release!: () => void;
    memory.hold = new Promise<void>(resolve => { release = resolve; });
    let finished = false; const prepared = await evidence();
    const pending = store(memory).freezeSubmission(prepared).then(() => { finished = true; });
    await new Promise(resolve => setTimeout(resolve, 20)); assert(!finished);
    release(); await pending; assert(finished);
  });
  await test("postcommit fresh read denial or mismatch never permits dispatch", "fault", async () => {
    const memory = new MemoryDriver(); memory.failRead = true;
    await rejects(async () => store(memory).freezeSubmission(await evidence()), "unavailable");
    assert(memory.snapshot.submissions.length === 1);
    const altered = new MemoryDriver(); altered.tamperRead = true;
    await rejects(async () => store(altered).freezeSubmission(await evidence()), "readback");
    assert(altered.snapshot.submissions.length === 1);
  });
  await test("terminal transaction failure preserves unresolved immutable evidence", "fault", async () => {
    const memory = new MemoryDriver(), prepared = await evidence();
    const original = await store(memory).freezeSubmission(prepared);
    memory.failCommit = true;
    await rejects(() => store(memory).appendTerminal(base, id(100), terminal(prepared)), "unavailable");
    assert(memory.snapshot.statuses.length === 0);
    assert(JSON.stringify(memory.snapshot.submissions[0]) === JSON.stringify(original.submission));
    memory.failCommit = false;
    await store(memory).appendTerminal(base, id(100), terminal(prepared));
    assert((await memory.read()).statuses.length === 1);
  });
  await test("terminal postcommit read failure is reported and idempotent retry verifies stored status", "fault", async () => {
    const memory = new MemoryDriver(), prepared = await evidence();
    const original = await store(memory).freezeSubmission(prepared);
    const postcommitFailure: RecoveryDriver = {
      read: () => memory.read(),
      async commit(change) {
        const value = await memory.commit(change);
        memory.failRead = true;
        return value;
      },
    };
    await rejects(() => store(postcommitFailure).appendTerminal(base, id(100), terminal(prepared)), "unavailable");
    assert(memory.snapshot.statuses.length === 1);
    assert(JSON.stringify(memory.snapshot.submissions[0]) === JSON.stringify(original.submission));
    memory.failRead = false;
    await store(memory).appendTerminal(base, id(100), terminal(prepared));
    assert(memory.snapshot.statuses.length === 1);
  });
  await test("caller mutation during asynchronous preparation cannot change frozen bytes", "fault", async () => {
    const memory = new MemoryDriver(), prepared = await evidence();
    let release!: () => void; const hold = new Promise<void>(resolve => { release = resolve; });
    const delayed: BoundaryVerifier = { ...verifier, async prepared(e, r) { await hold; return verifier.prepared(e, r); } };
    const pending = store(memory, qualified, delayed).freezeSubmission(prepared);
    (prepared.context as { actorId: string }).actorId = id(999);
    (prepared as { requestBytes: string }).requestBytes = "Synthetic changed caller bytes";
    release(); const receipt = await pending;
    assert(receipt.submission.evidence.context.actorId === base.actorId);
    assert(receipt.submission.evidence.requestBytes !== prepared.requestBytes);
  });
  await test("unsupported/denied IDB and absent strict durability fail closed", "fault", async () => {
    await rejects(() => openRecoveryDatabase(undefined), "unavailable");
    const fake = { transaction() { return { durability: "relaxed", abort() {} }; }, close() {} } as unknown as IDBDatabase;
    const unsupported = new IndexedDBDriver(fake);
    await rejects(async () => store(unsupported).freezeSubmission(await evidence()), "unavailable");
    const denied = { open() { throw new DOMException("Synthetic denial", "SecurityError"); } } as unknown as IDBFactory;
    await rejects(() => openRecoveryDatabase(denied), "unavailable");
  });
  await test("hash mismatch, attachment mismatch and verifier refusal reject evidence", "fault", async () => {
    const memory = new MemoryDriver();
    await rejects(async () => store(memory).freezeSubmission({ ...await evidence(), fingerprint: "a".repeat(64) }), "mismatch");
    await rejects(async () => store(memory).freezeSubmission({ ...await evidence(), attachment: { name: "Synthetic", mediaLabel: "text/plain", sha256: "a".repeat(64), byteLength: 1 } }), "invalid");
    await rejects(async () => store(memory).freezeSubmission({ ...await evidence(), canonicalBytes: "Unverified synthetic input" }), "invalid");
    assert(memory.snapshot.submissions.length === 0);
  });
  return { browserCases, faultCases, names };
}
