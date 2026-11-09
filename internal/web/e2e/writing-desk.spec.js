import { test, expect } from "./fixture.js";
import AxeBuilder from "@axe-core/playwright";

const accessible = async (page) => {
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(result.violations, JSON.stringify(result.violations, null, 2)).toEqual([]);
};

const stableSync = async (page) => {
  await page.route("**/api/sync/status", async (route) => {
    const response = await route.fetch();
    const status = await response.json();
    await route.fulfill({ json: { ...status, headline: "conflict", needs_person: true,
      review_path: "/sync", peer: { ...status.peer, conflicts: 5 } } });
  });
};

test("a fresh editor starts with prose and reversible frontmatter folding, respecting saved preferences", async ({ page }) => {
  await page.goto("/edit?key=Alpha");
  const editor = page.locator(".cm-content");
  await expect(page.getByLabel("Writing layout")).toHaveValue("write");
  await expect(page.getByLabel("Writing type")).toHaveValue("prose");
  await expect(page.locator("[data-source-pane=preview]")).toBeHidden();
  await expect(editor).toContainText("Alpha body");
  await expect(editor).not.toContainText("title: Alpha");
  const show = page.getByRole("button", { name: "Show frontmatter in source editor" });
  await show.focus();
  await show.press("Enter");
  await expect(editor).toContainText("title: Alpha");
  const hide = page.getByRole("button", { name: "Hide frontmatter in source editor" });
  await hide.focus();
  await hide.press("Enter");
  await expect(show).toBeFocused();
  await expect(editor).not.toContainText("title: Alpha");
  await expect(page.locator("[data-source-save]")).toBeDisabled();
  await accessible(page);
  await page.getByLabel("Writing layout").selectOption("split");
  await page.getByLabel("Writing type").selectOption("mono");
  await page.reload();
  await expect(page.getByLabel("Writing layout")).toHaveValue("split");
  await expect(page.getByLabel("Writing type")).toHaveValue("mono");
  await expect(page.locator("[data-source-pane=preview]")).toBeVisible();
});

