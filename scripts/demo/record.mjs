// Records the README demos: `just demo` (or `node scripts/demo/record.mjs [web|cli]`).
//
// Every run builds an unstamped `bn`, copies scripts/demo/vault into a fresh
// directory under the system temporary root, and drives only that copy:
//
//   web  Playwright tours `bn serve` (started through vault_sandbox.py, exactly
//        like the browser fixtures) and ffmpeg turns the video into
//        docs/assets/demo-web.gif.
//   cli  VHS replays scripts/demo/cli.tape against the same throwaway vault
//        into docs/assets/demo-cli.gif.
//
// No step reads $BNOTES_VAULT or the developer's HOME: the server gets an
// explicit --vault through the sandbox, and the terminal gets an isolated HOME
// whose BNOTES_VAULT names the copy.

import { execFileSync, spawn } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { chromium } from "@playwright/test";

import { REPO_ROOT } from "../../internal/web/e2e/manifest.mjs";
import { refuseFixtureStorageInsideCheckout } from "../../internal/web/e2e/prepare.mjs";

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

async function recordWeb(world) {
  const { stop, baseURL } = await startServer(world);
  const videoDir = join(world.root, "video");
  const browser = await chromium.launch();
  try {
    const context = await browser.newContext({
      baseURL,
      viewport: VIEWPORT,
      colorScheme: "dark",
      recordVideo: { dir: videoDir, size: VIEWPORT },
    });
    await context.addInitScript(CURSOR_SCRIPT);
    const page = await context.newPage();
    page.setDefaultTimeout(10000);
    const started = Date.now();
    const t = tour(page);

    // 1. The Daily Brief, then a quick capture into the inbox.
    await t.goto("/");
    const startOffset = (Date.now() - started) / 1000;
    await t.pause(1800);
    await t.type(page.locator("[data-capture-input]"), "Re-read [[Deep Work]] chapter on boredom");
    await t.pause(400);
    await t.click(page.locator("[data-capture-submit]"));
    await t.pause(1500);

    // 2. Library: full-text search, then one query expression for the shelf.
    await t.click(page.getByRole("link", { name: "Library", exact: true }).first());
    await page.waitForURL("**/library**");
    await t.pause(600);
    await t.type(page.getByRole("searchbox", { name: "Search notes" }), "deliberate practice");
    await page.keyboard.press("Enter");
    await t.pause(2200);

    // 3. Read a note: rendered Markdown, properties and links.
    await t.click(page.getByRole("link", { name: /^Deep Work/ }).first());
    await page.waitForURL("**/n/**");
    await t.pause(1500);
    await page.mouse.wheel(0, 420);
    await t.pause(1600);

    // 4. Edit it in the browser and save.
    await t.click(page.getByRole("link", { name: "Edit in browser", exact: true }));
    const surface = page.locator("[data-source-surface] .cm-content");
    await surface.waitFor();
    await t.pause(800);
    await t.click(surface);
    await page.keyboard.press("ControlOrMeta+End");
    await page.keyboard.type("\n\n## Practice\n\nTwo 90-minute blocks every morning, phone in another room.", { delay: 35 });
    await t.pause(600);
    await t.click(page.locator("[data-source-save]"));
    await t.pause(1800);

    // 5. The link graph, filtered down to one neighbourhood.
    await t.click(page.getByRole("link", { name: "Graph", exact: true }).first());
    await page.waitForURL("**/graph**");
    await t.pause(2200);
    await t.type(page.locator("#graph-filter-input"), "notes");
    await t.pause(2800);

    await context.close().catch(() => {});
    const [video] = readdirSync(videoDir).filter((name) => name.endsWith(".webm"));
    toGif(join(videoDir, video), join(ASSETS_DIR, "demo-web.gif"), startOffset, 880);
  } catch (error) {
    // Outside world.root, which is removed when the run ends.
    const shot = join(tmpdir(), "bn-demo-failure.png");
    await browser.contexts()[0]?.pages()[0]?.screenshot({ path: shot }).catch(() => {});
    console.error(`tour failed; screenshot at ${shot}`);
    throw error;
  } finally {
    await browser.close();
    stop();
  }
}

// toGif builds a palette from this video: a GIF has at most 256 colours, and a
// generic palette bands the dark theme. 10 fps, 880 px and 96 colours keep the
// tour legible at around 5 MB, under GitHub's 10 MB inline image limit.
function toGif(input, output, start, width) {
  run("ffmpeg", ["-y", "-loglevel", "error", "-ss", String(start), "-i", input,
    "-vf", `fps=10,scale=${width}:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96:stats_mode=diff[p];` +
      "[b][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle",
    output]);
  console.log(`wrote ${output}`);
}

function recordCli(world) {
  const tape = readFileSync(join(DEMO_DIR, "cli.tape"), "utf8").replaceAll("{{OUTPUT}}", join(ASSETS_DIR, "demo-cli.gif"));
  const tapePath = join(world.root, "cli.tape");
  writeFileSync(tapePath, tape);
  // VHS starts its shell with this environment; the prompt and HOME are the
  // demo's own so nothing from the developer's shell ends up in the recording.
  run("vhs", [tapePath], {
    cwd: world.vault,
    stdio: ["ignore", "inherit", "inherit"],
    env: {
      PATH: `${join(world.root, "bin")}:${process.env.PATH}`,
      TMPDIR: tmpdir(),
      HOME: world.home,
      XDG_CONFIG_HOME: join(world.home, ".config"),
      BNOTES_VAULT: world.vault,
      TERM: "xterm-256color",
    },
  });
  console.log(`wrote ${join(ASSETS_DIR, "demo-cli.gif")}`);
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
