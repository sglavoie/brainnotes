// Editing a note's source in a real browser.
//
// internal/web/static_src/source-*.test.ts covers the editor's decisions — what
// is dirty, which revision a save is planned against, which preview response
// wins, what is kept locally and what is offered back — against a fake surface,
// because jsdom has no layout and CodeMirror needs one. What is left is exactly
// what needs a browser and a server at once: the bundled editor actually
// mounting, keystrokes reaching the document, Ctrl/⌘ + S reaching
// /api/source/save, the typed bytes landing in the fixture vault's note and in
// nothing else — and, for the draft half, a real reload over a real
// localStorage, keyed by the vault identity this server published.

import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { expect, gitStatus, manifest, test } from "./fixture.js";

import {
  caretToEnd,
  keptDrafts,
  openEditor,
  phraseFor,
  SURFACE,
} from "./editor-helpers.js";

const NOTE = "Alpha.md";

test("a note typed in the browser is saved by Ctrl/⌘ + S and lands in the fixture vault", async ({
  page,
}) => {
  const notePath = join(manifest.localVault, NOTE);
  const peerNotePath = join(manifest.peerVault, NOTE);
  const unrelatedPath = join(manifest.localVault, manifest.unrelatedNote);
  const before = readFileSync(notePath, "utf8");
  const peerBefore = readFileSync(peerNotePath, "utf8");
  const unrelatedBefore = readFileSync(unrelatedPath, "utf8");
  expect(gitStatus(manifest.localVault), "the local vault did not start clean").toBe("");
  expect(gitStatus(manifest.peerVault), "the peer vault did not start clean").toBe("");

  const phrase = phraseFor("source edit");
  await openEditor(page);
  const cleanTitle = await page.title();
  await caretToEnd(page);
  await page.keyboard.type(`## ${phrase}`);
  await expect(page).toHaveTitle(`• ${cleanTitle}`);

  // Dirtiness is computed from the document, so a Save that is still disabled
  // means the keystrokes never reached CodeMirror's state.
  await expect(page.locator("[data-source-save]")).toBeEnabled();

  // The preview is the server's own renderer — internal/web/markdown.go, the
  // same one the note page uses — reached through /api/source/preview. A
  // browser-side Markdown renderer would be a second answer to what a note
  // looks like; this asserts there is only one.
  await expect(page.locator("[data-source-preview] h2")).toContainText(phrase);

  await page.keyboard.press("ControlOrMeta+s");
  await expect(page.locator("[data-source-status]")).toHaveClass(/is-success/);
  // Saved text is clean text: the button going back to disabled is the editor
  // re-pinning to the revision the server answered with.
  await expect(page.locator("[data-source-save]")).toBeDisabled();
  await expect(page).toHaveTitle(cleanTitle);

  const after = readFileSync(notePath, "utf8");
  expect(after, "the browser reported success but the note on disk is unchanged").not.toBe(before);
  expect(after).toContain(phrase);
  // A whole-file save replaces the file, so everything above the insertion is
  // the assertion that matters: the frontmatter and the body the edit never
  // touched must come back byte-for-byte, unsorted and unrewritten.
  expect(after.startsWith(before), "the save rewrote source the edit never touched").toBe(true);

  expect(readFileSync(unrelatedPath, "utf8"), "a note nobody edited was rewritten").toBe(
    unrelatedBefore,
  );
  expect(readFileSync(peerNotePath, "utf8"), "the edit reached the peer vault too").toBe(peerBefore);
  expect(gitStatus(manifest.peerVault), "the peer vault changed during a local edit").toBe("");
  // vaultgit snapshots the write, so the local vault is clean again but ahead by
  // a commit. A dirty tree here would mean the snapshot did not happen.
  expect(gitStatus(manifest.localVault), "the note was written but never snapshotted").toBe("");
});

test("a toolbar command edits the document and the Save button writes it", async ({ page }) => {
  const notePath = join(manifest.localVault, NOTE);
  const phrase = phraseFor("toolbar edit");

  await openEditor(page);
  await caretToEnd(page);
  await page.keyboard.press("Enter");
  await page.keyboard.type(phrase);
  // Selecting back to the start of the line gives the command a real selection
  // to wrap. source-format.ts is handed offsets, and only a mounted editor
  // turns a keystroke into them.
  await page.keyboard.press("Shift+Home");

  await page.locator(".source-editor-format > summary").click();
  await page.locator('[data-source-command="bold"]').click();
  await expect(page.locator(SURFACE)).toContainText(`**${phrase}**`);

  await page.locator("[data-source-save]").click();
  await expect(page.locator("[data-source-status]")).toHaveClass(/is-success/);

  expect(readFileSync(notePath, "utf8")).toContain(`**${phrase}**`);
  expect(gitStatus(manifest.localVault), "the note was written but never snapshotted").toBe("");
});

