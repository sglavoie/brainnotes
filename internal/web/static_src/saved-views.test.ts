// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { normalizeSavedViewsResponse } from "./api-views";
import { initSavedViewsNav } from "./saved-views";
import { savedViewHref, savedViewParams } from "./saved-views-model";
import { updateBrowserQuery } from "./filters";
import {
  jsonResponse,
  waitForExpect,
  rottedDetail,
  viewsPayload,
  mountNav,
  navItems,
  setLocation,
} from "./saved-views.test-helpers";


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
