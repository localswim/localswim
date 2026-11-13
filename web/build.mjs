/// <reference types="node" />
// @ts-check
import { spawnSync } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { brotliCompressSync, gzipSync } from "node:zlib";
import { build } from "esbuild";

const web = fileURLToPath(new URL(".", import.meta.url));
const dist = join(web, "dist");
const check = process.argv.includes("--check");
const sizes = process.argv.includes("--sizes");

/** @param {string} scratch */
function emitJavaScript(scratch) {
  const compiler = spawnSync(
    process.execPath,
    [
      "node_modules/typescript/lib/tsc.js",
      "--project",
      "tsconfig.json",
      "--noEmit",
      "false",
      "--inlineSourceMap",
      "--inlineSources",
      "--outDir",
      scratch,
    ],
    { cwd: web, stdio: "inherit" },
  );
  if (compiler.error) throw compiler.error;
  if (compiler.status !== 0) throw new Error("TypeScript release emission failed.");
}

async function releaseAssets() {
  const scratch = mkdtempSync(join(web, "node_modules", ".localswim-build-"));
  try {
    emitJavaScript(scratch);
    const { outputFiles } = await build({
      absWorkingDir: web,
      entryPoints: [
        { in: join(scratch, "src", "main.js"), out: "app" },
        { in: "app.css", out: "app" },
      ],
      outdir: dist,
      entryNames: "[name]",
      bundle: true,
      minify: true,
      format: "esm",
      target: "es2024",
      legalComments: "inline",
      sourcemap: "linked",
      write: false,
      // Preserve ordinary object property names, including API and storage fields.
      mangleProps: undefined,
    });
    const assets = new Map(outputFiles.map((file) => [relative(dist, file.path), file.contents]));
    const html = readFileSync(join(web, "index.html"), "utf8");
    // The HTML parser ignores space outside the doctype before the document root.
    assets.set(
      "index.html",
      Buffer.from(html.replace(/^[\t\n\f\r ]*(<!doctype html>)[\t\n\f\r ]+(?=<html\b)/iu, "$1")),
    );
    return assets;
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

const assets = await releaseAssets();
if (check || sizes) {
  const actual = existsSync(dist) ? readdirSync(dist, { withFileTypes: true }) : [];
  if (
    actual.length !== assets.size ||
    actual.some((file) => {
      const expected = assets.get(file.name);
      return !file.isFile() || !expected || !readFileSync(join(dist, file.name)).equals(expected);
    })
  ) {
    console.error("Embedded browser release assets are stale. Run npm --prefix web run build.");
    process.exitCode = 1;
  }
} else {
  rmSync(dist, { recursive: true, force: true });
  mkdirSync(dist);
  for (const [name, contents] of assets) {
    writeFileSync(join(dist, name), contents);
  }
}

if (sizes && !process.exitCode) {
  console.table(
    Array.from(assets, ([asset, bytes]) => ({
      asset,
      raw: bytes.byteLength,
      gzip: gzipSync(bytes).byteLength,
      brotli: brotliCompressSync(bytes).byteLength,
    })),
  );
}
