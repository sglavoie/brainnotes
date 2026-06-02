import { getElementByID, queryAllElements, queryElement } from "./dom";
import {
  buildFilteredAPIURL,
  filterChangeEventName,
  hasActiveSharedFilters,
  notePathForKey,
  readPageQuery,
  syncPageQueryParam,
} from "./filters";
import {
  emptyGraphElements,
  type GraphElements,
  type GraphFocusState,
  type GraphLabelMode,
  type GraphNodeModel,
  type GraphViewportSnapshot,
  graphNodeModelFromData,
  graphScopeLabel,
  normalizeGraphPayload,
  readDegreeValue,
  toCytoscapeElements,
} from "./graph";
import { type StatusKind, setStatusText } from "./status";

type CytoscapeGraphOptions = {
  container: HTMLElement;
  elements: cytoscape.ElementDefinition[];
  style: cytoscape.StylesheetJson;
};

type CytoscapeFactory = (options: CytoscapeGraphOptions) => cytoscape.Core;

type GraphState = {
  cy: cytoscape.Core | null;
  activeLayout: cytoscape.Layouts | null;
  currentQuery: string;
  currentGraphElements: GraphElements;
  hoveredNodeID: string;
  selectedNodeID: string;
  labelMode: GraphLabelMode;
  lastMeaningfulViewport: GraphViewportSnapshot | null;
  layoutRunID: number;
};
type GraphSelectionOptions = {
  fromCytoscape?: boolean;
};
type GraphLayoutRunOptions = {
  fitAfterLayout?: boolean;
  preserveViewport?: boolean;
  resetPositions?: boolean;
};
type GraphIssueItem = {
  className: string;
  label: string;
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
    resetLayoutButton: getElementByID("graph-reset-layout-button", HTMLButtonElement),
    labelModeInputs: queryAllElements(document, 'input[name="graph-label-mode"]', HTMLInputElement),
  };
  if (!dom.canvas) {
    return;
  }
  const canvas = dom.canvas;

  const state: GraphState = {
    cy: null,
    activeLayout: null,
    currentQuery: "",
    currentGraphElements: emptyGraphElements(),
    hoveredNodeID: "",
    selectedNodeID: "",
    labelMode: readLabelMode(),
    lastMeaningfulViewport: null,
    layoutRunID: 0,
  };
  let debounceToken = 0;
  let requestToken = 0;
  let selectionSyncing = false;

  function setStatus(message: string, kind: StatusKind = ""): void {
    setStatusText(dom.status, message, kind, ["is-loading", "is-empty", "is-error"]);
  }

  function setOpenNoteTarget(nodeID: string): void {
    if (!(dom.openNote instanceof HTMLAnchorElement)) {
      return;
    }

    const path = notePathForKey(nodeID);
    if (!path) {
      dom.openNote.removeAttribute("href");
      dom.openNote.classList.add("is-disabled");
      dom.openNote.setAttribute("aria-disabled", "true");
      dom.openNote.setAttribute("tabindex", "-1");
      return;
    }

    dom.openNote.setAttribute("href", path);
    dom.openNote.classList.remove("is-disabled");
    dom.openNote.setAttribute("aria-disabled", "false");
    dom.openNote.setAttribute("tabindex", "0");
  }

  function formatDegreeDetails(data: GraphNodeModel | null | undefined): string {
    if (!data) {
      return "";
    }
    const inDegree = readDegreeValue(data.inDegree, 0);
    const outDegree = readDegreeValue(data.outDegree, 0);
    const degree = readDegreeValue(data.degree, inDegree + outDegree);
    const filteredInDegree = readDegreeValue(data.filteredInDegree, inDegree);
    const filteredOutDegree = readDegreeValue(data.filteredOutDegree, outDegree);
    const filteredDegree = readDegreeValue(data.filteredDegree, filteredInDegree + filteredOutDegree);
    const visibleDetail = `Visible degree: ${filteredDegree} (${filteredInDegree} in, ${filteredOutDegree} out)`;
    if (filteredDegree === degree && filteredInDegree === inDegree && filteredOutDegree === outDegree) {
      return visibleDetail;
    }
    return `${visibleDetail}. Full graph: ${degree} (${inDegree} in, ${outDegree} out)`;
  }

  function issueItemsForNode(data: GraphNodeModel | null | undefined): GraphIssueItem[] {
    if (!data) {
      return [];
    }
    const items: GraphIssueItem[] = [];
    const brokenCount = readDegreeValue(data.brokenCount, 0);
    if (data.hasBroken || brokenCount > 0) {
      items.push({
        className: "is-broken",
        label:
          brokenCount > 0
            ? brokenCount === 1
              ? "1 broken link"
              : `${brokenCount} broken links`
            : "Broken links",
      });
    }
    if (data.isOrphan) {
      items.push({
        className: "is-orphan",
        label: "Orphan",
      });
    }
    if (data.isDeadEnd) {
      items.push({
        className: "is-dead-end",
        label: "Dead end",
      });
    }
    return items;
  }

  function renderIssueDetails(model: GraphNodeModel | null | undefined): void {
    if (!dom.hoverIssues) {
      return;
    }
    dom.hoverIssues.textContent = "";
    const items = issueItemsForNode(model);
    dom.hoverIssues.hidden = items.length === 0;
    for (const item of items) {
      const issue = document.createElement("li");
      issue.className = `graph-issue-item ${item.className}`;
      issue.textContent = item.label;
      dom.hoverIssues.appendChild(issue);
    }
  }

  function renderDetailsPanel(fallbackHoveredModel: GraphNodeModel | null = null): void {
    const selectedModel = findNodeModel(state.selectedNodeID);
    const hoveredModel = selectedModel
      ? null
      : findNodeModel(state.hoveredNodeID) || fallbackHoveredModel || null;
    const model = selectedModel || hoveredModel;

    if (dom.hoverTitle) {
      dom.hoverTitle.textContent = model ? model.title || model.id : "Hover a node";
    }
    if (dom.hoverFolder) {
      dom.hoverFolder.textContent = model ? `Folder: ${model.folder || "/"}` : "";
    }
    if (dom.hoverDegree) {
      dom.hoverDegree.textContent = model ? formatDegreeDetails(model) : "";
    }
    renderIssueDetails(model);
    if (dom.detailsPanel) {
      dom.detailsPanel.classList.toggle("is-locked", Boolean(selectedModel));
      dom.detailsPanel.classList.toggle("is-transient", !selectedModel && Boolean(hoveredModel));
    }
    setOpenNoteTarget(selectedModel ? selectedModel.id : "");
  }

  function readSpread(): number {
    if (!(dom.spreadInput instanceof HTMLInputElement)) {
      return 2;
    }
    const parsed = Number.parseFloat(dom.spreadInput.value);
    if (!Number.isFinite(parsed) || parsed <= 0) {
      return 2;
    }
    return parsed;
  }

  function clampedSpread(): number {
    return Math.min(6, Math.max(0.8, readSpread()));
  }

  function updateSpreadLabel(): void {
    if (!dom.spreadValue) {
      return;
    }
    const spread = readSpread();
    dom.spreadValue.textContent = `${spread.toFixed(1)}x`;
  }

  function readLabelMode(): GraphLabelMode {
    for (const input of dom.labelModeInputs) {
      if (input instanceof HTMLInputElement && input.checked) {
        return input.value === "all" ? "all" : "auto";
      }
    }
    return "auto";
  }

  function setLabelMode(nextMode: string): void {
    state.labelMode = nextMode === "all" ? "all" : "auto";
    applyGraphVisualState();
  }

  const labelPolicy = {
    smallGraphNodeLimit: 24,
    selectedNeighborLabelLimit: 10,
    zoomLabelMinZoom: 1.05,
    zoomVisibleLabelLimit: 22,
    zoomPriorityLabelLimit: 10,
    deepZoomMinZoom: 1.6,
    deepZoomPriorityLabelLimit: 24,
  };

  const graphStyle = [
    {
      selector: "node",
      style: {
        label: "",
        "background-color": "data(color)",
        color: "#e5e9f0",
        "font-size": "10px",
        "font-weight": 600,
        "text-valign": "bottom",
        "text-halign": "center",
        "text-margin-y": 6,
        "text-wrap": "wrap",
        "text-overflow-wrap": "anywhere",
        "text-max-width": 130,
        "text-outline-width": 1,
        "text-outline-color": "#0b0f16",
        "text-background-color": "#0c1018",
        "text-background-opacity": 0.78,
        "text-background-padding": 2,
        "text-background-shape": "roundrectangle",
        width: "data(size)",
        height: "data(size)",
        "border-width": 0,
        opacity: 1,
        "z-index": 1,
      },
    },
    {
      selector: "node.is-labeled",
      style: {
        label: "data(title)",
      },
    },
    {
      selector: "node.has-broken",
      style: {
        "border-width": 3,
        "border-color": "#ff9d9d",
        shape: "diamond",
      },
    },
    {
      selector: "node.is-orphan",
      style: {
        "border-width": 2,
        "border-style": "dashed",
        "border-color": "#c7d2e7",
      },
    },
    {
      selector: "node.is-dead-end",
      style: {
        "background-blacken": -0.12,
      },
    },
    {
      selector: "node.is-neighbor",
      style: {
        "border-width": 2,
        "border-color": "#8cb8ff",
        "z-index": 8,
      },
    },
    {
      selector: "node.is-hovered",
      style: {
        "border-width": 3,
        "border-color": "#f3f7ff",
        "font-size": "11px",
        "z-index": 20,
      },
    },
    {
      selector: "node.is-selected",
      style: {
        "border-width": 4,
        "border-color": "#ffd166",
        color: "#fff3c2",
        "font-size": "11px",
        "z-index": 30,
      },
    },
    {
      selector: "node.is-dimmed",
      style: {
        opacity: 0.22,
      },
    },
    {
      selector: "edge",
      style: {
        width: 1.5,
        "line-color": "#66718a",
        "target-arrow-color": "#66718a",
        "target-arrow-shape": "triangle",
        "curve-style": "bezier",
        opacity: 0.72,
        "z-index": 0,
      },
    },
    {
      selector: "edge.is-highlighted-edge",
      style: {
        width: 3,
        "line-color": "#8cb8ff",
        "target-arrow-color": "#8cb8ff",
        opacity: 1,
        "z-index": 12,
      },
    },
    {
      selector: "edge.is-dimmed",
      style: {
        opacity: 0.12,
      },
    },
  ] as unknown as cytoscape.StylesheetJson;

  function stopActiveLayout(): void {
    if (state.activeLayout && typeof state.activeLayout.stop === "function") {
      state.activeLayout.stop();
    }
    state.activeLayout = null;
  }

  function layoutOptionsForSpread(): cytoscape.CoseLayoutOptions {
    const spread = clampedSpread();
    return {
      name: "cose",
      animate: false,
      fit: false,
      randomize: false,
      padding: 72,
      idealEdgeLength: Math.round(70 + 55 * spread),
      nodeRepulsion: Math.round(7000 * spread * spread),
      componentSpacing: Math.round(100 + 85 * spread),
      nodeOverlap: Math.round(12 + 16 * spread),
      gravity: Math.max(0.05, 0.62 - 0.09 * spread),
      numIter: Math.round(900 + 120 * spread),
      initialTemp: Math.round(180 + 45 * spread),
      coolingFactor: 0.95,
      minTemp: 1,
    };
  }

  function currentViewport(): GraphViewportSnapshot | null {
    if (!state.cy) {
      return null;
    }
    const pan = state.cy.pan();
    return {
      pan: {
        x: pan.x,
        y: pan.y,
      },
      zoom: state.cy.zoom(),
    };
  }

  function applyViewport(viewport: GraphViewportSnapshot | null): void {
    if (!state.cy || !viewport) {
      return;
    }
    state.cy.viewport({
      pan: {
        x: viewport.pan.x,
        y: viewport.pan.y,
      },
      zoom: viewport.zoom,
    });
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
    state.lastMeaningfulViewport = currentViewport();
  }

  function ensureGraph(): cytoscape.Core {
    if (state.cy) {
      return state.cy;
    }
    const createCytoscape = window.cytoscape as CytoscapeFactory;
    state.cy = createCytoscape({
      container: canvas,
      elements: [],
      style: graphStyle,
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

  function cyNodeByID(nodeID: string): cytoscape.NodeSingular | null {
    if (!state.cy || !nodeID) {
      return null;
    }
    const node = state.cy.getElementById(nodeID);
    if (!node || node.length === 0 || !node.isNode()) {
      return null;
    }
    return node;
  }

  function nodeDegree(node: cytoscape.NodeSingular | null): number {
    if (!node) {
      return 0;
    }
    const filteredDegree = Number(node.data("filteredDegree"));
    if (Number.isFinite(filteredDegree) && filteredDegree > 0) {
      return filteredDegree;
    }
    const degree = Number(node.data("degree"));
    return Number.isFinite(degree) && degree > 0 ? degree : 0;
  }

  function sortedNodesByImportance(nodes: cytoscape.NodeSingular[]): cytoscape.NodeSingular[] {
    return nodes
      .slice()
      .sort((left, right) => nodeDegree(right) - nodeDegree(left) || left.id().localeCompare(right.id()));
  }

  function connectedNodeIDs(node: cytoscape.NodeSingular | null): Set<string> {
    const ids = new Set<string>();
    if (!node) {
      return ids;
    }
    node.connectedEdges().forEach((edge) => {
      edge.connectedNodes().forEach((connectedNode) => {
        const id = connectedNode.id();
        if (id !== node.id()) {
          ids.add(id);
        }
      });
    });
    return ids;
  }

  function visibleNodesForViewport(): cytoscape.NodeSingular[] {
    if (!state.cy) {
      return [];
    }
    const zoom = state.cy.zoom();
    const padding = 72 / Math.max(zoom, 0.1);
    const extent = state.cy.extent();
    return state.cy
      .nodes()
      .filter((node) => {
        const position = node.position();
        return (
          position.x >= extent.x1 - padding &&
          position.x <= extent.x2 + padding &&
          position.y >= extent.y1 - padding &&
          position.y <= extent.y2 + padding
        );
      })
      .toArray() as cytoscape.NodeSingular[];
  }

  function addZoomLabels(labelIDs: Set<string>): void {
    if (!state.cy) {
      return;
    }
    const zoom = state.cy.zoom();
    if (zoom < labelPolicy.zoomLabelMinZoom) {
      return;
    }

    const visibleNodes = visibleNodesForViewport();
    if (visibleNodes.length <= labelPolicy.zoomVisibleLabelLimit) {
      for (const node of visibleNodes) {
        labelIDs.add(node.id());
      }
      return;
    }

    const priorityLimit =
      zoom >= labelPolicy.deepZoomMinZoom
        ? labelPolicy.deepZoomPriorityLabelLimit
        : labelPolicy.zoomPriorityLabelLimit;
    for (const node of sortedNodesByImportance(visibleNodes).slice(0, priorityLimit)) {
      labelIDs.add(node.id());
    }
  }

  function labelIDsForCurrentState(): Set<string> {
    const labelIDs = new Set<string>();
    if (!state.cy) {
      return labelIDs;
    }

    if (state.labelMode === "all" || state.cy.nodes().length <= labelPolicy.smallGraphNodeLimit) {
      state.cy.nodes().forEach((node) => {
        labelIDs.add(node.id());
      });
    } else {
      addZoomLabels(labelIDs);
    }

    const selectedNode = cyNodeByID(state.selectedNodeID);
    const hoveredNode = cyNodeByID(state.hoveredNodeID);
    if (selectedNode) {
      labelIDs.add(selectedNode.id());
      const selectedNeighborIDs = connectedNodeIDs(selectedNode);
      if (selectedNeighborIDs.size <= labelPolicy.selectedNeighborLabelLimit) {
        for (const id of selectedNeighborIDs) {
          labelIDs.add(id);
        }
      }
    }
    if (hoveredNode) {
      labelIDs.add(hoveredNode.id());
    }

    return labelIDs;
  }

  function visualFocusState(): GraphFocusState {
    const selectedNode = cyNodeByID(state.selectedNodeID);
    const hoveredNode = cyNodeByID(state.hoveredNodeID);
    const focusNode = selectedNode || hoveredNode;
    const neighborIDs = connectedNodeIDs(focusNode);
    const highlightedEdgeIDs = new Set<string>();
    if (focusNode) {
      focusNode.connectedEdges().forEach((edge) => {
        highlightedEdgeIDs.add(edge.id());
      });
    }
    return {
      focusNodeID: focusNode ? focusNode.id() : "",
      selectedID: selectedNode ? selectedNode.id() : "",
      hoveredID: hoveredNode ? hoveredNode.id() : "",
      neighborIDs,
      highlightedEdgeIDs,
    };
  }

  function applyGraphVisualState() {
    if (!state.cy) {
      return;
    }
    const labelIDs = labelIDsForCurrentState();
    const focus = visualFocusState();
    const cy = state.cy;
    cy.batch(() => {
      cy.nodes().forEach((node) => {
        const id = node.id();
        const isSelected = id === focus.selectedID;
        const isHovered = id === focus.hoveredID;
        const isNeighbor = focus.neighborIDs.has(id);
        const isFocusedNode = focus.focusNodeID !== "" && id === focus.focusNodeID;
        const isDimmed = focus.focusNodeID !== "" && !isFocusedNode && !isHovered && !isNeighbor;
        node.toggleClass("is-labeled", labelIDs.has(id));
        node.toggleClass("is-selected", isSelected);
        node.toggleClass("is-hovered", isHovered);
        node.toggleClass("is-neighbor", isNeighbor);
        node.toggleClass("is-dimmed", isDimmed);
      });

      cy.edges().forEach((edge) => {
        const isHighlighted = focus.highlightedEdgeIDs.has(edge.id());
        edge.toggleClass("is-highlighted-edge", isHighlighted);
        edge.toggleClass("is-dimmed", focus.focusNodeID !== "" && !isHighlighted);
      });
    });
  }

  function seedDeterministicPositions(): void {
    if (!state.cy) {
      return;
    }
    const nodes = state.cy
      .nodes()
      .toArray()
      .sort((left, right) => left.id().localeCompare(right.id())) as cytoscape.NodeSingular[];
    const spread = clampedSpread();
    const radialGap = 52 + 26 * spread;
    const goldenAngle = Math.PI * (3 - Math.sqrt(5));

    nodes.forEach((node, index) => {
      const radius = radialGap * Math.sqrt(index);
      const angle = index * goldenAngle;
      node.position({
        x: Math.cos(angle) * radius,
        y: Math.sin(angle) * radius,
      });
    });
  }

  function separateIsolatedNodes(): void {
    if (!state.cy) {
      return;
    }
    const isolatedNodes = state.cy
      .nodes()
      .filter((node) => node.connectedEdges().length === 0)
      .toArray()
      .sort((left, right) => left.id().localeCompare(right.id())) as cytoscape.NodeSingular[];
    if (isolatedNodes.length === 0) {
      return;
    }

    const spread = clampedSpread();
    const spacing = 58 + 34 * spread;
    const columns = Math.ceil(Math.sqrt(isolatedNodes.length));
    const rows = Math.ceil(isolatedNodes.length / columns);
    const connectedNodes = state.cy.nodes().filter((node) => node.connectedEdges().length > 0);
    let startX = -((columns - 1) * spacing) / 2;
    let startY = -((rows - 1) * spacing) / 2;

    if (connectedNodes.length > 0) {
      const box = connectedNodes.boundingBox();
      startX = box.x2 + spacing * 1.6;
      startY = box.y1 + (box.h - (rows - 1) * spacing) / 2;
    }

    isolatedNodes.forEach((node, index) => {
      const column = index % columns;
      const row = Math.floor(index / columns);
      node.position({
        x: startX + column * spacing,
        y: startY + row * spacing,
      });
    });
  }

  function runLayout(options: GraphLayoutRunOptions = {}): void {
    if (!state.cy || state.cy.nodes().length === 0) {
      return;
    }
    const layoutOptions = options;
    const fitAfterLayout = Boolean(layoutOptions.fitAfterLayout);
    const preservedViewport = layoutOptions.preserveViewport ? currentViewport() : null;
    const runID = state.layoutRunID + 1;
    state.layoutRunID = runID;
    stopActiveLayout();

    if (layoutOptions.resetPositions) {
      seedDeterministicPositions();
    }

    let finalized = false;
    const finalizeLayout = () => {
      if (finalized || runID !== state.layoutRunID) {
        return;
      }
      finalized = true;
      separateIsolatedNodes();
      if (fitAfterLayout) {
        fitCurrentGraph();
      } else if (preservedViewport) {
        applyViewport(preservedViewport);
        rememberMeaningfulViewport();
      } else {
        rememberMeaningfulViewport();
      }
      applyGraphVisualState();
      state.activeLayout = null;
    };

    const layout = state.cy.layout(layoutOptionsForSpread());
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

    updateSpreadLabel();
    if (dom.spreadInput instanceof HTMLInputElement) {
      dom.spreadInput.addEventListener("input", () => {
        updateSpreadLabel();
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

  bindControls();
  scheduleLoad(initialQuery, 0);
};
