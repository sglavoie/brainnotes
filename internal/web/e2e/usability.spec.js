import { expect, test } from "./fixture.js";

test("search shortcut, native new-tab links, and keyboard navigation", async ({ page, context }) => {
  await page.goto("/n/Alpha");
  await page.keyboard.press("/");
  const search = page.locator("#search-input");
  await expect(search).toBeFocused();
  await search.fill("Beta");
  const result = page.locator("#search-results a").first();
  await expect(result).toBeVisible();
  const popupPromise = context.waitForEvent("page");
  await result.click({ button: "middle" });
  const popup = await popupPromise;
  await expect(popup).toHaveURL(/\/n\/beta$/);
  await expect(popup.locator("h1")).toHaveText("Beta");
  await popup.close();
  await expect(page).toHaveURL(/\/n\/Alpha$/);
  // Reopening the dropdown under a stationary pointer can hover-select a row
  // before ArrowDown; this part of the journey exercises keyboard selection.
  await page.mouse.move(0, 0);
  await search.focus();
  await expect(result).toBeVisible();
  await search.press("ArrowDown");
  await search.press("Enter");
  await expect(page).toHaveURL(/\/n\/beta$/);
});

test("capture draft survives reload and navigation without writing a note", async ({ page }) => {
  const captures = [];
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/api/capture") { captures.push(request.url()); }
  });
  await page.goto("/");
  const capture = page.locator("[data-capture-input]");
  await capture.fill("Keep this unfinished thought");
  await capture.press("/");
  await expect(capture).toBeFocused();
  await expect(capture).toHaveValue("Keep this unfinished thought/");
  await page.reload();
  await expect(capture).toHaveValue("Keep this unfinished thought/");
  await page.goto("/library");
  await page.goto("/");
  await expect(capture).toHaveValue("Keep this unfinished thought/");
  await capture.fill("");
  await page.reload();
  await expect(capture).toHaveValue("");
  expect(captures).toEqual([]);
  await page.locator(".capture-destination a").click();
  await expect(page).toHaveURL(/\/n\/inbox$/);
});
