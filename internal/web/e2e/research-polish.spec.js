import { test, expect } from "./fixture.js";

const draft = (page) => page.locator("[data-synthesize-draft]");
const title = (page) => page.locator("[data-synthesize-title]");

async function supportingNotes(page) {
  await page.getByRole("link", { name: "Sources", exact: true }).click();
  await page.getByText("Find supporting notes", { exact: true }).click();
  await expect(page.locator("[data-synthesize-add]").first()).toBeVisible();
}

test("mobile Synthesis opens on writing and keeps a draft through failed previews", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 640 });
  await page.goto("/synthesize?source=Alpha");
  await expect(draft(page)).toBeInViewport();
  await expect(title(page)).toBeInViewport();
  await expect(page.locator("[data-synthesize-agent-status]")).toBeHidden();
  await title(page).fill("A considered conclusion");
  await draft(page).fill("Words to keep after a failed preview.");
  await page.getByRole("link", { name: "Sources", exact: true }).click();
  await page.getByRole("link", { name: "Context", exact: true }).click();
  await page.locator("[data-synthesize-prepare]").click();
  await expect(page.locator("[data-synthesize-context-status]")).toContainText("Context ready");
  await page.route("**/api/synthesis/preview", (route) => route.abort());
  await page.getByRole("navigation", { name: "Synthesis sections" }).getByRole("link", { name: "Review" }).click();
  await page.locator("[data-synthesize-preview]").click();
  await expect(page.locator("[data-synthesize-preview-status]")).toHaveClass(/is-error/);
  await expect(page.locator("[data-synthesize-preview-status]")).toBeInViewport();
  await page.getByRole("link", { name: "Draft", exact: true }).click();
  await expect(draft(page)).toHaveValue("Words to keep after a failed preview.");
  await expect(draft(page)).toBeInViewport();
  await page.reload();
  await expect(draft(page)).toHaveValue("Words to keep after a failed preview.");
  await expect(page.locator("[data-synthesize-apply]")).toBeDisabled();
});

test("independent and duplicated workbenches retain both drafts across reloads", async ({ page, context }) => {
  await page.goto("/synthesize?source=Alpha");
  await draft(page).fill("First tab's original words");
  const duplicateURL = page.url();
  const second = await context.newPage();
  await second.goto(duplicateURL);
  await expect(draft(second)).toHaveValue("First tab's original words");
  await draft(second).fill("Second tab's different words");
  await draft(page).fill("First tab's revised words");
  await second.reload();
  await page.reload();
  await expect(draft(page)).toHaveValue("First tab's revised words");
  await expect(draft(second)).toHaveValue("Second tab's different words");
  await second.close();
});

test("inspecting a Synthesis source restores the draft, source focus and original search", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/library?q=Alpha");
  await page.locator(".overview-result-link").first().click();
  await page.locator(".note-actions-more > summary").click();
  await page.locator("[data-synthesize-launch]").click();
  await draft(page).fill("Research in progress");
  await supportingNotes(page);
  const candidate = page.locator(".synthesize-candidate-title a").first();
  const name = await candidate.textContent();
  await candidate.click();
  await page.getByRole("link", { name: "Back to Synthesis", exact: true }).click();
  await expect(draft(page)).toHaveValue("Research in progress");
  const returned = page.locator(".synthesize-candidate-title a").filter({ hasText: name }).first();
  await expect(returned).toBeFocused();
  await expect(returned).toBeInViewport();
  await page.getByRole("link", { name: "Back to results", exact: true }).click();
  await expect(page.locator("#search-input")).toHaveValue("Alpha");
  await expect(page.locator(".overview-result-link").first()).toBeFocused();
});

test("related note links keep the originating Library search", async ({ page }) => {
  await page.goto("/library?q=Alpha");
  await page.locator(".overview-result-link").first().click();
  const related = page.locator("[data-related-view-candidate]").first();
  await expect(related).toBeAttached();
  await related.click();
  await page.getByRole("link", { name: "Back to results", exact: true }).click();
  await expect(page.locator("#search-input")).toHaveValue("Alpha");
  await expect(page.locator(".overview-result-link").first()).toBeFocused();
});

test("History exposes clear only for active constraints and keeps the latest change reachable", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 640 });
  await page.goto("/history");
  await expect(page.locator("#history-list-status")).not.toHaveClass(/is-loading/);
  await expect(page.locator("[data-history-clear-filters]")).toBeHidden();
  await expect(page.locator("[data-history-refresh]")).toBeInViewport();
  await page.locator("[data-history-path]").fill("Alpha");
  await expect(page.locator("[data-history-clear-filters]")).toBeVisible();
  await page.locator("[data-history-clear-filters]").click();
  await expect(page.locator("[data-history-path]")).toHaveValue("");
  await expect(page.locator("[data-history-clear-filters]")).toBeHidden();
});
