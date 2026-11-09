import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { basename, dirname, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import test, { after } from 'node:test';
import { localTool, webRoot } from './local-tool.mjs';

const root = mkdtempSync(resolve(tmpdir(), 'localswim-identity-boundary-'));
const output = resolve(root, 'js');
writeFileSync(resolve(root, 'package.json'), '{"type":"module"}\n');
writeFileSync(resolve(root, 'tsconfig.json'), JSON.stringify({ extends: resolve(webRoot, 'tsconfig.json'),
  files: [resolve(webRoot, 'src/api/client.ts')], compilerOptions: { outDir: output } }));
after(() => {
  const target = realpathSync(root);
  assert.equal(dirname(target), realpathSync(tmpdir()));
  assert.match(basename(target), /^localswim-identity-boundary-[A-Za-z0-9_-]+$/);
  rmSync(target, { recursive: true, force: true });
});
const built = spawnSync(process.execPath, [localTool('typescript', 'tsc'), '-p', resolve(root, 'tsconfig.json')],
  { cwd: webRoot, encoding: 'utf8', timeout: 120_000 });
assert.equal(built.status, 0, built.stdout + built.stderr);
const api = await import(pathToFileURL(resolve(output, 'api/identity-probe.js')));
const { BoardClient } = await import(pathToFileURL(resolve(output, 'api/client.js')));
const { parse, validate } = await import(pathToFileURL(resolve(output, 'api/boundary.js')));
const vectors = JSON.parse(readFileSync(resolve(webRoot, '../internal/contract/transport/testdata/identity-probe-vectors.json'), 'utf8'));
const id = n => '00000000-0000-4000-8000-' + String(n).padStart(12, '0');
const context = { identity: { boardId: id(1), incarnation: id(2) }, runtimeGeneration: id(3), configurationGeneration: id(4), probeGeneration: id(5) };
const parsers = { ProbeRequest: api.parseProbeRequest, ProbeResult: api.parseProbeResult,
  ProbeControlResponse: api.parseProbeControlResponse, ProbeStopResponse: api.parseProbeStopResponse,
  ProbeOutcomeUnknown: api.parseProbeOutcomeUnknown };

test('W01 W02 W03 W04 W12 shared Go vectors agree with generated schema and production parsers', () => {
  assert.equal(vectors.length, 448);
  for (const row of vectors) {
    let accepted = false;
    try { parsers[row.type](row.json, context); accepted = true; } catch {}
    assert.equal(accepted, row.valid, row.name);
    // Schema describes JSON structure/semantics; parser owns raw byte ceiling.
    if (!(row.caseKey === 'W04' && !row.valid)) {
      let schemaAccepted = false;
      try { validate('transport:' + row.type, parse(row.json)); schemaAccepted = true; } catch {}
      assert.equal(schemaAccepted, row.valid, 'schema ' + row.name);
    }
  }
});

test('W06 rejects every identity and generation mismatch and keeps results detached', () => {
  for (const type of ['ProbeResult', 'ProbeControlResponse', 'ProbeStopResponse']) {
    const row = vectors.find(row => row.type === type && row.name === type + '-valid');
    const parsed = parsers[type](row.json, context);
    const result = type === 'ProbeResult' ? parsed : parsed.result;
    result.identity.boardId = id(90);
    assert.equal(context.identity.boardId, id(1));
    for (const key of ['boardId', 'incarnation', 'runtimeGeneration', 'configurationGeneration', 'probeGeneration']) {
      if (type === 'ProbeControlResponse' && key === 'probeGeneration') continue; // Ready reveals a fresh generation.
      const expected = structuredClone(context);
      if (key === 'boardId' || key === 'incarnation') expected.identity[key] = id(90);
      else expected[key] = id(90);
      assert.throws(() => parsers[type](row.json, expected), key + ' ' + type);
    }
    for (const key of ['runtimeGeneration', 'configurationGeneration', 'probeGeneration']) {
      if (type === 'ProbeControlResponse' && key === 'probeGeneration') continue;
      assert.throws(() => parsers[type](row.json, { ...context, [key]: null }));
    }
  }
});

test('raw bytes reject malformed UTF8, BOM, truncation and oversize without repairing encoding', () => {
  for (const row of vectors.filter(row => row.valid && row.name.endsWith('-valid'))) {
    const bytes = new TextEncoder().encode(row.json);
    assert.deepEqual(parsers[row.type](bytes, context), parsers[row.type](row.json, context));
    assert.throws(() => parsers[row.type](Uint8Array.from([0xff, ...bytes]), context));
    assert.throws(() => parsers[row.type](Uint8Array.from([0xef, 0xbb, 0xbf, ...bytes]), context));
    assert.throws(() => parsers[row.type](bytes.slice(0, -1), context));
  }
});

test('operation-specific refusal recognition never treats native POST503 as a known receipt', () => {
  const refusal = code => JSON.stringify({ apiVersion: 1, outcome: 'refused', error: { code, message: 'Request refused', field: null }, conflict: null });
  const pairs = [[400, 'INVALID_INPUT'], [401, 'UNAUTHENTICATED'], [403, 'FORBIDDEN'], [404, 'NOT_FOUND'], [405, 'INVALID_INPUT'], [409, 'CONTROL_STALE'], [409, 'CONTROL_BUSY'], [409, 'IDENTITY_CHANGED']];
  for (const [status, code] of pairs) {
    assert.equal(api.parseProbeRefusal(refusal(code), status, 'get').error.code, code);
    assert.equal(api.parseProbeRefusal(refusal(code), status, 'post').error.code, code);
  }
  assert.equal(api.parseProbeRefusal(refusal('UNAVAILABLE'), 503, 'get').error.code, 'UNAVAILABLE');
  assert.throws(() => api.parseProbeRefusal(refusal('UNAVAILABLE'), 503, 'post'));
  for (const [status] of pairs) for (const [, code] of pairs) {
    if (!pairs.some(([s, c]) => s === status && c === code)) assert.throws(() => api.parseProbeRefusal(refusal(code), status, 'post'));
  }
  for (const change of [body => { body.opId = id(6); }, body => { body.error.field = 'private'; },
    body => { body.error.message = 'raw details'; }, body => { body.result = {}; }]) {
    const value = JSON.parse(refusal('FORBIDDEN')); change(value);
    assert.throws(() => api.parseProbeRefusal(JSON.stringify(value), 403, 'post'));
  }
});

const control = (phase = 'ready') => ({ apiVersion: 1, scope: 'identity-control', result: { ...context, phase, joined: phase === 'ready' } });
const supported = { ...context, version: 1, kind: 'identity', state: 'supported', code: 'OK', toolVersion: '1.2.3', capability: null, joined: true };
const joined = { apiVersion: 1, scope: 'identity-stop', result: { ...context, phase: 'joined', joined: true } };
const refused = code => ({ apiVersion: 1, outcome: 'refused', error: { code, message: 'Request refused', field: null }, conflict: null });
const json = (value, status = 200, headers = {}) => {
  const body = typeof value === 'string' ? value : JSON.stringify(value);
  return new Response(body, { status, headers: { 'Cache-Control': 'no-store', 'Content-Type': 'application/json',
    'Content-Length': String(Buffer.byteLength(body)), ...headers } });
};
const recipientView = (configured = true) => ({
  apiVersion: 1, scope: 'recipient', result: {
    identity: context.identity, runtimeGeneration: context.runtimeGeneration,
    state: configured ? 'configured' : 'unconfigured', configurationRevision: configured ? 1 : 0,
    configurationGeneration: configured ? context.configurationGeneration : null,
    requestId: configured ? id(6) : null, reason: null,
    input: configured ? { version: 1, recipient: { kind: 'codex-thread', threadId: id(7) }, policy: 'conservative-metadata-v1' } : null,
  },
});
const probeWire = { incarnation: context.identity.incarnation, expectedRuntimeGeneration: context.runtimeGeneration,
  expectedConfigurationGeneration: context.configurationGeneration, probeGeneration: context.probeGeneration };
function clientFixture({ policy = () => json(recipientView()), get = () => json(control()), post = () => json(supported), stop = () => json(joined) } = {}) {
  const client = Object.create(BoardClient.prototype);
  const calls = [], captured = [], signal = new AbortController().signal;
  let credential = 'current', active = true;
  client.connection = () => credential;
  client.options = { credential: () => credential, selection: context.identity };
  client.sameConnection = presented => { if (credential !== presented) throw Error('Changed'); };
  client.path = board => { assert.equal(board, context.identity.boardId); return '/api/v1/boards/' + board; };
  client.raw = async (path, init) => {
    assert.equal(init.signal.aborted, false);
    const url = new URL(path, 'http://127.0.0.1');
    const action = url.pathname.split('/').at(-1);
    calls.push(action);
    if (action === 'status') {
      assert.equal(init.method, 'GET'); assert.equal(init.body, undefined);
      assert.deepEqual([...url.searchParams.entries()], [['incarnation', context.identity.incarnation]]);
      return { response: await policy(), credential };
    }
    if (action === 'probe-control') {
      assert.equal(init.method, 'GET'); assert.equal(init.body, undefined);
      const { probeGeneration, ...query } = probeWire;
      assert.deepEqual([...url.searchParams.entries()], Object.entries(query));
    } else {
      assert.equal(init.method, 'POST'); assert.equal(url.search, '');
      assert.deepEqual(JSON.parse(init.body), probeWire);
    }
    const response = await (action === 'probe-control' ? get() : action === 'probe-version' ? post() : stop());
    return { response, credential };
  };
  const view = () => { if (!active) throw Error('Stale view'); };
  return { calls, captured, rotate: () => { credential = 'rotated'; }, close: () => { active = false; },
    run: () => client.checkInstalledVersion(context.identity, signal, view, value => captured.push(value)),
    stop: () => client.stopInstalledVersion(captured[0], signal, view),
    ordinaryStatus: () => client.recipient(context.identity, { action: 'status' }, signal) };
}

test('C03 deliberate configured/ready path sends one status, control and start; missing policy and busy preclude start', async () => {
  const ready = clientFixture();
  assert.deepEqual(await ready.run(), supported);
  assert.deepEqual(ready.calls, ['status', 'probe-control', 'probe-version']);
  assert.deepEqual(ready.captured[0].context, context);
  const unconfigured = clientFixture({ policy: () => json(recipientView(false)) });
  await assert.rejects(unconfigured.run(), { code: 'UNCONFIGURED' });
  assert.deepEqual(unconfigured.calls, ['status']);
  for (const phase of ['running', 'draining']) {
    const busy = clientFixture({ get: () => json(control(phase)) });
    await assert.rejects(busy.run(), { code: 'CONTROL_BUSY' });
    assert.deepEqual(busy.calls, ['status', 'probe-control']);
    assert.deepEqual(busy.captured[0].context, context);
    assert.deepEqual(await busy.stop(), joined);
    assert.deepEqual(busy.calls, ['status', 'probe-control', 'status', 'stop']);
  }
});

test('W05 production transport rejects redirect, encoding, framing, type, cache and truncation before start', async () => {
  for (const get of [
    () => { const response = json(control()); Object.defineProperty(response, 'redirected', { value: true }); return response; },
    () => json(control(), 200, { 'Content-Encoding': 'gzip' }),
    () => json(control(), 200, { 'Transfer-Encoding': 'chunked' }),
    () => new Response(JSON.stringify(control()), { headers: { 'Cache-Control': 'no-store', 'Content-Type': 'application/json' } }),
    () => json(control(), 200, { 'Content-Length': '2049' }),
    () => json(control(), 200, { 'Content-Length': '0004' }),
    () => json(control(), 200, { 'Content-Length': '3' }),
    () => json(control(), 200, { 'Content-Type': 'text/html' }),
    () => json(control(), 200, { 'Cache-Control': 'max-age=30' }),
    () => json(JSON.stringify(control()).slice(0, -1)),
  ]) {
    const run = clientFixture({ get });
    await assert.rejects(run.run(), { code: 'UNAVAILABLE', phase: 'preflight' });
    assert.deepEqual(run.calls, ['status', 'probe-control']);
    assert.equal(run.captured.length, 0);
  }
});

test('W05 fixed-length identity policy preflight does not change the ordinary M16 read', async () => {
  const value = recipientView();
  const cases = [
    () => new Response(JSON.stringify(value), { headers: { 'Cache-Control': 'no-store', 'Content-Type': 'application/json' } }),
    () => json(value, 200, { 'Transfer-Encoding': 'chunked' }),
  ];
  for (const policy of cases) {
    const run = clientFixture({ policy });
    await assert.rejects(run.run(), { code: 'UNAVAILABLE', phase: 'preflight' });
    assert.deepEqual(run.calls, ['status']);
    assert.deepEqual(await run.ordinaryStatus(), value.result);
  }
});

test('W08 W09 W10 production transport distinguishes exact refusal from unknown start and stop', async () => {
  for (const [status, code] of [[400, 'INVALID_INPUT'], [401, 'UNAUTHENTICATED'], [403, 'FORBIDDEN'], [404, 'NOT_FOUND'],
    [405, 'INVALID_INPUT'], [409, 'CONTROL_STALE'], [409, 'CONTROL_BUSY'], [409, 'IDENTITY_CHANGED']]) {
    const response = () => json(refused(code), status);
    const run = clientFixture({ post: response, stop: response });
    await assert.rejects(run.run(), { code, phase: 'post' });
    await assert.rejects(run.stop(), { code, phase: 'post' });
    assert.deepEqual(run.calls, ['status', 'probe-control', 'probe-version', 'status', 'stop']);
  }
  for (const response of [
    () => json(refused('UNAVAILABLE'), 503), () => json(refused('UNAVAILABLE'), 409),
    () => json(supported, 200, { 'Transfer-Encoding': 'chunked' }),
    () => json('{'), () => json(refused('FORBIDDEN'), 502),
    () => json({ ...supported, probeGeneration: id(9) }),
    () => { throw Error('Lost response'); },
  ]) {
    const run = clientFixture({ post: response, stop: response });
    await assert.rejects(run.run(), { code: 'OUTCOME_UNKNOWN', phase: 'post' });
    await assert.rejects(run.stop(), { code: 'OUTCOME_UNKNOWN', phase: 'post' });
    assert.deepEqual(run.calls, ['status', 'probe-control', 'probe-version', 'status', 'stop']);
  }
});

test('C05 current connection is required from the first status through POST disclosure and Stop capture', async () => {
  const before = clientFixture({ get: () => { before.rotate(); return json(control()); } });
  await assert.rejects(before.run(), { code: 'UNAVAILABLE', phase: 'preflight' });
  assert.deepEqual(before.calls, ['status', 'probe-control']);
  const after = clientFixture({ post: () => { after.rotate(); return json(supported); } });
  await assert.rejects(after.run(), { code: 'OUTCOME_UNKNOWN', phase: 'post' });
  await assert.rejects(after.stop(), { code: 'UNAVAILABLE', phase: 'preflight' });
  assert.deepEqual(after.calls, ['status', 'probe-control', 'probe-version']);
  const closed = clientFixture({ get: () => { closed.close(); return json(control()); } });
  await assert.rejects(closed.run(), { code: 'UNAVAILABLE', phase: 'preflight' });
  assert.deepEqual(closed.calls, ['status', 'probe-control']);
});

test('C07 incomplete Stop can be followed by a separate deliberate Stop of the same exact generation', async () => {
  const incomplete = { ...joined, result: { ...joined.result, phase: 'incomplete', joined: false } };
  let stops = 0;
  const run = clientFixture({ post: () => { throw Error('Lost result'); }, stop: () => json(++stops === 1 ? incomplete : joined) });
  await assert.rejects(run.run(), { code: 'OUTCOME_UNKNOWN' });
  assert.deepEqual(await run.stop(), incomplete);
  assert.equal(stops, 1);
  assert.deepEqual(await run.stop(), joined);
  assert.deepEqual(run.calls, ['status', 'probe-control', 'probe-version', 'status', 'stop', 'status', 'stop']);
});
