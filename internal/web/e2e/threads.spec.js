import { readFileSync } from "node:fs";
import { join } from "node:path";
import AxeBuilder from "@axe-core/playwright";
import { test, expect, manifest } from "./fixture.js";
import { caretToEnd, SURFACE } from "./editor-helpers.js";

const createThread = async (page, title) => {
  await page.goto("/threads");
  const details = page.locator("details").filter({ has: page.locator("[data-new-note]") });
  if ((await details.getAttribute("open")) === null) await details.locator("summary").click();
  await page.getByLabel("Thread title", { exact: true }).fill(title);
  await page.getByLabel("Question", { exact: true }).fill("Which ideas survive a difficult week?");
  await page.getByRole("button", { name: "Create thread", exact: true }).click();
  await expect(page).toHaveURL(/\/edit\?key=/);
  await expect(page.locator(SURFACE)).toContainText("Which ideas survive");
  await expect(page.locator("[data-thread-material]")).toContainText("No passages yet");
};

const collect = async (page, source, title, group = "Evidence", selected = true) => {
  await page.goto(`/n/${source}`);
  if (selected) {
    await page.locator("[data-note-body] p").first().evaluate((element) => {
      const range = document.createRange(); range.selectNodeContents(element);
      const selection = window.getSelection(); selection.removeAllRanges(); selection.addRange(range);
    });
    await page.locator(".passage-tools").getByRole("button", { name: "Add to thread", exact: true }).click();
  } else {
    await page.locator(".note-actions-more > summary").click();
    await page.locator("[data-thread-collect]").click();
  }
  const dialog = page.getByRole("dialog", { name: "Add to thread" });
  await dialog.getByLabel("Thread", { exact: true }).selectOption({ label: title });
  await dialog.getByLabel("Group", { exact: true }).selectOption(group);
  await dialog.getByLabel("Why this matters (optional)").fill(`A reason to keep ${source}.`);
  await dialog.getByRole("button", { name: "Add to thread", exact: true }).click();
  return dialog;
};

for (const width of [1280, 390]) {
  test(`three passages and a paragraph resume from the vault at ${width}`, async ({ page, browser }) => {
    await page.setViewportSize({ width, height: 844 });
    const title = `A lasting inquiry ${width}`;
    await createThread(page, title);
    for (const [source, group, selected] of [["Survey", "Evidence", true], ["Alpha", "Counterexamples", true], ["Beta", "Still unclear", false]]) {
      const dialog = await collect(page, source, title, group, selected);
      await expect(dialog).toContainText("Added to thread.");
    }
    await page.getByRole("link", { name: "Open thread", exact: true }).click();
    let material = page.locator("[data-thread-material]");
    await expect(material.locator("article")).toHaveCount(3);
    await material.locator("article").nth(1).getByRole("button", { name: "Move earlier" }).click();
    await expect(material.locator("article").first()).toContainText("Alpha");
    await caretToEnd(page);
    await page.keyboard.type("A paragraph that should follow me to another device.");
    await page.locator("[data-source-save]").click();
    await expect(page.locator("[data-source-status]")).toHaveClass(/is-success/);
    await expect(page.locator("[data-source-save]")).toBeDisabled();
    const savedURL = page.url();
    // Close the original page and resume in a separate browser context: no
    // cookies, drafts, positions, or collection state are shared.
    const origin = new URL(savedURL).origin;
    const other = await browser.newContext({ baseURL: origin, viewport: { width, height: 844 } });
    await other.route("**/*", (route) => new URL(route.request().url()).origin === origin ? route.continue() : route.abort("blockedbyclient"));
    await page.close();
    try {
    page = await other.newPage();
    material = page.locator("[data-thread-material]");
    await page.goto("/");
    await page.getByRole("region", { name: "Resume a thread" }).getByRole("link", { name: title, exact: true }).click();
    await expect(page).toHaveURL((url) => url.searchParams.get("key") === new URL(savedURL).searchParams.get("key"));
    await expect(material.locator("article")).toHaveCount(3);
    await expect(material.locator("article").first()).toContainText("Alpha");
    await expect(page.locator(SURFACE)).toContainText("A paragraph that should follow me");
    const saved = readFileSync(join(manifest.localVault, `Threads/${title}.md`), "utf8");
    expect(saved).toContain("From [[Survey]]"); expect(saved).toContain("From [[Beta]]");
    expect(saved).toContain("A reason to keep Alpha.");
    expect(saved).toContain("Which ideas survive a difficult week?");
    expect(saved.match(/<!-- bn:thread:item /g)).toHaveLength(3);
    await page.screenshot({ path: test.info().outputPath(`threads-editor-${width}.png`), fullPage: true });
    const audit = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
    expect(audit.violations).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.evaluate(() => { document.documentElement.dataset.theme = "dark"; localStorage.setItem("bn.theme", "dark"); });
    await page.screenshot({ path: test.info().outputPath(`threads-editor-dark-${width}.png`), fullPage: true });
    const darkAudit = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
    expect(darkAudit.violations).toEqual([]);
    await material.getByRole("button", { name: "Open in synthesis", exact: true }).click();
    await expect(page.locator("[data-thread-synthesis-status]")).toContainText("Collected sources selected");
    await expect(page.locator("[data-synthesize-source]")).toHaveCount(4);
    await page.screenshot({ path: test.info().outputPath(`threads-synthesis-${width}.png`), fullPage: true });
    } finally { await other.close(); }
  });
}

test("a lost addition response replays after reload without duplicating the passage", async ({ page }) => {
  const title = "Recoverable collection";
  await createThread(page, title);
  const requests = [];
  await page.route("**/api/source/save", async (route) => {
    requests.push(route.request().postData());
    const response = await route.fetch(); expect(response.ok()).toBe(true);
    if (requests.length === 1) await route.abort("failed");
    else { expect((await response.json()).replayed).toBe(true); await route.fulfill({ response }); }
  });
  let dialog = await collect(page, "Alpha", title);
  await expect(dialog).toContainText("Addition not confirmed");
  await page.reload();
  await page.locator("[data-note-body] p").first().evaluate((element) => {
    const range = document.createRange(); range.selectNodeContents(element);
    const selection = window.getSelection(); selection.removeAllRanges(); selection.addRange(range);
  });
  await page.locator(".passage-tools").getByRole("button", { name: "Add to thread", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "Add to thread" });
  await dialog.getByRole("button", { name: "Check addition", exact: true }).click();
  await expect(dialog).toContainText("Added to thread.");
  expect(requests).toHaveLength(2); expect(requests[0]).toBe(requests[1]);
  const saved = readFileSync(join(manifest.localVault, `Threads/${title}.md`), "utf8");
  expect(saved.match(/<!-- bn:thread:item /g)).toHaveLength(1);
});
