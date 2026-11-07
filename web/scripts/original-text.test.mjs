import assert from "node:assert/strict";
import test from "node:test";
const { originalSnapshot, sameOriginal, copyOriginal } = await import(process.env.LOCALSWIM_COPY_MODULE ?? "../dist/js/board/original-text.js");
const payload = { version: 1, kind: "fallback", facts: null, text: "Inspect the selected board for changes." };
const pair = () => ({ targetId: "00000000-0000-4000-8000-000000000001", deliveryId: "a".repeat(64),
  acceptedAt: "2026-11-07T00:00:00Z", class: "human", lowerEvent: 0, upperEvent: 1, eventCount: 1, payload: structuredClone(payload) });

test("owned original snapshot and full payload identity", () => {
  const input = pair(), owned = originalSnapshot(input);
  input.payload.text = "Changed";
  assert.equal(owned.payload.text, payload.text);
  assert.ok(Object.isFrozen(owned) && Object.isFrozen(owned.payload));
  assert.equal(sameOriginal(owned, pair()), true);
  for (const changed of [{ targetId: "other" }, { deliveryId: "b".repeat(64) }, { payload: null },
    { payload: { ...payload, text: payload.text + "\n" } }]) assert.equal(sameOriginal(owned, { ...pair(), ...changed }), false);
  assert.equal(sameOriginal({ ...pair(), payload: null }, { ...pair(), payload: null }), false);
  const complete = { version: 1, kind: "complete", facts: { label: "é Ω", from: "2026-11-07T00:00:00Z", through: "2026-11-07T00:00:01Z", tickets: [7, 9] },
    text: "Board: é Ω\nObserved: 2026-11-07T00:00:00Z to 2026-11-07T00:00:01Z\nTickets: #7, #9" };
  const completePair = { ...pair(), payload: complete }, captured = originalSnapshot(completePair);
  complete.facts.tickets[0] = 8;
  assert.equal(captured.payload.facts.tickets[0], 7);
  assert.ok(Object.isFrozen(captured.payload.facts) && Object.isFrozen(captured.payload.facts.tickets));
  assert.equal(sameOriginal(captured, captured), true);
  assert.equal(sameOriginal(captured, completePair), false);
});

test("clipboard helper uses exact validated bytes and resolved OS result", async t => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  let active = true, current = true, clipboard, writes = [];
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: {
    get clipboard() { return clipboard; }, get userActivation() { return { isActive: active }; },
  } });
  t.after(() => { if (descriptor) Object.defineProperty(globalThis, "navigator", descriptor); else delete globalThis.navigator; });
  clipboard = { writeText: async value => { writes.push(value); } };
  assert.equal(await copyOriginal(pair(), () => current), "copied");
  assert.deepEqual(writes, [payload.text]);
  await t.test("malformed and legacy cannot write", async () => {
    assert.equal(await copyOriginal({ ...pair(), payload: null }, () => true), "unavailable");
    assert.equal(await copyOriginal({ ...pair(), payload: { ...payload, text: "arbitrary" } }, () => true), "unavailable");
    assert.equal(writes.length, 1);
  });
  await t.test("stale, unsupported and expired cannot invoke", async () => {
    assert.equal(await copyOriginal(pair(), () => false), "stale");
    clipboard = undefined;
    assert.equal(await copyOriginal(pair(), () => true), "unsupported");
    clipboard = { writeText: async value => { writes.push(value); } }; active = false;
    assert.equal(await copyOriginal(pair(), () => true), "expired"); active = true;
    assert.equal(writes.length, 1);
  });
  await t.test("denial and synchronous failure are not success", async () => {
    clipboard = { writeText: () => Promise.reject(Error("controlled denial")) };
    assert.equal(await copyOriginal(pair(), () => true), "failed");
    clipboard = { writeText: () => { throw Error("controlled synchronous failure"); } };
    assert.equal(await copyOriginal(pair(), () => true), "failed");
  });
  await t.test("late admitted write suppresses success without claiming undo", async () => {
    let resolve;
    clipboard = { writeText: () => new Promise(done => { resolve = done; }) };
    const result = copyOriginal(pair(), () => current);
    current = false; resolve();
    assert.equal(await result, "stale"); current = true;
  });
  await t.test("pending write is bounded and late resolution cannot become success", async () => {
    let resolve;
    clipboard = { writeText: () => new Promise(done => { resolve = done; }) };
    assert.equal(await copyOriginal(pair(), () => current), "failed");
    resolve();
  });
});
