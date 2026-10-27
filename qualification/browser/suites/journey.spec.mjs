import { readFileSync } from 'node:fs';
import { test, expect, scenario, requireInventory, login, mount, createEditor, privateInventory, control } from '../lib/browser.mjs';

const inventory = JSON.parse(readFileSync(new URL('../inventory.json', import.meta.url), 'utf8'));
const stats = run => control(run, { op: 'status' });
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
async function intent(page, run, command = 'card.create', target = null, input = null) {
  return page.evaluate(async ({ boardId, command, target, input }) => {
    const client = window.__v02.access.client, session = await client.session(), board = await client.board(boardId);
    const card = target ? board.result.cards.find(c => c.cardId === target.cardId) : undefined;
    return { command, draftId: crypto.randomUUID(), context: client.context(board, session), request: {
      opId: crypto.randomUUID(), incarnation: board.result.identity.incarnation, target,
      expectedRevision: board.result.revision, expectedCardRevision: card?.revision ?? null,
      input: input ?? { title: 'V02 fixture ' + crypto.randomUUID(), description: 'Synthetic private browser evidence', priority: 'P3' },
    } };
  }, { boardId: run.boardId, command, target, input });
}
const submit = (page, value) => page.evaluate(value => window.__v02.access.client.submit(value, () => {}), value);

test.beforeEach(async ({ app }) => {
  // Deliberate cleanup of this dedicated synthetic fixture only; never real data.
  await app.evaluate(() => window.__v02.access.client.recovery.clear('all', { discardPrivateProse: true, unresolvedMayBecomeUnknowable: true }));
});

scenario('V02-TLS-01', 'actual trusted HTTPS and installation bootstrap', async ({ app, run, profile }) => {
  expect(app.url()).toBe(run.origin + '/');
  expect(profile.evidence.installationId).toBe(run.installationId);
  expect(profile.evidence.browserVersion).toBe(run.browserVersion);
  expect(['TLS 1.2', 'TLS 1.3']).toContain(profile.evidence.tls.protocol);
});

scenario('V02-PROFILE-01', 'strict completion/readback precedes trusted profile proof', async ({ app, run, profile }) => {
  expect(profile.evidence.strict && profile.evidence.completeBeforeReadback && profile.evidence.identical).toBe(true);
  await mount(app, run, undefined); await login(app, run);
  const before = await stats(run), outcome = await submit(app, await intent(app, run));
  expect(outcome.state).toBe('unsent'); expect((await stats(run)).mutationCount).toBe(before.mutationCount);
});

scenario('V02-SESSION-01', 'one-use exchange derives current actor and grants', async ({ app, run }) => {
  const actor = await login(app, run);
  const current = await app.evaluate(() => window.__v02.access.client.session());
  expect(current.actorId).toBe(actor); expect(current.grant.boardId).toBe(run.boardId);
  expect(current.grant.incarnation).toBe(run.incarnation); expect(current.kind).toBe('automation');
  expect(await app.locator('input[name="session-code"]').inputValue()).toBe('');
  let once = await control(run, { op: 'code', subject: 'automation' });
  const reuse = await app.evaluate(async code => {
    const first = await window.__v02.access.exchange(code); let rejected = false;
    try { await window.__v02.access.exchange(code); } catch { rejected = true; }
    return { actorId: first.actorId, rejected };
  }, once.code);
  once.code = ''; once = undefined;
  expect(reuse).toEqual({ actorId: actor, rejected: true });
});

scenario('V02-ORIGIN-01', 'wrong and null Origin are refused on actual browser transport', async ({ app, run, profile }) => {
  const before = await stats(run), cdp = await profile.context.newCDPSession(app);
  try {
    await cdp.send('Network.enable');
    for (const origin of ['null', 'https://unaccepted.localhost:48443']) {
      await cdp.send('Network.setExtraHTTPHeaders', { headers: { Origin: origin } });
      const result = await app.evaluate(async () => {
        const response = await fetch('/api/v1/session/exchange', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: 'AAAAAAAAAAAAAAAAAAAAAA' }), credentials: 'omit' });
        return { status: response.status, cors: response.headers.get('access-control-allow-origin') };
      });
      expect(result.status).toBe(403); expect(result.cors).toBeNull();
    }
  } finally { await cdp.send('Network.setExtraHTTPHeaders', { headers: {} }); await cdp.detach(); }
  expect((await stats(run)).mutationCount).toBe(before.mutationCount);
});

