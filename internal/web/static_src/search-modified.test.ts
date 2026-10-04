// @vitest-environment jsdom
import { expect, it } from "vitest";
import { renderOverviewSearchResult } from "./overview-search-render";
import { searchResult } from "./search-test-helpers";

it.each([undefined, "", "not a date"])("omits unavailable modification dates: %s", (modified) => {
  const list = document.createElement("ul");
  renderOverviewSearchResult(list, searchResult({ modified }));
  expect(list.querySelector("time")).toBeNull();
});

it.each(["2026-10-03", "2026-10-03T00:00:00Z", "2026-10-03T18:25:12+05:30"])(
  "shows the sort timestamp and preserves its exact value: %s", (modified) => {
    const list = document.createElement("ul");
    renderOverviewSearchResult(list, searchResult({ modified }));
    const time = list.querySelector("time")!;
    expect(time.dateTime).toBe(modified);
    expect(time.title).toBe(`Modified: ${modified}`);
    const label = modified.length === 10 ? modified : new Date(modified).toLocaleString();
    expect(time.textContent).toBe(`Modified ${label}`);
  }
);
