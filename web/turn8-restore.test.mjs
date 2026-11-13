import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents: `
      export { hasPendingBoardRestore, mountRestore } from "./src/restore.ts";
      export { createBrowserApi } from "./api.mjs";
    `,
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  write: false,
  format: "esm",
});
const { createBrowserApi, hasPendingBoardRestore, mountRestore } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const otherBoard = "ba539cce-d162-48f6-9401-ef470d33039d";
const key = `localswim:pending-board-restore:${boardId}`;
const prefix = "/api/2026-11-10";
const snapshotPath = `${prefix}/snapshots/${boardId}`;
const snapshotTag = `"${"a".repeat(64)}"`;
const boardTag = (revision) => `"${boardId}-${revision}"`;
const zip = new Uint8Array([80, 75, 3, 4, 17, 19]);

function snapshot(revision, { etag = snapshotTag, id = boardId } = {}) {
  return Response.json(
    { format: "localswim-snapshot", format_version: 2, board: { id, revision } },
    { headers: { ETag: etag } },
  );
}

function restored(revision) {
  return Response.json(
    { id: boardId, revision },
    { headers: { "X-Board-ETag": boardTag(revision) } },
  );
}

function verification(revision, valid = true) {
  return Response.json({
    board_id: boardId,
    revision: valid ? revision : null,
    valid,
    issues: valid ? [] : [{ field: "snapshot", code: "integrity_failure" }],
  });
}

function live(revision, availability = "open") {
  return Response.json(
    { id: boardId, name: "Sample board", policy_id: otherBoard, revision, availability },
    { headers: { ETag: boardTag(revision) } },
  );
}

class Element extends EventTarget {
  constructor(tagName, doc) {
    super();
    this.tagName = tagName;
    this.ownerDocument = doc;
  }
  replaceChildren(...children) {
    this.children = children;
  }
  append(...children) {
    this.children = children;
  }
  setAttribute() {}
  focus() {}
  set value(value) {
    if (value === "") this.files = [];
  }
  click() {
    if (!this.disabled && !this.hidden) this.dispatchEvent(new Event("click"));
  }
}

function fixture(fetchImpl) {
  const tab = new Map();
  const refreshes = [];
  let held = false;
  const api = createBrowserApi({ getCapability: () => "capability", fetchImpl });
  function newDocument() {
    const window = new EventTarget();
    window.sessionStorage = {
      getItem: (key) => tab.get(key) ?? null,
      setItem: (key, value) => tab.set(key, value),
      removeItem: (key) => tab.delete(key),
    };
    const doc = {
      defaultView: window,
      createElement: (tagName) => new Element(tagName, doc),
    };
    return doc;
  }
  const doc = newDocument();
  function mount(id = boardId, owner = doc) {
    const life = new AbortController();
    const host = owner.createElement("section");
    const context = { name: "Sample board", revision: 3, allowed: true, readable: true };
    const controls = mountRestore(host, {
      api,
      signal: life.signal,
      boardId: id,
      readWithSession: (read) => read(),
      getContext: () => context,
      beginWrite: () => {
        assert.equal(held, false);
        held = true;
        return () => {
          held = false;
        };
      },
      refresh: (...args) => refreshes.push(args),
    });
    const [, label, review, confirmation, confirm, cancel, inspect, status] = host.children;
    const [picker] = label.children;
    function choose() {
      picker.files = [new File([zip], "backup.zip", { type: "application/x-zip-compressed" })];
      picker.dispatchEvent(new Event("change"));
      review.click();
    }
    return {
      ...controls,
      life,
      context,
      review,
      confirmation,
      confirm,
      cancel,
      inspect,
      status,
      choose,
    };
  }
  return { tab, refreshes, doc, mount, newDocument };
}

test("restore uploads one native ZIP with the rendered revision's snapshot validator", async () => {
  const submitted = Promise.withResolvers();
  const finish = Promise.withResolvers();
  const calls = [];
  const { tab, doc, mount, refreshes } = fixture(async (path, request) => {
    calls.push([path, request.method]);
    if (request.method === "GET") return snapshot(3);
    assert.equal(request.headers.get("If-Match"), snapshotTag);
    assert.equal(request.headers.get("Content-Type"), "application/zip");
    assert.deepEqual(new Uint8Array(await request.body.arrayBuffer()), zip);
    assert.equal(JSON.parse(tab.get(key)).phase, "submitted");
    assert.equal(JSON.parse(tab.get(key)).revision, 3);
    submitted.resolve();
    return finish.promise;
  });
  const original = mount();
  original.choose();
  assert.match(original.confirmation.textContent, /Sample board at revision 3 with backup\.zip/);
  assert.deepEqual(calls, []);
  original.cancel.click();
  assert.equal(original.confirm.hidden, true);
  original.review.click();
  original.confirm.click();
  original.confirm.click();
  await submitted.promise;
  assert.equal(hasPendingBoardRestore(doc, boardId), true);
  original.life.abort();
  assert.equal(hasPendingBoardRestore(doc, otherBoard), false);
  const back = mount();
  assert.equal(back.inspect.disabled, true);
  finish.resolve(restored(4));
  await flush();
  assert.deepEqual(calls, [
    [snapshotPath, "GET"],
    [snapshotPath, "PUT"],
  ]);
  assert.deepEqual(refreshes, [[boardId, 4]]);
  assert.equal(JSON.parse(tab.get(key)).phase, "restored");
  back.context.revision = 4;
  back.update();
  assert.equal(hasPendingBoardRestore(doc, boardId), false);
  assert.equal(back.status.textContent, "Native backup restored at revision 4.");
});

