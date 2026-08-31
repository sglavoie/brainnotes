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
 * A Cytoscape stand-in that records what the controller mounted.
 *
 * It stays collection-blind on purpose — `nodes()`, `edges()` and
 * `getElementById()` all answer with the empty collection, so the layout, fit
 * and viewport paths short circuit and what is left is the plumbing under test.
 * What it does not throw away is the element definitions themselves: `add`
 * retains them and `elements().remove()` clears them, mirroring the
 * clear-then-add sequence `applyGraphElements` performs, so a test can assert
 * which nodes and edges are actually mounted rather than only what the status
 * line says about them.
 */
type FakeTapEvent = { target: { id: () => string; data: () => unknown } };

/**
 * The node handlers the controller bound, by event name.
 *
 * The fake never dispatches events of its own, so a test that needs a selection
 * — the isolate control is disabled without one — calls these directly. That is
 * also the only way to deliver the late event a real Cytoscape can emit for an
 * element a remount already removed.
 */
const nodeHandlers = new Map<string, (event: FakeTapEvent) => void>();

const fireNodeEvent = (event: string, nodeID: string): void => {
  const handler = nodeHandlers.get(event);
  if (!handler) {
    throw new Error(`graph controller bound no node ${event} handler`);
  }
  handler({ target: { id: () => nodeID, data: () => ({ id: nodeID }) } });
};

const tapNode = (nodeID: string): void => fireNodeEvent("tap", nodeID);

/**
 * How many times the controller has remounted the element set.
 *
 * `applyGraphElements` is the only caller of `add`, and it runs exactly once per
 * `rebuildDisplay`, so this counts rebuilds — which is what a debounce test
 * needs to see coalescing. The layout itself is invisible here: the fake reports
 * no nodes, so `runLayout` short-circuits before it asks for one.
 */
let addCalls = 0;

/** Element definitions currently mounted, in the order the controller added them. */
type MountedElement = { data: { id?: string; source?: string; target?: string } };

let mountedElements: MountedElement[] = [];

/** Ids of the mounted nodes — an element definition with no endpoints. */
const mountedNodeIDs = (): string[] =>
  mountedElements
    .filter((element) => element.data.source === undefined)
    .map((element) => String(element.data.id));

const mountedEdgeIDs = (): string[] =>
  mountedElements
    .filter((element) => element.data.source !== undefined)
    .map((element) => String(element.data.id));

