import assert from "node:assert/strict";
import test from "node:test";
import { ActivityCursorStale } from "../../dist/js/api/activity.js";
import { BoardClient } from "../../dist/js/api/client.js";

const id = suffix => "10000000-0000-4000-8000-" + String(suffix).padStart(12, "0");
const identity = { boardId: id(1), incarnation: id(2) };
const source = { boardId: id(3), incarnation: id(4) };
const floor = "2026-11-03T00:00:00Z";
const event = (eventId, origin = identity) => ({
  origin, eventId, at: "2026-11-04T00:00:00Z", type: "card.edit", boardRevision: 42,
  actorKind: "human", actorId: id(5),
});
const envelope = () => ({
  apiVersion: 1, scope: "activity",
  result: {
    identity, from: "2026-10-30T00:00:00Z", through: "2026-11-06T00:00:00Z",
    revision: 7, localEventUpper: 21, coverage: { status: "complete", activityFloor: null, gaps: [] },
    events: [event(22, source), ...Array.from({ length: 19 }, (_, index) => event(21 - index))],
    nextCursor: "eA.eQ",
  },
});
const json = { "Content-Type": "application/json", "Cache-Control": "no-store" };
const respond = (data, status = 200, headers = json) => new Response(JSON.stringify(data), { status, headers });

test("selected activity read validates the page, wire, continuation and current connection", async () => {
  globalThis.location = new URL("http://127.0.0.1:38170");
  let captured, wire = respond(envelope()), credential = "initial", observedRefusals = 0;
  const client = await BoardClient.open({
    installationId: id(6), profileId: "synthetic", transport: "local-cookie",
    selection: { ...identity, mode: "workspace", actorId: id(5), grantId: id(7) },
    credential: () => credential,
    authenticationUnavailable: () => { observedRefusals++; },
    fetch: async (url, init) => { captured = { url, init }; return wire; },
  });
  const signal = new AbortController().signal;
  try {
    const first = await client.activity(identity, signal);
    assert.equal(first.events[0].eventId, 22);
    assert.equal(first.events[0].boardRevision, 42);
    const path = "/api/v1/boards/" + identity.boardId + "/activity?incarnation=" + identity.incarnation;
    assert.equal(captured.url, path);
    assert.equal(captured.init.body, undefined);
    assert.equal(captured.init.credentials, "same-origin");
    assert.equal(captured.init.headers["X-Localswim-Client"], "browser");
    assert.equal(captured.init.redirect, "error");
    assert.equal(captured.init.cache, "no-store");
    await assert.rejects(client.activity(source, signal));
    await assert.rejects(client.activity({ ...identity, incarnation: source.incarnation }, signal));

    const terminal = envelope();
    Object.assign(terminal.result, { events: [event(2), event(1)], nextCursor: null });
    wire = respond(terminal);
    assert.equal((await client.activity(identity, signal, first)).nextCursor, null);
    assert.equal(captured.url, path + "&cursor=eA.eQ");
    for (const result of [
      { from: "2026-10-29T00:00:00Z", through: "2026-11-05T00:00:00Z" },
      { revision: 8 }, { localEventUpper: 22 }, { events: [event(3)] },
      { coverage: { status: "gapped", activityFloor: floor, gaps: [] } },
    ]) {
      wire = respond({ ...terminal, result: { ...terminal.result, ...result } });
      await assert.rejects(client.activity(identity, signal, first));
    }
    wire = respond({ apiVersion: 1, outcome: "refused", conflict: null, error: { code: "CURSOR_STALE", field: null, message: "Request refused" } }, 409);
    await assert.rejects(client.activity(identity, signal, first), ActivityCursorStale);

    for (const change of [
      value => { value.result.identity = source; },
      value => { value.result.events.reverse(); },
      value => { value.result.events[0].origin = identity; },
      value => { value.result.events[0].actorKind = "owner"; },
      value => { value.result.events[0].actorId = null; },
      value => { value.result.events[0].title = "excluded field"; },
      value => { value.result.events[0].at = value.result.from; },
      value => { value.result.nextCursor = "eB.eQ"; },
      value => { value.result.coverage.activityFloor = floor; },
      value => { value.result.coverage = { status: "gapped", activityFloor: null,
        gaps: [{ origin: source, reason: "expired", fromExclusive: floor, throughInclusive: floor }] }; },
    ]) {
      const invalid = structuredClone(envelope());
      change(invalid);
      wire = respond(invalid);
      await assert.rejects(client.activity(identity, signal));
    }
    wire = respond(envelope(), 200, { ...json, "Cache-Control": "max-age=2" });
    await assert.rejects(client.activity(identity, signal));
    wire = new Response(JSON.stringify(envelope()) + " ".repeat(32_768), { headers: json });
    await assert.rejects(client.activity(identity, signal));
    wire = new Response('{"apiVersion":1,' + JSON.stringify(envelope()).slice(1), { headers: json });
    await assert.rejects(client.activity(identity, signal));
    wire = respond(envelope());
    const inFlight = client.activity(identity, signal);
    credential = "new-generation";
    await assert.rejects(inFlight);

    wire = respond({ apiVersion: 1, outcome: "refused", conflict: null, error: { code: "UNAUTHENTICATED", field: null, message: "Request refused" } }, 401);
    const cancelled = new AbortController();
    const lateRefusal = client.activity(identity, cancelled.signal);
    cancelled.abort();
    await assert.rejects(lateRefusal);
    assert.equal(observedRefusals, 0);
  } finally {
    client.close();
    delete globalThis.location;
  }
});
