import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, realpathSync, statSync } from 'node:fs';
import { dirname, isAbsolute, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const digest = value => createHash('sha256').update(value).digest('hex');
function canonical(path, directory) {
  if (typeof path !== 'string' || !isAbsolute(path) || resolve(path) !== path) throw Error('Canonical absolute qualification path required');
  const real = realpathSync.native(path);
  const info = statSync(path);
  if (real.toLowerCase() !== path.toLowerCase() || lstatSync(path).isSymbolicLink() || (directory ? !info.isDirectory() : !info.isFile())) throw Error('Unsafe qualification path');
  return path;
}

export function readRun() {
  if (process.platform !== 'win32' || process.arch !== 'x64' || process.versions.node !== '24.20.0') throw Error('Pinned Windows/amd64 Node environment required');
  const path = canonical(process.env.V02_RUN_MANIFEST, false);
  const bytes = readFileSync(path);
  if (bytes.length > 65_536 || bytes[0] === 0xef) throw Error('Invalid qualification manifest');
  const run = JSON.parse(bytes.toString('utf8'));
  if (run.schemaVersion !== 1 || !uuid.test(run.installationId) || !uuid.test(run.boardId) || !uuid.test(run.incarnation) || !uuid.test(run.profileId)) throw Error('Invalid qualification identity');
  const url = new URL(run.origin);
  if (url.protocol !== 'https:' || !/^[0-9a-f]{32}\.localhost$/.test(url.hostname) || url.port !== '47443' || url.origin !== run.origin || url.pathname !== '/' || url.search || url.hash || url.username || url.password) throw Error('Exact pinned installation origin required');
  for (const key of ['profileDirectory', 'artifactDirectory']) canonical(run[key], true);
  canonical(run.chromeExecutable, false);
  canonical(run.fixtureBinary, false);
  if (!run.pipeName?.startsWith('\\\\.\\pipe\\localswim-v02-') || /[\r\n]/.test(run.pipeName)) throw Error('Private fixture pipe required');
  if (!run.fixtureBinarySHA256 || digest(readFileSync(run.fixtureBinary)) !== run.fixtureBinarySHA256) throw Error('Fixture binary mismatch');
  if (!run.chromeSHA256 || digest(readFileSync(run.chromeExecutable)) !== run.chromeSHA256) throw Error('Browser binary mismatch');
  const pin = JSON.parse(readFileSync(resolve(root, 'qualification/environment.json'), 'utf8'));
  if (run.browserVersion !== pin.browser.candidate.version || run.browserArchiveSHA256 !== pin.browser.candidate.archive_sha256 || run.playwrightVersion !== pin.browser.candidate.runner_version) throw Error('Accepted browser pin mismatch');
  canonical(run.profileACLRecord, false);
  if (!run.profileACLRecordSHA256 || digest(readFileSync(run.profileACLRecord)) !== run.profileACLRecordSHA256 || !run.evidenceId) throw Error('Prepared profile ACL/evidence binding required');
  const insideSource = relative(root, run.profileDirectory);
  if (!(insideSource === '..' || insideSource.startsWith('..' + sep) || isAbsolute(insideSource))) throw Error('Private profile must be outside public source');
  return Object.freeze({ ...run, manifestSHA256: digest(bytes) });
}
