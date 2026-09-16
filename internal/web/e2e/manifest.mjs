// Where the e2e fixture manifest lives, and how to read it.
//
// It is its own module so that prepare.mjs, cleanup.mjs, playwright.config.js
// and the specs agree on one path without importing prepare.mjs — which builds
// a binary and a pair of vaults the moment it is loaded.

import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export const E2E_DIR = dirname(fileURLToPath(import.meta.url));
export const REPO_ROOT = dirname(dirname(dirname(E2E_DIR)));
export const ARTIFACTS_DIR = join(E2E_DIR, ".artifacts");
export const MANIFEST_PATH = join(ARTIFACTS_DIR, "fixture.json");

// readManifest fails loudly rather than returning a default. A suite that ran
// against an absent fixture would either refuse to start or — worse — reach
// whatever is already listening on a guessed port.
export function readManifest() {
  if (!existsSync(MANIFEST_PATH)) {
    throw new Error(
      `e2e fixture manifest not found at ${MANIFEST_PATH}. ` +
        `Run \`npm run test:e2e\`, which prepares the fixture before starting Playwright.`,
    );
  }
  return JSON.parse(readFileSync(MANIFEST_PATH, "utf8"));
}
