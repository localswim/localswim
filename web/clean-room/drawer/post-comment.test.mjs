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
  {
    inspectComment,
    postComment,
    readCommentDraft,
    readPendingComments,
    writeCommentDraft,
    writePendingComment,
  },
] = await Promise.all([import("../fixtures.ts"), import("./post-comment.ts")]);
hook.deregister();

void test("a lost comment response retains its identity and recovers without another POST", async (context) => {
  const { board, session } = createEntryFixture();
  assert.equal(board.status, "ready");
  const { view, boardEtag } = board.current.value;
  const card = view.cards[0];
  assert.ok(card);
  const input = {
    commentId: "58700000-0000-4000-8000-000000000001",
    requestId: "58700000-0000-4000-8000-000000000002",
    draftId: "58700000-0000-4000-8000-000000000004",
    boardId: view.board.id,
    cardId: card.id,
    authorId: session.principal_id,
    boardEtag,
    cardEtag: '"opaque_card_entity_tag"',
    text: "First line\ne\u0301",
  };
  const receipt = {
    id: input.commentId,
    board_id: input.boardId,
    card_id: input.cardId,
    author_id: input.authorId,
    text: input.text,
    created_at: card.created_at,
  };
  let posts = 0;
  let returnExactReceipt = false;
  sessionStorage.clear();
  context.after(() => sessionStorage.clear());
  context.mock.method(
    globalThis,
    "fetch",
    async (/** @type {string} */ path, /** @type {RequestInit} */ options) => {
      if (path.endsWith("/browser-session")) {
        return Response.json({
          ...session,
          access_token: Buffer.alloc(32, 5).toString("base64url"),
          expires_on_service_replacement: true,
        });
      }
      if (options.method === "POST") {
        posts += 1;
        assert.ok(path.endsWith("/comments"));
        assert.equal(new Headers(options.headers).get("X-Request-ID"), input.requestId);
        assert.deepEqual(JSON.parse(String(options.body)), {
          id: input.commentId,
          card_id: input.cardId,
          expected_card_etag: input.cardEtag,
          text: input.text,
        });
        throw new TypeError("Simulated lost response");
      }
      assert.equal(options.method, "GET");
      assert.ok(path.endsWith(`/comments/${input.commentId}`));
      const body = returnExactReceipt ? receipt : { ...receipt, text: "Different comment" };
      return Response.json(body, { headers: { ETag: '"opaque_comment_entity_tag"' } });
    },
  );

  writeCommentDraft(input.authorId, input.cardId, { id: input.draftId, text: input.text });
  const result = await postComment(input, undefined, () => writePendingComment(input));
  const newer = { id: "58700000-0000-4000-8000-000000000005", text: "" };
  writeCommentDraft(input.authorId, input.cardId, newer);
  assert.deepEqual(result, { outcome: "unknown", requestId: input.requestId, problem: null });
  assert.deepEqual(readPendingComments(input.authorId), [input]);
  assert.deepEqual(readPendingComments("58700000-0000-4000-8000-000000000003"), []);
  const restored = readPendingComments(input.authorId)[0];
  assert.ok(restored);
  assert.deepEqual(await inspectComment(restored), { outcome: "unknown" });
  returnExactReceipt = true;
  assert.deepEqual(await inspectComment(restored), {
    outcome: "committed",
    value: { comment: receipt, commentEtag: '"opaque_comment_entity_tag"' },
  });
  writePendingComment(restored, true);
  assert.deepEqual(readPendingComments(input.authorId), []);
  assert.deepEqual(readCommentDraft(input.authorId, input.cardId), newer);
  assert.equal(readCommentDraft("58700000-0000-4000-8000-000000000003", input.cardId), undefined);
  assert.equal(posts, 1);
});
