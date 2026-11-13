import assert from "node:assert/strict";
import test from "node:test";
import { ApiError, createBrowserApi } from "./api.mjs";
const board = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const lane = "ceab10a8-1492-4c90-aa07-13b83138919a";
const first = "d2ef6295-ace1-4e6e-bbf7-b1c1823f56fc";
const second = "163b1fa0-2a32-424b-8489-6bbd7761201c";
const backup = "f7e04b53-cfd9-717c-af8e-b6316af0a085";
const publication = "84b4ae21-e224-7f5a-9fca-9abf8e0c86bc";
const at = "2026-11-10 12:00:00+00:00";
const tag = (id, revision) => `"${id}-${revision}"`;
const client = (fetchImpl) =>
  createBrowserApi({ getCapability: () => "test-capability", fetchImpl });
const proposed = [
  { card_id: second, ticket: 1 },
  { card_id: first, ticket: 2 },
];
const current = [
  { card_id: first, ticket: 1 },
  { card_id: second, ticket: 2 },
];
const order = {
  board_id: board,
  lane_id: lane,
  board_revision: 4,
  selected_card_ids: [first, second],
  current,
  proposed,
  changed: true,
};
const published = {
  id: publication,
  board_id: board,
  workspace: false,
  state: "succeeded",
  finished_at: at,
  backup_id: backup,
  error_code: null,
};

test("search preserves literal UTF-8 query and explicit content/triage/lane/limit filters", async () => {
  const api = client(async (path, request) => {
    const url = new URL(path, "http://localhost");
    assert.equal(request.method, "GET");
    assert.equal(url.searchParams.get("q"), "#12 + café");
    assert.equal(url.searchParams.get("search_content"), "true");
    assert.equal(url.searchParams.get("sort"), "triage");
    assert.equal(url.searchParams.get("lane_id"), lane);
    assert.equal(url.searchParams.get("limit"), "2");
    return Response.json({ cards: [], next_cursor: null });
  });
  await api.listCards(board, {
    q: "#12 + café",
    searchContent: true,
    sort: "triage",
    laneId: lane,
    limit: 2,
  });
});

test("relationship lookup keeps default ticket order and refuses invalid explicit filters locally", async () => {
  let calls = 0;
  const api = client(async (path) => {
    calls++;
    const url = new URL(path, "http://localhost");
    assert.equal(url.searchParams.has("sort"), false);
    assert.equal(url.searchParams.get("ticket"), "12");
    return Response.json({ cards: [], next_cursor: null });
  });
  await api.listCards(board, { ticket: 12 });
  for (const filters of [
    { sort: "invalid" },
    { limit: 0 },
    { limit: true },
    { limit: 201 },
    { laneId: "invalid" },
  ]) {
    assert.throws(() => api.listCards(board, filters), TypeError);
  }
  assert.equal(calls, 1);
});

test("apply uses captured preview validator and ordered proposed UUIDs in one PUT", async () => {
  let calls = 0;
  const api = client(async (path, request) => {
    calls++;
    assert.equal(path, `/api/2026-11-10/ticket-orders/${lane}`);
    assert.equal(request.method, "PUT");
    assert.deepEqual(JSON.parse(request.body), {
      board_id: board,
      expected_board_etag: tag(board, 3),
      card_ids: [second, first],
    });
    return Response.json(order, { headers: { "X-Board-ETag": tag(board, 4) } });
  });
  assert.equal(
    (await api.applyTicketOrder(board, lane, tag(board, 3), proposed)).data.changed,
    true,
  );
  assert.equal(calls, 1);
});

test("apply stale refusal and malformed successes never replay", async () => {
  for (const response of [
    new Response(null, { status: 412 }),
    new Response("broken"),
    Response.json({ ...order, proposed: current }, { headers: { "X-Board-ETag": tag(board, 4) } }),
    Response.json(
      { ...order, selected_card_ids: [first, first] },
      { headers: { "X-Board-ETag": tag(board, 4) } },
    ),
  ]) {
    let calls = 0;
    const api = client(async () => {
      calls++;
      return response;
    });
    await assert.rejects(
      api.applyTicketOrder(board, lane, tag(board, 3), proposed),
      (error) =>
        error instanceof ApiError &&
        (error.status === 412 ? !error.outcomeUnknown : error.outcomeUnknown),
    );
    assert.equal(calls, 1);
  }
});

