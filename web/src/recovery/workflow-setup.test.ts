import { openSetupStore } from "./workflow-setup.js";
import { setupRequest } from "../api/configured-workflow.test.js";

function assert(value: unknown, message: string): asserts value { if (!value) throw Error(message); }
async function refuses(work: Promise<unknown>): Promise<void> { let failed = false; try { await work; } catch { failed = true; } assert(failed, "Unsafe setup storage accepted"); }
export async function runWorkflowSetupStorageTests(): Promise<number> {
  const request = setupRequest(), name = "localswim-setup-test-" + crypto.randomUUID(), windowId = crypto.randomUUID();
  const a = await openSetupStore(indexedDB, request.workspaceId, windowId, name), b = await openSetupStore(indexedDB, request.workspaceId, windowId, name);
  let count = 0;
  try {
    const { apiVersion: _, workspaceId: __, expectedConfigRevision: ___, ...draft } = request;
    const first = await a.write(0, { draft }); assert(first.revision === 1 && (await b.read())?.value.draft.label === draft.label, "Draft not retained"); count++;
    await refuses(b.write(0, { draft: { ...draft, label: "stale" } })); count++;
    const submitted = await a.write(first.revision, { draft, submitted: request, original: "pending" });
    assert((await b.read())?.value.submitted?.workflow.workflowId === request.workflow.workflowId, "Submitted workflow changed"); count++;
    await refuses(b.write(submitted.revision, { draft, submitted: { ...request, expectedConfigRevision: 9 }, original: "unknown" })); count++;
    await refuses(a.write(submitted.revision, { draft })); count++;
    const other = await openSetupStore(indexedDB, request.workspaceId, crypto.randomUUID(), name);
    assert(await other.read() === undefined, "Second window inherited setup"); other.close(); count++;
    await a.clear(submitted.revision); assert(await b.read() === undefined, "Explicit discard not verified"); count++;
    const replacement = await a.write(0, { draft: { ...draft, label: "Replacement" } });
    assert(replacement.revision > submitted.revision, "Deletion reused an old CAS token");
    await refuses(b.write(submitted.revision, { draft: { ...draft, label: "Stale overwrite" } }));
    assert((await a.read())?.value.draft.label === "Replacement", "Stale writer overwrote replacement draft"); count++;
  } finally { a.close(); b.close(); indexedDB.deleteDatabase(name); }
  const migratedName = name + "-migration", migratedKey = request.workspaceId + ":" + windowId;
  const { apiVersion: _api, workspaceId: _workspace, expectedConfigRevision: _revision, ...migratedDraft } = request;
  await new Promise<void>((resolve, reject) => {
    const opened = indexedDB.open(migratedName, 1);
    opened.onupgradeneeded = () => opened.result.createObjectStore("setups", { keyPath: "key" });
    opened.onerror = () => reject(opened.error);
    opened.onsuccess = () => {
      const db = opened.result, transaction = db.transaction("setups", "readwrite");
      transaction.objectStore("setups").put({ key: migratedKey, revision: 9, value: { draft: migratedDraft } });
      transaction.oncomplete = () => { db.close(); resolve(); };
      transaction.onabort = () => { db.close(); reject(transaction.error); };
    };
  });
  const migrated = await openSetupStore(indexedDB, request.workspaceId, windowId, migratedName);
  try {
    await migrated.clear(9);
    const replacement = await migrated.write(0, { draft: migratedDraft });
    assert(replacement.revision > 9, "Migration clear lost the revision high-water mark");
    await refuses(migrated.write(9, { draft: migratedDraft })); count++;
  } finally { migrated.close(); indexedDB.deleteDatabase(migratedName); }
  return count;
}
