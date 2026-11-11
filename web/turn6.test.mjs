import assert from "node:assert/strict";
import test from "node:test";
import { ApiError, createBrowserApi } from "./api.mjs";
const board = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const lane = "ceab10a8-1492-4c90-aa07-13b83138919a";
const card = "d2ef6295-ace1-4e6e-bbf7-b1c1823f56fc";
const priority = "163b1fa0-2a32-424b-8489-6bbd7761201c";
const owner = "84b4ae21-e224-4f5a-9fca-9abf8e0c86bc";
const backup = "f7e04b53-cfd9-717c-af8e-b6316af0a085";
const at = "2026-11-10 12:00:00+00:00";
const tag = (id, revision) => `"${id}-${revision}"`;
const client = (fetchImpl) => createBrowserApi({ getCapability: () => "test-capability", fetchImpl });
const laneData = { id: lane, board_id: board, key: "ready", label: "Ready", revision: 2,
  position: 1, retired: false, role: "ordinary", approval_enabled: false, reorder_enabled: false,
  created_at: at, updated_at: at };
const cardData = { id: card, board_id: board, lane_id: lane, owner_id: owner, priority_id: priority,
  title: "Card", description: "", ticket: 1, revision: 3, parent_id: null, child_ids: [],
  archived: false, cover_attachment_id: null, comment_count: 0, attachment_count: 0,
  blocking_depth: 0, blocked_card_count: 0, triage_score: 100, completed_at: null,
  created_at: at, updated_at: at };
test("ordered board view preserves server card order and validates membership", async () => {
  const value = { board: { id: board, revision: 3 }, lanes: [laneData], cards: [cardData] };
  const api = client(async (path) => {
    assert.equal(path, `/api/2026-11-10/boards/${board}?view=lanes`);
    return Response.json(value, { headers: { ETag: tag(board, 3) } });
  });
  assert.deepEqual((await api.getBoardLanes(board)).data, value);
  const invalid = client(async () => Response.json({ ...value, cards: [{ ...cardData, archived: true }] },
    { headers: { ETag: tag(board, 3) } }));
  await assert.rejects(invalid.getBoardLanes(board), ApiError);
});
test("board and lane reads validate notes independently of bulk approval comments", async () => {
  const notes = " Review 🧭\n<example> ";
  const reviewed = { ...laneData, role: "ready_for_review", bulk_approval_comment: " Approved " };
  const presentation = { human: { label: " Reviewer ", accent: "#123AbC" }, bot: { accent: "#456def" } };
  const prefix = "/api/2026-11-10";
  function apiFor(saved, actors = presentation) {
    return client(async (path) => {
      if (path === `${prefix}/boards/${board}?view=lanes`) {
        return Response.json({ board: { id: board, revision: 3, actor_presentation: actors }, lanes: [saved], cards: [] },
          { headers: { ETag: tag(board, 3) } });
      }
      assert.equal(path, `${prefix}/lanes?board_id=${board}`);
      return Response.json({ lanes: [saved], next_cursor: null });
    });
  }
  for (const saved of [{ ...laneData, notes }, reviewed, { ...reviewed, notes }, { ...laneData, notes: "🧭".repeat(4096) }]) {
    const api = apiFor(saved);
    const ordered = await api.getBoardLanes(board);
    assert.deepEqual(ordered.data.board.actor_presentation, presentation);
    assert.deepEqual(ordered.data.lanes, [saved]);
    assert.deepEqual((await api.listLanes(board)).data.lanes, [saved]);
  }
  const invalidResponse = (error) => error instanceof ApiError && error.code === "invalid_response" && !error.outcomeUnknown;
  for (const saved of [
    ...["", "🧭".repeat(4097), 5, null, "\uD800"].map((value) => ({ ...reviewed, notes: value })),
    { ...reviewed, notes, unexpected: true },
    { ...reviewed, notes, board_id: owner },
  ]) {
    const api = apiFor(saved);
    await assert.rejects(api.getBoardLanes(board), invalidResponse);
    await assert.rejects(api.listLanes(board), invalidResponse);
  }
  for (const actors of [null, [], { human: null }, { bot: { kind: "human" } },
    { human: { accent: "#123abc\n" } }, { bot: { label: "" } }, { human: { label: "x".repeat(257) } }]) {
    await assert.rejects(apiFor(laneData, actors).getBoardLanes(board), invalidResponse);
  }
});

