import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import test from 'node:test';

// Exercise emitted production modules and the actual generated schema, together.
const built = spawnSync(process.execPath, ['scripts/tsc.mjs', 'build'], { cwd: new URL('..', import.meta.url), encoding: 'utf8' });
assert.equal(built.status, 0, built.stdout + built.stderr);
const { BoardClient } = await import('../dist/js/api/client.js');
const { parse, validate } = await import('../dist/js/api/boundary.js');
const { RecipientRefusal, RecipientUnknown, validateRecipient } = await import('../dist/js/api/recipient.js');
const vectors = JSON.parse(readFileSync(new URL('../../internal/contract/transport/testdata/recipient-vectors.json', import.meta.url), 'utf8'));
const identity = { boardId: '00000000-0000-4000-8000-000000000002', incarnation: '00000000-0000-4000-8000-000000000003' };
const input = JSON.parse(vectors[0].json);
const generation = '00000000-0000-4000-8000-000000000004';
const requestId = '00000000-0000-4000-8000-000000000005';
const command = { action: 'configure', expectedRuntimeGeneration: generation, expectedConfigurationRevision: 0, requestId, input };
const result = { identity, runtimeGeneration: generation, state: 'configured', reason: null, configurationRevision: 1,
  configurationGeneration: '00000000-0000-4000-8000-000000000006', requestId, input };
const envelope = { apiVersion: 1, scope: 'recipient', result };
const refusal = code => ({ apiVersion: 1, outcome: 'refused', error: { code, message: 'Request refused', field: null }, conflict: null });

test('recipient shared exact input vectors agree with production boundary', () => {
  for (const row of vectors) {
    let accepted = false;
    try { if (new TextEncoder().encode(row.json).length > 1024) throw Error(); validate('transport:RecipientInput', parse(row.json)); accepted = true; } catch {}
    assert.equal(accepted, row.valid, row.name);
  }
});

async function invoke(value, status = 200, action = command, changes = {}) {
  let calls = 0;
  const fake = Object.create(BoardClient.prototype);
  fake.options = { credential: () => 'credential', selection: identity };
  fake.path = () => '/api/v1/boards/' + identity.boardId;
  fake.sameConnection = () => {};
  fake.raw = async (path, init) => {
    calls++;
    assert.equal(init.method, action.action === 'configure' ? 'POST' : 'GET');
    const body = typeof value === 'string' ? value : JSON.stringify(value);
    return { response: new Response(body, { status, headers: { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', ...changes } }), credential: 'credential' };
  };
  try { return await fake.recipient(identity, action, new AbortController().signal); }
  finally { assert.equal(calls, 1, 'single dispatch; no retry or status mutation'); }
}

test('recipient production client confirms only exact original POST and fresh GET observations', async () => {
  assert.deepEqual(await invoke(envelope), result);
  const reopened = structuredClone(envelope); reopened.result.runtimeGeneration = '00000000-0000-4000-8000-000000000007';
  assert.deepEqual(await invoke(reopened, 200, { action: 'status' }), reopened.result);
  await assert.rejects(invoke(reopened), RecipientUnknown);
});

test('recipient production client exact refusal whitelist', async () => {
  for (const [status, code] of [[400, 'INVALID_INPUT'], [401, 'UNAUTHENTICATED'], [403, 'FORBIDDEN'], [409, 'CONTROL_STALE'], [409, 'CONTROL_BUSY'], [409, 'CONFIGURATION_FIXED']]) {
    await assert.rejects(invoke(refusal(code), status), error => error instanceof RecipientRefusal && error.code === code);
    await assert.rejects(invoke(refusal(code), status === 409 ? 400 : 409), RecipientUnknown);
  }
  for (const [status, code] of [[503, 'UNAVAILABLE'], [409, 'IDENTITY_CHANGED'], [500, 'INVALID_INPUT']]) await assert.rejects(invoke(refusal(code), status), RecipientUnknown);
  const mismatched = refusal('CONTROL_BUSY'); mismatched.error.message = 'arbitrary';
  await assert.rejects(invoke(mismatched, 409), RecipientUnknown);
});

test('recipient production client every malformed or mismatched POST 200 is UNKNOWN', async () => {
  const variants = [null, {}, { ...envelope, scope: 'notices' }, ...['unconfigured', 'unavailable', 'unknown'].map(state => ({ ...envelope, result: { ...result, state, reason: state === 'unconfigured' ? null : 'unknown', configurationRevision: 0, configurationGeneration: null, requestId: null, input: null } }))];
  for (const patch of [{ identity: { ...identity, boardId: identity.incarnation } }, { runtimeGeneration: requestId }, { requestId: generation }, { input: { ...input, policy: 'wrong' } }, { input: { ...input, recipient: { ...input.recipient, threadId: generation } } }, { configurationRevision: 0 }, { configurationGeneration: null }]) variants.push({ ...envelope, result: { ...result, ...patch } });
  for (const value of variants) await assert.rejects(invoke(value), RecipientUnknown);
  for (const text of ['{', JSON.stringify(envelope) + '{}', '\ufeff' + JSON.stringify(envelope), JSON.stringify(envelope) + ' '.repeat(4096)]) await assert.rejects(invoke(text), RecipientUnknown);
  await assert.rejects(invoke(envelope, 200, command, { 'Content-Encoding': 'gzip' }), RecipientUnknown);
  await assert.rejects(invoke(envelope, 200, command, { 'Content-Length': '4097' }), RecipientUnknown);
  const invalid = structuredClone(result); invalid.state = 'unavailable';
  assert.throws(() => validateRecipient(invalid, identity, { action: 'status' }));
});
