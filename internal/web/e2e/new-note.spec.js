import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, manifest, test } from "./fixture.js";
import { caretToEnd, SURFACE } from "./editor-helpers.js";

test.use({ baseURL: manifest.hostedBaseURL });

test("hosted New note creates, opens the editor, saves and reopens", async ({ page }) => {
  await page.goto("/library");
  await page.getByRole("link", { name: "New note", exact: true }).click();
  await page.getByLabel("Folder (optional)").focus();
  await expect(page.locator('#bn-folder-suggestions option[value="Notes"]')).toHaveCount(1);
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
  await page.locator("[data-source-manage] > summary").click();
  await page.locator("[data-source-move-folder]").focus();
  await expect(page.locator('#bn-folder-suggestions option[value="New folder/Ideas"]')).toHaveCount(1);
  expect(readFileSync(join(manifest.hostedVault, "New folder/Ideas/Browser creation.md"), "utf8"))
    .toContain("A full note written from the hosted browser.");
});

test("empty search carries the title into a reviewable form without creating a note", async ({ page }) => {
  const title = "Unwritten café & idea";
  const path = join(manifest.hostedVault, `${title}.md`);
  await page.goto(`/library?${new URLSearchParams({ q: title, folder: "Notes" })}`);
  await page.getByRole("link", { name: `Create a note titled “${title}”…` }).click();
  await expect(page.getByLabel("Title", { exact: true })).toHaveValue(title);
  await expect(page.getByLabel("Folder (optional)")).toHaveValue("");
  await expect(page.locator("[data-new-note-destination]")).toHaveText(`Destination: ${title}.md`);
  expect(existsSync(path)).toBe(false);
});

test("creation keeps its Library search through editing, reading and reload", async ({ page }) => {
  const title = "Search continuity note";
  const search = `/library?${new URLSearchParams({ q: title, folder: "Notes", status: "reference", sort: "-modified" })}`;
  await page.goto(search);
  await page.locator("[data-empty-create-note]").click();
  await expect(page.getByRole("link", { name: "Back to results" })).toHaveAttribute("href", search);
  await page.getByRole("button", { name: "Create and write" }).click();
  await expect(page.locator(SURFACE)).toContainText(title);
  await caretToEnd(page);
  await page.keyboard.type("\n\nCreated outside the original folder filter.");
  await page.locator("[data-source-save]").click();
  await expect(page.locator("[data-source-status]")).toHaveClass(/is-success/);
  await page.reload();
  await page.locator("[data-source-note-return]").click();
  await expect(page.getByRole("link", { name: "Back to results" })).toHaveAttribute("href", search);
  await page.getByRole("link", { name: "Back to results" }).click();
  await expect(page).toHaveURL(manifest.hostedBaseURL + search);
  await expect(page.locator("[data-overview-search-status]")).toContainText(/no|0/i);
});

test("a lost creation reply survives reload and checks the original receipt", async ({ page }) => {
  await page.goto("/new");
  await page.getByLabel("Title", { exact: true }).fill("Creation receipt recovery");
  await page.getByLabel("Folder (optional)").fill("Recovery");
  const requests = [];
  await page.route("**/api/notes/new", async (route) => {
    requests.push(new URLSearchParams(route.request().postData()).get("save_id"));
    const response = await route.fetch();
    expect(response.ok()).toBe(true);
    if (requests.length === 1) await route.abort("failed");
    else {
      expect((await response.json()).replayed).toBe(true);
      await route.fulfill({ response });
    }
  });
  await page.getByRole("button", { name: "Create and write" }).click();
  await expect(page.locator("[data-new-note-status]")).toContainText("Creation not confirmed");
  await page.reload();
  await expect(page.getByLabel("Title", { exact: true })).toHaveValue("Creation receipt recovery");
  await expect(page.getByLabel("Folder (optional)")).toHaveValue("Recovery");
  await page.getByRole("button", { name: "Check creation", exact: true }).click();
  await expect(page.locator(SURFACE)).toContainText("Creation receipt recovery");
  expect(requests).toHaveLength(2);
  expect(requests[0]).toBe(requests[1]);
  await page.goto("/new");
  await expect(page.getByLabel("Title", { exact: true })).toHaveValue("");
});
