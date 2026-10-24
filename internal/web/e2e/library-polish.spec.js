import { expect, test } from "./fixture.js";

test("empty-result links loosen one constraint with keyboard navigation on mobile", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/library?q=Reading&tag=absent&expr=tag:absent&sort=title&limit=2");
  const help = page.locator("[data-overview-empty-help]");
  await expect(help).toContainText("Searching with: text, filters, expression.");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const removeFilters = help.getByRole("link", { name: "Remove filters", exact: true });
  await removeFilters.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("#search-input")).toHaveValue("Reading");
  await expect(page.locator("[data-query-bar-input]")).toHaveValue("tag:absent");
  await expect(page.locator("[data-facet-chips]")).toBeHidden();
  await expect(help).toContainText("Searching with: text, expression.");
  await expect(help.getByRole("link", { name: "Remove filters", exact: true })).toHaveCount(0);
  await help.getByRole("link", { name: "Remove expression", exact: true }).click();
  await expect(help).toBeHidden();
  await expect(page.locator(".overview-result-title")).toHaveText(["Reading"]);
  const params = new URL(page.url()).searchParams;
  expect(Object.fromEntries(params)).toEqual({ q: "Reading", sort: "title", limit: "2" });
  await expect(page.getByRole("combobox", { name: "Sort results" })).toHaveValue("title");
});

test("Library retries a failed search without changing its query settings", async ({ page }) => {
  let fail = true;
  const requests = [];
  await page.route("**/api/search?**", (route) => {
    requests.push(new URL(route.request().url()).search);
    return fail ? route.fulfill({ status: 503, body: "Unavailable" }) : route.continue();
  });
  const path = "/library?q=Beta&folder=Notes&sort=-modified&limit=2";
  await page.goto(path);
  const retry = page.getByRole("button", { name: "Retry search", exact: true });
  await expect(retry).toBeVisible();
  await expect(page.locator("[data-overview-search-status]")).toHaveText("Couldn't load matching notes. Check your connection, then select Retry search.");
  fail = false;
  await retry.click();
  await expect(retry).toBeHidden();
  await expect(page.locator("[data-overview-search-results]")).toHaveAttribute("aria-busy", "false");
  expect(requests.length).toBeGreaterThanOrEqual(2);
  expect(new Set(requests).size).toBe(1);
  await expect(page).toHaveURL(new RegExp(`${path.replaceAll("?", "\\?")}$`));
});

test("Reset search clears query settings, pending expression text, and selection", async ({ page }) => {
  await page.goto("/library?q=Beta&sort=relevance&limit=2&relative_to=alpha");
  await page.getByRole("button", { name: "Select displayed", exact: true }).click();
  await expect(page.locator("[data-overview-bulk]")).toBeVisible();
  await page.locator("[data-library-expression] > summary").click();
  await page.locator("[data-query-bar-input]").fill("tag:");
  await page.getByRole("link", { name: "Reset search", exact: true }).click();
  await expect(page).toHaveURL(/\/library$/);
  await expect(page.locator("#search-input")).toHaveValue("");
  await expect(page.locator("[data-query-bar-input]")).toHaveValue("");
  await expect(page.locator("[data-overview-search-status]")).toHaveText("Search by title or text, or choose a filter. Recently modified notes are below.");
  await expect(page.locator("[data-overview-bulk]")).toBeHidden();
  await expect(page.locator("[data-overview-search-list]")).toBeHidden();

  await page.goto("/library?q=missing&tag=work&category=Books&expr=tag:focus");
  await page.getByRole("link", { name: "Reset search", exact: true }).click();
  await expect(page).toHaveURL(/\/library$/);
  await expect(page.locator("[data-facet-chips]")).toBeHidden();
  await expect(page.locator("[data-query-bar-input]")).toHaveValue("");
});

test.describe("navigation before scripts load", () => {
  test.use({ javaScriptEnabled: false });

  test("the current navigation page is marked in the served HTML", async ({ page }) => {
    await page.goto("/library");
    const current = page.locator('.site-nav [aria-current="page"]');
    await expect(current).toHaveText("Library");
    await page.locator(".source-editor-workspace > summary").click();
    await page.locator('.site-nav a[href="/pinned"]').click();
    await expect(current).toHaveText("Pinned");
    await page.locator(".source-editor-workspace > summary").click();
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

test("an unconfirmed pin offers refresh without repeating the write", async ({ page }) => {
  let writes = 0;
  await page.route("**/api/pins/set", route => {
    writes += 1;
    return route.fulfill({ status: 200, contentType: "text/html", body: "Unexpected response" });
  });
  await page.goto("/library?q=Reading");
  const row = page.locator("[data-overview-search-list] li").filter({ hasText: "Reading" }).first();
  const pin = row.locator("[data-pin-button]");
  await pin.click();
  await expect(pin).toHaveAttribute("aria-pressed", "false");
  await expect(row).toContainText("not confirmed");
  await row.getByRole("link", { name: "Refresh to check" }).click();
  await expect(pin).toBeVisible();
  await expect(pin).toHaveAttribute("aria-pressed", "false");
  expect(writes).toBe(1);
});
