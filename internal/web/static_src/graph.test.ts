import { describe, expect, it } from "vitest";

import {
  DEFAULT_GRAPH_PALETTE,
  buildGraphStyleRules,
  colorForFolder,
  computeFallbackGraphDegrees,
  emptyGraphElements,
  graphNodeClasses,
  graphScopeLabel,
  normalizeGraphPayload,
  nodeSizeForDegree,
  readGraphPalette,
  toCytoscapeElements,
  type GraphPalette,
  type GraphStyleRule,
  type RawGraphEdge,
  type RawGraphNode,
} from "./graph";

const ruleFor = (rules: GraphStyleRule[], selector: string): GraphStyleRule => {
  const rule = rules.find((candidate) => candidate.selector === selector);
  if (!rule) {
    throw new Error(`missing style rule for selector ${selector}`);
  }
  return rule;
};

describe("computeFallbackGraphDegrees", () => {
  it("counts unique visible incoming and outgoing graph endpoints", () => {
    const rawNodes: RawGraphNode[] = [
      { data: { id: "alpha" } },
      { data: { id: "beta" } },
      { data: { id: "gamma" } },
    ];
    const rawEdges: RawGraphEdge[] = [
      { data: { source: "alpha", target: "beta" } },
      { data: { source: "alpha", target: "beta" } },
      { data: { source: "beta", target: "alpha" } },
      { data: { source: "alpha", target: "alpha" } },
      { data: { source: "alpha", target: "missing" } },
      { data: { source: "", target: "gamma" } },
    ];

    const degrees = computeFallbackGraphDegrees(rawNodes, rawEdges);

    expect(degrees.get("alpha")).toEqual({ inDegree: 1, outDegree: 1, degree: 2 });
    expect(degrees.get("beta")).toEqual({ inDegree: 1, outDegree: 1, degree: 2 });
    expect(degrees.get("gamma")).toEqual({ inDegree: 0, outDegree: 0, degree: 0 });
  });
});

describe("normalizeGraphPayload", () => {
  it("normalizes nodes, edges, fallback degrees, classes, and Cytoscape data", () => {
    const graph = normalizeGraphPayload({
      nodes: [
        {
          data: {
            id: " alpha ",
            title: " Alpha ",
            folder: "projects",
            brokenCount: "2",
            isOrphan: true,
          },
        },
        { data: { id: "beta", title: "Beta", folder: "archive" } },
        {
          data: {
            id: "gamma",
            title: "Gamma",
            inDegree: "bad",
            outDegree: -1,
            degree: "bad",
            filteredInDegree: "7",
            filteredOutDegree: "1",
            filteredDegree: "8",
            isDeadEnd: true,
          },
        },
      ],
      edges: [
        { data: { id: "e1", source: "alpha", target: "beta" } },
        { data: { id: "e2", source: "alpha", target: "beta" } },
        { data: { id: "e3", source: "beta", target: "alpha" } },
        { data: { id: "loop", source: "alpha", target: "alpha" } },
        { data: { id: "missing", source: "alpha", target: "missing" } },
      ],
    });

    const alpha = graph.nodes.find((node) => node.id === "alpha");
    const gamma = graph.nodes.find((node) => node.id === "gamma");

    expect(alpha).toMatchObject({
      id: "alpha",
      title: "Alpha",
      folder: "projects",
      color: colorForFolder("projects"),
      inDegree: 1,
      outDegree: 1,
      degree: 2,
      filteredInDegree: 1,
      filteredOutDegree: 1,
      filteredDegree: 2,
      hasBroken: true,
      brokenCount: 2,
      isOrphan: true,
      isDeadEnd: false,
      size: nodeSizeForDegree(2),
    });
    expect(gamma).toMatchObject({
      inDegree: 0,
      outDegree: 0,
      degree: 0,
      filteredInDegree: 7,
      filteredOutDegree: 1,
      filteredDegree: 8,
      hasBroken: false,
      isDeadEnd: true,
    });

    expect(graphNodeClasses(alpha!)).toBe("has-broken is-orphan");
    expect(graphNodeClasses(gamma!)).toBe("is-dead-end");
    expect(graph.edges).toEqual([
      { id: "e1", source: "alpha", target: "beta" },
      { id: "e2", source: "alpha", target: "beta" },
      { id: "e3", source: "beta", target: "alpha" },
      { id: "loop", source: "alpha", target: "alpha" },
      { id: "missing", source: "alpha", target: "missing" },
    ]);

    expect(toCytoscapeElements(graph)[0]).toMatchObject({
      classes: "has-broken is-orphan",
      data: {
        id: "alpha",
        title: "Alpha",
        filteredDegree: 2,
        size: nodeSizeForDegree(2),
      },
    });
  });

  it("returns empty graph elements for malformed top-level payloads", () => {
    expect(normalizeGraphPayload(null)).toEqual(emptyGraphElements());
    expect(normalizeGraphPayload({ nodes: "bad", edges: 42 })).toEqual(emptyGraphElements());
  });
});

