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
    selector: "node.is-search-match",
    style: {
      "border-width": 4,
      "border-color": palette.hovered,
      "z-index": 25,
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
    selector: "edge[?mutual]",
    style: {
      "source-arrow-shape": "triangle",
      "source-arrow-color": palette.edge,
      "curve-style": "straight",
    },
  },
  {
    selector: "edge.is-highlighted-edge",
    style: {
      width: 3,
      "line-color": palette.neighbor,
      "target-arrow-color": palette.neighbor,
      "source-arrow-color": palette.neighbor,
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

