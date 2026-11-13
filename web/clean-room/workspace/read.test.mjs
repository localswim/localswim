/// <reference types="node" />
import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const prefix = "/api/2026-11-10";
const actorId = "10000000-0000-4000-8000-000000000001";
const firstBoardId = "20000000-0000-4000-8000-000000000001";
const secondBoardId = "20000000-0000-4000-8000-000000000002";
const buildId = "30000000-0000-4000-8000-000000000001";
const requestId = "40000000-0000-4000-8000-000000000001";
const serviceId = "019a6f6b-83c0-7abc-8def-0123456789ab";
const firstToken = Buffer.alloc(32, 1).toString("base64url");
const secondToken = Buffer.alloc(32, 2).toString("base64url");
const tag = '"sample-strong-validator"';
const session = {
  principal_id: actorId,
  kind: "human",
  api_version: "2026-11-10",
  workspace_admin: true,
};
const workspace = {
  revision: 12,
  pinned_board_ids: [firstBoardId],
  publishing: {
    enabled: false,
    repository: null,
    last_success_at: null,
    pending: false,
    error_code: null,
  },
};
const service = {
  port: 53100,
  service_instance_id: serviceId,
  build_id: buildId,
  state: "running",
  api_version: "2026-11-10",
  restart_error_code: null,
};
const openBoard = {
  id: firstBoardId,
  name: "  Open board 🏊  ",
  revision: 15,
  availability: "open",
  attention: { blocked: 1, needs_input: 2, ready_for_review: 3 },
  last_card_edit_at: "2028-02-29 13:14:15+00:00",
  error_code: null,
};
const failedBoard = {
  id: secondBoardId,
  name: null,
  revision: null,
  availability: "unhealthy",
  attention: null,
  last_card_edit_at: null,
  error_code: "durable_state_unavailable",
};

const { outputFiles } = await build({
  entryPoints: [fileURLToPath(new URL("./read.ts", import.meta.url))],
  bundle: true,
  format: "esm",
  write: false,
});
const compiled = outputFiles[0];
assert.ok(compiled);
const moduleUrl = `data:text/javascript;base64,${Buffer.from(compiled.contents).toString("base64")}`;
let importId = 0;

/**
 * @param {import("node:test").TestContext} context
 * @param {(path: string, headers: Headers, signal: AbortSignal) => Response | Promise<Response>} fetcher
 * @returns {Promise<typeof import("./read.js")>}
 */
async function adapter(context, fetcher) {
  /** @type {typeof fetch} */
  const mockFetch = async (url, options) => {
    assert.equal(typeof url, "string");
    assert.ok(typeof url === "string" && options?.headers instanceof Headers);
    assert.ok(options.signal instanceof AbortSignal);
    assert.ok(url.startsWith(prefix + "/"));
    assert.equal(options.mode, "same-origin");
    assert.equal(options.credentials, "omit");
    assert.equal(options.redirect, "error");
    assert.equal(options.cache, "no-store");
    assert.equal(options.body, undefined);
    assert.equal(options.headers.get("Accept"), "application/json, application/problem+json");
    if (url === `${prefix}/browser-session`) {
      assert.equal(options.method, "POST");
      assert.equal(options.headers.get("Authorization"), null);
    } else {
      assert.equal(options.method, "GET");
      const authorization = options.headers.get("Authorization");
      assert.ok(
        authorization === `Bearer ${firstToken}` || authorization === `Bearer ${secondToken}`,
      );
    }
    return fetcher(url.slice(prefix.length), options.headers, options.signal);
  };
  context.mock.method(globalThis, "fetch", mockFetch);
  importId += 1;
  const bundled = /** @type {unknown} */ (await import(`${moduleUrl}#${importId}`));
  // The URL contains a fresh bundle of the module named in the return type.
  return /** @type {typeof import("./read.js")} */ (bundled);
}

/** @param {unknown} value @param {string | null} etag */
function json(value, etag = tag) {
  const headers = new Headers();
  if (etag !== null) headers.set("ETag", etag);
  return Response.json(value, { headers });
}

