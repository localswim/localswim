/// <reference types="node" />
import assert from "node:assert/strict";
import { test } from "node:test";
import { mountWorkspaceStatus } from "./status.ts";

void test("connection status honors the grace period, reconnect and disposal", (context) => {
  context.mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  const page = { title: "localswim" };
  Object.defineProperty(globalThis, "document", { configurable: true, value: page });
  Object.defineProperty(globalThis, "window", { configurable: true, value: globalThis });
  context.after(() => {
    Reflect.deleteProperty(globalThis, "document");
    Reflect.deleteProperty(globalThis, "window");
  });

  const indicator = { connected: false };
  const root = {
    dataset: { connection: "" },
    classList: {
      /** @param {string} className @param {boolean} connected */
      toggle(className, connected) {
        assert.equal(className, "lsw-workspace-connected");
        indicator.connected = connected;
      },
    },
  };
  const elements = {
    build: { textContent: "" },
    latest: { textContent: "" },
    clock: { textContent: "" },
    status: { textContent: "" },
  };
  const presenter = mountWorkspaceStatus(
    /** @type {HTMLElement} */ (/** @type {unknown} */ (root)),
    /** @type {Parameters<typeof mountWorkspaceStatus>[1]} */ (/** @type {unknown} */ (elements)),
  );
  /** @param {import("./controller.ts").LiveConnection} connection */
  function update(connection) {
    presenter.update({ connection, buildId: null, lastCardEdit: null, title: "Parity Alpha" });
  }

  update("connecting");
  assert.equal(indicator.connected, false);
  update("connected");
  assert.equal(indicator.connected, true);
  update("disconnected");
  context.mock.timers.tick(9_999);
  update("disconnected");
  assert.equal(indicator.connected, true);
  context.mock.timers.tick(1);
  assert.equal(indicator.connected, false);
  assert.equal(root.dataset.connection, "disconnected");

  update("connected");
  update("disconnected");
  context.mock.timers.tick(5_000);
  update("connected");
  context.mock.timers.tick(10_000);
  assert.equal(indicator.connected, true);

  update("disconnected");
  presenter.dispose();
  context.mock.timers.tick(10_000);
  assert.equal(indicator.connected, true);
  assert.equal(page.title, "localswim");
});
