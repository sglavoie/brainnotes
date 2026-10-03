import { expect, test } from "./fixture.js";

test.use({ hasTouch: true });

test("header search hands its query to the Library with Enter or a link", async ({ page }) => {
  await page.goto("/n/Alpha");
  const search = page.locator("#search-input");
  await search.fill("Beta");
  const library = page.locator("[data-search-library]");
  await expect(library).toHaveAttribute("href", "/library?q=Beta");
  await search.press("Enter");
  await expect(page).toHaveURL(/\/library\?q=Beta$/);
  await expect(search).toHaveValue("Beta");
  await expect(page.locator("[data-overview-search-results]")).toContainText("Beta");

  await page.goto("/n/Alpha");
  await search.fill("Alpha");
  await search.press("Tab");
  await expect(library).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/library\?q=Alpha$/);
});

test("save form shows its expression and warns about excluded filters before saving", async ({ page }) => {
  await page.goto("/library?expr=tag:focus&tag=work&q=Beta&sort=-modified&limit=10");
  await page.locator("[data-saved-views-open]").click();
  await expect(page.locator("[data-saved-views-scope]")).toHaveText(
    "This view will save:\nQuery: tag:focus\nSort: -modified\nLimit: 10"
  );
  await expect(page.locator("[data-saved-views-excluded]")).toContainText("Text search: Beta; tag: work");
  await page.locator("#search-input").fill("Alpha");
  await expect(page.locator("[data-saved-views-excluded]")).toContainText("Text search: Alpha");
});

test("unavailable saved views disclose their error with keyboard and touch", async ({ page }) => {
  const error = 'unknown field "statuz"\n  statuz:wip\n  ^\ndid you mean: status';
  await page.route("**/api/views", (route) => route.fulfill({
    json: { ok: true, views: [{ name: "Needs repair", enabled: false, detail: error }] },
  }));
  await page.goto("/library");
  const summary = page.locator('summary[data-saved-view="Needs repair"]');
  const detail = page.locator(".saved-view-error-detail");
  await expect(detail).toBeHidden();
  await summary.focus();
  await summary.press("Enter");
  await expect(detail).toBeVisible();
  await expect(detail).toHaveText(error);
  await summary.press("Space");
  await expect(detail).toBeHidden();
  await page.setViewportSize({ width: 390, height: 844 });
  await summary.tap();
  await expect(detail).toBeVisible();
  await expect(page).toHaveURL(/\/library$/);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
