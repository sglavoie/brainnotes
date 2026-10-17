import { test, expect } from "./fixture.js";
import { SURFACE } from "./editor-helpers.js";

// Deliberately small, paired coverage: every key surface has both themes and
// widths across the baseline set. Behavioral journeys use real API responses;
// only the destructive confirmation is supplied a fixed preview here.
for (const [theme, width] of [["light", 1280], ["dark", 390]]) {
  test.describe(`${theme} ${width}`, () => {
    test.use({ viewport: { width, height: 900 } });
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("bn.theme", value), theme);
    });
    const snapshot = async (page, name) => {
      await page.evaluate(() => document.fonts.ready);
      await page.mouse.move(0, 0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await expect(page).toHaveScreenshot(`${name}-${theme}-${width}.png`, {
        animations: "disabled", caret: "hide", maxDiffPixelRatio: 0.005,
        mask: [page.locator(".recent-note-meta")],
      });
    };
    test("Library dense results and a long title", async ({ page }) => {
      await page.goto("/library?q=body&status=draft&sort=title");
      await expect(page.locator(".overview-result-link").first()).toBeVisible();
      await page.locator(".overview-result-link").first().evaluate((link) => {
        link.textContent = "A long note title about field observations, unfinished ideas, and a careful return to the work in progress";
      });
      await page.locator("[data-overview-search-results]").scrollIntoViewIfNeeded();
      await snapshot(page, "library");
    });
    test("reading and Contents", async ({ page }) => {
      await page.goto("/n/Reading");
      await expect(page.locator("[data-note-body]")).toBeVisible();
      await page.locator("#second-section").evaluate((heading) => heading.scrollIntoView());
      await snapshot(page, "reading");
    });
    test("editor and exact Undo confirmation", async ({ page }) => {
      await page.goto("/edit?key=Beta");
      await expect(page.locator(SURFACE)).toContainText("Beta body");
      await snapshot(page, "editor");
      await page.route("**/api/history/preview", (route) => route.fulfill({ json: {
        ok: true, entry_id: "visual-entry", preview_id: "visual-receipt",
        command: "bn frontmatter set status", timestamp: "2026-01-02T03:04:05Z",
        can_restore: true, requires_force: false,
        changes: [{ path: "Notes/A long note title about field observations.md", current_state: "file", restored_state: "file", conflict: false }],
      } }));
      await page.locator("[data-source-manage] > summary").click();
      await page.locator("[data-source-undo]").click();
      await expect(page.getByRole("dialog")).toContainText("field observations");
      await snapshot(page, "confirmation");
      await page.keyboard.press("Escape");
    });
  });
}

test("Library empty and error states", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 900 });
  await page.addInitScript(() => localStorage.setItem("bn.theme", "light"));
  await page.goto("/library?q=no-such-visual-result");
  await expect(page.locator("[data-overview-search-status]")).toContainText(/no|0/i);
  await page.locator("[data-overview-search-status]").scrollIntoViewIfNeeded();
  await expect(page.locator("[data-overview-search-results]")).toHaveScreenshot("library-empty.png", { animations: "disabled" });
  await page.route("**/api/search?**", (route) => route.fulfill({ status: 503, json: { error: "Search is temporarily unavailable." } }));
  await page.locator("#search-input").fill("unavailable");
  await expect(page.locator("[data-overview-search-status]")).toContainText(/unavailable|failed|retry/i);
  await page.locator("[data-overview-search-status]").scrollIntoViewIfNeeded();
  await expect(page.locator("[data-overview-search-results]")).toHaveScreenshot("library-error.png", { animations: "disabled" });
});
