// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";

import { readSourceEditorConfig, readSourceEditorElements } from "./source-editor-elements";
import { initSourceEditor } from "./source-editor";
import { editorMarkup } from "./source-editor.test-helpers";

const panel = (): HTMLElement => {
  const element = document.querySelector<HTMLElement>("[data-source-editor]");
  if (!element) {
    throw new Error("fixture is missing the editor panel");
  }
  return element;
};

afterEach(() => {
  document.body.innerHTML = "";
});

describe("readSourceEditorConfig", () => {
  it("reads the endpoints and identity the page published", () => {
    document.body.innerHTML = editorMarkup();
    expect(readSourceEditorConfig(panel())).toEqual({
      key: "alpha",
      token: "csrf-token",
      relPath: "Alpha.md",
      vaultID: "sync:vault-under-test",
      maxBytes: 1048576,
      endpoints: {
        read: "/api/source",
        preview: "/api/source/preview",
        save: "/api/source/save",
        create: "/api/source/create",
        search: "/api/search",
      },
      attachments: {
        upload: "/api/attachments/upload",
        cancel: "/api/attachments/cancel",
      },
      manage: {
        editor: "/edit",
        home: "/",
        capture: "/api/capture",
        create: "/api/notes/create",
        rename: "/api/notes/rename",
        move: "/api/notes/move",
        trash: "/api/notes/trash",
        frontmatterSet: "/api/notes/frontmatter/set",
        frontmatterUnset: "/api/notes/frontmatter/unset",
        pinSet: "/api/pins/set",
        pinRemove: "/api/pins/remove",
        undo: "/api/undo",
      },
      maxAttachmentBytes: 26214400,
    });
  });

  // Attaching and managing are what a *hosted* page publishes. A read-only
  // replica serves the same template with those routes empty, and a page that
  // cannot upload must still be a page somebody can read and edit — so an absent
  // route is an absent feature, never an absent editor.
  it("keeps the editor usable on a page that publishes no attachment or manage routes", () => {
    document.body.innerHTML = editorMarkup();
    for (const attribute of [
      "data-source-upload-endpoint",
      "data-source-upload-cancel-endpoint",
      "data-source-max-attachment-bytes",
      "data-source-rename-endpoint",
      "data-source-trash-endpoint",
      "data-source-undo-endpoint",
      "data-source-home-path",
    ]) {
      panel().removeAttribute(attribute);
    }
    const config = readSourceEditorConfig(panel());
    expect(config).not.toBeNull();
    expect(config?.attachments).toEqual({ upload: "", cancel: "" });
    expect(config?.maxAttachmentBytes).toBe(0);
    expect(config?.manage.rename).toBe("");
    // Read from the page rather than defaulted, which is why the attribute is
    // removed here rather than asserted in place: with `data-source-home-path`
    // left alone this line would echo the fixture's own `/` back and would hold
    // just as well if nothing read the attribute at all. The value the server
    // publishes is pinned by the whole-config reading above.
    expect(config?.manage.home).toBe("");
  });

  // The vault identity is the one published field whose absence must not take
  // the editor away. Without it there are no browser-local drafts — a key of
  // note-plus-tab would be the same key for `Alpha.md` in every vault this
  // origin has ever served — but editing and saving are untouched.
  it("keeps the editor usable on a page with no vault identity", () => {
    document.body.innerHTML = editorMarkup();
    panel().removeAttribute("data-source-vault-id");
    const config = readSourceEditorConfig(panel());
    expect(config).not.toBeNull();
    expect(config?.vaultID).toBe("");
  });

});

// What this does with a page that is missing something: a page it cannot run on
// at all, and a page it can run on anyway.
describe("readSourceEditorConfig on an incomplete page", () => {
  // An editor that posts to "" is worse than no editor: it looks like it is
  // working and loses the text. Every route string is the server's, so a page
  // that did not publish one is a page this cannot run on.
  it("refuses a page missing any endpoint, the key or the token", () => {
    for (const attribute of [
      "data-source-key",
      "data-source-csrf",
      "data-source-read-endpoint",
      "data-source-preview-endpoint",
      "data-source-save-endpoint",
      "data-source-create-endpoint",
      "data-source-search-endpoint",
    ]) {
      document.body.innerHTML = editorMarkup();
      panel().removeAttribute(attribute);
      expect(readSourceEditorConfig(panel()), `missing ${attribute}`).toBeNull();
    }
  });

  // maxBytes is the one field with a usable absence: the size pre-check is a
  // courtesy and the server enforces the limit regardless.
  it("tolerates an unreadable byte limit", () => {
    document.body.innerHTML = editorMarkup();
    panel().setAttribute("data-source-max-bytes", "not a number");
    expect(readSourceEditorConfig(panel())?.maxBytes).toBe(0);
  });
});

describe("readSourceEditorElements", () => {
  it("refuses a panel with nowhere to mount the surface", () => {
    document.body.innerHTML = editorMarkup();
    panel().querySelector("[data-source-surface]")?.remove();
    expect(readSourceEditorElements(panel())).toBeNull();
  });
});

describe("initSourceEditor", () => {
  // Every page but /edit. The bundle is one file served everywhere, so the
  // entry point has to be inert wherever the panel is absent.
  it("does nothing on a page with no editor panel", () => {
    document.body.innerHTML = "<main><p>Just a note page.</p></main>";
    expect(() => initSourceEditor()).not.toThrow();
  });
});
