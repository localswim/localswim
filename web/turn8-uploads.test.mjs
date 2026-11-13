import assert from "node:assert/strict";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
  stdin: {
    contents: `export * from "./src/uploads.ts"; export { ApiError } from "./api.mjs";`,
    resolveDir: fileURLToPath(new URL(".", import.meta.url)),
  },
  bundle: true,
  write: false,
  format: "esm",
});
const { mountUploads, hasPendingUpload, hasPendingBoardUploads, ApiError } = await import(
  "data:text/javascript;base64," + Buffer.from(compiled.outputFiles[0].text).toString("base64")
);

class Element extends EventTarget {
  children = [];

  append(...children) {
    this.children.push(...children);
  }
  replaceChildren(...children) {
    this.children = children;
  }
  setAttribute() {}
  closest() {
    return null;
  }
  click() {
    if (!this.hidden && !this.disabled) this.dispatchEvent(new Event("click"));
  }
}

const board = "81b11b54-e045-4ff0-912e-a429e2ef4a66";
const first = "82b11b54-e045-4ff0-912e-a429e2ef4a66";
const second = "83b11b54-e045-4ff0-912e-a429e2ef4a66";
const revisionTag = (id, revision) => `"${id}-${revision}"`;

test("the board fence finds unresolved or malformed upload records only on that board", () => {
  const otherBoard = "84b11b54-e045-4ff0-912e-a429e2ef4a66";
  const scoped = `localswim:v2:uploads:${board}:`;
  const values = new Map([
    ["unrelated", "unreadable"],
    [`localswim:v2:uploads:${otherBoard}:invalid`, "unreadable"],
  ]);
  const storage = {
    get length() {
      return values.size;
    },
    key: (index) => [...values.keys()][index] ?? null,
    getItem: (entry) => values.get(entry) ?? null,
  };
  const journal = {
    remaining: [{ id: second, name: "original.txt", size: 0, digest: "a".repeat(64) }],
    expected: null,
  };
  assert.equal(hasPendingBoardUploads(board, storage), false);
  values.set(scoped + first, JSON.stringify(journal));
  assert.equal(hasPendingBoardUploads(board, storage), false);
  journal.expected = revisionTag(first, 3);
  values.set(scoped + first, JSON.stringify(journal));
  assert.equal(hasPendingBoardUploads(board, storage), true);
  values.set(scoped + first, "unreadable");
  assert.equal(hasPendingBoardUploads(board, storage), true);
  values.delete(scoped + first);
  values.set(scoped + "invalid", "unreadable");
  assert.equal(hasPendingBoardUploads(board, storage), true);
});

function harness(api, values = new Map(), awaitUploadAdmission) {
  const life = new AbortController();
  const host = new Element();
  const pasteTarget = new Element();
  host.ownerDocument = { createElement: () => new Element() };
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  };
  const leased = new Set();
  let changes = 0;
  function begin(cardId) {
    if (leased.has(cardId)) return null;
    leased.add(cardId);
    return () => leased.delete(cardId);
  }
  const controller = mountUploads(host, {
    api,
    boardId: board,
    signal: life.signal,
    pasteTarget,
    canUpload: () => true,
    awaitUploadAdmission,
    beginWrite: begin,
    beginInspect: begin,
    readWithSession: (read) => read(),
    onChange: () => changes++,
    storage,
  });
  const [label, upload, inspect, status, outcomes, resume, discard] = host.children;
  const [picker] = label.children;
  function selectFiles(files) {
    picker.files = files;
    picker.dispatchEvent(new Event("change"));
  }
  return {
    ...controller,
    leased,
    storage,
    inspect,
    status,
    outcomes,
    picker,
    resume,
    discard,
    upload,
    changes: () => changes,
    select(cardId, revision = 3, readable = true) {
      controller.show({
        cardId,
        etag: revisionTag(cardId, revision),
        readable,
        editable: readable,
      });
    },
    pick(...files) {
      selectFiles(files);
      upload.click();
    },
    reselect(...files) {
      selectFiles(files);
      resume.click();
    },
    selectFiles(...files) {
      selectFiles(files);
    },
    paste(...files) {
      const event = new Event("paste", { cancelable: true });
      event.clipboardData = { files, types: ["Files"] };
      pasteTarget.dispatchEvent(event);
      return event.defaultPrevented;
    },
  };
}

