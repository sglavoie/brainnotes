import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, gitStatus, manifest, test } from "./fixture.js";

// Use a seeded note and restore its property through the same browser controls.
// Only the sandboxed server writes; the peer must stay byte-for-byte untouched.
test("a confirmed property edit appears after explicit refresh, retaining Library context", async ({ page }) => {
  const peerPath = join(manifest.peerVault, "Reading.md");
  const peerBefore = readFileSync(peerPath, "utf8");
  const peerStatus = gitStatus(manifest.peerVault);
  const notePath = join(manifest.localVault, "Reading.md");
  const before = readFileSync(notePath, "utf8");
  await page.goto("/n/Reading?return_to=%2Flibrary%3Fq%3DReading#first-section");
  await page.waitForLoadState("networkidle");
  const originalURL = page.url();
  const panel = page.locator("[data-frontmatter-panel]");
  const form = panel.locator('[data-frontmatter-set][data-frontmatter-property="status"][data-frontmatter-op="="]');
  const value = panel.locator('.frontmatter-row:has([data-frontmatter-property="status"]) .frontmatter-text');
  await expect(value).toHaveText("draft");
  await panel.getByLabel("Edit status", { exact: true }).click();
  await form.locator("input").fill("done");
  await form.getByRole("button", { name: "Update property", exact: true }).click();
  await expect(panel.locator("[data-frontmatter-status]")).toContainText("Updated property.");
  expect(readFileSync(notePath, "utf8")).toContain("status: done");
  await expect(value).toHaveText("draft");
  await panel.getByRole("link", { name: "Refresh properties" }).click();
  await expect(value).toHaveText("done");
  expect(page.url()).toBe(originalURL);
  await expect(page.locator("[data-note-back-results]")).toHaveAttribute("href", "/library?q=Reading");
  await page.waitForLoadState("networkidle");
  await panel.getByLabel("Edit status", { exact: true }).click();
  await form.locator("input").fill("draft");
  await form.getByRole("button", { name: "Update property", exact: true }).click();
  await expect(panel.locator("[data-frontmatter-status]")).toContainText("Updated property.");
  // Property writes serialize frontmatter keys in canonical order.
  const restored = readFileSync(notePath, "utf8");
  const [oldHeader, ...oldBody] = before.split("\n---\n");
  const [newHeader, ...newBody] = restored.split("\n---\n");
  expect(newHeader.split("\n").sort()).toEqual(oldHeader.split("\n").sort());
  expect(newBody).toEqual(oldBody);
  expect(gitStatus(manifest.localVault)).toBe("");
  expect(readFileSync(peerPath, "utf8")).toBe(peerBefore);
  expect(gitStatus(manifest.peerVault)).toBe(peerStatus);
});

for (const surface of ["library", "graph"]) {
  test(`${surface} retries expression validation without changing the search`, async ({ page }) => {
    let fail = true;
    const requests = [];
    await page.route("**/api/query/validate?**", (route) => {
      requests.push(route.request().url());
      return fail ? route.fulfill({ status: 503, body: "Unavailable" }) : route.continue();
    });
    await page.goto(`/${surface}?q=Beta&sort=title&limit=2&expr=status%3Adraft`);
    const initialURL = page.url();
    const retry = page.getByRole("button", { name: "Retry check", exact: true });
    await expect(retry).toBeVisible();
    await expect(page.locator("[data-query-bar-status]")).toHaveText("Expression check unavailable.");
    fail = false;
    await retry.focus();
    await page.keyboard.press("Enter");
    await expect(page.locator("[data-query-bar]")).toHaveAttribute("data-query-bar-phase", "valid");
    await expect(retry).toBeHidden();
    await expect(page.locator("[data-query-bar-input]")).toHaveValue("status:draft");
    expect(page.url()).toBe(initialURL);
    expect(requests).toHaveLength(2);
    expect(new Set(requests).size).toBe(1);
  });
}
