import { describe, expect, it } from "vitest";

import {
  aggregateNodeIDPrefix,
  buildDisplayGraph,
  defaultGraphDisplayOptions,
  folderForAggregateNodeID,
  folderGroupKey,
  isAggregateNodeID,
} from "./graph-controller-view";
import {
  chainGraph,
  edgeIDs,
  makeEdge,
  makeNode,
  nodeIDs,
  withOptions,
} from "./graph-controller-view.test-helpers";
import { type GraphElements } from "./graph";

describe("defaultGraphDisplayOptions", () => {
  it("is every knob off", () => {
    expect(defaultGraphDisplayOptions()).toEqual({
      isolateID: "",
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
  it("keeps the anchor and its immediate neighbors, and drops every edge with a dropped endpoint", () => {
    const { elements, stats } = buildDisplayGraph(chainGraph(), withOptions({ isolateID: "b" }));

    // a links in and c links out, so both are immediate neighbors. d is one step
    // further and cd loses an endpoint along with it.
    expect(nodeIDs(elements)).toEqual(["a", "b", "c"]);
    expect(edgeIDs(elements)).toEqual(["ab", "bc"]);
    expect(stats.isolated).toBe(true);
    expect(stats.sourceNodeCount).toBe(6);
    expect(stats.displayedNodeCount).toBe(3);
  });

  it("walks edges undirected, so an inbound-only neighbor is still reached", () => {
    const { elements } = buildDisplayGraph(chainGraph(), withOptions({ isolateID: "d" }));

    expect(nodeIDs(elements)).toEqual(["c", "d"]);
  });

  it("keeps an outbound-only neighbor and stops there", () => {
    const { elements } = buildDisplayGraph(chainGraph(), withOptions({ isolateID: "a" }));

    expect(nodeIDs(elements)).toEqual(["a", "b"]);
    expect(edgeIDs(elements)).toEqual(["ab"]);
  });

  // The radius is not configurable, so "one more hop" is not something the caller
  // can ask for: a neighbor's own neighbor is outside the selection, always.
  it("never reaches a neighbor's neighbor, however the graph is shaped", () => {
    const star: GraphElements = {
      nodes: [makeNode("anchor"), makeNode("near"), makeNode("far"), makeNode("farther")],
      edges: [
        makeEdge("an", "anchor", "near"),
        makeEdge("nf", "near", "far"),
        makeEdge("ff", "far", "farther"),
      ],
    };

    const { elements } = buildDisplayGraph(star, withOptions({ isolateID: "anchor" }));

    expect(nodeIDs(elements)).toEqual(["anchor", "near"]);
    expect(edgeIDs(elements)).toEqual(["an"]);
  });

  it("keeps an unlinked anchor on its own rather than emptying the graph", () => {
    const { elements, stats } = buildDisplayGraph(
      {
        nodes: [makeNode("lonely"), makeNode("other")],
        edges: [],
      },
      withOptions({ isolateID: "lonely" })
    );

    expect(nodeIDs(elements)).toEqual(["lonely"]);
    expect(stats.isolated).toBe(true);
  });

  it("is a no-op when the anchor is not in the source", () => {
    const { elements, stats } = buildDisplayGraph(
      chainGraph(),
      withOptions({ isolateID: "missing" })
    );

    expect(nodeIDs(elements)).toEqual(["a", "b", "c", "d", "e", "f"]);
    expect(edgeIDs(elements)).toEqual(["ab", "bc", "cd", "ef"]);
    expect(stats.isolated).toBe(false);
  });

  it("reports isolated false when isolateID is empty", () => {
    const { stats } = buildDisplayGraph(chainGraph(), withOptions());
    expect(stats.isolated).toBe(false);
  });
});