scenario('V02-AUTOMATION-01', 'automation approval remains unavailable', async ({ app, run }) => {
  await login(app, run);
  const created = await submit(app, await intent(app, run)); expect(created.state).toBe('committed');
  const board = await app.evaluate(id => window.__v02.access.client.board(id), run.boardId);
  const card = board.result.cards.at(-1);
  expect((await app.evaluate(() => window.__v02.access.client.session())).grant.rights).not.toContain('approve');
  await app.getByRole('button', { name: 'Read current board', exact: true }).click();
  const approvalControls = app.getByRole('button', { name: 'Approve', exact: true }); await expect(approvalControls.first()).toBeVisible(); expect(await approvalControls.count()).toBeGreaterThan(0);
  for (const button of await approvalControls.all()) await expect(button).toBeDisabled();
  const before = await stats(run);
  const wire = app.waitForResponse(response => response.url().endsWith('/cards/' + card.cardId + '/approve') && response.request().method() === 'POST');
  const result = await submit(app, await intent(app, run, 'approve', { cardId: card.cardId }, { policyVersion: board.result.policyVersion }));
  expect((await wire).status()).toBe(403);
  expect(result.state).toBe('unknown');
  expect((await stats(run)).revision).toBe(before.revision);
  await expect(app.getByRole('button', { name: 'Create card', exact: true })).toHaveCount(0);
  expect(await app.evaluate(async () => { try { await window.__v02.access.client.session(); return true; } catch { return false; } })).toBe(false);
});

scenario('V02-REVOCATION-01', 'revocation clears visible authority and preserves recovery', async ({ app, run }) => {
  await login(app, run, 'revocable'); await createEditor(app, 'Revocation input retained');
  const before = await privateInventory(app); expect(before.drafts.length).toBeGreaterThan(0);
  await control(run, { op: 'revoke', subject: 'revocable' });
  await app.getByRole('button', { name: 'Read current board', exact: true }).click();
  await expect(app.getByRole('status').filter({ hasText: 'Authentication unavailable or revoked' })).toBeVisible();
  expect((await privateInventory(app)).drafts.length).toBe(before.drafts.length);
  await expect(app.getByRole('button', { name: 'Create card', exact: true })).toHaveCount(0);
});

scenario('V02-IDB-01', 'native opaque-origin IDB denial and fail-closed adapter boundary', async ({ app, run, profile }) => {
  await login(app, run); const before = await stats(run);
  const frameHandle = await app.evaluateHandle(() => {
    const frame = document.createElement('iframe'); frame.setAttribute('sandbox', 'allow-scripts'); document.body.append(frame); return frame;
  });
  const element = frameHandle.asElement(), frame = await element.contentFrame();
  const denied = await frame.evaluate(() => { try { indexedDB.open('v02-opaque-denial'); return false; } catch (error) { return error.name === 'SecurityError'; } });
  expect(denied).toBe(true);
  // Actual native denial above is distinct from this explicit factory-boundary
  // injection. No origin-policy or native-permission claim is inferred from it.
  await app.evaluate(() => {
    window.__v02SavedIDB = Object.getOwnPropertyDescriptor(window, 'indexedDB');
    Object.defineProperty(window, 'indexedDB', { configurable: true, value: { open() { throw new DOMException('Qualification denial', 'SecurityError'); } } });
  });
  try {
    await mount(app, run, profile.qualification); await login(app, run);
    const result = await submit(app, await intent(app, run)); expect(result.state).toBe('unsent');
    expect((await stats(run)).mutationCount).toBe(before.mutationCount);
  } finally {
    await app.evaluate(() => { if (window.__v02SavedIDB) Object.defineProperty(window, 'indexedDB', window.__v02SavedIDB); else delete window.indexedDB; });
    await element.evaluate(node => node.remove());
  }
});

