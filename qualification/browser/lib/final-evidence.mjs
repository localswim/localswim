import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { readRun, digest } from './run-input.mjs';
export default async function finalize() {
  const run = readRun(), bytes = readFileSync(new URL('../inventory.json', import.meta.url)), inventory = JSON.parse(bytes), inventorySHA256 = digest(bytes), records = [];
  if (!inventory.required.length || new Set(inventory.required).size !== inventory.required.length) throw Error('Empty or duplicate mandatory inventory');
  for (const id of inventory.required) {
    const record = JSON.parse(readFileSync(resolve(run.artifactDirectory, id + '.json'), 'utf8'));
    if (record.id !== id || record.status !== 'passed' || record.expectedStatus !== 'passed' || record.retry !== 0 || record.errors !== 0 ||
        record.manifestSHA256 !== run.manifestSHA256 || record.inventorySHA256 !== inventorySHA256 || record.sourceSHA256 !== run.sourceSHA256 || record.evidenceId !== run.evidenceId ||
        record.applicationSHA256 !== run.applicationSHA256 || record.fixtureSHA256 !== run.fixtureSHA256) throw Error('Missing or failed mandatory local browser evidence');
    records.push(record);
  }
  const before = JSON.parse(readFileSync(resolve(run.artifactDirectory, 'live-start.json'), 'utf8'));
  const fixture = JSON.parse(readFileSync(resolve(run.artifactDirectory, 'fixture-teardown.json'), 'utf8'));
  const browser = JSON.parse(readFileSync(resolve(run.artifactDirectory, 'browser-teardown.json'), 'utf8'));
  if (before.origin !== run.origin || before.manifestSHA256 !== run.manifestSHA256 || before.inventorySHA256 !== inventorySHA256 ||
      fixture.exitCode !== 0 || fixture.manifestSHA256 !== run.manifestSHA256 || !browser.closed || browser.manifestSHA256 !== run.manifestSHA256) throw Error('Normal local startup or owned teardown evidence incomplete');
  writeFileSync(resolve(run.artifactDirectory, 'mandatory-coverage.json'), JSON.stringify({ schemaVersion: 2,
    status: 'COMPLETE_CASE_RECORDS_NOT_FINAL_RUN_VERDICT', evidenceId: run.evidenceId, manifestSHA256: run.manifestSHA256,
    inventorySHA256, sourceSHA256: run.sourceSHA256, installationId: before.installationId, required: inventory.required, cases: records, skipped: 0, fullReleaseQualified: false,
    finalVerdictRequires: 'P02 exit0 and final Playwright JSON with all required cases passed and no suite or teardown errors; fresh independent review',
  }, null, 2) + '\n', { flag: 'wx' });
}