const fakeCytoscape = () => ({
  on(event: string, selector?: unknown, handler?: unknown): void {
    if (selector === "node" && typeof handler === "function") {
      nodeHandlers.set(event, handler as (nodeEvent: FakeTapEvent) => void);
    }
  },
  batch(fn: () => void): void {
    fn();
  },
  style(): void {},
  add(elements: MountedElement[]): void {
    addCalls += 1;
    mountedElements = [...mountedElements, ...elements];
  },
  fit(): void {},
  pan(): { x: number; y: number } {
    return { x: 0, y: 0 };
  },
  zoom(): number {
    return 1;
  },
  elements(): { remove: () => void } {
    return {
      remove: (): void => {
        mountedElements = [];
      },
    };
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
  nodeHandlers.clear();
  addCalls = 0;
  mountedElements = [];
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
    // The radius is not a knob, so isolating writes one display param and no
    // retired companion alongside it.
    expect(displayParam("g_hops")).toBeNull();

    clearButton.click();
    expect(displayParam("g_isolate")).toBeNull();
    expect(clearButton.disabled).toBe(true);
    expect(statusText()).not.toContain("isolated");

    // The whole exchange is display state: not one of those knobs may refetch.
    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  it("reproduces an isolated view from the URL without leaking g_ params into the fetch", async () => {
    const fetchMock = startGraph("/graph?g_isolate=beta", chainPayload);
    await flush();

    expect(graphRequests(fetchMock)).toEqual(["/api/graph.json"]);
    expect(control<HTMLButtonElement>("graph-isolate-clear-button").disabled).toBe(false);
    expect(statusText()).toContain('Showing 3 of 4 nodes — isolated 1 hop from "Beta"');
  });

  it("clears an isolate anchor the loaded graph does not contain, and says so", async () => {
    startGraph("/graph?g_isolate=missing", chainPayload);
    await flush();

    expect(displayParam("g_isolate")).toBeNull();
    expect(control<HTMLButtonElement>("graph-isolate-clear-button").disabled).toBe(true);
    expect(statusText()).toContain('Isolation cleared: "missing" is not in the current graph.');
  });
});

// `g_hops` was the isolation radius before the neighborhood became fixed. Links
// carrying it are still out there — in notes, in chat logs, in browser history —
// and they have to keep working, which means rendering the one-hop view and
// saying so in the address bar rather than half-honouring a radius that no
// longer exists.
describe("graph legacy g_hops URLs", () => {
  for (const legacy of ["2", "3"]) {
    it(`strips g_hops=${legacy} and renders exactly one hop`, async () => {
      const fetchMock = startGraph(`/graph?g_isolate=alpha&g_hops=${legacy}`, chainPayload);
      await flush();

      expect(displayParam("g_hops")).toBeNull();
      expect(displayParam("g_isolate")).toBe("alpha");
      // The value is not merely dropped from the URL, it is dropped from the
      // view: gamma sat inside the old radius and is not mounted.
      expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);
      expect(mountedEdgeIDs()).toEqual(["e1"]);
      expect(statusText()).toContain('Showing 2 of 4 nodes — isolated 1 hop from "Alpha"');
      expect(graphRequests(fetchMock)).toEqual(["/api/graph.json"]);
    });
  }

  it("keeps the path, the hash and every other parameter while canonicalizing", async () => {
    startGraph("/graph?q=daily&g_hops=2&tag=work&g_mindeg=2#anchor", chainPayload);
    await flush();

    expect(window.location.pathname).toBe("/graph");
    expect(window.location.hash).toBe("#anchor");
    expect(window.location.search).toBe("?q=daily&tag=work&g_mindeg=2");
    // The surviving display knob is still in force, not just still in the URL.
    expect(control<HTMLInputElement>("graph-mindegree-input").value).toBe("2");
    expect(control("graph-mindegree-value").textContent).toBe("≥ 2");
  });

  it("removes every value of a repeated g_hops", async () => {
    startGraph("/graph?g_hops=2&g_isolate=alpha&g_hops=3", chainPayload);
    await flush();

    expect(window.location.search).toBe("?g_isolate=alpha");
  });

  it("keeps display params out of the graph request", async () => {
    const fetchMock = startGraph(
      "/graph?q=daily&g_isolate=alpha&g_hops=3&g_mindeg=1&g_collapse=1",
      chainPayload
    );
    await flush();

    expect(graphRequests(fetchMock)).toEqual(["/api/graph.json?q=daily"]);
  });
});

const detailsTitle = (): string => control("graph-hover-title").textContent || "";