test("lane writes preserve exact notes and accept the canonical omitted note after clearing", async () => {
  const prefix = "/api/2026-11-10";
  const notes = " Review first.\nReady 🧭 ";
  const bulk = " Approved ";
  const reviewed = { ...laneData, role: "ready_for_review", bulk_approval_comment: bulk };
  const create = { label: "Ready", after_id: priority, create_for: [], connect_for: [], notes };
  const createApi = client(async (path, request) => {
    assert.equal(path, prefix + "/lanes");
    assert.equal(request.method, "POST");
    assert.deepEqual(JSON.parse(request.body), { ...create, board_id: board, board_etag: tag(board, 3) });
    return Response.json({ ...laneData, notes }, { status: 201, headers: { ETag: tag(lane, 2) } });
  });
  assert.equal((await createApi.createLane(board, tag(board, 3), create)).data.notes, notes);

  for (const text of [notes, ""]) {
    const change = { notes: text, bulk_approval_comment: bulk };
    const saved = text ? { ...reviewed, notes: text } : reviewed;
    const api = client(async (path, request) => {
      assert.equal(path, prefix + "/lanes/" + lane);
      assert.equal(request.method, "PATCH");
      assert.equal(request.headers.get("If-Match"), tag(lane, 2));
      assert.deepEqual(JSON.parse(request.body), { board_etag: tag(board, 3), ...change });
      return Response.json(saved, { headers: { ETag: tag(lane, 4) } });
    });
    assert.deepEqual((await api.updateLaneSettings(board, lane, 2, tag(board, 3), change)).data, saved);
  }
  const renameApi = client(async (path, request) => {
    assert.equal(path, prefix + "/lanes/" + lane);
    if (request.method === "GET") {
      return Response.json({ ...reviewed, notes }, { headers: { ETag: tag(lane, 2) } });
    }
    assert.equal(request.method, "PATCH");
    assert.equal(request.headers.get("If-Match"), tag(lane, 2));
    assert.deepEqual(JSON.parse(request.body), { board_etag: tag(board, 3), label: "Review" });
    return Response.json({ ...reviewed, label: "Review", notes, revision: 4 }, { headers: { ETag: tag(lane, 4) } });
  });
  assert.equal((await renameApi.renameLane(lane, 2, tag(board, 3), "Review")).data.notes, notes);

  for (const saved of [{ ...reviewed, notes: "" }, { ...reviewed, notes: false },
    { ...reviewed, notes: "Other note" }, { ...reviewed, notes, unexpected: true }]) {
    const api = client(async () => Response.json(saved, { headers: { ETag: tag(lane, 4) } }));
    await assert.rejects(api.updateLaneSettings(board, lane, 2, tag(board, 3), { notes }),
      (error) => error instanceof ApiError && error.code === "invalid_response" && error.outcomeUnknown);
  }
});

