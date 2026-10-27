import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { readRun } from './run-input.mjs';
import { requireProfileAudit } from './profile-probe.mjs';

export default async function finalize() {
  const run = readRun();
  const inventory = JSON.parse(readFileSync(new URL('../inventory.json', import.meta.url), 'utf8'));
  const records = [];
  for (const id of inventory.required) {
    const record = JSON.parse(readFileSync(resolve(run.artifactDirectory, id + '.json'), 'utf8'));
    if (record.id !== id || record.status !== 'passed' || record.expectedStatus !== 'passed' ||
        record.retry !== 0 || record.errors !== 0 || record.manifestSHA256 !== run.manifestSHA256 || record.evidenceId !== run.evidenceId) {
      throw Error('Missing, failed, retried or mismatched mandatory browser evidence');
    }
    records.push(record);
  }
  const before = JSON.parse(readFileSync(resolve(run.artifactDirectory, 'profile-preflight.json'), 'utf8'));
  const after = JSON.parse(readFileSync(resolve(run.artifactDirectory, 'profile-acl-after.json'), 'utf8'));
  requireProfileAudit(after, run.profileId);
  if (before.profileId !== run.profileId || before.installationId !== run.installationId || before.manifestSHA256 !== run.manifestSHA256 ||
      before.nativeACLVerified !== true || before.profileACLPolicy !== 'pinned-browser-account-profile-private-v1') {
    throw Error('Profile qualification or native privacy evidence incomplete');
  }
  writeFileSync(resolve(run.artifactDirectory, 'mandatory-coverage.json'), JSON.stringify({
    schemaVersion: 1, status: 'COMPLETE_CASE_RECORDS_NOT_FINAL_RUN_VERDICT', evidenceId: run.evidenceId,
    manifestSHA256: run.manifestSHA256, installationId: run.installationId, profileId: run.profileId,
    required: inventory.required, cases: records, skipped: 0, fullReleaseQualified: false,
    finalVerdictRequires: 'P02 launcher exit0 and final Playwright JSON without suite/teardown errors, complete native privacy evidence, and fresh independent review',
  }, null, 2) + '\n', { flag: 'wx' });
}
