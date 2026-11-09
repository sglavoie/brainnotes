import { test, expect } from "./fixture.js";
import AxeBuilder from "@axe-core/playwright";

async function expectAccessible(page) {
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(result.violations, JSON.stringify(result.violations, null, 2)).toEqual([]);
}

test("quiet reading and book type persist without changing note content", async ({ page }) => {
  await page.goto("/n/Reading");
  const before = await page.locator("[data-note-body]").textContent();
  await page.locator(".note-actions-more > summary").click();
  await page.locator("button[data-reading-mode]").click();
  await page.locator("button[data-reading-type]").click();
  await expect(page.locator(".note-meta-sidebar")).toBeHidden();
  await expect(page.locator(".note-toc-sidebar")).toBeHidden();
  await page.reload();
  await expect(page.locator("button[data-reading-mode]")).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("button[data-reading-type]")).toHaveAttribute("aria-pressed", "true");
  expect(await page.locator("[data-note-body]").textContent()).toBe(before);
  await page.locator(".note-actions-more > summary").click();
  await page.locator("button[data-reading-mode]").click();
  await expect(page.locator(".note-meta-sidebar")).toBeVisible();
});

test("preview supports keyboard, touch controls, failure recovery and search context", async ({ page }) => {
  // Other journeys intentionally edit Alpha in this shared disposable vault.
  const note = await (await page.request.get("/api/note/peek?key=Alpha")).json();
  await page.goto("/library?q=Alpha");
  const trigger = page.locator(".overview-result-row .note-peek-trigger").first();
  await trigger.focus();
  await trigger.press("Enter");
  const peek = page.locator("#note-peek");
  await expect(peek).toBeVisible();
  await expect(peek.locator("p")).toHaveText(note.excerpt.replace(/\[\[([^\]]+)\]\]/g, "$1"));
  await expectAccessible(page);
  await page.keyboard.press("Escape");
  await expect(peek).toBeHidden();
  await expect(trigger).toBeFocused();
  await page.route("**/api/note/peek?**", (route) => route.abort());
  await page.setViewportSize({ width: 390, height: 640 });
  await trigger.click({ force: true });
  await expect(peek.locator("p")).toContainText("Preview unavailable");
  await expect(peek.getByRole("link", { name: "Open note" })).toBeVisible();
  await page.unroute("**/api/note/peek?**");
  await peek.getByRole("link", { name: "Open note" }).click();
  await page.getByRole("link", { name: "Back to results", exact: true }).click();
  await expect(page.locator("#search-input")).toHaveValue("Alpha");
});

test("capture keeps an editor draft, restores focus and returns only after confirmation", async ({ page }) => {
  await page.goto("/edit?key=Alpha");
  const editor = page.locator(".cm-content");
  await editor.fill("# Alpha\n\nUnsaved writing remains here.");
  await editor.press("ControlOrMeta+Shift+Space");
  const dialog = page.locator("[data-capture-dialog]");
  const input = dialog.locator("[data-capture-input]");
  await input.fill("A passing idea");
  await expectAccessible(page);
  await page.keyboard.press("Escape");
  await expect(editor).toBeFocused();
  await expect(editor).toContainText("Unsaved writing remains here");
  await editor.press("ControlOrMeta+Shift+Space");
  await expect(input).toHaveValue("A passing idea");
  let release;
  const pending = new Promise((resolve) => { release = resolve; });
  await page.route("**/api/capture", async (route) => { await pending; await route.continue(); });
  await input.press("ControlOrMeta+Enter");
  await expect(input).toBeDisabled();
  await expect(dialog).toBeVisible();
  release();
  await expect(dialog).toBeHidden();
  await expect(page.locator("[data-capture-notice]")).toContainText(/Captured/i);
  await expect(editor).toBeFocused();
  await expect(editor).toContainText("Unsaved writing remains here");
  await expect(page.locator("[data-source-save]")).toBeEnabled();
});

test("the Brief uses one capture form and gives each card a useful browser action", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("[data-capture-root]")).toHaveCount(1);
  const input = page.locator("[data-capture-input]");
  await input.fill("Keep this draft in the same form");
  await page.locator("[data-capture-launch]").click();
  await expect(page.locator("[data-capture-dialog] [data-capture-input]")).toHaveValue("Keep this draft in the same form");
  await page.keyboard.press("Escape");
  await expect(page.locator(".capture-panel [data-capture-input]")).toHaveValue("Keep this draft in the same form");
  const resume = page.locator('[data-brief-slot="continue"] [data-brief-action-open]');
  await expect(resume).toHaveText("Resume writing");
  await expect(resume).toBeEnabled();
  await resume.click();
  await expect(page).toHaveURL(/\/edit\?key=/);
  await expect(page.locator(".cm-content")).toBeVisible();
});

test("reading and capture fit a small phone with reduced motion", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce", colorScheme: "dark" });
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto("/n/Alpha");
  await page.locator("[data-capture-launch]").click();
  await expect(page.locator("[data-capture-dialog] [data-capture-input]")).toBeInViewport();
  await expect(page.locator("[data-capture-close]")).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.keyboard.press("Escape");
  await page.locator(".note-actions-more > summary").click();
  await page.locator("button[data-reading-mode]").click();
  await expect(page.locator("[data-note-body]")).toBeVisible();
});

test("Continue resumes the passage last visited in this tab", async ({ page }) => {
  await page.goto("/");
  await page.locator('[data-brief-slot="continue"] [data-brief-action-open]').click();
  const editor = page.locator(".cm-content");
  await expect(editor).toBeVisible();
  await editor.click();
  await editor.press("ControlOrMeta+End");
  await page.goto("/");
  await expect(page.locator('[data-brief-slot="continue"] .brief-card-context')).toContainText("Last passage in this tab");
  await page.locator('[data-brief-slot="continue"] [data-brief-action-open]').click();
  await expect(page).toHaveURL(/read_at=/);
  const position = JSON.parse(new URL(page.url()).searchParams.get("read_at"));
  expect(position.sourceLine).toBeGreaterThan(1);
  await expect(editor).toBeVisible();
});

test("a dismissed hover preview cannot reappear when its request finishes", async ({ page }) => {
  let finish;
  const waiting = new Promise((resolve) => { finish = resolve; });
  await page.route("**/api/note/peek?**", async (route) => {
    await waiting;
    await route.fulfill({ json: { title: "Late note", excerpt: "This response arrived after dismissal." } });
  });
  await page.goto("/n/Alpha");
  // The fixture's conflict notice arrives asynchronously and moves the prose.
  // Hover the settled link, so this tests dismissal rather than pointer drift.
  await expect(page.locator(".sync-line-review")).toBeVisible();
  await page.locator('[data-note-body] a[href="/n/beta"]').hover();
  await expect(page.locator("#note-peek")).toBeVisible();
  await page.keyboard.press("Escape");
  finish();
  await expect(page.locator("#note-peek")).toBeHidden();
  await expect(page.locator("#note-peek h2")).not.toHaveText("Late note");
});
