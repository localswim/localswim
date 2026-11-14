/// <reference types="node" />
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
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
const [{ createEntryFixture }, { createAttachmentUploads }, { readAttachmentContent }] =
  await Promise.all([
    import("../fixtures.ts"),
    import("./attachment-upload.ts"),
    import("./attachment-data.ts"),
  ]);
hook.deregister();

function boardTag(/** @type {number} */ revision) {
  return `"attachment_board_revision_${revision}"`;
}

function cardTag(/** @type {number} */ revision) {
  return `"attachment_card_revision_${revision}"`;
}

function problem(
  /** @type {number} */ status,
  /** @type {string} */ code,
  /** @type {string} */ outcome,
  /** @type {string} */ requestId,
) {
  return Response.json(
    {
      type: "about:blank",
      title: "Fixture failure",
      status,
      code,
      detail: "",
      request_id: requestId,
      errors: [],
      outcome,
    },
    {
      status,
      headers: { "X-Request-ID": requestId, "Content-Type": "application/problem+json" },
    },
  );
}

function fixture(/** @type {import("node:test").TestContext} */ context) {
  const { board, session } = createEntryFixture();
  assert.equal(board.status, "ready");
  const { view } = board.current.value;
  const card = view.cards[0];
  assert.ok(card);
  const target = { boardId: view.board.id, cardId: card.id, actorId: session.principal_id };
  /** @type {Map<string, {attachment: import("./attachment-data.ts").Attachment, bytes: ArrayBuffer}>} */
  const stored = new Map();
  /** @type {{id: string, requestId: string, boardEtag: string | null, cardEtag: FormDataEntryValue | null}[]} */
  const posts = [];
  /** @type {Map<string, string>} */
  const journal = new Map();
  const events = {
    /** @type {"normal" | "missing" | "mismatched"} */
    readback: "normal",
    /** @type {number | undefined} */
    lose: undefined,
    /** @type {number | undefined} */
    refuse: undefined,
    unsafeToSave: false,
    allowed: true,
  };
  let revision = view.board.revision;
  let cardRevision = card.revision;

  context.mock.method(
    globalThis,
    "fetch",
    async (/** @type {string} */ url, /** @type {RequestInit} */ request) => {
      const headers = new Headers(request.headers);
      const requestId = headers.get("X-Request-ID") ?? crypto.randomUUID();
      if (url.endsWith("/browser-session")) {
        return Response.json({
          ...session,
          access_token: Buffer.alloc(32, 9).toString("base64url"),
          expires_on_service_replacement: true,
        });
      }
      if (url.endsWith("/session")) return Response.json(session);
      if (url.endsWith(`/boards/${target.boardId}`)) {
        return Response.json(
          { ...view.board, revision },
          { headers: { ETag: boardTag(revision) } },
        );
      }
      if (url.endsWith(`/cards/${target.cardId}`)) {
        return Response.json(
          { ...card, revision: cardRevision, attachment_count: stored.size },
          { headers: { ETag: cardTag(cardRevision) } },
        );
      }
      if (request.method === "POST") {
        assert.ok(url.endsWith("/attachments"));
        assert.equal(headers.get("Content-Type"), null);
        assert.ok(request.body instanceof FormData);
        const id = request.body.get("id");
        const file = request.body.get("file");
        assert.ok(typeof id === "string");
        assert.ok(file instanceof File);
        assert.equal(request.body.get("card_id"), card.id);
        const boardEtag = headers.get("X-Expected-Board-ETag");
        const cardEtag = request.body.get("expected_card_etag");
        assert.equal(boardEtag, boardTag(revision));
        assert.equal(cardEtag, cardTag(cardRevision));
        assert.ok(journal.get(`localswimAttachmentUploads:${target.actorId}`)?.includes(requestId));
        posts.push({ id, requestId, boardEtag, cardEtag });
        if (events.refuse === posts.length) {
          return problem(503, "unavailable", "not_applied", requestId);
        }
        assert.equal(stored.has(id), false);
        const bytes = await file.arrayBuffer();
        revision += 1;
        cardRevision = revision;
        const attachment = {
          id,
          revision,
          created_at: card.created_at,
          updated_at: card.created_at,
          board_id: target.boardId,
          card_id: target.cardId,
          original_name: file.name,
          size: bytes.byteLength,
          sha2_256: createHash("sha256").update(new Uint8Array(bytes)).digest("hex"),
          media_type: "text/plain; charset=utf-8",
          previewable: false,
          active: true,
          added_by: target.actorId,
        };
        stored.set(id, { attachment, bytes });
        if (events.lose === posts.length) throw new TypeError("Simulated response loss");
        return Response.json(attachment, {
          status: 201,
          headers: {
            ETag: '"opaque_attachment_entity_tag"',
            "X-Board-ETag": boardTag(revision),
            "X-Request-ID": requestId,
            Location: `${url}/${id}`,
          },
        });
      }
      for (const [id, { attachment, bytes }] of stored) {
        if (url.endsWith(`/${id}/content?disposition=attachment`)) {
          return new Response(bytes, {
            headers: {
              "Content-Type": "application/octet-stream",
              "Content-Length": String(bytes.byteLength),
              "Content-Disposition": 'attachment; filename="test.txt"',
              ETag: '"opaque_attachment_entity_tag"',
            },
          });
        }
        if (!url.endsWith(`/attachments/${id}`) || events.readback === "missing") continue;
        const data =
          events.readback === "mismatched"
            ? { ...attachment, original_name: "different.txt" }
            : attachment;
        return Response.json(data, { headers: { ETag: '"opaque_attachment_entity_tag"' } });
      }
      return problem(404, "not_found", "not_applicable", requestId);
    },
  );
  /** @type {import("./attachment-data.ts").Attachment[]} */
  const committed = [];
  /** @type {string[]} */
  const transitions = [];
  const newController = () => {
    const uploads = createAttachmentUploads({
      canDispatch: () => events.allowed,
      onChange: () => {
        const row = uploads.rows(target).find(({ status }) => status === "uploading");
        assert.equal(uploads.inFlightBoard(target.boardId, target.actorId), row !== undefined);
        if (row) transitions.push(row.id);
      },
      onCommitted: (actualTarget, attachment) => {
        assert.deepEqual(actualTarget, target);
        committed.push(attachment);
      },
      storage: {
        getItem: (key) => journal.get(key) ?? null,
        setItem: (key, value) => {
          if (events.unsafeToSave) throw new Error("Simulated storage failure");
          journal.set(key, value);
        },
        removeItem: (key) => journal.delete(key),
      },
    });
    return uploads;
  };
  return { newController, target, events, posts, stored, journal, committed, transitions };
}

