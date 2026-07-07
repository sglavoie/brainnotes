import { normalizeParamValue } from "./filters";

type GraphRecord = Record<string, unknown>;

type RawGraphNodeField =
  | "id"
  | "title"
  | "folder"
  | "color"
  | "inDegree"
  | "outDegree"
  | "degree"
  | "filteredInDegree"
  | "filteredOutDegree"
  | "filteredDegree"
  | "hasBroken"
  | "brokenCount"
  | "isOrphan"
  | "isDeadEnd";

type RawGraphEdgeField = "id" | "source" | "target";

export type RawGraphNodeData = Partial<Record<RawGraphNodeField, unknown>>;
export type RawGraphEdgeData = Partial<Record<RawGraphEdgeField, unknown>>;

export type RawGraphNode = {
  data: RawGraphNodeData;
};

export type RawGraphEdge = {
  data: RawGraphEdgeData;
};

export type DegreeSet = {
  inSources: Set<string>;
  outTargets: Set<string>;
};

export type DegreeSummary = {
  inDegree: number;
  outDegree: number;
  degree: number;
};

export type NormalizedGraphNode = DegreeSummary & {
  id: string;
  title: string;
  folder: string;
  color: string;
  filteredInDegree: number;
  filteredOutDegree: number;
  filteredDegree: number;
  hasBroken: boolean;
  brokenCount: number;
  isOrphan: boolean;
  isDeadEnd: boolean;
  size?: number;
};

export type NormalizedGraphEdge = {
  id: string;
  source: string;
  target: string;
};

export type GraphElements = {
  nodes: NormalizedGraphNode[];
  edges: NormalizedGraphEdge[];
};

export type GraphNodeModel = NormalizedGraphNode;
export type GraphEdgeModel = NormalizedGraphEdge;

export type GraphLabelMode = "auto" | "all";

export type GraphViewportSnapshot = {
  pan: cytoscape.Position;
  zoom: number;
};

export type GraphFocusState = {
  focusNodeID: string;
  selectedID: string;
  hoveredID: string;
  neighborIDs: Set<string>;
  highlightedEdgeIDs: Set<string>;
};

const asRecord = (value: unknown): GraphRecord | null =>
  value && typeof value === "object" ? (value as GraphRecord) : null;

const readRawGraphNodeData = (value: unknown): RawGraphNodeData => {
  const record = asRecord(value);
  const data = record ? asRecord(record.data) : null;
  return data || {};
};

const readRawGraphEdgeData = (value: unknown): RawGraphEdgeData => {
  const record = asRecord(value);
  const data = record ? asRecord(record.data) : null;
  return data || {};
};

const asRawGraphNodeArray = (value: unknown): RawGraphNode[] =>
  Array.isArray(value) ? value.map((item) => ({ data: readRawGraphNodeData(item) })) : [];

const asRawGraphEdgeArray = (value: unknown): RawGraphEdge[] =>
  Array.isArray(value) ? value.map((item) => ({ data: readRawGraphEdgeData(item) })) : [];

export const emptyGraphElements = (): GraphElements => ({ nodes: [], edges: [] });

export const readDegreeValue = (value: unknown, fallback: number): number => {
  if (value === undefined || value === null || value === "") {
    return fallback;
  }
  const parsed = Number(value);
  if (Number.isFinite(parsed) && parsed >= 0) {
    return Math.round(parsed);
  }
  return fallback;
};

export const nodeSizeForDegree = (degree: number): number => {
  const safeDegree = Number.isFinite(degree) && degree > 0 ? degree : 0;
  return Math.round(Math.min(40, 24 + Math.sqrt(safeDegree) * 4));
};

const hashFolder = (folder: string): number => {
  let hash = 0;
  for (let i = 0; i < folder.length; i += 1) {
    hash = (hash * 31 + folder.charCodeAt(i)) >>> 0;
  }
  return hash;
};

export const colorForFolder = (folder: string): string => {
  if (!folder) {
    return "#7aa2ff";
  }
  const hue = hashFolder(folder) % 360;
  return `hsl(${hue}, 65%, 60%)`;
};

// Themeable palette for the Cytoscape graph. Folder-derived node fill colors stay
// data-driven (see colorForFolder / toCytoscapeElements); everything else here is
// resolved from CSS theme tokens so the graph restyles with the rest of the UI.
export type GraphPalette = {
  nodeLabel: string;
  labelOutline: string;
  labelBackground: string;
  edge: string;
  neighbor: string;
  hovered: string;
  selected: string;
  selectedText: string;
  broken: string;
  orphan: string;
};

