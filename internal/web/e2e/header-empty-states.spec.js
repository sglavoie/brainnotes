import AxeBuilder from "@axe-core/playwright";
import { test, expect } from "./fixture.js";

for (const width of [1280, 390]) {
  test(`incoming badge shares the header and stays reachable at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    for (const path of ["/", "/library", "/n/Reading", "/edit?key=Reading", "/new", "/threads", "/pinned", "/graph", "/settings", "/health", "/history", "/properties", "/properties/status", "/sync", "/synthesize?source=Alpha", "/broken"]) {
      await page.goto(path);
      const badge = page.locator("[data-sync-line]");
      await expect(badge).toHaveCount(1);
      await expect(badge).toHaveText("5 incoming");
      await expect(badge).toHaveAccessibleName("Review 5 incoming changes");
      await expect(badge).toHaveAttribute("title", "Review 5 incoming changes");
      const link = badge.getByRole("link", { name: "Review 5 incoming changes" });
      await expect(link).toHaveAttribute("href", "/sync");
      await link.scrollIntoViewIfNeeded();
      await expect(link).toBeInViewport();
      await link.focus();
      await expect(link).toBeFocused();
      const header = page.locator("header.site-header");
      await expect(header.locator("[data-sync-line]")).toHaveCount(1);
      const badgeBox = await badge.boundingBox();
      const headerBox = await header.boundingBox();
      if (path.startsWith("/n/")) {
        const prose = await page.locator(".reading-title").boundingBox();
        expect(headerBox.y + headerBox.height).toBeLessThanOrEqual(prose.y);
      }
      expect(badgeBox.x + badgeBox.width).toBeLessThanOrEqual(width);
      if (width === 1280 && path !== "/broken") {
        const titleBox = await header.locator("h1, :scope > .reading-home").first().boundingBox();
        expect(badgeBox.y).toBeLessThan(titleBox.y + titleBox.height);
        expect(badgeBox.y + badgeBox.height).toBeGreaterThan(titleBox.y);
        expect(headerBox.height).toBeLessThan(65);
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    }
  });
}

test("empty badge occupies no header space", async ({ page }) => {
  await page.route("**/api/sync/status", async (route) => {
    const response = await route.fetch();
    const status = await response.json();
    await route.fulfill({ json: { ...status, needs_person: false } });
  });
  const response = page.waitForResponse("**/api/sync/status");
  await page.goto("/library");
  await response;
  await expect(page.locator("[data-sync-line]")).toBeEmpty();
  await expect(page.locator("[data-sync-line]")).toBeHidden();
});

test("first thread offers one keyboard action before its creation form", async ({ page }) => {
  await page.goto("/threads");
  const empty = page.locator(".workspace-empty");
  await expect(empty.locator("p").first()).toHaveText("Give a question a place to grow as you read.");
  await expect(empty.locator(".bn-primary:visible")).toHaveCount(1);
  await expect(page.getByLabel("Thread title", { exact: true })).toBeHidden();
  const action = empty.locator("summary");
  await action.focus();
  await action.press("Enter");
  await expect(page.getByLabel("Thread title", { exact: true })).toBeVisible();
  await action.press("Space");
  await expect(page.getByLabel("Thread title", { exact: true })).toBeHidden();
  const audit = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(audit.violations).toEqual([]);
});

test("empty Library groups reset, constraint recovery and creation", async ({ page }) => {
  await page.goto("/library?q=absent&tag=absent&expr=status:absent");
  const help = page.locator("[data-overview-empty-help]");
  await expect(help.locator(".bn-primary")).toHaveText("Reset search");
  await expect(help.getByRole("link")).toHaveCount(5);
  await expect(page.getByRole("link", { name: "Reset search", exact: true })).toBeVisible();
  await expect(page.locator("[data-overview-search-status]")).toHaveText('No notes match "absent"; try changing your search text, filters, and expression.');
  await help.getByRole("link", { name: "Reset search", exact: true }).click();
  await expect(page).toHaveURL(/\/library$/);
  await expect(help).toBeHidden();
});
