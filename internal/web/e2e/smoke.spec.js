// What a real browser proves that jsdom cannot.
//
// Each case here fails for a reason Vitest is structurally unable to see: the
// shipped esbuild bundle never executing, the server's rendered HTML never
// being parsed by an engine, a wikilink that renders but does not navigate.
// Page-level logic stays in internal/web/static_src/*.test.ts, where it is
// cheaper to test and faster to run.

import { expect, manifest, test } from "./fixture.js";

// The rendered Markdown of the note itself. A note page also carries an insight
// panel whose container reuses the .note-body class, so the bare class selector
// matches two elements and every assertion through it is a strict-mode error.
const NOTE_BODY = ".note-body:not([data-note-insight-body])";

test("the library page lists every seeded note", async ({ page }) => {
  const response = await page.goto("/library");
  expect(response.status()).toBe(200);

  const body = page.locator("body");
  for (const rel of manifest.seedNotes) {
    await expect(body).toContainText(rel.split("/").pop().replace(/\.md$/, ""));
  }
});

test("a note page renders its Markdown body in the browser", async ({ page }) => {
  await page.goto("/n/Alpha");

  await expect(page.locator("h1")).toHaveText("Alpha");
  await expect(page.locator(NOTE_BODY)).toContainText("Alpha body");
});

test("a rendered wikilink navigates to its target", async ({ page }) => {
  await page.goto("/n/Alpha");

  // The link is produced by the Go Markdown renderer and resolved by the
  // browser. A jsdom test can assert the href string; only this can assert that
  // following it reaches the note.
  await page.locator(`${NOTE_BODY} a`, { hasText: "Beta" }).click();
  await expect(page).toHaveURL(/\/n\/beta$/);
  await expect(page.locator("h1")).toHaveText("Beta");
});

test("the shipped bundle loads and drives the page", async ({ page }) => {
  const failures = [];
  page.on("pageerror", (error) => failures.push(error.message));

  await page.goto("/");

  // internal/web/static/app.js is built with --global-name=BrainNotesApp and
  // embedded in the binary. This is the assertion the bundle-drift check in
  // `just ci` cannot make: that the bytes it compared actually run.
  //
  // It asks whether the global was *declared*, not what it holds. esbuild emits
  // `var BrainNotesApp = (() => { ... })();` and main.ts exports nothing, so the
  // value is undefined and always will be; the `var` binding appearing on
  // `window` is what proves the top level ran to completion.
  await expect.poll(() => page.evaluate(() => "BrainNotesApp" in window)).toBe(true);

  // And that it did something. The theme radios are server-rendered with
  // "System" checked and no data-theme on <html>; only the change listener
  // initTheme() installs turns a click on "Dark" into that attribute. A bundle
  // that parsed but never initialised leaves the page exactly as it arrived.
  expect(await page.evaluate(() => document.documentElement.dataset.theme ?? "")).toBe("");
  await page.locator(".source-editor-workspace > summary").click();
  await page.locator("[data-theme-control] label", { hasText: "Dark" }).click();
  await expect.poll(() => page.evaluate(() => document.documentElement.dataset.theme)).toBe("dark");

  expect(failures, "the bundle raised an uncaught error in the browser").toEqual([]);
});

test("the daily brief page renders for a vault with notes", async ({ page }) => {
  const response = await page.goto("/");
  expect(response.status()).toBe(200);

  // The empty-vault notice is the fixture's own canary: seeing it would mean
  // the server loaded a vault with no notes, which the seeded fixture is not.
  await expect(page.locator("[data-brief-empty-vault]")).toHaveCount(0);
  await expect(page.locator("[data-brief-root]")).toBeVisible();
});

test("note copy options expand with the keyboard and retain manual copying", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
  });
  await page.goto("/n/Alpha");
  const controls = page.locator(".note-copy-actions");
  await expect(controls.getByRole("button", { name: "Copy wikilink", exact: true })).toBeVisible();
  await expect(controls.getByRole("button", { name: "Copy note URL", exact: true })).toBeVisible();
  const path = controls.getByRole("button", { name: "Copy vault path", exact: true });
  await expect(path).toBeHidden();
  const summary = controls.locator("summary");
  await summary.focus();
  await summary.press("Enter");
  await expect(path).toBeVisible();
  await path.click();
  await expect(controls.locator("[data-copy-fallback]")).toHaveValue("Alpha.md");
  await expect(controls.locator("[data-copy-fallback]")).toBeFocused();
  await summary.focus();
  await summary.press("Enter");
  await expect(path).toBeHidden();
});