test("restore sends raw ZIP with snapshot validator and validates target board/revision", async () => {
  const file = new Blob([new Uint8Array([80, 75, 3, 4])], { type: "application/zip" });
  const api = client(async (path, request) => {
    assert.equal(path, `/api/2026-11-10/snapshots/${board}`);
    assert.equal(request.method, "PUT");
    assert.equal(request.headers.get("If-Match"), tag(board, 3));
    assert.equal(request.headers.get("Content-Type"), "application/zip");
    assert.equal(request.body, file);
    return Response.json(
      { id: board, revision: 4 },
      { headers: { "X-Board-ETag": tag(board, 4) } },
    );
  });
  assert.equal((await api.restoreSnapshot(board, tag(board, 3), file)).data.id, board);
});

test("damaged restore refusals and lost responses remain single attempt", async () => {
  for (const response of [
    new Response(null, { status: 400 }),
    new Response("broken"),
    Response.json({ id: second, revision: 4 }, { headers: { "X-Board-ETag": tag(board, 4) } }),
  ]) {
    let calls = 0;
    const api = client(async () => {
      calls++;
      return response;
    });
    await assert.rejects(
      api.restoreSnapshot(board, tag(board, 3), new Blob()),
      (error) =>
        error instanceof ApiError &&
        (error.status === 400 ? !error.outcomeUnknown : error.outcomeUnknown),
    );
    assert.equal(calls, 1);
  }
});

test("publish keeps original backup and publication UUID for focused readback", async () => {
  let writes = 0;
  const api = client(async (path, request) => {
    if (request.method === "POST") {
      writes++;
      assert.equal(path, "/api/2026-11-10/publications");
      assert.deepEqual(JSON.parse(request.body), {
        id: publication,
        board_id: board,
        backup_id: backup,
      });
      return Response.json(published, {
        status: 202,
        headers: { Location: `/api/2026-11-10/publications/${publication}` },
      });
    }
    assert.equal(path, `/api/2026-11-10/publications/${publication}`);
    return Response.json(published, { headers: { ETag: tag(publication, 1) } });
  });
  await api.publishBackup(publication, board, backup);
  assert.equal((await api.getPublication(publication, board, backup)).data.state, "succeeded");
  assert.equal(writes, 1);
});

test("unusable publication success requires inspection with no replay", async () => {
  for (const response of [
    new Response("broken", { status: 202 }),
    Response.json(
      { ...published, backup_id: second },
      { status: 202, headers: { Location: `/api/2026-11-10/publications/${publication}` } },
    ),
  ]) {
    let calls = 0;
    const api = client(async () => {
      calls++;
      return response;
    });
    await assert.rejects(
      api.publishBackup(publication, board, backup),
      (error) => error instanceof ApiError && error.outcomeUnknown,
    );
    assert.equal(calls, 1);
  }
});

test("availability PATCH uses the original validator and validates the selected board's response", async () => {
  const writes = [];
  const states = ["closed", "open"];
  const api = client(async (path, request) => {
    const index = writes.length;
    writes.push({
      path,
      method: request.method,
      tag: request.headers.get("If-Match"),
      body: JSON.parse(request.body),
    });
    const revision = 2 + index;
    return Response.json(
      { id: board, name: "Board", policy_id: lane, revision, availability: states[index] },
      { headers: { ETag: tag(board, revision), "X-Board-ETag": tag(board, revision) } },
    );
  });
  assert.equal(
    (await api.setBoardAvailability(board, "closed", tag(board, 1))).etag,
    tag(board, 2),
  );
  assert.equal(
    (await api.setBoardAvailability(board, "open", tag(board, 2))).data.availability,
    "open",
  );
  assert.deepEqual(
    writes,
    states.map((availability, index) => ({
      path: "/api/2026-11-10/boards/" + board,
      method: "PATCH",
      tag: tag(board, index + 1),
      body: { availability },
    })),
  );
});

