import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';
import { prepareBrowserReport } from './browser-report.mjs';

function fixture() {
  const root = mkdtempSync(resolve(tmpdir(), 'localswim-report-'));
  const source = resolve(root, 'source'), evidence = resolve(root, 'private');
  mkdirSync(source); mkdirSync(evidence);
  const manifest = resolve(root, 'manifest.json');
  writeFileSync(manifest, JSON.stringify({ schemaVersion: 2, artifactDirectory: evidence }));
  return { root, source, evidence, manifest };
}

test('retains raw failed-run output before a failure verdict and refuses attempt reuse', () => {
  const f = fixture(), capture = prepareBrowserReport(f.manifest, f.source);
  capture.save('{incomplete reporter output');
  capture.finish({ status: 'FAILED', launcherExit: 1, childExit: 1, childSignal: null });
  assert.equal(readFileSync(resolve(f.evidence, 'final-playwright.json'), 'utf8'), '{incomplete reporter output');
  assert.equal(JSON.parse(readFileSync(resolve(f.evidence, 'final-launch.json'), 'utf8')).launcherExit, 1);
  assert.throws(() => prepareBrowserReport(f.manifest, f.source), /reused/);
});

test('refuses report destinations inside public source before execution', () => {
  const f = fixture();
  writeFileSync(f.manifest, JSON.stringify({ schemaVersion: 2, artifactDirectory: f.source }));
  assert.throws(() => prepareBrowserReport(f.manifest, f.source), /destination/);
});

test('retains the manifest binding with a successful launcher verdict', () => {
  const f = fixture(), capture = prepareBrowserReport(f.manifest, f.source);
  capture.save('{"suites":[],"errors":[]}');
  capture.finish({ status: 'PASSED_LAUNCHER_CHECKS', launcherExit: 0, tests: 20 });
  const start = JSON.parse(readFileSync(resolve(f.evidence, 'launch-start.json'), 'utf8'));
  const final = JSON.parse(readFileSync(resolve(f.evidence, 'final-launch.json'), 'utf8'));
  assert.match(start.manifestSHA256, /^[0-9a-f]{64}$/);
  assert.equal(final.manifestSHA256, start.manifestSHA256);
  assert.equal(final.status, 'PASSED_LAUNCHER_CHECKS');
});
