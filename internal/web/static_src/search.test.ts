// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { normalizeSearchResults, searchResultPath, type SearchResult } from "./api";
import { buildSearchAPIURL, dispatchFiltersChange } from "./filters";
import {
  overviewSearchEmptyMessage,
  renderOverviewSearchResult,
} from "./overview-search";
import {
  initSearch,
  nextSearchActiveIndex,
  renderSearchResultOption,
} from "./search";

const searchResult = (overrides: Partial<SearchResult> = {}): SearchResult => ({
  key: "folder/note.md",
  path: "/n/folder/note.md",
  title: "Note Title",
  folder: "folder",
  matched_fields: ["title"],
  issues: [],
  snippet: "Snippet text",
  ...overrides,
});

const setURL = (url: string): void => {
  window.history.replaceState(null, "", url);
};

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  setURL("/");
});

describe("search URL and result contracts", () => {
  it("builds search API URLs with optional shared filters", () => {
    setURL("/?tag=projects&category=weekly&q=old&broken=true#top");

    expect(buildSearchAPIURL(" daily note ", true)).toBe(
      "/api/search?q=daily+note&tag=projects&category=weekly&broken=true"
    );
    expect(buildSearchAPIURL(" daily note ", false)).toBe("/api/search?q=daily+note");
    expect(buildSearchAPIURL("", false)).toBe("/api/search");
  });

  it("normalizes result payloads and resolves safe note paths", () => {
    const results = normalizeSearchResults([
      {
        key: " daily/index.md ",
        path: " /n/server/path ",
        title: " Daily Index ",
        folder: " daily ",
        matched_fields: [" title ", "", "body"],
        issues: ["dead_end", 3],
        snippet: " <mark>daily</mark> ",
      },
      {
        key: "folder/name with spaces.md",
      },
      "bad",
    ]);

    expect(results).toHaveLength(2);
    expect(results[0]).toEqual({
      key: "daily/index.md",
      path: "/n/server/path",
      title: "Daily Index",
      folder: "daily",
      matched_fields: ["title", "body"],
      issues: ["dead_end", "3"],
      snippet: "<mark>daily</mark>",
    });
    expect(searchResultPath(results[0])).toBe("/n/server/path");
    expect(searchResultPath(results[1])).toBe("/n/folder/name%20with%20spaces.md");
  });
});

describe("search result rendering", () => {
  it("renders primary autocomplete options with ARIA option state", () => {
    const activated: number[] = [];
    const chosen: number[] = [];
    const item = renderSearchResultOption(
      document,
      searchResult({ snippet: "Body <mark>hit</mark>" }),
      2,
      {
        activate: (index) => activated.push(index),
        choose: (index) => chosen.push(index),
      }
    );

    const button = item.querySelector("button");
    expect(button?.id).toBe("search-option-2");
    expect(button?.getAttribute("role")).toBe("option");
    expect(button?.getAttribute("aria-selected")).toBe("false");
    expect(button?.getAttribute("data-search-index")).toBe("2");
    expect(button?.querySelector(".search-result-title")?.textContent).toBe("Note Title");
    expect(button?.querySelector(".search-result-folder")?.textContent).toBe("Folder: folder");
    expect(button?.querySelector(".search-result-snippet")?.innerHTML).toBe(
      "Body <mark>hit</mark>"
    );

    button?.dispatchEvent(new MouseEvent("mouseenter", { bubbles: true }));
    button?.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    expect(activated).toEqual([2]);
    expect(chosen).toEqual([2]);
  });

  it("renders overview results with metadata, issue badges, and fallback paths", () => {
    const list = document.createElement("ul");
    const rendered = renderOverviewSearchResult(
      list,
      searchResult({
        path: "",
        key: "daily/overview.md",
        matched_fields: ["title", "body"],
        issues: ["dead_end"],
        snippet: "<mark>overview</mark>",
      })
    );

    expect(rendered).toBe(true);
    expect(list.querySelector("a")?.getAttribute("href")).toBe("/n/daily/overview.md");
    expect(list.querySelector(".overview-result-title")?.textContent).toBe("Note Title");
    expect(list.querySelector(".overview-result-meta")?.textContent).toContain(
      "Matched: title, body"
    );
    expect(list.querySelector(".overview-result-badge.is-issue")?.textContent).toBe("dead end");
    expect(list.querySelector(".overview-result-snippet")?.innerHTML).toBe(
      "<mark>overview</mark>"
    );
    expect(overviewSearchEmptyMessage("")).toBe("No notes match the active filters.");
    expect(overviewSearchEmptyMessage("zettel")).toBe('No notes match "zettel".');
  });
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
