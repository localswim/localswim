import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { basename, dirname, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import test, { after } from 'node:test';
import { localTool, webRoot } from './local-tool.mjs';

// Never share compiler output with another Node test process or the product tree.
const root = mkdtempSync(resolve(tmpdir(), 'localswim-installation-api-'));
const output = resolve(root, 'js');
writeFileSync(resolve(root, 'package.json'), '{"type":"module"}\n');
writeFileSync(resolve(root, 'tsconfig.json'), JSON.stringify({ extends: resolve(webRoot, 'tsconfig.json'),
  files: [resolve(webRoot, 'src/api/installation.ts'), resolve(webRoot, 'src/api/client.ts'), resolve(webRoot, 'src/board/recipient.test.ts')], compilerOptions: { outDir: output } }));
after(() => {
  const target = realpathSync(root);
  assert.equal(dirname(target), realpathSync(tmpdir()));
  assert.match(basename(target), /^localswim-installation-api-[A-Za-z0-9_-]+$/);
  rmSync(target, { recursive: true, force: true });
});
const built = spawnSync(process.execPath, [localTool('typescript', 'tsc'), '-p', resolve(root, 'tsconfig.json')],
  { cwd: webRoot, encoding: 'utf8', timeout: 120_000 });
assert.equal(built.status, 0, built.stdout + built.stderr);
const api = await import(pathToFileURL(resolve(output, 'api/installation.js')));
const { BoardClient } = await import(pathToFileURL(resolve(output, 'api/client.js')));
const { parse, validate } = await import(pathToFileURL(resolve(output, 'api/boundary.js')));
const vectors = JSON.parse(readFileSync(resolve(webRoot, '../internal/contract/transport/testdata/installation-inspection-vectors.json'), 'utf8'));
const identity = { boardId: '00000000-0000-4000-8000-000000000002', incarnation: '00000000-0000-4000-8000-000000000003' };
const generation = '00000000-0000-4000-8000-000000000004';
const other = '00000000-0000-4000-8000-000000000005';
const response = (state = 'candidate', code = 'CANDIDATE_PRESENT') => ({ apiVersion: 1, scope: 'installation-inspection',
  result: { identity: structuredClone(identity), runtimeGeneration: generation, state, code } });
const refusal = code => ({ apiVersion: 1, outcome: 'refused', error: { code, message: 'Request refused', field: null }, conflict: null });
const headers = { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' };

function fixture(value = response(), status = 200, extraHeaders = {}, change = () => {}) {
  const calls = [];
  const transport = {
    assertCurrent() {}, sameConnection(token) { assert.equal(token, 'current'); },
    async raw(path, init) {
      calls.push({ path, init });
      await change();
      return { response: new Response(typeof value === 'string' || value instanceof Uint8Array ? value : JSON.stringify(value),
        { status, headers: { ...headers, ...extraHeaders } }), credential: 'current' };
    },
  };
  return { calls, transport, run: (selected = identity, signal = new AbortController().signal) => api.inspectInstallation(transport, selected, generation, signal) };
}

test('installation shared Go response vectors agree with actual production schema', () => {
  assert.ok(vectors.length > 0);
  for (const row of vectors) {
    let accepted = false;
    try {
      const value = parse(row.json);
      validate('transport:InstallationInspectionResponse', value);
      api.parseInstallationInspection(row.json, value.result.identity, value.result.runtimeGeneration);
      accepted = true;
    } catch {}
    assert.equal(accepted, row.valid, row.name);
  }
});

test('installation exact state/code, required fields, UUIDs and response byte boundary', () => {
  const pairs = [['candidate', 'CANDIDATE_PRESENT'], ['missing', 'TOOL_MISSING'], ...[
    'PLATFORM_UNQUALIFIED', 'SOURCE_UNQUALIFIED', 'PATH_UNQUALIFIED', 'PUBLISHER_UNQUALIFIED', 'FILE_CHANGED',
    'INSPECTION_UNKNOWN', 'INSPECTION_TIMED_OUT'].map(code => ['unqualified', code])];
  for (const [state, code] of pairs) assert.equal(api.parseInstallationInspection(JSON.stringify(response(state, code)), identity, generation).code, code);
  for (const [state] of pairs) for (const [, code] of pairs) {
    if (!pairs.some(([s, c]) => s === state && c === code)) assert.throws(() => api.parseInstallationInspection(JSON.stringify(response(state, code)), identity, generation));
  }
  const valid = JSON.stringify(response());
  assert.equal(api.parseInstallationInspection(valid + ' '.repeat(2048 - Buffer.byteLength(valid)), identity, generation).state, 'candidate');
  for (const text of [valid + ' '.repeat(2049 - Buffer.byteLength(valid)), '\ufeff' + valid, valid + '{}',
    valid.replace('"apiVersion":1', '"apiVersion":1,"apiVersion":1'), valid.replace('"apiVersion":1', '"apiVersion":1.0'),
    valid.replace('"apiVersion":1', '"apiVersion":1e0'), valid.replace('candidate', '\\ud800')]) assert.throws(() => api.parseInstallationInspection(text, identity, generation));
  for (const key of ['apiVersion', 'scope', 'result']) {
    const v = response(); delete v[key]; assert.throws(() => api.parseInstallationInspection(JSON.stringify(v), identity, generation));
    const n = response(); n[key] = null; assert.throws(() => api.parseInstallationInspection(JSON.stringify(n), identity, generation));
  }
  for (const key of ['identity', 'runtimeGeneration', 'state', 'code']) {
    const v = response(); delete v.result[key]; assert.throws(() => api.parseInstallationInspection(JSON.stringify(v), identity, generation));
    const n = response(); n.result[key] = null; assert.throws(() => api.parseInstallationInspection(JSON.stringify(n), identity, generation));
  }
  for (const v of [{ ...response(), path: 'private' }, { ...response(), result: { ...response().result, toolVersion: '1.0.0' } },
    response('supported', 'OK'), { ...response(), result: { ...response().result, runtimeGeneration: other } },
    { ...response(), result: { ...response().result, identity: { ...identity, incarnation: other } } }]) assert.throws(() => api.parseInstallationInspection(JSON.stringify(v), identity, generation));
});

test('installation dispatch is exactly one selected GET without body, override or retry', async () => {
  const f = fixture();
  assert.deepEqual(await f.run(), response().result);
  assert.equal(f.calls.length, 1);
  assert.equal(f.calls[0].path, `/api/v1/boards/${identity.boardId}/recipient/tool/inspect?incarnation=${identity.incarnation}&expectedRuntimeGeneration=${generation}`);
  assert.equal(f.calls[0].init.method, 'GET');
  assert.equal(f.calls[0].init.body, undefined);
  assert.deepEqual([...new URL('http://127.0.0.1' + f.calls[0].path).searchParams.keys()], ['incarnation', 'expectedRuntimeGeneration']);
  for (const value of [{ ...identity, path: 'elsewhere' }, { ...identity, boardId: '../bad' }, { ...identity, incarnation: identity.incarnation.toUpperCase() + 'X' },
    { ...identity, boardId: '00000000-0000-0000-0000-000000000000' }]) assert.throws(() => api.installationInspectionPath(value, generation));
  for (const value of ['', other.replace('-4000-', '-1000-'), generation + '&path=x', null]) assert.throws(() => api.installationInspectionPath(identity, value));
});

test('installation captures caller input and fences late authority, generation and A-B-A epochs', async () => {
  const selected = structuredClone(identity), f = fixture(response(), 200, {}, () => { selected.incarnation = other; });
  assert.deepEqual(await f.run(selected), response().result);
  for (const check of ['assertCurrent', 'sameConnection']) {
    let valid = true;
    const late = fixture(response(), 200, {}, () => { valid = false; });
    late.transport[check] = () => { if (!valid) throw Error('private detail must not escape'); };
    await assert.rejects(late.run(), error => error instanceof api.InstallationInspectionUnavailable && !error.message.includes('private'));
    assert.equal(late.calls.length, 1);
  }
  const stale = fixture();
  stale.transport.assertCurrent = () => { throw Error('changed epoch'); };
  await assert.rejects(stale.run(), api.InstallationInspectionUnavailable);
  assert.equal(stale.calls.length, 0);
});

test('installation accepts only exact refusal status/code shapes and redacts unusable failures', async () => {
  for (const [status, code] of [[400, 'INVALID_INPUT'], [401, 'UNAUTHENTICATED'], [403, 'FORBIDDEN'], [404, 'NOT_FOUND'],
    [405, 'INVALID_INPUT'], [409, 'CONTROL_STALE'], [409, 'CONTROL_BUSY'], [409, 'IDENTITY_CHANGED'], [503, 'UNAVAILABLE']]) {
    const f = fixture(refusal(code), status);
    await assert.rejects(f.run(), error => error instanceof api.InstallationInspectionRefusal && error.code === code);
    assert.equal(f.calls.length, 1);
  }
  for (const [value, status] of [[refusal('UNAVAILABLE'), 409], [response(), 503], [{ ...refusal('FORBIDDEN'), result: response().result }, 403],
    [{ ...refusal('CONTROL_BUSY'), opId: null }, 409], [{ ...refusal('CONTROL_BUSY'), conflict: {} }, 409],
    [{ ...refusal('CONTROL_BUSY'), error: { code: 'CONTROL_BUSY', message: 'private', field: null } }, 409]]) {
    await assert.rejects(fixture(value, status).run(), api.InstallationInspectionUnavailable);
  }
});

test('installation response streaming enforces media, bytes, UTF-8 and cancellation without retries', async () => {
  for (const extra of [{ 'Content-Type': 'text/plain' }, { 'Cache-Control': 'public' }, { 'Content-Encoding': 'gzip' },
    { 'Content-Length': '2049' }, { 'Content-Length': '01' }, { 'Content-Length': '1' }]) await assert.rejects(fixture(response(), 200, extra).run(), api.InstallationInspectionUnavailable);
  await assert.rejects(fixture(new Uint8Array([0xff])).run(), api.InstallationInspectionUnavailable);
  await assert.rejects(fixture(JSON.stringify(response()) + ' '.repeat(2049)).run(), api.InstallationInspectionUnavailable);
  const aborted = fixture(), abort = new AbortController(); abort.abort();
  await assert.rejects(aborted.run(identity, abort.signal), api.InstallationInspectionUnavailable);
  assert.equal(aborted.calls.length, 0);
  const fail = fixture(); fail.transport.raw = async () => { fail.calls.push({}); throw Error('raw private response'); };
  await assert.rejects(fail.run(), error => error instanceof api.InstallationInspectionUnavailable && error.message === 'Installation inspection unavailable');
  assert.equal(fail.calls.length, 1);
  const unvalidated = fixture();
  unvalidated.transport.raw = async () => { throw new api.InstallationInspectionRefusal('FORBIDDEN'); };
  await assert.rejects(unvalidated.run(), api.InstallationInspectionUnavailable);
  let current = true, cancelled = false, reads = 0;
  const lateBody = fixture();
  lateBody.transport.assertCurrent = () => { if (!current) throw Error('lost authority'); };
  lateBody.transport.raw = async () => ({ credential: 'current', response: new Response(new ReadableStream({
    pull(controller) { if (++reads === 1) { current = false; controller.enqueue(new TextEncoder().encode(JSON.stringify(response()))); } },
    cancel() { cancelled = true; },
  }), { headers }) });
  await assert.rejects(lateBody.run(), api.InstallationInspectionUnavailable);
  assert.equal(cancelled, true);
});

test('BoardClient installation method delegates only through its current authenticated selected transport', async () => {
  const client = Object.create(BoardClient.prototype);
  client.options = { selection: identity };
  client.path = board => { assert.equal(board, identity.boardId); return '/api/v1/boards/' + board; };
  client.connection = () => 'current';
  client.sameConnection = credential => { assert.equal(credential, 'current'); };
  let calls = 0, checks = 0;
  client.raw = async (path, init) => {
    calls++;
    assert.equal(path, api.installationInspectionPath(identity, generation));
    assert.equal(init.method, 'GET'); assert.equal(init.body, undefined);
    return { response: new Response(JSON.stringify(response()), { headers }), credential: 'current' };
  };
  assert.deepEqual(await client.installationInspection(identity, generation, new AbortController().signal, () => { checks++; }), response().result);
  assert.equal(calls, 1); assert.ok(checks >= 3);
  await assert.rejects(client.installationInspection({ ...identity, incarnation: other }, generation, new AbortController().signal, () => {}));
  assert.equal(calls, 1, 'foreign selected incarnation never dispatches');
  await assert.rejects(client.installationInspection(identity, generation, new AbortController().signal, () => { throw Error('stale view'); }), api.InstallationInspectionUnavailable);
  assert.equal(calls, 1, 'stale current view never dispatches');
});
