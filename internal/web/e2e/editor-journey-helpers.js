// Shared constants and drivers for editor-journey.spec.js: file delivery through
// the paste and drop doors, the staging-tree reading, and note management.

import { existsSync, readdirSync } from "node:fs";
import { join } from "node:path";

import { openEditor } from "./editor-helpers.js";
import { expect, manifest } from "./fixture.js";

export const NOTE = "Beta.md";
// The vault-relative directory attachments are committed into: config's
// DefaultAttachmentsDir, which the fixture's brainnotes.toml does not override.
export const ATTACHMENTS = "attachments";
export const ROWS = "[data-source-attachment-list] > li";
export const MARKER = manifest.fixtureRoot.split("/").pop();

// Every uploaded file carries a space and a pair of parentheses, because those
// are exactly the characters internal/attachments/name.go keeps and Markdown
// cannot take literally. A name with neither would pass through an encoder that
// does nothing.
//
// The bytes are deliberately nothing like the name. CodeMirror's own drop
// handler reads a dropped file and inserts its *contents* as text, so a payload
// distinguishable from the link is what lets a test tell "the attachment panel
// took this drop" apart from "CodeMirror did".
export function upload(what) {
  return {
    name: `e2e ${what} (v1) ${MARKER}.png`,
    mimeType: "image/png",
    buffer: Buffer.from(`bytes-not-text ${what} ${MARKER}`),
  };
}

// linkFor spells the expected reference out rather than building it with the
// page's own encoder, which would assert that the encoder equals itself. A
// space must arrive as %20 and the parentheses as %28/%29 — an unencoded `)`
// ends the destination early and points the link at a prefix of the file that
// was written.
export function linkFor(name) {
  const destination = name.replaceAll(" ", "%20").replaceAll("(", "%28").replaceAll(")", "%29");
  return `![${name}](${ATTACHMENTS}/${destination})`;
}

export function vaultPath(...parts) {
  return join(manifest.localVault, ...parts);
}

export function openBeta(page) {
  return openEditor(page, "/n/Beta", "Beta body");
}

// openManage opens the management panel. It is a closed <details> on load, so
// nothing inside it is clickable — or visible to a screen reader — until the
// summary has been activated, which is a fact about the shipped page that no
// fixture built from a string can have.
export async function openManage(page) {
  await page.locator("[data-source-manage] > summary").click();
  await expect(page.locator("[data-source-move]")).toBeVisible();
}

export async function createNote(page, title) {
  await openManage(page);
  await page.locator("[data-source-note-create-target]").fill(title);
  await page.locator("[data-source-note-create]").click();
  // The status echoes the path the *server* filed the note at, so asserting it
  // and then the disk is asserting the page did not invent either.
  await expect(page.locator('[data-source-manage-feedback="create"] [data-source-manage-status]')).toContainText(`${title}.md`);
  expect(existsSync(vaultPath(`${title}.md`)), "the created note is not in the vault").toBe(true);
}

// sendFiles delivers files through the paste and drop doors. The events are
// constructed in the page because a drag from the desktop and a ⌘V of a
// screenshot are both outside what a browser can be driven to originate.
//
// The target is CodeMirror's own content element, not the surface the listeners
// are on, and that is the whole point of running this in a browser. The panel
// binds `drop` in the capture phase and calls stopPropagation (see
// source-attachment-panel.ts) precisely because CodeMirror installs a drop
// handler of its own on the element it mounts *inside* the surface. Dispatching
// at the surface — which is what the jsdom test does, there being nothing
// mounted under it — never travels through CodeMirror, so capture-versus-bubble
// is unobservable and the stopPropagation is a no-op. Dispatching here, the two
// handlers are in the same path and the right one has to win.
export function sendFiles(page, kind, files) {
  const payload = files.map((file) => ({
    name: file.name,
    type: file.mimeType,
    text: file.buffer.toString(),
  }));
  return page.evaluate(
    ({ kind: which, payload: sent }) => {
      const transfer = new DataTransfer();
      for (const file of sent) {
        transfer.items.add(new File([file.text], file.name, { type: file.type }));
      }
      const target = document.querySelector("[data-source-surface] .cm-content");
      if (!target) {
        throw new Error("the editing surface has no CodeMirror content element");
      }
      // What CodeMirror would do with the file — read it through a FileReader
      // and insert its bytes as text — happens later than this call returns, so
      // asserting on the document right afterwards asserts on a race. Whether
      // the event ever reached CodeMirror's handlers is settled synchronously,
      // and this witness is how a browser says so: it is registered on the same
      // element and in the same phase as CodeMirror's own listener, so it runs
      // if and only if that one did. stopPropagation from the capture phase
      // above ends the event before the target phase, taking both with it.
      let reachedEditor = false;
      const witness = () => {
        reachedEditor = true;
      };
      target.addEventListener(which, witness);
      target.dispatchEvent(
        which === "paste"
          ? new ClipboardEvent("paste", { clipboardData: transfer, bubbles: true, cancelable: true })
          : new DragEvent("drop", { dataTransfer: transfer, bubbles: true, cancelable: true }),
      );
      target.removeEventListener(which, witness);
      return { reachedEditor };
    },
    { kind, payload },
  );
}

// stagedLeftovers lists what the attachment staging tree still holds: a blob, a
// record, and the claim on the destination name (internal/attachments/store.go).
//
// It is read by hand because fixture.js excludes `.bnotes/uploads/` from
// gitStatus — staging is outside the vault's history by design — and that
// exclusion is what takes away this suite's only end-to-end sight of it. Store
// .Forget swallows every failure on purpose, so a save or a cancel that leaves
// bytes behind is otherwise invisible here.
export function stagedLeftovers() {
  const staging = vaultPath(".bnotes", "uploads");
  if (!existsSync(staging)) {
    return [];
  }
  const claims = join(staging, "names");
  return [
    ...readdirSync(staging).filter((entry) => entry.endsWith(".bin") || entry.endsWith(".json")),
    ...(existsSync(claims) ? readdirSync(claims) : []),
  ];
}

// expectEditorKey waits for the editor to be open on one note.
//
// The comparison is over the *key* rather than the URL string, and it folds
// case, because neither of those is this test's subject: the server answers a
// rename with its own normalized key, and both the encoding of a space (`+` from
// a note page's link, `%20` from the editor's) and the folding are its business.
// What has to be true here is that the editor reopened on the note the operation
// produced rather than on the one it moved away from.
export async function expectEditorKey(page, key) {
  await expect
    .poll(() => new URL(page.url()).searchParams.get("key")?.toLowerCase())
    .toBe(key.toLowerCase());
}

// trashedPath is where Trash holds a note: the note's own vault-relative path
// under `Trash/`, folder and all. A collision adds a timestamp and a short id to
// the name, which an empty Trash cannot produce.
export function trashedPath(relPath) {
  return vaultPath("Trash", relPath);
}
