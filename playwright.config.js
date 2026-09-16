// The real-browser gate: `npm run test:e2e`.
//
// Vitest/jsdom already covers the TypeScript under internal/web/static_src, and
// it covers it well — but jsdom is not a browser. It does not run the esbuild
// bundle the binary actually ships, it does not lay anything out, it does not
// navigate, and it answers `fetch` from a stub rather than from `bn serve`.
// Everything asserted here is something jsdom cannot observe. This suite is
// therefore deliberately thin: page-level behaviour belongs in Vitest, and what
// lives here is what needs a browser and a server to be true at once.
//
// `just ci` does NOT run it. The Go and Vitest gates need no browser binary;
// this one needs a ~100 MB Chromium that `npm run test:e2e:install` downloads,
// and a gate that silently skips when its engine is absent enforces nothing. It
// is its own command so the requirement is visible.
//
// Chromium only. The vault is written from a desktop browser on a Mac; the
// deployment repository's companion dashboard tests both engines because that
// one is also read from an iPhone, and stating the reason here keeps this from
// looking like an oversight.
//
// package.json is `"type": "module"`, so this file is ESM.

import { defineConfig, devices } from "@playwright/test";

import { readManifest } from "./internal/web/e2e/manifest.mjs";

const manifest = readManifest();

export default defineConfig({
  testDir: "./internal/web/e2e",
  testMatch: /.*\.spec\.js$/,
  outputDir: "./internal/web/e2e/.artifacts/results",
  globalTeardown: "./internal/web/e2e/cleanup.mjs",
  // One server, and two specs that assert on the fixture vaults' Git state.
  // Concurrency here would buy seconds and cost determinism.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 30000,
  expect: { timeout: 7000 },
  reporter: [["list"]],

  use: {
    baseURL: manifest.baseURL,
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    video: "off",
  },

  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],

  webServer: {
    // Launched through scripts/vault_sandbox.py rather than directly. That
    // script is the sanctioned way to point a `bn` process at a vault during
    // verification: it removes $BNOTES_VAULT from the child, passes --vault
    // explicitly, asks `bn config` which root it *actually* resolved, and
    // refuses with exit 2 unless that root is the sandbox named here. Running
    // `bn serve` directly would mean this harness deciding for itself that it
    // is isolated, which is what the incident behind that script looked like.
    command: [
      "python3",
      quote(`${manifest.repoRoot}/scripts/vault_sandbox.py`),
      "--bn",
      quote(manifest.bn),
      "--sandbox",
      quote(manifest.localVault),
      "--",
      "serve",
      "--addr",
      `${manifest.host}:${manifest.port}`,
      "--no-open",
    ].join(" "),
    // The fixture directory, not the checkout. `serveWebProjectRoot` walks
    // *every* ancestor of its working directory looking for a web project root,
    // so a `bn serve` started inside this repository finds it, rebuilds
    // app.js/app.css into the working tree, and serves those — the suite would
    // then be testing assets it had just generated and would have modified
    // tracked files to do it. prepare.mjs refuses a fixture root under the
    // checkout for exactly this reason (a repo-local $TMPDIR is a normal CI
    // arrangement), so by the time this runs the ancestor walk has nothing to
    // find.
    cwd: manifest.fixtureRoot,
    // The server's own environment, not the developer's. vault_sandbox.py
    // scrubs $BNOTES_VAULT and nothing else, so without this `bn serve` — and
    // every vaultgit snapshot it takes — runs under the real HOME and the real
    // global git config. A global core.excludesFile matching *.md would then
    // produce a server whose snapshots silently omit notes, while the specs'
    // own `git status` (which already runs under manifest.home) reports the
    // same vault clean. internal/vaultfixture.Isolate treats this as
    // load-bearing for Go fixtures; the browser fixture gets it too.
    env: {
      HOME: manifest.home,
      XDG_CONFIG_HOME: `${manifest.home}/.config`,
      GIT_CONFIG_GLOBAL: `${manifest.home}/.gitconfig`,
      GIT_CONFIG_NOSYSTEM: "1",
      GIT_TERMINAL_PROMPT: "0",
    },
    // /api/ready reports 200 only once a snapshot with notes exists, so waiting
    // on it means the first spec sees a loaded vault rather than a cold one.
    url: `${manifest.baseURL}/api/ready`,
    // Never adopt a server this run did not start: an already-listening process
    // on this port is by definition not the isolated fixture.
    reuseExistingServer: false,
    timeout: 60000,
    stdout: "ignore",
    stderr: "pipe",
  },
});

// quote wraps a path for the shell Playwright runs `command` through. Fixture
// paths come from mkdtemp and have no spaces today; relying on that is how a
// harness breaks on the one machine whose $TMPDIR does.
function quote(value) {
  return `'${value.replaceAll("'", `'\\''`)}'`;
}
