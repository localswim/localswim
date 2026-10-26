import { spawnSync } from 'node:child_process';
import { statSync } from 'node:fs';
import { resolve } from 'node:path';
import { localTool, webRoot } from './local-tool.mjs';

// V02 owns this Playwright configuration and its mandatory suite inventory.
// This launcher proves dispatch and completion only, not journey completeness.
try {
  if (process.argv.length !== 2) throw new Error('Browser qualification accepts no launcher arguments');
  const config = resolve(webRoot, '../qualification/browser/run.mjs');
  if (!statSync(config).isFile()) throw new Error('Missing V02 browser configuration');
  const result = spawnSync(process.execPath, [
    localTool('@playwright/test', 'playwright'), 'test', '--config', config, '--reporter=json',
  ], { cwd: webRoot, encoding: 'utf8', timeout: 300_000, maxBuffer: 8 * 1024 * 1024 });
  if (result.error || result.signal || result.status !== 0) {
    throw new Error('Pinned browser runner failed or did not complete');
  }
  const report = JSON.parse(result.stdout);
  if (!Array.isArray(report.suites) || !Array.isArray(report.errors) || report.errors.length) {
    throw new Error('Invalid browser runner report');
  }
  const tests = [];
  function visit(suites) {
    for (const suite of suites) {
      if (!Array.isArray(suite.specs)) throw new Error('Invalid browser suite report');
      for (const spec of suite.specs) {
        if (!Array.isArray(spec.tests) || !spec.tests.length) throw new Error('Empty browser test report');
        tests.push(...spec.tests);
      }
      if (suite.suites !== undefined) {
        if (!Array.isArray(suite.suites)) throw new Error('Invalid nested browser suite report');
        visit(suite.suites);
      }
    }
  }
  visit(report.suites);
  if (!tests.length || tests.some(test => test.expectedStatus !== 'passed' || test.status !== 'expected' ||
      !Array.isArray(test.results) || !test.results.length || test.results.some(result => result.status !== 'passed')) ||
      report.stats?.expected !== tests.length || report.stats?.unexpected !== 0 ||
      report.stats?.flaky !== 0 || report.stats?.skipped !== 0) {
    throw new Error('Browser tests are missing, skipped, failed or incomplete');
  }
  console.log(`Browser runner completed ${tests.length} tests; V02 owns mandatory coverage evidence.`);
} catch (error) {
  console.error(`Browser qualification refused: ${error.message}`);
  process.exitCode = 1;
}