test("typing [[ completes a wikilink from the vault", async ({ page }) => {
  await openEditor(page);
  await caretToEnd(page);

  // The completion source asks /api/search and offers each result's `wikilink`
  // — the vault-relative path the server derives, not the folded key and not
  // the frontmatter title. Nothing below the browser can prove the popup opens.
  await page.keyboard.type("[[Bet");
  const options = page.locator(".cm-tooltip-autocomplete li");
  await expect(options.first()).toContainText("Beta");

  await page.keyboard.press("Enter");
  await expect(page.locator(SURFACE)).toContainText("[[Beta]]");

  // Nothing is written until Save, and this test never presses it.
  expect(gitStatus(manifest.localVault), "the editor wrote without being told to save").toBe("");
});

// The case above cannot fail on the defect this one is for: `Bet` is a prefix
// of the note's path, so a completion list re-filtered against that path agrees
// with the server by accident. `Cartographer` appears only in the *body* of
// Notes/Fieldwork.md, so the two disagree — the vault matches the note and its
// own label does not — and searching by what a note says rather than by where
// it is filed is most of why the completion asks the server at all.
test("a completion survives when the note matched on something other than its path", async ({ page }) => {
  await openEditor(page);
  await caretToEnd(page);

  await page.keyboard.type("[[Cartograph");
  const options = page.locator(".cm-tooltip-autocomplete li");
  await expect(options.first()).toContainText("Notes/Fieldwork");

  await page.keyboard.press("Enter");
  await expect(page.locator(SURFACE)).toContainText("[[Notes/Fieldwork]]");

  expect(gitStatus(manifest.localVault), "the editor wrote without being told to save").toBe("");
});

// The price of asking the server on every keystroke, and the only place it can
// be seen. Between a keystroke and the answer to the query it started,
// CodeMirror leaves the previous list on screen and marks it disabled — same
// options, same DOM, no attribute — and its Enter binding then declines, so the
// key falls through to inserting a newline. Nothing in jsdom can observe that:
// it is CodeMirror's own state field, reached through a keymap, over a real
// async source.
//
// So this types the query and presses Enter with no pause at all, which is what
// somebody finishing a link actually does. Either the completion takes it or
// refreshingCompletionKeymap declines it; what must not happen is a newline
// landing inside the brackets.
test("a completion still open declines Enter rather than breaking the link", async ({ page }) => {
  await openEditor(page);
  await caretToEnd(page);

  await page.keyboard.type("[[Cartograph", { delay: 0 });
  await page.keyboard.press("Enter");

  const value = () => page.locator(SURFACE).innerText();
  expect(await value(), "Enter fell through to the document mid-query").not.toContain("[[Cartograph\n");

  // And the keystroke is only deferred, not swallowed for good: once the query
  // lands the dialog is live again and the same Enter completes the link.
  const options = page.locator(".cm-tooltip-autocomplete li");
  await expect(options.first()).toContainText("Notes/Fieldwork");
  await page.keyboard.press("Enter");
  await expect(page.locator(SURFACE)).toContainText("[[Notes/Fieldwork]]");

  expect(gitStatus(manifest.localVault), "the editor wrote without being told to save").toBe("");
});

// The other side of the same guard, and the reason it consults the vault's
// answers rather than only the query's status. Writing the link before the note
// exists is an ordinary move, and there no dialog is ever coming — so a key held
// back "until the completion arrives" is a key taken for nothing.
//
// The shape is exact. The first query has to have *answered* — CodeMirror resets
// its activation debounce on every keystroke, so nothing is asked while typing
// runs on — and then one more character reopens the pending window over an
// answer already known to be empty. That is the window the guard would otherwise
// fire in, and the search is monotone, so no later query can fill the dialog
// either.
test("Enter is not held back for a completion that has already come back empty", async ({ page }) => {
  await openEditor(page);
  await caretToEnd(page);

  const answered = page.waitForResponse((response) => response.url().includes("/api/search"));
  await page.keyboard.type("[[Zzyzx Unwritten", { delay: 0 });
  await answered;

  await page.keyboard.type("!", { delay: 0 });
  await page.keyboard.press("Enter");

  const lines = await page.locator(`${SURFACE} .cm-line`).allInnerTexts();
  const opened = lines.findIndex((line) => line.includes("Zzyzx Unwritten!"));
  expect(opened, "the typed link never reached the document").toBeGreaterThanOrEqual(0);
  expect(lines.length - 1, "Enter was swallowed with no completion on the way").toBeGreaterThan(opened);

  expect(gitStatus(manifest.localVault), "the editor wrote without being told to save").toBe("");
});

