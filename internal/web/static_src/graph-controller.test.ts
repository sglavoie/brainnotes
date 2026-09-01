// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { dispatchFiltersChange, syncExprParam } from "./filters";
import {
  flush,
  graphRequests,
  resetGraphTest,
  startGraph,
} from "./graph-controller.test-helpers";

afterEach(resetGraphTest);

describe("graph filter propagation", () => {
  it("carries an expression from the page URL into the first graph request", async () => {
    const fetchMock = startGraph("/graph?q=daily&expr=tag%3Afocus&tag=work");
    await flush();

    expect(graphRequests(fetchMock)).toEqual([
      "/api/graph.json?q=daily&tag=work&expr=tag%3Afocus",
    ]);
  });

  // Library to Graph: the query bar publishes `mode: ""` after the server
  // confirms an expression. The graph never sees the bar — it re-reads the URL
  // and refetches, which is the whole coupling.
  it("refetches when a filter change is published for every mode", async () => {
    const fetchMock = startGraph("/graph");
    await flush();
    expect(graphRequests(fetchMock)).toEqual(["/api/graph.json"]);

    syncExprParam("tag:focus and not folder:Archive");
    dispatchFiltersChange(window, {
      mode: "",
      params: new URLSearchParams(window.location.search),
    });
    await flush();

    expect(graphRequests(fetchMock)[1]).toBe(
      "/api/graph.json?expr=tag%3Afocus+and+not+folder%3AArchive"
    );
  });

  it("ignores a filter change addressed to another page's mode", async () => {
    const fetchMock = startGraph("/graph");
    await flush();

    dispatchFiltersChange(window, {
      mode: "search",
      params: new URLSearchParams(),
    });
    await flush();

    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  // Graph to Library: the graph's own controls publish through the shared event
  // rather than reloading directly, so anything else listening on the page sees
  // the same change the graph reacted to.
  it("publishes its own control changes through the shared filter event", async () => {
    vi.useFakeTimers();
    const fetchMock = startGraph("/graph");
    await vi.advanceTimersByTimeAsync(0);
    expect(graphRequests(fetchMock)).toHaveLength(1);

    const published: string[] = [];
    window.addEventListener("bn:filterschange", (event) => {
      published.push(String((event as CustomEvent<{ mode: string }>).detail.mode));
    });

    const filterInput = document.getElementById("graph-filter-input") as HTMLInputElement;
    filterInput.value = "daily";
    filterInput.dispatchEvent(new Event("input"));
    // The debounce lives on the publish, so a keystroke emits nothing yet.
    expect(published).toEqual([]);
    expect(window.location.search).toBe("?q=daily");

    await vi.advanceTimersByTimeAsync(150);
    expect(published).toEqual(["graph"]);
    expect(graphRequests(fetchMock)[1]).toBe("/api/graph.json?q=daily");

    const focusInput = document.getElementById("graph-search-mode-focus") as HTMLInputElement;
    focusInput.checked = true;
    focusInput.dispatchEvent(new Event("change"));
    await vi.advanceTimersByTimeAsync(0);

    expect(published).toEqual(["graph", "graph"]);
    expect(graphRequests(fetchMock)[2]).toBe("/api/graph.json?q=daily&q_mode=focus");
  });
});
