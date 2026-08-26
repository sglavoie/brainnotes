import { describe, expect, it } from "vitest";

import {
  applyGraphVisualClasses,
  cullCollidingLabels,
  estimateLabelBox,
  labelBoxGeometry,
  labelPolicy,
  normalizeSearchMode,
  visualFocusState,
} from "./graph-controller-labels";

type FakeGraphSpec = {
  nodes: string[];
  edges?: Array<[string, string]>;
  // Screen-space geometry for the label cull. Unspecified nodes are parked far
  // apart on the x axis, so tests that do not care about collisions never hit
  // one.
  positions?: Record<string, { x: number; y: number }>;
  widths?: Record<string, number>;
  titles?: Record<string, string>;
  degrees?: Record<string, number>;
  zoom?: number;
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

  spec.nodes.forEach((id, index) => {
    const base = makeElement(id);
    const position = spec.positions?.[id] || { x: index * 1000, y: 0 };
    const nodeData: Record<string, unknown> = {
      title: spec.titles?.[id] ?? id,
    };
    if (spec.degrees && id in spec.degrees) {
      nodeData.filteredDegree = spec.degrees[id];
    }
    nodes.set(id, {
      ...base,
      isNode: () => true,
      data: (key: string) => nodeData[key],
      renderedPosition: () => position,
      renderedWidth: () => spec.widths?.[id] ?? 20,
      connectedEdges: () =>
        collection(
          edgeSpecs
            .filter(([source, target]) => source === id || target === id)
            .map(([source, target]) => edges.get(`${source}|${target}`))
        ),
    });
  });

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
    zoom: () => spec.zoom ?? 1,
    extent: () => ({ x1: 0, y1: 0, x2: 0, y2: 0 }),
  };

  return {
    cy: cy as unknown as cytoscape.Core,
    hasClass: (id: string, name: string) => classesByID.get(id)?.get(name) === true,
  };
};

describe("estimateLabelBox", () => {
  it("sits below the node and grows a line per wrap at the configured max width", () => {
    const single = estimateLabelBox({ x: 0, y: 0 }, 20, "ab", 1);
    expect(single).not.toBeNull();
    // text-valign: bottom pushes the label under the node: half the node width
    // plus text-margin-y.
    expect(single?.y1).toBe(10 + labelBoxGeometry.marginYPX);

    const charWidth = labelBoxGeometry.fontSizePX * labelBoxGeometry.averageCharWidthRatio;
    const longTitle = "x".repeat(Math.ceil((labelBoxGeometry.maxWidthPX * 2) / charWidth));
    const wrapped = estimateLabelBox({ x: 0, y: 0 }, 20, longTitle, 1);
    expect(wrapped?.x2 ?? 0).toBeLessThanOrEqual(
      labelBoxGeometry.maxWidthPX / 2 + labelBoxGeometry.backgroundPaddingPX
    );
    expect((wrapped?.y2 ?? 0) - (wrapped?.y1 ?? 0)).toBeGreaterThan(
      (single?.y2 ?? 0) - (single?.y1 ?? 0)
    );
  });

  it("scales the label box with zoom and rejects unusable positions", () => {
    const near = estimateLabelBox({ x: 0, y: 0 }, 20, "ab", 1);
    const zoomed = estimateLabelBox({ x: 0, y: 0 }, 20, "ab", 2);
    expect((zoomed?.x2 ?? 0) - (zoomed?.x1 ?? 0)).toBeCloseTo(
      ((near?.x2 ?? 0) - (near?.x1 ?? 0)) * 2
    );
    expect(estimateLabelBox({ x: Number.NaN, y: 0 }, 20, "ab", 1)).toBeNull();
  });
});

