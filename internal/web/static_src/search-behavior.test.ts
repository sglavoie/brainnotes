// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { dispatchFiltersChange } from "./filters";
import { initSearch, nextSearchActiveIndex } from "./search";
import { searchResult, setURL } from "./search-test-helpers";

const renderSearch = (): { input: HTMLInputElement; results: HTMLElement; feedback: HTMLElement } => {
  document.body.innerHTML = `
    <section data-search-box>
      <input id="search-input" aria-expanded="false" />
      <ul id="search-results" hidden></ul>
      <p data-search-feedback hidden></p>
    </section>
  `;
  return {
    input: document.getElementById("search-input") as HTMLInputElement,
    results: document.getElementById("search-results") as HTMLElement,
    feedback: document.querySelector("[data-search-feedback]") as HTMLElement,
  };
};

const typeQuery = (input: HTMLInputElement, query: string): void => {
  input.value = query;
  input.dispatchEvent(new Event("input", { bubbles: true }));
};

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

describe("search feedback", () => {
  it("explains empty results and failed requests without opening an empty listbox", async () => {
    vi.useFakeTimers();
    const { input, results, feedback } = renderSearch();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => [] })
      .mockResolvedValueOnce({ ok: false });
    vi.stubGlobal("fetch", fetchMock);
    initSearch();

    typeQuery(input, "missing");
    expect(feedback.textContent).toBe("Searching...");
    await vi.runAllTimersAsync();
    expect(feedback.textContent).toBe("No matching notes.");
    expect(feedback.hidden).toBe(false);
    expect(results.hidden).toBe(true);
    expect(input.getAttribute("aria-expanded")).toBe("false");

    typeQuery(input, "broken");
    await vi.runAllTimersAsync();
    expect(feedback.textContent).toBe("Search unavailable. Try again.");
    expect(results.hidden).toBe(true);

    typeQuery(input, "");
    expect(feedback.hidden).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("removes old results as soon as the query changes", async () => {
    vi.useFakeTimers();
    const { input, results, feedback } = renderSearch();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => [searchResult()] })
      .mockResolvedValueOnce({ ok: true, json: async () => [] });
    vi.stubGlobal("fetch", fetchMock);
    initSearch();

    typeQuery(input, "note");
    await vi.runAllTimersAsync();
    expect(results.hidden).toBe(false);

    typeQuery(input, "other");
    expect(results.hidden).toBe(true);
    expect(results.childElementCount).toBe(0);
    expect(feedback.textContent).toBe("Searching...");
    await vi.runAllTimersAsync();
    expect(feedback.textContent).toBe("No matching notes.");
  });
});

describe("search dismissal", () => {
  it("cancels a search dismissed during the debounce", async () => {
    vi.useFakeTimers();
    const { input, feedback } = renderSearch();
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    initSearch();

    typeQuery(input, "note");
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await vi.runAllTimersAsync();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(feedback.hidden).toBe(true);
  });

  it.each(["Escape", "outside click", "focus leaving"])(
    "keeps a pending search dismissed after %s",
    async (dismissal) => {
      vi.useFakeTimers();
      const { input, results, feedback } = renderSearch();
      let resolveResponse: ((response: unknown) => void) | undefined;
      vi.stubGlobal("fetch", vi.fn(() => new Promise((resolve) => {
        resolveResponse = resolve;
      })));
      initSearch();

      typeQuery(input, "note");
      await vi.runAllTimersAsync();
      if (dismissal === "Escape") {
        input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      } else if (dismissal === "outside click") {
        document.body.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      } else {
        input.dispatchEvent(new FocusEvent("focusout", {
          bubbles: true,
          relatedTarget: document.body,
        }));
      }
      expect(feedback.hidden).toBe(true);
      resolveResponse?.({ ok: true, json: async () => [searchResult()] });
      await Promise.resolve();
      await Promise.resolve();
      expect(results.hidden).toBe(true);
      expect(feedback.hidden).toBe(true);
    }
  );
});
