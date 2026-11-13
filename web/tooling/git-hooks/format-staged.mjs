/// <reference types="node" />

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { basename, dirname, isAbsolute, join } from "node:path";
import { isDeepStrictEqual } from "node:util";
import {
  configFile,
  formatPolicyFiles,
  formatter,
  ignoreFile,
  sourceText,
} from "./source-format.mjs";
import { git, setup, withScratch } from "./support.mjs";

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

/** @param {string} file @param {Buffer} working @param {Buffer} staged */
function sameFormatterPolicy(file, working, staged) {
  const workingText = sourceText(working).replace(/\r\n/gu, "\n");
  const stagedText = sourceText(staged).replace(/\r\n/gu, "\n");
  if (workingText === stagedText) return true;
  if (file !== configFile) return false;

  // Git's clean filter can normalize this JSON before the hook compares the two copies.
  try {
    /** @type {unknown} */
    const workingConfig = JSON.parse(workingText);
    /** @type {unknown} */
    const stagedConfig = JSON.parse(stagedText);
    return isDeepStrictEqual(workingConfig, stagedConfig);
  } catch {
    return false;
  }
}

function usesPathLimitedIndex() {
  const index = process.env.GIT_INDEX_FILE;
  if (!index || !isAbsolute(index) || !/^next-index-\d+\.lock$/u.test(basename(index))) {
    return false;
  }
  const gitDirectory = git(["rev-parse", "--absolute-git-dir"]).toString().trimEnd();
  return dirname(index).replaceAll("\\", "/") === gitDirectory;
}

async function formatStaged() {
  const files = stagedFiles();
  if (!files.length) return;

  for (const file of formatPolicyFiles) {
    if (!sameFormatterPolicy(file, readFileSync(file), indexFile(file))) {
      throw new Error(
        `The working copy of ${file} differs from this commit's version. Include its full change in the commit or restore the working copy to the staged version.`,
      );
    }
  }

  const prettier = await formatter(indexFile);
  await withScratch(async (directory) => {
    mkdirSync(join(directory, "web"));
    const configPath = join(directory, configFile);
    const ignorePath = join(directory, ignoreFile);
    writeFileSync(configPath, indexFile(configFile));
    writeFileSync(ignorePath, indexFile(ignoreFile));

    const updates = [];
    for (const { file, mode, object } of files) {
      const filepath = join(directory, file);
      if ((await prettier.getFileInfo(filepath, { ignorePath, resolveConfig: false })).ignored) {
        continue;
      }
      const original = git(["cat-file", "blob", object]);
      const config = await prettier.resolveConfig(filepath, { config: configPath });
      const formatted = Buffer.from(
        await prettier.format(sourceText(original), { ...config, filepath }),
      );
      if (!formatted.equals(original)) updates.push({ file, mode, formatted });
    }

    if (!updates.length) return;
    if (usesPathLimitedIndex()) {
      // A configured clean filter already normalized both indexes for a path-limited commit.
      // A difference here means rewriting Git's temporary index alone would leave stale staging.
      throw new Error(`Git did not normalize the selected paths. Run ${setup} and retry.`);
    }
    // Parse every candidate before writing the index. Git applies the replacements together.
    const records = updates.map(({ file, mode, formatted }) => {
      const object = git(["hash-object", "-w", "--stdin"], { input: formatted }).toString().trim();
      return `${mode} ${object}\t${file}\0`;
    });
    git(["update-index", "-z", "--index-info"], { input: records.join("") });
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
