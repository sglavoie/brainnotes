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
import { initSavedViewsNav, initSavedViewsSave } from "./saved-views";
import { dispatchFiltersChange, updateBrowserQuery } from "./filters";
import { savedViewIsCurrent } from "./saved-views-current";

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

    const rotted = navItems(container)[2]!;
    expect(rotted.tagName).toBe("SUMMARY");
    expect(rotted).not.toBeInstanceOf(HTMLAnchorElement);
    expect(rotted.classList.contains("is-disabled")).toBe(true);
    expect(rotted.hasAttribute("aria-disabled")).toBe(false);
    const disclosure = rotted.parentElement as HTMLDetailsElement;
    expect(disclosure.tagName).toBe("DETAILS");
    expect(disclosure.open).toBe(false);
    rotted.click();
    expect(disclosure.open).toBe(true);
    expect(disclosure.querySelector("pre")?.textContent).toBe(rottedDetail);
    expect(disclosure.querySelector("a")).toBeNull();
    rotted.click();
    expect(disclosure.open).toBe(false);
    expect(rotted.getAttribute("data-saved-view-detail")).toBe(rottedDetail);
    expect(rotted.title).toContain('unknown field "statuz"');
    expect(rotted.title).toContain("did you mean: status");
  });
});

describe("saved views nav lifecycle", () => {
  it("updates the current view after query changes without replacing focused links", async () => {
    const view = normalizeSavedViewsResponse(viewsPayload()).views[1]!;
    setLocation(savedViewHref(view, "/library"));
    const container = mountNav();
    const handle = initSavedViewsNav({ fetch: async () => jsonResponse(viewsPayload()) });
    await waitForExpect(() => expect(navItems(container)).toHaveLength(3));
    const link = navItems(container)[1]!;
    expect(link.getAttribute("aria-current")).toBe("true");
    link.focus();
    const params = savedViewParams(view);
    params.set("q", "extra text");
    updateBrowserQuery(params);
    expect(link.hasAttribute("aria-current")).toBe(false);
    expect(document.activeElement).toBe(link);
    params.delete("q");
    updateBrowserQuery(params);
    expect(link.getAttribute("aria-current")).toBe("true");
    handle?.render([{ ...view, limit: 10 }]);
    expect(container.querySelector("[aria-current]")).toBeNull();
    setLocation(savedViewHref({ ...view, limit: 10 }, "/library"));
    window.dispatchEvent(new PopStateEvent("popstate"));
    expect(container.querySelector("[aria-current]")?.textContent).toBe(view.name);
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

  it("offers a retry when the listing fails", async () => {
    const container = mountNav();
    initSavedViewsNav({
      fetch: async () => jsonResponse({ ok: false, message: "nope" }, 500),
    });

    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(navItems(container)).toHaveLength(0);
    expect(container.textContent).toContain("Saved views unavailable");
    expect(container.querySelector<HTMLButtonElement>("[data-saved-views-retry]")!.disabled).toBe(false);
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

const savePayload = (created: boolean, overrides: Record<string, unknown> = {}) => ({
  ok: true,
  generation: 4,
  action: "upsert",
  name: "Focus",
  views: viewsPayload().views.slice(0, 1),
  created,
  config_diff: created
    ? [
        { kind: "+", line: 12, text: "[[views]]" },
        { kind: "+", line: 13, text: 'name = "Focus"' },
      ]
    : [
        { kind: "-", line: 13, text: 'query = "tag:old"' },
        { kind: "+", line: 13, text: 'query = "tag:focus"' },
      ],
  config: { generation: 4, source_hash: "abc", status: "ok", restart_required: [], writable: true },
  ...overrides,
});

const mountSave = (): void => {
  document.body.innerHTML = `
    <div class="saved-views-save" data-saved-views-save data-saved-views-csrf="csrf-token">
      <button type="button" data-saved-views-open aria-expanded="false">Save as view</button>
      <form data-saved-views-form hidden>
        <pre data-saved-views-scope></pre>
        <p data-saved-views-excluded hidden></p>
        <input type="text" data-saved-views-name>
        <input type="text" data-saved-views-description>
        <button type="submit">Save</button>
        <button type="button" data-saved-views-confirm hidden>Confirm overwrite</button>
        <button type="button" data-saved-views-cancel>Cancel</button>
        <pre data-saved-views-diff hidden></pre>
        <p data-saved-views-status role="status"></p>
      </form>
    </div>
  `;
};

const saveElement = <T extends Element>(selector: string, ctor: new () => T): T => {
  const found = document.querySelector(selector);
  if (!(found instanceof ctor)) {
    throw new Error(`missing ${selector}`);
  }
  return found;
};

const submitSave = async (name: string): Promise<void> => {
  saveElement("[data-saved-views-name]", HTMLInputElement).value = name;
  saveElement("[data-saved-views-form]", HTMLFormElement).dispatchEvent(
    new Event("submit", { cancelable: true, bubbles: true })
  );
  await new Promise((resolve) => setTimeout(resolve, 0));
};

/**
 * The two halves of the saved-views write class, seen from the DOM: an
 * overwrite renders its diff as a question with the confirm button showing,
 * while a creation renders the same diff as a receipt with nothing to click.
 */
describe("saved views save scope", () => {
  it("shows the saved shape and updates excluded filters while the form is open", () => {
    mountSave();
    setLocation("/library?expr=tag:focus&sort=-modified&limit=10&q=words&tag=work&tag=books");
    initSavedViewsSave();
    saveElement("[data-saved-views-open]", HTMLButtonElement).click();
    const scope = saveElement("[data-saved-views-scope]", HTMLElement);
    const warning = saveElement("[data-saved-views-excluded]", HTMLElement);
    expect(scope.textContent).toBe("This view will save:\nQuery: tag:focus\nSort: -modified\nLimit: 10");
    expect(warning.hidden).toBe(false);
    expect(warning.textContent).toContain("Text search: words; tag: work; tag: books");

    setLocation("/library?expr=tag:focus");
    dispatchFiltersChange(window, { mode: "search", params: new URLSearchParams("expr=tag:focus") });
    expect(warning.hidden).toBe(true);
    expect(warning.textContent).toBe("");
    expect(scope.textContent).toContain("Sort: default\nLimit: none");

    setLocation("/library?q=%3Cb%3Etext%3C%2Fb%3E");
    document.body.insertAdjacentHTML("beforeend", '<input id="search-input">');
    document.querySelector("#search-input")?.dispatchEvent(new Event("input", { bubbles: true }));
    expect(warning.hidden).toBe(false);
    expect(warning.textContent).toContain("<b>text</b>");
    expect(warning.querySelector("b")).toBeNull();
    expect(scope.textContent).toContain("write a query expression first");
  });
});

describe("saved views save form", () => {
  it("renders the creation diff as a non-actionable receipt after the auto-applied write", async () => {
    mountSave();
    setLocation("/library?expr=tag%3Afocus");
    const routes: Record<string, () => Response> = {
      "/api/views/preview": () => jsonResponse(savePayload(true, { preview_id: "receipt-1" })),
      "/api/views/apply": () => jsonResponse(savePayload(true, { journal_id: "j-1" })),
    };
    const saver = initSavedViewsSave({
      fetch: async (input) => {
        const route = routes[String(input)];
        if (!route) {
          throw new Error(`unexpected request ${String(input)}`);
        }
        return route();
      },
    });

    await submitSave("Focus");

    const diff = saveElement("[data-saved-views-diff]", HTMLPreElement);
    expect(diff.hidden).toBe(false);
    expect(diff.getAttribute("data-saved-views-diff-role")).toBe("receipt");
    expect(diff.textContent).toBe('+ [[views]]\n+ name = "Focus"');
    // Nothing on the receipt can re-apply the preview the write consumed.
    expect(saveElement("[data-saved-views-confirm]", HTMLButtonElement).hidden).toBe(true);
    expect(saver?.pending()).toBeNull();
    expect(saveElement("[data-saved-views-status]", HTMLParagraphElement).textContent).toBe(
      'Saved view "Focus".'
    );
  });

  it("renders an overwrite diff as a confirmation instead, and writes only once confirmed", async () => {
    mountSave();
    setLocation("/library?expr=tag%3Afocus");
    const calls: string[] = [];
    const saver = initSavedViewsSave({
      fetch: async (input) => {
        calls.push(String(input));
        if (String(input) === "/api/views/preview") {
          return jsonResponse(savePayload(false, { preview_id: "receipt-1" }));
        }
        return jsonResponse(savePayload(false, { journal_id: "j-1" }));
      },
    });

    await submitSave("Focus");

    const diff = saveElement("[data-saved-views-diff]", HTMLPreElement);
    expect(diff.getAttribute("data-saved-views-diff-role")).toBe("confirm");
    expect(saveElement("[data-saved-views-confirm]", HTMLButtonElement).hidden).toBe(false);
    expect(calls).toEqual(["/api/views/preview"]);
    expect(saver?.pending()?.previewID).toBe("receipt-1");

    saveElement("[data-saved-views-confirm]", HTMLButtonElement).click();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(calls).toEqual(["/api/views/preview", "/api/views/apply"]);
    expect(diff.hidden).toBe(true);
    expect(saveElement("[data-saved-views-confirm]", HTMLButtonElement).hidden).toBe(true);
  });

  it("clears a creation receipt when the save panel is closed", async () => {
    mountSave();
    setLocation("/library?expr=tag%3Afocus");
    initSavedViewsSave({
      fetch: async (input) =>
        jsonResponse(
          savePayload(true, {
            preview_id: String(input) === "/api/views/preview" ? "receipt-1" : undefined,
          })
        ),
    });

    await submitSave("Focus");
    const diff = saveElement("[data-saved-views-diff]", HTMLPreElement);
    expect(diff.hidden).toBe(false);

    saveElement("[data-saved-views-cancel]", HTMLButtonElement).click();

    expect(diff.hidden).toBe(true);
    expect(diff.hasAttribute("data-saved-views-diff-role")).toBe(false);
  });
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

describe("saved-view listing recovery", () => {
  it("retries once while loading and restores focus and current-view highlighting", async () => {
    setLocation("/library?expr=tag%3Afocus+and+not+folder%3AArchive&sort=-modified");
    const container = mountNav();
    let resolve: (response: Response) => void = () => {};
    const fetcher = vi.fn().mockRejectedValueOnce(new Error("offline"))
      .mockImplementationOnce(() => new Promise<Response>((done) => { resolve = done; }));
    initSavedViewsNav({ fetch: fetcher });
    await vi.waitFor(() => expect(container.textContent).toContain("Saved views unavailable"));
    const retry = container.querySelector<HTMLButtonElement>("[data-saved-views-retry]")!;
    retry.focus();
    retry.click();
    retry.click();
    expect(retry.disabled).toBe(true);
    expect(document.activeElement).toBe(container.querySelector(".saved-views-feedback"));
    expect(container.textContent).toContain("Loading saved views");
    expect(fetcher).toHaveBeenCalledTimes(2);
    resolve(jsonResponse(viewsPayload()));
    await vi.waitFor(() => expect(navItems(container)).toHaveLength(3));
    expect(container.querySelector("[data-saved-views-retry]")).toBeNull();
    const current = container.querySelector('[data-saved-view="Focus"]');
    expect(current?.getAttribute("aria-current")).toBe("true");
    expect(document.activeElement).toBe(current);
  });

  it("keeps retry available after another failure and clears it for a successful empty list", async () => {
    const container = mountNav();
    const fetcher = vi.fn().mockResolvedValueOnce(jsonResponse({}, 503))
      .mockResolvedValueOnce(jsonResponse({}, 500))
      .mockResolvedValueOnce(jsonResponse({ ...viewsPayload(), views: [] }));
    initSavedViewsNav({ fetch: fetcher });
    await vi.waitFor(() => expect(container.textContent).toContain("Saved views unavailable"));
    const retry = container.querySelector<HTMLButtonElement>("[data-saved-views-retry]")!;
    retry.focus();
    retry.click();
    await vi.waitFor(() => expect(retry.disabled).toBe(false));
    expect(document.activeElement).toBe(retry);
    expect(container.textContent).toContain("Saved views unavailable");
    retry.focus();
    retry.click();
    await vi.waitFor(() => expect(container.textContent).toBe(""));
    expect(document.activeElement?.textContent).toBe("Brief");
  });

  it.each(["initial", "retry"])("ignores a late %s response after a save supplies newer views", async (phase) => {
    const container = mountNav();
    let resolve: (response: Response) => void = () => {};
    const fetcher = vi.fn();
    if (phase === "retry") fetcher.mockRejectedValueOnce(new Error("offline"));
    fetcher.mockImplementationOnce(() => new Promise<Response>((done) => { resolve = done; }));
    const handle = initSavedViewsNav({ fetch: fetcher })!;
    if (phase === "retry") {
      await vi.waitFor(() => expect(container.textContent).toContain("Saved views unavailable"));
      container.querySelector<HTMLButtonElement>("[data-saved-views-retry]")!.click();
    }
    handle.render([{ ...viewsPayload().views[0], name: "New view" }]);
    resolve(phase === "initial" ? jsonResponse(viewsPayload()) : jsonResponse({}, 503));
    await new Promise((done) => setTimeout(done, 0));
    expect(container.textContent).toBe("New view");
  });

  it("does not reclaim focus when the person moves away during a retry", async () => {
    const container = mountNav();
    let resolve: (response: Response) => void = () => {};
    const fetcher = vi.fn().mockRejectedValueOnce(new Error("offline"))
      .mockImplementationOnce(() => new Promise<Response>((done) => { resolve = done; }));
    initSavedViewsNav({ fetch: fetcher });
    await vi.waitFor(() => expect(container.textContent).toContain("Saved views unavailable"));
    const retry = container.querySelector<HTMLButtonElement>("[data-saved-views-retry]")!;
    retry.focus();
    retry.click();
    const other = document.querySelector<HTMLAnchorElement>('a[href="/library"]')!;
    other.focus();
    resolve(jsonResponse(viewsPayload()));
    await vi.waitFor(() => expect(navItems(container)).toHaveLength(3));
    expect(document.activeElement).toBe(other);
  });

});
