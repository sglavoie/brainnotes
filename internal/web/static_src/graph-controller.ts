import { getElementByID, queryAllElements, queryElement } from "./dom";
import {
  buildFilteredAPIURL,
  dispatchFiltersChange,
  filterChangeEventName,
  hasActiveFilters,
  readPageQuery,
  shouldHandleFilterChangeForMode,
  syncPageQueryParam,
  updateBrowserQuery,
} from "./filters";
import { type GraphDetailsRefs, renderGraphDetails } from "./graph-controller-details";
import {
  applyGraphVisualClasses,
  normalizeSearchMode,
  readLabelMode,
} from "./graph-controller-labels";
import {
  applyViewport,
  clampedSpread,
  currentViewport,
  layoutOptionsForSpread,
  seedDeterministicPositions,
  separateIsolatedNodes,
  updateSpreadLabel,
} from "./graph-controller-layout";
import { buildGraphStyle } from "./graph-controller-style";
import {
  type CytoscapeFactory,
  type GraphLayoutRunOptions,
  type GraphSelectionOptions,
  type GraphState,
} from "./graph-controller-types";
import {
  type GraphElements,
  type GraphLabelMode,
  type GraphNodeModel,
  type GraphSearchMode,
  type GraphViewportSnapshot,
  emptyGraphElements,
  graphNodeModelFromData,
  graphScopeLabel,
  normalizeGraphPayload,
  toCytoscapeElements,
} from "./graph";
import { type StatusKind, setStatusText } from "./status";
import { THEME_EVENT } from "./theme";

/**
 * The facet mode this page publishes and listens for. It matches
 * `graphFacetControlsData().Mode` on the server, so the graph's own facet chips
 * and its own controls speak the same mode; the query bar publishes `mode: ""`,
 * which reaches every mode.
 */
const graphFilterMode = "graph";

/** Debounce for a keystroke in the graph's free-text filter. */
const graphFilterDebounceMs = 150;

const readPageSearchMode = (): GraphSearchMode =>
  normalizeSearchMode(new URLSearchParams(window.location.search).get("q_mode"));

const syncPageSearchModeParam = (mode: GraphSearchMode): void => {
  const params = new URLSearchParams(window.location.search);
  if (mode === "focus") {
    params.set("q_mode", "focus");
  } else {
    params.delete("q_mode");
  }
  updateBrowserQuery(params);
};

