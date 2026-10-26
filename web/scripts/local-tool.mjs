import { readFileSync, realpathSync, statSync } from 'node:fs';
import { dirname, isAbsolute, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

export const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');

// Resolve only the installed package in this build boundary, never PATH or npx.
export function localTool(name, bin) {
  const read = path => JSON.parse(readFileSync(path, 'utf8'));
  const manifest = read(resolve(webRoot, 'package.json'));
  const lock = read(resolve(webRoot, 'package-lock.json'));
  const version = manifest.devDependencies?.[name];
  if (!/^\d+\.\d+\.\d+$/.test(version ?? '') ||
      lock.packages?.['']?.devDependencies?.[name] !== version ||
      lock.packages?.[`node_modules/${name}`]?.version !== version) {
    throw new Error(`Missing or inconsistent exact lock for ${name}`);
  }
  const root = realpathSync(resolve(webRoot, 'node_modules', name));
  const installed = read(resolve(root, 'package.json'));
  if (installed.name !== name || installed.version !== version) {
    throw new Error(`Installed ${name} does not match the lock`);
  }
  const target = installed.bin?.[bin];
  if (typeof target !== 'string' || isAbsolute(target)) throw new Error(`Missing local ${bin} CLI`);
  const cli = realpathSync(resolve(root, target));
  const path = relative(root, cli);
  if (path === '..' || path.startsWith(`..${sep}`) || isAbsolute(path) || !statSync(cli).isFile()) {
    throw new Error(`Invalid local ${bin} CLI`);
  }
  return cli;
}
