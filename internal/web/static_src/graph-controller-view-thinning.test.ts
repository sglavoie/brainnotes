import { describe, expect, it } from "vitest";

import { buildDisplayGraph } from "./graph-controller-view";
import {
  edgeIDs,
  edgePairs,
  makeEdge,
  makeNode,
  nodeIDs,
  withOptions,
} from "./graph-controller-view.test-helpers";
import { colorForFolder, nodeSizeForDegree, type GraphElements } from "./graph";

describe("buildDisplayGraph min-degree stage", () => {
  const hubGraph = (): GraphElements => ({
    nodes: [makeNode("hub"), makeNode("leaf1"), makeNode("leaf2"), makeNode("lonely")],
    edges: [makeEdge("h1", "hub", "leaf1"), makeEdge("h2", "hub", "leaf2")],
  });

  it("drops nodes below the threshold and then the dangling edges", () => {
    const { elements, stats } = buildDisplayGraph(hubGraph(), withOptions({ minDegree: 2 }));

    expect(nodeIDs(elements)).toEqual(["hub"]);
    expect(edgeIDs(elements)).toEqual([]);
    expect(stats.droppedByDegree).toBe(3);
  });

  it("is a single pass, not iterated to a fixed point", () => {
    // leaf1/leaf2 fall below 2 and are dropped; hub then has degree 0 in the displayed
    // graph but survives, because the threshold was evaluated once against the incoming
    // subset. Iterating would empty the graph and make the slider jumpy.
    const { elements } = buildDisplayGraph(hubGraph(), withOptions({ minDegree: 2 }));

    expect(elements.nodes[0].filteredDegree).toBe(0);
  });

  it("counts degrees within the current subset, not the source graph", () => {
    // Isolating on leaf1 leaves hub with subset degree 1, so a threshold of 2 drops it
    // even though it has degree 2 in the full graph.
    const { elements } = buildDisplayGraph(
      hubGraph(),
      withOptions({ isolateID: "leaf1", minDegree: 2, keepIDs: new Set(["leaf1"]) })
    );

    expect(nodeIDs(elements)).toEqual(["leaf1"]);
  });

  it("exempts keepIDs members from the threshold", () => {
    const { elements, stats } = buildDisplayGraph(
      hubGraph(),
      withOptions({ minDegree: 2, keepIDs: new Set(["lonely", "leaf1"]) })
    );

    expect(nodeIDs(elements)).toEqual(["hub", "leaf1", "lonely"]);
    expect(edgeIDs(elements)).toEqual(["h1"]);
    expect(stats.droppedByDegree).toBe(1);
  });

  it("is off at zero", () => {
    const { elements, stats } = buildDisplayGraph(hubGraph(), withOptions({ minDegree: 0 }));

    expect(nodeIDs(elements)).toEqual(["hub", "leaf1", "leaf2", "lonely"]);
    expect(stats.droppedByDegree).toBe(0);
  });
});

describe("buildDisplayGraph collapse stage", () => {
  const folderGraph = (): GraphElements => ({
    nodes: [
      makeNode("a1", { folder: "A/one" }),
      makeNode("a2", { folder: "A/two" }),
      makeNode("b1", { folder: "B" }),
      makeNode("b2", { folder: "B" }),
      makeNode("loose", { folder: "" }),
      makeNode("solo", { folder: "C" }),
    ],
    edges: [
      makeEdge("a1a2", "a1", "a2"),
      makeEdge("a1b1", "a1", "b1"),
      makeEdge("a2b2", "a2", "b2"),
      makeEdge("looseb1", "loose", "b1"),
      makeEdge("solob1", "solo", "b1"),
    ],
  });

  it("replaces multi-member groups with one aggregate placed at the first member's slot", () => {
    const { elements, stats } = buildDisplayGraph(folderGraph(), withOptions({ collapseDepth: 1 }));

    expect(nodeIDs(elements)).toEqual(["folder:A", "folder:B", "loose", "solo"]);
    expect(stats.aggregateCount).toBe(2);

    const aggregate = elements.nodes[0];
    expect(aggregate).toMatchObject({
      id: "folder:A",
      title: "A (2)",
      folder: "A",
      color: colorForFolder("A"),
      hasBroken: false,
      brokenCount: 0,
      isOrphan: false,
      isDeadEnd: false,
      isAggregate: true,
      memberIDs: ["a1", "a2"],
    });
  });

  it("drops intra-group edges and dedupes remapped parallel edges", () => {
    const { elements } = buildDisplayGraph(folderGraph(), withOptions({ collapseDepth: 1 }));

    // a1a2 is intra-group; a1b1 and a2b2 both remap to folder:A -> folder:B.
    expect(edgeIDs(elements)).toEqual(["a1b1", "looseb1", "solob1"]);
    expect(edgePairs(elements)).toEqual([
      "folder:A->folder:B",
      "loose->folder:B",
      "solo->folder:B",
    ]);
  });

  it("re-runs the reciprocal merge over remapped edges", () => {
    const source: GraphElements = {
      nodes: [
        makeNode("a1", { folder: "A" }),
        makeNode("a2", { folder: "A" }),
        makeNode("b1", { folder: "B" }),
        makeNode("b2", { folder: "B" }),
      ],
      // Two one-directional note links that become a reciprocal folder pair.
      edges: [makeEdge("e1", "a1", "b1"), makeEdge("e2", "b2", "a2")],
    };

    const { elements } = buildDisplayGraph(source, withOptions({ collapseDepth: 1 }));

    expect(elements.edges).toEqual([
      { id: "e1", source: "folder:A", target: "folder:B", mutual: true },
    ]);
  });

  it("groups on the first collapseDepth segments", () => {
    const { elements } = buildDisplayGraph(folderGraph(), withOptions({ collapseDepth: 2 }));

    // At depth 2 "A/one" and "A/two" are different groups of one, so both pass through.
    expect(nodeIDs(elements)).toEqual(["a1", "a2", "folder:B", "loose", "solo"]);
  });

  it("passes empty-folder nodes and single-member groups through unchanged", () => {
    const { elements } = buildDisplayGraph(folderGraph(), withOptions({ collapseDepth: 1 }));

    const loose = elements.nodes.find((node) => node.id === "loose");
    const solo = elements.nodes.find((node) => node.id === "solo");
    expect(loose?.isAggregate).toBeFalsy();
    expect(solo?.isAggregate).toBeFalsy();
    expect(solo?.folder).toBe("C");
  });

  it("restores the members of an expanded folder", () => {
    const { elements, stats } = buildDisplayGraph(
      folderGraph(),
      withOptions({ collapseDepth: 1, expandedFolders: new Set(["B"]) })
    );

    expect(nodeIDs(elements)).toEqual(["folder:A", "b1", "b2", "loose", "solo"]);
    expect(stats.aggregateCount).toBe(1);
    // Edges into the expanded folder are no longer merged onto one aggregate endpoint.
    expect(edgePairs(elements)).toEqual([
      "folder:A->b1",
      "folder:A->b2",
      "loose->b1",
      "solo->b1",
    ]);
  });

  it("is off at depth zero", () => {
    const { elements, stats } = buildDisplayGraph(folderGraph(), withOptions({ collapseDepth: 0 }));

    expect(nodeIDs(elements)).toEqual(["a1", "a2", "b1", "b2", "loose", "solo"]);
    expect(stats.aggregateCount).toBe(0);
  });

  it("marks an aggregate as matching when any member matches the query", () => {
    const source: GraphElements = {
      nodes: [
        makeNode("m1", { folder: "A", matchesQuery: false }),
        makeNode("m2", { folder: "A", matchesQuery: true }),
        makeNode("n1", { folder: "B", matchesQuery: false }),
        makeNode("n2", { folder: "B", matchesQuery: false }),
      ],
      edges: [],
    };

    const { elements } = buildDisplayGraph(source, withOptions({ collapseDepth: 1 }));

    expect(elements.nodes.map((node) => node.matchesQuery)).toEqual([true, false]);
  });
});

