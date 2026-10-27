import { createHash } from 'node:crypto';
import { existsSync, lstatSync, readFileSync, realpathSync, statSync, writeFileSync } from 'node:fs';
import { isAbsolute, relative, resolve, sep } from 'node:path';

// Test reports may contain synthetic private inputs. Capture only into an
// explicitly selected, canonical directory outside the public source tree.
export function prepareBrowserReport(manifestPath, sourceRoot) {
  if (!manifestPath) return undefined;
  try {
    if (!isAbsolute(manifestPath) || resolve(manifestPath) !== manifestPath || lstatSync(manifestPath).isSymbolicLink()) throw Error();
    const bytes = readFileSync(manifestPath);
    if (bytes.length > 65_536) throw Error();
    const manifest = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    const directory = manifest.artifactDirectory;
    if (manifest.schemaVersion !== 2 || typeof directory !== 'string' || !isAbsolute(directory) || resolve(directory) !== directory ||
        realpathSync.native(directory).toLowerCase() !== directory.toLowerCase() || lstatSync(directory).isSymbolicLink() || !statSync(directory).isDirectory()) throw Error();
    const inside = relative(sourceRoot, directory);
    if (!inside || !(inside === '..' || inside.startsWith('..' + sep) || isAbsolute(inside))) throw Error();
    const start = resolve(directory, 'launch-start.json'), report = resolve(directory, 'final-playwright.json'), verdict = resolve(directory, 'final-launch.json');
    if ([start, report, verdict].some(path => existsSync(path))) throw Error();
    const manifestSHA256 = createHash('sha256').update(bytes).digest('hex');
    const write = (path, value) => writeFileSync(path, value, { flag: 'wx', mode: 0o600 });
    write(start, JSON.stringify({ schemaVersion: 1, status: 'STARTED_NOT_A_VERDICT', manifestSHA256 }) + '\n');
    return Object.freeze({
      save(text) {
        try { write(report, text); } catch { throw Error('Could not retain private browser report'); }
      },
      finish(summary) {
        try { write(verdict, JSON.stringify({ schemaVersion: 1, manifestSHA256, ...summary }) + '\n'); }
        catch { throw Error('Could not retain private launcher verdict'); }
      },
    });
  } catch { throw Error('Invalid or reused private browser report destination'); }
}
