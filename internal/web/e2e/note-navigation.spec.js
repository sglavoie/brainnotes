import { expect, isFixtureOrigin, test } from "./fixture.js";

test("Library results retain search context in a new tab and after reload", async ({ page, context, foreignRequests }) => {
  const library = "/library?q=Reading&status=draft&sort=-modified";
  await page.goto(library);
  const result = page.locator(".overview-result-link", { hasText: "Reading" });
  await expect(result).toBeVisible();
  const note = await context.newPage();
  await note.route("**/*", async (route) => {
    const url = route.request().url();
    if (isFixtureOrigin(url, new URL(page.url()).origin)) return route.continue();
    foreignRequests.push(url);
    return route.abort("blockedbyclient");
  });
  await note.goto(await result.getAttribute("href"));
  await note.reload();
  await note.getByRole("link", { name: "Back to results", exact: true }).click();
  await expect(note).toHaveURL(new URL(library, page.url()).href);
  await expect(note.locator("#search-input")).toHaveValue("Reading");
  await expect(note.locator("[data-library-sort]")).toHaveValue("-modified");
  await expect(note.locator(".overview-result-link", { hasText: "Reading" })).toBeVisible();
  await note.close();
  expect(foreignRequests).toEqual([]);
});

test("mobile sidebars collapse independently and preserve the desktop preference", async ({ page }) => {
  await page.goto("/n/Reading");
  await page.locator("[data-note-sidebar-toggle]").click();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  const metadata = page.locator(".note-meta-sidebar details");
  const contents = page.locator(".note-toc-sidebar details");
  await expect(metadata.locator("summary")).toBeVisible();
  await expect(metadata.locator(".frontmatter-panel")).toBeHidden();
  await expect(contents.locator(".note-toc-list")).toBeHidden();
  await expect(page.locator("[data-note-body]")).toBeVisible();
  await metadata.locator("summary").focus();
  await page.keyboard.press("Enter");
  await expect(metadata.locator(".frontmatter-panel")).toBeVisible();
  await expect(contents.locator(".note-toc-list")).toBeHidden();
  await contents.locator("summary").click();
  await expect(contents.locator(".note-toc-list")).toBeVisible();
  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(page.locator(".note-meta-sidebar")).toBeHidden();
  await expect(contents.locator(".note-toc-list")).toBeVisible();
  await expect(contents.locator("summary")).toBeHidden();
  await page.locator("[data-note-sidebar-toggle]").click();
  await expect(metadata.locator(".frontmatter-panel")).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(metadata.locator(".frontmatter-panel")).toBeVisible();
});