function journal(values) {
  const [raw] = values.values();
  return JSON.parse(raw);
}

function attachment(cardId, item, revision = 4) {
  return {
    etag: revisionTag(item.id, revision),
    data: {
      id: item.id,
      board_id: board,
      card_id: cardId,
      original_name: item.name,
      size: item.size,
      sha2_256: item.digest,
      active: true,
      revision,
    },
  };
}

async function until(predicate) {
  const deadline = performance.now() + 2000;
  while (performance.now() < deadline) {
    if (predicate()) return;
    await delay(5);
  }
  assert.fail("Upload did not reach the expected state.");
}

test("picker skips oversized files, journals exact originals, and continues on the original card", async () => {
  const stored = new Map();
  const dispatched = [];
  const firstUpload = Promise.withResolvers();
  const ui = harness(
    {
      uploadAttachment(cardId, expected, file, id) {
        const saved = journal(stored);
        const [item] = saved.remaining;
        assert.equal(id, item.id);
        assert.equal(expected, saved.expected);
        assert.equal(file.name, item.name);
        assert.equal(file.size, item.size);
        dispatched.push({ cardId, expected, item });
        if (dispatched.length === 1) return firstUpload.promise;
        return Promise.resolve(attachment(cardId, item, 8));
      },
      getCard(cardId) {
        assert.equal(cardId, first);
        return Promise.resolve({
          etag: revisionTag(first, 7),
          data: { id: first, board_id: board, revision: 7 },
        });
      },
    },
    stored,
  );
  const text = new File(["abc"], "text.bin");
  const tooLarge = { name: "large.bin", size: 10 * 1024 * 1024 + 1 };
  ui.select(first);
  ui.pick(text, tooLarge, new File(["two"], "two.bin"));
  await until(() => dispatched.length === 1);
  const [sent] = dispatched;
  assert.equal(
    sent.item.digest,
    "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
  );
  assert.deepEqual(
    journal(stored).remaining.map(({ name }) => name),
    ["text.bin", "two.bin"],
  );
  assert.equal(ui.blocksCard(first), true);
  assert.equal(ui.blocksCard(second), false);
  assert.deepEqual([...ui.leased], [first]);
  ui.select(second);
  firstUpload.resolve(attachment(first, sent.item, 7));
  await until(() => !ui.leased.size);
  assert.equal(ui.outcomes.children.length, 0);
  assert.deepEqual(
    dispatched.map(({ cardId, expected }) => [cardId, expected]),
    [
      [first, revisionTag(first, 3)],
      [first, revisionTag(first, 7)],
    ],
  );
  assert.equal(stored.size, 0);
  assert.equal(ui.changes(), 1);
  ui.select(first, 8);
  assert.deepEqual(
    ui.outcomes.children.map(({ textContent }) => textContent),
    [
      "text.bin — Uploaded.",
      "large.bin — Refused. Files are limited to 10 MiB.",
      "two.bin — Uploaded.",
    ],
  );
});

test("raster paste stops on ambiguous response and inspects the original without replay", async (t) => {
  Object.defineProperty(globalThis, "Element", { configurable: true, value: Element });
  t.after(() => Reflect.deleteProperty(globalThis, "Element"));
  const stored = new Map();
  let attempt;
  let response;
  let posted = 0;
  const ui = harness(
    {
      uploadAttachment() {
        posted++;
        [attempt] = journal(stored).remaining;
        return Promise.reject(new ApiError("transport_failed", { outcomeUnknown: true }));
      },
    },
    stored,
  );
  ui.select(first);
  assert.equal(ui.paste(new File(["svg"], "image.svg", { type: "image/svg+xml" })), false);
  assert.equal(
    ui.paste(
      new File(["png"], "screenshot.png", { type: "image/png" }),
      new File(["binary"], "ignored.bin", { type: "application/octet-stream" }),
      new File(["gif"], "other.gif", { type: "image/gif" }),
    ),
    true,
  );
  await until(() => !ui.leased.size);
  assert.equal(hasPendingUpload(board, first, ui.storage), true);
  assert.equal(ui.inspect.hidden, false);
  assert.deepEqual(
    journal(stored).remaining.map(({ name }) => name),
    ["screenshot.png", "other.gif"],
  );
  ui.dispose();
  const reopened = harness(
    {
      getAttachment(id) {
        assert.equal(id, attempt.id);
        return response
          ? Promise.resolve(response)
          : Promise.reject(new ApiError("http_error", { status: 404 }));
      },
    },
    stored,
  );
  reopened.select(first);

  reopened.inspect.click();
  await until(() => !reopened.leased.size);
  assert.match(reopened.status.textContent, /still uncertain/);
  assert.equal(reopened.blocksCard(first), true);

  response = attachment(second, attempt);
  reopened.inspect.click();
  await until(() => !reopened.leased.size);
  assert.match(reopened.status.textContent, /could not be verified/);
  assert.equal(reopened.blocksCard(first), true);

  response = attachment(first, attempt);
  response.data.original_name = "another-name.png";
  reopened.inspect.click();
  await until(() => !reopened.leased.size);
  assert.match(reopened.status.textContent, /could not be verified/);
  assert.equal(reopened.blocksCard(first), true);

  response = attachment(first, attempt);
  reopened.inspect.click();
  await until(() => !reopened.leased.size);
  assert.equal(reopened.blocksCard(first), false);
  assert.equal(reopened.inspect.hidden, true);
  assert.equal(posted, 1);
  assert.equal(reopened.changes(), 1);
  assert.match(reopened.outcomes.children[0].textContent, /Confirmed by inspection/);
});

