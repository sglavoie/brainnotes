import { test, expect } from "./fixture.js";
import { graphVisuals } from "./graph-visuals.js";
import { fixedHistory, fixedRecentNotes, reviewVisuals } from "./review-visuals.js";
import { SURFACE } from "./editor-helpers.js";

// Every key surface covers both themes at both widths; theme and viewport
// regressions cannot hide behind a paired-only matrix. Behavioral journeys use real API responses;
// Confirmation uses a fixed preview; Brief copy is fixed because its recommendations
// rotate by date. The real selection and Another flow are covered by journeys.
for (const [theme, width] of [["light", 1280], ["dark", 390], ["light", 390], ["dark", 1280]]) {
  test.describe(`${theme} ${width}`, () => {
    test.use({ viewport: { width, height: 900 } });
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("bn.theme", value), theme);
    });
    const snapshot = async (page, name) => {
      await page.evaluate(() => document.fonts.ready);
      await page.mouse.move(0, 0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await expect(page).toHaveScreenshot(`${name}-${theme}-${width}.png`, {
        animations: "disabled", caret: "hide", maxDiffPixelRatio: 0.001,
        mask: [page.locator(".recent-note-meta"), page.locator(".brief-panel time")],
      });
    };
    reviewVisuals(snapshot);
    graphVisuals(snapshot);
    for (const [name, path] of [["new-note", "/new"], ["pinned", "/pinned"], ["synthesis", "/synthesize?source=Alpha"]]) {
      test(`${name} focused workspace`, async ({ page }) => {
        await page.goto(path);
        await expect(page.locator(".focused-workspace-header")).toBeVisible();
        await expect(page.locator("[data-sync-line]")).toContainText("Review 5 incoming changes");
        await snapshot(page, name);
      });
    }
    test("Library invitation and recently modified notes", async ({ page }) => {
      await fixedHistory(page);
      await page.goto("/library");
      await expect(page.locator("[data-overview-search-status]")).toContainText("Search by title or text");
      await expect(page.locator(".recent-note-title").first()).toBeVisible();
      await fixedRecentNotes(page);
      await expect(page.locator("[data-history-panel]")).toHaveAttribute("aria-busy", "false");
      await expect(page.locator("[data-sync-line]")).toContainText("Review 5 incoming changes");
      await snapshot(page, "library-start");
    });
    test("Library dense results and a long title", async ({ page }) => {
      await page.goto("/library?q=field&status=reference&sort=title");
      await expect(page.locator(".overview-result-link").first()).toBeVisible();
      const first = await page.locator(".overview-result-link").first().boundingBox();
      expect(first.y).toBeLessThan(500);
      await snapshot(page, "library-entry");
      await page.locator("[data-overview-search-results]").scrollIntoViewIfNeeded();
      await snapshot(page, "library");
    });
    test("Brief recommendations on entry", async ({ page }) => {
      await page.goto("/");
      await expect(page.locator("[data-brief-card]")).toHaveCount(3);
      await page.locator("[data-brief-card]").evaluateAll((cards) => {
        const titles = ["Field journal", "A small experiment in remembering what we read", "Connections worth revisiting"];
        const excerpts = [
          "The river is lower today. Compare the exposed roots with last month's sketches before drawing conclusions.",
          "One question at the end of a chapter can be more useful than a page of highlights.",
          "Both notes ask how repeated observation changes an explanation. What would make you revise yours?",
        ];
        for (const [index, card] of cards.entries()) {
          card.querySelector(".brief-card-title a").textContent = titles[index];
          card.querySelector(".brief-card-excerpt").textContent = excerpts[index];
        }
      });
      await snapshot(page, "brief-entry");
    });
    test("reading and Contents", async ({ page }) => {
      await page.goto("/n/Survey");
      await expect(page.locator("[data-note-body]")).toBeVisible();
      await expect(page.locator(".frontmatter-panel h2")).toHaveText("Properties");
      await expect(page.locator(".frontmatter-panel form:visible")).toHaveCount(0);
      await expect(page.locator("[data-note-body] p").first()).toBeInViewport();
      await expect(page.locator("[data-sync-line]")).toContainText("Review 5 incoming changes");
      await snapshot(page, "reading-entry");
      await page.locator("#measurements-and-uncertainties").evaluate((heading) => heading.scrollIntoView());
      await snapshot(page, "reading");
    });
    experienceVisuals(snapshot);
    thinkingVisuals(snapshot);
    test("editor and exact Undo confirmation", async ({ page }) => {
      await page.goto("/edit?key=Survey");
      await expect(page.locator(SURFACE)).toContainText("The water has fallen");
      await snapshot(page, "editor");
      if (width > 960) {
        await page.getByLabel("Writing layout").selectOption("write");
        await page.getByLabel("Writing type").selectOption("prose");
        await snapshot(page, "focused-writing");
        await page.getByLabel("Writing layout").selectOption("split");
        await page.getByLabel("Writing type").selectOption("mono");
      }
      await page.route("**/api/history/preview", (route) => route.fulfill({ json: {
        ok: true, entry_id: "visual-entry", preview_id: "visual-receipt",
        command: "bn frontmatter set status", timestamp: "2026-01-02T03:04:05Z",
        can_restore: true, requires_force: false,
        changes: [{ path: "Notes/A long note title about field observations.md", current_state: "file", restored_state: "file", conflict: false }],
      } }));
      await page.locator("[data-source-manage] > summary").click();
      await page.locator("[data-source-undo]").click();
      await expect(page.getByRole("dialog")).toContainText("field observations");
      await snapshot(page, "confirmation");
      await page.keyboard.press("Escape");
    });
  });
}

