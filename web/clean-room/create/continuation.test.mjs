/// <reference types="node" />
import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import test from "node:test";
import { setImmediate } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { createEntryFixture } from "../fixtures.ts";

const mockExports = {
  "board/read": ["readBoard"],
  "create/render": ["mountCreate"],
  "create/write": ["createCard", "inspectCardCreation"],
  "drawer/read": ["readDrawer", "readDrawerTicket"],
  "drawer/render": ["mountDrawer"],
  "live/controller": ["startLive"],
  "workspace/read": ["readSession", "readService", "readWorkspace"],
  "workspace/render": ["mountWorkspace"],
};
const { outputFiles } = await build({
  entryPoints: [fileURLToPath(new URL("../main.ts", import.meta.url))],
  bundle: true,
  format: "esm",
  write: false,
  plugins: [
    {
      name: "create-race-fixture",
      setup(plugin) {
        plugin.onResolve(
          {
            filter:
              /^\.\/(board\/read|create\/render|create\/write|drawer\/read|drawer\/render|live\/controller|workspace\/read|workspace\/render)\.js$/,
          },
          ({ path }) => ({ path: path.slice(2, -3), namespace: "create-race-fixture" }),
        );
        plugin.onLoad({ filter: /.*/, namespace: "create-race-fixture" }, ({ path }) => {
          const names = mockExports[/** @type {keyof typeof mockExports} */ (path)];
          return {
            contents: names
              .map((name) => `export const ${name} = globalThis.createRaceFixture.${name};`)
              .join("\n"),
          };
        });
      },
    },
  ],
});
const compiled = outputFiles[0];
assert.ok(compiled);
const moduleUrl = `data:text/javascript;base64,${Buffer.from(compiled.contents).toString("base64")}`;

class Element extends EventTarget {
  style = { setProperty() {} };
  append() {}
  querySelector() {
    return null;
  }
  setAttribute() {}
}