test("writing layouts and typography preserve unsaved text and remain accessible", async ({ page }) => {
  await page.goto("/edit?key=Writing");
  const editor = page.locator(".cm-content");
  await expect(editor).toBeVisible();
  await editor.fill("# A thought\n\nKeep these words and `inline code`.\n\n```js\nconst answer = 42;\n```\n");
  await page.getByLabel("Writing layout").selectOption("write");
  await page.getByLabel("Writing type").selectOption("prose");
  await expect(page.locator("[data-source-pane=preview]")).toBeHidden();
  await expect(editor).toContainText("Keep these words");
  const code = editor.locator(".cm-code").first();
  await expect(code).toBeVisible();
  expect(await code.evaluate((el) => getComputedStyle(el).fontFamily)).not.toBe(await editor.evaluate((el) => getComputedStyle(el).fontFamily));
  await accessible(page);
  await page.getByLabel("Writing layout").selectOption("preview");
  await expect(page.locator("[data-source-preview]")).toContainText("Keep these words");
  await expect(editor).toBeHidden();
  await page.getByLabel("Writing layout").selectOption("split");
  await expect(editor).toContainText("Keep these words");
  await page.getByLabel("Writing layout").selectOption("write");
  await page.goto("/edit?key=Beta");
  await expect(page.getByLabel("Writing layout")).toHaveValue("write");
  await expect(page.getByLabel("Writing type")).toHaveValue("prose");
  await expect(page.locator("[data-source-save]")).toBeInViewport();
  await page.getByLabel("Writing layout").selectOption("preview");
  await page.setViewportSize({ width: 390, height: 640 });
  await page.reload();
  await expect(page.getByRole("tab", { name: "Preview", exact: true })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("tab", { name: "Edit", exact: true }).click();
  await expect(editor).toBeVisible();
  await expect(page.getByRole("tab", { name: "Edit", exact: true })).toHaveAttribute("aria-selected", "true");
});

test("search excerpts highlight literal text and keep a reference beside unsaved writing", async ({ page }) => {
  await page.goto("/edit?key=Writing");
  const editor = page.locator(".cm-content");
  await editor.fill("# Writing\n\nAn unsaved thought.");
  await page.route("**/api/search?**", (route) => route.fulfill({ json: [
    { key: "Beta", path: "/n/Beta", title: "Beta", folder: "Notes", snippet: "A <mark>thought</mark> with &lt;script&gt;literal markup&lt;/script&gt;." },
  ] }));
  await editor.press("ControlOrMeta+k");
  await page.locator("#switcher-input").fill("thought");
  await expect(page.locator(".switcher-excerpt mark")).toHaveText("thought");
  await expect(page.locator(".switcher-excerpt")).toContainText("<script>literal markup</script>");
  await accessible(page);
  await page.locator("#switcher-input").press("Alt+Enter");
  await expect(page.getByRole("complementary", { name: "Reference note" })).toContainText("Beta");
  await expect(editor).toContainText("An unsaved thought.");
  await page.getByRole("button", { name: "Close reference" }).click();
  await expect(editor).toBeFocused();
});

test("reference resizing remembers width and a phone can collapse it", async ({ page }) => {
  await page.goto("/n/Alpha");
  const link = page.locator('[data-note-body] a[href^="/n/beta"]').first();
  await link.focus(); await link.press("Alt+ArrowDown");
  await page.getByRole("button", { name: "Keep beside this note" }).click();
  const handle = page.getByRole("separator", { name: "Resize reference" });
  await handle.focus(); await handle.press("ArrowLeft");
  const width = await handle.getAttribute("aria-valuenow");
  await page.reload();
  await expect(handle).toHaveAttribute("aria-valuenow", width);
  await accessible(page);
  await page.setViewportSize({ width: 390, height: 640 });
  await page.getByRole("button", { name: "Collapse reference" }).click();
  await expect(page.locator(".reference-content")).toBeHidden();
  expect((await page.locator(".side-reference").boundingBox()).height).toBeLessThan(125);
  await accessible(page);
  await page.getByRole("button", { name: "Expand reference" }).click();
  await expect(page.locator(".reference-content")).toBeVisible();
});

test("reading reaches prose early and keeps heading links beside long headings", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 640 });
  await stableSync(page);
  await page.goto("/n/Survey");
  await expect(page.locator(".sync-line-review")).toBeVisible();
  const first = page.locator("[data-note-body] p").first();
  expect((await first.boundingBox()).y).toBeLessThan(390);
  const heading = page.locator(".note-section-heading").filter({ hasText: "What changed along the north bank" });
  const box = await heading.locator("h2").boundingBox();
  const button = await heading.locator(".copy-button").boundingBox();
  expect(button.y).toBeLessThan(box.y + box.height);
  await page.getByRole("link", { name: "Edit", exact: true }).focus();
  await accessible(page);
});

test("opening and resizing a reference preserves the visible reading passage", async ({ page }) => {
  await stableSync(page);
  await page.goto("/n/Reading");
  await expect(page.locator(".sync-line-review")).toBeVisible();
  const passage = page.locator("#second-section");
  await passage.evaluate((node) => window.scrollTo(0, scrollY + node.getBoundingClientRect().top - 60));
  const before = (await passage.boundingBox()).y;
  await page.keyboard.press("ControlOrMeta+k");
  await page.locator("#switcher-input").fill("Beta");
  await expect(page.locator(".command-switcher [role=status]")).toContainText(/results?/);
  await page.locator("#switcher-input").press("Alt+Enter");
  await expect(page.locator(".reference-content h3")).toHaveText("Beta");
  expect(Math.abs((await passage.boundingBox()).y - before)).toBeLessThan(10);
  const handle = page.getByRole("separator", { name: "Resize reference" });
  await handle.focus(); await handle.press("ArrowLeft");
  expect(Math.abs((await passage.boundingBox()).y - before)).toBeLessThan(10);
  await page.getByRole("button", { name: "Close reference" }).click();
  expect(Math.abs((await passage.boundingBox()).y - before)).toBeLessThan(10);
});
