import { buildFilteredAPIURL, dispatchFiltersChange, hasActiveFilters } from "./filters";
import { rebuildDisplay } from "./graph-controller-display";
import { renderDetailsPanel } from "./graph-controller-interaction";
import { graphFilterMode, syncPageSearchModeParam } from "./graph-controller-params";
import {
  type GraphRuntime,
  computeDisplayedMatchIDs,
  currentDisplayOptions,
  elementSignature,
  matchIDsFor,
  pruneExpandedFolders,
} from "./graph-controller-runtime";
import { setStatus } from "./graph-controller-status";
import { buildDisplayGraph } from "./graph-controller-view";
import {
  applyGraphVisualState,
  updateZoomMatchesButton,
} from "./graph-controller-visual";
import {
  type GraphElements,
  type GraphSearchMode,
  graphScopeLabel,
  normalizeGraphPayload,
} from "./graph";

export const buildQueryURL = (runtime: GraphRuntime, query: string): string => {
  const url = buildFilteredAPIURL("/api/graph.json", query);
  if (runtime.state.searchMode !== "focus") {
    return url;
  }
  const parsed = new URL(url, window.location.origin);
  parsed.searchParams.set("q_mode", "focus");
  return `${parsed.pathname}${parsed.search}`;
};

// Repaints match highlighting on the mounted graph without rebuilding it, so
// positions, zoom, pan and selection survive a keystroke in focus mode.
export const refreshMatchHighlighting = (
  runtime: GraphRuntime,
  graphElements: GraphElements,
  displayElements: GraphElements
): void => {
  const instance = runtime.state.cy;
  if (!instance) {
    return;
  }
  runtime.state.currentGraphElements = graphElements;
  runtime.state.displayGraphElements = displayElements;
  // The mounted ids are display ids, so the match set has to be projected
  // through the aggregation before it can be written onto nodes.
  runtime.displayedMatchIDs = computeDisplayedMatchIDs(runtime);
  instance.batch(() => {
    instance.nodes().forEach((node) => {
      node.data("matchesQuery", runtime.displayedMatchIDs.has(node.id()));
    });
  });
  renderDetailsPanel(runtime);
  applyGraphVisualState(runtime);
};

// null means a newer load superseded this one, which then owns the status.
type GraphFetch = { ok: true; payload: unknown } | { ok: false; message: string } | null;

const fetchGraphPayload = async (
  runtime: GraphRuntime,
  query: string,
  localToken: number
): Promise<GraphFetch> => {
  const current = (): boolean => localToken === runtime.requestToken;
  try {
    const resp = await fetch(buildQueryURL(runtime, query), {
      headers: { Accept: "application/json" },
    });
    if (!current()) {
      return null;
    }
    if (!resp.ok) {
      return { ok: false, message: `Graph load failed (${resp.status}).` };
    }
    const payload: unknown = await resp.json();
    return current() ? { ok: true, payload } : null;
  } catch {
    return current() ? { ok: false, message: "Graph load failed." } : null;
  }
};

export const loadGraph = async (
  runtime: GraphRuntime,
  query: string,
  localToken: number
): Promise<void> => {
  if (typeof window.cytoscape !== "function") {
    setStatus(runtime, "Graph library failed to load.", "is-error");
    return;
  }

  setStatus(runtime, "Loading graph…", "is-loading");

  const fetched = await fetchGraphPayload(runtime, query, localToken);
  if (!fetched) {
    return;
  }
  if (!fetched.ok) {
    setStatus(runtime, fetched.message, "is-error");
    return;
  }
  const graphElements = normalizeGraphPayload(fetched.payload);
  mountGraph(runtime, graphElements);
  reportGraphLoaded(runtime, query, graphElements);
};

