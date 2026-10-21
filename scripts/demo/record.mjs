// Records the README demos: `just demo` (or `node scripts/demo/record.mjs [web|cli]`).
//
// Every run builds an unstamped `bn`, copies scripts/demo/vault into a fresh
// directory under the system temporary root, and drives only that copy:
//
//   web  Playwright tours `bn serve` (started through vault_sandbox.py, exactly
//        like the browser fixtures), screenshots each step, and
//        svg_slideshow.mjs stacks them into docs/assets/demo-web.svg.
//   cli  Each command in CLI_STEPS runs for real in a shell; its typing and
//        output become ANSI frames that render_terminal_svg.py turns into
//        docs/assets/demo-cli.svg.
//
// No step reads $BNOTES_VAULT or the developer's HOME: the server gets an
// explicit --vault through the sandbox, and the shell gets an isolated HOME
// whose BNOTES_VAULT names the copy.

import { execFileSync, spawn } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { chromium } from "@playwright/test";

import { REPO_ROOT } from "../../internal/web/e2e/manifest.mjs";
import { refuseFixtureStorageInsideCheckout } from "../../internal/web/e2e/prepare.mjs";

import { buildSlideshowSVG } from "./svg_slideshow.mjs";

const DEMO_DIR = join(REPO_ROOT, "scripts", "demo");
const ASSETS_DIR = join(REPO_ROOT, "docs", "assets");
const VIEWPORT = { width: 1280, height: 800 };

function run(command, args, options = {}) {
  execFileSync(command, args, { stdio: ["ignore", "pipe", "inherit"], ...options });
}

function freePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      server.close(() => resolve(port));
    });
  });
}

// prepareWorld builds the binary and the vault copy, both outside the checkout
// for the reason refuseFixtureStorageInsideCheckout gives.
function prepareWorld() {
  const temporaryRoot = realpathSync(tmpdir());
  refuseFixtureStorageInsideCheckout(temporaryRoot);
  const root = mkdtempSync(join(temporaryRoot, "bn-demo-"));
  const home = join(root, "home");
  const vault = join(root, "vault");
  mkdirSync(home);
  cpSync(join(DEMO_DIR, "vault"), vault, { recursive: true });

  const bn = join(root, "bin", "bn");
  run("go", ["build", "-o", bn, "./cmd/bn"], { cwd: REPO_ROOT });

  const env = { PATH: process.env.PATH, HOME: home, GIT_CONFIG_NOSYSTEM: "1" };
  run("git", ["-c", "init.defaultBranch=main", "init", "-q"], { cwd: vault, env });
  run("git", ["config", "user.name", "Demo User"], { cwd: vault, env });
  run("git", ["config", "user.email", "demo@example.invalid"], { cwd: vault, env });
  run("git", ["add", "-A"], { cwd: vault, env });
  run("git", ["commit", "-q", "-m", "Demo vault"], { cwd: vault, env });
  return { root, home, vault, bn };
}

