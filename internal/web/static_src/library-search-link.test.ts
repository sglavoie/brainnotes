// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initCopyControls } from "./clipboard";
import { updateBrowserQuery } from "./filters";
import { initLibrarySearchLink } from "./library-search-link";

afterEach(() => {
  document.body.innerHTML = "";
  window.history.replaceState(null, "", "/");
  vi.unstubAllGlobals();
});

const setup = (): HTMLButtonElement => {
  document.body.innerHTML = `<div data-copy-controls>
    <button data-copy-search-link data-copy-text="" data-copy-label="Search link" hidden>Copy search link</button>
    <span data-copy-status></span>
  </div>`;
  initLibrarySearchLink();
  initCopyControls();
  return document.querySelector("button")!;
};

it("copies the complete current search, excluding navigation state", async () => {
  const writeText = vi.fn<(text: string) => Promise<void>>(async () => {});
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  window.history.replaceState(null, "", "/library?q=caf%C3%A9&tag=work&tag=ideas&expr=status:wip&sort=relevance&relative_to=Hub&limit=10&return_to=ignore#result-1");
  const button = setup();
  expect(button.hidden).toBe(false);
  button.click();
  await vi.waitFor(() => expect(button.disabled).toBe(false));
  const copied = new URL(writeText.mock.calls[0][0]);
  expect(copied.pathname).toBe("/library");
  expect(copied.hash).toBe("");
  expect([...copied.searchParams]).toEqual([
    ["q", "café"], ["tag", "work"], ["tag", "ideas"], ["expr", "status:wip"],
    ["sort", "relevance"], ["relative_to", "Hub"], ["limit", "10"],
  ]);
  updateBrowserQuery(new URLSearchParams("q=changed&sort=modified"));
  button.click();
  await vi.waitFor(() => expect(button.disabled).toBe(false));
  expect(writeText).toHaveBeenLastCalledWith(`${location.origin}/library?q=changed&sort=modified`);
});

it("clears obsolete fallback text and copies the search restored by browser navigation", async () => {
  vi.stubGlobal("navigator", { clipboard: undefined });
  window.history.replaceState(null, "", "/library?q=first");
  const button = setup();
  button.click();
  await vi.waitFor(() => expect(button.disabled).toBe(false));
  expect(document.querySelector<HTMLTextAreaElement>("[data-copy-fallback]")!.value).toContain("q=first");
  window.history.replaceState(null, "", "/library?q=restored&sort=path");
  window.dispatchEvent(new PopStateEvent("popstate"));
  expect(document.querySelector("[data-copy-fallback]")).toBeNull();
  button.click();
  await vi.waitFor(() => expect(button.disabled).toBe(false));
  const fallback = document.querySelector<HTMLTextAreaElement>("[data-copy-fallback]")!;
  expect(fallback.value).toBe(`${location.origin}/library?q=restored&sort=path`);
  expect(document.activeElement).toBe(fallback);
});
