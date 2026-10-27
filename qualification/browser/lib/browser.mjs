import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { readRun, digest } from './run-input.mjs';
import { startFixture, control } from './control.mjs';
const require = createRequire(resolve(dirname(fileURLToPath(import.meta.url)), '../../../web/package.json'));
const { test: base, expect, chromium } = require('@playwright/test');
export { expect, control };
const run = readRun(), declared = new Set();
export const inventory = JSON.parse(readFileSync(new URL('../inventory.json', import.meta.url), 'utf8'));
const inventorySHA256 = digest(readFileSync(new URL('../inventory.json', import.meta.url)));

export const test = base.extend({
  run: [async ({}, use) => { await use(run); }, { scope: 'worker' }],
  fixture: [async ({ run }, use) => {
    if (existsSync(run.dataDirectory) || existsSync(run.profileDirectory)) throw Error('Fresh synthetic runtime and browser profile required');
    writeFileSync(resolve(run.artifactDirectory, 'journey-source-freeze.json'), JSON.stringify({ sourceSHA256: run.sourceSHA256, sourceHashes: run.sourceHashes, inventorySHA256, required: inventory.required }, null, 2) + '\n', { flag: 'wx' });
    const fixture = await startFixture(run);
    writeFileSync(resolve(run.artifactDirectory, 'live-start.json'), JSON.stringify({ ...fixture.ready, manifestSHA256: run.manifestSHA256, inventorySHA256 }) + '\n', { flag: 'wx' });
    try { await use(fixture); }
    finally {
      const result = await fixture.close();
      writeFileSync(resolve(run.artifactDirectory, 'fixture-teardown.json'), JSON.stringify({ ...result, manifestSHA256: run.manifestSHA256 }) + '\n', { flag: 'wx' });
    }
  }, { scope: 'worker' }],
  profile: [async ({ run, fixture }, use) => {
    if (require('@playwright/test/package.json').version !== run.playwrightVersion) throw Error('Actual Playwright pin mismatch');
    const context = await chromium.launchPersistentContext(run.profileDirectory, { executablePath: run.chromeExecutable, headless: true, acceptDownloads: true, serviceWorkers: 'block' });
    try {
      if (context.browser()?.version() !== run.browserVersion) throw Error('Actual browser version mismatch');
      await use({ context });
    } finally {
      await context.close();
      writeFileSync(resolve(run.artifactDirectory, 'browser-teardown.json'), JSON.stringify({ closed: true, manifestSHA256: run.manifestSHA256 }) + '\n', { flag: 'wx' });
    }
  }, { scope: 'worker' }],
  app: async ({ run, profile }, use) => {
    const page = await profile.context.newPage();
    try {
      const response = await page.goto(run.origin, { waitUntil: 'networkidle' });
      if (!response || response.status() !== 200 || page.url() !== run.origin + '/') throw Error('Normal local entry failed');
      await expect(page.getByRole('button', { name: 'Create card', exact: true })).toBeVisible();
      await expect(page.getByRole('status').filter({ hasText: 'Current at board revision' })).toBeVisible();
      await use(page);
    } finally { await page.close(); }
  },
});
export function scenario(id, title, body) {
  if (declared.has(id)) throw Error('Duplicate mandatory case');
  declared.add(id); test(id + ' ' + title, body);
}
export function requireInventory() {
  if (!inventory.required.length || inventory.required.length !== declared.size || inventory.required.some(id => !declared.has(id))) throw Error('Missing mandatory browser case');
}
test.afterEach(async ({}, info) => {
  const id = info.title.split(' ', 1)[0];
  if (!/^V02-[A-Z]+-\d+$/.test(id)) throw Error('Unidentified browser case');
  writeFileSync(resolve(run.artifactDirectory, id + '.json'), JSON.stringify({ id, status: info.status, expectedStatus: info.expectedStatus, retry: info.retry,
    errors: info.errors.length, durationMs: info.duration, evidenceId: run.evidenceId, manifestSHA256: run.manifestSHA256, inventorySHA256, sourceSHA256: run.sourceSHA256,
    fixtureSHA256: run.fixtureSHA256, applicationSHA256: run.applicationSHA256, browserVersion: run.browserVersion,
    ...(info.status !== 'passed' ? { failureEvidence: { finalReport: 'final-playwright.json', testTitle: info.title,
      outputDirectory: info.outputDir, detail: 'P02 retains the original Playwright errors; failed test output is preserved. No retry or new worker may reuse this runtime.' } } : {}) }) + '\n', { flag: 'wx' });
});