async function startServer(world) {
  const port = await freePort();
  const server = spawn(
    "python3",
    ["-B", join(REPO_ROOT, "scripts", "vault_sandbox.py"), "--bn", world.bn, "--sandbox", world.vault, "--keep",
      "--", "serve", "--addr", `127.0.0.1:${port}`, "--no-open"],
    {
      cwd: world.root,
      // TMPDIR is passed through because the sandbox checks the vault against it.
      env: { PATH: process.env.PATH, TMPDIR: tmpdir(), HOME: world.home, XDG_CONFIG_HOME: join(world.home, ".config") },
      stdio: ["ignore", "ignore", "inherit"],
      // Its own process group, so stopping it also stops the `bn` the sandbox
      // started; an orphaned server would hold the terminal's stderr open.
      detached: true,
    },
  );
  const stop = () => {
    try {
      process.kill(-server.pid, "SIGTERM");
    } catch {
      // Already gone.
    }
  };
  const baseURL = `http://127.0.0.1:${port}`;
  let exited = false;
  server.once("exit", () => (exited = true));
  for (let attempt = 0; attempt < 120 && !exited; attempt++) {
    try {
      if ((await fetch(`${baseURL}/api/ready`)).ok) return { stop, baseURL };
    } catch {
      // Not listening yet.
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  stop();
  throw new Error("bn serve did not become ready");
}

// A recorded browser has no pointer, so the tour draws one: a dot that follows
// the mouse and pulses on click.
const CURSOR_SCRIPT = `
addEventListener("DOMContentLoaded", () => {
  const dot = document.createElement("div");
  dot.style.cssText = "position:fixed;z-index:2147483647;width:22px;height:22px;margin:-11px 0 0 -11px;" +
    "border-radius:50%;background:rgba(250,204,21,.55);border:2px solid #facc15;pointer-events:none;" +
    "transition:transform .12s;left:" + (window.__demoX ?? 640) + "px;top:" + (window.__demoY ?? 400) + "px";
  document.body.append(dot);
  addEventListener("mousemove", (e) => { dot.style.left = e.clientX + "px"; dot.style.top = e.clientY + "px"; }, true);
  addEventListener("mousedown", () => { dot.style.transform = "scale(.6)"; }, true);
  addEventListener("mouseup", () => { dot.style.transform = ""; }, true);
});`;

function tour(page) {
  const pause = (ms) => page.waitForTimeout(ms);
  let x = 640;
  let y = 400;

  async function moveTo(locator) {
    const box = await locator.boundingBox();
    const tx = box.x + Math.min(box.width / 2, 60);
    const ty = box.y + box.height / 2;
    await page.mouse.move(tx, ty, { steps: 18 });
    x = tx;
    y = ty;
    await page.evaluate(([px, py]) => Object.assign(window, { __demoX: px, __demoY: py }), [x, y]);
  }
  async function click(locator) {
    await locator.scrollIntoViewIfNeeded();
    await moveTo(locator);
    await pause(250);
    await locator.click();
  }
  async function type(locator, text) {
    await click(locator);
    await page.keyboard.type(text, { delay: 55 });
  }
  async function goto(path) {
    await page.goto(path);
    await page.mouse.move(x, y);
  }

  return { pause, click, type, goto, moveTo };
}

// The address bar shows the default serving address rather than this run's
// random port, so the image does not change on every recording, and stops
// short of the caption beside it.
function displayURL(page) {
  const url = new URL(page.url());
  const shown = `127.0.0.1:8729${decodeURIComponent(url.pathname)}${decodeURIComponent(url.search)}`;
  return shown.length > 52 ? `${shown.slice(0, 51)}…` : shown;
}

async function recordWeb(world) {
  const { stop, baseURL } = await startServer(world);
  const browser = await chromium.launch();
  const frames = [];
  try {
    const context = await browser.newContext({ baseURL, viewport: VIEWPORT, colorScheme: "dark" });
    await context.addInitScript(CURSOR_SCRIPT);
    const page = await context.newPage();
    page.setDefaultTimeout(10000);
    const t = tour(page);
    const shot = async (caption, holdMs) => {
      await t.pause(350);
      frames.push({ jpeg: await page.screenshot({ type: "jpeg", quality: 82 }), url: displayURL(page), caption, holdMs });
    };

    // 1. The Daily Brief, then a quick capture into the inbox.
    await t.goto("/");
    await shot("Daily Brief: continue, rediscover, connect", 3000);
    await t.type(page.locator("[data-capture-input]"), "Re-read [[Deep Work]] chapter on boredom");
    await shot("Quick capture, straight into the inbox", 2200);
    await t.click(page.locator("[data-capture-submit]"));
    await shot("Captured, without choosing a file", 1800);

    // 2. Library: full-text search.
    await t.click(page.getByRole("link", { name: "Library", exact: true }).first());
    await page.waitForURL("**/library**");
    await t.type(page.getByRole("searchbox", { name: "Search notes" }), "deliberate practice");
    await page.keyboard.press("Enter");
    await page.getByRole("link", { name: /^Deep Work/ }).first().waitFor();
    await shot("Library: search titles and bodies", 3000);

    // 3. Read a note: rendered Markdown, properties and links.
    await t.click(page.getByRole("link", { name: /^Deep Work/ }).first());
    await page.waitForURL("**/n/**");
    await shot("Rendered Markdown with its properties", 3000);
    await page.mouse.wheel(0, 640);
    await shot("Related notes, ranked with reasons", 2600);

    // 4. Edit it in the browser and save.
    await t.click(page.getByRole("link", { name: "Edit in browser", exact: true }));
    const surface = page.locator("[data-source-surface] .cm-content");
    await surface.waitFor();
    await t.click(surface);
    await page.keyboard.press("ControlOrMeta+End");
    await page.keyboard.type("\n\n## Practice\n\nTwo 90-minute blocks every morning, phone in another room.", { delay: 10 });
    await shot("Edit the Markdown source with a live preview", 3000);
    await t.click(page.locator("[data-source-save]"));
    await page.locator("[data-source-status]").filter({ hasText: /Saved/ }).waitFor();
    await shot("Saved to the vault as a plain .md file", 2000);

    // 5. The link graph, filtered down to one neighbourhood.
    await t.click(page.getByRole("link", { name: "Graph", exact: true }).first());
    await page.waitForURL("**/graph**");
    await t.pause(1200);
    await shot("The link graph of the whole vault", 2800);
    await t.type(page.locator("#graph-filter-input"), "notes");
    await t.pause(1200);
    await shot("Filtered to one neighbourhood", 3200);

    await context.close();
    const output = join(ASSETS_DIR, "demo-web.svg");
    writeFileSync(output, buildSlideshowSVG(frames, VIEWPORT));
    console.log(`wrote ${output}`);
  } catch (error) {
    // Outside world.root, which is removed when the run ends.
    const failure = join(tmpdir(), "bn-demo-failure.png");
    await browser.contexts()[0]?.pages()[0]?.screenshot({ path: failure }).catch(() => {});
    console.error(`tour failed; screenshot at ${failure}`);
    throw error;
  } finally {
    await browser.close();
    stop();
  }
}

// The terminal session: each command runs for real against the vault copy.
// `clear` starts the step on an empty screen, as a person would before a long
// output.
const CLI_STEPS = [
  { caption: "saved views are named queries", command: "bn views" },
  { caption: "one query language", command: "bn find -q 'category:Books and prop:rating>=5' --json | jq -r '.[].title'" },
  { caption: "link-graph operators", command: "bn find -q 'near:[[Deep Work]]<=2 and status:wip' --json | jq -r '.[].title'" },
  { caption: "related notes, with reasons", command: "bn related 'Books/Deep Work.md' | head -12", clear: true },
  { caption: "capture without choosing a file", command: "bn capture 'Try the Feynman technique on [[Spaced Repetition]]' | head -2", clear: true },
  { caption: "bulk edits preview first", command: "bn find --view 'Reading list' | bn set - status=reading --dry-run" },
];
const ROWS = 16;
const PROMPT = "\x1b[1;36mvault\x1b[0m \x1b[2m$\x1b[0m ";
const CURSOR = "\x1b[7m \x1b[0m";

// cliFrames runs CLI_STEPS and returns the screens a viewer would see: the
// command appearing in a few bursts, then its output. Every screen is padded
// to ROWS lines so the terminal keeps one size.
function cliFrames(world) {
  const env = {
    PATH: `${join(world.root, "bin")}:${process.env.PATH}`,
    TMPDIR: tmpdir(),
    HOME: world.home,
    XDG_CONFIG_HOME: join(world.home, ".config"),
    BNOTES_VAULT: world.vault,
  };
  const frames = [];
  let lines = [];
  const screen = (current) => {
    const visible = [...lines, current].slice(-ROWS);
    return [...visible, ...Array(ROWS - visible.length).fill("")].join("\n");
  };
  for (const step of CLI_STEPS) {
    if (step.clear) lines = [];
    const typed = (text) => `${PROMPT}\x1b[1m${text}\x1b[0m`;
    frames.push({ ansi: screen(PROMPT + CURSOR), hold_ms: 600, caption: step.caption });
    for (const fraction of [0.3, 0.6, 0.85]) {
      const text = step.command.slice(0, Math.round(step.command.length * fraction));
      frames.push({ ansi: screen(typed(text) + CURSOR), hold_ms: 140, caption: step.caption });
    }
    frames.push({ ansi: screen(typed(step.command) + CURSOR), hold_ms: 500, caption: step.caption });
    const output = execFileSync("bash", ["-c", step.command], { cwd: world.vault, env, encoding: "utf8" });
    lines.push(typed(step.command), ...output.replace(/\n$/, "").split("\n"));
    frames.push({ ansi: screen(PROMPT + CURSOR), hold_ms: 2600, caption: step.caption });
  }
  return frames;
}

function recordCli(world) {
  const framesPath = join(world.root, "demo-cli.json");
  writeFileSync(framesPath, JSON.stringify(cliFrames(world)));
  const output = join(ASSETS_DIR, "demo-cli.svg");
  run("uv", ["run", "-q", join(DEMO_DIR, "render_terminal_svg.py"), framesPath, output], { stdio: "inherit" });
  console.log(`wrote ${output}`);
}

const which = process.argv[2] ?? "all";
if (!["all", "web", "cli"].includes(which)) throw new Error(`usage: record.mjs [all|web|cli], got ${which}`);
mkdirSync(ASSETS_DIR, { recursive: true });
const world = prepareWorld();
try {
  if (which !== "cli") await recordWeb(world);
  if (which !== "web") {
    // A fresh copy: the web tour captured and edited notes in the first one.
    if (which === "all") {
      rmSync(world.root, { recursive: true, force: true });
      Object.assign(world, prepareWorld());
    }
    recordCli(world);
  }
} finally {
  if (!process.env.BN_DEMO_KEEP) rmSync(world.root, { recursive: true, force: true });
}