void test("recover a partial batch by original UUID, verify persisted bytes, and resume after reselecting the exact file", async (context) => {
  const { newController, target, events, posts, stored, journal, committed, transitions } =
    fixture(context);
  const uploads = newController();
  const source = [
    new File(["first"], "first.txt"),
    new File(["second"], "seco\u0301nd.txt"),
    new File([new Uint8Array([0, 119, 255, 64])], "third.bin"),
  ];
  await uploads.stage(target, source);
  assert.deepEqual(
    uploads.rows(target).map(({ status }) => status),
    ["ready", "ready", "ready"],
  );
  const ids = uploads.rows(target).map(({ id }) => id);
  assert.equal(posts.length, 0);
  events.lose = 2;
  await Promise.all([uploads.send(target), uploads.send(target)]);
  assert.deepEqual(
    uploads.rows(target).map(({ status }) => status),
    ["committed", "unknown", "ready"],
  );
  assert.deepEqual(
    posts.map(({ id }) => id),
    ids.slice(0, 2),
  );
  assert.deepEqual(transitions, ids.slice(0, 2));
  assert.equal(uploads.inFlight(target), false);
  assert.equal(committed.length, 1);
  assert.equal(stored.size, 2);

  const restored = newController();
  assert.deepEqual(restored.targets(target.actorId), [target]);
  assert.deepEqual(restored.targets(crypto.randomUUID()), []);
  assert.deepEqual(
    restored.rows(target).map(({ status }) => status),
    ["committed", "unknown", "needs-file"],
  );
  const [firstId, uncertainId, unsentId] = ids;
  assert.ok(firstId && uncertainId && unsentId);
  restored.remove(target, uncertainId);
  restored.discard(target);
  const third = source[2];
  assert.ok(third);
  await restored.reselect(target, unsentId, new File([new Uint8Array(4)], third.name));
  assert.equal(restored.rows(target)[2]?.status, "needs-file");
  await restored.reselect(target, unsentId, third);
  assert.equal(restored.rows(target)[2]?.status, "ready");
  assert.equal(restored.rows(target)[2]?.canRetry, false);
  await restored.send(target, unsentId);
  events.readback = "missing";
  await restored.inspect(target, uncertainId);
  assert.equal(restored.rows(target)[1]?.status, "unknown");
  events.readback = "mismatched";
  await restored.inspect(target, uncertainId);
  assert.equal(restored.rows(target)[1]?.status, "unknown");
  assert.equal(posts.length, 2);
  events.readback = "normal";
  await restored.inspect(target, uncertainId);
  assert.equal(restored.rows(target)[1]?.status, "committed");
  assert.equal(restored.rows(target)[2]?.canRetry, true);
  assert.equal(posts.length, 2);
  await restored.send(target, unsentId);
  assert.deepEqual(
    posts.map(({ id }) => id),
    ids,
  );
  assert.deepEqual(
    committed.map(({ id }) => id),
    ids,
  );
  assert.equal(new Set(posts.map(({ requestId }) => requestId)).size, 3);
  assert.equal(journal.size, 0);
  for (const [index, attachment] of committed.entries()) {
    const original = source[index];
    assert.ok(original);
    const content = await readAttachmentContent(attachment, "attachment");
    assert.deepEqual(await content.arrayBuffer(), await original.arrayBuffer());
    assert.equal(attachment.original_name, original.name);
  }
});

