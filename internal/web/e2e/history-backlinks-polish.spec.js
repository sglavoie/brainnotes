import { expect, test } from "./fixture.js";

for (const width of [1280, 390]) {
  test(`note history and backlink disclosures work at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/n/Beta");
    if (width < 960) await page.locator("#note-metadata-sidebar > details > summary").click();
    const backlink = page.locator(".backlink-item", { hasText: "Gamma" });
    await expect(backlink.locator(".backlink-count")).toHaveText("4 mentions");
    await expect(backlink.locator(":scope > .backlink-occurrences > li")).toHaveCount(2);
    const hidden = backlink.locator("details li");
    await expect(hidden.first()).toBeHidden();
    const summary = backlink.locator("summary");
    await expect(summary).toHaveText("Show 2 more mentions");
    await summary.focus();
    await page.keyboard.press("Enter");
    await expect(hidden.first()).toBeVisible();
    await expect(hidden.last()).toContainText("Fourth");
    await page.keyboard.press("Space");
    await expect(hidden.first()).toBeHidden();

    await page.locator("[data-note-history]").click();
    await expect(page.locator("[data-history-path]")).toHaveValue("Beta.md");
    await expect(page.locator("[data-history-path-match]")).toHaveValue("exact");
    await page.reload();
    await expect(page.locator("[data-history-path-match]")).toHaveValue("exact");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.locator("[data-history-clear-filters]").click();
    await expect(page.locator("[data-history-path-match]")).toHaveValue("contains");

    await page.goto("/edit?key=Beta");
    await expect(page.locator("[data-note-history]")).toHaveAttribute("href", "/history?path=Beta.md&path_match=exact");
  });

  test(`broken link filters can be cleared with or without matches at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    for (const query of ["Unwritten Example", "NoSuchTarget"]) {
      await page.goto(`/broken?q=${encodeURIComponent(query)}`);
      const clear = page.locator(".filter-form").getByRole("link", { name: "Clear filter" });
      await expect(clear).toBeVisible();
      await clear.click();
      await expect(page).toHaveURL(/\/broken$/);
      await expect(page.locator("#q")).toHaveValue("");
      await expect(page.locator("tbody")).toContainText("Unwritten Example");
    }
  });
}
