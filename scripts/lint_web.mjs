#!/usr/bin/env node
// The TypeScript half of the function-complexity ratchet. The rules live in
// eslint.config.js; this file only decides which files ESLint is pointed at.
//
// Two modes, mirroring `just lint-new` / `just lint-full` on the Go side:
//
//   node scripts/lint_web.mjs          gate.   Lints only the TypeScript files
//                                      changed since the ratchet base, and
//                                      exits nonzero on any violation.
//   node scripts/lint_web.mjs --full   report. Lints the whole TypeScript tree,
//                                      prints the remaining count, and exits 0.
//
// Why changed-*file* and not changed-*line*: golangci-lint has a built-in
// new-code mode that reports per changed line; ESLint has none, and the two
// alternatives are a generated per-file override list (which has to be
// maintained and rots silently) or this. The consequence is deliberate and
// worth stating plainly: editing one line of a file that still contains an
// existing violation makes that whole file the editor's problem. That is the
// ratchet — the files this bites on are exactly the ones Phase 5 and Phase 6
// exist to decompose.
//
// The base commit is read from the `ratchet_base` line in the justfile so the
// Go gate and this one cannot drift onto different anchors. Every failure to
// establish the file list is fatal: a gate that quietly lints nothing when git
// or ESLint is missing reads as green and enforces nothing.

import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = dirname(dirname(fileURLToPath(import.meta.url)));
const TS_ROOT = "internal/web/static_src";
const ESLINT_BIN = join(ROOT, "node_modules", "eslint", "bin", "eslint.js");

function fail(message, ...hints) {
  console.error(`lint:web: ${message}`);
  for (const hint of hints) console.error(`  ${hint}`);
  process.exit(1);
}

function git(...argv) {
  return execFileSync("git", argv, { cwd: ROOT, encoding: "utf8" });
}

// The one anchor, read from the one place that defines it.
function ratchetBase() {
  const justfile = join(ROOT, "justfile");
  if (!existsSync(justfile)) fail(`justfile not found at ${justfile}`);
  const match = readFileSync(justfile, "utf8").match(
    /^ratchet_base\s*:=\s*"([0-9a-fA-F]{7,40})"\s*$/m,
  );
  if (!match) {
    fail(
      "no `ratchet_base := \"<sha>\"` line in the justfile",
      "The Go gate and this one must share one anchor; add it back rather than",
      "hard-coding a second copy here.",
    );
  }
  const base = match[1];
  try {
    git("cat-file", "-e", `${base}^{commit}`);
  } catch {
    fail(
      `ratchet base ${base} does not resolve to a commit in this checkout`,
      "A shallow clone does not contain it. CI checks out with fetch-depth: 0.",
    );
  }
  return base;
}

// Changed since the base, plus anything not yet committed. `git diff <commit>`
// compares the working tree against that commit, so a violation is caught while
// it is still unstaged; untracked files are invisible to it and are added
// separately. `--diff-filter=d` drops deletions.
function changedFiles(base) {
  const tracked = git("diff", "--name-only", "--diff-filter=d", base, "--", TS_ROOT);
  const untracked = git("ls-files", "--others", "--exclude-standard", "--", TS_ROOT);
  const seen = new Set();
  for (const line of `${tracked}\n${untracked}`.split("\n")) {
    const path = line.trim();
    if (!path.endsWith(".ts") || path.endsWith(".d.ts")) continue;
    if (!existsSync(join(ROOT, path))) continue;
    seen.add(path);
  }
  return [...seen].sort();
}

function requireESLint() {
  if (existsSync(ESLINT_BIN)) return;
  fail(
    "eslint is not installed, so the TypeScript complexity gate cannot run.",
    "Install the pinned frontend tooling with:  npm ci",
  );
}

function runESLint(argv, options = {}) {
  return spawnSync(process.execPath, [ESLINT_BIN, ...argv], {
    cwd: ROOT,
    encoding: "utf8",
    ...options,
  });
}

function gate() {
  const base = ratchetBase();
  const files = changedFiles(base);
  if (files.length === 0) {
    console.log(`lint:web: no TypeScript changed since ${base.slice(0, 7)} — nothing to lint`);
    return 0;
  }
  console.log(
    `lint:web: ${files.length} TypeScript file(s) changed since ${base.slice(0, 7)}`,
  );
  const result = runESLint(["--no-error-on-unmatched-pattern", ...files], {
    stdio: "inherit",
  });
  if (result.error) fail(`could not run eslint: ${result.error.message}`);
  if (result.status === 0) console.log("lint:web: clean");
  return result.status ?? 1;
}

function full() {
  const result = runESLint([
    "--no-error-on-unmatched-pattern",
    "--format",
    "json",
    TS_ROOT,
  ]);
  if (result.error) fail(`could not run eslint: ${result.error.message}`);
  let report;
  try {
    report = JSON.parse(result.stdout);
  } catch {
    process.stderr.write(result.stderr || result.stdout);
    fail("eslint did not produce a JSON report");
  }
  const counts = new Map();
  let total = 0;
  let source = 0;
  for (const file of report) {
    // `*.test.ts` is this repo's test convention (scripts/metrics_corpus.py
    // splits the corpus the same way), so `*.test-helpers.ts` counts as source.
    const isTest = file.filePath.endsWith(".test.ts");
    for (const message of file.messages) {
      total += 1;
      if (!isTest) source += 1;
      const rule = message.ruleId ?? "(fatal)";
      const seen = counts.get(rule) ?? { total: 0, source: 0 };
      seen.total += 1;
      seen.source += isTest ? 0 : 1;
      counts.set(rule, seen);
    }
  }
  console.log(`  eslint.config.js over ${TS_ROOT}; ${report.length} files linted`);
  console.log(`  ${"Rule".padEnd(34)}${"Total".padStart(7)}${"Source".padStart(8)}${"Test".padStart(7)}`);
  for (const [rule, seen] of [...counts].sort((a, b) => b[1].total - a[1].total)) {
    const line = `${rule.padEnd(34)}${String(seen.total).padStart(7)}`;
    console.log(`  ${line}${String(seen.source).padStart(8)}${String(seen.total - seen.source).padStart(7)}`);
  }
  console.log(
    `  ${"TOTAL".padEnd(34)}${String(total).padStart(7)}${String(source).padStart(8)}${String(total - source).padStart(7)}`,
  );
  return 0;
}

requireESLint();
process.exit(process.argv.includes("--full") ? full() : gate());
