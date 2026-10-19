// Attaching a file to a note, and managing the note, in a real browser.
//
// internal/web/static_src/source-attachment*.test.ts and source-editor-manage
// .test.ts already decide everything these journeys decide — which door a file
// came through, what a staged row says, what a cancel drops, which actions the
// dirty gate refuses outright. What they cannot have is the part that is not a
// decision: a file chooser the operating system opened, a drop landing on the
// element CodeMirror installed its own handlers on, a destination the *server*
// chose arriving back as percent-encoded Markdown in a mounted document, a
// the application dialog in front of a trash, and a `<details>` nothing inside is
// clickable until it is open.
//
// The line this file holds is ownership: a file is in the vault when a save put
// it there and at no earlier moment, and a note that has been created,
// propertied, pinned, moved, trashed and restored is the same note throughout.
// Every test therefore ends where editor.spec.js's do — `git status` over both
// vaults — because a journey that leaves a stray file behind has not preserved
// anything.
//
// This spec works on `Beta.md` and on notes it creates. `Alpha.md` belongs to
// editor.spec.js, which renames it away and recreates it; the suite runs one
// worker over one vault pair, so two specs editing one note would be two tests
// editing one note.

import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { caretToEnd, keptDrafts, openEditor, phraseFor, SURFACE } from "./editor-helpers.js";
import { expect, gitStatus, manifest, test } from "./fixture.js";

import { NOTE, ATTACHMENTS, ROWS, MARKER, upload, linkFor, vaultPath, openBeta, openManage, createNote, sendFiles, stagedLeftovers, expectEditorKey, trashedPath } from "./editor-journey-helpers.js";

test("a file chosen in the browser is staged, linked, and enters the vault with the save", async ({
  page,
}) => {
  const notePath = vaultPath(NOTE);
  const before = readFileSync(notePath, "utf8");
  expect(gitStatus(manifest.localVault), "the local vault did not start clean").toBe("");

  const file = upload("chosen");
  const filed = vaultPath(ATTACHMENTS, file.name);
  expect(existsSync(filed), "the fixture already holds this run's attachment").toBe(false);

  await openBeta(page);
  await caretToEnd(page);

  const chooser = page.waitForEvent("filechooser");
  await page.locator("[data-source-attachment-choose]").click();
  await (await chooser).setFiles(file);

  // The panel is `hidden` in the template and styled with `display`, the same
  // trap the conflict and recovery panels carry. jsdom applies no stylesheet and
  // cannot see it.
  await expect(page.locator("[data-source-attachments]")).toBeVisible();
  const row = page.locator(ROWS).first();
  await expect(row).toHaveAttribute("data-source-attachment-state", "staged");
  await expect(row).toContainText(`${ATTACHMENTS}/${file.name}`);

  // Staged is not saved, and this is the assertion the whole feature rests on.
  expect(existsSync(filed), "a staged file is already in the vault").toBe(false);
  expect(readFileSync(notePath, "utf8"), "the note changed before the save").toBe(before);

  await expect(page.locator(SURFACE)).toContainText(linkFor(file.name));
  // The inserted link is an edit like any other, so the editor is now dirty.
  await expect(page.locator("[data-source-save]")).toBeEnabled();

  await page.keyboard.press("ControlOrMeta+s");
  await expect(page.locator("[data-source-status]")).toHaveClass(/is-success/);

  expect(readFileSync(filed, "utf8"), "the committed bytes are not the sent bytes").toBe(
    file.buffer.toString(),
  );
  expect(readFileSync(notePath, "utf8")).toContain(linkFor(file.name));
  // The save settled the row, so the panel has nothing left to show.
  await expect(page.locator("[data-source-attachments]")).toBeHidden();
  await expect
    .poll(stagedLeftovers, { message: "the committed upload is still staged" })
    .toEqual([]);

  expect(existsSync(join(manifest.peerVault, ATTACHMENTS, file.name))).toBe(false);
  expect(gitStatus(manifest.localVault), "the local vault is dirty after the save").toBe("");
  expect(gitStatus(manifest.peerVault), "the peer vault was written to").toBe("");
});