describe("readGraphPalette", () => {
  it("resolves each palette color from its CSS theme token", () => {
    const tokens: Record<string, string> = {
      "--bn-graph-node-label": " #111111 ",
      "--bn-graph-label-outline": "#222222",
      "--bn-graph-canvas-bg": "#333333",
      "--bn-graph-edge": "#444444",
      "--bn-graph-neighbor": "#555555",
      "--bn-graph-hovered": "#666666",
      "--bn-graph-locked": "#777777",
      "--bn-graph-selected-text": "#888888",
      "--bn-graph-broken": "#999999",
      "--bn-graph-orphan": "#aaaaaa",
    };

    const palette = readGraphPalette((name) => tokens[name] ?? "");

    expect(palette).toEqual<GraphPalette>({
      nodeLabel: "#111111",
      labelOutline: "#222222",
      labelBackground: "#333333",
      edge: "#444444",
      neighbor: "#555555",
      hovered: "#666666",
      selected: "#777777",
      selectedText: "#888888",
      broken: "#999999",
      orphan: "#aaaaaa",
    });
  });

  it("falls back to default dark colors for missing, blank, or throwing reads", () => {
    expect(readGraphPalette(() => "")).toEqual(DEFAULT_GRAPH_PALETTE);
    expect(readGraphPalette(() => "   ")).toEqual(DEFAULT_GRAPH_PALETTE);
    expect(
      readGraphPalette(() => {
        throw new Error("no computed style");
      })
    ).toEqual(DEFAULT_GRAPH_PALETTE);
  });

  it("mixes resolved tokens with fallbacks per key", () => {
    const palette = readGraphPalette((name) =>
      name === "--bn-graph-edge" ? "#0f0f0f" : ""
    );

    expect(palette.edge).toBe("#0f0f0f");
    expect(palette.nodeLabel).toBe(DEFAULT_GRAPH_PALETTE.nodeLabel);
  });
});

describe("buildGraphStyleRules", () => {
  it("keeps data-driven node fills while theming the rest from the palette", () => {
    const palette: GraphPalette = {
      nodeLabel: "#101010",
      labelOutline: "#202020",
      labelBackground: "#303030",
      edge: "#404040",
      neighbor: "#505050",
      hovered: "#606060",
      selected: "#707070",
      selectedText: "#808080",
      broken: "#909090",
      orphan: "#a0a0a0",
    };

    const rules = buildGraphStyleRules(palette);

    const node = ruleFor(rules, "node").style;
    expect(node["background-color"]).toBe("data(color)");
    expect(node.color).toBe("#101010");
    expect(node["text-outline-color"]).toBe("#202020");
    expect(node["text-background-color"]).toBe("#303030");

    expect(ruleFor(rules, "node.has-broken").style["border-color"]).toBe("#909090");
    expect(ruleFor(rules, "node.is-orphan").style["border-color"]).toBe("#a0a0a0");
    expect(ruleFor(rules, "node.is-neighbor").style["border-color"]).toBe("#505050");
    expect(ruleFor(rules, "node.is-hovered").style["border-color"]).toBe("#606060");

    const selected = ruleFor(rules, "node.is-selected").style;
    expect(selected["border-color"]).toBe("#707070");
    expect(selected.color).toBe("#808080");

    const edge = ruleFor(rules, "edge").style;
    expect(edge["line-color"]).toBe("#404040");
    expect(edge["target-arrow-color"]).toBe("#404040");

    const highlightedEdge = ruleFor(rules, "edge.is-highlighted-edge").style;
    expect(highlightedEdge["line-color"]).toBe("#505050");
    expect(highlightedEdge["target-arrow-color"]).toBe("#505050");
  });

  it("defaults to the preserved dark palette", () => {
    const rules = buildGraphStyleRules();
    expect(ruleFor(rules, "node").style.color).toBe(DEFAULT_GRAPH_PALETTE.nodeLabel);
    expect(ruleFor(rules, "edge").style["line-color"]).toBe(DEFAULT_GRAPH_PALETTE.edge);
  });
});

describe("graphScopeLabel", () => {
  it("describes query and filter scope", () => {
    expect(graphScopeLabel("", false)).toBe("");
    expect(graphScopeLabel("daily", false)).toBe('"daily"');
    expect(graphScopeLabel("", true)).toBe("the active filters");
    expect(graphScopeLabel("daily", true)).toBe('"daily" with the active filters');
  });
});
