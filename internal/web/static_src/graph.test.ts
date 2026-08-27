import { describe, expect, it } from "vitest";

import {
  DEFAULT_GRAPH_PALETTE,
  buildGraphStyleRules,
  colorForFolder,
  computeFallbackGraphDegrees,
  emptyGraphElements,
  graphNodeClasses,
  graphScopeLabel,
  mergeReciprocalGraphEdges,
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

describe("mergeReciprocalGraphEdges", () => {
  it("passes self-edges, blank endpoints, and one-directional edges through as mutual: false", () => {
    expect(
      mergeReciprocalGraphEdges([
        { id: "loop", source: "alpha", target: "alpha", mutual: false },
        { id: "blank", source: "", target: "beta", mutual: false },
        { id: "one-way", source: "alpha", target: "beta", mutual: false },
      ])
    ).toEqual([
      { id: "loop", source: "alpha", target: "alpha", mutual: false },
      { id: "blank", source: "", target: "beta", mutual: false },
      { id: "one-way", source: "alpha", target: "beta", mutual: false },
    ]);
  });

  it("keeps the first-seen edge when both ids tie", () => {
    expect(
      mergeReciprocalGraphEdges([
        { id: "", source: "alpha", target: "beta", mutual: false },
        { id: "", source: "beta", target: "alpha", mutual: false },
      ])
    ).toEqual([{ id: "", source: "alpha", target: "beta", mutual: true }]);
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
      { id: "e1", source: "alpha", target: "beta", mutual: true },
      { id: "e2", source: "alpha", target: "beta", mutual: false },
      { id: "loop", source: "alpha", target: "alpha", mutual: false },
      { id: "missing", source: "alpha", target: "missing", mutual: false },
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

  it("carries matchesQuery through normalization and Cytoscape data", () => {
    const graph = normalizeGraphPayload({
      nodes: [
        { data: { id: "alpha", title: "Alpha", matchesQuery: true } },
        { data: { id: "beta", title: "Beta", matchesQuery: false } },
        { data: { id: "gamma", title: "Gamma" } },
      ],
      edges: [],
    });

    expect(graph.nodes.map((node) => node.matchesQuery)).toEqual([true, false, false]);
    expect(toCytoscapeElements(graph).map((element) => element.data.matchesQuery)).toEqual([
      true,
      false,
      false,
    ]);
  });

  it("collapses reciprocal pairs onto the lexicographically smaller id", () => {
    const graph = normalizeGraphPayload({
      nodes: [{ data: { id: "alpha" } }, { data: { id: "beta" } }, { data: { id: "gamma" } }],
      edges: [
        { data: { id: "z-first", source: "alpha", target: "beta" } },
        { data: { id: "a-second", source: "beta", target: "alpha" } },
        { data: { id: "one-way", source: "beta", target: "gamma" } },
      ],
    });

    expect(graph.edges).toEqual([
      { id: "a-second", source: "beta", target: "alpha", mutual: true },
      { id: "one-way", source: "beta", target: "gamma", mutual: false },
    ]);
  });

  it("computes fallback degrees before the reciprocal merge halves the edge list", () => {
    const graph = normalizeGraphPayload({
      nodes: [{ data: { id: "alpha" } }, { data: { id: "beta" } }],
      edges: [
        { data: { id: "e1", source: "alpha", target: "beta" } },
        { data: { id: "e2", source: "beta", target: "alpha" } },
      ],
    });

    expect(graph.edges).toHaveLength(1);
    expect(graph.nodes.map((node) => node.degree)).toEqual([2, 2]);
    expect(graph.nodes.map((node) => node.filteredDegree)).toEqual([2, 2]);
  });

  it("carries mutual through Cytoscape edge data", () => {
    const graph = normalizeGraphPayload({
      nodes: [{ data: { id: "alpha" } }, { data: { id: "beta" } }, { data: { id: "gamma" } }],
      edges: [
        { data: { id: "e1", source: "alpha", target: "beta" } },
        { data: { id: "e2", source: "beta", target: "alpha" } },
        { data: { id: "e3", source: "beta", target: "gamma" } },
      ],
    });

    expect(
      toCytoscapeElements(graph)
        .filter((element) => element.data.source)
        .map((element) => ({ id: element.data.id, mutual: element.data.mutual }))
    ).toEqual([
      { id: "e1", mutual: true },
      { id: "e3", mutual: false },
    ]);
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

    const searchMatch = ruleFor(rules, "node.is-search-match").style;
    expect(searchMatch["border-color"]).toBe("#606060");
    expect(searchMatch["border-width"]).toBe(4);

    const selected = ruleFor(rules, "node.is-selected").style;
    expect(selected["border-color"]).toBe("#707070");
    expect(selected.color).toBe("#808080");

    // A collapsed folder has to read as a container, not as an unusually large
    // note, and its hover has to promise "expands" rather than "selects".
    const aggregate = ruleFor(rules, "node[?isAggregate]").style;
    expect(aggregate.shape).toBe("round-rectangle");
    expect(aggregate["border-style"]).toBe("double");
    expect(aggregate["border-color"]).toBe("#101010");
    expect(Number(aggregate["z-index"])).toBeGreaterThan(Number(ruleFor(rules, "node").style["z-index"]));

    const aggregateHover = ruleFor(rules, "node[?isAggregate].is-hovered").style;
    expect(aggregateHover["border-style"]).toBe("double");
    expect(aggregateHover["border-color"]).toBe("#707070");
    expect(aggregateHover["border-color"]).not.toBe(
      ruleFor(rules, "node.is-hovered").style["border-color"]
    );

    const edge = ruleFor(rules, "edge").style;
    expect(edge["line-color"]).toBe("#404040");
    expect(edge["target-arrow-color"]).toBe("#404040");

    const mutualEdge = ruleFor(rules, "edge[?mutual]").style;
    expect(mutualEdge["source-arrow-shape"]).toBe("triangle");
    expect(mutualEdge["source-arrow-color"]).toBe("#404040");
    expect(mutualEdge["curve-style"]).toBe("straight");

    const highlightedEdge = ruleFor(rules, "edge.is-highlighted-edge").style;
    expect(highlightedEdge["line-color"]).toBe("#505050");
    expect(highlightedEdge["target-arrow-color"]).toBe("#505050");
    expect(highlightedEdge["source-arrow-color"]).toBe("#505050");
  });

  // Cytoscape breaks an equal-specificity tie by array order, so where the two
  // aggregate rules sit is part of their meaning.
  it("orders the aggregate rules so state classes still win, but the aggregate hover wins over them", () => {
    const rules = buildGraphStyleRules();
    const indexOf = (selector: string): number =>
      rules.findIndex((rule) => rule.selector === selector);

    expect(indexOf("node[?isAggregate]")).toBeGreaterThan(indexOf("node"));
    expect(indexOf("node[?isAggregate]")).toBeLessThan(indexOf("node.is-selected"));
    expect(indexOf("node[?isAggregate].is-hovered")).toBeGreaterThan(indexOf("node.is-hovered"));
    expect(indexOf("node[?isAggregate].is-hovered")).toBeGreaterThan(indexOf("node.is-selected"));
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
