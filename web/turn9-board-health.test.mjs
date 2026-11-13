import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate as flush } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents:
      'export { ApiError } from "./api.mjs"; export { createBoardState } from "./src/board-state.ts";',
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  write: false,
  format: "esm",
});
const { ApiError, createBoardState } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const policyId = "22e1ffb8-7d2a-43ed-a8cf-8871165a98e9";
const tag = (revision) => JSON.stringify(boardId + "-" + revision);
const board = {
  id: boardId,
  name: "Board",
  revision: 1,
  policy_id: policyId,
  default_owner_id: policyId,
  user_css: "",
};
const retained = { board, etag: tag(1), lanes: [], cards: [], priorities: [], users: [] };
const healthy = {
  id: boardId,
  name: board.name,
  revision: 1,
  availability: "open",
  errorCode: null,
};
const unhealthy = { ...healthy, availability: "unhealthy", errorCode: "durable_state_unavailable" };

function setup(t, options = {}) {
  let metadata = { ...board, availability: "open" };
  let denied = false;
  let snapshot;
  let fullReads = 0;
  const signal = new AbortController().signal;
  const api = {
    async getBoardAvailability() {
      if (denied) throw new ApiError("http_error", { status: 403 });
      return { data: metadata, etag: tag(metadata.revision) };
    },
    async getBoardLanes() {
      fullReads++;
      return { data: { board: metadata, lanes: [], cards: [] }, etag: tag(metadata.revision) };
    },
    async getBoardIfChanged() {
      return { data: null, etag: tag(metadata.revision) };
    },
    async listPriorities() {
      return { data: { priorities: [], next_cursor: null } };
    },
    async listUsers() {
      return { data: { users: [], next_cursor: null } };
    },
    async getVerification() {
      throw new Error("Private verification failure");
    },
    ...options.api,
  };
  const state = createBoardState({
    boardId,
    signal,
    api,
    readWithSession: (read) => read(),
    onChange: (view) => {
      snapshot = view;
    },
    retained: options.retained,
  });
  t.after(state.dispose);
  state.watch.report("connected");
  return {
    state,
    get view() {
      return snapshot;
    },
    get fullReads() {
      return fullReads;
    },
    metadata(availability) {
      metadata = { ...metadata, availability };
    },
    deny() {
      denied = true;
    },
    catalog(...boards) {
      state.observeCatalog({ status: "ready", boards });
    },
  };
}

test("a confirmed board stays visible and fenced until a full same-revision recovery read", async (t) => {
  const app = setup(t);
  app.catalog(healthy);
  app.state.start();
  await app.state.retry();
  const confirmed = app.view.data;
  assert.ok(confirmed);
  assert.equal(app.view.writesFenced, false);

  app.catalog({ ...unhealthy, errorCode: "durable_state_changed" });
  assert.equal(app.view.data, confirmed);
  assert.equal(app.view.writesFenced, true);
  assert.equal(app.view.lifecycleReady, false);
  assert.match(app.view.healthDiagnostic, /differs from the last accepted state/);
  await app.state.retry();
  assert.equal(app.fullReads, 1);

  app.catalog(healthy);
  assert.equal(app.view.availability, "unhealthy");
  assert.equal(app.view.writesFenced, true);
  await app.state.retry();
  assert.equal(app.fullReads, 2);
  assert.equal(app.view.availability, "open");
  assert.equal(app.view.healthDiagnostic, null);
  assert.equal(app.view.writesFenced, false);
});

test("a same-board cached snapshot stays read-only and verification uses fixed copy", async (t) => {
  const verification = Promise.withResolvers();
  const app = setup(t, {
    retained,
    api: { getVerification: () => verification.promise },
  });
  app.catalog(unhealthy);
  app.state.start();
  await app.state.retry();
  assert.equal(app.view.data, retained);
  assert.equal(app.view.writesFenced, true);
  assert.match(app.view.healthDiagnostic, /snapshot is unavailable/);

  verification.resolve({
    data: {
      board_id: boardId,
      valid: false,
      revision: null,
      issues: [{ code: "integrity_failure", field: "/private/path" }],
    },
  });
  await flush();
  assert.equal(app.view.healthDiagnostic, "The saved snapshot failed integrity verification.");
  assert.equal(app.view.data, retained);
});

test("startup quarantine and loss of read admission remove retained content", async (t) => {
  const app = setup(t, { retained });
  app.catalog({ ...unhealthy, name: null, revision: null });
  app.state.start();
  await app.state.retry();
  assert.equal(app.view.startupQuarantined, true);
  assert.equal(app.view.data, null);
  assert.equal(app.view.name, null);
  assert.equal(app.view.policyId, null);
  assert.equal(app.view.writesFenced, true);
  assert.match(app.view.healthDiagnostic, /saved snapshot at startup/);

  app.catalog(healthy);
  await app.state.retry();
  assert.ok(app.view.data);
  app.deny();
  app.catalog(unhealthy);
  await app.state.retry();
  assert.equal(app.view.availability, "unavailable");
  assert.equal(app.view.data, null);
  assert.equal(app.view.policyId, null);
  assert.equal(app.view.writesFenced, true);
});

test("focused unhealthy metadata fences retained data before catalog catches up", async (t) => {
  const app = setup(t, { retained });
  app.metadata("unhealthy");
  app.catalog(healthy);
  app.state.start();
  await app.state.retry();
  assert.equal(app.view.data, retained);
  assert.equal(app.view.writesFenced, true);
  assert.equal(app.view.availability, "unhealthy");
  assert.equal(app.view.healthDiagnostic, "This board’s saved state is unhealthy.");
  assert.equal(app.fullReads, 0);
  app.catalog();
  assert.equal(app.view.data, null);
  assert.equal(app.view.name, null);
  assert.equal(app.view.availability, "unavailable");
});
