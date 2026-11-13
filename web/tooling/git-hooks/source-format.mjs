/// <reference types="node" />

import { lockfile } from "./support.mjs";

export const configFile = "web/.prettierrc.json";
export const ignoreFile = "web/.prettierignore";
export const formatPolicyFiles = [".gitattributes", configFile, ignoreFile, lockfile];
export const filter = "filter.localswim-prettier";
export const cleanCommand = "node web/tooling/git-hooks/clean-source.mjs %f";

/** @param {(file: string) => Buffer} read */
export async function formatter(read) {
  /** @type {unknown} */
  const lock = JSON.parse(read(lockfile).toString());
  if (typeof lock !== "object" || !lock || !("packages" in lock)) {
    throw new Error("The package lock has no packages map.");
  }
  const { packages } = lock;
  if (typeof packages !== "object" || !packages || !("node_modules/prettier" in packages)) {
    throw new Error("The package lock has no Prettier entry.");
  }
  const entry = packages["node_modules/prettier"];
  const prettier = await import("prettier");
  if (
    typeof entry !== "object" ||
    !entry ||
    !("version" in entry) ||
    entry.version !== prettier.version
  ) {
    throw new Error("Installed Prettier differs from the package lock.");
  }
  return prettier;
}

/** @param {Buffer} bytes */
export function sourceText(bytes) {
  return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes);
}
