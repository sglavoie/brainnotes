// Vitest configuration for the frontend unit suite (`npm run test:web`).
//
// Files share workers (`isolate: false`): building a jsdom window per file was
// most of the suite's wall time. The setup file restores the page, globals and
// module graph between files, so each file still starts from a fresh page.
// package.json is `"type": "module"`, so this file is ESM.
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    isolate: false,
    setupFiles: ["internal/web/static_src/vitest-setup.test-helpers.ts"],
  },
});
