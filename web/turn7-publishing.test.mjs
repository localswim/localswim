import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents: `
      export { mountPublishingSettings } from "./src/publishing-settings.ts";
      export { ApiError } from "./api.mjs";
    `,
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  write: false,
  format: "esm",
});
const { ApiError, mountPublishingSettings } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const etag = (revision) => `"publishing-test-${revision}"`;

class Element extends EventTarget {
  constructor(tagName, ownerDocument) {
    super();
    this.tagName = tagName;
    this.ownerDocument = ownerDocument;
    this.children = [];
  }
  append(...children) {
    this.children.push(...children.filter((child) => child instanceof Element));
  }
  setAttribute() {}
  descendants() {
    return this.children.flatMap((child) => [child, ...child.descendants()]);
  }
}

function fixture(kind, send = undefined) {
  const scope = kind === "board" ? { kind, boardId } : { kind };
  const storage = new Map();
  const key = `localswim:publishing:${kind === "board" ? boardId : kind}`;
  const tab = {
    getItem: (id) => storage.get(id) ?? null,
    setItem: (id, value) => storage.set(id, value),
    removeItem: (id) => storage.delete(id),
  };
  const state = {
    enabled: false,
    revision: 1,
    unavailable: undefined,
    failRead: false,
    confirmed: 0,
  };
  const attempts = [];
  function reply() {
    return {
      data: { enabled: state.enabled, pending: false, error_code: null, last_success_at: null },
      etag: etag(state.revision),
    };
  }
  function read() {
    if (state.failRead) throw new ApiError("transport_failed");
    return reply();
  }
  function write(enabled, expected) {
    attempts.push({ enabled, expected, journal: tab.getItem(key) });
    if (send) return send(attempts.length);
    if (attempts.length === 1) {
      throw new ApiError("transport_failed", { outcomeUnknown: true });
    }
    if (expected !== etag(state.revision)) {
      throw new ApiError("precondition_failed", { status: 412 });
    }
    state.enabled = enabled;
    state.revision++;
    return reply();
  }
  function workspace({ data, etag: tag }) {
    return { data: { publishing: data }, etag: tag };
  }
  const api =
    kind === "workspace"
      ? {
          getWorkspace: async () => workspace(read()),
          configureWorkspacePublishing: async (...args) => workspace(await write(...args)),
        }
      : {
          getBoardPublishing: async (id) => {
            assert.equal(id, boardId);
            return read();
          },
          configureBoardPublishing: async (id, ...args) => {
            assert.equal(id, boardId);
            return write(...args);
          },
        };

  function mount() {
    const doc = { createElement: (name) => new Element(name, doc) };
    const root = doc.createElement("main");
    const ui = mountPublishingSettings(root, {
      api,
      scope,
      signal: new AbortController().signal,
      readWithSession: (read) => read(),
      canPublish: () => true,
      writeUnavailable: () => state.unavailable,
      storage: tab,
      onSaved: () => state.confirmed++,
    });
    const nodes = root.descendants();
    const save = nodes.find((node) => node.type === "submit");
    const inspect = nodes.find((node) => node.textContent === "Inspect saved settings");
    const checkbox = nodes.find((node) => node.type === "checkbox");
    const form = nodes.find((node) => node.tagName === "form");
    return {
      ...ui,
      save,
      inspect,
      checkbox,
      submit: () => form.dispatchEvent(new Event("submit")),
      feedback: () => nodes.filter((node) => node.tagName === "p").at(-1).textContent,
      edit() {
        checkbox.checked = true;
        checkbox.dispatchEvent(new Event("change"));
      },
    };
  }
  return {
    ...mount(),
    mount,
    state,
    attempts,
    journal: () => tab.getItem(key),
    replaceJournal: (value) => tab.setItem(key, value),
  };
}

const pending = `inspect:on:${etag(1)}`;

function expectOwnedJournal(journal) {
  assert.equal(journal.split("/")[0], pending);
  assert.match(journal.split("/")[1], /^[0-9a-f-]{36}$/u);
}