// Defaults preserve the original dark graph colors so any missing token (or a
// non-browser host such as jsdom) falls back to the previous appearance.
export const DEFAULT_GRAPH_PALETTE: GraphPalette = {
  nodeLabel: "#e5e9f0",
  labelOutline: "#0b0f16",
  labelBackground: "#0c1018",
  edge: "#66718a",
  neighbor: "#8cb8ff",
  hovered: "#f3f7ff",
  selected: "#ffd166",
  selectedText: "#fff3c2",
  broken: "#ff9d9d",
  orphan: "#c7d2e7",
};

const GRAPH_PALETTE_VARIABLES: Record<keyof GraphPalette, string> = {
  nodeLabel: "--bn-graph-node-label",
  labelOutline: "--bn-graph-label-outline",
  labelBackground: "--bn-graph-canvas-bg",
  edge: "--bn-graph-edge",
  neighbor: "--bn-graph-neighbor",
  hovered: "--bn-graph-hovered",
  selected: "--bn-graph-locked",
  selectedText: "--bn-graph-selected-text",
  broken: "--bn-graph-broken",
  orphan: "--bn-graph-orphan",
};

export type CSSVariableReader = (name: string) => string;

export const readGraphPalette = (
  read: CSSVariableReader,
  fallback: GraphPalette = DEFAULT_GRAPH_PALETTE
): GraphPalette => {
  const resolve = (key: keyof GraphPalette): string => {
    let raw = "";
    try {
      raw = read(GRAPH_PALETTE_VARIABLES[key]);
    } catch {
      raw = "";
    }
    const value = typeof raw === "string" ? raw.trim() : "";
    return value || fallback[key];
  };
  return {
    nodeLabel: resolve("nodeLabel"),
    labelOutline: resolve("labelOutline"),
    labelBackground: resolve("labelBackground"),
    edge: resolve("edge"),
    neighbor: resolve("neighbor"),
    hovered: resolve("hovered"),
    selected: resolve("selected"),
    selectedText: resolve("selectedText"),
    broken: resolve("broken"),
    orphan: resolve("orphan"),
  };
};

export type GraphStyleRule = {
  selector: string;
  style: Record<string, unknown>;
};

