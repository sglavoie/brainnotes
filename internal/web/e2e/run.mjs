#!/usr/bin/env node
// `npm run test:e2e` after its lint step: prepare the fixture, then run
// Playwright with the arguments this script was given.
//
// It exists so prepare.mjs can see the `--project` filters that npm appends to
// the last command of a script. Those filters decide whether the performance
// vaults are seeded and served at all; see performance-selection.mjs.

import { spawnSync } from "node:child_process";
import { join } from "node:path";

import { E2E_DIR, REPO_ROOT } from "./manifest.mjs";
import { needsPerformance } from "./performance-selection.mjs";

const args = process.argv.slice(2);
const env = { ...process.env, BN_E2E_PERFORMANCE: needsPerformance(args) ? "1" : "0" };

function run(command, commandArgs) {
  const result = spawnSync(command, commandArgs, { cwd: REPO_ROOT, env, stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}

run(process.execPath, [join(E2E_DIR, "prepare.mjs")]);
run(join(REPO_ROOT, "node_modules", ".bin", "playwright"), ["test", ...args]);
