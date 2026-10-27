import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { randomUUID, createHash } from 'node:crypto';
import { test, expect, scenario, requireInventory, cli, board, request, mutate, createEditor, createCard, cardArticle, refresh, save, recoveryRows, control } from '../lib/browser.mjs';

const stats = fixture => control(fixture, { op: 'status' });
const status = (page, text) => page.getByRole('status').filter({ hasText: text });
async function clear(page) {
  await page.getByRole('button', { name: 'Clear all private recovery', exact: true }).click();
  await page.getByLabel('Discard selected private prose and evidence', { exact: false }).check();
  await page.getByLabel('Unresolved outcomes may become unknowable', { exact: false }).check();
  await page.getByRole('button', { name: 'Confirm private discard', exact: true }).click();
  await expect(status(page, 'Selected private recovery discarded.')).toBeVisible();
}
test.beforeEach(async ({ app }) => { await clear(app); });

scenario('V02-LOCAL-01', 'normal HTTP entry automatically bootstraps cookie and measured storage', async ({ app, run, profile, fixture }) => {
  expect(app.url()).toBe(run.origin + '/');
  await expect(status(app, 'Connected to local board. Private recovery is ready.')).toBeVisible();
  expect(await app.getByLabel('One-use session code', { exact: false }).count()).toBe(0);
  const cookies = await profile.context.cookies(run.origin);
  const cookie = cookies.find(value => value.httpOnly && value.sameSite === 'Strict');
  expect(Boolean(cookie && !cookie.secure && cookie.domain === '127.0.0.1' && cookie.path === '/')).toBe(true);
  // Only attributes are asserted; never print a credential-bearing cookie DTO.
  expect(await app.evaluate(() => document.cookie)).toBe('');
  expect(board(run).identity.boardId).toBe(fixture.ready.boardId);
});

scenario('V02-SHARED-01', 'actual browser and actual CLI writes share current state and P3 default', async ({ app, run }) => {
  const card = await createCard(app, run, 'DOM <inert> Ω ' + randomUUID());
  expect(card.priority).toBe('P3'); expect(card.lane).toBe('Ready for Work');
  mutate(run, 'card edit', card, { description: 'CLI changed exact text Ω', priority: 'P4' });
  await refresh(app);
  await expect(cardArticle(app, card)).toContainText('CLI changed exact text Ω');
  await expect(cardArticle(app, card)).toContainText('P4');
  await cardArticle(app, card).getByRole('button', { name: 'Add comment', exact: true }).click();
  await app.getByLabel('text', { exact: true }).fill('DOM comment <script>inert</script>'); await save(app);
  const current = board(run);
  expect(current.comments.some(row => row.cardId === card.cardId && row.text === 'DOM comment <script>inert</script>')).toBe(true);
  expect(cli(run, ['card', 'history', card.cardId]).result).toBeTruthy();
});

scenario('V02-WORKFLOW-01', 'bot handoff then explicit current human approval controls Completed', async ({ app, run }) => {
  const card = await createCard(app, run);
  await cardArticle(app, card).getByRole('button', { name: 'Start work', exact: true }).click(); await save(app);
  expect(board(run).cards.find(row => row.cardId === card.cardId).lane).toBe('In Progress');
  mutate(run, 'card review-handoff', card, {});
  expect(board(run).cards.find(row => row.cardId === card.cardId).lane).toBe('Ready for Review');
  await refresh(app); await cardArticle(app, card).getByRole('button', { name: 'Approve', exact: true }).click();
  expect(board(run).cards.find(row => row.cardId === card.cardId).lane).toBe('Ready for Review');
  await save(app);
  const done = board(run).cards.find(row => row.cardId === card.cardId);
  expect(done.lane).toBe('Completed'); expect(done.currentApprovalId).toBeTruthy();
});

