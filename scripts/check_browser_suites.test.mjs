import { test } from "node:test";
import assert from "node:assert/strict";
import { auditBrowserSuites } from "./check_browser_suites.mjs";

test("a new spec cannot silently miss CI", () => {
  assert.deepEqual(auditBrowserSuites(["old.spec.js", "nested/new.spec.js"], { pr: ["old.spec.js"] }), ["Unassigned browser spec: nested/new.spec.js"]);
});
test("deleted specs and duplicate assignments fail coverage", () => {
  assert.deepEqual(auditBrowserSuites(["a.spec.js"], { pr: ["a.spec.js", "gone.spec.js"], weekly: ["a.spec.js"] }), [
    "Duplicate browser spec: a.spec.js", "Missing browser spec: gone.spec.js",
  ]);
});
test("every declared suite contributes to coverage", () => {
  assert.deepEqual(auditBrowserSuites(["a.spec.js", "b.spec.js"], { pr: ["a.spec.js"], weekly: ["b.spec.js"] }), []);
});
