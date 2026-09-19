// The /history page, driven in a real browser against a real vault.
//
// What jsdom cannot answer here is the only thing this surface is for: whether
// the bytes somebody chose to recover are the bytes that come back, and whether
// recovering them left a record of its own. The Vitest suite stops at the
// request it issued — it can prove the page sends a receipt with a force, not
// that the file on disk went back or that the vault is clean afterwards.
//
// Each test creates the change it later recovers and leaves the vault as it
// found it. A spec that recovered a change another spec made would be asserting
// against whatever ran before it.
//
// Page-level logic stays in internal/web/static_src/history-workspace*.test.ts.
// What is here is the round trip and nothing else.

import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { expect, gitStatus, manifest, test } from "./fixture.js";

// Two templates, one per test, named by nothing else in the suite. Templates/
// is excluded from note queries, so writing here cannot disturb the twelve-item
// recent list smoke.spec.js asserts against, and it is inside vaultgit.InScope
// and vaultsync.Portable, which is what makes the durability the restore panel
// claims checkable rather than decorative.
const ORIGIN_TEMPLATE = "E2EHistoryOrigin";
const RESTORE_TEMPLATE = "E2EHistoryRestore";
const TEMPLATE_BODY = "---\ntitle: {{ .Title }}\n---\n\n# {{ .Title }}\n";
const EDITED_BODY = "---\ntitle: {{ .Title }}\n---\n\n# {{ .Title }}\n\nEdited after the first save.\n";
const SAVE_COMMAND = "bn serve template save";

function templatePath(vault, name) {
  return join(vault, "Templates", `${name}.md`);
}

// createTemplate makes one change through the browser, so the entry this spec
// later recovers was recorded by the server the same way any other change is.
// Every write is awaited by its own status sentence before anything else is
// clicked: the Git safety lock is taken without waiting, so a second request
// arriving inside the first is refused rather than queued.
async function createTemplate(page, name) {
  await page.goto("/settings");
  await page.waitForLoadState("networkidle");
  await page.locator("[data-templates-new]").click();
  await page.locator("[data-templates-name]").fill(name);
  await page.locator("[data-templates-content]").fill(TEMPLATE_BODY);
  await page.locator("[data-templates-save]").click();
  await expect(page.locator("[data-templates-diff]")).toBeVisible();
  await page.locator("[data-templates-confirm]").click();
  await expect(page.locator("[data-templates-status]")).toHaveText(`Created "${name}.md".`);
  expect(readFileSync(templatePath(manifest.localVault, name), "utf8")).toBe(TEMPLATE_BODY);
}

// editTemplate rewrites an existing template through the same panel, so the
// journal gets a second entry whose "before" state is real content rather than
// an absent path — which is what makes restoring it a test of the bytes.
async function editTemplate(page, name, body) {
  await page.goto("/settings");
  await page.waitForLoadState("networkidle");
  await page.locator(`[data-templates-open="${name}.md"]`).click();
  // Opening reads the bytes and their revision from the server and then writes
  // both into the editor, so typing before that lands would be typing into a
  // draft about to be replaced — and the revision is what makes the save a
  // rewrite of this file rather than a refused create.
  await expect(page.locator("[data-templates-status]")).toHaveText(
    `Editing Templates/${name}.md.`,
  );
  await page.locator("[data-templates-content]").fill(body);
  await page.locator("[data-templates-save]").click();
  await expect(page.locator("[data-templates-diff]")).toBeVisible();
  await page.locator("[data-templates-confirm]").click();
  await expect(page.locator("[data-templates-status]")).toHaveText(`Saved "${name}.md".`);
}

async function deleteTemplate(page, name) {
  await page.goto("/settings");
  await page.waitForLoadState("networkidle");
  await page.locator(`[data-templates-open="${name}.md"]`).click();
  await page.locator("[data-templates-delete]").click();
  await expect(page.locator("[data-templates-diff]")).toBeVisible();
  await page.locator("[data-templates-confirm]").click();
  await expect(page.locator("[data-templates-status]")).toContainText(`Deleted "${name}.md".`);
}

async function openHistory(page) {
  await page.goto("/history");
  await page.waitForLoadState("networkidle");
  await expect(page.locator("[data-history-status]")).not.toHaveClass(/is-loading/);
}

test("the history workspace names where each change came from and filters by it", async ({
  page,
}) => {
  await createTemplate(page, ORIGIN_TEMPLATE);
  await openHistory(page);

  // The vocabulary is the server's, rendered from the reply rather than from a
  // list the bundle carries — so this is also the assertion that the two agree.
  const origins = page.locator("[data-history-origin-filters]");
  await expect(origins).toContainText("Settings and templates");
  await expect(origins).toContainText("Imported from your other replica");

  const entry = page.locator("[data-history-entry]", { hasText: SAVE_COMMAND }).first();
  await expect(entry).toBeVisible();
  // The classified origin and the recorded command are both on the row: the
  // label is a reading of the command, and a reader who doubts it can check.
  await expect(entry.locator("[data-history-entry-origin]")).toHaveText("Settings and templates");
  await expect(entry).toContainText(SAVE_COMMAND);

  // Filtering to the other origins has to make it go away, or the filter is
  // decorative. `browser` and `settings` are different origins by design, so a
  // template save is not in this view.
  await origins.locator('input[value="browser"]').check();
  await expect(page.locator("[data-history-entry]", { hasText: SAVE_COMMAND })).toHaveCount(0);
  await page.locator("[data-history-clear-filters]").click();
  await expect(page.locator("[data-history-entry]", { hasText: SAVE_COMMAND }).first()).toBeVisible();

  // A path filter the server applies, not a client-side hide. Narrowed first and
  // widened second on purpose: the entry is already on screen, so filling in a
  // path that matches it would pass whether or not the filter did anything.
  await page.locator("[data-history-path]").fill("No/Such/Directory/");
  await expect(page.locator("[data-history-status]")).toHaveText(
    "No recorded change matches this view.",
  );
  await page.locator("[data-history-path]").fill("Templates/");
  await expect(page.locator("[data-history-entry]", { hasText: SAVE_COMMAND }).first()).toBeVisible();

  await deleteTemplate(page, ORIGIN_TEMPLATE);
  expect(existsSync(templatePath(manifest.localVault, ORIGIN_TEMPLATE))).toBe(false);
  expect(gitStatus(manifest.localVault), "the cleanup was written but never snapshotted").toBe("");
});

