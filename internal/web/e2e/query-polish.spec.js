import { expect, test } from "./fixture.js";

test.use({ hasTouch: true });

test("saved-view retry works by keyboard without losing focus on mobile", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  let fail = true;
  await page.route("**/api/views", (route) => fail
    ? route.fulfill({ status: 503, json: { ok: false } })
    : route.fulfill({ json: { ok: true, views: [
      { name: "Drafts", enabled: true, query: "status:draft", sort: "title" },
    ] } }));
  await page.goto("/library?expr=status:draft&sort=title");
  const retry = page.getByRole("button", { name: "Retry saved views", exact: true });
  await expect(retry).toBeVisible();
  await expect(page.locator(".saved-views-feedback")).toContainText("Saved views unavailable");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  fail = false;
  await retry.focus();
  await page.keyboard.press("Enter");
  const view = page.locator('a[data-saved-view="Drafts"]');
  await expect(view).toHaveAttribute("aria-current", "true");
  await expect(view).toBeFocused();
  await expect(retry).toHaveCount(0);
});

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
  const name = page.locator("[data-saved-views-name]");
  await name.focus();
  await name.press("Escape");
  await expect(page.locator("[data-saved-views-form]")).toBeHidden();
  await expect(page.locator("[data-saved-views-open]")).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(name).toBeFocused();
  await page.locator("[data-saved-views-cancel]").click();
  await expect(page.locator("[data-saved-views-open]")).toBeFocused();
});

test("saved view highlighting follows live search and sorting", async ({ page }) => {
  await page.route("**/api/views", (route) => route.fulfill({
    json: { ok: true, views: [{ name: "Drafts", enabled: true, query: "status:draft", sort: "title" }] },
  }));
  await page.goto("/library?expr=status:draft&sort=title");
  const view = page.locator('a[data-saved-view="Drafts"]');
  await expect(view).toHaveAttribute("aria-current", "true");
  await page.locator("#search-input").fill("Alpha");
  await expect(view).not.toHaveAttribute("aria-current");
  await page.locator("#search-input").fill("");
  await expect(view).toHaveAttribute("aria-current", "true");
  await page.locator("#search-input").press("Escape");
  await page.getByRole("combobox", { name: "Sort results" }).focus();
  await page.getByRole("combobox", { name: "Sort results" }).selectOption("-title");
  await expect(view).not.toHaveAttribute("aria-current");
  await view.click();
  await expect(view).toHaveAttribute("aria-current", "true");
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
