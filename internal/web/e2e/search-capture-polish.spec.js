import { expect, test } from "./fixture.js";

test("header search can retry a failed request from the keyboard", async ({ page }) => {
  let attempts = 0;
  await page.route("**/api/search?*", (route) => {
    attempts += 1;
    return attempts === 1 ? route.fulfill({ status: 503, body: "Unavailable" }) : route.continue();
  });
  await page.goto("/n/Reading");
  await page.keyboard.press("/");
  const input = page.locator("#search-input");
  await input.fill("Alpha");
  const retry = page.getByRole("button", { name: "Retry search", exact: true });
  await expect(retry).toBeVisible();
  await retry.focus();
  await retry.press("Enter");
  await expect(page.locator("#search-results")).toBeVisible();
  await expect(page.locator(".search-result-title").first()).toHaveText("Alpha");
  await expect(retry).toBeHidden();
  await expect(input).toBeFocused();
  expect(attempts).toBe(2);
});

test("capture count follows restored drafts and native emoji length enforcement", async ({ page }) => {
  await page.goto("/");
  const input = page.locator("[data-capture-input]");
  const count = page.locator("[data-capture-count]");
  await expect(input).toHaveAttribute("aria-describedby", "capture-hint capture-count");
  await expect(count).toHaveText("0 / 2000 characters");
  await input.fill("a".repeat(1998));
  await input.press("End");
  await page.keyboard.insertText("😀");
  await expect(count).toHaveText("2000 / 2000 characters");
  await input.press("x");
  await expect(input).toHaveValue("a".repeat(1998) + "😀");
  await page.reload();
  await expect(count).toHaveText("2000 / 2000 characters");
  await input.fill("");
  await expect(count).toHaveText("0 / 2000 characters");
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(count).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("capture completes body-matched wikilinks and keeps the completed draft on reload", async ({ page }) => {
  await page.goto("/");
  const input = page.locator("[data-capture-input]");
  await input.fill("Remember [[Cartographer");
  const suggestion = page.getByRole("option", { name: "Notes/Fieldwork", exact: true });
  await expect(suggestion).toBeVisible();
  await input.press("Enter");
  await expect(input).toHaveValue("Remember [[Notes/Fieldwork]]");
  await expect(page.locator("[data-capture-count]")).toHaveText("28 / 2000 characters");
  await page.reload();
  await expect(input).toHaveValue("Remember [[Notes/Fieldwork]]");
  await input.fill("[[Cartographer");
  await expect(suggestion).toBeVisible();
  await suggestion.click();
  await expect(input).toHaveValue("[[Notes/Fieldwork]]");
  await expect(input).toBeFocused();
  await expect(suggestion).toBeHidden();
});