test("a restore is previewed, puts the earlier bytes back, and is recorded as a change of its own", async ({
  page,
}) => {
  const file = templatePath(manifest.localVault, RESTORE_TEMPLATE);
  const peerFile = templatePath(manifest.peerVault, RESTORE_TEMPLATE);

  // Two changes, so the restore is a *rewrite* rather than a removal. Undoing a
  // creation only has to delete a path, which would leave the central claim —
  // that the bytes somebody chose come back — untested.
  await createTemplate(page, RESTORE_TEMPLATE);
  await editTemplate(page, RESTORE_TEMPLATE, EDITED_BODY);
  expect(readFileSync(file, "utf8")).toBe(EDITED_BODY);
  expect(gitStatus(manifest.localVault), "the edit was written but never snapshotted").toBe("");

  // Recorded before the restore, so "the peer did not change" is a comparison
  // rather than a restatement of how the fixture started. The pair is the point:
  // the file is here and is not there, at the same moment.
  const peerBefore = { exists: existsSync(peerFile), status: gitStatus(manifest.peerVault) };
  expect(peerBefore.exists, "the fixture already shared this file with the peer").toBe(false);

  await openHistory(page);
  const entry = page.locator("[data-history-entry]", { hasText: SAVE_COMMAND }).first();
  // Its own identifier, not its command: two saves of this template are in the
  // journal and the origin test left more, so "an entry with this command" is
  // true of the history whether or not this one survives the restore.
  const reversedID = await entry.getAttribute("data-history-entry");
  expect(reversedID, "the list rendered an entry with no identifier").toBeTruthy();
  await entry.locator("[data-history-preview-entry]").click();

  // Nothing is written by a preview, and the page says so before it offers the
  // button. The file still holds the *edited* bytes at this point, which is the
  // assertion that matters: a preview that had already acted would make the
  // confirmation a formality.
  const panel = page.locator("[data-history-restore]");
  await expect(panel).toBeVisible();
  await expect(page.locator("[data-history-status]")).toHaveText(
    "Previewed. Nothing has changed yet.",
  );
  expect(readFileSync(file, "utf8"), "the preview wrote to the vault").toBe(EDITED_BODY);

  const change = panel.locator(`[data-history-change="Templates/${RESTORE_TEMPLATE}.md"]`);
  await expect(change).toBeVisible();
  // Recorded and travels are separate questions and the panel answers both. This
  // asserts what the panel *says* — that Templates/ is portable — against
  // vaultsync.Portable, which is the rule it is reading; it does not synchronize
  // anything, and no e2e test does.
  await expect(change).toContainText("travels to your other replica");
  // Nothing is contested, so there is no consent box and no forcing.
  await expect(page.locator("[data-history-restore-conflicts]")).toBeHidden();
  await expect(page.locator("[data-history-restore-confirm]")).toHaveText("Restore");

  await page.locator("[data-history-restore-confirm]").click();
  await expect(page.locator("[data-history-status]")).toHaveClass(/is-success/);

  // The one thing jsdom cannot answer: the bytes on disk, put back by the server
  // rather than reported back by the page.
  expect(readFileSync(file, "utf8"), "the browser reported a restore the vault did not perform")
    .toBe(TEMPLATE_BODY);
  // Containment: the peer is exactly as it was before the restore, compared
  // against the reading taken above rather than against an assumption.
  expect(existsSync(peerFile), "the restore reached the peer vault").toBe(peerBefore.exists);
  expect(gitStatus(manifest.peerVault), "the peer vault changed during a restore")
    .toBe(peerBefore.status);
  // A restore is a mutation like any other: it is journaled and snapshotted, so
  // the tree is clean and ahead by a commit rather than dirty.
  expect(gitStatus(manifest.localVault), "the restore was applied but never snapshotted").toBe("");

  // And it is in the history as its own entry, which is what makes it
  // recoverable in turn and what lets it travel to the other replica.
  await openHistory(page);
  const restored = page.locator("[data-history-entry]", { hasText: "bn web undo" }).first();
  await expect(restored).toBeVisible();
  await expect(restored.locator("[data-history-entry-origin]")).toHaveText("Restored from history");

  // The entry it undid is still listed — that one, by identifier. Recovering a
  // change does not erase the record of the change, or the history would get
  // shorter every time somebody used it.
  await expect(page.locator(`[data-history-entry="${reversedID}"]`)).toBeVisible();

  await deleteTemplate(page, RESTORE_TEMPLATE);
  expect(existsSync(file)).toBe(false);
  expect(gitStatus(manifest.localVault), "the cleanup was written but never snapshotted").toBe("");
});
