import { test, expect } from "./fixture.js";
import AxeBuilder from "@axe-core/playwright";

test("formatting starts tasks, continues them, and can be undone", async ({ page }) => {
  await page.goto("/edit?key=Alpha");
  const editor = page.locator(".cm-content");
  await expect(editor).toContainText("Alpha body");
  await editor.fill("First thought\nSecond thought");
  await editor.press("ControlOrMeta+a");
  await page.locator(".source-editor-format > summary").click();
  await page.getByRole("button", { name: "Task list", exact: true }).click();
  await expect(editor).toContainText("- [ ] First thought");
  await expect(editor).toContainText("- [ ] Second thought");
  await expect(editor).toBeFocused();
  await editor.press("ControlOrMeta+z");
  await expect(editor).not.toContainText("[ ]");
  await editor.fill("");
  await page.locator(".source-editor-format > summary").click();
  await page.getByRole("button", { name: "Task list", exact: true }).click();
  await page.keyboard.type("Remember this");
  await page.keyboard.press("Enter");
  await page.keyboard.type("And this");
  await expect(editor).toHaveText("- [ ] Remember this- [ ] And this");
  await expect(page.locator("[data-source-save]")).toBeEnabled();
});

test("counts exclude frontmatter and respond to selections, edits, and undo", async ({ page }) => {
  await page.goto("/edit?key=Alpha");
  const editor = page.locator(".cm-content");
  await expect(editor).toContainText("Alpha body");
  await editor.fill("---\ntitle: Ignore these metadata words\n---\n\nOne two three.");
  const count = page.locator("[data-writing-count]");
  await expect(count).toHaveText("3 words");
  await editor.press("ControlOrMeta+End");
  await editor.press("Shift+Home");
  await expect(count).toHaveText("3 words selected · 3 words total");
  await page.keyboard.type("Four five");
  await expect(count).toHaveText("2 words");
  await editor.press("ControlOrMeta+z");
  await expect(count).toContainText("3 words");
});

test("spelling preferences and focus survive reload without dirtying a note", async ({ page }) => {
  await page.goto("/edit?key=Alpha");
  const editor = page.locator(".cm-content");
  await expect(editor).toContainText("Alpha body");
  await expect(editor).toHaveAttribute("spellcheck", "true");
  await page.getByLabel("Spellcheck", { exact: true }).uncheck();
  await expect(editor).toHaveAttribute("spellcheck", "false");
  await page.getByLabel("Writing layout").selectOption("focus");
  await expect(page.locator(".site-header")).toBeHidden();
  await expect(page.locator("[data-source-save]")).toBeDisabled();
  await page.reload();
  await expect(editor).toHaveAttribute("spellcheck", "false");
  await expect(page.getByLabel("Writing layout")).toHaveValue("focus");
  await expect(page.locator("[data-source-save]")).toBeDisabled();
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(result.violations).toEqual([]);
  await page.getByLabel("Writing layout").selectOption("write");
  await expect(page.locator(".site-header")).toBeVisible();
});

test("focus keeps editing and Save reachable on a short phone screen", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto("/edit?key=Survey");
  const editor = page.locator(".cm-content");
  await expect(editor).toBeVisible();
  const format = page.locator(".source-editor-format > summary");
  await format.click();
  await page.getByLabel("Writing layout").selectOption("focus");
  await format.click();
  await expect(page.locator("[data-source-save]")).toBeInViewport();
  await expect(editor).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await format.click();
  await page.getByLabel("Writing layout").selectOption("write");
  await format.click();
  await expect(page.locator(".site-header")).toBeVisible();
});
