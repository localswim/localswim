import assert from "node:assert/strict";
import { setImmediate } from "node:timers/promises";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const { outputFiles } = await build({
  stdin: {
    contents: `export { mountCardComments } from "./src/comments.ts";
               export { ApiError } from "./api.mjs";`,
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  format: "esm",
  write: false,
});
const compiled =
  "data:text/javascript;base64," + Buffer.from(outputFiles[0].text).toString("base64");
const { mountCardComments, ApiError } = await import(compiled);
const secondDocument = await import(`${compiled}#second-document`);

globalThis.InputEvent = class InputEvent extends Event {};
const doc = { createElement: (tag) => new Element(tag) };
class Element extends EventTarget {
  constructor(tag) {
    super();
    this.tag = tag;
    this.ownerDocument = doc;
    this.children = [];
    this.textContent = "";
    this.value = "";
    this.selectionStart = 0;
    this.selectionEnd = 0;
  }
  append(...values) {
    this.textContent += values.filter((value) => typeof value === "string").join("");
    this.children.push(...values.filter((value) => value instanceof Element));
  }
  replaceChildren(...values) {
    this.children = values;
  }
  descendants() {
    return this.children.flatMap((value) => [value, ...value.descendants()]);
  }
  contains(value) {
    return this === value || this.descendants().includes(value);
  }
  setAttribute() {}
  removeAttribute() {}
  setSelectionRange(selectionStart, selectionEnd) {
    Object.assign(this, { selectionStart, selectionEnd });
  }
  focus() {
    doc.activeElement = this;
  }
}

const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const cardId = "d2ef6295-ace1-4e6e-bbf7-b1c1823f56fc";
const actorId = "163b1fa0-2a32-424b-8489-6bbd7761201c";
const tag = (id, revision) => JSON.stringify(`${id}-${revision}`);
const text = "  Exact comment\nSecond line  ";

function harness() {
  const journal = new Map();
  const committed = new Map();
  const writes = [];
  const reads = [];
  let boardRevision = 4;
  let cardRevision = 4;
  let afterMissing;
  let afterBoardRead;
  let beforeRetry;
  let readOriginalComment;
  let readSavedComments = async () => ({ data: { comments: [], next_cursor: null } });
  let readSavedActivity = async () => ({ data: { activities: [], next_cursor: null } });
  const response = Promise.withResolvers();

  function commit(write) {
    if (
      write.cardEtag !== tag(cardId, cardRevision) ||
      write.boardEtag !== tag(boardId, boardRevision)
    ) {
      throw new ApiError("http_error", { status: 412 });
    }
    cardRevision = ++boardRevision;
    const data = {
      id: write.id,
      board_id: boardId,
      card_id: cardId,
      author_id: actorId,
      created_at: "2026-11-13 15:00:00+00:00",
      text: write.text,
    };
    committed.set(write.id, data);
    return { data, boardEtag: tag(boardId, boardRevision) };
  }

  function mount(controller = { mountCardComments, ApiError }) {
    const lifetime = new AbortController();
    const root = doc.createElement("main");
    const view = controller.mountCardComments(root, {
      api: {
        listComments: (...args) => readSavedComments(...args),
        listFocusedCardActivity: (...args) => readSavedActivity(...args),
        getBoardAvailability: async (id) => {
          reads.push(id);
          const result = { data: { id, revision: boardRevision }, etag: tag(id, boardRevision) };
          afterBoardRead?.();
          return result;
        },
        getCard: async (id) => {
          reads.push(id);
          return { data: { id, board_id: boardId }, etag: tag(id, cardRevision) };
        },
        getComment: async (id) => {
          reads.push(id);
          if (readOriginalComment) return readOriginalComment(id);
          if (!committed.has(id)) {
            afterMissing?.();
            throw new controller.ApiError("http_error", { status: 404 });
          }
          return { data: committed.get(id) };
        },
        createComment: async (_cardId, cardEtag, text, id, boardEtag) => {
          const write = { cardEtag, boardEtag, text, id };
          writes.push(write);
          if (writes.length === 1) return response.promise;
          if (beforeRetry) return beforeRetry(write, controller.ApiError);
          return commit(write);
        },
      },
      storage: {
        getItem: (key) => journal.get(key) ?? null,
        setItem: (key, value) => journal.set(key, value),
        removeItem: (key) => journal.delete(key),
      },
      scope: { boardId, cardId },
      signal: lifetime.signal,
      context: () => ({
        actorId,
        boardEtag: tag(boardId, boardRevision),
        boardRevision,
        cardEtag: tag(cardId, cardRevision),
        revision: cardRevision,
        commentCount: committed.size,
        writable: true,
      }),
      authorName: () => "Test",
      ticketLink: () => doc.createElement("a"),
      readWithSession: (read) => read(),
      beginWrite: () => () => {},
      onConfirmed() {},
      onStale() {},
    });
    const find = (label) => root.descendants().find((node) => node.textContent === label);
    return {
      close: () => lifetime.abort(),
      update: view.update,
      refreshSaved: view.refreshSaved,
      preview: root.descendants().find((node) => node.tag === "ol"),
      reloadPreview: find("Reload recent comments"),
      histories: root.descendants().filter((node) => node.tag === "details"),
      dismiss: find("Dismiss unresolved post"),
      retry: find("Retry original post"),
      inspect: () => find("Inspect original comment").dispatchEvent(new Event("click")),
      input: root.descendants().find((node) => node.tag === "textarea"),
      submit: () =>
        root
          .descendants()
          .find((node) => node.tag === "form")
          .dispatchEvent(new Event("submit")),
    };
  }

  return {
    journal,
    reads,
    writes,
    committed,
    advanceBoard: () => ++boardRevision,
    onSavedComments: (read) => {
      readSavedComments = read;
    },
    onSavedActivity: (read) => {
      readSavedActivity = read;
    },
    onReadOriginal: (read) => {
      readOriginalComment = read;
    },
    commitOriginal: () => commit(writes[0]),
    commitRetry: () => commit(writes[1]),
    mount,
    rejectOriginal: (status) => response.reject(new ApiError("http_error", { status })),
    onMissing: (action) => {
      afterMissing = action;
    },
    onBoardRead: (action) => {
      afterBoardRead = action;
    },
    onRetry: (action) => {
      beforeRetry = action;
    },
    submitOriginal() {
      const original = mount();
      original.input.value = text;
      original.input.dispatchEvent(new Event("input"));
      original.submit();
      assert.equal(writes.length, 1);
      return original;
    },
    async restore(rewriteJournal) {
      const original = this.submitOriginal();
      original.close();
      response.reject(new ApiError("transport_failed", { outcomeUnknown: true }));
      await setImmediate();
      if (rewriteJournal) {
        const [key, raw] = [...journal.entries()][0];
        journal.set(key, JSON.stringify(rewriteJournal(JSON.parse(raw))));
      }
      return mount();
    },
  };
}

test("a missing original at the submission revision remains fenced across remount", async () => {
  const fixture = harness();
  const restored = await fixture.restore();
  const original = fixture.writes[0].id;
  const { dispatch, ...pending } = JSON.parse([...fixture.journal.values()][0]);
  assert.match(dispatch, /^[0-9a-f-]{36}$/);
  assert.deepEqual(pending, {
    text,
    id: original,
    base: {
      boardEtag: tag(boardId, 4),
      boardRevision: 4,
      cardEtag: tag(cardId, 4),
      cardRevision: 4,
    },
  });
  restored.inspect();
  await setImmediate();
  assert.deepEqual(fixture.reads, [original, boardId, cardId]);
  assert.equal(restored.dismiss.hidden, true);
  restored.dismiss.dispatchEvent(new Event("click"));
  fixture.commitOriginal();
  restored.submit();
  assert.equal(fixture.writes.length, 1);
  restored.inspect();
  await setImmediate();
  assert.equal(fixture.journal.size, 0);
  assert.equal(fixture.committed.size, 1);
  assert.equal(restored.input.value, "");
  restored.close();
});

test("explicit retry after inspection uses exactly the journaled CAS and UUID", async () => {
  const fixture = harness();
  const restored = await fixture.restore();
  assert.equal(restored.retry.hidden, true);
  restored.retry.dispatchEvent(new Event("click"));
  assert.equal(fixture.writes.length, 1);
  restored.inspect();
  await setImmediate();
  assert.equal(restored.retry.hidden, false);
  assert.equal(fixture.writes.length, 1);
  restored.retry.dispatchEvent(new Event("click"));
  await setImmediate();
  assert.equal(fixture.writes.length, 2);
  assert.deepEqual(fixture.writes[0], fixture.writes[1]);
  assert.throws(fixture.commitOriginal, { status: 412 });
  assert.equal(fixture.journal.size, 0);
  assert.equal(fixture.committed.size, 1);
  restored.close();
});

test("a stale or refused retry keeps the original journal for reconciliation", async () => {
  for (const status of [412, 403]) {
    const fixture = harness();
    const restored = await fixture.restore();
    const before = JSON.parse([...fixture.journal.values()][0]);
    restored.inspect();
    await setImmediate();
    fixture.onRetry((_write, ErrorType) => {
      throw new ErrorType("http_error", { status });
    });
    restored.retry.dispatchEvent(new Event("click"));
    await setImmediate();
    assert.deepEqual(fixture.writes[0], fixture.writes[1]);
    const after = JSON.parse([...fixture.journal.values()][0]);
    assert.notEqual(after.dispatch, before.dispatch);
    assert.deepEqual({ ...after, dispatch: before.dispatch }, before);
    assert.equal(restored.retry.hidden, true);
    assert.equal(restored.dismiss.hidden, true);
    restored.submit();
    assert.equal(fixture.writes.length, 2);
    fixture.commitOriginal();
    restored.inspect();
    await setImmediate();
    assert.equal(fixture.journal.size, 0);
    assert.equal(fixture.committed.size, 1);
    restored.close();
  }
});

test("an older refusal cannot clear the retry journal in another document", async () => {
  for (const status of [401, 412]) {
    const fixture = harness();
    const original = fixture.submitOriginal();
    const before = JSON.parse([...fixture.journal.values()][0]);
    const restored = fixture.mount(secondDocument);
    restored.inspect();
    await setImmediate();
    assert.equal(restored.retry.hidden, false);
    fixture.onRetry((_write, ErrorType) => {
      throw new ErrorType("transport_failed", { outcomeUnknown: true });
    });
    restored.retry.dispatchEvent(new Event("click"));
    await setImmediate();
    const owned = [...fixture.journal.values()][0];
    assert.notEqual(JSON.parse(owned).dispatch, before.dispatch);
    assert.deepEqual(fixture.writes[0], fixture.writes[1]);

    // Refresh the first controller too so its mutable entry sees the retry record.
    original.update();
    fixture.rejectOriginal(status);
    await setImmediate();
    assert.equal([...fixture.journal.values()][0], owned);
    fixture.commitRetry();
    restored.submit();
    assert.equal(fixture.writes.length, 2);
    restored.inspect();
    await setImmediate();
    assert.equal(fixture.journal.size, 0);
    assert.equal(fixture.committed.size, 1);
    original.close();
    restored.close();
  }
});

test("the post-fence original-ID read catches a commit after the initial 404", async () => {
  const fixture = harness();
  const restored = await fixture.restore();
  const original = fixture.writes[0].id;
  fixture.onMissing(fixture.commitOriginal);
  restored.inspect();
  await setImmediate();
  assert.deepEqual(fixture.reads, [original, boardId, original]);
  assert.equal(restored.dismiss.hidden, true);
  assert.equal(fixture.journal.size, 0);
  assert.equal(fixture.committed.size, 1);
  restored.close();
});

test("a pre-upgrade unresolved post without recorded validators can still be confirmed", async () => {
  const fixture = harness();
  const restored = await fixture.restore(({ id }) => ({ text, id }));
  const original = fixture.writes[0].id;
  restored.inspect();
  await setImmediate();
  assert.deepEqual(fixture.reads, [original]);
  assert.equal(restored.dismiss.hidden, true);
  fixture.commitOriginal();
  restored.inspect();
  await setImmediate();
  assert.equal(fixture.journal.size, 0);
  restored.close();
});

test("dismissal requires original absence within one newer board version", async () => {
  const fixture = harness();
  const restored = await fixture.restore();
  const original = fixture.writes[0].id;
  fixture.advanceBoard();
  fixture.onBoardRead(fixture.advanceBoard);
  restored.inspect();
  await setImmediate();
  assert.equal(restored.dismiss.hidden, true);
  fixture.onBoardRead(undefined);
  fixture.reads.length = 0;
  restored.inspect();
  await setImmediate();
  assert.deepEqual(fixture.reads, [original, boardId, original, boardId]);
  assert.equal(restored.dismiss.hidden, false);
  restored.dismiss.dispatchEvent(new Event("click"));
  assert.deepEqual(JSON.parse([...fixture.journal.values()][0]), { text, id: null });
  assert.throws(fixture.commitOriginal, { status: 412 });
  restored.submit();
  await setImmediate();
  assert.equal(fixture.writes.length, 2);
  assert.notEqual(fixture.writes[1].id, original);
  assert.equal(fixture.committed.size, 1);
  restored.close();
});

test("restore refresh replaces same-count saved comments and open history without changing a draft", async () => {
  const fixture = harness();
  const earlier = "26fae956-9008-4da4-b2b6-5bddf94fe746";
  const later = "35d46372-34e8-4c63-b5c1-8fb772ab3ab4";
  const timestamp = "2026-11-13 15:00:00+00:00";
  function comment(id, prose) {
    return {
      id,
      board_id: boardId,
      card_id: cardId,
      author_id: actorId,
      created_at: timestamp,
      text: prose,
    };
  }
  function activity(revision, prose) {
    return {
      id: `01994576-cb24-7981-b2b6-5bddf94fe74${revision}`,
      board_id: boardId,
      resource_id: cardId,
      resource_type: "card",
      actor_id: actorId,
      revision,
      at: timestamp,
      operation: "patch",
      field: "description",
      before: null,
      after: prose,
    };
  }
  const before = comment(later, "Later saved comment");
  const after = comment(earlier, "Earlier saved comment");
  fixture.committed.set(later, before);
  const oldHistory = Promise.withResolvers();
  const oldActivity = Promise.withResolvers();
  const oldPreview = Promise.withResolvers();
  let oldHistorySignal;
  let oldActivitySignal;
  let oldPreviewSignal;
  let reloadPreview = false;
  let restored = false;
  fixture.onSavedComments(async (_filter, limit, _cursor, signal) => {
    if (!restored && limit !== 2) {
      oldHistorySignal = signal;
      return oldHistory.promise;
    }
    if (!restored && reloadPreview) {
      oldPreviewSignal = signal;
      return oldPreview.promise;
    }
    return { data: { comments: [restored ? after : before], next_cursor: null } };
  });
  fixture.onSavedActivity(async (_board, _card, _cursor, signal) => {
    if (!restored) {
      oldActivitySignal = signal;
      return oldActivity.promise;
    }
    return { data: { activities: [activity(3, "Earlier saved activity")], next_cursor: null } };
  });
  const current = fixture.mount();
  const [commentsHistory, activityHistory] = current.histories;
  for (const history of current.histories) {
    history.open = true;
    history.dispatchEvent(new Event("toggle"));
  }
  current.input.value = text;
  current.input.dispatchEvent(new Event("input"));
  const originalJournal = [...fixture.journal];
  await setImmediate();
  const prose = (list) => list.descendants().map((node) => node.textContent);
  assert.ok(prose(current.preview).includes("Later saved comment"));
  assert.ok(oldHistorySignal);
  assert.ok(oldActivitySignal);
  reloadPreview = true;
  current.reloadPreview.dispatchEvent(new Event("click"));
  assert.ok(oldPreviewSignal);

  restored = true;
  fixture.committed.delete(later);
  fixture.committed.set(earlier, after);
  current.refreshSaved();
  assert.deepEqual(current.preview.children, []);
  assert.equal(oldPreviewSignal.aborted, true);
  assert.equal(oldHistorySignal.aborted, true);
  assert.equal(oldActivitySignal.aborted, true);
  oldPreview.resolve({ data: { comments: [before], next_cursor: null } });
  oldHistory.resolve({ data: { comments: [before], next_cursor: null } });
  oldActivity.resolve({
    data: { activities: [activity(4, "Later saved activity")], next_cursor: null },
  });
  await setImmediate();

  assert.ok(prose(current.preview).includes("Earlier saved comment"));
  assert.ok(prose(commentsHistory).includes("Earlier saved comment"));
  assert.ok(prose(activityHistory).includes("Earlier saved activity"));
  assert.ok(!prose(current.preview).includes("Later saved comment"));
  assert.ok(!prose(commentsHistory).includes("Later saved comment"));
  assert.ok(!prose(activityHistory).includes("Later saved activity"));
  assert.deepEqual([...fixture.journal], originalJournal);
  assert.equal(current.input.value, text);
  current.close();
});

test("a pre-restore original comment read cannot clear an unresolved draft", async () => {
  const fixture = harness();
  const current = await fixture.restore();
  const preRestore = Promise.withResolvers();
  fixture.onReadOriginal(() => preRestore.promise);
  current.inspect();
  const oldReply = fixture.commitOriginal();
  const journal = [...fixture.journal];
  fixture.committed.clear();
  current.refreshSaved();
  preRestore.resolve(oldReply);
  await setImmediate();
  assert.deepEqual([...fixture.journal], journal);
  assert.equal(current.input.value, text);
  assert.equal(fixture.writes.length, 1);
  current.close();
});
