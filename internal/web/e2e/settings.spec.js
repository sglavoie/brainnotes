// The settings page, driven in a real browser against a real vault.
//
// Both panels here write through a preview and a confirmation, and what jsdom
// cannot answer is whether the bytes a person confirmed are the bytes that reach
// the disk: the Vitest suite stops at the request it issued. These cases type
// into the shipped bundle, confirm the rendered diff, and then read the vault —
// the configuration file, the templates directory, the peer vault, and
// `git status` — to decide whether the write landed where it said it did.
//
// Each test leaves the vault as it found it, rather than handing state to the
// next one: a spec that depends on its predecessor reports a cascade of failures
// for one broken write and cannot be run alone.
//
// Page-level logic stays in internal/web/static_src/settings-*.test.ts and
// templates-*.test.ts. What is here is the round trip and nothing else.

import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { expect, gitStatus, manifest, test } from "./fixture.js";

// The eight keys internal/config/ownership.go assigns to brainnotes.local.toml.
// They name *this machine* — an editor binary, a bind address, an agent command
// — and the whole point of the portable-settings surface is that they are not on
// it. Listed here rather than derived, so a key quietly moving between layers
// fails this spec instead of silently appearing in a browser.
const MACHINE_LOCAL_KEYS = [
  "fzf.args",
  "open.editor",
  "serve.addr",
  "serve.open_browser",
  "agent.command",
  "agent.timeout_seconds",
  "cockpit.agent_command",
  "cockpit.agent_timeout_seconds",
];

// The setting this spec writes. `brief.exclude_types` is a portable list nothing
// else in the suite reads, and the fixture config does not assign it — so the
// write is observable as an appearance in the file and the reset as its removal,
// with no other spec depending on either state.
const SETTING_KEY = "brief.exclude_types";
const SETTING_VALUE = "e2e-excluded-type";

// A template's frontmatter holds unrendered actions, which is exactly why these
// bytes have to round-trip: `bn new` renders them, and no YAML parser accepts
// them. A surface that normalized this content would break every template it
// touched.
const TEMPLATE_NAME = "E2EFixture";
const TEMPLATE_FILE = "Templates/E2EFixture.md";
const TEMPLATE_BODY = "---\ntitle: {{ .Title }}\ncreated: {{ today }}\n---\n\n# {{ .Title }}\n";

// openSettings loads the page and waits for both panels to have fetched their
// own state. The page renders no setting value and no template byte server-side,
// so every assertion below is against what the bundle put there.
async function openSettings(page) {
  await page.goto("/settings");
  // The history panel fetches on load, and serving the brief writes
  // .bnotes/brief.json through vaultstate.Save, which takes the same vaultgit
  // safety lock a write here needs. A click into that window is refused as busy
  // — correct product behaviour, a race in a suite — so the page is allowed to
  // finish its own work first, which is also what a person does.
  await page.waitForLoadState("networkidle");
  await expect(page.locator(`[data-settings-row="${SETTING_KEY}"]`)).toBeVisible();
}

// confirmWrite drives one preview-then-confirm cycle and waits for the sentence
// that says it landed. Every write is awaited by its own status before anything
// else is clicked: the Git safety lock is taken without waiting, so a second
// request arriving inside the first is refused rather than queued.
async function confirmWrite(page, panel, expected) {
  const status = page.locator(`[data-${panel}-status]`);
  await expect(page.locator(`[data-${panel}-diff]`)).toBeVisible();
  await page.locator(`[data-${panel}-confirm]`).click();
  await expect(status).toHaveText(expected);
  await expect(status).toHaveClass(/is-success/);
}

test("the settings panel offers portable keys and never a machine-local one", async ({ page }) => {
  await openSettings(page);

  // A positive beside the absence, for the same reason isolation.spec.js pairs
  // them: "no machine-local key is listed" is satisfied by a panel that rendered
  // nothing at all.
  await expect(page.locator(`[data-settings-row="capture.inbox_note"]`)).toBeVisible();
  for (const key of MACHINE_LOCAL_KEYS) {
    await expect(page.locator(`[data-settings-row="${key}"]`)).toHaveCount(0);
  }

  // The page has to say where they went, or a person looking for their editor
  // setting concludes the surface is broken.
  await expect(page.locator("body")).toContainText("brainnotes.local.toml");
});

test("the templates panel states both properties of the configured directory", async ({ page }) => {
  await openSettings(page);

  // Read from the server's own predicates — vault.WalkExclusion for the first
  // half, vaultgit.InScope plus vaultsync.Portable for the second — against this
  // vault's real configuration, not a fixture's idea of it.
  await expect(page.locator("[data-templates-notice]")).toHaveText(
    "Templates/ is excluded from note queries and synchronizes with your other vaults.",
  );
});

