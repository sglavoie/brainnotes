// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initOverviewSearchResults } from "./overview-search";
import { renderOverviewBulkFixture } from "./search-rendering.test-helpers";
import { searchResult, setURL } from "./search-test-helpers";

afterEach(() => {
  vi.useRealTimers();
  document.body.innerHTML = "";
  setURL("/");
});

const inputs = (): HTMLInputElement[] =>
  Array.from(document.querySelectorAll<HTMLInputElement>(".overview-result-select input"));
const checked = (): string[] => inputs().filter((input) => input.checked).map((input) => input.value);
const click = (index: number, shiftKey = false): void => {
  inputs()[index].dispatchEvent(new MouseEvent("click", { shiftKey, bubbles: true }));
};
const control = (name: string): HTMLButtonElement =>
  document.querySelector<HTMLButtonElement>(`[data-${name}]`)!;

const setup = async (): Promise<ReturnType<typeof vi.fn>> => {
  vi.useFakeTimers();
  renderOverviewBulkFixture();
  setURL("/library?q=note");
  const fetcher = vi.fn(async (url: RequestInfo | URL) => {
    const body = String(url) === "/api/notes/bulk/preview"
      ? { ok: true, action: "trash", preview_id: "preview" }
      : ["a", "b", "c", "d", "e"].map((key) => searchResult({ key, wikilink: key.toUpperCase() }));
    return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
  });
  initOverviewSearchResults({ fetch: fetcher });
  await vi.runAllTimersAsync();
  return fetcher;
};

it("selects forward and clears backward ranges, preserving other selections and copied order", async () => {
  await setup();
  click(4);
  click(0);
  click(2, true);
  expect(checked()).toEqual(["a", "b", "c", "e"]);
  expect(control("overview-copy-wikilinks").dataset.copyText).toBe("[[A]]\n[[B]]\n[[C]]\n[[E]]");
  click(1, true);
  expect(checked()).toEqual(["a", "e"]);
  expect(document.querySelector("[data-overview-selected-count]")!.textContent).toBe("2 selected");
});

it("invalidates a bulk preview after a range selection without applying it", async () => {
  const fetcher = await setup();
  click(0);
  control("bulk-trash").click();
  expect(inputs().every((input) => input.disabled)).toBe(true);
  await vi.runAllTimersAsync();
  const bulk = document.querySelector("[data-overview-bulk]")!;
  expect(bulk.getAttribute("data-overview-preview-id")).toBe("preview");
  click(2, true);
  expect(checked()).toEqual(["a", "b", "c"]);
  expect(bulk.hasAttribute("data-overview-preview-id")).toBe(false);
  expect(fetcher.mock.calls.some(([url]) => String(url) === "/api/notes/bulk")).toBe(false);
});

it.each(["overview-clear-selection", "overview-select-displayed", "search"])(
  "resets the range anchor on %s", async (action) => {
    await setup();
    click(0);
    if (action === "search") {
      const search = document.querySelector<HTMLInputElement>("#search-input")!;
      search.value = "another";
      search.dispatchEvent(new Event("input"));
      expect(inputs().every((input) => input.disabled)).toBe(true);
      await vi.runAllTimersAsync();
    } else control(action).click();
    click(3, true);
    const expected = action === "overview-select-displayed" ? ["a", "b", "c", "e"]
      : action === "search" ? ["a", "d"] : ["d"];
    expect(checked()).toEqual(expected);
  }
);

it("supports range copying when bulk write controls are absent", async () => {
  await setup();
  // Remount with the read-only page shape.
  renderOverviewBulkFixture();
  document.querySelector("[data-overview-bulk]")!.remove();
  initOverviewSearchResults({ fetch: async () => new Response(JSON.stringify(
    ["a", "b", "c"].map((key) => searchResult({ key, wikilink: key }))
  )) });
  await vi.runAllTimersAsync();
  click(0);
  click(2, true);
  expect(control("overview-copy-wikilinks").dataset.copyText).toBe("[[a]]\n[[b]]\n[[c]]");
});
