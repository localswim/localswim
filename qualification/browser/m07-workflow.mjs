// Reusable M07 real-browser journeys. The caller supplies an isolated native
// workspace, actual CLI reads and the existing sanitized localpost transport.
import { randomUUID } from 'node:crypto';

export const m07CaseIds = ['V02-MWORKFLOW-01', 'V02-MWORKFLOW-02', 'V02-MINPUT-01',
  'V02-MCONTEXT-01', 'V02-MSTORAGE-01', 'V02-MDELAY-01', 'V02-MSTALE-01', 'V02-MUNKNOWN-01'];

export async function runWorkflowJourneys({ browser, expect, origin, readBoard, diagnostics }) {
  const report = { cases: [], assertions: 0, fullQualification: false };
  const button = (page, name) => page.getByRole('button', { name, exact: true });
  const field = (page, name) => page.getByLabel(name, { exact: true });
  const assert = (value, label) => { if (!value) throw Error(label); report.assertions++; };
  const equal = (a, b, label) => assert(JSON.stringify(a) === JSON.stringify(b), label);
  const open = async (page, key) => {
    await page.goto(origin + '/board/harbor/' + key, { waitUntil: 'networkidle' });
    await expect(button(page, 'Edit workflow')).toBeVisible();
  };
  const edit = async page => { await button(page, 'Edit workflow').click(); await expect(field(page, 'Lane 1 label')).toBeVisible(); };
  const preview = async (page, saveAllowed = true) => {
    const response = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/workflow/preview'));
    await button(page, 'Preview workflow').click();
    const value = await (await response).json();
    await expect(button(page, 'Save workflow'))[saveAllowed ? 'toBeEnabled' : 'toBeDisabled']();
    return value;
  };
  const save = async page => {
    const response = page.waitForResponse(r => r.request().method() === 'PATCH' && new URL(r.url()).pathname.endsWith('/workflow'));
    await button(page, 'Save workflow').click();
    const terminal = await (await response).json();
    assert(terminal.outcome === 'committed', 'Workflow original must commit');
    await expect(page.getByRole('status').filter({ hasText: 'Confirmed saved.' }).first()).toBeVisible();
    return terminal;
  };
  const refresh = async page => {
    const response = page.waitForResponse(r => r.request().method() === 'GET' && /\/api\/v1\/boards\/[a-f0-9-]+$/.test(new URL(r.url()).pathname));
    await button(page, 'Read current board').click();
    const value = await (await response).json();
    await expect(page.getByRole('status').filter({ hasText: 'Current at board revision ' + value.result.revision + ';' })).toBeVisible();
  };
  const runCase = async (id, body) => {
    void diagnostics.emit({ caseId: id, kind: 'case', phase: 'start', code: 'OK' });
    try {
      await body(); report.cases.push({ id, status: 'passed' });
      void diagnostics.emit({ caseId: id, kind: 'case', phase: 'complete', code: 'OK' });
    } catch (error) {
      report.cases.push({ id, status: 'failed' });
      void diagnostics.emit({ caseId: id, kind: 'case', phase: 'error', code: 'CASE_FAILED' });
      throw error;
    }
  };
  const context = await browser.newContext();
  const page = await context.newPage();
  let currentCase = m07CaseIds[0];
  // Reattach for each case so error metadata retains a fixed allowlisted case ID.
  let detach = () => {};
  const useCase = async (id, body) => { currentCase = id; detach(); detach = diagnostics.attach(page, id); await runCase(id, body); };
  try {
    for (const [index, key] of ['default', 'configured'].entries()) {
      await useCase(m07CaseIds[index], async () => {
        await open(page, key);
        const before = readBoard(key);
        assert(before.approvals.length > 0 && before.cards.some(card => card.archived === false), 'Rich fixture must include approval and current cards');
        await edit(page);
        assert(await field(page, 'Lane 1 role').count() === 0, 'Existing roles must be fixed');
        const renamed = 'M07 ' + key + ' queue';
        await field(page, 'Lane 1 label').fill(renamed);
        await button(page, 'Move down lane 1').click();
        await button(page, 'Move up lane 2').click();
        await field(page, 'human: export').uncheck();
        const proposed = await preview(page);
        equal(readBoard(key), before, 'Read-only preview changed board');
        assert(proposed.candidate.lanes[0].id === (before.workflow?.lanes[0].id ?? 'Ready for Work'), 'Existing lane identity changed');
        await expect(page.getByLabel('Complete workflow preview')).toContainText(renamed);
        const terminal = await save(page), after = readBoard(key);
        equal(after.cards, before.cards, 'Policy edit changed cards');
        equal(after.approvals, before.approvals, 'Policy edit changed approvals');
        assert(after.revision === terminal.revision && after.policyVersion === (before.policyVersion ?? 1) + 1, 'CLI policy/revision parity');
        equal(after.workflow, proposed.candidate, 'Actual CLI and browser policy differ');
        assert(!after.workflow.actorRights.human.includes('export'), 'Right ceiling edit lost');
        await refresh(page); await edit(page);
        assert(await field(page, 'Lane 1 label').inputValue() === renamed, 'Current editor lost renamed lane');
        await button(page, 'Cancel').click();
        if (key === 'configured') {
          await edit(page);
          const count = after.workflow.lanes.length, edgeCount = after.workflow.transitions.length;
          const waitingLabel = 'M07 empty waiting';
          await button(page, 'Add waiting lane').click();
          await field(page, `Lane ${count + 1} label`).fill(waitingLabel);
          for (const [offset, from, to] of [[1, after.workflow.lanes.find(l => l.role === 'initial').id, null], [2, null, after.workflow.lanes.find(l => l.role === 'in_progress').id]]) {
            await button(page, 'Add transition').click();
            const fromField = field(page, `Transition ${edgeCount + offset} from`), toField = field(page, `Transition ${edgeCount + offset} to`);
            const waiting = await fromField.locator('option').evaluateAll(options => options.find(option => option.textContent.includes('M07 empty waiting')).value);
            await fromField.selectOption(from ?? waiting); await toField.selectOption(to ?? waiting);
          }
          const added = await preview(page); await save(page);
          assert(added.candidate.lanes.at(-1).role === 'waiting', 'Added waiting role changed');
          await refresh(page); await edit(page);
          await button(page, `Remove lane ${count + 1}`).click();
          await preview(page); await save(page);
          equal(readBoard(key).cards, before.cards, 'Empty waiting retirement moved cards');
        }
      });
    }
    await useCase('V02-MINPUT-01', async () => {
      await open(page, 'configured'); await edit(page);
      const before = readBoard('configured'), input = field(page, 'Lane 1 label');
      await input.fill('Private M07 ' + randomUUID());
      await input.press('Enter'); equal(readBoard('configured'), before, 'Input Enter submitted');
      const cdp = await context.newCDPSession(page);
      try {
        await input.focus();
        await cdp.send('Input.imeSetComposition', { text: '\u6cf3', selectionStart: 1, selectionEnd: 1 });
        await page.keyboard.press('Escape');
        await expect(input).toBeVisible();
        await page.keyboard.press('Enter'); equal(readBoard('configured'), before, 'IME Enter submitted');
        await cdp.send('Input.imeSetComposition', { text: '', selectionStart: 0, selectionEnd: 0 });
      } finally { await cdp.detach(); }
      await input.press('Escape');
      await expect(button(page, 'Keep editing')).toBeVisible();
      await button(page, 'Keep editing').click(); await expect(input).toBeFocused();
      await button(page, 'Cancel').click();
      await button(page, 'Confirm discard editor input').click();
      await expect(button(page, 'Edit workflow')).toBeFocused();
      equal(readBoard('configured'), before, 'Discard changed policy');
      await edit(page);
      const restoredLabel = 'Restored unsent ' + randomUUID();
      await field(page, 'Lane 1 label').fill(restoredLabel);
      await expect(page.getByRole('status').filter({ hasText: 'Recovery saved privately.' }).first()).toBeVisible();
      await page.reload({ waitUntil: 'networkidle' });
      await expect(field(page, 'Lane 1 label')).toHaveValue(restoredLabel);
      await button(page, 'Cancel').click();
      await expect(button(page, 'Keep editing')).toBeVisible();
      await button(page, 'Keep editing').click();
      await expect(field(page, 'Lane 1 label')).toHaveValue(restoredLabel);
      await button(page, 'Cancel').click();
      await button(page, 'Confirm discard editor input').click();
      equal(readBoard('configured'), before, 'Restored unsent draft was submitted or discarded authoritatively');
    });
    await useCase('V02-MCONTEXT-01', async () => {
      await open(page, 'configured'); await edit(page);
      const before = readBoard('configured');
      const privateLabel = 'Context-private ' + randomUUID();
      await field(page, 'Lane 1 label').fill(privateLabel);
      const link = page.locator('a[href="/board/harbor/default"]').first();
      await link.click();
      await expect(page).toHaveURL(origin + '/board/harbor/default');
      await expect(button(page, 'Edit workflow')).toBeVisible();
      equal(readBoard('configured'), before, 'Context switch mutated board');
      await edit(page);
      assert(await field(page, 'Lane 1 label').inputValue() === readBoard('default').workflow.lanes[0].label, 'Equal-ticket boards reused another policy');
      await button(page, 'Cancel').click();
      await page.locator('a[href="/board/harbor/configured"]').first().click();
      await expect(page).toHaveURL(origin + '/board/harbor/configured');
      await expect(field(page, 'Lane 1 label')).toHaveValue(privateLabel);
      await button(page, 'Cancel').click(); await button(page, 'Confirm discard editor input').click();
    });
    await useCase('V02-MSTORAGE-01', async () => {
      const denied = await browser.newContext();
      const deniedPage = await denied.newPage();
      const detachDenied = diagnostics.attach(deniedPage, currentCase);
      try {
        await deniedPage.addInitScript(() => Object.defineProperty(window, 'indexedDB', { configurable: true, value: { open() { throw new DOMException('Qualification denial', 'SecurityError'); } } }));
        const before = readBoard('configured');
        await open(deniedPage, 'configured'); await edit(deniedPage);
        await field(deniedPage, 'Lane 1 label').fill('Denied storage input');
        await preview(deniedPage, false);
        await expect(field(deniedPage, 'Lane 1 label')).toHaveValue('Denied storage input');
        equal(readBoard('configured'), before, 'Storage denial admitted mutation');
      } finally { detachDenied(); await denied.close(); }
    });
    await useCase('V02-MDELAY-01', async () => {
      await open(page, 'configured'); await edit(page);
      await field(page, 'Lane 1 label').fill('Old delayed proposal');
      const before = readBoard('configured');
      let release, reached;
      const held = new Promise(resolve => { release = resolve; });
      const captured = new Promise(resolve => { reached = resolve; });
      const pattern = '**/api/v1/boards/*/workflow/preview';
      await page.route(pattern, async route => {
        const response = await route.fetch(); reached(); await held;
        await route.fulfill({ response });
      });
      try {
        await button(page, 'Preview workflow').click(); await captured;
        await button(page, 'Cancel').click(); await button(page, 'Confirm discard editor input').click();
        await edit(page); await field(page, 'Lane 1 label').fill('Fresh unpreviewed proposal');
        const completed = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/workflow/preview'));
        release(); await completed;
        await expect(button(page, 'Save workflow')).toBeDisabled();
        await expect(field(page, 'Lane 1 label')).toHaveValue('Fresh unpreviewed proposal');
        equal(readBoard('configured'), before, 'Delayed read changed authoritative board');
        await button(page, 'Cancel').click(); await button(page, 'Confirm discard editor input').click();
      } finally { release(); await page.unroute(pattern); }
    });
    await useCase('V02-MSTALE-01', async () => {
      await open(page, 'configured'); await edit(page);
      await field(page, 'Lane 1 label').fill('Stale local proposal'); await preview(page);
      const other = await context.newPage();
      try {
        await open(other, 'configured'); await edit(other);
        await field(other, 'Lane 1 label').fill('Other window winner'); await preview(other); await save(other);
        const committed = readBoard('configured');
        const response = page.waitForResponse(r => r.request().method() === 'PATCH' && new URL(r.url()).pathname.endsWith('/workflow'));
        await button(page, 'Save workflow').click();
        const refused = await (await response).json();
        assert(refused.outcome === 'not_committed', 'Stale two-window edit was accepted');
        equal(readBoard('configured'), committed, 'Stale refusal changed winner');
        await expect(field(page, 'Lane 1 label')).toHaveValue('Stale local proposal');
      } finally { await other.close(); }
    });
    await useCase('V02-MUNKNOWN-01', async () => {
      await open(page, 'default'); await edit(page);
      await field(page, 'Lane 1 label').fill('Unknown response policy'); await preview(page);
      const before = readBoard('default'); let writes = 0, original;
      const pattern = '**/api/v1/boards/*/workflow';
      await page.route(pattern, async route => {
        if (route.request().method() !== 'PATCH') return route.continue();
        writes++; original = route.request().postDataJSON();
        await route.fetch(); await route.abort('failed');
      });
      try {
        await button(page, 'Save workflow').click();
        await expect(page.getByRole('status').filter({ hasText: /unknown/i }).first()).toBeVisible();
        assert(writes === 1 && readBoard('default').revision === before.revision + 1, 'Lost response changed dispatch count');
        await expect(button(page, 'Save workflow')).toBeDisabled();
        await button(page, 'Look up original outcome').click();
        await expect(page.getByRole('status').filter({ hasText: 'Confirmed saved.' }).first()).toBeVisible();
        assert(writes === 1 && original.opId, 'Original lookup replayed mutation');
        await page.reload({ waitUntil: 'networkidle' });
        assert(writes === 1, 'Reload replayed workflow mutation');
        equal(readBoard('default').workflow.lanes[0].label, 'Unknown response policy', 'Lost-response policy missing');
      } finally { await page.unroute(pattern); }
    });
    return report;
  } finally { detach(); await context.close(); }
}
