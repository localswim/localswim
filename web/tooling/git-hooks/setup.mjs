/// <reference types="node" />

import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { cleanCommand, filter } from "./source-format.mjs";
import { digest, git, installedLock, lockfile, npm } from "./support.mjs";

try {
  process.chdir(fileURLToPath(new URL("../../../", import.meta.url)));
  const lock = digest(readFileSync(lockfile));
  npm("ci");
  if (digest(readFileSync(lockfile)) !== lock) {
    throw new Error("The package lock changed during installation. Run setup again.");
  }
  writeFileSync(installedLock, lock);
  git(["config", "--local", `${filter}.clean`, cleanCommand]);
  git(["config", "--local", `${filter}.smudge`, "cat"]);
  git(["config", "--local", `${filter}.required`, "true"]);
  git(["config", "--local", "core.hooksPath", ".githooks"]);
  console.log("Installed web dependencies and configured Git source formatting and hooks.");
} catch (error) {
  console.error(`Git hook setup failed. ${error instanceof Error ? error.message : String(error)}`);
  process.exitCode = 1;
}