void test("a validated unavailable refusal is retryable with the same IDs only after an explicit submission", async (context) => {
  const { newController, target, events, posts, journal } = fixture(context);
  const uploads = newController();
  const file = new File([""], "empty.txt");
  await uploads.stage(target, [file]);
  events.refuse = 1;
  await uploads.send(target);
  assert.equal(uploads.rows(target)[0]?.status, "refused");
  const row = uploads.rows(target)[0];
  assert.ok(row);
  await uploads.send(target);
  assert.equal(posts.length, 1);
  const restored = newController();
  assert.equal(restored.rows(target)[0]?.status, "needs-file");
  await restored.reselect(target, row.id, file);
  assert.equal(restored.rows(target)[0]?.status, "refused");
  await restored.send(target, row.id);
  assert.equal(posts.length, 2);
  assert.deepEqual(posts[1], posts[0]);
  assert.equal(restored.rows(target)[0]?.status, "committed");
  assert.equal(journal.size, 0);
});

void test("journal failures, canceled preparation, and changed originals cannot dispatch", async (context) => {
  const { newController, target, events, posts, journal } = fixture(context);
  const uploads = newController();
  const original = new File(["original"], "test.txt");
  const staging = uploads.stage(target, [original]);
  uploads.discard(target);
  await staging;
  assert.deepEqual(uploads.rows(target), []);
  assert.equal(journal.size, 0);

  await uploads.stage(target, [original]);
  events.unsafeToSave = true;
  await uploads.send(target);
  assert.equal(uploads.rows(target)[0]?.status, "ready");
  assert.match(uploads.rows(target)[0]?.message ?? "", /could not be saved/u);
  assert.equal(posts.length, 0);
  events.unsafeToSave = false;
  const restored = newController();
  const row = restored.rows(target)[0];
  assert.equal(row?.status, "needs-file");
  assert.ok(row);
  const changed = new File(["original"], "test.txt");
  await restored.reselect(target, row.id, changed);
  context.mock.method(
    changed,
    "arrayBuffer",
    async () => new TextEncoder().encode("changed!").buffer,
  );
  await restored.send(target);
  assert.equal(restored.rows(target)[0]?.status, "needs-file");
  assert.equal(posts.length, 0);
});

void test("unsubmitted create files can be removed with storage blocked and retained files return to draft", async (context) => {
  const { newController, target, events, journal, posts } = fixture(context);
  const uploads = newController();
  const file = new File(["draft"], "draft.txt");
  events.unsafeToSave = true;
  await uploads.stage(target, [file], true);
  const row = uploads.rows(target)[0];
  assert.ok(row);
  assert.equal(uploads.retain(target), false);
  uploads.remove(target, row.id);
  assert.deepEqual(uploads.rows(target), []);
  await uploads.stage(target, [file], true);
  uploads.discard(target);
  assert.deepEqual(uploads.rows(target), []);
  events.unsafeToSave = false;
  await uploads.stage(target, [file], true);
  assert.equal(journal.size, 0);
  assert.equal(uploads.retain(target), true);
  assert.equal(journal.size, 1);
  uploads.releaseToDraft(target);
  assert.equal(journal.size, 0);
  assert.equal(uploads.rows(target)[0]?.status, "ready");
  assert.equal(posts.length, 0);
});
