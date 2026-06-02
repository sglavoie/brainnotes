// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";

import {
  buildFilteredAPIURL,
  copySharedFilterParams,
  dispatchFiltersChange,
  filterChangeEventName,
  hasActiveSharedFilters,
  shouldHandleFilterChangeForMode,
  syncPageQueryParam,
  updateBrowserQuery,
  type FiltersChangeDetail,
  type FiltersChangeEvent,
} from "./filters";

const setURL = (url: string): void => {
  window.history.replaceState(null, "", url);
};

afterEach(() => {
  setURL("/");
});

describe("shared filter URL state", () => {
  it("copies normalized shared params and detects active filters", () => {
    const params = new URLSearchParams("q=ignored&tag=work&tag=&category=area&min_links=2");
    const copied = new URLSearchParams("page=1");

    copySharedFilterParams(params, copied);

    expect(Array.from(copied.entries())).toEqual([
      ["page", "1"],
      ["tag", "work"],
      ["category", "area"],
      ["min_links", "2"],
    ]);
    expect(hasActiveSharedFilters(new URLSearchParams("q=only"))).toBe(false);
    expect(hasActiveSharedFilters(new URLSearchParams("missing=title"))).toBe(true);
    expect(hasActiveSharedFilters(new URLSearchParams("tag="))).toBe(false);
  });

  it("updates browser query strings while preserving path and hash", () => {
    setURL("/graph?tag=old&q=hello#panel");

    syncPageQueryParam("daily note");
    expect(`${window.location.pathname}${window.location.search}${window.location.hash}`).toBe(
      "/graph?tag=old&q=daily+note#panel"
    );

    syncPageQueryParam(" ");
    expect(`${window.location.pathname}${window.location.search}${window.location.hash}`).toBe(
      "/graph?tag=old#panel"
    );

    updateBrowserQuery(new URLSearchParams("category=work"));
    expect(`${window.location.pathname}${window.location.search}${window.location.hash}`).toBe(
      "/graph?category=work#panel"
    );
  });

  it("builds filtered API URLs and gates filter-change events by mode", () => {
    setURL("/search?q=old&tag=projects&tag=&category=area&folder=notes#top");
    const seen: FiltersChangeDetail[] = [];
    window.addEventListener(filterChangeEventName, (event) => {
      seen.push((event as FiltersChangeEvent).detail);
    });

    expect(buildFilteredAPIURL("/api/graph", " note ")).toBe(
      "/api/graph?q=note&tag=projects&category=area&folder=notes"
    );

    const graphEvent = new CustomEvent(filterChangeEventName, {
      detail: { mode: "graph", params: new URLSearchParams("tag=projects") },
    }) as FiltersChangeEvent;
    const globalEvent = new CustomEvent(filterChangeEventName, {
      detail: { mode: "", params: new URLSearchParams() },
    }) as FiltersChangeEvent;

    expect(shouldHandleFilterChangeForMode(graphEvent, "graph")).toBe(true);
    expect(shouldHandleFilterChangeForMode(graphEvent, "search")).toBe(false);
    expect(shouldHandleFilterChangeForMode(globalEvent, "search")).toBe(true);

    dispatchFiltersChange(window, graphEvent.detail);

    expect(seen).toHaveLength(1);
    expect(seen[0].mode).toBe("graph");
    expect(Array.from(seen[0].params.entries())).toEqual([["tag", "projects"]]);
  });
});
