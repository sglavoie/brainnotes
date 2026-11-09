import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test, expect, manifest } from './fixture.js';

for (const width of [1280, 390]) {
  test(`search and write-first creation keep an idea moving at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto('/n/Survey');
    await page.locator('[data-switcher-launch]').click();
    const search = page.locator('#switcher-input');
    await search.fill('An unwritten Café idea');
    await expect(page.locator('.command-switcher [role="status"]')).toContainText('No matches');
    await expect(page.getByRole('button', { name: 'Keep beside · Alt + Enter' })).toBeHidden();
    await expect(page.getByRole('link', { name: 'Search in Library', exact: true })).toHaveAttribute('href', '/library?q=An+unwritten+Caf%C3%A9+idea');
    await page.getByRole('link', { name: 'Create “An unwritten Café idea”', exact: true }).click();
    await expect(page.getByLabel('Title', { exact: true })).toHaveValue('An unwritten Café idea');
    await expect(page.getByLabel('Your thought (optional)', { exact: true })).toBeFocused();
    await page.goto('/new');
    const body = page.getByLabel('Your thought (optional)', { exact: true });
    await body.fill(`# A more inviting first sentence ${width}\n\nThere is room to think here.`);
    await expect(page.getByLabel('Title', { exact: true })).toHaveValue(`A more inviting first sentence ${width}`);
    expect((await body.boundingBox()).y).toBeLessThan((await page.getByLabel('Title', { exact: true }).boundingBox()).y);
    await page.reload();
    await expect(body).toHaveValue(`# A more inviting first sentence ${width}\n\nThere is room to think here.`);
    await page.getByRole('button', { name: 'Create and write', exact: true }).click();
    await expect(page.locator('[data-source-save]')).toBeDisabled();
    const source = page.locator('.cm-content');
    await expect(source).toContainText('There is room to think here.');
    await expect(source).not.toContainText(`# A more inviting first sentence ${width}`);
    if (width < 961) await page.locator('.source-editor-format > summary').click();
    await page.getByLabel('Writing layout', { exact: true }).selectOption('source');
    await expect(source).toContainText(`# A more inviting first sentence ${width}`);
    await expect(page.locator('[data-source-save]')).toBeDisabled();
    await page.getByLabel('Writing layout', { exact: true }).selectOption('write');
    await expect(source).not.toContainText(`# A more inviting first sentence ${width}`);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}


test('passage thoughts follow moved prose and renamed sources, and leave repeated quotations unplaced', async ({ page }) => {
  const title = 'A source for a passage trail';
  const quote = 'A precise observation deserves a place in the next experiment.';
  await page.goto('/new');
  await page.getByLabel('Your thought (optional)', { exact: true }).fill(quote);
  await page.getByLabel('Title', { exact: true }).fill(title);
  await page.getByRole('button', { name: 'Create and write', exact: true }).click();
  await expect(page.locator('.cm-content')).toContainText(quote);
  await page.locator('[data-source-note-return]').click();
  await page.locator('[data-note-body] p').first().evaluate((element) => {
    const range = document.createRange(); range.selectNodeContents(element);
    getSelection().removeAllRanges(); getSelection().addRange(range);
  });
  await page.getByRole('button', { name: 'Write a thought', exact: true }).click();
  await page.getByLabel('Your thought', { exact: true }).fill('An anchored observation follows its source.');
  await page.getByRole('button', { name: 'Save thought…', exact: true }).click();
  await page.getByRole('button', { name: 'Save thought', exact: true }).click();
  await page.getByRole('button', { name: 'Back to your passage', exact: true }).click();
  const marker = page.locator('.passage-annotation-group');
  await expect(marker).toBeVisible();
  await page.locator('[data-source-editor-launch]').click();
  const original = readFileSync(join(manifest.localVault, `${title}.md`), 'utf8');
  await replaceSource(page, original.replace(quote, `New introductory prose.\n\n${quote}`));
  await page.locator('[data-source-note-return]').click();
  await expect(marker).toBeVisible();
  await page.locator('[data-source-editor-launch]').click();
  await page.locator('[data-source-manage] > summary').click();
  await page.getByText('Rename note', { exact: true }).click();
  await page.locator('[data-source-rename-title]').fill('A renamed passage source');
  await page.locator('[data-source-rename]').click();
  await expect.poll(() => new URL(page.url()).searchParams.get('key')?.toLowerCase()).toBe('a renamed passage source');
  await page.locator('[data-source-note-return]').click();
  await expect(marker).toBeVisible();
  expect(readFileSync(join(manifest.localVault, 'An anchored observation follows its source.md'), 'utf8')).toContain('[[A renamed passage source]]');
  await page.locator('[data-source-editor-launch]').click();
  const renamed = readFileSync(join(manifest.localVault, 'A renamed passage source.md'), 'utf8');
  await replaceSource(page, renamed.replace(quote, `${quote}\n\n${quote}`));
  await page.locator('[data-source-note-return]').click();
  await expect(marker).toHaveCount(0);
  await expect(page.getByRole('region', { name: 'Thoughts from earlier passages' })).toContainText('An anchored observation follows its source');
});

async function replaceSource(page, text) {
  const editor = page.locator('.cm-content');
  await editor.click();
  await editor.press('ControlOrMeta+a');
  await page.keyboard.insertText(text);
  await page.locator('[data-source-save]').click();
  await expect(page.locator('[data-source-save]')).toBeDisabled();
  await expect(page.locator('[data-source-status]')).toHaveClass(/is-success/);
}