function bootstrap(token = firstToken) {
  return json(
    {
      ...session,
      access_token: token,
      expires_on_service_replacement: true,
    },
    null,
  );
}

/** @param {number} status @param {string} code @param {Record<string, unknown>} overrides */
function problem(status, code, overrides = {}) {
  const body = {
    type: "about:blank",
    title: "Request unavailable",
    status,
    code,
    detail: "Bounded native problem.",
    request_id: requestId,
    outcome: "not_applicable",
    errors: [],
    ...overrides,
  };
  return Response.json(body, {
    status,
    headers: { "Content-Type": "application/problem+json", "X-Request-ID": requestId },
  });
}

/** @param {typeof import("./read.js")} api @param {import("../contracts.js").ReadFailure["kind"]} kind */
function failureIs(api, kind) {
  /** @param {unknown} error */
  return (error) => {
    assert.equal(api.readFailure(error).kind, kind);
    return true;
  };
}

void test("concurrent reads share a credentialless bootstrap and validate the native resources", async (context) => {
  let bootstrapCalls = 0;
  /** @type {(string | null)[]} */
  const cursors = [];
  const nextCursor = "opaque +/= 日本";
  const api = await adapter(context, (path) => {
    if (path === "/browser-session") {
      bootstrapCalls += 1;
      return bootstrap();
    }
    if (path === "/session") return json(session, null);
    if (path === "/workspace") return json(workspace);
    if (path === "/service") return json(service);
    const url = new URL(path, "http://127.0.0.1");
    assert.equal(url.pathname, "/boards");
    assert.equal(url.searchParams.get("limit"), "200");
    const cursor = url.searchParams.get("cursor");
    cursors.push(cursor);
    return cursor === null
      ? json({ boards: [openBoard], next_cursor: nextCursor })
      : json({ boards: [failedBoard], next_cursor: null }, '"different-page-validator"');
  });

  const [confirmed, current, running] = await Promise.all([
    api.readSession(),
    api.readWorkspace(),
    api.readService(),
  ]);
  assert.deepEqual(confirmed, session);
  assert.deepEqual(current, { workspace, workspaceEtag: tag, boards: [openBoard, failedBoard] });
  assert.deepEqual(running, service);
  assert.deepEqual(cursors, [null, nextCursor]);
  assert.equal(bootstrapCalls, 1);
});

void test("simultaneous stale capabilities renew once without leaking to returned session data", async (context) => {
  let bootstrapCalls = 0;
  let firstTokenReads = 0;
  const api = await adapter(context, (path, headers) => {
    if (path === "/browser-session") {
      bootstrapCalls += 1;
      return bootstrap(bootstrapCalls === 1 ? firstToken : secondToken);
    }
    if (headers.get("Authorization") === `Bearer ${firstToken}`) {
      firstTokenReads += 1;
      return problem(401, "unauthorized");
    }
    if (path === "/session") return json(session, null);
    if (path === "/service") return json(service);
    throw new Error("unexpected route");
  });

  const results = await Promise.all([api.readSession(), api.readService()]);
  assert.deepEqual(results, [session, service]);
  assert.equal(firstTokenReads, 2);
  assert.equal(bootstrapCalls, 2);
});

void test("a second validated 401 ends the read with bounded HTTP details", async (context) => {
  let bootstrapCalls = 0;
  let getCalls = 0;
  const api = await adapter(context, (path) => {
    if (path === "/browser-session") {
      bootstrapCalls += 1;
      return bootstrap();
    }
    getCalls += 1;
    return problem(401, "unauthorized");
  });

  await assert.rejects(api.readSession(), (error) => {
    assert.ok(error instanceof Error);
    const failure = api.readFailure(error);
    assert.equal(failure.kind, "http");
    assert.ok(failure.kind === "http");
    assert.equal(failure.problem.code, "unauthorized");
    assert.equal(failure.problem.request_id, requestId);
    assert.equal(error.message, "http");
    return true;
  });
  assert.equal(bootstrapCalls, 2);
  assert.equal(getCalls, 2);
});

