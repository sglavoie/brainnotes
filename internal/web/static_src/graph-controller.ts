import { getElementByID, queryAllElements, queryElement } from "./dom";
import {
  buildFilteredAPIURL,
  filterChangeEventName,
  hasActiveSharedFilters,
  readPageQuery,
  syncPageQueryParam,
} from "./filters";
import { type GraphDetailsRefs, renderGraphDetails } from "./graph-controller-details";
import { applyGraphVisualClasses, readLabelMode } from "./graph-controller-labels";
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
  type GraphViewportSnapshot,
  emptyGraphElements,
  graphNodeModelFromData,
  graphScopeLabel,
  normalizeGraphPayload,
  toCytoscapeElements,
} from "./graph";
import { type StatusKind, setStatusText } from "./status";
import { THEME_EVENT } from "./theme";

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
    resetLayoutButton: getElementByID("graph-reset-layout-button", HTMLButtonElement),
    labelModeInputs: queryAllElements(document, 'input[name="graph-label-mode"]', HTMLInputElement),
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
    lastMeaningfulViewport: null,
    layoutRunID: 0,
  };
  let debounceToken = 0;
  let requestToken = 0;
  let selectionSyncing = false;

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
    applyGraphVisualClasses(state.cy, state.labelMode, state.selectedNodeID, state.hoveredNodeID);
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
    return buildFilteredAPIURL("/api/graph.json", query);
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

    state.currentGraphElements = normalizeGraphPayload(payload);
    applyGraphElements(state.currentGraphElements, true);

    const hasFilters = hasActiveSharedFilters();
    const scopeLabel = graphScopeLabel(query, hasFilters);
    if (scopeLabel) {
      if (state.currentGraphElements.nodes.length === 0) {
        setStatus(`No notes match ${scopeLabel}.`, "is-empty");
        return;
      }
      setStatus(
        `Showing ${state.currentGraphElements.nodes.length} nodes and ${state.currentGraphElements.edges.length} edges for ${scopeLabel}.`
      );
      return;
    }

    setStatus(
      `Loaded ${state.currentGraphElements.nodes.length} nodes and ${state.currentGraphElements.edges.length} edges.`
    );
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
        const query = filterInput.value.trim();
        syncPageQueryParam(query);
        scheduleLoad(query, 150);
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
  }

  const initialQuery = readPageQuery();
  const filterInput = dom.filterInput;
  if (filterInput instanceof HTMLInputElement) {
    filterInput.value = initialQuery;
  }
  window.addEventListener(filterChangeEventName, () => {
    const query = filterInput instanceof HTMLInputElement ? filterInput.value.trim() : state.currentQuery;
    scheduleLoad(query, 0);
  });
  // theme.ts dispatches bn:themechange on document (non-bubbling), so listen
  // there. Restyle in place without reloading graph data.
  document.addEventListener(THEME_EVENT, restyleGraphForTheme);

  bindControls();
  scheduleLoad(initialQuery, 0);
};
