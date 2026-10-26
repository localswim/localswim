import type { Confirmation, MutationRequest, SessionRead, Terminal } from "../../generated/transport/contracts";
import type { Context } from "../recovery/types";
import { boardBoundaryVerifier, canonical, parse, prepare, validate } from "./boundary";
import type { PreparedCommand, Value } from "./boundary";
import { openSessionClient } from "./w03-session";

function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
export async function runW03Boundary(vectors: readonly { command: PreparedCommand; semantic: string; terminal: Terminal }[]): Promise<number> {
  let passed = 0;
  for (const vector of vectors) {
    const semantic = JSON.parse(vector.semantic) as Record<string, Value>;
    const context: Context = { installationId: "00000000-0000-4000-8000-000000000090", boardId: semantic.boardId as string, incarnation: semantic.incarnation as string, actorId: semantic.actorId as string };
    const payload = semantic.payload as Record<string, Value>;
    const recovery = vector.command === "history.clear" || vector.command === "retention.set";
    const request = { opId: semantic.opId, incarnation: semantic.incarnation, target: semantic.target, expectedRevision: semantic.expectedRevision,
      expectedCardRevision: semantic.expectedCardRevision, input: recovery ? payload.input : payload,
      ...(recovery ? { command: vector.command, previewHash: payload.previewHash } : {}) } as unknown as MutationRequest | Confirmation;
    const e = await prepare(vector.command, request, context, crypto.randomUUID());
    assert(e.canonicalBytes === vector.semantic, "Accepted W03 canonical bytes differ");
    assert(await boardBoundaryVerifier.prepared(e, request as MutationRequest), "Accepted W03 evidence rejected"); passed += 2;
    const tampered = { ...e, attachment: e.attachment ? { ...e.attachment, byteLength: e.attachment.byteLength + 1 } : { name: "x", mediaLabel: "", byteLength: 0, sha256: "0".repeat(64) } };
    assert(!await boardBoundaryVerifier.prepared(tampered, request as MutationRequest), "Unbound attachment metadata accepted"); passed++;
    for (const retention of ["full", "compact"] as const) {
      const terminal = { ...vector.terminal, ...(retention === "compact" ? { retention, result: null, removal: { boardRevision: 7, reason: "expired", removedAt: "2026-10-25T01:00:00Z" } } : {}) } as Terminal;
      assert(await boardBoundaryVerifier.terminal(e, terminal), "Accepted W03 terminal rejected"); passed++;
      assert(!await boardBoundaryVerifier.terminal(e, { ...terminal, byteFacts: [{ sha256: "0".repeat(64), byteLength: 0 }] } as Terminal), "Unbound byte facts accepted"); passed++;
      assert(!await boardBoundaryVerifier.terminal(e, { ...terminal, resultRefs: [] } as unknown as Terminal), "Missing result refs accepted"); passed++;
      const refused = { ...terminal, outcome: "not_committed", code: "REVISION_CONFLICT", revision: null, cardRevision: null, resultRefs: [],
        result: retention === "full" ? { error: { code: "REVISION_CONFLICT", message: "Synthetic", field: null }, conflict: null } : null } as Terminal;
      assert(await boardBoundaryVerifier.terminal(e, refused), "Bound definitive noncommit rejected"); passed++;
    }
    let refused = false;
    try { await prepare(vector.command, { ...request, extra: true } as unknown as MutationRequest, context, crypto.randomUUID()); } catch { refused = true; }
    assert(refused, "Unknown W03 wire key accepted"); passed++;
  }
  for (const name of ["transport:ExchangeRequest", "transport:ExchangeSecret", "transport:SessionRevoked", "archive:Preview", "archive:Confirmation"]) {
    let refused = false; try { validate(name, parse('{"apiVersion":2}')); } catch { refused = true; }
    assert(refused, "Incomplete/version-2 W03 DTO accepted"); passed++;
  }
  return passed;
}

