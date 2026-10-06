import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, manifest, test } from "./fixture.js";
import { openEditor } from "./editor-helpers.js";

for (const width of [1280, 390]) {
  test(`editor capture keeps local feedback and blocks repeats at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await openEditor(page, "/n/Beta", "Beta body");
    await page.locator("[data-source-manage] > summary").click();
    const group = page.locator('[data-source-manage-feedback="capture"]');
    const input = group.locator("textarea");
    const button = group.getByRole("button", { name: "Capture", exact: true });
    const status = group.locator('[role="status"]');
    const thought = `management capture ${width} ${manifest.fixtureRoot.split("/").pop()}`;
    await input.fill(thought);
    await group.scrollIntoViewIfNeeded();

    let release;
    const pending = new Promise((resolve) => { release = resolve; });
    let requests = 0;
    let committed = false;
    await page.route("**/api/capture", async (route) => {
      requests++;
      const response = await route.fetch();
      expect(response.ok()).toBe(true);
      committed = true;
      await pending;
      // The write completed, but the browser never receives the answer.
      await route.abort("failed");
    });
    try {
      await button.click();
      await expect.poll(() => committed).toBe(true);
      await expect(status).toHaveText("Capture this line…");
      await expect(status).toBeInViewport();
      await expect(button).toBeDisabled();
      await expect(page.locator("[data-source-rename]")).toBeDisabled();
      await expect(page.locator("[data-source-undo]")).toBeDisabled();
      await button.dispatchEvent("click");
      await page.locator("[data-source-undo]").dispatchEvent("click");
      expect(requests).toBe(1);
    } finally {
      release();
    }
    await expect(status).toContainText("Check your inbox before retrying");
    await expect(status).toBeInViewport();
    await expect(input).toHaveValue(thought);
    await expect(button).toBeEnabled();
    await expect(page.locator("[data-source-rename]")).toBeEnabled();
    await expect(page.locator('[data-source-manage-feedback="rename"] [role="status"]')).toBeEmpty();
    expect(requests).toBe(1);
    const inbox = readFileSync(join(manifest.localVault, "Inbox.md"), "utf8");
    expect(inbox.split(thought)).toHaveLength(2);
  });
}