test("an intervening card write stops a batch while preserving confirmed file results", async () => {
  const stored = new Map();
  let posted = 0;
  const ui = harness(
    {
      uploadAttachment(cardId) {
        posted++;
        const [item] = journal(stored).remaining;
        return Promise.resolve(attachment(cardId, item));
      },
      getCard() {
        return Promise.resolve({
          etag: revisionTag(first, 5),
          data: { id: first, board_id: board, revision: 5 },
        });
      },
    },
    stored,
  );
  ui.select(first);
  ui.pick(new File(["one"], "one.bin"), new File(["two"], "two.bin"));
  await until(() => !ui.leased.size);
  assert.equal(posted, 1);
  assert.equal(ui.blocksCard(first), false);
  assert.equal(ui.changes(), 1);
  assert.match(ui.outcomes.children[0].textContent, /Uploaded/);
  assert.match(ui.outcomes.children[1].textContent, /stopped/);
});

test("a failed fresh admission stops subsequent dispatches", async () => {
  const stored = new Map();
  const events = [];
  const permission = Promise.withResolvers();
  const ui = harness(
    {
      uploadAttachment(cardId) {
        events.push("posted");
        const [item] = journal(stored).remaining;
        return Promise.resolve(attachment(cardId, item));
      },
      getCard() {
        events.push("checked card revision");
        return Promise.resolve({
          etag: revisionTag(first, 4),
          data: { id: first, board_id: board, revision: 4 },
        });
      },
    },
    stored,
    (signal, confirmedCardRevision) => {
      assert.equal(signal.aborted, false);
      assert.equal(confirmedCardRevision, 4);
      events.push("awaiting write access");
      return permission.promise;
    },
  );
  ui.select(first);
  ui.pick(new File(["one"], "one.bin"), new File(["two"], "two.bin"));
  await until(() => events.includes("awaiting write access"));
  assert.deepEqual(events, ["posted", "checked card revision", "awaiting write access"]);
  permission.resolve(false);
  await until(() => !ui.leased.size);
  assert.equal(events.filter((event) => event === "posted").length, 1);
  assert.equal(ui.blocksCard(first), false);
  assert.match(ui.status.textContent, /write access could not be confirmed/);
});

test("unavailable journal storage stops the batch before sending any bytes", async () => {
  let posted = 0;
  const ui = harness({
    uploadAttachment() {
      posted++;
    },
  });
  ui.storage.setItem = () => {
    throw new Error("Storage full");
  };
  ui.select(first);
  ui.pick(new File(["one"], "one.bin"));
  await until(() => !ui.leased.size);
  assert.equal(posted, 0);
  assert.match(ui.status.textContent, /could not be verified/);
});