scenario('V02-QUOTA-01', 'native CDP quota and shared logical caps refuse dispatch', async ({ app, run, profile }) => {
  test.setTimeout(120_000);
  await login(app, run); const value = await intent(app, run), before = await stats(run);
  const cdp = await profile.context.newCDPSession(app);
  try {
    await cdp.send('Storage.overrideQuotaForOrigin', { origin: run.origin, quotaSize: 1024 });
    const quota = await cdp.send('Storage.getUsageAndQuota', { origin: run.origin });
    expect(quota.overrideActive).toBe(true); expect(quota.quota).toBe(1024);
    // The pinned browser's bucket-space allowance may outlive a quota override.
    // The independently executed native diagnostic establishes this bounded wait.
    await wait(31_000);
    const nativeError = await app.evaluate(() => new Promise(resolve => {
      const q = indexedDB.open('v02-native-quota-probe', 1);
      q.onupgradeneeded = () => q.result.createObjectStore('probe');
      q.onerror = () => resolve(q.error?.name);
      q.onsuccess = () => {
        const db = q.result, tx = db.transaction('probe', 'readwrite', { durability: 'strict' });
        const data = new Uint8Array(8 * 1024 * 1024);
        for (let n = 0; n < data.length; n += 65536) crypto.getRandomValues(data.subarray(n, n + 65536));
        tx.objectStore('probe').put(data, crypto.randomUUID());
        tx.onerror = () => {};
        tx.onabort = () => { const name = tx.error?.name; db.close(); resolve(name); };
        tx.oncomplete = () => { db.close(); resolve('unexpected completion'); };
      };
    }));
    expect(nativeError).toBe('QuotaExceededError');
    const large = await intent(app, run, 'card.create', null, { title: 'Quota caller input', description: 'q'.repeat(60_000), priority: 'P3' });
    expect((await submit(app, large)).state).toBe('unsent');
    expect((await stats(run)).mutationCount).toBe(before.mutationCount);
  } finally { await cdp.send('Storage.overrideQuotaForOrigin', { origin: run.origin }); await cdp.detach(); }
  const result = await app.evaluate(async context => {
    const store = window.__v02.access.client.recovery;
    for (let n = 0; n < 20; n++) await store.saveDraft({ context, draftId: crypto.randomUUID(), expectedRevision: 0, input: 'Live private draft ' + n });
    let refused = false;
    try { await store.saveDraft({ context, draftId: crypto.randomUUID(), expectedRevision: 0, input: 'Retained caller input' }); } catch (error) { if (error.code !== 'capacity') throw Error('Unexpected draft failure'); refused = true; }
    return { refused, count: (await store.inventory()).drafts.length };
  }, value.context);
  expect(result).toEqual({ refused: true, count: 20 }); expect((await stats(run)).mutationCount).toBe(before.mutationCount);
  const draftBytes = await app.evaluate(async context => {
    const store = window.__v02.access.client.recovery;
    await store.clear('all', { discardPrivateProse: true, unresolvedMayBecomeUnknowable: true });
    const admitted = []; let refused = false; const input = '\u6cf3'.repeat(40_000);
    for (let n = 0; n < 20; n++) {
      const draftId = crypto.randomUUID();
      try { await store.saveDraft({ context, draftId, expectedRevision: 0, input }); admitted.push(draftId); }
      catch (error) { if (error.code !== 'capacity') throw Error('Unexpected draft-byte failure'); refused = true; break; }
    }
    let preserved = true;
    for (const id of admitted) if ((await store.loadDraft(context, id))?.input !== input) preserved = false;
    return { refused, count: admitted.length, preserved, callerLength: input.length, retained: (await store.inventory()).drafts.length };
  }, value.context);
  expect(draftBytes.refused && draftBytes.preserved).toBe(true); expect(draftBytes.count).toBeGreaterThan(0); expect(draftBytes.count).toBeLessThan(20); expect(draftBytes.retained).toBe(draftBytes.count); expect(draftBytes.callerLength).toBe(40_000);
  await app.evaluate(() => window.__v02.access.client.recovery.clear('all', { discardPrivateProse: true, unresolvedMayBecomeUnknowable: true }));
  const submitted = await app.evaluate(async intent => {
    const { prepare } = await import('/js/api/boundary.js'), store = window.__v02.access.client.recovery;
    for (let n = 0; n < 100; n++) {
      intent.request.opId = crypto.randomUUID(); intent.draftId = crypto.randomUUID();
      await store.freezeSubmission(await prepare(intent.command, intent.request, intent.context, intent.draftId));
    }
    let refused = false; intent.request.opId = crypto.randomUUID(); intent.draftId = crypto.randomUUID();
    try { await store.freezeSubmission(await prepare(intent.command, intent.request, intent.context, intent.draftId)); } catch (error) { if (error.code !== 'capacity') throw Error('Unexpected submission failure'); refused = true; }
    return { refused, count: (await store.inventory()).submissions.length };
  }, value);
  expect(submitted).toEqual({ refused: true, count: 100 });
  expect((await submit(app, await intent(app, run))).state).toBe('unsent');
  expect((await stats(run)).mutationCount).toBe(before.mutationCount);
  const submissionBytes = await app.evaluate(async intent => {
    const { prepare } = await import('/js/api/boundary.js'), store = window.__v02.access.client.recovery;
    await store.clear('all', { discardPrivateProse: true, unresolvedMayBecomeUnknowable: true });
    intent.request.input.description = 'b'.repeat(60_000); const accepted = []; let refused = false;
    for (let n = 0; n < 100; n++) {
      intent.request.opId = crypto.randomUUID(); intent.draftId = crypto.randomUUID();
      const evidence = await prepare(intent.command, intent.request, intent.context, intent.draftId);
      try { await store.freezeSubmission(evidence); accepted.push({ id: intent.request.opId, bytes: evidence.requestBytes }); }
      catch (error) { if (error.code !== 'capacity') throw Error('Unexpected submission-byte failure'); refused = true; break; }
    }
    let preserved = true;
    for (const item of accepted) if ((await store.loadSubmission(intent.context, item.id))?.evidence.requestBytes !== item.bytes) preserved = false;
    return { refused, preserved, count: accepted.length, retained: (await store.inventory()).submissions.length, callerLength: intent.request.input.description.length };
  }, value);
  expect(submissionBytes.refused && submissionBytes.preserved).toBe(true); expect(submissionBytes.count).toBeGreaterThan(0); expect(submissionBytes.count).toBeLessThan(100); expect(submissionBytes.retained).toBe(submissionBytes.count); expect(submissionBytes.callerLength).toBe(60_000);
  expect((await stats(run)).mutationCount).toBe(before.mutationCount);
});

