// What every editor journey needs before it can assert anything of its own.
//
// These were `editor.spec.js`'s own definitions until a second spec needed the
// same four things — opening the editor the way a person opens it, a phrase no
// other file on this machine contains, the caret at the end of the document, and
// this origin's kept drafts. A second copy of `openEditor` would be a second
// answer to "is this page mounted", and the two would drift the first time the
// page grew an element worth waiting for.

import { expect, manifest } from "./fixture.js";

// The editable text CodeMirror renders. Its presence is the mount: the server
// sends an empty <div data-source-surface>, so .cm-content exists only because
// the shipped bundle constructed an EditorView in it.
export const SURFACE = "[data-source-surface] .cm-content";

// A phrase no other file on this machine contains, so finding it on disk
// identifies this run's write rather than any pre-existing text.
export function phraseFor(what) {
  return `e2e ${what} ${manifest.fixtureRoot.split("/").pop()}`;
}

// openEditor walks the path a person walks: the note page, then its "Edit
// source" link. Navigating straight to /edit would skip the one affordance that
// makes the editor reachable at all.
//
// The route and the opening text are parameters because the note is not always
// Alpha — a journey that creates a note has to open the note it created — but
// every assertion below is about the editor rather than the note, so they are
// the same assertions whichever note is behind them.
export async function openEditor(page, route = "/n/Alpha", opening = "Alpha body") {
  await page.goto(route);
  // The note page's own scripts fetch insights on load, and a save posted into
  // that window contends for the vaultgit safety lock. Waiting for the page to
  // go quiet writes after it has finished its own work, which is also what a
  // person does — the same reason isolation.spec.js waits before a capture.
  await page.waitForLoadState("networkidle");

  await page.locator("[data-source-editor-launch]").click();
  await expect(page).toHaveURL(/\/edit\?key=/);

  const surface = page.locator(SURFACE);
  await expect(surface).toContainText(opening);
  // A freshly loaded note is clean, so Save starts disabled. Asserting it here
  // is what gives the later `toBeEnabled()` its meaning: without it, that
  // assertion would pass just as well on a page whose Save button was never
  // disabled at all, and "the keystrokes reached CodeMirror" would be proved by
  // a button that was always on.
  await expect(page.locator("[data-source-save]")).toBeDisabled();
  // The conflict panel is `hidden` in the template, and `hidden` is only a
  // user-agent `display: none` — a stylesheet that sets `display` on the same
  // element outranks it. This is the assertion that keeps that from coming back
  // as an empty "this note changed on disk" warning over every edit; jsdom
  // applies no stylesheet and cannot see it.
  await expect(page.locator("[data-source-conflict]")).toBeHidden();
  // The recovery panel is the second element on this page that is `hidden` in
  // the template and styled with `display`, and it carries a `hidden` button of
  // its own inside a flex row. Same trap, same blind spot in jsdom: without its
  // own `[hidden] { display: none; }` it would sit above every edit asking about
  // a draft nobody has.
  await expect(page.locator("[data-source-recovery]")).toBeHidden();
  await page.waitForLoadState("networkidle");
  return surface;
}

// keptDrafts reads this origin's draft records. The key prefix is spelled out
// rather than imported: it is the contract between two page loads — and, for a
// note somebody left open on another machine, between two builds — so a test
// that asked the bundle for it would assert nothing.
export const DRAFT_KEY_PREFIX = "bn.draft.1|";

export function keptDrafts(page) {
  return page.evaluate(
    (prefix) =>
      Object.keys(window.localStorage)
        .filter((key) => key.startsWith(prefix))
        .map((key) => ({ key, ...JSON.parse(window.localStorage.getItem(key)) })),
    DRAFT_KEY_PREFIX,
  );
}

// caretToEnd clicks the last rendered line and puts the caret after it.
// CodeMirror renders only the lines in view, so this is a click on a real
// element rather than a cursor command that depends on the platform keymap.
export async function caretToEnd(page) {
  await page.locator("[data-source-surface] .cm-line").last().click();
  await page.keyboard.press("End");
}
