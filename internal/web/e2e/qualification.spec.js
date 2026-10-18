// Release qualification for the writable hosted workspace.
//
// playwright.config.js runs this file once in every browser/layout row. The
// older specs remain the deep Chromium/local regression suite; these cases are
// the compact cross-engine contract: browse the Brief and library, enter both
// shortcut-driven and IME-style text, save through the hosted profile, survive
// an Access-style HTML answer, and probe the hosted origin's security boundary
// directly instead of trusting hidden controls.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { expect, gitStatus, manifest, test } from "./fixture.js";
import { caretToEnd, keptDrafts, openEditor } from "./editor-helpers.js";

const NOTE = "Alpha.md";
const NOTE_PATH = join(manifest.hostedVault, NOTE);
const IME_TEXT = "日本語入力";

test("editor Find searches long drafts, replaces, and undoes in either layout", async ({ page }, testInfo) => {
  const before = readFileSync(NOTE_PATH, "utf8");
  const surface = await openEditor(page);
  const needle = "searchneedle";
  await caretToEnd(page);
  await page.keyboard.insertText(`\n${needle}\n${"Padding paragraph.\n\n".repeat(160)}${needle}\n`);
  const previewTab = page.locator('[data-source-tab="preview"]');
  if (testInfo.project.name.endsWith("mobile")) await previewTab.click();
  await page.getByRole("button", { name: "Find", exact: true }).click();
  const panel = page.locator(".cm-search");
  const find = panel.getByRole("textbox", { name: "Find", exact: true });
  await expect(find).toBeFocused();
  await expect(surface).toBeVisible();
  await find.fill(needle);
  await find.press("Tab");
  await find.press("Enter");
  const firstScroll = await page.locator(".cm-scroller").evaluate((node) => node.scrollTop);
  await find.press("Enter");
  await expect.poll(() => page.locator(".cm-scroller").evaluate((node) => node.scrollTop)).not.toBe(firstScroll);
  await find.press("Shift+Enter");
  await expect(panel.getByRole("button", { name: "previous", exact: true })).toBeVisible();

  await panel.getByRole("textbox", { name: "Replace", exact: true }).fill("replacementneedle");
  await panel.getByRole("button", { name: "replace all", exact: true }).click();
  await expect.poll(async () => (await keptDrafts(page))[0]?.content.match(/replacementneedle/g)?.length).toBe(2);
  expect(readFileSync(NOTE_PATH, "utf8")).toBe(before);
  await find.press("Escape");
  await expect(panel).toBeHidden();
  await expect(surface).toBeFocused();
  await page.keyboard.press("ControlOrMeta+z");
  await expect.poll(async () => (await keptDrafts(page))[0]?.content.match(/searchneedle/g)?.length).toBe(2);
  await page.keyboard.press("ControlOrMeta+f");
  await expect(find).toBeFocused();
  await find.fill("definitelynomatch");
  await find.press("Tab");
  await find.press("Enter");
  await expect(page.locator(".cm-searchMatch")).toHaveCount(0);
  expect(readFileSync(NOTE_PATH, "utf8")).toBe(before);
});

function qualificationPhrase(projectName) {
  return `qualification ${projectName} ${manifest.fixtureRoot.split("/").pop()}`;
}

function compositionEvent(surface, type, data) {
  return surface.evaluate(
    (element, event) =>
      element.dispatchEvent(
        new window.CompositionEvent(event.type, { bubbles: true, data: event.data }),
      ),
    { type, data },
  );
}

for (const path of ["/", "/properties"]) {
  test(`header search opens a note from ${path} with one click or tap`, async ({ page }, testInfo) => {
    await page.goto(path);
    if (path === "/") await page.locator(".source-editor-workspace > summary").click();
    const input = page.locator("#search-input");
    await input.fill("Alpha");
    const title = page.locator("#search-results .search-result-title").filter({ hasText: /^Alpha$/ });
    await expect(title).toBeVisible();
    await expect(input).toBeFocused();
    if (testInfo.project.use.hasTouch) {
      await title.tap();
    } else {
      await title.click();
    }
    await expect(page).toHaveURL((url) => url.pathname === "/n/alpha");
    if (path === "/") await expect(page.getByRole("link", { name: "Back to Brief", exact: true })).toBeVisible();
    await expect(page.locator("h1")).toHaveText("Alpha");
  });
}

