// L02 synthetic component smoke. Browser storage is real; HTTP/domain replies are
// test doubles. This is neither V02 end-to-end evidence nor power-loss proof.
import { openLocalSession, readLocalBootstrap } from "./local-session.js";
import { localRecoveryReadiness } from "../recovery/local-readiness.js";
import type { SessionRead } from "../../generated/transport/contracts.js";
function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
export async function runLocalSessionSmoke(): Promise<number> {
  let passed = 0;
  const installationId = crypto.randomUUID(), boardId = crypto.randomUUID(), incarnation = crypto.randomUUID();
  const session: SessionRead = { apiVersion: 1, kind: "human", actorId: crypto.randomUUID(), grant: {
    grantId: crypto.randomUUID(), boardId, incarnation, rights: ["create", "read", "read-own-outcome"] } };
  let epoch = crypto.randomUUID(), deny = false, invalid = false, mutations = 0;
  const requests: { path: string; auth: string | null; credentials: RequestCredentials | undefined; header: string | null; body: BodyInit | null | undefined }[] = [];
  const fetcher: typeof fetch = async (input, init = {}) => {
    const headers = new Headers(init.headers), path = String(input);
    requests.push({ path, auth: headers.get("Authorization"), credentials: init.credentials, header: headers.get("X-Localswim-Client"), body: init.body });
    if (path.endsWith("/bootstrap")) return new Response(JSON.stringify({ apiVersion: 1, installationId, boardId, incarnation, epoch, session,
      ...(invalid ? { token: "forbidden" } : {}) }), { headers: { "Cache-Control": "no-store" } });
    if (deny) return new Response("{}", { status: 401 });
    if (path === "/api/v1/session") return new Response(JSON.stringify(session));
    mutations++; throw Error("Synthetic post-transmission disconnect");
  };
  const first = await localRecoveryReadiness(installationId), second = await localRecoveryReadiness(installationId);
  assert(first.profileId === second.profileId && first.qualification.evidenceReference !== second.qualification.evidenceReference, "Profile identity/probe mismatch"); passed++;
  assert((await localRecoveryReadiness(crypto.randomUUID())).profileId !== first.profileId, "Installation profile binding missing"); passed++;
  const nativeTransaction = IDBDatabase.prototype.transaction;
  IDBDatabase.prototype.transaction = function(names: string | string[], mode?: IDBTransactionMode): IDBTransaction {
    return nativeTransaction.call(this, names, mode);
  };
  let relaxedRefused = false;
  try { await localRecoveryReadiness(installationId); } catch { relaxedRefused = true; }
  finally { IDBDatabase.prototype.transaction = nativeTransaction; }
  assert(relaxedRefused, "Relaxed transaction manufactured readiness"); passed++;
  let refused = false;
  try { await localRecoveryReadiness(installationId, { open() { throw Error("denied"); } } as unknown as IDBFactory); } catch { refused = true; }
  assert(refused, "Storage denial manufactured readiness"); passed++;
  invalid = true; refused = false;
  try { await readLocalBootstrap(fetcher); } catch { refused = true; }
  assert(refused, "Unknown bootstrap fields accepted"); passed++; invalid = false;
  const access = await openLocalSession(fetcher);
  try {
    assert(access.recoveryReady, "Actual browser strict storage readiness absent"); passed++;
    const context = { installationId, boardId, incarnation, actorId: session.actorId }, draftId = crypto.randomUUID();
    const draft = await access.client.recovery.saveDraft({ context, draftId, expectedRevision: 0, input: "retained synthetic input" });
    const opId = crypto.randomUUID();
    const outcome = await access.client.submit({ command: "card.create", context, draftId: draft.draft.draftId,
      request: { opId, incarnation, expectedRevision: 0, expectedCardRevision: null, target: null, input: { title: "Synthetic", description: "Retained" } } }, () => {});
    assert(outcome.state === "unknown" && mutations === 1 && !!await access.client.recovery.loadSubmission(context, opId), "UNKNOWN/original evidence lost"); passed++;
    deny = true; try { await access.client.session(); } catch {}
    const before = requests.length; try { await access.client.session(); } catch {}
    assert(requests.length === before, "Unavailable connection reused"); passed++;
    deny = false; epoch = crypto.randomUUID(); await access.reconnect();
    assert(mutations === 1 && !!await access.client.recovery.loadSubmission(context, opId), "Reconnect replayed/replaced original"); passed++;
    assert((await access.client.recovery.inventory()).drafts.length === 1, "Reconnect lost input"); passed++;
    assert(requests.every(r => r.auth === null && r.credentials === "same-origin" && r.header === "browser"), "Cookie transport leaked bearer or omitted browser credentials"); passed++;
    assert(requests.filter(r => r.path.endsWith("/bootstrap")).every(r => r.body === "{}"), "Bootstrap body changed"); passed++;
  } finally { access.clear(); access.client.close(); }
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, "indexedDB");
  Object.defineProperty(globalThis, "indexedDB", { configurable: true, value: undefined });
  let unavailable: Awaited<ReturnType<typeof openLocalSession>> | undefined;
  try {
    unavailable = await openLocalSession(fetcher);
    assert(!unavailable.recoveryReady, "Storage denial granted readiness");
    const before = mutations;
    const outcome = await unavailable.client.submit({ command: "card.create", context: { installationId, boardId, incarnation, actorId: session.actorId },
      draftId: crypto.randomUUID(), request: { opId: crypto.randomUUID(), incarnation, expectedRevision: 0, expectedCardRevision: null,
        target: null, input: { title: "Retain in caller", description: "No storage" } } }, () => {});
    assert(outcome.state === "unsent" && mutations === before, "Storage-denied composition dispatched"); passed++;
  } finally {
    unavailable?.clear(); unavailable?.client.close();
    if (descriptor) Object.defineProperty(globalThis, "indexedDB", descriptor);
    else Reflect.deleteProperty(globalThis, "indexedDB");
  }
  return passed;
}
