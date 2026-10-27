// This policy rejects reparses during the complete native walk. Its success
// response has aggregate counts, not the legacy audit's reparseCount field.
export function requireProfileAudit(audit, profileId) {
  if (audit?.ok !== true || audit.nativeACLVerified !== true ||
      audit.protectedRootDACL !== true || audit.profileId !== profileId ||
      audit.policy !== 'pinned-browser-account-profile-private-v1' ||
      audit.namedCapability !== 'lpacChromeNetworkSandbox' ||
      JSON.stringify(audit.ordinaryAllowPrincipals) !== JSON.stringify(['current-user', 'SYSTEM']) ||
      !Number.isSafeInteger(audit.directoryCount) || audit.directoryCount < 1 ||
      !Number.isSafeInteger(audit.fileCount) || audit.fileCount < 0 ||
      !Number.isSafeInteger(audit.namedCapabilityACECount) || audit.namedCapabilityACECount < 0 ||
      !Number.isSafeInteger(audit.restrictiveFileExecuteDenyCount) || audit.restrictiveFileExecuteDenyCount < 0) {
    throw Error('Native profile privacy evidence does not match the pinned policy');
  }
}

export async function strictProbe(page, run) {
  return page.evaluate(async ({ profileId, installationId }) => {
    const value = { profileId, installationId, text: 'strict completion/readback: e\u0301 \u{1f3ca}', nonce: crypto.randomUUID() };
    const name = 'v02-profile-qualification-' + profileId;
    const database = await new Promise((resolve, reject) => {
      const request = indexedDB.open(name, 1);
      request.onupgradeneeded = () => request.result.createObjectStore('probe');
      request.onerror = () => reject(Error('Profile database unavailable'));
      request.onblocked = () => reject(Error('Profile database blocked'));
      request.onsuccess = () => resolve(request.result);
    });
    let completed = false, strict = false;
    try {
      await new Promise((resolve, reject) => {
        const transaction = database.transaction('probe', 'readwrite', { durability: 'strict' });
        strict = transaction.durability === 'strict';
        transaction.objectStore('probe').add(value, value.nonce);
        transaction.onabort = () => reject(Error('Profile transaction aborted'));
        transaction.onerror = () => {};
        transaction.oncomplete = () => { completed = true; resolve(); };
      });
      const completeBeforeReadback = completed;
      const actual = await new Promise((resolve, reject) => {
        const transaction = database.transaction('probe', 'readonly');
        const request = transaction.objectStore('probe').get(value.nonce);
        request.onerror = () => reject(Error('Profile readback failed'));
        request.onsuccess = () => resolve(request.result);
      });
      return { strict, completeBeforeReadback, identical: JSON.stringify(actual) === JSON.stringify(value) };
    } finally { database.close(); }
  }, { profileId: run.profileId, installationId: run.installationId });
}