test("availability PATCH refusal or unusable success is never replayed", async () => {
  const metadata = {
    id: board,
    name: "Board",
    policy_id: lane,
    revision: 2,
    availability: "closed",
  };
  for (const response of [
    new Response(null, { status: 412 }),
    Response.json(
      { ...metadata, id: lane },
      { headers: { ETag: tag(board, 2), "X-Board-ETag": tag(board, 2) } },
    ),
    Response.json(
      { ...metadata, availability: "open" },
      { headers: { ETag: tag(board, 2), "X-Board-ETag": tag(board, 2) } },
    ),
    Response.json(metadata, { headers: { ETag: tag(board, 1), "X-Board-ETag": tag(board, 1) } }),
    Response.json(metadata, { headers: { ETag: tag(board, 2) } }),
    Response.json(metadata, { headers: { ETag: tag(board, 2), "X-Board-ETag": tag(board, 1) } }),
  ]) {
    let calls = 0;
    const api = client(async () => {
      calls++;
      return response;
    });
    await assert.rejects(
      api.setBoardAvailability(board, "closed", tag(board, 1)),
      (error) =>
        error instanceof ApiError &&
        (error.status === 412 ? !error.outcomeUnknown : error.outcomeUnknown),
    );
    assert.equal(calls, 1);
  }
});

test("availability metadata only requires the entity ETag", async () => {
  const metadata = {
    id: board,
    name: "Board",
    policy_id: lane,
    revision: 2,
    availability: "closed",
  };
  const api = client(async () => Response.json(metadata, { headers: { ETag: tag(board, 2) } }));
  assert.deepEqual(await api.getBoardAvailability(board), { data: metadata, etag: tag(board, 2) });
});

test("only an exact unavailable lifecycle problem establishes a 503 refusal", async () => {
  const problem = {
    type: "about:blank",
    title: "Service Unavailable",
    status: 503,
    code: "unavailable",
    detail: "Board availability change refused.",
    request_id: first,
    outcome: "not_applied",
    errors: [],
  };
  const cases = [
    [problem, 503, "application/problem+json", false],
    [{ ...problem, outcome: "unknown" }, 503, "application/problem+json", true],
    [{ ...problem, code: "outcome_unknown" }, 503, "application/problem+json", true],
    [{ ...problem, status: "503" }, 503, "application/problem+json", true],
    [{ ...problem, request_id: "bad" }, 503, "application/problem+json", true],
    [problem, 503, "application/json", true],
    [problem, 500, "application/problem+json", true],
    ["invalid JSON", 503, "application/problem+json", true],
    [" ".repeat(4096) + JSON.stringify(problem), 503, "application/problem+json", true],
  ];
  for (const [body, status, contentType, outcomeUnknown] of cases) {
    let writes = 0;
    const api = client(async () => {
      writes++;
      return new Response(typeof body === "string" ? body : JSON.stringify(body), {
        status,
        headers: { "Content-Type": contentType },
      });
    });
    await assert.rejects(
      api.setBoardAvailability(board, "closed", tag(board, 1)),
      (error) =>
        error instanceof ApiError &&
        error.status === status &&
        error.outcomeUnknown === outcomeUnknown,
    );
    assert.equal(writes, 1);
  }
});

test("ordinary board mutations accept only an exact fence refusal without replay", async () => {
  const problem = {
    type: "about:blank",
    title: "Service Unavailable",
    status: 503,
    code: "unavailable",
    detail: "Writes are paused after an uncertain commit. Inspect the saved state.",
    request_id: first,
    outcome: "not_applied",
    errors: [],
  };
  const valid = JSON.stringify(problem);
  const cases = [
    [valid + "\n", "application/problem+json", "unavailable", false],
    [
      JSON.stringify({ ...problem, detail: "Another operation is unavailable." }),
      "application/problem+json",
      "http_error",
      true,
    ],
    [
      JSON.stringify({ ...problem, outcome: "unknown" }),
      "application/problem+json",
      "http_error",
      true,
    ],
    [
      valid.replace('"outcome":', '"outcome":"unknown","outcome":'),
      "application/problem+json",
      "http_error",
      true,
    ],
    [valid, "application/json", "http_error", true],
    [valid.slice(0, -1), "application/problem+json", "http_error", true],
  ];
  for (const method of ["PATCH", "DELETE"]) {
    for (const [body, contentType, code, outcomeUnknown] of cases) {
      let writes = 0;
      const api = client(async (_url, request) => {
        writes++;
        assert.equal(request.method, method);
        return new Response(body, { status: 503, headers: { "Content-Type": contentType } });
      });
      const result =
        method === "PATCH"
          ? api.patchCard(first, tag(first, 1), { title: "Changed" })
          : api.detachAttachment(first, 1, tag(board, 1));
      await assert.rejects(result, { code, status: 503, outcomeUnknown });
      assert.equal(writes, 1);
    }
  }
});
