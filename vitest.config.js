// Vitest configuration for the frontend unit suite (`npm run test:web`).
//
// The setup file restores the page, globals and module graph between files,
// so a file never depends on what an earlier file in the same worker left.
// package.json is `"type": "module"`, so this file is ESM.
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    setupFiles: ["internal/web/static_src/vitest-setup.test-helpers.ts"],
  },
});
