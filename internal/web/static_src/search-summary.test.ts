// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initOverviewSearchResults } from "./overview-search";
import { readSearchCounts, searchSummary } from "./search-summary";
import { searchResult, setURL } from "./search-test-helpers";
import { renderOverviewBulkFixture } from "./search-rendering.test-helpers";

afterEach(() => {
  vi.useRealTimers();
  document.body.innerHTML = "";
  setURL("/");
});

it.each([
  [0, "Use Next results"], [10, "Query limit: 10."],
  [50, "Query limit: 50."], [500, "Use Next results"],
])("explains the effective cap for requested limit %i", (limit, reason) => {
  const shown = Math.min(limit || 50, 50);
  const text = searchSummary(shown, "books", { total: 137, limit, cap: 50 });
  expect(text).toContain(`Showing ${shown} of 137 matching notes for "books".`);
  expect(text).toContain(reason);
  expect(text).toContain("Bulk actions apply only to selected notes.");
});

it("does not claim truncation when the count exactly equals the cap", () => {
  expect(searchSummary(50, "", { total: 50, limit: 0, cap: 50 })).toBe("50 matching notes.");
});

it("accepts legacy responses without count headers and rejects malformed counts", () => {
  expect(readSearchCounts(new Response("[]"))).toBeNull();
  expect(readSearchCounts(new Response("[]", { headers: {
    "X-BN-Search-Total": "NaN", "X-BN-Search-Limit": "0", "X-BN-Search-Cap": "50",
  } }))).toBeNull();
});

it("renders server totals while selecting only the displayed notes", async () => {
  vi.useFakeTimers();
  setURL("/library?q=notes&limit=1");
  renderOverviewBulkFixture();
  initOverviewSearchResults({ fetch: vi.fn(async () => new Response(JSON.stringify([searchResult()]), {
    headers: { "X-BN-Search-Total": "137", "X-BN-Search-Limit": "1", "X-BN-Search-Cap": "50" },
  })) });
  await vi.runAllTimersAsync();
  expect(document.querySelector("[data-overview-search-status]")!.textContent)
    .toContain('Showing 1 of 137 matching notes for "notes". Query limit: 1.');
  expect(document.querySelectorAll(".overview-result-select input")).toHaveLength(1);
});