export async function runW03SessionRaces(session: SessionRead): Promise<number> {
  const requests: { path: string; method: string; authorization: string | null; body: string | undefined }[] = [];
  const pending: (() => void)[] = [];
  let holdExchange = false, mismatch = false, status = 200;
  const token = "A".repeat(43), code = "A".repeat(22);
  const fetcher: typeof fetch = async (input, init = {}) => {
    const path = String(input), headers = new Headers(init.headers);
    requests.push({ path, method: init.method ?? "GET", authorization: headers.get("Authorization"), body: typeof init.body === "string" ? init.body : undefined });
    if (path.endsWith("/exchange")) {
      if (holdExchange) await new Promise<void>(resolve => pending.push(resolve));
      const issued = Math.floor(Date.now() / 1000) * 1000;
      const time = (offset: number): string => new Date(issued + offset).toISOString().replace(".000Z", "Z");
      return new Response(JSON.stringify({ apiVersion: 1, token, session, issuedAt: time(0), idleExpiresAt: time(1800000), expiresAt: time(28800000) }), { status: 200, headers: { "Cache-Control": "no-store" } });
    }
    if (init.method === "DELETE") return new Response('{"apiVersion":1,"scope":"session","outcome":"revoked"}', { status: 200 });
    return new Response(JSON.stringify(mismatch ? { ...session, actorId: crypto.randomUUID() } : session), { status });
  };
  const access = await openSessionClient({ installationId: crypto.randomUUID(), profileId: "synthetic-W03-race", databaseName: "synthetic-w03-session-" + crypto.randomUUID(), fetch: fetcher });
  let invalidations = 0; const remove = access.onUnavailable(() => { invalidations++; });
  try {
    const current = await access.exchange(code);
    assert(canonical(current as unknown as Value) === canonical(session as unknown as Value), "Exchange identity did not derive current session");
    assert(requests[0]!.path === "/api/v1/session/exchange" && requests[0]!.authorization === null && requests[0]!.body === JSON.stringify({ code }), "Exchange transport leaked or changed code");
    assert(requests[1]!.authorization === "Bearer " + token, "Exchange skipped independent current-bearer read");
    const beforeLogout = invalidations, loggedOut = access.logout();
    assert(invalidations > beforeLogout, "Logout did not clear authority synchronously");
    assert(await loggedOut, "Matching DELETE not validated");
    const revoke = requests.at(-1)!;
    assert(revoke.method === "DELETE" && revoke.body === undefined && revoke.authorization === "Bearer " + token, "Logout body/presented token differs");
    let denied = false; try { await access.client.session(); } catch { denied = true; }
    assert(denied, "Logged-out client retains bearer");
    holdExchange = true;
    const stale = access.exchange(code).then(() => false, () => true);
    await Promise.resolve(); access.clear(); pending.shift()!();
    assert(await stale, "Late exchange restored cleared authority");
    holdExchange = false; mismatch = true;
    denied = false; try { await access.exchange(code); } catch { denied = true; }
    assert(denied, "Exchange accepted mismatched independently read actor");
    mismatch = false; await access.exchange(code); status = 403;
    const beforeDenial = invalidations; denied = false; try { await access.client.session(); } catch { denied = true; }
    assert(denied && invalidations > beforeDenial, "403 did not clear authority");
    status = 200; const count = requests.length;
    denied = false; try { await access.client.session(); } catch { denied = true; }
    assert(denied && requests.length === count, "Revoked client reused token");
    return 10;
  } finally { remove(); access.clear(); access.client.close(); }
}

export async function runW03HttpProof(session: SessionRead, semanticBytes: string, terminalTemplate: Terminal): Promise<number> {
  const semantic = JSON.parse(semanticBytes) as Record<string, Value>;
  const context: Context = { installationId: crypto.randomUUID(), boardId: semantic.boardId as string, incarnation: semantic.incarnation as string, actorId: semantic.actorId as string };
  let status = 503, presented: Terminal | undefined;
  const fetcher: typeof fetch = async (url, init = {}) => {
    if (String(url) === "/api/v1/session") return new Response(JSON.stringify(session));
    if (String(url).includes("/operations/")) return new Response(JSON.stringify(presented), { status });
    const request = JSON.parse(String(init.body)) as MutationRequest;
    const evidence = await prepare("comment.add", request, context, crypto.randomUUID());
    presented = { ...terminalTemplate, opId: request.opId, fingerprint: evidence.fingerprint } as Terminal;
    return new Response(JSON.stringify(presented), { status });
  };
  const client = await (await import("./client")).BoardClient.open({ installationId: context.installationId, profileId: "synthetic-http-proof", credential: () => "synthetic-ephemeral-only", fetch: fetcher,
    databaseName: "synthetic-w03-http-" + crypto.randomUUID(), qualification: { installationId: context.installationId, profileId: "synthetic-http-proof", strictDurabilityQualified: true, evidenceReference: "synthetic component assertion only" } });
  try {
    for (const httpStatus of [503, 403, 200]) {
      status = httpStatus;
      const request = { opId: crypto.randomUUID(), incarnation: semantic.incarnation, expectedRevision: semantic.expectedRevision, expectedCardRevision: semantic.expectedCardRevision, target: semantic.target, input: semantic.payload } as MutationRequest;
      const result = await client.submit({ command: "comment.add", request, context, draftId: crypto.randomUUID() }, () => {});
      assert(result.state === (status === 200 ? "committed" : "unknown"), "HTTP error classified terminal-looking body");
      const inventory = await client.recovery.inventory();
      assert(inventory.submissions.find(s => s.opId === request.opId)?.outcome === (status === 200 ? "committed" : "unknown"), "HTTP error persisted terminal classification");
      if (status !== 200) assert((await client.lookup(context, request.opId)).state === "unknown", "HTTP lookup error classified terminal-looking body");
    }
    return 8;
  } finally { client.close(); }
}
