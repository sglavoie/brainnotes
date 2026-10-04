// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initNoteNavigation, libraryResultPath, withLibraryContext } from "./note-navigation";
import { initCopyControls } from "./clipboard";

afterEach(() => {
  document.body.innerHTML = "";
  history.replaceState(null, "", "/");
  vi.unstubAllGlobals();
});

it("carries the full Library search into note links and restores it after navigation", () => {
  const library = "/library?q=caf%C3%A9&tag=one&tag=two&expr=status%3Adraft&sort=-modified&limit=10#results";
  history.replaceState(null, "", library);
  const path = libraryResultPath("/n/Caf%C3%A9#section");
  expect(new URL(path, location.origin).searchParams.get("return_to")).toBe(library);
  history.replaceState(null, "", path);
  document.body.innerHTML = '<a data-note-back-results href="/library" hidden>Back to results</a>';
  initNoteNavigation();
  const back = document.querySelector("a")!;
  expect(back.hidden).toBe(false);
  expect(back.getAttribute("href")).toBe(library);
  expect(libraryResultPath("/n/Other")).toBe("/n/Other");
});

it.each([null, "https://example.com/library", "//example.com/library", "/settings", "javascript:alert(1)", "/\\example.com/library"])(
  "omits the return link for a missing or invalid destination: %s", (value) => {
    const query = value === null ? "" : `?return_to=${encodeURIComponent(value)}`;
    history.replaceState(null, "", `/n/Note${query}`);
    document.body.innerHTML = '<a data-note-back-results href="/library" hidden>Back to results</a><div data-note-context-links><a href="/n/Other">Other</a></div>';
    initNoteNavigation();
    expect(document.querySelector("a")!.hidden).toBe(true);
    expect(document.querySelector("div a")!.getAttribute("href")).toBe("/n/Other");
  }
);

it("carries search context through note links while preserving anchors and other parameters", () => {
  const library = "/library?q=body&tag=work&sort=-modified#results";
  history.replaceState(null, "", `/n/Alpha?return_to=${encodeURIComponent(library)}`);
  document.body.innerHTML = `
    <div data-note-context-links><a href="/n/Beta?mode=read#section">Body</a></div>
    <ul data-note-context-links><li><a href="/n/Backlink">Backlink</a></li></ul>
    <ul data-note-context-links><li><a href="/n/Outgoing?return_to=%2Flibrary%3Fq%3Dold">Outgoing</a></li></ul>`;
  initNoteNavigation();
  const links = [...document.querySelectorAll("a")];
  for (const link of links) expect(new URL(link.href).searchParams.get("return_to")).toBe(library);
  const bodyURL = new URL(links[0].href);
  expect(bodyURL.hash).toBe("#section");
  expect(bodyURL.searchParams.get("mode")).toBe("read");
  history.replaceState(null, "", links[0].href);
  document.body.innerHTML = '<a data-note-back-results hidden></a>';
  initNoteNavigation();
  expect(document.querySelector("a")!.getAttribute("href")).toBe(library);
});

it("leaves external, credentialed, download, same-page and unrelated navigation links alone", () => {
  history.replaceState(null, "", "/n/Alpha?return_to=%2Flibrary%3Fq%3Dbody");
  const paths = ["https://example.com/n/Beta", "//example.com/n/Beta", "#section", "/media/file.pdf", "/editor?key=Alpha", "http://[invalid", `http://user@${location.host}/n/Beta`];
  document.body.innerHTML = '<div data-note-context-links></div><a href="/n/Unrelated">Unrelated</a>';
  for (const path of paths) {
    const link = document.createElement("a");
    link.setAttribute("href", path);
    document.querySelector("div")!.append(link);
  }
  initNoteNavigation();
  expect([...document.querySelectorAll("div a")].map((link) => link.getAttribute("href"))).toEqual(paths);
  expect(document.querySelector("body > a")!.getAttribute("href")).toBe("/n/Unrelated");
});

it("copies a clean absolute note URL and uses the manual fallback when copying fails", async () => {
  history.replaceState(null, "", "/n/Caf%C3%A9?return_to=%2Flibrary%3Fq%3Dprivate#section");
  const copy = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { clipboard: { writeText: copy } });
  document.body.innerHTML = '<div data-copy-controls><button data-copy-note-url data-copy-text="" data-copy-label="Note URL" hidden>Copy note URL</button><p data-copy-status></p></div>';
  initNoteNavigation();
  initCopyControls();
  const button = document.querySelector("button")!;
  expect(button.hidden).toBe(false);
  button.click();
  await vi.waitFor(() => expect(button.disabled).toBe(false));
  expect(copy).toHaveBeenCalledWith(`${location.origin}/n/Caf%C3%A9`);
  copy.mockRejectedValueOnce(new Error("refused"));
  button.click();
  await vi.waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
  expect(document.querySelector("textarea")!.value).toBe(`${location.origin}/n/Caf%C3%A9`);
});

it("keeps Library context through the editor and its return link, including a renamed note", () => {
  const library = "/library?q=caf%C3%A9&tag=one&tag=two&sort=-modified&limit=10#results";
  history.replaceState(null, "", `/n/Alpha?return_to=${encodeURIComponent(library)}`);
  document.body.innerHTML = '<a href="/edit?key=Alpha" data-source-editor-launch>Edit source</a>';
  initNoteNavigation();
  const edit = document.querySelector("a")!;
  expect(new URL(edit.href).searchParams.get("key")).toBe("Alpha");
  expect(new URL(edit.href).searchParams.get("return_to")).toBe(library);
  history.replaceState(null, "", edit.href);
  const renamed = withLibraryContext("/edit?key=Renamed", true);
  history.replaceState(null, "", renamed);
  document.body.innerHTML = '<a href="/n/Renamed" data-source-note-return>Renamed</a>';
  initNoteNavigation();
  history.replaceState(null, "", document.querySelector("a")!.href);
  document.body.innerHTML = '<a data-note-back-results hidden></a>';
  initNoteNavigation();
  expect(document.querySelector("a")!.getAttribute("href")).toBe(library);
});

it.each(["/", "/settings", "https://example.com/edit?key=Alpha", "//example.com/n/Alpha", "#section"])(
  "does not attach editor context to unrelated destinations: %s", (target) => {
    history.replaceState(null, "", "/edit?key=Alpha&return_to=%2Flibrary%3Fq%3Dbody");
    expect(withLibraryContext(target, true)).toBe(target);
  }
);

it.each(["https://example.com/library", "/settings", "//example.com/library"])(
  "does not pass an unsafe return URL into the editor: %s", (target) => {
    history.replaceState(null, "", `/n/Alpha?return_to=${encodeURIComponent(target)}`);
    expect(withLibraryContext("/edit?key=Alpha", true)).toBe("/edit?key=Alpha");
  }
);