scenario('V02-ASSIGNMENT-01', 'assignment is available through actual product controls and shared state', async ({ app, run }) => {
  const card = await createCard(app, run); await cardArticle(app, card).getByRole('button', { name: 'Edit card', exact: true }).click();
  // Required product behavior. Absence is a visible failure, never API-only coverage.
  const assignment = app.getByLabel('Assignee', { exact: true });
  await expect(assignment, 'Accepted assignment scope requires an actual user-facing control').toBeVisible();
  const bot = board(run).actors.find(actor => actor.kind === 'automation'); expect(bot).toBeTruthy();
  await assignment.selectOption(bot.actorId); await save(app);
  expect(board(run).cards.find(row => row.cardId === card.cardId).ownerId).toBe(bot.actorId);
  mutate(run, 'card edit', card, { ownerId: null });
  await refresh(app); await cardArticle(app, card).getByRole('button', { name: 'Edit card', exact: true }).click();
  await expect(app.getByLabel('Assignee', { exact: true })).toHaveValue('');
  expect(board(run).cards.find(row => row.cardId === card.cardId).ownerId ?? null).toBeNull();
});

scenario('V02-RELATIONS-01', 'DOM add/remove relationships and CLI reads preserve priority', async ({ app, run }) => {
  const first = await createCard(app, run), peer = await createCard(app, run);
  await cardArticle(app, first).getByRole('button', { name: 'Add link', exact: true }).click();
  await app.getByLabel('kind', { exact: true }).selectOption('blocking');
  await app.getByLabel('targetCardId', { exact: true }).fill(peer.cardId); await save(app);
  let current = board(run), link = current.links.find(row => row.cardId === first.cardId && row.target.cardId === peer.cardId && row.active);
  expect(link).toBeTruthy(); expect(current.cards.find(row => row.cardId === first.cardId).priority).toBe('P3');
  await refresh(app); await cardArticle(app, first).getByRole('button', { name: 'Remove link', exact: true }).click();
  await app.getByLabel('linkId', { exact: true }).fill(link.linkId); await save(app);
  current = board(run);
  // CurrentBoard contains active relationships; removed history is a separate read.
  expect(current.cards.find(row => row.cardId === first.cardId).linkIds).not.toContain(link.linkId);
  expect(current.links.some(row => row.linkId === link.linkId && row.active)).toBe(false);
});