void test("canceling one reader leaves the shared bootstrap usable by another", async (context) => {
  /** @type {(response: Response) => void} */
  let finishBootstrap = () => {
    assert.fail("bootstrap fixture was not started");
  };
  /** @type {Promise<Response>} */
  const pending = new Promise((resolve) => {
    finishBootstrap = resolve;
  });
  /** @type {string[]} */
  const requests = [];
  const api = await adapter(context, (path) => {
    requests.push(path);
    if (path === "/browser-session") return pending;
    return json(service);
  });

  const controller = new AbortController();
  const canceled = api.readSession(controller.signal);
  const retained = api.readService();
  controller.abort();
  await assert.rejects(canceled, failureIs(api, "transport"));
  finishBootstrap(bootstrap());
  assert.deepEqual(await retained, service);
  assert.deepEqual(requests, ["/browser-session", "/service"]);
});

void test("read deadline and caller cancellation interrupt a stalled response body", async (context) => {
  /** @type {AbortController[]} */
  const deadlines = [];
  /** @type {typeof AbortSignal.timeout} */
  const timeout = (milliseconds) => {
    assert.equal(milliseconds, 15_000);
    const controller = new AbortController();
    deadlines.push(controller);
    return controller.signal;
  };
  context.mock.method(AbortSignal, "timeout", timeout);

  /** @type {AbortController | undefined} */
  let bodyStarted;
  const api = await adapter(context, (path, _headers, signal) => {
    if (path === "/browser-session") return bootstrap();
    assert.equal(path, "/service");
    return new Response(
      new ReadableStream({
        start(controller) {
          signal.addEventListener("abort", () => controller.error(signal.reason), { once: true });
          bodyStarted?.abort();
        },
      }),
      { headers: { "Content-Type": "application/json", ETag: tag } },
    );
  });

  for (const cancel of ["deadline", "caller"]) {
    bodyStarted = new AbortController();
    const started = new Promise((resolve) => {
      bodyStarted?.signal.addEventListener("abort", resolve, { once: true });
    });
    const caller = new AbortController();
    const deadlineIndex = deadlines.length;
    const result = api.readService(caller.signal);
    await started;
    const deadline = deadlines[deadlineIndex];
    assert.ok(deadline);
    (cancel === "deadline" ? deadline : caller).abort();
    await assert.rejects(result, failureIs(api, "transport"));
  }
  assert.equal(deadlines.length, 3);
});

void test("malformed bootstrap can recover, and session identity must match bootstrap", async (context) => {
  let bootstrapCalls = 0;
  let getCalls = 0;
  const api = await adapter(context, (path) => {
    if (path === "/browser-session") {
      bootstrapCalls += 1;
      return bootstrap(bootstrapCalls === 1 ? "A".repeat(42) + "B" : firstToken);
    }
    getCalls += 1;
    return json({ ...session, principal_id: firstBoardId }, null);
  });

  await assert.rejects(api.readSession(), failureIs(api, "invalid_response"));
  assert.equal(getCalls, 0);
  await assert.rejects(api.readSession(), failureIs(api, "invalid_response"));
  assert.equal(bootstrapCalls, 2);
  assert.equal(getCalls, 1);
});

void test("invalid HTTP data never triggers an authorization retry or exposes raw error text", async (context) => {
  let bootstrapCalls = 0;
  const api = await adapter(context, (path) => {
    if (path === "/browser-session") {
      bootstrapCalls += 1;
      return bootstrap();
    }
    if (path === "/session") return problem(401, "unauthorized", { request_id: firstBoardId });
    throw new Error("private transport detail");
  });

  await assert.rejects(api.readSession(), failureIs(api, "invalid_response"));
  await assert.rejects(api.readService(), (error) => {
    assert.ok(error instanceof Error);
    assert.deepEqual(api.readFailure(error), { kind: "transport" });
    assert.equal(error.message, "transport");
    return true;
  });
  assert.equal(bootstrapCalls, 1);
});

