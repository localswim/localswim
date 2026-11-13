import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";
import { build } from "esbuild";

process.chdir(fileURLToPath(new URL(".", import.meta.url)));
const check = process.argv.includes("--check");
const scratch = mkdtempSync(resolve("node_modules", ".localswim-build-"));
try {
  const compiled = join(scratch, "js");
  const result = spawnSync(
    process.execPath,
    [
      "node_modules/typescript/lib/tsc.js",
      "--project",
      "clean-room/tsconfig.json",
      "--noEmit",
      "false",
      "--sourceMap",
      "--inlineSources",
      "--outDir",
      compiled,
    ],
    { stdio: "inherit" },
  );
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error("TypeScript emission failed.");

  const output = join(scratch, "release");
  await build({
    entryPoints: [
      { in: join(compiled, "main.js"), out: "app" },
      { in: "clean-room/entry.css", out: "app" },
      { in: "clean-room/board/board.css", out: "board" },
    ],
    outdir: output,
    outExtension: { ".js": ".mjs" },
    external: ["/assets/inter-variable.woff2"],
    bundle: true,
    format: "esm",
    target: "es2024",
    minify: true,
    sourcemap: "external",
    legalComments: "eof",
  });
  const html = readFileSync("clean-room/index.html", "utf8").replace(/^[ \t]+(?=<)/gmu, "");
  writeFileSync(join(output, "index.html"), html);
  for (const file of ["inter-variable.woff2", "OFL.txt"]) {
    writeFileSync(join(output, file), readFileSync(join("clean-room/fonts", file)));
  }
  if (!check) {
    mkdirSync("generated", { recursive: true });
    mkdirSync("dist", { recursive: true });
  }
  for (const file of [
    "index.html",
    "app.mjs",
    "app.css",
    "board.css",
    "inter-variable.woff2",
    "OFL.txt",
  ]) {
    const fresh = readFileSync(join(output, file));
    const embedded = join("generated", file);
    if (check) {
      if (!existsSync(embedded) || !fresh.equals(readFileSync(embedded))) {
        throw new Error(`Generated ${file} is stale. Run npm --prefix web run build.`);
      }
    } else {
      writeFileSync(embedded, fresh);
      writeFileSync(join("dist", file), fresh);
    }
    console.log(`${file}: ${fresh.length} bytes, gzip ${gzipSync(fresh).length} bytes`);
  }
  if (!check) {
    for (const file of ["app.mjs.map", "app.css.map", "board.css.map"]) {
      /** @type {unknown} */
      const map = JSON.parse(readFileSync(join(output, file), "utf8"));
      if (
        typeof map !== "object" ||
        map === null ||
        !("sources" in map) ||
        !Array.isArray(map.sources)
      ) {
        throw new Error(`Invalid source map ${file}.`);
      }
      /** @type {readonly unknown[]} */
      const sources = map.sources;
      const stable = sources.map((path) => {
        if (typeof path !== "string") throw new Error(`Invalid source path in ${file}.`);
        return relative(resolve("dist"), resolve(output, path)).replaceAll("\\", "/");
      });
      writeFileSync(join("dist", file), JSON.stringify({ ...map, sources: stable }) + "\n");
    }
  }
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