scenario('V02-AUTHORITY-01', 'actual automation denies approve and every completion or human-authority bypass', async ({ app, run }) => {
  const card = await createCard(app, run);
  mutate(run, 'card edit', card, { lane: 'In Progress' }); mutate(run, 'card review-handoff', card, {});
  const before = board(run);
  mutate(run, 'card approve', card, { policyVersion: before.policyVersion }, 2);
  mutate(run, 'card edit', card, { lane: 'Completed' }, 2);
  mutate(run, 'card create', null, { title: 'Forbidden completion', description: '', lane: 'Completed' }, 2);
  cli(run, ['card', 'approve', card.cardId, '--input', '-', '--actor', 'human'], request(run, card, { policyVersion: before.policyVersion }), 1);
  cli(run, ['grant', 'create', '--input', '-'], {}, 1);
  cli(run, ['batch', 'apply', '--input', '-'], {}, 1);
  // Direct HTTP admission probes are separate from positive DOM coverage.
  const metadata = JSON.parse(readFileSync(resolve(run.dataDirectory, 'service.json'), 'utf8'));
  const base = '/api/v1/boards/' + before.identity.boardId + '/cards/' + card.cardId;
  for (const [method, path, input, command, denialCodes] of [
    ['POST', base + '/approve', { policyVersion: before.policyVersion }, 'approve', ['FORBIDDEN']],
    ['PATCH', base, { lane: 'Completed' }, 'card.edit', ['INVALID_INPUT']],
    ['POST', '/api/v1/grants', { granteeActorId: metadata.identity.botId, kind: 'human', rights: ['approve', 'read'] }, 'grant.create', ['NOT_FOUND', 'FORBIDDEN']],
  ]) {
    const wire = request(run, path === '/api/v1/grants' ? null : card, input);
    const response = await fetch(run.origin + path, { method, headers: { Authorization: 'Bearer ' + metadata.botToken, 'Content-Type': 'application/json' }, body: JSON.stringify(wire) });
    const body = await response.json();
    if (response.status === 200) {
      // HTTP 200 also carries durable NOT_COMMITTED receipts. Validate the
      // actual typed envelope and its original operation/actor binding.
      const terminalType = { approve: 'Approve', 'card.edit': 'CardEdit', 'grant.create': 'GrantCreate' }[command];
      await app.evaluate(async ({ body, terminalType }) => {
        const { validate } = await import('/js/api/boundary.js');
        validate('transport:Terminal' + terminalType + 'NotcommittedFull', body);
      }, { body, terminalType });
      expect(body).toMatchObject({ apiVersion: 1, outcome: 'not_committed', opId: wire.opId,
        boardId: before.identity.boardId, incarnation: wire.incarnation, actorId: metadata.identity.botId,
        command, target: wire.target, expectedRevision: wire.expectedRevision, expectedCardRevision: wire.expectedCardRevision,
        revision: null, cardRevision: null, retention: 'full', removal: null, resultRefs: [], byteFacts: [] });
      expect(denialCodes).toContain(body.code);
      expect(body.result.error.code).toBe(body.code);
    } else {
      expect([400, 403, 404]).toContain(response.status);
      await app.evaluate(async body => { const { validate } = await import('/js/api/boundary.js'); validate('transport:NonterminalRefusal', body); }, body);
      expect(body.outcome).toBe('refused'); expect(denialCodes).toContain(body.error.code);
      if (body.opId !== undefined) expect(body.opId).toBe(wire.opId);
    }
  }
  const after = board(run); expect(after.revision).toBe(before.revision);
  expect(after.cards.find(row => row.cardId === card.cardId).lane).toBe('Ready for Review');
  expect(after.cards).toEqual(before.cards); expect(after.approvals).toEqual(before.approvals);
});

scenario('V02-CONFLICT-01', 'stale DOM save retains caller input and exposes current CLI text', async ({ app, run }) => {
  const card = await createCard(app, run); await cardArticle(app, card).getByRole('button', { name: 'Edit card', exact: true }).click();
  await app.getByLabel('description', { exact: true }).fill('Retained stale DOM input');
  mutate(run, 'card edit', card, { description: 'Concurrent CLI winner' }); const revision = board(run).revision;
  await app.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(status(app, /Conflict|Definitive noncommit/).first()).toBeVisible();
  await expect(app.getByLabel('description', { exact: true })).toHaveValue('Retained stale DOM input');
  await expect(app.getByRole('region', { name: 'Current confirmed text' })).toContainText('Concurrent CLI winner');
  expect(board(run).revision).toBe(revision);
});

