/// <reference types="node" />

import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

export const setup = "node web/tooling/git-hooks/setup.mjs";
export const lockfile = "web/package-lock.json";
export const installedLock = "web/node_modules/.localswim-lock";

/** @param {string[]} args @param {import("node:child_process").SpawnSyncOptionsWithBufferEncoding} [options] */
export function git(args, options = {}) {
  const result = spawnSync("git", args, { maxBuffer: 32 * 1024 * 1024, ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(`git ${args[0]} failed. ${result.stderr.toString().trim()}`);
  }
  return result.stdout;
}

/** @param {"ci" | "run check"} operation @param {import("node:child_process").SpawnSyncOptions} [options] */
export function npm(operation, options = {}) {
  const windows = process.platform === "win32";
  const args = windows
    ? ["/d", "/s", "/c", `npm.cmd --prefix web ${operation}`]
    : ["--prefix", "web", ...operation.split(" ")];
  const result = spawnSync(windows ? "cmd.exe" : "npm", args, { stdio: "inherit", ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`npm ${operation} failed.`);
}

/** @param {Buffer} bytes */
export function digest(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

/** @param {(directory: string) => Promise<void> | void} action */
export async function withScratch(action) {
  const directory = mkdtempSync(join(tmpdir(), "localswim-hooks-"));
  try {
    return await action(directory);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}