test("a file dropped on the editor and one pasted into it are staged the same way", async ({
  page,
}) => {
  const dropped = upload("dropped");
  const pasted = upload("pasted");
  expect(gitStatus(manifest.localVault)).toBe("");

  await openBeta(page);
  await caretToEnd(page);

  const drop = await sendFiles(page, "drop", [dropped]);
  // The panel took the drop *instead of* CodeMirror, not alongside it. What it
  // is being kept away from: CodeMirror answers a dropped file by inserting its
  // bytes into the document as text and selecting them, so a drop it sees puts
  // the file's contents in the note and arms the next insertion to replace them
  // — which is why the document is the wrong place to look and this is checked
  // at the event instead. Remove either the capture flag or the stopPropagation
  // from source-attachment-panel.ts and this line is the one that fails.
  expect(drop.reachedEditor, "the drop reached CodeMirror's own handlers").toBe(false);
  await expect(page.locator(ROWS)).toHaveCount(1);
  await sendFiles(page, "paste", [pasted]);
  await expect(page.locator(ROWS)).toHaveCount(2);
  await expect(page.locator(`${ROWS}[data-source-attachment-state="staged"]`)).toHaveCount(2);

  for (const file of [dropped, pasted]) {
    expect(existsSync(vaultPath(ATTACHMENTS, file.name)), `${file.name} was not staged`).toBe(false);
    await expect(page.locator(SURFACE)).toContainText(linkFor(file.name));
  }

  await page.keyboard.press("ControlOrMeta+s");
  await expect(page.locator("[data-source-status]")).toHaveClass(/is-success/);
  await expect(page.locator("[data-source-attachments]")).toBeHidden();

  const note = readFileSync(vaultPath(NOTE), "utf8");
  for (const file of [dropped, pasted]) {
    expect(readFileSync(vaultPath(ATTACHMENTS, file.name), "utf8")).toBe(file.buffer.toString());
    expect(note).toContain(linkFor(file.name));
  }
  await expect.poll(stagedLeftovers, { message: "an upload is still staged" }).toEqual([]);
  expect(gitStatus(manifest.localVault)).toBe("");
  expect(gitStatus(manifest.peerVault)).toBe("");
});

test("a staged file removed before the save never reaches the vault", async ({ page }) => {
  const file = upload("removed");
  const filed = vaultPath(ATTACHMENTS, file.name);
  expect(gitStatus(manifest.localVault)).toBe("");

  await openBeta(page);
  await caretToEnd(page);
  await page.locator("[data-source-attachment-input]").setInputFiles(file);
  await expect(page.locator(ROWS).first()).toHaveAttribute(
    "data-source-attachment-state",
    "staged",
  );

  // "Remove" rather than "Cancel": the row is staged, so there is a file on the
  // server to drop rather than a request to abandon.
  await page.locator("[data-source-attachment-cancel]").click();
  await expect(page.locator("[data-source-attachments]")).toBeHidden();
  await expect(page.locator("[data-source-attachment-status]")).toContainText("It is not in the vault.");
  // Removed from staging too, not just from the list. A row can go away with the
  // bytes still on the server, and no other assertion in this suite would see it.
  await expect
    .poll(stagedLeftovers, { message: "the removed upload is still staged" })
    .toEqual([]);
  // The link is deliberately left where it was inserted: the text belongs to the
  // person now. This asserts the page does not reach into their document.
  await expect(page.locator(SURFACE)).toContainText(linkFor(file.name));

  await page.keyboard.press("ControlOrMeta+s");
  await expect(page.locator("[data-source-status]")).toHaveClass(/is-success/);
  expect(existsSync(filed), "a removed file reached the vault on the next save").toBe(false);
  expect(readFileSync(vaultPath(NOTE), "utf8")).toContain(linkFor(file.name));
  expect(gitStatus(manifest.localVault)).toBe("");
  expect(gitStatus(manifest.peerVault)).toBe("");
});