test("upload settlement distinguishes a literal percent sequence from an escaped name", async () => {
  const stored = new Map();
  let inspection;
  const ui = harness(
    {
      uploadAttachment(cardId) {
        const [item] = journal(stored).remaining;
        const response = attachment(cardId, item);
        if (item.name === "daily%22report.txt") response.data.original_name = 'daily"report.txt';
        return Promise.resolve(response);
      },
      getAttachment() {
        return Promise.resolve(inspection);
      },
    },
    stored,
  );
  ui.select(first);
  ui.pick(new File(["one"], 'daily"report.txt'));
  await until(() => !ui.leased.size);
  assert.equal(ui.blocksCard(first), false);
  assert.equal(ui.outcomes.children[0].textContent, 'daily"report.txt — Uploaded.');

  ui.select(first, 4);
  ui.pick(new File(["two"], "daily%22report.txt"));
  await until(() => !ui.leased.size);
  assert.equal(ui.blocksCard(first), true);
  assert.match(ui.outcomes.children[0].textContent, /Outcome uncertain/);

  const [original] = journal(stored).remaining;
  inspection = attachment(first, original, 5);
  inspection.data.original_name = 'daily"report.txt';
  ui.inspect.click();
  await until(() => !ui.leased.size);
  assert.equal(ui.blocksCard(first), true);

  inspection = attachment(first, original, 5);
  ui.inspect.click();
  await until(() => !ui.leased.size);
  assert.equal(ui.blocksCard(first), false);
  assert.match(ui.outcomes.children[0].textContent, /Confirmed by inspection/);
});

test("reload recovers a partially completed batch using the remaining original IDs in order", async () => {
  const stored = new Map();
  const sent = [];
  const reads = [];
  const admitted = [];
  const committed = new Map();
  let revision = 3;
  let revealPending = false;
  const api = {
    uploadAttachment(cardId, expected, file, id) {
      const saved = journal(stored);
      const [item] = saved.remaining;
      assert.equal(item.id, id);
      assert.equal(item.name, file.name);
      assert.equal(saved.expected, revisionTag(first, revision));
      assert.equal(expected, saved.expected);
      sent.push(id);
      if (sent.length === 2) {
        return Promise.reject(new ApiError("transport_failed", { outcomeUnknown: true }));
      }
      const reply = attachment(cardId, item, ++revision);
      committed.set(id, reply);
      return Promise.resolve(reply);
    },
    getAttachment(id) {
      reads.push(id);
      if (!revealPending) return Promise.reject(new ApiError("http_error", { status: 404 }));
      return Promise.resolve(committed.get(id));
    },
    getCard() {
      return Promise.resolve({
        data: { id: first, board_id: board, revision },
        etag: revisionTag(first, revision),
      });
    },
  };
  const admission = (_signal, current) => {
    admitted.push(current);
    return Promise.resolve(true);
  };
  const initial = harness(api, stored, admission);
  const third = new File(["one"], "same.txt");
  const fourth = new File(["two"], "same.txt");
  initial.select(first);
  initial.pick(
    new File(["complete"], "complete.txt"),
    new File(["pending"], "pending.txt"),
    third,
    fourth,
  );
  await until(() => !initial.leased.size);
  const [uncertain, ...remaining] = journal(stored).remaining;
  assert.equal(uncertain.id, sent[1]);
  assert.equal(committed.has(sent[0]), true);
  assert.equal(stored.size, 1);
  initial.dispose();

  const reopened = harness(api, stored, admission);
  reopened.select(first, revision);
  assert.equal(reopened.picker.disabled, true);
  assert.equal(reopened.resume.hidden, true);
  assert.equal(reopened.discard.hidden, true);
  reopened.inspect.click();
  await until(() => !reopened.leased.size);
  assert.match(reopened.status.textContent, /still uncertain/);
  assert.deepEqual(journal(stored).remaining, [uncertain, ...remaining]);
  assert.deepEqual(reads, [uncertain.id]);
  assert.equal(sent.length, 2);
  assert.equal(reopened.blocksCard(first), true);
  reopened.select(second);
  assert.equal(reopened.upload.hidden, false);
  assert.equal(reopened.blocksCard(second), false);

  revealPending = true;
  committed.set(uncertain.id, attachment(first, uncertain, ++revision));
  reopened.select(first, revision);
  reopened.inspect.click();
  await until(() => !reopened.leased.size);
  assert.deepEqual(reads, [uncertain.id, uncertain.id]);
  assert.deepEqual(journal(stored), { remaining, expected: null });
  assert.equal(reopened.blocksCard(first), false);
  assert.equal(reopened.discard.hidden, false);
  assert.equal(reopened.resume.hidden, false);
  assert.equal(reopened.upload.hidden, true);

  reopened.reselect(third, new File(["bad"], fourth.name));
  await until(() => !reopened.leased.size);
  assert.match(reopened.status.textContent, /recorded names, sizes and contents/);
  assert.deepEqual(journal(stored), { remaining, expected: null });
  assert.equal(sent.length, 2);

  reopened.reselect(fourth, third);
  await until(() => !reopened.leased.size);
  assert.deepEqual(
    sent.slice(2),
    remaining.map(({ id }) => id),
  );
  assert.deepEqual(admitted, [4, 5, 6]);
  assert.equal(new Set(sent).size, 4);
  assert.equal(committed.size, 4);
  assert.equal(stored.size, 0);
  assert.equal(reopened.resume.hidden, true);
  assert.equal(reopened.discard.hidden, true);
  assert.equal(reopened.upload.hidden, false);
});

