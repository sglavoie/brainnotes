import { vi } from "vitest";

import { initGraph } from "./graph-controller";

/**
 * Shared fixtures for the graph-controller page tests, following the same
 * `<cluster>.test-helpers.ts` convention as daily-fixes and synthesize.
 *
 * Everything here is state the fake Cytoscape and the mounted page share across
 * the topic files that split off this cluster, so `startGraph` is the one entry
 * point and `resetGraphTest` — called from each file's `afterEach` — is the one
 * way that state is put back.
 */

export const setURL = (url: string): void => {
  window.history.replaceState(null, "", url);
};

/** A singular element: Cytoscape reports `length: 1` for one node, 0 for a miss. */
type FakeNode = { length: number; id: () => string };

type FakeCollection = {
  length: number;
  forEach: () => void;
  filter: () => FakeCollection;
  toArray: () => unknown[];
  first: () => FakeNode | null;
};

const emptyCollection: FakeCollection = {
  length: 0,
  forEach: () => {},
  filter: () => emptyCollection,
  toArray: () => [],
  first: () => null,
};

/**
 * A node Cytoscape still reports as selected, whatever the controller mounted.
 *
 * Keyboard activation falls back to Cytoscape's own selection when neither the
 * hover nor the controller's selection resolves, and that fallback is the one
 * interactive lookup a test cannot reach through the node handlers: it reads the
 * renderer, not the controller's state. Setting this is how a test puts the
 * renderer into the state a remount can leave it in — a selection flag still
 * standing on an element the display pipeline removed — so the guard that checks
 * that id against the displayed set has something to refuse.
 */
let staleCytoscapeSelectionID = "";

export const setStaleCytoscapeSelectionID = (nodeID: string): void => {
  staleCytoscapeSelectionID = nodeID;
};

/**
 * The node collection, still length-blind so layout and fit short circuit.
 *
 * Only `first()` carries anything, and only when a test asked for a stale
 * selection; `filter()` answers with the same collection because the controller
 * filters on `selected()` and this fake has exactly one selection to report.
 */
const nodesCollection = (): FakeCollection => {
  const collection: FakeCollection = {
    length: 0,
    forEach: () => {},
    filter: () => collection,
    toArray: () => [],
    first: () =>
      staleCytoscapeSelectionID === ""
        ? null
        : { length: 1, id: () => staleCytoscapeSelectionID },
  };
  return collection;
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

export const fireNodeEvent = (event: string, nodeID: string): void => {
  const handler = nodeHandlers.get(event);
  if (!handler) {
    throw new Error(`graph controller bound no node ${event} handler`);
  }
  handler({ target: { id: () => nodeID, data: () => ({ id: nodeID }) } });
};

export const tapNode = (nodeID: string): void => fireNodeEvent("tap", nodeID);

/**
 * How many times the controller has remounted the element set.
 *
 * `applyGraphElements` is the only caller of `add`, and it runs exactly once per
 * `rebuildDisplay`, so this counts rebuilds — which is what a debounce test
 * needs to see coalescing. The layout itself is invisible here: the fake reports
 * no nodes, so `runLayout` short-circuits before it asks for one.
 */
let addCalls = 0;

export const mountCount = (): number => addCalls;

/** Element definitions currently mounted, in the order the controller added them. */
type MountedElement = { data: { id?: string; source?: string; target?: string } };

let mountedElements: MountedElement[] = [];

/** Ids of the mounted nodes — an element definition with no endpoints. */
export const mountedNodeIDs = (): string[] =>
  mountedElements
    .filter((element) => element.data.source === undefined)
    .map((element) => String(element.data.id));

export const mountedEdgeIDs = (): string[] =>
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
    return nodesCollection();
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
export const graphRequests = (fetchMock: ReturnType<typeof vi.fn>): string[] =>
  fetchMock.mock.calls.map((call) => String(call[0]));

// initGraph subscribes to window for the lifetime of the page, and jsdom keeps
// one window for the whole file. Unsubscribing between tests is what stops an
// earlier controller from answering a later test's event with a fetch of its
// own, which would shift every request index by one.
const teardowns: Array<() => void> = [];

export const startGraph = (
  url: string,
  payload: unknown = { nodes: [], edges: [] }
): ReturnType<typeof vi.fn> => {
  setURL(url);
  mountGraphPage();
  nodeHandlers.clear();
  addCalls = 0;
  mountedElements = [];
  staleCytoscapeSelectionID = "";
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

export const flush = async (): Promise<void> => {
  await new Promise((resolve) => setTimeout(resolve, 0));
};

export const resetGraphTest = (): void => {
  while (teardowns.length > 0) {
    teardowns.pop()?.();
  }
  vi.unstubAllGlobals();
  vi.useRealTimers();
  document.body.innerHTML = "";
  setURL("/");
};

export const control = <T extends HTMLElement>(id: string): T =>
  document.getElementById(id) as unknown as T;

export const statusText = (): string => control("graph-status").textContent || "";

export const detailsTitle = (): string => control("graph-hover-title").textContent || "";

export const displayParam = (key: string): string | null =>
  new URLSearchParams(window.location.search).get(key);

export const moveSlider = (id: string, value: string): void => {
  const input = control<HTMLInputElement>(id);
  input.value = value;
  input.dispatchEvent(new Event("input"));
};

/** Presses a key on the canvas and reports whether the controller claimed it. */
export const pressCanvasKey = (key: string): boolean => {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
  control("graph-canvas").dispatchEvent(event);
  return event.defaultPrevented;
};

// A chain: alpha - beta - gamma, with delta off on its own.
export const chainPayload = {
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

// A hub with three same-folder satellites plus a leaf and a stray in a second
// folder — enough shape for all three reductions to bite at once.
export const clusterPayload = {
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
