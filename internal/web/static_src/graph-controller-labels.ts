import type { GraphFocusState, GraphLabelMode, GraphSearchMode } from "./graph";

export const labelPolicy = {
  smallGraphNodeLimit: 24,
  searchMatchLabelLimit: 30,
  selectedNeighborLabelLimit: 10,
  zoomLabelMinZoom: 1.05,
  zoomVisibleLabelLimit: 22,
  zoomPriorityLabelLimit: 10,
  deepZoomMinZoom: 1.6,
  deepZoomPriorityLabelLimit: 24,
};

export const readLabelMode = (inputs: readonly HTMLInputElement[]): GraphLabelMode => {
  for (const input of inputs) {
    if (input.checked) {
      return input.value === "all" ? "all" : "auto";
    }
  }
  return "auto";
};

export const normalizeSearchMode = (value: unknown): GraphSearchMode =>
  String(value || "")
    .trim()
    .toLowerCase() === "focus"
    ? "focus"
    : "filter";

export const cyNodeByID = (
  cy: cytoscape.Core | null,
  nodeID: string
): cytoscape.NodeSingular | null => {
  if (!cy || !nodeID) {
    return null;
  }
  const node = cy.getElementById(nodeID);
  if (!node || node.length === 0 || !node.isNode()) {
    return null;
  }
  return node;
};

export const nodeDegree = (node: cytoscape.NodeSingular | null): number => {
  if (!node) {
    return 0;
  }
  const filteredDegree = Number(node.data("filteredDegree"));
  if (Number.isFinite(filteredDegree) && filteredDegree > 0) {
    return filteredDegree;
  }
  const degree = Number(node.data("degree"));
  return Number.isFinite(degree) && degree > 0 ? degree : 0;
};

export const sortedNodesByImportance = (
  nodes: cytoscape.NodeSingular[]
): cytoscape.NodeSingular[] =>
  nodes
    .slice()
    .sort((left, right) => nodeDegree(right) - nodeDegree(left) || left.id().localeCompare(right.id()));

export const connectedNodeIDs = (node: cytoscape.NodeSingular | null): Set<string> => {
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
};

export const visibleNodesForViewport = (cy: cytoscape.Core | null): cytoscape.NodeSingular[] => {
  if (!cy) {
    return [];
  }
  const zoom = cy.zoom();
  const padding = 72 / Math.max(zoom, 0.1);
  const extent = cy.extent();
  return cy
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
};

export const addZoomLabels = (cy: cytoscape.Core | null, labelIDs: Set<string>): void => {
  if (!cy) {
    return;
  }
  const zoom = cy.zoom();
  if (zoom < labelPolicy.zoomLabelMinZoom) {
    return;
  }

  const visibleNodes = visibleNodesForViewport(cy);
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
};

export const labelIDsForCurrentState = (
  cy: cytoscape.Core | null,
  labelMode: GraphLabelMode,
  selectedNodeID: string,
  hoveredNodeID: string,
  matchIDs: ReadonlySet<string> = new Set<string>()
): Set<string> => {
  const labelIDs = new Set<string>();
  if (!cy) {
    return labelIDs;
  }

  if (labelMode === "all" || cy.nodes().length <= labelPolicy.smallGraphNodeLimit) {
    cy.nodes().forEach((node) => {
      labelIDs.add(node.id());
    });
  } else {
    addZoomLabels(cy, labelIDs);
  }

  const selectedNode = cyNodeByID(cy, selectedNodeID);
  const hoveredNode = cyNodeByID(cy, hoveredNodeID);
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

  // Matched nodes are what you searched for, so they always get a label — but a
  // broad query would otherwise carpet the canvas in text, so cap by degree.
  if (matchIDs.size > 0) {
    const matchNodes = cy
      .nodes()
      .filter((node) => matchIDs.has(node.id()))
      .toArray() as cytoscape.NodeSingular[];
    const labeledMatches =
      matchNodes.length <= labelPolicy.searchMatchLabelLimit
        ? matchNodes
        : sortedNodesByImportance(matchNodes).slice(0, labelPolicy.searchMatchLabelLimit);
    for (const node of labeledMatches) {
      labelIDs.add(node.id());
    }
  }

  return labelIDs;
};

