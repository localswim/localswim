/// <reference types="node" />

import { readFileSync } from "node:fs";
import { configFile, formatter, ignoreFile, sourceText } from "./source-format.mjs";
import { setup } from "./support.mjs";

/** @param {unknown} error */
function isSyntaxError(error) {
  return error instanceof SyntaxError && "loc" in error;
}

async function cleanSource() {
  const filepath = process.argv[2];
  if (!filepath) throw new Error("Git did not supply the source path.");
  const original = readFileSync(0);
  const prettier = await formatter(readFileSync);
  if (
    (await prettier.getFileInfo(filepath, { ignorePath: ignoreFile, resolveConfig: false })).ignored
  ) {
    return original;
  }

  const config = await prettier.resolveConfig(filepath, { config: configFile });
  if (!config) throw new Error("The canonical Prettier configuration is missing.");
  const text = sourceText(original);
  try {
    return Buffer.from(await prettier.format(text, { ...config, filepath }));
  } catch (error) {
    // Git also cleans files during status and diff. Let pre-commit diagnose incomplete syntax.
    if (isSyntaxError(error)) return original;
    throw error;
  }
}

try {
  process.stdout.write(await cleanSource());
} catch (error) {
  console.error(
    `Git source formatting failed. ${error instanceof Error ? error.message : String(error)}`,
  );
  console.error(`Run ${setup} to configure Git and install the pinned formatter.`);
  process.exitCode = 1;
}
