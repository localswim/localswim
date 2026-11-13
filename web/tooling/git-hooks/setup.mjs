/// <reference types="node" />

import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { digest, git, installedLock, lockfile, npm } from "./support.mjs";

try {
  process.chdir(fileURLToPath(new URL("../../../", import.meta.url)));
  const lock = digest(readFileSync(lockfile));
  npm("ci");
  if (digest(readFileSync(lockfile)) !== lock) {
    throw new Error("The package lock changed during installation. Run setup again.");
  }
  writeFileSync(installedLock, lock);
  git(["config", "--local", "core.hooksPath", ".githooks"]);
  console.log("Installed web dependencies and configured Git hooks.");
} catch (error) {
  console.error(`Git hook setup failed. ${error instanceof Error ? error.message : String(error)}`);
  process.exitCode = 1;
}
