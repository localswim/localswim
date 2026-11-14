/// <reference types="node" />
import assert from "node:assert/strict";
import { test } from "node:test";
import { createEntryFixture } from "../fixtures.ts";
import { createRetainedBoardCache } from "./retained-cache.ts";

void test("retained boards are scoped to the admitted principal and catalog", () => {
  const fixture = createEntryFixture();
  assert.equal(fixture.board.status, "ready");
  const read = fixture.board.current;
  const { id } = read.value.view.board;
  const cache = createRetainedBoardCache();
  cache.retain(read);
  assert.equal(cache.previous(id), null);

  cache.setPrincipal(fixture.session.principal_id);
  cache.retain(read);
  assert.equal(cache.previous(id), read);
  assert.ok(cache.settings().bytes > 0);
  cache.retainOnly(new Set());
  assert.equal(cache.previous(id), null);

  cache.retain(read);
  cache.setPrincipal("418a33be-d5b8-46a5-aef3-4f30fef3a190");
  assert.equal(cache.previous(id), null);
  assert.equal(cache.settings().bytes, 0);
});

void test("lowering the retained-board budget to zero removes cached reads", () => {
  const fixture = createEntryFixture();
  assert.equal(fixture.board.status, "ready");
  const cache = createRetainedBoardCache();
  cache.setPrincipal(fixture.session.principal_id);
  cache.retain(fixture.board.current);
  assert.equal(cache.settings().count, 1);
  cache.setBudget(0);
  cache.retain(fixture.board.current);
  assert.equal(cache.settings().count, 0);
});