test("unsaved text holds a rename until it is saved, and nothing is left under the old note", async ({
  page,
}) => {
  const title = `Gate ${MARKER}`;
  const renamed = `Renamed ${MARKER}`;
  const phrase = phraseFor("gated rename");
  expect(gitStatus(manifest.localVault)).toBe("");

  await openBeta(page);
  await createNote(page, title);
  await openEditor(page, `/n/${encodeURIComponent(title)}`, `title: ${title}`);

  await caretToEnd(page);
  await page.keyboard.type(phrase);
  await expect.poll(() => keptDrafts(page)).toHaveLength(1);

  // Renaming is available above the editor without opening management.
  await expect(page.locator("[data-source-manage]")).not.toHaveAttribute("open");
  await expect(page.getByLabel("Note title", { exact: true })).toHaveValue(title);
  await page.locator("[data-source-manage] > summary").click();
  await page.getByText("Rename note", { exact: true }).click();
  await page.locator("[data-source-rename]").scrollIntoViewIfNeeded();
  await expect(page.locator("[data-source-rename]")).toBeInViewport();
  await page.getByLabel("Note title", { exact: true }).fill(renamed);
  await page.locator("[data-source-rename]").click();

  // A rename changes where the note lives, which is where the unsaved text is
  // filed too, so this is the one refusal with no way past it.
  const gate = page.locator("[data-source-gate]");
  await expect(gate).toBeVisible();
  await expect(page.locator("[data-source-gate-message]")).toContainText(
    "it has to be saved first, or not done",
  );
  await expect(page.locator("[data-source-gate-proceed]")).toBeHidden();
  expect(existsSync(vaultPath(`${renamed}.md`)), "the gate let the rename through").toBe(false);

  await page.locator("[data-source-gate-cancel]").click();
  await expect(gate).toBeHidden();
  await expect(page.locator('[data-source-manage-feedback="rename"] [data-source-manage-status]')).toContainText(
    "Rename this note: canceled.",
  );
  expect(existsSync(vaultPath(`${title}.md`))).toBe(true);

  await page.locator("[data-source-rename]").click();
  await expect(gate).toBeVisible();
  await page.locator("[data-source-gate-save]").click();

  await expectEditorKey(page, renamed);
  await expect(page.getByLabel("Note title", { exact: true })).toHaveValue(renamed);
  expect(existsSync(vaultPath(`${title}.md`)), "the old note is still there").toBe(false);
  expect(readFileSync(vaultPath(`${renamed}.md`), "utf8")).toContain(phrase);
  // The draft was for a note that no longer exists. A save clears it, which is
  // what keeps the recovery panel from offering it back over the renamed note.
  await expect.poll(() => keptDrafts(page)).toHaveLength(0);
  expect(gitStatus(manifest.localVault)).toBe("");
  expect(gitStatus(manifest.peerVault)).toBe("");
});

test("a note created from the editor survives a property, a pin, a move, a trash and an undo", async ({
  page,
}) => {
  // Trash and undo use application dialogs before sending any write.
  const title = `Journey ${MARKER}`;
  const moved = `Notes/${title}`;
  expect(gitStatus(manifest.localVault)).toBe("");

  await openBeta(page);
  await createNote(page, title);
  await openEditor(page, `/n/${encodeURIComponent(title)}`, `title: ${title}`);
  await openManage(page);

  await page.locator("[data-source-property-name]").fill("status");
  await page.locator("[data-source-property-value]").fill("journeyed");
  await page.locator("[data-source-property-set]").click();
  // A property rewrites the note behind the editor's back, so a clean editor
  // re-reads it. The property arriving on screen is the vault's own answer.
  await expect(page.locator(SURFACE)).toContainText("status: journeyed");

  await page.locator("[data-source-pin]").click();
  // The message rather than the success class, because the class is still on the
  // element from the property above. Waiting for the answer is also what keeps
  // the next action from arriving while this one still holds the Git safety
  // lock, which is taken without waiting and answers a 409 rather than queueing
  // (the mechanism is spelled out where the pin and unpin are paired, below).
  await expect(page.locator('[data-source-manage-feedback="pins"] [data-source-manage-status]')).toHaveText("Pinned note.");
  // A pin does not touch this note's bytes as far as the editor is concerned, so
  // it does not reload: the file is the only witness.
  expect(readFileSync(vaultPath(`${title}.md`), "utf8")).toContain("pinned:");

  await page.locator("[data-source-move-folder]").fill("Notes");
  await page.locator("[data-source-move]").click();
  await expectEditorKey(page, moved);
  expect(existsSync(vaultPath(`${title}.md`)), "the note is still at its old path").toBe(false);

  await openManage(page);
  await page.locator("[data-source-trash]").click();
  await page.getByRole("dialog").getByRole("button", { name: "Move to Trash", exact: true }).click();
  await expect(page).toHaveURL(`${manifest.baseURL}/`);
  expect(existsSync(vaultPath("Notes", `${title}.md`))).toBe(false);
  expect(existsSync(trashedPath(`${moved}.md`)), "the note is not in Trash/").toBe(true);

  // Undo is a vault-wide action, so it is reached from whatever note is open.
  await openBeta(page);
  await openManage(page);
  await page.locator("[data-source-undo]").click();
  const confirmation = page.getByRole("dialog");
  await expect(confirmation).toContainText(`${title}.md`);
  await confirmation.getByRole("button", { name: "Restore note", exact: true }).click();
  await expect(page.locator('[data-source-manage-feedback="undo"] [data-source-manage-status]')).toHaveClass(/is-success/);

  await expect.poll(() => existsSync(vaultPath("Notes", `${title}.md`))).toBe(true);
  const restored = readFileSync(vaultPath("Notes", `${title}.md`), "utf8");
  expect(restored, "the restored note lost its property").toContain("status: journeyed");
  expect(restored, "the restored note lost its pin").toContain("pinned:");
  expect(
    existsSync(trashedPath(`${moved}.md`)),
    "the note is in Trash/ and back in the vault at once",
  ).toBe(false);
  expect(gitStatus(manifest.localVault)).toBe("");
  expect(gitStatus(manifest.peerVault)).toBe("");
});