for (const delayed of ["submit", "inspect", "navigate", "ordinary success"]) {
  const scenario =
    delayed === "ordinary success"
      ? "a confirmed create closes the draft and leaves the board open"
      : delayed === "navigate"
        ? "a late create result leaves the newly selected board alone"
        : `a late create ${delayed} result cannot clear a new principal's draft`;
  void test(scenario, async (context) => {
    const fixture = createEntryFixture();
    const { board, service, workspace } = fixture;
    assert.equal(board.status, "ready");
    assert.equal(service.status, "ready");
    assert.equal(workspace.status, "ready");
    const lane = board.current.value.view.lanes[0];
    const card = board.current.value.view.cards[0];
    const beta = workspace.current.value.boards[1];
    assert.ok(lane && card && beta);
    const oldActor = fixture.session;
    const newActor = { ...oldActor, principal_id: "58700000-0000-4000-8000-000000000001" };
    let actor = oldActor;
    /** @type {import("../contracts.js").WorkspaceProps | undefined} */
    let renderedWorkspace;
    /** @type {import("./contracts.js").CreateProps | undefined} */
    let renderedCreate;
    /** @type {(() => void) | undefined} */
    let release;
    const response = new Promise((resolve) => {
      release = () => resolve(undefined);
    });
    const receipt = { outcome: "committed", value: { card, cardEtag: '"created_card_etag"' } };
    let inspections = 0;
    const readBoards = [];
    const location = { pathname: `/board/${board.current.value.view.board.id}` };
    const hooks = {
      async readBoard(/** @type {string} */ id) {
        readBoards.push(id);
        return board.current.value;
      },
      readService: async () => service.current.value,
      readWorkspace: async () => workspace.current.value,
      readSession: async () => actor,
      async readDrawer() {
        throw new Error("Drawer fixture has no data");
      },
      mountDrawer() {
        return { update() {}, dispose() {} };
      },
      startLive(/** @type {{onConnection: (value: "connected") => void}} */ live) {
        return {
          update() {
            live.onConnection("connected");
          },
          retry() {},
          dispose() {},
        };
      },
      mountWorkspace(
        /** @type {Element} */ _host,
        /** @type {import("../contracts.js").WorkspaceProps} */ props,
      ) {
        renderedWorkspace = props;
        return {
          update(/** @type {typeof props} */ next) {
            renderedWorkspace = next;
          },
        };
      },
      mountCreate(
        /** @type {Element} */ _host,
        /** @type {import("./contracts.js").CreateProps} */ props,
      ) {
        renderedCreate = props;
        return {
          update(/** @type {typeof props} */ next) {
            renderedCreate = next;
          },
          dispose() {
            renderedCreate = undefined;
          },
        };
      },
      async createCard(
        /** @type {import("./contracts.js").CreateInput} */ _intent,
        /** @type {AbortSignal | undefined} */ _signal,
        /** @type {(session: import("../api-types.js").SessionDto) => void} */ beforeDispatch,
      ) {
        beforeDispatch(actor);
        if (delayed === "inspect") return { outcome: "unknown" };
        await response;
        return receipt;
      },
      async inspectCardCreation() {
        inspections += 1;
        await response;
        return receipt;
      },
    };
    const globals = {
      createRaceFixture: hooks,
      document: { createElement: () => new Element(), getElementById: () => new Element() },
      location,
      history: {
        state: null,
        replaceState() {},
        pushState(
          /** @type {unknown} */ _state,
          /** @type {string} */ _title,
          /** @type {string} */ path,
        ) {
          location.pathname = path;
        },
      },
      window: { setTimeout: () => 0, clearTimeout },
      addEventListener() {},
    };
    for (const [key, value] of Object.entries(globals)) {
      const descriptor = Object.getOwnPropertyDescriptor(globalThis, key);
      Object.defineProperty(globalThis, key, { configurable: true, value });
      context.after(() => {
        if (descriptor) Object.defineProperty(globalThis, key, descriptor);
        else Reflect.deleteProperty(globalThis, key);
      });
    }
    await import(`${moduleUrl}#${delayed}`);
    await setImmediate();
    assert.equal(renderedWorkspace?.selectedBoard?.writable, true);
    renderedWorkspace?.selectedBoard?.onLaneAction?.("create", lane.id);
    assert.ok(renderedCreate);
    renderedCreate.onDraftChange({ ...renderedCreate.draft, title: "Original actor's card" });
    renderedCreate.onSubmit();
    await setImmediate();
    assert.equal(renderedCreate.phase, "submitting");
    assert.equal(inspections, delayed === "inspect" ? 1 : 0);

    if (delayed === "ordinary success") {
      assert.ok(release);
      release();
      await setImmediate();
      assert.equal(renderedCreate, undefined);
      assert.equal(location.pathname, `/board/${board.current.value.view.board.id}`);
      assert.equal(
        renderedWorkspace?.route.kind === "board" ? renderedWorkspace.route.cardId : undefined,
        null,
      );
      return;
    }

    if (delayed === "navigate") {
      renderedWorkspace?.onNavigate({
        route: { kind: "board", boardId: beta.id, cardId: null, search: "", showCompleted: false },
        history: "push",
      });
      await setImmediate();
      assert.equal(renderedCreate, undefined);
      const count = readBoards.length;
      assert.ok(release);
      release();
      await setImmediate();
      assert.equal(location.pathname, `/board/${beta.id}`);
      assert.equal(readBoards.length, count);
      return;
    }

    actor = newActor;
    renderedWorkspace.onRefresh();
    await setImmediate();
    assert.equal(renderedWorkspace?.selectedBoard?.writable, true);
    assert.equal(renderedCreate, undefined);
    renderedWorkspace?.selectedBoard?.onLaneAction?.("create", lane.id);
    const current = /** @type {import("./contracts.js").CreateProps | undefined} */ (
      renderedCreate
    );
    assert.ok(current);
    current.onDraftChange({ ...current.draft, title: "New actor's unfinished draft" });
    assert.ok(release);
    release();
    await setImmediate();
    assert.equal(current.phase, "editing");
    const after = /** @type {import("./contracts.js").CreateProps | undefined} */ (renderedCreate);
    assert.equal(after?.phase, "editing");
    assert.equal(after?.draft.title, "New actor's unfinished draft");
  });
}
