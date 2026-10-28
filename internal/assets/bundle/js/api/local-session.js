import { BoardClient, isLocalOrigin } from "./client.js";
import { canonical, parse, validate } from "./boundary.js";
import { readLocalBoundary, readRuntimeMode } from "./runtime.js";
import { localRecoveryReadiness } from "../recovery/local-readiness.js";
export async function readLocalBootstrap(fetcher = globalThis.fetch.bind(globalThis)) {
    if ((await readRuntimeMode(fetcher)).mode !== "standalone")
        throw Error("Standalone service required");
    const origin = location.origin;
    const response = await fetcher("/api/v1/local/bootstrap", { method: "POST", body: "{}", credentials: "same-origin",
        signal: AbortSignal.timeout(20_000), cache: "no-store", redirect: "error", headers: { "Content-Type": "application/json", "X-Localswim-Client": "browser" } });
    const result = await readLocalBoundary(response, "transport:LocalBootstrap", origin);
    assertLocalBootstrap(result);
    return result;
}
export function assertLocalBootstrap(result) {
    validate("transport:LocalBootstrap", parse(JSON.stringify(result)));
    if (result.session.kind !== "human" || result.session.grant.boardId !== result.boardId || result.session.grant.incarnation !== result.incarnation)
        throw Error("Local session scope mismatch");
}
export async function openLocalSession(fetcher = globalThis.fetch.bind(globalThis)) {
    return openBoundLocalSession("standalone", () => readLocalBootstrap(fetcher), fetcher);
}
// Both local modes retain the original recovery context and stable client.
// Reconnect installs a new opaque generation after validating original identity;
// requests keep their captured immutable generation across every await.
export async function openBoundLocalSession(mode, bootstrap, fetcher = globalThis.fetch.bind(globalThis)) {
    if (!isLocalOrigin())
        throw Error("Local service origin required");
    const origin = location.origin, initial = structuredClone(await bootstrap());
    assertLocalBootstrap(initial);
    let marker = initial.epoch + ":" + crypto.randomUUID(), generation = 0;
    const callbacks = new Set();
    const clear = () => { marker = undefined; generation++; for (const callback of callbacks) {
        try {
            callback();
        }
        catch { }
    } };
    let readiness;
    try {
        readiness = await localRecoveryReadiness(initial.installationId);
    }
    catch { /* Reads stay available; mutation gate refuses. */ }
    if (location.origin !== origin) {
        clear();
        throw Error("Local origin changed");
    }
    const client = await BoardClient.open({ installationId: initial.installationId, profileId: readiness?.profileId ?? "storage-unavailable",
        ...(readiness ? { qualification: readiness.qualification } : {}), transport: "local-cookie", credential: () => marker, fetch: fetcher,
        selection: { mode, boardId: initial.boardId, incarnation: initial.incarnation, actorId: initial.session.actorId, grantId: initial.session.grant.grantId },
        authenticationUnavailable: presented => { if (marker === presented)
            clear(); } });
    try {
        const current = await client.session();
        if (location.origin !== origin || canonical(current) !== canonical(initial.session))
            throw Error("Local session changed");
    }
    catch (error) {
        clear();
        client.close();
        throw error;
    }
    return Object.freeze({ client, initial: freezeSession(initial.session), recoveryReady: !!readiness && client.recoveryStorageAvailable, clear,
        onUnavailable(callback) { callbacks.add(callback); return () => { callbacks.delete(callback); }; },
        async reconnect() {
            clear();
            const started = generation;
            try {
                const next = await bootstrap();
                assertLocalBootstrap(next);
                if (location.origin !== origin || generation !== started || next.installationId !== initial.installationId || next.boardId !== initial.boardId || next.incarnation !== initial.incarnation ||
                    canonical(next.session) !== canonical(initial.session))
                    throw Error("Local identity changed; reload deliberately");
                marker = next.epoch + ":" + crypto.randomUUID();
                const session = await client.session();
                if (generation !== started || canonical(session) !== canonical(next.session))
                    throw Error("Local session changed");
                return session;
            }
            catch (error) {
                if (generation === started)
                    clear();
                throw error;
            }
        },
    });
}
function freezeSession(session) {
    return Object.freeze({ ...session, grant: Object.freeze({ ...session.grant, rights: Object.freeze([...session.grant.rights]) }) });
}
