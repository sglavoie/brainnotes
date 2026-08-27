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
type FakeTapEvent = { target: { id: () => string; data: () => unknown } };

/**
 * The `tap` handler the controller bound for node selection.
 *
 * The fake never dispatches events of its own, so a test that needs a selection
 * — the isolate control is disabled without one — calls this directly.
 */
let nodeTapHandler: ((event: FakeTapEvent) => void) | null = null;

const tapNode = (nodeID: string): void => {
  if (!nodeTapHandler) {
    throw new Error("graph controller bound no node tap handler");
  }
  nodeTapHandler({ target: { id: () => nodeID, data: () => ({ id: nodeID }) } });
};

/**
 * How many times the controller has remounted the element set.
 *
 * `applyGraphElements` is the only caller of `add`, and it runs exactly once per
 * `rebuildDisplay`, so this counts rebuilds — which is what a debounce test
 * needs to see coalescing. The layout itself is invisible here: the fake mounts
 * no nodes, so `runLayout` short-circuits before it asks for one.
 */
let addCalls = 0;

const fakeCytoscape = () => ({
  on(event: string, selector?: unknown, handler?: unknown): void {
    if (event === "tap" && selector === "node" && typeof handler === "function") {
      nodeTapHandler = handler as (tapEvent: FakeTapEvent) => void;
    }
  },
  batch(fn: () => void): void {
    fn();
  },
  style(): void {},
  add(): void {
    addCalls += 1;
  },
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
  // Nothing is mounted in this fake, so every id lookup misses — which is what
  // the label policy already handles for a stale id.
  getElementById(): FakeCollection {
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
      <button id="graph-isolate-button" type="button" disabled></button>
      <button id="graph-isolate-clear-button" type="button" disabled></button>
      <input id="graph-hops-input" type="range" min="1" max="3" step="1" value="1" />
      <output id="graph-hops-value"></output>
      <input id="graph-mindegree-input" type="range" min="0" max="10" step="1" value="0" />
      <output id="graph-mindegree-value"></output>
      <input id="graph-collapse-input" type="range" min="0" max="3" step="1" value="0" />
      <output id="graph-collapse-value"></output>
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

const startGraph = (
  url: string,
  payload: unknown = { nodes: [], edges: [] }
): ReturnType<typeof vi.fn> => {
  setURL(url);
  mountGraphPage();
  nodeTapHandler = null;
  addCalls = 0;
  const fetchMock = vi.fn(async () => ({
    ok: true,
    json: async () => payload,
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

// A chain: alpha - beta - gamma, with delta off on its own.
const chainPayload = {
  nodes: [
    { data: { id: "alpha", title: "Alpha", folder: "Notes" } },
    { data: { id: "beta", title: "Beta", folder: "Notes" } },
    { data: { id: "gamma", title: "Gamma", folder: "Notes" } },
    { data: { id: "delta", title: "Delta", folder: "Notes" } },
  ],
  edges: [
    { data: { id: "e1", source: "alpha", target: "beta" } },
    { data: { id: "e2", source: "beta", target: "gamma" } },
  ],
};

const control = <T extends HTMLElement>(id: string): T =>
  document.getElementById(id) as unknown as T;

const statusText = (): string => control("graph-status").textContent || "";

const displayParam = (key: string): string | null =>
  new URLSearchParams(window.location.search).get(key);

describe("graph display controls", () => {
  it("isolates the selection through display-only params without refetching", async () => {
    const fetchMock = startGraph("/graph", chainPayload);
    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);

    const isolateButton = control<HTMLButtonElement>("graph-isolate-button");
    const clearButton = control<HTMLButtonElement>("graph-isolate-clear-button");
    const hopsInput = control<HTMLInputElement>("graph-hops-input");

    // Isolation needs an anchor, so both controls start dead.
    expect(isolateButton.disabled).toBe(true);
    expect(clearButton.disabled).toBe(true);

    tapNode("alpha");
    expect(isolateButton.disabled).toBe(false);

    isolateButton.click();
    expect(displayParam("g_isolate")).toBe("alpha");
    expect(clearButton.disabled).toBe(false);
    // The reduction is stated outright rather than left for the user to notice.
    expect(statusText()).toContain('Showing 2 of 4 nodes — isolated 1 hop from "Alpha"');

    hopsInput.value = "3";
    hopsInput.dispatchEvent(new Event("input"));
    expect(displayParam("g_hops")).toBe("3");
    expect(control("graph-hops-value").textContent).toBe("3 hops");
    expect(statusText()).toContain('Showing 3 of 4 nodes — isolated 3 hops from "Alpha"');

    clearButton.click();
    expect(displayParam("g_isolate")).toBeNull();
    expect(clearButton.disabled).toBe(true);
    expect(statusText()).not.toContain("isolated");

    // The whole exchange is display state: not one of those knobs may refetch.
    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  it("reproduces an isolated view from the URL without leaking g_ params into the fetch", async () => {
    const fetchMock = startGraph("/graph?g_isolate=beta&g_hops=2", chainPayload);
    await flush();

    expect(graphRequests(fetchMock)).toEqual(["/api/graph.json"]);
    expect(control<HTMLInputElement>("graph-hops-input").value).toBe("2");
    expect(control("graph-hops-value").textContent).toBe("2 hops");
    expect(control<HTMLButtonElement>("graph-isolate-clear-button").disabled).toBe(false);
    expect(statusText()).toContain('Showing 3 of 4 nodes — isolated 2 hops from "Beta"');
  });

  it("clears an isolate anchor the loaded graph does not contain, and says so", async () => {
    startGraph("/graph?g_isolate=missing", chainPayload);
    await flush();

    expect(displayParam("g_isolate")).toBeNull();
    expect(control<HTMLButtonElement>("graph-isolate-clear-button").disabled).toBe(true);
    expect(statusText()).toContain('Isolation cleared: "missing" is not in the current graph.');
  });
});

// A hub with three same-folder satellites plus a leaf and a stray in a second
// folder — enough shape for all three reductions to bite at once.
const clusterPayload = {
  nodes: [
    { data: { id: "hub", title: "Hub", folder: "Tradition" } },
    { data: { id: "a1", title: "A1", folder: "Tradition" } },
    { data: { id: "a2", title: "A2", folder: "Tradition" } },
    { data: { id: "a3", title: "A3", folder: "Tradition" } },
    { data: { id: "leaf", title: "Leaf", folder: "Elsewhere" } },
    { data: { id: "stray", title: "Stray", folder: "Elsewhere" } },
  ],
  edges: [
    { data: { id: "e1", source: "hub", target: "a1" } },
    { data: { id: "e2", source: "hub", target: "a2" } },
    { data: { id: "e3", source: "hub", target: "a3" } },
    { data: { id: "e4", source: "a1", target: "a2" } },
    { data: { id: "e5", source: "hub", target: "leaf" } },
  ],
};

const moveSlider = (id: string, value: string): void => {
  const input = control<HTMLInputElement>(id);
  input.value = value;
  input.dispatchEvent(new Event("input"));
};

describe("graph thinning controls", () => {
  it("round-trips both params into the URL and drops each at its off value", async () => {
    vi.useFakeTimers();
    const fetchMock = startGraph("/graph", chainPayload);
    await vi.advanceTimersByTimeAsync(0);
    expect(graphRequests(fetchMock)).toHaveLength(1);

    // Off is the default, so a clean view keeps a clean URL.
    expect(displayParam("g_mindeg")).toBeNull();
    expect(displayParam("g_collapse")).toBeNull();
    expect(control("graph-mindegree-value").textContent).toBe("off");
    expect(control("graph-collapse-value").textContent).toBe("off");

    moveSlider("graph-mindegree-input", "1");
    moveSlider("graph-collapse-input", "1");
    expect(displayParam("g_mindeg")).toBe("1");
    expect(displayParam("g_collapse")).toBe("1");
    expect(control("graph-mindegree-value").textContent).toBe("≥ 1");
    expect(control("graph-collapse-value").textContent).toBe("depth 1");

    await vi.advanceTimersByTimeAsync(150);
    // delta has no edges, so the degree floor takes it; the three survivors all
    // sit in Notes and fold into one aggregate.
    expect(statusText()).toContain(
      "Showing 1 of 4 nodes — degree ≥ 1, folders collapsed to depth 1."
    );

    moveSlider("graph-mindegree-input", "0");
    moveSlider("graph-collapse-input", "0");
    await vi.advanceTimersByTimeAsync(150);
    expect(displayParam("g_mindeg")).toBeNull();
    expect(displayParam("g_collapse")).toBeNull();
    expect(statusText()).not.toContain("degree ≥");
    expect(statusText()).not.toContain("collapsed");

    // Not one of those moves may cost a request: this is display state.
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  it("reproduces every knob from the URL and names each reduction in one line", async () => {
    const fetchMock = startGraph(
      "/graph?g_isolate=hub&g_hops=1&g_mindeg=2&g_collapse=1",
      clusterPayload
    );
    await flush();

    expect(graphRequests(fetchMock)).toEqual(["/api/graph.json"]);
    expect(control<HTMLInputElement>("graph-mindegree-input").value).toBe("2");
    expect(control("graph-mindegree-value").textContent).toBe("≥ 2");
    expect(control<HTMLInputElement>("graph-collapse-input").value).toBe("1");
    expect(control("graph-collapse-value").textContent).toBe("depth 1");
    expect(statusText()).toContain(
      'Showing 1 of 6 nodes — isolated 1 hop from "Hub", degree ≥ 2, folders collapsed to depth 1.'
    );
  });

  it("clears expanded folders when the collapse depth changes", async () => {
    vi.useFakeTimers();
    startGraph("/graph", chainPayload);
    await vi.advanceTimersByTimeAsync(0);

    moveSlider("graph-collapse-input", "1");
    await vi.advanceTimersByTimeAsync(150);
    expect(statusText()).toContain("folders collapsed to depth 1.");

    // Tapping an aggregate expands it rather than selecting it.
    tapNode("folder:Notes");
    expect(statusText()).not.toContain("collapsed");

    // Expansion keys name the first N path segments, so they mean something
    // different at depth 2 — the set has to start empty again.
    moveSlider("graph-collapse-input", "2");
    await vi.advanceTimersByTimeAsync(150);
    expect(statusText()).toContain("folders collapsed to depth 2.");
  });

  it("coalesces a slider sweep into a single rebuild", async () => {
    vi.useFakeTimers();
    startGraph("/graph", clusterPayload);
    await vi.advanceTimersByTimeAsync(0);
    // The initial load mounted the graph once.
    expect(addCalls).toBe(1);

    for (const step of ["1", "2", "3", "4"]) {
      moveSlider("graph-mindegree-input", step);
      await vi.advanceTimersByTimeAsync(20);
    }
    // Four steps inside the window, none of them a rebuild yet.
    expect(addCalls).toBe(1);
    // The URL still tracks the slider live, so the shareable state is never stale.
    expect(displayParam("g_mindeg")).toBe("4");

    await vi.advanceTimersByTimeAsync(150);
    expect(addCalls).toBe(2);
  });
});
