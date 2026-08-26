// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { normalizeSavedViewsResponse } from "./api-views";
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
import { initSavedViewsNav } from "./saved-views";

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  }
  throw lastError;
};

const rottedDetail = `unknown field "statuz"
  statuz:wip
  ^
did you mean: status`;

const viewsPayload = () => ({
  ok: true,
  generation: 3,
  action: "list",
  views: [
    {
      name: "Focus",
      description: "Active focus work",
      query: "tag:focus and not folder:Archive",
      normalized: "tag:focus and not folder:Archive",
      sort: "-modified",
      relative_to: "",
      limit: 0,
      enabled: true,
      detail: "",
      needs_graph: false,
    },
    {
      name: "Near Hub",
      description: "",
      query: "near:[[Hub]]<=2",
      normalized: "near:[[Hub]]<=2",
      sort: "relevance",
      relative_to: "areas/hub",
      limit: 20,
      enabled: true,
      detail: "",
      needs_graph: true,
    },
    {
      name: "Rotted",
      description: "",
      query: "statuz:wip",
      normalized: "",
      sort: "",
      relative_to: "",
      limit: 0,
      enabled: false,
      detail: rottedDetail,
      needs_graph: false,
    },
  ],
  created: false,
  config_diff: [],
  config: { generation: 3, source_hash: "abc", status: "ok", restart_required: [], writable: true },
});

const mountNav = (): HTMLElement => {
  document.body.innerHTML = `
    <nav class="site-nav" aria-label="Primary">
      <a href="/">Brief</a>
      <a href="/library">Library</a>
      <div class="saved-views-nav" data-saved-views-nav role="group" aria-label="Saved views"></div>
    </nav>
  `;
  const container = document.querySelector("[data-saved-views-nav]");
  if (!(container instanceof HTMLElement)) {
    throw new Error("nav container missing");
  }
  return container;
};

const navItems = (container: HTMLElement): HTMLElement[] =>
  Array.from(container.querySelectorAll(".saved-views-nav-item")).flatMap((item) =>
    item instanceof HTMLElement ? [item] : []
  );

const setLocation = (path: string): void => {
  window.history.replaceState(null, "", path);
};

beforeEach(() => {
  setLocation("/library");
});

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("saved views nav", () => {
  it("fills the static container from GET /api/views", async () => {
    const container = mountNav();
    const calls: string[] = [];
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      calls.push(String(input));
      return jsonResponse(viewsPayload());
    });

    initSavedViewsNav({ fetch: fetcher });

    await waitForExpect(() => {
      expect(navItems(container)).toHaveLength(3);
    });
    expect(calls).toEqual(["/api/views"]);
  });

  it("links an enabled view to the query shape it stores", async () => {
    const container = mountNav();
    initSavedViewsNav({ fetch: async () => jsonResponse(viewsPayload()) });

    await waitForExpect(() => {
      expect(navItems(container)).toHaveLength(3);
    });

    const [focus, near] = navItems(container);
    expect(focus).toBeInstanceOf(HTMLAnchorElement);
    expect(focus?.getAttribute("href")).toBe(
      "/library?expr=tag%3Afocus+and+not+folder%3AArchive&sort=-modified"
    );
    expect(focus?.textContent).toBe("Focus");
    expect(focus?.title).toBe("Active focus work — tag:focus and not folder:Archive");
    expect(near?.getAttribute("href")).toBe(
      "/library?expr=near%3A%5B%5BHub%5D%5D%3C%3D2&sort=relevance&relative_to=areas%2Fhub&limit=20"
    );
  });

  it("renders a view whose expression no longer parses disabled with its parse error", async () => {
    const container = mountNav();
    initSavedViewsNav({ fetch: async () => jsonResponse(viewsPayload()) });

    await waitForExpect(() => {
      expect(navItems(container)).toHaveLength(3);
    });

    const rotted = navItems(container)[2];
    expect(rotted?.tagName).toBe("SPAN");
    expect(rotted).not.toBeInstanceOf(HTMLAnchorElement);
    expect(rotted?.classList.contains("is-disabled")).toBe(true);
    expect(rotted?.getAttribute("aria-disabled")).toBe("true");
    expect(rotted?.getAttribute("data-saved-view-detail")).toBe(rottedDetail);
    expect(rotted?.title).toContain('unknown field "statuz"');
    expect(rotted?.title).toContain("did you mean: status");
  });

  it("keeps a saved view on the graph when the graph is the current page", async () => {
    setLocation("/graph");
    const container = mountNav();
    initSavedViewsNav({ fetch: async () => jsonResponse(viewsPayload()) });

    await waitForExpect(() => {
      expect(navItems(container)).toHaveLength(3);
    });
    expect(navItems(container)[0]?.getAttribute("href")).toContain("/graph?expr=");
  });

  it("does not fetch when the page renders no container", () => {
    document.body.innerHTML = "";
    const fetcher = vi.fn(async () => jsonResponse(viewsPayload()));

    expect(initSavedViewsNav({ fetch: fetcher })).toBeNull();
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("leaves the strip empty when the listing fails", async () => {
    const container = mountNav();
    initSavedViewsNav({
      fetch: async () => jsonResponse({ ok: false, message: "nope" }, 500),
    });

    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(navItems(container)).toHaveLength(0);
    expect(container.textContent).toBe("");
  });

  it("re-renders from a list handed back after a save", async () => {
    const container = mountNav();
    const handle = initSavedViewsNav({ fetch: async () => jsonResponse(viewsPayload()) });

    await waitForExpect(() => {
      expect(navItems(container)).toHaveLength(3);
    });
    handle?.render(normalizeSavedViewsResponse(viewsPayload()).views.slice(0, 1));
    expect(navItems(container)).toHaveLength(1);
  });
});

describe("saved views model", () => {
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