describe("cullCollidingLabels", () => {
  it("keeps the higher-degree label and drops the overlapping one", () => {
    const { cy } = createFakeCy({
      nodes: ["hub", "leaf"],
      positions: { hub: { x: 0, y: 0 }, leaf: { x: 0, y: 0 } },
      degrees: { hub: 9, leaf: 1 },
    });

    const kept = cullCollidingLabels(cy, new Set(["hub", "leaf"]), new Set());

    expect([...kept]).toEqual(["hub"]);
  });

  it("breaks equal-degree ties deterministically by id", () => {
    const spec = {
      nodes: ["b", "a"],
      positions: { a: { x: 0, y: 0 }, b: { x: 0, y: 0 } },
      degrees: { a: 3, b: 3 },
    };

    expect([...cullCollidingLabels(createFakeCy(spec).cy, new Set(["b", "a"]), new Set())]).toEqual([
      "a",
    ]);
    expect([...cullCollidingLabels(createFakeCy(spec).cy, new Set(["a", "b"]), new Set())]).toEqual([
      "a",
    ]);
  });

  it("keeps every pinned label even when the pins overlap each other", () => {
    const { cy } = createFakeCy({
      nodes: ["hub", "one", "two"],
      positions: { hub: { x: 0, y: 0 }, one: { x: 0, y: 0 }, two: { x: 0, y: 0 } },
      degrees: { hub: 9, one: 1, two: 1 },
    });

    const kept = cullCollidingLabels(cy, new Set(["hub", "one", "two"]), new Set(["one", "two"]));

    expect(kept.has("one")).toBe(true);
    expect(kept.has("two")).toBe(true);
    expect(kept.has("hub")).toBe(false);
  });

  it("keeps everything when no boxes overlap and ignores unknown ids", () => {
    const { cy } = createFakeCy({ nodes: ["a", "b", "c"] });

    const kept = cullCollidingLabels(cy, new Set(["a", "b", "c", "ghost"]), new Set(["ghost"]));

    expect([...kept].sort()).toEqual(["a", "b", "c"]);
  });
});

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

  it("culls a colliding lower-degree label but never a pinned one", () => {
    const { cy, hasClass } = createFakeCy({
      nodes: ["hub", "leaf", "sel", "far"],
      positions: {
        hub: { x: 0, y: 0 },
        leaf: { x: 0, y: 0 },
        sel: { x: 0, y: 0 },
        far: { x: 900, y: 0 },
      },
      degrees: { hub: 9, leaf: 4, sel: 1, far: 1 },
    });

    applyGraphVisualClasses(cy, "auto", "sel", "");

    expect(hasClass("sel", "is-labeled")).toBe(true);
    expect(hasClass("hub", "is-labeled")).toBe(false);
    expect(hasClass("leaf", "is-labeled")).toBe(false);
    expect(hasClass("far", "is-labeled")).toBe(true);
    // The cull touches only is-labeled; focus classes are unaffected.
    expect(hasClass("sel", "is-selected")).toBe(true);
    expect(hasClass("hub", "is-dimmed")).toBe(true);
  });

  it("keeps a hovered label that collides with a higher-degree node", () => {
    const { cy, hasClass } = createFakeCy({
      nodes: ["hub", "hov"],
      positions: { hub: { x: 0, y: 0 }, hov: { x: 0, y: 0 } },
      degrees: { hub: 9, hov: 1 },
    });

    applyGraphVisualClasses(cy, "auto", "", "hov");

    expect(hasClass("hov", "is-labeled")).toBe(true);
    expect(hasClass("hub", "is-labeled")).toBe(false);
  });

  it("bypasses the cull entirely in all mode", () => {
    const { cy, hasClass } = createFakeCy({
      nodes: ["hub", "leaf"],
      positions: { hub: { x: 0, y: 0 }, leaf: { x: 0, y: 0 } },
      degrees: { hub: 9, leaf: 1 },
    });

    applyGraphVisualClasses(cy, "all", "", "");

    expect(hasClass("hub", "is-labeled")).toBe(true);
    expect(hasClass("leaf", "is-labeled")).toBe(true);
  });

  it("changes nothing when no labels overlap", () => {
    const { cy, hasClass } = createFakeCy({ nodes: ["a", "b", "c"] });

    applyGraphVisualClasses(cy, "auto", "", "");

    for (const id of ["a", "b", "c"]) {
      expect(hasClass(id, "is-labeled")).toBe(true);
    }
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
