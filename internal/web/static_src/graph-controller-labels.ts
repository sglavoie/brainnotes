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

const nodesForIDs = (cy: cytoscape.Core, ids: ReadonlySet<string>): cytoscape.NodeSingular[] =>
  Array.from(ids, (id) => cyNodeByID(cy, id))
    .filter((node): node is cytoscape.NodeSingular => node !== null);

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

// The label-box estimate is derived from the base `node` style rule in
// graph-style.ts: a 10px font, wrapped at `text-max-width: 130`, drawn below
// the node (`text-valign: bottom`) offset by `text-margin-y: 6`, with 2px of
// text background padding around it.
export const labelBoxGeometry = {
  fontSizePX: 10,
  maxWidthPX: 130,
  marginYPX: 6,
  backgroundPaddingPX: 2,
  lineHeightRatio: 1.3,
  averageCharWidthRatio: 0.58,
};

export type LabelBox = { x1: number; y1: number; x2: number; y2: number };

// Estimates the screen-space box a node's wrapped label occupies. Pure
// geometry: it takes the numbers a renderer would hand it, so it is testable
// without one.
export const estimateLabelBox = (
  center: { x: number; y: number },
  renderedNodeWidth: number,
  title: string,
  zoom: number
): LabelBox | null => {
  if (!Number.isFinite(center.x) || !Number.isFinite(center.y)) {
    return null;
  }
  const scale = Math.max(Number.isFinite(zoom) ? zoom : 1, 0.01);
  const nodeWidth = Number.isFinite(renderedNodeWidth) ? Math.max(renderedNodeWidth, 0) : 0;
  const charWidthPX = labelBoxGeometry.fontSizePX * labelBoxGeometry.averageCharWidthRatio;
  const textWidthPX = Math.max(title.length, 1) * charWidthPX;
  const wrappedWidthPX = Math.min(textWidthPX, labelBoxGeometry.maxWidthPX);
  const lineCount = Math.max(1, Math.ceil(textWidthPX / labelBoxGeometry.maxWidthPX));
  const lineHeightPX = labelBoxGeometry.fontSizePX * labelBoxGeometry.lineHeightRatio;
  const width = (wrappedWidthPX + labelBoxGeometry.backgroundPaddingPX * 2) * scale;
  const height = (lineCount * lineHeightPX + labelBoxGeometry.backgroundPaddingPX * 2) * scale;
  const top = center.y + nodeWidth / 2 + labelBoxGeometry.marginYPX * scale;
  return {
    x1: center.x - width / 2,
    x2: center.x + width / 2,
    y1: top,
    y2: top + height,
  };
};

export const labelBoxesIntersect = (left: LabelBox, right: LabelBox): boolean =>
  left.x1 < right.x2 && right.x1 < left.x2 && left.y1 < right.y2 && right.y1 < left.y2;

const labelBoxForNode = (node: cytoscape.NodeSingular, zoom: number): LabelBox | null => {
  const position = node.renderedPosition();
  if (!position) {
    return null;
  }
  const title = String(node.data("title") ?? "");
  return estimateLabelBox(
    { x: Number(position.x), y: Number(position.y) },
    Number(node.renderedWidth()),
    title,
    zoom
  );
};

// Drops labels whose estimated rendered boxes overlap, so a dense cluster stops
// stacking text on top of itself. Pinned ids (the explicit selection and hover
// focus) are seeded first and are never dropped; everything else is walked in
// `sortedNodesByImportance` order, so the highest-degree label wins a collision
// and equal-degree ties break deterministically by id.
export const cullCollidingLabels = (
  cy: cytoscape.Core | null,
  candidateIDs: ReadonlySet<string>,
  pinnedIDs: ReadonlySet<string>
): Set<string> => {
  const keptIDs = new Set<string>();
  if (!cy) {
    return keptIDs;
  }
  const zoom = cy.zoom();
  const acceptedBoxes: LabelBox[] = [];

  const accept = (node: cytoscape.NodeSingular): void => {
    keptIDs.add(node.id());
    const box = labelBoxForNode(node, zoom);
    if (box) {
      acceptedBoxes.push(box);
    }
  };

  for (const id of pinnedIDs) {
    if (!candidateIDs.has(id)) {
      continue;
    }
    const node = cyNodeByID(cy, id);
    if (node) {
      accept(node);
    }
  }

  const remaining = nodesForIDs(cy, candidateIDs).filter((node) => !keptIDs.has(node.id()));

  for (const node of sortedNodesByImportance(remaining)) {
    const box = labelBoxForNode(node, zoom);
    if (!box) {
      // No usable geometry to judge against — keep the label rather than
      // silently hiding it.
      keptIDs.add(node.id());
      continue;
    }
    if (acceptedBoxes.some((accepted) => labelBoxesIntersect(accepted, box))) {
      continue;
    }
    acceptedBoxes.push(box);
    keptIDs.add(node.id());
  }

  return keptIDs;
};

const addSearchLabels = (
  cy: cytoscape.Core,
  matchIDs: ReadonlySet<string>,
  labelIDs: Set<string>
): void => {
  const matchNodes = nodesForIDs(cy, matchIDs);
  const labeledMatches =
    matchNodes.length <= labelPolicy.searchMatchLabelLimit
      ? matchNodes
      : sortedNodesByImportance(matchNodes).slice(0, labelPolicy.searchMatchLabelLimit);
  for (const node of labeledMatches) {
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
    addSearchLabels(cy, matchIDs, labelIDs);
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
  if (cy) {
    for (const matchNode of nodesForIDs(cy, matchIDs)) {
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
  matchIDs: ReadonlySet<string> = new Set<string>(),
  showSelectedNeighborLabels = false
): void => {
  if (!cy) {
    return;
  }
  const labelIDs = labelIDsForCurrentState(cy, labelMode, selectedNodeID, hoveredNodeID, matchIDs);
  const focus = visualFocusState(cy, selectedNodeID, hoveredNodeID, matchIDs);
  // "all" is the explicit show-me-everything-overlap-included escape hatch, so
  // it bypasses the collision cull entirely.
  const pinnedLabelIDs = new Set([focus.selectedID, focus.hoveredID].filter(Boolean));
  // An explicit request reveals every selected neighbor, even in a crowded
  // neighborhood. Pin them so neither the automatic cap nor collisions hide them.
  if (focus.selectedID && showSelectedNeighborLabels) {
    for (const id of focus.neighborIDs) {
      labelIDs.add(id);
      pinnedLabelIDs.add(id);
    }
  }
  const visibleLabelIDs =
    labelMode === "all" ? labelIDs : cullCollidingLabels(cy, labelIDs, pinnedLabelIDs);
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
      node.toggleClass("is-labeled", visibleLabelIDs.has(id));
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
