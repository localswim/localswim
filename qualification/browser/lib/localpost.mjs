// Development-only transport. localpost alone owns diagnostic file delivery.
import { randomUUID } from 'node:crypto';

const uuid4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const deliveredName = /^localpost_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.dat$/;
const kinds = new Set(['case', 'page', 'request', 'console']);
const phases = new Set(['start', 'complete', 'error']);
const codes = new Set(['OK', 'CASE_FAILED', 'CASE_SKIPPED', 'PAGE_ERROR', 'REQUEST_FAILED', 'CONSOLE_ERROR']);
const maxMessages = 64, messageLimit = 1024, receiptLimit = 2048;

function endpoint(value) {
  try {
    const url = new URL(value);
    if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || url.username || url.password ||
        url.pathname !== '/' || url.search || url.hash || !url.port || Number(url.port) < 1024) return null;
    return url.origin;
  } catch { return null; }
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

async function receipt(response, expectedBytes) {
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
    const value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    return value && Object.keys(value).sort().join() === 'bytes,filename' &&
      deliveredName.test(value.filename) && value.bytes === expectedBytes;
  } finally { reader.releaseLock(); }
}

export function createLocalpostDiagnostics({
  registrationId = process.env.LOCALPOST_REGISTRATION_ID,
  serviceUrl = process.env.LOCALPOST_URL ?? 'http://127.0.0.1:53147',
  runId = process.env.LOCALPOST_RUN_ID ?? randomUUID(), caseIds = [], fetcher = globalThis.fetch,
} = {}) {
  const allowed = new Set((Array.isArray(caseIds) ? caseIds : []).filter(id => typeof id === 'string' && /^V02-[A-Z]+-\d{2}$/.test(id)));
  const origin = endpoint(serviceUrl);
  const enabled = Boolean(origin && typeof registrationId === 'string' && uuid4.test(registrationId) &&
    typeof runId === 'string' && uuid4.test(runId) && allowed.size && typeof fetcher === 'function');
  let accepting = true, offline = false, sequence = 0, delivered = 0, rejected = 0, failed = 0;
  let tail = Promise.resolve(false), current;
  const emit = event => {
    if (!enabled || !accepting || offline || sequence >= maxMessages) { rejected++; return Promise.resolve(false); }
    const projected = projectDiagnostic(event, runId, sequence + 1, allowed);
    if (!projected) { rejected++; return Promise.resolve(false); }
    const body = JSON.stringify(projected), bytes = Buffer.byteLength(body);
    if (bytes > messageLimit) { rejected++; return Promise.resolve(false); }
    sequence++;
    tail = tail.then(async () => {
      if (offline) return false;
      const controller = new AbortController(); current = controller;
      const timer = setTimeout(() => controller.abort(), 1500);
      try {
        const response = await fetcher(`${origin}/registrations/${registrationId}/messages`, {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          credentials: 'omit', redirect: 'error', signal: controller.signal, body,
        });
        if (!await receipt(response, bytes)) throw Error('Invalid delivery');
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
