import { describe, expect, it } from "vitest";

import {
  colorForFolder,
  computeFallbackGraphDegrees,
  emptyGraphElements,
  graphNodeClasses,
  graphScopeLabel,
  normalizeGraphPayload,
  nodeSizeForDegree,
  toCytoscapeElements,
  type RawGraphEdge,
  type RawGraphNode,
} from "./graph";

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

describe("graphScopeLabel", () => {
  it("describes query and filter scope", () => {
    expect(graphScopeLabel("", false)).toBe("");
    expect(graphScopeLabel("daily", false)).toBe('"daily"');
    expect(graphScopeLabel("", true)).toBe("the active filters");
    expect(graphScopeLabel("daily", true)).toBe('"daily" with the active filters');
  });
});