// The same link as two tests above, but reached the way somebody actually writes
// one: type `[[`, pause while deciding, then type the title. The pause is the
// point. It is longer than CodeMirror's 100 ms activation delay, so the *empty*
// query runs — and `/api/search?q=` answers with no rows without reading a note,
// because it is declining to scan rather than reporting on the vault. Taking
// that zero for a fact about the vault would record `""` as a known-empty
// prefix, which is a prefix of every query there is, and the guard above would
// be off for the rest of this editor's life.
test("a pause after [[ does not switch the guard off for everything typed next", async ({ page }) => {
  await openEditor(page);
  await caretToEnd(page);

  const emptyQuery = page.waitForResponse((response) => /\/api\/search\?q=(&|$)/.test(response.url()));
  await page.keyboard.type("[[", { delay: 0 });
  await emptyQuery;

  await page.keyboard.type("Cartograph", { delay: 0 });
  await page.keyboard.press("Enter");

  const value = () => page.locator(SURFACE).innerText();
  expect(await value(), "Enter fell through to the document mid-query").not.toContain("[[Cartograph\n");

  const options = page.locator(".cm-tooltip-autocomplete li");
  await expect(options.first()).toContainText("Notes/Fieldwork");
  await page.keyboard.press("Enter");
  await expect(page.locator(SURFACE)).toContainText("[[Notes/Fieldwork]]");

  expect(gitStatus(manifest.localVault), "the editor wrote without being told to save").toBe("");
});

// The conflict round trip. jsdom covers what the controller *does* with a 409,
// against a stubbed client; what it cannot produce is a real second writer
// moving the note's revision under a mounted editor, and a 409 the server
// actually decided to send.
test("a note that changed under the edit offers the vault's version", async ({ page }) => {
  const notePath = join(manifest.localVault, NOTE);
  await openEditor(page);

  const panel = page.locator("[data-source-editor]");
  const key = await panel.getAttribute("data-source-key");
  const token = await panel.getAttribute("data-source-csrf");

  // The second writer goes through the same API the editor uses, so this is the
  // ordinary case — the note edited in another window — rather than a file
  // written behind the server's back. page.request is not the page: the editor
  // is never told, and its pinned revision simply goes stale.
  const read = await page.request.get(`/api/source?key=${encodeURIComponent(key)}`);
  const current = await read.json();
  const theirs = `${current.content}\n${phraseFor("other window")}\n`;
  const written = await page.request.post("/api/source/save", {
    headers: { "X-BN-CSRF": token },
    form: {
      key,
      content: theirs,
      revision: current.revision,
      save_id: `e2e-conflict-${current.revision}`,
    },
  });
  expect(written.ok(), "the out-of-band save was refused").toBe(true);

  await caretToEnd(page);
  await page.keyboard.type("my draft");
  await page.locator("[data-source-save]").click();

  const conflict = page.locator("[data-source-conflict]");
  await expect(conflict).toBeVisible();
  await expect(page.locator("[data-source-conflict-source]")).toContainText(
    phraseFor("other window"),
  );
  await expect(conflict.locator(".comparison-added", { hasText: "my draft" })).toBeVisible();
  await expect(conflict.locator(".comparison-removed", { hasText: phraseFor("other window") })).toBeVisible();
  // Nothing was written and the draft is still in the editor, which is the
  // promise the panel's own copy makes.
  await expect(page.locator(SURFACE)).toContainText("my draft");
  expect(readFileSync(notePath, "utf8"), "the refused save was written anyway").toBe(theirs);

  // Discarding adopts the vault's version — both the bytes and the revision —
  // so the editor ends up clean rather than holding text it would re-offer.
  await page.locator("[data-source-conflict-discard]").click();
  await expect(conflict).toBeHidden();
  await expect(page.locator(SURFACE)).toContainText(phraseFor("other window"));
  await expect(page.locator(SURFACE)).not.toContainText("my draft");
  await expect(page.locator("[data-source-save]")).toBeDisabled();

  expect(readFileSync(notePath, "utf8"), "discarding wrote to the vault").toBe(theirs);
  expect(gitStatus(manifest.localVault), "a write left the vault unsnapshotted").toBe("");
});

