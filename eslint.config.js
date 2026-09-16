// TypeScript function-complexity ratchet — the frontend half of the gate that
// .golangci.yml defines for Go. See docs/internals/refactor-baseline.md.
//
// This config is measurement and gate at once, exactly like the Go one:
// `npm run lint:web:full` runs it over the whole TypeScript tree to report the
// remaining count, and `npm run lint:web` runs it over files changed since the
// ratchet base so the existing violations do not block while no new one can
// land. scripts/lint_web.mjs is the file-selection half of that; the rules
// themselves live only here.
//
// The thresholds mirror .golangci.yml where the two languages have a
// comparable rule: complexity 10 = cyclop 10, max-lines-per-function 100 =
// funlen 100. max-depth 4 is the nestif 4 analogue. There is no ESLint
// equivalent of gocognit, so cognitive complexity is unmeasured on this side.
//
// package.json is `"type": "module"`, so this file is ESM.
import tseslint from "typescript-eslint";

export default [
  {
    // An object with only `ignores` sets the global ignore list.
    ignores: [
      // Build output and vendored third-party assets. Named here as well as in
      // .refactorignore and .golangci.yml so all three exclude the same paths.
      "internal/web/static/**",
      // Ambient declarations, not hand-written logic: every declaration in the
      // file is unused by construction, so no-unused-vars would fire on all of
      // them. .refactorignore excludes it from the metrics denominator for the
      // same reason.
      "internal/web/static_src/globals.d.ts",
    ],
  },
  {
    // Test files are deliberately NOT excluded. .golangci.yml lints Go test
    // files under the same thresholds as Go source, and the baseline doc counts
    // both; splitting the decision by language would make the two halves of one
    // ratchet disagree.
    files: ["internal/web/static_src/**/*.ts"],
    languageOptions: {
      parser: tseslint.parser,
      ecmaVersion: 2022,
      sourceType: "module",
    },
    plugins: {
      "@typescript-eslint": tseslint.plugin,
    },
    rules: {
      complexity: ["error", 10],
      "max-lines-per-function": ["error", 100],
      "max-depth": ["error", 4],
      "@typescript-eslint/no-unused-vars": "error",
    },
  },
  {
    // The browser harness, which is plain ESM JavaScript rather than TypeScript
    // and so falls outside every pattern above.
    //
    // It is a separate block rather than an extra glob on the one above, and
    // `npm run lint:e2e` rather than part of `npm run lint:web`, because
    // scripts/lint_web.mjs is a *ratchet*: it counts violations under
    // internal/web/static_src and docs/internals/refactor-baseline.md records
    // that number. Folding these files into it would move a recorded baseline
    // to lint files the baseline was never about. The rules are the same; only
    // the bookkeeping is separate. `npm run test:e2e` runs this first, so the
    // gate that owns these files is the gate that lints them.
    //
    // No @typescript-eslint here: the plugin's no-unused-vars wants type-aware
    // parsing these files have no tsconfig for, and the core rule reports the
    // same thing for plain JavaScript.
    files: ["internal/web/e2e/**/*.{js,mjs}", "playwright.config.js"],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: "module",
      globals: {
        process: "readonly",
        console: "readonly",
        window: "readonly",
        document: "readonly",
        fetch: "readonly",
        URL: "readonly",
      },
    },
    rules: {
      complexity: ["error", 10],
      "max-lines-per-function": ["error", 100],
      "max-depth": ["error", 4],
      "no-unused-vars": "error",
    },
  },
];
