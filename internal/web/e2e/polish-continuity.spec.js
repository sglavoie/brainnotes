import { readFileSync } from "node:fs";
import { expect, test, manifest } from "./fixture.js";
import { openBeta, openManage, vaultPath } from "./editor-journey-helpers.js";
import { SURFACE } from "./editor-helpers.js";

test("Undo confirms one exact change even after another tab writes", async ({ page, context }) => {
  await openBeta(page);
  await openManage(page);
  const before = readFileSync(vaultPath("Beta.md"), "utf8");
  await page.locator("[data-source-property-name]").fill("status");
  await page.locator("[data-source-property-value]").fill("undo-preview-test");
  await page.locator("[data-source-property-set]").click();
  await expect(page.locator(SURFACE)).toContainText("undo-preview-test");
  const undo = page.locator("[data-source-undo]");
  await undo.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("Beta.md");
  await expect(dialog.getByRole("button", { name: "Cancel", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(undo).toBeFocused();
  expect(readFileSync(vaultPath("Beta.md"), "utf8")).toContain("undo-preview-test");
  await undo.click();
  await expect(dialog).toBeVisible();
  const other = await context.newPage();
  await openBeta(other);
  await openManage(other);
  const capture = `after undo preview ${manifest.fixtureRoot.split("/").pop()}`;
  await other.locator("[data-source-capture-text]").fill(capture);
  await other.locator("[data-source-capture]").click();
  await expect(other.locator("[data-source-capture-text]")).toHaveValue("");
  await dialog.getByRole("button", { name: "Undo property change", exact: true }).click();
  await expect.poll(() => readFileSync(vaultPath("Beta.md"), "utf8")).toBe(before);
  expect(readFileSync(vaultPath("Inbox.md"), "utf8")).toContain(capture);
  await other.close();
});

test("reading position and origin survive editing and reload", async ({ page }) => {
  await page.goto("/n/Reading?return_to=%2Fpinned");
  await page.locator("#second-section").evaluate((heading) => window.scrollTo(0, heading.getBoundingClientRect().top + scrollY + 125));
  const before = await page.locator("#second-section").evaluate((heading) => heading.getBoundingClientRect().top);
  // Activate without scrolling the reading position to the top of the page.
  await page.locator("[data-source-editor-launch]").evaluate((link) => link.click());
  await expect(page.locator(SURFACE)).toBeVisible();
  await page.reload();
  await page.locator("[data-source-note-return]").click();
  await expect(page.locator("#second-section")).toBeAttached();
  // Header feedback may change height while editing; the same paragraph must
  // return to the same viewport position, regardless of absolute scrollY.
  await expect.poll(async () => Math.abs(await page.locator("#second-section").evaluate((heading) => heading.getBoundingClientRect().top) - before)).toBeLessThan(8);
  await expect(page.getByRole("link", { name: "Back to Pinned", exact: true })).toHaveAttribute("href", "/pinned");
});

test("Save as view preserves text, facets, expression, ordering and limit", async ({ page }) => {
  const params = "q=body&status=draft&expr=not%20folder%3AArchive&sort=-title&limit=2";
  await page.goto(`/library?${params}`);
  const links = page.locator(".overview-result-link");
  await expect(links).toHaveCount(2);
  const before = await links.allTextContents();
  await page.locator("[data-saved-views-open]").click();
  await expect(page.locator("[data-saved-views-scope]")).toContainText("Text search: body");
  await page.locator("[data-saved-views-name]").fill("Complete browser selection");
  await page.locator("[data-saved-views-form] button[type=submit]").click();
  const saved = page.locator('a[data-saved-view="Complete browser selection"]');
  await expect(saved).toBeVisible();
  await saved.click();
  await expect(links).toHaveCount(2);
  expect(await links.allTextContents()).toEqual(before);
  await expect(page.locator("#search-input")).toHaveValue("body");
  await expect(page.locator('[data-saved-view="Complete browser selection"]')).toHaveAttribute("aria-current", "true");
});