// The interruption this feature exists for, in the only place it is real: a
// browser that actually reloads, over a localStorage that actually persists,
// under a key built from the vault identity this server published. jsdom covers
// what the editor decides; it cannot reload a page, and its storage goes away
// with the test.
test("unsaved text survives a reload and is offered back rather than saved", async ({ page }) => {
  const notePath = join(manifest.localVault, NOTE);
  const before = readFileSync(notePath, "utf8");
  const phrase = phraseFor("draft recovery");

  // Leaving a dirty editor arms the browser's own "leave site?" prompt — the
  // page asks for it in beforeunload — and an unanswered prompt would stall the
  // reload below.
  page.on("dialog", (dialog) => dialog.accept());

  await openEditor(page);
  const vaultID = await page.locator("[data-source-editor]").getAttribute("data-source-vault-id");
  await caretToEnd(page);
  await page.keyboard.type(`\n${phrase}`);

  // Written while typing, by the debounce, rather than on the way out. That is
  // what makes it a copy a power cut survives.
  await expect.poll(() => keptDrafts(page)).toHaveLength(1);
  const [kept] = await keptDrafts(page);
  // The key carries the server's own name for this vault, so one origin serving
  // two vaults over a machine's life cannot offer one's text over the other's
  // note of the same name.
  expect(vaultID).not.toBe("");
  expect(kept.key).toContain(encodeURIComponent(vaultID));
  expect(kept.content).toContain(phrase);
  expect(readFileSync(notePath, "utf8"), "keeping a local copy wrote to the vault").toBe(before);

  await page.reload();

  const recovery = page.locator("[data-source-recovery]");
  await expect(recovery).toBeVisible();
  await expect(recovery).toHaveAttribute("data-source-recovery-kind", "resume");
  await expect(page.locator("[data-source-recovery-source]")).toContainText(phrase);
  // Offered, never adopted. The editor is showing the vault's bytes, the Save
  // button has nothing to send, and the note on disk is untouched — a recovered
  // draft that submitted itself would be this feature's worst failure.
  await expect(page.locator(SURFACE)).not.toContainText(phrase);
  await expect(page.locator("[data-source-save]")).toBeDisabled();
  expect(readFileSync(notePath, "utf8"), "a recovered draft was submitted by itself").toBe(before);

  // And still in storage while the question is on screen. Everything asserted
  // above is in this page's DOM and nowhere else, so this is the one assertion
  // that separates "survived the interruption" from "was read into memory once
  // and deleted" — the second interruption would find nothing left to offer.
  await expect.poll(() => keptDrafts(page)).toHaveLength(1);

  await page.locator("[data-source-recovery-restore]").click();
  await expect(recovery).toBeHidden();
  await expect(page.locator(SURFACE)).toContainText(phrase);
  await expect(page.locator("[data-source-save]")).toBeEnabled();
  expect(readFileSync(notePath, "utf8"), "putting the text back wrote it to the vault").toBe(before);

  await page.locator("[data-source-save]").click();
  await expect(page.locator("[data-source-status]")).toHaveClass(/is-success/);
  expect(readFileSync(notePath, "utf8")).toContain(phrase);
  // The vault holds it now, so the browser's copy is finished.
  await expect.poll(() => keptDrafts(page)).toHaveLength(0);
  expect(gitStatus(manifest.localVault), "the note was written but never snapshotted").toBe("");
});