void test("catalog rejects malformed UTF-8 and preserves literal replacement characters", async (context) => {
  const literalName = "A \uFFFD board";
  const catalog = { boards: [{ ...openBoard, name: literalName }], next_cursor: null };
  const encoded = Buffer.from(JSON.stringify(catalog));
  const literalOffset = encoded.indexOf(Buffer.from("\uFFFD"));
  assert.ok(literalOffset >= 0);
  const invalid = Buffer.from(encoded);
  invalid[literalOffset + 1] = 0x20;
  let malformed = true;
  const api = await adapter(context, (path) => {
    if (path === "/browser-session") return bootstrap();
    if (path === "/workspace") return json(workspace);
    assert.equal(path, "/boards?limit=200");
    const bytes = malformed ? invalid : encoded;
    return new Response(
      new ReadableStream({
        start(controller) {
          // Split a character across the two chunks to exercise streaming decode.
          controller.enqueue(bytes.subarray(0, literalOffset + 1));
          controller.enqueue(bytes.subarray(literalOffset + 1));
          controller.close();
        },
      }),
      { headers: { "Content-Type": "application/json", ETag: tag } },
    );
  });

  await assert.rejects(api.readWorkspace(), failureIs(api, "invalid_response"));
  malformed = false;
  const result = await api.readWorkspace();
  assert.deepEqual(result.boards, catalog.boards);
});

void test("catalog rejects repeated rows, repeated cursors and malformed board data", async (context) => {
  let page = 0;
  let variant = "duplicate row";
  const api = await adapter(context, (path) => {
    if (path === "/browser-session") return bootstrap();
    if (path === "/workspace") return json(workspace);
    page += 1;
    if (variant === "malformed board") {
      return json({
        boards: [{ ...openBoard, last_card_edit_at: "2026-02-30 12:00:00+00:00" }],
        next_cursor: null,
      });
    }
    if (page === 1) return json({ boards: [openBoard], next_cursor: "cycle" });
    return variant === "duplicate row"
      ? json({ boards: [openBoard], next_cursor: null })
      : json({ boards: [failedBoard], next_cursor: "cycle" });
  });

  for (const scenario of ["duplicate row", "cursor cycle", "malformed board"]) {
    page = 0;
    variant = scenario;
    await assert.rejects(api.readWorkspace(), failureIs(api, "invalid_response"));
    assert.equal(page, scenario === "malformed board" ? 1 : 2);
  }
});

void test("catalog traversal stops at its explicit page limit", async (context) => {
  let page = 0;
  const api = await adapter(context, (path) => {
    if (path === "/browser-session") return bootstrap();
    if (path === "/workspace") return json(workspace);
    page += 1;
    const suffix = String(page).padStart(12, "0");
    return json({
      boards: [{ ...openBoard, id: `20000000-0000-4000-8000-${suffix}` }],
      next_cursor: `page-${page}`,
    });
  });

  await assert.rejects(api.readWorkspace(), failureIs(api, "invalid_response"));
  assert.equal(page, 100);
});

void test("workspace and service refuse invalid representations before exposing data", async (context) => {
  let workspaceResponse = json(workspace, `W/${tag}`);
  let serviceResponse = json({ ...service, service_instance_id: actorId });
  const api = await adapter(context, (path) => {
    if (path === "/browser-session") return bootstrap();
    if (path === "/workspace") return workspaceResponse;
    if (path === "/service") return serviceResponse;
    throw new Error("unexpected route");
  });

  await assert.rejects(api.readWorkspace(), failureIs(api, "invalid_response"));
  workspaceResponse = json({ ...workspace, pinned_board_ids: [firstBoardId, firstBoardId] });
  await assert.rejects(api.readWorkspace(), failureIs(api, "invalid_response"));
  await assert.rejects(api.readService(), failureIs(api, "invalid_response"));
  serviceResponse = json(service, null);
  await assert.rejects(api.readService(), failureIs(api, "invalid_response"));
});
