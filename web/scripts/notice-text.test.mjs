import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { validNoticeText } from "../dist/js/api/notice-text.js";
import { BoardClient } from "../dist/js/api/client.js";
import { parse } from "../dist/js/api/boundary.js";

const vectors = JSON.parse(readFileSync(new URL("../../fixtures/contracts/notice-text/v1.json", import.meta.url), "utf8"));
const id = n => "00000000-0000-4000-8000-" + String(n).padStart(12, "0");
const identity = { boardId: id(1), incarnation: id(2) };
const original = vectors.buildCases.find(value => value.id === "ordinary-two-tickets").expected;
const fallback = vectors.buildCases.find(value => value.id === "no-facts").expected;
const row = payload => ({ targetId: id(3), deliveryId: "a".repeat(64), acceptedAt: "2026-11-07T10:00:03Z",
  class: "human", lowerEvent: 0, upperEvent: 2, eventCount: 2, ...(payload === undefined ? {} : { payload }) });
const envelope = payload => ({ apiVersion: 1, scope: "notices", result: { identity,
  controlGeneration: id(4), configurationRevision: 1, controlOperation: null, targetId: id(3),
  state: "stopped", reason: null, checkpoint: 2, gapCount: 0, last: null, pending: null,
  entries: [row(payload)], lookup: null } });
const json = { "Content-Type": "application/json", "Cache-Control": "no-store" };

test("production text validator agrees with every shared canonical and rejecting vector", async t => {
  assert.ok(vectors.buildCases.length >= 24 && vectors.rejectPayloads.length >= 8);
  for (const value of vectors.buildCases) {
    await t.test(value.id, () => {
      assert.equal(validNoticeText(value.expected), true);
      assert.equal(value.expected.text.endsWith("\n"), false);
      if (value.expected.kind === "fallback" && value.facts !== null) {
        const facts = value.facts;
        const text = "Board: " + facts.label + "\nObserved: " + facts.from + " to " + facts.through + "\nTickets: " +
          (facts.tickets.length ? facts.tickets.map(ticket => "#" + String(ticket)).join(", ") : "none");
        assert.equal(validNoticeText({ version: 1, kind: "complete", facts, text }), false);
      }
    });
  }
  for (const value of vectors.rejectPayloads) {
    await t.test(value.id, () => assert.equal(validNoticeText(value.payload), false));
  }
});

test("production text validator enforces exact UTF-8 budget, calendar and fixed scalar policy", () => {
  const facts = { ...original.facts, label: "A".repeat(81),
    tickets: Array.from({ length: 19 }, (_, index) => 9007199254740973 + index) };
  const complete = f => ({ version: 1, kind: "complete", facts: f,
    text: "Board: " + f.label + "\nObserved: " + f.from + " to " + f.through + "\nTickets: " + f.tickets.map(n => "#" + n).join(", ") });
  const exact = complete(facts);
  assert.equal(new TextEncoder().encode(exact.text).length, 512);
  assert.equal(validNoticeText(exact), true);
  assert.equal(validNoticeText(complete({ ...facts, label: facts.label + "A" })), false);
  assert.equal(validNoticeText(complete({ ...facts, label: "A".repeat(80) + "é" })), false);
  for (const label of ["\u00a0\u1680\u2000\u202f\u205f\u3000", "a\u0000b", "a\u009fb", "a\u061cb", "a\u200fb", "a\u202ab", "a\u2069b", "a\ufeffb", "\ud800"]) {
    assert.equal(validNoticeText(complete({ ...original.facts, label })), false);
  }
  for (const from of ["0000-01-01T00:00:00Z", "2026-02-30T00:00:00Z", "2026-11-07T10:00:00.0Z", "2026-11-07T10:00:00+00:00"]) {
    assert.equal(validNoticeText(complete({ ...original.facts, from })), false);
  }
  assert.equal(validNoticeText(complete({ ...original.facts, from: "0001-01-01T00:00:00Z", through: "9999-12-31T23:59:59Z" })), true);
  for (const label of [" e\u0301 ", " é ", "a\u200bb"]) assert.equal(validNoticeText(complete({ ...original.facts, label })), true);
});

test("actual BoardClient accepts originals and legacy absence, and refuses malformed payloads without POST", async t => {
  globalThis.location = new URL("http://127.0.0.1:38170");
  t.after(() => { delete globalThis.location; });
  let wire = envelope(original), raw, credential = "generation-A", calls = 0, captured;
  const client = await BoardClient.open({ installationId: id(5), profileId: "synthetic-notice-text", transport: "local-cookie",
    selection: { mode: "workspace", ...identity, actorId: id(6), grantId: id(7) }, credential: () => credential,
    fetch: async (url, init) => { calls++; captured = { url, init }; return new Response(raw ?? JSON.stringify(wire), { headers: json }); } });
  t.after(() => client.close());
  const read = () => client.notices(identity, { action: "inbox" }, new AbortController().signal);
  for (const payload of [original, fallback, undefined, null]) {
    wire = envelope(payload);
    const result = await read();
    assert.deepEqual(result.entries[0].payload, payload === undefined ? undefined : parse(JSON.stringify(payload)));
    assert.equal(captured.init.method ?? "GET", "GET");
    assert.equal(captured.init.body, undefined);
    assert.equal(captured.init.credentials, "same-origin");
    assert.equal(captured.init.redirect, "error");
    assert.equal(captured.init.cache, "no-store");
    assert.equal(captured.url, "/api/v1/boards/" + identity.boardId + "/notices/inbox?incarnation=" + identity.incarnation);
  }
  for (const value of vectors.rejectPayloads) { wire = envelope(value.payload); await assert.rejects(read()); }
  for (const payload of [0, false, {}, [], { ...original, facts: { ...original.facts, extra: "refused" } }]) {
    wire = envelope(payload); await assert.rejects(read());
  }
  wire = envelope(original); wire.result.reason = "foreign";
  await assert.rejects(read());
  for (const kind of ["observed-empty", "expired", "gapped", "unavailable"]) {
    wire = envelope(original);
    Object.assign(wire.result.entries[0], { class: kind, eventCount: kind === "observed-empty" ? 0 : null,
      upperEvent: kind === "observed-empty" ? 0 : 2 });
    await assert.rejects(read());
    wire.result.entries[0].payload = fallback;
    assert.equal((await read()).entries[0].payload.text, fallback.text);
  }
  wire = envelope(original);
  raw = JSON.stringify(wire).replace('"version":1', '"version":1,"version":1');
  await assert.rejects(read());
  raw = JSON.stringify(wire) + " ".repeat(131_072); await assert.rejects(read()); raw = undefined;
  const inFlight = read(); credential = "generation-B"; await assert.rejects(inFlight);
  assert.ok(calls > 20);
  for (const bad of ['{"version":1,"version":1}', '"\\ud800"', '{"number":9007199254740992}']) assert.throws(() => parse(bad));
});
