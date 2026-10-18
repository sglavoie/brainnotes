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
// this one needs browser binaries that `npm run test:e2e:install` downloads,
// and a gate that silently skips when an engine is absent enforces nothing. It
// is its own command so the requirement is visible.
//
// The long local regression journey stays on Chromium desktop. The release
// qualification is a separate, bounded spec run in Chromium, Firefox and
// WebKit at both desktop and mobile layout widths. Keeping those projects on an
// independent hosted vault means each engine exercises real writes without
// multiplying every destructive regression spec six times.
//
// package.json is `"type": "module"`, so this file is ESM.

import { defineConfig, devices } from "@playwright/test";

import { browserSuites } from "./internal/web/e2e/suites.js";

import { readManifest } from "./internal/web/e2e/manifest.mjs";

const manifest = readManifest();
const suite = process.env.BN_E2E_SUITE ?? "all";
if (!["all", "pr"].includes(suite)) throw new Error(`Unknown browser suite: ${suite}`);

const qualificationSpec = browserSuites.hosted;
const visualSpec = browserSuites.visual;
const readOnlySpec = browserSuites.readOnly;

const mobileLayout = (device) => ({
  ...device,
  viewport: { width: 390, height: 844 },
  hasTouch: true,
});

const qualificationProject = (name, device) => ({
  name,
  testMatch: qualificationSpec,
  use: { ...device, baseURL: manifest.hostedBaseURL },
});

const serverEnvironment = {
  HOME: manifest.home,
  XDG_CONFIG_HOME: `${manifest.home}/.config`,
  GIT_CONFIG_GLOBAL: `${manifest.home}/.gitconfig`,
  GIT_CONFIG_NOSYSTEM: "1",
  GIT_TERMINAL_PROMPT: "0",
};

// Every profile is launched through the sanctioned vault sandbox, from the
// disposable fixture root and under an isolated Git/HOME environment. The
// sandbox passes --vault explicitly and verifies the resolved root before it
// starts the server. /api/ready then proves this run's process loaded notes;
// reuseExistingServer stays false so a listener left by another run is never
// adopted as the fixture.
const fixtureServer = (vault, port, baseURL, profileArgs = []) => ({
  command: [
    "python3",
    `${manifest.repoRoot}/scripts/vault_sandbox.py`,
    "--bn",
    manifest.bn,
    "--sandbox",
    vault,
    "--",
    "serve",
    "--addr",
    `${manifest.host}:${port}`,
    "--no-open",
    ...profileArgs,
  ]
    .map(quote)
    .join(" "),
  cwd: manifest.fixtureRoot,
  env: serverEnvironment,
  url: `${baseURL}/api/ready`,
  reuseExistingServer: false,
  timeout: 60000,
  stdout: "ignore",
  stderr: "pipe",
});

export default defineConfig({
  testDir: "./internal/web/e2e",
  testMatch: /.*\.spec\.js$/,
  outputDir: "./internal/web/e2e/.artifacts/results",
  globalTeardown: "./internal/web/e2e/cleanup.mjs",
  // Three servers and several specs write to their own vaults and then assert
  // on Git state. Concurrency here would buy seconds and cost determinism.
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

  projects: [
    ...manifest.performance.map((profile) => ({
      name: `performance-chromium-${profile.count}`,
      testMatch: browserSuites.performance,
      use: { ...devices["Desktop Chrome"], baseURL: profile.baseURL },
      metadata: { noteCount: profile.count + 1 },
    })),
    {
      name: "visual-chromium",
      testMatch: visualSpec,
      snapshotPathTemplate: "{testDir}/visual-baselines/{arg}{ext}",
      use: { ...devices["Desktop Chrome"], locale: "en-US", timezoneId: "UTC" },
    },
    {
      name: "local-chromium-regression",
      testMatch: suite === "pr" ? browserSuites.pr : [...browserSuites.pr, ...browserSuites.weekly],
      use: { ...devices["Desktop Chrome"] },
    },
    qualificationProject("hosted-chromium-desktop", devices["Desktop Chrome"]),
    qualificationProject("hosted-firefox-desktop", devices["Desktop Firefox"]),
    qualificationProject("hosted-webkit-desktop", devices["Desktop Safari"]),
    qualificationProject("hosted-chromium-mobile", mobileLayout(devices["Desktop Chrome"])),
    qualificationProject("hosted-firefox-mobile", mobileLayout(devices["Desktop Firefox"])),
    qualificationProject("hosted-webkit-mobile", mobileLayout(devices["Desktop Safari"])),
    {
      name: "read-only-chromium-regression",
      testMatch: readOnlySpec,
      use: { ...devices["Desktop Chrome"], baseURL: manifest.readOnlyBaseURL },
    },
  ],

  webServer: [
    ...manifest.performance.map((profile) => fixtureServer(profile.vault, profile.port, profile.baseURL)),
    fixtureServer(manifest.localVault, manifest.port, manifest.baseURL),
    fixtureServer(manifest.hostedVault, manifest.hostedPort, manifest.hostedBaseURL, ["--hosted"]),
    fixtureServer(manifest.peerVault, manifest.readOnlyPort, manifest.readOnlyBaseURL, [
      "--read-only",
      "--state-dir",
      manifest.readOnlyState,
    ]),
  ],
});

// quote wraps a path for the shell Playwright runs `command` through. Fixture
// paths come from mkdtemp and have no spaces today; relying on that is how a
// harness breaks on the one machine whose $TMPDIR does.
function quote(value) {
  return `'${value.replaceAll("'", `'\\''`)}'`;
}
