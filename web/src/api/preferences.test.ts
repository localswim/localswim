// Synthetic transport cases; ordinary native browser evidence is separate.
import type { WorkspacePreferences, WorkspacePreferencesRequest } from "../../generated/transport/contracts.js";
import { fakeWorkspaceAccess } from "./catalog.test.js";
import { createWorkspacePreferencesClient, PreferencesRefusal } from "./preferences.js";
import { parse, validate } from "./boundary.js";

function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
async function refuses(work: Promise<unknown>): Promise<void> { let failed = false; try { await work; } catch { failed = true; } assert(failed, "Unsafe preferences accepted"); }
const reply = (value: unknown, status = 200, headers: HeadersInit = { "Cache-Control": "no-store" }): Response => new Response(typeof value === "string" ? value : JSON.stringify(value), { status, headers });
export function preferencesFixture(revision = 1): WorkspacePreferences { return { apiVersion: 1, workspaceId: "10000000-0000-4000-8000-000000000001", preferencesRevision: revision, pins: [] }; }
export async function runPreferencesTransportTests(): Promise<number> {
  let passed = 0, active = 0, maximum = 0;
  const fixture = preferencesFixture(), access = fakeWorkspaceAccess(fixture.workspaceId), requests: RequestInit[] = [];
  let special: (() => Promise<Response>) | undefined;
  const fetcher: typeof fetch = async (input, init = {}) => {
    assert(String(input) === "/api/v1/workspace/preferences", "Wrong preference endpoint"); requests.push(init);
    active++; maximum = Math.max(maximum, active);
    try { return special ? await special() : reply(fixture); } finally { active--; }
  };
  const client = createWorkspacePreferencesClient(access, fetcher);
  const mutation: WorkspacePreferencesRequest = { apiVersion: 1, expectedPreferencesRevision: 1, action: "pin", pin: { projectKey: "harbor", boardKey: "dispatch" }, order: [] };
  try {
    for (const request of [mutation, { ...mutation, action: "unpin" }, { ...mutation, action: "reorder", pin: null, order: [mutation.pin] }]) validate("transport:WorkspacePreferencesRequest", parse(JSON.stringify(request)));
    for (const request of [{ ...mutation, pin: null }, { ...mutation, order: [mutation.pin] }, { ...mutation, action: "unpin", pin: null }, { ...mutation, action: "reorder" }, { ...mutation, action: "reorder", pin: null, order: [mutation.pin, mutation.pin] }]) {
      let invalid = false; try { validate("transport:WorkspacePreferencesRequest", parse(JSON.stringify(request))); } catch { invalid = true; }
      assert(invalid, "Invalid action/ref combination accepted"); passed++;
    }
    assert((await client.read()).preferencesRevision === 1, "Initial pins lost");
    assert(requests.every(init => init.method === "GET" && init.credentials === "same-origin" && init.cache === "no-store" && init.redirect === "error" && new Headers(init.headers).get("X-Localswim-Client") === "browser" && !init.body && !new Headers(init.headers).has("Authorization")), "Preference channel changed"); passed++;
    for (const value of [{ ...fixture, privateRoot: "hidden" }, { ...fixture, pins: [{ projectKey: "harbor", boardKey: "dispatch", identity: {} }] }, '{"apiVersion":1,"apiVersion":1}', { ...fixture, preferencesRevision: 0 }, { ...fixture, pins: [{ projectKey: "harbor", boardKey: "dispatch" }, { projectKey: "harbor", boardKey: "dispatch" }] }]) {
      special = async () => reply(value); await refuses(client.read()); passed++;
    }
    special = async () => reply(fixture, 200, {}); await refuses(client.read()); passed++;
    special = async () => reply(fixture, 200, { "Cache-Control": "no-store", "Content-Length": "65537" }); await refuses(client.read()); passed++;
    special = async () => new Response(new Uint8Array(65537), { headers: { "Cache-Control": "no-store" } }); await refuses(client.read()); passed++;
    special = async () => new Response(new Uint8Array([255]), { headers: { "Cache-Control": "no-store" } }); await refuses(client.read()); passed++;
    special = async () => reply({ ...fixture, preferencesRevision: 2, pins: [mutation.pin] });
    assert((await client.write(mutation)).pins.length === 1 && requests.at(-1)!.method === "POST", "Pin not sent once"); passed++;
    special = async () => reply(fixture); await refuses(client.write(mutation)); passed++;
    const before = requests.length; special = async () => { throw Error("lost response"); }; await refuses(client.write(mutation));
    assert(requests.length === before + 1, "Uncertain write replayed"); passed++;
    special = async () => reply({ apiVersion: 1, outcome: "refused", error: { code: "PREFERENCES_CHANGED", field: null, message: "Preferences changed" }, preferences: preferencesFixture(3) }, 412);
    let conflict: unknown; try { await client.write(mutation); } catch (error) { conflict = error; }
    assert(conflict instanceof PreferencesRefusal && conflict.kind === "conflict" && conflict.preferences?.preferencesRevision === 3, "Stale response lost current pins"); passed++;
    let release: (() => void) | undefined, started: (() => void) | undefined;
    const held = new Promise<void>(resolve => { started = resolve; });
    special = async () => { started!(); return new Response(new ReadableStream<Uint8Array>({ start(controller) { release = () => { controller.enqueue(new TextEncoder().encode(JSON.stringify(fixture))); controller.close(); }; } }), { headers: { "Cache-Control": "no-store" } }); };
    const old = client.read(); assert(old === client.read(), "Read not singleflight");
    const oldFailed = old.then(() => false, () => true); await held; await access.reconnect(); release!();
    assert(await oldFailed, "Late preference body crossed generation"); passed++;
    const heldDenial = new Promise<void>(resolve => { started = resolve; });
    special = async () => { started!(); await new Promise<void>(resolve => { release = resolve; }); return reply({}, 401); };
    const denial = client.read().then(() => false, () => true); await heldDenial; await access.reconnect(); const clears = access.clearCount(); release!();
    assert(await denial && access.clearCount() === clears, "Old denial cleared renewed authority"); passed++;
    special = async () => reply({}, 403); const beforeClear = access.clearCount(); await refuses(client.read()); assert(access.clearCount() === beforeClear + 1, "Denial retained authority"); passed++;
    await access.reconnect(); special = undefined; assert((await client.read()).workspaceId === fixture.workspaceId && maximum === 1, "Explicit reconnect or serial requests failed"); passed++;
  } finally { client.dispose(); }
  const beforeDispose = requests.length; await refuses(client.read()); assert(requests.length === beforeDispose, "Disposed client fetched"); passed++;
  return passed;
}
