import { parse, validate } from "../api/boundary.js";
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const encodedSize = (value) => new TextEncoder().encode(JSON.stringify(value)).length;
function validDraft(draft) {
    const exact = (value, keys) => !!value && typeof value === "object" && !Array.isArray(value) && Object.keys(value).length === keys.length && Object.keys(value).every(key => keys.includes(key));
    const text = (value, max) => typeof value === "string" && value.length <= max;
    if (!exact(draft, ["projectKey", "boardKey", "label", "workflow"]) || !text(draft.projectKey, 64) || !text(draft.boardKey, 64) || !text(draft.label, 400))
        return false;
    const workflow = draft.workflow;
    return exact(workflow, ["workflowId", "revision", "lanes", "transitions", "actorRights"]) && text(workflow.workflowId, 36) && workflow.revision === 1 &&
        Array.isArray(workflow.lanes) && workflow.lanes.length >= 4 && workflow.lanes.length <= 16 && workflow.lanes.every(lane => exact(lane, ["id", "label", "role"]) && text(lane.id, 36) && text(lane.label, 400) && text(lane.role, 20)) &&
        Array.isArray(workflow.transitions) && workflow.transitions.length <= 64 && workflow.transitions.every(edge => exact(edge, ["from", "to", "actors"]) && text(edge.from, 36) && text(edge.to, 36) && Array.isArray(edge.actors) && edge.actors.length <= 2 && edge.actors.every(actor => actor === "human" || actor === "automation")) &&
        exact(workflow.actorRights, ["human", "automation"]) && [workflow.actorRights.human, workflow.actorRights.automation].every(rights => Array.isArray(rights) && rights.length <= 9 && rights.every(right => text(right, 30)));
}
function validRecord(record, key) {
    if (!record || record.key !== key || !Number.isSafeInteger(record.revision) || record.revision < 1 || encodedSize(record) > 131072)
        throw Error("Setup recovery invalid");
    const value = record.value;
    if (!value || !validDraft(value.draft))
        throw Error("Setup recovery invalid");
    if (Object.keys(value).some(field => !["draft", "submitted", "original", "knownIdentity", "result", "observation"].includes(field)))
        throw Error("Setup recovery invalid");
    if (value.submitted) {
        validate("transport:ConfiguredWorkspaceBoardRequest", parse(JSON.stringify(value.submitted)));
        if (!key.startsWith(value.submitted.workspaceId + ":") || !["pending", "unknown", "registered", "refused"].includes(value.original ?? "") ||
            JSON.stringify(value.draft) !== JSON.stringify({ projectKey: value.submitted.projectKey, boardKey: value.submitted.boardKey, label: value.submitted.label, workflow: value.submitted.workflow }))
            throw Error("Setup recovery mismatch");
    }
    else if (value.original || value.result || value.observation || value.knownIdentity)
        throw Error("Setup recovery invalid");
    if (value.knownIdentity)
        validate("transport:WorkspaceIdentity", parse(JSON.stringify(value.knownIdentity)));
    for (const result of [value.result, value.observation])
        if (result) {
            validate("transport:WorkspaceControlResult", parse(JSON.stringify(result)));
            if (result.workspaceId !== value.submitted?.workspaceId || result.projectKey !== value.draft.projectKey || result.boardKey !== value.draft.boardKey)
                throw Error("Setup recovery mismatch");
            if (value.knownIdentity && result.identity && ["installationId", "boardId", "incarnation"].some(key => value.knownIdentity[key] !== result.identity[key]))
                throw Error("Setup recovery identity mismatch");
        }
}
// This database is setup-only: it has no card identity, credentials, operation
// ledger, or effect on existing card recovery. Pending is UNKNOWN after reload.
export async function openSetupStore(factory, workspaceId, windowId, name = "localswim-private-workflow-setup-v1") {
    if (!factory || !uuid.test(workspaceId) || !uuid.test(windowId))
        throw Error("Private setup storage unavailable");
    const key = workspaceId + ":" + windowId;
    const db = await new Promise((resolve, reject) => {
        let request;
        try {
            request = factory.open(name, 2);
        }
        catch {
            reject(Error("Private setup storage unavailable"));
            return;
        }
        let blocked = false;
        request.onupgradeneeded = () => {
            if (!request.result.objectStoreNames.contains("setups"))
                request.result.createObjectStore("setups", { keyPath: "key" });
            if (!request.result.objectStoreNames.contains("metadata"))
                request.result.createObjectStore("metadata");
        };
        request.onerror = () => reject(Error("Private setup storage unavailable"));
        request.onblocked = () => { blocked = true; reject(Error("Private setup storage unavailable")); };
        request.onsuccess = () => { if (blocked)
            request.result.close();
        else
            resolve(request.result); };
    });
    db.onversionchange = () => db.close();
    const transact = (mode, change) => new Promise((resolve, reject) => {
        let tx;
        try {
            tx = db.transaction(["setups", "metadata"], mode, { durability: "strict" });
            if (mode === "readwrite" && tx.durability !== "strict") {
                tx.abort();
                reject(Error("Strict setup storage unavailable"));
                return;
            }
        }
        catch {
            reject(Error("Private setup storage unavailable"));
            return;
        }
        let output, failure;
        tx.oncomplete = () => resolve(output);
        tx.onabort = () => reject(failure ?? Error("Private setup storage unavailable"));
        tx.onerror = () => { };
        const store = tx.objectStore("setups"), metadata = tx.objectStore("metadata"), request = store.getAll(), clock = metadata.get("sequence");
        let remaining = 2;
        const completed = () => {
            if (--remaining !== 0)
                return;
            try {
                const sequence = clock.result ?? 0;
                if (!Number.isSafeInteger(sequence) || sequence < 0)
                    throw Error("Setup revision sequence invalid");
                output = change(request.result, store, sequence, metadata);
            }
            catch (error) {
                failure = error;
                tx.abort();
            }
        };
        request.onsuccess = clock.onsuccess = completed;
    });
    const read = () => transact("readonly", rows => { const record = rows.find(row => row.key === key); if (record)
        validRecord(record, key); return record ? structuredClone(record) : undefined; });
    return Object.freeze({ read, close: () => db.close(),
        async write(revision, value) {
            const snapshot = structuredClone(value);
            const record = await transact("readwrite", (rows, store, sequence, metadata) => {
                const before = rows.find(row => row.key === key);
                if ((before?.revision ?? 0) !== revision)
                    throw Error("Setup changed in another context");
                // A singleton sequence survives deletion; recreating a window's draft
                // cannot reuse an old CAS token and admit a stale writer (ABA).
                const nextRevision = Math.max(sequence, ...rows.map(row => row.revision)) + 1;
                const record = { key, revision: nextRevision, value: snapshot };
                validRecord(record, key);
                if (before) {
                    validRecord(before, key);
                    if (before.value.submitted && JSON.stringify(before.value.submitted) !== JSON.stringify(record.value.submitted))
                        throw Error("Submitted setup is immutable");
                }
                const next = [...rows.filter(row => row.key !== key), record];
                if (next.length > 20 || encodedSize(next) > 2097152)
                    throw Error("Private setup storage full");
                store.put(record);
                metadata.put(nextRevision, "sequence");
                return record;
            });
            const after = await read();
            if (JSON.stringify(after) !== JSON.stringify(record))
                throw Error("Setup readback unavailable");
            return record;
        },
        async clear(revision) {
            await transact("readwrite", (rows, store, sequence, metadata) => {
                if ((rows.find(row => row.key === key)?.revision ?? 0) !== revision)
                    throw Error("Setup changed in another context");
                // Preserve v1 high-water values even if the first v2 operation deletes
                // a migrated record before the allocator has ever written metadata.
                const highWater = Math.max(sequence, ...rows.map(row => row.revision));
                if (!Number.isSafeInteger(highWater) || highWater < 0)
                    throw Error("Setup revision sequence invalid");
                metadata.put(highWater, "sequence");
                store.delete(key);
            });
            if (await read())
                throw Error("Setup removal unverified");
        },
    });
}
