/** @typedef {{start: number, end: number, width: number}} Span */

/**
 * Pair consecutive delimiter runs of the same width on one line. A different
 * width ends an unmatched opener. Emphasis must touch its text at both ends.
 * Protect code spans before looking for stars.
 * Keep the grammar in sync with readableCardProse and the shared test cases.
 * @param {string} text @param {RegExp} delimiters @param {number} maxWidth
 * @param {Span[]} [code]
 * @returns {Span[]}
 */
function spans(text, delimiters, maxWidth, code = []) {
  /** @type {Span[]} */
  const result = [];
  let start = -1, width = 0, codeIndex = 0;
  for (const {0: run, index} of text.matchAll(delimiters)) {
    let span = code[codeIndex];
    while (span && span.end <= index) span = code[++codeIndex];
    if (span && span.start < index) continue;
    if (run === "\n" || run === "\r" || run.length > maxWidth) {
      start = -1;
      continue;
    }
    const end = index + run.length;
    const isCode = run === "`";
    if (start !== -1 && width === run.length && (isCode || !/[ \t]/.test(text.charAt(index - 1)))) {
      result.push({ start, end: index + width, width });
      start = -1;
    } else {
      start = isCode || (end < text.length && !/[ \t]/.test(text.charAt(end))) ? index : -1;
      width = run.length;
    }
  }
  return result;
}

/**
 * @param {HTMLElement} body @param {string} text
 * @param {((ticket: number, label: string) => HTMLAnchorElement)|undefined} ticketLink
 */
export function appendCardProse(body, text, ticketLink) {
  const code = spans(text, /`+|[\r\n]/g, 1);
  const emphasis = spans(text, /\*+|[\r\n]/g, 2, code);
  let codeIndex = 0;

  /** @param {HTMLElement} parent @param {string} plain */
  function appendPlain(parent, plain) {
    if (!plain) return;
    if (!ticketLink) {
      parent.append(plain);
      return;
    }
    let last = 0;
    for (const match of plain.matchAll(/(?<![\p{L}\p{N}_#])#([0-9]+)(?![\p{L}\p{N}_])/gu)) {
      const ticket = Number(match[1]);
      if (!Number.isSafeInteger(ticket) || ticket < 1) continue;
      parent.append(plain.slice(last, match.index), ticketLink(ticket, match[0]));
      last = match.index + match[0].length;
    }
    parent.append(plain.slice(last));
  }

  /** @param {HTMLElement} parent @param {number} from @param {number} to */
  function appendWithCode(parent, from, to) {
    let span = code[codeIndex];
    while (span && span.end <= to) {
      const { start, end } = span;
      appendPlain(parent, text.slice(from, start));
      const literal = body.ownerDocument.createElement("code");
      literal.textContent = text.slice(start + 1, end - 1);
      parent.append(literal);
      from = end;
      span = code[++codeIndex];
    }
    appendPlain(parent, text.slice(from, to));
  }

  let from = 0;
  for (const { start, end, width } of emphasis) {
    appendWithCode(body, from, start);
    const styled = body.ownerDocument.createElement(width === 2 ? "strong" : "em");
    appendWithCode(styled, start + width, end - width);
    body.append(styled);
    from = end;
  }
  appendWithCode(body, from, text.length);
}