describe("buildDisplayGraph degree and size recompute", () => {
  it("recomputes the filtered degrees from the displayed edges and resizes", () => {
    const source: GraphElements = {
      nodes: [
        makeNode("hub", { filteredInDegree: 40, filteredOutDegree: 40, filteredDegree: 80, size: 40 }),
        makeNode("leaf", { filteredInDegree: 9, filteredOutDegree: 9, filteredDegree: 18, size: 40 }),
        makeNode("gone", { filteredDegree: 5, size: 40 }),
      ],
      edges: [makeEdge("hl", "hub", "leaf"), makeEdge("hg", "hub", "gone")],
    };

    const { elements } = buildDisplayGraph(
      source,
      withOptions({ isolateID: "hub", minDegree: 2 })
    );

    expect(nodeIDs(elements)).toEqual(["hub"]);
    expect(elements.nodes[0]).toMatchObject({
      filteredInDegree: 0,
      filteredOutDegree: 0,
      filteredDegree: 0,
      size: nodeSizeForDegree(0),
    });
  });

  it("counts in and out degrees separately from the displayed edge set", () => {
    const source: GraphElements = {
      nodes: [makeNode("a"), makeNode("b"), makeNode("c")],
      edges: [makeEdge("ab", "a", "b"), makeEdge("cb", "c", "b")],
    };

    const { elements } = buildDisplayGraph(source, withOptions());

    expect(
      elements.nodes.map((node) => [
        node.id,
        node.filteredInDegree,
        node.filteredOutDegree,
        node.filteredDegree,
      ])
    ).toEqual([
      ["a", 0, 1, 1],
      ["b", 2, 0, 2],
      ["c", 0, 1, 1],
    ]);
  });

  it("counts a merged reciprocal pair as both an in and an out edge", () => {
    const source: GraphElements = {
      nodes: [makeNode("a"), makeNode("b")],
      edges: [makeEdge("ab", "a", "b"), makeEdge("ba", "b", "a")],
    };

    const { elements } = buildDisplayGraph(source, withOptions());

    expect(elements.edges).toHaveLength(1);
    expect(elements.nodes.map((node) => node.filteredDegree)).toEqual([2, 2]);
  });

  it("sizes an aggregate by its member count, not by its surviving edges", () => {
    const members = Array.from({ length: 9 }, (_, index) =>
      makeNode(`m${index}`, { folder: "Big" })
    );
    const source: GraphElements = {
      nodes: [...members, makeNode("outside", { folder: "" })],
      edges: [makeEdge("link", "m0", "outside")],
    };

    const { elements } = buildDisplayGraph(source, withOptions({ collapseDepth: 1 }));

    const aggregate = elements.nodes[0];
    expect(aggregate.memberIDs).toHaveLength(9);
    expect(aggregate.filteredDegree).toBe(1);
    expect(aggregate.size).toBe(nodeSizeForDegree(9));
    // An aggregate's raw degree mirrors its displayed degree rather than staying 0.
    expect(aggregate.degree).toBe(1);
    expect(elements.nodes[1].size).toBe(nodeSizeForDegree(1));
  });
});
