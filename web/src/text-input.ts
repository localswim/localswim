export type InputChange = Readonly<{ start: number; end: number; inserted: string | null }>;

const recentValueCount = 8;
const recentValueMaxLength = 32_768;

export function textareaText(text: string): string {
  return text.replace(/\r\n?/g, "\n");
}

export function rememberExact(recent: Map<string, string | null>, value: string) {
  // A CRLF uses two raw code units for one textarea code unit.
  if (value.length > 2 * recentValueMaxLength) return;
  const normalized = textareaText(value);
  if (normalized.length > recentValueMaxLength) return;
  const previous = recent.get(normalized);
  recent.delete(normalized);
  recent.set(normalized, previous === undefined || previous === value ? value : null);
  if (recent.size > recentValueCount) {
    const oldest = recent.keys().next();
    if (!oldest.done) recent.delete(oldest.value);
  }
}

function rawOffset(text: string, offset: number): number {
  // Each CRLF before this boundary adds one code unit to the textarea offset.
  for (const { index } of text.matchAll(/\r\n/g)) {
    if (index >= offset) break;
    offset++;
  }
  return offset;
}

export function applyInput(previous: string, value: string, change?: InputChange): string {
  const normalized = textareaText(previous);
  let start = 0;
  let end = normalized.length;
  let inserted = value;
  const after = change ? normalized.slice(change.end) : "";
  if (
    change &&
    change.start >= 0 &&
    change.start <= change.end &&
    change.end <= normalized.length &&
    value.startsWith(normalized.slice(0, change.start)) &&
    value.endsWith(after) &&
    value.length >= change.start + after.length
  ) {
    start = change.start;
    end = change.end;
    inserted = value.slice(start, value.length - after.length);
    if (change.inserted !== null && textareaText(change.inserted) === inserted) {
      inserted = change.inserted;
    } else if (value === normalized) {
      return previous;
    }
  } else {
    if (value === normalized) return previous;
    while (start < normalized.length && normalized[start] === value[start]) start++;
    let valueEnd = value.length;
    while (end > start && valueEnd > start && normalized[end - 1] === value[valueEnd - 1]) {
      end--;
      valueEnd--;
    }
    inserted = value.slice(start, valueEnd);
  }

  // Only the changed span comes from the textarea. Its value normalizes existing CR/CRLF.
  const prefix = previous.slice(0, rawOffset(previous, start));
  const suffix = previous.slice(rawOffset(previous, end));
  // Newly adjacent CR and LF normalize as one line break in the textarea.
  return prefix + inserted + suffix;
}