scenario('V02-TABS-01', 'real tabs preserve winner and fork stale draft', async ({ app, run, profile }) => {
  await login(app, run); const value = await intent(app, run);
  const other = await profile.context.newPage(); await other.goto(run.origin, { waitUntil: 'networkidle' }); await mount(other, run, profile.qualification); await login(other, run);
  try {
    const draft = await app.evaluate(async context => (await window.__v02.access.client.recovery.saveDraft({ context, draftId: crypto.randomUUID(), expectedRevision: 0, input: 'Winner' })).draft, value.context);
    await app.evaluate(async draft => window.__v02.access.client.recovery.saveDraft({ context: draft.context, draftId: draft.draftId, expectedRevision: draft.revision, input: 'Newer winner' }), draft);
    const fork = await other.evaluate(async draft => window.__v02.access.client.recovery.saveDraft({ context: draft.context, draftId: draft.draftId, expectedRevision: draft.revision, input: 'Other tab retained' }), draft);
    expect(fork.state).toBe('forked'); expect(fork.draft.draftId).not.toBe(draft.draftId);
    const winner = await app.evaluate(async draft => window.__v02.access.client.recovery.loadDraft(draft.context, draft.draftId), draft);
    expect(winner.input).toBe('Newer winner'); expect(fork.draft.input).toBe('Other tab retained');
  } finally { await other.close(); }
});

