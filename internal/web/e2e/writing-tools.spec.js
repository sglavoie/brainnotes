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
  await editor.press("ControlOrMeta+a");
  await page.keyboard.insertText("---\ntitle: Ignore these metadata words\n---\n\nOne two three.");
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
  await page.locator(".source-editor-format > summary").click();
  await page.getByLabel("Spellcheck", { exact: true }).uncheck();
  await page.locator(".source-editor-format > summary").click();
  await expect(editor).toHaveAttribute("spellcheck", "false");
  await page.getByLabel("Writing layout").selectOption("focus");
  await expect(page.locator(".site-header .reading-home")).toBeHidden();
  await expect(page.locator(".source-editor-format")).toBeHidden();
  await expect(page.locator("[data-source-save]")).toBeDisabled();
  await page.reload();
  await expect(editor).toHaveAttribute("spellcheck", "false");
  await expect(page.getByLabel("Writing layout")).toHaveValue("focus");
  await expect(page.locator("[data-source-save]")).toBeDisabled();
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(result.violations).toEqual([]);
  await page.keyboard.press("Escape");
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
  await expect(page.locator("[data-source-save]")).toBeInViewport();
  await expect(editor).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.keyboard.press("Escape");
  await expect(page.locator(".site-header")).toBeVisible();
});

for (const [width, height] of [[320, 568], [390, 640]]) {
  test(`quiet Save and touch Focus exit at ${width} × ${height}`, async ({ page }) => {
    await page.setViewportSize({ width, height });
    await page.goto("/edit?key=Survey");
    const state = page.locator("[data-source-save-state]");
    const save = page.locator("[data-source-save]");
    await expect(state).toHaveText("Saved");
    const format = page.locator(".source-editor-format > summary");
    expect((await page.locator(".source-editor-tabs").boundingBox()).y).toBeLessThan((await page.locator("#source-editor-heading").boundingBox()).y);
    expect(Math.abs((await format.boundingBox()).y - (await save.boundingBox()).y)).toBeLessThan(5);
    expect((await state.boundingBox()).x).toBeLessThan((await save.boundingBox()).x);
    await format.focus();
    await format.press("Enter");
    const spelling = page.getByLabel("Spellcheck", { exact: true });
    await spelling.focus();
    await spelling.press("Space");
    await expect(page.locator(".cm-content")).toHaveAttribute("spellcheck", "false");
    await page.getByLabel("Writing layout").selectOption("focus");
    await expect(format).toBeHidden();
    await expect(page.locator("[data-source-find]")).toBeHidden();
    await expect(page.locator("[data-writing-count]")).toBeHidden();
    await expect(state).toBeInViewport();
    await expect(save).toBeInViewport();
    await page.getByRole("button", { name: "Leave Focus view:" }).click();
    await expect(format).toBeVisible();
    await expect(page.locator(".cm-content")).toBeFocused();
    await page.keyboard.insertText("An unsaved observation. ");
    await expect(state).toHaveText("Unsaved changes");
    await expect(save).toBeInViewport();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const attach = page.getByRole("button", { name: "Attach files…" });
    await expect(page.locator("#source-attach-hint")).toBeHidden();
    await attach.focus();
    await expect(page.locator("#source-attach-hint")).toHaveText("Paste images or drop files here, then Save to add them to the note.");
    await expect(page.locator("#source-attach-hint")).toBeVisible();
  });
}

test("Focus preserves actionable sync and conflict recovery", async ({ page }) => {
  await page.route("**/api/sync/status", async (route) => {
    const response = await route.fetch();
    const status = await response.json();
    await route.fulfill({ json: { ...status, headline: "conflict", needs_person: true,
      review_path: "/sync", peer: { ...status.peer, conflicts: 5 } } });
  });
  await page.route("**/api/source/save", (route) => route.fulfill({ json: {
    ok: false, code: "conflict", message: "This note changed. Nothing was written.",
    revision: "other", content: "Other writer's text", changed: false, attachments: [],
  } }));
  await page.goto("/edit?key=Survey");
  await expect(page.locator(".cm-content")).toBeVisible();
  await page.getByLabel("Writing layout").selectOption("focus");
  await expect(page.getByRole("link", { name: "Review 5 incoming changes" })).toBeVisible();
  await page.locator(".cm-content").press("ControlOrMeta+End");
  await page.keyboard.insertText("\nMy new text");
  await page.locator("[data-source-save]").click();
  await expect(page.locator("[data-save-label]")).toHaveText("Unsaved changes");
  await expect(page.locator("[data-source-status]")).toHaveText("This note changed. Nothing was written.");
  await expect(page.locator("[data-source-status]")).toBeVisible();
  await expect(page.getByRole("button", { name: "Save mine over it" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Discard mine and load the note" })).toBeVisible();
});
