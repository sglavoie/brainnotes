// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { normalizeSavedViewsResponse } from "./api-views";
import { savedViewIsCurrent } from "./saved-views-current";
import {
  savedViewDefinition,
  savedViewHref,
  savedViewLimitParam,
  savedViewParams,
  savedViewTitle,
  savedViewsNavBasePath,
  savedViewInputFromParams,
  savedViewSaveError,
} from "./saved-views-model";
import {
  viewsPayload,
  setLocation,
} from "./saved-views.test-helpers";


beforeEach(() => {
  setLocation("/library");
});

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("saved views model", () => {
  it("matches the complete view only on query pages, accepting canonical expressions", () => {
    const view = normalizeSavedViewsResponse(viewsPayload()).views[1]!;
    const url = new URL(savedViewHref(view, "/graph"), location.origin);
    expect(savedViewIsCurrent(view, url)).toBe(true);
    for (const [key, value] of [["tag", "extra"], ["q", "words"], ["sort", "title"],
      ["relative_to", "Other"], ["limit", "10"], ["expr", "tag:other"]]) {
      const changed = new URL(url);
      changed.searchParams.set(key, value);
      expect(savedViewIsCurrent(view, changed)).toBe(false);
    }
    url.pathname = "/n/Hub";
    expect(savedViewIsCurrent(view, url)).toBe(false);
    url.pathname = "/library";
    expect(savedViewIsCurrent({ ...view, enabled: false }, url)).toBe(false);
    expect(savedViewIsCurrent({ ...view, query: "different spelling" }, url)).toBe(true);
    url.searchParams.delete("expr");
    expect(savedViewIsCurrent({ ...view, normalized: "" }, url)).toBe(false);
  });

  it("drops a summary with no name and defaults every missing field", () => {
    const result = normalizeSavedViewsResponse({
      ok: true,
      views: [{ name: "" }, { name: "Only" }],
    });
    expect(result.views).toHaveLength(1);
    expect(result.views[0]).toEqual({
      name: "Only",
      description: "",
      query: "",
      normalized: "",
      sort: "",
      relative_to: "",
      limit: 0,
      enabled: false,
      detail: "",
      needs_graph: false,
    });
  });

  it("opens a view on the Library from any page that has no query bar", () => {
    expect(savedViewsNavBasePath("/library")).toBe("/library");
    expect(savedViewsNavBasePath("/library/")).toBe("/library");
    expect(savedViewsNavBasePath("/graph")).toBe("/graph");
    expect(savedViewsNavBasePath("/pinned")).toBe("/library");
    expect(savedViewsNavBasePath("/n/some/note")).toBe("/library");
    expect(savedViewsNavBasePath("")).toBe("/library");
  });

  it("carries only the view's own query shape, never the chips already selected", () => {
    const view = normalizeSavedViewsResponse(viewsPayload()).views[1];
    if (!view) {
      throw new Error("fixture missing");
    }
    const href = savedViewHref(view, "/library");
    const params = new URLSearchParams(href.slice(href.indexOf("?")));
    expect(Array.from(params.keys())).toEqual(["expr", "sort", "relative_to", "limit"]);
    expect(params.get("expr")).toBe("near:[[Hub]]<=2");
    // The stored cap is part of the view's definition, so the link carries it:
    // opening this view must show the 20 results it was saved as, not every
    // match that happens to satisfy the expression.
    expect(params.get("limit")).toBe("20");
  });

  it("falls back to the stored query when a view has no description", () => {
    const view = normalizeSavedViewsResponse(viewsPayload()).views[1];
    if (!view) {
      throw new Error("fixture missing");
    }
    expect(savedViewTitle(view)).toBe("near:[[Hub]]<=2");
  });

});

describe("saved view definitions", () => {
  it("reads the query shape a save stores from the URL", () => {
    const params = new URLSearchParams(
      "?expr=tag%3Afocus&sort=-modified&relative_to=areas%2Fhub&tag=other&q=text"
    );
    expect(
      savedViewInputFromParams(params, { name: "  Focus  ", description: " Work " })
    ).toEqual({
      name: "Focus",
      description: "Work",
      query: "tag:focus",
      sort: "-modified",
      relativeTo: "areas/hub",
      limit: 0,
    });
  });

  it("round-trips a non-zero saved limit through the URL and back into a definition", () => {
    const view = normalizeSavedViewsResponse(viewsPayload()).views[1];
    if (!view) {
      throw new Error("fixture missing");
    }
    const params = savedViewParams(view);
    const reread = savedViewInputFromParams(params, { name: "Near Hub", description: "" });
    expect(reread.limit).toBe(20);
    expect(savedViewDefinition(reread).limit).toBe(20);
  });

  it("treats a zero, negative, or non-numeric limit as no cap", () => {
    expect(savedViewLimitParam(0)).toBe("");
    expect(savedViewLimitParam(-5)).toBe("");
    expect(savedViewLimitParam(2.5)).toBe("");
    expect(savedViewLimitParam("nope")).toBe("");
    expect(savedViewLimitParam(20)).toBe("20");
    expect(
      savedViewInputFromParams(new URLSearchParams("?expr=tag%3Afocus&limit=-3"), {
        name: "N",
        description: "",
      }).limit
    ).toBe(0);
  });

  it("refuses a save with no name and a save with no expression", () => {
    const base = {
      name: "Focus",
      description: "",
      query: "tag:focus",
      sort: "",
      relativeTo: "",
      limit: 0,
    };
    expect(savedViewSaveError(base)).toBe("");
    expect(savedViewSaveError({ ...base, name: "" })).toContain("Name the view");
    expect(savedViewSaveError({ ...base, query: "" })).toContain("query expression");
  });
});
