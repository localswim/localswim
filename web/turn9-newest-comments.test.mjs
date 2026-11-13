import assert from "node:assert/strict";
import { setImmediate } from "node:timers/promises";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const { outputFiles } = await build({
  entryPoints: [fileURLToPath(new URL("./src/newest-comments.ts", import.meta.url))],
  bundle: true,
  format: "esm",
  write: false,
});
const { mountNewestComments } = await import(
  "data:text/javascript;base64," + Buffer.from(outputFiles[0].text).toString("base64")
);

class Element extends EventTarget {
  children = [];
  textContent = "";
  open = false;

  constructor(tag, document) {
    super();
    this.tag = tag;
    this.ownerDocument = document;
  }

  append(...children) {
    this.children.push(...children);
  }

  prepend(child) {
    this.children.unshift(child);
  }

  replaceChildren(...children) {
    this.children = children;
  }

  contains(target) {
    return (
      target === this ||
      this.children.some((child) => child instanceof Element && child.contains(target))
    );
  }

  setAttribute(name, value) {
    this[name] = value;
  }

  focus() {
    this.ownerDocument.activeElement = this;
  }
}

const boardId = "87b11b54-e045-4ff0-912e-a429e2ef4a66";
const cardId = "d2ef6295-ace1-4e6e-bbf7-b1c1823f56fc";
const otherCard = "d2ef6295-ace1-4e6e-bbf7-b1c1823f56fd";
const humanId = "163b1fa0-2a32-424b-8489-6bbd7761201c";
const botId = "163b1fa0-2a32-424b-8489-6bbd7761201d";
const newest = {
  id: "05909230-ec46-4693-b260-c12f92875c97",
  board_id: boardId,
  card_id: otherCard,
  author_id: humanId,
  ticket: 12,
  created_at: "2026-11-13 15:00:00+00:00",
  text: "Updated **#9**.",
};
const older = {
  ...newest,
  id: "05909230-ec46-4693-b260-c12f92875c98",
  card_id: cardId,
  author_id: botId,
  ticket: 9,
  text: "Reviewed #12.",
};
const reply = (...comments) => ({ data: { comments, next_cursor: null } });

function harness() {
  const requests = [];
  const opened = [];
  const sessionSignals = [];
  const doc = {
    activeElement: null,
    createElement: (tag) => new Element(tag, doc),
  };
  const host = doc.createElement("section");
  const lifetime = new AbortController();
  let revision = 3;
  const view = mountNewestComments(host, {
    api: {
      listComments(filter, limit, cursor, signal) {
        const response = Promise.withResolvers();
        requests.push({ filter, limit, cursor, signal, ...response });
        return response.promise;
      },
    },
    boardId,
    signal: lifetime.signal,
    readWithSession(read, signal) {
      sessionSignals.push(signal);
      return read();
    },
    context: () =>
      revision === null
        ? null
        : {
            revision,
            authorName: (id) => (id === humanId ? "Terry" : "Bot"),
            authorKind: (id) => (id === humanId ? "human" : "bot"),
          },
    ticketLink(ticket, label) {
      const link = doc.createElement("a");
      link.href = `/boards/${boardId}?ticket=${ticket}`;
      link.textContent = label;
      return link;
    },
    onOpenCard: (...args) => opened.push(args),
  });
  const [panel] = host.children;
  const [heading, status, results, retry] = panel.children;
  return {
    ...view,
    doc,
    heading,
    requests,
    opened,
    sessionSignals,
    status,
    results,
    retry,
    show(open = true) {
      panel.open = open;
      panel.dispatchEvent(new Event("toggle"));
    },
    revise(nextRevision) {
      revision = nextRevision;
      view.update();
    },
    dispose: () => lifetime.abort(),
  };
}

function click(target, modifiers = {}) {
  const event = new Event("click", { cancelable: true });
  Object.assign(event, { button: 0, ...modifiers });
  target.dispatchEvent(event);
  return event.defaultPrevented;
}

test("newest comments reads lazily across cards in server order and opens stable card links", async () => {
  const view = harness();
  assert.equal(view.requests.length, 0);
  view.show();
  const [read] = view.requests;
  assert.deepEqual(read.filter, { board_id: boardId });
  assert.equal(read.limit, 2);
  assert.equal(read.cursor, undefined);
  assert.equal(read.signal, view.sessionSignals[0]);
  assert.match(view.status.textContent, /Loading/u);
  read.resolve(reply(newest, older));
  await setImmediate();

  assert.equal(view.status.hidden, true);
  const [first, second] = view.results.children;
  const [source, byline, prose] = first.children;
  assert.equal(source.textContent, "#12");
  assert.equal(source.href, `/boards/${boardId}?card=${otherCard}`);
  assert.equal(second.children[0].href, `/boards/${boardId}?card=${cardId}`);
  assert.equal(byline.children[0], "Terry");
  assert.equal(byline.children[2].dateTime, "2026-11-13T15:00:00+00:00");
  assert.equal(prose.children.find((part) => part.tag === "strong").children[1].textContent, "#9");
  assert.deepEqual(second.children[2].children, ["Reviewed #12."]);
  assert.equal(click(source, { ctrlKey: true }), false);
  assert.equal(click(source), true);
  assert.deepEqual(view.opened, [[otherCard, source]]);
  view.dispose();
});

test("superseded, fenced, and disposed requests cannot repopulate newest comments", async () => {
  const view = harness();
  view.show();
  const [first] = view.requests;
  view.revise(4);
  const second = view.requests[1];
  assert.equal(first.signal.aborted, true);
  second.resolve(reply(older));
  first.resolve(reply(newest));
  await setImmediate();
  const [source] = view.results.children[0].children;
  assert.equal(source.href, `/boards/${boardId}?card=${cardId}`);
  source.focus();

  view.refresh();
  assert.equal(view.doc.activeElement, view.heading);
  const third = view.requests[2];
  view.revise(null);
  assert.equal(third.signal.aborted, true);
  third.resolve(reply(newest));
  await setImmediate();
  assert.equal(view.results.hidden, true);
  assert.match(view.status.textContent, /unavailable/u);
  view.revise(4);
  const fourth = view.requests[3];
  view.show(false);
  assert.equal(fourth.signal.aborted, true);
  view.show();
  const fifth = view.requests[4];
  view.dispose();
  assert.equal(fifth.signal.aborted, true);
  fifth.resolve(reply(newest));
  await setImmediate();
  assert.equal(view.results.hidden, true);
});

test("newest comments rejects malformed pages and reports failure separately from an empty retry", async () => {
  const view = harness();
  view.show();
  const invalidReplies = [
    reply(newest, { ...older, board_id: older.id }),
    reply(newest, { ...older, card_id: "#9" }),
    reply(newest, { ...older, ticket: -1 }),
    reply(newest, newest),
    reply(newest, older, { ...older, id: otherCard }),
    { data: { comments: [] } },
  ];
  for (const [index, invalid] of invalidReplies.entries()) {
    view.requests[index].resolve(invalid);
    await setImmediate();
    assert.equal(view.results.hidden, true);
    assert.equal(view.results.children.length, 0);
    assert.match(view.status.textContent, /could not be loaded/u);
    assert.equal(view.retry.hidden, false);
    click(view.retry);
  }
  view.requests.at(-1).resolve(reply());
  await setImmediate();
  assert.equal(view.status.textContent, "No comments.");
  assert.equal(view.retry.hidden, true);
  view.dispose();
});
