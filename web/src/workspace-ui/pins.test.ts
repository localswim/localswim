// Deterministic preference lifecycle and intent tests, not native qualification.
import type { WorkspacePreferences, WorkspacePreferencesRequest } from "../../generated/transport/contracts.js";
import type { WorkspacePreferencesClient } from "../api/preferences.js";
import { PreferencesRefusal } from "../api/preferences.js";
import { preferencesFixture } from "../api/preferences.test.js";
import { WorkspacePins } from "./pins.js";

function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
async function settle(): Promise<void> { for (let i = 0; i < 12; i++) await Promise.resolve(); }
export async function runPinsTests(): Promise<number> {
  let passed = 0, reads = 0, disposed = false;
  let resolveRead: ((value: WorkspacePreferences) => void) | undefined, rejectRead: (() => void) | undefined;
  let resolveWrite: ((value: WorkspacePreferences) => void) | undefined, rejectWrite: ((error: unknown) => void) | undefined;
  const requests: WorkspacePreferencesRequest[] = [], unavailable = new Set<() => void>(), connected = new Set<() => void>();
  const client: WorkspacePreferencesClient = {
    read() { reads++; return new Promise((resolve, reject) => { resolveRead = resolve; rejectRead = () => reject(Error("Unavailable")); }); },
    write(request) { requests.push(request); return new Promise((resolve, reject) => { resolveWrite = resolve; rejectWrite = reject; }); },
    onUnavailable(callback) { unavailable.add(callback); return () => { unavailable.delete(callback); }; },
    onConnected(callback) { connected.add(callback); return () => { connected.delete(callback); }; },
    dispose() { disposed = true; },
  };
  let paints = 0;
  const pins = new WorkspacePins(client, () => { paints++; });
  const a = { projectKey: "harbor", boardKey: "dispatch" }, b = { projectKey: "orchard", boardKey: "garden" };
  const fixture = preferencesFixture();
  const first = pins.refresh(); assert(first === pins.refresh() && reads === 1, "Preference reads overlapped");
  resolveRead!({ ...fixture, pins: [a] }); await first; assert(String(pins.snapshot.state) === "ready" && Number(pins.snapshot.pins.length) === 1, "Initial pins unavailable"); passed++;
  const secondWindow = pins.refresh(); resolveRead!({ ...fixture, preferencesRevision: 2, pins: [b, a] }); await secondWindow;
  assert(pins.snapshot.pins[0]!.boardKey === b.boardKey, "Other-window order not observed"); passed++;
  const staleRead = pins.refresh(); const change = pins.change([a, b]);
  assert(String(pins.snapshot.state) === "saving" && Number(requests.length) === 1 && requests[0]!.action === "reorder" && requests[0]!.expectedPreferencesRevision === 2, "Reorder lost separate preference revision");
  resolveRead!({ ...fixture, preferencesRevision: 2, pins: [b] }); await staleRead;
  assert(String(pins.snapshot.state) === "saving" && Number(pins.snapshot.pins.length) === 2, "Old poll repainted a pending change");
  await pins.change([a]); await pins.refresh(); assert(Number(requests.length) === 1, "Saving accepted another intent");
  resolveWrite!({ ...fixture, preferencesRevision: 3, pins: [a, b] }); await change; passed++;
  const unknown = pins.change([b]); rejectWrite!(Error("Unreadable success")); await unknown;
  const readsBefore = reads; await pins.refresh(); await pins.change([a]);
  assert(String(pins.snapshot.state) === "unknown" && pins.snapshot.message?.includes("unpin harbor/dispatch") && Number(pins.snapshot.pins.length) === 2 && reads === readsBefore && Number(requests.length) === 2, "Unknown save reset pins, hid requested intent or replayed"); passed++;
  const failedInspect = pins.refresh(true); rejectRead!(); await failedInspect;
  assert(String(pins.snapshot.state) === "unknown", "Failed inspection erased uncertainty");
  const inspect = pins.refresh(true); resolveRead!({ ...fixture, preferencesRevision: 4, pins: [b] }); await inspect;
  assert(String(pins.snapshot.state) === "ready" && Number(pins.snapshot.pins.length) === 1, "Deliberate GET did not resolve current state"); passed++;
  const conflict = pins.change([b, a]); rejectWrite!(new PreferencesRefusal("conflict", { ...fixture, preferencesRevision: 5, pins: [a] })); await conflict;
  const requestsBefore = requests.length; await pins.refresh();
  assert(String(pins.snapshot.state) === "conflict" && pins.snapshot.pins[0]!.boardKey === a.boardKey && Number(requests.length) === requestsBefore, "Conflict hid current state or overwrote automatically"); passed++;
  const inspectConflict = pins.refresh(true); resolveRead!({ ...fixture, preferencesRevision: 5, pins: [a] }); await inspectConflict;
  const retry = pins.change([a, b]); assert(requests.at(-1)!.expectedPreferencesRevision === 5 && requests.at(-1)!.action === "pin", "Deliberate retry used stale revision");
  resolveWrite!({ ...fixture, preferencesRevision: 6, pins: [a, b] }); await retry; passed++;
  const failed = pins.refresh(); rejectRead!(); await failed;
  assert(String(pins.snapshot.state) === "failed" && Number(pins.snapshot.pins.length) === 2, "Read failure reset persisted pins"); passed++;
  const late = pins.refresh(); for (const callback of unavailable) callback(); resolveRead!({ ...fixture, preferencesRevision: 7, pins: [a, b] }); await late;
  assert(String(pins.snapshot.state) === "failed" && Number(pins.snapshot.pins.length) === 0, "Late read restored unsafe authority cache"); passed++;
  for (const callback of connected) callback();
  const renewed = pins.refresh(); resolveRead!({ ...fixture, preferencesRevision: 7, pins: [a] }); await renewed;
  const lateWrite = pins.change([]); for (const callback of unavailable) callback(); for (const callback of connected) callback();
  resolveWrite!({ ...fixture, preferencesRevision: 8, pins: [] }); await lateWrite;
  assert(String(pins.snapshot.state) === "unknown" && Number(pins.snapshot.pins.length) === 0, "Late write resolved uncertainty after reconnect"); passed++;
  const finalRead = pins.refresh(true), beforeDispose = paints; pins.dispose(); resolveRead!({ ...fixture, preferencesRevision: 8, pins: [] }); await finalRead; await settle();
  assert(disposed && paints === beforeDispose, "Disposed pins published late state"); passed++;
  return passed;
}
