/// <reference types="node" />
import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const first = "20000000-0000-4000-8000-000000000001";
const second = "20000000-0000-4000-8000-000000000002";
const third = "20000000-0000-4000-8000-000000000003";
const hidden = "20000000-0000-4000-8000-000000000004";
const actor = "10000000-0000-4000-8000-000000000001";
const originalTag = '"workspace-revision-40"';
const nextTag = '"workspace-revision-41"';
const publishing = {
  enabled: false,
  repository: null,
  last_success_at: null,
  pending: false,
  error_code: null,
};

/** @param {string} id @param {string} name @returns {import("../api-types.js").BoardSummaryDto} */
function board(id, name) {
  return {
    id,
    name,
    revision: 1,
    availability: "open",
    attention: { blocked: 1, needs_input: 0, ready_for_review: 0 },
    last_card_edit_at: "2026-11-13 00:00:00+00:00",
    error_code: null,
  };
}

const boards = [board(first, "First board"), board(second, "Second board"), board(third, "Third")];
const { outputFiles } = await build({
  entryPoints: [fileURLToPath(new URL("./pins.ts", import.meta.url))],
  bundle: true,
  format: "esm",
  write: false,
});
const compiled = outputFiles[0];
assert.ok(compiled);
const moduleUrl = `data:text/javascript;base64,${Buffer.from(compiled.contents).toString("base64")}`;

/** @param {string} id @returns {Promise<typeof import("./pins.js")>} */
async function adapter(id) {
  const bundled = /** @type {unknown} */ (await import(`${moduleUrl}#${id}`));
  return /** @type {typeof import("./pins.js")} */ (bundled);
}

void test("pins use saved order, other boards keep catalog order, and search leaves the catalog intact", async () => {
  const { projectPinBoards, reorderPins } = await adapter("projection");
  const order = [third, hidden, second];
  const unfiltered = projectPinBoards(boards, order, "", true);
  assert.deepEqual(
    unfiltered.map(({ summary, pinned, canReorder }) => [summary.id, pinned, canReorder]),
    [
      [third, true, true],
      [second, true, true],
      [first, false, false],
    ],
  );
  const filtered = projectPinBoards(boards, order, " BOARD ", true);
  assert.deepEqual(
    filtered.map(({ summary }) => summary.id),
    [second, first],
  );
  assert.equal(filtered[0]?.canReorder, false);
  assert.equal(filtered[0]?.summary, boards[1]);
  assert.deepEqual(reorderPins(order, second, third, "before"), [second, third, hidden]);
  assert.deepEqual(order, [third, hidden, second]);
  assert.equal(reorderPins(order, third, first, "after"), order);
});

class FakeElement extends EventTarget {
  dataset = {};
  textContent = "";
  value = "";
  hidden = false;
  disabled = false;
  isConnected = true;
  classList = { toggle() {} };
  /** @type {FakeElement[]} */
  children = [];

  /** @param {FakeElement[]} children */
  append(...children) {
    this.children.push(...children);
  }

  setAttribute() {}
  removeAttribute() {}
  after() {}
  focus() {}
  remove() {}
  closest() {
    return this;
  }
}

