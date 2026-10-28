// Deterministic acquisition-clock regression; synthetic evidence only.
import { connectLocalApplication, initialConnection } from "./app.js";
import { catalogFixture } from "./api/catalog.test.js";
import type { ConnectionClock } from "./app.js";

export async function runInitialConnectionProbe(): Promise<number> {
  let deadline: (() => void) | undefined, cancelled = false, milliseconds = 0;
  const clock: ConnectionClock = { schedule(run, delay) { deadline = run; milliseconds = delay; return run; }, cancel() { cancelled = true; } };
  const pending: Array<{ release(): void; signal: AbortSignal | null | undefined }> = [];
  const fetcher: typeof fetch = (_input, init) => new Promise<Response>(resolve => pending.push({ release: () => resolve(new Response("{}")), signal: init?.signal }));
  let cleanup = 0, mounted = 0, liveAtLateCompletion = true;
  const result = initialConnection(async (guarded, current) => {
    // Distinct discriminator, workspace metadata and selected hydration phases.
    await guarded("/runtime-mode"); await guarded("/workspace/bootstrap"); await guarded("/selected/read");
    liveAtLateCompletion = current(); return "late application";
  }, () => { cleanup++; }, fetcher, clock).then(() => { mounted++; return "mounted"; }, () => "timed out");
  const settle = async (): Promise<void> => { for (let i = 0; i < 8; i++) await Promise.resolve(); };
  if (milliseconds !== 20_000 || pending.length !== 1) throw Error("Initial connection did not start one total20s deadline");
  pending[0]!.release(); await settle(); pending[1]!.release(); await settle();
  if (Number(pending.length) !== 3) throw Error("Composite phases did not share acquisition");
  deadline!();
  if (await result !== "timed out" || cleanup !== 1 || !pending.every(p => p.signal?.aborted)) throw Error("Composite deadline did not abort/clean up all initial phases");
  pending[2]!.release(); await settle();
  if (mounted || liveAtLateCompletion || cleanup < 2) throw Error("Abort-ignoring late phase mounted or leaked");
  let connectedSignal: AbortSignal | null | undefined;
  const fast: typeof fetch = async (_input, init) => { connectedSignal = init?.signal; return new Response("{}"); };
  cleanup = 0; cancelled = false;
  const client = await initialConnection(async guarded => { await guarded("/bootstrap"); return guarded; }, () => { cleanup++; }, fast, clock);
  if (!cancelled || cleanup) throw Error("Successful acquisition did not clear its deadline");
  deadline!(); await client("/later-reconnect");
  if (connectedSignal?.aborted || cleanup) throw Error("Expired acquisition timer invalidated a connected client");
  return 6;
}


// Ordinary app wiring: healthy/stale Refresh is a read, recovery Retry renews
// only after current metadata authority was actually lost.
export async function runApplicationRefreshProbe(): Promise<number> {
  const fixture = catalogFixture(), workspaceId = fixture.catalog.workspaceId, epoch = crypto.randomUUID();
  const root = document.createElement("main"); document.body.replaceChildren(root);
  const requests: { path: string; method: string }[] = [];
  let release: (() => void) | undefined, hold = false, held = false, failure = 0;
  const fetcher: typeof fetch = async (input, init = {}) => {
    const path = String(input); requests.push({ path, method: init.method ?? "GET" });
    if (path === "/api/v1/workspace/projects" && hold) { hold = false; held = true; await new Promise<void>(resolve => { release = resolve; }); }
    const value = path === "/api/v1/runtime-mode" ? { apiVersion: 1, mode: "workspace", workspaceId }
      : path === "/api/v1/workspace/bootstrap" ? { apiVersion: 1, workspaceId, epoch, mode: "human" }
      : path === "/api/v1/workspace/projects" ? fixture.projects : path === "/api/v1/workspace/catalog" ? fixture.catalog : undefined;
    if (!value) throw Error("Unexpected board/bootstrap/mutation path " + path);
    return new Response(JSON.stringify(value), { status: path === "/api/v1/workspace/catalog" ? failure || 200 : 200, headers: { "Cache-Control": "no-store" } });
  };
  const settle = async (test: () => boolean): Promise<void> => { for (let i = 0; i < 250; i++) { if (test()) return; await new Promise(resolve => setTimeout(resolve, 5)); } throw Error("Integrated app refresh did not settle"); };
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); };
  const oldState = history.state, oldURL = location.href; history.replaceState(null, "", "/");
  const application = await connectLocalApplication(root, fetcher);
  const refresh = (): void => root.querySelector<HTMLButtonElement>(".workspace-refresh")!.click();
  try {
    await settle(() => !!root.querySelector(".workspace-board-list a"));
    const row = root.querySelector(".workspace-board-list a"), initialBootstraps = requests.filter(r => r.method === "POST").length;
    check(initialBootstraps === 1, "Initial composition duplicated metadata bootstrap");
    const start = requests.length; hold = true; refresh(); await settle(() => held);
    check(root.querySelector(".workspace-board-list a") === row && root.textContent!.includes("Ready for Review: 0"), "Healthy Refresh cleared trusted catalog while read was in flight");
    check(requests.slice(start).every(r => r.method === "GET"), "Healthy Refresh renewed metadata/board authentication");
    release!(); await settle(() => requests.slice(start).some(r => r.path.endsWith("/catalog"))); await new Promise(resolve => setTimeout(resolve, 10));
    check(requests.filter(r => r.method === "POST").length === initialBootstraps && root.querySelector(".workspace-board-list a") === row, "Healthy Refresh replaced catalog nodes or renewed authentication");
    failure = 503; refresh(); await settle(() => root.textContent!.includes("out of date"));
    failure = 0; const staleStart = requests.length; refresh(); await settle(() => requests.slice(staleStart).some(r => r.path.endsWith("/catalog"))); await new Promise(resolve => setTimeout(resolve, 10));
    check(requests.slice(staleStart).every(r => r.method === "GET") && requests.filter(r => r.method === "POST").length === initialBootstraps, "Stale transport Refresh renewed authentication");
    failure = 401; refresh(); await settle(() => !root.querySelector(".workspace-board-list a"));
    check(requests.filter(r => r.method === "POST").length === initialBootstraps, "Failed GET automatically renewed unavailable authority");
    failure = 0; refresh(); await settle(() => !!root.querySelector(".workspace-board-list a"));
    check(requests.filter(r => r.method === "POST").length === initialBootstraps + 1 && requests.every(r => !r.path.startsWith("/api/v1/boards/")), "Explicit recovery Retry did not renew metadata exactly once or bootstrapped a default board");
    return 7;
  } finally { release?.(); application.destroy(); history.replaceState(oldState, "", oldURL); }
}