// What the display pipeline excludes must leave Cytoscape, not merely stop being
// mentioned in the status line: an element that is still mounted is still
// tappable, hoverable and reachable from the keyboard.
describe("graph mounted-element boundary", () => {
  it("mounts only the anchor neighborhood while isolated, and the whole source again after", async () => {
    const fetchMock = startGraph("/graph", chainPayload);
    await flush();

    // The fetched graph, mounted whole: alpha - beta - gamma, with delta adrift.
    expect(mountedNodeIDs()).toEqual(["alpha", "beta", "gamma", "delta"]);
    expect(mountedEdgeIDs()).toEqual(["e1", "e2"]);

    tapNode("alpha");
    control<HTMLButtonElement>("graph-isolate-button").click();

    // One hop from alpha: the anchor and beta. gamma is two hops out and delta is
    // unreachable, so e2 loses an endpoint and goes with them.
    expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);
    expect(mountedEdgeIDs()).toEqual(["e1"]);

    control<HTMLButtonElement>("graph-isolate-clear-button").click();

    // "Show whole graph" is a remount of the fetched set, not an unhide — and it
    // comes from the payload already in hand, so restoring costs no request.
    expect(mountedNodeIDs()).toEqual(["alpha", "beta", "gamma", "delta"]);
    expect(mountedEdgeIDs()).toEqual(["e1", "e2"]);
    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  it("ignores late node events for an element isolation removed", async () => {
    const fetchMock = startGraph("/graph", chainPayload);
    await flush();

    const isolateButton = control<HTMLButtonElement>("graph-isolate-button");
    const canvas = control("graph-canvas");
    const openNote = control<HTMLAnchorElement>("graph-open-note");

    tapNode("alpha");
    isolateButton.click();
    expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);
    expect(detailsTitle()).toBe("Alpha");
    // The anchor is the selection, so isolating it again is a no-op the control
    // already refuses.
    expect(isolateButton.disabled).toBe(true);
    const anchorHref = openNote.getAttribute("href");
    const mountsBefore = addCalls;

    // gamma left the graph with the rebuild; Cytoscape can still deliver a queued
    // gesture or a pointer event that named it.
    tapNode("gamma");
    fireNodeEvent("select", "gamma");
    fireNodeEvent("mouseover", "gamma");

    // Selection, details, and the display controls all still describe the anchor.
    expect(detailsTitle()).toBe("Alpha");
    expect(openNote.getAttribute("href")).toBe(anchorHref);
    expect(isolateButton.disabled).toBe(true);
    expect(canvas.classList.contains("is-node-hovered")).toBe(false);
    expect(displayParam("g_isolate")).toBe("alpha");
    expect(statusText()).toContain('Showing 2 of 4 nodes — isolated 1 hop from "Alpha"');

    // The hidden node cannot be acted on either: the isolate control stays dead,
    // so clicking it cannot re-anchor the view onto gamma.
    isolateButton.click();
    expect(displayParam("g_isolate")).toBe("alpha");
    expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);
    expect(addCalls).toBe(mountsBefore);

    // None of it touched the network, either.
    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  // A mouseout is delivered after the pointer has already left, so it is the one
  // node event that routinely arrives *late* — after a rebuild removed its node,
  // or after the hover has already moved on to another node.
  it("ignores a late mouseout for an element isolation removed", async () => {
    const fetchMock = startGraph("/graph?g_isolate=alpha", chainPayload);
    await flush();

    const canvas = control("graph-canvas");
    // Isolated from the URL, so nothing is selected and the panel is free to
    // report the hover.
    expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);

    fireNodeEvent("mouseover", "beta");
    expect(canvas.classList.contains("is-node-hovered")).toBe(true);
    expect(detailsTitle()).toBe("Beta");

    // gamma left the graph with the rebuild; its queued mouseout names a node
    // the user cannot see and must not end beta's hover.
    fireNodeEvent("mouseout", "gamma");
    expect(canvas.classList.contains("is-node-hovered")).toBe(true);
    expect(detailsTitle()).toBe("Beta");

    // The node that owns the hover still ends it.
    fireNodeEvent("mouseout", "beta");
    expect(canvas.classList.contains("is-node-hovered")).toBe(false);
    expect(detailsTitle()).toBe("Hover a node");

    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  it("does not let an older mouseout clear a newer hover on another node", async () => {
    startGraph("/graph", chainPayload);
    await flush();

    const canvas = control("graph-canvas");

    // The pointer crosses alpha and lands on beta before alpha's mouseout is
    // delivered — both nodes are displayed, so the displayed-set guard alone
    // would let the stale event through.
    fireNodeEvent("mouseover", "alpha");
    fireNodeEvent("mouseover", "beta");
    expect(detailsTitle()).toBe("Beta");

    fireNodeEvent("mouseout", "alpha");
    expect(canvas.classList.contains("is-node-hovered")).toBe(true);
    expect(detailsTitle()).toBe("Beta");

    fireNodeEvent("mouseout", "beta");
    expect(canvas.classList.contains("is-node-hovered")).toBe(false);
    expect(detailsTitle()).toBe("Hover a node");
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
    const fetchMock = startGraph("/graph?g_isolate=hub&g_mindeg=2&g_collapse=1", clusterPayload);
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
