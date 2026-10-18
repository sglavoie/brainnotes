import { expect, test } from "./fixture.js";

for (const width of [1280, 390]) {
  for (const javaScriptEnabled of [true, false]) {
    test.describe(`skip link at ${width}px with JavaScript ${javaScriptEnabled}`, () => {
      test.use({ javaScriptEnabled, viewport: { width, height: 844 } });

      test("skips metadata and continues tabbing inside the note", async ({ page }) => {
        await page.goto("/n/Reading");
        const skip = page.getByRole("link", { name: "Skip to note", exact: true });
        expect((await skip.boundingBox()).y + (await skip.boundingBox()).height).toBeLessThan(0);
        await page.keyboard.press("Tab");
        await expect(skip).toBeFocused();
        await expect(skip).toBeInViewport();
        await page.keyboard.press("Enter");
        const body = page.locator("[data-note-body]");
        await expect(body).toBeFocused();
        await expect(body).toBeInViewport();
        await page.keyboard.press("Tab");
        await expect(body.locator("a:visible, button:visible").first()).toBeFocused();
        await page.emulateMedia({ media: "print" });
        await expect(skip).toBeHidden();
      });
    });
  }
}

for (const status of [200, 409]) {
  test(`capture preserves search focus after a delayed ${status} response`, async ({ page }) => {
    let finish;
    const pending = new Promise((resolve) => { finish = resolve; });
    await page.route("**/api/capture", async (route) => {
      await pending;
      await route.fulfill({ status, json: { ok: status === 200, message: "Capture result" } });
    });
    await page.goto("/");
    const input = page.locator("[data-capture-input]");
    await input.fill("A thought while browsing");
    await page.locator("[data-capture-submit]").click();
    await expect(input).toBeDisabled();
    await page.keyboard.press("/");
    const search = page.locator("#search-input");
    await search.fill("Reading");
    finish();
    await expect(input).toBeEnabled();
    await expect(search).toBeFocused();
    await expect(input).toHaveValue(status === 200 ? "" : "A thought while browsing");
  });
}

test("removing search text keeps filters and expression on mobile", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/library?q=missing&tag=absent&expr=tag:absent&sort=title&limit=2");
  const removeText = page.getByRole("link", { name: "Remove search text", exact: true });
  await removeText.focus();
  await removeText.press("Enter");
  await expect(page.locator("#search-input")).toHaveValue("");
  await expect(page.locator("[data-query-bar-input]")).toHaveValue("tag:absent");
  await expect(page.locator("[data-overview-empty-help]")).toContainText("Searching with: filters, expression.");
  await expect(removeText).toHaveCount(0);
  expect(new URL(page.url()).search).toBe("?tag=absent&expr=tag%3Aabsent&sort=title&limit=2");
});
