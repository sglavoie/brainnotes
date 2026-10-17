import { execFileSync } from "node:child_process";
import { readFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const args = JSON.parse(readFileSync("internal/cli/web-build-args.json", "utf8"));
const binary = resolve("node_modules/esbuild/bin/esbuild");
if (!process.argv.includes("--check")) {
  execFileSync(binary, args, { stdio: "inherit" });
} else {
  // Compare every generated bundle without rewriting the tracked artifacts.
  const scratch = mkdtempSync(join(tmpdir(), "bn-web-build-"));
  try {
    execFileSync(binary, [...args, `--outdir=${scratch}`], { stdio: "inherit" });
    for (const entry of args.filter((arg) => !arg.startsWith("--"))) {
      const file = `${entry.split("=")[0]}.js`;
      const generated = readFileSync(join(scratch, file));
      const committed = readFileSync(join("internal/web/static", file));
      if (!generated.equals(committed)) throw new Error(`Bundle drift in ${file}; run just web-build.`);
    }
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}
