import { test, expect } from "./fixture.js";
import AxeBuilder from "@axe-core/playwright";

async function accessible(page) {
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(result.violations, JSON.stringify(result.violations, null, 2)).toEqual([]);
}

test("switcher preserves unsaved writing and hands off to capture", async ({ page }) => {
  await page.goto("/edit?key=Alpha");
  const editor = page.locator(".cm-content");
  await editor.fill("# Alpha\n\nAn unfinished thought.");
  await editor.press("ControlOrMeta+k");
  const input = page.getByRole("combobox", { name: "Search notes, views and actions" });
  await input.fill("Beta");
  await expect(page.locator("#switcher-results")).toContainText("Beta");
  await accessible(page);
  await page.keyboard.press("Escape");
  await expect(editor).toBeFocused();
  await expect(editor).toContainText("An unfinished thought.");
  await editor.press("ControlOrMeta+k");
  await input.fill("Capture a thought");
  await input.press("Enter");
  await expect(page.locator("[data-capture-dialog]")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(editor).toContainText("An unfinished thought.");
});

test("switcher finds recent notes and recovers after failed search", async ({ page }) => {
  await page.goto("/n/Beta");
  await page.goto("/library");
  await page.locator("[data-switcher-launch]").click();
  await expect(page.locator("#switcher-results")).toContainText("Recent note");
  await page.route("**/api/search?**", (route) => route.abort());
  const input = page.locator("#switcher-input");
  await input.fill("Alpha");
  await expect(page.locator(".command-switcher [role=status]")).toContainText("Search unavailable");
  await page.unroute("**/api/search?**");
  await input.fill("Beta");
  await expect(page.locator(".command-switcher [role=status]")).toContainText(/\bresults?\b/);
  await expect(page.locator("#switcher-results .switcher-result-title").filter({ hasText: /^Beta$/ })).toHaveCount(1);
  await input.press("Enter");
  await expect(page).toHaveURL(/\/n\/beta/i);
});

test("kept reference survives navigation and closes without losing the current note", async ({ page }) => {
  await page.goto("/n/Alpha");
  const link = page.locator('[data-note-body] a[href^="/n/beta"]').first();
  await link.focus();
  await link.press("Alt+ArrowDown");
  await page.getByRole("button", { name: "Keep beside this note" }).click();
  const reference = page.getByRole("complementary", { name: "Reference note" });
  await expect(reference).toContainText("Beta");
  await expect(page.locator(".reading-title")).toHaveText("Alpha");
  await accessible(page);
  await page.goto("/n/Reading");
  await expect(reference).toContainText("Beta");
  await expect(page.locator(".reading-title")).toHaveText("Reading");
  await page.getByRole("button", { name: "Close reference" }).click();
  await expect(reference).toBeHidden();
  await page.reload();
  await expect(reference).toHaveCount(0);
});

test("connection opens two passages and creates a recoverable draft with both sources", async ({ page }) => {
  await page.goto("/n/Alpha");
  await page.locator(".related-card .connection-launch").first().click();
  const dialog = page.getByRole("dialog", { name: "Where these ideas meet" });
  await expect(dialog.locator(".connection-note h3")).toHaveCount(2);
  await expect(dialog.locator(".connection-note").first()).toContainText("Alpha");
  await accessible(page);
  await dialog.getByRole("button", { name: "Write a thought about this connection" }).click();
  const body = dialog.getByLabel("Your thought", { exact: true });
  await expect(body).toHaveValue("");
  const sources = await dialog.locator("[data-new-note]").getAttribute("data-new-note-prefix");
  expect(sources).toMatch(/\[\[.+\]\] ↔ \[\[.+\]\]/);
  await dialog.getByRole("button", { name: "Save thought…", exact: true }).click();
  await dialog.getByLabel("Title", { exact: true }).fill("A connection worth keeping");
  await body.fill("Practice becomes useful when it changes my next action.");
  await page.keyboard.press("Escape");
  await page.locator(".related-card .connection-launch").first().click();
  await dialog.getByRole("button", { name: "Write a thought about this connection" }).click();
  await expect(body).toHaveValue(/Practice becomes useful/);
  await page.getByRole("button", { name: "Create and write" }).click();
  await expect(page.locator(".cm-content")).toContainText("Practice becomes useful");
  await expect(page.locator(".cm-content")).toContainText(sources.trim().split("\n").filter(Boolean).join(""));
});

test("reference and switcher fit a small phone and respect reduced motion", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.emulateMedia({ reducedMotion: "reduce", colorScheme: "dark" });
  await page.goto("/n/Alpha");
  await page.locator("[data-switcher-launch]").click();
  await expect(page.locator("#switcher-input")).toBeInViewport();
  await expect(page.locator(".command-switcher")).toHaveCSS("animation-name", "none");
  await accessible(page);
  await page.keyboard.press("Escape");
  await page.locator("[data-note-body] .note-peek-trigger").first().click();
  await page.getByRole("button", { name: "Keep beside this note" }).click();
  await expect(page.getByRole("button", { name: "Close reference" })).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("graph selection reveals a passage and a connection without leaving the graph", async ({ page }) => {
  await page.goto("/graph");
  await page.locator("#graph-node-list").selectOption({ label: "Alpha" });
  await expect(page.locator("[data-graph-note-excerpt]")).not.toContainText("Loading");
  await expect(page.locator("[data-graph-note-connection] .connection-launch")).toBeVisible();
  await page.locator("[data-graph-note-connection] .connection-launch").click();
  await expect(page.locator(".connection-note h3")).toHaveCount(2);
  await page.keyboard.press("Escape");
  await expect(page).toHaveURL(/\/graph/);
});
