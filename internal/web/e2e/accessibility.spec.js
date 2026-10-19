import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "./fixture.js";

const journeys = [
  ["Library", "/library", "[data-overview-search-results]"],
  ["reading", "/n/Alpha", "[data-note-body]"],
  ["editing", "/edit?key=Alpha", "[data-source-editor]"],
];

async function expectNoAxeViolations(page) {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
}

for (const [name, path, ready] of journeys) {
  test(`${name} has no detectable WCAG A or AA violations`, async ({ page }) => {
    await page.goto(path);
    await expect(page.locator(ready)).toBeVisible();
    await expectNoAxeViolations(page);
  });

  for (const [label, width, zoom] of [["320 px", 320, "1"], ["200% zoom", 640, "2"]]) {
    test(`${name} reflows at ${label}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 640 });
      await page.goto(path);
      await expect(page.locator(ready)).toBeVisible();
      await page.evaluate((value) => { document.documentElement.style.zoom = value; }, zoom);
      await expect.poll(() => page.evaluate(() =>
        document.documentElement.scrollWidth <= document.documentElement.clientWidth
      )).toBe(true);
    });
  }
}

test("open Library controls and reading actions remain accessible", async ({ page }) => {
  await page.goto("/library?q=field&status=reference");
  await expect(page.locator(".overview-result-link").first()).toBeVisible();
  await page.locator(".library-filters > summary").click();
  await page.locator(".library-search-menu > summary").click();
  await expectNoAxeViolations(page);

  await page.goto("/n/Reading");
  await page.locator(".note-actions-more > summary").click();
  await page.locator(".frontmatter-editor > summary").first().click();
  await expectNoAxeViolations(page);
});

test("editor formatting and confirmation dialog remain accessible", async ({ page }) => {
  await page.goto("/edit?key=Writing");
  await expect(page.locator("[data-source-editor]")).toBeVisible();
  await page.locator(".source-editor-format > summary").click();
  await expectNoAxeViolations(page);
  await page.locator(".source-editor-format > summary").click();
  await page.route("**/api/history/preview", (route) => route.fulfill({ json: {
    ok: true, entry_id: "accessibility-entry", preview_id: "accessibility-receipt",
    command: "bn frontmatter set status", timestamp: "2026-01-02T03:04:05Z",
    can_restore: true, requires_force: false,
    changes: [{ path: "Writing.md", current_state: "file", restored_state: "file", conflict: false }],
  } }));
  await page.locator("[data-source-manage] > summary").click();
  await page.locator("[data-source-undo]").click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expectNoAxeViolations(page);
});

test("search failure and pending write reminder remain accessible", async ({ page }) => {
  await page.addInitScript(() => sessionStorage.setItem("bn.pending-write-outcomes", JSON.stringify([{
    id: "0123456789abcdef0123456789abcdef", label: "Trash",
  }])));
  await page.route("**/api/search?**", (route) => route.fulfill({ status: 503, json: { error: "Search is temporarily unavailable." } }));
  await page.route("**/api/operations/outcome?**", (route) => route.fulfill({ json: {
    ok: true, status: "confirmed", journal_id: "accessibility-entry",
  } }));
  await page.goto("/library?q=unavailable");
  await expect(page.locator(".pending-write-outcomes")).toBeVisible();
  await expect(page.locator("[data-overview-search-status]")).toContainText(/unavailable|failed|retry/i);
  await expectNoAxeViolations(page);
  await page.locator(".pending-write-outcomes .write-outcome-check").click();
  await expect(page.locator(".pending-write-outcomes")).toHaveCount(0);
  await expect(page.locator(".write-outcome-confirmation a")).toHaveAttribute("href", "/history");
  await expectNoAxeViolations(page);
});

test("main destinations and Search are directly available on desktop", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/n/Reading");
  const nav = page.getByRole("navigation", { name: "Main destinations" });
  await expect(nav).toBeVisible();
  await expect(nav.getByRole("link", { name: "Search notes", exact: true })).toHaveAttribute("href", "/library");
  await expect(nav.getByRole("link", { name: "Brief" })).toBeVisible();
  await page.locator(".source-editor-workspace > summary").click();
  await expect(nav).toBeHidden();
  await expect(page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Library" })).toBeVisible();
  await page.locator(".source-editor-workspace > summary").click();
  for (const path of ["/", "/library", "/settings", "/edit?key=Writing"]) {
    await page.goto(path);
    await expect(page.getByRole("navigation", { name: "Main destinations" })).toBeVisible();
    if (path.startsWith("/edit")) {
      const navigation = await page.locator(".desktop-workspace-nav").boundingBox();
      const workspace = await page.locator(".source-editor-workspace > summary").boundingBox();
      expect(navigation.x + navigation.width).toBeLessThan(workspace.x);
      expect(workspace.x + workspace.width).toBeLessThanOrEqual(1280);
    }
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(nav).toBeHidden();
  for (const path of ["/", "/n/Reading", "/settings", "/graph", "/edit?key=Writing"]) {
    await page.goto(path);
    const search = page.getByRole("link", { name: "Search notes", exact: true });
    await expect(search).toBeInViewport();
    const bounds = await search.boundingBox();
    expect(bounds.width).toBeGreaterThanOrEqual(44);
    expect(bounds.height).toBeGreaterThanOrEqual(44);
    await search.click();
    await expect(page).toHaveURL(/\/library$/);
  }
  await page.goto("/edit?key=Writing");
  await page.locator(".source-editor-workspace > summary").click();
  await expect(page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Library" })).toBeVisible();
});
