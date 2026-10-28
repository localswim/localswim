import type { WorkspacePinReference, WorkspacePreferences, WorkspacePreferencesRequest } from "../../generated/transport/contracts.js";
import { PreferencesRefusal } from "../api/preferences.js";
import type { WorkspacePreferencesClient } from "../api/preferences.js";
import type { ShellPinSnapshot } from "./view.js";

const same = (a: WorkspacePinReference, b: WorkspacePinReference): boolean => a.projectKey === b.projectKey && a.boardKey === b.boardKey;
const sameOrder = (a: readonly WorkspacePinReference[], b: readonly WorkspacePinReference[]): boolean => a.length === b.length && a.every((pin, index) => same(pin, b[index]!));

// Shared preferences contain only pin keys/order. No route, editor, layout or
// catalog facts are owned here. Unknown writes require deliberate inspection.
export class WorkspacePins {
  private generation = 0;
  private disposed = false;
  private connected = true;
  private current: WorkspacePreferences | undefined;
  private reading: Promise<void> | undefined;
  private requested = "";
  private state: ShellPinSnapshot = { state: "loading", pins: [] };
  private readonly remove: (() => void)[];
  constructor(private readonly client: WorkspacePreferencesClient, private readonly onChange: (state: ShellPinSnapshot) => void) {
    this.remove = [client.onUnavailable(() => {
      if (this.disposed) return;
      this.generation++; this.connected = false; this.current = undefined;
      const uncertain = this.state.state === "saving" || this.state.state === "unknown";
      this.publish({ state: uncertain ? "unknown" : "failed", pins: [], message: uncertain ? "Pin save outcome UNKNOWN. " + this.requested + " Reconnect, then inspect saved pins before editing." : "Pins unavailable. Reconnect the workspace to inspect saved pins." });
    }), client.onConnected(() => { if (!this.disposed) { this.generation++; this.connected = true; } })];
    this.publish(this.state);
  }
  get snapshot(): ShellPinSnapshot { return structuredClone(this.state); }
  private publish(state: ShellPinSnapshot): void { this.state = structuredClone(state); try { this.onChange(this.snapshot); } catch {} }
  refresh(deliberate = false): Promise<void> {
    if (this.disposed || !this.connected || this.state.state === "saving" || (!deliberate && (this.state.state === "unknown" || this.state.state === "conflict"))) return Promise.resolve();
    if (this.reading) return this.reading;
    const generation = this.generation;
    const work = async (): Promise<void> => {
      try {
        const value = await this.client.read();
        if (this.disposed || !this.connected || generation !== this.generation) return;
        if (this.current && value.preferencesRevision < this.current.preferencesRevision) throw Error("Preference revision regressed");
        this.current = structuredClone(value); this.requested = ""; this.publish({ state: "ready", pins: value.pins });
      } catch {
        if (this.disposed || !this.connected || generation !== this.generation) return;
        const unknown = this.state.state === "unknown";
        this.publish({ ...this.state, state: unknown ? "unknown" : "failed", message: unknown ? "Pin save outcome UNKNOWN. " + this.requested + " Saved pins could not be inspected; no action was repeated." : "Saved pins could not be refreshed. The last observed order is shown." });
      }
    };
    this.reading = work().finally(() => { this.reading = undefined; }); return this.reading;
  }
  async change(pins: readonly WorkspacePinReference[]): Promise<void> {
    if (this.disposed || !this.connected || this.state.state !== "ready" || !this.current || sameOrder(this.current.pins, pins)) return;
    const before = this.current, desired = structuredClone(pins);
    const added = desired.filter(pin => !before.pins.some(old => same(pin, old))), removed = before.pins.filter(pin => !desired.some(next => same(pin, next)));
    let request: WorkspacePreferencesRequest;
    const base = { apiVersion: 1 as const, expectedPreferencesRevision: before.preferencesRevision };
    if (added.length === 1 && removed.length === 0 && sameOrder(desired.slice(0, -1), before.pins)) request = { ...base, action: "pin", pin: added[0]!, order: [] };
    else if (removed.length === 1 && added.length === 0 && sameOrder(desired, before.pins.filter(pin => !same(pin, removed[0]!)))) request = { ...base, action: "unpin", pin: removed[0]!, order: [] };
    else if (!added.length && !removed.length && desired.length === before.pins.length) request = { ...base, action: "reorder", pin: null, order: desired };
    else return;
    const label = (pin: WorkspacePinReference): string => pin.projectKey + "/" + pin.boardKey;
    this.requested = request.action === "reorder" ? "Requested order: " + request.order.map(label).join(", ") + "." : "Requested: " + request.action + " " + label(request.pin!) + ".";
    const generation = ++this.generation;
    this.publish({ state: "saving", pins: before.pins, message: "Saving pins…" });
    try {
      const next = await this.client.write(request);
      if (this.disposed || !this.connected || generation !== this.generation) return;
      this.current = structuredClone(next); this.requested = ""; this.publish({ state: "ready", pins: next.pins });
    } catch (error) {
      if (this.disposed || !this.connected || generation !== this.generation) return;
      if (error instanceof PreferencesRefusal) {
        if (error.preferences) this.current = structuredClone(error.preferences);
        this.publish({ state: error.kind === "conflict" ? "conflict" : "failed", pins: this.current?.pins ?? before.pins,
          message: error.kind === "conflict" ? "Pins changed in another window. Current saved pins are shown. Review them before trying your change again." : "Pin change was refused. Review saved pins before trying again." });
      } else this.publish({ state: "unknown", pins: before.pins, message: "Pin save outcome UNKNOWN. " + this.requested + " Inspect saved pins before editing; no action will be repeated automatically." });
    }
  }
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true; this.generation++; this.current = undefined;
    for (const remove of this.remove) remove(); this.client.dispose();
  }
}
