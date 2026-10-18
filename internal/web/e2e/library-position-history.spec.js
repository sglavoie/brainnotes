import { expect, test } from "./fixture.js";

test("browser Back restores the opened Library result on mobile", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/library?status=draft&sort=title");
  const result = page.locator(".overview-result-link", { hasText: "Reading" });
  await result.click();
  await expect(page.locator("[data-note-body]")).toBeVisible();
  await page.goBack();
  await expect(result).toBeFocused();
  await expect(result).toBeInViewport();
  await expect(page).toHaveURL(/#library-result-reading$/);
});

test("Library history exposes affected paths with an accessible mobile expander", async ({ page }) => {
  const paths = ["Folder/Old.md", "Folder/New.md", "Backlink.md", "Deep/" + "Long".repeat(35) + ".md"];
  await page.route(/\/api\/history(?:\?|$)/, (route) => route.fulfill({ json: {
    ok: true,
    entries: [{
      id: "fixture-history", command: "bn web move", timestamp: "2026-10-03T12:00:00Z",
      file_count: paths.length,
      files: paths.map((path) => ({ path, before: "file", after: "file" })),
    }],
  } }));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/library");
  const entry = page.locator("[data-history-list] .history-item");
  await expect(entry.locator(".history-item-main > .history-paths li > span")).toHaveText(paths.slice(0, 3));
  const summary = entry.locator(".history-more-paths > summary");
  await expect(summary).toHaveText("1 more path");
  await expect(entry.locator("details li")).toBeHidden();
  await summary.focus();
  await page.keyboard.press("Enter");
  await expect(entry.locator("details li")).toBeVisible();
  await expect(entry.locator("details li > span")).toHaveText(paths[3]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await entry.locator("details li a").click();
  await expect(page.locator("[data-history-path]")).toHaveValue(paths[3]);
  await expect(page.locator("[data-history-path-match]")).toHaveValue("exact");
  await page.goBack();
  await expect(page).toHaveURL(/\/library$/);
});

test("Related Notes retry works with the keyboard and returns focus after recovery", async ({ page }) => {
  let attempts = 0;
  await page.route(/\/api\/related\?/, (route) => {
    attempts += 1;
    return attempts === 1
      ? route.fulfill({ status: 503, json: { message: "Temporarily unavailable" } })
      : route.continue();
  });
  await page.goto("/n/Beta");
  const retry = page.getByRole("button", { name: "Retry related notes" });
  await expect(retry).toBeVisible();
  await expect(page.locator("[data-related-status]")).toHaveText("Temporarily unavailable");
  await retry.focus();
  await page.keyboard.press("Enter");
  await expect(retry).toBeHidden();
  await expect(page.locator("[data-related-status]")).toBeFocused();
  await expect(page.locator("[data-related-status]")).not.toHaveClass(/is-error|is-loading/);
  expect(attempts).toBe(2);
});
