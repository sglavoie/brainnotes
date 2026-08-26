// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { dispatchFiltersChange, syncExprParam } from "./filters";
import { initGraph } from "./graph-controller";

const setURL = (url: string): void => {
  window.history.replaceState(null, "", url);
};

type FakeCollection = {
  length: number;
  forEach: () => void;
  filter: () => FakeCollection;
  toArray: () => unknown[];
  first: () => null;
};

const emptyCollection: FakeCollection = {
  length: 0,
  forEach: () => {},
  filter: () => emptyCollection,
  toArray: () => [],
  first: () => null,
};

/**
 * The smallest Cytoscape stand-in initGraph can drive.
 *
 * Every payload in this file is an empty graph, which is what keeps the fake
 * this small: with no nodes, the layout, fit and viewport paths all short
 * circuit, so what is left is exactly the plumbing under test — which URL the
 * controller fetches, and what makes it fetch again.
 */
const fakeCytoscape = () => ({
  on(): void {},
  batch(fn: () => void): void {
    fn();
  },
  style(): void {},
  add(): void {},
  fit(): void {},
  pan(): { x: number; y: number } {
    return { x: 0, y: 0 };
  },
  zoom(): number {
    return 1;
  },
  elements(): { remove: () => void } {
    return { remove: () => {} };
  },
  nodes(): FakeCollection {
    return emptyCollection;
  },
  edges(): FakeCollection {
    return emptyCollection;
  },
  layout(): { run: () => void; stop: () => void } {
    return { run: () => {}, stop: () => {} };
  },
});

const mountGraphPage = (): void => {
  document.body.innerHTML = `
    <p id="graph-status"></p>
    <div class="graph-controls">
      <input id="graph-filter-input" type="search" />
      <input id="graph-search-mode-filter" type="radio" name="graph-search-mode" value="filter" checked />
      <input id="graph-search-mode-focus" type="radio" name="graph-search-mode" value="focus" />
      <input id="graph-spread-input" type="range" min="0.8" max="6.0" step="0.1" value="2.0" />
      <output id="graph-spread-value"></output>
      <button id="graph-fit-button" type="button"></button>
      <button id="graph-zoom-matches-button" type="button"></button>
      <button id="graph-reset-layout-button" type="button"></button>
      <input id="graph-label-mode-auto" type="radio" name="graph-label-mode" value="auto" checked />
      <input id="graph-label-mode-all" type="radio" name="graph-label-mode" value="all" />
    </div>
    <div class="graph-hover">
      <p id="graph-hover-title"></p>
      <p id="graph-hover-folder"></p>
      <p id="graph-hover-degree"></p>
      <ul id="graph-hover-issues" hidden></ul>
      <a id="graph-open-note"></a>
    </div>
    <div id="graph-canvas"></div>
  `;
};

/** URLs the controller has fetched, oldest first. */
const graphRequests = (fetchMock: ReturnType<typeof vi.fn>): string[] =>
  fetchMock.mock.calls.map((call) => String(call[0]));

// initGraph subscribes to window for the lifetime of the page, and jsdom keeps
// one window for the whole file. Unsubscribing between tests is what stops an
// earlier controller from answering a later test's event with a fetch of its
// own, which would shift every request index by one.
const teardowns: Array<() => void> = [];

const startGraph = (url: string): ReturnType<typeof vi.fn> => {
  setURL(url);
  mountGraphPage();
  const fetchMock = vi.fn(async () => ({
    ok: true,
    json: async () => ({ nodes: [], edges: [] }),
  }));
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("cytoscape", fakeCytoscape);

  const added: Array<[string, EventListener]> = [];
  const subscribe = window.addEventListener.bind(window);
  const spy = vi
    .spyOn(window, "addEventListener")
    .mockImplementation((type: string, listener: EventListenerOrEventListenerObject | null) => {
      added.push([type, listener as EventListener]);
      subscribe(type, listener as EventListener);
    });
  try {
    initGraph();
  } finally {
    spy.mockRestore();
  }
  teardowns.push(() => {
    for (const [type, listener] of added) {
      window.removeEventListener(type, listener);
    }
  });
  return fetchMock;
};

const flush = async (): Promise<void> => {
  await new Promise((resolve) => setTimeout(resolve, 0));
};

afterEach(() => {
  while (teardowns.length > 0) {
    teardowns.pop()?.();
  }
  vi.unstubAllGlobals();
  vi.useRealTimers();
  document.body.innerHTML = "";
  setURL("/");
});

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
