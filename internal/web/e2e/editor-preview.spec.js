import { readFileSync } from "node:fs";
import { join } from "node:path";

import { expect, gitStatus, manifest, test } from "./fixture.js";
import { caretToEnd, openEditor, phraseFor } from "./editor-helpers.js";

const NOTE = "Alpha.md";

// The responsive layout, which is the one part of this page that is entirely
// CSS. jsdom parses the stylesheet and applies nothing, so "below 960px the two
// columns become two tabs" can only be asserted where something lays out.
const PANE_EDIT = '[data-source-pane="edit"]';
const PANE_PREVIEW = '[data-source-pane="preview"]';

test("both panes are shown side by side on a wide window", async ({ page }) => {
  await openEditor(page);

  await expect(page.locator(PANE_EDIT)).toBeVisible();
  await expect(page.locator(PANE_PREVIEW)).toBeVisible();
  // Tabs that chose between two panes both already on screen would be a control
  // with nothing to do.
  await expect(page.locator('[data-source-tab="edit"]')).toBeHidden();
});

test.describe("on a narrow window", () => {
  test.use({ viewport: { width: 700, height: 900 } });

  test("the two panes become two tabs", async ({ page }) => {
    await openEditor(page);

    const editTab = page.locator('[data-source-tab="edit"]');
    const previewTab = page.locator('[data-source-tab="preview"]');
    await expect(editTab).toBeVisible();
    await expect(page.locator(PANE_EDIT)).toBeVisible();
    await expect(page.locator(PANE_PREVIEW)).toBeHidden();

    await previewTab.click();
    await expect(page.locator(PANE_PREVIEW)).toBeVisible();
    await expect(page.locator(PANE_EDIT)).toBeHidden();
    // Choosing the pane flushes a render, so what arrives is the server's own
    // HTML for the note now in the editor.
    await expect(page.locator("[data-source-preview]")).toContainText("Alpha body");

    await editTab.click();
    await expect(page.locator(PANE_EDIT)).toBeVisible();
    await expect(page.locator(PANE_PREVIEW)).toBeHidden();

    expect(gitStatus(manifest.localVault), "switching panes wrote to the vault").toBe("");
  });
});

test("Retry preview recovers the current draft and returns keyboard focus", async ({ page }) => {
  await openEditor(page);
  const preview = page.locator("[data-source-preview]");
  await expect(preview).toContainText("Alpha body");
  await page.route("**/api/source/preview", route => route.fulfill({ status: 503, body: "Offline" }));
  const phrase = phraseFor("preview retry");
  await caretToEnd(page);
  await page.keyboard.insertText(`\n\n${phrase}\n`);
  const retry = page.getByRole("button", { name: "Retry preview", exact: true });
  await expect(retry).toBeVisible();
  await expect(page.locator("[data-source-preview-status]")).toContainText("previous preview is outdated");
  await page.unroute("**/api/source/preview");
  await retry.focus();
  await page.keyboard.press("Enter");
  await expect(retry).toBeHidden();
  await expect(preview).toContainText(phrase);
  await expect(page.locator('[data-source-pane="preview"]')).toBeFocused();
  await expect(page.locator("[data-source-save]")).toBeEnabled();
  expect(readFileSync(join(manifest.localVault, NOTE), "utf8")).not.toContain(phrase);
});
