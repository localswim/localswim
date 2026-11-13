import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents: `
      export { mountBackupPublication } from "./src/backup-publication.ts";
      export { createBrowserApi } from "./api.mjs";
    `,
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  write: false,
  format: "esm",
});
const { createBrowserApi, mountBackupPublication } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const otherBoard = "ba539cce-d162-48f6-9401-ef470d33039d";
const backupId = "0196ef9d-ec00-70f1-92a6-0ca3734bf5d3";
const laterBackup = "0196ef9e-ec00-70f1-92a6-0ca3734bf5d3";
const journalKey = `localswim:pending-backup-publication:${boardId}`;
const prefix = "/api/2026-11-10/publications";
const finished = "2026-11-13 15:00:00+00:00";
const capture = (id = backupId, revision = 3, board = boardId) => ({
  id,
  board_id: board,
  board_revision: revision,
  size: 22,
  sha2_256: "a".repeat(64),
});

function receipt(id, state, changes = {}, status = 200) {
  return Response.json(
    {
      id,
      board_id: boardId,
      backup_id: backupId,
      workspace: false,
      state,
      finished_at: state === "failed" || state === "succeeded" ? finished : null,
      error_code: state === "failed" ? "git_push_timeout" : null,
      ...changes,
    },
    { status, headers: { ETag: '"publication-receipt"', Location: `${prefix}/${id}` } },
  );
}

class Element extends EventTarget {
  constructor(doc) {
    super();
    this.ownerDocument = doc;
  }
  replaceChildren(...children) {
    this.children = children;
  }
  setAttribute() {}
  click() {
    if (!this.disabled && !this.hidden) this.dispatchEvent(new Event("click"));
  }
}

function fixture(fetchImpl) {
  const pending = new Map();
  const api = createBrowserApi({ getCapability: () => "capability", fetchImpl });
  const tab = {
    getItem: (key) => pending.get(key) ?? null,
    setItem: (key, value) => pending.set(key, value),
    removeItem: (key) => pending.delete(key),
  };
  function newDocument() {
    const doc = {
      defaultView: { sessionStorage: tab },
      createElement: () => new Element(doc),
    };
    return doc;
  }
  const doc = newDocument();
  function mount({
    id = boardId,
    owner = doc,
    initial = capture(),
    readable = true,
    publishable = true,
    readWithSession = (read) => read(),
  } = {}) {
    const life = new AbortController();
    const host = owner.createElement("section");
    const context = { backup: initial, readable, publishable };
    const controls = mountBackupPublication(host, {
      api,
      signal: life.signal,
      boardId: id,
      readWithSession,
      canRead: () => context.readable,
      canPublish: () => context.publishable,
      capturedBackup: () => context.backup,
    });
    const [publish, inspect, scope, status] = host.children;
    return { ...controls, life, host, context, publish, inspect, scope, status };
  }
  return { mount, pending, tab, newDocument };
}