test("lane key uses lane and board validators and checks exact saved key", async () => {
  const api = client(async (path, request) => {
    assert.equal(path, `/api/2026-11-10/lanes/${lane}`);
    assert.equal(request.method, "PATCH");
    assert.equal(request.headers.get("If-Match"), tag(lane, 2));
    assert.deepEqual(JSON.parse(request.body), { board_etag: tag(board, 3), key: "ready_next" });
    return Response.json({ ...laneData, key: "ready_next", revision: 4 }, { headers: { ETag: tag(lane, 4) } });
  });
  await api.updateLaneSettings(board, lane, 2, tag(board, 3), { key: "ready_next" });
});
test("batch priorities submit one guarded request with effective selected cards", async () => {
  let calls = 0;
  const api = client(async (path, request) => {
    calls++;
    assert.equal(path, "/api/2026-11-10/cards");
    assert.equal(request.method, "PATCH");
    assert.deepEqual(JSON.parse(request.body), { board_id: board, expected_board_etag: tag(board, 3),
      card_ids: [card], priority_id: priority });
    return Response.json({ board_id: board, board_revision: 3, cards: [cardData], changes: [] },
      { headers: { "X-Board-ETag": tag(board, 3) } });
  });
  assert.deepEqual((await api.setPriorities(board, tag(board, 3), [card], priority)).data.cards, [cardData]);
  await assert.rejects(api.setPriorities(board, tag(board, 3), [card, card.toUpperCase()], priority), TypeError);
  assert.equal(calls, 1);
});
test("uncertain batch result never replays a write", async () => {
  for (const response of [new Response("broken", { status: 200 }), new Response(null, { status: 503 })]) {
    let calls = 0;
    const api = client(async () => { calls++; return response; });
    await assert.rejects(api.setPriorities(board, tag(board, 3), [card], priority),
      (error) => error instanceof ApiError && error.outcomeUnknown);
    assert.equal(calls, 1);
  }
});
test("ticket preview is read-only with repeated selected-card query", async () => {
  const api = client(async (path, request) => {
    const url = new URL(path, "http://localhost");
    assert.equal(request.method, "GET");
    assert.equal(url.searchParams.get("preview"), "true");
    assert.deepEqual(url.searchParams.getAll("card_id"), [card]);
    return Response.json({ board_id: board, lane_id: lane, board_revision: 3, selected_card_ids: [card],
      current: [{ card_id: card, ticket: 1 }], proposed: [{ card_id: card, ticket: 1 }], changed: false },
      { headers: { "X-Board-ETag": tag(board, 3) } });
  });
  assert.equal((await api.previewTicketOrder(board, lane, [card])).data.changed, false);
});
test("backup capture keeps UUID for focused uncertain-outcome readback", async () => {
  const data = { id: backup, board_id: board, board_revision: 3,
    sha2_256: "0".repeat(64), size: 10, attachment_ids: [] };
  let writes = 0;
  const api = client(async (path, request) => {
    if (request.method === "POST") {
      writes++;
      assert.deepEqual(JSON.parse(request.body), { id: backup, board_id: board, expected_board_etag: tag(board, 3) });
      return Response.json(data, { status: 201, headers: { Location: `/api/2026-11-10/backups/${backup}` } });
    }
    assert.equal(path, `/api/2026-11-10/backups/${backup}`);
    return Response.json(data, { headers: { ETag: tag(backup, 1) } });
  });
  await api.createBackup(backup, board, tag(board, 3));
  await api.getBackup(backup, board);
  assert.equal(writes, 1);
});

test("ticket preview rejects a changed ticket pool or false change indicator", async () => {
  for (const proposed of [[{ card_id: card, ticket: 2 }], [{ card_id: card, ticket: 1 }]]) {
    const api = client(async () => Response.json({ board_id: board, lane_id: lane, board_revision: 3,
      selected_card_ids: [card], current: [{ card_id: card, ticket: 1 }], proposed, changed: true },
      { headers: { "X-Board-ETag": tag(board, 3) } }));
    await assert.rejects(api.previewTicketOrder(board, lane, [card]), ApiError);
  }
});
test("backup bytes are bounded by metadata and SHA-2-256 verified before download", async () => {
  const bytes = new Uint8Array([80, 75, 3, 4]);
  const digest = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)),
    (value) => value.toString(16).padStart(2, "0")).join("");
  const api = client(async () => new Response(bytes, { headers: { "Content-Type": "application/zip" } }));
  assert.equal((await api.getBackupContent(backup, bytes.length, digest)).size, bytes.length);
  await assert.rejects(api.getBackupContent(backup, bytes.length - 1, digest), ApiError);
  await assert.rejects(api.getBackupContent(backup, bytes.length, "0".repeat(64)), ApiError);
});

test("backup creation rejects malformed success without replay", async () => {
  const data = { id: backup, board_id: board, board_revision: 3,
    sha2_256: "0".repeat(64), size: 10, attachment_ids: [] };
  for (const response of [
    Response.json(data, { status: 201 }),
    Response.json(data, { status: 201, headers: { Location: `/api/2026-11-10/backups/${card}` } }),
    Response.json({ ...data, id: card }, { status: 201, headers: { Location: `/api/2026-11-10/backups/${backup}` } }),
    new Response("broken", { status: 201, headers: { Location: `/api/2026-11-10/backups/${backup}` } }),
    Response.json(data, { status: 200, headers: { Location: `/api/2026-11-10/backups/${backup}` } }),
  ]) {
    let calls = 0;
    const api = client(async () => { calls++; return response; });
    await assert.rejects(api.createBackup(backup, board, tag(board, 3)),
      (error) => error instanceof ApiError && error.outcomeUnknown);
    assert.equal(calls, 1);
  }
});
