// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { dispatchFiltersChange } from "./filters";
import { initSearch, nextSearchActiveIndex } from "./search";
import { searchResult, setURL } from "./search-test-helpers";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  setURL("/");
});

describe("search keyboard and filter-change behavior", () => {
  it("wraps active search result indexes for keyboard navigation", () => {
    expect(nextSearchActiveIndex(-1, 3, "next")).toBe(0);
    expect(nextSearchActiveIndex(0, 3, "next")).toBe(1);
    expect(nextSearchActiveIndex(2, 3, "next")).toBe(0);
    expect(nextSearchActiveIndex(-1, 3, "previous")).toBe(2);
    expect(nextSearchActiveIndex(0, 3, "previous")).toBe(2);
    expect(nextSearchActiveIndex(1, 3, "previous")).toBe(0);
    expect(nextSearchActiveIndex(0, 0, "next")).toBe(-1);
  });

  it("runs a filter-aware search when search-mode filters change", async () => {
    vi.useFakeTimers();
    setURL("/");
    document.body.innerHTML = `
      <section data-search-box>
        <input id="search-input" aria-expanded="false" />
        <ul id="search-results" hidden></ul>
      </section>
      <div data-facet-controls data-facet-mode="search"></div>
    `;
    const payload = [
      searchResult({ title: "Filtered A", path: "/n/filtered/a.md" }),
      searchResult({ title: "Filtered B", path: "/n/filtered/b.md" }),
    ];
    const fetchMock = vi.fn(async () => ({
      ok: true,
      json: async () => payload,
    })) as unknown as typeof fetch;
    vi.stubGlobal("fetch", fetchMock);

    initSearch();
    setURL("/?tag=work");
    dispatchFiltersChange(window, {
      mode: "search",
      params: new URLSearchParams("tag=work"),
    });

    await vi.runAllTimersAsync();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith("/api/search?tag=work", {
      headers: { Accept: "application/json" },
    });
    const input = document.getElementById("search-input") as HTMLInputElement;
    const resultsEl = document.getElementById("search-results") as HTMLElement;
    expect(resultsEl.hidden).toBe(false);
    expect(input.getAttribute("aria-expanded")).toBe("true");
    expect(resultsEl.querySelectorAll(".search-result")).toHaveLength(2);

    input.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    const firstOption = document.getElementById("search-option-0");
    expect(firstOption?.getAttribute("aria-selected")).toBe("true");
    expect(firstOption?.classList.contains("is-active")).toBe(true);
    expect(input.getAttribute("aria-activedescendant")).toBe("search-option-0");

    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    expect(resultsEl.hidden).toBe(true);
    expect(input.getAttribute("aria-expanded")).toBe("false");
    expect(input.hasAttribute("aria-activedescendant")).toBe(false);
  });
});
