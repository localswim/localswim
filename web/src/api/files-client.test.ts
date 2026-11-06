import { BoardClient } from "./client.js";
import { FilesReadRefusal } from "./files.js";
import { fileReadMaximum } from "./files.js";
import { fileFixture, filesFixture, fileSelectionFixture } from "./files.test.js";

// In-process HTTP adapter probe. It makes no requests to a running service.
export async function runFilesClientTests(): Promise<number> {
  let checks = 0, mode = "list", generation = "connection-1", requests = 0, cancelled = false;
  const payload = new Uint8Array([0, 128, 255]);
  const sha256 = [...new Uint8Array(await crypto.subtle.digest("SHA-256", payload))].map(value => value.toString(16).padStart(2, "0")).join("");
  const fetcher: typeof fetch = async (input, init) => {
    requests++;
    const url = String(input);
    if (init?.method && init.method !== "GET" || init?.body !== undefined || init?.redirect !== "error") throw Error("Unsafe file read transport");
    if (mode === "oversize") {
      const chunk = new Uint8Array(1_048_576);
      return new Response(new ReadableStream<Uint8Array>({ pull(controller) { controller.enqueue(chunk); }, cancel() { cancelled = true; } }));
    }
    if (mode === "oversize-header") return new Response("{}", { headers: { "Content-Length": String(fileReadMaximum + 1) } });
    if (mode === "conflict") return new Response(JSON.stringify({ apiVersion: 1, outcome: "refused", conflict: null, error: { code: "REVISION_CONFLICT", field: null, message: "Synthetic" } }), { status: 409 });
    if (mode === "connection") generation = "connection-2";
    if (mode.startsWith("download")) {
      if (url.includes("/cards/")) {
        const file = fileFixture(); Object.assign(file.attachment, { byteLength: 3, sha256 });
        if (mode === "download-retained") Object.assign(file, { referenceState: "retained", downloadState: "not_current" });
        return new Response(JSON.stringify({ apiVersion: 1, scope: "card-file", result: { ...fileSelectionFixture, revision: 4, cardRevision: 3, file } }));
      }
      const headers = { "Content-Type": mode === "download-mime" ? "text/html" : "application/octet-stream", "Content-Length": "3",
        "Content-Disposition": 'attachment; filename="attachment-' + fileFixture().attachment.attachmentId + '.bin"', "X-Content-Type-Options": mode === "download-nosniff" ? "" : "nosniff" };
      return new Response(mode === "download-truncated" ? payload.slice(0, 2) : mode === "download-hash" ? new Uint8Array([0, 128, 254]) : payload, { headers });
    }
    if (mode === "inspect" || mode === "large-inspect") {
      if (url.includes("?")) throw Error("Inspect query supplied");
      const file = fileFixture();
      if (mode === "large-inspect") Object.assign(file.attribution, { actorLabel: "é".repeat(10_240) });
      return new Response(JSON.stringify({ apiVersion: 1, scope: "card-file", result: { ...fileSelectionFixture, revision: 4, cardRevision: 3, file } }));
    }
    const read = structuredClone(filesFixture());
    if (mode === "large-list") Object.assign(read.result, { items: Array.from({ length: 50 }, (_, index) => {
      const file = fileFixture(index + 4); Object.assign(file.attribution, { actorLabel: "x".repeat(5000) }); return file;
    }) });
    if (mode === "short-page") Object.assign(read.result, { nextCursor: "cursor" });
    if (!url.endsWith("/cards/" + fileSelectionFixture.cardId + "/attachments?view=current&limit=50")) throw Error("File list route mismatch");
    if (mode === "foreign") Object.assign(read.result.identity, { incarnation: "00000000-0000-4000-8000-000000000099" });
    if (mode === "unknown") Object.assign(read.result, { extra: true });
    const body = JSON.stringify(read);
    return new Response(mode === "duplicate" ? body.replace('"apiVersion":1', '"apiVersion":1,"apiVersion":1') : body,
      mode === "length-mismatch" ? { headers: { "Content-Length": String(new TextEncoder().encode(body).length + 1) } } : undefined);
  };
  const client = await BoardClient.open({ installationId: "00000000-0000-4000-8000-000000000030", profileId: "file-probe", credential: () => generation,
    transport: "local-cookie", selection: { mode: "standalone", ...fileSelectionFixture.identity, actorId: "00000000-0000-4000-8000-000000000031", grantId: "00000000-0000-4000-8000-000000000032" },
    databaseName: "localswim-file-probe-" + crypto.randomUUID(), fetch: fetcher });
  const check = (value: boolean, message: string): void => { if (!value) throw Error(message); checks++; };
  try {
    check((await client.cardFiles(fileSelectionFixture)).result.items.length === 1, "Valid list refused");
    mode = "inspect"; check((await client.cardFile(fileSelectionFixture, fileFixture().attachment.attachmentId)).result.file.attachment.name === fileFixture().attachment.name, "Exact inspect metadata changed");
    mode = "large-inspect"; check((await client.cardFile(fileSelectionFixture, fileFixture().attachment.attachmentId)).result.file.attribution.actorLabel === "é".repeat(10_240), "20 KiB legacy label changed");
    mode = "large-list"; const large = await client.cardFiles(fileSelectionFixture);
    check(large.result.items.length === 50 && large.result.items.every(file => file.attribution.actorLabel === "x".repeat(5000)), "Large legacy attribution page changed");
    mode = "short-page"; check((await client.cardFiles(fileSelectionFixture)).result.nextCursor === "cursor", "Byte-short page rejected");
    for (mode of ["foreign", "unknown", "duplicate", "oversize", "oversize-header", "length-mismatch", "connection"]) {
      let refused = false; try { await client.cardFiles(fileSelectionFixture); } catch { refused = true; }
      check(refused, "Unsafe file response accepted"); generation = "connection-1";
    }
    check(cancelled, "Oversized stream was not cancelled");
    mode = "conflict"; let conflict = false;
    try { await client.cardFiles(fileSelectionFixture); } catch (error) { conflict = error instanceof FilesReadRefusal && error.code === "REVISION_CONFLICT"; }
    check(conflict, "Stale cursor lost its explicit classification");
    const before = requests;
    for (const limit of [0, 101, 1.5]) { try { await client.cardFiles(fileSelectionFixture, "current", undefined, limit); } catch {} }
    try { await client.cardFiles(fileSelectionFixture, "current", "has padding="); } catch {}
    check(before === requests, "Malformed query sent to server");
    const facts = { ...fileSelectionFixture.identity, cardId: fileSelectionFixture.cardId, byteLength: 3, sha256 };
    mode = "download";
    check(new Uint8Array(await client.downloadAttachment(fileSelectionFixture.identity.boardId, fileFixture().attachment.attachmentId, facts, new AbortController().signal)).join() === payload.join(), "Verified bytes changed");
    const current = { ...fileFixture().attachment, byteLength: payload.byteLength, sha256 };
    const previewRequests = requests;
    const cover = await client.coverBytes(fileSelectionFixture, current, new AbortController().signal);
    check(new Uint8Array(cover).join() === payload.join() && requests === previewRequests + 1, "Cover preview must reuse the verified raw request");
    let foreignCover = false;
    try { await client.coverBytes(fileSelectionFixture, { ...current, cardId: current.attachmentId }, new AbortController().signal); }
    catch { foreignCover = true; }
    check(foreignCover && requests === previewRequests + 1, "Cross-card cover attempted a transfer");
    for (mode of ["download-retained", "download-mime", "download-nosniff", "download-truncated", "download-hash"]) {
      let refused = false;
      try { await client.downloadAttachment(fileSelectionFixture.identity.boardId, fileFixture().attachment.attachmentId, facts, new AbortController().signal); } catch { refused = true; }
      check(refused, "Ineligible or unverified bytes accepted");
    }
    mode = "download"; const cancelledDownload = new AbortController(); cancelledDownload.abort();
    let interrupted = false;
    try { await client.downloadAttachment(fileSelectionFixture.identity.boardId, fileFixture().attachment.attachmentId, facts, cancelledDownload.signal); } catch { interrupted = true; }
    check(interrupted, "Interrupted download succeeded");
    return checks;
  } finally { client.close(); }
}

