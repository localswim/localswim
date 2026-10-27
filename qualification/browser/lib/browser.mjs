import { requireProfileAudit, strictProbe } from './profile-probe.mjs';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { writeFileSync } from 'node:fs';
import { readRun } from './run-input.mjs';
import { control, requireReady } from './control.mjs';

const require = createRequire(resolve(dirname(fileURLToPath(import.meta.url)), '../../../web/package.json'));
const { test: base, expect, chromium } = require('@playwright/test');
export { expect, control };
const run = readRun();
const declared = new Set();

export const test = base.extend({
  run: [async ({}, use) => { await requireReady(run); await use(run); }, { scope: 'worker' }],
  profile: [async ({ run }, use) => {
    await control(run, { op: 'provisionAutomation' });
    const context = await chromium.launchPersistentContext(run.profileDirectory, {
      executablePath: run.chromeExecutable,
      headless: true,
      ignoreHTTPSErrors: false,
      acceptDownloads: false,
      serviceWorkers: 'block',
    });
    try {
      if (context.browser()?.version() !== run.browserVersion) throw Error('Actual browser version mismatch');
      const page = await context.newPage();
      const response = await page.goto(run.origin, { waitUntil: 'networkidle' });
      if (!response || response.status() !== 200 || page.url() !== run.origin + '/' ||
          await response.headerValue('x-localswim-installation') !== run.installationId) throw Error('Trusted installation bootstrap failed');
      const security = await response.securityDetails();
      if (!security || !['TLS 1.2', 'TLS 1.3'].includes(security.protocol)) throw Error('Actual browser TLS evidence missing');
      const storage = await strictProbe(page, run);
      if (!storage.completeBeforeReadback || !storage.strict || !storage.identical) throw Error('Profile strict storage evidence failed');
      const liveACL = await control(run, { op: 'auditProfile' });
      requireProfileAudit(liveACL, run.profileId);
      const evidence = { evidenceId: run.evidenceId, installationId: run.installationId, profileId: run.profileId,
        browserVersion: context.browser().version(), manifestSHA256: run.manifestSHA256,
        strict: storage.strict, completeBeforeReadback: storage.completeBeforeReadback, identical: storage.identical,
        nativeACLVerified: liveACL.nativeACLVerified, profileACLPolicy: liveACL.policy,
        tls: { protocol: security.protocol, subjectName: security.subjectName, issuer: security.issuer } };
      writeFileSync(resolve(run.artifactDirectory, 'profile-preflight.json'), JSON.stringify(evidence, null, 2) + '\n', { flag: 'wx' });
      await page.close();
      await use({ context, evidence, qualification: { installationId: run.installationId, profileId: run.profileId,
        strictDurabilityQualified: true, evidenceReference: run.evidenceId + ':profile-preflight' } });
    } finally {
      await context.close();
      const audit = await control(run, { op: 'auditProfile' });
      requireProfileAudit(audit, run.profileId);
      writeFileSync(resolve(run.artifactDirectory, 'profile-acl-after.json'), JSON.stringify(audit, null, 2) + '\n', { flag: 'wx' });
    }
  }, { scope: 'worker' }],
  app: async ({ run, profile }, use) => {
    const page = await profile.context.newPage();
    await page.goto(run.origin, { waitUntil: 'networkidle' });
    await mount(page, run, profile.qualification);
    try { await use(page); }
    finally {
      if (!page.isClosed()) await page.evaluate(async () => { try { await window.__v02?.access.logout(); } catch {} });
      await page.close();
    }
  },
});

export function scenario(id, title, body) {
  if (declared.has(id)) throw Error('Duplicate mandatory case');
  declared.add(id); test(id + ' ' + title, body);
}
export function requireInventory(inventory) {
  if (inventory.required.length !== declared.size || inventory.required.some(id => !declared.has(id))) throw Error('Missing mandatory browser case');
}

test.afterEach(async ({}, info) => {
  const id = info.title.split(' ', 1)[0];
  if (!/^V02-[A-Z]+-\d+$/.test(id)) throw Error('Unidentified browser case');
  const record = { id, status: info.status, expectedStatus: info.expectedStatus, retry: info.retry,
    errors: info.errors.length, durationMs: info.duration, evidenceId: run.evidenceId,
    manifestSHA256: run.manifestSHA256, browserVersion: run.browserVersion };
  writeFileSync(resolve(run.artifactDirectory, id + '.json'), JSON.stringify(record, null, 2) + '\n', { flag: 'wx' });
});


export async function mount(page, run, qualification) {
  await page.getByRole('heading', { name: 'Localswim session', exact: true }).waitFor();
  await page.evaluate(async ({ installationId, profileId, qualification }) => {
    window.__v02?.view.destroy(); window.__v02?.access.clear(); window.__v02?.access.client.close();
    const { mountApplication } = await import('/js/app.js');
    const { openSessionClient } = await import('/js/api/w03-session.js');
    const { SessionView } = await import('/js/session-ui/view.js');
    const { SessionWorkspace } = await import('/js/session-ui/workspace.js');
    const root = document.getElementById('app');
    const old = await mountApplication(root, { installationId, profileId: 'unqualified' }); old.destroy();
    const access = await openSessionClient({ installationId, profileId, ...(qualification ? { qualification } : {}) });
    const state = { access, view: undefined, workspace: undefined, installationId, profileId, qualification };
    state.view = new SessionView(root, access, (content, client, session) => { state.workspace = new SessionWorkspace(content, client, session); return state.workspace; });
    window.__v02 = state;
  }, { installationId: run.installationId, profileId: run.profileId, qualification });
}

export async function login(page, run, subject = 'automation') {
  let secret = await control(run, { op: 'code', subject });
  const actorId = secret.actorId;
  if (typeof secret.code !== 'string' || secret.code.length !== 22) throw Error('Ephemeral code unavailable');
  await page.evaluate(value => {
    const input = document.querySelector('input[name="session-code"]');
    input.value = value; input.dispatchEvent(new InputEvent('input', { bubbles: true }));
  }, secret.code);
  secret.code = ''; secret = undefined;
  await page.getByRole('button', { name: 'Reauthenticate with code', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Authenticated actor ' + actorId })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Create card', exact: true })).toBeVisible();
  return actorId;
}

export async function createEditor(page, title = 'V02 synthetic card') {
  await page.getByRole('button', { name: 'Create card', exact: true }).click();
  await page.getByLabel('title', { exact: false }).fill(title);
  await page.getByLabel('description', { exact: false }).fill('V02 private synthetic prose');
  await expect(page.getByRole('status').filter({ hasText: 'Recovery saved' }).first()).toBeVisible();
}

export async function privateInventory(page) {
  return page.evaluate(() => window.__v02.access.client.recovery.inventory());
}
