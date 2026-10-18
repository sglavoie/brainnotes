import { test, expect } from "./fixture.js";

for (const width of [390, 1280]) {
  for (const theme of ["light", "dark"]) {
    test(`Library hierarchy and controls fit ${width}px in ${theme}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript((value) => localStorage.setItem("bn.theme", value), theme);
      await page.goto("/library?q=Reading&status=draft&sort=title&limit=10");
      const search = page.locator("[data-library-search]");
      await expect(search.locator("#search-input")).toHaveValue("Reading");
      await expect(search.locator("[data-library-expression]")).not.toHaveAttribute("open");
      await expect(search.getByRole("link", { name: "Remove text: Reading" })).toBeVisible();
      await expect(search.locator("[data-facet-chips]")).toContainText("draft");
      await expect(page.locator("[data-overview-search-status]")).toContainText("1");
      await expect(page.getByRole("navigation", { name: "Primary", exact: true }).getByRole("link", { name: "Settings" })).toBeHidden();
      await page.locator(".source-editor-workspace > summary").click();
      await page.locator(".manage-nav > summary").click();
      await expect(page.getByRole("link", { name: "Settings", exact: true })).toBeVisible();
      await page.locator(".manage-nav > summary").click();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      const heights = await page.locator("[data-query-bar-clear], [data-saved-views-open]").evaluateAll((buttons) =>
        buttons.map((button) => parseFloat(getComputedStyle(button).minHeight)));
      expect(heights.every((height) => height >= 33)).toBe(true);
      const screenshot = testInfo.outputPath(`library-${width}-${theme}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`library-${width}-${theme}`, { path: screenshot, contentType: "image/png" });
    });
  }
}

test("Library combines constraints and removal preserves the remaining query", async ({ page }) => {
  await page.goto("/library?q=Reading&status=draft&expr=status:draft&sort=title&limit=10");
  const search = page.locator("[data-library-search]");
  await expect(search.locator("[data-library-expression]")).toHaveAttribute("open");
  const remove = search.getByRole("link", { name: "Remove expression: status:draft" });
  await expect(remove).toBeVisible();
  await remove.click();
  const url = new URL(page.url());
  expect(Object.fromEntries(url.searchParams)).toEqual({ q: "Reading", status: "draft", sort: "title", limit: "10" });
  await expect(search.locator("[data-library-expression]")).not.toHaveAttribute("open");
  await search.locator("[data-library-expression] > summary").click();
  await expect(page.locator("#query-bar-input")).toBeVisible();
  await page.locator("#query-bar-input").fill("status:draft");
  await expect(page).toHaveURL(/expr=status%3Adraft/);
  await expect(search.getByRole("link", { name: "Remove expression: status:draft" })).toBeVisible();
});

test("every workspace has a native keyboard skip target", async ({ page }) => {
  for (const path of ["/", "/library", "/pinned", "/health", "/properties", "/settings", "/history", "/sync", "/graph"]) {
    await page.goto(path);
    await page.keyboard.press("Tab");
    await expect(page.getByRole("link", { name: "Skip to content" })).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(page.locator("#workspace-content")).toBeFocused();
    await expect(page.locator("#workspace-content")).toBeVisible();
  }
});

test.describe("without JavaScript", () => {
  test.use({ javaScriptEnabled: false });
  test("native navigation and skip links work", async ({ page }) => {
    await page.goto("/library");
    await page.keyboard.press("Tab");
    await page.keyboard.press("Enter");
    await expect(page.locator("#workspace-content")).toBeFocused();
    await page.locator(".source-editor-workspace > summary").click();
    await page.locator(".manage-nav > summary").click();
    await page.getByRole("link", { name: "Settings", exact: true }).click();
    await expect(page.locator(".manage-nav")).toHaveAttribute("open");
    await expect(page.getByRole("link", { name: "Settings", exact: true })).toHaveAttribute("aria-current", "page");
  });
});
