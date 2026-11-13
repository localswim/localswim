import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents: `
      export { mountBackup, formatOperationTime } from "./src/backup.ts";
      export { createBrowserApi } from "./api.mjs";
    `,
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  write: false,
  format: "esm",
});
const { createBrowserApi, formatOperationTime, mountBackup } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const otherBoard = "ba539cce-d162-48f6-9401-ef470d33039d";
const tag = (id, revision) => `"${id}-${revision}"`;
const prefix = "/api/2026-11-10/backups";
const journalKey = `localswim:pending-backup-capture:${boardId}`;
const zip = new Uint8Array([80, 75, 5, 6, ...Array(18).fill(0)]);
const sha = createHash("sha256").update(zip).digest("hex");

function metadata(id, revision) {
  return {
    id,
    board_id: boardId,
    board_revision: revision,
    size: zip.length,
    sha2_256: sha,
    attachment_ids: [],
  };
}

class Element extends EventTarget {
  constructor(tagName, doc, downloads) {
    super();
    this.tagName = tagName;
    this.ownerDocument = doc;
    this.downloads = downloads;
  }
  replaceChildren(...children) {
    this.children = children;
  }
  setAttribute() {}
  click() {
    if (this.tagName === "a") this.downloads.push(this.download);
    if (!this.disabled && !this.hidden) this.dispatchEvent(new Event("click"));
  }
}

function fixture(fetchImpl) {
  const pending = new Map();
  const downloads = [];
  const api = createBrowserApi({ getCapability: () => "capability", fetchImpl });
  function newDocument() {
    const doc = {
      defaultView: {
        sessionStorage: {
          getItem: (key) => pending.get(key) ?? null,
          setItem: (key, value) => pending.set(key, value),
          removeItem: (key) => pending.delete(key),
        },
      },
      createElement: (tagName) => new Element(tagName, doc, downloads),
    };
    return doc;
  }
  const doc = newDocument();
  function mount(id = boardId, owner = doc) {
    const life = new AbortController();
    const host = owner.createElement("section");
    const context = { etag: tag(id, 3), revision: 3, allowed: true };
    const access = { readable: true };
    const controls = mountBackup(host, {
      api,
      signal: life.signal,
      boardId: id,
      readWithSession: (read) => read(),
      canRead: () => access.readable,
      captureContext: () => context,
    });
    const [, create, inspect, download, summary, status] = host.children;
    return { ...controls, life, context, access, create, inspect, download, summary, status };
  }
  return { mount, pending, downloads, newDocument };
}

test("a delayed backup remains scoped to its original board and revision after navigation", async () => {
  const submitted = Promise.withResolvers();
  const finish = Promise.withResolvers();
  const requestLog = [];
  const { mount, pending } = fixture((path, request) => {
    requestLog.push([path, request.method]);
    const { id, board_id, expected_board_etag } = JSON.parse(request.body);
    assert.equal(board_id, boardId);
    assert.equal(expected_board_etag, tag(boardId, 3));
    assert.deepEqual(JSON.parse(pending.get(journalKey)), { id, revision: 3 });
    submitted.resolve(id);
    return finish.promise;
  });
  const first = mount();
  first.create.click();
  first.create.click();
  const id = await submitted.promise;
  first.life.abort();
  const away = mount(otherBoard);
  assert.equal(away.create.disabled, false);
  assert.equal(away.capturedBackup(), undefined);
  const back = mount();
  back.context.etag = tag(boardId, 4);
  back.context.revision = 4;
  back.update();
  assert.equal(back.create.disabled, true);
  assert.equal(back.inspect.disabled, true);

  finish.resolve(
    Response.json(metadata(id, 3), { status: 201, headers: { Location: `${prefix}/${id}` } }),
  );
  await flush();
  assert.equal(back.capturedBackup().id, id);
  assert.equal(back.capturedBackup().board_revision, 3);
  assert.match(
    back.summary.textContent,
    /^Captured revision 3 at \d{4}-\d\d-\d\d \d\d:\d\d:\d\d\+00:00\.$/,
  );
  assert.equal(away.capturedBackup(), undefined);
  assert.equal(pending.has(journalKey), false);
  assert.deepEqual(requestLog, [[prefix, "POST"]]);
});

test("an unknown capture survives reload and only its original ID and revision can settle it", async () => {
  const calls = [];
  let inspection = 0;
  const { mount, newDocument, pending } = fixture((path, request) => {
    calls.push([path, request.method]);
    if (request.method === "POST") throw new TypeError("response lost");
    const id = path.split("/").at(-1);
    inspection++;
    if (inspection === 1) return new Response(null, { status: 404 });
    return Response.json(metadata(id, inspection === 2 ? 4 : 3), {
      headers: { ETag: tag(boardId, 3) },
    });
  });
  const first = mount();
  first.create.click();
  await flush();
  const { id } = JSON.parse(pending.get(journalKey));
  first.life.abort();

  const restored = mount(boardId, newDocument());
  assert.equal(restored.create.disabled, true);
  assert.equal(restored.inspect.hidden, false);
  restored.create.click();
  for (let count = 0; count < 2; count++) {
    restored.inspect.click();
    await flush();
    assert.equal(restored.capturedBackup(), undefined);
    assert.equal(JSON.parse(pending.get(journalKey)).id, id);
  }
  restored.inspect.click();
  await flush();
  assert.equal(restored.capturedBackup().id, id);
  assert.equal(pending.has(journalKey), false);
  assert.deepEqual(calls, [[prefix, "POST"], ...Array(3).fill([`${prefix}/${id}`, "GET"])]);
});

test("download verifies size and digest before publishing only the captured revision", async () => {
  const gated = Promise.withResolvers();
  let downloadsAttempted = 0;
  let capturedId;
  const response = (bytes) =>
    new Response(bytes, { headers: { "Content-Type": "application/zip" } });
  const { mount, downloads } = fixture((path, request) => {
    if (request.method === "POST") {
      capturedId = JSON.parse(request.body).id;
      return Response.json(metadata(capturedId, 3), {
        status: 201,
        headers: { Location: `${prefix}/${capturedId}` },
      });
    }
    assert.equal(path, `${prefix}/${capturedId}/content`);
    downloadsAttempted++;
    if (downloadsAttempted === 1) return response(zip.subarray(0, -1));
    if (downloadsAttempted === 2) return response(new Uint8Array(zip.length));
    if (downloadsAttempted === 3) return gated.promise;
    return response(zip);
  });
  const first = mount();
  first.create.click();
  await flush();
  async function waitForDownload(view) {
    for (let attempt = 0; attempt < 1000 && view.download.disabled; attempt++) await flush();
    assert.equal(view.download.disabled, false);
  }
  for (let count = 0; count < 2; count++) {
    first.download.click();
    await waitForDownload(first);
    assert.match(first.status.textContent, /could not be verified/);
    assert.deepEqual(downloads, []);
  }
  first.download.click();
  first.life.abort();
  const back = mount();
  gated.resolve(response(zip));
  await waitForDownload(back);
  assert.deepEqual(downloads, []);

  back.context.etag = tag(boardId, 4);
  back.context.revision = 4;
  back.update();
  back.download.click();
  await waitForDownload(back);
  assert.deepEqual(downloads, [`localswim-${boardId}-r3.zip`]);
});

test("the operation timestamp discards subsecond precision", () => {
  assert.equal(
    formatOperationTime("01234567-89ab-7abc-9abc-123456789abc"),
    "2009-08-23 03:58:16+00:00",
  );
});