test("Library empty and error states", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 900 });
  await page.addInitScript(() => localStorage.setItem("bn.theme", "light"));
  await page.goto("/library?q=no-such-visual-result");
  await expect(page.locator("[data-overview-search-status]")).toContainText(/no|0/i);
  await page.locator("[data-overview-search-status]").scrollIntoViewIfNeeded();
  await expect(page.locator("[data-overview-search-results]")).toHaveScreenshot("library-empty.png", { animations: "disabled" });
  await page.route("**/api/search?**", (route) => route.fulfill({ status: 503, json: { error: "Search is temporarily unavailable." } }));
  await page.locator("#search-input").fill("unavailable");
  await expect(page.locator("[data-overview-search-status]")).toContainText(/unavailable|failed|retry/i);
  await page.locator("[data-overview-search-status]").scrollIntoViewIfNeeded();
  await expect(page.locator("[data-overview-search-results]")).toHaveScreenshot("library-error.png", { animations: "disabled" });
});


test("short-screen editor opens on prose with long frontmatter and an attachment", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.addInitScript(() => localStorage.setItem("bn.theme", "light"));
  await page.goto("/edit?key=Writing");
  await expect(page.locator(SURFACE)).toContainText("The morning trail");
  await expect(page.locator("[data-source-save]")).toBeInViewport();
  await page.evaluate(() => document.fonts.ready);
  await expect(page).toHaveScreenshot("editor-short-320.png", { animations: "disabled", caret: "hide", maxDiffPixelRatio: 0.001 });
});

function experienceVisuals(snapshot) {
  test("quiet reading, capture and note preview", async ({ page }) => {
    await page.goto("/n/Survey");
    await page.locator(".note-actions-more > summary").click();
    await page.locator("button[data-reading-mode]").click();
    await page.locator("button[data-reading-type]").click();
    await page.locator(".note-actions-more > summary").click();
    await snapshot(page, "quiet-reading");
    await page.locator("[data-capture-launch]").click();
    await page.locator("[data-capture-dialog] [data-capture-input]").fill("An idea to come back to: compare the river sketches.");
    await snapshot(page, "global-capture");
    await page.keyboard.press("Escape");
    await page.goto("/library?q=field&status=reference&sort=title");
    // The async sync line shifts the anchor; open the preview after it settles.
    await expect(page.locator("[data-sync-line]")).toContainText("Review 5 incoming changes");
    const preview = page.locator(".note-peek-trigger").first();
    await preview.focus();
    await preview.press("Enter");
    await expect(page.locator("#note-peek p")).not.toHaveText("Loading note…");
    await snapshot(page, "note-peek");
  });
}

function thinkingVisuals(snapshot) {
  test("connection, reference and command search", async ({ page }) => {
    await page.goto("/n/Alpha");
    await page.locator('[data-related-card="beta"] .connection-launch').click();
    await expect(page.locator(".connection-note h3")).toHaveCount(2);
    await page.evaluate(() => window.scrollTo(0, 0));
    await snapshot(page, "connection");
    await page.getByRole("button", { name: "Write a thought about this connection" }).click();
    await page.getByLabel("Your thought", { exact: true }).fill("These observations suggest a better question for the next visit.");
    await snapshot(page, "connection-writing");
    await page.getByRole("button", { name: "Save thought…", exact: true }).click();
    // Fixed success keeps this visual state from changing subsequent fixtures;
    // return-to-thought.spec.js exercises the real write and receipt recovery.
    await page.route("**/api/notes/new", (route) => route.fulfill({ json: { ok: true, key: "A better question" } }));
    await page.getByLabel("Title", { exact: true }).fill("A better question");
    await page.getByRole("button", { name: "Save thought", exact: true }).click();
    await expect(page.locator(".connection-saved")).toContainText("Thought saved");
    await snapshot(page, "connection-saved");
    await page.getByRole("button", { name: "Keep exploring" }).click();
    const link = page.locator('[data-note-body] a[href^="/n/beta"]').first();
    await link.focus();
    await link.press("Alt+ArrowDown");
    await page.getByRole("button", { name: "Keep beside this note" }).click();
    await expect(page.locator(".reference-content h3")).toHaveText("Beta");
    await page.evaluate(() => window.scrollTo(0, 0));
    await snapshot(page, "side-reference");
    await page.getByRole("button", { name: "Close reference" }).click();
    await page.locator("[data-switcher-launch]").click();
    await page.locator("#switcher-input").fill("Alpha");
    await expect(page.locator(".command-switcher [role=status]")).toContainText(/\bresults?\b/);
    await page.evaluate(() => window.scrollTo(0, 0));
    await snapshot(page, "command-search");
  });
}