export const initGraph = (): void => {
  const dom = {
    canvas: getElementByID("graph-canvas", HTMLElement),
    status: getElementByID("graph-status", HTMLElement),
    detailsPanel: queryElement(document, ".graph-hover", HTMLElement),
    hoverTitle: getElementByID("graph-hover-title", HTMLElement),
    hoverFolder: getElementByID("graph-hover-folder", HTMLElement),
    hoverDegree: getElementByID("graph-hover-degree", HTMLElement),
    hoverIssues: getElementByID("graph-hover-issues", HTMLElement),
    openNote: getElementByID("graph-open-note", HTMLAnchorElement),
    filterInput: getElementByID("graph-filter-input", HTMLInputElement),
    spreadInput: getElementByID("graph-spread-input", HTMLInputElement),
    spreadValue: getElementByID("graph-spread-value", HTMLElement),
    fitButton: getElementByID("graph-fit-button", HTMLButtonElement),
    zoomMatchesButton: getElementByID("graph-zoom-matches-button", HTMLButtonElement),
    resetLayoutButton: getElementByID("graph-reset-layout-button", HTMLButtonElement),
    labelModeInputs: queryAllElements(document, 'input[name="graph-label-mode"]', HTMLInputElement),
    searchModeInputs: queryAllElements(document, 'input[name="graph-search-mode"]', HTMLInputElement),
  };
  if (!dom.canvas) {
    return;
  }
  const canvas = dom.canvas;
  const detailsRefs: GraphDetailsRefs = {
    detailsPanel: dom.detailsPanel,
    hoverTitle: dom.hoverTitle,
    hoverFolder: dom.hoverFolder,
    hoverDegree: dom.hoverDegree,
    hoverIssues: dom.hoverIssues,
    openNote: dom.openNote,
  };

  const state: GraphState = {
    cy: null,
    activeLayout: null,
    currentQuery: "",
    currentGraphElements: emptyGraphElements(),
    hoveredNodeID: "",
    selectedNodeID: "",
    labelMode: readLabelMode(dom.labelModeInputs),
    searchMode: readPageSearchMode(),
    matchIDs: new Set<string>(),
    lastMeaningfulViewport: null,
    layoutRunID: 0,
  };
  let debounceToken = 0;
  let publishToken = 0;
  let requestToken = 0;
  let selectionSyncing = false;
  // Cheap identity of the element set currently mounted in Cytoscape. Equal
  // signatures across a focus-mode reload mean the graph itself did not change,
  // so we can repaint match highlighting without touching positions.
  let currentElementSignature = "";

  function setStatus(message: string, kind: StatusKind = ""): void {
    setStatusText(dom.status, message, kind, ["is-loading", "is-empty", "is-error"]);
  }

  function renderDetailsPanel(fallbackHoveredModel: GraphNodeModel | null = null): void {
    const selectedModel = findNodeModel(state.selectedNodeID);
    const hoveredModel = selectedModel
      ? null
      : findNodeModel(state.hoveredNodeID) || fallbackHoveredModel || null;
    renderGraphDetails(detailsRefs, selectedModel, hoveredModel);
  }

  function setLabelMode(nextMode: string): void {
    state.labelMode = nextMode === "all" ? "all" : "auto";
    applyGraphVisualState();
  }

  function setSearchMode(nextMode: GraphSearchMode): void {
    if (state.searchMode === nextMode) {
      return;
    }
    state.searchMode = nextMode;
    syncPageSearchModeParam(nextMode);
    // Toggling changes the included set, so this reload deliberately goes
    // through the normal relayout path.
    publishFilterChange(0);
  }

  function restyleGraphForTheme(): void {
    if (!state.cy) {
      return;
    }
    // Replacing the stylesheet repaints colors only; graph data, selection,
    // viewport, and layout positions are untouched. Re-toggle visual classes so
    // label/focus state stays in sync with the freshly applied style.
    state.cy.style(buildGraphStyle());
    applyGraphVisualState();
  }

  function stopActiveLayout(): void {
    if (state.activeLayout && typeof state.activeLayout.stop === "function") {
      state.activeLayout.stop();
    }
    state.activeLayout = null;
  }

  function fitCurrentGraph(): void {
    if (!state.cy || state.cy.nodes().length === 0) {
      return;
    }
    state.cy.fit(state.cy.elements(), 72);
    rememberMeaningfulViewport();
  }

  function rememberMeaningfulViewport(): void {
    if (!state.cy || state.cy.nodes().length === 0) {
      return;
    }
    state.lastMeaningfulViewport = currentViewport(state.cy);
  }

  function ensureGraph(): cytoscape.Core {
    if (state.cy) {
      return state.cy;
    }
    const createCytoscape = window.cytoscape as CytoscapeFactory;
    state.cy = createCytoscape({
      container: canvas,
      elements: [],
      style: buildGraphStyle(),
    });
    bindGraphEvents(state.cy);
    return state.cy;
  }

  function bindGraphEvents(instance: cytoscape.Core): void {
    instance.on("tap", "node", (event: cytoscape.EventObjectNode) => {
      selectNode(event.target.id());
    });

    instance.on("tap", (event: cytoscape.EventObjectCore) => {
      if (event.target === instance) {
        clearSelectedNode();
      }
    });

    instance.on("mouseover", "node", (event: cytoscape.EventObjectNode) => {
      const data = event.target.data();
      canvas.classList.add("is-node-hovered");
      setHoveredNode(event.target.id(), data);
    });
    instance.on("mouseout", "node", () => {
      canvas.classList.remove("is-node-hovered");
      setHoveredNode("", null);
    });
    instance.on("select", "node", (event: cytoscape.EventObjectNode) => {
      if (!selectionSyncing) {
        selectNode(event.target.id(), { fromCytoscape: true });
      }
    });
    instance.on("unselect", "node", (event: cytoscape.EventObjectNode) => {
      if (!selectionSyncing && event.target.id() === state.selectedNodeID) {
        clearSelectedNode({ fromCytoscape: true });
      }
    });
    instance.on("viewport", () => {
      rememberMeaningfulViewport();
      applyGraphVisualState();
    });
  }

  function findNodeModel(nodeID: string): GraphNodeModel | null {
    return state.currentGraphElements.nodes.find((node) => node.id === nodeID) || null;
  }

  function syncCytoscapeSelection(nodeID: string): void {
    if (!state.cy || selectionSyncing) {
      return;
    }

    selectionSyncing = true;
    try {
      state.cy.nodes().forEach((node) => {
        const shouldSelect = node.id() === nodeID;
        if (shouldSelect && !node.selected()) {
          node.select();
        } else if (!shouldSelect && node.selected()) {
          node.unselect();
        }
      });
    } finally {
      selectionSyncing = false;
    }
  }

  function selectNode(nodeID: string, options: GraphSelectionOptions = {}): void {
    const model = findNodeModel(nodeID);
    state.selectedNodeID = model ? model.id : "";
    if (!options || !options.fromCytoscape) {
      syncCytoscapeSelection(state.selectedNodeID);
    }
    renderDetailsPanel();
    applyGraphVisualState();
  }

  function clearSelectedNode(options: GraphSelectionOptions = {}): void {
    state.selectedNodeID = "";
    if (!options || !options.fromCytoscape) {
      syncCytoscapeSelection("");
    }
    renderDetailsPanel();
    applyGraphVisualState();
  }

  function setHoveredNode(nodeID: string, fallbackData: unknown): void {
    state.hoveredNodeID = nodeID || "";
    if (!state.hoveredNodeID) {
      renderDetailsPanel();
      applyGraphVisualState();
      return;
    }

    const fallbackModel = fallbackData
      ? graphNodeModelFromData(fallbackData, state.hoveredNodeID)
      : null;
    renderDetailsPanel(fallbackModel);
    applyGraphVisualState();
  }

  function keyboardTargetNodeID(): string {
    if (state.hoveredNodeID && findNodeModel(state.hoveredNodeID)) {
      return state.hoveredNodeID;
    }
    if (state.selectedNodeID && findNodeModel(state.selectedNodeID)) {
      return state.selectedNodeID;
    }
    if (!state.cy) {
      return "";
    }

    const selectedNode = state.cy
      .nodes()
      .filter((node) => node.selected())
      .first();
    return selectedNode && selectedNode.length > 0 ? selectedNode.id() : "";
  }

  function handleGraphKeyDown(event: KeyboardEvent): void {
    if (event.key !== "Enter" && event.key !== " " && event.key !== "Spacebar") {
      return;
    }

    const nodeID = keyboardTargetNodeID();
    if (!nodeID) {
      return;
    }

    event.preventDefault();
    selectNode(nodeID);
  }

  function applyGraphVisualState(): void {
    applyGraphVisualClasses(
      state.cy,
      state.labelMode,
      state.selectedNodeID,
      state.hoveredNodeID,
      state.matchIDs
    );
  }

  function updateZoomMatchesButton(): void {
    if (dom.zoomMatchesButton instanceof HTMLButtonElement) {
      dom.zoomMatchesButton.disabled = state.matchIDs.size === 0;
    }
  }

  function zoomToMatches(): void {
    if (!state.cy || state.matchIDs.size === 0) {
      return;
    }
    const matchedNodes = state.cy.nodes().filter((node) => state.matchIDs.has(node.id()));
    if (matchedNodes.length === 0) {
      return;
    }
    state.cy.fit(matchedNodes, 96);
    rememberMeaningfulViewport();
  }

  function runLayout(options: GraphLayoutRunOptions = {}): void {
    if (!state.cy || state.cy.nodes().length === 0) {
      return;
    }
    const layoutOptions = options;
    const fitAfterLayout = Boolean(layoutOptions.fitAfterLayout);
    const preservedViewport = layoutOptions.preserveViewport ? currentViewport(state.cy) : null;
    const runID = state.layoutRunID + 1;
    state.layoutRunID = runID;
    stopActiveLayout();

    if (layoutOptions.resetPositions) {
      seedDeterministicPositions(state.cy, clampedSpread(dom.spreadInput));
    }

    let finalized = false;
    const finalizeLayout = () => {
      if (finalized || runID !== state.layoutRunID) {
        return;
      }
      finalized = true;
      separateIsolatedNodes(state.cy, clampedSpread(dom.spreadInput));
      if (fitAfterLayout) {
        fitCurrentGraph();
      } else if (preservedViewport) {
        applyViewport(state.cy, preservedViewport);
        rememberMeaningfulViewport();
      } else {
        rememberMeaningfulViewport();
      }
      applyGraphVisualState();
      state.activeLayout = null;
    };

    const layout = state.cy.layout(layoutOptionsForSpread(clampedSpread(dom.spreadInput)));
    state.activeLayout = layout;
    if (typeof layout.one === "function") {
      layout.one("layoutstop", finalizeLayout);
    }
    layout.run();
    if (typeof layout.one !== "function") {
      finalizeLayout();
    }
  }

  function applyGraphElements(graphElements: GraphElements, fitAfterLayout: boolean): void {
    const instance = ensureGraph();
    stopActiveLayout();
    const selectedNodeID = state.selectedNodeID;

    selectionSyncing = true;
    try {
      instance.elements().remove();
      if (graphElements.nodes.length > 0 || graphElements.edges.length > 0) {
        instance.add(toCytoscapeElements(graphElements));
      }
    } finally {
      selectionSyncing = false;
    }

    state.selectedNodeID = graphElements.nodes.some((node) => node.id === selectedNodeID) ? selectedNodeID : "";
    syncCytoscapeSelection(state.selectedNodeID);
    setHoveredNode("", null);

    if (instance.nodes().length === 0) {
      state.lastMeaningfulViewport = null;
      applyGraphVisualState();
      return;
    }

    runLayout({
      fitAfterLayout,
      resetPositions: true,
    });
  }

  function buildQueryURL(query: string): string {
    const url = buildFilteredAPIURL("/api/graph.json", query);
    if (state.searchMode !== "focus") {
      return url;
    }
    const parsed = new URL(url, window.location.origin);
    parsed.searchParams.set("q_mode", "focus");
    return `${parsed.pathname}${parsed.search}`;
  }

  function elementSignature(graphElements: GraphElements): string {
    const nodeIDs = graphElements.nodes.map((node) => node.id).sort();
    const edgeIDs = graphElements.edges.map((edge) => edge.id).sort();
    return `${nodeIDs.join("\n")} ${edgeIDs.join("\n")}`;
  }

  function matchIDsFor(graphElements: GraphElements): Set<string> {
    const ids = new Set<string>();
    for (const node of graphElements.nodes) {
      if (node.matchesQuery) {
        ids.add(node.id);
      }
    }
    return ids;
  }

  // Repaints match highlighting on the mounted graph without rebuilding it, so
  // positions, zoom, pan and selection survive a keystroke in focus mode.
  function refreshMatchHighlighting(graphElements: GraphElements): void {
    const instance = state.cy;
    if (!instance) {
      return;
    }
    instance.batch(() => {
      instance.nodes().forEach((node) => {
        node.data("matchesQuery", state.matchIDs.has(node.id()));
      });
    });
    state.currentGraphElements = graphElements;
    renderDetailsPanel();
    applyGraphVisualState();
  }

  async function loadGraph(query: string, localToken: number): Promise<void> {
    if (typeof window.cytoscape !== "function") {
      setStatus("Graph library failed to load.", "is-error");
      return;
    }

    setStatus("Loading graph...", "is-loading");

    let payload: unknown;
    try {
      const resp = await fetch(buildQueryURL(query), {
        headers: { Accept: "application/json" },
      });
      if (localToken !== requestToken) {
        return;
      }
      if (!resp.ok) {
        setStatus(`Graph load failed (${resp.status}).`, "is-error");
        return;
      }
      payload = await resp.json();
    } catch (error) {
      if (localToken !== requestToken) {
        return;
      }
      setStatus("Graph load failed.", "is-error");
      return;
    }

    if (localToken !== requestToken) {
      return;
    }

    const graphElements = normalizeGraphPayload(payload);
    const signature = elementSignature(graphElements);
    const focusMode = state.searchMode === "focus";
    state.matchIDs = matchIDsFor(graphElements);

    if (focusMode && state.cy && signature === currentElementSignature) {
      // Same nodes and edges as the mounted graph: only the match set moved, so
      // repaint highlighting in place instead of relaying out under the cursor.
      refreshMatchHighlighting(graphElements);
    } else {
      state.currentGraphElements = graphElements;
      currentElementSignature = signature;
      applyGraphElements(graphElements, true);
    }
    updateZoomMatchesButton();

    // The expression counts as an active filter here: a graph narrowed only by
    // `expr=` would otherwise report an unscoped node count.
    const hasFilters = hasActiveFilters();
    const scopeLabel = graphScopeLabel(query, hasFilters);

    if (focusMode && query) {
      if (state.matchIDs.size === 0) {
        // The graph stays on screen in focus mode; only the highlighting is empty.
        setStatus(`No notes match ${scopeLabel}.`, "is-empty");
        return;
      }
      setStatus(
        `Focusing ${state.matchIDs.size} of ${graphElements.nodes.length} nodes matching ${scopeLabel}.`
      );
      return;
    }

    if (scopeLabel) {
      if (graphElements.nodes.length === 0) {
        setStatus(`No notes match ${scopeLabel}.`, "is-empty");
        return;
      }
      setStatus(
        `Showing ${graphElements.nodes.length} nodes and ${graphElements.edges.length} edges for ${scopeLabel}.`
      );
      return;
    }

    setStatus(`Loaded ${graphElements.nodes.length} nodes and ${graphElements.edges.length} edges.`);
  }

  function scheduleLoad(query: string, delay: number): void {
    window.clearTimeout(debounceToken);
    state.currentQuery = query;
    const localToken = ++requestToken;
    if (delay <= 0) {
      void loadGraph(query, localToken);
      return;
    }
    debounceToken = window.setTimeout(() => {
      void loadGraph(query, localToken);
    }, delay);
  }

  /**
   * Announces that this page's filter state moved.
   *
   * The graph's own controls publish rather than reloading directly, so every
   * refetch — the free-text filter, the search mode, the facet chips, and the
   * Library query bar — arrives through the one listener below. The debounce
   * lives on the publish rather than on the load so a burst of keystrokes emits
   * one event instead of one per character.
   */
  function publishFilterChange(delay: number): void {
    window.clearTimeout(publishToken);
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
    publishToken = window.setTimeout(emit, delay);
  }

  function bindControls(): void {
    canvas.addEventListener("keydown", handleGraphKeyDown);

    const openNote = dom.openNote;
    if (openNote instanceof HTMLAnchorElement) {
      openNote.addEventListener("click", (event) => {
        if (openNote.getAttribute("aria-disabled") === "true") {
          event.preventDefault();
        }
      });
    }

    const filterInput = dom.filterInput;
    if (filterInput instanceof HTMLInputElement) {
      filterInput.addEventListener("input", () => {
        syncPageQueryParam(filterInput.value.trim());
        publishFilterChange(graphFilterDebounceMs);
      });
      filterInput.addEventListener("keydown", (event) => {
        if (event.key !== "Enter") {
          return;
        }
        event.preventDefault();
        zoomToMatches();
      });
    }

    updateSpreadLabel(dom.spreadInput, dom.spreadValue);
    if (dom.spreadInput instanceof HTMLInputElement) {
      dom.spreadInput.addEventListener("input", () => {
        updateSpreadLabel(dom.spreadInput, dom.spreadValue);
        runLayout({
          fitAfterLayout: false,
          preserveViewport: true,
        });
      });
    }

    if (dom.fitButton instanceof HTMLButtonElement) {
      dom.fitButton.addEventListener("click", fitCurrentGraph);
    }

    if (dom.zoomMatchesButton instanceof HTMLButtonElement) {
      dom.zoomMatchesButton.addEventListener("click", zoomToMatches);
    }
    updateZoomMatchesButton();

    if (dom.resetLayoutButton instanceof HTMLButtonElement) {
      dom.resetLayoutButton.addEventListener("click", () => {
        runLayout({
          fitAfterLayout: true,
          resetPositions: true,
        });
      });
    }

    for (const input of dom.labelModeInputs) {
      if (!(input instanceof HTMLInputElement)) {
        continue;
      }
      input.addEventListener("change", () => {
        if (input.checked) {
          setLabelMode(input.value);
        }
      });
    }

    for (const input of dom.searchModeInputs) {
      if (!(input instanceof HTMLInputElement)) {
        continue;
      }
      input.addEventListener("change", () => {
        if (input.checked) {
          setSearchMode(normalizeSearchMode(input.value));
        }
      });
    }
  }

  const initialQuery = readPageQuery();
  const filterInput = dom.filterInput;
  if (filterInput instanceof HTMLInputElement) {
    filterInput.value = initialQuery;
  }
  for (const input of dom.searchModeInputs) {
    if (input instanceof HTMLInputElement) {
      input.checked = normalizeSearchMode(input.value) === state.searchMode;
    }
  }
  // The single refetch path. `mode: ""` — what the Library query bar publishes —
  // passes the gate, so an expression confirmed anywhere on the page reloads the
  // graph; `buildFilteredAPIURL` reads the URL, so the listener needs to know
  // nothing about the parameter that changed.
  window.addEventListener(filterChangeEventName, (event) => {
    if (!shouldHandleFilterChangeForMode(event, graphFilterMode)) {
      return;
    }
    const query = filterInput instanceof HTMLInputElement ? filterInput.value.trim() : state.currentQuery;
    scheduleLoad(query, 0);
  });
  // theme.ts dispatches bn:themechange on document (non-bubbling), so listen
  // there. Restyle in place without reloading graph data.
  document.addEventListener(THEME_EVENT, restyleGraphForTheme);

  bindControls();
  scheduleLoad(initialQuery, 0);
};
