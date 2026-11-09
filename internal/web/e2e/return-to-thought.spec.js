import { test, expect } from "./fixture.js";
import AxeBuilder from "@axe-core/playwright";

test("compact properties remember expansion and Library puts results near the query", async ({ page }) => {
  await page.goto("/n/Survey");
  const properties = page.locator("[data-note-properties]");
  await expect(properties).not.toHaveAttribute("open");
  await expect(page.getByRole("definition").first()).toBeVisible();
  await properties.locator(":scope > summary").press("Enter");
  await expect(properties).toHaveAttribute("open");
  await page.reload();
  await expect(properties).toHaveAttribute("open");
  await properties.locator(":scope > summary").click();
  await page.goto("/library?q=field&status=reference&sort=title");
  await expect(page.locator(".overview-result-link").first()).toBeVisible();
  expect((await page.locator(".overview-result-link").first().boundingBox()).y).toBeLessThan(360);
  await expect(page.locator("[data-library-expression] > summary")).toBeHidden();
  await page.locator(".library-filters > summary").click();
  await page.locator("[data-library-expression] > summary").click();
  await expect(page.locator("[data-query-bar-input]")).toBeVisible();
  const audit = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(audit.violations).toEqual([]);
});

test("capture expands only for composing and keeps a draft visible after blur", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  const input = page.locator("#capture-input");
  const resting = await input.boundingBox();
  expect(resting.height).toBeLessThan(50);
  await input.focus();
  expect((await input.boundingBox()).height).toBeGreaterThan(resting.height);
  await input.fill("Keep this unfinished thought visible.");
  await page.locator("h1").click();
  expect((await input.boundingBox()).height).toBeGreaterThan(resting.height);
  await page.reload();
  await expect(input).toHaveValue("Keep this unfinished thought visible.");
  expect((await input.boundingBox()).height).toBeGreaterThan(resting.height);
});

test("a new tab restores the passage and reference, and forgetting leaves drafts alone", async ({ page, context }) => {
  await page.goto("/n/Reading");
  await page.locator("#second-section").evaluate((el) => window.scrollTo(0, scrollY + el.getBoundingClientRect().top - 60));
  await page.keyboard.press("ControlOrMeta+k");
  await page.locator("#switcher-input").fill("Beta");
  await expect(page.locator(".command-switcher [role=status]")).toContainText(/results?/);
  await page.locator("#switcher-input").press("Alt+Enter");
  await expect(page.locator(".reference-content h3")).toHaveText("Beta");
  await page.goto("/library");
  const next = await context.newPage();
  await next.goto("/n/Reading");
  await expect(next.locator(".reference-content h3")).toHaveText("Beta");
  await expect.poll(async () => (await next.locator("#second-section").boundingBox()).y).toBeLessThan(100);
  await next.evaluate(() => { window.scrollTo(0, 0); sessionStorage.setItem("bn.test-draft", "keep me"); });
  await next.locator(".source-editor-workspace > summary").click();
  await next.getByRole("button", { name: "Forget remembered places" }).click();
  await expect(next.locator(".side-reference")).toBeHidden();
  expect(await next.evaluate(() => sessionStorage.getItem("bn.test-draft"))).toBe("keep me");
  await next.reload();
  await expect(next.locator(".side-reference")).toHaveCount(0);
  await expect(next.locator(".reading-title")).toBeInViewport();
  await next.close();
});

test("opening a side reference keeps the desktop header on one row", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/n/Alpha");
  await page.locator("[data-note-body] .note-peek-trigger").first().click();
  await page.getByRole("button", { name: "Keep beside this note" }).click();
  await expect(page.locator(".reference-content h3")).toHaveText("Beta");
  expect((await page.locator(".site-header").boundingBox()).height).toBeLessThan(60);
  const edit = await page.getByRole("link", { name: "Edit", exact: true }).boundingBox();
  const search = await page.locator("[data-switcher-launch]").boundingBox();
  expect(Math.abs(edit.y - search.y)).toBeLessThan(10);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("a lost connection-save response stays recoverable and completion keeps the source pair", async ({ page }) => {
  await page.goto("/n/Alpha");
  await page.locator(".related-card .connection-launch").first().click();
  const dialog = page.getByRole("dialog", { name: "Where these ideas meet" });
  await dialog.getByRole("button", { name: "Write a thought about this connection" }).click();
  await dialog.getByLabel("Your thought", { exact: true }).fill("Observation changes the next question. A second sentence.");
  await dialog.getByRole("button", { name: "Save thought…", exact: true }).click();
  await expect(dialog.getByLabel("Title", { exact: true })).toHaveValue("Observation changes the next question");
  await expect(dialog).toContainText("Creates a note linked to both sources.");
  const requests = [];
  await page.route("**/api/notes/new", async (route) => {
    requests.push(route.request().postData());
    const response = await route.fetch();
    if (requests.length === 1) await route.abort("failed");
    else await route.fulfill({ response });
  });
  await dialog.getByRole("button", { name: "Save thought", exact: true }).click();
  await expect(dialog.locator("[data-new-note-status]")).toContainText("Creation not confirmed");
  await expect(dialog.locator(".connection-saved")).toHaveCount(0);
  await dialog.getByRole("button", { name: "Check creation", exact: true }).click();
  await expect(dialog.locator(".connection-saved")).toContainText("Thought saved, linked to both sources.");
  expect(requests).toHaveLength(2);
  expect(requests[1]).toBe(requests[0]);
  await expect(dialog.locator(".connection-note h3")).toHaveCount(2);
  await expect(dialog.locator(".connection-saved-thought")).toHaveText("Observation changes the next question. A second sentence.");
  await dialog.getByRole("button", { name: "Keep exploring" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator(".related-card .connection-launch").first()).toBeFocused();
});
