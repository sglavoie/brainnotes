import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test, expect, manifest } from "./fixture.js";

for (const path of ["/new", "/pinned", "/synthesize?source=Alpha"]) {
  test(`shared navigation works on ${path}`, async ({ page }) => {
    await page.goto(path);
    const workspace = page.locator(".focused-workspace-header summary", { hasText: "Workspace" });
    await expect(workspace).toBeVisible();
    await workspace.focus();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("link", { name: "Library", exact: true })).toBeVisible();
    await page.locator("#search-input").fill("Alpha");
    await expect(page.locator(".search-result-title").first()).toHaveText("Alpha");
    await page.setViewportSize({ width: 390, height: 640 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}

test("Library activity follows the search but respects an explicit disclosure", async ({ page }) => {
  await page.goto("/library");
  const activity = page.locator("[data-library-activity]");
  await expect(activity).toHaveAttribute("open", "");
  await page.locator("#search-input").fill("Alpha");
  await expect(activity).not.toHaveAttribute("open");
  await activity.locator(":scope > summary").click();
  await page.locator("#search-input").fill("Beta");
  await expect(activity).toHaveAttribute("open", "");
  await page.getByRole("link", { name: "Reset search", exact: true }).click();
  await expect(activity).toHaveAttribute("open", "");
});

test("a capture whose response was lost is confirmed after reload without a duplicate", async ({ page }) => {
  const text = "Capture receipt browser retry fixture";
  let attemptID;
  await page.goto("/");
  await page.route("**/api/capture", async (route) => {
    attemptID = new URLSearchParams(route.request().postData()).get("capture_id");
    const response = await route.fetch();
    expect(response.ok()).toBe(true);
    await route.abort("failed");
  });
  await page.locator("[data-capture-input]").fill(text);
  await page.locator("[data-capture-submit]").click();
  await expect(page.locator("[data-capture-status]")).toContainText("Capture not confirmed");
  expect(attemptID).toBeTruthy();
  await page.unroute("**/api/capture");
  await page.reload();
  await expect(page.locator("[data-capture-input]")).toHaveValue(text);
  const request = page.waitForRequest((r) => r.url().endsWith("/api/capture"));
  await page.getByRole("button", { name: "Check / retry capture", exact: true }).click();
  expect(new URLSearchParams((await request).postData()).get("capture_id")).toBe(attemptID);
  await expect(page.locator("[data-capture-status]")).toContainText("This retry added nothing");
  await expect(page.locator("[data-capture-input]")).toHaveValue("");
  expect(readFileSync(join(manifest.localVault, manifest.inboxNote), "utf8").split(text)).toHaveLength(2);
});
