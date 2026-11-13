import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush, setTimeout as delay } from "node:timers/promises";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents: `
      export { mountServiceTools, hasPendingServiceActivation } from "./src/service-tools.ts";
      export { ApiError } from "./api.mjs";
    `,
    resolveDir: import.meta.dirname,
  },
  bundle: true,
  write: false,
  format: "esm",
});
const {
  mountServiceTools,
  hasPendingServiceActivation,
  ApiError: BrowserApiError,
} = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

const running = "24025c7c-599a-4d89-8f82-56a82a486974";
const candidate = "5ea7b117-ea76-4c57-9cc8-73be303c687b";
const alternative = "3929f28d-6d26-4675-a877-b9ff3b9db5d0";
const instanceId = "019aa833-7650-746b-a3f0-e2f7af0b9e3f";
const replacementId = "019aa833-7651-746b-a3f0-e2f7af0b9e3f";
const serviceEtag = '"captured-service-validator"';
const initialService = {
  data: {
    build_id: running,
    service_instance_id: instanceId,
    port: 12345,
    state: "running",
    restart_error_code: null,
  },
  etag: serviceEtag,
};

class Element extends EventTarget {
  children = [];
  value = "";
  textContent = "";
  hidden = false;
  disabled = false;

  get childElementCount() {
    return this.children.length;
  }

  append(...children) {
    this.children.push(...children);
  }

  replaceChildren(...children) {
    this.children = children;
  }

  setAttribute() {}

  click() {
    if (!this.disabled && !this.hidden) this.dispatchEvent(new Event("click"));
  }
}

function fixture(t) {
  const records = new Map();
  const storage = {
    getItem: (key) => records.get(key) ?? null,
    setItem: (key, value) => records.set(key, value),
    removeItem: (key) => records.delete(key),
  };
  const doc = {
    defaultView: { sessionStorage: storage },
    createElement: (tag) => Object.assign(new Element(), { tag }),
  };
  function mount(api, extra = {}) {
    const life = new AbortController();
    t.after(() => life.abort());
    const host = Object.assign(new Element(), { ownerDocument: doc });
    const controller = mountServiceTools(host, {
      api: {
        getService: async () => initialService,
        listBuilds: async () => ({
          data: {
            builds: [
              { id: candidate, version: "Candidate", validation: "valid", error_code: null },
              { id: alternative, version: "Alternative", validation: "valid", error_code: null },
              { id: running, version: "Current", validation: "valid", error_code: null },
            ],
          },
        }),
        ...api,
      },
      signal: life.signal,
      readWithSession: (read) => read(),
      canManage: () => true,
      onReconnect: async () => {},
      ...extra,
    });
    const [section] = host.children;
    const [, , , , label, activate, refresh, stop, shutdownStatus, status] = section.children;
    const [, select] = label.children;
    return {
      ...controller,
      section,
      select,
      activate,
      refresh,
      stop,
      shutdownStatus,
      status,
      dispose: () => life.abort(),
    };
  }
  return { records, storage, mount };
}

async function until(condition) {
  for (let attempt = 0; attempt < 20; attempt++) {
    if (condition()) return;
    await flush();
  }
  assert.fail("Condition did not settle");
}

function selectBuild(panel) {
  panel.select.value = candidate;
  panel.select.dispatchEvent(new Event("change"));
}

test("the activation fence reads the current tab storage by default", (t) => {
  const { storage } = fixture(t);
  const original = Object.getOwnPropertyDescriptor(globalThis, "sessionStorage");
  t.after(() => {
    if (original) Object.defineProperty(globalThis, "sessionStorage", original);
    else Reflect.deleteProperty(globalThis, "sessionStorage");
  });
  Object.defineProperty(globalThis, "sessionStorage", { configurable: true, value: storage });
  assert.equal(hasPendingServiceActivation(), false);
  storage.setItem("localswim.service-activation.v1", "{}");
  assert.equal(hasPendingServiceActivation(), true);
  Object.defineProperty(globalThis, "sessionStorage", {
    configurable: true,
    get() {
      throw new Error("Storage access denied");
    },
  });
  assert.equal(hasPendingServiceActivation(), false);
  assert.equal(hasPendingServiceActivation(storage), true);
});