test("publication stays pinned to the selected immutable backup while the live board changes", async () => {
  const calls = [];
  const accepted = Promise.withResolvers();
  const finish = Promise.withResolvers();
  const { mount, pending } = fixture((path, request) => {
    calls.push([path, request.method]);
    const { id, ...body } = JSON.parse(request.body);
    assert.match(id, /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    assert.deepEqual(body, { board_id: boardId, backup_id: backupId });
    assert.equal(request.headers.has("If-Match"), false);
    assert.deepEqual(JSON.parse(pending.get(journalKey)), { id, boardId, backupId, revision: 3 });
    accepted.resolve(id);
    return finish.promise;
  });
  const first = mount();
  assert.equal(first.publish.textContent, "Publish captured revision 3");
  assert.equal(first.scope.textContent, "Uses the configured Git destination.");
  first.publish.click();
  first.publish.click();
  const id = await accepted.promise;
  first.life.abort();
  const away = mount({ id: otherBoard });
  assert.equal(away.host.hidden, true);
  const back = mount({ initial: capture(laterBackup, 9) });
  assert.equal(back.publish.hidden, true);
  assert.equal(back.inspect.disabled, true);
  assert.match(
    back.scope.textContent,
    /^Publication for captured revision 3, requested at .*\+00:00\.$/,
  );

  finish.resolve(receipt(id, "succeeded", {}, 202));
  await flush();
  assert.equal(back.publish.textContent, "Publish captured revision 9");
  assert.equal(back.publish.hidden, false);
  assert.equal(back.status.textContent, `Published captured revision 3 at ${finished}.`);
  assert.equal(away.status.textContent, "");
  assert.equal(pending.has(journalKey), false);
  assert.deepEqual(calls, [[prefix, "POST"]]);
});

test("uncertain and nonterminal receipts keep the original scope across reload", async () => {
  const calls = [];
  let reads = 0;
  const { mount, pending, newDocument } = fixture((path, request) => {
    calls.push([path, request.method]);
    if (request.method === "POST") throw new TypeError("lost response");
    const id = path.split("/").at(-1);
    switch (++reads) {
      case 1:
        return new Response(null, { status: 404 });
      case 2:
        return receipt(id, "succeeded", { backup_id: laterBackup });
      case 3:
        return receipt(id, "succeeded", { finished_at: null });
      case 4:
        return receipt(id, "running");
      default:
        return receipt(id, "failed");
    }
  });
  const first = mount();
  first.publish.click();
  await flush();
  const original = pending.get(journalKey);
  const { id } = JSON.parse(original);
  assert.match(first.status.textContent, /could not be confirmed/);
  first.life.abort();
  const restored = mount({ owner: newDocument(), initial: null, publishable: false });
  assert.equal(restored.inspect.hidden, false);
  assert.equal(restored.publish.hidden, true);
  assert.match(restored.scope.textContent, /captured revision 3/);

  for (let read = 0; read < 3; read++) {
    restored.inspect.click();
    await flush();
    assert.equal(pending.get(journalKey), original);
    assert.match(restored.status.textContent, /could not be confirmed/);
  }
  restored.inspect.click();
  await flush();
  assert.equal(restored.status.textContent, "Publication running.");
  assert.equal(pending.get(journalKey), original);
  restored.inspect.click();
  await flush();
  assert.equal(pending.has(journalKey), false);
  assert.equal(restored.inspect.hidden, true);
  assert.equal(
    restored.status.textContent,
    `Publication failed for captured revision 3 at ${finished}. Git push timed out. The remote outcome is unknown.`,
  );
  assert.deepEqual(calls, [
    [prefix, "POST"],
    ...Array.from({ length: 5 }, () => [`${prefix}/${id}`, "GET"]),
  ]);
});

test("a rejected write clears only definitive refusals and preserves a duplicate ID", async () => {
  let status = 403;
  const { mount, pending } = fixture(() => new Response(null, { status }));
  const page = mount();
  page.publish.click();
  await flush();
  assert.equal(pending.has(journalKey), false);
  assert.equal(page.status.textContent, "The service refused publication of captured revision 3.");
  status = 409;
  page.publish.click();
  await flush();
  assert.equal(pending.has(journalKey), true);
  assert.match(page.status.textContent, /could not be confirmed/);
});

test("only known pre-reservation 503 refusals clear the publication journal", async () => {
  const cases = [
    [{ detail: "This board is closed or draining." }, false],
    [{ detail: "The service is draining." }, false],
    [{ detail: "Durable backup content is unavailable." }, false],
    [{ detail: "Publication status is unavailable." }, true],
    [
      {
        detail: "This board is closed or draining.",
        code: "outcome_unknown",
        outcome: "unknown",
      },
      true,
    ],
  ];
  for (const [problem, uncertain] of cases) {
    const { mount, pending } = fixture(() =>
      Response.json(
        {
          type: "about:blank",
          title: "Service Unavailable",
          status: 503,
          code: "unavailable",
          request_id: boardId,
          outcome: "not_applied",
          errors: [],
          ...problem,
        },
        { status: 503, headers: { "Content-Type": "application/problem+json" } },
      ),
    );
    const page = mount();
    page.publish.click();
    await flush();
    assert.equal(pending.has(journalKey), uncertain, problem.detail);
    assert.equal(page.publish.hidden, uncertain, problem.detail);
    assert.equal(page.inspect.hidden, !uncertain, problem.detail);
  }
});

test("publication requires a same-board capture, write admission, and a saved journal", async () => {
  let calls = 0;
  const { mount, pending, tab } = fixture(() => {
    calls++;
    return new Response(null, { status: 403 });
  });
  const page = mount({ initial: capture(backupId, 3, otherBoard) });
  page.publish.click();
  page.context.backup = capture();
  page.context.publishable = false;
  page.update();
  page.publish.click();
  page.context.publishable = true;
  page.context.readable = false;
  page.update();
  assert.equal(page.host.hidden, true);
  page.publish.click();
  page.context.readable = true;
  tab.setItem = () => {
    throw new Error("quota");
  };
  page.update();
  page.publish.click();
  await flush();
  assert.equal(calls, 0);
  assert.equal(pending.has(journalKey), false);
  assert.match(page.status.textContent, /record could not be saved/);
});

test("a delayed status read cannot act after the board loses read admission", async () => {
  const resume = Promise.withResolvers();
  const calls = [];
  const { mount, pending } = fixture((path, request) => {
    calls.push([path, request.method]);
    const { id } = JSON.parse(request.body);
    return receipt(id, "queued", {}, 202);
  });
  const page = mount({
    readWithSession: async (read) => {
      await resume.promise;
      return read();
    },
  });
  page.publish.click();
  await flush();
  assert.equal(page.status.textContent, "Publication queued.");
  page.inspect.click();
  page.context.readable = false;
  page.update();
  resume.resolve();
  await flush();
  assert.deepEqual(calls, [[prefix, "POST"]]);
  assert.equal(pending.has(journalKey), true);
  assert.equal(page.host.hidden, true);
});
