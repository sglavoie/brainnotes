import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test, expect, manifest, gitStatus } from "./fixture.js";

test("a lost settings reply keeps the input and offers History without claiming refusal", async ({ page, context }) => {
  const key = "brief.exclude_types";
  const value = "uncertain-response-fixture";
  await page.goto("/settings");
  await page.waitForLoadState("networkidle");
  const row = page.locator(`[data-settings-row="${key}"]`);
  await row.locator("[data-settings-edit]").click();
  const input = row.locator("[data-settings-control]");
  await input.fill(value);
  await row.locator("[data-settings-save]").click();
  await expect(page.locator("[data-settings-confirm]")).toBeVisible();
  // Let the real write finish, then replace only its answer with a login page.
  await page.route("**/api/settings/apply", async (route) => {
    const response = await route.fetch();
    expect(response.ok()).toBe(true);
    await route.fulfill({ status: 200, contentType: "text/html", body: "<html>Sign in</html>" });
  });
  await page.locator("[data-settings-confirm]").click();
  await expect(page.locator("[data-settings-status]")).toContainText("Change not confirmed");
  await expect(input).toHaveValue(value);
  await expect(page.locator("[data-settings-confirm]")).toBeHidden();
  expect(readFileSync(join(manifest.localVault, "brainnotes.toml"), "utf8")).toContain(value);
  const opened = context.waitForEvent("page");
  await page.locator("[data-settings-panel] .write-check-link").click();
  const history = await opened;
  await history.waitForLoadState();
  expect(new URL(history.url()).pathname).toBe("/history");
  await history.close();
  await expect(input).toHaveValue(value);
  await page.unroute("**/api/settings/apply");
  await page.locator("[data-settings-refresh]").click();
  await expect(row.locator("[data-settings-origin]")).toHaveText("brainnotes.toml");
  await row.locator("[data-settings-unset]").click();
  await page.locator("[data-settings-confirm]").click();
  await expect(page.locator("[data-settings-status]")).toContainText(`Removed "${key}"`);
  expect(readFileSync(join(manifest.localVault, "brainnotes.toml"), "utf8")).not.toContain(value);
  expect(gitStatus(manifest.localVault)).toBe("");
});

test("an unreadable template reply retains the draft and spends the preview", async ({ page }) => {
  await page.goto("/settings");
  await page.waitForLoadState("networkidle");
  await page.locator("[data-templates-new]").click();
  await page.locator("[data-templates-name]").fill("UnconfirmedTemplate");
  await page.locator("[data-templates-content]").fill("# Keep these exact bytes\n");
  await page.locator("[data-templates-save]").click();
  await expect(page.locator("[data-templates-confirm]")).toBeVisible();
  // No write reaches the fixture in this case; an unreadable reply still cannot
  // prove that to the application, so it must preserve the draft either way.
  await page.route("**/api/templates/apply", (route) => route.fulfill({ body: '{"ok":', contentType: "application/json" }));
  await page.locator("[data-templates-confirm]").click();
  await expect(page.locator("[data-templates-status]")).toContainText("Change not confirmed");
  await expect(page.locator("[data-templates-name]")).toHaveValue("UnconfirmedTemplate");
  await expect(page.locator("[data-templates-content]")).toHaveValue("# Keep these exact bytes\n");
  await expect(page.locator("[data-templates-confirm]")).toBeHidden();
  await expect(page.locator("[data-templates-panel] .write-check-link")).toBeVisible();
  await page.locator("[data-templates-status]").getByRole("button", { name: "Check outcome" }).click();
  await expect(page.locator("[data-templates-status]")).toContainText("still unconfirmed");
  expect(gitStatus(manifest.localVault)).toBe("");
});

test("leaving with an unsaved setting or template edit asks first", async ({ page }) => {
  await page.goto("/settings");
  await page.waitForLoadState("networkidle");
  const prompts = [];
  page.on("dialog", async (dialog) => {
    prompts.push(dialog.type());
    await dialog.dismiss();
  });
  const row = page.locator('[data-settings-row="brief.exclude_types"]');
  await row.locator("[data-settings-edit]").click();
  const control = row.locator("[data-settings-control]");
  const original = await control.inputValue();
  await control.fill("unsaved-unload-fixture");
  await page.goto("/library").catch(() => {});
  expect(prompts).toEqual(["beforeunload"]);
  await expect(page).toHaveURL(/\/settings/);

  await control.fill(original);
  await page.locator("[data-templates-content]").fill("# typed, never saved\n");
  await page.goto("/library").catch(() => {});
  expect(prompts).toEqual(["beforeunload", "beforeunload"]);
  await expect(page).toHaveURL(/\/settings/);
});

test("a refused settings preview offers correction without uncertain-write recovery", async ({ page }) => {
  await page.goto("/settings");
  await page.waitForLoadState("networkidle");
  const recovery = page.locator("[data-settings-panel] .write-check-link");
  await expect(recovery).toBeHidden();
  await page.route("**/api/settings/preview", (route) => route.fulfill({ status: 400, json: {
    ok: false, code: "invalid_request", message: "Choose a valid value.",
  } }));
  const row = page.locator('[data-settings-row="brief.exclude_types"]');
  await row.locator("[data-settings-edit]").click();
  await row.locator("[data-settings-control]").fill("keep this input");
  await row.locator("[data-settings-save]").click();
  await expect(page.locator("[data-settings-status]")).toHaveText("Choose a valid value.");
  await expect(recovery).toBeHidden();
  await expect(row.locator("[data-settings-control]")).toHaveValue("keep this input");
});

test("settings keep other edits through a save and refresh, with feedback beside the setting", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/settings");
  const row = page.locator('[data-settings-row="brief.exclude_types"]');
  const other = page.locator('[data-settings-row="brief.exclude_folders"]');
  await expect(row.locator("[data-settings-control]")).toBeHidden();
  await row.locator("[data-settings-edit]").click();
  await other.locator("[data-settings-edit]").click();
  await other.locator("[data-settings-control]").fill("Unfinished folder draft");
  await row.locator("[data-settings-control]").fill("settings-continuity-fixture");
  await row.locator("[data-settings-save]").click();
  await expect(row.locator("[data-settings-confirm]")).toBeFocused();
  await expect(row.locator("[data-settings-confirm]")).toBeInViewport();
  await row.locator("[data-settings-confirm]").click();
  await expect(row.locator("[data-settings-status]")).toContainText('Saved "brief.exclude_types"');
  await expect(row.locator("[data-settings-save]")).toBeFocused();
  await expect(other.locator("[data-settings-control]")).toHaveValue("Unfinished folder draft");
  await page.locator("[data-settings-refresh]").click();
  await expect(row.locator("[data-settings-status]")).toContainText("portable settings");
  await expect(other.locator("[data-settings-control]")).toHaveValue("Unfinished folder draft");
  await row.locator("[data-settings-unset]").click();
  await row.locator("[data-settings-confirm]").click();
  await expect(row.locator("[data-settings-status]")).toContainText('Removed "brief.exclude_types"');
  expect(gitStatus(manifest.localVault)).toBe("");
});
