import { test, expect } from "./fixture.js";
import { SURFACE } from "./editor-helpers.js";

for (const [width, height] of [[390, 640], [320, 568]]) {
  test(`writing space and expandable frontmatter at ${width} × ${height}`, async ({ page }) => {
    await page.setViewportSize({ width, height });
    await page.goto("/edit?key=Writing");
    await expect(page.locator(SURFACE)).toContainText("The morning trail");
    const prose = page.locator(".cm-line").filter({ hasText: /^The morning trail/ }).first();
    await expect(prose).toBeInViewport();
    const bounds = await page.locator(".cm-scroller").boundingBox();
    expect(bounds.y).toBeLessThan(320);
    expect(Math.min(bounds.y + bounds.height, height) - bounds.y).toBeGreaterThan(180);
    await expect(page.locator("[data-source-save]")).toBeInViewport();
    await expect(page.locator("[data-source-rename-title]")).toBeHidden();
    await expect(page.locator("[data-note-history]")).toBeHidden();
    await expect(page.locator("[data-source-save]")).toBeDisabled();
    const unfold = page.getByRole("button", { name: "Show frontmatter in source editor" });
    await unfold.focus();
    await unfold.press("Enter");
    await expect(page.locator(SURFACE)).toContainText("field_0: Observation 0");
    await expect(page.locator("[data-source-save]")).toBeDisabled();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}

test("formatting and Save remain usable in a reduced writing viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 640 });
  await page.goto("/edit?key=Writing");
  const prose = page.locator(".cm-line").filter({ hasText: /^The morning trail/ }).first();
  await prose.click();
  // Models the space left by a keyboard; native keyboard behavior still needs a device.
  await page.setViewportSize({ width: 390, height: 360 });
  await page.keyboard.insertText("Observation: ");
  await expect(page.locator("[data-source-save]")).toBeInViewport();
  await expect(page.locator("[data-source-save]")).toBeEnabled();
  const format = page.locator(".source-editor-format > summary");
  await format.click();
  await expect(page.getByRole("button", { name: "Wikilink", exact: true })).toBeVisible();
  await format.press("Escape");
  await expect(format).toBeFocused();
  await expect(page.locator("[data-source-draft-status]")).toContainText("Draft kept in this browser");
});

test("Library owns one results list and shows readable highlighted excerpts", async ({ page }) => {
  await page.goto("/library?q=Beta");
  const input = page.getByRole("searchbox", { name: "Search notes" });
  await input.focus();
  await expect(page.locator("#search-results")).toHaveCount(0);
  await expect(page.locator("[data-search-library]")).toHaveCount(0);
  const snippet = page.locator('[data-overview-result-key="notes/gamma"] .overview-result-snippet');
  await expect(snippet).toContainText("Beta");
  await expect(snippet).not.toContainText("[[");
  await expect(snippet.locator("mark").first()).toHaveText("Beta");
  await input.press("Enter");
  await expect(page.getByRole("heading", { name: "Search results", exact: true })).toBeFocused();
});

test("Brief leads with notes and keeps reasons and AI behind disclosures after Another", async ({ page }) => {
  await page.goto("/");
  const card = page.locator('[data-brief-slot="continue"] [data-brief-card]');
  await expect(card).toBeVisible();
  const firstID = await card.getAttribute("data-brief-card-id");
  await expect(card.locator(".brief-card-explanation")).toBeHidden();
  await expect(card.locator("[data-brief-insight-prompt]")).toBeHidden();
  expect((await card.boundingBox()).y).toBeLessThan((await page.locator(".brief-activity").boundingBox()).y);
  expect((await card.boundingBox()).y).toBeLessThan((await page.locator(".capture-panel").boundingBox()).y);
  await card.locator(".brief-card-why > summary").focus();
  await page.keyboard.press("Enter");
  await expect(card.locator(".brief-card-explanation")).toBeVisible();
  await card.locator(".brief-ai > summary").click();
  await expect(card.locator("[data-brief-insight-prompt]")).toBeVisible();
  await card.locator("[data-brief-action-another]").click();
  await expect(card).not.toHaveAttribute("data-brief-card-id", firstID);
  await expect(card.locator(".brief-card-explanation")).toBeHidden();
  await expect(card.locator("[data-brief-insight-prompt]")).toBeHidden();
  await card.locator(".brief-card-why > summary").click();
  await expect(card.locator(".brief-card-explanation")).toBeVisible();
});

test("uncertain save keeps the draft and offers safe retry with distinct success feedback", async ({ page }) => {
  await page.goto("/edit?key=Writing");
  await page.locator(".cm-line").filter({ hasText: /^The morning trail/ }).first().click();
  await page.keyboard.insertText("A new observation. ");
  await page.route("**/api/source/save", (route) => route.fulfill({ contentType: "text/html", body: "<html>Sign in</html>" }));
  await page.locator("[data-source-save]").click();
  await expect(page.locator("[data-source-status]")).toContainText("Save not confirmed");
  await expect(page.locator("[data-source-draft-status]")).toContainText("Draft kept in this browser");
  await expect(page.locator("[data-source-save]")).toBeEnabled();
  await page.unroute("**/api/source/save");
  await page.locator("[data-source-save]").click();
  await expect(page.locator("[data-source-status]")).toHaveText("Saved to vault.");
  await expect(page.locator("[data-source-draft-status]")).toBeEmpty();
});
