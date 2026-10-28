// Deterministic acquisition-clock regression; synthetic evidence only.
import { initialConnection } from "./app.js";
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