test("hosted browsing, keyboard save, and IME composition form one complete workflow", async ({
  page,
}, testInfo) => {
  const mobile = testInfo.project.name.endsWith("-mobile");
  const viewport = page.viewportSize();
  expect(viewport).not.toBeNull();
  expect(viewport.width <= 500, "the project name and layout width disagree").toBe(mobile);

  const brief = await page.goto("/");
  expect(brief.status()).toBe(200);
  await expect(page.locator("[data-brief-empty-vault]")).toHaveCount(0);
  await expect(page.locator("[data-brief-root]")).toBeVisible();
  await page.waitForLoadState("networkidle");

  await page.goto("/library");
  await page.getByRole("link", { name: "Alpha", exact: true }).click();
  await expect(page).toHaveURL(/\/n\/alpha$/);
  await expect(page.locator("h1")).toHaveText("Alpha");

  const before = readFileSync(NOTE_PATH, "utf8");
  const surface = await openEditor(page);
  const phrase = qualificationPhrase(testInfo.project.name);
  await caretToEnd(page);
  await page.keyboard.press("Enter");
  await page.keyboard.type(`## ${phrase}`);
  await page.keyboard.press("Enter");
  await page.keyboard.insertText(IME_TEXT);
  await expect(surface).toContainText(phrase);
  await expect(surface).toContainText(IME_TEXT);

  // A pending completion normally consumes Enter so it cannot break a
  // wikilink. During composition it must decline that guard: some IMEs report
  // their commit as Enter, and consuming it loses the composition. Dispatching
  // the browser's composition events around a real contenteditable insertion
  // drives CodeMirror's view.composing state; the newline proves Enter reached
  // the editor while the search request was still unanswered.
  let releaseSearch;
  const searchGate = new Promise((resolve) => {
    releaseSearch = resolve;
  });
  await page.route("**/api/search?*", async (route) => {
    await searchGate;
    await route.fulfill({ json: { results: [] } });
  });
  const searchRequest = page.waitForRequest((request) => request.url().includes("/api/search?"));
  await page.keyboard.press("Enter");
  await page.keyboard.type("[[composition");
  await searchRequest;
  await compositionEvent(surface, "compositionstart", "");
  await page.keyboard.insertText(IME_TEXT);
  await expect(surface).toContainText(IME_TEXT);
  await page.keyboard.press("Enter");
  await compositionEvent(surface, "compositionend", IME_TEXT);
  releaseSearch();

  await page.keyboard.press("ControlOrMeta+s");
  await expect(page.locator("[data-source-status]")).toHaveClass(/is-success/);
  await expect(page.locator("[data-source-save]")).toBeDisabled();

  const after = readFileSync(NOTE_PATH, "utf8");
  expect(after.startsWith(before), "the save rewrote source above the insertion").toBe(true);
  expect(after).toContain(phrase);
  expect(after).toContain(IME_TEXT);
  expect(after).toContain(`[[composition${IME_TEXT}\n`);
  expect(gitStatus(manifest.hostedVault), "the hosted save was not snapshotted").toBe("");
});

test("an expired sign-in answer keeps the editor dirty and reports an unknown save", async ({
  page,
}, testInfo) => {
  const before = readFileSync(NOTE_PATH, "utf8");
  const surface = await openEditor(page);
  const phrase = qualificationPhrase(`expired-${testInfo.project.name}`);

  await page.route("**/api/source/save", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/html; charset=utf-8",
      body: "<!doctype html><title>Sign in</title><h1>Your session expired</h1>",
    }),
  );
  await caretToEnd(page);
  await page.keyboard.press("Enter");
  await page.keyboard.insertText(phrase);
  await page.locator("[data-source-save]").click();

  const status = page.locator("[data-source-status]");
  await expect(status).toHaveClass(/is-error/);
  await expect(status).toContainText("Save not confirmed.");
  await expect(status).toContainText("If sign-in expired, sign in again");
  await expect(status).toContainText("press Save to retry safely");
  await expect(page.locator("[data-source-save]")).toBeEnabled();
  await expect(surface).toContainText(phrase);
  expect(readFileSync(NOTE_PATH, "utf8"), "the simulated sign-in page wrote the note").toBe(before);
});

test("direct hosted-origin requests refuse forgery and private or active content", async ({
  page,
  request,
}) => {
  await page.goto("/edit?key=Alpha");
  const token = await page.locator("[data-source-editor]").getAttribute("data-source-csrf");
  expect(token).toBeTruthy();

  const forged = await request.post("/api/source/preview", {
    headers: {
      Origin: "https://attacker.invalid",
      "Sec-Fetch-Site": "cross-site",
      "X-BN-CSRF": token,
    },
    form: { content: "forged", token },
  });
  expect(forged.status()).toBe(403);
  expect(await forged.text()).toContain("cross-origin request refused");

  for (const name of ["brainnotes.toml", "brainnotes.local.toml"]) {
    const response = await request.get(`/media/${name}`);
    expect(response.status(), `${name} was exposed as media`).toBe(404);
    expect(await response.text()).not.toContain("private-hosted");
  }

  for (const path of manifest.activeMedia) {
    const response = await request.get(`/media/${path}`);
    expect(response.status(), path).toBe(200);
    expect(response.headers()["content-security-policy"], path).toBe("sandbox");
    expect(response.headers()["content-disposition"], path).toMatch(/^attachment;/);
    expect(response.headers()["x-content-type-options"], path).toBe("nosniff");
  }
});