// Actual built CLI only. Captured JSON is used in-process; never log its body.
export function cli(run, args, input, expectedExit = 0) {
  const result = spawnSync(run.applicationBinary, ['--data', run.dataDirectory, '--json', ...args], {
    input: input === undefined ? undefined : JSON.stringify(input) + '\n', encoding: 'utf8', windowsHide: true, timeout: 30_000, maxBuffer: 4 * 1024 * 1024,
  });
  if (result.error || result.signal || result.status !== expectedExit) {
    let code = 'NO_TYPED_CATEGORY';
    try { const body = JSON.parse(result.stdout), value = body.error?.code ?? body.code; if (typeof value === 'string' && /^[A-Z_]{1,60}$/.test(value)) code = value; } catch {}
    // Fixed category/exit only: no body, private path, stderr or credential.
    throw Error(`Actual CLI exit ${result.status ?? 'unavailable'} expected ${expectedExit}; ${code}`);
  }
  try { return JSON.parse(result.stdout); } catch { throw Error('Actual CLI JSON unavailable'); }
}
export const board = run => cli(run, ['board', 'read']).result;
export function request(run, card, input, current = board(run)) {
  const actual = card ? current.cards.find(row => row.cardId === card.cardId) : undefined;
  return { opId: randomUUID(), incarnation: current.identity.incarnation, target: actual ? { cardId: actual.cardId } : null,
    expectedRevision: current.revision, expectedCardRevision: actual?.revision ?? null, input };
}
export function mutate(run, command, card, input, expectedExit = 0, current) {
  const wire = request(run, card, input, current);
  const parts = command.split(' '); if (card) parts.push(card.cardId); parts.push('--input', '-');
  return { wire, result: cli(run, parts, wire, expectedExit) };
}
export async function createEditor(page, title = 'V02 synthetic ' + randomUUID()) {
  await page.getByRole('button', { name: 'Create card', exact: true }).click();
  await page.getByLabel('title', { exact: true }).fill(title);
  await page.getByLabel('description', { exact: true }).fill('Synthetic private input');
  await expect(page.getByRole('status').filter({ hasText: 'Recovery saved privately.' }).first()).toBeVisible();
  return title;
}
export async function refresh(page) {
  const response = page.waitForResponse(value => value.request().method() === 'GET' && /\/api\/v1\/boards\/[a-f0-9-]+$/.test(new URL(value.url()).pathname));
  await page.getByRole('button', { name: 'Read current board', exact: true }).click();
  const result = await (await response).json();
  await expect(page.getByRole('status').filter({ hasText: 'Current at board revision ' + result.result.revision + ';' })).toBeVisible();
}
export async function save(page) {
  const outcomes = page.getByRole('region', { name: 'Original operation outcomes' }).getByRole('status').filter({ hasText: 'Confirmed saved.' });
  const before = await outcomes.count();
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(outcomes).toHaveCount(before + 1);
}
export async function createCard(page, run, title) {
  title = await createEditor(page, title); await save(page);
  const card = board(run).cards.find(row => row.title === title);
  expect(card).toBeTruthy(); await refresh(page); return card;
}
export const cardArticle = (page, card) => page.getByRole('region', { name: 'Cards' }).getByRole('article').filter({ has: page.getByRole('heading', { name: '#' + card.ticket + ' ' + card.title, exact: true }) });
// Observation of actual product recovery records; never creates qualification,
// mounts a second application, edits records or supplies synthetic authority.
export const recoveryRows = page => page.evaluate(async () => {
  return new Promise((resolve, reject) => {
    const q = indexedDB.open('localswim-private-recovery-v1', 1); q.onerror = () => reject(Error('Recovery observation unavailable'));
    q.onsuccess = () => {
      const db = q.result, names = [...db.objectStoreNames], tx = db.transaction(names, 'readonly'), result = {};
      for (const name of names) { const read = tx.objectStore(name).getAll(); read.onsuccess = () => { result[name] = read.result; }; }
      tx.onabort = () => { db.close(); reject(Error('Recovery observation aborted')); };
      tx.oncomplete = () => { db.close(); resolve(result); };
    };
  });
});
