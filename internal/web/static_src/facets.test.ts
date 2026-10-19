// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  fallbackFacetOption,
  normalizeFacetOptions,
  type FacetFilterOption,
} from "./api";
import {
  facetOptionMatchesQuery,
  facetSelectionFromParams,
  hydrateFacetSelection,
  initFacets,
  paramsForFacetSelection,
  withFacetOptionSelected,
} from "./facets";
import {
  filterChangeEventName,
  type FiltersChangeDetail,
  type FiltersChangeEvent,
} from "./filters";
import { waitForExpect } from "./shared.test-helpers";

const setURL = (url: string): void => {
  window.history.replaceState(null, "", url);
};

const facetControlsMarkup = `
  <section data-facet-controls data-facet-mode="search">
    <input id="search-facet-input" class="facet-input" aria-expanded="false" />
    <button type="button" data-facet-input-clear hidden>Clear</button>
    <ul class="facet-options" hidden></ul>
    <button type="button" data-facet-clear-all hidden>Clear filters</button>
    <div data-facet-chips hidden></div>
  </section>
`;

const facetOption = (overrides: Partial<FacetFilterOption>): FacetFilterOption => ({
  param: "tag",
  value: "alpha",
  label: "alpha",
  count: 0,
  groupLabel: "Tags",
  repeat: true,
  ...overrides,
});

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  setURL("/");
});

describe("facet option normalization", () => {
  it("normalizes facet API payloads into sorted filter options", () => {
    const options = normalizeFacetOptions({
      tags: [
        { value: " zeta ", count: "2" },
        { value: " alpha ", count: "2" },
        { value: "", count: 9 },
      ],
      categories: [{ value: " work ", count: "5" }],
      issues: [
        { param: "dead_end", value: "true", label: "", count: "3" },
        { param: "broken", value: "true", label: "Broken links", count: "7" },
      ],
    });

    expect(
      options.map((option) => ({
        groupLabel: option.groupLabel,
        param: option.param,
        value: option.value,
        label: option.label,
        count: option.count,
        repeat: option.repeat,
      }))
    ).toEqual([
      {
        groupLabel: "Categories",
        param: "category",
        value: "work",
        label: "work",
        count: 5,
        repeat: true,
      },
      {
        groupLabel: "Issues",
        param: "broken",
        value: "true",
        label: "Broken links",
        count: 7,
        repeat: false,
      },
      {
        groupLabel: "Issues",
        param: "dead_end",
        value: "true",
        label: "Dead ends",
        count: 3,
        repeat: false,
      },
      {
        groupLabel: "Tags",
        param: "tag",
        value: "alpha",
        label: "alpha",
        count: 2,
        repeat: true,
      },
      {
        groupLabel: "Tags",
        param: "tag",
        value: "zeta",
        label: "zeta",
        count: 2,
        repeat: true,
      },
    ]);
  });

  it("matches option search text across group, label, and value", () => {
    const option = facetOption({
      value: "work/weekly",
      label: "Weekly notes",
      groupLabel: "Categories",
    });

    expect(facetOptionMatchesQuery(option, "")).toBe(true);
    expect(facetOptionMatchesQuery(option, " weekly ")).toBe(true);
    expect(facetOptionMatchesQuery(option, "categories")).toBe(true);
    expect(facetOptionMatchesQuery(option, "missing")).toBe(false);
  });
});

describe("facet selection state", () => {
  it("builds URL params from repeated filters and last non-repeated values", () => {
    const selection = facetSelectionFromParams(
      new URLSearchParams(
        "tag=alpha&tag=beta&tag=&status=draft&status=published&broken=true"
      )
    );
    const params = paramsForFacetSelection(selection);
    const broken = Array.from(selection.values()).find((option) => option.param === "broken");

    expect(params.getAll("tag")).toEqual(["alpha", "beta"]);
    expect(params.getAll("status")).toEqual(["published"]);
    expect(params.getAll("broken")).toEqual(["true"]);
    expect(broken).toMatchObject({
      groupLabel: "Issues",
      label: "Broken links",
      repeat: false,
    });
  });

  it("hydrates fallback selections from catalog metadata", () => {
    let selection = facetSelectionFromParams(new URLSearchParams("tag=alpha&status=draft"));
    selection = withFacetOptionSelected(selection, fallbackFacetOption("status", "published"));
    const hydrated = hydrateFacetSelection(selection, [
      facetOption({
        value: "alpha",
        label: "Alpha tag",
        count: 8,
      }),
      facetOption({
        param: "status",
        value: "published",
        label: "Published",
        count: 3,
        groupLabel: "Status",
        repeat: false,
      }),
    ]);

    expect(Array.from(hydrated.values())).toEqual([
      facetOption({
        value: "alpha",
        label: "Alpha tag",
        count: 8,
      }),
      facetOption({
        param: "status",
        value: "published",
        label: "Published",
        count: 3,
        groupLabel: "Status",
        repeat: false,
      }),
    ]);
  });
});

