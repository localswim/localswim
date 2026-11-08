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
  files: [resolve(webRoot, 'src/api/identity-probe.ts')], compilerOptions: { outDir: output } }));
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
