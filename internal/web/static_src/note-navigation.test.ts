// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initNoteNavigation, libraryResultPath } from "./note-navigation";
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
    document.body.innerHTML = '<a data-note-back-results href="/library" hidden>Back to results</a>';
    initNoteNavigation();
    expect(document.querySelector("a")!.hidden).toBe(true);
  }
);

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