export const visualFocusState = (
  cy: cytoscape.Core | null,
  selectedNodeID: string,
  hoveredNodeID: string,
  matchIDs: ReadonlySet<string> = new Set<string>()
): GraphFocusState => {
  const selectedNode = cyNodeByID(cy, selectedNodeID);
  const hoveredNode = cyNodeByID(cy, hoveredNodeID);
  const focusNode = selectedNode || hoveredNode;
  const neighborIDs = connectedNodeIDs(focusNode);
  const highlightedEdgeIDs = new Set<string>();
  if (focusNode) {
    focusNode.connectedEdges().forEach((edge) => {
      highlightedEdgeIDs.add(edge.id());
    });
  }

  // Search matches are a second, independent focus source. They contribute the
  // same treatment as a click focus: the match, its 1-hop neighbors, and every
  // edge touching it stay bright.
  const resolvedMatchIDs = new Set<string>();
  const matchNeighborIDs = new Set<string>();
  if (matchIDs.size > 0) {
    for (const id of matchIDs) {
      const matchNode = cyNodeByID(cy, id);
      if (!matchNode) {
        continue;
      }
      resolvedMatchIDs.add(matchNode.id());
      for (const neighborID of connectedNodeIDs(matchNode)) {
        matchNeighborIDs.add(neighborID);
      }
      matchNode.connectedEdges().forEach((edge) => {
        highlightedEdgeIDs.add(edge.id());
      });
    }
  }

  return {
    focusNodeID: focusNode ? focusNode.id() : "",
    selectedID: selectedNode ? selectedNode.id() : "",
    hoveredID: hoveredNode ? hoveredNode.id() : "",
    neighborIDs,
    highlightedEdgeIDs,
    matchIDs: resolvedMatchIDs,
    matchNeighborIDs,
  };
};

// Toggles the label/focus/dim classes for the current selection and hover.
// Purely derived from the passed-in instance and IDs; no state is mutated.
export const applyGraphVisualClasses = (
  cy: cytoscape.Core | null,
  labelMode: GraphLabelMode,
  selectedNodeID: string,
  hoveredNodeID: string,
  matchIDs: ReadonlySet<string> = new Set<string>()
): void => {
  if (!cy) {
    return;
  }
  const labelIDs = labelIDsForCurrentState(cy, labelMode, selectedNodeID, hoveredNodeID, matchIDs);
  const focus = visualFocusState(cy, selectedNodeID, hoveredNodeID, matchIDs);
  // Click/hover focus and search focus simply union: clicking a node while a
  // search is active keeps both bright.
  const hasFocusSource = focus.focusNodeID !== "" || focus.matchIDs.size > 0;
  cy.batch(() => {
    cy.nodes().forEach((node) => {
      const id = node.id();
      const isSelected = id === focus.selectedID;
      const isHovered = id === focus.hoveredID;
      const isNeighbor = focus.neighborIDs.has(id);
      const isMatch = focus.matchIDs.has(id);
      const isMatchNeighbor = focus.matchNeighborIDs.has(id);
      const isFocusedNode = focus.focusNodeID !== "" && id === focus.focusNodeID;
      const isDimmed =
        hasFocusSource &&
        !isFocusedNode &&
        !isHovered &&
        !isNeighbor &&
        !isMatch &&
        !isMatchNeighbor;
      node.toggleClass("is-labeled", labelIDs.has(id));
      node.toggleClass("is-selected", isSelected);
      node.toggleClass("is-hovered", isHovered);
      node.toggleClass("is-neighbor", isNeighbor);
      node.toggleClass("is-search-match", isMatch);
      node.toggleClass("is-dimmed", isDimmed);
    });

    cy.edges().forEach((edge) => {
      const isHighlighted = focus.highlightedEdgeIDs.has(edge.id());
      edge.toggleClass("is-highlighted-edge", isHighlighted);
      edge.toggleClass("is-dimmed", hasFocusSource && !isHighlighted);
    });
  });
};
