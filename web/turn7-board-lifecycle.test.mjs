import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  entryPoints: [fileURLToPath(new URL("./src/board-lifecycle.ts", import.meta.url))],
  bundle: true,
  write: false,
  format: "esm",
});
const { mountBoardLifecycle } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

class Element extends EventTarget {
  replaceChildren(...children) {
    this.children = children;
  }
  setAttribute() {}
  click() {
    if (!this.disabled && !this.hidden) this.dispatchEvent(new Event("click"));
  }
}

test("a remounted lifecycle follows the detached close's journal settlement", async (t) => {
  const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
  const tag = (revision) => `"${boardId}-${revision}"`;
  const key = `localswim:pending-board-availability:${boardId}`;
  const storage = new Map();
  const original = Object.getOwnPropertyDescriptor(globalThis, "sessionStorage");
  Object.defineProperty(globalThis, "sessionStorage", {
    configurable: true,
    value: {
      getItem: (id) => storage.get(id) ?? null,
      setItem: (id, value) => storage.set(id, value),
      removeItem: (id) => storage.delete(id),
    },
  });
  t.after(() => {
    if (original) Object.defineProperty(globalThis, "sessionStorage", original);
    else Reflect.deleteProperty(globalThis, "sessionStorage");
  });

  let context = { canChange: true, availability: "open", etag: tag(1) };
  const close = Promise.withResolvers();
  const writes = [];
  let leased = false;
  function begin() {
    if (leased) return null;
    leased = true;
    return () => {
      leased = false;
    };
  }
  function mount() {
    const life = new AbortController();
    const root = new Element();
    root.ownerDocument = { createElement: () => new Element() };
    const controller = mountBoardLifecycle(root, {
      api: {
        setBoardAvailability: (id, availability, etag) => {
          writes.push({ id, availability, etag });
          return close.promise;
        },
      },
      boardId,
      signal: life.signal,
      readWithSession: (read) => read(),
      context: () => context,
      onChange: () => {},
      beginWrite: begin,
      beginInspection: begin,
    });
    const [change, , inspect, status] = root.children;
    return { ...controller, life, change, inspect, status };
  }

  const detached = mount();
  detached.change.click();
  detached.change.click();
  assert.deepEqual(writes, [{ id: boardId, availability: "closed", etag: tag(1) }]);
  detached.update();
  assert.equal(detached.status.textContent, "Closing board…");
  detached.life.abort();

  const active = mount();
  context = { canChange: true, availability: "closed", etag: tag(2) };
  active.update();
  assert.equal(active.inspect.hidden, false);
  assert.equal(active.blocksWrites(), true);

  close.resolve({ data: { id: boardId, availability: "closed" }, etag: tag(2) });
  await flush();
  assert.equal(storage.has(key), false);
  active.update();
  assert.equal(active.inspect.hidden, true);
  assert.equal(active.blocksWrites(), false);
  assert.equal(active.change.textContent, "Reopen board");
  assert.equal(active.change.disabled, false);

  storage.set(key, JSON.stringify({ availability: "open", etag: tag(2), attempt: "next" }));
  active.update();
  assert.equal(active.inspect.hidden, false);
  assert.equal(active.blocksWrites(), true);
  assert.equal(active.change.hidden, true);
  assert.equal(writes.length, 1);
});