scenario('V02-LOGOUT-01', 'logout removes visible authority but retains drafts', async ({ app, run }) => {
  await login(app, run); await createEditor(app, 'Logout retained draft'); const before = await privateInventory(app);
  await app.getByRole('button', { name: 'Log out and clear visible content', exact: true }).click();
  await expect(app.getByRole('button', { name: 'Create card', exact: true })).toHaveCount(0);
  expect((await privateInventory(app)).drafts.length).toBe(before.drafts.length);
});

scenario('V02-CLEAR-01', 'explicit two-warning private discard has no server mutation', async ({ app, run }) => {
  await login(app, run); await createEditor(app, 'Discard only this synthetic profile'); const before = await stats(run);
  await app.getByRole('button', { name: 'Clear all private recovery', exact: true }).click();
  const confirm = app.getByRole('button', { name: 'Confirm private discard', exact: true }); await expect(confirm).toBeDisabled();
  await app.getByLabel('Discard selected private prose and evidence', { exact: false }).check(); await expect(confirm).toBeDisabled();
  await app.getByLabel('Unresolved outcomes may become unknowable', { exact: false }).check(); await confirm.click();
  await expect.poll(async () => { const i = await privateInventory(app); return i.drafts.length + i.submissions.length; }).toBe(0);
  expect((await stats(run)).mutationCount).toBe(before.mutationCount);
});

scenario('V02-KEYBOARD-01', 'Tab and Enter activate deliberate Save', async ({ app, run }) => {
  await login(app, run); await createEditor(app, 'Keyboard ' + Date.now()); const before = await stats(run);
  await app.keyboard.press('Tab'); await expect(app.getByLabel('priority', { exact: false })).toBeFocused();
  await app.keyboard.press('Tab'); const save = app.getByRole('button', { name: 'Save', exact: true }); await expect(save).toBeFocused(); await app.keyboard.press('Enter');
  await expect(app.getByRole('status').filter({ hasText: 'Confirmed saved.' }).first()).toBeVisible();
  expect((await stats(run)).mutationCount).toBe(before.mutationCount + 1);
  await app.getByRole('button', { name: 'Read current board', exact: true }).click();
  const edits = app.getByRole('button', { name: 'Edit card', exact: true }); expect(await edits.count()).toBeGreaterThan(0);
  const edit = edits.last(); await edit.focus();
  const key = await edit.getAttribute('data-focus-key');
  // Public refresh API on the actual mounted view drives real network/DOM work;
  // this is separate from the Tab/Enter input proof above.
  await app.evaluate(() => window.__v02.workspace.board.refresh());
  expect(await app.evaluate(() => document.activeElement?.dataset.focusKey)).toBe(key);
});

