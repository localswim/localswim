/// <reference types="node" />

import { cpSync, mkdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { digest, git, installedLock, lockfile, npm, setup, withScratch } from "./support.mjs";

function pushedCommits() {
  /** @type {Set<string>} */
  const commits = new Set();
  for (const line of readFileSync(0, "utf8").trim().split("\n")) {
    if (!line) continue;
    const [, localObject] = line.trim().split(" ");
    if (!localObject) throw new Error("Git did not supply the object for a pushed ref.");
    if (/^0+$/u.test(localObject)) continue;
    const peeled = `${localObject}^{}`;
    if (git(["--no-replace-objects", "cat-file", "-t", peeled]).toString().trim() === "commit") {
      commits.add(git(["--no-replace-objects", "rev-parse", peeled]).toString().trim());
    }
  }
  return commits;
}

async function checkPush() {
  const commits = pushedCommits();
  if (!commits.size) return;
  const installed = readFileSync(installedLock, "utf8");
  const dependencies = resolve("web/node_modules");
  const gitDirectory = git(["rev-parse", "--absolute-git-dir"]).toString().trim();
  const environment = { ...process.env };
  for (const name of git(["rev-parse", "--local-env-vars"]).toString().trim().split("\n")) {
    delete environment[name];
  }

  for (const commit of commits) {
    const lock = git(["--no-replace-objects", "show", `${commit}:${lockfile}`]);
    if (digest(lock) !== installed) {
      throw new Error(
        `Installed dependencies differ from ${commit.slice(0, 12)}. Run ${setup} in a checkout of the ref being pushed.`,
      );
    }

    await withScratch((directory) => {
      const checkout = join(directory, "checkout");
      mkdirSync(checkout);
      const args = ["--no-replace-objects", "--git-dir", gitDirectory, "--work-tree", checkout];
      const options = {
        cwd: checkout,
        env: { ...environment, GIT_INDEX_FILE: join(directory, "index") },
      };
      git([...args, "read-tree", commit], options);
      git([...args, "checkout-index", "--all"], options);
      // Keep any tooling writes local to the snapshot, including release build scratch files.
      cpSync(dependencies, join(checkout, "web/node_modules"), {
        recursive: true,
        verbatimSymlinks: true,
      });
      console.log(`Checking pushed commit ${commit.slice(0, 12)}.`);
      npm("run check", { cwd: checkout, env: environment });
    });
  }
}

try {
  await checkPush();
} catch (error) {
  console.error(
    `Pre-push quality check failed. ${error instanceof Error ? error.message : String(error)}`,
  );
  console.error(
    `Run ${setup} to install dependencies. Fix the pushed ref and verify it with npm --prefix web run check.`,
  );
  process.exitCode = 1;
}
