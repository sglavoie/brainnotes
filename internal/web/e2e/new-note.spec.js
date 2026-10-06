import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, manifest, test } from "./fixture.js";
import { caretToEnd, SURFACE } from "./editor-helpers.js";

test.use({ baseURL: manifest.hostedBaseURL });

test("hosted New note creates, opens the editor, saves and reopens", async ({ page }) => {
  await page.goto("/library");
  await page.getByRole("link", { name: "New note", exact: true }).click();
  await page.getByLabel("Title", { exact: true }).fill("Browser creation");
  await page.getByLabel("Folder (optional)").fill("New folder/Ideas");
  await expect(page.locator("[data-new-note-destination]")).toHaveText("Destination: New folder/Ideas/Browser creation.md");
  await page.getByRole("button", { name: "Create and write" }).click();
  await expect(page).toHaveURL(/\/edit\?key=/);
  await expect(page.locator(SURFACE)).toContainText("Browser creation");
  await caretToEnd(page);
  await page.keyboard.type("\n\nA full note written from the hosted browser.");
  await page.locator("[data-source-save]").click();
  await expect(page.locator("[data-source-status]")).toHaveClass(/is-success/);
  await expect(page.locator("[data-source-save]")).toBeDisabled();
  await page.reload();
  await expect(page.locator(SURFACE)).toContainText("A full note written from the hosted browser.");
  expect(readFileSync(join(manifest.hostedVault, "New folder/Ideas/Browser creation.md"), "utf8"))
    .toContain("A full note written from the hosted browser.");
});