scenario('V02-IME-01', 'CDP composition prevents Enter submission', async ({ app, run, profile }) => {
  await login(app, run); await createEditor(app, 'IME input'); const before = await stats(run), cdp = await profile.context.newCDPSession(app);
  try {
    await app.getByLabel('description', { exact: false }).focus();
    await app.evaluate(() => { window.__v02Composition = []; for (const name of ['compositionstart', 'compositionupdate', 'compositionend']) document.addEventListener(name, () => window.__v02Composition.push(name), { capture: true }); });
    await cdp.send('Input.imeSetComposition', { text: '\u6cf3', selectionStart: 1, selectionEnd: 1 });
    await expect(app.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
    expect(await app.evaluate(() => window.__v02Composition.includes('compositionstart'))).toBe(true);
    await app.keyboard.press('Enter'); expect((await stats(run)).mutationCount).toBe(before.mutationCount);
    await cdp.send('Input.imeSetComposition', { text: '', selectionStart: 0, selectionEnd: 0 });
    await cdp.send('Input.insertText', { text: '\u6cf3' });
    expect(await app.evaluate(() => window.__v02Composition.includes('compositionend'))).toBe(true);
  } finally { await cdp.detach(); }
  expect((await stats(run)).mutationCount).toBe(before.mutationCount);
  const save = app.getByRole('button', { name: 'Save', exact: true }); await expect(save).toBeEnabled(); await save.focus(); await app.keyboard.press('Space');
  await expect(app.getByRole('status').filter({ hasText: 'Confirmed saved.' }).first()).toBeVisible();
  expect((await stats(run)).mutationCount).toBe(before.mutationCount + 1);
});

scenario('V02-LATE-01', 'late original response cannot replace newer input or focus', async ({ app, run }) => {
  await login(app, run); await createEditor(app, 'Delayed original'); await control(run, { op: 'delayNext' });
  await app.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => (await stats(run)).delayReached).toBe(true);
  const input = app.getByLabel('description', { exact: false }); await input.fill('Newer retained input'); await input.focus();
  await control(run, { op: 'release' }); await expect(app.getByRole('status').filter({ hasText: 'Confirmed saved.' }).first()).toBeVisible();
  expect(await input.inputValue()).toBe('Newer retained input'); await expect(input).toBeFocused();
});

scenario('V02-RELOAD-01', 'disconnect reload reauthentication looks up original without replay', async ({ app, run, profile }) => {
  await login(app, run); await createEditor(app, 'Committed response lost'); const before = await stats(run);
  await control(run, { op: 'dropNext' }); await app.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(app.getByRole('status').filter({ hasText: 'Original outcome unknown' }).first()).toBeVisible();
  const original = (await privateInventory(app)).submissions.find(s => s.outcome === 'unknown'); expect(original).toBeTruthy();
  await control(run, { op: 'restart' });
  await app.reload({ waitUntil: 'networkidle' }); await mount(app, run, profile.qualification); await login(app, run);
  expect((await stats(run)).mutationCount).toBe(before.mutationCount + 1);
  const result = await app.evaluate(row => window.__v02.access.client.lookup(row.context, row.opId), original);
  expect(result.state).toBe('committed'); expect(result.opId).toBe(original.opId); expect((await stats(run)).mutationCount).toBe(before.mutationCount + 1);
});

scenario('V02-UNKNOWN-01', 'absent original outcome never becomes noncommit', async ({ app, run }) => {
  await login(app, run); const value = await intent(app, run), before = await stats(run);
  const outcome = await app.evaluate(async intent => {
    const { prepare } = await import('/js/api/boundary.js');
    await window.__v02.access.client.recovery.freezeSubmission(await prepare(intent.command, intent.request, intent.context, intent.draftId));
    return window.__v02.access.client.lookup(intent.context, intent.request.opId);
  }, value);
  expect(outcome.state).toBe('unknown'); expect((await stats(run)).mutationCount).toBe(before.mutationCount);
});

scenario('V02-LOSS-01', 'known original-evidence loss is unavailable, not noncommit', async ({ app, run }) => {
  await login(app, run); const value = await intent(app, run);
  const result = await app.evaluate(async intent => {
    const { prepare } = await import('/js/api/boundary.js'); const store = window.__v02.access.client.recovery;
    await store.freezeSubmission(await prepare(intent.command, intent.request, intent.context, intent.draftId));
    await store.clear({ kind: 'submission', context: intent.context, id: intent.request.opId }, { discardPrivateProse: true, unresolvedMayBecomeUnknowable: true });
    return window.__v02.access.client.lookup(intent.context, intent.request.opId);
  }, value);
  expect(result.state).toBe('unknown'); expect(result.terminal).toBeUndefined();
});