const mountGraph = (runtime: GraphRuntime, graphElements: GraphElements): void => {
  const { state } = runtime;
  const focusMode = state.searchMode === "focus";
  state.matchIDs = matchIDsFor(graphElements);
  // Before anything is built from the new payload, so the signature compared
  // below and the graph finally mounted run with the same expansion set.
  pruneExpandedFolders(runtime, graphElements);
  // The comparison is over what the new payload *would* mount, not over the
  // payload itself: display knobs sit between the two, so only the displayed
  // signature can answer "is the mounted graph the same graph?".
  const built = buildDisplayGraph(graphElements, currentDisplayOptions(runtime));
  const signature = elementSignature(built.elements);

  if (focusMode && state.cy && signature === runtime.currentElementSignature) {
    // Same nodes and edges as the mounted graph: only the match set moved, so
    // repaint highlighting in place instead of relaying out under the cursor.
    refreshMatchHighlighting(runtime, graphElements, built.elements);
    runtime.displayStats = built.stats;
  } else {
    state.currentGraphElements = graphElements;
    rebuildDisplay(
      runtime,
      {
        fitAfterLayout: true,
        resetPositions: true,
      },
      built
    );
  }
  updateZoomMatchesButton(runtime);
};

const reportGraphLoaded = (
  runtime: GraphRuntime,
  query: string,
  graphElements: GraphElements
): void => {
  const { state } = runtime;
  const focusMode = state.searchMode === "focus";
  // The expression counts as an active filter here: a graph narrowed only by
  // `expr=` would otherwise report an unscoped node count.
  const hasFilters = hasActiveFilters();
  const scopeLabel = graphScopeLabel(query, hasFilters);

  if (focusMode && query) {
    if (state.matchIDs.size === 0) {
      // The graph stays on screen in focus mode; only the highlighting is empty.
      setStatus(runtime, `No notes match ${scopeLabel}.`, "is-empty");
      return;
    }
    setStatus(
      runtime,
      `Focusing ${state.matchIDs.size} of ${graphElements.nodes.length} nodes matching ${scopeLabel}.`
    );
    return;
  }

  if (scopeLabel) {
    if (graphElements.nodes.length === 0) {
      setStatus(runtime, `No notes match ${scopeLabel}.`, "is-empty");
      return;
    }
    setStatus(
      runtime,
      `Showing ${graphElements.nodes.length} nodes and ${graphElements.edges.length} edges for ${scopeLabel}.`
    );
    return;
  }

  setStatus(
    runtime,
    `Loaded ${graphElements.nodes.length} nodes and ${graphElements.edges.length} edges.`
  );
};

export const scheduleLoad = (runtime: GraphRuntime, query: string, delay: number): void => {
  window.clearTimeout(runtime.debounceToken);
  runtime.state.currentQuery = query;
  const localToken = ++runtime.requestToken;
  if (delay <= 0) {
    void loadGraph(runtime, query, localToken);
    return;
  }
  runtime.debounceToken = window.setTimeout(() => {
    void loadGraph(runtime, query, localToken);
  }, delay);
};

/**
 * Announces that this page's filter state moved.
 *
 * The graph's own controls publish rather than reloading directly, so every
 * refetch — the free-text filter, the search mode, the facet chips, and the
 * Library query bar — arrives through the one listener below. The debounce
 * lives on the publish rather than on the load so a burst of keystrokes emits
 * one event instead of one per character.
 */
export const publishFilterChange = (runtime: GraphRuntime, delay: number): void => {
  window.clearTimeout(runtime.publishToken);
  const emit = (): void => {
    dispatchFiltersChange(window, {
      mode: graphFilterMode,
      params: new URLSearchParams(window.location.search),
    });
  };
  if (delay <= 0) {
    emit();
    return;
  }
  runtime.publishToken = window.setTimeout(emit, delay);
};

export const setSearchMode = (runtime: GraphRuntime, nextMode: GraphSearchMode): void => {
  if (runtime.state.searchMode === nextMode) {
    return;
  }
  runtime.state.searchMode = nextMode;
  syncPageSearchModeParam(nextMode);
  // Toggling changes the included set, so this reload deliberately goes
  // through the normal relayout path.
  publishFilterChange(runtime, 0);
};
