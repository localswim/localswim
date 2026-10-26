import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';
import test from 'node:test';
import { webRoot } from './local-tool.mjs';

function fixture(t) {
  const root = mkdtempSync(resolve(tmpdir(), 'localswim-package-scripts-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const web = resolve(root, 'web');
  for (const path of ['scripts', 'package.json', 'package-lock.json', 'tsconfig.json', 'src', 'generated', 'node_modules']) {
    cpSync(resolve(webRoot, path), resolve(web, path), { recursive: true });
  }
  return { root, web };
}

function run(web, name) {
  const manifest = JSON.parse(readFileSync(resolve(web, 'package.json'), 'utf8'));
  const command = manifest.scripts[name];
  assert.match(command, /^node scripts\/[a-z-]+\.mjs(?: (?:check|build))?$/);
  const [, target, ...args] = command.split(' ');
  return spawnSync(process.execPath, [target, ...args], {
    cwd: web, encoding: 'utf8', timeout: 150_000,
    env: { ...process.env, PATH: '' },
  });
}

test('actual package check uses local compiler and does not emit; build emits declared tree', t => {
  const { web } = fixture(t);
  const check = run(web, 'check');
  assert.equal(check.status, 0, check.stdout + check.stderr);
  assert.equal(existsSync(resolve(web, 'dist')), false);
  const build = run(web, 'build');
  assert.equal(build.status, 0, build.stdout + build.stderr);
  assert.equal(existsSync(resolve(web, 'dist/js/app.js')), true);
  assert.equal(existsSync(resolve(web, 'dist/js/app.js.map')), false);
});

test('actual check and build propagate diagnostics and noEmitOnError prevents output', t => {
  const { web } = fixture(t);
  writeFileSync(resolve(web, 'src/app.ts'), 'const invalid: string = 42;\n');
  for (const name of ['check', 'build']) {
    const result = run(web, name);
    assert.notEqual(result.status, 0);
    assert.match(result.stdout + result.stderr, /Type 'number' is not assignable to type 'string'/);
    assert.equal(existsSync(resolve(web, 'dist')), false);
  }
});

test('compiler requires the exact installed locked local package and a valid project', t => {
  const { web } = fixture(t);
  const packagePath = resolve(web, 'node_modules/typescript/package.json');
  const saved = readFileSync(packagePath, 'utf8');
  const installed = JSON.parse(saved);
  installed.version = '0.0.0';
  writeFileSync(packagePath, JSON.stringify(installed));
  assert.notEqual(run(web, 'check').status, 0);
  writeFileSync(packagePath, saved);
  rmSync(resolve(web, 'tsconfig.json'));
  assert.notEqual(run(web, 'check').status, 0);
  rmSync(resolve(web, 'node_modules/typescript'), { recursive: true });
  assert.notEqual(run(web, 'build').status, 0);
});

test('package targets pass explicit project/mode arguments to the local CLI and propagate its exit', t => {
  const { web } = fixture(t);
  const cli = resolve(web, 'node_modules/typescript/bin/tsc');
  writeFileSync(cli, 'console.log(JSON.stringify(process.argv.slice(2))); process.exitCode = 23;\n');
  for (const [name, flag] of [['check', '--noEmit'], ['build', '--noEmitOnError']]) {
    const result = run(web, name);
    assert.equal(result.status, 23);
    assert.deepEqual(JSON.parse(result.stdout), ['-p', 'tsconfig.json', flag]);
  }
  const lockPath = resolve(web, 'package-lock.json');
  const lock = JSON.parse(readFileSync(lockPath, 'utf8'));
  lock.packages['node_modules/typescript'].version = '0.0.0';
  writeFileSync(lockPath, JSON.stringify(lock));
  assert.notEqual(run(web, 'check').status, 0);
});

function browserFixture(root, body) {
  const browser = resolve(root, 'qualification/browser');
  mkdirSync(resolve(browser, 'suites'), { recursive: true });
  writeFileSync(resolve(browser, 'run.mjs'), "export default { testDir: './suites', workers: 1, retries: 0, timeout: 5000 };\n");
  writeFileSync(resolve(browser, 'suites/wiring.spec.mjs'),
    "import { test, expect } from '../../../web/node_modules/@playwright/test/index.mjs';\n" + body + '\n');
}

// Synthetic runner cases prove wiring only. They do not exercise a browser or
// provide any of V02's real browser/session/recovery qualification evidence.
test('actual pinned Playwright JSON accepts completed wiring fixture and refuses skipped, empty and failed cases', t => {
  const { root, web } = fixture(t);
  for (const [body, succeeds] of [
    ["test('wiring', () => expect(1).toBe(1));", true],
    ["test.skip('wiring', () => {});", false],
    ['', false],
    ["test('wiring', () => expect(1).toBe(2));", false],
    ["test.fail('wiring', () => expect(1).toBe(2));", false],
  ]) {
    browserFixture(root, body);
    const result = run(web, 'qualification:browser');
    assert.equal(result.status === 0, succeeds, result.stdout + result.stderr);
  }
});

test('browser launcher refuses missing V02 configuration and missing pinned runner', t => {
  const { root, web } = fixture(t);
  assert.notEqual(run(web, 'qualification:browser').status, 0);
  browserFixture(root, "test('wiring', () => {});");
  rmSync(resolve(web, 'node_modules/@playwright/test'), { recursive: true });
  assert.notEqual(run(web, 'qualification:browser').status, 0);
});

test('browser launcher refuses malformed, zero, skipped and incomplete successful runner reports', t => {
  const { root, web } = fixture(t);
  browserFixture(root, "test('wiring', () => {});");
  const testCase = { expectedStatus: 'passed', status: 'expected', results: [{ status: 'passed' }] };
  const valid = () => ({ errors: [], suites: [{ specs: [{ tests: [structuredClone(testCase)] }] }],
    stats: { expected: 1, unexpected: 0, flaky: 0, skipped: 0 } });
  const missing = valid(); delete missing.suites;
  const empty = valid(); empty.suites = [];
  const skipped = valid(); skipped.suites[0].specs[0].tests[0].results[0].status = 'skipped';
  const incomplete = valid(); incomplete.suites[0].specs[0].tests[0].results = [];
  const mismatch = valid(); mismatch.stats.expected = 2;
  const errors = valid(); errors.errors = [{ message: 'synthetic error' }];
  const cli = resolve(web, 'node_modules/@playwright/test/cli.js');
  for (const report of ['not json', missing, empty, skipped, incomplete, mismatch, errors]) {
    const output = typeof report === 'string' ? report : JSON.stringify(report);
    writeFileSync(cli, `process.stdout.write(${JSON.stringify(output)});\n`);
    assert.notEqual(run(web, 'qualification:browser').status, 0);
  }
});
