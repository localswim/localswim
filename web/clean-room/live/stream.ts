import type { Uuid } from "../api-types.js";
import { invalidResponse, isRecord, isUuid, openEventStream } from "../http.js";

const frameLimit = 4_096;

export type LiveEvent = { readonly type: "snapshot" | "change" } & (
  | { readonly resource: "workspace"; readonly revision: number }
  | { readonly resource: "catalog" }
  | { readonly resource: "board"; readonly boardId: Uuid; readonly revision: number }
);

function isRevision(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function parseEvent(text: string): LiveEvent {
  let data: unknown;
  try {
    data = JSON.parse(text);
  } catch {
    return invalidResponse();
  }
  if (!isRecord(data) || (data.type !== "snapshot" && data.type !== "change")) {
    return invalidResponse();
  }
  const { type, resource } = data;
  const fields = Object.keys(data).length;
  if (resource === "catalog" && fields === 2) {
    return { type, resource };
  }
  if (resource === "workspace" && fields === 3 && isRevision(data.workspace_revision)) {
    return { type, resource, revision: data.workspace_revision };
  }
  if (
    resource === "board" &&
    fields === 4 &&
    isUuid(data.board_id) &&
    isRevision(data.board_revision)
  ) {
    return { type, resource, boardId: data.board_id, revision: data.board_revision };
  }
  return invalidResponse();
}

function decode(decoder: TextDecoder, bytes?: Uint8Array): string {
  try {
    return decoder.decode(bytes, { stream: bytes !== undefined });
  } catch {
    return invalidResponse();
  }
}

export async function readEventStream(
  boardId: Uuid | null,
  signal: AbortSignal,
  onFrame: (event: LiveEvent) => void,
  onHeartbeat: () => void,
): Promise<void> {
  const response = await openEventStream(boardId, signal);
  const mediaType = response.headers.get("Content-Type")?.split(";", 1)[0]?.trim().toLowerCase();
  if (response.status !== 200 || mediaType !== "text/event-stream" || response.body === null) {
    void response.body?.cancel().catch(() => {});
    return invalidResponse();
  }

  // The native writer emits one JSON data line or one heartbeat comment per frame.
  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true });
  let line = "";
  let frame: string | null = null;

  function consume(chunk: string): void {
    let start = 0;
    let end = chunk.indexOf("\n");
    while (end !== -1) {
      if (line.length + end - start > frameLimit) return invalidResponse();
      line += chunk.slice(start, end);
      if (line.endsWith("\r")) line = line.slice(0, -1);
      if (line === "" && frame !== null) {
        if (frame.startsWith(":")) onHeartbeat();
        else onFrame(parseEvent(frame.slice(6)));
        frame = null;
      } else if (line !== "") {
        if (frame !== null || (!line.startsWith("data: ") && !line.startsWith(":"))) {
          return invalidResponse();
        }
        frame = line;
      }
      line = "";
      start = end + 1;
      end = chunk.indexOf("\n", start);
    }
    if (line.length + chunk.length - start > frameLimit) return invalidResponse();
    line += chunk.slice(start);
  }

  try {
    let chunk = await reader.read();
    while (!chunk.done && !signal.aborted) {
      consume(decode(decoder, chunk.value));
      chunk = await reader.read();
    }
    if (!signal.aborted) {
      consume(decode(decoder));
      if (line !== "" || frame !== null) return invalidResponse();
    }
  } finally {
    void reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
