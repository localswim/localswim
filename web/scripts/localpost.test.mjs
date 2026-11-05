import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { createLocalpostDiagnostics, projectDiagnostic } from '../../qualification/browser/lib/localpost.mjs';

const registrationId = '10000000-0000-4000-8000-000000000001';
const runId = '20000000-0000-4000-8000-000000000001';
const caseIds = ['V02-LOCAL-01'];
const event = { caseId: caseIds[0], kind: 'case', phase: 'start', code: 'OK' };
const options = { registrationId, runId, caseIds };
const reply = (value = { XADD: '1793898000000-0' }) => new Response(JSON.stringify(value));
const decode = body => JSON.parse(Buffer.from(body.slice(4), 'base64').toString('utf8'));

// Projection is the privacy boundary for both normal lifecycle events and page hooks.
test('fixed projection refuses arbitrary payload fields and identifiers', () => {
  assert.deepEqual(Object.keys(projectDiagnostic(event, runId, 1, new Set(caseIds))).sort(),
    ['caseId', 'code', 'kind', 'phase', 'runId', 'schemaVersion', 'sequence', 'source']);
  for (const bad of [{ ...event, token: 'secret' }, { ...event, code: 'raw error' },
    { ...event, caseId: 'V02-UNKNOWN-01' }, { ...event, kind: 'url' }, null]) {
    assert.equal(projectDiagnostic(bad, runId, 1, new Set(caseIds)), null);
  }
  const hostile = { ...event, get code() { throw Error('hostile getter'); } };
  assert.equal(projectDiagnostic(hostile, runId, 1, new Set(caseIds)), null);
});

test('native PUT sends canonical base64, omits credentials and validates stream receipt', async () => {
  let count = 0;
  const diagnostics = createLocalpostDiagnostics({ ...options, fetcher: async (url, request) => {
    count++;
    assert.equal(url, `http://127.0.0.1:53147/XADD/localpost%3Av1%3A${registrationId}/*/data`);
    assert.equal(request.method, 'PUT');
    assert.equal(request.credentials, 'omit');
    assert.equal(request.redirect, 'error');
    assert.equal(request.cache, 'no-store');
    assert.deepEqual(request.headers, { 'Content-Type': 'text/plain;charset=UTF-8' });
    assert.ok(request.body.startsWith('b64:'));
    assert.equal(Buffer.from(request.body.slice(4), 'base64').toString('base64'), request.body.slice(4));
    assert.deepEqual(decode(request.body), projectDiagnostic(event, runId, 1, new Set(caseIds)));
    return reply();
  } });
  assert.equal(await diagnostics.emit(event), true);
  await diagnostics.close();
  assert.equal(count, 1);
  assert.equal(diagnostics.stats().delivered, 1);
});

test('disabled or invalid configuration never invokes transport', async () => {
  for (const overrides of [
    { registrationId: '' }, { registrationId: registrationId.toUpperCase().replace('1', 'A') },
    { serviceUrl: 'https://127.0.0.1:53147' }, { serviceUrl: 'http://localhost:53147' },
    { serviceUrl: 'http://example.com:53147' }, { serviceUrl: 'http://127.0.0.1:0' },
    { serviceUrl: 'http://127.0.0.1:65536' }, { serviceUrl: 'http://user@127.0.0.1' },
    { serviceUrl: 'http://127.0.0.1/?extra=1' }, { serviceUrl: 'http://127.0.0.1/#fragment' },
    { serviceUrl: 'http://127.0.0.1/api/2026-11-05' }, { serviceUrl: 'http://127.0.0.1/.' },
    { serviceUrl: 'http://127.0.0.1/%2f' }, { serviceUrl: 'http://127.1' },
    { serviceUrl: ' http://127.0.0.1' }, { serviceUrl: 'http://[::ffff:127.0.0.1]' },
  ]) {
    const diagnostics = createLocalpostDiagnostics({ ...options, ...overrides,
      fetcher: () => { throw Error('must not run'); } });
    assert.equal(await diagnostics.emit(event), false);
    assert.equal(diagnostics.stats().enabled, false);
    await diagnostics.close();
  }
});

test('contract loopback endpoints support root paths, IPv6 and all valid ports', async () => {
  for (const serviceUrl of ['http://127.0.0.1', 'http://127.0.0.1/',
    'http://127.0.0.1:1', 'http://127.0.0.1:65535/', 'http://[::1]:53147/']) {
    const diagnostics = createLocalpostDiagnostics({ ...options, serviceUrl, fetcher: async url => {
      assert.ok(url.startsWith(`${new URL(serviceUrl).origin}/XADD/`));
      return reply();
    } });
    assert.equal(await diagnostics.emit(event), true);
    await diagnostics.close();
  }
});

test('transport failure disables queued and later sends without retry', async () => {
  let count = 0;
  const diagnostics = createLocalpostDiagnostics({ ...options, fetcher: async () => {
    count++;
    throw Error('private failure');
  } });
  const first = diagnostics.emit(event), second = diagnostics.emit(event);
  assert.equal(await first, false);
  assert.equal(await second, false);
  assert.equal(await diagnostics.emit(event), false);
  await diagnostics.close();
  assert.equal(count, 1);
  assert.equal(diagnostics.stats().failed, 1);
});

