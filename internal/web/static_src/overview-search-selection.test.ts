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

const button = (name: string): HTMLButtonElement =>
  document.querySelector<HTMLButtonElement>(`[data-${name}]`)!;

it("selects only displayed matches and invalidates a preview when clearing selection", async () => {
  vi.useFakeTimers();
  renderOverviewBulkFixture();
  setURL("/library?q=note");
  const fetcher = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url) === "/api/notes/bulk/preview") {
      const body = new URLSearchParams(String(init!.body));
      expect(body.getAll("keys[]")).toEqual(["one", "two"]);
      return new Response(JSON.stringify({ ok: true, action: "trash", preview_id: "preview" }),
        { headers: { "Content-Type": "application/json" } });
    }
    return new Response(JSON.stringify([
      searchResult({ key: "one" }), searchResult({ key: "two" }),
    ]), { headers: {
      "X-BN-Search-Total": "60", "X-BN-Search-Limit": "2", "X-BN-Search-Cap": "50",
    } });
  });
  initOverviewSearchResults({ fetch: fetcher });
  await vi.runAllTimersAsync();
  const controls = document.querySelector<HTMLElement>("[data-overview-selection]")!;
  expect(controls.hidden).toBe(false);
  expect(button("overview-clear-selection").disabled).toBe(true);
  button("overview-select-displayed").click();
  expect(document.querySelectorAll(".overview-result-select input:checked")).toHaveLength(2);
  expect(button("overview-select-displayed").disabled).toBe(true);
  expect(document.querySelector("[data-overview-selected-count]")!.textContent).toBe("2 selected");

  button("bulk-trash").click();
  expect(button("overview-clear-selection").disabled).toBe(true);
  expect(document.querySelectorAll(".overview-result-select input:disabled")).toHaveLength(2);
  await vi.runAllTimersAsync();
  expect(document.querySelector("[data-overview-bulk]")!.getAttribute("data-overview-preview-id")).toBe("preview");
  button("overview-clear-selection").click();
  expect(document.querySelector("[data-overview-bulk]")!.hasAttribute("data-overview-preview-id")).toBe(false);
  expect(document.querySelectorAll(".overview-result-select input:checked")).toHaveLength(0);
  expect(document.querySelector("[data-overview-selected-count]")!.textContent).toBe("0 selected");
  expect(button("overview-clear-selection").disabled).toBe(true);
  expect(button("overview-select-displayed").disabled).toBe(false);
  expect(fetcher.mock.calls.filter(([url]) => String(url) === "/api/notes/bulk")).toHaveLength(0);

  const input = document.querySelector<HTMLInputElement>("#search-input")!;
  input.value = "missing";
  fetcher.mockImplementationOnce(async () => new Response("[]"));
  input.dispatchEvent(new Event("input"));
  expect(button("overview-select-displayed").disabled).toBe(true);
  await vi.runAllTimersAsync();
  expect(controls.hidden).toBe(true);
});

it("updates the count and copied links when refreshed results drop a selected note", async () => {
  vi.useFakeTimers();
  renderOverviewBulkFixture();
  document.querySelector("[data-overview-bulk]")!.remove();
  setURL("/library?q=note");
  const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify([
    searchResult({ key: "one", wikilink: "One" }), searchResult({ key: "two", wikilink: "Two" }),
  ]))).mockResolvedValueOnce(new Response(JSON.stringify([searchResult({ key: "two", wikilink: "Two" })])));
  initOverviewSearchResults({ fetch: fetcher });
  await vi.runAllTimersAsync();
  button("overview-select-displayed").click();
  const count = document.querySelector("[data-overview-selected-count]")!;
  expect(count.textContent).toBe("2 selected");
  const input = document.querySelector<HTMLInputElement>("#search-input")!;
  input.value = "two";
  input.dispatchEvent(new Event("input"));
  await vi.runAllTimersAsync();
  expect(count.textContent).toBe("1 selected");
  expect(button("overview-copy-wikilinks").dataset.copyText).toBe("[[Two]]");
});
