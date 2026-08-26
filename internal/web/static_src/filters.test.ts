// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";

import {
  buildFilteredAPIURL,
  buildSearchAPIURL,
  copySharedFilterParams,
  dispatchFiltersChange,
  filterChangeEventName,
  hasActiveFilters,
  hasActiveSharedFilters,
  queryShapeParamKeys,
  readExprParam,
  sharedFilterParamKeys,
  shouldHandleFilterChangeForMode,
  syncExprParam,
  syncPageQueryParam,
  syncQueryShapeParam,
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

describe("query-shape URL parameters", () => {
  it("keeps expr, sort, and relative_to out of the facet chip vocabulary", () => {
    for (const key of queryShapeParamKeys) {
      expect(sharedFilterParamKeys as readonly string[]).not.toContain(key);
    }
  });

  it("carries them through copySharedFilterParams and both API URL builders", () => {
    const source = new URLSearchParams(
      "tag=work&expr=tag%3Afocus+and+not+folder%3AArchive&sort=-modified&relative_to=Deep+Work&other=drop"
    );
    const copied = new URLSearchParams();

    copySharedFilterParams(source, copied);

    expect(Array.from(copied.entries())).toEqual([
      ["tag", "work"],
      ["expr", "tag:focus and not folder:Archive"],
      ["sort", "-modified"],
      ["relative_to", "Deep Work"],
    ]);

    setURL("/library?tag=work&expr=tag%3Afocus&sort=-modified&relative_to=Deep+Work");
    expect(buildFilteredAPIURL("/api/graph.json", "")).toBe(
      "/api/graph.json?tag=work&expr=tag%3Afocus&sort=-modified&relative_to=Deep+Work"
    );
    expect(buildSearchAPIURL("note", true)).toBe(
      "/api/search?q=note&tag=work&expr=tag%3Afocus&sort=-modified&relative_to=Deep+Work"
    );
    // The unfiltered builder is the global nav search, which deliberately
    // ignores the page it happens to be rendered on.
    expect(buildSearchAPIURL("note", false)).toBe("/api/search?q=note");
  });

  it("reads and writes expr on the browser URL without disturbing the rest", () => {
    setURL("/library?tag=work#top");
    expect(readExprParam()).toBe("");

    expect(syncExprParam(" tag:focus ")).toBe(true);
    expect(window.location.search).toBe("?tag=work&expr=tag%3Afocus");
    expect(window.location.hash).toBe("#top");
    expect(readExprParam()).toBe("tag:focus");

    expect(syncExprParam("tag:focus")).toBe(false);
    expect(syncQueryShapeParam("sort", "-modified")).toBe(true);
    expect(window.location.search).toBe("?tag=work&expr=tag%3Afocus&sort=-modified");

    expect(syncExprParam("")).toBe(true);
    expect(window.location.search).toBe("?tag=work&sort=-modified");
  });

  it("counts expr as an active filter but never an ordering", () => {
    expect(hasActiveFilters(new URLSearchParams("q=only"))).toBe(false);
    expect(hasActiveFilters(new URLSearchParams("expr=tag%3Afocus"))).toBe(true);
    expect(hasActiveFilters(new URLSearchParams("expr=+"))).toBe(false);
    expect(hasActiveFilters(new URLSearchParams("tag=work"))).toBe(true);
    // An ordering selects nothing, so it must not make an empty search box
    // fetch the whole vault.
    expect(hasActiveFilters(new URLSearchParams("sort=-modified&relative_to=Deep+Work"))).toBe(
      false
    );
    expect(hasActiveSharedFilters(new URLSearchParams("expr=tag%3Afocus"))).toBe(false);
  });
});