test("activation persists its captured validator and inspects the original receipt from a remounted route", async (t) => {
  const { records, storage, mount } = fixture(t);
  const write = Promise.withResolvers();
  const calls = [];
  let capability = "original";
  let replacement = false;
  const api = {
    getService: async () =>
      replacement
        ? {
            ...initialService,
            data: {
              ...initialService.data,
              service_instance_id: replacementId,
              build_id: candidate,
            },
          }
        : initialService,
    activateBuild: (id, buildId, tag) => {
      assert.deepEqual(JSON.parse([...records.values()][0]), {
        id,
        buildId: candidate,
        instanceId,
        etag: serviceEtag,
        port: 12345,
      });
      calls.push([id, buildId, tag]);
      return write.promise;
    },
    getActivation: async (id, buildId) => {
      assert.deepEqual([id, buildId, capability], [calls[0][0], candidate, "fresh"]);
      return { id, build_id: buildId, state: "activated", error_code: null };
    },
  };
  const workspace = mount(api);
  await until(() => workspace.select.children.length === 3 && !workspace.stop.disabled);
  assert.equal(workspace.activate.disabled, true);
  selectBuild(workspace);
  workspace.activate.click();
  workspace.activate.click();
  assert.equal(workspace.stop.disabled, true);
  assert.equal(hasPendingServiceActivation(storage), true);
  workspace.dispose();
  const board = mount(api, {
    recoveryOnly: true,
    onReconnect: async () => {
      capability = "fresh";
    },
  });
  assert.equal(board.activate.hidden, true);
  assert.equal(board.stop.hidden, true);
  assert.equal(board.section.hidden, false);
  replacement = true;
  write.reject(new BrowserApiError("transport_failed", { outcomeUnknown: true }));
  await until(() => !hasPendingServiceActivation(storage));
  assert.equal(calls.length, 1);
  assert.match(calls[0][0], /^[\da-f]{8}-[\da-f]{4}-7[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/);
  assert.deepEqual(calls[0].slice(1), [candidate, serviceEtag]);
  assert.match(board.status.textContent, /Build activated\. Reconnected on the same port/);
});

test("a refused activation refreshes only on request and never replays the original write", async (t) => {
  const { storage, mount } = fixture(t);
  let writes = 0;
  let serviceReads = 0;
  const panel = mount({
    getService: async () => {
      serviceReads++;
      return initialService;
    },
    activateBuild: async () => {
      writes++;
      throw new BrowserApiError("http_error", { status: 412 });
    },
  });
  await until(() => panel.select.children.length === 3 && !panel.stop.disabled);
  selectBuild(panel);
  panel.activate.click();
  await until(() => /Service status changed/.test(panel.status.textContent));
  assert.equal(panel.activate.disabled, true);
  assert.equal(hasPendingServiceActivation(storage), false);
  assert.equal(serviceReads, 1);
  panel.refresh.click();
  await until(() => !panel.activate.disabled);
  assert.equal(serviceReads, 2);
  assert.equal(writes, 1);
});

for (const cachedBeforeRefusal of [true, false]) {
  test(`a detached activation refusal fences a remounted service read (${cachedBeforeRefusal ? "cached" : "in flight"})`, async (t) => {
    const { storage, mount } = fixture(t);
    const write = Promise.withResolvers();
    const stale = Promise.withResolvers();
    const freshTag = '"fresh-service-validator"';
    const calls = [];
    const workspace = mount({ activateBuild: () => write.promise });
    await until(() => workspace.select.children.length === 3 && !workspace.stop.disabled);
    selectBuild(workspace);
    workspace.activate.click();
    workspace.dispose();
    let reads = 0;
    const active = mount({
      getService: () => {
        reads++;
        return reads === 1 ? stale.promise : Promise.resolve({ ...initialService, etag: freshTag });
      },
      activateBuild: async (id, buildId, etag) => {
        calls.push([id, buildId, etag]);
        throw new BrowserApiError("http_error", { status: 412 });
      },
    });
    await until(() => reads === 1);
    if (cachedBeforeRefusal) {
      stale.resolve(initialService);
      await flush();
    }
    write.reject(new BrowserApiError("http_error", { status: 412 }));
    await until(() => /Service status changed/.test(active.status.textContent));
    if (!cachedBeforeRefusal) stale.resolve(initialService);
    await until(() => !active.refresh.disabled);
    selectBuild(active);
    assert.equal(hasPendingServiceActivation(storage), false);
    assert.equal(active.activate.disabled, true);
    assert.equal(active.stop.disabled, true);
    assert.equal(reads, 1);
    active.refresh.click();
    await until(() => !active.activate.disabled);
    active.activate.click();
    await until(() => calls.length === 1);
    assert.deepEqual(calls[0].slice(1), [candidate, freshTag]);
  });
}

test("a missing receipt on the original service keeps the operation fenced for explicit inspection", async (t) => {
  const { storage, mount } = fixture(t);
  const failure = new BrowserApiError("http_error", { status: 404 });
  const id = "019aa833-7652-746b-a3f0-e2f7af0b9e3f";
  storage.setItem(
    "localswim.service-activation.v1",
    JSON.stringify({ id, buildId: candidate, instanceId, etag: serviceEtag, port: 12345 }),
  );
  const receipts = [];
  let managed = true;
  const panel = mount(
    {
      getActivation: async (receiptId, buildId) => {
        receipts.push([receiptId, buildId]);
        throw failure;
      },
    },
    { canManage: () => managed },
  );
  await until(() => receipts.length === 1);
  managed = false;
  await delay(800);
  managed = true;
  panel.update();
  panel.refresh.click();
  await until(() => receipts.length === 2 && !panel.refresh.disabled);
  assert.deepEqual(receipts, [
    [id, candidate],
    [id, candidate],
  ]);
  assert.equal(hasPendingServiceActivation(storage), true);
  assert.equal(panel.activate.disabled, true);
  assert.equal(panel.stop.disabled, true);
  assert.match(panel.status.textContent, /Activation outcome is unconfirmed/);
});

test("shutdown keeps its captured validator and leaves activation unavailable during drain", async (t) => {
  const { storage, mount } = fixture(t);
  const shutdown = Promise.withResolvers();
  const calls = [];
  const panel = mount({
    stopService: (service, tag) => {
      calls.push([service, tag]);
      return shutdown.promise;
    },
  });
  await until(() => panel.select.children.length === 3 && !panel.stop.disabled);
  selectBuild(panel);
  panel.stop.click();
  panel.activate.click();
  assert.equal(panel.activate.disabled, true);
  assert.equal(hasPendingServiceActivation(storage), false);
  shutdown.resolve({ ...initialService.data, state: "draining" });
  await until(() => /Shutdown accepted/.test(panel.shutdownStatus.textContent));
  assert.equal(panel.stop.disabled, true);
  assert.deepEqual(calls, [[initialService.data, serviceEtag]]);
  assert.equal(panel.activate.disabled, true);
});