export const buildGraphStyleRules = (
  palette: GraphPalette = DEFAULT_GRAPH_PALETTE
): GraphStyleRule[] => [
  {
    selector: "node",
    style: {
      label: "",
      "background-color": "data(color)",
      color: palette.nodeLabel,
      "font-size": "10px",
      "font-weight": 600,
      "text-valign": "bottom",
      "text-halign": "center",
      "text-margin-y": 6,
      "text-wrap": "wrap",
      "text-overflow-wrap": "anywhere",
      "text-max-width": 130,
      "text-outline-width": 1,
      "text-outline-color": palette.labelOutline,
      "text-background-color": palette.labelBackground,
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
      "border-color": palette.broken,
      shape: "diamond",
    },
  },
  {
    selector: "node.is-orphan",
    style: {
      "border-width": 2,
      "border-style": "dashed",
      "border-color": palette.orphan,
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
      "border-color": palette.neighbor,
      "z-index": 8,
    },
  },
  {
    selector: "node.is-hovered",
    style: {
      "border-width": 3,
      "border-color": palette.hovered,
      "font-size": "11px",
      "z-index": 20,
    },
  },
  {
    selector: "node.is-selected",
    style: {
      "border-width": 4,
      "border-color": palette.selected,
      color: palette.selectedText,
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
      "line-color": palette.edge,
      "target-arrow-color": palette.edge,
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
      "line-color": palette.neighbor,
      "target-arrow-color": palette.neighbor,
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
];

export const computeFallbackGraphDegrees = (
  rawNodes: RawGraphNode[],
  rawEdges: RawGraphEdge[]
): Map<string, DegreeSummary> => {
  const degreeSets = new Map<string, DegreeSet>();
  for (const node of rawNodes) {
    const id = normalizeParamValue(node.data.id);
    if (id === "") {
      continue;
    }
    degreeSets.set(id, {
      inSources: new Set<string>(),
      outTargets: new Set<string>(),
    });
  }

  for (const edge of rawEdges) {
    const source = normalizeParamValue(edge.data.source);
    const target = normalizeParamValue(edge.data.target);
    if (source === "" || target === "" || source === target) {
      continue;
    }
    if (!degreeSets.has(source) || !degreeSets.has(target)) {
      continue;
    }
    degreeSets.get(source)?.outTargets.add(target);
    degreeSets.get(target)?.inSources.add(source);
  }

  const degrees = new Map<string, DegreeSummary>();
  for (const [id, sets] of degreeSets) {
    const inDegree = sets.inSources.size;
    const outDegree = sets.outTargets.size;
    degrees.set(id, {
      inDegree,
      outDegree,
      degree: inDegree + outDegree,
    });
  }
  return degrees;
};

const normalizeGraphNodeData = (
  nodeData: RawGraphNodeData,
  fallback: DegreeSummary,
  fallbackID = ""
): NormalizedGraphNode => {
  const id = normalizeParamValue(nodeData.id || fallbackID);
  const folder = normalizeParamValue(nodeData.folder || "");
  const inDegree = readDegreeValue(nodeData.inDegree, fallback.inDegree);
  const outDegree = readDegreeValue(nodeData.outDegree, fallback.outDegree);
  const degree = readDegreeValue(nodeData.degree, inDegree + outDegree);
  const filteredInDegree = readDegreeValue(nodeData.filteredInDegree, inDegree);
  const filteredOutDegree = readDegreeValue(nodeData.filteredOutDegree, outDegree);
  const filteredDegree = readDegreeValue(
    nodeData.filteredDegree,
    filteredInDegree + filteredOutDegree
  );
  const brokenCount = readDegreeValue(nodeData.brokenCount, 0);

  return {
    id,
    title: normalizeParamValue(nodeData.title || id),
    folder,
    color: normalizeParamValue(nodeData.color) || colorForFolder(folder),
    inDegree,
    outDegree,
    degree,
    filteredInDegree,
    filteredOutDegree,
    filteredDegree,
    hasBroken: Boolean(nodeData.hasBroken) || brokenCount > 0,
    brokenCount,
    isOrphan: Boolean(nodeData.isOrphan),
    isDeadEnd: Boolean(nodeData.isDeadEnd),
    size: nodeSizeForDegree(filteredDegree),
  };
};

const emptyDegreeSummary = (): DegreeSummary => ({
  inDegree: 0,
  outDegree: 0,
  degree: 0,
});

export const graphNodeModelFromData = (
  value: unknown,
  fallbackID = ""
): NormalizedGraphNode | null => {
  const data = asRecord(value);
  if (!data) {
    return fallbackID ? normalizeGraphNodeData({}, emptyDegreeSummary(), fallbackID) : null;
  }
  const inDegree = readDegreeValue(data.inDegree, 0);
  const outDegree = readDegreeValue(data.outDegree, 0);
  const degree = readDegreeValue(data.degree, inDegree + outDegree);
  return normalizeGraphNodeData(data, { inDegree, outDegree, degree }, fallbackID);
};

export const normalizeGraphPayload = (payload: unknown): GraphElements => {
  const data = asRecord(payload) || {};
  const rawNodes = asRawGraphNodeArray(data.nodes);
  const rawEdges = asRawGraphEdgeArray(data.edges);
  const fallbackDegrees = computeFallbackGraphDegrees(rawNodes, rawEdges);

  const nodes = rawNodes.map((node) => {
    const id = normalizeParamValue(node.data.id);
    return normalizeGraphNodeData(node.data, fallbackDegrees.get(id) || emptyDegreeSummary());
  });

  const edges = rawEdges.map((edge) => ({
    id: normalizeParamValue(edge.data.id),
    source: normalizeParamValue(edge.data.source),
    target: normalizeParamValue(edge.data.target),
  }));

  return {
    nodes,
    edges,
  };
};

export const graphNodeClasses = (node: NormalizedGraphNode): string => {
  const classes: string[] = [];
  if (node.hasBroken) {
    classes.push("has-broken");
  }
  if (node.isOrphan) {
    classes.push("is-orphan");
  }
  if (node.isDeadEnd) {
    classes.push("is-dead-end");
  }
  return classes.join(" ");
};

export const toCytoscapeElements = (graphElements: GraphElements): cytoscape.ElementDefinition[] => [
  ...graphElements.nodes.map((node) => ({
    classes: graphNodeClasses(node),
    data: {
      id: node.id,
      title: node.title,
      folder: node.folder,
      color: node.color,
      inDegree: node.inDegree,
      outDegree: node.outDegree,
      degree: node.degree,
      filteredInDegree: node.filteredInDegree,
      filteredOutDegree: node.filteredOutDegree,
      filteredDegree: node.filteredDegree,
      hasBroken: node.hasBroken,
      brokenCount: node.brokenCount,
      isOrphan: node.isOrphan,
      isDeadEnd: node.isDeadEnd,
      size: node.size,
    },
  })),
  ...graphElements.edges.map((edge) => ({
    data: {
      id: edge.id,
      source: edge.source,
      target: edge.target,
    },
  })),
];

export const graphScopeLabel = (query: string, hasFilters: boolean): string => {
  if (query && hasFilters) {
    return `"${query}" with the active filters`;
  }
  if (query) {
    return `"${query}"`;
  }
  if (hasFilters) {
    return "the active filters";
  }
  return "";
};
