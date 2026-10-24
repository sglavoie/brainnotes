import { test, expect } from "./fixture.js";
import { SURFACE } from "./editor-helpers.js";

const timestamp = "2026-02-03T10:05:00Z";
const current = "# Field journal\n\n" + "A passage worth keeping.\n".repeat(14) + "The old ending.\n";
const restored = current.replace("The old ending.", "The earlier ending.");
const entry = {
  id: "review-entry", timestamp, command: "bn web source save", summary: "Edited Field journal",
  origin: "browser", origin_label: "Saved in the browser", file_count: 1,
  classes: [{ class: "note", label: "Notes", count: 1 }],
  files: [{ path: "Notes/Field journal.md", class: "note", before: "file", after: "file", recorded: true, travels: true }],
};

export const fixedHistory = async (page) => {
  await page.route(/\/api\/history(?:\?.*)?$/, (route) => route.fulfill({ json: {
    ok: true, entries: [entry], total: 1, offset: 0, limit: 20, has_more: false,
    origins: [{ value: "browser", label: "Saved in the browser" }],
    classes: [{ value: "note", label: "Notes" }],
  } }));
};

const fixedSync = async (page) => {
  await page.route("**/api/sync/status", (route) => route.fulfill({ json: {
    ok: true, mode: "local", enrolled: true, observed_at: timestamp, headline: "idle", states: ["idle"],
    summary: "Everything is up to date.", needs_person: false,
    save: { managed: true, last_commit_at: timestamp, blocked: false },
    export: { pending_commits: 0, baseline_pending: false },
    transport: { published: 3, acknowledged: 3, awaiting_receipt: 0, rejected: [] },
    peer: { delivered: 3, applied: 3, awaiting_application: 0, conflicts: 0 },
    workers: [], problems: [],
  } }));
  await page.route("**/api/sync/conflicts", (route) => route.fulfill({ json: { ok: true, operations: [] } }));
};

export const reviewVisuals = (snapshot) => {
  for (const workspace of ["settings", "history", "sync", "health", "properties", "properties/status"]) {
    test(`${workspace} management workspace`, async ({ page }) => {
      await fixedHistory(page);
      await fixedSync(page);
      await page.goto(`/${workspace}`);
      await page.waitForLoadState("networkidle");
      await expect(page.getByText("Workspace", { exact: true })).toBeVisible();
      if (workspace === "history") {
        await expect(page.locator("[data-history-entry]").first()).toBeInViewport();
        await expect(page.getByRole("button", { name: /Preview restoring/ }).first()).toBeInViewport();
      }
      if (workspace === "health") {
        await expect(page.locator("[data-fixes-panel]")).toHaveAttribute("aria-busy", "false");
        await expect(page.locator("[data-fixes-error]")).toBeHidden();
        await expect(page.locator("[data-fixes-task]").first()).toBeVisible();
      }
      if (workspace === "properties") {
        await expect(page.locator(".properties-item").first()).toBeInViewport();
        const input = await page.locator("[data-properties-query]").boundingBox();
        expect(input.height).toBeLessThan(60);
      }
      await snapshot(page, `manage-${workspace.replaceAll("/", "-")}`);
    });
  }
  test("Health read failure is a separate recoverable state", async ({ page }) => {
    await fixedSync(page);
    await page.route("**/api/cockpit?view=daily", (route) => route.fulfill({
      status: 503, json: { ok: false, message: "Fixes are temporarily unavailable." },
    }));
    await page.goto("/health");
    await expect(page.getByText("Fixes are temporarily unavailable.", { exact: true })).toHaveCount(1);
    await expect(page.locator("[data-fixes-stale]")).toBeVisible();
    await expect(page.getByRole("button", { name: "Reload fixes" })).toBeEnabled();
    await snapshot(page, "health-error");
  });
  test("settings confirmation beside the edited value", async ({ page }) => {
    await fixedHistory(page);
    await fixedSync(page);
    await page.goto("/settings");
    const row = page.locator('[data-settings-row="attachments.max_file_bytes"]');
    await row.locator("[data-settings-edit]").click();
    await row.locator("[data-settings-control]").fill("10");
    await row.locator("[data-settings-save]").click();
    await expect(row.locator("[data-settings-confirm]")).toBeFocused();
    await expect(row.locator("[data-settings-status]")).toBeInViewport();
    await expect(row.locator("[data-settings-confirm]")).toBeInViewport();
    await snapshot(page, "settings-confirmation");
  });
  test("restore content comparison", async ({ page }) => {
    await fixedHistory(page);
    await fixedSync(page);
    await page.route("**/api/history/preview", (route) => route.fulfill({ json: {
      ok: true, entry_id: entry.id, command: entry.command, timestamp, origin_label: entry.origin_label,
      preview_id: "review-receipt", expires_at: "2099-01-01T00:00:00Z", requires_force: true,
      message: "This restore would replace newer work in one note.",
      changes: [{ path: entry.files[0].path, class: "note", current_state: "file", restored_state: "file",
        recorded: true, travels: true, conflict: true, comparison: { current, restored } }],
      conflicts: [{ path: entry.files[0].path, expected_state: "file", current_state: "file" }],
    } }));
    await page.goto("/history");
    await page.getByRole("button", { name: /Preview restoring/ }).click();
    const panel = page.locator("[data-history-restore]");
    await expect(panel.locator("[data-history-restore-facts]")).toContainText("Feb 3, 2026");
    await expect(panel.locator("[data-history-restore-facts]")).toContainText("GMT");
    await expect(panel.locator(".comparison-added")).toContainText("earlier ending");
    await panel.scrollIntoViewIfNeeded();
    await snapshot(page, "restore-comparison");
  });
  test("editor conflict comparison", async ({ page }) => {
    await fixedSync(page);
    await page.route("**/api/source/save", (route) => route.fulfill({ status: 409, json: {
      ok: false, code: "conflict", revision: "new-revision", content: current,
      message: "This note changed since it was loaded. Nothing was written.",
    } }));
    await page.goto("/edit?key=Writing");
    await expect(page.locator(SURFACE)).toContainText("The morning trail");
    await page.locator(SURFACE).click();
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.insertText(restored);
    // The preview follows the draft after a debounce; wait for it so the
    // snapshot never catches the note it replaced.
    await expect(page.locator("[data-source-preview]")).toContainText("The earlier ending");
    await page.locator("[data-source-save]").click();
    await expect(page.locator("[data-source-conflict]")).toBeVisible();
    await page.locator("[data-source-conflict]").scrollIntoViewIfNeeded();
    await expect(page.locator(".comparison-legend")).toContainText("Your draft");
    await snapshot(page, "editor-conflict");
  });
};

export const fixedRecentNotes = async (page) => {
  // Other browser journeys write to this vault. Pin the recent list's
  // content for visual comparison; screen-polish checks the real list.
  await page.locator(".recent-list").evaluate((list) => {
    const sample = list.querySelector("li");
    const notes = [
      ["Writing", "/n/Writing", "/"],
      ["A long note title about field observations, unfinished ideas, and a careful return to the work in progress", "/n/Notes/A%20long%20note%20title%20about%20field%20observations%2C%20unfinished%20ideas%2C%20and%20a%20careful%20return%20to%20the%20work%20in%20progress", "Notes"],
      ["Reading", "/n/Reading", "/"],
    ];
    list.replaceChildren(...notes.map(([title, href, folder]) => {
      const item = sample.cloneNode(true);
      const link = item.querySelector("a");
      link.textContent = title;
      link.href = href;
      item.querySelector(".recent-note-meta span").textContent = folder;
      return item;
    }));
  });
};