scenario('V02-INCARNATION-01', 'different incarnation cannot adopt original draft or submission', async ({ app, run }) => {
  await login(app, run); const value = await intent(app, run), before = await stats(run);
  const result = await app.evaluate(async intent => {
    const { prepare } = await import('/js/api/boundary.js');
    const store = window.__v02.access.client.recovery, id = crypto.randomUUID(), current = intent.context;
    const original = { ...current, incarnation: crypto.randomUUID() };
    intent.context = original; intent.request.incarnation = original.incarnation;
    await store.saveDraft({ context: original, draftId: id, expectedRevision: 0, input: 'Original context only' });
    await store.freezeSubmission(await prepare(intent.command, intent.request, original, intent.draftId));
    const before = JSON.stringify(await store.loadSubmission(original, intent.request.opId));
    const outcome = await window.__v02.access.client.lookup(original, intent.request.opId);
    const absent = await store.loadSubmission(current, intent.request.opId);
    const copied = await store.copyDraft(original, id, current, crypto.randomUUID());
    return { absent: absent === undefined, source: (await store.loadDraft(original, id)).input,
      copied: copied.draft.context.incarnation === current.incarnation, outcome: outcome.state,
      unchanged: JSON.stringify(await store.loadSubmission(original, intent.request.opId)) === before };
  }, value);
  expect(result).toEqual({ absent: true, source: 'Original context only', copied: true, outcome: 'unknown', unchanged: true });
  const after = await stats(run); expect(after.mutationCount).toBe(before.mutationCount); expect(after.lookupCount).toBe(before.lookupCount);
});

scenario('V02-RETENTION-01', 'expired private originals stay unknown while committed source survives', async ({ app, run }) => {
  await login(app, run); const value = await intent(app, run), committed = await submit(app, value);
  expect(committed.state).toBe('committed'); const before = await stats(run);
  const result = await app.evaluate(async context => {
    const store = window.__v02.access.client.recovery, id = crypto.randomUUID();
    await store.saveDraft({ context, draftId: id, expectedRevision: 0, input: 'Expired synthetic input' });
    await new Promise((resolve, reject) => { const q = indexedDB.open('localswim-private-recovery-v1', 1); q.onerror = reject; q.onsuccess = () => {
      const db = q.result, tx = db.transaction('drafts', 'readwrite', { durability: 'strict' }), request = tx.objectStore('drafts').getAll();
      request.onsuccess = () => { const row = request.result.find(r => r.draftId === id); row.editedAt = Date.now() - 8 * 86400000; tx.objectStore('drafts').put(row); };
      tx.onabort = reject; tx.oncomplete = () => { db.close(); resolve(); };
    }; });
    return { hidden: await store.loadDraft(context, id) === undefined };
  }, value.context);
  expect(result.hidden).toBe(true);
  const expired = await app.evaluate(async intent => {
    const store = window.__v02.access.client.recovery;
    const original = JSON.stringify((await store.loadSubmission(intent.context, intent.request.opId)).evidence);
    await new Promise((resolve, reject) => { const q = indexedDB.open('localswim-private-recovery-v1', 1); q.onerror = reject; q.onsuccess = () => {
      const db = q.result, tx = db.transaction('statuses', 'readwrite', { durability: 'strict' }), request = tx.objectStore('statuses').getAll();
      request.onsuccess = () => { const row = request.result.find(r => JSON.parse(r.key).at(-1) === intent.request.opId); row.recordedAt = Date.now() - 8 * 86400000; tx.objectStore('statuses').put(row); };
      tx.onabort = reject; tx.oncomplete = () => { db.close(); resolve(); };
    }; });
    const absent = await store.loadSubmission(intent.context, intent.request.opId);
    const outcome = await window.__v02.access.client.lookup(intent.context, intent.request.opId);
    const board = await window.__v02.access.client.board(intent.context.boardId);
    return { absent: absent === undefined, state: outcome.state, terminal: Boolean(outcome.terminal),
      effectPresent: board.result.cards.some(c => c.title === intent.request.input.title), originalCaptured: original.length > 0 };
  }, value);
  expect(expired).toEqual({ absent: true, state: 'unknown', terminal: false, effectPresent: true, originalCaptured: true });
  const after = await stats(run); expect(after.mutationCount).toBe(before.mutationCount); expect(after.lookupCount).toBe(before.lookupCount);
});

