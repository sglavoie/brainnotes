import { expect, test } from "./fixture.js";

// Keyboard ownership, modal containment and dismissal across workspace screens.
// Every preview here is cancelled: nothing is written to the fixture vault.

const openStatusEditor = async (page) => {
  await page.goto("/properties/status");
  const row = page.locator("[data-property-row]").first();
  await expect(row).toBeVisible();
  await row.focus();
  await row.press("Enter");
  const form = page.locator("[data-property-editor-form]");
  await expect(form).toHaveCount(1);
  return { row, form };
};

test("Properties owns / for its note search and keeps the editor on a popover's Escape", async ({ page }) => {
  const { row, form } = await openStatusEditor(page);
  const input = form.locator("input").first();
  await input.fill("kept value");

  // The Workspace popover takes Escape first; the open editor and its value stay.
  const workspace = page.locator(".source-editor-workspace");
  await workspace.locator("> summary").click();
  await expect(workspace).toHaveAttribute("open", "");
  await page.keyboard.press("Escape");
  await expect(workspace).not.toHaveAttribute("open", "");
  await expect(form).toHaveCount(1);
  await expect(input).toHaveValue("kept value");

  // On a property's page / goes to that page's search, never also the header's.
  await expect(page.locator("#search-input")).not.toHaveAttribute("aria-keyshortcuts", "/");
  await expect(page.locator(".source-editor-workspace .search-shortcut-hint")).toHaveCount(0);
  await row.focus();
  await page.keyboard.press("/");
  await expect(page.locator("[data-property-filter-query]")).toBeFocused();
  await expect(workspace).not.toHaveAttribute("open", "");

  // A modified or repeated / is not the shortcut.
  await row.focus();
  await page.keyboard.press("Control+/");
  await expect(row).toBeFocused();
});

test("the property preview is a native modal that starts on Cancel", async ({ page }) => {
  await page.goto("/properties/status");
  const row = page.locator("[data-property-row]").first();
  await row.focus();
  await row.press(" ");
  const remove = page.getByRole("button", { name: "Remove property", exact: true });
  await remove.click();
  const dialog = page.locator("dialog[data-property-dialog]");
  await expect(dialog).toBeVisible();
  expect(await dialog.evaluate((element) => element.matches(":modal"))).toBe(true);
  await expect(dialog.locator("[data-property-preview-cancel]")).toBeFocused();

  // Clicking plain text inside keeps focus in the dialog; Tab cannot reach the page.
  await dialog.locator("[data-property-preview-summary]").click();
  for (let step = 0; step < 4; step += 1) {
    await page.keyboard.press("Tab");
    const inside = await dialog.evaluate((element) => element.contains(document.activeElement));
    const chrome = await page.evaluate(() => document.activeElement === document.body);
    expect(inside || chrome).toBe(true);
  }

  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(remove).toBeFocused();
  await expect(page.locator("[data-property-selection-count]")).toHaveText("1 note selected.");
});

test("the Workspace popover closes when keyboard focus leaves it", async ({ page }) => {
  await page.goto("/settings");
  const workspace = page.locator(".source-editor-workspace");
  const summary = workspace.locator("> summary");
  await summary.focus();
  await page.keyboard.press("Enter");
  await expect(workspace).toHaveAttribute("open", "");
  await page.keyboard.press("Shift+Tab");
  await expect(summary).not.toBeFocused();
  await expect(workspace).not.toHaveAttribute("open", "");
});

// Each region must already be in the accessibility tree, never inside a hidden
// ancestor, before its first message is written into it.
const persistentLiveRegions = async (page, selectors) => {
  for (const selector of selectors) {
    const region = page.locator(selector).locator("xpath=ancestor-or-self::*[@role='status'][1]");
    await expect(region, selector).toHaveCount(1);
    expect(await region.evaluate((element) => element.closest("[hidden]") === null), selector).toBe(true);
  }
};

test("shown-and-hidden status messages sit inside persistent live regions", async ({ page }) => {
  await page.goto("/properties/status");
  await expect(page.locator("[data-property-row]").first()).toBeVisible();
  await persistentLiveRegions(page, [
    "[data-property-toast]", "[data-property-config-restart]", "[data-search-feedback-message]",
  ]);
  await page.goto("/library?q=Reading&tag=absent");
  await expect(page.locator("[data-overview-empty-help]")).toBeVisible();
  await persistentLiveRegions(page, ["[data-overview-empty-help]"]);
  await page.goto("/history");
  await persistentLiveRegions(page, ["[data-history-active-filters]"]);
  await page.goto("/");
  await persistentLiveRegions(page, ["[data-capture-draft-status]"]);
});

test("an unreachable server is described in plain words, never as \"Failed to fetch\"", async ({ page }) => {
  await page.route("**/api/related?**", (route) => route.abort("connectionrefused"));
  await page.goto("/n/Reading");
  const status = page.locator("[data-related-status]");
  await expect(status).toHaveText("Couldn't reach the bn server. Check it is still running, then retry.");
  await expect(page.locator("[data-related-notes]")).not.toContainText("Failed to fetch");
});