test("a portable setting typed in the browser reaches brainnotes.toml and comes back out", async ({
  page,
}) => {
  const config = join(manifest.localVault, "brainnotes.toml");
  const peerConfig = join(manifest.peerVault, "brainnotes.toml");
  const peerBefore = readFileSync(peerConfig, "utf8");
  expect(gitStatus(manifest.localVault), "the local vault did not start clean").toBe("");
  expect(readFileSync(config, "utf8")).not.toContain(SETTING_VALUE);

  await openSettings(page);
  const row = page.locator(`[data-settings-row="${SETTING_KEY}"]`);
  await expect(row.locator("[data-settings-origin]")).toHaveText("default");

  await row.locator("[data-settings-edit]").click();
  await row.locator("[data-settings-control]").fill(SETTING_VALUE);
  await row.locator("[data-settings-save]").click();
  await confirmWrite(page, "settings", `Saved "${SETTING_KEY}".`);

  // The file the *server* wrote, not a path this spec chose.
  expect(
    readFileSync(config, "utf8"),
    "the browser reported success but the file is unchanged",
  ).toContain(SETTING_VALUE);
  expect(readFileSync(peerConfig, "utf8"), "the write reached the peer vault too").toBe(peerBefore);
  expect(gitStatus(manifest.peerVault), "the peer vault changed during a settings write").toBe("");
  // vaultgit snapshots the write, so the tree is clean again but ahead by a
  // commit. A dirty tree here would mean the snapshot did not happen.
  expect(gitStatus(manifest.localVault), "the setting was written but never snapshotted").toBe("");
  // The row re-renders from the applied result, so it now names the file that
  // assigned the key rather than the default it used to resolve to.
  await expect(row.locator("[data-settings-origin]")).toHaveText("brainnotes.toml");

  // Reset is a distinct op, not a set-to-empty: the assignment goes away and the
  // key resolves to its default again, which is what the sentence claims.
  await row.locator("[data-settings-unset]").click();
  await confirmWrite(page, "settings", `Removed "${SETTING_KEY}". It is back to its default.`);
  expect(readFileSync(config, "utf8")).not.toContain(SETTING_VALUE);
  await expect(row.locator("[data-settings-origin]")).toHaveText("default");
  await expect(row.locator("[data-settings-unset]")).toBeDisabled();
  expect(gitStatus(manifest.localVault), "the reset was written but never snapshotted").toBe("");
});

test("a template is created, overwritten and deleted from the browser", async ({ page }) => {
  const template = join(manifest.localVault, TEMPLATE_FILE);
  const peerTemplate = join(manifest.peerVault, TEMPLATE_FILE);
  expect(existsSync(template), "the fixture already holds the template this test writes").toBe(false);

  await openSettings(page);
  await page.locator("[data-templates-new]").click();
  // Typed as a bare stem, which is how `bn new --template` names one; the
  // configured extension is appended from what the server reported.
  await page.locator("[data-templates-name]").fill(TEMPLATE_NAME);
  await page.locator("[data-templates-content]").fill(TEMPLATE_BODY);
  await page.locator("[data-templates-save]").click();
  await confirmWrite(page, "templates", `Created "${TEMPLATE_NAME}.md".`);

  // Byte for byte: the placeholders are stored exactly as typed, because a
  // template nothing can render is a template `bn new` cannot use.
  expect(readFileSync(template, "utf8")).toBe(TEMPLATE_BODY);
  expect(existsSync(peerTemplate), "the template reached the peer vault too").toBe(false);
  expect(gitStatus(manifest.peerVault), "the peer vault changed during a template write").toBe("");
  expect(gitStatus(manifest.localVault), "the template was written but never snapshotted").toBe("");
  // The listing is re-read from the server after every write rather than patched
  // in place, and the draft now names bytes that exist, so a delete is offered.
  await expect(page.locator(`[data-templates-open="${TEMPLATE_NAME}.md"]`)).toBeVisible();
  await expect(page.locator("[data-templates-delete]")).toBeEnabled();

  // An overwrite pinned to the revision the create answered with.
  const edited = `${TEMPLATE_BODY}\nAdded in a browser.\n`;
  await page.locator("[data-templates-content]").fill(edited);
  await page.locator("[data-templates-save]").click();
  await confirmWrite(page, "templates", `Saved "${TEMPLATE_NAME}.md".`);
  expect(readFileSync(template, "utf8")).toBe(edited);
  expect(gitStatus(manifest.localVault), "the overwrite was written but never snapshotted").toBe("");

  await page.locator("[data-templates-delete]").click();
  await confirmWrite(page, "templates", `Deleted "${TEMPLATE_NAME}.md". Undo it from Recent changes.`);
  expect(existsSync(template), "the browser reported a delete the vault did not perform").toBe(false);
  expect(gitStatus(manifest.localVault), "the delete was performed but never snapshotted").toBe("");
  // That sentence points at the history panel, so it has to be on the page it
  // points from.
  await expect(page.locator("[data-history-panel]")).toBeVisible();
});
