import {
  clearSelectedNode,
  selectNode,
  setHoveredNode,
  syncCytoscapeSelection,
} from "./graph-controller-interaction";
import {
  captureNodePositions,
  clampedSpread,
  registerGraphLayouts,
  seedDeterministicPositions,
} from "./graph-controller-layout";
import {
  type GraphRuntime,
  findDisplayedNodeModel,
  isDisplayedNodeID,
} from "./graph-controller-runtime";
import { buildGraphStyle } from "./graph-controller-style";
import { type CytoscapeFactory, type GraphLayoutRunOptions } from "./graph-controller-types";
import {
  applyGraphVisualState,
  rememberMeaningfulViewport,
  runLayout,
  stopActiveLayout,
} from "./graph-controller-visual";
import { type GraphElements, toCytoscapeElements } from "./graph";

/**
 * Node events are only honoured for elements the display pipeline kept.
 *
 * Cytoscape can deliver a tap, select, mouseover or mouseout for an element a
 * remount already removed — a queued gesture, or a pointer that was over an
 * excluded node when the rebuild ran. Such an event names a node the user can
 * no longer see, so it must not move selection, details, the display controls
 * or the isolation state.
 */
export const bindGraphEvents = (runtime: GraphRuntime, instance: cytoscape.Core): void => {
  instance.on("tap", "node", (event: cytoscape.EventObjectNode) => {
    const nodeID = event.target.id();
    const model = findDisplayedNodeModel(runtime, nodeID);
    if (!model) {
      return;
    }
    if (model.isAggregate) {
      // A collapsed folder is a drill-down affordance, not a note: tapping it
      // expands the group back out rather than selecting a synthetic node.
      runtime.toggleFolderExpansion(model.folder);
      return;
    }
    selectNode(runtime, nodeID, { toggleNeighborLabels: true });
  });

  instance.on("tap", (event: cytoscape.EventObjectCore) => {
    if (event.target === instance) {
      clearSelectedNode(runtime);
    }
  });

  instance.on("mouseover", "node", (event: cytoscape.EventObjectNode) => {
    const nodeID = event.target.id();
    if (!isDisplayedNodeID(runtime, nodeID)) {
      return;
    }
    const data = event.target.data();
    setHoveredNode(runtime, nodeID, data);
  });
  instance.on("mouseout", "node", (event: cytoscape.EventObjectNode) => {
    const nodeID = event.target.id();
    if (!isDisplayedNodeID(runtime, nodeID)) {
      return;
    }
    // Only the node that currently owns the hover may end it. A mouseout is
    // delivered after the fact, so one that left node A can arrive once the
    // pointer is already over node B; clearing on it would blank details and
    // hover styling that belong to a node the user is still pointing at. A
    // hover nobody owns is already ended — a remount clears it, canvas class
    // included — so there is nothing left for a late event to take off.
    if (runtime.state.hoveredNodeID !== nodeID) {
      return;
    }
    setHoveredNode(runtime, "", null);
  });
  instance.on("select", "node", (event: cytoscape.EventObjectNode) => {
    if (!runtime.selectionSyncing) {
      selectNode(runtime, event.target.id(), { fromCytoscape: true });
    }
  });
  instance.on("unselect", "node", (event: cytoscape.EventObjectNode) => {
    const nodeID = event.target.id();
    if (
      !runtime.selectionSyncing &&
      isDisplayedNodeID(runtime, nodeID) &&
      nodeID === runtime.state.selectedNodeID
    ) {
      clearSelectedNode(runtime, { fromCytoscape: true });
    }
  });
  instance.on("viewport", () => {
    rememberMeaningfulViewport(runtime);
    applyGraphVisualState(runtime);
  });
};

export const ensureGraph = (runtime: GraphRuntime): cytoscape.Core => {
  if (runtime.state.cy) {
    return runtime.state.cy;
  }
  registerGraphLayouts();
  const createCytoscape = window.cytoscape as CytoscapeFactory;
  runtime.state.cy = createCytoscape({
    container: runtime.canvas,
    elements: [],
    style: buildGraphStyle(),
  });
  bindGraphEvents(runtime, runtime.state.cy);
  return runtime.state.cy;
};

export const applyGraphElements = (
  runtime: GraphRuntime,
  graphElements: GraphElements,
  layoutOptions: GraphLayoutRunOptions
): void => {
  const { dom, state } = runtime;
  const instance = ensureGraph(runtime);
  stopActiveLayout(runtime);
  const selectedNodeID = state.selectedNodeID;
  // Remounting throws every position away. A display-knob rebuild asks for
  // `resetPositions: false`, which here means "carry the surviving nodes'
  // positions across", so isolating does not shuffle the neighborhood the user
  // was already looking at.
  const rememberedPositions = layoutOptions.resetPositions ? null : captureNodePositions(instance);

  runtime.selectionSyncing = true;
  try {
    instance.elements().remove();
    if (graphElements.nodes.length > 0 || graphElements.edges.length > 0) {
      instance.add(toCytoscapeElements(graphElements));
    }
  } finally {
    runtime.selectionSyncing = false;
  }

  state.selectedNodeID = graphElements.nodes.some((node) => node.id === selectedNodeID) ? selectedNodeID : "";
  if (!state.selectedNodeID) {
    state.showSelectedNeighborLabels = false;
  }
  syncCytoscapeSelection(runtime, state.selectedNodeID);
  setHoveredNode(runtime, "", null);

  if (instance.nodes().length === 0) {
    state.lastMeaningfulViewport = null;
    applyGraphVisualState(runtime);
    return;
  }

  if (rememberedPositions) {
    seedDeterministicPositions(instance, clampedSpread(dom.spreadInput), rememberedPositions);
  }
  runLayout(runtime, layoutOptions);
};
