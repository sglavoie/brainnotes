import { expectPassagePlacement } from "./passage-placement.js";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import AxeBuilder from "@axe-core/playwright";
import { test, expect, manifest } from "./fixture.js";

const selectPassage = async (page) => {
  const passage = page.locator("[data-note-body] p").filter({ hasText: "These measurements describe" }).first();
  await passage.scrollIntoViewIfNeeded();
  return passage.evaluate((element) => {
    const range = document.createRange(); range.selectNodeContents(element);
    const selection = getSelection(); selection.removeAllRanges(); selection.addRange(range);
    return { text: selection.toString(), y: scrollY };
  });
};

for (const width of [1280, 390]) {
  test(`a selected passage becomes a linked thought and returns to reading at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/n/Survey");
    await page.locator(".note-actions-more > summary").click();
    await expect(page.getByRole("button", { name: "Quiet reading", exact: true })).toBeVisible();
    await page.locator(".note-actions-more > summary").click();
    const sourceBefore = readFileSync(join(manifest.localVault, "Survey.md"), "utf8");
    const selected = await selectPassage(page);
    await expectPassagePlacement(page);
    await page.getByRole("button", { name: "Write a thought", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Follow this thought" });
    await expect(dialog.locator("blockquote")).toHaveText(selected.text);
    const thought = dialog.getByLabel("Your thought", { exact: true });
    await thought.fill(`A better next observation ${width}. Keep the explanation provisional.`);
    // Dismissing the composer preserves this passage's unfinished thought.
    await page.keyboard.press("Escape");
    await expect(page.locator("[data-note-body]")).toBeFocused();
    expect(Math.abs(await page.evaluate(() => scrollY) - selected.y)).toBeLessThan(2);
    await page.getByRole("button", { name: "Write a thought", exact: true }).click();
    await expect(thought).toHaveValue(`A better next observation ${width}. Keep the explanation provisional.`);
    const audit = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
    expect(audit.violations).toEqual([]);
    await dialog.getByRole("button", { name: "Save thought…", exact: true }).click();
    await expect(dialog.getByLabel("Title", { exact: true })).toHaveValue(`A better next observation ${width}`);
    await dialog.getByRole("button", { name: "Save thought", exact: true }).click();
    await expect(dialog.locator(".connection-saved")).toContainText("Thought saved with your quotation and source.");
    await expect(dialog.locator(".thought-source-links a")).toHaveAttribute("href", "/n/survey");
    const saved = readFileSync(join(manifest.localVault, `A better next observation ${width}.md`), "utf8");
    expect(saved).toBe(`---\ntitle: A better next observation ${width}\n---\n\nFrom [[Survey]]\n\n> ${selected.text}\n\nA better next observation ${width}. Keep the explanation provisional.`);
    expect(saved).toContain(`> ${selected.text}`);
    expect(saved).toContain(`A better next observation ${width}. Keep the explanation provisional.`);
    expect(readFileSync(join(manifest.localVault, "Survey.md"), "utf8")).toBe(sourceBefore);
    await dialog.getByRole("button", { name: "Back to your passage" }).click();
    await expect(dialog).toHaveCount(0);
    expect(Math.abs(await page.evaluate(() => scrollY) - selected.y)).toBeLessThan(2);
    await expect.poll(() => page.evaluate(() => getSelection().toString())).toBe(selected.text);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const marker = page.locator('.passage-annotation-group').filter({ hasText: `A better next observation ${width}` });
    await expect(marker).toBeVisible();
    await marker.locator('summary').click();
    await marker.locator('[data-passage-annotation]').filter({ hasText: `A better next observation ${width}` }).getByRole('button', { name: 'Read beside this passage' }).click();
    await expect(page.locator('.side-reference')).toContainText(`A better next observation ${width}`);
    await page.getByRole('button', { name: 'Close reference', exact: true }).click();
    await page.reload();
    await expect(marker).toBeVisible();

  });
}

test("passage creation checks a lost response after reload without duplicating the note", async ({ page }) => {
  await page.goto("/n/Survey");
  await selectPassage(page);
  await page.getByRole("button", { name: "Write a thought", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Follow this thought" });
  await dialog.getByLabel("Your thought", { exact: true }).fill("An observation survives an uncertain save.");
  await dialog.getByRole("button", { name: "Save thought…", exact: true }).click();
  const requests = [];
  await page.route("**/api/notes/new", async (route) => {
    requests.push(route.request().postData());
    const response = await route.fetch();
    expect(response.ok()).toBe(true);
    if (requests.length === 1) await route.abort("failed");
    else { expect((await response.json()).replayed).toBe(true); await route.fulfill({ response }); }
  });
  await dialog.getByRole("button", { name: "Save thought", exact: true }).click();
  await expect(dialog.locator("[data-new-note-status]")).toContainText("Creation not confirmed");
  await page.reload();
  await selectPassage(page);
  await page.getByRole("button", { name: "Write a thought", exact: true }).click();
  await dialog.getByRole("button", { name: "Check creation", exact: true }).click();
  await expect(dialog.locator(".connection-saved")).toBeVisible();
  expect(requests).toHaveLength(2); expect(requests[1]).toBe(requests[0]);
  await expect(page.locator('[data-passage-annotation]').filter({ hasText: 'An observation survives an uncertain save.' })).toHaveCount(1);
});

test("capture confirms in place only after a successful response", async ({ page }) => {
  await page.goto("/");
  const input = page.locator("[data-capture-input]");
  const button = page.locator("[data-capture-submit]");
  await input.fill("A precise little confirmation.");
  const before = await button.boundingBox();
  await button.click();
  await expect(button).toHaveText("Captured ✓");
  expect((await button.boundingBox()).width).toBe(before.width);
  await input.fill("The next thought.");
  await expect(button).toHaveText("Capture");
  await page.route("**/api/capture", (route) => route.fulfill({ status: 503, json: { error: "Unavailable" } }));
  await button.click();
  await expect(page.locator("[data-capture-status]")).toHaveClass(/is-error/);
  await expect(button).not.toHaveText("Captured ✓");
  await expect(input).toHaveValue("The next thought.");
});

test("connection and keyboard passage writing share one creation module", async ({ page }) => {
  await page.goto("/n/Alpha");
  await page.locator('[data-related-card="beta"] .connection-launch').click();
  await page.getByRole("button", { name: "Write a thought about this connection" }).click();
  await expect(page.getByLabel("Your thought", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.locator("[data-note-body]").focus();
  await page.locator("[data-note-body] p").first().evaluate((element) => {
    const range = document.createRange(); range.selectNodeContents(element);
    getSelection().removeAllRanges(); getSelection().addRange(range);
  });
  await expect(page.getByRole("button", { name: "Write a thought", exact: true })).toBeVisible();
  await page.keyboard.press("Alt+Enter");
  await expect(page.getByRole("dialog", { name: "Follow this thought" })).toBeVisible();
  await expect(page.getByLabel("Your thought", { exact: true })).toBeFocused();
  await expect(page.locator('script[src="/static/connection-writing.js"]')).toHaveCount(1);
});

test("desktop passage tools follow multiline selections, scroll and viewport edges", async ({ page }) => {
  await page.goto('/n/Survey');
  await selectPassage(page);
  await expectPassagePlacement(page);
  await page.evaluate(() => {
    const rect = getSelection().getRangeAt(0).getBoundingClientRect();
    scrollBy(0, rect.top - 20);
  });
  await expectPassagePlacement(page);
  await expect.poll(() => page.evaluate(() => document.querySelector('.passage-tools').getBoundingClientRect().top >= getSelection().getRangeAt(0).getBoundingClientRect().bottom)).toBe(true);
  await page.evaluate(() => scrollBy(0, -200));
  await expectPassagePlacement(page);
  await expect.poll(() => page.evaluate(() => document.querySelector('.passage-tools').getBoundingClientRect().bottom <= getSelection().getRangeAt(0).getBoundingClientRect().top)).toBe(true);
  await page.locator('[data-note-body]').evaluate((body) => {
    const passage = document.createElement('p');
    passage.textContent = 'A long selection keeps all its words visible. '.repeat(100);
    body.append(passage); passage.scrollIntoView();
    const range = document.createRange(); range.selectNodeContents(passage);
    getSelection().removeAllRanges(); getSelection().addRange(range);
  });
  await expectPassagePlacement(page);
  await expect(page.locator('.passage-tools')).toHaveAttribute('data-side', 'true');
});

test("Library title is the sole transition source at click time and cancellation restores it", async ({ page }) => {
  await page.goto('/library?q=Alpha');
  const link = page.locator('.overview-result-link').first();
  await expect(link).toBeVisible();
  const names = await link.evaluate((element) => {
    let names;
    element.addEventListener('click', (event) => {
      names = [...document.querySelectorAll('*')].filter((node) => getComputedStyle(node).viewTransitionName === 'thought-title').map((node) => node.className);
      event.preventDefault();
    }, { once: true });
    element.click();
    return names;
  });
  expect(names).toEqual(['overview-result-title']);
  await expect(link.locator('.overview-result-title')).toHaveCSS('view-transition-name', 'none');
  await link.click();
  await expect(page.locator('.reading-title')).toBeVisible();
  await expect(page.locator('.reading-title')).toHaveCSS('view-transition-name', 'thought-title');
});

test("a saved passage opens from its confirmed title and links back to the source", async ({ page }) => {
  await page.goto('/n/Survey');
  const sourceTitle = await page.locator(".reading-title").innerText();
  await selectPassage(page);
  await page.getByRole('button', { name: 'Write a thought', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Follow this thought' });
  await dialog.getByLabel('Your thought', { exact: true }).fill('A visible arrival connects the next observation.');
  await dialog.getByRole('button', { name: 'Save thought…', exact: true }).click();
  await dialog.getByRole('button', { name: 'Save thought', exact: true }).click();
  await expect(dialog.locator('.connection-saved [role=status]')).toBeInViewport();
  await dialog.getByRole('link', { name: 'Open note', exact: true }).evaluate((link) => {
    link.addEventListener('click', () => {
      sessionStorage.setItem('passage-title-source', JSON.stringify({
        title: getComputedStyle(document.querySelector('.connection-saved h3')).viewTransitionName,
        source: getComputedStyle(document.querySelector('.reading-title')).viewTransitionName,
      }));
    }, { once: true });
  });
  await dialog.getByRole('link', { name: 'Open note', exact: true }).click();
  await expect(page.locator('.reading-title')).toHaveText('A visible arrival connects the next observation');
  expect(await page.evaluate(() => JSON.parse(sessionStorage.getItem('passage-title-source')))).toEqual({ title: 'thought-title', source: 'none' });
  await page.locator('[data-note-body] a').filter({ hasText: 'Survey' }).click();
  await expect(page.locator('.reading-title')).toHaveText(sourceTitle);
});

test("reduced motion keeps passage controls and note navigation still", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/n/Survey');
  await selectPassage(page);
  await expect(page.locator('.passage-tools')).toHaveCSS('animation-name', 'none');
  await page.getByRole('button', { name: 'Write a thought', exact: true }).click();
  await expect(page.locator('.passage-thought')).toHaveCSS('animation-name', 'none');
  await page.goto('/library?q=Alpha');
  const link = page.locator('.overview-result-link').first();
  await expect(link).toBeVisible();
  const name = await link.evaluate((element) => {
    let name;
    element.addEventListener('click', (event) => {
      name = getComputedStyle(element.querySelector('.overview-result-title')).viewTransitionName;
      event.preventDefault();
    }, { once: true });
    element.click(); return name;
  });
  expect(name).toBe('none');
});

test("switcher keeps its chosen title visible until note navigation", async ({ page }) => {
  await page.goto('/n/Alpha');
  await page.locator('[data-switcher-launch]').click();
  await page.locator('#switcher-input').fill('Beta');
  const option = page.getByRole('option').filter({ has: page.locator('.switcher-result-title', { hasText: /^Beta$/ }) }).first();
  await expect(option).toBeVisible();
  await page.evaluate(() => document.addEventListener('bn:title-navigation', (event) => {
    const title = event.detail;
    sessionStorage.setItem('switcher-title-source', JSON.stringify({
      name: getComputedStyle(title).viewTransitionName,
      visible: title.checkVisibility(),
      source: getComputedStyle(document.querySelector('.reading-title')).viewTransitionName,
      display: getComputedStyle(title).display,
    }));
  }));
  await page.locator('#switcher-input').press('Enter');
  await expect(page.locator('.reading-title')).toHaveText('Beta');
  expect(await page.evaluate(() => JSON.parse(sessionStorage.getItem('switcher-title-source')))).toEqual({ name: 'thought-title', visible: true, source: 'none', display: 'block' });
  await page.goBack();
  await expect(page.locator('.reading-title')).toHaveText('Alpha');
  await expect(page.locator('.command-switcher[open]')).toHaveCount(0);
  await expect(page.locator('.reading-title')).toHaveCSS('view-transition-name', 'thought-title');
});

test("a viewport-filling selection keeps its keyboard route and reveals the bar on focus", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 640 });
  await page.goto('/n/Survey');
  await page.locator('[data-note-body]').focus();
  await page.locator('[data-note-body]').evaluate((body) => {
    const passage = document.createElement('p');
    passage.textContent = 'Every word in this long selection remains readable. '.repeat(80);
    body.append(passage); passage.scrollIntoView();
    const range = document.createRange(); range.selectNodeContents(passage);
    getSelection().removeAllRanges(); getSelection().addRange(range);
  });
  const bar = page.locator('.passage-tools');
  await expect(bar).toHaveCSS('opacity', '0');
  await page.getByRole('button', { name: 'Write a thought', exact: true }).focus();
  await expectPassagePlacement(page);
  await expect(bar).toHaveCSS('opacity', '1');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog', { name: 'Follow this thought' })).toBeVisible();
});