test("discard after inspection clears only the remaining recovery details", async () => {
  const stored = new Map();
  const accepted = new Map();
  const api = {
    uploadAttachment(cardId) {
      const [item] = journal(stored).remaining;
      accepted.set(item.id, attachment(cardId, item));
      return Promise.reject(new ApiError("transport_failed", { outcomeUnknown: true }));
    },
    getAttachment(id) {
      return Promise.resolve(accepted.get(id));
    },
  };
  const initial = harness(api, stored);
  initial.select(first);
  initial.pick(new File(["one"], "one.bin"), new File(["two"], "two.bin"));
  await until(() => !initial.leased.size);
  initial.dispose();

  const reopened = harness(api, stored);
  reopened.select(first, 4);
  reopened.discard.dispatchEvent(new Event("click"));
  assert.equal(stored.size, 1);
  reopened.inspect.click();
  await until(() => !reopened.leased.size);
  reopened.discard.click();
  assert.equal(stored.size, 0);
  assert.equal(accepted.size, 1);
  assert.equal(reopened.changes(), 1);
  assert.match(reopened.status.textContent, /discarded/);
  assert.deepEqual(
    reopened.outcomes.children.map(({ textContent }) => textContent),
    ["one.bin — Uploaded. Confirmed by inspection."],
  );
});

test("an oversized batch stops before hashing or dispatch", () => {
  const stored = new Map();
  const ui = harness({}, stored);
  ui.select(first);
  ui.pick(...Array.from({ length: 65 }, () => ({ name: "one.bin", size: 3 })));
  assert.equal(ui.leased.size, 0);
  assert.equal(stored.size, 0);
  assert.match(ui.status.textContent, /at most 64 files/);
});

test("same-card admission refresh preserves the picker and resumed upload", async () => {
  const original = {
    id: second,
    name: "same.txt",
    size: 3,
    digest: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
  };
  const key = `localswim:v2:uploads:${board}:${first}`;
  const raw = JSON.stringify({ remaining: [original], expected: null });
  const stored = new Map([[key, raw]]);
  const admissions = [];
  const sent = [];
  const ui = harness(
    {
      getCard() {
        return Promise.resolve({
          data: { id: first, board_id: board, revision: 3 },
          etag: revisionTag(first, 3),
        });
      },
      uploadAttachment(cardId, expected, _file, id) {
        sent.push({ cardId, expected, id });
        return Promise.resolve(attachment(cardId, original));
      },
    },
    stored,
    (signal) => {
      const permission = Promise.withResolvers();
      ui.select(first, 3, false);
      admissions.push({ signal, permission });
      return permission.promise;
    },
  );
  const file = new File(["abc"], original.name);
  ui.select(first);
  ui.selectFiles(file);
  ui.select(first, 3, false);
  assert.equal(ui.resume.disabled, true);
  ui.select(first);
  assert.equal(ui.resume.disabled, false);
  ui.resume.click();
  await until(() => admissions.length === 1);
  assert.equal(admissions[0].signal.aborted, false);
  ui.select(second);
  assert.equal(admissions[0].signal.aborted, true);
  admissions[0].permission.resolve(true);
  await until(() => !ui.leased.size);
  assert.deepEqual(sent, []);
  assert.equal(stored.get(key), raw);

  ui.select(first);
  ui.reselect(file);
  await until(() => admissions.length === 2);
  assert.equal(admissions[1].signal.aborted, false);
  ui.select(first);
  admissions[1].permission.resolve(true);
  await until(() => !ui.leased.size);
  assert.deepEqual(sent, [{ cardId: first, expected: revisionTag(first, 3), id: original.id }]);
  assert.equal(stored.size, 0);
});
