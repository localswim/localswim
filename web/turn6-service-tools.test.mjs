import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush, setTimeout as delay } from "node:timers/promises";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents: `
      export { mountServiceTools, hasPendingServiceActivation } from "./src/service-tools.ts";
      export { ApiError, createBrowserApi } from "./api.mjs";
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
  createBrowserApi,
} = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

const running = "24025c7c-599a-4d89-8f82-56a82a486974";
const candidate = "5ea7b117-ea76-4c57-9cc8-73be303c687b";
const alternative = "3929f28d-6d26-4675-a877-b9ff3b9db5d0";
const instanceId = "019aa833-7650-746b-a3f0-e2f7af0b9e3f";
const replacementId = "019aa833-7651-746b-a3f0-e2f7af0b9e3f";
const serviceEtag = `"${instanceId}-2"`;
const activationKey = "localswim.service-activation.v1";
const originalIntent = {
  id: "019aa833-7652-746b-a3f0-e2f7af0b9e3f",
  attempt: "79acc8ba-c0cc-4fd1-aaf5-8b11863d1940",
  buildId: candidate,
  runningBuildId: running,
  instanceId,
  etag: serviceEtag,
  port: 12345,
  acknowledged: false,
};
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
    const [, , , , label, activate, refresh, stop, shutdownStatus, status, retry, dismiss] =
      section.children;
    const [, select] = label.children;
    return {
      ...controller,
      section,
      select,
      activate,
      refresh,
      retry,
      dismiss,
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

test("activation receipt validation enforces state-dependent error codes", async () => {
  const { id } = originalIntent;
  let receipt;
  const api = createBrowserApi({
    getCapability: () => "capability",
    fetchImpl: async (_url, request) =>
      Response.json(receipt, {
        status: request.method === "POST" ? 202 : 200,
        headers: { ETag: serviceEtag, Location: `/api/2026-11-10/activations/${id}` },
      }),
  });
  for (const [state, error_code] of [
    ["accepted", "activation_handoff_failed"],
    ["activated", "activation_interrupted"],
    ["failed", null],
    ["failed", "unknown"],
  ]) {
    receipt = { id, build_id: candidate, state, error_code };
    await assert.rejects(api.getActivation(id, candidate), { code: "invalid_response" });
  }
  receipt = { id, build_id: candidate, state: "accepted", error_code: "activation_interrupted" };
  await assert.rejects(api.activateBuild(id, candidate, serviceEtag), {
    code: "invalid_response",
    outcomeUnknown: true,
  });
  for (const error_code of ["activation_handoff_failed", "activation_interrupted"]) {
    receipt = { id, build_id: candidate, state: "failed", error_code };
    assert.deepEqual(await api.getActivation(id, candidate), receipt);
  }
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
      const intent = JSON.parse([...records.values()][0]);
      assert.match(
        intent.attempt,
        /^[\da-f]{8}-[\da-f]{4}-4[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/,
      );
      assert.deepEqual(intent, { ...originalIntent, id, attempt: intent.attempt });
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

test("replacement session renewal must preserve administrator access before receipt inspection", async (t) => {
  const { storage, mount } = fixture(t);
  const { id } = originalIntent;
  storage.setItem(activationKey, JSON.stringify(originalIntent));
  let managed = true;
  let receiptReads = 0;
  let renewals = 0;
  const panel = mount(
    {
      getService: async () => ({
        ...initialService,
        data: { ...initialService.data, service_instance_id: replacementId, build_id: candidate },
      }),
      getActivation: async () => {
        receiptReads++;
        return { id, build_id: candidate, state: "activated", error_code: null };
      },
    },
    {
      recoveryOnly: true,
      canManage: () => managed,
      onReconnect: async () => {
        renewals++;
        managed = false;
      },
    },
  );
  await until(() => renewals === 1 && !panel.refresh.disabled);
  panel.update();
  assert.equal(receiptReads, 0);
  assert.equal(hasPendingServiceActivation(storage), true);
  assert.equal(panel.section.hidden, true);
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

test("explicit retry preserves the original activation CAS and retains a refused retry for inspection", async (t) => {
  const { storage, mount } = fixture(t);
  const failure = new BrowserApiError("http_error", { status: 404 });
  const { id, attempt } = originalIntent;
  storage.setItem(activationKey, JSON.stringify(originalIntent));
  const receipts = [];
  const writes = [];
  let managed = true;
  const panel = mount(
    {
      getService: async () => ({
        ...initialService,
        data: { ...initialService.data, restart_error_code: "git_push_failed" },
      }),
      getActivation: async (receiptId, buildId) => {
        receipts.push([receiptId, buildId]);
        throw failure;
      },
      activateBuild: async (...args) => {
        writes.push(args);
        const next = JSON.parse(storage.getItem(activationKey));
        assert.notEqual(next.attempt, attempt);
        assert.deepEqual({ ...next, attempt }, originalIntent);
        throw new BrowserApiError("http_error", { status: 412 });
      },
    },
    { canManage: () => managed },
  );
  await until(() => receipts.length === 1);
  assert.equal(panel.retry.hidden, true);
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
  assert.equal(panel.retry.hidden, false);
  assert.deepEqual(writes, []);
  panel.retry.click();
  panel.retry.click();
  await until(() => /refused the retry/.test(panel.status.textContent));
  assert.deepEqual(writes, [[id, candidate, serviceEtag]]);
  assert.deepEqual(receipts, [
    [id, candidate],
    [id, candidate],
    [id, candidate],
  ]);
  assert.equal(panel.retry.hidden, true);
  assert.equal(panel.activate.disabled, true);
  assert.equal(panel.stop.disabled, true);
  assert.equal(hasPendingServiceActivation(storage), true);
});

test("the retry click rereads the original service validator after checking receipt absence", async (t) => {
  const { storage, mount } = fixture(t);
  storage.setItem(activationKey, JSON.stringify(originalIntent));
  let reads = 0;
  let serviceReads = 0;
  let managed = true;
  let writes = 0;
  let changeOnNextReceipt = false;
  let reply = initialService;
  const panel = mount(
    {
      getService: async () => {
        serviceReads++;
        return reply;
      },
      getActivation: async () => {
        reads++;
        if (changeOnNextReceipt) reply = { ...reply, etag: '"replacement-service-validator"' };
        throw new BrowserApiError("http_error", { status: 404 });
      },
      activateBuild: async () => {
        writes++;
      },
    },
    { canManage: () => managed },
  );
  await until(() => reads === 1);
  managed = false;
  await delay(800);
  managed = true;
  panel.update();
  panel.refresh.click();
  await until(() => !panel.retry.hidden);
  changeOnNextReceipt = true;
  const beforeRetry = serviceReads;
  panel.retry.click();
  await until(() => /Activation outcome is unconfirmed/.test(panel.status.textContent));
  assert.equal(serviceReads - beforeRetry, 2);
  assert.equal(writes, 0);
  assert.equal(panel.retry.hidden, true);
  assert.deepEqual(JSON.parse(storage.getItem(activationKey)), originalIntent);
});

test("stable advanced validator and missing receipt allow explicit dismissal after retry publication refusal", async (t) => {
  const { storage, mount } = fixture(t);
  const advancedTag = `"${instanceId}-4"`;
  const newerTag = `"${instanceId}-6"`;
  const calls = [];
  let service = initialService;
  let changeOnReceipt = false;
  let canManage = true;
  let receiptReads = 0;
  storage.setItem(activationKey, JSON.stringify(originalIntent));
  const panel = mount(
    {
      getService: async () => service,
      getActivation: async (id, buildId) => {
        assert.deepEqual([id, buildId], [originalIntent.id, candidate]);
        receiptReads++;
        if (changeOnReceipt) service = { ...service, etag: newerTag };
        throw new BrowserApiError("http_error", { status: 404 });
      },
      activateBuild: async (id, buildId, etag) => {
        calls.push([id, buildId, etag]);
        if (calls.length === 1) {
          service = {
            data: { ...service.data, restart_error_code: "git_push_failed" },
            etag: advancedTag,
          };
          throw new BrowserApiError("final_publication_failed", { status: 503 });
        }
        throw new BrowserApiError("http_error", { status: 412 });
      },
    },
    { canManage: () => canManage },
  );
  await until(() => receiptReads === 1);
  canManage = false;
  await delay(800);
  canManage = true;
  panel.update();
  panel.refresh.click();
  await until(() => !panel.retry.hidden);
  panel.retry.click();
  await until(() => /refused the retry/.test(panel.status.textContent));
  assert.equal(panel.dismiss.hidden, true);
  assert.equal(hasPendingServiceActivation(storage), true);
  for (const etag of [
    `"${instanceId}-1"`,
    `"${replacementId}-4"`,
    `"${instanceId}-04"`,
    `"${instanceId}-18446744073709551616"`,
    '"unbound-advanced-service-validator"',
  ]) {
    service = { ...service, etag };
    panel.refresh.click();
    await until(() => /Activation outcome is unconfirmed/.test(panel.status.textContent));
    assert.equal(panel.dismiss.hidden, true);
    assert.equal(panel.retry.hidden, true);
  }
  service = { ...service, etag: advancedTag };
  panel.refresh.click();
  await until(() => !panel.dismiss.hidden);
  assert.equal(panel.retry.hidden, true);
  assert.equal(hasPendingServiceActivation(storage), true);

  changeOnReceipt = true;
  panel.dismiss.click();
  await until(() => /Activation outcome is unconfirmed/.test(panel.status.textContent));
  assert.equal(hasPendingServiceActivation(storage), true);
  assert.equal(panel.dismiss.hidden, true);
  assert.equal(calls.length, 1);

  changeOnReceipt = false;
  panel.refresh.click();
  await until(() => !panel.dismiss.hidden);
  panel.dismiss.click();
  await until(() => !hasPendingServiceActivation(storage));
  assert.match(panel.status.textContent, /Dismissed original activation/);
  assert.deepEqual(calls, [[originalIntent.id, candidate, serviceEtag]]);
  selectBuild(panel);
  await until(() => !panel.activate.disabled);
  panel.activate.click();
  await until(() => calls.length === 2);
  assert.notEqual(calls[1][0], originalIntent.id);
  assert.deepEqual(calls[1].slice(1), [candidate, newerTag]);
});

test("dismisses a missing cold-start activation receipt after an exact retry advances revision zero to two", async (t) => {
  const { storage, mount } = fixture(t);
  const coldTag = `"${instanceId}-0"`;
  const advancedTag = `"${instanceId}-2"`;
  const calls = [];
  let service = { ...initialService, etag: coldTag };
  let canManage = true;
  let receiptReads = 0;
  const panel = mount(
    {
      getService: async () => service,
      getActivation: async (id, buildId) => {
        assert.deepEqual([id, buildId], calls[0].slice(0, 2));
        receiptReads++;
        throw new BrowserApiError("http_error", { status: 404 });
      },
      activateBuild: async (...args) => {
        calls.push(args);
        if (calls.length === 1) {
          throw new BrowserApiError("transport_failed", { outcomeUnknown: true });
        }
        service = {
          data: { ...service.data, restart_error_code: "git_push_failed" },
          etag: advancedTag,
        };
        throw new BrowserApiError("final_publication_failed", { status: 503 });
      },
    },
    { canManage: () => canManage },
  );
  await until(() => panel.select.children.length === 3 && !panel.stop.disabled);
  selectBuild(panel);
  panel.activate.click();
  await until(() => receiptReads === 1);
  canManage = false;
  await delay(800);
  canManage = true;
  panel.update();
  assert.equal(panel.retry.hidden, true);
  panel.refresh.click();
  await until(() => !panel.retry.hidden);
  panel.retry.click();
  await until(() => /refused the retry/.test(panel.status.textContent));
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[0], [calls[1][0], candidate, coldTag]);
  assert.deepEqual(calls[1], calls[0]);
  assert.equal(panel.dismiss.hidden, true);
  panel.refresh.click();
  await until(() => !panel.dismiss.hidden);
  assert.equal(hasPendingServiceActivation(storage), true);
  panel.dismiss.click();
  await until(() => !hasPendingServiceActivation(storage));
  assert.match(panel.status.textContent, /Dismissed original activation/);
  assert.equal(calls.length, 2);
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
