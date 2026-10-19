import { test, expect } from "./fixture.js";

for (const width of [390, 1280]) {
  test(`editor shows writing on entry at ${width}px and keeps navigation accessible`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/edit?key=Reading");
    const content = page.locator(".cm-content");
    await expect(content).toContainText("First section");
    // No scrollIntoView or click may precede this check: this is the entry viewport.
    const bounds = await content.boundingBox();
    expect(bounds.y).toBeLessThan(350);
    expect(Math.min(bounds.y + bounds.height, 844) - bounds.y).toBeGreaterThan(250);
    await expect(page.locator("[data-source-save]")).toBeInViewport();
    await expect(page.locator("[data-sync-line]")).toBeVisible();
    await expect(page.locator("[data-source-rename-title]")).toBeHidden();
    await expect(page.locator(".source-editor-workspace")).not.toHaveAttribute("open", "");
    await page.keyboard.press("/");
    await expect(page.locator("#search-input")).toBeFocused();
    await expect(page.locator(".source-editor-workspace")).toHaveAttribute("open", "");
    await page.getByText("Workspace", { exact: true }).click();
    await page.locator("[data-source-manage] > summary").click();
    await page.getByText("Rename note", { exact: true }).click();
    await expect(page.getByLabel("Note title", { exact: true })).toBeVisible();
    await page.getByText("Rename note", { exact: true }).click();
    await page.locator("[data-source-manage] > summary").click();
    if (width === 390) {
      await page.evaluate(() => window.scrollTo(0, 350));
      await expect(page.locator("[data-source-save]")).toBeInViewport();
      await expect(content).toBeInViewport();
    }
  });
}

// Under a long title the tools wrap onto their own line at the left, and a
// panel anchored to the summary's right edge then opened off-screen.
for (const path of ["/n/Reading", "/library", "/edit?key=Reading"]) {
  test(`the Workspace panel stays on screen under a long title at 390px on ${path}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(path);
    // Set in the page rather than the fixture, so no note has to be renamed.
    await page.locator("main h1").first().evaluate((heading) => {
      heading.textContent = "A deliberately long note title that wraps the header onto several lines";
    });
    await page.locator(".source-editor-workspace > summary").click();
    const panel = page.locator(".source-editor-workspace-content");
    await expect(panel).toBeVisible();
    const box = await panel.boundingBox();
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(390);
  });
}

test("reading shows values and opens only the selected property editor", async ({ page }) => {
  await page.goto("/n/Reading");
  const panel = page.locator("[data-frontmatter-panel]");
  await expect(panel.locator('.frontmatter-text').filter({ hasText: /^draft$/ })).toBeVisible();
  await expect(panel.locator("form:visible")).toHaveCount(0);
  await panel.getByLabel("Edit status", { exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(panel.locator('[data-frontmatter-property="status"][data-frontmatter-op="="] input')).toBeVisible();
  await panel.getByLabel("Edit title", { exact: true }).click();
  await expect(panel.locator('[data-frontmatter-property="status"][data-frontmatter-op="="] input')).toBeHidden();
  await panel.locator(".frontmatter-add > summary").click();
  await expect(panel.locator('.frontmatter-add-form input[name="property"]')).toBeVisible();
  await expect(panel.locator('[data-frontmatter-property="title"][data-frontmatter-op="="] input')).toBeHidden();
});

test("Library reveals actions for a selection and keeps structural facts neutral", async ({ page }) => {
  await page.goto("/library?q=body");
  const checkboxes = page.locator(".overview-result-select input");
  await expect(checkboxes.first()).toBeVisible();
  await expect(page.locator("[data-overview-clear-selection]")).toBeHidden();
  await expect(page.locator("[data-overview-copy-wikilinks]")).toBeHidden();
  const row = page.locator(".overview-result-item").filter({ has: page.getByRole("link", { name: /^Beta / }) });
  await expect(row.locator(".overview-result-details")).not.toHaveAttribute("open", "");
  await row.locator(".overview-result-details summary").click();
  await expect(row.locator(".overview-result-details")).toContainText(/dead end/i);
  await expect(row.locator(".is-issue")).toHaveCount(0);
  await checkboxes.first().check();
  await expect(page.locator("[data-overview-copy-wikilinks]")).toBeVisible();
  await expect(page.locator("[data-overview-clear-selection]")).toBeEnabled();
  await page.locator("[data-overview-clear-selection]").click();
  await expect(page.locator("[data-overview-copy-wikilinks]")).toBeHidden();
});

test("Trash cancels with focus restored and never claims success for an HTML response", async ({ page }) => {
  let writes = 0;
  await page.route("**/api/notes/trash", async (route) => {
    writes += 1;
    await route.fulfill({ status: 200, contentType: "text/html", body: "<html>Sign in</html>" });
  });
  await page.goto("/n/Reading");
  await page.getByText("Actions", { exact: true }).click();
  const trash = page.locator("[data-trash-button]");
  await trash.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("Reading");
  await expect(dialog.getByRole("button", { name: "Cancel", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(trash).toBeFocused();
  expect(writes).toBe(0);
  await trash.click();
  await dialog.getByRole("button", { name: "Move to Trash", exact: true }).click();
  await expect(page.locator("[data-trash-status]")).toContainText("not confirmed");
  await expect(page).toHaveURL(/\/n\/Reading$/);
  expect(writes).toBe(1);
});

test("reading opens on prose on a short phone screen and keeps search reachable", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 640 });
  await page.goto("/n/Reading");
  await expect(page.locator("[data-note-body] p").first()).toBeInViewport({ ratio: 1 });
  await expect(page.locator("[data-source-editor-launch]")).toBeInViewport();
  await expect(page.locator(".source-editor-workspace")).not.toHaveAttribute("open", "");
  await page.keyboard.press("/");
  await expect(page.locator("#search-input")).toBeFocused();
  await expect(page.locator(".source-editor-workspace")).toHaveAttribute("open", "");
  await page.locator("#search-input").fill("Alpha");
  await expect(page.locator(".search-result-title").first()).toHaveText("Alpha");
});
