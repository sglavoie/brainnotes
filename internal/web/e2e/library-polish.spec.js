import { expect, test } from "./fixture.js";

test.describe("navigation before scripts load", () => {
  test.use({ javaScriptEnabled: false });

  test("the current navigation page is marked in the served HTML", async ({ page }) => {
    await page.goto("/library");
    const current = page.locator('.site-nav [aria-current="page"]');
    await expect(current).toHaveText("Library");
    await page.locator('.site-nav a[href="/pinned"]').click();
    await expect(current).toHaveText("Pinned");
    await page.locator('.site-nav a[href="/"]').click();
    await expect(current).toHaveText("Brief");
  });
});

test("Library selection controls work with the shipped bundle", async ({ page }) => {
  await page.goto("/library?q=Beta");
  const checkboxes = page.locator(".overview-result-select input");
  await expect(checkboxes.first()).toBeVisible();
  const count = await checkboxes.count();
  const select = page.getByRole("button", { name: "Select displayed", exact: true });
  const clear = page.getByRole("button", { name: "Clear selection", exact: true });
  await select.click();
  await expect(page.locator(".overview-result-select input:checked")).toHaveCount(count);
  await expect(select).toBeDisabled();
  await expect(page.locator("[data-overview-bulk]")).toBeVisible();
  await clear.click();
  await expect(page.locator(".overview-result-select input:checked")).toHaveCount(0);
  await expect(page.locator("[data-overview-bulk]")).toBeHidden();
  await expect(select).toBeEnabled();
});

test("pin errors and retry remain visible on a narrow screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route("**/api/pins/set", (route) => route.fulfill({
    status: 409,
    json: { ok: false, message: "This note changed. Try again." },
  }));
  await page.goto("/library?q=Beta");
  const row = page.locator(".overview-result-item").first();
  const pin = row.locator("[data-pin-button]");
  await pin.click();
  await expect(row.getByRole("status")).toHaveText("This note changed. Try again.");
  await expect(pin).toHaveAttribute("aria-pressed", "false");
  const retry = row.getByRole("button", { name: /Retry pin action/ });
  await expect(retry).toBeVisible();
  const response = page.waitForResponse("**/api/pins/set");
  await retry.click();
  expect((await response).status()).toBe(409);
  await expect(retry).toBeEnabled();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
