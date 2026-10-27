import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';

const require = createRequire(resolve(import.meta.dirname, '../../../web/package.json'));
const { chromium, expect } = require('@playwright/test');
const pin = JSON.parse(readFileSync(resolve(import.meta.dirname, '../../../qualification/environment.json'), 'utf8')).browser.candidate;
if (process.versions.node !== '24.20.0' || !/^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/.test(process.env.L02_ORIGIN ?? '')) throw Error('Pinned local smoke inputs required');
const browser = await chromium.launch({ executablePath: process.env.L02_BROWSER, headless: true });
const watchdog = setTimeout(() => { void browser.close(); }, 120_000);
const context = await browser.newContext();
try {
  expect(browser.version()).toBe(pin.version);
  const page = await context.newPage();
  let browserMutationCount = 0;
  page.on('request', request => {
    if (request.method() === 'POST' && /\/api\/v1\/boards\/[^/]+\/cards$/.test(new URL(request.url()).pathname)) browserMutationCount++;
  });
  await page.goto(process.env.L02_ORIGIN, { waitUntil: 'networkidle' });
  await expect(page.getByText('Connected to local board. Private recovery is ready.', { exact: true })).toBeVisible();
  await expect(page.getByText(/^Current at board revision /)).toBeVisible();
  await expect(page.locator('input[name="session-code"]')).toHaveCount(0);
  const browserCreated = 'Local browser ' + randomUUID();
  await page.getByRole('button', { name: 'Create card', exact: true }).click();
  await page.locator('input[name="title"]').fill(browserCreated);
  await page.locator('textarea[name="description"]').fill('Synthetic normal HTTP product smoke');
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Original operation outcomes', exact: true }).getByRole('status')).toHaveText(/Confirmed saved\.$/);
  const cli = (args, input) => {
    const result = spawnSync(process.env.L02_BINARY, ['--data', process.env.L02_DATA, '--json', ...args], { input, encoding: 'utf8', windowsHide: true, timeout: 30_000 });
    if (result.error || result.status !== 0) throw Error('Actual CLI invocation failed');
    return JSON.parse(result.stdout);
  };
  const board = cli(['board', 'read']);
  expect(board.result.cards.some(card => card.title === browserCreated)).toBe(true);
  const cliCreated = 'Local CLI ' + randomUUID();
  const result = cli(['card', 'create', '--input', '-'], JSON.stringify({ opId: randomUUID(), incarnation: board.result.identity.incarnation,
    target: null, expectedRevision: board.result.revision, expectedCardRevision: null,
    input: { title: cliCreated, description: 'Synthetic automatic bot-channel smoke', priority: 'P3' } }));
  expect(result.outcome).toBe('committed');
  await page.getByRole('button', { name: 'Read current board', exact: true }).click();
  await expect(page.getByRole('heading', { name: new RegExp(cliCreated), exact: false })).toBeVisible();
  await page.getByRole('button', { name: 'Reconnect local service', exact: true }).click();
  await expect(page.getByText('Connected to local board. Private recovery is ready.', { exact: true })).toBeVisible();
  await page.reload({ waitUntil: 'networkidle' });
  await expect(page.getByText('Connected to local board. Private recovery is ready.', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: new RegExp(browserCreated), exact: false })).toBeVisible();
  expect(browserMutationCount).toBe(1);
  process.stdout.write(JSON.stringify({ browserCreated, cliCreated, browserMutationCount }));
} finally {
  clearTimeout(watchdog);
  await context.close();
  await browser.close();
}