// The renamed target. The note this tab was opened for stops existing while the
// tab is open, which is the one refusal the editor answers with an offer rather
// than a message: there is nothing left to save over, so the kept text is
// written back as a new note at the path this page was opened for — on a button.
test("a note renamed under the edit offers the kept text back as a new note", async ({ page }) => {
  const notePath = join(manifest.localVault, NOTE);
  const phrase = phraseFor("renamed target");

  await openEditor(page);
  const panel = page.locator("[data-source-editor]");
  const key = await panel.getAttribute("data-source-key");
  const token = await panel.getAttribute("data-source-csrf");

  await caretToEnd(page);
  await page.keyboard.type(`\n${phrase}`);
  await expect.poll(() => keptDrafts(page)).toHaveLength(1);

  // The note moves out from under the tab through the same API the rest of the
  // app renames with. page.request is not the page: the editor is never told.
  const renamed = await page.request.post("/api/notes/rename", {
    headers: { "X-BN-CSRF": token },
    form: { key, title: `Alpha Moved ${phrase}` },
  });
  expect(renamed.ok(), "the out-of-band rename was refused").toBe(true);
  const renamedNote = await renamed.json();
  expect(existsSync(notePath), "the rename left the original path in place").toBe(false);

  await page.locator("[data-source-save]").click();
  await expect(page.locator("[data-source-status]")).toHaveClass(/is-error/);

  const recovery = page.locator("[data-source-recovery]");
  await expect(recovery).toBeVisible();
  await expect(recovery).toHaveAttribute("data-source-recovery-kind", "missing");
  await expect(page.locator("[data-source-recovery-source]")).toContainText(phrase);
  // The one control on this page that writes a file the vault does not have.
  // Its `hidden` lives on a flex child, which needs its own rule to mean
  // anything — so whether it is really on screen can only be asked here.
  await expect(page.locator("[data-source-recovery-recreate]")).toBeVisible();
  await expect(page.locator("[data-source-recovery-restore]")).toBeHidden();
  expect(existsSync(notePath), "the refusal recreated the note by itself").toBe(false);

  await page.locator("[data-source-recovery-recreate]").click();
  // Leaving for the new note is what the create does; both are consequences of
  // the one click, and neither happens without it.
  await expect(page).toHaveURL(/\/n\//);
  expect(readFileSync(notePath, "utf8"), "the kept text was never written back").toContain(phrase);
  // Dropped before the navigation: a copy left behind would be offered back
  // later as unsaved text that is, by then, a file.
  await expect.poll(() => keptDrafts(page)).toHaveLength(0);
  // The recovery is complete. Retire the renamed copy so later Library and
  // backlink journeys see the original fixture identity only.
  await page.goto(renamedNote.path);
  await page.getByText("More actions", { exact: true }).click();
  await page.locator("[data-trash-button]").click();
  await page.getByRole("dialog").getByRole("button", { name: "Move to Trash", exact: true }).click();
  await expect(page).toHaveURL(`${manifest.baseURL}/`);
  await page.waitForLoadState("networkidle");
  expect(gitStatus(manifest.localVault), "the note was written but never snapshotted").toBe("");
});

// The responsive layout, which is the one part of this page that is entirely
// CSS. jsdom parses the stylesheet and applies nothing, so "below 960px the two
// columns become two tabs" can only be asserted where something lays out.
const PANE_EDIT = '[data-source-pane="edit"]';
const PANE_PREVIEW = '[data-source-pane="preview"]';

test("both panes are shown side by side on a wide window", async ({ page }) => {
  await openEditor(page);

  await expect(page.locator(PANE_EDIT)).toBeVisible();
  await expect(page.locator(PANE_PREVIEW)).toBeVisible();
  // Tabs that chose between two panes both already on screen would be a control
  // with nothing to do.
  await expect(page.locator('[data-source-tab="edit"]')).toBeHidden();
});

test.describe("on a narrow window", () => {
  test.use({ viewport: { width: 700, height: 900 } });

  test("the two panes become two tabs", async ({ page }) => {
    await openEditor(page);

    const editTab = page.locator('[data-source-tab="edit"]');
    const previewTab = page.locator('[data-source-tab="preview"]');
    await expect(editTab).toBeVisible();
    await expect(page.locator(PANE_EDIT)).toBeVisible();
    await expect(page.locator(PANE_PREVIEW)).toBeHidden();

    await previewTab.click();
    await expect(page.locator(PANE_PREVIEW)).toBeVisible();
    await expect(page.locator(PANE_EDIT)).toBeHidden();
    // Choosing the pane flushes a render, so what arrives is the server's own
    // HTML for the note now in the editor.
    await expect(page.locator("[data-source-preview]")).toContainText("Alpha body");

    await editTab.click();
    await expect(page.locator(PANE_EDIT)).toBeVisible();
    await expect(page.locator(PANE_PREVIEW)).toBeHidden();

    expect(gitStatus(manifest.localVault), "switching panes wrote to the vault").toBe("");
  });
});

test("Retry preview recovers the current draft and returns keyboard focus", async ({ page }) => {
  await openEditor(page);
  const preview = page.locator("[data-source-preview]");
  await expect(preview).toContainText("Alpha body");
  await page.route("**/api/source/preview", route => route.fulfill({ status: 503, body: "Offline" }));
  const phrase = phraseFor("preview retry");
  await caretToEnd(page);
  await page.keyboard.insertText(`\n\n${phrase}\n`);
  const retry = page.getByRole("button", { name: "Retry preview", exact: true });
  await expect(retry).toBeVisible();
  await expect(page.locator("[data-source-preview-status]")).toContainText("previous preview is outdated");
  await page.unroute("**/api/source/preview");
  await retry.focus();
  await page.keyboard.press("Enter");
  await expect(retry).toBeHidden();
  await expect(preview).toContainText(phrase);
  await expect(page.locator('[data-source-pane="preview"]')).toBeFocused();
  await expect(page.locator("[data-source-save]")).toBeEnabled();
  expect(readFileSync(join(manifest.localVault, NOTE), "utf8")).not.toContain(phrase);
});