for (const kind of ["workspace", "board"]) {
  test(`${kind} publishing retries a lost write only after inspection and explicit submission`, async () => {
    const ui = fixture(kind);
    await ui.refresh();
    ui.edit();
    ui.submit();
    await flush();
    const original = ui.journal();
    expectOwnedJournal(original);
    assert.equal(ui.save.disabled, true);
    await ui.refresh();
    ui.submit();
    await flush();
    assert.equal(ui.attempts.length, 1);
    ui.inspect.dispatchEvent(new Event("click"));
    await flush();
    assert.equal(ui.attempts.length, 1);
    assert.equal(ui.journal(), original);
    assert.equal(ui.checkbox.disabled, true);
    assert.equal(ui.save.disabled, false);
    assert.equal(ui.save.textContent, `Retry ${kind} publishing save`);
    ui.submit();
    await flush();
    const [first, second] = ui.attempts;
    assert.deepEqual(first, { enabled: true, expected: etag(1), journal: original });
    expectOwnedJournal(second.journal);
    assert.notEqual(second.journal, original);
    assert.deepEqual(second, { enabled: true, expected: etag(1), journal: second.journal });
    assert.equal(ui.state.enabled, true);
    assert.equal(ui.state.confirmed, 1);
    assert.equal(ui.journal(), null);
  });

  test(`a late ${kind} publishing refusal cannot replace a newer retry journal`, async () => {
    const first = Promise.withResolvers();
    const retry = Promise.withResolvers();
    const ui = fixture(kind, (attempt) => (attempt === 1 ? first.promise : retry.promise));
    await ui.refresh();
    ui.edit();
    ui.submit();
    const original = ui.journal();
    const recovered = ui.mount();
    await recovered.refresh();
    recovered.inspect.dispatchEvent(new Event("click"));
    await flush();
    recovered.submit();
    const newer = ui.journal();
    expectOwnedJournal(original);
    expectOwnedJournal(newer);
    assert.notEqual(newer, original);
    assert.deepEqual(
      ui.attempts.map(({ enabled, expected }) => ({ enabled, expected })),
      [
        { enabled: true, expected: etag(1) },
        { enabled: true, expected: etag(1) },
      ],
    );

    first.reject(new ApiError("forbidden", { status: 403 }));
    await flush();
    assert.equal(ui.journal(), newer);
    assert.equal(ui.save.disabled, true);
    assert.match(ui.feedback(), /pending publishing settings changed/);

    retry.reject(new ApiError("transport_failed", { outcomeUnknown: true }));
    await flush();
    assert.equal(ui.journal(), newer);
    assert.equal(recovered.save.disabled, true);
    assert.equal(recovered.inspect.hidden, false);
    assert.equal(ui.state.confirmed, 0);
  });
}

test("a retained publishing intent without an ownership token can be inspected and retried", async () => {
  const ui = fixture("workspace");
  ui.replaceJournal(pending);
  const recovered = ui.mount();
  await recovered.refresh();
  assert.equal(recovered.save.disabled, true);
  recovered.inspect.dispatchEvent(new Event("click"));
  await flush();
  assert.equal(recovered.save.disabled, false);
  assert.equal(ui.journal(), pending);
  recovered.submit();
  await flush();
  const [attempt] = ui.attempts;
  assert.equal(attempt.expected, etag(1));
  assert.equal(attempt.enabled, true);
  expectOwnedJournal(attempt.journal);
});

test("a late original publishing commit wins the CAS and is confirmed by inspection", async () => {
  const ui = fixture("board");
  await ui.refresh();
  ui.edit();
  ui.submit();
  await flush();
  ui.inspect.dispatchEvent(new Event("click"));
  await flush();
  ui.state.enabled = true;
  ui.state.revision++;
  ui.submit();
  await flush();
  assert.equal(ui.attempts[1].expected, etag(1));
  expectOwnedJournal(ui.journal());
  assert.equal(ui.save.disabled, true);
  assert.match(ui.feedback(), /Inspect the current setting/);
  ui.inspect.dispatchEvent(new Event("click"));
  await flush();
  assert.equal(ui.attempts.length, 2);
  assert.equal(ui.state.confirmed, 1);
  assert.equal(ui.journal(), null);
});

test("board write readiness disables Save and preserves the draft until reconfirmed", async () => {
  const ui = fixture("board");
  await ui.refresh();
  ui.edit();
  for (const reason of ["Reopen the board before saving.", "Reconnect before saving."]) {
    ui.state.unavailable = reason;
    ui.updatePermission();
    assert.equal(ui.save.disabled, true);
    assert.equal(ui.feedback(), reason);
    ui.submit();
    await flush();
    assert.equal(ui.attempts.length, 0);
    assert.equal(ui.journal(), "draft:on");
  }
  ui.state.unavailable = undefined;
  ui.updatePermission();
  assert.equal(ui.save.disabled, false);
});

test("failed reads, newer validators and replaced journals fence an inspected retry", async () => {
  const ui = fixture("workspace");
  await ui.refresh();
  ui.edit();
  ui.submit();
  await flush();
  ui.inspect.dispatchEvent(new Event("click"));
  await flush();
  assert.equal(ui.save.disabled, false);

  ui.state.failRead = true;
  await ui.refresh();
  ui.state.failRead = false;
  await ui.refresh();
  assert.equal(ui.save.disabled, true);
  ui.inspect.dispatchEvent(new Event("click"));
  await flush();
  assert.equal(ui.save.disabled, false);

  ui.state.revision++;
  await ui.refresh();
  assert.equal(ui.save.disabled, true);
  ui.submit();
  await flush();
  assert.equal(ui.attempts.length, 1);
  expectOwnedJournal(ui.journal());

  ui.inspect.dispatchEvent(new Event("click"));
  await flush();
  assert.equal(ui.save.disabled, false);
  ui.replaceJournal("draft:off");
  ui.submit();
  await flush();
  assert.equal(ui.attempts.length, 1);
  assert.equal(ui.journal(), "draft:off");
});
