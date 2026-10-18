import { test, expect } from "./fixture.js";

test.describe("touch layouts", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true });

  test("Library starts with two results and keeps constraints outside disclosures", async ({ page }) => {
    await page.goto("/library?q=body&status=draft&sort=title");
    const rows = page.locator(".overview-result-item");
    await expect(rows.nth(1)).toBeVisible();
    const second = await rows.nth(1).boundingBox();
    expect(second.y + second.height).toBeLessThan(844);
    await expect(page.locator(".facet-input")).toBeHidden();
    await expect(page.locator(".facet-chip")).toContainText("draft");
    await expect(page.locator("[data-copy-search-link]")).toBeHidden();
    await expect(page.locator("[data-selection-help]")).toBeHidden();
    await page.locator(".library-search-menu > summary").click();
    await expect(page.locator("[data-copy-search-link]")).toBeVisible();
    await expect(page.locator("[data-saved-views-open]")).toBeVisible();
    await page.locator(".library-filters > summary").click();
    await expect(page.locator(".facet-input")).toBeVisible();
    await expect(page.locator(".facet-chip")).toContainText("draft");
    expect(new URL(page.url()).searchParams.get("status")).toBe("draft");
    await rows.first().locator('input[type="checkbox"]').check();
    await expect(page.locator("[data-selection-help]")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });

  test("short editor puts prose near the top and keeps Save reachable", async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 568 });
    await page.goto("/edit?key=Writing");
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Editing Writing");
    const line = page.locator(".cm-line").filter({ hasText: "Field journal" });
    await expect(line).toBeVisible();
    expect((await line.boundingBox()).y).toBeLessThan(280);
    for (const selector of ["[data-source-save]", "[data-source-find]", ".source-editor-format > summary"]) {
      expect((await page.locator(selector).boundingBox()).height).toBeGreaterThanOrEqual(44);
    }
    await page.setViewportSize({ width: 390, height: 420 });
    await line.click();
    await page.keyboard.press("ControlOrMeta+End");
    await expect(page.locator("[data-source-save]")).toBeInViewport();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });

  test("long excerpts leave room for the next result", async ({ page }) => {
    await page.route("**/api/search?**", async (route) => {
      const response = await route.fetch();
      const notes = await response.json();
      for (const note of notes) note.snippet = "An observation with a long attachment URL and more writing to revisit. ".repeat(5);
      await route.fulfill({ response, json: notes });
    });
    await page.goto("/library?q=body&status=draft&sort=title");
    const second = page.locator(".overview-result-item").nth(1);
    await expect(second).toBeVisible();
    const box = await second.boundingBox();
    expect(box.y + box.height).toBeLessThan(844);
    const snippet = second.locator(".overview-result-snippet");
    expect(await snippet.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true);
  });
});

test("Library disclosures work by keyboard and at enlarged text sizes", async ({ page }) => {
  await page.setViewportSize({ width: 640, height: 900 });
  await page.goto("/library?q=body&status=draft");
  await page.addStyleTag({ content: "html { font-size: 200%; }" });
  const filters = page.locator(".library-filters > summary");
  await filters.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(".facet-input")).toBeVisible();
  await expect(filters).toBeFocused();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.emulateMedia({ forcedColors: "active" });
  await filters.focus();
  const outline = await filters.evaluate((element) => getComputedStyle(element).outlineStyle);
  expect(outline).toBe("solid");
});

for (const width of [320, 390, 640]) {
  test(`editor controls reflow at 200% text size and ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/edit?key=Writing");
    await expect(page.locator(".cm-content")).toBeVisible();
    await page.addStyleTag({ content: "html { font-size: 200%; }" });
    const line = page.locator(".cm-line").filter({ hasText: "Field journal" });
    await line.click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await expect(page.locator("[data-source-save]")).toBeInViewport();
    await page.locator(".source-editor-format > summary").click();
    await expect(page.getByRole("button", { name: "Bold", exact: true })).toBeInViewport();
  });
}

test("healthy Sync has one summary and reveals its precise facts on demand", async ({ page }) => {
  await page.route("**/api/sync/status", async (route) => {
    const response = await route.fetch();
    const status = await response.json();
    status.summary = "Everything is up to date.";
    status.needs_person = false;
    status.observed_at = new Date(Date.now() - 120_000).toISOString();
    status.problems = [];
    status.workers = [];
    status.export = { pending_commits: 0, baseline_pending: false, reserved: false };
    status.transport = { ...status.transport, published: 0, awaiting_receipt: 0, incomplete: 0, rejected: [] };
    status.peer = { ...status.peer, conflicts: 0, pending: false, awaiting_application: 0, incomplete: 0 };
    await route.fulfill({ json: status });
  });
  await page.goto("/sync");
  await expect(page.getByText("Everything is up to date.", { exact: true })).toHaveCount(1);
  await expect(page.locator("[data-sync-checked]")).toHaveText("Checked 2 minutes ago.");
  await expect(page.locator("[data-sync-checked]")).toHaveAttribute("datetime", /T/);
  await expect(page.locator("[data-sync-details]")).not.toHaveAttribute("open");
  await page.locator("[data-sync-details] > summary").click();
  await expect(page.locator("[data-sync-facts]")).toBeVisible();
  await page.locator("[data-sync-refresh]").click();
  await expect(page.locator("[data-sync-summary]")).toHaveText("Everything is up to date.");
  await expect(page.locator("[data-sync-details]")).toHaveAttribute("open");
});
