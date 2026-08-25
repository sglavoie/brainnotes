import { describe, expect, it } from "vitest";

import {
  applyGraphVisualClasses,
  labelPolicy,
  normalizeSearchMode,
  visualFocusState,
} from "./graph-controller-labels";

type FakeGraphSpec = {
  nodes: string[];
  edges?: Array<[string, string]>;
};

type FakeElement = {
  id: () => string;
  classes: Map<string, boolean>;
  toggleClass: (name: string, on: boolean) => void;
};

// A minimal stand-in for the slice of cytoscape.Core these helpers touch, so the
// tests stay pure (no jsdom, no real Cytoscape instance).
const createFakeCy = (spec: FakeGraphSpec) => {
  const edgeSpecs = spec.edges || [];
  const classesByID = new Map<string, Map<string, boolean>>();

  const makeElement = (id: string): FakeElement => {
    const classes = new Map<string, boolean>();
    classesByID.set(id, classes);
    return {
      id: () => id,
      classes,
      toggleClass: (name: string, on: boolean) => {
        classes.set(name, on);
      },
    };
  };

  const collection = <T>(items: T[]) => ({
    length: items.length,
    forEach: (fn: (item: T) => void) => items.forEach(fn),
    filter: (fn: (item: T) => boolean) => collection(items.filter(fn)),
    toArray: () => items.slice(),
  });

  const nodes = new Map<string, Record<string, unknown>>();
  const edges = new Map<string, Record<string, unknown>>();

  for (const id of spec.nodes) {
    const base = makeElement(id);
    nodes.set(id, {
      ...base,
      isNode: () => true,
      data: (_key: string) => undefined,
      connectedEdges: () =>
        collection(
          edgeSpecs
            .filter(([source, target]) => source === id || target === id)
            .map(([source, target]) => edges.get(`${source}|${target}`))
        ),
    });
  }

  for (const [source, target] of edgeSpecs) {
    const edgeID = `${source}|${target}`;
    const base = makeElement(edgeID);
    edges.set(edgeID, {
      ...base,
      isNode: () => false,
      connectedNodes: () => collection([nodes.get(source), nodes.get(target)]),
    });
  }

  const cy = {
    nodes: () => collection([...nodes.values()]),
    edges: () => collection([...edges.values()]),
    getElementById: (id: string) => nodes.get(id) || edges.get(id) || { length: 0 },
    batch: (fn: () => void) => fn(),
    zoom: () => 1,
    extent: () => ({ x1: 0, y1: 0, x2: 0, y2: 0 }),
  };

  return {
    cy: cy as unknown as cytoscape.Core,
    hasClass: (id: string, name: string) => classesByID.get(id)?.get(name) === true,
  };
};

describe("normalizeSearchMode", () => {
  it("only recognizes focus, defaulting everything else to filter", () => {
    expect(normalizeSearchMode("focus")).toBe("focus");
    expect(normalizeSearchMode(" FOCUS ")).toBe("focus");
    expect(normalizeSearchMode("filter")).toBe("filter");
    expect(normalizeSearchMode("wat")).toBe("filter");
    expect(normalizeSearchMode(null)).toBe("filter");
  });
});

describe("visualFocusState", () => {
  it("resolves matches, their neighbors, and their edges", () => {
    const { cy } = createFakeCy({
      nodes: ["a", "b", "c", "d"],
      edges: [
        ["a", "b"],
        ["c", "d"],
      ],
    });

    const focus = visualFocusState(cy, "", "", new Set(["a", "ghost"]));

    expect([...focus.matchIDs]).toEqual(["a"]);
    expect([...focus.matchNeighborIDs]).toEqual(["b"]);
    expect([...focus.highlightedEdgeIDs]).toEqual(["a|b"]);
    expect(focus.focusNodeID).toBe("");
  });
});

describe("applyGraphVisualClasses", () => {
  it("keeps matches and their neighbors bright while dimming the rest", () => {
    const { cy, hasClass } = createFakeCy({
      nodes: ["a", "b", "c", "d", "e"],
      edges: [
        ["a", "b"],
        ["c", "d"],
      ],
    });

    applyGraphVisualClasses(cy, "auto", "", "", new Set(["a"]));

    expect(hasClass("a", "is-search-match")).toBe(true);
    expect(hasClass("b", "is-search-match")).toBe(false);
    expect(hasClass("a", "is-dimmed")).toBe(false);
    expect(hasClass("b", "is-dimmed")).toBe(false);
    expect(hasClass("c", "is-dimmed")).toBe(true);
    expect(hasClass("d", "is-dimmed")).toBe(true);
    expect(hasClass("e", "is-dimmed")).toBe(true);
    expect(hasClass("a|b", "is-highlighted-edge")).toBe(true);
    expect(hasClass("c|d", "is-dimmed")).toBe(true);
  });

  it("unions a click focus with the search focus", () => {
    const { cy, hasClass } = createFakeCy({
      nodes: ["a", "b", "c", "d", "e"],
      edges: [
        ["a", "b"],
        ["c", "d"],
      ],
    });

    applyGraphVisualClasses(cy, "auto", "c", "", new Set(["a"]));

    for (const id of ["a", "b", "c", "d"]) {
      expect(hasClass(id, "is-dimmed")).toBe(false);
    }
    expect(hasClass("e", "is-dimmed")).toBe(true);
    expect(hasClass("c", "is-selected")).toBe(true);
    expect(hasClass("a", "is-search-match")).toBe(true);
    expect(hasClass("a|b", "is-highlighted-edge")).toBe(true);
    expect(hasClass("c|d", "is-highlighted-edge")).toBe(true);
  });

  it("dims nothing when there is no selection and no match", () => {
    const { cy, hasClass } = createFakeCy({
      nodes: ["a", "b", "c"],
      edges: [["a", "b"]],
    });

    applyGraphVisualClasses(cy, "auto", "", "");

    for (const id of ["a", "b", "c"]) {
      expect(hasClass(id, "is-dimmed")).toBe(false);
      expect(hasClass(id, "is-search-match")).toBe(false);
    }
    expect(hasClass("a|b", "is-dimmed")).toBe(false);
  });

  it("labels every match on a small match set and caps a broad one", () => {
    const manyNodes = Array.from({ length: 60 }, (_unused, index) => `n${String(index).padStart(2, "0")}`);
    const { cy, hasClass } = createFakeCy({ nodes: manyNodes });

    const fewMatches = new Set(manyNodes.slice(0, 3));
    applyGraphVisualClasses(cy, "auto", "", "", fewMatches);
    for (const id of fewMatches) {
      expect(hasClass(id, "is-labeled")).toBe(true);
    }

    const broadMatches = new Set(manyNodes.slice(0, 40));
    applyGraphVisualClasses(cy, "auto", "", "", broadMatches);
    const labeled = manyNodes.filter((id) => hasClass(id, "is-labeled"));
    expect(labeled).toHaveLength(labelPolicy.searchMatchLabelLimit);
  });
});
