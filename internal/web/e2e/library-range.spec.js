import { expect, test } from "./fixture.js";

test("Shift-click selects a displayed range and clears it through real checkbox events", async ({ page }) => {
  await page.goto("/library?q=body");
  const checkboxes = page.locator(".overview-result-select input");
  await expect(checkboxes.nth(2)).toBeVisible();
  await checkboxes.first().click();
  // Click the label to include the browser's forwarding to its checkbox.
  await page.locator(".overview-result-select").nth(2).click({ modifiers: ["Shift"] });
  await expect(page.locator(".overview-result-select input:checked")).toHaveCount(3);
  await expect(page.locator("[data-overview-selected-count]")).toHaveText("3 selected");
  await checkboxes.first().click({ modifiers: ["Shift"] });
  await expect(page.locator(".overview-result-select input:checked")).toHaveCount(0);
  await expect(page.locator("[data-overview-copy-wikilinks]")).toBeHidden();
});