test("a property removed, a pin lifted, and a line captured over unsaved text", async ({ page }) => {
  const title = `Aside ${MARKER}`;
  const notePath = vaultPath(`${title}.md`);
  const inbox = vaultPath("Inbox.md");
  const phrase = phraseFor("kept through a capture");
  const captured = `e2e captured ${MARKER}`;
  expect(readFileSync(inbox, "utf8"), "Inbox already holds this run's line").not.toContain(captured);
  expect(gitStatus(manifest.localVault)).toBe("");

  await openBeta(page);
  await createNote(page, title);
  await openEditor(page, `/n/${encodeURIComponent(title)}`, `title: ${title}`);
  await openManage(page);

  // Set then removed through the same field, which is the only way a person
  // would do it — and the removal is the half no browser has ever run.
  await page.locator("[data-source-property-name]").fill("status");
  await page.locator("[data-source-property-value]").fill("temporary");
  await page.locator("[data-source-property-set]").click();
  await expect(page.locator(SURFACE)).toContainText("status: temporary");
  await page.locator("[data-source-property-unset]").click();
  await expect(page.locator(SURFACE)).not.toContainText("status: temporary");

  // Each of these waits for its own sentence before the next is asked for. The
  // file changing is not enough: a mutation holds the Git safety lock for its
  // whole operation and that lock is taken without waiting, so an unpin sent
  // inside the pin's is refused — vaultgit's preflight TryLockPath fails,
  // KindBusy comes back, noteops.classifySafetyError turns it into CodeConflict
  // and the browser gets a 409. Unwaited-for, that is a test that removes
  // nothing and says so only in the file it reads back.
  await page.locator("[data-source-pin]").click();
  await expect(page.locator('[data-source-manage-feedback="pins"] [data-source-manage-status]')).toHaveText("Pinned note.");
  expect(readFileSync(notePath, "utf8")).toContain("pinned:");
  await page.locator("[data-source-unpin]").click();
  await expect(page.locator('[data-source-manage-feedback="pins"] [data-source-manage-status]')).toHaveText("Unpinned note.");
  expect(readFileSync(notePath, "utf8")).not.toContain("pinned:");

  await caretToEnd(page);
  // This journey checks draft retention across a capture. Insert one text
  // input so browser selection events cannot interleave rapid synthetic keys.
  await page.keyboard.insertText(phrase);
  await expect(page.locator(SURFACE)).toContainText(phrase);
  await expect(page.locator("[data-source-save]")).toBeEnabled();

  await page.locator("[data-source-capture-text]").fill(captured);
  await page.locator("[data-source-capture]").click();

  // A capture writes to Inbox and never to this note, so the gate offers a way
  // past itself instead of refusing outright. That button is `hidden` on a flex
  // child, which is a stylesheet's business: jsdom reads the attribute, a
  // browser is where it is either on the screen or not.
  const gate = page.locator("[data-source-gate]");
  await expect(gate).toBeVisible();
  const proceed = page.locator("[data-source-gate-proceed]");
  await expect(proceed).toBeVisible();
  await expect(proceed).toHaveText("Capture this line anyway");
  await proceed.click();
  await expect(gate).toBeHidden();

  // The field is cleared by the same handler that reports the success, so its
  // emptiness is the answer having arrived — a class this element already had is
  // not.
  await expect(page.locator("[data-source-capture-text]")).toHaveValue("");
  await expect(page.locator('[data-source-manage-feedback="capture"] [data-source-manage-status]')).toHaveClass(/is-success/);
  expect(readFileSync(inbox, "utf8")).toContain(captured);

  // Proceeding is not saving. The text the gate asked about is still unsaved,
  // still on the screen, and still nowhere in the vault.
  await expect(page.locator(SURFACE)).toContainText(phrase);
  await expect(page.locator("[data-source-save]")).toBeEnabled();
  expect(readFileSync(notePath, "utf8")).not.toContain(phrase);
  expect(gitStatus(manifest.localVault)).toBe("");
  expect(gitStatus(manifest.peerVault)).toBe("");
});
