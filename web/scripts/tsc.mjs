import { spawnSync } from 'node:child_process';
import { localTool, webRoot } from './local-tool.mjs';

try {
  const mode = process.argv[2];
  if (process.argv.length !== 3 || !['check', 'build'].includes(mode)) {
    throw new Error('Expected check or build');
  }
  const args = ['-p', 'tsconfig.json', ...(mode === 'check' ? ['--noEmit'] : ['--noEmitOnError'])];
  const result = spawnSync(process.execPath, [localTool('typescript', 'tsc'), ...args], {
    cwd: webRoot, stdio: 'inherit', timeout: 120_000,
  });
  if (result.error || result.signal) throw new Error('Local TypeScript CLI did not complete');
  process.exitCode = result.status ?? 1;
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