test('HTTP, backend errors and malformed stream receipts fail closed without replay', async () => {
  const invalidValues = [null, [], {}, { XADD: ['false', 'error'] },
    { XADD: [false, 'OOM private backend text'] }, { XADD: [false, 'ERR private backend text'] },
    { XADD: '1-0', extra: true }, { xadd: '1-0' }, { XADD: 123 },
    { XADD: '01-0' }, { XADD: '1--1' }, { XADD: '1-0\n' },
    { XADD: '18446744073709551616-0' }, { XADD: '1-18446744073709551616' },
    { filename: 'legacy.dat', bytes: 10 }];
  const fetchers = invalidValues.map(value => async () => reply(value));
  fetchers.push(async () => new Response('x'.repeat(2049)),
    async () => new Response('{'), async () => new Response('', { status: 503 }),
    async () => new Response(null, { status: 200 }),
    async () => new Response(new Uint8Array([0xff])));
  for (const fetcher of fetchers) {
    const diagnostics = createLocalpostDiagnostics({ ...options, fetcher });
    assert.equal(await diagnostics.emit(event), false);
    await diagnostics.close();
    assert.equal(diagnostics.stats().failed, 1);
    assert.equal(diagnostics.stats().offline, true);
  }
});

test('duplicate receipt members are refused before JSON parsing can collapse them', async () => {
  for (const body of [
    '{"XADD":[false,"OOM"],"XADD":"1-0"}',
    '{"XADD":"1-0","XADD":"2-0"}',
    '{"XADD":"1-0","\\u0058ADD":"2-0"}',
    '{"\\u0058ADD":"1-0","XADD":"2-0"}',
  ]) {
    const diagnostics = createLocalpostDiagnostics({ ...options,
      fetcher: async () => new Response(body) });
    assert.equal(await diagnostics.emit(event), false);
    await diagnostics.close();
    assert.equal(diagnostics.stats().failed, 1);
    assert.equal(diagnostics.stats().offline, true);
  }
});

test('one escaped receipt member and value retain valid JSON semantics', async () => {
  const diagnostics = createLocalpostDiagnostics({ ...options,
    fetcher: async () => new Response(' { "\\u0058ADD" : "\\u0031-0" } ') });
  assert.equal(await diagnostics.emit(event), true);
  await diagnostics.close();
});

test('uint64 stream components are validated as strings without precision loss', async () => {
  const diagnostics = createLocalpostDiagnostics({ ...options,
    fetcher: async () => reply({ XADD: '18446744073709551615-18446744073709551615' }) });
  assert.equal(await diagnostics.emit(event), true);
  await diagnostics.close();
});

test('message volume is capped for the complete run', async () => {
  let count = 0;
  const diagnostics = createLocalpostDiagnostics({ ...options, fetcher: async () => {
    count++;
    return reply();
  } });
  await Promise.all(Array.from({ length: 70 }, () => diagnostics.emit(event)));
  await diagnostics.close();
  assert.equal(count, 64);
  assert.equal(diagnostics.stats().rejected, 6);
});

test('browser hooks never read error, request or console prose and detach exactly', async () => {
  const sent = [];
  const diagnostics = createLocalpostDiagnostics({ ...options, fetcher: async (_url, request) => {
    sent.push(decode(request.body));
    return reply();
  } });
  const page = new EventEmitter(), detach = diagnostics.attach(page, caseIds[0]);
  const secret = new Proxy({}, { get() { throw Error('raw browser payload read'); } });
  page.emit('pageerror', secret);
  page.emit('requestfailed', secret);
  page.emit('console', { type: () => 'error', text: () => { throw Error('prose read'); } });
  assert.doesNotThrow(() => page.emit('console', { type: () => { throw Error('unavailable'); } }));
  detach();
  page.emit('pageerror', secret);
  await diagnostics.close();
  assert.deepEqual(sent.map(value => value.code), ['PAGE_ERROR', 'REQUEST_FAILED', 'CONSOLE_ERROR']);
  assert.equal(page.listenerCount('pageerror'), 0);
  assert.equal(page.listenerCount('requestfailed'), 0);
  assert.equal(page.listenerCount('console'), 0);
});

test('stalled transport and body reads are cancelled within the diagnostic deadline', async () => {
  const fetchers = [(_url, { signal }) => new Promise((_resolve, reject) => {
    signal.addEventListener('abort', () => reject(Error('aborted')), { once: true });
  }), async (_url, { signal }) => new Response(new ReadableStream({ start(controller) {
    signal.addEventListener('abort', () => controller.error(Error('aborted')), { once: true });
  } }))];
  for (const fetcher of fetchers) {
    const diagnostics = createLocalpostDiagnostics({ ...options, fetcher });
    const before = Date.now();
    void diagnostics.emit(event);
    await diagnostics.close();
    assert.ok(Date.now() - before < 2500);
    assert.equal(diagnostics.stats().failed, 1);
  }
});
