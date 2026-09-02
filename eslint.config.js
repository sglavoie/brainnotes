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
];
