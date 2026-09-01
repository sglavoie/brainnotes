import { describe, expect, it } from "vitest";

import { aggregateNodeIDPrefix, buildDisplayGraph } from "./graph-controller-view";
import {
  chainGraph,
  edgeIDs,
  edgePairs,
  makeEdge,
  makeNode,
  nodeIDs,
  withOptions,
} from "./graph-controller-view.test-helpers";
import { type GraphElements } from "./graph";

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
      withOptions({ isolateID: "hub", collapseDepth: 1 })
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
    // b1 sits one step past a1/a2, so isolation drops it before collapse sees it.
    expect(nodeIDs(first.elements)).toEqual(["hub", "folder:A"]);
  });

  it("does not mutate the source elements", () => {
    const source = chainGraph();
    const snapshot = structuredClone(source);

    buildDisplayGraph(
      source,
      withOptions({ isolateID: "a", minDegree: 1, collapseDepth: 1 })
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
        withOptions({ isolateID: "a" })
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
