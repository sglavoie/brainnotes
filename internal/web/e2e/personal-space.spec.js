import { readFileSync } from "node:fs";
import { join } from "node:path";
import AxeBuilder from "@axe-core/playwright";
import { test, expect, manifest } from "./fixture.js";

const selectPassage = async (page) => {
  const passage = page.locator("[data-note-body] p").filter({ hasText: "These measurements describe" }).first();
  await passage.scrollIntoViewIfNeeded();
  return passage.evaluate((element) => {
    const range = document.createRange(); range.selectNodeContents(element);
    const selection = getSelection(); selection.removeAllRanges(); selection.addRange(range);
    return { text: selection.toString(), y: scrollY };
  });
};

for (const width of [1280, 390]) {
  test(`a selected passage becomes a linked thought and returns to reading at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/n/Survey");
    await page.locator(".note-actions-more > summary").click();
    await expect(page.getByRole("button", { name: "Quiet reading", exact: true })).toBeVisible();
    await page.locator(".note-actions-more > summary").click();
    const sourceBefore = readFileSync(join(manifest.localVault, "Survey.md"), "utf8");
    const selected = await selectPassage(page);
    await page.getByRole("button", { name: "Write a thought", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Follow this thought" });
    await expect(dialog.locator("blockquote")).toHaveText(selected.text);
    const thought = dialog.getByLabel("Your thought", { exact: true });
    await thought.fill(`A better next observation ${width}. Keep the explanation provisional.`);
    // Dismissing the composer preserves this passage's unfinished thought.
    await page.keyboard.press("Escape");
    await expect(page.locator("[data-note-body]")).toBeFocused();
    expect(Math.abs(await page.evaluate(() => scrollY) - selected.y)).toBeLessThan(2);
    await page.getByRole("button", { name: "Write a thought", exact: true }).click();
    await expect(thought).toHaveValue(`A better next observation ${width}. Keep the explanation provisional.`);
    const audit = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
    expect(audit.violations).toEqual([]);
    await dialog.getByRole("button", { name: "Save thought…", exact: true }).click();
    await expect(dialog.getByLabel("Title", { exact: true })).toHaveValue(`A better next observation ${width}`);
    await dialog.getByRole("button", { name: "Save thought", exact: true }).click();
    await expect(dialog.locator(".connection-saved")).toContainText("Thought saved with your quotation and source.");
    await expect(dialog.locator(".thought-source-links a")).toHaveAttribute("href", "/n/survey");
    const saved = readFileSync(join(manifest.localVault, `A better next observation ${width}.md`), "utf8");
    expect(saved).toContain("From [[Survey]]");
    expect(saved).toContain(`> ${selected.text}`);
    expect(saved).toContain(`A better next observation ${width}. Keep the explanation provisional.`);
    expect(readFileSync(join(manifest.localVault, "Survey.md"), "utf8")).toBe(sourceBefore);
    await dialog.getByRole("button", { name: "Back to your passage" }).click();
    await expect(dialog).toHaveCount(0);
    expect(Math.abs(await page.evaluate(() => scrollY) - selected.y)).toBeLessThan(2);
    await expect.poll(() => page.evaluate(() => getSelection().toString())).toBe(selected.text);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const marker = page.locator('.passage-annotation-group').filter({ hasText: `A better next observation ${width}` });
    await expect(marker).toBeVisible();
    await marker.locator('summary').click();
    await marker.locator('[data-passage-annotation]').filter({ hasText: `A better next observation ${width}` }).getByRole('button', { name: 'Read beside this passage' }).click();
    await expect(page.locator('.side-reference')).toContainText(`A better next observation ${width}`);
    await page.getByRole('button', { name: 'Close reference', exact: true }).click();
    await page.reload();
    await expect(marker).toBeVisible();

  });
}

test("passage creation checks a lost response after reload without duplicating the note", async ({ page }) => {
  await page.goto("/n/Survey");
  await selectPassage(page);
  await page.getByRole("button", { name: "Write a thought", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Follow this thought" });
  await dialog.getByLabel("Your thought", { exact: true }).fill("An observation survives an uncertain save.");
  await dialog.getByRole("button", { name: "Save thought…", exact: true }).click();
  const requests = [];
  await page.route("**/api/notes/new", async (route) => {
    requests.push(route.request().postData());
    const response = await route.fetch();
    expect(response.ok()).toBe(true);
    if (requests.length === 1) await route.abort("failed");
    else { expect((await response.json()).replayed).toBe(true); await route.fulfill({ response }); }
  });
  await dialog.getByRole("button", { name: "Save thought", exact: true }).click();
  await expect(dialog.locator("[data-new-note-status]")).toContainText("Creation not confirmed");
  await page.reload();
  await selectPassage(page);
  await page.getByRole("button", { name: "Write a thought", exact: true }).click();
  await dialog.getByRole("button", { name: "Check creation", exact: true }).click();
  await expect(dialog.locator(".connection-saved")).toBeVisible();
  expect(requests).toHaveLength(2); expect(requests[1]).toBe(requests[0]);
  await expect(page.locator('[data-passage-annotation]').filter({ hasText: 'An observation survives an uncertain save.' })).toHaveCount(1);
});

test("capture confirms in place only after a successful response", async ({ page }) => {
  await page.goto("/");
  const input = page.locator("[data-capture-input]");
  const button = page.locator("[data-capture-submit]");
  await input.fill("A precise little confirmation.");
  const before = await button.boundingBox();
  await button.click();
  await expect(button).toHaveText("Captured ✓");
  expect((await button.boundingBox()).width).toBe(before.width);
  await input.fill("The next thought.");
  await expect(button).toHaveText("Capture");
  await page.route("**/api/capture", (route) => route.fulfill({ status: 503, json: { error: "Unavailable" } }));
  await button.click();
  await expect(page.locator("[data-capture-status]")).toHaveClass(/is-error/);
  await expect(button).not.toHaveText("Captured ✓");
  await expect(input).toHaveValue("The next thought.");
});

test("connection and keyboard passage writing share one creation module", async ({ page }) => {
  await page.goto("/n/Alpha");
  await page.locator('[data-related-card="beta"] .connection-launch').click();
  await page.getByRole("button", { name: "Write a thought about this connection" }).click();
  await expect(page.getByLabel("Your thought", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.locator("[data-note-body]").focus();
  await page.locator("[data-note-body] p").first().evaluate((element) => {
    const range = document.createRange(); range.selectNodeContents(element);
    getSelection().removeAllRanges(); getSelection().addRange(range);
  });
  await expect(page.getByRole("button", { name: "Write a thought", exact: true })).toBeVisible();
  await page.keyboard.press("Alt+Enter");
  await expect(page.getByRole("dialog", { name: "Follow this thought" })).toBeVisible();
  await expect(page.getByLabel("Your thought", { exact: true })).toBeFocused();
  await expect(page.locator('script[src="/static/connection-writing.js"]')).toHaveCount(1);
});
