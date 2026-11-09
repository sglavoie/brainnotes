import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test, expect, manifest } from "./fixture.js";

// Runs in each hosted engine and viewport, including native modal focus and
// selection behavior that the Chromium-only regression journey cannot prove.
test("hosted passage writing preserves its source and returns the selection", async ({ page }, testInfo) => {
  await page.goto("/n/Survey");
  const source = readFileSync(join(manifest.hostedVault, "Survey.md"), "utf8");
  const text = await page.locator("[data-note-body] p").first().evaluate((element) => {
    const range = document.createRange(); range.selectNodeContents(element);
    getSelection().removeAllRanges(); getSelection().addRange(range);
    return getSelection().toString();
  });
  await page.getByRole("button", { name: "Write a thought", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Follow this thought" });
  await expect(dialog.locator("blockquote")).toHaveText(text);
  await dialog.getByLabel("Your thought", { exact: true }).fill("Return at the same hour to make the comparison useful.");
  await dialog.getByRole("button", { name: "Save thought…", exact: true }).click();
  const title = `Passage from ${testInfo.project.name}`;
  await dialog.getByLabel("Title", { exact: true }).fill(title);
  await dialog.getByRole("button", { name: "Save thought", exact: true }).click();
  await expect(dialog.locator(".connection-saved")).toBeVisible();
  const saved = readFileSync(join(manifest.hostedVault, `${title}.md`), "utf8");
  expect(saved).toContain(`> ${text}`);
  expect(saved).toContain("From [[Survey]]");
  expect(readFileSync(join(manifest.hostedVault, "Survey.md"), "utf8")).toBe(source);
  await dialog.getByRole("button", { name: "Back to your passage" }).click();
  await expect(dialog).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => getSelection().toString())).toBe(text);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