// Controlled HTTPS page. Legacy bearer sessions have no preselected local member.
export async function runCoverBearerProbe(): Promise<number> {
  const payload = new Uint8Array([1, 4, 16]);
  const sha256 = [...new Uint8Array(await crypto.subtle.digest("SHA-256", payload))]
    .map(value => value.toString(16).padStart(2, "0")).join("");
  const attachment = { ...fileFixture().attachment, byteLength: payload.length, sha256 };
  let requests = 0;
  const fetcher: typeof fetch = async (input, init) => {
    if (String(input) !== "/api/v1/boards/" + fileSelectionFixture.identity.boardId + "/attachments/" + attachment.attachmentId ||
        init?.credentials !== "omit" || new Headers(init.headers).get("Authorization") !== "Bearer synthetic-probe") {
      throw Error("Unexpected preview bearer transport");
    }
    requests++;
    return new Response(payload, { headers: {
      "Content-Type": "application/octet-stream", "Content-Length": "3", "X-Content-Type-Options": "nosniff",
      "Content-Disposition": 'attachment; filename="attachment-' + attachment.attachmentId + '.bin"',
    } });
  };
  const client = await BoardClient.open({
    installationId: "00000000-0000-4000-8000-000000000030", profileId: "bearer-probe",
    credential: () => "synthetic-probe", databaseName: "localswim-bearer-probe-" + crypto.randomUUID(), fetch: fetcher,
  });
  try {
    const verified = await client.coverBytes(fileSelectionFixture, attachment, new AbortController().signal);
    if (requests !== 1 || new Uint8Array(verified).join() !== payload.join()) throw Error("Bearer preview bytes changed");
    return 2;
  } finally { client.close(); }
}