/** @param {import("node:test").TestContext} context @param {"refused" | "committed" | "unknown" | "superseded" | "live-before-read" | "live-during-read"} disposition */
async function runSave(context, disposition) {
  const { mountPins } = await adapter(disposition);
  const pin = Object.assign(new FakeElement(), { dataset: { pinAction: second } });
  const search = new FakeElement();
  const list = new FakeElement();
  const status = new FakeElement();
  const documentMock = { activeElement: pin, createElement: () => new FakeElement() };
  Object.defineProperty(globalThis, "Element", { value: FakeElement, configurable: true });
  Object.defineProperty(globalThis, "document", { value: documentMock, configurable: true });
  context.after(() => {
    Reflect.deleteProperty(globalThis, "Element");
    Reflect.deleteProperty(globalThis, "document");
  });
  /** @type {string[]} */
  const calls = [];
  const base = { revision: 40, pinned_board_ids: [first], publishing };
  const saved = { revision: 41, pinned_board_ids: [first, second], publishing };
  const superseded = { revision: 41, pinned_board_ids: [third], publishing };
  let didPublish = false;
  let rendered = 0;
  /** @type {unknown} */
  let patch;
  /** @type {typeof fetch} */
  const fetcher = async (path, options) => {
    const method = options?.method;
    assert.ok(typeof path === "string" && options?.headers instanceof Headers);
    if (path.endsWith("/browser-session")) {
      return Response.json({
        api_version: "2026-11-10",
        principal_id: actor,
        kind: "human",
        workspace_admin: false,
        access_token: Buffer.alloc(32, 1).toString("base64url"),
        expires_on_service_replacement: true,
      });
    }
    assert.equal(path, "/api/2026-11-10/workspace");
    calls.push(method ?? "");
    if (method === "GET") {
      if (disposition === "live-during-read") publish();
      const current =
        disposition === "committed" ? saved : disposition === "superseded" ? superseded : base;
      return Response.json(current, {
        headers: { ETag: current === base ? originalTag : nextTag },
      });
    }
    assert.equal(method, "PATCH");
    assert.equal(options.headers.get("If-Match"), originalTag);
    assert.ok(typeof options.body === "string");
    patch = JSON.parse(options.body);
    if (disposition !== "refused") throw new Error("response lost");
    const requestId = options.headers.get("X-Request-ID");
    assert.ok(requestId);
    return Response.json(
      {
        type: "about:blank",
        title: "Precondition failed",
        status: 412,
        code: "precondition_failed",
        detail: "The saved workspace changed.",
        request_id: requestId,
        outcome: "not_applied",
        errors: [],
      },
      {
        status: 412,
        headers: { "Content-Type": "application/problem+json", "X-Request-ID": requestId },
      },
    );
  };
  context.mock.method(globalThis, "fetch", fetcher);

  /** @type {import("../contracts.js").ReadState<import("../contracts.js").WorkspaceData>} */
  let data = {
    status: "ready",
    current: { value: { workspace: base, workspaceEtag: originalTag, boards }, confirmedAt: "" },
  };
  /** @type {readonly { summary: import("../api-types.js").BoardSummaryDto; pinned: boolean; canToggle: boolean }[]} */
  let rows = [];
  const component = mountPins({
    search: /** @type {HTMLInputElement} */ (/** @type {unknown} */ (search)),
    list: /** @type {HTMLElement} */ (/** @type {unknown} */ (list)),
    status: /** @type {HTMLElement} */ (/** @type {unknown} */ (status)),
    render: () => {
      rendered += 1;
      if (disposition === "live-before-read" && rendered === 2) publish();
      else rows = component.update(data, true);
    },
  });
  function publish() {
    didPublish = true;
    data = {
      status: "ready",
      current: { value: { workspace: saved, workspaceEtag: nextTag, boards }, confirmedAt: "" },
    };
    rows = component.update(data, true);
  }
  rows = component.update(data, true);
  function clickPin() {
    const event = new Event("click");
    Object.defineProperty(event, "target", { value: pin });
    list.dispatchEvent(event);
  }

  clickPin();
  assert.deepEqual(
    rows.filter(({ pinned }) => pinned).map(({ summary }) => summary.id),
    [first, second],
  );
  await new Promise((resolve) => {
    setImmediate(resolve);
  });
  assert.deepEqual(patch, { pinned_board_ids: [first, second] });
  assert.deepEqual(calls, disposition === "live-before-read" ? ["PATCH"] : ["PATCH", "GET"]);
  const matched = disposition === "committed" || didPublish;
  assert.deepEqual(
    rows.filter(({ pinned }) => pinned).map(({ summary }) => summary.id),
    matched ? [first, second] : disposition === "superseded" ? [third] : [first],
  );
  assert.equal(
    rows.some(({ canToggle }) => canToggle),
    disposition !== "unknown",
  );
  if (disposition === "unknown") {
    clickPin();
    assert.deepEqual(calls, ["PATCH", "GET"]);
  }
  const feedback = status.children[0]?.textContent;
  if (matched) assert.equal(feedback, "Current pinned order matches the requested order.");
  else assert.match(feedback ?? "", /Requested change: Second board pinned at position 2\./u);
  component.dispose();
}

for (const disposition of /** @type {const} */ ([
  "refused",
  "committed",
  "unknown",
  "superseded",
  "live-before-read",
  "live-during-read",
])) {
  void test(`pin recovery uses one conditional write and focused readback for ${disposition}`, (context) =>
    runSave(context, disposition));
}
