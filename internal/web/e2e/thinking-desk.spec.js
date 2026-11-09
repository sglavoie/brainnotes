import { test, expect } from "./fixture.js";
import AxeBuilder from "@axe-core/playwright";

test("confirmed capture settles without hiding recovery or moving focus", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  const input = page.locator("#capture-input");
  const resting = await input.boundingBox();
  await input.fill("A small thought worth keeping.");
  await input.press("ControlOrMeta+Enter");
  await expect(page.locator(".capture-panel .capture-status")).toContainText("Captured");
  await expect(input).toBeFocused();
  await expect(input).toHaveValue("");
  expect((await input.boundingBox()).height).toBe(resting.height);
  await expect(page.locator(".capture-history")).not.toHaveAttribute("open");
  await expect(page.locator(".capture-panel [data-capture-view-inbox]")).toBeVisible();
  await page.locator(".capture-history summary").click();
  await expect(page.locator(".capture-recent")).toContainText("A small thought worth keeping.");
  await page.route("**/api/capture", (route) => route.abort());
  await input.fill("Keep my text if capture fails.");
  await input.press("ControlOrMeta+Enter");
  await expect(page.locator(".capture-panel .capture-status")).toContainText("not confirmed");
  await expect(input).toHaveValue("Keep my text if capture fails.");
});

test("Library excerpts and exact dates survive a persisted density change", async ({ page }) => {
  await page.goto("/library");
  const excerpt = page.locator(".recent-note-excerpt").first();
  await expect(excerpt).toBeVisible();
  const date = page.locator("[data-recent-time]").first();
  await expect(date).toHaveAttribute("datetime", /^\d{4}-/);
  await expect(date).toHaveAttribute("title", /\d{4}-\d{2}-\d{2}/);
  await page.getByRole("button", { name: "Compact list" }).click();
  await expect(excerpt).toBeHidden();
  await page.reload();
  await expect(page.getByRole("button", { name: "Compact list" })).toHaveAttribute("aria-pressed", "true");
  await expect(excerpt).toBeHidden();
  await page.getByRole("button", { name: "Compact list" }).click();
  await expect(excerpt).toBeVisible();
  const audit = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(audit.violations).toEqual([]);
});

test("reading type and passage follow into writing without changing the source", async ({ page }) => {
  await page.goto("/n/Reading");
  const typography = await page.locator("[data-note-body]").evaluate((node) => {
    const style = getComputedStyle(node);
    return [style.fontFamily, style.fontSize, style.lineHeight];
  });
  await page.locator("#second-section").evaluate((node) => window.scrollTo(0, scrollY + node.getBoundingClientRect().top - 60));
  await page.locator("[data-source-editor-launch]").evaluate((link) => link.click());
  const editor = page.locator(".cm-content");
  await expect(editor).toBeVisible();
  await expect(editor.locator(".resume-passage")).toBeVisible();
  expect(await editor.evaluate((node) => {
    const style = getComputedStyle(node);
    return [style.fontFamily, style.fontSize, style.lineHeight];
  })).toEqual(typography);
  await expect(page.locator(".cm-scroller")).not.toHaveJSProperty("scrollTop", 0);
  await expect(page.locator("[data-source-save]")).toBeDisabled();
  await page.getByLabel("Writing layout").selectOption("preview");
  expect(await page.locator("[data-source-preview]").evaluate((node) => {
    const style = getComputedStyle(node);
    return [style.fontFamily, style.fontSize, style.lineHeight];
  })).toEqual(typography);
  await page.getByLabel("Writing layout").selectOption("write");
  await page.getByLabel("Writing type").selectOption("sans");
  await page.locator("[data-source-note-return]").click();
  await expect(page.locator("#second-section")).toBeInViewport();
  await expect(page.locator("html")).toHaveAttribute("data-reading-type", "");
});