scenario('V02-RESTART-01', 'actual same-store restart invalidates sessions and codes without replay', async ({ app, run }) => {
  await login(app, run); await createEditor(app, 'Restart retained input'); const saved = await privateInventory(app), before = await stats(run);
  let secret = await control(run, { op: 'code', subject: 'automation' });
  await control(run, { op: 'restart' });
  const refused = await app.evaluate(async code => {
    let oldSession = false; try { await window.__v02.access.client.session(); } catch { oldSession = true; }
    let oldCode = false; try { await window.__v02.access.exchange(code); } catch { oldCode = true; }
    return { oldSession, oldCode };
  }, secret.code);
  secret.code = ''; secret = undefined;
  expect(refused).toEqual({ oldSession: true, oldCode: true }); expect((await privateInventory(app)).drafts.length).toBe(saved.drafts.length);
  await login(app, run); expect((await stats(run)).mutationCount).toBe(before.mutationCount);
});

scenario('V02-CANCEL-01', 'stop waiting does not cancel a committed original', async ({ app, run }) => {
  await login(app, run); await createEditor(app, 'Stop waiting original'); const before = await stats(run);
  await control(run, { op: 'delayNext' }); await app.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => (await stats(run)).delayReached).toBe(true);
  await app.getByRole('button', { name: 'Stop waiting', exact: true }).click();
  await expect(app.getByRole('status').filter({ hasText: 'does not cancel a possible commit' })).toBeVisible();
  const row = (await privateInventory(app)).submissions.find(s => s.outcome === 'unknown'); expect(row).toBeTruthy();
  await control(run, { op: 'release' });
  const outcome = await app.evaluate(row => window.__v02.access.client.lookup(row.context, row.opId), row);
  expect(outcome.state).toBe('committed'); expect(outcome.opId).toBe(row.opId); expect((await stats(run)).mutationCount).toBe(before.mutationCount + 1);
});

scenario('V02-INACCESSIBLE-01', 'native closed IDB connection cannot settle original or replay', async ({ app, run }) => {
  await login(app, run); const value = await intent(app, run); expect((await submit(app, value)).state).toBe('committed'); const before = await stats(run);
  const outcome = await app.evaluate(async intent => { window.__v02.access.client.close(); return window.__v02.access.client.lookup(intent.context, intent.request.opId); }, value);
  expect(outcome.state).toBe('unknown'); expect(outcome.terminal).toBeUndefined();
  const after = await stats(run); expect(after.mutationCount).toBe(before.mutationCount); expect(after.lookupCount).toBe(before.lookupCount);
});

scenario('V02-PRIVACY-01', 'browser semantic storage contains no authentication fields', async ({ app, run }) => {
  await login(app, run); await createEditor(app, 'Private audit prose');
  const audit = await app.evaluate(async () => {
    const local = Object.keys(localStorage), session = Object.keys(sessionStorage), cookies = document.cookie;
    const values = await new Promise((resolve, reject) => { const q = indexedDB.open('localswim-private-recovery-v1', 1); q.onerror = reject; q.onsuccess = () => {
      const db = q.result, names = [...db.objectStoreNames], tx = db.transaction(names, 'readonly'), rows = []; let left = names.length;
      for (const name of names) { const r = tx.objectStore(name).getAll(); r.onerror = reject; r.onsuccess = () => { rows.push(...r.result); if (--left === 0) { db.close(); resolve(rows); } }; }
    }; });
    const forbidden = /^(authorization|bearer|token|credential|credentials|password|secret|sessioncode|code|cookie|privatekey)$/i;
    const scan = value => value && typeof value === 'object' && Object.entries(value).some(([k, v]) => forbidden.test(k) || scan(v));
    return { local, session, cookies, forbiddenField: Boolean(scan(values)) };
  });
  expect(audit).toEqual({ local: [], session: [], cookies: '', forbiddenField: false });
});

requireInventory(inventory);
