import { expect, test } from "./fixture.js";
import { SURFACE } from "./editor-helpers.js";

test("Library context survives the editor, a reload, and the note return link", async ({ page }) => {
  const library = "/library?q=Reading&status=draft&sort=-modified&limit=2";
  await page.goto(library);
  await page.locator(".overview-result-link", { hasText: "Reading" }).click();
  await page.locator("[data-source-editor-launch]").click();
  await expect(page.locator(SURFACE)).toBeVisible();
  expect(new URL(page.url()).searchParams.get("return_to")).toBe(library + "#library-result-reading");
  await page.reload();
  await expect(page.locator(SURFACE)).toBeVisible();
  await page.locator("[data-source-note-return]").click();
  await page.getByRole("link", { name: "Back to results", exact: true }).click();
  await expect(page).toHaveURL(new URL(library + "#library-result-reading", page.url()).href);
  await expect(page.locator("#search-input")).toHaveValue("Reading");
  await expect(page.locator("[data-library-sort]")).toHaveValue("-modified");
  await expect(page.locator(".facet-chip")).toContainText("draft");
});

test("the filter picker recovers from a failed request without losing search state", async ({ page }) => {
  let requests = 0;
  await page.route("**/api/facets", async (route) => {
    requests++;
    if (requests === 1) return route.fulfill({ status: 503, body: "unavailable" });
    return route.continue();
  });
  const library = "/library?q=body&status=draft&sort=title&limit=2";
  await page.goto(library);
  const retry = page.getByRole("button", { name: "Retry filters" });
  await expect(retry).toBeVisible();
  const input = page.locator(".facet-input");
  await input.fill("draft");
  await expect(page.locator(".facet-options")).toContainText("Facets unavailable.");
  await retry.focus();
  await page.keyboard.press("Enter");
  await expect(retry).toBeHidden();
  await expect(input).toBeFocused();
  await expect(input).toHaveValue("draft");
  await expect(page.locator('.facet-option[aria-selected="true"]')).toContainText("draft");
  await expect(page).toHaveURL(new URL(library, page.url()).href);
  await expect(page.locator(".overview-result-title")).toHaveText(["Alpha", "Beta"]);
  expect(requests).toBe(2);
  await page.getByRole("button", { name: "Select displayed", exact: true }).click();
  await page.locator("#overview-bulk-folder").focus();
  await expect(page.locator('#bn-folder-suggestions option[value="Notes"]')).toHaveCount(1);
});
