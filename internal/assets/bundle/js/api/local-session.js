import { BoardClient, isLocalOrigin } from "./client.js";
import { canonical, object, parse, validate } from "./boundary.js";
import { localRecoveryReadiness } from "../recovery/local-readiness.js";
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const v4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export async function readLocalBootstrap(fetcher = globalThis.fetch.bind(globalThis)) {
    if (!isLocalOrigin())
        throw Error("Local service origin required");
    const response = await fetcher("/api/v1/local/bootstrap", { method: "POST", body: "{}", credentials: "same-origin",
        cache: "no-store", redirect: "error", headers: { "Content-Type": "application/json", "X-Localswim-Client": "browser" } });
    if (response.status !== 200 || response.redirected || response.headers.get("Cache-Control") !== "no-store")
        throw Error("Local connection unavailable");
    const body = object(parse(new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(await response.arrayBuffer())));
    if (Object.keys(body).sort().join() !== "apiVersion,boardId,epoch,incarnation,installationId,session" || body.apiVersion !== 1)
        throw Error("Invalid local connection");
    for (const name of ["installationId", "boardId", "incarnation", "epoch"]) {
        const value = body[name];
        if (typeof value !== "string" || !(name === "installationId" || name === "epoch" ? v4 : uuid).test(value) || /^0{8}-0{4}-0{4}-0{4}-0{12}$/.test(value))
            throw Error("Invalid local identity");
    }
    validate("transport:SessionRead", body.session);
    const result = body;
    if (result.session.kind !== "human" || result.session.grant.boardId !== result.boardId || result.session.grant.incarnation !== result.incarnation)
        throw Error("Local session scope mismatch");
    return structuredClone(result);
}
export async function openLocalSession(fetcher = globalThis.fetch.bind(globalThis)) {
    const initial = await readLocalBootstrap(fetcher);
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
    const client = await BoardClient.open({ installationId: initial.installationId, profileId: readiness?.profileId ?? "storage-unavailable",
        ...(readiness ? { qualification: readiness.qualification } : {}), transport: "local-cookie", credential: () => marker, fetch: fetcher,
        authenticationUnavailable: presented => { if (marker === presented)
            clear(); } });
    try {
        const current = await client.session();
        if (canonical(current) !== canonical(initial.session))
            throw Error("Local session changed");
    }
    catch (error) {
        clear();
        client.close();
        throw error;
    }
    return Object.freeze({ client, initial: initial.session, recoveryReady: !!readiness && client.recoveryStorageAvailable, clear,
        onUnavailable(callback) { callbacks.add(callback); return () => { callbacks.delete(callback); }; },
        async reconnect() {
            clear();
            const started = generation;
            try {
                const next = await readLocalBootstrap(fetcher);
                if (generation !== started || next.installationId !== initial.installationId || next.boardId !== initial.boardId || next.incarnation !== initial.incarnation ||
                    next.session.actorId !== initial.session.actorId || next.session.grant.grantId !== initial.session.grant.grantId)
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
