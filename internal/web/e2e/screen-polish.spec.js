import { test, expect } from "./fixture.js";

test("mobile Properties starts with usable filtering and actual properties", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/properties");
  const first = page.locator(".properties-item").first();
  await expect(first).toBeInViewport();
  const input = page.locator("[data-properties-query]");
  const box = await input.boundingBox();
  expect(box.height).toBeGreaterThanOrEqual(44);
  expect(box.height).toBeLessThan(60);
  await expect(page.locator("[data-properties-totals]")).toBeHidden();
  const summary = page.locator(".properties-counts-details > summary");
  await summary.focus();
  await summary.press("Enter");
  await expect(page.locator("[data-properties-totals]")).toBeVisible();
  await summary.press("Enter");
  await input.fill("status");
  await expect(page.locator(".properties-item-name")).toHaveText(["status"]);
});

test("reading puts prose first and keeps secondary actions keyboard accessible", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/n/Reading");
  const prose = page.locator("[data-note-body] p").first();
  await expect(prose).toBeInViewport();
  expect((await prose.boundingBox()).y).toBeLessThan(380);
  const actions = page.locator(".note-actions-more > summary");
  await expect(page.locator("[data-note-history]")).toBeHidden();
  await actions.focus();
  await actions.press("Enter");
  await expect(page.locator("[data-note-history]")).toBeVisible();
  await expect(page.locator("[data-copy-note-url]")).toBeVisible();
  await expect(page.locator("[data-trash-button]")).toBeVisible();
});

test("Health loads real tasks and recovers a failed read with one error", async ({ page }) => {
  await page.goto("/health");
  await expect(page.locator("[data-fixes-panel]")).toHaveAttribute("aria-busy", "false");
  await expect(page.locator("[data-fixes-error]")).toBeHidden();
  await expect(page.locator("[data-fixes-task]").first()).toBeVisible();
  await expect(page.locator("[data-sync-line]")).toContainText("Sync:");
  const counts = await page.locator("[data-fixes-count-active]").textContent();
  await page.route("**/api/cockpit?view=daily", (route) => route.fulfill({
    status: 503, json: { ok: false, message: "Fixes are temporarily unavailable." },
  }));
  await page.getByRole("button", { name: "Reload fixes" }).click();
  await expect(page.getByText("Fixes are temporarily unavailable.", { exact: true })).toHaveCount(1);
  await expect(page.locator("[data-fixes-stale]")).toBeVisible();
  await expect(page.locator("[data-fixes-count-active]")).toHaveText(counts);
  await page.unroute("**/api/cockpit?view=daily");
  await page.getByRole("button", { name: "Reload fixes" }).click();
  await expect(page.locator("[data-fixes-status]")).toHaveText("Fixes refreshed.");
  await expect(page.locator("[data-fixes-error]")).toBeHidden();
  await expect(page.locator("[data-fixes-stale]")).toBeHidden();
});

test("settings group links preserve drafts and size preview uses exact bytes", async ({ page }) => {
  await page.goto("/settings");
  const row = page.locator('[data-settings-row="attachments.max_file_bytes"]');
  await row.locator("[data-settings-edit]").click();
  const control = row.getByRole("spinbutton", { name: "Maximum upload size (MiB)" });
  await expect(control).toHaveValue("25");
  await control.fill("10.5");
  await page.getByRole("navigation", { name: "Settings groups" }).getByRole("link", { name: "Daily Fixes" }).click();
  await expect(page.locator("#settings-group-cockpit")).toBeInViewport();
  await page.getByRole("navigation", { name: "Settings groups" }).getByRole("link", { name: "Attachments", exact: true }).click();
  await expect(control).toHaveValue("10.5");
  await row.locator("[data-settings-save]").click();
  await expect(row.locator("[data-settings-diff]")).toContainText("11010048");
  await expect(row.locator("[data-settings-confirm]")).toBeFocused();
  await row.locator("[data-settings-cancel]").click();
  await control.fill("0.1");
  await row.locator("[data-settings-save]").click();
  await expect(row.locator("[data-settings-status]")).toContainText("No rounding was applied");
  await expect(row.locator("[data-settings-confirm]")).toBeHidden();
  await control.fill("10.5");
  await row.locator("[data-settings-save]").click();
  await row.locator("[data-settings-confirm]").click();
  await expect(row.locator("[data-settings-current]")).toHaveText("10.5 MiB");
  await page.reload();
  await row.locator("[data-settings-edit]").click();
  await expect(control).toHaveValue("10.5");
  // Restore the fixture's limit for subsequent upload journeys.
  await control.fill("25");
  await row.locator("[data-settings-save]").click();
  await row.locator("[data-settings-confirm]").click();
  await expect(row.locator("[data-settings-current]")).toHaveText("25 MiB");
  await page.goto("/settings#settings-group-cockpit");
  await page.reload();
  await expect(page.locator("#settings-group-cockpit")).toBeFocused();
  await expect(page.locator("#settings-group-cockpit")).toBeInViewport();
});

test("mobile Library gives the title the remaining row width and moves Pin below it", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/library?q=field&status=reference&sort=title");
  const row = page.locator(".overview-result-item").first();
  await expect(row).toBeVisible();
  const title = await row.locator(".overview-result-title").boundingBox();
  const pin = await row.locator(".overview-result-pin").boundingBox();
  expect(title.width).toBeGreaterThan(290);
  expect(pin.y).toBeGreaterThan(title.y + title.height);
  expect(pin.height).toBeGreaterThanOrEqual(44);
  await row.locator('input[type="checkbox"]').check();
  await expect(page.locator("[data-overview-selected-count]")).toContainText("1");
});
