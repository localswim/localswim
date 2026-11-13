import { spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdtempSync, readdirSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const web = fileURLToPath(new URL(".", import.meta.url));
const generated = join(web, "generated", "src");
const output = mkdtempSync(join(tmpdir(), "localswim-web-"));
const check = process.argv[2] === "--check";

function files(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) =>
    entry.isDirectory()
      ? files(join(directory, entry.name)).map((file) => join(entry.name, file))
      : [entry.name]
  ).sort();
}

try {
  const compiler = spawnSync(process.execPath, [
    "node_modules/typescript/lib/tsc.js", "--project", "tsconfig.json",
    "--noEmit", "false", "--outDir", output,
  ], { cwd: web, stdio: "inherit" });
  if (compiler.error) throw compiler.error;
  const source = join(output, "src");
  if (compiler.status !== 0) {
    process.exitCode = compiler.status ?? 1;
  } else if (!existsSync(join(source, "main.js"))) {
    console.error("The TypeScript build did not emit src/main.js.");
    process.exitCode = 1;
  } else if (!check) {
    rmSync(generated, { recursive: true, force: true });
    cpSync(source, generated, { recursive: true });
  } else {
    const expected = files(source);
    const actual = existsSync(generated) ? files(generated) : [];
    if (expected.length !== actual.length || expected.some((file, index) =>
      file !== actual[index] || !readFileSync(join(source, file)).equals(readFileSync(join(generated, file))))) {
      console.error("Embedded browser JavaScript is stale. Run npm --prefix web run build.");
      process.exitCode = 1;
    }
  }
} finally {
  rmSync(output, { recursive: true, force: true });
}
