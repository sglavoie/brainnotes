import { existsSync } from "node:fs";
import { join } from "node:path";

import { expect, gitStatus, manifest, test } from "./fixture.js";

test("a lost create response can be checked without creating again", async ({ page }) => {
  await page.goto("/broken?q=Unwritten%20Example");
  const action = page.locator('[data-create-note][data-create-target="Unwritten Example"]');
  let requests = 0;
  let createdPath;
  let createdRoute;
  await page.route("**/api/notes/create", async (route) => {
    requests += 1;
    const response = await route.fetch();
    expect(response.ok()).toBe(true);
    const result = await response.json();
    expect(result.ok).toBe(true);
    createdPath = result.rel_path;
    createdRoute = result.path;
    // Commit the real creation, then lose the answer on the way to the browser.
    await route.abort("failed");
  });
  await action.getByRole("button", { name: "Create missing note" }).click();
  await expect(action.locator("[data-create-status]")).toContainText("Couldn't confirm note creation");
  expect(existsSync(join(manifest.localVault, createdPath))).toBe(true);
  await action.getByRole("link", { name: "Refresh to check" }).click();
  await expect(page).toHaveURL(/\/broken\?q=Unwritten%20Example$/);
  await expect(page.getByText("No broken links match this filter.")).toBeVisible();
  expect(requests).toBe(1);
  // Return the shared fixture to a broken target for later browsing tests.
  await page.goto(createdRoute);
  await page.getByText("More actions", { exact: true }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator("[data-trash-button]").click();
  await expect(page).toHaveURL(`${manifest.baseURL}/`);
  expect(existsSync(join(manifest.localVault, createdPath))).toBe(false);
  await page.waitForLoadState("networkidle");
  expect(gitStatus(manifest.localVault)).toBe("");
});
