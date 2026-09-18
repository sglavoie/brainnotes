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
      maxBytes: 1048576,
      endpoints: {
        read: "/api/source",
        preview: "/api/source/preview",
        save: "/api/source/save",
        search: "/api/search",
      },
    });
  });

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