describe("facet DOM behavior", () => {
  it("hydrates selected chips, writes shared filters to the URL, and dispatches changes", async () => {
    setURL("/search?q=keep&tag=existing#results");
    document.body.innerHTML = facetControlsMarkup;
    const fetchMock = vi.fn(async () => ({
      ok: true,
      json: async () => ({
        tags: [{ value: "existing", count: 4 }],
        categories: [{ value: "work", count: 2 }],
        status: [{ value: "draft", count: 1 }],
      }),
    }));
    vi.stubGlobal("fetch", fetchMock);
    const changes: FiltersChangeDetail[] = [];
    window.addEventListener(filterChangeEventName, (event) => {
      changes.push((event as FiltersChangeEvent).detail);
    });

    initFacets();

    await waitForExpect(() => {
      expect(document.querySelectorAll(".facet-option")).toHaveLength(3);
    });

    const chips = document.querySelector<HTMLElement>("[data-facet-chips]");
    const clearAll = document.querySelector<HTMLButtonElement>("[data-facet-clear-all]");
    expect(fetchMock).toHaveBeenCalledWith("/api/facets", {
      headers: { Accept: "application/json" },
    });
    expect(chips?.hidden).toBe(false);
    expect(chips?.textContent).toContain("Tags: existing");
    expect(chips?.textContent).toContain("(4)");

    const workButton = Array.from(
      document.querySelectorAll<HTMLButtonElement>(".facet-option")
    ).find((button) => button.textContent?.includes("work"));
    workButton?.click();

    expect(`${window.location.pathname}${window.location.search}${window.location.hash}`).toBe(
      "/search?q=keep&tag=existing&category=work#results"
    );
    expect(changes).toHaveLength(1);
    expect(changes[0].mode).toBe("search");
    expect(Array.from(changes[0].params.entries())).toEqual([
      ["tag", "existing"],
      ["category", "work"],
    ]);
    expect(chips?.textContent).toContain("Categories: work");

    clearAll?.click();

    expect(`${window.location.pathname}${window.location.search}${window.location.hash}`).toBe(
      "/search?q=keep#results"
    );
    expect(changes).toHaveLength(2);
    expect(Array.from(changes[1].params.entries())).toEqual([]);
    expect(chips?.hidden).toBe(true);
    expect(clearAll?.hidden).toBe(true);
  });

  // The chips rewrite the URL by deleting every shared filter key and writing
  // the current selection back. The query-shape parameters are deliberately not
  // in that vocabulary, so toggling a chip — or clearing every chip — must leave
  // the expression and the ordering exactly where they were.
  it("preserves expr, sort, and relative_to across a chip edit", async () => {
    setURL("/search?q=keep&expr=tag%3Afocus&sort=-modified&relative_to=Deep+Work");
    document.body.innerHTML = facetControlsMarkup;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({
        ok: true,
        json: async () => ({ categories: [{ value: "work", count: 2 }] }),
      }))
    );

    initFacets();

    await waitForExpect(() => {
      expect(document.querySelectorAll(".facet-option")).toHaveLength(1);
    });

    const carried = "expr=tag%3Afocus&sort=-modified&relative_to=Deep+Work";
    document.querySelector<HTMLButtonElement>(".facet-option")?.click();
    expect(window.location.search).toBe(`?q=keep&${carried}&category=work`);

    document.querySelector<HTMLButtonElement>("[data-facet-clear-all]")?.click();
    expect(window.location.search).toBe(`?q=keep&${carried}`);
  });
});
