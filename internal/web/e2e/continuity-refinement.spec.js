import { test, expect } from "./fixture.js";
import { SURFACE } from "./editor-helpers.js";

test("Library keeps checked notes through inspection and revalidates them on return", async ({ page }) => {
  await page.goto("/library?q=body&sort=title");
  const checks = page.locator(".overview-result-select input");
  await expect(checks.first()).toBeVisible();
  await checks.first().check();
  const key = await checks.first().inputValue();
  await page.locator(".overview-result-link").first().click();
  await page.getByRole("link", { name: "Back to results", exact: true }).click();
  await expect(checks.first()).toBeChecked();
  await expect(page.locator("[data-overview-selected-count]")).toHaveText("1 selected");
  await expect(page.locator("[data-overview-bulk-preview]")).toBeHidden();
  await page.reload();
  await expect(checks.first()).toBeChecked();
  // Model a note that no longer belongs to the search while it is being read.
  await page.locator(".overview-result-link").first().click();
  await page.route("**/api/search?**", (route) => {
    const url = new URL(route.request().url());
    return url.searchParams.getAll("selected").includes(key)
      ? route.fulfill({ json: [] }) : route.continue();
  });
  await page.getByRole("link", { name: "Back to results", exact: true }).click();
  await expect(checks.first()).not.toBeChecked();
  await expect(page.locator("[data-overview-bulk]")).toBeHidden();
});

test("reading opens the relevant source passage and mobile pane switches keep it", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/n/Reading");
  const heading = page.locator("#second-section");
  await heading.evaluate((node) => window.scrollTo(0, node.getBoundingClientRect().top + scrollY - 50));
  const line = Number(await heading.getAttribute("data-source-line"));
  await page.locator("[data-source-editor-launch]").evaluate((link) => link.click());
  await expect(page.locator(SURFACE)).toBeVisible();
  const scroller = page.locator(".cm-scroller");
  await expect.poll(() => scroller.evaluate((node) => node.scrollTop)).toBeGreaterThan(1000);
  await expect(page.locator("[data-source-preview] [data-source-line]").first()).toBeAttached();
  const before = await scroller.evaluate((node) => node.scrollTop);
  await page.getByRole("tab", { name: "Preview", exact: true }).click();
  const preview = page.locator("[data-source-preview]");
  await expect.poll(() => preview.evaluate((node) => node.scrollTop)).toBeGreaterThan(1000);
  const activeLine = await preview.evaluate((node) => {
    const top = node.getBoundingClientRect().top;
    const blocks = [...node.querySelectorAll("[data-source-line]")];
    return Number(blocks.findLast((block) => block.getBoundingClientRect().top <= top + 10)?.dataset.sourceLine);
  });
  expect(Math.abs(activeLine - line)).toBeLessThan(6);
  await page.getByRole("tab", { name: "Edit", exact: true }).click();
  await expect.poll(async () => Math.abs(await scroller.evaluate((node) => node.scrollTop) - before)).toBeLessThan(50);
  await expect(page.locator("[data-source-save]")).toBeDisabled();
});

for (const width of [390, 1280]) test(`Graph keyboard navigation and layout controls at ${width}px`, async ({ page }) => {
  await page.setViewportSize({ width, height: width === 390 ? 640 : 900 });
  await page.goto("/graph");
  const list = page.getByLabel("Notes and folders in this view");
  await expect(list.locator("option").first()).toBeAttached();
  await expect(page.locator(".graph-refine-options")).not.toHaveAttribute("open");
  if (width === 390) await expect(page.locator("#graph-canvas")).toBeInViewport({ ratio: 1 });
  await expect(page.locator("#graph-spread-input")).toBeHidden();
  await list.focus();
  await page.keyboard.press("Home");
  await page.keyboard.press("ArrowDown");
  await expect(page.locator("#graph-open-note")).toHaveAttribute("aria-disabled", "false");
  const selected = await list.inputValue();
  const canvas = page.locator("#graph-canvas");
  await canvas.focus();
  await page.keyboard.press("ArrowDown");
  await expect(list).not.toHaveValue(selected);
  await page.keyboard.press("Escape");
  await expect(page.locator("#graph-open-note")).toHaveAttribute("aria-disabled", "true");
  await page.locator(".graph-refine-options > summary").click();
  await page.getByText("Graph layout", { exact: true }).click();
  await expect(page.locator("#graph-spread-input")).toBeVisible();
  await page.locator("#graph-collapse-input").focus();
  await page.keyboard.press("Home");
  await page.keyboard.press("ArrowRight");
  await expect(page.locator(".graph-refine-options > summary")).toContainText("active");
  const folder = list.locator('option').filter({ hasText: "Expand folder: Notes" });
  await expect(folder).toHaveCount(1);
  await list.selectOption({ label: "Expand folder: Notes" });
  await expect(folder).toHaveCount(0);
  await expect(page.locator("#graph-open-note")).toHaveAttribute("aria-disabled", "true");
  await page.locator("#graph-filter-input").fill("Beta");
  await expect(list.locator("option")).toHaveCount(3);
  await page.locator(".graph-refine-options > summary").click();
  await expect(page.locator(".graph-refine-options > summary")).toContainText("active");
  await page.reload();
  await expect(page.locator(".graph-refine-options")).toHaveAttribute("open", "");
  await expect(list.locator("option")).toHaveCount(3);
  await list.selectOption("beta");
  await page.locator("#graph-open-note").click();
  await expect(page.locator("[data-note-back-results]")).toHaveText("Back to Graph");
});

test("wide reading keeps prose bounded with metadata shown or hidden", async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1000 });
  await page.goto("/n/Reading");
  const paragraph = page.locator("[data-note-body] p").first();
  await paragraph.evaluate((node) => { node.textContent = "A long paragraph that remains comfortable to read. ".repeat(80); });
  for (const hidden of [false, true]) {
    await page.evaluate((hide) => { document.documentElement.dataset.noteMetaSidebar = hide ? "hidden" : "visible"; }, hidden);
    const box = await paragraph.boundingBox();
    const characterWidth = await paragraph.evaluate((node) => {
      const style = getComputedStyle(node);
      const context = document.createElement("canvas").getContext("2d");
      context.font = `${style.fontSize} ${style.fontFamily}`;
      return context.measureText("0").width;
    });
    expect(box.width / characterWidth).toBeLessThanOrEqual(75);
    expect(box.width).toBeGreaterThan(300);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  }
});
