import { expect, test } from "./fixture.js";

test("Escape returns from shortcut search to the reading control without scrolling", async ({ page }) => {
  await page.goto("/n/Reading");
  const origin = page.locator('#second-section + [data-section-copy] button');
  await origin.focus();
  await origin.press("/");
  const search = page.getByRole("combobox", { name: "Search notes" });
  await expect(search).toBeFocused();
  await search.fill("Reading");
  await expect(page.locator("#search-results")).toBeVisible();
  const scroll = await page.evaluate(() => scrollY);
  await search.press("Escape");
  await expect(origin).toBeFocused();
  await expect(page.locator("#search-results")).toBeHidden();
  expect(await page.evaluate(() => scrollY)).toBe(scroll);

  // A browser, unlike jsdom, refuses focus on an element hidden by CSS.
  await origin.press("/");
  await origin.evaluate((button) => { button.style.display = "none"; });
  await search.press("Escape");
  await expect(search).toBeFocused();
});

for (const theme of ["light", "dark"]) {
  test(`printing a ${theme} note keeps only its title and body and fits wide content`, async ({ page }) => {
    await page.addInitScript((value) => localStorage.setItem("bn.theme", value), theme);
    await page.goto("/n/Reading");
    const body = page.locator("[data-note-body]");
    const originalText = await body.locator("pre code").textContent();
    // Stress the real note layout without changing fixture vault content.
    await body.evaluate((element) => {
      const code = document.createElement("pre");
      code.innerHTML = `<code>${"long_token_".repeat(100)}</code>`;
      const table = document.createElement("table");
      table.innerHTML = `<tr><td>${"wide_cell_".repeat(100)}</td><td>Second cell</td></tr>`;
      const image = document.createElement("img");
      image.width = 2400;
      image.height = 1200;
      image.alt = "Wide image";
      image.src = "data:image/svg+xml," + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="2400" height="1200"/>');
      element.append(code, table, image);
    });
    await page.emulateMedia({ media: "print" });
    await expect(page.locator(".site-header h1")).toBeVisible();
    await expect(body).toBeVisible();
    for (const selector of [".site-nav", ".site-header-tools", ".note-meta-sidebar", ".note-toc-sidebar",
      ".note-actions", ".note-insight", ".related-notes", ".note-print-body > h2", "[data-copy-controls]"]) {
      for (const element of await page.locator(selector).all()) await expect(element).toBeHidden();
    }
    await expect(page.locator("body")).toHaveCSS("background-color", "rgb(255, 255, 255)");
    await expect(body).toHaveCSS("color", "rgb(0, 0, 0)");
    await expect(body.locator("pre code").first()).toHaveText(originalText);
    for (const element of await body.locator("pre, table, img").all()) {
      expect(await element.evaluate((node) => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
      expect((await element.boundingBox()).width).toBeLessThanOrEqual((await body.boundingBox()).width);
    }
    await page.emulateMedia({ media: "screen" });
    await expect(page.locator(".site-nav")).toBeVisible();
    await expect(page.locator(".note-print-body > h2")).toBeVisible();
  });
}
