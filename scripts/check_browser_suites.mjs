import { readdirSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { browserSuites } from "../internal/web/e2e/suites.js";

export function auditBrowserSuites(files, suites) {
  const entries = Object.values(suites).flat();
  const errors = files.filter((file) => !entries.includes(file)).map((file) => `Unassigned browser spec: ${file}`);
  for (const file of new Set(entries)) {
    if (!files.includes(file)) errors.push(`Missing browser spec: ${file}`);
    if (entries.filter((entry) => entry === file).length !== 1) errors.push(`Duplicate browser spec: ${file}`);
  }
  return errors;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const files = readdirSync(new URL("../internal/web/e2e/", import.meta.url), { recursive: true }).filter((file) => file.endsWith(".spec.js"));
  const errors = auditBrowserSuites(files, browserSuites);
  if (errors.length) {
    console.error(errors.join("\n"));
    process.exitCode = 1;
  }
}
