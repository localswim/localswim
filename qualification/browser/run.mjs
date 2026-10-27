import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { readRun } from './lib/run-input.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const require = createRequire(resolve(here, '../../web/package.json'));
const { defineConfig } = require('@playwright/test');
const run = readRun();

// P02 invokes this configuration through its exact locked Playwright CLI.
// Every required case is collected; command-line filtering is not a substitute.
export default defineConfig({
  testDir: resolve(here, 'suites'),
  testMatch: '**/*.spec.mjs',
  workers: 1,
  fullyParallel: false,
  retries: 0,
  maxFailures: 1,
  timeout: 30_000,
  expect: { timeout: 8_000 },
  forbidOnly: true,
  outputDir: resolve(run.artifactDirectory, 'playwright-output'),
  globalTeardown: resolve(here, 'lib/final-evidence.mjs'),
  preserveOutput: 'never',
  use: { trace: 'off', video: 'off', screenshot: 'off' },
});
