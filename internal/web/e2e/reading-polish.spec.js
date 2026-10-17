import { expect, test } from "./fixture.js";

test("Library sort preserves the query and cap and changes the actual results", async ({ page }) => {
  await page.goto("/library?q=body&status=draft&limit=2");
  const sort = page.getByRole("combobox", { name: "Sort results" });
  await sort.selectOption("title");
  await expect(page.locator("[data-overview-search-status]")).toContainText("Showing 2 of");
  await expect(page.locator(".overview-result-title")).toHaveText(["Alpha", "Beta"]);
  const params = new URL(page.url()).searchParams;
  expect(params.get("q")).toBe("body");
  expect(params.get("status")).toBe("draft");
  expect(params.get("limit")).toBe("2");
  await page.reload();
  await expect(sort).toHaveValue("title");
  await sort.selectOption("-title");
  await expect(page.locator(".overview-result-title")).toHaveText(["Unrelated", "Gamma"]);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator("[data-library-expression] > summary").click();
  await page.getByText("Query help", { exact: true }).click();
  await expect(page.locator(".query-help")).toContainText("A note must match all three");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("Contents follows reading without changing focus or the URL", async ({ page }) => {
  await page.goto("/n/Reading");
  const body = page.locator("[data-note-body]");
  const insight = page.locator("[data-note-insight]");
  expect((await body.boundingBox()).y).toBeLessThan((await insight.boundingBox()).y);
  const first = page.locator('.note-toc-list a').first();
  const second = page.locator('.note-toc-list a').nth(1);
  await first.focus();
  const initialURL = page.url();
  const historyLength = await page.evaluate(() => history.length);
  await page.locator("#second-section").evaluate((heading) => heading.scrollIntoView());
  await expect(second).toHaveAttribute("aria-current", "location");
  await expect(first).not.toHaveAttribute("aria-current");
  await expect(first).toBeFocused();
  expect(page.url()).toBe(initialURL);
  expect(await page.evaluate(() => history.length)).toBe(historyLength);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("syntax-highlighted code copies without labels and falls back to selected text", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", { configurable: true,
      value: { writeText: (text) => { window.copiedCode = text; return Promise.resolve(); } } });
  });
  await page.goto("/n/Reading");
  const code = page.locator("[data-note-body] pre code");
  const expected = 'package main\n\nfunc main() {\n\tprintln("hello")\n}\n';
  await expect(code.locator("span").first()).toBeAttached();
  await page.getByRole("button", { name: "Copy code", exact: true }).click();
  expect(await page.evaluate(() => window.copiedCode)).toBe(expected);
  await page.evaluate(() => { navigator.clipboard.writeText = () => Promise.reject(new Error("refused")); });
  await page.getByRole("button", { name: "Copy code", exact: true }).click();
  const fallback = page.locator("[data-code-copy] [data-copy-fallback]");
  await expect(fallback).toBeFocused();
  await expect(fallback).toHaveValue(expected);
  expect(await fallback.evaluate((field) => field.value.slice(field.selectionStart, field.selectionEnd))).toBe(expected);
});

test("section links navigate to the same heading and fit on narrow screens", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", { configurable: true,
      value: { writeText: (text) => { window.copiedSection = text; return Promise.resolve(); } } });
  });
  await page.goto("/n/Reading");
  const button = page.locator('#second-section + [data-section-copy] button');
  await button.focus();
  await button.press("Enter");
  await expect(page.locator('#second-section + [data-section-copy] [data-copy-status]')).toHaveText("Section link copied.");
  const url = await page.evaluate(() => window.copiedSection);
  expect(url).toBe(`${new URL(page.url()).origin}/n/Reading#second-section`);
  await page.goto(url);
  await expect(page.locator('.note-toc-list a').nth(1)).toHaveAttribute("aria-current", "location");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(() => { navigator.clipboard.writeText = () => Promise.reject(new Error("refused")); });
  await button.click();
  const fallback = page.locator('[data-section-copy] [data-copy-fallback]');
  await expect(fallback).toBeFocused();
  await expect(fallback).toHaveValue(url);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
