import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, gitStatus, manifest, test } from "./fixture.js";

test("arrow-key search navigation scrolls the popup without moving the page", async ({ page }) => {
  await page.route("**/api/search?*", (route) => route.fulfill({ json: Array.from({ length: 15 }, (_, i) => ({
    key: `note-${i}`, path: `/n/note-${i}`, title: `Note ${i}`, folder: "Notes", snippet: "A matching passage.",
  })) }));
  await page.goto("/n/Notes/Gamma");
  await page.keyboard.press("/");
  const input = page.locator("#search-input");
  await input.fill("note");
  await expect(page.locator("[data-search-index]")).toHaveCount(15);
  const pageTop = await page.evaluate(() => window.scrollY);
  // ArrowUp wraps from no selection to the last result, initially below the popup.
  await input.press("ArrowUp");
  const popup = page.locator("#search-results");
  await expect.poll(() => popup.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
  const selected = await page.locator('[data-search-index="14"]').boundingBox();
  const bounds = await popup.boundingBox();
  expect(selected.y).toBeGreaterThanOrEqual(bounds.y);
  expect(selected.y + selected.height).toBeLessThanOrEqual(bounds.y + bounds.height);
  expect(await page.evaluate(() => window.scrollY)).toBe(pageTop);
  await expect(input).toBeFocused();
  await input.press("ArrowDown");
  const first = await page.locator('[data-search-index="0"]').boundingBox();
  expect(first.y).toBeGreaterThanOrEqual(bounds.y);
  expect(first.y + first.height).toBeLessThanOrEqual(bounds.y + bounds.height);
});

for (const mode of ["unavailable", "refused"]) {
  test(`copy offers selected text when the clipboard is ${mode}`, async ({ page }) => {
    await page.addInitScript((mode) => {
      Object.defineProperty(navigator, "clipboard", { configurable: true, value: mode === "unavailable"
        ? undefined : { writeText: () => Promise.reject(new Error("denied")) } });
    }, mode);
    await page.goto("/n/Notes/Gamma");
    await page.locator(".note-actions-more > summary").click();
    const button = page.getByRole("button", { name: "Copy wikilink", exact: true });
    const expected = await button.getAttribute("data-copy-text");
    await button.click();
    const fallback = page.locator("[data-copy-fallback]");
    await expect(fallback).toBeVisible();
    await expect(fallback).toBeFocused();
    await expect(fallback).toHaveValue(expected);
    expect(await fallback.evaluate((el) => el.value.slice(el.selectionStart, el.selectionEnd))).toBe(expected);
    await page.getByRole("button", { name: "Copy vault path", exact: true }).click();
    await expect(fallback).toHaveCount(1);
    await expect(fallback).toHaveValue("Notes/Gamma.md");
  });
}

test("trash offers undo after navigation and restores the original file", async ({ page }) => {
  const file = join(manifest.localVault, "Notes/Gamma.md");
  const original = readFileSync(file, "utf8");
  const peerBefore = gitStatus(manifest.peerVault);
  const peerBytes = readFileSync(join(manifest.peerVault, "Notes/Gamma.md"), "utf8");
  await page.goto("/n/Notes/Gamma");
  await expect(page.getByRole("link", { name: "Edit in browser", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open in external editor", exact: true })).toBeHidden();
  await expect(page.locator("[data-trash-button]")).toBeHidden();
  await page.getByText("Actions", { exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("link", { name: "Synthesize from this note" })).toBeVisible();
  await page.locator("[data-trash-button]").click();
  await page.getByRole("dialog").getByRole("button", { name: "Move to Trash", exact: true }).click();
  await expect(page).toHaveURL(`${manifest.baseURL}/`);
  expect(existsSync(file)).toBe(false);
  const toast = page.locator("[data-trash-undo-toast]");
  await expect(toast).toContainText("Moved note to Trash.");
  await page.waitForLoadState("networkidle");
  await toast.getByRole("button", { name: "Undo", exact: true }).click();
  await expect(page).toHaveURL(/\/n\/Notes\/Gamma$/);
  expect(readFileSync(file, "utf8")).toBe(original);
  expect(gitStatus(manifest.localVault)).toBe("");
  expect(gitStatus(manifest.peerVault)).toBe(peerBefore);
  expect(readFileSync(join(manifest.peerVault, "Notes/Gamma.md"), "utf8")).toBe(peerBytes);
  await page.goto("/");
  await page.waitForLoadState("networkidle");
  await expect(page.locator("[data-trash-undo-toast]")).toHaveCount(0);
});
