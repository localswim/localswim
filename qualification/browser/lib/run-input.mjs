import { createHash } from 'node:crypto';
import { existsSync, lstatSync, readFileSync, readdirSync, realpathSync, statSync } from 'node:fs';
import { dirname, isAbsolute, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
export const digest = bytes => createHash('sha256').update(bytes).digest('hex');
function canonical(path, directory) {
  if (typeof path !== 'string' || !isAbsolute(path) || resolve(path) !== path) throw Error('Canonical absolute qualification path required');
  const real = realpathSync.native(path), info = statSync(path);
  if (real.toLowerCase() !== path.toLowerCase() || lstatSync(path).isSymbolicLink() || (directory ? !info.isDirectory() : !info.isFile())) throw Error('Unsafe qualification path');
  return path;
}
function privateDirectory(path, fresh = false) {
  if (fresh) {
    if (typeof path !== 'string' || !isAbsolute(path) || resolve(path) !== path || existsSync(path)) throw Error('Fresh absent private runtime directory required');
    canonical(dirname(path), true);
  } else canonical(path, true);
  const rel = relative(root, path);
  if (!(rel === '..' || rel.startsWith('..' + sep) || isAbsolute(rel))) throw Error('Synthetic private directory must be outside public source');
}
export function readRun() {
  if (process.platform !== 'win32' || process.arch !== 'x64' || process.versions.node !== '26.12.1') throw Error('Pinned Windows/amd64 Node environment required');
  const path = canonical(process.env.V02_RUN_MANIFEST, false), bytes = readFileSync(path);
  if (bytes.length > 65_536 || bytes[0] === 0xef) throw Error('Invalid qualification manifest');
  const run = JSON.parse(bytes.toString('utf8'));
  const keys = ['schemaVersion', 'artifactDirectory', 'dataDirectory', 'fixtureBinary', 'fixtureSHA256', 'applicationBinary', 'applicationSHA256', 'chromeExecutable', 'chromeSHA256', 'browserVersion', 'browserArchiveSHA256', 'playwrightVersion', 'port', 'evidenceId'];
  if (Object.keys(run).sort().join() !== keys.sort().join()) throw Error('Exact local manifest v2 fields required');
  if (run.schemaVersion !== 2 || !Number.isInteger(run.port) || run.port < 1024 || run.port > 65535 || typeof run.evidenceId !== 'string' || !/^[a-zA-Z0-9_-]{1,100}$/.test(run.evidenceId)) throw Error('Invalid local run manifest');
  privateDirectory(run.artifactDirectory);
  // Startup must create a fresh DataDir. Later worker/finalizer reads validate
  // its canonical location rather than treating the live directory as fresh.
  if (existsSync(run.dataDirectory)) privateDirectory(run.dataDirectory);
  else privateDirectory(run.dataDirectory, true);
  if (run.artifactDirectory === run.dataDirectory) throw Error('Separate artifact and data directories required');
  for (const [key, hash] of [['fixtureBinary', 'fixtureSHA256'], ['applicationBinary', 'applicationSHA256'], ['chromeExecutable', 'chromeSHA256']]) {
    canonical(run[key], false);
    if (!/^[a-f0-9]{64}$/.test(run[hash]) || digest(readFileSync(run[key])) !== run[hash]) throw Error('Pinned executable mismatch');
  }
  const pin = JSON.parse(readFileSync(resolve(root, 'qualification/environment.json'), 'utf8')).browser.candidate;
  if (run.browserVersion !== pin.version || run.browserArchiveSHA256 !== pin.archive_sha256 || run.playwrightVersion !== pin.runner_version) throw Error('Accepted browser pin mismatch');
  const browserRoot = resolve(root, 'qualification/browser'), files = ['run.mjs', 'inventory.json'];
  for (const dir of ['lib', 'suites']) for (const name of readdirSync(resolve(browserRoot, dir)).sort()) if (name.endsWith('.mjs')) files.push(dir + '/' + name);
  const sourceHashes = Object.fromEntries(files.sort().map(name => [name, digest(readFileSync(resolve(browserRoot, name)))]));
  return Object.freeze({ ...run, origin: `http://127.0.0.1:${run.port}`, profileDirectory: resolve(run.artifactDirectory, 'profile'), manifestSHA256: digest(bytes), sourceHashes, sourceSHA256: digest(JSON.stringify(sourceHashes)) });
}
