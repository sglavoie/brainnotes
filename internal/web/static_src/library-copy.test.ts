// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initCopyControls } from "./clipboard";
import { initOverviewSearchResults } from "./overview-search";
import { renderOverviewBulkFixture } from "./search-rendering.test-helpers";
import { searchResult, setURL } from "./search-test-helpers";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  setURL("/");
});

it("does not restore stale fallback text after selection is cleared during a clipboard request", async () => {
  vi.useFakeTimers();
  let rejectCopy!: (error: Error) => void;
  const writeText = vi.fn(() => new Promise<void>((_, reject) => { rejectCopy = reject; }));
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  renderOverviewBulkFixture();
  setURL("/library?q=note");
  initCopyControls();
  initOverviewSearchResults({ fetch: async () => new Response(JSON.stringify([searchResult()])) });
  await vi.runAllTimersAsync();
  document.querySelector<HTMLButtonElement>("[data-overview-select-displayed]")!.click();
  const copy = document.querySelector<HTMLButtonElement>("[data-overview-copy-wikilinks]")!;
  copy.click();
  document.querySelector<HTMLButtonElement>("[data-overview-clear-selection]")!.click();
  rejectCopy(new Error("Denied"));
  await vi.runAllTimersAsync();
  expect(copy.disabled).toBe(true);
  expect(document.querySelector("[data-copy-fallback]")).toBeNull();
  expect(document.querySelector("[data-copy-status]")!.textContent).toBe("");
});

it.each([false, true])("copies only selected displayed targets in result order (clipboard available: %s)", async (available) => {
  vi.useFakeTimers();
  const writeText = vi.fn(async () => {});
  vi.stubGlobal("navigator", { clipboard: available ? { writeText } : undefined });
  renderOverviewBulkFixture();
  // Copying needs neither bulk controls nor a mutation token.
  document.querySelector("[data-overview-bulk]")?.remove();
  setURL("/library?q=note");
  const fetcher = vi.fn(async () => new Response(JSON.stringify([
    searchResult({ key: "z", title: "Display title", wikilink: "Folder/Zèbre" }),
    searchResult({ key: "a", wikilink: "Other/Alpha" }),
    searchResult({ key: "b", wikilink: "Unselected" }),
  ])));
  initCopyControls();
  initOverviewSearchResults({ fetch: fetcher });
  await vi.runAllTimersAsync();
  const copy = document.querySelector<HTMLButtonElement>("[data-overview-copy-wikilinks]")!;
  expect(copy.disabled).toBe(true);
  const inputs = document.querySelectorAll<HTMLInputElement>(".overview-result-select input");
  for (const index of [1, 0]) {
    inputs[index].checked = true;
    inputs[index].dispatchEvent(new Event("change", { bubbles: true }));
  }
  copy.click();
  await vi.runAllTimersAsync();
  const text = "[[Folder/Zèbre]]\n[[Other/Alpha]]";
  if (available) {
    expect(writeText).toHaveBeenCalledWith(text);
  } else {
    const fallback = document.querySelector<HTMLTextAreaElement>("[data-copy-fallback]")!;
    expect(fallback.value).toBe(text);
    expect(document.activeElement).toBe(fallback);
    expect(fallback.selectionEnd).toBe(text.length);
  }
  document.querySelector<HTMLButtonElement>("[data-overview-clear-selection]")!.click();
  expect(copy.disabled).toBe(true);
  expect(document.querySelector("[data-copy-fallback]")).toBeNull();
  expect(document.querySelector("[data-copy-status]")!.textContent).toBe("");
  expect(fetcher).toHaveBeenCalledTimes(1);
});
