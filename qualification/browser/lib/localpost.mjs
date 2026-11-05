// Development-only transport to the shared volatile Valkey/Webdis queue.
import { randomUUID } from 'node:crypto';

const uuid4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const streamId = /^(0|[1-9][0-9]*)-(0|[1-9][0-9]*)$/;
// The receipt schema has exactly one string-valued member. Check its syntax
// before JSON.parse can collapse duplicate (including escaped) member names.
const singleStringMember = /^\s*\{\s*"(?:\\[\s\S]|[^"\\])*"\s*:\s*"(?:\\[\s\S]|[^"\\])*"\s*\}\s*$/;
const maxUint64 = 18446744073709551615n;
const kinds = new Set(['case', 'page', 'request', 'console']);
const phases = new Set(['start', 'complete', 'error']);
const codes = new Set(['OK', 'CASE_FAILED', 'CASE_SKIPPED', 'PAGE_ERROR', 'REQUEST_FAILED', 'CONSOLE_ERROR']);
const maxMessages = 64, messageLimit = 1024, receiptLimit = 2048;

function endpoint(value) {
  // Check the original spelling before URL parsing can normalize paths or hosts.
  if (typeof value !== 'string' ||
      !/^http:\/\/(127\.0\.0\.1|\[::1\])(?::([0-9]+))?\/?$/.test(value)) return null;
  try {
    const port = /^http:\/\/(?:127\.0\.0\.1|\[::1\])(?::([0-9]+))?\/?$/.exec(value)[1];
    if (port && (Number(port) < 1 || Number(port) > 65535)) return null;
    return new URL(value).origin;
  } catch { return null; }
}

function validStreamId(value) {
  if (typeof value !== 'string' || value.length > 41) return false;
  const parts = streamId.exec(value);
  return Boolean(parts && BigInt(parts[1]) <= maxUint64 && BigInt(parts[2]) <= maxUint64);
}

export function projectDiagnostic(event, runId, sequence, caseIds) {
  try {
    if (!event || typeof event !== 'object' || Array.isArray(event) ||
        Object.keys(event).sort().join() !== 'caseId,code,kind,phase') return null;
    const { caseId, kind, phase, code } = event;
    if (typeof runId !== 'string' || !uuid4.test(runId) || !Number.isInteger(sequence) || sequence < 1 || sequence > maxMessages ||
        !caseIds.has(caseId) || !kinds.has(kind) || !phases.has(phase) || !codes.has(code)) return null;
    // Construct a new fixed object. Never serialize browser errors, URLs or prose.
    return { schemaVersion: 1, source: 'localswim-v2-browser', runId, sequence, caseId, kind, phase, code };
  } catch { return null; }
}

async function receipt(response) {
  if (response.status !== 200) { await response.body?.cancel(); return false; }
  const reader = response.body?.getReader();
  if (!reader) return false;
  let size = 0; const chunks = [];
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > receiptLimit) { await reader.cancel(); return false; }
      chunks.push(value);
    }
    const bytes = new Uint8Array(size); let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
    const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
    if (!singleStringMember.test(text)) return false;
    const value = JSON.parse(text);
    return value !== null && typeof value === 'object' && !Array.isArray(value) &&
      Object.keys(value).join() === 'XADD' && validStreamId(value.XADD);
  } finally { reader.releaseLock(); }
}

export function createLocalpostDiagnostics({
  registrationId = process.env.LOCALPOST_REGISTRATION_ID,
  serviceUrl = process.env.LOCALPOST_URL ?? 'http://127.0.0.1:53147',
  runId = process.env.LOCALPOST_RUN_ID ?? randomUUID(), caseIds = [], fetcher = globalThis.fetch,
} = {}) {
  const allowed = new Set((Array.isArray(caseIds) ? caseIds : []).filter(id => typeof id === 'string' && /^V02-[A-Z]+-\d{2}$/.test(id)));
  const baseUrl = endpoint(serviceUrl);
  const enabled = Boolean(baseUrl && typeof registrationId === 'string' && uuid4.test(registrationId) &&
    typeof runId === 'string' && uuid4.test(runId) && allowed.size && typeof fetcher === 'function');
  let accepting = true, offline = false, sequence = 0, delivered = 0, rejected = 0, failed = 0;
  let tail = Promise.resolve(false), current;
  const emit = event => {
    if (!enabled || !accepting || offline || sequence >= maxMessages) { rejected++; return Promise.resolve(false); }
    const projected = projectDiagnostic(event, runId, sequence + 1, allowed);
    if (!projected) { rejected++; return Promise.resolve(false); }
    const payload = Buffer.from(JSON.stringify(projected), 'utf8');
    const bytes = payload.byteLength;
    const body = `b64:${payload.toString('base64')}`;
    if (bytes > messageLimit) { rejected++; return Promise.resolve(false); }
    sequence++;
    tail = tail.then(async () => {
      if (offline) return false;
      const controller = new AbortController(); current = controller;
      const timer = setTimeout(() => controller.abort(), 1500);
      try {
        const response = await fetcher(`${baseUrl}/XADD/${encodeURIComponent(`localpost:v1:${registrationId}`)}/*/data`, {
          method: 'PUT', headers: { 'Content-Type': 'text/plain;charset=UTF-8' },
          credentials: 'omit', redirect: 'error', cache: 'no-store', signal: controller.signal, body,
        });
        if (!await receipt(response)) throw Error('Invalid delivery');
        delivered++; return true;
      } catch { failed++; offline = true; return false; }
      finally { clearTimeout(timer); current = undefined; }
    });
    return tail;
  };
  return Object.freeze({
    emit,
    attach(page, caseId) {
      const pageError = () => { void emit({ caseId, kind: 'page', phase: 'error', code: 'PAGE_ERROR' }); };
      const requestFailed = () => { void emit({ caseId, kind: 'request', phase: 'error', code: 'REQUEST_FAILED' }); };
      const consoleError = message => { try { if (message.type() === 'error') void emit({ caseId, kind: 'console', phase: 'error', code: 'CONSOLE_ERROR' }); } catch { /* Diagnostics never change the test outcome. */ } };
      page.on('pageerror', pageError); page.on('requestfailed', requestFailed); page.on('console', consoleError);
      return () => { page.off('pageerror', pageError); page.off('requestfailed', requestFailed); page.off('console', consoleError); };
    },
    async close() {
      accepting = false;
      let timer;
      await Promise.race([tail, new Promise(resolve => { timer = setTimeout(() => { offline = true; current?.abort(); resolve(false); }, 2000); })]);
      clearTimeout(timer);
    },
    stats: () => ({ enabled, attempted: sequence, delivered, rejected, failed, offline }),
  });
}
