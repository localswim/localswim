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
  // One fixture owns one fresh runtime/profile. Stop on the first failure;
  // a replacement worker must never adopt or overwrite that failed attempt.
  // Final coverage still requires every frozen case to pass with zero skips.
  maxFailures: 1,
  timeout: 90_000,
  expect: { timeout: 8_000 },
  forbidOnly: true,
  outputDir: resolve(run.artifactDirectory, 'playwright-output'),
  globalTeardown: resolve(here, 'lib/final-evidence.mjs'),
  preserveOutput: 'failures-only',
  use: { trace: 'off', video: 'off', screenshot: 'off' },
});
