import { RecoveryError } from "./types.js";
const stores = ["drafts", "submissions", "statuses"];
// One database per origin/profile, so installation limits span every board.
// A caller may use a different name only for isolated tests or migrations.
export async function openRecoveryDatabase(factory, name = "localswim-private-recovery-v1") {
    if (!factory)
        throw new RecoveryError("unavailable");
    return new Promise((resolve, reject) => {
        let request;
        try {
            request = factory.open(name, 1);
        }
        catch {
            reject(new RecoveryError("unavailable"));
            return;
        }
        request.onupgradeneeded = () => {
            for (const store of stores)
                request.result.createObjectStore(store, { keyPath: "key" });
        };
        request.onerror = () => reject(new RecoveryError("unavailable"));
        // A blocked open never waits indefinitely and never removes another tab's DB.
        let blocked = false;
        request.onblocked = () => { blocked = true; reject(new RecoveryError("unavailable")); };
        request.onsuccess = () => {
            if (blocked) {
                request.result.close();
                return;
            }
            resolve(new IndexedDBDriver(request.result));
        };
    });
}
export class IndexedDBDriver {
    database;
    constructor(database) {
        this.database = database;
        database.onversionchange = () => database.close();
    }
    close() { this.database.close(); }
    read() { return this.transaction("readonly", snapshot => ({ value: snapshot })); }
    commit(change) {
        return this.transaction("readwrite", change);
    }
    transaction(mode, change) {
        return new Promise((resolve, reject) => {
            let transaction;
            try {
                transaction = this.database.transaction([...stores], mode, { durability: "strict" });
                if (mode === "readwrite" && transaction.durability !== "strict") {
                    transaction.abort();
                    reject(new RecoveryError("unavailable"));
                    return;
                }
            }
            catch {
                reject(new RecoveryError("unavailable"));
                return;
            }
            let value;
            let failure;
            let remaining = stores.length;
            const snapshot = {};
            transaction.onabort = () => reject(failure instanceof RecoveryError ? failure : new RecoveryError("unavailable"));
            transaction.onerror = () => { };
            transaction.oncomplete = () => resolve(value);
            for (const store of stores) {
                const request = transaction.objectStore(store).getAll();
                request.onsuccess = () => {
                    snapshot[store] = request.result;
                    if (--remaining !== 0)
                        return;
                    try {
                        const changes = change(snapshot);
                        value = changes.value;
                        if (mode !== "readwrite")
                            return;
                        for (const key of changes.draftDeletes ?? [])
                            transaction.objectStore("drafts").delete(key);
                        for (const key of changes.submissionDeletes ?? [])
                            transaction.objectStore("submissions").delete(key);
                        for (const key of changes.statusDeletes ?? [])
                            transaction.objectStore("statuses").delete(key);
                        for (const row of changes.draftPuts ?? [])
                            transaction.objectStore("drafts").put(row);
                        // Submitted intent and status have no update operation.
                        for (const row of changes.submissionAdds ?? [])
                            transaction.objectStore("submissions").add(row);
                        for (const row of changes.statusAdds ?? [])
                            transaction.objectStore("statuses").add(row);
                    }
                    catch (error) {
                        failure = error;
                        transaction.abort();
                    }
                };
            }
        });
    }
}