scenario('V02-INPUT-01', 'invalid input refuses before mutation and remains editable', async ({ app, fixture }) => {
  await createEditor(app, ''); const before = await stats(fixture);
  await app.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(status(app, 'Not dispatched.').first()).toBeVisible();
  await expect(app.getByLabel('description', { exact: true })).toHaveValue('Synthetic private input');
  await expect(app.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
  expect((await stats(fixture)).mutationCount).toBe(before.mutationCount);
});

scenario('V02-KEYBOARD-01', 'Tab Enter saves and explicit current read restores card focus', async ({ app, run }) => {
  await createEditor(app);
  const visited = [];
  for (let n = 0; n < 5; n++) {
    await app.keyboard.press('Tab');
    visited.push(await app.evaluate(() => document.activeElement?.getAttribute('name') ?? document.activeElement?.textContent));
    if (await app.getByRole('button', { name: 'Save', exact: true }).evaluate(node => node === document.activeElement)) break;
  }
  expect(visited).toContain('priority'); expect(visited).toContain('ownerId');
  await expect(app.getByRole('button', { name: 'Save', exact: true })).toBeFocused();
  await app.keyboard.press('Enter'); await expect(status(app, 'Confirmed saved.').first()).toBeVisible();
  await refresh(app); const edit = app.getByRole('button', { name: 'Edit card', exact: true }).last();
  await edit.focus(); const key = await edit.getAttribute('data-focus-key'), original = await edit.elementHandle();
  // Dispatch the actual public DOM refresh control without changing focus first.
  await app.getByRole('button', { name: 'Read current board', exact: true }).dispatchEvent('click');
  await expect.poll(() => original.evaluate(node => node.isConnected)).toBe(false);
  await expect.poll(() => app.evaluate(key => document.activeElement?.getAttribute('data-focus-key') === key, key)).toBe(true);
  expect(board(run).cards.length).toBeGreaterThan(0);
});

scenario('V02-IME-01', 'CDP composition on actual editor blocks Enter and retains composed text', async ({ app, profile, fixture }) => {
  await createEditor(app); const before = await stats(fixture), cdp = await profile.context.newCDPSession(app);
  try {
    await app.getByLabel('description', { exact: true }).focus();
    await cdp.send('Input.imeSetComposition', { text: '\u6cf3', selectionStart: 1, selectionEnd: 1 });
    await expect(app.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
    await app.keyboard.press('Enter'); expect((await stats(fixture)).mutationCount).toBe(before.mutationCount);
    await cdp.send('Input.imeSetComposition', { text: '', selectionStart: 0, selectionEnd: 0 });
    await cdp.send('Input.insertText', { text: '\u6cf3' });
  } finally { await cdp.detach(); }
  expect(await app.getByLabel('description', { exact: true }).inputValue()).toContain('\u6cf3');
  await expect(app.getByRole('button', { name: 'Save', exact: true })).toBeEnabled(); await save(app);
  expect((await stats(fixture)).mutationCount).toBe(before.mutationCount + 1);
});

scenario('V02-LATE-01', 'post-handler delayed real response preserves newer input and focus', async ({ app, run, fixture }) => {
  await createEditor(app, 'Delayed immutable original'); await control(fixture, { op: 'delayNext' });
  try {
    await app.getByRole('button', { name: 'Save', exact: true }).click();
    await expect.poll(async () => (await stats(fixture)).delayReached).toBe(true);
    const input = app.getByLabel('description', { exact: true }); await input.fill('Newer retained text'); await input.focus();
    await control(fixture, { op: 'release' }); await expect(status(app, 'Confirmed saved.').first()).toBeVisible();
    await expect(input).toHaveValue('Newer retained text'); await expect(input).toBeFocused();
    expect(board(run).cards.find(row => row.title === 'Delayed immutable original').description).toBe('Synthetic private input');
  } finally { await control(fixture, { op: 'release' }); }
});

scenario('V02-RELOAD-01', 'lost real response reload uses original immutable operation without replay', async ({ app, run, fixture }) => {
  const title = await createEditor(app); const before = await stats(fixture);
  await control(fixture, { op: 'dropNext' }); await app.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(status(app, 'Original outcome unknown').first()).toBeVisible();
  const original = await recoveryRows(app);
  expect(original.submissions).toHaveLength(1);
  await app.reload({ waitUntil: 'networkidle' }); await expect(status(app, 'Private recovery is ready.')).toBeVisible();
  expect((await stats(fixture)).mutationCount).toBe(before.mutationCount + 1);
  const lookupRequest = app.waitForRequest(value => new URL(value.url()).pathname.endsWith('/operations/' + original.submissions[0].opId));
  await app.getByRole('button', { name: 'Look up this original operation', exact: true }).click();
  const lookup = await lookupRequest;
  expect(lookup.method()).toBe('GET');
  expect(new URL(lookup.url()).searchParams.get('fingerprint')).toBe(original.submissions[0].evidence.fingerprint);
  await expect(status(app, 'Confirmed saved.').first()).toBeVisible();
  expect((await stats(fixture)).mutationCount).toBe(before.mutationCount + 1);
  expect(board(run).cards.filter(card => card.title === title)).toHaveLength(1);
  const after = await recoveryRows(app);
  expect(after.submissions).toEqual(original.submissions);
});

scenario('V02-RESTART-01', 'same-store restart rotates live epoch reconnect retains input without replay', async ({ app, run, fixture }) => {
  await createEditor(app, 'Restart retained draft'); const before = await stats(fixture), old = board(run);
  const next = await control(fixture, { op: 'restart' }); expect(next.epoch).not.toBe(before.epoch);
  await app.getByRole('button', { name: 'Reconnect local service', exact: true }).click();
  await expect(status(app, 'Connected to local board. Private recovery is ready.')).toBeVisible();
  await expect(app.getByLabel('title', { exact: true })).toHaveValue('Restart retained draft');
  expect(board(run).identity).toEqual(old.identity); expect(board(run).revision).toBe(old.revision);
  expect((await stats(fixture)).mutationCount).toBe(before.mutationCount);
});

scenario('V02-UNKNOWN-01', 'stop waiting plus restart retains original GET lookup and never replays', async ({ app, run, fixture }) => {
  const title = await createEditor(app); const before = await stats(fixture);
  await control(fixture, { op: 'delayNext' });
  try {
    await app.getByRole('button', { name: 'Save', exact: true }).click(); await expect.poll(async () => (await stats(fixture)).delayReached).toBe(true);
    await app.getByRole('button', { name: 'Stop waiting', exact: true }).click();
    await expect(status(app, 'does not cancel a possible commit').first()).toBeVisible();
    const original = await recoveryRows(app); expect(original.submissions).toHaveLength(1);
    // Restart releases the delayed real response; that original response may
    // settle before reload. It is never evidence of a replacement submission.
    await control(fixture, { op: 'restart' });
    await app.getByRole('button', { name: 'Reconnect local service', exact: true }).click();
    await expect(status(app, 'Connected to local board. Private recovery is ready.')).toBeVisible();
    await app.reload({ waitUntil: 'networkidle' });
    const button = app.getByRole('button', { name: 'Look up this original operation', exact: true }); await expect(button).toBeVisible();
    expect((await stats(fixture)).mutationCount).toBe(before.mutationCount + 1);
    const lookupRequest = app.waitForRequest(value => new URL(value.url()).pathname.endsWith('/operations/' + original.submissions[0].opId));
    await button.click(); const lookup = await lookupRequest;
    expect(lookup.method()).toBe('GET');
    expect(new URL(lookup.url()).searchParams.get('fingerprint')).toBe(original.submissions[0].evidence.fingerprint);
    await expect(status(app, 'Confirmed saved.').first()).toBeVisible();
    expect((await stats(fixture)).mutationCount).toBe(before.mutationCount + 1);
    expect(board(run).cards.filter(card => card.title === title)).toHaveLength(1);
    expect((await recoveryRows(app)).submissions).toEqual(original.submissions);
  } finally { await control(fixture, { op: 'release' }); }
});

scenario('V02-IDB-01', 'labeled IDB factory denial reaches normal bootstrap read-only refusal', async ({ app, run, profile, fixture }) => {
  const page = await profile.context.newPage(), before = await stats(fixture);
  try {
    await page.addInitScript(() => { Object.defineProperty(window, 'indexedDB', { configurable: true, value: { open() { throw new DOMException('Qualification denial', 'SecurityError'); } } }); });
    await page.goto(run.origin, { waitUntil: 'networkidle' });
    await expect(status(page, 'Connected for reading. Private recovery storage is unavailable')).toBeVisible();
    await page.getByRole('button', { name: 'Create card', exact: true }).click();
    await page.getByLabel('title', { exact: true }).fill('Denied IDB retained input');
    await page.getByRole('button', { name: 'Save', exact: true }).click(); await expect(status(page, 'Not dispatched.').first()).toBeVisible();
    await expect(page.getByLabel('title', { exact: true })).toHaveValue('Denied IDB retained input');
    expect((await stats(fixture)).mutationCount).toBe(before.mutationCount);
  } finally { await page.close(); }
});

scenario('V02-QUOTA-01', 'native CDP quota exhaustion refuses actual DOM dispatch and retains input', async ({ app, run, profile, fixture }) => {
  test.setTimeout(120_000); const cdp = await profile.context.newCDPSession(app), before = await stats(fixture);
  try {
    await cdp.send('Storage.overrideQuotaForOrigin', { origin: run.origin, quotaSize: 1024 });
    const quota = await cdp.send('Storage.getUsageAndQuota', { origin: run.origin }); expect(quota.overrideActive).toBe(true); expect(quota.quota).toBe(1024);
    await new Promise(resolve => setTimeout(resolve, 31_000));
    const error = await app.evaluate(() => new Promise(resolve => {
      const q = indexedDB.open('v02-quota-component-probe', 1); q.onupgradeneeded = () => q.result.createObjectStore('probe'); q.onerror = () => resolve(q.error?.name);
      q.onsuccess = () => { const db = q.result, tx = db.transaction('probe', 'readwrite', { durability: 'strict' }), bytes = new Uint8Array(8 * 1024 * 1024);
        for (let n = 0; n < bytes.length; n += 65536) crypto.getRandomValues(bytes.subarray(n, n + 65536));
        tx.objectStore('probe').put(bytes, 'probe'); tx.onerror = () => {}; tx.onabort = () => { db.close(); resolve(tx.error?.name); }; tx.oncomplete = () => { db.close(); resolve('unexpected completion'); };
      };
    })); expect(error).toBe('QuotaExceededError');
    await app.getByRole('button', { name: 'Create card', exact: true }).click(); await app.getByLabel('title', { exact: true }).fill('Quota retained');
    await app.getByLabel('description', { exact: true }).fill('q'.repeat(60_000)); await app.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(status(app, 'Not dispatched.').first()).toBeVisible(); await expect(app.getByLabel('description', { exact: true })).toHaveValue('q'.repeat(60_000));
    expect((await stats(fixture)).mutationCount).toBe(before.mutationCount);
  } finally { await cdp.send('Storage.overrideQuotaForOrigin', { origin: run.origin }); await cdp.detach(); }
});

scenario('V02-TABS-01', 'two actual tabs preserve distinct drafts and stale-base conflict input', async ({ app, run, profile }) => {
  const card = await createCard(app, run), other = await profile.context.newPage();
  try {
    await other.goto(run.origin, { waitUntil: 'networkidle' });
    await cardArticle(app, card).getByRole('button', { name: 'Edit card', exact: true }).click();
    await cardArticle(other, card).getByRole('button', { name: 'Edit card', exact: true }).click();
    await app.getByLabel('description', { exact: true }).fill('First tab winner');
    await other.getByLabel('description', { exact: true }).fill('Second tab retained'); await save(app);
    await other.getByRole('button', { name: 'Save', exact: true }).click(); await expect(status(other, /Conflict|Definitive noncommit/).first()).toBeVisible();
    await expect(other.getByLabel('description', { exact: true })).toHaveValue('Second tab retained');
    expect(board(run).cards.find(row => row.cardId === card.cardId).description).toBe('First tab winner');
    const rows = await recoveryRows(app), drafts = JSON.stringify(rows.drafts);
    expect(drafts.includes('First tab winner') && drafts.includes('Second tab retained')).toBe(true);
  } finally { await other.close(); }
});

scenario('V02-CLEAR-01', 'explicit two-warning private discard never changes authoritative board', async ({ app, run, fixture }) => {
  await createEditor(app); const before = await stats(fixture), revision = board(run).revision;
  await app.getByRole('button', { name: 'Clear all private recovery', exact: true }).click();
  const confirm = app.getByRole('button', { name: 'Confirm private discard', exact: true }); await expect(confirm).toBeDisabled();
  await app.getByLabel('Discard selected private prose and evidence', { exact: false }).check(); await expect(confirm).toBeDisabled();
  await app.getByLabel('Unresolved outcomes may become unknowable', { exact: false }).check(); await confirm.click();
  await expect(status(app, 'Selected private recovery discarded.')).toBeVisible();
  const rows = await recoveryRows(app); expect(rows.drafts).toHaveLength(0); expect(rows.submissions).toHaveLength(0);
  expect(board(run).revision).toBe(revision); expect((await stats(fixture)).mutationCount).toBe(before.mutationCount);
});

scenario('V02-BYTES-01', 'DOM attachment and browser/CLI downloads preserve exact binary bytes', async ({ app, run }) => {
  const card = await createCard(app, run), bytes = Buffer.from([0, 255, 10, 13, 128, 60, 0, 226, 152, 131]);
  await app.getByRole('button', { name: 'Read current attachment board', exact: true }).click();
  await app.getByLabel('Attachment target card', { exact: true }).selectOption(card.cardId);
  await app.getByRole('button', { name: 'Open attachment editor', exact: true }).click();
  await app.getByLabel('Attachment file', { exact: true }).setInputFiles({ name: 'synthetic.bin', mimeType: 'application/octet-stream', buffer: bytes });
  await expect(app.getByRole('button', { name: 'Attach once', exact: true })).toBeEnabled(); await app.getByRole('button', { name: 'Attach once', exact: true }).click();
  await expect(status(app, 'Confirmed attachment saved.')).toBeVisible();
  const attachment = board(run).attachments.find(row => row.cardId === card.cardId); expect(attachment).toBeTruthy();
  expect(attachment.sha256).toBe(createHash('sha256').update(bytes).digest('hex')); expect(attachment.byteLength).toBe(bytes.length);
  await app.getByRole('button', { name: 'Read current attachment board', exact: true }).click();
  const downloadPromise = app.waitForEvent('download');
  await app.getByRole('article').filter({ hasText: attachment.sha256 }).getByRole('button', { name: 'Offer attachment download', exact: true }).click();
  const download = await downloadPromise, path = await download.path(); expect(await download.failure()).toBeNull(); expect(readFileSync(path).equals(bytes)).toBe(true);
  // Fresh attempt directory supplies uniqueness; leave room for the native
  // publication partial suffix within its accepted Windows path bound.
  const destination = resolve(run.artifactDirectory, 'attachment.bin');
  cli(run, ['attachment', 'download', attachment.attachmentId, '--destination', destination]); expect(readFileSync(destination).equals(bytes)).toBe(true);
});

scenario('V02-PRIVACY-01', 'actual browser storage excludes authentication and inline execution', async ({ app, run }) => {
  await createCard(app, run, '<img src=x onerror=alert(1)> ' + randomUUID());
  const rows = await recoveryRows(app);
  const forbidden = /^(authorization|bearer|token|credential|credentials|password|secret|sessioncode|cookie|privatekey)$/i;
  const scan = value => value && typeof value === 'object' && Object.entries(value).some(([key, child]) => forbidden.test(key) || scan(child));
  expect(Boolean(scan(rows))).toBe(false);
  const metadata = JSON.parse(readFileSync(resolve(run.dataDirectory, 'service.json'), 'utf8'));
  expect(JSON.stringify(rows).includes(metadata.botToken)).toBe(false);
  expect(await app.evaluate(() => ({ local: Object.keys(localStorage), session: Object.keys(sessionStorage), cookie: document.cookie, maliciousImage: !!document.querySelector('img[onerror]') }))).toEqual({ local: [], session: [], cookie: '', maliciousImage: false });
});

requireInventory();
