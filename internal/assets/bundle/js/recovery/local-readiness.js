const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
// This automatically measures the current storage profile/origin. It proves a
// strict transaction completion and separate readback, not physical power loss.
export async function localRecoveryReadiness(installationId, factory = globalThis.indexedDB) {
    if (!uuid.test(installationId) || !factory)
        throw Error("Private recovery storage unavailable");
    const database = await new Promise((resolve, reject) => {
        const request = factory.open("localswim-local-profile-v1", 1);
        request.onupgradeneeded = () => { request.result.createObjectStore("profiles", { keyPath: "key" }); };
        let refused = false;
        request.onblocked = () => { refused = true; reject(Error("Private recovery storage blocked")); };
        request.onerror = () => reject(Error("Private recovery storage unavailable"));
        request.onsuccess = () => { if (refused)
            request.result.close();
        else
            resolve(request.result); };
    });
    database.onversionchange = () => database.close();
    const key = JSON.stringify([location.origin, installationId]), probe = crypto.randomUUID();
    try {
        const profileId = await new Promise((resolve, reject) => {
            let tx;
            try {
                tx = database.transaction("profiles", "readwrite", { durability: "strict" });
            }
            catch {
                reject(Error("Strict private storage unavailable"));
                return;
            }
            tx.onabort = () => reject(Error("Private storage transaction failed"));
            tx.onerror = () => { };
            if (tx.durability !== "strict") {
                tx.abort();
                return;
            }
            let id;
            tx.oncomplete = () => resolve(id);
            const store = tx.objectStore("profiles"), read = store.get(key);
            read.onsuccess = () => {
                const old = read.result;
                if (old && (old.key !== key || !uuid.test(old.profileId))) {
                    tx.abort();
                    return;
                }
                id = old?.profileId ?? crypto.randomUUID();
                store.put({ key, profileId: id, probe });
            };
        });
        await new Promise((resolve, reject) => {
            const tx = database.transaction("profiles", "readonly"), read = tx.objectStore("profiles").get(key);
            let matched = false;
            read.onsuccess = () => { matched = read.result?.profileId === profileId && read.result?.probe === probe; };
            tx.onabort = () => reject(Error("Private storage readback failed"));
            tx.onerror = () => { };
            tx.oncomplete = () => matched ? resolve() : reject(Error("Private storage readback mismatch"));
        });
        return Object.freeze({ profileId, qualification: Object.freeze({ installationId, profileId,
                strictDurabilityQualified: true, evidenceReference: "local-idb-strict-complete-readback:" + probe }) });
    }
    finally {
        database.close();
    }
}
