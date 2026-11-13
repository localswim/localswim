import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { mountManualRefresh } from "./src/manual-refresh.ts";

class Element extends EventTarget {
  attributes = new Map();
  children = [];
  dataset = {};
  textContent = "";
  ownerDocument = { createElement: () => new Element() };

  append(...children) {
    this.children.push(...children);
  }

  setAttribute(name, value) {
    this.attributes.set(name, value);
  }

  removeAttribute(name) {
    this.attributes.delete(name);
  }

  click() {
    this.dispatchEvent(new Event("click"));
  }
}

test("manual board refresh coalesces repeated activation until confirmation settles", async () => {
  const host = new Element();
  const signal = new AbortController().signal;
  const first = Promise.withResolvers();
  let requests = 0;
  mountManualRefresh(host, {
    signal,
    scope: "board",
    refresh: () => (++requests === 1 ? first.promise : Promise.resolve(true)),
  });
  const [button, , status] = host.children;
  assert.equal(button.textContent, "Refresh");
  assert.equal(button.dataset.manualRefresh, "board");
  assert.equal(status.attributes.get("role"), "status");

  button.click();
  button.click();
  assert.equal(requests, 1);
  assert.equal(status.textContent, "Refreshing board…");
  assert.equal(button.attributes.get("aria-disabled"), "true");
  first.resolve(false);
  await flush();
  assert.equal(status.textContent, "Could not refresh board.");
  assert.equal(button.attributes.has("aria-disabled"), false);

  button.click();
  await flush();
  assert.equal(requests, 2);
  assert.equal(status.textContent, "Board refreshed.");
  assert.equal(button.attributes.has("aria-disabled"), false);
});

test("manual workspace refresh ignores a late rejection after navigation", async () => {
  const host = new Element();
  const route = new AbortController();
  const read = Promise.withResolvers();
  let requests = 0;
  mountManualRefresh(host, {
    signal: route.signal,
    scope: "workspace",
    refresh: () => {
      requests++;
      return read.promise;
    },
  });
  const [button, , status] = host.children;
  assert.equal(button.dataset.manualRefresh, "workspace");
  button.click();
  assert.equal(status.textContent, "Refreshing workspace…");

  route.abort();
  read.reject(new Error("Request aborted"));
  await flush();
  button.click();
  assert.equal(requests, 1);
  assert.equal(status.textContent, "Refreshing workspace…");
});
