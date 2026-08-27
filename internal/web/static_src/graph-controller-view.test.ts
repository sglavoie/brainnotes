import { describe, expect, it } from "vitest";

import {
  aggregateNodeIDPrefix,
  buildDisplayGraph,
  defaultGraphDisplayOptions,
  folderForAggregateNodeID,
  folderGroupKey,
  isAggregateNodeID,
  type GraphDisplayOptions,
} from "./graph-controller-view";
import {
  colorForFolder,
  nodeSizeForDegree,
  type GraphElements,
  type NormalizedGraphEdge,
  type NormalizedGraphNode,
} from "./graph";

const makeNode = (
  id: string,
  overrides: Partial<NormalizedGraphNode> = {}
): NormalizedGraphNode => ({
  id,
  title: id,
  folder: "",
  color: "#7aa2ff",
  inDegree: 0,
  outDegree: 0,
  degree: 0,
  filteredInDegree: 0,
  filteredOutDegree: 0,
  filteredDegree: 0,
  hasBroken: false,
  brokenCount: 0,
  isOrphan: false,
  isDeadEnd: false,
  matchesQuery: false,
  size: nodeSizeForDegree(0),
  ...overrides,
});

const makeEdge = (id: string, source: string, target: string): NormalizedGraphEdge => ({
  id,
  source,
  target,
  mutual: false,
});

const withOptions = (overrides: Partial<GraphDisplayOptions> = {}): GraphDisplayOptions => ({
  ...defaultGraphDisplayOptions(),
  ...overrides,
});

const nodeIDs = (elements: GraphElements): string[] => elements.nodes.map((node) => node.id);
const edgeIDs = (elements: GraphElements): string[] => elements.edges.map((edge) => edge.id);
const edgePairs = (elements: GraphElements): string[] =>
  elements.edges.map((edge) => `${edge.source}->${edge.target}${edge.mutual ? "*" : ""}`);

// a -> b -> c -> d, plus a disconnected island e -> f.
const chainGraph = (): GraphElements => ({
  nodes: [
    makeNode("a"),
    makeNode("b"),
    makeNode("c"),
    makeNode("d"),
    makeNode("e"),
    makeNode("f"),
  ],
  edges: [
    makeEdge("ab", "a", "b"),
    makeEdge("bc", "b", "c"),
    makeEdge("cd", "c", "d"),
    makeEdge("ef", "e", "f"),
  ],
});

describe("defaultGraphDisplayOptions", () => {
  it("is every knob off with hops at the minimum", () => {
    expect(defaultGraphDisplayOptions()).toEqual({
      isolateID: "",
      hops: 1,
      minDegree: 0,
      collapseDepth: 0,
      expandedFolders: new Set<string>(),
      keepIDs: new Set<string>(),
    });
  });

  it("returns a fresh set each call so callers cannot share mutable state", () => {
    const first = defaultGraphDisplayOptions();
    const second = defaultGraphDisplayOptions();
    expect(first.expandedFolders).not.toBe(second.expandedFolders);
    expect(first.keepIDs).not.toBe(second.keepIDs);
  });
});

describe("isAggregateNodeID", () => {
  it("recognizes the aggregate prefix and round-trips the folder", () => {
    expect(aggregateNodeIDPrefix).toBe("folder:");
    expect(isAggregateNodeID("folder:Work/Notes")).toBe(true);
    expect(isAggregateNodeID("Work/Notes.md")).toBe(false);
    expect(folderForAggregateNodeID("folder:Work/Notes")).toBe("Work/Notes");
    expect(folderForAggregateNodeID("Work/Notes.md")).toBe("");
  });

  it("is not what the pipeline relies on: a note key starting with folder: stays a note", () => {
    const source: GraphElements = {
      nodes: [makeNode("folder:trap", { folder: "Inbox" })],
      edges: [],
    };

    const { elements } = buildDisplayGraph(source, withOptions({ collapseDepth: 1 }));

    expect(isAggregateNodeID(elements.nodes[0].id)).toBe(true);
    expect(elements.nodes[0].isAggregate).toBeFalsy();
    expect(elements.nodes[0].memberIDs).toBeUndefined();
  });
});

describe("folderGroupKey", () => {
  it("takes the first depth segments and ignores empty ones", () => {
    expect(folderGroupKey("Work/Projects/Alpha", 1)).toBe("Work");
    expect(folderGroupKey("Work/Projects/Alpha", 2)).toBe("Work/Projects");
    expect(folderGroupKey("Work/Projects/Alpha", 9)).toBe("Work/Projects/Alpha");
    expect(folderGroupKey("Work", 3)).toBe("Work");
    expect(folderGroupKey("", 1)).toBe("");
    expect(folderGroupKey("Work", 0)).toBe("");
  });
});

