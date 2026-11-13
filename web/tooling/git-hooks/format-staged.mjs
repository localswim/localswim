/// <reference types="node" />

import { lstatSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { git, lockfile, setup, withScratch } from "./support.mjs";

function stagedFiles() {
  const changed = new Set(
    git(["diff", "--cached", "--name-only", "--no-renames", "--diff-filter=AMT", "-z", "--", "web"])
      .toString()
      .split("\0")
      .filter((file) => /\.(?:ts|js|mjs|cjs|css|html|json)$/u.test(file)),
  );
  if (!changed.size) return [];

  return git(["ls-files", "--stage", "-z", "--", "web"])
    .toString()
    .split("\0")
    .flatMap((entry) => {
      const separator = entry.indexOf("\t");
      if (separator < 0) return [];
      const file = entry.slice(separator + 1);
      const [mode, object, stage] = entry.slice(0, separator).split(" ");
      return changed.has(file) &&
        object &&
        stage === "0" &&
        (mode === "100644" || mode === "100755")
        ? [{ file, mode, object }]
        : [];
    });
}

/** @param {string} file */
function indexFile(file) {
  return git(["show", `:${file}`]);
}

async function formatter() {
  /** @type {unknown} */
  const lock = JSON.parse(indexFile(lockfile).toString());
  if (typeof lock !== "object" || !lock || !("packages" in lock)) {
    throw new Error("The staged package lock has no packages map.");
  }
  const { packages } = lock;
  if (typeof packages !== "object" || !packages || !("node_modules/prettier" in packages)) {
    throw new Error("The staged package lock has no Prettier entry.");
  }
  const entry = packages["node_modules/prettier"];
  const prettier = await import("prettier");
  if (
    typeof entry !== "object" ||
    !entry ||
    !("version" in entry) ||
    entry.version !== prettier.version
  ) {
    throw new Error("Installed Prettier differs from the staged package lock.");
  }
  return prettier;
}

/** @param {string} file @param {string} checkout @param {Buffer} original @param {Buffer} formatted */
function syncCleanFile(file, checkout, original, formatted) {
  // Leave partially staged, removed and replaced working files byte-for-byte intact.
  const path = join(checkout, file);
  if (
    lstatSync(path, { throwIfNoEntry: false })?.isFile() &&
    realpathSync(path) === path &&
    readFileSync(path).equals(original)
  ) {
    writeFileSync(path, formatted);
  }
}

async function formatStaged() {
  const files = stagedFiles();
  if (!files.length) return;

  const prettier = await formatter();
  await withScratch(async (directory) => {
    mkdirSync(join(directory, "web"));
    const configPath = join(directory, "web/.prettierrc.json");
    const ignorePath = join(directory, "web/.prettierignore");
    writeFileSync(configPath, indexFile("web/.prettierrc.json"));
    writeFileSync(ignorePath, indexFile("web/.prettierignore"));

    const updates = [];
    for (const { file, mode, object } of files) {
      const filepath = join(directory, file);
      if ((await prettier.getFileInfo(filepath, { ignorePath, resolveConfig: false })).ignored) {
        continue;
      }
      const original = git(["cat-file", "blob", object]);
      const text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(original);
      const config = await prettier.resolveConfig(filepath, { config: configPath });
      const formatted = Buffer.from(await prettier.format(text, { ...config, filepath }));
      if (!formatted.equals(original)) updates.push({ file, mode, original, formatted });
    }

    // Parse every candidate before writing the index. Git applies the replacements together.
    if (!updates.length) return;
    const records = updates.map(({ file, mode, formatted }) => {
      const object = git(["hash-object", "-w", "--stdin"], { input: formatted }).toString().trim();
      return `${mode} ${object}\t${file}\0`;
    });
    git(["update-index", "-z", "--index-info"], { input: records.join("") });
    const checkout = realpathSync(".");
    for (const { file, original, formatted } of updates) {
      syncCleanFile(file, checkout, original, formatted);
    }
  });
}

try {
  await formatStaged();
} catch (error) {
  console.error(
    `Pre-commit formatting failed. ${error instanceof Error ? error.message : String(error)}`,
  );
  console.error(`Fix the staged source or run ${setup} to install the pinned formatter.`);
  process.exitCode = 1;
}
