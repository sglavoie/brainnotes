import { renderGraphDetails } from "./graph-controller-details";
import {
  type GraphRuntime,
  findDisplayedNodeModel,
  isDisplayedNodeID,
} from "./graph-controller-runtime";
import { type GraphSelectionOptions } from "./graph-controller-types";
import { applyGraphVisualState, updateDisplayControls } from "./graph-controller-visual";
import { type GraphNodeModel, graphNodeModelFromData } from "./graph";

export const renderDetailsPanel = (
  runtime: GraphRuntime,
  fallbackHoveredModel: GraphNodeModel | null = null
): void => {
  const selectedModel = findDisplayedNodeModel(runtime, runtime.state.selectedNodeID);
  const hoveredModel = selectedModel
    ? null
    : findDisplayedNodeModel(runtime, runtime.state.hoveredNodeID) || fallbackHoveredModel || null;
  renderGraphDetails(runtime.detailsRefs, selectedModel, hoveredModel);
};

export const syncCytoscapeSelection = (runtime: GraphRuntime, nodeID: string): void => {
  if (!runtime.state.cy || runtime.selectionSyncing) {
    return;
  }

  runtime.selectionSyncing = true;
  try {
    runtime.state.cy.nodes().forEach((node) => {
      const shouldSelect = node.id() === nodeID;
      if (shouldSelect && !node.selected()) {
        node.select();
      } else if (!shouldSelect && node.selected()) {
        node.unselect();
      }
    });
  } finally {
    runtime.selectionSyncing = false;
  }
};

export const selectNode = (
  runtime: GraphRuntime,
  nodeID: string,
  options: GraphSelectionOptions = {}
): void => {
  const model = findDisplayedNodeModel(runtime, nodeID);
  if (!model) {
    // Nothing mounted answers to this id, so there is no selection to make and
    // no reason to disturb the one already in place.
    return;
  }
  // An aggregate is never a selection: it stands for a group, and every
  // selection-driven affordance (Open note, Isolate) means one note.
  const selectedNodeID = model.isAggregate ? "" : model.id;
  if (!selectedNodeID || selectedNodeID !== runtime.state.selectedNodeID) {
    runtime.state.showSelectedNeighborLabels = false;
  } else if (options.toggleNeighborLabels) {
    runtime.state.showSelectedNeighborLabels = !runtime.state.showSelectedNeighborLabels;
  }
  runtime.state.selectedNodeID = selectedNodeID;
  if (!options || !options.fromCytoscape) {
    syncCytoscapeSelection(runtime, runtime.state.selectedNodeID);
  }
  renderDetailsPanel(runtime);
  applyGraphVisualState(runtime);
  updateDisplayControls(runtime);
};

export const clearSelectedNode = (
  runtime: GraphRuntime,
  options: GraphSelectionOptions = {}
): void => {
  runtime.state.selectedNodeID = "";
  runtime.state.showSelectedNeighborLabels = false;
  if (!options || !options.fromCytoscape) {
    syncCytoscapeSelection(runtime, "");
  }
  renderDetailsPanel(runtime);
  applyGraphVisualState(runtime);
  updateDisplayControls(runtime);
};

/**
 * The hovered id and the canvas hover cursor are one piece of state, so the
 * class is written here beside the id it describes rather than at each call
 * site. That is what keeps a remount honest: `applyGraphElements` clears the
 * hover for a node that may not have survived the rebuild, and that node's
 * late mouseout is refused by the displayed-set guard, so nothing else would
 * ever take the class back off.
 */
export const setHoveredNode = (
  runtime: GraphRuntime,
  nodeID: string,
  fallbackData: unknown
): void => {
  runtime.state.hoveredNodeID = nodeID || "";
  runtime.canvas.classList.toggle("is-node-hovered", runtime.state.hoveredNodeID !== "");
  if (!runtime.state.hoveredNodeID) {
    renderDetailsPanel(runtime);
    applyGraphVisualState(runtime);
    return;
  }

  const fallbackModel = fallbackData
    ? graphNodeModelFromData(fallbackData, runtime.state.hoveredNodeID)
    : null;
  renderDetailsPanel(runtime, fallbackModel);
  applyGraphVisualState(runtime);
};

export const keyboardTargetNodeID = (runtime: GraphRuntime): string => {
  if (isDisplayedNodeID(runtime, runtime.state.hoveredNodeID)) {
    return runtime.state.hoveredNodeID;
  }
  if (isDisplayedNodeID(runtime, runtime.state.selectedNodeID)) {
    return runtime.state.selectedNodeID;
  }
  if (!runtime.state.cy) {
    return "";
  }

  const selectedNode = runtime.state.cy
    .nodes()
    .filter((node) => node.selected())
    .first();
  if (!selectedNode || selectedNode.length === 0) {
    return "";
  }
  // Cytoscape's own selection is the last resort, and it is checked against the
  // displayed set like every other id: a keyboard activation must not reach an
  // element the pipeline excluded.
  const nodeID = selectedNode.id();
  return isDisplayedNodeID(runtime, nodeID) ? nodeID : "";
};

export const handleGraphKeyDown = (runtime: GraphRuntime, event: KeyboardEvent): void => {
  if (event.key !== "Enter" && event.key !== " " && event.key !== "Spacebar") {
    return;
  }

  const nodeID = keyboardTargetNodeID(runtime);
  if (!nodeID) {
    return;
  }

  event.preventDefault();
  selectNode(runtime, nodeID, { toggleNeighborLabels: true });
};
