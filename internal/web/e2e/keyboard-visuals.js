import { test, expect } from "./fixture.js";

export function keyboardVisuals(snapshot) {
    test("keyboard shortcut sheet and Workspace toggle", async ({ page }) => {
      await page.goto("/n/Alpha");
      await expect(page.locator("[data-sync-line]")).toHaveText("5 incoming");
      await page.keyboard.press("?");
      await expect(page.getByRole("dialog", { name: "Keyboard shortcuts" })).toBeVisible();
      await snapshot(page, "keyboard-shortcuts");
      await page.keyboard.press("Escape");
      await page.locator(".source-editor-workspace > summary").click();
      await expect(page.getByLabel("Single-key shortcuts", { exact: true })).toBeVisible();
      await snapshot(page, "keyboard-workspace");
    });
}
