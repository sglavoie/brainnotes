// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

/**
 * fcose registration is module state (it is global to the Cytoscape factory),
 * so every case loads a fresh copy of the module rather than sharing one across
 * tests. Without this, the first successful registration would leak into every
 * later assertion about the fallback path.
 */
const freshLayoutModule = async () => {
  vi.resetModules();
  return import("./graph-controller-layout");
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
});

/** The jsdom stand-in from graph-controller.test.ts: a bare function, no `.use`. */
const bareCytoscapeFake = () => ({});

describe("registerGraphLayouts", () => {
  it("no-ops when the fcose bundle never loaded", async () => {
    const use = vi.fn();
    const factory = Object.assign(() => ({}), { use });
    vi.stubGlobal("cytoscape", factory);

    const layout = await freshLayoutModule();
    layout.registerGraphLayouts();

    expect(use).not.toHaveBeenCalled();
    expect(layout.hasFcose()).toBe(false);
  });

  it("no-ops on a Cytoscape fake that has no use()", async () => {
    vi.stubGlobal("cytoscape", bareCytoscapeFake);
    vi.stubGlobal("cytoscapeFcose", () => {});

    const layout = await freshLayoutModule();
    expect(() => layout.registerGraphLayouts()).not.toThrow();
    expect(layout.hasFcose()).toBe(false);
  });

  it("registers exactly once when both globals are present", async () => {
    const use = vi.fn();
    const factory = Object.assign(() => ({}), { use });
    const extension = () => {};
    vi.stubGlobal("cytoscape", factory);
    vi.stubGlobal("cytoscapeFcose", extension);

    const layout = await freshLayoutModule();
    layout.registerGraphLayouts();
    layout.registerGraphLayouts();

    expect(use).toHaveBeenCalledTimes(1);
    expect(use).toHaveBeenCalledWith(extension);
    expect(layout.hasFcose()).toBe(true);
  });
});

describe("layoutOptionsForSpread", () => {
  it("falls back to cose when fcose is absent", async () => {
    vi.stubGlobal("cytoscape", bareCytoscapeFake);

    const layout = await freshLayoutModule();
    layout.registerGraphLayouts();

    expect(layout.layoutOptionsForSpread(2).name).toBe("cose");
  });

  it("selects fcose once registration succeeded", async () => {
    const factory = Object.assign(() => ({}), { use: vi.fn() });
    vi.stubGlobal("cytoscape", factory);
    vi.stubGlobal("cytoscapeFcose", () => {});

    const layout = await freshLayoutModule();
    layout.registerGraphLayouts();

    const options = layout.layoutOptionsForSpread(2) as unknown as Record<string, unknown>;
    expect(options.name).toBe("fcose");
    expect(options.randomize).toBe(false);
    expect(options.nodeDimensionsIncludeLabels).toBe(true);
    expect(options.packComponents).toBe(true);
    expect(options.tile).toBe(true);
  });

  it("keeps labels reserved on the cose fallback too", async () => {
    const layout = await freshLayoutModule();
    expect(layout.coseLayoutOptionsForSpread(2).nodeDimensionsIncludeLabels).toBe(true);
  });
});

const spreads = [0.8, 2.0, 6.0];

/** Reads the numeric option `key` at each spread, through the untyped fcose shape. */
const seriesFor = (
  build: (spread: number) => unknown,
  key: string
): number[] =>
  spreads.map((spread) => (build(spread) as Record<string, unknown>)[key] as number);

const expectStrictlyIncreasing = (values: number[]): void => {
  for (let index = 1; index < values.length; index += 1) {
    expect(values[index]).toBeGreaterThan(values[index - 1]);
  }
};

const expectStrictlyDecreasing = (values: number[]): void => {
  for (let index = 1; index < values.length; index += 1) {
    expect(values[index]).toBeLessThan(values[index - 1]);
  }
};

describe("spread scaling", () => {
  it("stays monotonic across 0.8/2.0/6.0 for the cose builder", async () => {
    const layout = await freshLayoutModule();
    const build = layout.coseLayoutOptionsForSpread;

    for (const key of ["idealEdgeLength", "nodeRepulsion", "componentSpacing", "numIter"]) {
      expectStrictlyIncreasing(seriesFor(build, key));
    }
    expectStrictlyDecreasing(seriesFor(build, "gravity"));
  });

  it("stays monotonic across 0.8/2.0/6.0 for the fcose builder", async () => {
    const factory = Object.assign(() => ({}), { use: vi.fn() });
    vi.stubGlobal("cytoscape", factory);
    vi.stubGlobal("cytoscapeFcose", () => {});

    const layout = await freshLayoutModule();
    layout.registerGraphLayouts();
    const build = layout.layoutOptionsForSpread;

    for (const key of ["idealEdgeLength", "nodeRepulsion", "nodeSeparation", "numIter"]) {
      expectStrictlyIncreasing(seriesFor(build, key));
    }
    expectStrictlyDecreasing(seriesFor(build, "gravity"));
  });
});

/** A node stand-in that records whatever position the seeder writes. */
const fakeNode = (id: string, position: { x: number; y: number }) => {
  let current = position;
  return {
    id: () => id,
    position: (next?: { x: number; y: number }) => {
      if (next) {
        current = next;
      }
      return current;
    },
  };
};

const fakeCyWithNodes = (nodes: ReturnType<typeof fakeNode>[]) => ({
  nodes: () => ({ toArray: () => nodes }),
});

describe("position carry-over across a remount", () => {
  it("captures the mounted positions by id and skips non-finite ones", async () => {
    const layout = await freshLayoutModule();
    const cy = fakeCyWithNodes([
      fakeNode("a", { x: 10, y: -4 }),
      fakeNode("broken", { x: Number.NaN, y: 3 }),
    ]);

    const captured = layout.captureNodePositions(cy as unknown as cytoscape.Core);

    expect(captured.get("a")).toEqual({ x: 10, y: -4 });
    expect(captured.has("broken")).toBe(false);
  });

  it("keeps a remembered position and seeds only the node that has none", async () => {
    const layout = await freshLayoutModule();
    const survivor = fakeNode("a", { x: 0, y: 0 });
    const arrival = fakeNode("b", { x: 0, y: 0 });
    const cy = fakeCyWithNodes([survivor, arrival]);
    const keep = new Map([["a", { x: 123, y: 456 }]]);

    layout.seedDeterministicPositions(cy as unknown as cytoscape.Core, 2, keep);

    expect(survivor.position()).toEqual({ x: 123, y: 456 });
    expect(arrival.position()).not.toEqual({ x: 123, y: 456 });
  });

  it("seeds everything deterministically when no positions are carried over", async () => {
    const layout = await freshLayoutModule();
    const node = fakeNode("a", { x: 999, y: 999 });

    layout.seedDeterministicPositions(fakeCyWithNodes([node]) as unknown as cytoscape.Core, 2);

    expect(node.position()).toEqual({ x: 0, y: 0 });
  });
});
