import { parse, validate } from "./boundary.js";
import { isLocalOrigin } from "./client.js";
export class PreferencesRefusal extends Error {
    kind;
    preferences;
    constructor(kind, preferences) {
        super("Workspace preferences refused");
        this.kind = kind;
        this.preferences = preferences;
    }
}
const path = "/api/v1/workspace/preferences", maxBodyBytes = 64 * 1024;
// Serial reads and single-send writes share the workspace cookie channel. This
// client never renews authority or replays a POST after a failure.
export function createWorkspacePreferencesClient(access, fetcher = globalThis.fetch.bind(globalThis)) {
    const workspaceId = access.initial.workspaceId, origin = location.origin;
    let generation = 0, connected = true, disposed = false;
    let tail = Promise.resolve(), reading;
    const unavailable = new Set(), available = new Set();
    const notify = (callbacks) => { for (const callback of callbacks) {
        try {
            callback();
        }
        catch { }
    } };
    const removeUnavailable = access.onUnavailable(() => { generation++; connected = false; notify(unavailable); });
    const removeConnected = access.onConnected(() => { if (!disposed) {
        generation++;
        connected = true;
        notify(available);
    } });
    const current = (expected) => {
        if (!disposed && generation === expected && (!isLocalOrigin() || location.origin !== origin))
            access.clear();
        if (disposed || !connected || expected !== generation || !isLocalOrigin() || location.origin !== origin)
            throw Error("Workspace preferences unavailable");
    };
    const identity = (value) => {
        if (value.workspaceId !== workspaceId) {
            access.clear();
            throw Error("Workspace identity changed");
        }
        return value;
    };
    const request = async (expected, mutation) => {
        current(expected);
        if (mutation)
            validate("transport:WorkspacePreferencesRequest", parse(JSON.stringify(mutation)));
        const signal = AbortSignal.timeout(20_000);
        const response = await fetcher(path, { method: mutation ? "POST" : "GET", credentials: "same-origin", cache: "no-store", redirect: "error", signal,
            headers: { "X-Localswim-Client": "browser", ...(mutation ? { "Content-Type": "application/json" } : {}) },
            ...(mutation ? { body: JSON.stringify(mutation) } : {}) });
        if (generation === expected && (response.status === 401 || response.status === 403))
            access.clear();
        try {
            current(expected);
        }
        catch (error) {
            await response.body?.cancel().catch(() => { });
            throw error;
        }
        if (response.redirected || response.headers.get("Cache-Control") !== "no-store") {
            await response.body?.cancel().catch(() => { });
            throw Error("Workspace preferences unavailable");
        }
        const value = parse(await boundedBody(response, signal));
        current(expected);
        if (response.status === 200) {
            validate("transport:WorkspacePreferences", value);
            const result = identity(structuredClone(value));
            if (mutation) {
                const same = (a, b) => a.projectKey === b.projectKey && a.boardKey === b.boardKey;
                const contains = mutation.pin && result.pins.some(pin => same(pin, mutation.pin));
                if (result.preferencesRevision !== mutation.expectedPreferencesRevision + 1 ||
                    (mutation.action === "pin" && !contains) || (mutation.action === "unpin" && contains) ||
                    (mutation.action === "reorder" && (result.pins.length !== mutation.order.length || !result.pins.every((pin, index) => same(pin, mutation.order[index])))))
                    throw Error("Uncertain preference response");
            }
            current(expected);
            return result;
        }
        validate("transport:WorkspacePreferencesError", value);
        const failure = structuredClone(value);
        if (failure.preferences)
            identity(failure.preferences);
        current(expected);
        if (mutation && response.status === 412 && failure.error.code === "PREFERENCES_CHANGED" && failure.preferences)
            throw new PreferencesRefusal("conflict", failure.preferences);
        if (mutation && response.status >= 400 && response.status < 500)
            throw new PreferencesRefusal("refused", failure.preferences);
        throw Error("Workspace preferences unavailable");
    };
    const enqueue = (mutation) => {
        const expected = generation;
        const result = tail.then(() => request(expected, mutation));
        tail = result.then(() => { }, () => { });
        return result;
    };
    return Object.freeze({
        read() {
            if (!reading)
                reading = enqueue().finally(() => { reading = undefined; });
            return reading;
        },
        write(mutation) { return enqueue(structuredClone(mutation)); },
        onUnavailable(callback) { unavailable.add(callback); return () => { unavailable.delete(callback); }; },
        onConnected(callback) { available.add(callback); return () => { available.delete(callback); }; },
        dispose() { if (disposed)
            return; disposed = true; generation++; connected = false; removeUnavailable(); removeConnected(); unavailable.clear(); available.clear(); },
    });
}
async function boundedBody(response, signal) {
    const length = response.headers.get("Content-Length");
    if (length !== null && (!/^[0-9]+$/.test(length) || Number(length) > maxBodyBytes)) {
        await response.body?.cancel().catch(() => { });
        throw Error("Workspace preferences unavailable");
    }
    const reader = response.body?.getReader();
    if (!reader)
        throw Error("Workspace preferences unavailable");
    const chunks = [];
    let size = 0, abort;
    const aborted = new Promise((_, reject) => {
        abort = () => { void reader.cancel().catch(() => { }); reject(Error("Workspace preferences unavailable")); };
        signal.addEventListener("abort", abort, { once: true });
        if (signal.aborted)
            abort();
    });
    try {
        for (;;) {
            const part = await Promise.race([reader.read(), aborted]);
            if (part.done)
                break;
            size += part.value.byteLength;
            if (size > maxBodyBytes) {
                await reader.cancel().catch(() => { });
                throw Error("Workspace preferences unavailable");
            }
            chunks.push(part.value);
        }
        const bytes = new Uint8Array(size);
        let offset = 0;
        for (const chunk of chunks) {
            bytes.set(chunk, offset);
            offset += chunk.byteLength;
        }
        return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes);
    }
    finally {
        if (abort)
            signal.removeEventListener("abort", abort);
        reader.releaseLock();
    }
}
