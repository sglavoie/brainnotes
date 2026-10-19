import { test } from "node:test";
import assert from "node:assert/strict";
import { needsPerformance, projectFilters } from "../internal/web/e2e/performance-selection.mjs";

const none = {};

test("a run without project filters runs every project, performance included", () => {
  assert.equal(needsPerformance([], none), true);
  assert.equal(needsPerformance(["smoke.spec.js"], none), true);
});
test("the PR and weekly performance wildcard selects the performance vaults", () => {
  assert.equal(needsPerformance(["--project=local-chromium-regression", "--project=performance-*"], none), true);
  assert.equal(needsPerformance(["--project", "PERFORMANCE-chromium-1000"], none), true);
  assert.equal(needsPerformance(["--project=*-10000"], none), true);
});
test("projects that never use the performance vaults do not prepare them", () => {
  assert.equal(needsPerformance(["--project=visual-chromium"], none), false);
  assert.equal(needsPerformance(["--project=local-chromium-regression", "--project=hosted-*"], none), false);
  assert.equal(needsPerformance(["--project=performance-chromium-100"], none), false);
});
test("the environment overrides the derivation and rejects other values", () => {
  assert.equal(needsPerformance(["--project=visual-chromium"], { BN_E2E_PERFORMANCE: "1" }), true);
  assert.equal(needsPerformance([], { BN_E2E_PERFORMANCE: "0" }), false);
  assert.throws(() => needsPerformance([], { BN_E2E_PERFORMANCE: "yes" }));
});
test("both spellings of --project are read", () => {
  assert.deepEqual(projectFilters(["--project=a", "x.spec.js", "--project", "b", "--project"]), ["a", "b"]);
});
