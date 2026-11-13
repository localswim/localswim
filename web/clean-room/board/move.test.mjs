/// <reference types="node" />
import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import { registerHooks } from "node:module";
import test from "node:test";

const hook = registerHooks({
  resolve(specifier, context, nextResolve) {
    if (
      context.parentURL?.startsWith(new URL("../", import.meta.url).href) &&
      specifier.startsWith(".") &&
      specifier.endsWith(".js")
    ) {
      return nextResolve(`${specifier.slice(0, -3)}.ts`, context);
    }
    return nextResolve(specifier, context);
  },
});
const [
  { createEntryFixture },
  { inspectCardMove, moveDestinations, prepareCardMove, requestCardMove },
] = await Promise.all([import("../fixtures.ts"), import("./move.ts")]);
hook.deregister();

const entry = createEntryFixture();
assert.equal(entry.board.status, "ready");
const data = entry.board.current.value;
const { board, cards, lanes } = data.view;
const actor = data.users.find((user) => user.kind === "human");
const card = cards[0];
const destination = lanes.find((lane) => lane.role === "completed");
assert.ok(actor && card && destination);
const actorId = actor.id;
const cardEtag = '"original-card-validator"';

void test("movement options require a directed grant for the actor", () => {
  const bot = data.users.find((user) => user.kind === "bot");
  assert.ok(bot);
  assert.ok(moveDestinations(data, card, actorId).some((lane) => lane.id === destination.id));
  assert.ok(!moveDestinations(data, card, bot.id).some((lane) => lane.id === destination.id));
  assert.ok(!moveDestinations(data, card, actorId).some((lane) => lane.id === card.lane_id));
  assert.deepEqual(moveDestinations(data, { ...card, archived: true }, actorId), []);
});

void test("guards lane moves and inspects a lost response without sending another PATCH", async (context) => {
  const moved = {
    ...card,
    revision: board.revision + 1,
    lane_id: destination.id,
    completed_at: board.updated_at,
  };
  const movedBack = { ...card, revision: moved.revision + 1 };
  const audit = {
    id: "77777777-0000-7000-8000-000000000001",
    board_id: board.id,
    revision: moved.revision,
    at: board.updated_at,
    actor_id: actorId,
    operation: "patch",
    resource_type: "card",
    resource_id: card.id,
    field: "lane_id",
    before: card.lane_id,
    after: destination.id,
  };
  /** @type {{cardTag: string | null, boardTag: string | null, body: RequestInit["body"]}[]} */
  const requests = [];
  let focused = card;
  let matchingActivity = false;
  /** @type {"lost" | "stale" | "success"} */
  let response = "lost";
  /** @type {typeof fetch} */
  const fetchMock = async (path, options) => {
    assert.ok(typeof path === "string" && options !== undefined);
    const url = new URL(path, "http://127.0.0.1:53146");
    if (url.pathname.endsWith("/browser-session")) {
      return Response.json({
        principal_id: actorId,
        kind: "human",
        api_version: "2026-11-10",
        workspace_admin: true,
        access_token: Buffer.alloc(32, 1).toString("base64url"),
        expires_on_service_replacement: true,
      });
    }
    if (url.pathname.endsWith(`/cards/${card.id}`)) {
      if (options.method === "PATCH") {
        const headers = new Headers(options.headers);
        requests.push({
          cardTag: headers.get("If-Match"),
          boardTag: headers.get("X-Expected-Board-ETag"),
          body: options.body,
        });
        const requestId = headers.get("X-Request-ID");
        assert.ok(requestId);
        if (response === "stale") {
          return Response.json(
            {
              type: "about:blank",
              title: "Precondition Failed",
              status: 412,
              code: "precondition_failed",
              detail: "",
              request_id: requestId,
              outcome: "not_applied",
              errors: [],
            },
            {
              status: 412,
              headers: {
                "Content-Type": "application/problem+json",
                "X-Request-ID": requestId,
              },
            },
          );
        }
        focused = moved;
        if (response === "success") {
          return Response.json(moved, {
            headers: {
              ETag: '"committed-card-validator"',
              "X-Board-ETag": '"committed-board-validator"',
              "X-Request-ID": requestId,
            },
          });
        }
        throw new Error("response interrupted");
      }
      return Response.json(focused, { headers: { ETag: cardEtag } });
    }
    assert.ok(url.pathname.endsWith("/activities"));
    assert.equal(url.searchParams.get("card_id"), card.id);
    assert.equal(url.searchParams.get("board_id"), board.id);
    assert.equal(url.searchParams.get("view"), "full");
    return Response.json({
      board_revision: focused.revision,
      activities: [matchingActivity ? audit : { ...audit, revision: moved.revision + 1 }],
    });
  };
  context.mock.method(globalThis, "fetch", fetchMock);

  const move = await prepareCardMove(data, card.id, destination.id, actorId);
  assert.ok(move);
  let guardedActor;
  await requestCardMove(move, undefined, (session) => {
    guardedActor = session.principal_id;
    throw new Error("Move no longer owns the active board");
  });
  assert.equal(guardedActor, actorId);
  assert.equal(requests.length, 0);
  assert.equal((await requestCardMove(move)).outcome, "unknown");
  assert.equal((await inspectCardMove(move)).outcome, "unknown");
  matchingActivity = true;
  focused = movedBack;
  assert.deepEqual(await inspectCardMove(move), { outcome: "committed", value: movedBack });
  assert.equal(requests.length, 1);
  assert.equal(await prepareCardMove(data, card.id, destination.id, actorId), null);

  focused = card;
  response = "stale";
  const stale = await prepareCardMove(data, card.id, destination.id, actorId);
  assert.ok(stale);
  const refused = await requestCardMove(stale);
  assert.equal(refused.outcome, "not_applied");
  assert.equal(refused.problem.code, "precondition_failed");
  assert.deepEqual(focused, card);
  assert.equal(requests.length, 2);

  response = "success";
  const next = await prepareCardMove(data, card.id, destination.id, actorId);
  assert.ok(next);
  assert.deepEqual(await requestCardMove(next), { outcome: "committed", value: moved });
  assert.equal(requests.length, 3);
  for (const request of requests) {
    assert.deepEqual(request, {
      cardTag: cardEtag,
      boardTag: data.boardEtag,
      body: JSON.stringify({ lane_id: destination.id }),
    });
  }
});