describe("buildDisplayGraph isolate stage", () => {
  it("keeps the anchor neighborhood and drops every edge with a dropped endpoint", () => {
    const { elements, stats } = buildDisplayGraph(
      chainGraph(),
      withOptions({ isolateID: "b", hops: 1 })
    );

    expect(nodeIDs(elements)).toEqual(["a", "b", "c"]);
    expect(edgeIDs(elements)).toEqual(["ab", "bc"]);
    expect(stats.isolated).toBe(true);
    expect(stats.sourceNodeCount).toBe(6);
    expect(stats.displayedNodeCount).toBe(3);
  });

  it("walks edges undirected, so an inbound-only neighbor is still reached", () => {
    const { elements } = buildDisplayGraph(
      chainGraph(),
      withOptions({ isolateID: "d", hops: 1 })
    );

    expect(nodeIDs(elements)).toEqual(["c", "d"]);
  });

  it("is a no-op when the anchor is not in the source", () => {
    const { elements, stats } = buildDisplayGraph(
      chainGraph(),
      withOptions({ isolateID: "missing", hops: 2 })
    );

    expect(nodeIDs(elements)).toEqual(["a", "b", "c", "d", "e", "f"]);
    expect(edgeIDs(elements)).toEqual(["ab", "bc", "cd", "ef"]);
    expect(stats.isolated).toBe(false);
  });

  it("reports isolated false when isolateID is empty", () => {
    const { stats } = buildDisplayGraph(chainGraph(), withOptions());
    expect(stats.isolated).toBe(false);
  });

  it("is exact at each hop boundary", () => {
    const atHops = (hops: number): string[] =>
      nodeIDs(buildDisplayGraph(chainGraph(), withOptions({ isolateID: "a", hops })).elements);

    expect(atHops(1)).toEqual(["a", "b"]);
    expect(atHops(2)).toEqual(["a", "b", "c"]);
    expect(atHops(3)).toEqual(["a", "b", "c", "d"]);
  });

  it("clamps hops into 1..3", () => {
    const atHops = (hops: number): string[] =>
      nodeIDs(buildDisplayGraph(chainGraph(), withOptions({ isolateID: "a", hops })).elements);

    expect(atHops(0)).toEqual(["a", "b"]);
    expect(atHops(99)).toEqual(["a", "b", "c", "d"]);
    expect(atHops(Number.NaN)).toEqual(["a", "b"]);
  });
});

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
    // Isolating on leaf1 at 1 hop leaves hub with subset degree 1, so a threshold of 2
    // drops it even though it has degree 2 in the full graph.
    const { elements } = buildDisplayGraph(
      hubGraph(),
      withOptions({ isolateID: "leaf1", hops: 1, minDegree: 2, keepIDs: new Set(["leaf1"]) })
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
      withOptions({ isolateID: "hub", hops: 1, minDegree: 2 })
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

describe("buildDisplayGraph composition", () => {
  // hub is linked to every member of folders A and B plus two loose notes; far/orphan sit
  // outside the 1-hop neighborhood.
  const composedSource = (): GraphElements => ({
    nodes: [
      makeNode("hub", { folder: "" }),
      makeNode("a1", { folder: "A/x" }),
      makeNode("a2", { folder: "A/y" }),
      makeNode("b1", { folder: "B" }),
      makeNode("b2", { folder: "B" }),
      makeNode("thin", { folder: "B" }),
      makeNode("far", { folder: "B" }),
      makeNode("orphan", { folder: "" }),
    ],
    edges: [
      makeEdge("h-a1", "hub", "a1"),
      makeEdge("h-a2", "hub", "a2"),
      makeEdge("h-b1", "hub", "b1"),
      makeEdge("h-b2", "hub", "b2"),
      makeEdge("a1-b1", "a1", "b1"),
      makeEdge("a1-b2", "a1", "b2"),
      makeEdge("hub-thin", "hub", "thin"),
      makeEdge("b1-far", "b1", "far"),
    ],
  });

  it("applies isolate, then min-degree, then collapse", () => {
    const { elements, stats } = buildDisplayGraph(
      composedSource(),
      withOptions({
        isolateID: "hub",
        hops: 1,
        minDegree: 2,
        collapseDepth: 1,
        keepIDs: new Set(["hub"]),
      })
    );

    // Isolate keeps hub/a1/a2/b1/b2/thin (far sits 2 hops out). Min-degree 2 drops a2 and
    // thin, each at subset degree 1. Collapse then folds b1/b2 into folder:B, while a1 is
    // alone in folder A at depth 1 and passes through.
    expect(nodeIDs(elements)).toEqual(["hub", "a1", "folder:B"]);
    expect(edgePairs(elements)).toEqual(["hub->a1", "hub->folder:B", "a1->folder:B"]);
    expect(stats).toEqual({
      sourceNodeCount: 8,
      displayedNodeCount: 3,
      aggregateCount: 1,
      droppedByDegree: 2,
      isolated: true,
    });
  });

  it("does not let the collapse stage resurrect nodes the earlier stages dropped", () => {
    const { elements } = buildDisplayGraph(
      composedSource(),
      withOptions({ isolateID: "hub", hops: 1, collapseDepth: 1 })
    );

    expect(nodeIDs(elements)).not.toContain("far");
    expect(elements.nodes.find((node) => node.id === "folder:B")?.memberIDs).toEqual([
      "b1",
      "b2",
      "thin",
    ]);
  });
});

describe("buildDisplayGraph purity and determinism", () => {
  it("returns identical output for identical input", () => {
    const options = withOptions({
      isolateID: "hub",
      hops: 2,
      minDegree: 1,
      collapseDepth: 1,
      keepIDs: new Set(["hub"]),
    });
    const source: GraphElements = {
      nodes: [
        makeNode("hub", { folder: "" }),
        makeNode("a1", { folder: "A" }),
        makeNode("a2", { folder: "A" }),
        makeNode("b1", { folder: "B" }),
      ],
      edges: [
        makeEdge("h-a1", "hub", "a1"),
        makeEdge("h-a2", "hub", "a2"),
        makeEdge("a1-b1", "a1", "b1"),
        makeEdge("a2-b1", "a2", "b1"),
      ],
    };

    const first = buildDisplayGraph(source, options);
    const second = buildDisplayGraph(source, options);

    expect(second.elements).toEqual(first.elements);
    expect(second.stats).toEqual(first.stats);
    expect(nodeIDs(first.elements)).toEqual(["hub", "folder:A", "b1"]);
  });

  it("does not mutate the source elements", () => {
    const source = chainGraph();
    const snapshot = structuredClone(source);

    buildDisplayGraph(
      source,
      withOptions({ isolateID: "a", hops: 2, minDegree: 1, collapseDepth: 1 })
    );

    expect(source).toEqual(snapshot);
  });

  // The regression this suite exists to prevent a second time: the source the controller
  // hands in has ALREADY been through mergeReciprocalGraphEdges in normalizeGraphPayload,
  // so by the time the pipeline's own merge runs there is no counterpart left to rediscover.
  // Recomputing the flag instead of carrying it silently un-flagged every mutual edge in
  // the vault, and the mounted graph drew plain one-way beziers with no test noticing.
  describe("mutual edges already merged upstream", () => {
    const mergedSource = (): GraphElements => ({
      nodes: [
        makeNode("a", { folder: "notes" }),
        makeNode("b", { folder: "notes" }),
        makeNode("c", { folder: "other" }),
        makeNode("d", { folder: "other" }),
      ],
      edges: [
        { ...makeEdge("ac", "a", "c"), mutual: true },
        makeEdge("bd", "b", "d"),
      ],
    });

    it("keeps the flag with every knob off", () => {
      const { elements } = buildDisplayGraph(mergedSource(), withOptions());

      expect(edgePairs(elements)).toEqual(["a->c*", "b->d"]);
    });

    it("keeps the flag through the isolate stage", () => {
      const { elements } = buildDisplayGraph(
        mergedSource(),
        withOptions({ isolateID: "a", hops: 1 })
      );

      expect(edgePairs(elements)).toEqual(["a->c*"]);
    });

    it("keeps the flag through the min-degree stage", () => {
      const { elements } = buildDisplayGraph(mergedSource(), withOptions({ minDegree: 1 }));

      expect(edgePairs(elements)).toEqual(["a->c*", "b->d"]);
    });

    it("keeps the flag through the collapse stage", () => {
      const { elements } = buildDisplayGraph(mergedSource(), withOptions({ collapseDepth: 1 }));

      expect(edgePairs(elements)).toEqual([`${aggregateNodeIDPrefix}notes->${aggregateNodeIDPrefix}other*`]);
    });

    // The collapse dedupe keeps the first-seen edge for a remapped pair. When a later
    // duplicate is the mutual one, the survivor has to inherit the flag: the aggregate
    // line stands for both note links, and one of them is reciprocal.
    it("inherits the flag from a deduped duplicate onto the surviving aggregate edge", () => {
      const source: GraphElements = {
        nodes: [
          makeNode("a", { folder: "notes" }),
          makeNode("b", { folder: "notes" }),
          makeNode("c", { folder: "other" }),
          makeNode("d", { folder: "other" }),
        ],
        edges: [makeEdge("ac", "a", "c"), { ...makeEdge("bd", "b", "d"), mutual: true }],
      };

      const { elements } = buildDisplayGraph(source, withOptions({ collapseDepth: 1 }));

      expect(edgeIDs(elements)).toEqual(["ac"]);
      expect(edgePairs(elements)).toEqual([`${aggregateNodeIDPrefix}notes->${aggregateNodeIDPrefix}other*`]);
    });
  });

  it("handles an empty graph", () => {
    const { elements, stats } = buildDisplayGraph(
      { nodes: [], edges: [] },
      withOptions({ isolateID: "a", minDegree: 3, collapseDepth: 2 })
    );

    expect(elements).toEqual({ nodes: [], edges: [] });
    expect(stats).toEqual({
      sourceNodeCount: 0,
      displayedNodeCount: 0,
      aggregateCount: 0,
      droppedByDegree: 0,
      isolated: false,
    });
  });
});
