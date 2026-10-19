import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "./fixture.js";

const journeys = [
  ["Library", "/library", "[data-overview-search-results]"],
  ["reading", "/n/Alpha", "[data-note-body]"],
  ["editing", "/edit?key=Alpha", "[data-source-editor]"],
];

for (const [name, path, ready] of journeys) {
  test(`${name} has no detectable WCAG A or AA violations`, async ({ page }) => {
    await page.goto(path);
    await expect(page.locator(ready)).toBeVisible();
    const results = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
      .analyze();
    expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
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