test("preflight refuses stale views, foreign snapshots, and board tags before upload", async () => {
  for (const response of [
    snapshot(4),
    snapshot(3, { id: otherBoard }),
    snapshot(3, { etag: boardTag(3) }),
  ]) {
    let writes = 0;
    const { mount, tab, refreshes } = fixture((_, request) => {
      if (request.method === "PUT") writes++;
      return response;
    });
    const controls = mount();
    controls.choose();
    controls.confirm.click();
    await flush();
    assert.equal(writes, 0);
    assert.equal(tab.has(key), false);
    assert.deepEqual(refreshes, [[boardId]]);
  }
  const pending = Promise.withResolvers();
  let writes = 0;
  const { mount } = fixture((_, request) => {
    if (request.method === "PUT") writes++;
    return pending.promise;
  });
  const controls = mount();
  controls.choose();
  controls.confirm.click();
  controls.context.revision = 4;
  pending.resolve(snapshot(3));
  await flush();
  assert.equal(writes, 0);
});

test("a lost response survives reload and only newer reconciled readback releases writes", async () => {
  let writes = 0;
  let revision = 3;
  let durableValid = false;
  const { mount, doc, tab, refreshes, newDocument } = fixture((path, request) => {
    if (request.method === "PUT") {
      writes++;
      throw new Error("transport lost");
    }
    if (path === snapshotPath) return snapshot(3);
    if (path === `${prefix}/verifications/${boardId}`) return verification(revision, durableValid);
    assert.equal(path, `${prefix}/boards/${boardId}`);
    return live(revision);
  });
  const original = mount();
  original.choose();
  original.confirm.click();
  await flush();
  original.life.abort();
  assert.equal(hasPendingBoardRestore(doc, boardId), true);
  const reloaded = newDocument();
  const current = mount(boardId, reloaded);
  assert.match(current.status.textContent, /outcome is unknown/);
  current.inspect.click();
  await flush();
  assert.match(current.status.textContent, /failed verification/);
  durableValid = true;
  current.inspect.click();
  await flush();
  assert.match(current.status.textContent, /remains unknown/);
  assert.equal(hasPendingBoardRestore(reloaded, boardId), true);
  revision = 5;
  current.inspect.click();
  await flush();
  assert.equal(JSON.parse(tab.get(key)).phase, "inspected");
  assert.equal(hasPendingBoardRestore(reloaded, boardId), true);
  current.context.revision = 5;
  current.update();
  assert.equal(hasPendingBoardRestore(reloaded, boardId), false);
  assert.equal(current.status.textContent, "The live and saved board agree at revision 5.");
  assert.equal(writes, 1);
  assert.deepEqual(refreshes, [[boardId], [boardId], [boardId], [boardId]]);
});

test("definite damaged ZIP refusals offer inspection at the unchanged revision", async () => {
  for (const response of [
    new Response(null, { status: 400 }),
    Response.json(
      {
        type: "about:blank",
        title: "Service Unavailable",
        status: 503,
        code: "unavailable",
        detail: "Verified recovery content is unavailable.",
        request_id: otherBoard,
        outcome: "not_applied",
        errors: [],
      },
      { status: 503, headers: { "Content-Type": "application/problem+json" } },
    ),
  ]) {
    let writes = 0;
    const { mount, tab } = fixture((path, request) => {
      if (request.method === "PUT") {
        writes++;
        return response;
      }
      if (path === snapshotPath) return snapshot(3);
      if (path === `${prefix}/verifications/${boardId}`) return verification(3);
      return live(3);
    });
    const current = mount();
    current.choose();
    current.confirm.click();
    await flush();
    assert.match(current.status.textContent, /Inspect the board/);
    assert.equal(tab.has(key), true);
    current.inspect.click();
    await flush();
    assert.equal(tab.has(key), false);
    assert.equal(writes, 1);
  }
});

test("a detached reply cannot overwrite a different journal attempt", async () => {
  const submitted = Promise.withResolvers();
  const finish = Promise.withResolvers();
  const { tab, mount } = fixture((_, request) => {
    if (request.method === "GET") return snapshot(3);
    submitted.resolve();
    return finish.promise;
  });
  const controls = mount();
  controls.choose();
  controls.confirm.click();
  await submitted.promise;
  controls.life.abort();
  const replacement = JSON.stringify({ attempt: otherBoard, revision: 9, phase: "submitted" });
  tab.set(key, replacement);
  finish.resolve(restored(4));
  await flush();
  assert.equal(tab.get(key), replacement);
});
