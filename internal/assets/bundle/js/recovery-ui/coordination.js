// Live clients in the same origin/database/installation clear domain share this
// FIFO, including separately mounted views and boards. Register work at the
// input/action event, before any await. Other databases/installations stay separate.
// A clear boundary drains earlier writes and holds later deliberate edits until
// the atomic store clear/readback settles. It never submits or replays anything.
export class RecoveryCoordinator {
    tail = Promise.resolve();
    epoch = 0;
    listeners = new Set();
    get generation() { return this.epoch; }
    subscribe(listener) { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; }
    enqueue(run) {
        const result = this.tail.then(run);
        this.tail = result.then(() => { }, () => { });
        return result;
    }
    persist(run) { return this.enqueue(run); }
    // Reads begun after a boundary wait for it, but an older, delayed read cannot
    // block a clear. Consumers also compare generation before showing its result.
    read(run) { return this.tail.then(run); }
    // A mutation's pending announcement means its immutable evidence has completed
    // persistence/readback. Release the FIFO there, without waiting on the network.
    // Late terminal append cannot recreate evidence removed by the W01 clear.
    admit(run) {
        return new Promise((resolve, reject) => {
            const admitted = this.enqueue(async () => {
                let release;
                const ready = new Promise(done => { release = done; });
                try {
                    void run(release).then(value => { release(); resolve(value); }, error => { release(); reject(error); });
                }
                catch (error) {
                    release();
                    reject(error);
                }
                await ready;
            });
            void admitted.catch(reject);
        });
    }
    clear(run, signal) {
        // Install the barrier before notifying views: listener-triggered/new edits
        // must register after it, even when an earlier write is still in flight.
        const result = this.enqueue(async () => {
            if (signal.aborted)
                throw Error("Private discard cancelled before storage admission");
            await run();
        });
        this.epoch++;
        for (const listener of this.listeners) {
            try {
                listener();
            }
            catch { /* All remaining participants still see the boundary. */ }
        }
        return result;
    }
}
// Keep one registry per JS workspace even if an entry module is loaded through
// two equivalent URLs. This is ephemeral coordination, never persisted authority.
const registryKey = Symbol.for("localswim.private-recovery-coordination.v1");
const workspace = globalThis;
const domains = workspace[registryKey] ??= new Map();
export function recoveryCoordinator(client) {
    let coordinator = domains.get(client.recoveryDomainKey);
    if (!coordinator) {
        coordinator = new RecoveryCoordinator();
        domains.set(client.recoveryDomainKey, coordinator);
    }
    return coordinator;
}
