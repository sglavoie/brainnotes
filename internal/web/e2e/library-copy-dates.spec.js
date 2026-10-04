import { expect, test } from "./fixture.js";

test("Library shows the sort date and copies selected wikilinks on mobile", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/library?q=Reading&sort=-modified");
  await expect(page.locator(".overview-result-meta time")).toHaveText("Modified 2026-10-03");
  await page.getByRole("button", { name: "Select displayed", exact: true }).click();
  await page.getByRole("button", { name: "Copy wikilinks", exact: true }).click();
  const fallback = page.locator("[data-overview-selection] [data-copy-fallback]");
  await expect(fallback).toHaveValue("[[Reading]]");
  await expect(fallback).toBeFocused();
  expect(await fallback.evaluate((field) => field.selectionEnd - field.selectionStart)).toBe(11);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole("button", { name: "Clear selection", exact: true }).click();
  await expect(fallback).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Copy wikilinks", exact: true })).toBeDisabled();
});