test("Continue reflects only recorded passages and previews both connection sources", async ({ page }) => {
  await page.goto("/");
  const card = page.locator('article[data-brief-slot="continue"] [data-brief-card]');
  await expect(card.locator(".brief-card-context")).toContainText("Edited ");
  await card.locator(".brief-card-title a").click();
  await expect(page.locator("[data-note-body]")).toBeVisible();
  await page.goto("/");
  await expect(card.locator("[data-brief-action-open]")).toHaveText("Continue here");
  await expect(card.locator(".brief-card-context")).toContainText(/Continue at|last passage/);
  const pair = await briefConnection(page);
  await expect(pair.locator("blockquote")).toHaveCount(2);
  await expect(pair.locator("cite").first()).not.toBeEmpty();
  await page.locator('article[data-brief-slot="connect"] [data-brief-action-open]').click();
  await expect(page.locator(".connection-note h3")).toHaveCount(2);
});

test("long connection passages wrap within a narrow phone", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.route("**/api/note/peek?*", async (route) => {
    const response = await route.fetch();
    const note = await response.json();
    note.passages = [{ text: "attachment-".repeat(70), heading: false, relevant: true }];
    await route.fulfill({ response, json: note });
  });
  await page.goto("/");
  await briefConnection(page);
  await expect(page.locator(".brief-connection-passages blockquote")).toHaveCount(2);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

async function briefConnection(page) {
  // Earlier journeys can make a note without connections the Continue choice.
  // Use the real Another flow until the fixture offers a connected source.
  const source = page.locator('article[data-brief-slot="continue"] [data-brief-card]');
  for (let attempt = 0; attempt < 20 && await page.locator('article[data-brief-slot="connect"] [data-brief-card]').count() === 0; attempt++) {
    const id = await source.getAttribute("data-brief-card-id");
    await source.locator("[data-brief-action-another]").click();
    await expect(source).not.toHaveAttribute("data-brief-card-id", id);
  }
  const pair = page.locator(".brief-connection-passages");
  await expect(pair.locator("blockquote")).toHaveCount(2);
  return pair;
}

for (const motion of ["reduce", "no-preference"]) test(`Graph Focus preserves nodes with ${motion} motion`, async ({ page }) => {
  await page.emulateMedia({ reducedMotion: motion });
  await page.goto("/graph?mode=focus");
  const list = page.locator("#graph-node-list");
  await expect(list.locator("option").first()).toBeAttached();
  await page.locator(".graph-search-mode-option").filter({ hasText: "Focus" }).click();
  await expect(page.getByLabel("Focus", { exact: true })).toBeChecked();
  const positions = await page.locator("#graph-canvas").evaluate((node) => node._cyreg.cy.nodes().map((item) => ({ id: item.id(), ...item.position() })));
  await list.selectOption({ label: "Alpha" });
  await expect(page.locator("#graph-open-note")).toHaveAttribute("aria-disabled", "false");
  await expect.poll(() => page.locator("#graph-canvas").evaluate((node) => {
    const cy = node._cyreg.cy;
    return Math.abs(cy.nodes(".is-selected").renderedPosition().x - cy.width() / 2);
  })).toBeLessThan(1);
  const state = await page.locator("#graph-canvas").evaluate((node) => {
    const cy = node._cyreg.cy;
    const selected = cy.nodes(".is-selected");
    return { animated: cy.animated(), x: selected.renderedPosition().x, center: cy.width() / 2,
      positions: cy.nodes().map((item) => ({ id: item.id(), ...item.position() })) };
  });
  expect(state.animated).toBe(false);
  expect(state.x).toBeCloseTo(state.center, 0);
  expect(state.positions).toEqual(positions);
});

for (const note of ["Alpha", "Reading"]) test(`the ${note} title stays aligned when entering the editor`, async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto(`/n/${note}`);
  const title = await page.locator(".reading-title").boundingBox();
  await page.locator("[data-source-editor-launch]").click();
  await expect(page.locator(".cm-content")).toBeVisible();
  const writingTitle = await page.locator("#source-editor-heading a").boundingBox();
  expect(Math.abs(title.x - writingTitle.x)).toBeLessThan(40);
  expect(Math.abs(title.height - writingTitle.height)).toBeLessThan(5);
});
