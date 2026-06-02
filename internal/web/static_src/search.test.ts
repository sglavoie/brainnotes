// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildBulkNotesForm,
  normalizeBulkNoteKeys,
  normalizeSearchResults,
  searchResultPath,
  type SearchResult,
} from "./api";
import { buildSearchAPIURL, dispatchFiltersChange } from "./filters";
import {
  initOverviewSearchResults,
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

  it("normalizes explicit selected keys for bulk requests", () => {
    expect(normalizeBulkNoteKeys([" source ", "", "Source", "folder/Note"])).toEqual([
      "source",
      "folder/Note",
    ]);

    const body = buildBulkNotesForm({
      action: "move",
      keys: [" source ", "Source", "archive/a"],
      token: "csrf-token",
      folder: "Archive",
    });

    expect(body.get("action")).toBe("move");
    expect(body.get("token")).toBe("csrf-token");
    expect(body.get("folder")).toBe("Archive");
    expect(body.getAll("keys[]")).toEqual(["source", "archive/a"]);
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

  it("selects overview rows and submits confirmed bulk trash requests", async () => {
    vi.useFakeTimers();
    setURL("/?q=source");
    document.body.innerHTML = `
      <input id="search-input" />
      <section data-overview-search-results aria-busy="false">
        <p data-overview-search-status></p>
        <div data-overview-bulk data-overview-csrf="csrf-token" hidden>
          <p data-overview-selected-count></p>
          <button type="button" data-bulk-trash>Trash</button>
          <p data-overview-bulk-status></p>
        </div>
        <ul data-overview-search-list hidden></ul>
      </section>
    `;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      if (String(input) === "/api/notes/bulk") {
        return new Response(JSON.stringify({
          ok: true,
          message: "Moved 1 note to Trash. 1 failed.",
          changed: 1,
          failed: 1,
          failures: [{ key: "missing", message: "note not found" }],
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify([searchResult({
        key: "source",
        path: "/n/source",
        title: "Source",
      })]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
    const confirmMock = vi.fn(() => true);

    initOverviewSearchResults({ fetch: fetchMock, confirm: confirmMock });
    await vi.runAllTimersAsync();

    const checkbox = document.querySelector<HTMLInputElement>(".overview-result-select input");
    const bulkRoot = document.querySelector<HTMLElement>("[data-overview-bulk]");
    const selectedCount = document.querySelector<HTMLElement>("[data-overview-selected-count]");
    expect(checkbox).not.toBeNull();
    expect(bulkRoot?.hidden).toBe(true);

    checkbox!.checked = true;
    checkbox!.dispatchEvent(new Event("change", { bubbles: true }));

    expect(bulkRoot?.hidden).toBe(false);
    expect(selectedCount?.textContent).toBe("1 selected");

    document.querySelector<HTMLButtonElement>("[data-bulk-trash]")?.click();
    await vi.runAllTimersAsync();

    expect(confirmMock).toHaveBeenCalledWith("Move 1 selected note to Trash?");
    const bulkCall = fetchMock.mock.calls.find(([input]) => String(input) === "/api/notes/bulk");
    expect(bulkCall).toBeTruthy();
    const init = bulkCall?.[1] as RequestInit;
    expect(init.method).toBe("POST");
    expect(String(init.body)).toContain("keys%5B%5D=source");
    expect(String(init.body)).toContain("action=trash");
    expect(document.querySelector("[data-overview-bulk-status]")?.textContent).toContain(
      "Failures: missing: note not found."
    );
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
